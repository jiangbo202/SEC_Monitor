package discovery

import (
	"context"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	LongbridgeRefreshFamilyMarketResearch = "market_research"
	LongbridgeRefreshFamilyValuation      = "valuation"
)

// FreshLongbridgeResearchTickers returns tickers successfully queried in the
// current Shanghai research day. A successful empty response is still fresh:
// it is evidence that the provider was queried and avoids paying twice for a
// known no-coverage result. Failures are intentionally absent so the other
// independent queue may retry them.
func FreshLongbridgeResearchTickers(ctx context.Context, db *gorm.DB, family string, now time.Time) (map[string]bool, error) {
	fresh := map[string]bool{}
	if db == nil {
		return fresh, nil
	}
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		return fresh, err
	}
	local := now.In(location)
	start := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, location).UTC()
	var rows []LongbridgeResearchRefreshState
	if err := db.WithContext(ctx).Where("family = ? AND status = ? AND last_success_at >= ?", family, "success", start).Find(&rows).Error; err != nil {
		return fresh, err
	}
	for _, row := range rows {
		if ticker := normalizeAnalystRatingTicker(row.Ticker); ticker != "" {
			fresh[ticker] = true
		}
	}
	return fresh, nil
}

// MarkLongbridgeResearchSuccess advances the shared freshness cursor only
// after an external request completes without an error.
func MarkLongbridgeResearchSuccess(ctx context.Context, db *gorm.DB, family, ticker string, now time.Time) error {
	if db == nil {
		return nil
	}
	ticker = normalizeAnalystRatingTicker(ticker)
	if ticker == "" {
		return nil
	}
	row := LongbridgeResearchRefreshState{Ticker: ticker, Family: strings.TrimSpace(family), LastAttemptAt: now, LastSuccessAt: &now, Status: "success"}
	return db.WithContext(ctx).Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "ticker"}, {Name: "family"}}, DoUpdates: clause.AssignmentColumns([]string{"last_attempt_at", "last_success_at", "status", "updated_at"})}).Create(&row).Error
}
