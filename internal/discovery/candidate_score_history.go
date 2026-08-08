package discovery

import (
	"context"
	"errors"
	"sort"

	"gorm.io/gorm"
)

const candidateScoreHistoryLimit = 12

// CandidateScoreHistoryPoint is a compact, immutable view of one published
// candidate batch. It keeps the score version and the drivers needed to tell
// a fundamental change from a screening or risk-block change.
type CandidateScoreHistoryPoint struct {
	BatchID          string                  `json:"batch_id"`
	EffectiveDate    string                  `json:"effective_date"`
	Grade            string                  `json:"grade"`
	TotalScore       int                     `json:"total_score"`
	ScoreDelta       *int                    `json:"score_delta,omitempty"`
	EligibleA        bool                    `json:"eligible_a"`
	EligibleB        bool                    `json:"eligible_b"`
	RevenueGrowthPct float64                 `json:"revenue_growth_pct"`
	CashRunwayMonths float64                 `json:"cash_runway_months"`
	MarketCapUSD     int64                   `json:"market_cap_usd"`
	ActiveBlocksA    bool                    `json:"active_blocks_a"`
	ActiveBlocksB    bool                    `json:"active_blocks_b"`
	ScoringVersion   string                  `json:"scoring_version"`
	ChangeStatus     string                  `json:"change_status"`
	ChangeReasons    []CandidateChangeReason `json:"change_reasons"`
}

func candidateScoreHistory(ctx context.Context, db *gorm.DB, securityID uint) ([]CandidateScoreHistoryPoint, error) {
	items := []CandidateScoreHistoryPoint{}
	if db == nil {
		return items, errors.New("database is required")
	}
	if ctx == nil {
		return items, errors.New("context is required")
	}
	var scores []CandidateScoreSnapshot
	if err := db.WithContext(ctx).Model(&CandidateScoreSnapshot{}).
		Joins("JOIN universe_batches ON universe_batches.batch_id = candidate_score_snapshots.batch_id").
		Where("candidate_score_snapshots.security_id = ? AND universe_batches.kind = ? AND universe_batches.status = ?", securityID, BatchKindPrescreen, BatchStatusPublished).
		Order("universe_batches.started_at DESC, candidate_score_snapshots.id DESC").
		Limit(candidateScoreHistoryLimit).
		Find(&scores).Error; err != nil {
		return items, err
	}
	if len(scores) == 0 {
		return items, nil
	}
	batchIDs := make([]string, 0, len(scores))
	for _, score := range scores {
		batchIDs = append(batchIDs, score.BatchID)
	}
	var batches []UniverseBatch
	if err := db.WithContext(ctx).Where("batch_id IN ?", batchIDs).Find(&batches).Error; err != nil {
		return items, err
	}
	effectiveDates := make(map[string]string, len(batches))
	for _, batch := range batches {
		effectiveDates[batch.BatchID] = batch.EffectiveDate
	}

	// Scores are fetched newest-first for efficient limiting. Compare in
	// chronological order, then return newest-first for the detail timeline.
	for left, right := 0, len(scores)-1; left < right; left, right = left+1, right-1 {
		scores[left], scores[right] = scores[right], scores[left]
	}
	var previous *CandidateScoreSnapshot
	for _, score := range scores {
		point := CandidateScoreHistoryPoint{
			BatchID: score.BatchID, EffectiveDate: effectiveDates[score.BatchID], Grade: score.Grade, TotalScore: score.TotalScore,
			EligibleA: score.EligibleA, EligibleB: score.EligibleB, RevenueGrowthPct: score.RevenueGrowthPct,
			CashRunwayMonths: score.CashRunwayMonths, MarketCapUSD: score.MarketCapUSD, ActiveBlocksA: score.ActiveBlocksA,
			ActiveBlocksB: score.ActiveBlocksB, ScoringVersion: score.ScoringVersion, ChangeReasons: []CandidateChangeReason{},
		}
		if previous == nil {
			point.ChangeStatus = "new"
			point.ChangeReasons = candidateChangeReasons(nil, score)
		} else {
			delta := score.TotalScore - previous.TotalScore
			point.ScoreDelta = &delta
			point.ChangeStatus = candidateChangeStatus(*previous, score)
			point.ChangeReasons = candidateChangeReasons(previous, score)
		}
		current := score
		previous = &current
		items = append(items, point)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].EffectiveDate != items[j].EffectiveDate {
			return items[i].EffectiveDate > items[j].EffectiveDate
		}
		return items[i].BatchID > items[j].BatchID
	})
	return items, nil
}

func candidateSignalEvents(ctx context.Context, db *gorm.DB, securityID uint) ([]CandidateSignalEvent, error) {
	items := []CandidateSignalEvent{}
	if db == nil {
		return items, errors.New("database is required")
	}
	if ctx == nil {
		return items, errors.New("context is required")
	}
	if err := db.WithContext(ctx).Where("security_id = ?", securityID).Order("signal_date DESC, id DESC").Limit(candidateScoreHistoryLimit).Find(&items).Error; err != nil {
		return items, err
	}
	return items, nil
}
