package discovery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	TechnicalHistoryRetryBackoff  = "backoff"
	TechnicalHistoryRetryDeferred = "deferred"
	TechnicalHistoryRetryResolved = "resolved"
)

// TechnicalHistoryRetryState is the durable, per-symbol recovery checkpoint
// for historical OHLCV. It prevents one unavailable symbol from forcing the
// whole candidate universe to restart on every scheduled run.
type TechnicalHistoryRetryState struct {
	Ticker          string     `json:"ticker" gorm:"size:32;primaryKey;autoIncrement:false"`
	BatchID         string     `json:"batch_id" gorm:"size:64;index"`
	Status          string     `json:"status" gorm:"size:16;index"`
	Reason          string     `json:"reason" gorm:"size:64;index"`
	FailureCount    int        `json:"failure_count"`
	SampleDays      int        `json:"sample_days"`
	RequiredDays    int        `json:"required_days"`
	LatestTradeDate string     `json:"latest_trade_date" gorm:"size:10"`
	LastAttemptAt   time.Time  `json:"last_attempt_at"`
	NextRetryAt     *time.Time `json:"next_retry_at,omitempty" gorm:"index"`
	LastSuccessAt   *time.Time `json:"last_success_at,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

type technicalHistoryCoverage struct {
	SampleDays int
	LatestDate string
}

func filterTechnicalHistoryRetries(ctx context.Context, db *gorm.DB, batchID string, listings []Listing, now time.Time, force bool) ([]Listing, int, error) {
	if len(listings) == 0 {
		return listings, 0, nil
	}
	tickers := make([]string, 0, len(listings))
	for _, listing := range listings {
		tickers = append(tickers, strings.ToUpper(strings.TrimSpace(listing.Ticker)))
	}
	// Carry unresolved checkpoints into the current published universe. This
	// keeps health counts scoped to today's candidate set without discarding the
	// ticker's accumulated failure evidence.
	if err := db.WithContext(ctx).Model(&TechnicalHistoryRetryState{}).
		Where("ticker IN ? AND status <> ?", tickers, TechnicalHistoryRetryResolved).
		Update("batch_id", batchID).Error; err != nil {
		return nil, 0, err
	}
	if force {
		return listings, 0, nil
	}
	var states []TechnicalHistoryRetryState
	if err := db.WithContext(ctx).Where("ticker IN ? AND status <> ?", tickers, TechnicalHistoryRetryResolved).Find(&states).Error; err != nil {
		return nil, 0, err
	}
	stateByTicker := make(map[string]TechnicalHistoryRetryState, len(states))
	for _, state := range states {
		stateByTicker[state.Ticker] = state
	}
	eligible := make([]Listing, 0, len(listings))
	deferred := 0
	for _, listing := range listings {
		state, ok := stateByTicker[strings.ToUpper(strings.TrimSpace(listing.Ticker))]
		if ok && state.NextRetryAt != nil && state.NextRetryAt.After(now.UTC()) {
			deferred++
			continue
		}
		eligible = append(eligible, listing)
	}
	return eligible, deferred, nil
}

func loadTechnicalHistoryCoverage(ctx context.Context, db *gorm.DB, tickers []string) (map[string]technicalHistoryCoverage, error) {
	result := make(map[string]technicalHistoryCoverage, len(tickers))
	if len(tickers) == 0 {
		return result, nil
	}
	var rows []PriceSnapshot
	if err := db.WithContext(ctx).Where("symbol IN ? AND quality_status = ? AND close_micros > 0", tickers, QualityStatusValid).Find(&rows).Error; err != nil {
		return nil, err
	}
	dates := make(map[string]map[string]struct{}, len(tickers))
	for _, row := range rows {
		if !priceSnapshotHasOHLC(row) {
			continue
		}
		ticker := strings.ToUpper(strings.TrimSpace(row.Symbol))
		if dates[ticker] == nil {
			dates[ticker] = map[string]struct{}{}
		}
		date := row.TradeDate.Format(time.DateOnly)
		dates[ticker][date] = struct{}{}
		coverage := result[ticker]
		if date > coverage.LatestDate {
			coverage.LatestDate = date
		}
		result[ticker] = coverage
	}
	for ticker, tickerDates := range dates {
		coverage := result[ticker]
		coverage.SampleDays = len(tickerDates)
		result[ticker] = coverage
	}
	return result, nil
}

// resolveSatisfiedTechnicalHistoryRetries closes checkpoints that became
// complete through the ordinary daily-price task. Without this reconciliation
// a ticker could reach the required depth without being requested again, while
// its old warning remained open forever.
func resolveSatisfiedTechnicalHistoryRetries(ctx context.Context, db *gorm.DB, batchID string, tickers []string, required int, expectedDate string, resolvedAt time.Time) error {
	if len(tickers) == 0 {
		return nil
	}
	var states []TechnicalHistoryRetryState
	if err := db.WithContext(ctx).Where("ticker IN ? AND status <> ?", tickers, TechnicalHistoryRetryResolved).Find(&states).Error; err != nil {
		return err
	}
	if len(states) == 0 {
		return nil
	}
	stateTickers := make([]string, 0, len(states))
	for _, state := range states {
		stateTickers = append(stateTickers, state.Ticker)
	}
	coverageByTicker, err := loadTechnicalHistoryCoverage(ctx, db, stateTickers)
	if err != nil {
		return err
	}
	for _, state := range states {
		coverage := coverageByTicker[state.Ticker]
		if coverage.SampleDays < required || (strings.TrimSpace(expectedDate) != "" && coverage.LatestDate < expectedDate) {
			continue
		}
		if err := resolveTechnicalHistoryRetry(ctx, db, batchID, state.Ticker, coverage, required, resolvedAt); err != nil {
			return err
		}
	}
	return nil
}

func recordTechnicalHistoryRetry(ctx context.Context, db *gorm.DB, batchID, ticker, reason string, coverage technicalHistoryCoverage, required int, attemptedAt time.Time) (TechnicalHistoryRetryState, error) {
	ticker = strings.ToUpper(strings.TrimSpace(ticker))
	attemptedAt = attemptedAt.UTC()
	var prior TechnicalHistoryRetryState
	err := db.WithContext(ctx).Where("ticker = ?", ticker).First(&prior).Error
	if err != nil && err != gorm.ErrRecordNotFound {
		return TechnicalHistoryRetryState{}, err
	}
	failures := 1
	if err == nil && prior.Status != TechnicalHistoryRetryResolved {
		failures = prior.FailureCount + 1
	}
	delay := technicalHistoryRetryDelay(reason, failures)
	nextRetry := attemptedAt.Add(delay)
	status := TechnicalHistoryRetryBackoff
	if failures >= 5 {
		status = TechnicalHistoryRetryDeferred
	}
	state := TechnicalHistoryRetryState{
		Ticker: ticker, BatchID: batchID, Status: status, Reason: reason, FailureCount: failures,
		SampleDays: coverage.SampleDays, RequiredDays: required, LatestTradeDate: coverage.LatestDate,
		LastAttemptAt: attemptedAt, NextRetryAt: &nextRetry,
	}
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "ticker"}},
			DoUpdates: clause.AssignmentColumns([]string{"batch_id", "status", "reason", "failure_count", "sample_days", "required_days", "latest_trade_date", "last_attempt_at", "next_retry_at", "updated_at"}),
		}).Create(&state).Error; err != nil {
			return err
		}
		if err := resolveTechnicalHistoryIncidents(tx, ticker, reason, attemptedAt); err != nil {
			return err
		}
		return upsertTechnicalHistoryIncident(tx, state, attemptedAt)
	})
	return state, err
}

func resolveTechnicalHistoryRetry(ctx context.Context, db *gorm.DB, batchID, ticker string, coverage technicalHistoryCoverage, required int, resolvedAt time.Time) error {
	ticker = strings.ToUpper(strings.TrimSpace(ticker))
	resolvedAt = resolvedAt.UTC()
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		state := TechnicalHistoryRetryState{
			Ticker: ticker, BatchID: batchID, Status: TechnicalHistoryRetryResolved,
			SampleDays: coverage.SampleDays, RequiredDays: required, LatestTradeDate: coverage.LatestDate,
			LastAttemptAt: resolvedAt, LastSuccessAt: &resolvedAt,
		}
		if err := tx.Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "ticker"}},
			DoUpdates: clause.Assignments(map[string]interface{}{
				"batch_id": batchID, "status": TechnicalHistoryRetryResolved, "reason": "", "failure_count": 0,
				"sample_days": coverage.SampleDays, "required_days": required, "latest_trade_date": coverage.LatestDate,
				"last_attempt_at": resolvedAt, "next_retry_at": nil, "last_success_at": resolvedAt, "updated_at": resolvedAt,
			}),
		}).Create(&state).Error; err != nil {
			return err
		}
		return tx.Model(&DataQualityIncident{}).
			Where("domain = ? AND entity_key = ? AND status = ?", "technical_history", ticker, DataQualityIncidentOpen).
			Updates(map[string]interface{}{"status": DataQualityIncidentResolved, "retryable": false, "resolved_at": resolvedAt, "updated_at": resolvedAt}).Error
	})
}

func technicalHistoryRetryDelay(reason string, failures int) time.Duration {
	if failures < 1 {
		failures = 1
	}
	base, capDelay := 30*time.Minute, 24*time.Hour
	switch reason {
	case "no_usable_records":
		base, capDelay = 12*time.Hour, 7*24*time.Hour
	case "insufficient_history":
		base, capDelay = 24*time.Hour, 7*24*time.Hour
	case "stale_history":
		base, capDelay = time.Hour, 24*time.Hour
	}
	delay := base
	for attempt := 1; attempt < failures && delay < capDelay; attempt++ {
		delay *= 2
	}
	if delay > capDelay {
		return capDelay
	}
	return delay
}

func resolveTechnicalHistoryIncidents(tx *gorm.DB, ticker, currentReason string, at time.Time) error {
	return tx.Model(&DataQualityIncident{}).
		Where("domain = ? AND entity_key = ? AND reason <> ? AND status = ?", "technical_history", ticker, currentReason, DataQualityIncidentOpen).
		Updates(map[string]interface{}{"status": DataQualityIncidentResolved, "retryable": false, "resolved_at": at, "updated_at": at}).Error
}

func upsertTechnicalHistoryIncident(tx *gorm.DB, state TechnicalHistoryRetryState, observedAt time.Time) error {
	payload := strings.Join([]string{"technical_history", state.Ticker, state.Reason}, "\x00")
	fingerprint := sha256.Sum256([]byte(payload))
	detail := fmt.Sprintf("%s：有效 OHLC 日线 %d/%d，连续失败 %d 次，下次重试 %s", state.Reason, state.SampleDays, state.RequiredDays, state.FailureCount, state.NextRetryAt.Format(time.RFC3339))
	incident := DataQualityIncident{
		Fingerprint: hex.EncodeToString(fingerprint[:]), Layer: DataLayerFact, Domain: "technical_history", EntityKey: state.Ticker,
		Reason: state.Reason, Source: "historical_price_provider", SourceVersion: state.BatchID,
		Status: DataQualityIncidentOpen, Retryable: true, OccurrenceCount: 1, Detail: detail,
		FirstObservedAt: observedAt, LastObservedAt: observedAt,
	}
	return tx.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "fingerprint"}},
		DoUpdates: clause.Assignments(map[string]interface{}{
			"status": DataQualityIncidentOpen, "retryable": true, "detail": detail, "source_version": state.BatchID,
			"last_observed_at": observedAt, "resolved_at": nil,
			"occurrence_count": gorm.Expr("occurrence_count + 1"), "updated_at": observedAt,
		}),
	}).Create(&incident).Error
}

func technicalHistoryRetryCounts(ctx context.Context, db *gorm.DB, batchID string, now time.Time) (pending, due, deferred int, err error) {
	query := db.WithContext(ctx).Model(&TechnicalHistoryRetryState{}).Where("batch_id = ? AND status <> ?", batchID, TechnicalHistoryRetryResolved)
	var states []TechnicalHistoryRetryState
	if err = query.Find(&states).Error; err != nil {
		return
	}
	for _, state := range states {
		pending++
		if state.Status == TechnicalHistoryRetryDeferred {
			deferred++
		}
		if state.NextRetryAt == nil || !state.NextRetryAt.After(now.UTC()) {
			due++
		}
	}
	return
}
