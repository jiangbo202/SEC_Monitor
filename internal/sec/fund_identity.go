package sec

import (
	"context"
	"encoding/json"
	"fmt"
	stdhtml "html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
)

const fundIdentitySource = "sec_company_tickers_mf"

const fundFilingIndexSource = "sec_filing_index"

// SEC full-text search is not a ticker directory: a short symbol can appear
// in many unrelated filings. Keep this best-effort fallback bounded so a
// newly listed ETF cannot make the target-setup request fan out indefinitely.
const (
	maxFundSearchHits     = 8
	maxFundNameSearchHits = 6
	fundSearchTimeout     = 12 * time.Second
)

type FundIdentity struct {
	Ticker      string
	CIK         string
	SeriesID    string
	ClassID     string
	FundName    string
	Source      string
	EvidenceURL string
}

type FundResolution struct {
	Identity   *FundIdentity
	Candidates []FundIdentity
	Reason     string
}

type FundIdentityClient interface {
	ResolveFundTicker(context.Context, string) (FundResolution, error)
	MatchFundFiling(context.Context, FundIdentity, FilingResult) (bool, string, error)
}

// FundFilingRelationship is one class belonging to a Series in a filing
// index. It is deliberately independent of a watched ticker so callers can
// cache it once per SEC accession and evaluate many targets locally.
type FundFilingRelationship struct {
	SeriesID string `json:"series_id"`
	ClassID  string `json:"class_id"`
}

type FundFilingMetadata struct {
	Relationships []FundFilingRelationship `json:"relationships"`
	Incomplete    bool                     `json:"incomplete"`
}

type FundFilingMetadataClient interface {
	ParseFundFiling(context.Context, FilingResult) (FundFilingMetadata, error)
}

type fundTickerPayload struct {
	Fields []string            `json:"fields"`
	Data   [][]json.RawMessage `json:"data"`
}

func (c *HTTPClient) ResolveFundTicker(ctx context.Context, ticker string) (FundResolution, error) {
	want := strings.ToUpper(strings.TrimSpace(ticker))
	if want == "" {
		return FundResolution{Reason: "ticker is required"}, nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.companyTickersMFURL(), nil)
	if err != nil {
		return FundResolution{}, err
	}
	c.setHeaders(req)

	resp, err := c.do(req, "fund ticker lookup")
	if err != nil {
		return FundResolution{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return FundResolution{}, fmt.Errorf("sec fund ticker lookup status: %d", resp.StatusCode)
	}

	var payload fundTickerPayload
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return FundResolution{}, err
	}
	indexes, missing := fundTickerFieldIndexes(payload.Fields)
	if len(missing) > 0 {
		return FundResolution{Reason: fmt.Sprintf("SEC fund ticker data is missing required fields: %s", strings.Join(missing, ", "))}, nil
	}

	matchingRecords := 0
	incompleteRecords := 0
	candidates := make([]FundIdentity, 0)
	for _, row := range payload.Data {
		symbol := fundTickerValue(row, indexes["symbol"])
		if strings.ToUpper(strings.TrimSpace(symbol)) != want {
			continue
		}
		matchingRecords++
		identity, ok := fundIdentityFromRow(row, indexes)
		if !ok {
			incompleteRecords++
			continue
		}
		candidates = append(candidates, identity)
	}

	if matchingRecords == 0 {
		fundName, nameErr := c.lookupFundNameFromCBOE(ctx, want)
		if nameErr == nil && fundName != "" {
			return c.resolveFundTickerFromSearch(ctx, want, `"`+fundName+`"`, fundName, maxFundNameSearchHits)
		}
		return c.resolveFundTickerFromSearch(ctx, want, want, "", maxFundSearchHits)
	}
	if matchingRecords == 1 && incompleteRecords == 0 && len(candidates) == 1 {
		return FundResolution{Identity: &candidates[0]}, nil
	}
	if incompleteRecords > 0 {
		return FundResolution{
			Candidates: candidates,
			Reason:     fmt.Sprintf("SEC fund ticker data for %s contains incomplete identity records", want),
		}, nil
	}
	return FundResolution{
		Candidates: candidates,
		Reason:     fmt.Sprintf("multiple SEC fund identities match ticker %s", want),
	}, nil
}

type fundSearchPayload struct {
	Hits struct {
		Hits []struct {
			Source struct {
				ADSH string          `json:"adsh"`
				CIKs json.RawMessage `json:"ciks"`
			} `json:"_source"`
		} `json:"hits"`
	} `json:"hits"`
}

type fundIndexParseResult struct {
	Identities []FundIdentity
	Incomplete bool
}

func (c *HTTPClient) lookupFundNameFromCBOE(ctx context.Context, ticker string) (string, error) {
	listingURL := "https://www.cboe.com/us/equities/listings/listed_products/symbols/" + url.PathEscape(ticker) + "/"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, listingURL, nil)
	if err != nil {
		return "", err
	}
	c.setHeaders(req)
	resp, err := c.do(req, "fund filing search")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return "", nil
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return "", fmt.Errorf("cboe listed product lookup status: %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return "", err
	}
	return parseCBOEFundName(string(body), ticker), nil
}

func parseCBOEFundName(body, ticker string) string {
	tickerPattern := `(?i)Cboe\s*:\s*` + regexp.QuoteMeta(strings.TrimSpace(ticker)) + `(?:\s|<|$)`
	if !regexp.MustCompile(tickerPattern).MatchString(body) {
		return ""
	}
	match := regexp.MustCompile(`(?is)<h1\b[^>]*>\s*(.*?)\s*</h1>`).FindStringSubmatch(body)
	if len(match) != 2 {
		return ""
	}
	name := regexp.MustCompile(`(?is)<[^>]+>`).ReplaceAllString(match[1], "")
	return strings.TrimSpace(stdhtml.UnescapeString(name))
}

func (c *HTTPClient) resolveFundTickerFromSearch(ctx context.Context, ticker, query, expectedFundName string, maxHits int) (FundResolution, error) {
	searchCtx, cancel := context.WithTimeout(ctx, fundSearchTimeout)
	defer cancel()
	queryParams := url.Values{
		"q":    []string{query},
		"size": []string{fmt.Sprintf("%d", maxHits)},
	}
	if expectedFundName != "" {
		// Prospectus and post-effective amendment filings expose the official
		// Series/Class table while excluding most unrelated mentions of the fund.
		queryParams.Set("forms", "497,485APOS")
	}
	searchURL := "https://efts.sec.gov/LATEST/search-index?" + queryParams.Encode()
	req, err := http.NewRequestWithContext(searchCtx, http.MethodGet, searchURL, nil)
	if err != nil {
		return FundResolution{}, err
	}
	c.setHeaders(req)
	resp, err := c.do(req, "fund filing document")
	if err != nil {
		return FundResolution{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return FundResolution{}, fmt.Errorf("sec fund search status: %d", resp.StatusCode)
	}

	var payload fundSearchPayload
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return FundResolution{}, err
	}

	type indexJob struct {
		cik      string
		indexURL string
	}
	type indexResult struct {
		cik      string
		indexURL string
		parsed   fundIndexParseResult
		err      error
	}
	jobs := make([]indexJob, 0, maxHits)
	seenIndexURLs := make(map[string]struct{})
	for index, hit := range payload.Hits.Hits {
		if index >= maxHits {
			break
		}
		accessionNumber := strings.TrimSpace(hit.Source.ADSH)
		if !validAccessionNumber(accessionNumber) {
			continue
		}
		for _, rawCIK := range rawStringSlice(hit.Source.CIKs) {
			cik := normalizeFundCIK(rawCIK)
			if cik == "" {
				continue
			}
			indexURL := fundFilingIndexURL(cik, accessionNumber)
			if _, seen := seenIndexURLs[indexURL]; seen {
				continue
			}
			seenIndexURLs[indexURL] = struct{}{}
			jobs = append(jobs, indexJob{cik: cik, indexURL: indexURL})
		}
	}

	results := make(chan indexResult, len(jobs))
	workerCount := min(4, len(jobs))
	work := make(chan indexJob)
	var workers sync.WaitGroup
	for range workerCount {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for job := range work {
				parsed, err := c.fetchFundIndex(searchCtx, job.indexURL)
				results <- indexResult{cik: job.cik, indexURL: job.indexURL, parsed: parsed, err: err}
			}
		}()
	}
	go func() {
		defer close(work)
		for _, job := range jobs {
			select {
			case work <- job:
			case <-searchCtx.Done():
				return
			}
		}
	}()
	go func() {
		workers.Wait()
		close(results)
	}()

	candidates := make([]FundIdentity, 0)
	incompleteMetadata := false
	indexFetchFailed := false
	for result := range results {
		if result.err != nil {
			// Full-text results can include unrelated funds. A temporarily
			// unavailable index must not turn the entire lookup into a 500 or
			// prevent checking the other independently verifiable candidates.
			indexFetchFailed = true
			continue
		}
		if result.parsed.Incomplete {
			incompleteMetadata = true
		}
		for _, identity := range result.parsed.Identities {
			if identity.Ticker != ticker || (expectedFundName != "" && normalizeFundName(identity.FundName) != normalizeFundName(expectedFundName)) {
				continue
			}
			identity.CIK = result.cik
			identity.Source = fundFilingIndexSource
			identity.EvidenceURL = result.indexURL
			candidates = appendUniqueFundIdentity(candidates, identity)
		}
	}
	if searchCtx.Err() != nil && len(candidates) == 0 {
		return FundResolution{Reason: fmt.Sprintf("SEC filing search timed out before a complete identity was found for %s", ticker)}, nil
	}

	if incompleteMetadata {
		return FundResolution{
			Candidates: candidates,
			Reason:     fmt.Sprintf("SEC filing index metadata is incomplete for ticker %s", ticker),
		}, nil
	}
	if len(candidates) == 0 && indexFetchFailed {
		return FundResolution{Reason: fmt.Sprintf("SEC filing indexes are temporarily unavailable for %s; please retry shortly", ticker)}, nil
	}
	// SEC full-text search display names usually identify the filing issuer
	// (for example, a fund trust), not the individual ETF. The filing index is
	// the identity authority because it carries the exact ticker/Series/Class
	// tuple that must be unique before auto-resolution.
	if len(candidates) == 1 {
		return FundResolution{Identity: &candidates[0]}, nil
	}
	if len(candidates) > 1 {
		return FundResolution{
			Candidates: candidates,
			Reason:     fmt.Sprintf("multiple SEC filing identities match ticker %s", ticker),
		}, nil
	}
	return FundResolution{Reason: fmt.Sprintf("no complete SEC filing identity found for %s", ticker)}, nil
}

func normalizeFundName(value string) string {
	value = strings.ToUpper(stdhtml.UnescapeString(strings.TrimSpace(value)))
	value = regexp.MustCompile(`[^A-Z0-9]+`).ReplaceAllString(value, " ")
	return strings.TrimSpace(value)
}

func (c *HTTPClient) MatchFundFiling(ctx context.Context, identity FundIdentity, filing FilingResult) (bool, string, error) {
	if normalizeFundCIK(identity.CIK) == "" || strings.TrimSpace(identity.SeriesID) == "" || strings.TrimSpace(identity.ClassID) == "" {
		return false, "identity_incomplete", nil
	}
	cik := normalizeFundCIK(filing.CIK)
	if cik == "" {
		return false, "filing_cik_missing", nil
	}
	if cik != normalizeFundCIK(identity.CIK) {
		return false, "cik_mismatch", nil
	}
	accessionNumber := strings.TrimSpace(filing.AccessionNumber)
	if !validAccessionNumber(accessionNumber) {
		return false, "accession_number_missing", nil
	}

	parsed, err := c.fetchFundIndex(ctx, fundFilingIndexURL(cik, accessionNumber))
	if err != nil {
		return false, "", err
	}
	if len(parsed.Identities) == 0 || parsed.Incomplete {
		return false, "filing_identity_incomplete", nil
	}
	seriesFound := false
	for _, candidate := range parsed.Identities {
		if candidate.SeriesID != strings.TrimSpace(identity.SeriesID) {
			continue
		}
		seriesFound = true
		if candidate.ClassID == strings.TrimSpace(identity.ClassID) {
			return true, "matched_class", nil
		}
	}
	if !seriesFound {
		return false, "series_not_found", nil
	}
	return false, "class_not_found", nil
}

func (c *HTTPClient) ParseFundFiling(ctx context.Context, filing FilingResult) (FundFilingMetadata, error) {
	cik := normalizeFundCIK(filing.CIK)
	if cik == "" {
		return FundFilingMetadata{}, fmt.Errorf("filing cik is missing")
	}
	accessionNumber := strings.TrimSpace(filing.AccessionNumber)
	if !validAccessionNumber(accessionNumber) {
		return FundFilingMetadata{}, fmt.Errorf("filing accession number is missing")
	}
	parsed, err := c.fetchFundIndex(ctx, fundFilingIndexURL(cik, accessionNumber))
	if err != nil {
		return FundFilingMetadata{}, err
	}
	metadata := FundFilingMetadata{Incomplete: parsed.Incomplete}
	for _, identity := range parsed.Identities {
		if strings.TrimSpace(identity.SeriesID) == "" || strings.TrimSpace(identity.ClassID) == "" {
			metadata.Incomplete = true
			continue
		}
		relationship := FundFilingRelationship{SeriesID: strings.TrimSpace(identity.SeriesID), ClassID: strings.TrimSpace(identity.ClassID)}
		duplicate := false
		for _, existing := range metadata.Relationships {
			if existing == relationship {
				duplicate = true
				break
			}
		}
		if !duplicate {
			metadata.Relationships = append(metadata.Relationships, relationship)
		}
	}
	return metadata, nil
}

func (c *HTTPClient) fetchFundIndex(ctx context.Context, indexURL string) (fundIndexParseResult, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, indexURL, nil)
	if err != nil {
		return fundIndexParseResult{}, err
	}
	c.setHeaders(req)
	resp, err := c.do(req, "fund filing index")
	if err != nil {
		return fundIndexParseResult{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fundIndexParseResult{}, fmt.Errorf("sec filing index status: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fundIndexParseResult{}, err
	}
	return parseFundIndex(string(body)), nil
}

func parseFundIndex(body string) fundIndexParseResult {
	if parsed, found := parseFundIndexSeriesClassTable(body); found {
		return parsed
	}
	text := normalizeFundIndexText(body)
	result := fundIndexParseResult{}
	var row fundIndexRow
	for _, line := range strings.Split(text, "\n") {
		kind, value := parseFundIndexField(line)
		if kind == "" {
			continue
		}
		switch kind {
		case "series_name":
			if !row.empty() {
				result.Incomplete = true
			}
			row = fundIndexRow{seriesName: value}
		case "series_id":
			if row.seriesName == "" || row.seriesID != "" || row.className != "" || row.classID != "" || row.ticker != "" {
				result.Incomplete = true
				continue
			}
			row.seriesID = value
		case "class_name":
			if row.seriesName == "" || row.seriesID == "" || row.className != "" || row.classID != "" || row.ticker != "" {
				result.Incomplete = true
				continue
			}
			row.className = value
		case "class_id":
			if row.seriesName == "" || row.seriesID == "" || row.className == "" || row.classID != "" || row.ticker != "" {
				result.Incomplete = true
				continue
			}
			row.classID = value
		case "ticker":
			if !row.completeWithoutTicker() || row.ticker != "" {
				result.Incomplete = true
				continue
			}
			row.ticker = value
			result.Identities = appendUniqueFundIdentity(result.Identities, FundIdentity{
				Ticker:   strings.ToUpper(strings.TrimSpace(row.ticker)),
				SeriesID: strings.TrimSpace(row.seriesID),
				ClassID:  strings.TrimSpace(row.classID),
				FundName: strings.TrimSpace(row.seriesName),
			})
			row = fundIndexRow{}
		}
	}
	if !row.empty() {
		result.Incomplete = true
	}
	return result
}

// parseFundIndexSeriesClassTable handles the table layout used by real SEC
// filing indexes: a Series row establishes a Series ID and each following
// Class/Contract row belongs to that series until the next Series row.
func parseFundIndexSeriesClassTable(body string) (fundIndexParseResult, bool) {
	rows := regexp.MustCompile(`(?is)<tr\b[^>]*>(.*?)</tr>`).FindAllStringSubmatch(body, -1)
	if len(rows) == 0 {
		return fundIndexParseResult{}, false
	}
	result := fundIndexParseResult{}
	var seriesID, seriesName, classID, className string
	seenSeries := false
	seriesPattern := regexp.MustCompile(`(?i)^Series\s+(S[0-9]+)\s*$`)
	classPattern := regexp.MustCompile(`(?i)^Class/Contract\s+(C[0-9]+)\s*$`)
	tickerPattern := regexp.MustCompile(`(?i)^Ticker(?:\s+Symbol)?\s+([A-Za-z0-9.\-]+)\s*$`)
	for _, row := range rows {
		cells := fundIndexRowCells(row[1])
		text := strings.Join(cells, " ")
		if text == "" {
			continue
		}
		if len(cells) >= 1 {
			if match := seriesPattern.FindStringSubmatch(cells[0]); len(match) == 2 {
				if classID != "" {
					result.Incomplete = true
				}
				seriesID, seriesName, classID, className = match[1], "", "", ""
				if len(cells) >= 2 {
					seriesName = cells[1]
				}
				seenSeries = true
				continue
			}
			if match := classPattern.FindStringSubmatch(cells[0]); len(match) == 2 {
				if !seenSeries || seriesID == "" || classID != "" {
					result.Incomplete = true
					continue
				}
				classID, className = match[1], ""
				if len(cells) >= 2 {
					className = cells[1]
				}
				if len(cells) >= 3 && seriesName != "" && className != "" {
					result.Identities = appendUniqueFundIdentity(result.Identities, FundIdentity{Ticker: strings.ToUpper(cells[2]), SeriesID: seriesID, ClassID: classID, FundName: seriesName})
					classID, className = "", ""
				}
				continue
			}
		}
		if len(cells) >= 2 && strings.EqualFold(cells[0], "Series") && regexp.MustCompile(`^S[0-9]+$`).MatchString(cells[1]) {
			if classID != "" {
				result.Incomplete = true
			}
			seriesID, seriesName, classID, className = cells[1], "", "", ""
			if len(cells) >= 3 {
				seriesName = cells[2]
			}
			seenSeries = true
			continue
		}
		if len(cells) >= 2 && strings.EqualFold(cells[0], "Class/Contract") && regexp.MustCompile(`^C[0-9]+$`).MatchString(cells[1]) {
			if !seenSeries || seriesID == "" || classID != "" {
				result.Incomplete = true
				continue
			}
			classID, className = cells[1], ""
			if len(cells) >= 3 {
				className = cells[2]
			}
			if len(cells) >= 4 && seriesName != "" && className != "" {
				result.Identities = appendUniqueFundIdentity(result.Identities, FundIdentity{Ticker: strings.ToUpper(cells[3]), SeriesID: seriesID, ClassID: classID, FundName: seriesName})
				classID, className = "", ""
			}
			continue
		}
		if match := seriesPattern.FindStringSubmatch(text); len(match) == 2 {
			if classID != "" {
				result.Incomplete = true
			}
			seriesID, seriesName, classID, className = match[1], "", "", ""
			seenSeries = true
			continue
		}
		if match := classPattern.FindStringSubmatch(text); len(match) == 2 {
			if !seenSeries || seriesID == "" || classID != "" {
				result.Incomplete = true
				continue
			}
			classID, className = match[1], ""
			continue
		}
		if match := tickerPattern.FindStringSubmatch(text); len(match) == 2 {
			if seriesID == "" || seriesName == "" || classID == "" || className == "" {
				result.Incomplete = true
				continue
			}
			result.Identities = appendUniqueFundIdentity(result.Identities, FundIdentity{Ticker: strings.ToUpper(match[1]), SeriesID: seriesID, ClassID: classID, FundName: seriesName})
			classID, className = "", ""
			continue
		}
		if classID != "" && className == "" {
			className = text
			continue
		}
		if seriesID != "" && seriesName == "" {
			seriesName = text
		}
	}
	if seenSeries && classID != "" {
		result.Incomplete = true
	}
	return result, seenSeries
}

func fundIndexRowText(row string) string {
	return strings.Join(fundIndexRowCells(row), " ")
}

func fundIndexRowCells(row string) []string {
	cellPattern := regexp.MustCompile(`(?is)<t[dh]\b[^>]*>(.*?)</t[dh]>`)
	cells := cellPattern.FindAllStringSubmatch(row, -1)
	values := make([]string, 0, len(cells))
	for _, cell := range cells {
		value := regexp.MustCompile(`(?is)<[^>]+>`).ReplaceAllString(cell[1], " ")
		value = strings.Join(strings.Fields(stdhtml.UnescapeString(value)), " ")
		if value != "" {
			values = append(values, value)
		}
	}
	return values
}

type fundIndexRow struct {
	seriesName string
	seriesID   string
	className  string
	classID    string
	ticker     string
}

func (r fundIndexRow) empty() bool {
	return r.seriesName == "" && r.seriesID == "" && r.className == "" && r.classID == "" && r.ticker == ""
}

func (r fundIndexRow) completeWithoutTicker() bool {
	return r.seriesName != "" && r.seriesID != "" && r.className != "" && r.classID != ""
}

func parseFundIndexField(line string) (string, string) {
	line = strings.TrimSpace(line)
	patterns := []struct {
		kind    string
		pattern string
	}{
		{kind: "series_name", pattern: `(?i)^Series(?:\s+Name)?\s*:\s*(.+?)\s*$`},
		{kind: "series_id", pattern: `(?i)^Series\s+(?:ID|Identifier)\s*:\s*(S[0-9]+)\s*$`},
		{kind: "class_name", pattern: `(?i)^Class/Contract(?:\s+Name)?\s*:\s*(.+?)\s*$`},
		{kind: "class_id", pattern: `(?i)^Class/Contract\s+(?:ID|Identifier)\s*:\s*(C[0-9]+)\s*$`},
		{kind: "ticker", pattern: `(?i)^(?:Ticker(?:\s+Symbol)?|Symbol)\s*:\s*([A-Za-z0-9.\-]+)\s*$`},
	}
	for _, candidate := range patterns {
		match := regexp.MustCompile(candidate.pattern).FindStringSubmatch(line)
		if len(match) == 2 {
			return candidate.kind, strings.TrimSpace(match[1])
		}
	}
	return "", ""
}

func normalizeFundIndexText(body string) string {
	body = regexp.MustCompile(`(?is)<(?:br|/p|/div|/tr|/li)\b[^>]*>`).ReplaceAllString(body, "\n")
	body = regexp.MustCompile(`(?is)<[^>]+>`).ReplaceAllString(body, "")
	body = strings.ReplaceAll(body, "&nbsp;", " ")
	return body
}

func rawStringSlice(raw json.RawMessage) []string {
	var values []string
	if err := json.Unmarshal(raw, &values); err == nil {
		return values
	}
	var value string
	if err := json.Unmarshal(raw, &value); err == nil && strings.TrimSpace(value) != "" {
		return []string{value}
	}
	return nil
}

func validAccessionNumber(value string) bool {
	return regexp.MustCompile(`^[0-9]{10}-[0-9]{2}-[0-9]{6}$`).MatchString(value)
}

func fundFilingIndexURL(cik, accessionNumber string) string {
	archiveCIK := strings.TrimLeft(normalizeFundCIK(cik), "0")
	if archiveCIK == "" {
		archiveCIK = "0"
	}
	accessionPath := strings.ReplaceAll(accessionNumber, "-", "")
	return fmt.Sprintf("https://www.sec.gov/Archives/edgar/data/%s/%s/%s-index.htm", archiveCIK, accessionPath, accessionNumber)
}

func appendUniqueFundIdentity(identities []FundIdentity, identity FundIdentity) []FundIdentity {
	for _, existing := range identities {
		if existing.CIK == identity.CIK && existing.SeriesID == identity.SeriesID && existing.ClassID == identity.ClassID && existing.Ticker == identity.Ticker {
			return identities
		}
	}
	return append(identities, identity)
}

func fundTickerFieldIndexes(fields []string) (map[string]int, []string) {
	indexes := make(map[string]int, len(fields))
	for index, field := range fields {
		key := strings.ToLower(strings.TrimSpace(field))
		if key != "" {
			indexes[key] = index
		}
	}
	required := []string{"cik", "seriesid", "classid", "symbol"}
	missing := make([]string, 0)
	for _, field := range required {
		if _, ok := indexes[field]; !ok {
			missing = append(missing, field)
		}
	}
	return indexes, missing
}

func fundIdentityFromRow(row []json.RawMessage, indexes map[string]int) (FundIdentity, bool) {
	cik := normalizeFundCIK(fundTickerValue(row, indexes["cik"]))
	seriesID := strings.TrimSpace(fundTickerValue(row, indexes["seriesid"]))
	classID := strings.TrimSpace(fundTickerValue(row, indexes["classid"]))
	ticker := strings.ToUpper(strings.TrimSpace(fundTickerValue(row, indexes["symbol"])))
	if cik == "" || seriesID == "" || classID == "" || ticker == "" {
		return FundIdentity{}, false
	}
	return FundIdentity{
		Ticker:   ticker,
		CIK:      cik,
		SeriesID: seriesID,
		ClassID:  classID,
		Source:   fundIdentitySource,
	}, true
}

func fundTickerValue(row []json.RawMessage, index int) string {
	if index < 0 || index >= len(row) {
		return ""
	}
	var value string
	if err := json.Unmarshal(row[index], &value); err == nil {
		return value
	}
	var number json.Number
	if err := json.Unmarshal(row[index], &number); err == nil {
		return number.String()
	}
	return ""
}

func normalizeFundCIK(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	for _, char := range value {
		if char < '0' || char > '9' {
			return ""
		}
	}
	if len(value) > 10 {
		return ""
	}
	return normalizeCIK(value)
}
