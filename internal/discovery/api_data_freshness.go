package discovery

import (
	"context"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"time"
)

// Successful, validated data queries have their own namespaced cursors. Their
// times must not be confused with immutable snapshots' first-observed times,
// or with the shared P1 cursor (which can succeed without any EPS coverage).
func APIDataSyncFamily(provider, capability string) string {
	return "coverage:" + provider + ":" + capability
}

func RecordAPIDataSync(ctx context.Context, db *gorm.DB, provider, capability, ticker, status string, checkedAt time.Time) error {
	if db == nil || !db.Migrator().HasTable(&LongbridgeResearchRefreshState{}) {
		return nil
	}
	row := LongbridgeResearchRefreshState{Ticker: normalizeAnalystRatingTicker(ticker), Family: APIDataSyncFamily(provider, capability), LastAttemptAt: checkedAt, LastSuccessAt: &checkedAt, Status: status}
	return db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "ticker"}, {Name: "family"}},
		DoUpdates: clause.AssignmentColumns([]string{"last_attempt_at", "last_success_at", "status", "updated_at"}),
		Where:     clause.Where{Exprs: []clause.Expression{clause.Expr{SQL: "excluded.last_success_at >= longbridge_research_refresh_states.last_success_at"}}},
	}).Create(&row).Error
}

func APIDataSyncReceipts(ctx context.Context, db *gorm.DB, provider, capability string) (map[string]LongbridgeResearchRefreshState, error) {
	result := map[string]LongbridgeResearchRefreshState{}
	if db == nil || !db.Migrator().HasTable(&LongbridgeResearchRefreshState{}) {
		return result, nil
	}
	var rows []LongbridgeResearchRefreshState
	if err := db.WithContext(ctx).Where("family = ? AND last_success_at IS NOT NULL", APIDataSyncFamily(provider, capability)).Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		result[row.Ticker] = row
	}
	return result, nil
}

func MergeAPIDataSyncTimes(ctx context.Context, db *gorm.DB, provider, capability string, latest map[string]time.Time) error {
	receipts, err := APIDataSyncReceipts(ctx, db, provider, capability)
	if err != nil {
		return err
	}
	for ticker, row := range receipts {
		if row.LastSuccessAt.After(latest[ticker]) {
			latest[ticker] = *row.LastSuccessAt
		}
	}
	return nil
}
