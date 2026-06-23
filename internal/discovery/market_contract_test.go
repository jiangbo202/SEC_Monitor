package discovery

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const testGoldSHA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func TestNormalizedPriceSchemasAndTickerMapping(t *testing.T) {
	full := "symbol,trade_date,open,high,low,close,volume,currency,is_adjusted,source\nBRK-B,2026-06-18,10,11,9,10.5,12,USD,false,manual\n"
	closeOnly := "symbol,trade_date,close,currency,is_adjusted,source\nBRK-B,2026-06-18,10.5,USD,false,manual\n"
	opts := marketValidationOptions(t, []Listing{{Ticker: "BRK.B", ProviderTicker: "BRK-B"}})
	for _, input := range []string{full, closeOnly} {
		records, _, err := ParsePriceCSV(context.Background(), strings.NewReader(input), PriceFormatNormalized, opts)
		if err != nil {
			t.Fatalf("ParsePriceCSV() error = %v", err)
		}
		if len(records) != 1 || records[0].Symbol != "BRK.B" || records[0].Source != "manual" || records[0].CloseMicros != 10_500_000 {
			t.Fatalf("records = %+v", records)
		}
	}

	unknown := strings.Replace(full, "BRK-B", "UNKNOWN", 1)
	if _, _, err := ParsePriceCSV(context.Background(), strings.NewReader(unknown), PriceFormatNormalized, opts); err == nil || !strings.Contains(err.Error(), "mapping") {
		t.Fatalf("unknown ticker error = %v", err)
	}
	conflict := opts
	conflict.Expected = []Listing{{Ticker: "A", ProviderTicker: "SAME"}, {Ticker: "B", ProviderTicker: "SAME"}}
	if _, _, err := ParsePriceCSV(context.Background(), strings.NewReader(full), PriceFormatNormalized, conflict); err == nil || !strings.Contains(err.Error(), "maps to both") {
		t.Fatalf("mapping conflict error = %v", err)
	}
}

func TestParsePriceMicrosAcceptsExactInt64Maximum(t *testing.T) {
	got, err := parsePriceMicros("9223372036854.775807")
	if err != nil || got != int64(^uint64(0)>>1) {
		t.Fatalf("parse max micros = %d, err = %v", got, err)
	}
}

func TestImportPriceCSVUsesBoundedBatches(t *testing.T) {
	var input strings.Builder
	input.WriteString("symbol,trade_date,close,currency,is_adjusted,source\n")
	for index := 0; index < 1001; index++ {
		fmt.Fprintf(&input, "S%04d,2026-06-18,1,USD,false,batch-source\n", index)
	}
	db := openMigratedTestDatabase(t)
	opts := marketValidationOptions(t, nil)
	if _, err := ImportPriceCSV(context.Background(), db, strings.NewReader(input.String()), PriceFormatNormalized, opts); err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := db.Model(&PriceSnapshot{}).Count(&count).Error; err != nil || count != 1001 {
		t.Fatalf("batch import count = %d, err = %v", count, err)
	}
}

func TestPriceZIPRejectsEntryFloodBeforeParsing(t *testing.T) {
	var payload bytes.Buffer
	writer := zip.NewWriter(&payload)
	for index := 0; index <= maxPriceZIPFiles; index++ {
		entry, err := writer.Create(fmt.Sprintf("prices-%02d.csv", index))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte("x")); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := readSingleCSVFromZIP(payload.Bytes()); err == nil || !strings.Contains(err.Error(), "exceeds 16 entries") {
		t.Fatalf("ZIP entry flood error = %v", err)
	}
}

func TestPriceZIPRejectsFakeEOCDInComment(t *testing.T) {
	var payload bytes.Buffer
	writer := zip.NewWriter(&payload)
	for index := 0; index <= maxPriceZIPFiles; index++ {
		entry, _ := writer.Create(fmt.Sprintf("prices-%02d.csv", index))
		_, _ = entry.Write([]byte("x"))
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	crafted := append([]byte(nil), payload.Bytes()...)
	realEOCD := len(crafted) - 22
	centralSize := binary.LittleEndian.Uint32(crafted[realEOCD+12 : realEOCD+16])
	centralOffset := binary.LittleEndian.Uint32(crafted[realEOCD+16 : realEOCD+20])
	binary.LittleEndian.PutUint16(crafted[realEOCD+20:realEOCD+22], 22)
	fake := make([]byte, 22)
	binary.LittleEndian.PutUint32(fake[0:4], 0x06054b50)
	binary.LittleEndian.PutUint16(fake[8:10], 1)
	binary.LittleEndian.PutUint16(fake[10:12], 1)
	binary.LittleEndian.PutUint32(fake[12:16], centralSize)
	binary.LittleEndian.PutUint32(fake[16:20], centralOffset)
	crafted = append(crafted, fake...)
	if _, err := zipCentralDirectoryEntryCount(crafted); err == nil {
		t.Fatal("fake EOCD in ZIP comment bypassed central-directory validation")
	}
}

func TestPriceTimelinessUsesNextTradingDay(t *testing.T) {
	input := "symbol,trade_date,close,currency,is_adjusted,source\nACME,2026-07-02,1,USD,false,manual\n"
	calendar := &stubMarketCalendar{holidays: map[string]bool{"2026-07-03": true}}
	opts := marketValidationOptions(t, []Listing{{Ticker: "ACME"}})
	opts.Calendar = calendar
	opts.EffectiveDate = civilDate(t, "2026-07-02")
	opts.Now = time.Date(2026, 7, 6, 12, 0, 0, 0, mustNY(t))
	_, result, err := ParsePriceCSV(context.Background(), strings.NewReader(input), PriceFormatNormalized, opts)
	if err != nil || !result.Timely {
		t.Fatalf("at Monday deadline: result=%+v err=%v", result, err)
	}
	opts.Now = opts.Now.Add(time.Nanosecond)
	_, result, err = ParsePriceCSV(context.Background(), strings.NewReader(input), PriceFormatNormalized, opts)
	if err != nil || result.Timely {
		t.Fatalf("after Monday deadline: result=%+v err=%v", result, err)
	}
}

func TestIndependentGoldMustBeFrozenAuditedAndComplete(t *testing.T) {
	var csv strings.Builder
	csv.WriteString("symbol,trade_date,primary_close,expected_close,primary_provider,source_url,observed_at,reviewer,source_provider,source_tier,fallback_reason,case_type\n")
	cases := []string{"split", "ticker_change", "multi_class", "delisted"}
	for i := 0; i < MinimumIndependentGoldRows; i++ {
		symbol := fmt.Sprintf("S%03d", i)
		fmt.Fprintf(&csv, "%s,2026-06-18,1.000000,1.000000,stooq,https://exchange.example.test/%s,2026-06-20T12:00:00Z,reviewer-%d,exchange,exchange,,%s\n", symbol, symbol, i, cases[i%len(cases)])
	}
	gold, err := validateIndependentGoldCSV(strings.NewReader(csv.String()), "stooq", time.Date(2026, 6, 22, 0, 0, 0, 0, time.UTC))
	if err != nil || !gold.ready || gold.rows != MinimumIndependentGoldRows || gold.errorPct != 0 {
		t.Fatalf("gold = %+v, err = %v", gold, err)
	}
	if _, err := validateIndependentGoldCSV(strings.NewReader(strings.Replace(csv.String(), ",exchange,exchange,", ",stooq,exchange,", 1)), "stooq", time.Date(2026, 6, 22, 0, 0, 0, 0, time.UTC)); err == nil {
		t.Fatal("primary provider reused as independent gold")
	}
	missingFallbackReason := strings.Replace(csv.String(), ",exchange,,split", ",other,,split", 1)
	if _, err := validateIndependentGoldCSV(strings.NewReader(missingFallbackReason), "stooq", time.Date(2026, 6, 22, 0, 0, 0, 0, time.UTC)); err == nil || !strings.Contains(err.Error(), "fallback reason") {
		t.Fatalf("other source without fallback reason error = %v", err)
	}
	wrongPrimary := strings.Replace(csv.String(), ",stooq,https://", ",other-primary,https://", 1)
	if _, err := validateIndependentGoldCSV(strings.NewReader(wrongPrimary), "stooq", time.Date(2026, 6, 22, 0, 0, 0, 0, time.UTC)); err == nil {
		t.Fatal("frozen primary provider was not bound to provider result")
	}
	short := strings.Join(strings.Split(csv.String(), "\n")[:100], "\n") + "\n"
	if _, err := validateIndependentGoldCSV(strings.NewReader(short), "stooq", time.Date(2026, 6, 22, 0, 0, 0, 0, time.UTC)); err == nil {
		t.Fatal("99-row gold accepted")
	}
}

func TestProviderHealthUsesTradingDayWindow(t *testing.T) {
	ctx := context.Background()
	calendar := &stubMarketCalendar{holidays: map[string]bool{"2026-05-25": true}}
	state := ProviderHealth{Provider: "prices", Status: ProviderStatusValidation}
	date := civilDate(t, "2026-05-01")
	tradingDays := make([]time.Time, 0, 23)
	for len(tradingDays) < 23 {
		ok, _ := calendar.IsTradingDate(ctx, date.Format(time.DateOnly))
		if ok {
			tradingDays = append(tradingDays, date)
		}
		date = date.AddDate(0, 0, 1)
	}
	for i := 0; i < 20; i++ {
		day := ProviderDayResult{TradeDate: tradingDays[i], coveragePct: 98, timely: i != 0, validationOK: true, goldReady: true, goldSHA256: testGoldSHA}
		var err error
		state, err = AdvanceProviderHealth(ctx, calendar, state, day)
		if err != nil {
			t.Fatal(err)
		}
	}
	if state.Status != ProviderStatusActive {
		t.Fatalf("19/20 timely days should activate: %+v", state)
	}
	for i := 20; i < 23; i++ {
		var err error
		state, err = AdvanceProviderHealth(ctx, calendar, state, ProviderDayResult{TradeDate: tradingDays[i], coveragePct: 97.99, timely: true, validationOK: true, goldReady: true, goldSHA256: testGoldSHA})
		if err != nil {
			t.Fatal(err)
		}
	}
	if state.Status != ProviderStatusDegraded || state.FailureStreak != 3 {
		t.Fatalf("three failed trading days should degrade: %+v", state)
	}
	weekend := civilDate(t, "2026-05-02")
	if _, err := AdvanceProviderHealth(ctx, calendar, ProviderHealth{Provider: "x"}, ProviderDayResult{TradeDate: weekend}); err == nil {
		t.Fatal("weekend advanced provider state")
	}
}

func TestProviderWindowRequiresNineteenTimelyDays(t *testing.T) {
	ctx := context.Background()
	calendar := &stubMarketCalendar{}
	state := ProviderHealth{Provider: "prices", Status: ProviderStatusValidation}
	date := civilDate(t, "2026-04-01")
	added := 0
	for added < 20 {
		trading, _ := calendar.IsTradingDate(ctx, date.Format(time.DateOnly))
		if trading {
			day := ProviderDayResult{TradeDate: date, coveragePct: 98, timely: added >= 2, validationOK: true, goldReady: true, goldSHA256: testGoldSHA}
			var err error
			state, err = AdvanceProviderHealth(ctx, calendar, state, day)
			if err != nil {
				t.Fatal(err)
			}
			added++
		}
		date = date.AddDate(0, 0, 1)
	}
	if state.Status != ProviderStatusValidation {
		t.Fatalf("18/20 timely days activated provider: %+v", state)
	}
}

func TestProviderHealthCountsSkippedTradingDaysAsFailures(t *testing.T) {
	calendar := &stubMarketCalendar{}
	state := activatedProviderHealth(t, calendar)
	last := civilDate(t, state.LastTradeDate)
	first, _ := nextTradingDate(context.Background(), calendar, last)
	second, _ := nextTradingDate(context.Background(), calendar, first)
	third, _ := nextTradingDate(context.Background(), calendar, second)
	state, err := AdvanceProviderHealth(context.Background(), calendar, state, ProviderDayResult{TradeDate: third, goldSHA256: testGoldSHA})
	if err != nil || state.Status != ProviderStatusDegraded || state.FailureStreak != 3 || state.LastTradeDate != third.Format(time.DateOnly) {
		t.Fatalf("skipped trading days state = %+v, err = %v", state, err)
	}
}

func activatedProviderHealth(t *testing.T, calendar MarketCalendar) ProviderHealth {
	t.Helper()
	state := ProviderHealth{Provider: "prices", Status: ProviderStatusValidation}
	date := civilDate(t, "2026-04-01")
	for state.Status != ProviderStatusActive {
		trading, err := calendar.IsTradingDate(context.Background(), date.Format(time.DateOnly))
		if err != nil {
			t.Fatal(err)
		}
		if trading {
			state, err = AdvanceProviderHealth(context.Background(), calendar, state, ProviderDayResult{TradeDate: date, coveragePct: 100, timely: true, validationOK: true, goldReady: true, goldSHA256: testGoldSHA})
			if err != nil {
				t.Fatal(err)
			}
		}
		date = date.AddDate(0, 0, 1)
	}
	return state
}

func TestProviderHealthGoldChangeForcesRevalidation(t *testing.T) {
	calendar := &stubMarketCalendar{}
	state := activatedProviderHealth(t, calendar)
	last := civilDate(t, state.LastTradeDate)
	next, _ := nextTradingDate(context.Background(), calendar, last)
	newSHA := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	state, err := AdvanceProviderHealth(context.Background(), calendar, state, ProviderDayResult{TradeDate: next, coveragePct: 100, timely: true, validationOK: true, goldReady: true, goldSHA256: newSHA})
	if err != nil || state.Status != ProviderStatusValidation || state.QualifiedTradingDays != 1 || state.GoldSHA256 != newSHA {
		t.Fatalf("gold change state = %+v, err = %v", state, err)
	}
}

func TestProviderHealthRejectsCorruptWindow(t *testing.T) {
	state := ProviderHealth{
		Provider: "prices", Status: ProviderStatusValidation, QualifiedTradingDays: 1,
		LastTradeDate: "2026-06-18", WindowJSON: `[{"date":"2026-06-18","coverage_pct":101,"timely":true,"validation_ok":true}]`,
	}
	_, err := AdvanceProviderHealth(context.Background(), &stubMarketCalendar{}, state, ProviderDayResult{TradeDate: civilDate(t, "2026-06-19"), goldSHA256: testGoldSHA})
	if err == nil || !strings.Contains(err.Error(), "invalid coverage") {
		t.Fatalf("corrupt window error = %v", err)
	}
}

func TestActiveProviderHealthRequiresSupportingWindow(t *testing.T) {
	calendar := &stubMarketCalendar{}
	state := activatedProviderHealth(t, calendar)
	var window []providerWindowDay
	if err := json.Unmarshal([]byte(state.WindowJSON), &window); err != nil {
		t.Fatal(err)
	}
	for index := range window {
		window[index].CoveragePct = 0
	}
	encoded, _ := json.Marshal(window)
	state.WindowJSON = string(encoded)
	last := civilDate(t, state.LastTradeDate)
	next, _ := nextTradingDate(context.Background(), calendar, last)
	_, err := AdvanceProviderHealth(context.Background(), calendar, state, ProviderDayResult{TradeDate: next, goldSHA256: testGoldSHA})
	if err == nil || !strings.Contains(err.Error(), "does not support") {
		t.Fatalf("unsupported active window error = %v", err)
	}
}

func TestInvalidDownloadPreservesValidatedCacheAcrossRestart(t *testing.T) {
	cacheDir := t.TempDir()
	valid := []byte(normalizedPrices)
	requests := 0
	client := &http.Client{Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		requests++
		header := make(http.Header)
		switch requests {
		case 1:
			header.Set("ETag", `"v1"`)
			return &http.Response{StatusCode: http.StatusOK, Header: header, Body: io.NopCloser(bytes.NewReader(valid)), ContentLength: int64(len(valid)), Request: request}, nil
		case 2:
			header.Set("ETag", `"v2"`)
			bad := []byte("not,a,price,file\n")
			return &http.Response{StatusCode: http.StatusOK, Header: header, Body: io.NopCloser(bytes.NewReader(bad)), ContentLength: int64(len(bad)), Request: request}, nil
		default:
			if request.Header.Get("If-None-Match") != `"v1"` {
				t.Errorf("restart If-None-Match = %q", request.Header.Get("If-None-Match"))
			}
			return &http.Response{StatusCode: http.StatusNotModified, Header: header, Body: io.NopCloser(strings.NewReader("")), Request: request}, nil
		}
	})}
	options := DownloadedPriceProviderOptions{
		Provider: "configured", URL: "https://prices.example.test/daily.csv", CacheKey: "preserved", Format: PriceFormatNormalized,
		Downloader: &Downloader{Client: client, CacheDir: cacheDir, MaxBytes: 1 << 20}, Validation: marketValidationOptions(t, nil),
	}
	provider, err := NewDownloadedPriceProvider(options)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := provider.Load(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if _, _, err := provider.Load(context.Background(), nil); err == nil {
		t.Fatal("invalid replacement was accepted")
	}
	validated, err := os.ReadFile(provider.validatedDataPath())
	if err != nil || !bytes.Equal(validated, valid) {
		t.Fatalf("validated cache changed: err=%v payload=%q", err, validated)
	}
	restarted, err := NewDownloadedPriceProvider(options)
	if err != nil {
		t.Fatal(err)
	}
	records, result, err := restarted.Load(context.Background(), nil)
	if err != nil || len(records) != 2 || result.SHA256 == "" {
		t.Fatalf("restart load records=%d result=%+v err=%v", len(records), result, err)
	}
}

func TestValidatedCacheRejectsNonHexSHAPath(t *testing.T) {
	cacheDir := t.TempDir()
	state := `{"SourceURL":"https://prices.example.test/daily.csv","SHA256":"../../outside","Size":1}`
	if err := os.WriteFile(absolutePriceCachePath(cacheDir, "unsafe.validated.json"), []byte(state), 0o600); err != nil {
		t.Fatal(err)
	}
	provider, err := NewDownloadedPriceProvider(DownloadedPriceProviderOptions{
		Provider: "configured", URL: "https://prices.example.test/daily.csv", CacheKey: "unsafe", Format: PriceFormatNormalized,
		Downloader: &Downloader{Client: &http.Client{}, CacheDir: cacheDir, MaxBytes: 1 << 20}, Validation: marketValidationOptions(t, nil),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := provider.Load(context.Background(), nil); err == nil || !strings.Contains(err.Error(), "metadata does not match") {
		t.Fatalf("unsafe cache SHA error = %v", err)
	}
}

func TestDownloadedProvidersSerializeSharedCacheLifecycle(t *testing.T) {
	var large strings.Builder
	large.WriteString("symbol,trade_date,close,currency,is_adjusted,source\n")
	for i := 0; i < 50_000; i++ {
		fmt.Fprintf(&large, "S%05d,2026-06-18,1,USD,false,row-source\n", i)
	}
	largePayload := []byte(large.String())
	cacheDir := t.TempDir()
	var requests atomic.Int32
	var firstReturned atomic.Bool
	var overlapped atomic.Bool
	firstServed := make(chan struct{})
	client := &http.Client{Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		requestNumber := requests.Add(1)
		payload := []byte(normalizedPrices)
		if requestNumber == 1 {
			payload = largePayload
			close(firstServed)
		} else if !firstReturned.Load() {
			overlapped.Store(true)
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(payload)), ContentLength: int64(len(payload)), Request: request}, nil
	})}
	options := DownloadedPriceProviderOptions{
		Provider: "configured", URL: "https://prices.example.test/daily.csv", CacheKey: "shared", Format: PriceFormatNormalized,
		Downloader: &Downloader{Client: client, CacheDir: cacheDir, MaxBytes: 8 << 20}, Validation: marketValidationOptions(t, nil),
	}
	first, _ := NewDownloadedPriceProvider(options)
	second, _ := NewDownloadedPriceProvider(options)
	firstDone := make(chan error, 1)
	go func() {
		_, _, err := first.Load(context.Background(), nil)
		firstReturned.Store(true)
		firstDone <- err
	}()
	<-firstServed
	secondDone := make(chan error, 1)
	go func() {
		_, _, err := second.Load(context.Background(), nil)
		secondDone <- err
	}()
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
	if err := <-secondDone; err != nil {
		t.Fatal(err)
	}
	if overlapped.Load() {
		t.Fatal("providers sharing a cache key overlapped download and promotion lifecycles")
	}
}

func TestSharedCacheLifecycleWaitHonorsContext(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	client := &http.Client{Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		close(started)
		<-release
		payload := []byte(normalizedPrices)
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(payload)), ContentLength: int64(len(payload)), Request: request}, nil
	})}
	options := DownloadedPriceProviderOptions{
		Provider: "configured", URL: "https://prices.example.test/daily.csv", CacheKey: "context-lock", Format: PriceFormatNormalized,
		Downloader: &Downloader{Client: client, CacheDir: t.TempDir(), MaxBytes: 1 << 20}, Validation: marketValidationOptions(t, nil),
	}
	first, _ := NewDownloadedPriceProvider(options)
	second, _ := NewDownloadedPriceProvider(options)
	firstDone := make(chan error, 1)
	go func() {
		_, _, err := first.Load(context.Background(), nil)
		firstDone <- err
	}()
	<-started
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := second.Load(ctx, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled lifecycle wait error = %v", err)
	}
	close(release)
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
}
