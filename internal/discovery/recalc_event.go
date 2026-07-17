package discovery

import (
	"context"
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	CandidateRecalcStatusDirty = "dirty"
)

type CandidateRecalcFilingInput struct {
	FilingID        string
	AccessionNumber string
	Ticker          string
	CIK             string
	FilingType      string
	FilingDate      time.Time
}

func RecordCandidateRecalcEventForFiling(ctx context.Context, db *gorm.DB, input CandidateRecalcFilingInput) (bool, error) {
	if db == nil {
		return false, errors.New("database is required")
	}
	if ctx == nil {
		return false, errors.New("context is required")
	}
	if !candidateFinancialRecalcForm(input.FilingType) {
		return false, nil
	}
	var pointer CurrentBatchPointer
	err := db.WithContext(ctx).First(&pointer, "kind = ?", BatchKindPrescreen).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var batch UniverseBatch
	err = db.WithContext(ctx).First(&batch, "batch_id = ? AND kind = ? AND status = ?", pointer.BatchID, BatchKindPrescreen, BatchStatusPublished).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	ticker := strings.ToUpper(strings.TrimSpace(input.Ticker))
	cik := strings.TrimSpace(input.CIK)
	var candidate CandidateScoreSnapshot
	query := db.WithContext(ctx).Where("batch_id = ?", batch.BatchID)
	switch {
	case ticker != "" && cik != "":
		query = query.Where("ticker = ? OR security_id IN (SELECT security_id FROM security_batch_identities WHERE batch_id = ? AND cik = ?)", ticker, batch.UniverseSourceVersion, cik)
	case ticker != "":
		query = query.Where("ticker = ?", ticker)
	case cik != "":
		query = query.Where("security_id IN (SELECT security_id FROM security_batch_identities WHERE batch_id = ? AND cik = ?)", batch.UniverseSourceVersion, cik)
	default:
		return false, nil
	}
	err = query.Order("id DESC").First(&candidate).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	event := CandidateRecalcEvent{
		BatchID: batch.BatchID, SecurityID: candidate.SecurityID, Ticker: candidate.Ticker, CIK: cik,
		FilingID: input.FilingID, AccessionNumber: input.AccessionNumber, FilingType: strings.ToUpper(strings.TrimSpace(input.FilingType)),
		FilingDate: input.FilingDate, Status: CandidateRecalcStatusDirty,
		Reason: "new SEC filing matched current small-cap candidate; candidate score should be refreshed by incremental or next batch sync",
	}
	res := db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&event)
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected == 1, nil
}

// Only filings that can update the structured Company Facts metrics enter the
// financial re-evaluation queue. 8-K/Form 4 events remain visible in the SEC
// evidence feed but do not cause needless financial recalculation.
func candidateFinancialRecalcForm(form string) bool {
	switch strings.ToUpper(strings.TrimSpace(form)) {
	case "10-Q", "10-Q/A", "10-K", "10-K/A":
		return true
	default:
		return false
	}
}
