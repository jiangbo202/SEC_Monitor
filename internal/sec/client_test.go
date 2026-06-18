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
