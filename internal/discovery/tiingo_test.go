package discovery

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestTiingoPriceProviderLoadsLatestUnadjustedPrices(t *testing.T) {
	requests := make(map[string]string)
	client := httpClientForHandler(func(req *http.Request) (*http.Response, error) {
		if got := req.Header.Get("Authorization"); got != "Token test-token" {
			t.Fatalf("authorization header = %q", got)
		}
		requests[req.URL.Path] = req.URL.RawQuery
		body := `[{"date":"2026-06-17T00:00:00.000Z","open":9,"high":10,"low":8,"close":9.5,"volume":90},{"date":"2026-06-18T00:00:00.000Z","open":10.000001,"high":11,"low":9,"close":10.25,"volume":1234}]`
		return textResponse(http.StatusOK, body), nil
	})
	provider, err := NewTiingoPriceProvider(TiingoPriceProviderOptions{
		Token:    "test-token",
		BaseURL:  "https://tiingo.example.test",
		Client:   client,
		Now:      func() time.Time { return time.Date(2026, 6, 18, 16, 0, 0, 0, time.UTC) },
		Calendar: &stubMarketCalendar{holidays: map[string]bool{"2026-06-19": true}},
	})
	if err != nil {
		t.Fatal(err)
	}

	records, result, err := provider.Load(context.Background(), []Listing{{Ticker: "BRK.B", ProviderTicker: "BRK-B"}, {Ticker: "PER"}})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("records = %+v", records)
	}
	if records[0].Symbol != "BRK.B" || records[0].CloseMicros != 10_250_000 || records[0].OpenMicros != 10_000_001 || records[0].Volume != 1234 || records[0].Source != "tiingo" {
		t.Fatalf("first record = %+v", records[0])
	}
	if result.Provider != "tiingo" || result.Records != 2 || result.Expected != 2 || result.CoveragePct != 100 || result.SourceVersion == "" || result.SHA256 == "" {
		t.Fatalf("result = %+v", result)
	}
	if _, ok := requests["/tiingo/daily/brk-b/prices"]; !ok {
		t.Fatalf("missing BRK-B request paths: %#v", requests)
	}
}

func TestTiingoPriceProviderSkipsFailedOrStaleSymbols(t *testing.T) {
	client := httpClientForHandler(func(req *http.Request) (*http.Response, error) {
		switch {
		case strings.Contains(req.URL.Path, "/ok/"):
			return textResponse(http.StatusOK, `[{"date":"2026-06-18","open":1,"high":2,"low":1,"close":1.5,"volume":10}]`), nil
		case strings.Contains(req.URL.Path, "/stale/"):
			return textResponse(http.StatusOK, `[{"date":"2026-06-17","open":1,"high":2,"low":1,"close":1.5,"volume":10}]`), nil
		default:
			return textResponse(http.StatusTooManyRequests, `rate limited`), nil
		}
	})
	provider, err := NewTiingoPriceProvider(TiingoPriceProviderOptions{
		Token:    "test-token",
		BaseURL:  "https://tiingo.example.test",
		Client:   client,
		Now:      func() time.Time { return time.Date(2026, 6, 18, 16, 0, 0, 0, time.UTC) },
		Calendar: &stubMarketCalendar{},
	})
	if err != nil {
		t.Fatal(err)
	}

	records, result, err := provider.Load(context.Background(), []Listing{{Ticker: "OK"}, {Ticker: "STALE"}, {Ticker: "FAIL"}})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(records) != 1 || records[0].Symbol != "OK" {
		t.Fatalf("records = %+v", records)
	}
	if result.Records != 1 || result.Expected != 3 || result.CoveragePct < 33 || result.CoveragePct > 34 {
		t.Fatalf("result = %+v", result)
	}
}

func TestTiingoPriceProviderRequiresToken(t *testing.T) {
	if _, err := NewTiingoPriceProvider(TiingoPriceProviderOptions{BaseURL: "https://tiingo.example.test", Client: http.DefaultClient, Calendar: &stubMarketCalendar{}}); err == nil || !strings.Contains(err.Error(), "token") {
		t.Fatalf("err = %v, want token error", err)
	}
}

func httpClientForHandler(handler func(*http.Request) (*http.Response, error)) *http.Client {
	return &http.Client{Transport: roundTripFunc(handler)}
}

func textResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Status:     http.StatusText(status),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}
