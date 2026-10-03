package discovery

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
)

type OptionResearchListQuery struct{ Page, PageSize int }
type OptionResearchListSummary struct {
	Total         int64      `json:"total"`
	OptionCovered int64      `json:"option_covered"`
	ShortCovered  int64      `json:"short_covered"`
	LastFetchedAt *time.Time `json:"last_fetched_at,omitempty"`
}
type OptionResearchList struct {
	Items    []OptionResearchSnapshot  `json:"items"`
	Total    int64                     `json:"total"`
	Page     int                       `json:"page"`
	PageSize int                       `json:"page_size"`
	Summary  OptionResearchListSummary `json:"summary"`
}

// ListOptionResearch reads one latest observation per ticker. Backfilled older
// observation dates cannot replace the latest snapshot; the resulting list is
// ordered by local synchronization time, independently of report-period dates.
func ListOptionResearch(ctx context.Context, db *gorm.DB, q OptionResearchListQuery) (OptionResearchList, error) {
	if q.Page < 1 {
		q.Page = 1
	}
	if q.PageSize < 1 || q.PageSize > maxDiscoveryPageSize {
		q.PageSize = 20
	}
	result := OptionResearchList{Items: []OptionResearchSnapshot{}, Page: q.Page, PageSize: q.PageSize}
	if db == nil {
		return result, errors.New("database is required")
	}
	latest := func() *gorm.DB {
		return db.WithContext(ctx).Table("option_research_snapshots AS snapshot").Where("snapshot.provider = ?", longbridgeOptionResearchProvider).
			Where(`NOT EXISTS (SELECT 1 FROM option_research_snapshots AS newer
                WHERE newer.provider = snapshot.provider AND newer.ticker = snapshot.ticker
                AND (newer.observed_date > snapshot.observed_date
                    OR (newer.observed_date = snapshot.observed_date AND newer.id > snapshot.id)))`)
	}
	if err := latest().Select(`COUNT(*) AS total,
        COALESCE(SUM(CASE WHEN call_volume IS NOT NULL OR put_volume IS NOT NULL THEN 1 ELSE 0 END), 0) AS option_covered,
        COALESCE(SUM(CASE WHEN short_ratio_pct IS NOT NULL OR current_shares_short IS NOT NULL THEN 1 ELSE 0 END), 0) AS short_covered`).Scan(&result.Summary).Error; err != nil {
		return result, err
	}
	result.Total = result.Summary.Total
	var newest OptionResearchSnapshot
	if err := latest().Select("snapshot.fetched_at").Order("snapshot.fetched_at DESC").Limit(1).Find(&newest).Error; err != nil {
		return result, err
	}
	if result.Total > 0 {
		result.Summary.LastFetchedAt = &newest.FetchedAt
	}
	if err := latest().Select("snapshot.*").Order("snapshot.fetched_at DESC, snapshot.observed_date DESC, snapshot.ticker ASC").
		Offset((q.Page - 1) * q.PageSize).Limit(q.PageSize).Find(&result.Items).Error; err != nil {
		return result, err
	}
	for i := range result.Items {
		decodeOptionResearchAnomalies(&result.Items[i])
	}
	return result, nil
}
