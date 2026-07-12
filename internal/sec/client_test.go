package sec

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type fixtureRoutes map[string]fixtureRoute

type fixtureRoute struct {
	statusCode int
	body       string
}

func fixtureBody(body string) fixtureRoute {
	return fixtureRoute{statusCode: http.StatusOK, body: body}
}

func newFixtureHTTPClient(t *testing.T, routes fixtureRoutes) *HTTPClient {
	t.Helper()
	client := NewHTTPClient("https://sec.test", "sec-monitor-test", time.Second)
	client.CompanyTickersMFURL = "https://sec.test/company_tickers_mf.json"
	client.Client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		route, ok := routes[r.URL.Path]
		if !ok {
			t.Fatalf("unexpected request: %s", r.URL.String())
		}
		statusCode := route.statusCode
		if statusCode == 0 {
			statusCode = http.StatusOK
		}
		return &http.Response{
			StatusCode: statusCode,
			Body:       io.NopCloser(strings.NewReader(route.body)),
			Header:     make(http.Header),
		}, nil
	})}
	return client
}

func newTestHTTPClient(t *testing.T, responses map[string]string) *HTTPClient {
	t.Helper()
	routes := make(fixtureRoutes, len(responses))
	for path, body := range responses {
		routes[path] = fixtureBody(body)
	}
	return newFixtureHTTPClient(t, routes)
}

func searchHit(accessionNumber, cik, displayName string) string {
	return `{"hits":{"hits":[{"_source":{"adsh":"` + accessionNumber + `","ciks":["` + cik + `"],"display_names":["` + displayName + `"]}}]}}`
}

func fundIndex(seriesName, seriesID, className, classID, ticker string) string {
	return `<html><body>
Series: ` + seriesName + `
Series ID: ` + seriesID + `
Class/Contract: ` + className + `
Class/Contract ID: ` + classID + `
Ticker Symbol: ` + ticker + `
</body></html>`
}

func TestHTTPClientResolveFundTickerFallsBackToSECSearch(t *testing.T) {
	client := newFixtureHTTPClient(t, fixtureRoutes{
		"/company_tickers_mf.json": fixtureBody(`{"fields":["cik","seriesId","classId","symbol"],"data":[]}`),
		"/LATEST/search-index":     fixtureBody(searchHit("0001976517-26-005961", "0001976517", "Roundhill Memory ETF (DRAM)")),
		"/Archives/edgar/data/1976517/000197651726005961/0001976517-26-005961-index.htm": fixtureBody(fundIndex("Roundhill Memory ETF", "S000102337", "Roundhill Memory ETF", "C000272806", "DRAM")),
	})

	got, err := client.ResolveFundTicker(context.Background(), "DRAM")
	if err != nil || got.Identity == nil || got.Identity.ClassID != "C000272806" {
		t.Fatalf("resolution=%+v err=%v", got, err)
	}
	if got.Identity.Source != "sec_filing_index" || got.Identity.EvidenceURL == "" {
		t.Fatalf("filing identity=%+v", *got.Identity)
	}
}

func TestHTTPClientResolveFundTickerFallbackKeepsAmbiguousCandidates(t *testing.T) {
	client := newFixtureHTTPClient(t, fixtureRoutes{
		"/company_tickers_mf.json": fixtureBody(`{"fields":["cik","seriesId","classId","symbol"],"data":[]}`),
		"/LATEST/search-index": fixtureBody(`{"hits":{"hits":[
            {"_source":{"adsh":"0001976517-26-000001","ciks":["0001976517"],"display_names":["Roundhill Memory ETF (DRAM)"]}},
            {"_source":{"adsh":"0001976518-26-000001","ciks":["0001976518"],"display_names":["Different Memory ETF (DRAM)"]}}
        ]}}`),
		"/Archives/edgar/data/1976517/000197651726000001/0001976517-26-000001-index.htm": fixtureBody(fundIndex("Roundhill Memory ETF", "S1", "Roundhill Memory ETF", "C1", "DRAM")),
		"/Archives/edgar/data/1976518/000197651826000001/0001976518-26-000001-index.htm": fixtureBody(fundIndex("Different Memory ETF", "S2", "Different Memory ETF", "C2", "DRAM")),
	})

	got, err := client.ResolveFundTicker(context.Background(), "DRAM")
	if err != nil {
		t.Fatalf("ResolveFundTicker: %v", err)
	}
	if got.Identity != nil || len(got.Candidates) != 2 || got.Reason == "" {
		t.Fatalf("resolution=%+v, want two candidates without auto resolution", got)
	}
}

func TestHTTPClientResolveFundTickerFallbackDoesNotAutoResolveUnsafeIndexMetadata(t *testing.T) {
	tests := []struct {
		name        string
		displayName string
		indexBody   string
	}{
		{
			name:        "mixed complete and incomplete index metadata",
			displayName: "Roundhill Memory ETF (DRAM)",
			indexBody:   fundIndex("Roundhill Memory ETF", "S1", "Roundhill Memory ETF", "C1", "DRAM") + "\nSeries: orphaned series",
		},
		{
			name:        "series name does not exactly match search display name",
			displayName: "Roundhill Memory ETF (DRAM)",
			indexBody:   fundIndex("Different Memory ETF", "S1", "Different Memory ETF", "C1", "DRAM"),
		},
		{
			name:        "surplus and misaligned index metadata",
			displayName: "Roundhill Memory ETF (DRAM)",
			indexBody:   fundIndex("Roundhill Memory ETF", "S1", "Roundhill Memory ETF", "C1", "DRAM") + "\nClass/Contract: orphaned class",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := newFixtureHTTPClient(t, fixtureRoutes{
				"/company_tickers_mf.json": fixtureBody(`{"fields":["cik","seriesId","classId","symbol"],"data":[]}`),
				"/LATEST/search-index":     fixtureBody(searchHit("0001976517-26-000001", "0001976517", tt.displayName)),
				"/Archives/edgar/data/1976517/000197651726000001/0001976517-26-000001-index.htm": fixtureBody(tt.indexBody),
			})

			got, err := client.ResolveFundTicker(context.Background(), "DRAM")
			if err != nil {
				t.Fatalf("ResolveFundTicker: %v", err)
			}
			if got.Identity != nil || len(got.Candidates) == 0 || strings.TrimSpace(got.Reason) == "" {
				t.Fatalf("resolution=%+v, want candidates and a safe non-resolution reason", got)
			}
		})
	}
}

func TestFundNameMatchesSearchDisplayNameNormalization(t *testing.T) {
	if !fundNameMatchesSearchDisplayName("Roundhill Memory ETF Inc", []string{" roundhill-memory ETF, inc. (DRAM) "}, "DRAM") {
		t.Fatal("punctuation and case normalization should retain an exact fund-name match")
	}
	if fundNameMatchesSearchDisplayName("Roundhill Memory ETF", []string{"Roundhill Memory ETF Trust (DRAM)"}, "DRAM") {
		t.Fatal("substring-only fund-name matching must not be accepted")
	}
}

func TestHTTPClientMatchFundFiling(t *testing.T) {
	identity := FundIdentity{Ticker: "DRAM", CIK: "0001976517", SeriesID: "S1", ClassID: "C1"}
	client := newFixtureHTTPClient(t, fixtureRoutes{
		"/Archives/edgar/data/1976517/000197651726000001/0001976517-26-000001-index.htm": fixtureBody(fundIndex("Roundhill Memory ETF", "S1", "Roundhill Memory ETF", "C1", "DRAM")),
	})

	matched, reason, err := client.MatchFundFiling(context.Background(), identity, FilingResult{CIK: identity.CIK, AccessionNumber: "0001976517-26-000001"})
	if err != nil || !matched || reason != "matched_class" {
		t.Fatalf("matched=%v reason=%q err=%v", matched, reason, err)
	}
}

func TestHTTPClientMatchFundFilingTableDriven(t *testing.T) {
	identity := FundIdentity{Ticker: "DRAM", CIK: "0001976517", SeriesID: "S1", ClassID: "C1"}
	tests := []struct {
		name        string
		route       fixtureRoute
		wantMatched bool
		wantReason  string
		wantErr     bool
	}{
		{
			name:       "returns candidates reason when index has different series",
			route:      fixtureBody(fundIndex("Other Fund", "S2", "Other Fund", "C2", "DRAM") + fundIndex("Another Fund", "S3", "Another Fund", "C3", "DRAM")),
			wantReason: "series_not_found",
		},
		{
			name:       "does not match incomplete index identity",
			route:      fixtureBody(`<html><body>Series: Roundhill Memory ETF\nTicker Symbol: DRAM</body></html>`),
			wantReason: "filing_identity_incomplete",
		},
		{
			name:    "returns error for index http failure",
			route:   fixtureRoute{statusCode: http.StatusServiceUnavailable, body: `busy`},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := newFixtureHTTPClient(t, fixtureRoutes{
				"/Archives/edgar/data/1976517/000197651726000001/0001976517-26-000001-index.htm": tt.route,
			})
			matched, reason, err := client.MatchFundFiling(context.Background(), identity, FilingResult{CIK: identity.CIK, AccessionNumber: "0001976517-26-000001"})
			if tt.wantErr {
				if err == nil {
					t.Fatalf("MatchFundFiling expected error")
				}
				return
			}
			if err != nil || matched != tt.wantMatched || reason != tt.wantReason {
				t.Fatalf("matched=%v reason=%q err=%v", matched, reason, err)
			}
		})
	}
}

func TestHTTPClientResolveFundTicker(t *testing.T) {
	client := newTestHTTPClient(t, map[string]string{
		"/company_tickers_mf.json": `{"fields":["cik","seriesId","classId","symbol"],"data":[[1976517,"S000102337","C000272806","DRAM"]]}`,
	})
	got, err := client.ResolveFundTicker(context.Background(), "dram")
	if err != nil || got.Identity == nil {
		t.Fatalf("resolution=%+v err=%v", got, err)
	}
	if *got.Identity != (FundIdentity{Ticker: "DRAM", CIK: "0001976517", SeriesID: "S000102337", ClassID: "C000272806", Source: "sec_company_tickers_mf"}) {
		t.Fatalf("identity=%+v", *got.Identity)
	}
}

func TestHTTPClientResolveFundTickerTableDriven(t *testing.T) {
	tests := []struct {
		name           string
		statusCode     int
		body           string
		wantErr        bool
		wantIdentity   bool
		wantCandidates int
	}{
		{
			name:         "uses field names when SEC changes column order",
			body:         `{"fields":["symbol","classId","cik","seriesId"],"data":[["DRAM","C000272806",1976517,"S000102337"]]}`,
			wantIdentity: true,
		},
		{
			name: "does not resolve incomplete identity with empty cik",
			body: `{"fields":["cik","seriesId","classId","symbol"],"data":[["","S000102337","C000272806","DRAM"]]}`,
		},
		{
			name: "does not resolve identity with more than ten digit cik",
			body: `{"fields":["cik","seriesId","classId","symbol"],"data":[[12345678901,"S000102337","C000272806","DRAM"]]}`,
		},
		{
			name:           "returns candidates without auto resolving ambiguous ticker",
			body:           `{"fields":["cik","seriesId","classId","symbol"],"data":[[1976517,"S000102337","C000272806","DRAM"],[1976518,"S000102338","C000272807","DRAM"]]}`,
			wantCandidates: 2,
		},
		{
			name: "does not resolve when required SEC field is missing",
			body: `{"fields":["cik","seriesId","symbol"],"data":[[1976517,"S000102337","DRAM"]]}`,
		},
		{
			name:       "returns error for SEC rate limit",
			statusCode: http.StatusTooManyRequests,
			body:       `{}`,
			wantErr:    true,
		},
		{
			name:    "returns error for invalid json",
			body:    `{`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := NewHTTPClient("https://sec.test", "sec-monitor-test", time.Second)
			client.Client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				statusCode := tt.statusCode
				if statusCode == 0 {
					statusCode = http.StatusOK
				}
				return &http.Response{
					StatusCode: statusCode,
					Body:       io.NopCloser(strings.NewReader(tt.body)),
					Header:     make(http.Header),
				}, nil
			})}

			got, err := client.ResolveFundTicker(context.Background(), "DRAM")
			if tt.wantErr {
				if err == nil {
					t.Fatal("ResolveFundTicker expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("ResolveFundTicker: %v", err)
			}
			if (got.Identity != nil) != tt.wantIdentity {
				t.Fatalf("identity=%+v, want present=%t", got.Identity, tt.wantIdentity)
			}
			if len(got.Candidates) != tt.wantCandidates {
				t.Fatalf("candidates=%+v, want %d", got.Candidates, tt.wantCandidates)
			}
			if !tt.wantIdentity && strings.TrimSpace(got.Reason) == "" {
				t.Fatalf("resolution reason must explain safe failure: %+v", got)
			}
		})
	}
}

func TestNormalizeCIKTableDriven(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "empty", in: "", want: ""},
		{name: "pads numeric cik", in: "320193", want: "0000320193"},
		{name: "keeps all zero value", in: "0000", want: "0000000000"},
		{name: "trims whitespace", in: "  123  ", want: "0000000123"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizeCIK(tt.in); got != tt.want {
				t.Fatalf("normalizeCIK(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestValueAtTableDriven(t *testing.T) {
	tests := []struct {
		name  string
		index int
		want  string
	}{
		{name: "first", index: 0, want: "a"},
		{name: "second", index: 1, want: "b"},
		{name: "negative", index: -1, want: ""},
		{name: "out of range", index: 2, want: ""},
	}
	values := []string{"a", "b"}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := valueAt(values, tt.index); got != tt.want {
				t.Fatalf("valueAt index %d = %q, want %q", tt.index, got, tt.want)
			}
		})
	}
}

func TestHTTPClientLookupCIKTableDriven(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		body       string
		ticker     string
		wantCIK    string
		wantErr    bool
	}{
		{name: "finds ticker case insensitive", statusCode: http.StatusOK, body: `{"0":{"cik_str":320193,"ticker":"AAPL","title":"Apple Inc."}}`, ticker: "aapl", wantCIK: "0000320193"},
		{name: "not found returns error", statusCode: http.StatusOK, body: `{"0":{"cik_str":789019,"ticker":"MSFT","title":"Microsoft Corp."}}`, ticker: "AAPL", wantErr: true},
		{name: "non success status returns error", statusCode: http.StatusTooManyRequests, body: `{}`, ticker: "AAPL", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := NewHTTPClient("https://sec.test", "sec-monitor-test", time.Second)
			client.CompanyTickersURL = "https://sec.test/company_tickers.json"
			client.Client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				if r.Header.Get("User-Agent") == "" {
					t.Fatalf("missing user agent")
				}
				return &http.Response{
					StatusCode: tt.statusCode,
					Body:       io.NopCloser(strings.NewReader(tt.body)),
					Header:     make(http.Header),
				}, nil
			})}

			got, _, err := client.LookupCIK(context.Background(), tt.ticker)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("LookupCIK expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("LookupCIK: %v", err)
			}
			if got != tt.wantCIK {
				t.Fatalf("CIK = %q, want %q", got, tt.wantCIK)
			}
		})
	}
}

func TestHTTPClientListFilingsTableDriven(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		body       string
		query      FilingQuery
		wantLen    int
		wantErr    bool
	}{
		{
			name:       "maps recent submissions",
			statusCode: http.StatusOK,
			query:      FilingQuery{Ticker: "aapl", CIK: "320193"},
			wantLen:    1,
			body: `{
				"cik":"0000320193",
				"name":"Apple Inc.",
				"filings":{"recent":{
					"accessionNumber":["0000320193-26-000001"],
					"form":["8-K"],
					"filingDate":["2026-06-01"],
					"acceptanceDateTime":["2026-06-01T16:30:12.000Z"],
					"primaryDocument":["aapl-20260601.htm"],
					"primaryDocDescription":["Current report"]
				}}
			}`,
		},
		{
			name:       "loads archived submissions when requested",
			statusCode: http.StatusOK,
			query:      FilingQuery{Ticker: "aapl", CIK: "320193", FetchFullHistory: true},
			wantLen:    2,
			body: `{
				"cik":"0000320193",
				"name":"Apple Inc.",
				"filings":{
					"recent":{
						"accessionNumber":["0000320193-26-000001"],
						"form":["8-K"],
						"filingDate":["2026-06-01"],
						"acceptanceDateTime":["2026-06-01T16:30:12.000Z"],
						"primaryDocument":["aapl-20260601.htm"],
						"primaryDocDescription":["Current report"]
					},
					"files":[{"name":"CIK0000320193-submissions-001.json"}]
				}
			}`,
		},
		{name: "missing cik", query: FilingQuery{Ticker: "AAPL"}, wantErr: true},
		{name: "non success status", statusCode: http.StatusInternalServerError, body: `{}`, query: FilingQuery{Ticker: "AAPL", CIK: "320193"}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := NewHTTPClient("https://sec.test", "sec-monitor-test", time.Second)
			client.Client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				body := tt.body
				if strings.HasSuffix(r.URL.Path, "/submissions/CIK0000320193-submissions-001.json") {
					body = `{
						"accessionNumber":["0000320193-25-000001"],
						"form":["10-K"],
						"filingDate":["2025-12-31"],
						"acceptanceDateTime":["2025-12-31T09:05:06.000Z"],
						"primaryDocument":["aapl-20251231.htm"],
						"primaryDocDescription":["Annual report"]
					}`
				}
				return &http.Response{
					StatusCode: tt.statusCode,
					Body:       io.NopCloser(strings.NewReader(body)),
					Header:     make(http.Header),
				}, nil
			})}
			got, err := client.ListFilings(context.Background(), tt.query)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ListFilings expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("ListFilings: %v", err)
			}
			if len(got) != tt.wantLen {
				t.Fatalf("len = %d, want %d", len(got), tt.wantLen)
			}
			if got[0].Ticker != "AAPL" || got[0].FilingType != "8-K" || got[0].Title != "Current report" {
				t.Fatalf("mapped filing = %+v", got[0])
			}
			if got[0].PublishedAt == nil || got[0].PublishedAt.Format(time.RFC3339) != "2026-06-01T16:30:12Z" {
				t.Fatalf("PublishedAt = %v, want 2026-06-01T16:30:12Z", got[0].PublishedAt)
			}
			if tt.query.FetchFullHistory {
				if got[1].PublishedAt == nil || got[1].PublishedAt.Format(time.RFC3339) != "2025-12-31T09:05:06Z" {
					t.Fatalf("archived PublishedAt = %v, want 2025-12-31T09:05:06Z", got[1].PublishedAt)
				}
			}
		})
	}
}

func TestHTTPClientListCurrentFilingsTableDriven(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		body       string
		query      CurrentFilingQuery
		wantLen    int
		wantErr    bool
		assert     func(t *testing.T, got []CurrentFilingResult)
	}{
		{
			name:       "maps atom current filing",
			statusCode: http.StatusOK,
			query:      CurrentFilingQuery{FormTypes: []string{"S-1"}, Count: 10},
			wantLen:    1,
			body: `<feed xmlns="http://www.w3.org/2005/Atom">
				<entry>
					<title>S-1 - Acme Space Inc. (0000000001) (Filer)</title>
					<updated>2026-06-18T14:30:16-04:00</updated>
					<link href="https://www.sec.gov/Archives/edgar/data/1/000000000126000001/acme-s1.htm"/>
					<category term="S-1"/>
					<summary>CIK: 0000000001&lt;br/&gt;Accession Number: 0000000001-26-000001&lt;br/&gt;Filing Date: 2026-06-18</summary>
				</entry>
			</feed>`,
			assert: func(t *testing.T, got []CurrentFilingResult) {
				item := got[0]
				if item.FilingID != "0000000001-26-000001" || item.CIK != "0000000001" || item.CompanyName != "Acme Space Inc." || item.FilingType != "S-1" {
					t.Fatalf("mapped current filing = %+v", item)
				}
				if item.AcceptedAt == nil || item.AcceptedAt.Format(time.RFC3339) != "2026-06-18T18:30:16Z" {
					t.Fatalf("AcceptedAt = %v, want UTC timestamp", item.AcceptedAt)
				}
			},
		},
		{
			name:       "deduplicates across form queries",
			statusCode: http.StatusOK,
			query:      CurrentFilingQuery{FormTypes: []string{"S-1", "S-1/A"}, Count: 10},
			wantLen:    1,
			body:       `<feed><entry><title>S-1 - Acme Space Inc.</title><link href="https://www.sec.gov/Archives/dup.htm"/><category term="S-1"/></entry></feed>`,
		},
		{
			name:       "decodes sec legacy charset declaration",
			statusCode: http.StatusOK,
			query:      CurrentFilingQuery{FormTypes: []string{"S-1"}, Count: 10},
			wantLen:    1,
			body: `<?xml version="1.0" encoding="ISO-8859-1"?>
			<feed>
				<entry>
					<title>S-1 - Caf&#233; Robotics Inc. (0000000002) (Filer)</title>
					<link href="https://www.sec.gov/Archives/edgar/data/2/000000000226000001/cafe-s1.htm"/>
					<category term="S-1"/>
					<summary>CIK: 0000000002&lt;br/&gt;Accession Number: 0000000002-26-000001&lt;br/&gt;Filing Date: 2026-06-18</summary>
				</entry>
			</feed>`,
			assert: func(t *testing.T, got []CurrentFilingResult) {
				if got[0].CompanyName != "Café Robotics Inc." {
					t.Fatalf("CompanyName = %q, want decoded charset text", got[0].CompanyName)
				}
			},
		},
		{name: "non success status returns error", statusCode: http.StatusTooManyRequests, body: `<feed/>`, query: CurrentFilingQuery{FormTypes: []string{"S-1"}}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := NewHTTPClient("https://sec.test", "sec-monitor-test", time.Second)
			client.CurrentFilingsURL = "https://sec.test/cgi-bin/browse-edgar"
			client.Client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				if r.URL.Query().Get("output") != "atom" || r.URL.Query().Get("action") != "getcurrent" {
					t.Fatalf("unexpected query = %s", r.URL.RawQuery)
				}
				return &http.Response{
					StatusCode: tt.statusCode,
					Body:       io.NopCloser(strings.NewReader(tt.body)),
					Header:     make(http.Header),
				}, nil
			})}
			got, err := client.ListCurrentFilings(context.Background(), tt.query)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ListCurrentFilings expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("ListCurrentFilings: %v", err)
			}
			if len(got) != tt.wantLen {
				t.Fatalf("len = %d, want %d", len(got), tt.wantLen)
			}
			if tt.assert != nil {
				tt.assert(t, got)
			}
		})
	}
}
