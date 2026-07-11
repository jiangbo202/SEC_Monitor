package sec

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"math/big"
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

type ListedCompany struct {
	CIK      string `json:"cik"`
	Name     string `json:"name"`
	Ticker   string `json:"ticker"`
	Exchange string `json:"exchange"`
}

type IPOOffering struct {
	OfferPrice    string `json:"offer_price"`
	SharesOffered int64  `json:"shares_offered"`
	GrossProceeds string `json:"gross_proceeds"`
	OfferingType  string `json:"offering_type"`
	Source        string `json:"source"`
	Confidence    string `json:"confidence"`
	ParseMessage  string `json:"parse_message"`
}

type listedCompaniesResponse struct {
	Fields []string            `json:"fields"`
	Data   [][]json.RawMessage `json:"data"`
}

func (c *HTTPClient) ListListedCompanies(ctx context.Context) ([]ListedCompany, error) {
	endpoint := strings.TrimSpace(c.CompanyTickersExchangeURL)
	if endpoint == "" {
		endpoint = "https://www.sec.gov/files/company_tickers_exchange.json"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
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
		return nil, fmt.Errorf("sec listed companies status: %d", resp.StatusCode)
	}
	var payload listedCompaniesResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 20<<20)).Decode(&payload); err != nil {
		return nil, err
	}
	indexes := map[string]int{}
	for index, field := range payload.Fields {
		indexes[strings.ToLower(strings.TrimSpace(field))] = index
	}
	for _, required := range []string{"cik", "name", "ticker", "exchange"} {
		if _, ok := indexes[required]; !ok {
			return nil, fmt.Errorf("sec listed companies missing field: %s", required)
		}
	}
	companies := make([]ListedCompany, 0, len(payload.Data))
	for _, row := range payload.Data {
		if len(row) < len(payload.Fields) {
			continue
		}
		var cik int64
		var name, ticker, exchange string
		if err := json.Unmarshal(row[indexes["cik"]], &cik); err != nil || cik <= 0 {
			continue
		}
		if err := json.Unmarshal(row[indexes["name"]], &name); err != nil {
			continue
		}
		if err := json.Unmarshal(row[indexes["ticker"]], &ticker); err != nil {
			continue
		}
		if err := json.Unmarshal(row[indexes["exchange"]], &exchange); err != nil {
			continue
		}
		companies = append(companies, ListedCompany{CIK: fmt.Sprintf("%010d", cik), Name: name, Ticker: strings.ToUpper(strings.TrimSpace(ticker)), Exchange: strings.TrimSpace(exchange)})
	}
	return companies, nil
}

func (c *HTTPClient) FetchFilingDocument(ctx context.Context, filingURL string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSpace(filingURL), nil)
	if err != nil {
		return "", err
	}
	c.setHeaders(req)
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("sec filing document status: %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	return string(body), err
}

var (
	tagsPattern        = regexp.MustCompile(`(?s)<[^>]+>`)
	tableCellPattern   = regexp.MustCompile(`(?is)</?(?:td|th|tr)[^>]*>`)
	spacePattern       = regexp.MustCompile(`\s+`)
	offerPricePatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)(?:public\s+offering\s+price|offering\s+price)\s+(?:is|of|at)\s+\$\s*([0-9]+(?:\.[0-9]{1,4})?)\s+per\s+share`),
		regexp.MustCompile(`(?i)(?:initial\s+)?public\s+offering\s+price\s+per\s+share\s+(?:is|of|at)\s+\$\s*([0-9]+(?:\.[0-9]{1,4})?)`),
	}
	offerSharesPattern      = regexp.MustCompile(`(?i)(?:we\s+are\s+offering|offering\s+of)\s+([0-9][0-9,]*)\s+(?:ordinary\s+|common\s+)?shares`)
	tableOfferPricePattern  = regexp.MustCompile(`(?i)public\s+offering\s+price\s+\$\s*([0-9]+(?:\.[0-9]{1,4})?)`)
	tableOfferSharesPattern = regexp.MustCompile(`(?i)shares\s+offered\s+([0-9][0-9,]*)`)
)

func Parse424B4Offering(document string) (IPOOffering, bool) {
	text := normalize424B4Document(document)
	text = strings.NewReplacer("\u00a0", " ", "\u202f", " ", "\u2007", " ").Replace(text)
	text = spacePattern.ReplaceAllString(text, " ")
	var priceMatch []string
	for _, pattern := range offerPricePatterns {
		if priceMatch = pattern.FindStringSubmatch(text); len(priceMatch) == 2 {
			break
		}
	}
	if len(priceMatch) != 2 {
		priceMatch = tableOfferPricePattern.FindStringSubmatch(text)
	}
	sharesMatch := offerSharesPattern.FindStringSubmatch(text)
	if len(sharesMatch) != 2 {
		sharesMatch = tableOfferSharesPattern.FindStringSubmatch(text)
	}
	if len(priceMatch) != 2 {
		return IPOOffering{ParseMessage: "offer_price_not_found"}, false
	}
	if len(sharesMatch) != 2 {
		return IPOOffering{ParseMessage: "shares_offered_not_found"}, false
	}
	shares, err := strconv.ParseInt(strings.ReplaceAll(sharesMatch[1], ",", ""), 10, 64)
	price, errPrice := strconv.ParseFloat(priceMatch[1], 64)
	if err != nil || shares <= 0 {
		return IPOOffering{ParseMessage: "shares_offered_invalid"}, false
	}
	if errPrice != nil || price <= 0 {
		return IPOOffering{ParseMessage: "offer_price_invalid"}, false
	}
	priceValue, ok := new(big.Rat).SetString(priceMatch[1])
	if !ok {
		return IPOOffering{ParseMessage: "offer_price_invalid"}, false
	}
	gross := new(big.Rat).Mul(priceValue, new(big.Rat).SetInt64(shares))
	offeringType := "follow_on"
	if strings.Contains(strings.ToLower(text), "initial public offering") {
		offeringType = "initial"
	}
	return IPOOffering{OfferPrice: priceMatch[1], SharesOffered: shares, GrossProceeds: gross.FloatString(2), OfferingType: offeringType, Confidence: "medium"}, true
}

func normalize424B4Document(document string) string {
	withTableBoundaries := tableCellPattern.ReplaceAllString(document, " ")
	return html.UnescapeString(tagsPattern.ReplaceAllString(withTableBoundaries, " "))
}
