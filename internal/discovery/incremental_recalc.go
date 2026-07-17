package discovery

import (
	"context"
	"errors"
	"sort"
	"time"

	"gorm.io/gorm"
)

// RefreshCurrentCandidateFinancials recomputes only the current candidates
// whose SEC facts have changed. It intentionally keeps the current market
// batch and its price evidence unchanged: this is a financial re-score, not a
// market-data publication.
func RefreshCurrentCandidateFinancials(ctx context.Context, db *gorm.DB, securityIDs []uint, now time.Time) (int, error) {
	if db == nil {
		return 0, errors.New("database is required")
	}
	if len(securityIDs) == 0 {
		return 0, nil
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	var pointer CurrentBatchPointer
	if err := db.WithContext(ctx).First(&pointer, "kind = ?", BatchKindPrescreen).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, nil
		}
		return 0, err
	}
	var marketBatch UniverseBatch
	if err := db.WithContext(ctx).First(&marketBatch, "batch_id = ? AND kind = ? AND status = ?", pointer.BatchID, BatchKindPrescreen, BatchStatusPublished).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, nil
		}
		return 0, err
	}
	securityBatchID := marketBatch.UniverseSourceVersion
	if securityBatchID == "" {
		return 0, errors.New("current market batch has no security source version")
	}

	var universeRows []UniverseSnapshot
	if err := db.WithContext(ctx).Where("batch_id = ? AND security_id IN ?", marketBatch.BatchID, securityIDs).Find(&universeRows).Error; err != nil {
		return 0, err
	}
	if len(universeRows) == 0 {
		return 0, nil
	}
	ids := make([]uint, 0, len(universeRows))
	for _, row := range universeRows {
		ids = append(ids, row.SecurityID)
	}

	var factRows []FinancialFactSnapshot
	if err := db.WithContext(ctx).Where("security_id IN ? AND quality_status = ?", ids, QualityStatusValid).Find(&factRows).Error; err != nil {
		return 0, err
	}
	factsBySecurity := map[uint][]FinancialFact{}
	for _, row := range factRows {
		factsBySecurity[row.SecurityID] = append(factsBySecurity[row.SecurityID], FinancialFactFromSnapshot(row))
	}
	var insiders []InsiderTransactionSnapshot
	if err := db.WithContext(ctx).Where("security_id IN ?", ids).Find(&insiders).Error; err != nil {
		return 0, err
	}
	insidersBySecurity := map[uint][]InsiderTransactionSnapshot{}
	for _, row := range insiders {
		insidersBySecurity[row.SecurityID] = append(insidersBySecurity[row.SecurityID], row)
	}
	var risks []CapitalRiskSnapshot
	if err := db.WithContext(ctx).Where("batch_id = ? AND security_id IN ?", securityBatchID, ids).Find(&risks).Error; err != nil {
		return 0, err
	}
	risksBySecurity := map[uint][]CapitalRiskSnapshot{}
	for _, row := range risks {
		risksBySecurity[row.SecurityID] = append(risksBySecurity[row.SecurityID], row)
	}
	var identities []SecurityBatchIdentity
	if err := db.WithContext(ctx).Where("batch_id = ? AND security_id IN ?", securityBatchID, ids).Find(&identities).Error; err != nil {
		return 0, err
	}
	identityBySecurity := map[uint]SecurityBatchIdentity{}
	for _, row := range identities {
		identityBySecurity[row.SecurityID] = row
	}

	sort.Slice(universeRows, func(i, j int) bool { return universeRows[i].SecurityID < universeRows[j].SecurityID })
	updated := 0
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, row := range universeRows {
			metric, err := FinancialMetricFromFacts(securityBatchID, row.SecurityID, factsBySecurity[row.SecurityID], now)
			if err != nil {
				return err
			}
			var existingMetric FinancialMetricSnapshot
			err = tx.Where("batch_id = ? AND security_id = ?", securityBatchID, row.SecurityID).First(&existingMetric).Error
			if errors.Is(err, gorm.ErrRecordNotFound) {
				if err := tx.Create(&metric).Error; err != nil {
					return err
				}
			} else if err != nil {
				return err
			} else {
				metric.ID = existingMetric.ID
				if err := tx.Save(&metric).Error; err != nil {
					return err
				}
			}
			if row.QualityStatus != QualityStatusValid || row.MarketCapUSD <= 0 {
				updated++
				continue
			}
			score := ScoreDiscoveryCandidate(DiscoveryScoreInput{
				SecurityID: row.SecurityID, Ticker: row.Ticker, MarketCapUSD: row.MarketCapUSD,
				Financial: metric, Insiders: insidersBySecurity[row.SecurityID], Risks: risksBySecurity[row.SecurityID],
				GrossMarginPct: metric.GrossMarginPct, SectorScore: SectorRatingForSIC(identityBySecurity[row.SecurityID].SIC).Score, AsOf: now,
			})
			scoreRow := CandidateScoreToSnapshot(marketBatch.BatchID, score, now)
			var existingScore CandidateScoreSnapshot
			err = tx.Where("batch_id = ? AND security_id = ?", marketBatch.BatchID, row.SecurityID).First(&existingScore).Error
			if errors.Is(err, gorm.ErrRecordNotFound) {
				if err := tx.Create(&scoreRow).Error; err != nil {
					return err
				}
			} else if err != nil {
				return err
			} else {
				scoreRow.ID = existingScore.ID
				if err := tx.Save(&scoreRow).Error; err != nil {
					return err
				}
			}
			updated++
		}
		return nil
	})
	return updated, err
}
