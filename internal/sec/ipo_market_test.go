package sec

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestHTTPClientListListedCompaniesTableDriven(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		body       string
		wantErr    bool
	}{
		{name: "parses official exchange mapping", statusCode: http.StatusOK, body: `{"fields":["cik","name","ticker","exchange"],"data":[[1318605,"Tesla, Inc.","TSLA","Nasdaq"]]}`},
		{name: "rejects non success", statusCode: http.StatusTooManyRequests, body: `{}`, wantErr: true},
		{name: "rejects malformed data", statusCode: http.StatusOK, body: `{"fields":[],"data":[[1]]}`, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := NewHTTPClient("https://data.sec.test", "test test@example.com", 0)
			client.CompanyTickersExchangeURL = "https://sec.test/company_tickers_exchange.json"
			client.Client = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				if req.Header.Get("User-Agent") == "" {
					t.Fatalf("missing user agent")
				}
				return &http.Response{StatusCode: tt.statusCode, Body: io.NopCloser(strings.NewReader(tt.body)), Header: make(http.Header)}, nil
			})}
			got, err := client.ListListedCompanies(context.Background())
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ListListedCompanies expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("ListListedCompanies: %v", err)
			}
			if len(got) != 1 || got[0].CIK != "0001318605" || got[0].Ticker != "TSLA" || got[0].Exchange != "Nasdaq" {
				t.Fatalf("companies = %+v", got)
			}
		})
	}
}

func TestHTTPClientFetchFilingDocumentTableDriven(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		wantErr    bool
	}{
		{name: "fetches document", statusCode: http.StatusOK},
		{name: "rejects non success", statusCode: http.StatusNotFound, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := NewHTTPClient("https://data.sec.test", "test test@example.com", 0)
			client.Client = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: tt.statusCode, Body: io.NopCloser(strings.NewReader("<html><body>Offering</body></html>")), Header: make(http.Header)}, nil
			})}
			got, err := client.FetchFilingDocument(context.Background(), "https://sec.test/filing")
			if tt.wantErr {
				if err == nil {
					t.Fatalf("FetchFilingDocument expected error")
				}
				return
			}
			if err != nil || !strings.Contains(got, "Offering") {
				t.Fatalf("document=%q err=%v", got, err)
			}
		})
	}
}

func TestParse424B4OfferingTableDriven(t *testing.T) {
	tests := []struct {
		name       string
		text       string
		wantOK     bool
		wantPrice  string
		wantShares int64
		wantGross  string
		wantType   string
	}{
		{name: "extracts follow-on offering", text: `<p>We are offering 10,000,000 ordinary shares. The public offering price is $15.00 per share.</p>`, wantOK: true, wantPrice: "15.00", wantShares: 10000000, wantGross: "150000000.00", wantType: "follow_on"},
		{name: "extracts offer price wording", text: `This prospectus relates to an offering of 2,500,000 shares at an offering price of $8.50 per share.`, wantOK: true, wantPrice: "8.50", wantShares: 2500000, wantGross: "21250000.00"},
		{name: "extracts Kardigan price per share wording", text: `We are offering 25,000,000 shares of our common stock. The initial public offering price per share is $16.00.`, wantOK: true, wantPrice: "16.00", wantShares: 25000000, wantGross: "400000000.00", wantType: "initial"},
		{name: "normalizes SEC nonbreaking spaces", text: `We are offering 25,000,000&nbsp;shares of our common stock. The initial public offering price per share is $16.00.`, wantOK: true, wantPrice: "16.00", wantShares: 25000000, wantGross: "400000000.00"},
		{name: "rejects ambiguous prices", text: `Prices may range from $8.00 to $10.00 per share for 1,000,000 shares.`, wantOK: false},
		{name: "rejects unsupported text", text: `Final prospectus without structured pricing sentence.`, wantOK: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := Parse424B4Offering(tt.text)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v; offering=%+v", ok, tt.wantOK, got)
			}
			if ok && (got.OfferPrice != tt.wantPrice || got.SharesOffered != tt.wantShares || got.GrossProceeds != tt.wantGross || (tt.wantType != "" && got.OfferingType != tt.wantType)) {
				t.Fatalf("offering = %+v", got)
			}
		})
	}
}
