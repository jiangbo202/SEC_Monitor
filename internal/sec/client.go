package sec

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/html/charset"
)

type FilingQuery struct {
	Ticker           string
	CIK              string
	FetchFullHistory bool
}

type FilingResult struct {
	FilingID        string     `json:"filing_id"`
	AccessionNumber string     `json:"accession_number"`
	Ticker          string     `json:"ticker"`
	CIK             string     `json:"cik"`
	CompanyName     string     `json:"company_name"`
	FilingType      string     `json:"filing_type"`
	FilingDate      time.Time  `json:"filing_date"`
	PublishedAt     *time.Time `json:"published_at"`
	FilingURL       string     `json:"filing_url"`
	Title           string     `json:"title"`
	RawContent      string     `json:"raw_content"`
}

type CurrentFilingQuery struct {
	FormTypes []string
	Count     int
}

type CurrentFilingResult struct {
	FilingID        string     `json:"filing_id"`
	AccessionNumber string     `json:"accession_number"`
	CIK             string     `json:"cik"`
	CompanyName     string     `json:"company_name"`
	FilingType      string     `json:"filing_type"`
	FilingDate      time.Time  `json:"filing_date"`
	AcceptedAt      *time.Time `json:"accepted_at"`
	FilingURL       string     `json:"filing_url"`
	Title           string     `json:"title"`
}

type Client interface {
	LookupCIK(ctx context.Context, ticker string) (string, string, error)
	ListFilings(ctx context.Context, query FilingQuery) ([]FilingResult, error)
}

type CurrentFilingsClient interface {
	ListCurrentFilings(ctx context.Context, query CurrentFilingQuery) ([]CurrentFilingResult, error)
	ListFilings(ctx context.Context, query FilingQuery) ([]FilingResult, error)
}

type IPOMarketClient interface {
	ListListedCompanies(ctx context.Context) ([]ListedCompany, error)
	FetchFilingDocument(ctx context.Context, filingURL string) (string, error)
}

type HTTPClient struct {
	BaseURL                   string
	CompanyTickersURL         string
	CompanyTickersMFURL       string
	CompanyTickersExchangeURL string
	CurrentFilingsURL         string
	UserAgent                 string
	Client                    *http.Client
	RequestPolicy             RequestPolicy
	pacer                     *requestPacer
}

// RequestPolicy keeps SEC traffic comfortably below EDGAR's published
// fair-access limit. MaxRetries counts retries after the initial request.
// A zero RequestsPerSecond disables pacing, which is useful for local tests.
type RequestPolicy struct {
	RequestsPerSecond int
	MaxRetries        int
	RetryBaseDelay    time.Duration
}

func DefaultRequestPolicy() RequestPolicy {
	return RequestPolicy{
		RequestsPerSecond: 8,
		MaxRetries:        2,
		RetryBaseDelay:    500 * time.Millisecond,
	}
}

// RequestError preserves the operation and HTTP status so callers can show
// an actionable message instead of a generic transport failure.
type RequestError struct {
	Operation  string
	StatusCode int
	Attempts   int
	Cause      error
}

func (e *RequestError) Error() string {
	if e == nil {
		return "SEC request failed"
	}
	if e.StatusCode == http.StatusTooManyRequests {
		return fmt.Sprintf("SEC request rate limited during %s after %d attempt(s)", e.Operation, e.Attempts)
	}
	if e.StatusCode > 0 {
		return fmt.Sprintf("SEC request failed during %s with HTTP %d after %d attempt(s)", e.Operation, e.StatusCode, e.Attempts)
	}
	if e.Cause != nil {
		return fmt.Sprintf("SEC request failed during %s after %d attempt(s): %v", e.Operation, e.Attempts, e.Cause)
	}
	return fmt.Sprintf("SEC request failed during %s after %d attempt(s)", e.Operation, e.Attempts)
}

func (e *RequestError) Unwrap() error { return e.Cause }

// UserMessage deliberately keeps provider diagnostics useful without exposing
// an implementation-specific URL in API responses. The original error remains
// attached to the sync run and server logs for operators.
func UserMessage(err error) string {
	var requestErr *RequestError
	if errors.As(err, &requestErr) {
		switch requestErr.StatusCode {
		case http.StatusTooManyRequests:
			return "SEC 请求频率受限；系统已自动退避重试，请稍后再次同步"
		case http.StatusNotFound:
			return "SEC 未找到请求的数据；该文件可能已迁移或暂不可用"
		case http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
			return "SEC 服务暂时不可用；系统已自动重试，请稍后再次同步"
		}
		if errors.Is(requestErr.Cause, context.DeadlineExceeded) {
			return "SEC 请求超时；系统已按策略重试，请检查网络后重试"
		}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "SEC 请求超时；请检查网络后重试"
	}
	return ""
}

func NewHTTPClient(baseURL string, userAgent string, timeout time.Duration) *HTTPClient {
	return NewHTTPClientWithPolicy(baseURL, userAgent, timeout, DefaultRequestPolicy())
}

func NewHTTPClientWithPolicy(baseURL string, userAgent string, timeout time.Duration, policy RequestPolicy) *HTTPClient {
	if policy.MaxRetries < 0 {
		policy.MaxRetries = 0
	}
	if policy.RetryBaseDelay <= 0 {
		policy.RetryBaseDelay = 500 * time.Millisecond
	}
	interval := time.Duration(0)
	if policy.RequestsPerSecond > 0 {
		interval = time.Second / time.Duration(policy.RequestsPerSecond)
	}
	return &HTTPClient{
		BaseURL:                   strings.TrimRight(baseURL, "/"),
		CompanyTickersURL:         "https://www.sec.gov/files/company_tickers.json",
		CompanyTickersMFURL:       "https://www.sec.gov/files/company_tickers_mf.json",
		CompanyTickersExchangeURL: "https://www.sec.gov/files/company_tickers_exchange.json",
		CurrentFilingsURL:         "https://www.sec.gov/cgi-bin/browse-edgar",
		UserAgent:                 userAgent,
		Client:                    &http.Client{Timeout: timeout},
		RequestPolicy:             policy,
		pacer:                     &requestPacer{interval: interval},
	}
}

func (c *HTTPClient) ListCurrentFilings(ctx context.Context, query CurrentFilingQuery) ([]CurrentFilingResult, error) {
	count := query.Count
	if count <= 0 || count > 100 {
		count = 100
	}
	formTypes := query.FormTypes
	if len(formTypes) == 0 {
		formTypes = []string{""}
	}
	all := make([]CurrentFilingResult, 0, count*len(formTypes))
	seen := map[string]bool{}
	for _, formType := range formTypes {
		values := url.Values{}
		values.Set("action", "getcurrent")
		values.Set("owner", "include")
		values.Set("count", fmt.Sprintf("%d", count))
		values.Set("output", "atom")
		if strings.TrimSpace(formType) != "" {
			values.Set("type", strings.TrimSpace(formType))
		}
		endpoint := c.currentFilingsURL() + "?" + values.Encode()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return nil, err
		}
		c.setHeaders(req)
		resp, err := c.do(req, "current filings")
		if err != nil {
			return nil, err
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			resp.Body.Close()
			return nil, fmt.Errorf("sec current filings status: %d", resp.StatusCode)
		}
		var feed atomFeed
		decoder := xml.NewDecoder(resp.Body)
		decoder.CharsetReader = charset.NewReaderLabel
		err = decoder.Decode(&feed)
		resp.Body.Close()
		if err != nil {
			return nil, err
		}
		for _, item := range feed.toCurrentFilings() {
			key := item.FilingID
			if key == "" {
				key = item.FilingURL
			}
			if key == "" || seen[key] {
				continue
			}
			seen[key] = true
			all = append(all, item)
		}
	}
	return all, nil
}

func (c *HTTPClient) LookupCIK(ctx context.Context, ticker string) (string, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.companyTickersURL(), nil)
	if err != nil {
		return "", "", err
	}
	c.setHeaders(req)

	resp, err := c.do(req, "CIK lookup")
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", "", fmt.Errorf("sec cik lookup status: %d", resp.StatusCode)
	}

	var payload map[string]struct {
		CIKStr int    `json:"cik_str"`
		Ticker string `json:"ticker"`
		Title  string `json:"title"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", "", err
	}

	want := strings.ToUpper(strings.TrimSpace(ticker))
	for _, item := range payload {
		if strings.ToUpper(item.Ticker) == want {
			return fmt.Sprintf("%010d", item.CIKStr), item.Title, nil
		}
	}
	return "", "", fmt.Errorf("ticker not found: %s", ticker)
}

func (c *HTTPClient) ListFilings(ctx context.Context, query FilingQuery) ([]FilingResult, error) {
	cik := normalizeCIK(query.CIK)
	if cik == "" {
		return nil, fmt.Errorf("cik is required")
	}
	url := fmt.Sprintf("%s/submissions/CIK%s.json", c.BaseURL, cik)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	c.setHeaders(req)

	resp, err := c.do(req, "submissions")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("sec filings status: %d", resp.StatusCode)
	}

	var payload submissionsResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}
	filings := payload.toFilings(strings.ToUpper(query.Ticker), cik)
	if !query.FetchFullHistory {
		return filings, nil
	}
	for _, file := range payload.Filings.Files {
		archived, err := c.loadArchivedSubmissions(ctx, file.Name)
		if err != nil {
			return nil, err
		}
		filings = append(filings, archived.toFilings(strings.ToUpper(query.Ticker), cik, payload.Name)...)
	}
	return filings, nil
}

func (c *HTTPClient) httpClient() *http.Client {
	if c.Client != nil {
		return c.Client
	}
	return http.DefaultClient
}

type requestPacer struct {
	mu       sync.Mutex
	nextAt   time.Time
	interval time.Duration
}

func (p *requestPacer) wait(ctx context.Context) error {
	if p == nil || p.interval <= 0 {
		return nil
	}
	p.mu.Lock()
	now := time.Now()
	when := now
	if p.nextAt.After(when) {
		when = p.nextAt
	}
	p.nextAt = when.Add(p.interval)
	p.mu.Unlock()
	if delay := time.Until(when); delay > 0 {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
		}
	}
	return nil
}

func (c *HTTPClient) do(req *http.Request, operation string) (*http.Response, error) {
	if req == nil {
		return nil, &RequestError{Operation: operation, Attempts: 1, Cause: errors.New("nil request")}
	}
	policy := c.RequestPolicy
	if policy.RetryBaseDelay <= 0 {
		policy.RetryBaseDelay = 500 * time.Millisecond
	}
	if policy.MaxRetries < 0 {
		policy.MaxRetries = 0
	}
	for attempt := 0; attempt <= policy.MaxRetries; attempt++ {
		if err := c.pacer.wait(req.Context()); err != nil {
			return nil, &RequestError{Operation: operation, Attempts: attempt + 1, Cause: err}
		}
		response, err := c.httpClient().Do(req.Clone(req.Context()))
		if err == nil && !retryableStatus(response.StatusCode) {
			return response, nil
		}

		requestErr := &RequestError{Operation: operation, Attempts: attempt + 1, Cause: err}
		var retryAfter time.Duration
		if response != nil {
			requestErr.StatusCode = response.StatusCode
			retryAfter = retryAfterDelay(response.Header.Get("Retry-After"))
			response.Body.Close()
		}
		if attempt == policy.MaxRetries || !retryableRequestError(err, requestErr.StatusCode) {
			return nil, requestErr
		}
		if retryAfter <= 0 {
			retryAfter = retryDelay(policy.RetryBaseDelay, attempt)
		}
		if err := waitForRetry(req.Context(), retryAfter); err != nil {
			return nil, &RequestError{Operation: operation, Attempts: attempt + 1, StatusCode: requestErr.StatusCode, Cause: err}
		}
	}
	return nil, &RequestError{Operation: operation, Attempts: 1}
}

func retryableStatus(statusCode int) bool {
	return statusCode == http.StatusTooManyRequests || statusCode == http.StatusRequestTimeout || statusCode == http.StatusBadGateway || statusCode == http.StatusServiceUnavailable || statusCode == http.StatusGatewayTimeout
}

func retryableRequestError(err error, statusCode int) bool {
	if statusCode > 0 {
		return retryableStatus(statusCode)
	}
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var networkErr net.Error
	return errors.As(err, &networkErr) && (networkErr.Timeout() || networkErr.Temporary())
}

func retryAfterDelay(value string) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(value); err == nil && seconds >= 0 {
		return time.Duration(seconds) * time.Second
	}
	if at, err := http.ParseTime(value); err == nil {
		return max(time.Until(at), 0)
	}
	return 0
}

func retryDelay(base time.Duration, attempt int) time.Duration {
	shift := min(attempt, 5)
	delay := base * time.Duration(1<<shift)
	// A small deterministic jitter prevents all similarly configured workers
	// from issuing the next retry at exactly the same instant.
	return delay + time.Duration((attempt+1)*37)*time.Millisecond
}

func waitForRetry(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (c *HTTPClient) companyTickersURL() string {
	if c.CompanyTickersURL != "" {
		return c.CompanyTickersURL
	}
	return "https://www.sec.gov/files/company_tickers.json"
}

func (c *HTTPClient) companyTickersMFURL() string {
	if c.CompanyTickersMFURL != "" {
		return c.CompanyTickersMFURL
	}
	return "https://www.sec.gov/files/company_tickers_mf.json"
}

func (c *HTTPClient) currentFilingsURL() string {
	if c.CurrentFilingsURL != "" {
		return c.CurrentFilingsURL
	}
	return "https://www.sec.gov/cgi-bin/browse-edgar"
}

func (c *HTTPClient) setHeaders(req *http.Request) {
	if c.UserAgent != "" {
		req.Header.Set("User-Agent", c.UserAgent)
	}
	req.Header.Set("Accept", "application/json")
}

type submissionsResponse struct {
	CIK     string `json:"cik"`
	Name    string `json:"name"`
	Filings struct {
		Recent struct {
			AccessionNumber []string `json:"accessionNumber"`
			Form            []string `json:"form"`
			FilingDate      []string `json:"filingDate"`
			AcceptanceDate  []string `json:"acceptanceDateTime"`
			ReportDate      []string `json:"reportDate"`
			PrimaryDocument []string `json:"primaryDocument"`
			PrimaryDocDesc  []string `json:"primaryDocDescription"`
		} `json:"recent"`
		Files []struct {
			Name string `json:"name"`
		} `json:"files"`
	} `json:"filings"`
}

type archivedSubmissionsResponse struct {
	AccessionNumber []string `json:"accessionNumber"`
	Form            []string `json:"form"`
	FilingDate      []string `json:"filingDate"`
	AcceptanceDate  []string `json:"acceptanceDateTime"`
	ReportDate      []string `json:"reportDate"`
	PrimaryDocument []string `json:"primaryDocument"`
	PrimaryDocDesc  []string `json:"primaryDocDescription"`
}

type atomFeed struct {
	Entries []atomEntry `xml:"entry"`
}

type atomEntry struct {
	Title      string         `xml:"title"`
	Updated    string         `xml:"updated"`
	Summary    string         `xml:"summary"`
	Links      []atomLink     `xml:"link"`
	Categories []atomCategory `xml:"category"`
}

type atomLink struct {
	Href string `xml:"href,attr"`
}

type atomCategory struct {
	Term string `xml:"term,attr"`
}

func (f atomFeed) toCurrentFilings() []CurrentFilingResult {
	results := make([]CurrentFilingResult, 0, len(f.Entries))
	for _, entry := range f.Entries {
		acceptedAt := parseAcceptanceDate(entry.Updated)
		formType := strings.TrimSpace(valueAt(categoryTerms(entry.Categories), 0))
		if formType == "" {
			formType = parseFormFromTitle(entry.Title)
		}
		accession := parseSummaryValue(entry.Summary, `(?i)Accession\s+Number:\s*([0-9-]+)`)
		cik := normalizeCIK(parseSummaryValue(entry.Summary, `(?i)CIK:\s*([0-9]+)`))
		if cik == "" {
			cik = normalizeCIK(parseSummaryValue(entry.Title, `\(([0-9]{6,10})\)`))
		}
		company := parseCompanyFromTitle(entry.Title)
		filingDate, _ := time.Parse("2006-01-02", parseSummaryValue(entry.Summary, `(?i)Filing\s+Date:\s*([0-9]{4}-[0-9]{2}-[0-9]{2})`))
		if filingDate.IsZero() && acceptedAt != nil {
			filingDate = time.Date(acceptedAt.Year(), acceptedAt.Month(), acceptedAt.Day(), 0, 0, 0, 0, time.UTC)
		}
		results = append(results, CurrentFilingResult{
			FilingID:        stringOrDefault(accession, firstLink(entry.Links)),
			AccessionNumber: accession,
			CIK:             cik,
			CompanyName:     company,
			FilingType:      formType,
			FilingDate:      filingDate,
			AcceptedAt:      acceptedAt,
			FilingURL:       firstLink(entry.Links),
			Title:           strings.TrimSpace(entry.Title),
		})
	}
	return results
}

func categoryTerms(categories []atomCategory) []string {
	items := make([]string, 0, len(categories))
	for _, item := range categories {
		items = append(items, item.Term)
	}
	return items
}

func firstLink(links []atomLink) string {
	for _, link := range links {
		if strings.TrimSpace(link.Href) != "" {
			return strings.TrimSpace(link.Href)
		}
	}
	return ""
}

func stringOrDefault(value string, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return strings.TrimSpace(fallback)
	}
	return strings.TrimSpace(value)
}

func parseSummaryValue(value string, pattern string) string {
	re := regexp.MustCompile(pattern)
	matches := re.FindStringSubmatch(value)
	if len(matches) < 2 {
		return ""
	}
	return strings.TrimSpace(matches[1])
}

func parseFormFromTitle(value string) string {
	parts := strings.SplitN(strings.TrimSpace(value), " - ", 2)
	if len(parts) == 0 {
		return ""
	}
	return strings.TrimSpace(parts[0])
}

func parseCompanyFromTitle(value string) string {
	parts := strings.SplitN(strings.TrimSpace(value), " - ", 2)
	if len(parts) < 2 {
		return strings.TrimSpace(value)
	}
	company := regexp.MustCompile(`\s+\([0-9]{6,10}\).*$`).ReplaceAllString(parts[1], "")
	return strings.TrimSpace(company)
}

func (r submissionsResponse) toFilings(ticker string, cik string) []FilingResult {
	return recentSubmissions{
		AccessionNumber: r.Filings.Recent.AccessionNumber,
		Form:            r.Filings.Recent.Form,
		FilingDate:      r.Filings.Recent.FilingDate,
		AcceptanceDate:  r.Filings.Recent.AcceptanceDate,
		ReportDate:      r.Filings.Recent.ReportDate,
		PrimaryDocument: r.Filings.Recent.PrimaryDocument,
		PrimaryDocDesc:  r.Filings.Recent.PrimaryDocDesc,
	}.toFilings(ticker, cik, r.Name)
}

func (r archivedSubmissionsResponse) toFilings(ticker string, cik string, companyName string) []FilingResult {
	return recentSubmissions{
		AccessionNumber: r.AccessionNumber,
		Form:            r.Form,
		FilingDate:      r.FilingDate,
		AcceptanceDate:  r.AcceptanceDate,
		ReportDate:      r.ReportDate,
		PrimaryDocument: r.PrimaryDocument,
		PrimaryDocDesc:  r.PrimaryDocDesc,
	}.toFilings(ticker, cik, companyName)
}

type recentSubmissions struct {
	AccessionNumber []string
	Form            []string
	FilingDate      []string
	AcceptanceDate  []string
	ReportDate      []string
	PrimaryDocument []string
	PrimaryDocDesc  []string
}

func (r recentSubmissions) toFilings(ticker string, cik string, companyName string) []FilingResult {
	count := len(r.AccessionNumber)
	results := make([]FilingResult, 0, count)
	for i := 0; i < count; i++ {
		accession := r.AccessionNumber[i]
		form := valueAt(r.Form, i)
		filingDate, _ := time.Parse("2006-01-02", valueAt(r.FilingDate, i))
		publishedAt := parseAcceptanceDate(valueAt(r.AcceptanceDate, i))
		primaryDoc := valueAt(r.PrimaryDocument, i)
		noDash := strings.ReplaceAll(accession, "-", "")
		url := fmt.Sprintf("https://www.sec.gov/Archives/edgar/data/%s/%s/%s", strings.TrimLeft(cik, "0"), noDash, primaryDoc)
		results = append(results, FilingResult{
			FilingID:        accession,
			AccessionNumber: accession,
			Ticker:          ticker,
			CIK:             cik,
			CompanyName:     companyName,
			FilingType:      form,
			FilingDate:      filingDate,
			PublishedAt:     publishedAt,
			FilingURL:       url,
			Title:           valueAt(r.PrimaryDocDesc, i),
		})
	}
	return results
}

func parseAcceptanceDate(value string) *time.Time {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	layouts := []string{
		time.RFC3339Nano,
		"2006-01-02T15:04:05.000Z",
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05",
	}
	for _, layout := range layouts {
		if parsed, err := time.Parse(layout, value); err == nil {
			utc := parsed.UTC()
			return &utc
		}
	}
	return nil
}

func (c *HTTPClient) loadArchivedSubmissions(ctx context.Context, name string) (archivedSubmissionsResponse, error) {
	if strings.TrimSpace(name) == "" {
		return archivedSubmissionsResponse{}, nil
	}
	url := fmt.Sprintf("%s/submissions/%s", c.BaseURL, name)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return archivedSubmissionsResponse{}, err
	}
	c.setHeaders(req)

	resp, err := c.do(req, "archived submissions")
	if err != nil {
		return archivedSubmissionsResponse{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return archivedSubmissionsResponse{}, fmt.Errorf("sec archived filings status: %d", resp.StatusCode)
	}

	var payload archivedSubmissionsResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return archivedSubmissionsResponse{}, err
	}
	return payload, nil
}

func normalizeCIK(cik string) string {
	cik = strings.TrimSpace(cik)
	if cik == "" {
		return ""
	}
	cik = strings.TrimLeft(cik, "0")
	if cik == "" {
		return "0000000000"
	}
	return fmt.Sprintf("%010s", cik)
}

func valueAt(values []string, index int) string {
	if index < 0 || index >= len(values) {
		return ""
	}
	return values[index]
}
