package discovery

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"gorm.io/gorm"
)

type CandidateDetail struct {
	BatchID      string                       `json:"batch_id"`
	Security     Security                     `json:"security"`
	Universe     *UniverseSnapshot            `json:"universe,omitempty"`
	Score        CandidateScoreSnapshot       `json:"score"`
	Financial    *FinancialMetricSnapshot     `json:"financial,omitempty"`
	Insiders     []InsiderTransactionSnapshot `json:"insiders"`
	CapitalRisks []CapitalRiskSnapshot        `json:"capital_risks"`
	Sector       SectorExplanation            `json:"sector"`
	DataQuality  map[string]string            `json:"data_quality"`
	Evidence     []Evidence                   `json:"evidence"`
}

func GetCandidateDetail(ctx context.Context, db *gorm.DB, ticker string) (CandidateDetail, error) {
	result := CandidateDetail{Insiders: []InsiderTransactionSnapshot{}, CapitalRisks: []CapitalRiskSnapshot{}, DataQuality: map[string]string{}, Evidence: []Evidence{}}
	if db == nil {
		return result, errors.New("database is required")
	}
	if ctx == nil {
		return result, errors.New("context is required")
	}
	symbol := strings.ToUpper(strings.TrimSpace(ticker))
	if symbol == "" {
		return result, errors.New("ticker is required")
	}
	batch, ok, err := currentPublishedPrescreenBatch(ctx, db)
	if err != nil {
		return result, err
	}
	if !ok {
		return result, gorm.ErrRecordNotFound
	}
	result.BatchID = batch.BatchID
	if err := db.WithContext(ctx).First(&result.Score, "batch_id = ? AND ticker = ?", batch.BatchID, symbol).Error; err != nil {
		return result, err
	}
	if err := db.WithContext(ctx).First(&result.Security, result.Score.SecurityID).Error; err != nil {
		return result, err
	}
	result.Sector = ExplainSectorScore(result.Score, result.Security)
	var universe UniverseSnapshot
	if err := db.WithContext(ctx).First(&universe, "batch_id = ? AND security_id = ?", batch.BatchID, result.Score.SecurityID).Error; err == nil {
		result.Universe = &universe
		result.DataQuality["universe"] = stringOrDefault(universe.QualityStatus, QualityStatusMissing)
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return result, err
	} else {
		result.DataQuality["universe"] = QualityStatusMissing
	}
	var financial FinancialMetricSnapshot
	if err := db.WithContext(ctx).First(&financial, "batch_id = ? AND security_id = ?", batch.BatchID, result.Score.SecurityID).Error; err == nil {
		result.Financial = &financial
		if financial.RevenueGrowthAvailable || financial.RunwayAvailable {
			result.DataQuality["financial"] = QualityStatusValid
		} else {
			result.DataQuality["financial"] = QualityStatusMissing
		}
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return result, err
	} else {
		result.DataQuality["financial"] = QualityStatusMissing
	}
	if err := db.WithContext(ctx).Where("security_id = ?", result.Score.SecurityID).Order("transaction_date DESC").Limit(20).Find(&result.Insiders).Error; err != nil {
		return result, err
	}
	if len(result.Insiders) > 0 {
		result.DataQuality["insider"] = QualityStatusValid
	} else {
		result.DataQuality["insider"] = QualityStatusMissing
	}
	if err := db.WithContext(ctx).Where("batch_id = ? AND security_id = ?", batch.BatchID, result.Score.SecurityID).Order("severity DESC, effective_at DESC").Find(&result.CapitalRisks).Error; err != nil {
		return result, err
	}
	result.DataQuality["capital_risk"] = QualityStatusValid
	result.Evidence = candidateDetailEvidence(result)
	return result, nil
}

func stringOrDefault(value string, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func candidateDetailEvidence(detail CandidateDetail) []Evidence {
	evidence := []Evidence{
		{Field: "grade", Value: detail.Score.Grade, Source: "candidate_score_snapshots"},
		{Field: "total_score", Value: fmt.Sprintf("%d", detail.Score.TotalScore), Source: "candidate_score_snapshots"},
		{Field: "market_cap_usd", Value: fmt.Sprintf("%d", detail.Score.MarketCapUSD), Source: "candidate_score_snapshots"},
		{Field: "revenue_growth_pct", Value: fmt.Sprintf("%.2f", detail.Score.RevenueGrowthPct), Source: "candidate_score_snapshots"},
		{Field: "cash_runway_months", Value: fmt.Sprintf("%.2f", detail.Score.CashRunwayMonths), Source: "candidate_score_snapshots"},
	}
	if detail.Financial != nil {
		evidence = append(evidence,
			Evidence{Field: "quarterly_revenue_yoy_pct", Value: fmt.Sprintf("%.2f", detail.Financial.QuarterlyRevenueYoYPct), Source: "financial_metric_snapshots"},
			Evidence{Field: "cash_runway_months", Value: fmt.Sprintf("%.2f", detail.Financial.CashRunwayMonths), Source: "financial_metric_snapshots"},
		)
	}
	if len(detail.Insiders) > 0 {
		evidence = append(evidence, Evidence{Field: "qualified_insider_buys", Value: fmt.Sprintf("%d", len(detail.Insiders)), Source: "insider_transaction_snapshots"})
	}
	if len(detail.CapitalRisks) > 0 {
		evidence = append(evidence, Evidence{Field: "capital_risk_events", Value: fmt.Sprintf("%d", len(detail.CapitalRisks)), Source: "capital_risk_snapshots"})
	}
	return evidence
}
