package sec

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

const fundIdentitySource = "sec_company_tickers_mf"

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
		return FundResolution{Reason: fmt.Sprintf("no SEC fund ticker record found for %s", want)}, nil
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
