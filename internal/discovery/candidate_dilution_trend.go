package discovery

import (
	"context"
	"sort"
	"time"

	"gorm.io/gorm"
)

// CandidateDilutionTrend compares accepted SEC cover-page share counts. It
// is evidence, not a proxy for fully diluted share count or a confirmation of
// issuance. Split/capital-event context remains separately visible.
type CandidateDilutionTrend struct {
	Status          string   `json:"status"`
	Reasons         []string `json:"reasons"`
	ShareChangePct  float64  `json:"share_change_pct"`
	LatestShares    int64    `json:"latest_shares"`
	PriorShares     int64    `json:"prior_shares"`
	LatestInstant   string   `json:"latest_instant"`
	PriorInstant    string   `json:"prior_instant"`
	ObservationDays int      `json:"observation_days"`
}

func hydrateCandidateDilutionTrends(ctx context.Context, db *gorm.DB, batch UniverseBatch, items []CandidateScoreResult) error {
	if len(items) == 0 {
		return nil
	}
	ids := make([]uint, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.SecurityID)
	}
	asOf := readinessAsOf(batch).Add(24*time.Hour - time.Nanosecond)
	var rows []ShareSnapshot
	if err := db.WithContext(ctx).Where("security_id IN ? AND quality_status = ? AND accepted_at <= ?", ids, QualityStatusValid, asOf).Find(&rows).Error; err != nil {
		return err
	}
	bySecurity := map[uint][]ShareSnapshot{}
	for _, row := range rows {
		bySecurity[row.SecurityID] = append(bySecurity[row.SecurityID], row)
	}
	for i := range items {
		items[i].DilutionTrend = buildCandidateDilutionTrend(bySecurity[items[i].SecurityID])
	}
	return nil
}

func buildCandidateDilutionTrend(rows []ShareSnapshot) CandidateDilutionTrend {
	result := CandidateDilutionTrend{Status: "missing", Reasons: []string{}}
	if len(rows) < 2 {
		result.Reasons = append(result.Reasons, "share_history_insufficient")
		return result
	}
	sort.Slice(rows, func(i, j int) bool {
		if !rows[i].Instant.Equal(rows[j].Instant) {
			return rows[i].Instant.Before(rows[j].Instant)
		}
		return rows[i].AcceptedAt.Before(rows[j].AcceptedAt)
	})
	latest := rows[len(rows)-1]
	var prior ShareSnapshot
	found := false
	for index := len(rows) - 2; index >= 0; index-- {
		candidate := rows[index]
		if candidate.Instant.Before(latest.Instant) && latest.Instant.Sub(candidate.Instant) >= 90*24*time.Hour {
			prior, found = candidate, true
			break
		}
	}
	if !found || prior.Shares <= 0 || latest.Shares <= 0 {
		result.Reasons = append(result.Reasons, "share_history_insufficient")
		return result
	}
	result.LatestShares, result.PriorShares = latest.Shares, prior.Shares
	result.LatestInstant, result.PriorInstant = latest.Instant.Format(time.DateOnly), prior.Instant.Format(time.DateOnly)
	result.ObservationDays = int(latest.Instant.Sub(prior.Instant).Hours() / 24)
	result.ShareChangePct = (float64(latest.Shares)/float64(prior.Shares) - 1) * 100
	switch {
	case result.ShareChangePct >= 25:
		result.Status = "high_dilution"
		result.Reasons = append(result.Reasons, "shares_up_25pct_or_more")
	case result.ShareChangePct >= 10:
		result.Status = "elevated_dilution"
		result.Reasons = append(result.Reasons, "shares_up_10pct_or_more")
	case result.ShareChangePct <= -10:
		result.Status = "shares_reduced"
		result.Reasons = append(result.Reasons, "shares_down_10pct_or_more")
	default:
		result.Status = "stable"
	}
	return result
}
