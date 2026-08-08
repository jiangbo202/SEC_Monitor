package discovery

import (
	"context"
	"errors"
	"math"

	"gorm.io/gorm"
)

type DiscoverySyncRunQuery struct {
	Page     int
	PageSize int
	Status   string
	Kind     string
}

type DiscoverySyncRunPage struct {
	Page     int                `json:"page"`
	PageSize int                `json:"page_size"`
	Total    int64              `json:"total"`
	Items    []DiscoverySyncRun `json:"items"`
}

// LatestDiscoverySyncRun returns the newest workflow lifecycle record. A
// missing record is a normal first-run state, not an API error.
func LatestDiscoverySyncRun(ctx context.Context, db *gorm.DB) (DiscoverySyncRun, error) {
	if db == nil {
		return DiscoverySyncRun{}, errors.New("database is required")
	}
	if ctx == nil {
		return DiscoverySyncRun{}, errors.New("context is required")
	}
	var run DiscoverySyncRun
	err := db.WithContext(ctx).Order("started_at DESC, id DESC").First(&run).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return DiscoverySyncRun{}, nil
	}
	return run, err
}

// ListDiscoverySyncRuns returns workflow lifecycles, including failed runs
// which have no corresponding discovery batch.
func ListDiscoverySyncRuns(ctx context.Context, db *gorm.DB, input DiscoverySyncRunQuery) (DiscoverySyncRunPage, error) {
	result := DiscoverySyncRunPage{}
	if db == nil {
		return result, errors.New("database is required")
	}
	if ctx == nil {
		return result, errors.New("context is required")
	}
	page := input.Page
	if page < 1 {
		page = 1
	}
	pageSize := input.PageSize
	if pageSize < 1 {
		pageSize = 20
	}
	pageSize = int(math.Min(float64(pageSize), 100))
	query := db.WithContext(ctx).Model(&DiscoverySyncRun{})
	if input.Status != "" {
		query = query.Where("status = ?", input.Status)
	}
	if input.Kind != "" {
		query = query.Where("kind = ?", input.Kind)
	}
	if err := query.Count(&result.Total).Error; err != nil {
		return result, err
	}
	if err := query.Order("started_at DESC, id DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&result.Items).Error; err != nil {
		return result, err
	}
	result.Page, result.PageSize = page, pageSize
	return result, nil
}

func ListDiscoverySyncSteps(ctx context.Context, db *gorm.DB, runID uint) ([]DiscoverySyncStep, error) {
	if db == nil {
		return nil, errors.New("database is required")
	}
	if ctx == nil {
		return nil, errors.New("context is required")
	}
	if runID == 0 {
		return nil, errors.New("run ID is required")
	}
	var items []DiscoverySyncStep
	err := db.WithContext(ctx).Where("run_id = ?", runID).Order("sequence ASC, id ASC").Find(&items).Error
	return items, err
}
