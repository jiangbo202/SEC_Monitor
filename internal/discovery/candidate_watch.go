package discovery

import (
	"context"
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"
)

const CandidateWatchStatusActive = "active"
const CandidateWatchStatusArchived = "archived"

type CandidateWatchInput struct {
	Ticker string `json:"ticker"`
	Note   string `json:"note"`
	Status string `json:"status"`
}

type CandidateWatchQuery struct {
	Page, PageSize int
	Ticker         string
	Status         string
}

type CandidateWatchPage struct {
	Page, PageSize int                    `json:"page"`
	Total          int64                  `json:"total"`
	Items          []CandidateWatchResult `json:"items"`
}

type CandidateWatchResult struct {
	CandidateWatch
	LatestScore *CandidateScoreResult `json:"latest_score,omitempty"`
}

func ListCandidateWatches(ctx context.Context, db *gorm.DB, filter CandidateWatchQuery) (CandidateWatchPage, error) {
	page, size, err := normalizePage(filter.Page, filter.PageSize)
	if err != nil {
		return CandidateWatchPage{}, err
	}
	result := CandidateWatchPage{Page: page, PageSize: size, Items: []CandidateWatchResult{}}
	if db == nil {
		return result, errors.New("database is required")
	}
	query := db.WithContext(ctx).Model(&CandidateWatch{})
	if ticker := normalizeTicker(filter.Ticker); ticker != "" {
		query = query.Where("ticker = ?", ticker)
	}
	status := normalizeCandidateWatchStatus(filter.Status)
	if status == "" {
		status = CandidateWatchStatusActive
	}
	if status != "all" {
		query = query.Where("status = ?", status)
	}
	if err := query.Count(&result.Total).Error; err != nil {
		return result, err
	}
	var watches []CandidateWatch
	if err := query.Order("updated_at DESC").Order("ticker ASC").Offset((page - 1) * size).Limit(size).Find(&watches).Error; err != nil {
		return result, err
	}
	result.Items = make([]CandidateWatchResult, 0, len(watches))
	for _, watch := range watches {
		result.Items = append(result.Items, CandidateWatchResult{CandidateWatch: watch})
	}
	if err := attachLatestCandidateScores(ctx, db, result.Items); err != nil {
		return result, err
	}
	return result, nil
}

func attachLatestCandidateScores(ctx context.Context, db *gorm.DB, items []CandidateWatchResult) error {
	if len(items) == 0 {
		return nil
	}
	batch, ok, err := currentPublishedPrescreenBatch(ctx, db)
	if err != nil || !ok {
		return err
	}
	tickers := make([]string, 0, len(items))
	for _, item := range items {
		tickers = append(tickers, item.Ticker)
	}
	var scores []CandidateScoreSnapshot
	if err := db.WithContext(ctx).Where("batch_id = ? AND ticker IN ?", batch.BatchID, tickers).Find(&scores).Error; err != nil {
		return err
	}
	if len(scores) == 0 {
		return nil
	}
	scoreItems, err := hydrateCandidateSectorEvidence(ctx, db, batch.UniverseSourceVersion, scores)
	if err != nil {
		return err
	}
	if scoreItems, err = hydrateCandidateRevenueGrowthEvidence(ctx, db, batch.UniverseSourceVersion, scoreItems); err != nil {
		return err
	}
	if scoreItems, err = hydrateCandidatePriceEvidence(ctx, db, batch.BatchID, scoreItems); err != nil {
		return err
	}
	riskBatchID := strings.TrimSpace(batch.UniverseSourceVersion)
	if riskBatchID == "" {
		riskBatchID = batch.BatchID
	}
	if scoreItems, err = hydrateCandidateCapitalRiskSummaries(ctx, db, riskBatchID, scoreItems); err != nil {
		return err
	}
	if scoreItems, err = annotateCandidateChanges(ctx, db, batch, scoreItems); err != nil {
		return err
	}
	annotateCandidateQuality(scoreItems)
	if err = hydrateCandidatePerformance(ctx, db, scoreItems); err != nil {
		return err
	}
	byTicker := map[string]CandidateScoreResult{}
	for _, score := range scoreItems {
		byTicker[score.Ticker] = score
	}
	for i := range items {
		if score, ok := byTicker[items[i].Ticker]; ok {
			scoreCopy := score
			items[i].LatestScore = &scoreCopy
		}
	}
	return nil
}

func UpsertCandidateWatch(ctx context.Context, db *gorm.DB, input CandidateWatchInput) (CandidateWatch, error) {
	if db == nil {
		return CandidateWatch{}, errors.New("database is required")
	}
	ticker := normalizeTicker(input.Ticker)
	if ticker == "" {
		return CandidateWatch{}, errors.New("ticker is required")
	}
	status := normalizeCandidateWatchStatus(input.Status)
	if status == "" {
		status = CandidateWatchStatusActive
	}
	if status != CandidateWatchStatusActive && status != CandidateWatchStatusArchived {
		return CandidateWatch{}, errors.New("invalid watch status")
	}
	watch := CandidateWatch{Ticker: ticker, Status: status, Note: strings.TrimSpace(input.Note), UpdatedAt: time.Now().UTC()}
	if score, ok, err := currentCandidateScoreByTicker(ctx, db, ticker); err != nil {
		return CandidateWatch{}, err
	} else if ok {
		watch.SecurityID = score.SecurityID
		watch.SourceBatchID = score.BatchID
		var security Security
		if err := db.WithContext(ctx).First(&security, score.SecurityID).Error; err == nil {
			watch.CIK = security.CIK
			watch.CompanyName = security.CompanyName
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return CandidateWatch{}, err
		}
	}
	var existing CandidateWatch
	err := db.WithContext(ctx).First(&existing, "ticker = ?", ticker).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		if err := db.WithContext(ctx).Create(&watch).Error; err != nil {
			return CandidateWatch{}, err
		}
		return watch, nil
	}
	if err != nil {
		return CandidateWatch{}, err
	}
	updates := map[string]any{
		"security_id": watch.SecurityID, "cik": watch.CIK, "company_name": watch.CompanyName,
		"status": watch.Status, "note": watch.Note, "source_batch_id": watch.SourceBatchID, "updated_at": watch.UpdatedAt,
	}
	if err := db.WithContext(ctx).Model(&existing).Updates(updates).Error; err != nil {
		return CandidateWatch{}, err
	}
	if err := db.WithContext(ctx).First(&existing, existing.ID).Error; err != nil {
		return CandidateWatch{}, err
	}
	return existing, nil
}

func DeleteCandidateWatch(ctx context.Context, db *gorm.DB, id uint) error {
	if db == nil {
		return errors.New("database is required")
	}
	if id == 0 {
		return errors.New("id is required")
	}
	return db.WithContext(ctx).Delete(&CandidateWatch{}, id).Error
}

func currentCandidateScoreByTicker(ctx context.Context, db *gorm.DB, ticker string) (CandidateScoreSnapshot, bool, error) {
	batch, ok, err := currentPublishedPrescreenBatch(ctx, db)
	if err != nil || !ok {
		return CandidateScoreSnapshot{}, false, err
	}
	var score CandidateScoreSnapshot
	err = db.WithContext(ctx).First(&score, "batch_id = ? AND ticker = ?", batch.BatchID, ticker).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return CandidateScoreSnapshot{}, false, nil
	}
	if err != nil {
		return CandidateScoreSnapshot{}, false, err
	}
	return score, true, nil
}

func normalizeTicker(ticker string) string {
	return strings.ToUpper(strings.TrimSpace(ticker))
}

func normalizeCandidateWatchStatus(status string) string {
	return strings.ToLower(strings.TrimSpace(status))
}
