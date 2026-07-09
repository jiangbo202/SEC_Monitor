package discovery

import (
	"context"
	"errors"
	"fmt"

	"gorm.io/gorm"
)

const (
	CandidateHealthOK       = "ok"
	CandidateHealthDegraded = "degraded"
	CandidateHealthMissing  = "missing"
)

type CandidateHealth struct {
	BatchID           string   `json:"batch_id"`
	Status            string   `json:"status"`
	TotalCandidates   int      `json:"total_candidates"`
	MissingFinancials int      `json:"missing_financials"`
	MissingInsiders   int      `json:"missing_insiders"`
	MissingMarketCap  int      `json:"missing_market_cap"`
	ActiveRiskEvents  int      `json:"active_risk_events"`
	Issues            []string `json:"issues"`
}

func BuildCandidateHealth(ctx context.Context, db *gorm.DB) (CandidateHealth, error) {
	result := CandidateHealth{Status: CandidateHealthMissing, Issues: []string{}}
	if db == nil {
		return result, errors.New("database is required")
	}
	batch, ok, err := currentPublishedPrescreenBatch(ctx, db)
	if err != nil {
		return result, err
	}
	if !ok {
		result.Issues = append(result.Issues, "no_current_published_prescreen_batch")
		return result, nil
	}
	result.BatchID = batch.BatchID
	financialBatchID := batch.UniverseSourceVersion
	if financialBatchID == "" {
		financialBatchID = batch.BatchID
	}

	var scores []CandidateScoreSnapshot
	if err := db.WithContext(ctx).Where("batch_id = ? AND grade IN ?", batch.BatchID, []string{CandidateGradeA, CandidateGradeB}).Find(&scores).Error; err != nil {
		return result, err
	}
	result.TotalCandidates = len(scores)
	for _, score := range scores {
		if score.MarketCapUSD <= 0 {
			result.MissingMarketCap++
		}
		var financial FinancialMetricSnapshot
		err := db.WithContext(ctx).First(&financial, "batch_id = ? AND security_id = ?", financialBatchID, score.SecurityID).Error
		if errors.Is(err, gorm.ErrRecordNotFound) || (err == nil && !financial.RevenueGrowthAvailable && !financial.RunwayAvailable) {
			result.MissingFinancials++
		} else if err != nil {
			return result, err
		}
		var insiderCount int64
		if err := db.WithContext(ctx).Model(&InsiderTransactionSnapshot{}).Where("security_id = ? AND qualified = ?", score.SecurityID, true).Count(&insiderCount).Error; err != nil {
			return result, err
		}
		if insiderCount == 0 {
			result.MissingInsiders++
		}
	}
	var activeRiskEvents int64
	if err := db.WithContext(ctx).Model(&CapitalRiskSnapshot{}).Where("batch_id = ? AND active = ?", batch.BatchID, true).Count(&activeRiskEvents).Error; err != nil {
		return result, err
	}
	result.ActiveRiskEvents = int(activeRiskEvents)
	result.Status = CandidateHealthOK
	if result.MissingFinancials > 0 {
		result.Issues = append(result.Issues, fmt.Sprintf("missing_financials:%d", result.MissingFinancials))
	}
	if result.MissingInsiders > 0 {
		result.Issues = append(result.Issues, fmt.Sprintf("missing_insiders:%d", result.MissingInsiders))
	}
	if result.MissingMarketCap > 0 {
		result.Issues = append(result.Issues, fmt.Sprintf("missing_market_cap:%d", result.MissingMarketCap))
	}
	if len(result.Issues) > 0 {
		result.Status = CandidateHealthDegraded
	}
	return result, nil
}
