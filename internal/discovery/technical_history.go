package discovery

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"
)

const (
	// Roughly 320 calendar days usually contains 200 US trading days, enough
	// to calculate MA200 without requesting multiple years of history.
	defaultTechnicalHistoryLookbackDays = 320
	minimumTechnicalHistoryLookbackDays = technicalMA200LookbackDays
	maximumTechnicalHistoryLookbackDays = 400
)

// TechnicalHistoryBackfillResult describes a manual, one-time price-history
// warm-up. It does not publish a new market batch or change candidate scores.
type TechnicalHistoryBackfillResult struct {
	BatchID               string                    `json:"batch_id"`
	EffectiveDate         string                    `json:"effective_date"`
	LookbackCalendarDays  int                       `json:"lookback_calendar_days"`
	CandidateCount        int                       `json:"candidate_count"`
	AlreadyReadyCount     int                       `json:"already_ready_count"`
	RequestedCount        int                       `json:"requested_count"`
	BenchmarkTicker       string                    `json:"benchmark_ticker"`
	BenchmarkReady        bool                      `json:"benchmark_ready"`
	BenchmarkRequested    bool                      `json:"benchmark_requested"`
	BenchmarkStatus       string                    `json:"benchmark_status"`
	BenchmarkSampleDays   int                       `json:"benchmark_sample_days"`
	BenchmarkRequiredDays int                       `json:"benchmark_required_days"`
	BenchmarkLatestDate   string                    `json:"benchmark_latest_date"`
	RecordCount           int                       `json:"record_count"`
	PersistedCount        int                       `json:"persisted_count"`
	SourceRecordCounts    map[string]int            `json:"source_record_counts"`
	Failures              []TechnicalHistoryFailure `json:"failures"`
	Warnings              []string                  `json:"warnings"`
}

type TechnicalHistoryFailure struct {
	Ticker string `json:"ticker"`
	Reason string `json:"reason"`
}

type benchmarkHistoryReadiness struct {
	Status     string
	SampleDays int
	Required   int
	LatestDate string
	Ready      bool
}

// TickerTechnicalHistory is the local end-of-day history for an arbitrary
// watch target. Unlike a candidate detail it has no batch as-of constraint:
// it intentionally presents the freshest locally persisted price series.
type TickerTechnicalHistory struct {
	Ticker    string                         `json:"ticker"`
	Technical CandidateTechnicalAnalysis     `json:"technical"`
	History   []CandidateTechnicalHistoryRow `json:"history"`
}

// GetTickerTechnicalHistory returns the local daily OHLCV history used
// by a watch-target detail. It works for stocks and ETFs, including targets
// that are outside the current small-cap candidate universe.
func GetTickerTechnicalHistory(ctx context.Context, db *gorm.DB, ticker string) (TickerTechnicalHistory, error) {
	result := TickerTechnicalHistory{Ticker: strings.ToUpper(strings.TrimSpace(ticker)), History: []CandidateTechnicalHistoryRow{}}
	if db == nil {
		return result, errors.New("database is required")
	}
	if result.Ticker == "" {
		return result, errors.New("ticker is required")
	}
	rows, err := technicalPriceHistoryForSymbol(ctx, db, result.Ticker, "", technicalDetailHistoryDays, nil)
	if err != nil {
		return result, err
	}
	result.Technical = buildCandidateTechnicalAnalysis(rows)
	if err := applyTickerCorporateActionReview(ctx, db, result.Ticker, rows, &result.Technical); err != nil {
		return result, err
	}
	if err := hydrateTickerTradeSetupStatusSince(ctx, db, result.Ticker, &result.Technical); err != nil {
		return result, err
	}
	result.History = candidateTechnicalHistoryRows(rows)
	return result, nil
}

func applyTickerCorporateActionReview(ctx context.Context, db *gorm.DB, ticker string, rows []PriceSnapshot, technical *CandidateTechnicalAnalysis) error {
	if technical == nil || !hasUnadjustedPriceRows(rows) {
		return nil
	}
	var security Security
	err := db.WithContext(ctx).Table("securities").
		Select("securities.*").Joins("JOIN listings ON listings.security_id = securities.id").
		Where("listings.ticker = ?", strings.ToUpper(strings.TrimSpace(ticker))).Order("listings.valid_from DESC").First(&security).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	var actions []CapitalRiskSnapshot
	if err := db.WithContext(ctx).Where("security_id = ? AND active = ? AND kind = ?", security.ID, true, CapitalEventReverseSplit).Find(&actions).Error; err != nil {
		return err
	}
	if len(actions) == 0 {
		return nil
	}
	technical.Status = TechnicalStatusCorporateActionReview
	technical.AdjustmentReview = PriceAdjustmentReview{Status: "review_required", QualityStatus: QualityStatusConflict, EventKinds: []string{CapitalEventReverseSplit}, Detail: "存在拆股/合股事件，且本地价格未确认复权；技术信号已暂停"}
	technical.Signals = []CandidateTechnicalSignal{}
	technical.TradeSetup = unavailableCandidateTradeSetup(TechnicalStatusCorporateActionReview)
	return nil
}

// BackfillTickerTechnicalHistory persists a one-time local EOD history for a
// single watch target. A regular candidate sync remains untouched, so opening
// a target detail never spends market-data quota implicitly.
func BackfillTickerTechnicalHistory(ctx context.Context, db *gorm.DB, provider HistoricalPriceProvider, ticker string, now time.Time, lookbackDays int) (TechnicalHistoryBackfillResult, error) {
	result := TechnicalHistoryBackfillResult{SourceRecordCounts: map[string]int{}, CandidateCount: 1}
	if db == nil {
		return result, errors.New("database is required")
	}
	if provider == nil {
		return result, errors.New("historical price provider is required")
	}
	if ctx == nil {
		return result, errors.New("context is required")
	}
	ticker = strings.ToUpper(strings.TrimSpace(ticker))
	if ticker == "" {
		return result, errors.New("ticker is required")
	}
	result.LookbackCalendarDays = normalizeTechnicalHistoryLookbackDays(lookbackDays)
	newYork, err := time.LoadLocation("America/New_York")
	if err != nil {
		return result, err
	}
	result.EffectiveDate = now.In(newYork).Format(time.DateOnly)
	result.RequestedCount = 1
	records, err := provider.LoadHistory(ctx, []Listing{{Ticker: ticker, ProviderTicker: ticker, MappingStatus: MappingStatusCurrent}}, result.EffectiveDate, result.LookbackCalendarDays)
	if err != nil {
		return result, err
	}
	snapshots := technicalHistorySnapshots(records, result.EffectiveDate, result.SourceRecordCounts)
	result.RecordCount = len(snapshots)
	if len(snapshots) == 0 {
		return result, errors.New("history provider returned no usable price records")
	}
	if err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return persistPriceSnapshotsInBatches(tx, snapshots)
	}); err != nil {
		return result, err
	}
	result.PersistedCount = len(snapshots)
	return result, nil
}

func normalizeTechnicalHistoryLookbackDays(value int) int {
	if value == 0 {
		return defaultTechnicalHistoryLookbackDays
	}
	if value < minimumTechnicalHistoryLookbackDays {
		return minimumTechnicalHistoryLookbackDays
	}
	if value > maximumTechnicalHistoryLookbackDays {
		return maximumTechnicalHistoryLookbackDays
	}
	return value
}

func BackfillCandidateTechnicalHistory(ctx context.Context, db *gorm.DB, provider HistoricalPriceProvider, now time.Time, lookbackDays int) (TechnicalHistoryBackfillResult, error) {
	result := TechnicalHistoryBackfillResult{SourceRecordCounts: map[string]int{}, BenchmarkTicker: "IWM", Failures: []TechnicalHistoryFailure{}, Warnings: []string{}}
	if db == nil {
		return result, errors.New("database is required")
	}
	if provider == nil {
		return result, errors.New("historical price provider is required")
	}
	if ctx == nil {
		return result, errors.New("context is required")
	}
	batch, ok, err := currentPublishedPrescreenBatch(ctx, db)
	if err != nil {
		return result, err
	}
	if !ok {
		return result, errors.New("no current published prescreen batch")
	}
	result.BatchID = batch.BatchID
	result.LookbackCalendarDays = normalizeTechnicalHistoryLookbackDays(lookbackDays)
	newYork, err := time.LoadLocation("America/New_York")
	if err != nil {
		return result, err
	}
	result.EffectiveDate = now.In(newYork).Format(time.DateOnly)

	var scores []CandidateScoreSnapshot
	if err := db.WithContext(ctx).
		Where("batch_id = ? AND grade IN ?", batch.BatchID, []string{CandidateGradeA, CandidateGradeB}).
		Order("ticker ASC").Find(&scores).Error; err != nil {
		return result, err
	}
	result.CandidateCount = len(scores)
	requiredSamples := technicalHistorySamplesRequired
	if result.LookbackCalendarDays >= defaultTechnicalHistoryLookbackDays {
		requiredSamples = technicalMA200LookbackDays
	}
	result.BenchmarkRequiredDays = requiredSamples
	listings, readyCount, benchmarkReady, err := candidateTechnicalHistoryListings(ctx, db, batch, scores, requiredSamples)
	if err != nil {
		return result, err
	}
	result.AlreadyReadyCount = readyCount
	result.BenchmarkReady = benchmarkReady
	result.RequestedCount = len(listings)
	for _, listing := range listings {
		if strings.EqualFold(listing.Ticker, result.BenchmarkTicker) {
			result.BenchmarkRequested = true
		}
	}
	if len(listings) == 0 {
		readiness, readinessErr := loadBenchmarkHistoryReadiness(ctx, db, result.BenchmarkTicker, requiredSamples, batch.EffectiveDate)
		if readinessErr != nil {
			return result, readinessErr
		}
		applyBenchmarkReadiness(&result, readiness)
		return result, nil
	}

	benchmarkListings, candidateListings := splitBenchmarkListings(listings, result.BenchmarkTicker)
	records := make([]PriceRecord, 0)
	// The benchmark is deliberately fetched in its own first request. A large
	// candidate batch can no longer consume the request budget while silently
	// leaving the comparison series empty.
	for _, group := range [][]Listing{benchmarkListings, candidateListings} {
		if len(group) == 0 {
			continue
		}
		rows, loadErr := provider.LoadHistory(ctx, group, result.EffectiveDate, result.LookbackCalendarDays)
		if loadErr != nil {
			for _, listing := range group {
				result.Failures = append(result.Failures, TechnicalHistoryFailure{Ticker: strings.ToUpper(strings.TrimSpace(listing.Ticker)), Reason: "provider_request_failed"})
			}
			result.Warnings = append(result.Warnings, "行情源未能返回 "+technicalHistoryTickerList(group)+" 的技术历史")
			continue
		}
		filtered, missing := technicalHistoryRecordsForListings(rows, group)
		records = append(records, filtered...)
		for _, ticker := range missing {
			result.Failures = append(result.Failures, TechnicalHistoryFailure{Ticker: ticker, Reason: "no_usable_records"})
		}
		if len(missing) > 0 {
			result.Warnings = append(result.Warnings, "行情源未返回可用日线："+strings.Join(missing, "、"))
		}
	}
	snapshots := technicalHistorySnapshots(records, result.EffectiveDate, result.SourceRecordCounts)
	result.RecordCount = len(snapshots)
	if len(snapshots) > 0 {
		if err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			return persistPriceSnapshotsInBatches(tx, snapshots)
		}); err != nil {
			return result, err
		}
		result.PersistedCount = len(snapshots)
	}
	readiness, err := loadBenchmarkHistoryReadiness(ctx, db, result.BenchmarkTicker, requiredSamples, batch.EffectiveDate)
	if err != nil {
		return result, err
	}
	applyBenchmarkReadiness(&result, readiness)
	if !result.BenchmarkReady {
		result.Warnings = append(result.Warnings, "IWM 基准历史未就绪，候选相对收益与效果验证保持降级状态")
	}
	return result, nil
}

func splitBenchmarkListings(listings []Listing, benchmark string) ([]Listing, []Listing) {
	benchmarkRows := make([]Listing, 0, 1)
	candidates := make([]Listing, 0, len(listings))
	for _, listing := range listings {
		if strings.EqualFold(strings.TrimSpace(listing.Ticker), benchmark) {
			benchmarkRows = append(benchmarkRows, listing)
			continue
		}
		candidates = append(candidates, listing)
	}
	return benchmarkRows, candidates
}

func technicalHistoryRecordsForListings(records []PriceRecord, listings []Listing) ([]PriceRecord, []string) {
	expected := make(map[string]struct{}, len(listings))
	for _, listing := range listings {
		if ticker := strings.ToUpper(strings.TrimSpace(listing.Ticker)); ticker != "" {
			expected[ticker] = struct{}{}
		}
	}
	covered := make(map[string]struct{}, len(expected))
	filtered := make([]PriceRecord, 0, len(records))
	for _, record := range records {
		ticker := strings.ToUpper(strings.TrimSpace(record.Symbol))
		if _, ok := expected[ticker]; !ok {
			continue
		}
		filtered = append(filtered, record)
		if record.CloseMicros > 0 {
			covered[ticker] = struct{}{}
		}
	}
	missing := make([]string, 0)
	for ticker := range expected {
		if _, ok := covered[ticker]; !ok {
			missing = append(missing, ticker)
		}
	}
	sort.Strings(missing)
	return filtered, missing
}

func technicalHistoryTickerList(listings []Listing) string {
	tickers := make([]string, 0, len(listings))
	for _, listing := range listings {
		if ticker := strings.ToUpper(strings.TrimSpace(listing.Ticker)); ticker != "" {
			tickers = append(tickers, ticker)
		}
	}
	return strings.Join(tickers, "、")
}

func loadBenchmarkHistoryReadiness(ctx context.Context, db *gorm.DB, ticker string, required int, expectedDate string) (benchmarkHistoryReadiness, error) {
	result := benchmarkHistoryReadiness{Status: "missing", Required: required}
	var rows []PriceSnapshot
	if err := db.WithContext(ctx).Where("symbol = ? AND quality_status = ? AND close_micros > 0", strings.ToUpper(strings.TrimSpace(ticker)), QualityStatusValid).Order("trade_date ASC").Find(&rows).Error; err != nil {
		return result, err
	}
	dates := map[string]struct{}{}
	for _, row := range rows {
		if !priceSnapshotHasOHLC(row) {
			continue
		}
		date := row.TradeDate.Format(time.DateOnly)
		dates[date] = struct{}{}
		if date > result.LatestDate {
			result.LatestDate = date
		}
	}
	result.SampleDays = len(dates)
	switch {
	case result.SampleDays == 0:
		result.Status = "missing"
	case result.SampleDays < required:
		result.Status = "insufficient"
	case strings.TrimSpace(expectedDate) != "" && result.LatestDate < expectedDate:
		result.Status = "stale"
	default:
		result.Status = "ready"
		result.Ready = true
	}
	return result, nil
}

func applyBenchmarkReadiness(result *TechnicalHistoryBackfillResult, readiness benchmarkHistoryReadiness) {
	result.BenchmarkStatus = readiness.Status
	result.BenchmarkSampleDays = readiness.SampleDays
	result.BenchmarkRequiredDays = readiness.Required
	result.BenchmarkLatestDate = readiness.LatestDate
	result.BenchmarkReady = readiness.Ready
}

func candidateTechnicalHistoryListings(ctx context.Context, db *gorm.DB, batch UniverseBatch, scores []CandidateScoreSnapshot, requiredSamples int) ([]Listing, int, bool, error) {
	if len(scores) == 0 {
		return []Listing{{Ticker: "IWM", ProviderTicker: "IWM", MappingStatus: MappingStatusCurrent}}, 0, false, nil
	}
	securityIDs := make([]uint, 0, len(scores))
	tickers := make([]string, 0, len(scores))
	for _, score := range scores {
		securityIDs = append(securityIDs, score.SecurityID)
		tickers = append(tickers, strings.ToUpper(strings.TrimSpace(score.Ticker)))
	}
	tickers = append(tickers, "IWM")
	identityBatchID := strings.TrimSpace(batch.UniverseSourceVersion)
	if identityBatchID == "" {
		identityBatchID = batch.BatchID
	}
	var identities []SecurityBatchIdentity
	if err := db.WithContext(ctx).Where("batch_id = ? AND security_id IN ?", identityBatchID, securityIDs).Find(&identities).Error; err != nil {
		return nil, 0, false, err
	}
	identityBySecurity := make(map[uint]SecurityBatchIdentity, len(identities))
	for _, identity := range identities {
		identityBySecurity[identity.SecurityID] = identity
	}
	var prices []PriceSnapshot
	if err := db.WithContext(ctx).Where("symbol IN ? AND quality_status = ?", tickers, QualityStatusValid).Find(&prices).Error; err != nil {
		return nil, 0, false, err
	}
	datesByTicker := map[string]map[string]struct{}{}
	for _, price := range prices {
		// A legacy close-only row is not sufficient for standard KDJ or candle
		// rendering. Keep requesting the symbol until the required OHLC window
		// has been populated by a current historical provider.
		if !priceSnapshotHasOHLC(price) {
			continue
		}
		ticker := strings.ToUpper(strings.TrimSpace(price.Symbol))
		if datesByTicker[ticker] == nil {
			datesByTicker[ticker] = map[string]struct{}{}
		}
		datesByTicker[ticker][price.TradeDate.Format(time.DateOnly)] = struct{}{}
	}
	seen := map[string]struct{}{}
	listings := make([]Listing, 0, len(scores))
	ready := 0
	for _, score := range scores {
		ticker := strings.ToUpper(strings.TrimSpace(score.Ticker))
		if ticker == "" {
			continue
		}
		if len(datesByTicker[ticker]) >= requiredSamples {
			ready++
			continue
		}
		if _, exists := seen[ticker]; exists {
			continue
		}
		seen[ticker] = struct{}{}
		listing := Listing{SecurityID: score.SecurityID, Ticker: ticker, MappingStatus: MappingStatusCurrent}
		if identity, ok := identityBySecurity[score.SecurityID]; ok {
			listing.ProviderTicker = identity.ProviderTicker
			listing.Exchange = identity.Exchange
			listing.MappingStatus = identity.MappingStatus
		}
		listings = append(listings, listing)
	}
	benchmarkLatest := ""
	for date := range datesByTicker["IWM"] {
		if date > benchmarkLatest {
			benchmarkLatest = date
		}
	}
	// Count alone is not enough: IWM must also cover the current published
	// market date so future cohort outcomes continue to accumulate each day.
	benchmarkReady := len(datesByTicker["IWM"]) >= requiredSamples && (strings.TrimSpace(batch.EffectiveDate) == "" || benchmarkLatest >= batch.EffectiveDate)
	if !benchmarkReady {
		listings = append(listings, Listing{Ticker: "IWM", ProviderTicker: "IWM", MappingStatus: MappingStatusCurrent})
	}
	sort.Slice(listings, func(i, j int) bool { return listings[i].Ticker < listings[j].Ticker })
	return listings, ready, benchmarkReady, nil
}

func technicalHistorySnapshots(records []PriceRecord, effectiveDate string, sourceCounts map[string]int) []PriceSnapshot {
	byKey := map[string]PriceSnapshot{}
	for _, record := range records {
		if strings.TrimSpace(record.Symbol) == "" || strings.TrimSpace(record.Source) == "" || record.TradeDate.IsZero() || record.CloseMicros <= 0 {
			continue
		}
		record.Symbol = strings.ToUpper(strings.TrimSpace(record.Symbol))
		record.Source = strings.ToLower(strings.TrimSpace(record.Source))
		key := record.Source + "\x00" + record.Symbol + "\x00" + record.TradeDate.Format(time.DateOnly)
		byKey[key] = PriceSnapshot{
			Source:        record.Source,
			SourceVersion: record.Source + ":technical-history:" + effectiveDate,
			Symbol:        record.Symbol,
			TradeDate:     record.TradeDate,
			OpenMicros:    record.OpenMicros,
			HighMicros:    record.HighMicros,
			LowMicros:     record.LowMicros,
			CloseMicros:   record.CloseMicros,
			Volume:        record.Volume,
			Currency:      stringOrDefault(record.Currency, "USD"),
			Adjusted:      record.Adjusted,
			QualityStatus: QualityStatusValid,
		}
	}
	keys := make([]string, 0, len(byKey))
	for key := range byKey {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	snapshots := make([]PriceSnapshot, 0, len(keys))
	for _, key := range keys {
		snapshot := byKey[key]
		snapshots = append(snapshots, snapshot)
		sourceCounts[snapshot.Source]++
	}
	return snapshots
}

// PersistTechnicalPriceHistory stores local end-of-day records without
// publishing a candidate batch. It is shared by manual watch-target history
// refreshes and the scheduled watch-target daily-close task, so both detail
// panels read exactly the same durable PriceSnapshot history.
func PersistTechnicalPriceHistory(ctx context.Context, db *gorm.DB, records []PriceRecord, sourceVersion string) (int, error) {
	if db == nil {
		return 0, errors.New("database is required")
	}
	sourceVersion = strings.TrimSpace(sourceVersion)
	if sourceVersion == "" {
		return 0, errors.New("price history source version is required")
	}
	snapshots := make([]PriceSnapshot, 0, len(records))
	for _, record := range records {
		if strings.TrimSpace(record.Symbol) == "" || strings.TrimSpace(record.Source) == "" || record.TradeDate.IsZero() || record.CloseMicros <= 0 {
			continue
		}
		snapshots = append(snapshots, PriceSnapshot{
			Source: strings.ToLower(strings.TrimSpace(record.Source)), SourceVersion: sourceVersion,
			Symbol: strings.ToUpper(strings.TrimSpace(record.Symbol)), TradeDate: record.TradeDate,
			OpenMicros: record.OpenMicros, HighMicros: record.HighMicros, LowMicros: record.LowMicros,
			CloseMicros: record.CloseMicros, Volume: record.Volume, Currency: stringOrDefault(record.Currency, "USD"),
			Adjusted: record.Adjusted, QualityStatus: QualityStatusValid,
		})
	}
	if len(snapshots) == 0 {
		return 0, errors.New("no usable price history records")
	}
	if err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return persistPriceSnapshotsInBatches(tx, snapshots)
	}); err != nil {
		return 0, err
	}
	return len(snapshots), nil
}
