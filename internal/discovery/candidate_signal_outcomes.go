package discovery

import (
	"context"
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	CandidateSignalOutcomePending          = "pending"
	CandidateSignalOutcomeBenchmarkMissing = "benchmark_missing"
	CandidateSignalOutcomeMature           = "mature"
)

type CandidateSignalOutcomeRefreshResult struct {
	SignalCount      int        `json:"signal_count"`
	TrackedCount     int        `json:"tracked_count"`
	PendingCount     int        `json:"pending_count"`
	BenchmarkMissing int        `json:"benchmark_missing"`
	MatureCount      int        `json:"mature_count"`
	LastEvaluatedAt  *time.Time `json:"last_evaluated_at,omitempty"`
}

// RefreshCandidateSignalOutcomes advances every immutable signal across the
// configured holding horizons using only persisted daily facts. It is safe to
// rerun: one signal/horizon owns one row and mature timestamps are preserved.
func RefreshCandidateSignalOutcomes(ctx context.Context, db *gorm.DB, now time.Time) (CandidateSignalOutcomeRefreshResult, error) {
	result := CandidateSignalOutcomeRefreshResult{}
	if db == nil {
		return result, errors.New("database is required")
	}
	if ctx == nil {
		return result, errors.New("context is required")
	}
	now = now.UTC()
	var events []CandidateSignalEvent
	if err := db.WithContext(ctx).
		Where("event_type IN ?", []string{CandidateSignalEnteredA, CandidateSignalEnteredB, CandidateSignalUpgradedQuality}).
		Order("signal_date ASC, id ASC").Find(&events).Error; err != nil {
		return result, err
	}
	result.SignalCount = len(events)
	for _, event := range events {
		if event.ID == 0 || event.BaselineTradeDate.IsZero() || event.BaselineCloseMicros <= 0 || strings.TrimSpace(event.Ticker) == "" {
			continue
		}
		for _, horizon := range candidateEffectivenessHorizons {
			outcome, err := buildCandidateSignalOutcome(ctx, db, event, horizon, now)
			if err != nil {
				return result, err
			}
			var existing CandidateSignalOutcome
			existingErr := db.WithContext(ctx).Where("signal_event_id = ? AND horizon_days = ?", event.ID, horizon).First(&existing).Error
			if existingErr != nil && !errors.Is(existingErr, gorm.ErrRecordNotFound) {
				return result, existingErr
			}
			if existing.MaturedAt != nil && outcome.MaturedAt != nil {
				outcome.MaturedAt = existing.MaturedAt
			}
			if existing.ID > 0 {
				outcome.ID = existing.ID
				outcome.CreatedAt = existing.CreatedAt
			}
			if err := db.WithContext(ctx).Clauses(clause.OnConflict{
				Columns: []clause.Column{{Name: "signal_event_id"}, {Name: "horizon_days"}},
				DoUpdates: clause.AssignmentColumns([]string{
					"ticker", "grade", "scoring_version", "status", "baseline_trade_date", "baseline_close_micros",
					"outcome_trade_date", "outcome_close_micros", "return_pct", "max_drawdown_pct", "benchmark_ticker",
					"benchmark_return_pct", "excess_return_pct", "quality_status", "evaluated_at", "matured_at", "updated_at",
				}),
			}).Create(&outcome).Error; err != nil {
				return result, err
			}
			result.TrackedCount++
			switch outcome.Status {
			case CandidateSignalOutcomeMature:
				result.MatureCount++
			case CandidateSignalOutcomeBenchmarkMissing:
				result.BenchmarkMissing++
			default:
				result.PendingCount++
			}
		}
	}
	result.LastEvaluatedAt = &now
	return result, nil
}

func buildCandidateSignalOutcome(ctx context.Context, db *gorm.DB, event CandidateSignalEvent, horizon int, now time.Time) (CandidateSignalOutcome, error) {
	outcome := CandidateSignalOutcome{
		SignalEventID: event.ID, Ticker: event.Ticker, Grade: event.Grade, ScoringVersion: event.ScoringVersion,
		HorizonDays: horizon, Status: CandidateSignalOutcomePending, BaselineTradeDate: event.BaselineTradeDate,
		BaselineCloseMicros: event.BaselineCloseMicros, BenchmarkTicker: "IWM", QualityStatus: QualityStatusMissing,
		EvaluatedAt: now, CreatedAt: now, UpdatedAt: now,
	}
	rows, err := candidateHorizonRows(ctx, db, event.Ticker, event.BaselineTradeDate, horizon)
	if err != nil {
		return outcome, err
	}
	if len(rows) < horizon {
		return outcome, nil
	}
	baseClose := float64(event.BaselineCloseMicros) / 1_000_000
	returnPct, drawdown := horizonReturnAndDrawdown(rows, baseClose)
	last := rows[len(rows)-1]
	outcome.OutcomeTradeDate = &last.TradeDate
	outcome.OutcomeCloseMicros = last.CloseMicros
	outcome.ReturnPct = &returnPct
	outcome.MaxDrawdownPct = &drawdown
	benchmarkReturn, _, benchmarkMature, err := benchmarkHorizonReturn(ctx, db, outcome.BenchmarkTicker, event.BaselineTradeDate, horizon)
	if err != nil {
		return outcome, err
	}
	if !benchmarkMature {
		outcome.Status = CandidateSignalOutcomeBenchmarkMissing
		return outcome, nil
	}
	excess := returnPct - benchmarkReturn
	outcome.BenchmarkReturnPct = &benchmarkReturn
	outcome.ExcessReturnPct = &excess
	outcome.Status = CandidateSignalOutcomeMature
	outcome.QualityStatus = QualityStatusValid
	outcome.MaturedAt = &now
	return outcome, nil
}

func candidateHorizonRows(ctx context.Context, db *gorm.DB, ticker string, baseDate time.Time, horizon int) ([]PriceSnapshot, error) {
	rows := []PriceSnapshot{}
	if horizon <= 0 {
		return rows, nil
	}
	err := db.WithContext(ctx).
		Where("symbol = ? AND trade_date > ? AND quality_status = ?", strings.ToUpper(strings.TrimSpace(ticker)), baseDate, QualityStatusValid).
		Order("trade_date ASC").Limit(horizon).Find(&rows).Error
	return rows, err
}

func horizonReturnAndDrawdown(rows []PriceSnapshot, baseClose float64) (float64, float64) {
	if len(rows) == 0 || baseClose <= 0 {
		return 0, 0
	}
	peak, maxDrawdown := baseClose, 0.0
	for _, row := range rows {
		close := float64(row.CloseMicros) / 1_000_000
		if close > peak {
			peak = close
		}
		if peak > 0 {
			drawdown := (close/peak - 1) * 100
			if drawdown < maxDrawdown {
				maxDrawdown = drawdown
			}
		}
	}
	lastClose := float64(rows[len(rows)-1].CloseMicros) / 1_000_000
	return (lastClose/baseClose - 1) * 100, maxDrawdown
}
