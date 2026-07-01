package discovery

import (
	"context"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
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
			return textResponse(http.StatusNotFound, `not found`), nil
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

func TestTiingoPriceProviderLoadsRequestedEffectiveDate(t *testing.T) {
	client := httpClientForHandler(func(req *http.Request) (*http.Response, error) {
		if got := req.URL.Query().Get("endDate"); got != "2026-06-30" {
			t.Fatalf("endDate = %q, want requested effective date", got)
		}
		body := `[{"date":"2026-06-30","open":1,"high":2,"low":1,"close":1.5,"volume":10},{"date":"2026-07-01","open":2,"high":3,"low":2,"close":2.5,"volume":20}]`
		return textResponse(http.StatusOK, body), nil
	})
	provider, err := NewTiingoPriceProvider(TiingoPriceProviderOptions{
		Token:    "test-token",
		BaseURL:  "https://tiingo.example.test",
		Client:   client,
		Now:      func() time.Time { return time.Date(2026, 7, 1, 16, 0, 0, 0, time.UTC) },
		Calendar: &stubMarketCalendar{},
	})
	if err != nil {
		t.Fatal(err)
	}

	records, result, err := provider.LoadForDate(context.Background(), []Listing{{Ticker: "OK"}}, "2026-06-30")
	if err != nil {
		t.Fatalf("LoadForDate() error = %v", err)
	}
	if len(records) != 1 || records[0].TradeDate.Format(time.DateOnly) != "2026-06-30" {
		t.Fatalf("records = %+v", records)
	}
	if result.EffectiveDate.Format(time.DateOnly) != "2026-06-30" || result.SourceVersion != "tiingo:2026-06-30" {
		t.Fatalf("result = %+v", result)
	}
}

func TestTiingoPriceProviderFallsBackToPreviousCloseForRequestedEffectiveDate(t *testing.T) {
	client := httpClientForHandler(func(req *http.Request) (*http.Response, error) {
		if got := req.URL.Query().Get("endDate"); got != "2026-06-30" {
			t.Fatalf("endDate = %q, want requested effective date", got)
		}
		body := `[{"date":"2026-06-29","open":1,"high":2,"low":1,"close":1.5,"volume":10}]`
		return textResponse(http.StatusOK, body), nil
	})
	provider, err := NewTiingoPriceProvider(TiingoPriceProviderOptions{
		Token:    "test-token",
		BaseURL:  "https://tiingo.example.test",
		Client:   client,
		Now:      func() time.Time { return time.Date(2026, 6, 30, 13, 15, 0, 0, time.FixedZone("EDT", -4*60*60)) },
		Calendar: &stubMarketCalendar{},
	})
	if err != nil {
		t.Fatal(err)
	}

	records, result, err := provider.LoadForDate(context.Background(), []Listing{{Ticker: "OK"}}, "2026-06-30")
	if err != nil {
		t.Fatalf("LoadForDate() error = %v", err)
	}
	if len(records) != 1 || records[0].TradeDate.Format(time.DateOnly) != "2026-06-29" {
		t.Fatalf("records = %+v", records)
	}
	if result.EffectiveDate.Format(time.DateOnly) != "2026-06-30" || result.SourceVersion != "tiingo:2026-06-29" || !result.Timely {
		t.Fatalf("result = %+v", result)
	}
}

func TestTiingoPriceProviderReportsProgress(t *testing.T) {
	var progress []TiingoProgress
	client := httpClientForHandler(func(req *http.Request) (*http.Response, error) {
		return textResponse(http.StatusOK, `[{"date":"2026-06-30","open":1,"high":2,"low":1,"close":1.5,"volume":10}]`), nil
	})
	provider, err := NewTiingoPriceProvider(TiingoPriceProviderOptions{
		Token:         "test-token",
		BaseURL:       "https://tiingo.example.test",
		Client:        client,
		Now:           func() time.Time { return time.Date(2026, 6, 30, 16, 0, 0, 0, time.UTC) },
		Calendar:      &stubMarketCalendar{},
		ProgressEvery: 1,
		Progress: func(update TiingoProgress) {
			progress = append(progress, update)
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, _, err := provider.LoadForDate(context.Background(), []Listing{{Ticker: "A"}, {Ticker: "B"}}, "2026-06-30"); err != nil {
		t.Fatalf("LoadForDate() error = %v", err)
	}
	if len(progress) != 2 {
		t.Fatalf("progress updates = %+v", progress)
	}
	last := progress[len(progress)-1]
	if last.Total != 2 || last.Processed != 2 || last.Records != 2 || last.Skipped != 0 || last.Provider != "tiingo" {
		t.Fatalf("last progress = %+v", last)
	}
}

func TestTiingoPriceProviderHonorsConcurrencyLimit(t *testing.T) {
	var mu sync.Mutex
	active := 0
	maxActive := 0
	release := make(chan struct{})
	client := httpClientForHandler(func(req *http.Request) (*http.Response, error) {
		mu.Lock()
		active++
		if active > maxActive {
			maxActive = active
		}
		mu.Unlock()
		<-release
		mu.Lock()
		active--
		mu.Unlock()
		return textResponse(http.StatusOK, `[{"date":"2026-06-30","open":1,"high":2,"low":1,"close":1.5,"volume":10}]`), nil
	})
	provider, err := NewTiingoPriceProvider(TiingoPriceProviderOptions{
		Token:       "test-token",
		BaseURL:     "https://tiingo.example.test",
		Client:      client,
		Now:         func() time.Time { return time.Date(2026, 6, 30, 16, 0, 0, 0, time.UTC) },
		Calendar:    &stubMarketCalendar{},
		Concurrency: 2,
	})
	if err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() {
		_, _, err := provider.LoadForDate(context.Background(), []Listing{{Ticker: "A"}, {Ticker: "B"}, {Ticker: "C"}, {Ticker: "D"}}, "2026-06-30")
		done <- err
	}()
	for i := 0; i < 4; i++ {
		release <- struct{}{}
	}
	if err := <-done; err != nil {
		t.Fatalf("LoadForDate() error = %v", err)
	}
	if maxActive > 2 {
		t.Fatalf("max active requests = %d, want <= 2", maxActive)
	}
}

func TestTiingoPriceProviderStopsOnRateLimit(t *testing.T) {
	client := httpClientForHandler(func(req *http.Request) (*http.Response, error) {
		return textResponse(http.StatusTooManyRequests, `rate limited`), nil
	})
	provider, err := NewTiingoPriceProvider(TiingoPriceProviderOptions{
		Token:    "test-token",
		BaseURL:  "https://tiingo.example.test",
		Client:   client,
		Now:      func() time.Time { return time.Date(2026, 6, 30, 16, 0, 0, 0, time.UTC) },
		Calendar: &stubMarketCalendar{},
	})
	if err != nil {
		t.Fatal(err)
	}

	_, _, err = provider.LoadForDate(context.Background(), []Listing{{Ticker: "A"}}, "2026-06-30")
	if err == nil || !strings.Contains(err.Error(), "rate_limited") {
		t.Fatalf("err = %v, want rate limit error", err)
	}
}

func TestTiingoPriceProviderHonorsRequestBudget(t *testing.T) {
	var mu sync.Mutex
	requests := 0
	client := httpClientForHandler(func(req *http.Request) (*http.Response, error) {
		mu.Lock()
		requests++
		mu.Unlock()
		return textResponse(http.StatusOK, `[{"date":"2026-06-30","open":1,"high":2,"low":1,"close":1.5,"volume":10}]`), nil
	})
	provider, err := NewTiingoPriceProvider(TiingoPriceProviderOptions{
		Token:         "test-token",
		BaseURL:       "https://tiingo.example.test",
		Client:        client,
		Now:           func() time.Time { return time.Date(2026, 6, 30, 16, 0, 0, 0, time.UTC) },
		Calendar:      &stubMarketCalendar{},
		RequestBudget: 2,
	})
	if err != nil {
		t.Fatal(err)
	}

	records, result, err := provider.LoadForDate(context.Background(), []Listing{{Ticker: "A"}, {Ticker: "B"}, {Ticker: "C"}}, "2026-06-30")
	if err != nil {
		t.Fatalf("LoadForDate() error = %v", err)
	}
	if requests != 2 {
		t.Fatalf("http requests = %d, want 2", requests)
	}
	if len(records) != 2 || result.Expected != 3 || result.Records != 2 {
		t.Fatalf("records=%+v result=%+v", records, result)
	}
}

func TestTiingoPriceProviderRotatesTokensAfterRateLimit(t *testing.T) {
	var tokens []string
	client := httpClientForHandler(func(req *http.Request) (*http.Response, error) {
		token := strings.TrimPrefix(req.Header.Get("Authorization"), "Token ")
		tokens = append(tokens, token)
		if token == "limited-token" {
			return textResponse(http.StatusTooManyRequests, `rate limited`), nil
		}
		return textResponse(http.StatusOK, `[{"date":"2026-06-30","open":1,"high":2,"low":1,"close":1.5,"volume":10}]`), nil
	})
	provider, err := NewTiingoPriceProvider(TiingoPriceProviderOptions{
		Tokens:   []string{"limited-token", "fresh-token"},
		BaseURL:  "https://tiingo.example.test",
		Client:   client,
		Now:      func() time.Time { return time.Date(2026, 6, 30, 16, 0, 0, 0, time.UTC) },
		Calendar: &stubMarketCalendar{},
	})
	if err != nil {
		t.Fatal(err)
	}

	records, _, err := provider.LoadForDate(context.Background(), []Listing{{Ticker: "A"}}, "2026-06-30")
	if err != nil {
		t.Fatalf("LoadForDate() error = %v", err)
	}
	if len(records) != 1 || records[0].Symbol != "A" {
		t.Fatalf("records = %+v", records)
	}
	if strings.Join(tokens, ",") != "limited-token,fresh-token" {
		t.Fatalf("tokens = %#v", tokens)
	}
}

func TestTiingoPriceProviderReturnsCachedRecordsWhenUncachedSymbolsRateLimit(t *testing.T) {
	requests := 0
	client := httpClientForHandler(func(req *http.Request) (*http.Response, error) {
		requests++
		return textResponse(http.StatusOK, `[{"date":"2026-06-30","open":1,"high":2,"low":1,"close":1.5,"volume":10}]`), nil
	})
	provider, err := NewTiingoPriceProvider(TiingoPriceProviderOptions{
		Token:    "test-token",
		BaseURL:  "https://tiingo.example.test",
		Client:   client,
		Now:      func() time.Time { return time.Date(2026, 6, 30, 16, 0, 0, 0, time.UTC) },
		Calendar: &stubMarketCalendar{},
		CacheDir: filepath.Join(t.TempDir(), "tiingo-cache"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := provider.LoadForDate(context.Background(), []Listing{{Ticker: "A"}}, "2026-06-30"); err != nil {
		t.Fatalf("prime cache: %v", err)
	}
	provider.options.Client = httpClientForHandler(func(req *http.Request) (*http.Response, error) {
		requests++
		return textResponse(http.StatusTooManyRequests, `rate limited`), nil
	})

	records, result, err := provider.LoadForDate(context.Background(), []Listing{{Ticker: "A"}, {Ticker: "B"}}, "2026-06-30")
	if err != nil {
		t.Fatalf("LoadForDate() error = %v", err)
	}
	if len(records) != 1 || records[0].Symbol != "A" {
		t.Fatalf("records = %+v", records)
	}
	if result.Expected != 2 || result.Records != 1 {
		t.Fatalf("result = %+v", result)
	}
	if requests != 2 {
		t.Fatalf("http requests = %d, want one cache-prime request and one rate-limited request", requests)
	}
}

func TestTiingoPriceProviderCachesParsedRecords(t *testing.T) {
	requests := 0
	client := httpClientForHandler(func(req *http.Request) (*http.Response, error) {
		requests++
		return textResponse(http.StatusOK, `[{"date":"2026-06-30","open":1,"high":2,"low":1,"close":1.5,"volume":10}]`), nil
	})
	provider, err := NewTiingoPriceProvider(TiingoPriceProviderOptions{
		Token:    "test-token",
		BaseURL:  "https://tiingo.example.test",
		Client:   client,
		Now:      func() time.Time { return time.Date(2026, 6, 30, 16, 0, 0, 0, time.UTC) },
		Calendar: &stubMarketCalendar{},
		CacheDir: filepath.Join(t.TempDir(), "tiingo-cache"),
	})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		records, _, loadErr := provider.LoadForDate(context.Background(), []Listing{{Ticker: "A"}}, "2026-06-30")
		if loadErr != nil {
			t.Fatalf("LoadForDate run %d error = %v", i+1, loadErr)
		}
		if len(records) != 1 || records[0].Symbol != "A" {
			t.Fatalf("records run %d = %+v", i+1, records)
		}
	}
	if requests != 1 {
		t.Fatalf("http requests = %d, want cached second load", requests)
	}
}

func TestTiingoPriceProviderThrottlesRequests(t *testing.T) {
	var mu sync.Mutex
	var seen []time.Time
	client := httpClientForHandler(func(req *http.Request) (*http.Response, error) {
		mu.Lock()
		seen = append(seen, time.Now())
		mu.Unlock()
		return textResponse(http.StatusOK, `[{"date":"2026-06-30","open":1,"high":2,"low":1,"close":1.5,"volume":10}]`), nil
	})
	provider, err := NewTiingoPriceProvider(TiingoPriceProviderOptions{
		Token:           "test-token",
		BaseURL:         "https://tiingo.example.test",
		Client:          client,
		Now:             func() time.Time { return time.Date(2026, 6, 30, 16, 0, 0, 0, time.UTC) },
		Calendar:        &stubMarketCalendar{},
		Concurrency:     2,
		RequestInterval: 25 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, _, err := provider.LoadForDate(context.Background(), []Listing{{Ticker: "A"}, {Ticker: "B"}, {Ticker: "C"}}, "2026-06-30"); err != nil {
		t.Fatalf("LoadForDate() error = %v", err)
	}
	if len(seen) != 3 {
		t.Fatalf("request times = %+v", seen)
	}
	for i := 1; i < len(seen); i++ {
		if delta := seen[i].Sub(seen[i-1]); delta < 20*time.Millisecond {
			t.Fatalf("request %d spacing = %s, want throttled", i, delta)
		}
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
