package discovery

import (
	"context"
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type CandidateEffectivenessReplayInput struct {
	ScoringVersion string `json:"scoring_version"`
	Confirm        bool   `json:"confirm"`
}

type CandidateEffectivenessReplayResult struct {
	ScoringVersion  string                              `json:"scoring_version"`
	Confirm         bool                                `json:"confirm"`
	BatchCount      int                                 `json:"batch_count"`
	EligibleBatches int                                 `json:"eligible_batches"`
	SignalCount     int                                 `json:"signal_count"`
	InsertedCount   int64                               `json:"inserted_count"`
	Skipped         map[string]int                      `json:"skipped"`
	Tracking        CandidateSignalOutcomeRefreshResult `json:"tracking"`
}

// ReplayCandidateSignalHistory reconstructs transitions from immutable,
// published point-in-time score snapshots. It never recalculates a historical
// score with today's facts. Batches with incomplete timestamps or a price from
// after the effective date are rejected to prevent look-ahead leakage.
func ReplayCandidateSignalHistory(ctx context.Context, db *gorm.DB, input CandidateEffectivenessReplayInput, now time.Time) (CandidateEffectivenessReplayResult, error) {
	result := CandidateEffectivenessReplayResult{Confirm: input.Confirm, Skipped: map[string]int{}}
	if db == nil {
		return result, errors.New("database is required")
	}
	version := strings.TrimSpace(input.ScoringVersion)
	if version == "" {
		var err error
		version, err = currentCandidateScoringVersion(ctx, db)
		if err != nil {
			return result, err
		}
	}
	result.ScoringVersion = version
	if version == "" {
		return result, errors.New("scoring version is required")
	}
	var batches []UniverseBatch
	if err := db.WithContext(ctx).Where("kind = ? AND status = ?", BatchKindPrescreen, BatchStatusPublished).Order("effective_date ASC, started_at ASC, batch_id ASC").Find(&batches).Error; err != nil {
		return result, err
	}
	result.BatchCount = len(batches)
	previous := map[uint]CandidateScoreSnapshot{}
	events := make([]CandidateSignalEvent, 0)
	for _, batch := range batches {
		effectiveDate, dateErr := time.Parse(time.DateOnly, batch.EffectiveDate)
		if dateErr != nil || batch.CompletedAt == nil || batch.CompletedAt.IsZero() {
			result.Skipped["incomplete_batch_metadata"]++
			continue
		}
		var scores []CandidateScoreSnapshot
		if err := db.WithContext(ctx).Where("batch_id = ? AND scoring_version = ?", batch.BatchID, version).Order("security_id ASC").Find(&scores).Error; err != nil {
			return result, err
		}
		if len(scores) == 0 {
			result.Skipped["scoring_version_not_present"]++
			continue
		}
		result.EligibleBatches++
		var snapshots []UniverseSnapshot
		if err := db.WithContext(ctx).Where("batch_id = ?", batch.BatchID).Find(&snapshots).Error; err != nil {
			return result, err
		}
		priceIDBySecurity := map[uint]uint{}
		priceIDs := make([]uint, 0, len(snapshots))
		for _, snapshot := range snapshots {
			if snapshot.PriceSnapshotID != nil {
				priceIDBySecurity[snapshot.SecurityID] = *snapshot.PriceSnapshotID
				priceIDs = append(priceIDs, *snapshot.PriceSnapshotID)
			}
		}
		prices := map[uint]PriceSnapshot{}
		if len(priceIDs) > 0 {
			var rows []PriceSnapshot
			if err := db.WithContext(ctx).Where("id IN ?", priceIDs).Find(&rows).Error; err != nil {
				return result, err
			}
			for _, row := range rows {
				prices[row.ID] = row
			}
		}
		for _, score := range scores {
			if score.CreatedAt.After(batch.CompletedAt.Add(time.Minute)) {
				result.Skipped["score_created_after_batch"]++
				continue
			}
			eventType := candidateSignalEventType(previous[score.SecurityID], score)
			previous[score.SecurityID] = score
			if eventType == "" {
				continue
			}
			price, ok := prices[priceIDBySecurity[score.SecurityID]]
			if !ok || price.CloseMicros <= 0 || price.QualityStatus != QualityStatusValid {
				result.Skipped["baseline_price_missing"]++
				continue
			}
			if price.TradeDate.After(effectiveDate) {
				result.Skipped["future_price_rejected"]++
				continue
			}
			events = append(events, CandidateSignalEvent{BatchID: batch.BatchID, SecurityID: score.SecurityID, Ticker: score.Ticker, Grade: score.Grade, EventType: eventType, ScoringVersion: version, TotalScore: score.TotalScore, SignalDate: effectiveDate, BaselineTradeDate: price.TradeDate, BaselineCloseMicros: price.CloseMicros, PriceSource: price.Source, CreatedAt: now.UTC()})
		}
	}
	result.SignalCount = len(events)
	if !input.Confirm || len(events) == 0 {
		return result, nil
	}
	write := db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&events)
	if write.Error != nil {
		return result, write.Error
	}
	result.InsertedCount = write.RowsAffected
	tracking, err := RefreshCandidateSignalOutcomes(ctx, db, now)
	result.Tracking = tracking
	return result, err
}
