package discovery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"gorm.io/gorm"
)

const (
	CandidateHealthOK       = "ok"
	CandidateHealthDegraded = "degraded"
	CandidateHealthMissing  = "missing"
)

type CandidateHealth struct {
	BatchID                      string   `json:"batch_id"`
	Status                       string   `json:"status"`
	TotalCandidates              int      `json:"total_candidates"`
	MissingFinancials            int      `json:"missing_financials"`
	MissingInsiders              int      `json:"missing_insiders"`
	InsiderDataStatus            string   `json:"insider_data_status"`
	CandidatesWithInsiderRecords int      `json:"candidates_with_insider_records"`
	InsiderRecordCoveragePct     float64  `json:"insider_record_coverage_pct"`
	QualifiedInsiderCandidates   int      `json:"qualified_insider_candidates"`
	NoQualifiedInsiderCandidates int      `json:"no_qualified_insider_candidates"`
	PriceEffectiveDate           string   `json:"price_effective_date"`
	CurrentPriceCandidates       int      `json:"current_price_candidates"`
	FallbackPriceCandidates      int      `json:"fallback_price_candidates"`
	StalePriceCandidates         int      `json:"stale_price_candidates"`
	MissingPriceCandidates       int      `json:"missing_price_candidates"`
	MissingMarketCap             int      `json:"missing_market_cap"`
	ActiveRiskEvents             int      `json:"active_risk_events"`
	Issues                       []string `json:"issues"`
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
	result.PriceEffectiveDate = batch.EffectiveDate
	insiderDataAvailable, err := candidateInsiderDataAvailable(ctx, db, batch)
	if err != nil {
		return result, err
	}
	result.InsiderDataStatus = "missing"
	if insiderDataAvailable {
		result.InsiderDataStatus = "available"
	}
	financialBatchID := batch.UniverseSourceVersion
	if financialBatchID == "" {
		financialBatchID = batch.BatchID
	}

	var scores []CandidateScoreSnapshot
	if err := db.WithContext(ctx).Where("batch_id = ? AND grade IN ?", batch.BatchID, []string{CandidateGradeA, CandidateGradeB}).Find(&scores).Error; err != nil {
		return result, err
	}
	result.TotalCandidates = len(scores)
	priceFreshnessBySecurity, err := candidatePriceFreshnessBySecurity(ctx, db, batch, scores)
	if err != nil {
		return result, err
	}
	insiderCoverageBySecurity, err := candidateInsiderCoverageBySecurity(ctx, db, scores)
	if err != nil {
		return result, err
	}
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
		coverage := insiderCoverageBySecurity[score.SecurityID]
		if coverage.records > 0 {
			result.CandidatesWithInsiderRecords++
		}
		if !insiderDataAvailable {
			result.MissingInsiders++
		} else if !coverage.qualified {
			result.NoQualifiedInsiderCandidates++
		} else {
			result.QualifiedInsiderCandidates++
		}
		switch priceFreshnessBySecurity[score.SecurityID] {
		case PriceFreshnessCurrent:
			result.CurrentPriceCandidates++
		case PriceFreshnessPreviousTradingDay:
			result.FallbackPriceCandidates++
		case PriceFreshnessStale, PriceFreshnessFuture:
			result.StalePriceCandidates++
		case PriceFreshnessMissing:
			result.MissingPriceCandidates++
		}
	}
	if result.TotalCandidates > 0 {
		result.InsiderRecordCoveragePct = float64(result.CandidatesWithInsiderRecords) * 100 / float64(result.TotalCandidates)
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
		result.Issues = append(result.Issues, fmt.Sprintf("missing_insider_data:%d", result.MissingInsiders))
	}
	if insiderDataAvailable && result.TotalCandidates > 0 && result.CandidatesWithInsiderRecords == 0 {
		result.Issues = append(result.Issues, fmt.Sprintf("candidate_insider_records:0/%d", result.TotalCandidates))
	}
	if result.MissingMarketCap > 0 {
		result.Issues = append(result.Issues, fmt.Sprintf("missing_market_cap:%d", result.MissingMarketCap))
	}
	if result.FallbackPriceCandidates > 0 {
		result.Issues = append(result.Issues, fmt.Sprintf("price_previous_trading_day:%d", result.FallbackPriceCandidates))
	}
	if result.StalePriceCandidates > 0 {
		result.Issues = append(result.Issues, fmt.Sprintf("stale_prices:%d", result.StalePriceCandidates))
	}
	if result.MissingPriceCandidates > 0 {
		result.Issues = append(result.Issues, fmt.Sprintf("missing_prices:%d", result.MissingPriceCandidates))
	}
	if result.MissingFinancials > 0 || result.MissingInsiders > 0 || result.MissingMarketCap > 0 || result.StalePriceCandidates > 0 || result.MissingPriceCandidates > 0 {
		result.Status = CandidateHealthDegraded
	}
	return result, nil
}

type candidateInsiderCoverage struct {
	records   int
	qualified bool
}

func candidateInsiderCoverageBySecurity(ctx context.Context, db *gorm.DB, scores []CandidateScoreSnapshot) (map[uint]candidateInsiderCoverage, error) {
	result := map[uint]candidateInsiderCoverage{}
	if len(scores) == 0 {
		return result, nil
	}
	securityIDs := make([]uint, 0, len(scores))
	for _, score := range scores {
		securityIDs = append(securityIDs, score.SecurityID)
	}
	var rows []InsiderTransactionSnapshot
	if err := db.WithContext(ctx).Where("security_id IN ?", securityIDs).Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		coverage := result[row.SecurityID]
		coverage.records++
		coverage.qualified = coverage.qualified || row.Qualified
		result[row.SecurityID] = coverage
	}
	return result, nil
}

func candidatePriceFreshnessBySecurity(ctx context.Context, db *gorm.DB, batch UniverseBatch, scores []CandidateScoreSnapshot) (map[uint]string, error) {
	result := make(map[uint]string, len(scores))
	if len(scores) == 0 {
		return result, nil
	}
	securityIDs := make([]uint, 0, len(scores))
	for _, score := range scores {
		securityIDs = append(securityIDs, score.SecurityID)
	}
	var universeRows []UniverseSnapshot
	if err := db.WithContext(ctx).Where("batch_id = ? AND security_id IN ?", batch.BatchID, securityIDs).Find(&universeRows).Error; err != nil {
		return nil, err
	}
	priceIDBySecurity := map[uint]uint{}
	priceIDs := []uint{}
	for _, row := range universeRows {
		if row.PriceSnapshotID == nil {
			continue
		}
		priceIDBySecurity[row.SecurityID] = *row.PriceSnapshotID
		priceIDs = append(priceIDs, *row.PriceSnapshotID)
	}
	priceByID := map[uint]PriceSnapshot{}
	if len(priceIDs) > 0 {
		var prices []PriceSnapshot
		if err := db.WithContext(ctx).Where("id IN ?", priceIDs).Find(&prices).Error; err != nil {
			return nil, err
		}
		for _, price := range prices {
			priceByID[price.ID] = price
		}
	}
	for _, score := range scores {
		price, ok := priceByID[priceIDBySecurity[score.SecurityID]]
		if !ok {
			status, _ := candidatePriceFreshness(batch.EffectiveDate, nil)
			result[score.SecurityID] = status
			continue
		}
		status, _ := candidatePriceFreshness(batch.EffectiveDate, &price.TradeDate)
		result[score.SecurityID] = status
	}
	return result, nil
}

func candidateInsiderDataAvailable(ctx context.Context, db *gorm.DB, marketBatch UniverseBatch) (bool, error) {
	available, err := sourceVersionsContainPrefix(marketBatch.SourceVersionsJSON, "insiders:")
	if err != nil || available || strings.TrimSpace(marketBatch.UniverseSourceVersion) == "" {
		return available, err
	}
	var securityBatch UniverseBatch
	if err := db.WithContext(ctx).First(&securityBatch, "batch_id = ?", marketBatch.UniverseSourceVersion).Error; err != nil {
		return false, err
	}
	return sourceVersionsContainPrefix(securityBatch.SourceVersionsJSON, "insiders:")
}

func sourceVersionsContainPrefix(payload, prefix string) (bool, error) {
	if strings.TrimSpace(payload) == "" {
		return false, nil
	}
	var versions []SourceVersion
	if err := json.Unmarshal([]byte(payload), &versions); err != nil {
		return false, fmt.Errorf("decode source versions: %w", err)
	}
	for _, version := range versions {
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(version.Source)), strings.ToLower(prefix)) {
			return true, nil
		}
	}
	return false, nil
}
