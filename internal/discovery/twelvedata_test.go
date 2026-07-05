package discovery

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestTwelveDataPriceProviderLoadsRequestedEffectiveDate(t *testing.T) {
	var requestedPath, requestedQuery string
	client := httpClientForHandler(func(req *http.Request) (*http.Response, error) {
		requestedPath = req.URL.Path
		requestedQuery = req.URL.RawQuery
		if got := req.URL.Query().Get("apikey"); got != "test-key" {
			t.Fatalf("apikey = %q", got)
		}
		return textResponse(http.StatusOK, `{"meta":{"symbol":"ABSI"},"values":[{"datetime":"2026-06-30","open":"1.00","high":"2.00","low":"0.50","close":"1.50","volume":"12345"}],"status":"ok"}`), nil
	})
	provider, err := NewTwelveDataPriceProvider(TwelveDataPriceProviderOptions{
		APIKey:        "test-key",
		BaseURL:       "https://api.twelvedata.com",
		Client:        client,
		Calendar:      &stubMarketCalendar{},
		Now:           func() time.Time { return time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC) },
		RequestBudget: 10,
	})
	if err != nil {
		t.Fatal(err)
	}

	records, result, err := provider.LoadForDate(context.Background(), []Listing{{Ticker: "ABSI"}}, "2026-06-30")
	if err != nil {
		t.Fatalf("LoadForDate() error = %v", err)
	}
	if requestedPath != "/time_series" || !strings.Contains(requestedQuery, "interval=1day") || !strings.Contains(requestedQuery, "symbol=ABSI") {
		t.Fatalf("request path=%q query=%q", requestedPath, requestedQuery)
	}
	if len(records) != 1 || records[0].Symbol != "ABSI" || records[0].Source != "twelvedata" || records[0].CloseMicros != 1_500_000 || records[0].Volume != 12345 {
		t.Fatalf("records=%#v", records)
	}
	if result.Provider != "twelvedata" || result.EffectiveDate.Format(time.DateOnly) != "2026-06-30" || result.SourceVersion != "twelvedata:2026-06-30" || result.CoveragePct != 100 {
		t.Fatalf("result=%#v", result)
	}
}

func TestTwelveDataPriceProviderHonorsRequestBudget(t *testing.T) {
	requests := 0
	client := httpClientForHandler(func(req *http.Request) (*http.Response, error) {
		requests++
		return textResponse(http.StatusOK, `{"values":[{"datetime":"2026-06-30","open":"1","high":"1","low":"1","close":"1","volume":"1"}],"status":"ok"}`), nil
	})
	provider, err := NewTwelveDataPriceProvider(TwelveDataPriceProviderOptions{
		APIKey:        "test-key",
		BaseURL:       "https://api.twelvedata.com",
		Client:        client,
		Calendar:      &stubMarketCalendar{},
		Now:           func() time.Time { return time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC) },
		RequestBudget: 1,
	})
	if err != nil {
		t.Fatal(err)
	}

	records, result, err := provider.LoadForDate(context.Background(), []Listing{{Ticker: "ONE"}, {Ticker: "TWO"}}, "2026-06-30")
	if err != nil {
		t.Fatalf("LoadForDate() error = %v", err)
	}
	if requests != 1 || len(records) != 1 || result.Expected != 2 || result.CoveragePct != 50 {
		t.Fatalf("requests=%d records=%#v result=%#v", requests, records, result)
	}
}

func TestTwelveDataPriceProviderStopsOnRateLimitWithPartialRecords(t *testing.T) {
	requests := 0
	client := httpClientForHandler(func(req *http.Request) (*http.Response, error) {
		requests++
		if requests == 1 {
			return textResponse(http.StatusOK, `{"values":[{"datetime":"2026-06-30","open":"1","high":"1","low":"1","close":"1","volume":"1"}],"status":"ok"}`), nil
		}
		return textResponse(http.StatusOK, `{"code":429,"message":"API credits exceeded","status":"error"}`), nil
	})
	provider, err := NewTwelveDataPriceProvider(TwelveDataPriceProviderOptions{
		APIKey:   "test-key",
		BaseURL:  "https://api.twelvedata.com",
		Client:   client,
		Calendar: &stubMarketCalendar{},
		Now:      func() time.Time { return time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatal(err)
	}

	records, result, err := provider.LoadForDate(context.Background(), []Listing{{Ticker: "ONE"}, {Ticker: "TWO"}, {Ticker: "THREE"}}, "2026-06-30")
	if err != nil {
		t.Fatalf("LoadForDate() error = %v", err)
	}
	if requests != 2 || len(records) != 1 || result.CoveragePct < 33 || result.CoveragePct > 34 {
		t.Fatalf("requests=%d records=%#v result=%#v", requests, records, result)
	}
}

func TestTwelveDataPriceProviderCachesParsedRecords(t *testing.T) {
	requests := 0
	client := httpClientForHandler(func(req *http.Request) (*http.Response, error) {
		requests++
		return textResponse(http.StatusOK, `{"values":[{"datetime":"2026-06-30","open":"1","high":"2","low":"1","close":"1.5","volume":"10"}],"status":"ok"}`), nil
	})
	provider, err := NewTwelveDataPriceProvider(TwelveDataPriceProviderOptions{
		APIKey:   "test-key",
		BaseURL:  "https://api.twelvedata.com",
		Client:   client,
		Calendar: &stubMarketCalendar{},
		CacheDir: t.TempDir(),
		Now:      func() time.Time { return time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 2; i++ {
		records, _, loadErr := provider.LoadForDate(context.Background(), []Listing{{Ticker: "ABSI"}}, "2026-06-30")
		if loadErr != nil {
			t.Fatalf("LoadForDate run %d error = %v", i+1, loadErr)
		}
		if len(records) != 1 || records[0].Symbol != "ABSI" {
			t.Fatalf("records run %d = %+v", i+1, records)
		}
	}
	if requests != 1 {
		t.Fatalf("http requests = %d, want cached second load", requests)
	}
}

func TestTwelveDataPriceProviderSkipsInvalidRecordAndContinues(t *testing.T) {
	requests := 0
	client := httpClientForHandler(func(req *http.Request) (*http.Response, error) {
		requests++
		if requests == 1 {
			return textResponse(http.StatusOK, `{"values":[{"datetime":"2026-06-30","open":"bad","high":"2","low":"1","close":"1.5","volume":"10"}],"status":"ok"}`), nil
		}
		return textResponse(http.StatusOK, `{"values":[{"datetime":"2026-06-30","open":"1","high":"2","low":"1","close":"1.5","volume":"10"}],"status":"ok"}`), nil
	})
	provider, err := NewTwelveDataPriceProvider(TwelveDataPriceProviderOptions{
		APIKey:        "test-key",
		BaseURL:       "https://api.twelvedata.com",
		Client:        client,
		Calendar:      &stubMarketCalendar{},
		Now:           func() time.Time { return time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC) },
		RequestBudget: 2,
	})
	if err != nil {
		t.Fatal(err)
	}

	records, result, err := provider.LoadForDate(context.Background(), []Listing{{Ticker: "BAD"}, {Ticker: "OK"}}, "2026-06-30")
	if err != nil {
		t.Fatalf("LoadForDate() error = %v", err)
	}
	if requests != 2 || len(records) != 1 || records[0].Symbol != "OK" || result.Expected != 2 || result.Records != 1 {
		t.Fatalf("requests=%d records=%#v result=%#v", requests, records, result)
	}
}
