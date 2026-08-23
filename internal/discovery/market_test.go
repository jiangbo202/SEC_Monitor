package discovery

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

const normalizedPrices = `symbol,trade_date,open,high,low,close,volume,currency,is_adjusted,source
BRK.B,2026-06-18,500.000001,501,499.5,500.123456,1000,USD,false,row-source
PER,2026-06-18,10,11,9,10.25,2000,USD,false,row-source
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
	header := "symbol,trade_date,open,high,low,close,volume,currency,is_adjusted,source\n"
	tests := []struct {
		name string
		rows string
		want string
	}{
		{"adjusted", "ACME,2026-06-18,1,2,1,1,1,USD,true,manual\n", "adjusted"},
		{"non USD", "ACME,2026-06-18,1,2,1,1,1,EUR,false,manual\n", "USD"},
		{"negative volume", "ACME,2026-06-18,1,2,1,1,-1,USD,false,manual\n", "volume"},
		{"zero price", "ACME,2026-06-18,0,2,1,1,1,USD,false,manual\n", "positive"},
		{"invalid OHLC", "ACME,2026-06-18,3,2,1,1,1,USD,false,manual\n", "OHLC"},
		{"duplicate", "ACME,2026-06-18,1,2,1,1,1,USD,false,manual\nACME,2026-06-18,1,2,1,1,1,USD,false,manual\n", "duplicate"},
		{"invalid date", "ACME,2026-02-30,1,2,1,1,1,USD,false,manual\n", "date"},
		{"non trading", "ACME,2026-06-19,1,2,1,1,1,USD,false,manual\n", "trading"},
		{"future", "ACME,2026-06-22,1,2,1,1,1,USD,false,manual\n", "future"},
		{"stale", "ACME,2026-06-17,1,2,1,1,1,USD,false,manual\n", "stale"},
		{"plus whole", "ACME,2026-06-18,+1,2,1,1,1,USD,false,manual\n", "decimal"},
		{"plus fraction", "ACME,2026-06-18,1.+1,2,1,1,1,USD,false,manual\n", "decimal"},
		{"overflow", "ACME,2026-06-18,9223372036854.775808,9223372036854.775808,9223372036854.775808,9223372036854.775808,1,USD,false,manual\n", "range"},
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
	input := "symbol,trade_date,open,high,low,close,volume,currency,is_adjusted,source\nACME,2026-06-18,1,2,1,1,1,USD,false,manual\n"
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
		{"at boundary", time.Date(2026, 6, 22, 12, 0, 0, 0, mustNY(t)), true},
		{"after boundary", time.Date(2026, 6, 22, 12, 0, 0, 1, mustNY(t)), false},
		{"DST instant before", time.Date(2026, 3, 10, 15, 59, 0, 0, time.UTC), true},
		{"DST instant after", time.Date(2026, 3, 10, 16, 0, 1, 0, time.UTC), false},
	} {
		t.Run(test.name, func(t *testing.T) {
			date := "2026-06-18"
			if strings.HasPrefix(test.name, "DST") {
				date = "2026-03-09"
			}
			input := "symbol,trade_date,open,high,low,close,volume,currency,is_adjusted,source\nACME," + date + ",1,2,1,1,1,USD,false,manual\n"
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
	input := "symbol,trade_date,open,high,low,close,volume,currency,is_adjusted,source\nACME,2026-06-18,1,2,1,1,1,USD,false,manual\n"
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

func TestPriceValidationAllowsPreviousTradingPriceForNonTradingEffectiveDate(t *testing.T) {
	input := "symbol,trade_date,open,high,low,close,volume,currency,is_adjusted,source\nACME,2026-06-18,1,2,1,1,1,USD,false,manual\n"
	opts := marketValidationOptions(t, []Listing{{Ticker: "ACME"}})
	opts.EffectiveDate = civilDate(t, "2026-06-19")
	opts.AllowPreviousTradingDatePrice = true
	_, result, err := ParsePriceCSV(context.Background(), strings.NewReader(input), PriceFormatNormalized, opts)
	if err != nil {
		t.Fatalf("ParsePriceCSV() error = %v", err)
	}
	if got := result.EffectiveDate.Format(time.DateOnly); got != "2026-06-19" {
		t.Fatalf("EffectiveDate = %s, want 2026-06-19", got)
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
	var sources []string
	if err := db.Model(&PriceSnapshot{}).Distinct().Pluck("source", &sources).Error; err != nil || len(sources) != 1 || sources[0] != "row-source" {
		t.Fatalf("persisted sources = %v, err = %v", sources, err)
	}

	bad := strings.Replace(normalizedPrices, "2000,USD,false", "-1,USD,false", 1)
	if _, err := ImportPriceCSV(context.Background(), db, strings.NewReader(bad), PriceFormatNormalized, opts); err == nil {
		t.Fatal("invalid import error = nil")
	}
	db.Model(&PriceSnapshot{}).Count(&count)
	if count != 2 {
		t.Fatalf("failed import changed snapshot count to %d", count)
	}
	conflict := strings.Replace(normalizedPrices, "10.25,2000", "10.5,2000", 1)
	if _, err := ImportPriceCSV(context.Background(), db, strings.NewReader(conflict), PriceFormatNormalized, opts); !errors.Is(err, ErrPriceImportConflict) {
		t.Fatalf("conflicting idempotency error = %v", err)
	} else if !strings.Contains(err.Error(), "existing ohlc=10000000/11000000/9000000/10250000 volume=2000") || !strings.Contains(err.Error(), "incoming ohlc=10000000/11000000/9000000/10500000 volume=2000") {
		t.Fatalf("conflict diagnostics = %v", err)
	}
}

func TestImportPriceCSVAllowsSameDayRevisionWithNewContentVersion(t *testing.T) {
	db := openMigratedTestDatabase(t)
	baseOptions := marketValidationOptions(t, []Listing{{Ticker: "BRK.B", ProviderTicker: "BRK-B"}, {Ticker: "PER"}})
	baseOptions.SourceVersion = "provider:2026-06-18:content-a"
	if _, err := ImportPriceCSV(context.Background(), db, strings.NewReader(normalizedPrices), PriceFormatNormalized, baseOptions); err != nil {
		t.Fatalf("first import: %v", err)
	}

	revisedInput := strings.Replace(normalizedPrices, "10.25,2000", "10.5,2001", 1)
	revisedOptions := baseOptions
	revisedOptions.SourceVersion = "provider:2026-06-18:content-b"
	if _, err := ImportPriceCSV(context.Background(), db, strings.NewReader(revisedInput), PriceFormatNormalized, revisedOptions); err != nil {
		t.Fatalf("revised import: %v", err)
	}

	var count int64
	if err := db.Model(&PriceSnapshot{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 4 {
		t.Fatalf("price snapshot count = %d, want both two-row versions", count)
	}
}

func TestPersistPriceSnapshotsUpgradesLegacyCloseOnlyRow(t *testing.T) {
	db := openMigratedTestDatabase(t)
	tradeDate := civilDate(t, "2026-06-18")
	legacy := PriceSnapshot{
		Source: "longbridge", SourceVersion: "longbridge:technical-history:2026-06-18",
		Symbol: "ACME", TradeDate: tradeDate, CloseMicros: 10_250_000, Volume: 2_000,
		Currency: "USD", Adjusted: false, QualityStatus: QualityStatusValid,
	}
	if err := db.Create(&legacy).Error; err != nil {
		t.Fatal(err)
	}
	// AutoMigrate adds the OHLC columns as NULL for rows created by an older
	// binary. Reproduce that real upgrade state rather than only testing zeros.
	if err := db.Model(&PriceSnapshot{}).Where("id = ?", legacy.ID).Updates(map[string]interface{}{
		"open_micros": nil, "high_micros": nil, "low_micros": nil,
	}).Error; err != nil {
		t.Fatal(err)
	}
	incoming := legacy
	incoming.ID = 0
	incoming.OpenMicros = 10_000_000
	incoming.HighMicros = 11_000_000
	incoming.LowMicros = 9_000_000
	if err := persistPriceSnapshotsInBatches(db, []PriceSnapshot{incoming}); err != nil {
		t.Fatalf("upgrade close-only row: %v", err)
	}
	var stored PriceSnapshot
	if err := db.First(&stored, legacy.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.OpenMicros != incoming.OpenMicros || stored.HighMicros != incoming.HighMicros || stored.LowMicros != incoming.LowMicros || stored.CloseMicros != incoming.CloseMicros {
		t.Fatalf("upgraded OHLC = %d/%d/%d/%d", stored.OpenMicros, stored.HighMicros, stored.LowMicros, stored.CloseMicros)
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
	primary := []PriceRecord{
		{Symbol: "BRK.B", TradeDate: civilDate(t, "2026-06-18"), CloseMicros: 500_123_456, Source: "stooq"},
		{Symbol: "PER", TradeDate: civilDate(t, "2026-06-18"), CloseMicros: 10_250_000, Source: "stooq"},
	}
	if _, err := LoadFrozenMarketGold(primary, "stooq", time.Now()); err == nil || !strings.Contains(err.Error(), "want at least 100") {
		t.Fatalf("incomplete frozen gold should block activation: %v", err)
	}
}

func TestPriceValidationTreatsFrozenGoldProviderMismatchAsIncomplete(t *testing.T) {
	primary := []PriceRecord{{Symbol: "BRK.B", TradeDate: civilDate(t, "2026-06-18"), CloseMicros: 500_123_456, Source: "tiingo"}}
	_, err := LoadFrozenMarketGold(primary, "tiingo", time.Date(2026, 6, 22, 0, 0, 0, 0, time.UTC))
	if !errors.Is(err, ErrGoldEvidenceIncomplete) {
		t.Fatalf("err = %v, want ErrGoldEvidenceIncomplete", err)
	}
}

func TestProviderStateTransitions(t *testing.T) {
	calendar := &stubMarketCalendar{}
	state := activatedProviderHealth(t, calendar)
	last := civilDate(t, state.LastTradeDate)
	first, _ := nextTradingDate(context.Background(), calendar, last)
	day := ProviderDayResult{TradeDate: first, coveragePct: 90, timely: true, validationOK: true, goldReady: true, goldSHA256: testGoldSHA}
	var err error
	state, err = AdvanceProviderHealth(context.Background(), calendar, state, day)
	if err != nil || state.FailureStreak != 1 {
		t.Fatalf("failure state = %+v, err=%v", state, err)
	}
	secondFailure, _ := nextTradingDate(context.Background(), calendar, first)
	day.TradeDate = secondFailure
	state, err = AdvanceProviderHealth(context.Background(), calendar, state, day)
	if err != nil || state.FailureStreak != 2 {
		t.Fatalf("second failure state = %+v, err=%v", state, err)
	}
	thirdFailure, _ := nextTradingDate(context.Background(), calendar, secondFailure)
	day.TradeDate = thirdFailure
	state, err = AdvanceProviderHealth(context.Background(), calendar, state, day)
	if err != nil || state.Status != ProviderStatusDegraded || state.FailureStreak != 3 {
		t.Fatalf("degraded state = %+v, err=%v", state, err)
	}
	// Continue the reset assertion from a fresh active state.
	state = activatedProviderHealth(t, calendar)
	first, _ = nextTradingDate(context.Background(), calendar, civilDate(t, state.LastTradeDate))
	second, _ := nextTradingDate(context.Background(), calendar, first)
	day = ProviderDayResult{TradeDate: second, coveragePct: 100, timely: true, validationOK: true, goldReady: true, goldSHA256: testGoldSHA}
	state, err = AdvanceProviderHealth(context.Background(), calendar, state, day)
	if err != nil || state.FailureStreak != 0 {
		t.Fatalf("successful day did not reset failure streak: %+v, err=%v", state, err)
	}
}

func TestEvaluateProviderDayEnforcesEveryThreshold(t *testing.T) {
	base := ProviderResult{Provider: "stooq", EffectiveDate: civilDate(t, "2026-06-18"), Expected: 100, CoveragePct: DefaultPriceCoveragePct, ValidationErrorPct: 0, Timely: true}
	primary := []PriceRecord{
		{Symbol: "BRK.B", TradeDate: civilDate(t, "2026-06-18"), CloseMicros: 500_123_456, Source: "stooq"},
		{Symbol: "PER", TradeDate: civilDate(t, "2026-06-18"), CloseMicros: 10_250_000, Source: "stooq"},
	}
	day, err := EvaluateProviderDay(base, primary, time.Now())
	if err != nil || day.goldReady || day.goldSHA256 == "" {
		t.Fatalf("incomplete frozen evidence day = %+v, error = %v", day, err)
	}
	badPrimary := append([]PriceRecord(nil), primary...)
	badPrimary[0].Source = "not-stooq"
	if day2, err := EvaluateProviderDay(base, badPrimary, time.Now()); err != nil || day2.goldSHA256 != day.goldSHA256 {
		t.Fatalf("daily records must not be joined to historical frozen evidence: day=%+v err=%v", day2, err)
	}
	base.Expected = 0
	if _, err := EvaluateProviderDay(base, primary, time.Now()); err == nil {
		t.Fatal("empty expected universe accepted for activation")
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
