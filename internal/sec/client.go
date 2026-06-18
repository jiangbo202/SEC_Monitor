package sec

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
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

type Client interface {
	LookupCIK(ctx context.Context, ticker string) (string, string, error)
	ListFilings(ctx context.Context, query FilingQuery) ([]FilingResult, error)
}

type HTTPClient struct {
	BaseURL           string
	CompanyTickersURL string
	UserAgent         string
	Client            *http.Client
}

func NewHTTPClient(baseURL string, userAgent string, timeout time.Duration) *HTTPClient {
	return &HTTPClient{
		BaseURL:           strings.TrimRight(baseURL, "/"),
		CompanyTickersURL: "https://www.sec.gov/files/company_tickers.json",
		UserAgent:         userAgent,
		Client:            &http.Client{Timeout: timeout},
	}
}

func (c *HTTPClient) LookupCIK(ctx context.Context, ticker string) (string, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.companyTickersURL(), nil)
	if err != nil {
		return "", "", err
	}
	c.setHeaders(req)

	resp, err := c.httpClient().Do(req)
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

	resp, err := c.httpClient().Do(req)
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

func (c *HTTPClient) companyTickersURL() string {
	if c.CompanyTickersURL != "" {
		return c.CompanyTickersURL
	}
	return "https://www.sec.gov/files/company_tickers.json"
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

	resp, err := c.httpClient().Do(req)
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
