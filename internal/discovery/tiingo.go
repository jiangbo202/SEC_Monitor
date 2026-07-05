package discovery

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const defaultTiingoProvider = "tiingo"

const defaultTiingoProgressEvery = 250

type TiingoPriceProviderOptions struct {
	Provider        string
	Token           string
	Tokens          []string
	BaseURL         string
	Client          *http.Client
	Now             func() time.Time
	Calendar        MarketCalendar
	CacheDir        string
	Concurrency     int
	RequestBudget   int
	RequestInterval time.Duration
	ProgressEvery   int
	Progress        func(TiingoProgress)
}

type TiingoProgress struct {
	Provider         string
	Processed, Total int
	Records, Skipped int
	Elapsed          time.Duration
	SkipReasons      map[string]int
}

type TiingoPriceProvider struct {
	options       TiingoPriceProviderOptions
	baseURL       *url.URL
	throttleMu    sync.Mutex
	nextRequestAt time.Time
	tokenMu       sync.Mutex
	nextToken     int
}

func NewTiingoPriceProvider(options TiingoPriceProviderOptions) (*TiingoPriceProvider, error) {
	options.Provider = strings.TrimSpace(options.Provider)
	if options.Provider == "" {
		options.Provider = defaultTiingoProvider
	}
	options.Tokens = normalizeTiingoTokens(options.Token, options.Tokens)
	if len(options.Tokens) == 0 {
		return nil, errors.New("tiingo token is required")
	}
	if strings.TrimSpace(options.BaseURL) == "" {
		options.BaseURL = "https://api.tiingo.com"
	}
	parsed, err := url.Parse(options.BaseURL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
		return nil, errors.New("tiingo base URL must be HTTPS without user info")
	}
	if options.Client == nil {
		options.Client = &http.Client{Timeout: 30 * time.Second}
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.Calendar == nil {
		return nil, errors.New("tiingo market calendar is required")
	}
	if options.Concurrency <= 0 {
		options.Concurrency = 1
	}
	if options.ProgressEvery <= 0 {
		options.ProgressEvery = defaultTiingoProgressEvery
	}
	return &TiingoPriceProvider{options: options, baseURL: parsed}, nil
}

func normalizeTiingoTokens(primary string, extras []string) []string {
	seen := map[string]struct{}{}
	values := make([]string, 0, 1+len(extras))
	for _, token := range append([]string{primary}, extras...) {
		for _, part := range strings.Split(token, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			if _, ok := seen[part]; ok {
				continue
			}
			seen[part] = struct{}{}
			values = append(values, part)
		}
	}
	return values
}

func (provider *TiingoPriceProvider) ProviderName() string {
	return provider.options.Provider
}

func (provider *TiingoPriceProvider) Load(ctx context.Context, expected []Listing) ([]PriceRecord, ProviderResult, error) {
	return provider.loadForEffectiveDate(ctx, expected, "")
}

func (provider *TiingoPriceProvider) LoadForDate(ctx context.Context, expected []Listing, effectiveDate string) ([]PriceRecord, ProviderResult, error) {
	return provider.loadForEffectiveDate(ctx, expected, effectiveDate)
}

func (provider *TiingoPriceProvider) loadForEffectiveDate(ctx context.Context, expected []Listing, effectiveDateText string) ([]PriceRecord, ProviderResult, error) {
	if _, err := expectedSymbolMapping(expected); err != nil {
		return nil, ProviderResult{}, err
	}
	var requestedDate time.Time
	if strings.TrimSpace(effectiveDateText) != "" {
		newYork, err := time.LoadLocation("America/New_York")
		if err != nil {
			return nil, ProviderResult{}, err
		}
		requestedDate, err = parseCivilDate(strings.TrimSpace(effectiveDateText), newYork)
		if err != nil {
			return nil, ProviderResult{}, fmt.Errorf("invalid tiingo effective date %q", effectiveDateText)
		}
	}
	requests := uniqueTiingoRequests(expected)
	records, err := provider.loadSymbols(ctx, requests, requestedDate)
	if err != nil {
		return nil, ProviderResult{}, err
	}
	if len(records) == 0 {
		return nil, ProviderResult{}, errors.New("tiingo returned no usable price records")
	}
	priceDate := latestPriceRecordDate(records)
	effectiveDate := requestedDate
	if effectiveDate.IsZero() {
		effectiveDate = priceDate
	}
	records = filterPriceRecordsByDate(records, priceDate)
	if len(records) == 0 {
		return nil, ProviderResult{}, fmt.Errorf("tiingo returned no usable price records for %s", priceDate.Format(time.DateOnly))
	}
	sort.SliceStable(records, func(i, j int) bool {
		return records[i].Symbol < records[j].Symbol
	})
	validation := PriceValidationOptions{
		Provider:                      provider.options.Provider,
		SourceURL:                     strings.TrimRight(provider.baseURL.String(), "/"),
		SourceVersion:                 "tiingo:" + priceDate.Format(time.DateOnly),
		EffectiveDate:                 effectiveDate,
		Now:                           provider.options.Now().UTC(),
		Calendar:                      provider.options.Calendar,
		Expected:                      expected,
		AllowPreviousTradingDatePrice: !requestedDate.IsZero(),
	}
	result, err := validatePriceBatch(ctx, records, validation)
	if err != nil {
		return nil, ProviderResult{}, err
	}
	result.SHA256 = hashTiingoPriceRecords(records)
	if result.SourceVersion == "" {
		result.SourceVersion = validation.SourceVersion
	}
	return records, result, nil
}

func (provider *TiingoPriceProvider) loadSymbols(ctx context.Context, requests []tiingoRequest, effectiveDate time.Time) ([]PriceRecord, error) {
	if len(requests) == 0 {
		return nil, nil
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	state := &tiingoLoadState{budget: provider.totalRequestBudget()}
	started := time.Now()
	jobs := make(chan tiingoRequest)
	var wg sync.WaitGroup
	var mu sync.Mutex
	records := make([]PriceRecord, 0, len(requests))
	skipReasons := make(map[string]int)
	processed := 0
	skipped := 0
	var firstErr error
	workerCount := provider.options.Concurrency
	if workerCount > len(requests) {
		workerCount = len(requests)
	}
	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for request := range jobs {
				record, ok, reason, err := provider.loadSymbol(ctx, request, effectiveDate, state)
				mu.Lock()
				if err != nil && firstErr == nil {
					firstErr = err
					cancel()
				}
				if err == nil && ok {
					records = append(records, record)
				} else if err == nil {
					skipped++
					if reason == "" {
						reason = "no_usable_record"
					}
					skipReasons[reason]++
				}
				processed++
				provider.emitProgressLocked(processed, len(requests), len(records), skipped, skipReasons, time.Since(started))
				mu.Unlock()
			}
		}()
	}
	for _, request := range requests {
		select {
		case <-ctx.Done():
			break
		case jobs <- request:
		}
		if ctx.Err() != nil {
			break
		}
	}
	close(jobs)
	wg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}
	if len(records) == 0 && skipped > 0 {
		return nil, fmt.Errorf("tiingo returned no usable price records; skipped=%d reasons=%s", skipped, formatTiingoSkipReasons(skipReasons))
	}
	return records, nil
}

func (provider *TiingoPriceProvider) totalRequestBudget() int {
	if provider == nil || provider.options.RequestBudget <= 0 {
		return 0
	}
	tokenCount := len(provider.options.Tokens)
	if tokenCount <= 0 {
		tokenCount = 1
	}
	return provider.options.RequestBudget * tokenCount
}

type tiingoLoadState struct {
	mu         sync.Mutex
	budget     int
	httpCalls  int
	limitedAll bool
}

func (state *tiingoLoadState) acquireRequestBudget() bool {
	if state == nil || state.budget <= 0 {
		return true
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.httpCalls >= state.budget {
		return false
	}
	state.httpCalls++
	return true
}

func (state *tiingoLoadState) isRateLimited() bool {
	if state == nil {
		return false
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	return state.limitedAll
}

func (state *tiingoLoadState) markRateLimited() {
	if state == nil {
		return
	}
	state.mu.Lock()
	state.limitedAll = true
	state.mu.Unlock()
}

func formatTiingoSkipReasons(reasons map[string]int) string {
	if len(reasons) == 0 {
		return "unknown"
	}
	parts := make([]string, 0, len(reasons))
	for reason, count := range reasons {
		parts = append(parts, fmt.Sprintf("%s=%d", reason, count))
	}
	sort.Strings(parts)
	return strings.Join(parts, ",")
}

func (provider *TiingoPriceProvider) emitProgressLocked(processed, total, records, skipped int, skipReasons map[string]int, elapsed time.Duration) {
	if provider.options.Progress == nil {
		return
	}
	if processed != total && processed%provider.options.ProgressEvery != 0 {
		return
	}
	reasons := make(map[string]int, len(skipReasons))
	for reason, count := range skipReasons {
		reasons[reason] = count
	}
	provider.options.Progress(TiingoProgress{Provider: provider.options.Provider, Processed: processed, Total: total, Records: records, Skipped: skipped, SkipReasons: reasons, Elapsed: elapsed})
}

type tiingoRequest struct {
	canonical string
	provider  string
}

func uniqueTiingoRequests(expected []Listing) []tiingoRequest {
	seen := make(map[string]struct{})
	requests := make([]tiingoRequest, 0, len(expected))
	for _, listing := range expected {
		canonical := strings.ToUpper(strings.TrimSpace(listing.Ticker))
		if canonical == "" {
			continue
		}
		if _, exists := seen[canonical]; exists {
			continue
		}
		providerTicker := strings.TrimSpace(listing.ProviderTicker)
		if providerTicker == "" {
			providerTicker = canonical
		}
		requests = append(requests, tiingoRequest{
			canonical: canonical,
			provider:  strings.ToLower(providerTicker),
		})
		seen[canonical] = struct{}{}
	}
	return requests
}

func (provider *TiingoPriceProvider) loadSymbol(ctx context.Context, request tiingoRequest, effectiveDate time.Time, state *tiingoLoadState) (PriceRecord, bool, string, error) {
	if record, ok := provider.loadCachedRecord(request, effectiveDate); ok {
		return record, true, "", nil
	}
	if state.isRateLimited() {
		return PriceRecord{}, false, "rate_limited", nil
	}
	end := provider.options.Now().UTC()
	if !effectiveDate.IsZero() {
		end = effectiveDate.UTC()
	}
	start := end.AddDate(0, 0, -10)
	endpoint := *provider.baseURL
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + "/tiingo/daily/" + url.PathEscape(request.provider) + "/prices"
	query := endpoint.Query()
	query.Set("startDate", start.Format(time.DateOnly))
	query.Set("endDate", end.Format(time.DateOnly))
	query.Set("format", "json")
	endpoint.RawQuery = query.Encode()

	tokens := provider.nextTokenSequence()
	rateLimited := 0
	for _, token := range tokens {
		if !state.acquireRequestBudget() {
			return PriceRecord{}, false, "request_budget_exhausted", nil
		}
		if err := provider.waitForRequestSlot(ctx); err != nil {
			return PriceRecord{}, false, "", err
		}
		httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
		if err != nil {
			return PriceRecord{}, false, "", err
		}
		httpReq.Header.Set("Authorization", "Token "+token)
		httpReq.Header.Set("Accept", "application/json")
		resp, err := provider.options.Client.Do(httpReq)
		if err != nil {
			if ctx.Err() != nil {
				return PriceRecord{}, false, "", ctx.Err()
			}
			return PriceRecord{}, false, "network_error", nil
		}
		payload, readErr := readTiingoResponseBody(resp)
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			return PriceRecord{}, false, "", fmt.Errorf("tiingo authentication failed with HTTP %d", resp.StatusCode)
		}
		if resp.StatusCode == http.StatusTooManyRequests {
			rateLimited++
			continue
		}
		if resp.StatusCode >= 500 {
			return PriceRecord{}, false, "", fmt.Errorf("tiingo server error HTTP %d", resp.StatusCode)
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return PriceRecord{}, false, fmt.Sprintf("http_%d", resp.StatusCode), nil
		}
		if readErr != nil {
			return PriceRecord{}, false, "read_error", nil
		}
		var record PriceRecord
		var ok bool
		if !effectiveDate.IsZero() {
			record, ok = parseLatestTiingoRecordOnOrBeforeDate(payload, request.canonical, provider.options.Provider, effectiveDate)
		} else {
			record, ok = parseLatestTiingoRecord(payload, request.canonical, provider.options.Provider)
		}
		if ok {
			provider.storeCachedRecord(request, effectiveDate, record)
			return record, true, "", nil
		}
		if len(bytes.TrimSpace(payload)) == 0 || bytes.Equal(bytes.TrimSpace(payload), []byte("[]")) {
			return PriceRecord{}, false, "empty_payload", nil
		}
		return PriceRecord{}, false, "no_matching_trade_date", nil
	}
	if rateLimited > 0 {
		state.markRateLimited()
		return PriceRecord{}, false, "rate_limited", nil
	}
	return PriceRecord{}, false, "no_token_available", nil
}

func readTiingoResponseBody(resp *http.Response) ([]byte, error) {
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1024))
		return nil, nil
	}
	return io.ReadAll(io.LimitReader(resp.Body, 2<<20))
}

func (provider *TiingoPriceProvider) nextTokenSequence() []string {
	tokens := provider.options.Tokens
	if len(tokens) <= 1 {
		return tokens
	}
	provider.tokenMu.Lock()
	start := provider.nextToken % len(tokens)
	provider.nextToken = (provider.nextToken + 1) % len(tokens)
	provider.tokenMu.Unlock()
	sequence := make([]string, 0, len(tokens))
	for offset := 0; offset < len(tokens); offset++ {
		sequence = append(sequence, tokens[(start+offset)%len(tokens)])
	}
	return sequence
}

func (provider *TiingoPriceProvider) waitForRequestSlot(ctx context.Context) error {
	interval := provider.options.RequestInterval
	if interval <= 0 {
		return nil
	}
	provider.throttleMu.Lock()
	now := time.Now()
	wait := time.Duration(0)
	if now.Before(provider.nextRequestAt) {
		wait = provider.nextRequestAt.Sub(now)
		now = provider.nextRequestAt
	}
	provider.nextRequestAt = now.Add(interval)
	provider.throttleMu.Unlock()
	if wait <= 0 {
		return nil
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (provider *TiingoPriceProvider) tiingoRecordCachePath(request tiingoRequest, effectiveDate time.Time) string {
	cacheDir := strings.TrimSpace(provider.options.CacheDir)
	if cacheDir == "" {
		return ""
	}
	date := "latest"
	if !effectiveDate.IsZero() {
		date = effectiveDate.Format(time.DateOnly)
	}
	digest := sha256.Sum256([]byte(provider.options.Provider + "\x00" + request.canonical + "\x00" + request.provider + "\x00" + date))
	return filepath.Join(cacheDir, "tiingo-price-"+hex.EncodeToString(digest[:])+".json")
}

func (provider *TiingoPriceProvider) loadCachedRecord(request tiingoRequest, effectiveDate time.Time) (PriceRecord, bool) {
	path := provider.tiingoRecordCachePath(request, effectiveDate)
	if path == "" {
		return PriceRecord{}, false
	}
	payload, err := os.ReadFile(path)
	if err != nil {
		return PriceRecord{}, false
	}
	var record PriceRecord
	if err := json.Unmarshal(payload, &record); err != nil || record.Symbol == "" || record.TradeDate.IsZero() || record.Source != provider.options.Provider {
		return PriceRecord{}, false
	}
	return record, true
}

func (provider *TiingoPriceProvider) storeCachedRecord(request tiingoRequest, effectiveDate time.Time, record PriceRecord) {
	path := provider.tiingoRecordCachePath(request, effectiveDate)
	if path == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return
	}
	payload, err := json.Marshal(record)
	if err != nil {
		return
	}
	_ = os.WriteFile(path, payload, 0o600)
}

type tiingoDailyPrice struct {
	Date   string      `json:"date"`
	Open   json.Number `json:"open"`
	High   json.Number `json:"high"`
	Low    json.Number `json:"low"`
	Close  json.Number `json:"close"`
	Volume json.Number `json:"volume"`
}

func parseLatestTiingoRecord(payload []byte, canonical string, source string) (PriceRecord, bool) {
	rows, ok := decodeTiingoDailyPrices(payload)
	if !ok {
		return PriceRecord{}, false
	}
	var selected PriceRecord
	for _, row := range rows {
		record, err := tiingoDailyPriceToRecord(row, canonical, source)
		if err != nil {
			continue
		}
		if selected.TradeDate.IsZero() || record.TradeDate.After(selected.TradeDate) {
			selected = record
		}
	}
	if selected.TradeDate.IsZero() {
		return PriceRecord{}, false
	}
	return selected, true
}

func parseLatestTiingoRecordOnOrBeforeDate(payload []byte, canonical string, source string, target time.Time) (PriceRecord, bool) {
	rows, ok := decodeTiingoDailyPrices(payload)
	if !ok {
		return PriceRecord{}, false
	}
	targetDate := target.Format(time.DateOnly)
	var selected PriceRecord
	for _, row := range rows {
		record, err := tiingoDailyPriceToRecord(row, canonical, source)
		if err != nil {
			continue
		}
		date := record.TradeDate.Format(time.DateOnly)
		if date <= targetDate && (selected.TradeDate.IsZero() || record.TradeDate.After(selected.TradeDate)) {
			selected = record
		}
	}
	if selected.TradeDate.IsZero() {
		return PriceRecord{}, false
	}
	return selected, true
}

func decodeTiingoDailyPrices(payload []byte) ([]tiingoDailyPrice, bool) {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	var rows []tiingoDailyPrice
	if err := decoder.Decode(&rows); err != nil || len(rows) == 0 {
		return nil, false
	}
	return rows, true
}

func tiingoDailyPriceToRecord(row tiingoDailyPrice, canonical string, source string) (PriceRecord, error) {
	dateText := strings.TrimSpace(row.Date)
	if len(dateText) >= len(time.DateOnly) {
		dateText = dateText[:len(time.DateOnly)]
	}
	newYork, err := time.LoadLocation("America/New_York")
	if err != nil {
		return PriceRecord{}, err
	}
	tradeDate, err := parseCivilDate(dateText, newYork)
	if err != nil {
		return PriceRecord{}, err
	}
	openMicros, err := parsePriceMicros(row.Open.String())
	if err != nil {
		return PriceRecord{}, err
	}
	highMicros, err := parsePriceMicros(row.High.String())
	if err != nil {
		return PriceRecord{}, err
	}
	lowMicros, err := parsePriceMicros(row.Low.String())
	if err != nil {
		return PriceRecord{}, err
	}
	closeMicros, err := parsePriceMicros(row.Close.String())
	if err != nil {
		return PriceRecord{}, err
	}
	volume, err := parseTiingoVolume(row.Volume)
	if err != nil {
		return PriceRecord{}, err
	}
	record := PriceRecord{
		Symbol:      strings.ToUpper(strings.TrimSpace(canonical)),
		TradeDate:   tradeDate,
		OpenMicros:  openMicros,
		HighMicros:  highMicros,
		LowMicros:   lowMicros,
		CloseMicros: closeMicros,
		Volume:      volume,
		Currency:    "USD",
		Adjusted:    false,
		Source:      source,
	}
	if err := validateOHLC(record); err != nil {
		return PriceRecord{}, err
	}
	return record, nil
}

func parseTiingoVolume(value json.Number) (int64, error) {
	if strings.TrimSpace(value.String()) == "" {
		return 0, nil
	}
	volume, err := strconv.ParseInt(value.String(), 10, 64)
	if err == nil && volume >= 0 {
		return volume, nil
	}
	floatVolume, err := strconv.ParseFloat(value.String(), 64)
	if err != nil || floatVolume < 0 {
		return 0, errors.New("invalid tiingo volume")
	}
	return int64(floatVolume), nil
}

func latestPriceRecordDate(records []PriceRecord) time.Time {
	var latest time.Time
	for _, record := range records {
		if latest.IsZero() || record.TradeDate.After(latest) {
			latest = record.TradeDate
		}
	}
	return latest
}

func filterPriceRecordsByDate(records []PriceRecord, date time.Time) []PriceRecord {
	filtered := records[:0]
	target := date.Format(time.DateOnly)
	for _, record := range records {
		if record.TradeDate.Format(time.DateOnly) == target {
			filtered = append(filtered, record)
		}
	}
	return filtered
}

func hashTiingoPriceRecords(records []PriceRecord) string {
	type row struct {
		Symbol      string `json:"symbol"`
		TradeDate   string `json:"trade_date"`
		OpenMicros  int64  `json:"open_micros"`
		HighMicros  int64  `json:"high_micros"`
		LowMicros   int64  `json:"low_micros"`
		CloseMicros int64  `json:"close_micros"`
		Volume      int64  `json:"volume"`
		Source      string `json:"source"`
	}
	rows := make([]row, 0, len(records))
	for _, record := range records {
		rows = append(rows, row{
			Symbol:      record.Symbol,
			TradeDate:   record.TradeDate.Format(time.DateOnly),
			OpenMicros:  record.OpenMicros,
			HighMicros:  record.HighMicros,
			LowMicros:   record.LowMicros,
			CloseMicros: record.CloseMicros,
			Volume:      record.Volume,
			Source:      record.Source,
		})
	}
	payload, _ := json.Marshal(rows)
	hash := sha256.Sum256(payload)
	return hex.EncodeToString(hash[:])
}
