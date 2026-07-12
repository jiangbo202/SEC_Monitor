package sec

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"unicode"
)

const fundIdentitySource = "sec_company_tickers_mf"

const fundFilingIndexSource = "sec_filing_index"

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

	resp, err := c.httpClient().Do(req)
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
		return c.resolveFundTickerFromSearch(ctx, want)
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
				ADSH         string          `json:"adsh"`
				CIKs         json.RawMessage `json:"ciks"`
				DisplayNames json.RawMessage `json:"display_names"`
			} `json:"_source"`
		} `json:"hits"`
	} `json:"hits"`
}

type fundIndexParseResult struct {
	Identities []FundIdentity
	Incomplete bool
}

func (c *HTTPClient) resolveFundTickerFromSearch(ctx context.Context, ticker string) (FundResolution, error) {
	searchURL := "https://efts.sec.gov/LATEST/search-index?" + url.Values{"q": []string{ticker}}.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, searchURL, nil)
	if err != nil {
		return FundResolution{}, err
	}
	c.setHeaders(req)
	resp, err := c.httpClient().Do(req)
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

	candidates := make([]FundIdentity, 0)
	exactCandidates := make([]FundIdentity, 0)
	incompleteMetadata := false
	fundNameMismatch := false
	for _, hit := range payload.Hits.Hits {
		accessionNumber := strings.TrimSpace(hit.Source.ADSH)
		displayNames := rawStringSlice(hit.Source.DisplayNames)
		if !validAccessionNumber(accessionNumber) || !searchDisplayNamesContainTicker(displayNames, ticker) {
			continue
		}
		for _, rawCIK := range rawStringSlice(hit.Source.CIKs) {
			cik := normalizeFundCIK(rawCIK)
			if cik == "" {
				continue
			}
			indexURL := fundFilingIndexURL(cik, accessionNumber)
			parsed, err := c.fetchFundIndex(ctx, indexURL)
			if err != nil {
				return FundResolution{}, err
			}
			if parsed.Incomplete {
				incompleteMetadata = true
			}
			for _, identity := range parsed.Identities {
				if identity.Ticker != ticker {
					continue
				}
				identity.CIK = cik
				identity.Source = fundFilingIndexSource
				identity.EvidenceURL = indexURL
				candidates = appendUniqueFundIdentity(candidates, identity)
				if fundNameMatchesSearchDisplayName(identity.FundName, displayNames, ticker) {
					exactCandidates = appendUniqueFundIdentity(exactCandidates, identity)
				} else {
					fundNameMismatch = true
				}
			}
		}
	}

	if incompleteMetadata {
		return FundResolution{
			Candidates: candidates,
			Reason:     fmt.Sprintf("SEC filing index metadata is incomplete for ticker %s", ticker),
		}, nil
	}
	if fundNameMismatch {
		return FundResolution{
			Candidates: candidates,
			Reason:     fmt.Sprintf("SEC filing series name does not exactly match search display name for ticker %s", ticker),
		}, nil
	}
	if len(candidates) == 1 && len(exactCandidates) == 1 {
		return FundResolution{Identity: &exactCandidates[0]}, nil
	}
	if len(candidates) > 1 {
		return FundResolution{
			Candidates: candidates,
			Reason:     fmt.Sprintf("multiple SEC filing identities match ticker %s", ticker),
		}, nil
	}
	return FundResolution{Reason: fmt.Sprintf("no complete SEC filing identity found for %s", ticker)}, nil
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

func (c *HTTPClient) fetchFundIndex(ctx context.Context, indexURL string) (fundIndexParseResult, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, indexURL, nil)
	if err != nil {
		return fundIndexParseResult{}, err
	}
	c.setHeaders(req)
	resp, err := c.httpClient().Do(req)
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

func searchDisplayNamesContainTicker(displayNames []string, ticker string) bool {
	pattern := regexp.MustCompile(`(?i)(?:^|[^A-Z0-9])` + regexp.QuoteMeta(ticker) + `(?:$|[^A-Z0-9])`)
	for _, displayName := range displayNames {
		if pattern.MatchString(strings.TrimSpace(displayName)) {
			return true
		}
	}
	return false
}

func fundNameMatchesSearchDisplayName(fundName string, displayNames []string, ticker string) bool {
	want := normalizeFundName(fundName)
	if want == "" {
		return false
	}
	tickerPattern := regexp.MustCompile(`(?i)\(\s*` + regexp.QuoteMeta(strings.TrimSpace(ticker)) + `\s*\)`)
	for _, displayName := range displayNames {
		match := tickerPattern.FindStringIndex(displayName)
		if match == nil {
			continue
		}
		if normalizeFundName(displayName[:match[0]]) == want {
			return true
		}
	}
	return false
}

func normalizeFundName(value string) string {
	var normalized strings.Builder
	spacePending := false
	for _, char := range strings.ToUpper(strings.TrimSpace(value)) {
		if unicode.IsLetter(char) || unicode.IsDigit(char) {
			if spacePending && normalized.Len() > 0 {
				normalized.WriteByte(' ')
			}
			normalized.WriteRune(char)
			spacePending = false
			continue
		}
		spacePending = normalized.Len() > 0
	}
	return strings.TrimSpace(normalized.String())
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
