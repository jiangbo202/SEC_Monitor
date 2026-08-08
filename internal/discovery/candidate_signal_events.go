package discovery

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	CandidateSignalEnteredA        = "entered_a"
	CandidateSignalEnteredB        = "entered_b"
	CandidateSignalUpgradedQuality = "upgraded_quality"
)

// persistCandidateSignalEvents creates a compact, immutable trail of changes
// that a historical evaluator can use without selecting only today's
// survivors. It runs before the current-batch pointer is advanced, so that
// the pointer still identifies the previous published comparison set.
func persistCandidateSignalEvents(ctx context.Context, db *gorm.DB, batch UniverseBatch, now time.Time) error {
	if batch.Kind != BatchKindPrescreen {
		return nil
	}
	var pointer CurrentBatchPointer
	previousBySecurity := map[uint]CandidateScoreSnapshot{}
	if err := db.WithContext(ctx).First(&pointer, "kind = ?", BatchKindPrescreen).Error; err == nil && pointer.BatchID != batch.BatchID {
		var previous []CandidateScoreSnapshot
		if err := db.WithContext(ctx).Where("batch_id = ?", pointer.BatchID).Find(&previous).Error; err != nil {
			return err
		}
		for _, score := range previous {
			previousBySecurity[score.SecurityID] = score
		}
	} else if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	var scores []CandidateScoreSnapshot
	if err := db.WithContext(ctx).Where("batch_id = ? AND grade IN ?", batch.BatchID, []string{CandidateGradeA, CandidateGradeB}).Find(&scores).Error; err != nil {
		return err
	}
	if len(scores) == 0 {
		return nil
	}
	securityIDs := make([]uint, 0, len(scores))
	for _, score := range scores {
		securityIDs = append(securityIDs, score.SecurityID)
	}
	var snapshots []UniverseSnapshot
	if err := db.WithContext(ctx).Where("batch_id = ? AND security_id IN ?", batch.BatchID, securityIDs).Find(&snapshots).Error; err != nil {
		return err
	}
	priceIDs := make([]uint, 0, len(snapshots))
	priceIDBySecurity := map[uint]uint{}
	for _, snapshot := range snapshots {
		if snapshot.PriceSnapshotID == nil {
			continue
		}
		priceIDBySecurity[snapshot.SecurityID] = *snapshot.PriceSnapshotID
		priceIDs = append(priceIDs, *snapshot.PriceSnapshotID)
	}
	var prices []PriceSnapshot
	if len(priceIDs) > 0 {
		if err := db.WithContext(ctx).Where("id IN ? AND quality_status = ?", priceIDs, QualityStatusValid).Find(&prices).Error; err != nil {
			return err
		}
	}
	priceByID := map[uint]PriceSnapshot{}
	for _, price := range prices {
		priceByID[price.ID] = price
	}
	signalDate, err := time.Parse(time.DateOnly, batch.EffectiveDate)
	if err != nil {
		signalDate = now.UTC().Truncate(24 * time.Hour)
	}
	events := make([]CandidateSignalEvent, 0, len(scores))
	for _, score := range scores {
		eventType := candidateSignalEventType(previousBySecurity[score.SecurityID], score)
		if eventType == "" {
			continue
		}
		price, found := priceByID[priceIDBySecurity[score.SecurityID]]
		if !found || price.CloseMicros <= 0 {
			continue
		}
		events = append(events, CandidateSignalEvent{
			BatchID: batch.BatchID, SecurityID: score.SecurityID, Ticker: score.Ticker, Grade: score.Grade,
			EventType: eventType, ScoringVersion: score.ScoringVersion, TotalScore: score.TotalScore,
			SignalDate: signalDate, BaselineTradeDate: price.TradeDate, BaselineCloseMicros: price.CloseMicros,
			PriceSource: price.Source, CreatedAt: now,
		})
	}
	if len(events) == 0 {
		return nil
	}
	// Publishing may be retried after a process interruption.  Events are
	// immutable, and the unique key makes retrying safe without emitting a
	// second signal for the same batch/security/event type.
	return db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&events).Error
}

func candidateSignalEventType(previous, current CandidateScoreSnapshot) string {
	if current.EligibleA && !previous.EligibleA {
		return CandidateSignalEnteredA
	}
	if current.EligibleB && !previous.EligibleB {
		return CandidateSignalEnteredB
	}
	if (current.EligibleA || current.EligibleB) && (previous.EligibleA || previous.EligibleB) && current.TotalScore >= previous.TotalScore+10 {
		return CandidateSignalUpgradedQuality
	}
	return ""
}
