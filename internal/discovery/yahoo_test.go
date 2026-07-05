package discovery

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestYahooPriceProviderLoadsRequestedEffectiveDate(t *testing.T) {
	var requestedPath, requestedQuery string
	client := httpClientForHandler(func(req *http.Request) (*http.Response, error) {
		requestedPath = req.URL.Path
		requestedQuery = req.URL.RawQuery
		return textResponse(http.StatusOK, `{"chart":{"result":[{"timestamp":[1782777600],"indicators":{"quote":[{"open":[1.0],"high":[2.0],"low":[0.5],"close":[1.5],"volume":[12345]}]}}],"error":null}}`), nil
	})
	provider, err := NewYahooPriceProvider(YahooPriceProviderOptions{
		BaseURL:       "https://query1.finance.yahoo.com",
		Client:        client,
		Calendar:      &stubMarketCalendar{},
		Now:           func() time.Time { return time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC) },
		RequestBudget: 10,
	})
	if err != nil {
		t.Fatal(err)
	}

	records, result, err := provider.LoadForDate(context.Background(), []Listing{{Ticker: "BRK.B", ProviderTicker: "BRK-B"}}, "2026-06-30")
	if err != nil {
		t.Fatalf("LoadForDate() error = %v", err)
	}
	if requestedPath != "/v8/finance/chart/BRK-B" || !strings.Contains(requestedQuery, "interval=1d") {
		t.Fatalf("request path=%q query=%q", requestedPath, requestedQuery)
	}
	if len(records) != 1 || records[0].Symbol != "BRK.B" || records[0].Source != "yahoo" || records[0].CloseMicros != 1_500_000 || records[0].Volume != 12345 {
		t.Fatalf("records=%#v", records)
	}
	if result.Provider != "yahoo" || result.EffectiveDate.Format(time.DateOnly) != "2026-06-30" || result.SourceVersion != "yahoo:2026-06-30" || result.CoveragePct != 100 {
		t.Fatalf("result=%#v", result)
	}
}

func TestYahooPriceProviderHonorsRequestBudget(t *testing.T) {
	requests := 0
	client := httpClientForHandler(func(req *http.Request) (*http.Response, error) {
		requests++
		return textResponse(http.StatusOK, `{"chart":{"result":[{"timestamp":[1782777600],"indicators":{"quote":[{"open":[1],"high":[1],"low":[1],"close":[1],"volume":[1]}]}}],"error":null}}`), nil
	})
	provider, err := NewYahooPriceProvider(YahooPriceProviderOptions{
		BaseURL:       "https://query1.finance.yahoo.com",
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
