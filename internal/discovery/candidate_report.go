package discovery

import (
	"context"
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"
)

type CandidateReport struct {
	Date      string           `json:"date"`
	Batch     UniverseBatch    `json:"batch"`
	Summary   CandidateSummary `json:"summary"`
	Health    CandidateHealth  `json:"health"`
	Generated time.Time        `json:"generated_at"`
}

func BuildCandidateReport(ctx context.Context, db *gorm.DB, date string) (CandidateReport, error) {
	result := CandidateReport{Date: strings.TrimSpace(date), Generated: time.Now().UTC()}
	if db == nil {
		return result, errors.New("database is required")
	}
	if result.Date == "" {
		result.Date = time.Now().UTC().Format("2006-01-02")
	}
	batch, ok, err := candidateReportBatch(ctx, db, result.Date)
	if err != nil {
		return result, err
	}
	if !ok {
		return result, gorm.ErrRecordNotFound
	}
	result.Batch = batch
	result.Summary, err = BuildCandidateSummary(ctx, db, 10)
	if err != nil {
		return result, err
	}
	result.Health, err = BuildCandidateHealth(ctx, db)
	return result, err
}

func candidateReportBatch(ctx context.Context, db *gorm.DB, date string) (UniverseBatch, bool, error) {
	var batch UniverseBatch
	query := db.WithContext(ctx).Where("kind = ? AND status = ?", BatchKindPrescreen, BatchStatusPublished)
	if date != "" {
		query = query.Where("effective_date = ? OR date(started_at) = ?", date, date)
	}
	err := query.Order("started_at DESC").First(&batch).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		current, ok, currentErr := currentPublishedPrescreenBatch(ctx, db)
		return current, ok, currentErr
	}
	if err != nil {
		return UniverseBatch{}, false, err
	}
	return batch, true, nil
}
