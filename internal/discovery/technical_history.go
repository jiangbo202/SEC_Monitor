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
	BatchID              string         `json:"batch_id"`
	EffectiveDate        string         `json:"effective_date"`
	LookbackCalendarDays int            `json:"lookback_calendar_days"`
	CandidateCount       int            `json:"candidate_count"`
	AlreadyReadyCount    int            `json:"already_ready_count"`
	RequestedCount       int            `json:"requested_count"`
	RecordCount          int            `json:"record_count"`
	PersistedCount       int            `json:"persisted_count"`
	SourceRecordCounts   map[string]int `json:"source_record_counts"`
}

// TickerTechnicalHistory is the local end-of-day history for an arbitrary
// watch target. Unlike a candidate detail it has no batch as-of constraint:
// it intentionally presents the freshest locally persisted price series.
type TickerTechnicalHistory struct {
	Ticker    string                         `json:"ticker"`
	Technical CandidateTechnicalAnalysis     `json:"technical"`
	History   []CandidateTechnicalHistoryRow `json:"history"`
}

// GetTickerTechnicalHistory returns the local daily close/volume history used
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
	if err := hydrateTickerTradeSetupStatusSince(ctx, db, result.Ticker, &result.Technical); err != nil {
		return result, err
	}
	result.History = candidateTechnicalHistoryRows(rows)
	return result, nil
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
	result := TechnicalHistoryBackfillResult{SourceRecordCounts: map[string]int{}}
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
	listings, readyCount, err := candidateTechnicalHistoryListings(ctx, db, batch, scores, requiredSamples)
	if err != nil {
		return result, err
	}
	result.AlreadyReadyCount = readyCount
	result.RequestedCount = len(listings)
	if len(listings) == 0 {
		return result, nil
	}

	records, err := provider.LoadHistory(ctx, listings, result.EffectiveDate, result.LookbackCalendarDays)
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

func candidateTechnicalHistoryListings(ctx context.Context, db *gorm.DB, batch UniverseBatch, scores []CandidateScoreSnapshot, requiredSamples int) ([]Listing, int, error) {
	if len(scores) == 0 {
		return []Listing{}, 0, nil
	}
	securityIDs := make([]uint, 0, len(scores))
	tickers := make([]string, 0, len(scores))
	for _, score := range scores {
		securityIDs = append(securityIDs, score.SecurityID)
		tickers = append(tickers, strings.ToUpper(strings.TrimSpace(score.Ticker)))
	}
	identityBatchID := strings.TrimSpace(batch.UniverseSourceVersion)
	if identityBatchID == "" {
		identityBatchID = batch.BatchID
	}
	var identities []SecurityBatchIdentity
	if err := db.WithContext(ctx).Where("batch_id = ? AND security_id IN ?", identityBatchID, securityIDs).Find(&identities).Error; err != nil {
		return nil, 0, err
	}
	identityBySecurity := make(map[uint]SecurityBatchIdentity, len(identities))
	for _, identity := range identities {
		identityBySecurity[identity.SecurityID] = identity
	}
	var prices []PriceSnapshot
	if err := db.WithContext(ctx).Where("symbol IN ? AND quality_status = ?", tickers, QualityStatusValid).Find(&prices).Error; err != nil {
		return nil, 0, err
	}
	datesByTicker := map[string]map[string]struct{}{}
	for _, price := range prices {
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
	sort.Slice(listings, func(i, j int) bool { return listings[i].Ticker < listings[j].Ticker })
	return listings, ready, nil
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
