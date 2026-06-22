package discovery

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

const normalizedPrices = `symbol,trade_date,open,high,low,close,volume,currency,is_adjusted
BRK.B,2026-06-18,500.000001,501,499.5,500.123456,1000,USD,false
PER,2026-06-18,10,11,9,10.25,2000,USD,false
`

func TestParsePricesNormalizedAndExactMicros(t *testing.T) {
	opts := marketValidationOptions(t, []Listing{{Ticker: "BRK.B", ProviderTicker: "BRK-B"}, {Ticker: "PER"}})
	records, result, err := ParsePriceCSV(context.Background(), strings.NewReader(normalizedPrices), PriceFormatNormalized, opts)
	if err != nil {
		t.Fatalf("ParsePriceCSV() error = %v", err)
	}
	if len(records) != 2 || records[0].Symbol != "BRK.B" || records[0].OpenMicros != 500000001 || records[0].CloseMicros != 500123456 {
		t.Fatalf("records = %+v", records)
	}
	if records[1].Symbol != "PER" || records[1].CloseMicros != 10250000 {
		t.Fatalf("PER daily record = %+v", records[1])
	}
	if records[0].TradeDate.Location().String() != "America/New_York" || records[0].TradeDate.Format(time.DateOnly) != "2026-06-18" {
		t.Fatalf("TradeDate = %v (%s)", records[0].TradeDate, records[0].TradeDate.Location())
	}
	wantHash := sha256.Sum256([]byte(normalizedPrices))
	if result.SHA256 != hex.EncodeToString(wantHash[:]) || result.SourceVersion == "" {
		t.Fatalf("result provenance = %+v", result)
	}
	if result.Records != 2 || result.Expected != 2 || result.CoveragePct != 100 || !result.Timely {
		t.Fatalf("result metrics = %+v", result)
	}
}

func TestParsePricesStooqPlainAndZIP(t *testing.T) {
	stooq := "<TICKER>,<PER>,<DATE>,<OPEN>,<HIGH>,<LOW>,<CLOSE>,<VOL>\nBRK-B.US,D,20260618,500.1,501,499,500.5,42\nPER.US,D,20260618,10,11,9,10.5,99\n"
	opts := marketValidationOptions(t, []Listing{{Ticker: "BRK.B", ProviderTicker: "BRK-B.US"}, {Ticker: "PER", ProviderTicker: "PER.US"}})

	for _, test := range []struct {
		name   string
		input  io.Reader
		format PriceFormat
	}{
		{name: "plain", input: strings.NewReader(stooq), format: PriceFormatStooq},
		{name: "zip", input: bytes.NewReader(zipPayload(t, "prices.csv", stooq)), format: PriceFormatStooqZIP},
	} {
		t.Run(test.name, func(t *testing.T) {
			records, _, err := ParsePriceCSV(context.Background(), test.input, test.format, opts)
			if err != nil {
				t.Fatalf("ParsePriceCSV() error = %v", err)
			}
			if len(records) != 2 || records[0].Symbol != "BRK.B" || records[1].Symbol != "PER" {
				t.Fatalf("records = %+v", records)
			}
		})
	}
}

func TestParsePricesStooqStandardASCIIColumns(t *testing.T) {
	input := "<TICKER>,<PER>,<DATE>,<TIME>,<OPEN>,<HIGH>,<LOW>,<CLOSE>,<VOL>,<OPENINT>\nBRK-B.US,D,20260618,000000,500.1,501,499,500.5,42,0\n"
	opts := marketValidationOptions(t, []Listing{{Ticker: "BRK.B", ProviderTicker: "BRK-B.US"}})
	records, _, err := ParsePriceCSV(context.Background(), strings.NewReader(input), PriceFormatStooq, opts)
	if err != nil {
		t.Fatalf("ParsePriceCSV() error = %v", err)
	}
	if len(records) != 1 || records[0].Symbol != "BRK.B" || records[0].CloseMicros != 500500000 {
		t.Fatalf("records = %+v", records)
	}
}

func TestPriceValidationRejectsInvalidRows(t *testing.T) {
	header := "symbol,trade_date,open,high,low,close,volume,currency,is_adjusted\n"
	tests := []struct {
		name string
		rows string
		want string
	}{
		{"adjusted", "ACME,2026-06-18,1,2,1,1,1,USD,true\n", "adjusted"},
		{"non USD", "ACME,2026-06-18,1,2,1,1,1,EUR,false\n", "USD"},
		{"negative volume", "ACME,2026-06-18,1,2,1,1,-1,USD,false\n", "volume"},
		{"zero price", "ACME,2026-06-18,0,2,1,1,1,USD,false\n", "positive"},
		{"invalid OHLC", "ACME,2026-06-18,3,2,1,1,1,USD,false\n", "OHLC"},
		{"duplicate", "ACME,2026-06-18,1,2,1,1,1,USD,false\nACME,2026-06-18,1,2,1,1,1,USD,false\n", "duplicate"},
		{"invalid date", "ACME,2026-02-30,1,2,1,1,1,USD,false\n", "date"},
		{"non trading", "ACME,2026-06-19,1,2,1,1,1,USD,false\n", "trading"},
		{"future", "ACME,2026-06-22,1,2,1,1,1,USD,false\n", "future"},
		{"stale", "ACME,2026-06-17,1,2,1,1,1,USD,false\n", "stale"},
		{"too precise", "ACME,2026-06-18,1.0000001,2,1,1,1,USD,false\n", "precision"},
		{"overflow", "ACME,2026-06-18,9223372036854,9223372036854,9223372036854,9223372036854,1,USD,false\n", "range"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, _, err := ParsePriceCSV(context.Background(), strings.NewReader(header+test.rows), PriceFormatNormalized, marketValidationOptions(t, []Listing{{Ticker: "ACME"}}))
			if err == nil || !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(test.want)) {
				t.Fatalf("ParsePriceCSV() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestPriceValidationCoverageUsesUniqueExpectedListings(t *testing.T) {
	opts := marketValidationOptions(t, []Listing{{Ticker: "ACME"}, {Ticker: "ACME"}, {Ticker: "OTHER"}})
	input := "symbol,trade_date,open,high,low,close,volume,currency,is_adjusted\nACME,2026-06-18,1,2,1,1,1,USD,false\n"
	_, result, err := ParsePriceCSV(context.Background(), strings.NewReader(input), PriceFormatNormalized, opts)
	if err != nil {
		t.Fatal(err)
	}
	if result.Expected != 2 || result.CoveragePct != 50 {
		t.Fatalf("result = %+v", result)
	}

	opts = marketValidationOptions(t, nil)
	_, result, err = ParsePriceCSV(context.Background(), strings.NewReader(input), PriceFormatNormalized, opts)
	if err != nil || result.Expected != 0 || result.CoveragePct != 100 {
		t.Fatalf("zero expected result = %+v, error = %v", result, err)
	}
}

func TestPriceValidationTimelinessBoundaryAndDST(t *testing.T) {
	for _, test := range []struct {
		name   string
		now    time.Time
		timely bool
	}{
		{"at boundary", time.Date(2026, 6, 19, 12, 0, 0, 0, mustNY(t)), true},
		{"after boundary", time.Date(2026, 6, 19, 12, 0, 0, 1, mustNY(t)), false},
		{"DST instant before", time.Date(2026, 3, 10, 15, 59, 0, 0, time.UTC), true},
		{"DST instant after", time.Date(2026, 3, 10, 16, 0, 1, 0, time.UTC), false},
	} {
		t.Run(test.name, func(t *testing.T) {
			date := "2026-06-18"
			if strings.HasPrefix(test.name, "DST") {
				date = "2026-03-09"
			}
			input := "symbol,trade_date,open,high,low,close,volume,currency,is_adjusted\nACME," + date + ",1,2,1,1,1,USD,false\n"
			opts := marketValidationOptions(t, []Listing{{Ticker: "ACME"}})
			opts.Now = test.now
			opts.EffectiveDate = civilDate(t, date)
			_, result, err := ParsePriceCSV(context.Background(), strings.NewReader(input), PriceFormatNormalized, opts)
			if err != nil {
				t.Fatal(err)
			}
			if result.Timely != test.timely {
				t.Fatalf("Timely = %v, want %v", result.Timely, test.timely)
			}
		})
	}
}

func TestPriceValidationTreatsEffectiveDateAsCivilDate(t *testing.T) {
	input := "symbol,trade_date,open,high,low,close,volume,currency,is_adjusted\nACME,2026-06-18,1,2,1,1,1,USD,false\n"
	opts := marketValidationOptions(t, []Listing{{Ticker: "ACME"}})
	// UTC midnight represents the supplied civil date. Converting this instant
	// to New York before extracting the date would incorrectly produce June 17.
	opts.EffectiveDate = time.Date(2026, 6, 18, 0, 0, 0, 0, time.UTC)
	_, result, err := ParsePriceCSV(context.Background(), strings.NewReader(input), PriceFormatNormalized, opts)
	if err != nil {
		t.Fatalf("ParsePriceCSV() error = %v", err)
	}
	if got := result.EffectiveDate.Format(time.DateOnly); got != "2026-06-18" {
		t.Fatalf("EffectiveDate = %s", got)
	}
}

func TestImportPriceCSVIsAtomicAndIdempotent(t *testing.T) {
	db := openMigratedTestDatabase(t)
	opts := marketValidationOptions(t, []Listing{{Ticker: "BRK.B", ProviderTicker: "BRK-B"}, {Ticker: "PER"}})
	if _, err := ImportPriceCSV(context.Background(), db, strings.NewReader(normalizedPrices), PriceFormatNormalized, opts); err != nil {
		t.Fatalf("ImportPriceCSV() error = %v", err)
	}
	if _, err := ImportPriceCSV(context.Background(), db, strings.NewReader(normalizedPrices), PriceFormatNormalized, opts); err != nil {
		t.Fatalf("repeat ImportPriceCSV() error = %v", err)
	}
	var count int64
	db.Model(&PriceSnapshot{}).Count(&count)
	if count != 2 {
		t.Fatalf("snapshot count = %d", count)
	}

	bad := strings.Replace(normalizedPrices, "2000,USD,false", "-1,USD,false", 1)
	if _, err := ImportPriceCSV(context.Background(), db, strings.NewReader(bad), PriceFormatNormalized, opts); err == nil {
		t.Fatal("invalid import error = nil")
	}
	db.Model(&PriceSnapshot{}).Count(&count)
	if count != 2 {
		t.Fatalf("failed import changed snapshot count to %d", count)
	}
}

func TestDownloadedPriceProviderUsesHTTPSAndVerified304Cache(t *testing.T) {
	payload := []byte(normalizedPrices)
	requests := 0
	client := &http.Client{Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		requests++
		if requests == 2 {
			if request.Header.Get("If-None-Match") != `"prices-v1"` {
				t.Errorf("If-None-Match = %q", request.Header.Get("If-None-Match"))
			}
			return &http.Response{StatusCode: http.StatusNotModified, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("")), Request: request}, nil
		}
		header := make(http.Header)
		header.Set("ETag", `"prices-v1"`)
		return &http.Response{StatusCode: http.StatusOK, Header: header, Body: io.NopCloser(bytes.NewReader(payload)), ContentLength: int64(len(payload)), Request: request}, nil
	})}
	provider, err := NewDownloadedPriceProvider(DownloadedPriceProviderOptions{
		Provider: "configured", URL: "https://prices.example.test/daily.csv", CacheKey: "daily-prices", Format: PriceFormatNormalized,
		Downloader: &Downloader{Client: client, CacheDir: t.TempDir(), MaxBytes: 1 << 20}, Validation: marketValidationOptions(t, nil),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewDownloadedPriceProvider(DownloadedPriceProviderOptions{Provider: "bad", URL: "http://example.test/prices", CacheKey: "x", Downloader: &Downloader{}}); err == nil {
		t.Fatal("HTTP provider URL accepted")
	}
	first, firstResult, err := provider.Load(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	second, secondResult, err := provider.Load(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 2 || len(second) != 2 || firstResult.SHA256 != secondResult.SHA256 || requests != 2 {
		t.Fatalf("loads = (%d, %d), results = (%+v, %+v), requests = %d", len(first), len(second), firstResult, secondResult, requests)
	}
}

func TestDownloadedPriceProviderConcurrentLoads(t *testing.T) {
	payload := []byte(normalizedPrices)
	client := &http.Client{Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(payload)), ContentLength: int64(len(payload)), Request: request}, nil
	})}
	provider, err := NewDownloadedPriceProvider(DownloadedPriceProviderOptions{
		Provider: "configured", URL: "https://prices.example.test/daily.csv", CacheKey: "daily-prices", Format: PriceFormatNormalized,
		Downloader: &Downloader{Client: client, CacheDir: t.TempDir(), MaxBytes: 1 << 20}, Validation: marketValidationOptions(t, nil),
	})
	if err != nil {
		t.Fatal(err)
	}
	var wait sync.WaitGroup
	errors := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, _, loadErr := provider.Load(context.Background(), nil)
			errors <- loadErr
		}()
	}
	wait.Wait()
	close(errors)
	for loadErr := range errors {
		if loadErr != nil {
			t.Errorf("Load() error = %v", loadErr)
		}
	}
}

func TestPriceValidationRequiresIndependentGoldProvenance(t *testing.T) {
	records := make([]PriceRecord, 100)
	gold := make([]PriceRecord, 100)
	for i := range records {
		symbol := fmt.Sprintf("S%03d", i)
		records[i] = PriceRecord{Symbol: symbol, TradeDate: civilDate(t, "2026-06-18"), CloseMicros: 1_000_000}
		gold[i] = records[i]
	}
	if _, err := CompareIndependentPrices(records, gold, GoldProvenance{}); err == nil {
		t.Fatal("gold without provenance accepted")
	}
	if _, err := CompareIndependentPrices(records[:99], gold[:99], GoldProvenance{Provider: "gold", SourceURL: "https://gold.example.test", SHA256: strings.Repeat("a", 64)}); err == nil {
		t.Fatal("fewer than 100 gold rows accepted")
	}
	gold[99].CloseMicros = 1_100_000
	errorPct, err := CompareIndependentPrices(records, gold, GoldProvenance{Provider: "gold", SourceURL: "https://gold.example.test", SHA256: strings.Repeat("a", 64)})
	if err != nil || errorPct <= 0 {
		t.Fatalf("CompareIndependentPrices() = %v, %v", errorPct, err)
	}
}

func TestProviderStateTransitions(t *testing.T) {
	state := ProviderHealth{Provider: "prices", Status: ProviderStatusValidation}
	day := civilDate(t, "2026-05-20")
	for i := 0; i < 19; i++ {
		state = AdvanceProviderHealth(state, ProviderDayResult{TradeDate: day.AddDate(0, 0, i), qualified: true})
	}
	if state.Status != ProviderStatusValidation || state.QualifiedTradingDays != 19 {
		t.Fatalf("after 19 = %+v", state)
	}
	state = AdvanceProviderHealth(state, ProviderDayResult{TradeDate: day.AddDate(0, 0, 19), qualified: true})
	if state.Status != ProviderStatusActive {
		t.Fatalf("after 20 = %+v", state)
	}
	for i := 0; i < 2; i++ {
		state = AdvanceProviderHealth(state, ProviderDayResult{TradeDate: day.AddDate(0, 0, 20+i), qualified: false})
	}
	if state.Status != ProviderStatusActive || state.FailureStreak != 2 {
		t.Fatalf("after two failures = %+v", state)
	}
	state = AdvanceProviderHealth(state, ProviderDayResult{TradeDate: day.AddDate(0, 0, 22), qualified: false})
	if state.Status != ProviderStatusDegraded {
		t.Fatalf("after three failures = %+v", state)
	}
	state = AdvanceProviderHealth(state, ProviderDayResult{TradeDate: day.AddDate(0, 0, 23), qualified: true})
	if state.FailureStreak != 0 {
		t.Fatalf("successful day did not reset failure streak: %+v", state)
	}
	unchanged := AdvanceProviderHealth(state, ProviderDayResult{TradeDate: day.AddDate(0, 0, 23), qualified: false})
	if unchanged != state {
		t.Fatalf("duplicate date changed state: before %+v after %+v", state, unchanged)
	}
}

func TestEvaluateProviderDayEnforcesEveryThreshold(t *testing.T) {
	base := ProviderResult{EffectiveDate: civilDate(t, "2026-06-18"), CoveragePct: DefaultPriceCoveragePct, ValidationErrorPct: 0, Timely: true}
	provenance := GoldProvenance{Provider: "independent", SourceURL: "https://gold.example.test/prices.csv", SHA256: strings.Repeat("a", 64)}
	qualified, err := EvaluateProviderDay(base, MinimumIndependentGoldRows, DefaultIndependentErrorPct, provenance)
	if err != nil || !qualified.qualified {
		t.Fatalf("qualified day = %+v, error = %v", qualified, err)
	}
	tests := []struct {
		name       string
		mutate     func(*ProviderResult)
		goldRows   int
		goldError  float64
		provenance GoldProvenance
	}{
		{name: "low coverage", mutate: func(result *ProviderResult) { result.CoveragePct-- }, goldRows: 100, goldError: 0, provenance: provenance},
		{name: "late", mutate: func(result *ProviderResult) { result.Timely = false }, goldRows: 100, goldError: 0, provenance: provenance},
		{name: "validation error", mutate: func(result *ProviderResult) { result.ValidationErrorPct = 0.01 }, goldRows: 100, goldError: 0, provenance: provenance},
		{name: "gold mismatch", mutate: func(*ProviderResult) {}, goldRows: 100, goldError: DefaultIndependentErrorPct + 0.01, provenance: provenance},
		{name: "insufficient gold", mutate: func(*ProviderResult) {}, goldRows: 99, goldError: 0, provenance: provenance},
		{name: "missing provenance", mutate: func(*ProviderResult) {}, goldRows: 100, goldError: 0, provenance: GoldProvenance{}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := base
			test.mutate(&result)
			day, evalErr := EvaluateProviderDay(result, test.goldRows, test.goldError, test.provenance)
			if evalErr == nil && day.qualified {
				t.Fatalf("day qualified with failing threshold: %+v", day)
			}
		})
	}
}

func marketValidationOptions(t *testing.T, expected []Listing) PriceValidationOptions {
	t.Helper()
	calendar := &stubMarketCalendar{holidays: map[string]bool{"2026-06-19": true}}
	return PriceValidationOptions{
		Provider: "test-provider", SourceVersion: "2026-06-18-v1", SourceURL: "https://prices.example.test/file.csv",
		EffectiveDate: civilDate(t, "2026-06-18"), Now: time.Date(2026, 6, 19, 11, 59, 0, 0, mustNY(t)), Calendar: calendar, Expected: expected,
	}
}

type stubMarketCalendar struct{ holidays map[string]bool }

func (calendar *stubMarketCalendar) IsTradingDate(_ context.Context, date string) (bool, error) {
	parsed, err := time.Parse(time.DateOnly, date)
	if err != nil {
		return false, err
	}
	return parsed.Weekday() != time.Saturday && parsed.Weekday() != time.Sunday && !calendar.holidays[date], nil
}

func (calendar *stubMarketCalendar) IsTradingDay(ctx context.Context, day time.Time) (bool, error) {
	return calendar.IsTradingDate(ctx, day.In(mustNYNoTest()).Format(time.DateOnly))
}

func zipPayload(t *testing.T, name, content string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	file, err := writer.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(file, content); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func civilDate(t *testing.T, date string) time.Time {
	t.Helper()
	parsed, err := time.ParseInLocation(time.DateOnly, date, mustNY(t))
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

func mustNY(t *testing.T) *time.Location {
	t.Helper()
	return mustNYNoTest()
}

func mustNYNoTest() *time.Location {
	location, err := time.LoadLocation("America/New_York")
	if err != nil {
		panic(err)
	}
	return location
}
