package discovery

import (
	"context"
	"errors"
	"strings"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func UpsertSocialHeatSnapshot(ctx context.Context, db *gorm.DB, input SocialHeatSnapshot) error {
	if db == nil {
		return errors.New("database is required")
	}
	input.Ticker = strings.ToUpper(strings.TrimSpace(input.Ticker))
	input.Provider = strings.TrimSpace(input.Provider)
	if input.BatchID == "" || input.SecurityID == 0 || input.Provider == "" {
		return errors.New("batch_id, security_id and provider are required")
	}
	return db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "batch_id"}, {Name: "security_id"}, {Name: "provider"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"ticker", "mention_count", "baseline_count", "heat_score", "sentiment_score",
			"source_status", "window_start", "window_end", "source_url", "updated_at",
		}),
	}).Create(&input).Error
}

func ListSocialHeatForBatch(ctx context.Context, db *gorm.DB, batchID string) ([]SocialHeatSnapshot, error) {
	if db == nil {
		return nil, errors.New("database is required")
	}
	items := []SocialHeatSnapshot{}
	err := db.WithContext(ctx).Where("batch_id = ?", batchID).Order("heat_score DESC").Order("mention_count DESC").Order("ticker ASC").Find(&items).Error
	return items, err
}
