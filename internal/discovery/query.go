package discovery

import (
	"context"
	"errors"
	"strings"

	"gorm.io/gorm"
)

const maxDiscoveryPageSize = 200

type UniverseQuery struct {
	Page, PageSize int
	Ticker, Status string
	ReasonCode     string
	QualityStatus  string
}

type UniversePage struct {
	Page, PageSize int                `json:"page"`
	Total          int64              `json:"total"`
	Items          []UniverseSnapshot `json:"items"`
}

type BatchQuery struct {
	Page, PageSize int
	Kind, Status   string
}
type BatchPage struct {
	Page, PageSize int             `json:"page"`
	Total          int64           `json:"total"`
	Items          []UniverseBatch `json:"items"`
}
type ProviderRunQuery struct {
	Page, PageSize   int
	Provider, Status string
}
type ProviderRunPage struct {
	Page, PageSize int           `json:"page"`
	Total          int64         `json:"total"`
	Items          []ProviderRun `json:"items"`
}

func normalizePage(page, size int) (int, int, error) {
	if page < 0 || size < 0 {
		return 0, 0, errors.New("page and page_size cannot be negative")
	}
	if page == 0 {
		page = 1
	}
	if size == 0 {
		size = 20
	}
	if size > maxDiscoveryPageSize {
		return 0, 0, errors.New("page_size exceeds 200")
	}
	return page, size, nil
}

func ListUniverse(ctx context.Context, db *gorm.DB, filter UniverseQuery) (UniversePage, error) {
	page, size, err := normalizePage(filter.Page, filter.PageSize)
	if err != nil {
		return UniversePage{}, err
	}
	result := UniversePage{Page: page, PageSize: size, Items: []UniverseSnapshot{}}
	if db == nil {
		return result, errors.New("database is required")
	}
	if ctx == nil {
		return result, errors.New("context is required")
	}
	var pointer CurrentBatchPointer
	err = db.WithContext(ctx).First(&pointer, "kind = ?", BatchKindPrescreen).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return result, nil
	}
	if err != nil {
		return result, err
	}
	var batch UniverseBatch
	if err = db.WithContext(ctx).First(&batch, "batch_id = ? AND kind = ? AND status = ?", pointer.BatchID, BatchKindPrescreen, BatchStatusPublished).Error; errors.Is(err, gorm.ErrRecordNotFound) {
		return result, nil
	} else if err != nil {
		return result, err
	}
	query := db.WithContext(ctx).Model(&UniverseSnapshot{}).Joins("JOIN securities ON securities.id = universe_snapshots.security_id").Where("universe_snapshots.batch_id = ?", batch.BatchID)
	if ticker := strings.ToUpper(strings.TrimSpace(filter.Ticker)); ticker != "" {
		query = query.Where("universe_snapshots.ticker = ?", ticker)
	}
	if status := strings.TrimSpace(filter.Status); status != "" {
		query = query.Where("universe_snapshots.status = ?", status)
	}
	if reason := strings.TrimSpace(filter.ReasonCode); reason != "" {
		query = query.Where("universe_snapshots.reason_code = ?", reason)
	}
	if quality := strings.TrimSpace(filter.QualityStatus); quality != "" {
		query = query.Where("universe_snapshots.quality_status = ?", quality)
	}
	if err = query.Count(&result.Total).Error; err != nil {
		return result, err
	}
	if err = query.Order("universe_snapshots.market_cap_usd DESC").Order("universe_snapshots.ticker ASC").Order("universe_snapshots.id ASC").Offset((page - 1) * size).Limit(size).Find(&result.Items).Error; err != nil {
		return result, err
	}
	return result, nil
}

func ListBatches(ctx context.Context, db *gorm.DB, filter BatchQuery) (BatchPage, error) {
	page, size, err := normalizePage(filter.Page, filter.PageSize)
	if err != nil {
		return BatchPage{}, err
	}
	result := BatchPage{Page: page, PageSize: size, Items: []UniverseBatch{}}
	if db == nil {
		return result, errors.New("database is required")
	}
	if ctx == nil {
		return result, errors.New("context is required")
	}
	query := db.WithContext(ctx).Model(&UniverseBatch{})
	if kind := strings.TrimSpace(filter.Kind); kind != "" {
		query = query.Where("kind = ?", kind)
	}
	if status := strings.TrimSpace(filter.Status); status != "" {
		query = query.Where("status = ?", status)
	}
	if err = query.Count(&result.Total).Error; err != nil {
		return result, err
	}
	err = query.Order("started_at DESC").Order("batch_id DESC").Offset((page - 1) * size).Limit(size).Find(&result.Items).Error
	return result, err
}

func ListProviderDiagnostics(ctx context.Context, db *gorm.DB, filter ProviderRunQuery) (ProviderRunPage, error) {
	page, size, err := normalizePage(filter.Page, filter.PageSize)
	if err != nil {
		return ProviderRunPage{}, err
	}
	result := ProviderRunPage{Page: page, PageSize: size, Items: []ProviderRun{}}
	if db == nil {
		return result, errors.New("database is required")
	}
	if ctx == nil {
		return result, errors.New("context is required")
	}
	query := db.WithContext(ctx).Model(&ProviderRun{})
	if provider := strings.TrimSpace(filter.Provider); provider != "" {
		query = query.Where("provider = ?", provider)
	}
	if status := strings.TrimSpace(filter.Status); status != "" {
		query = query.Where("status = ?", status)
	}
	if err = query.Count(&result.Total).Error; err != nil {
		return result, err
	}
	err = query.Order("created_at DESC").Order("id DESC").Offset((page - 1) * size).Limit(size).Find(&result.Items).Error
	return result, err
}
