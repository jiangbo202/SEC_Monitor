package discovery

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
)

type CandidateDetail struct {
	BatchID            string                         `json:"batch_id"`
	Security           Security                       `json:"security"`
	Universe           *UniverseSnapshot              `json:"universe,omitempty"`
	Score              CandidateScoreSnapshot         `json:"score"`
	Financial          *FinancialMetricSnapshot       `json:"financial,omitempty"`
	ProfitHistory      ProfitHistory                  `json:"profit_history"`
	Insiders           []InsiderTransactionSnapshot   `json:"insiders"`
	CapitalRisks       []CapitalRiskSnapshot          `json:"capital_risks"`
	CapitalRiskSummary CandidateCapitalRiskSummary    `json:"capital_risk_summary"`
	RecentFilings      []RecentSECFiling              `json:"recent_filings"`
	Sector             SectorExplanation              `json:"sector"`
	Technical          CandidateTechnicalAnalysis     `json:"technical"`
	TechnicalHistory   []CandidateTechnicalHistoryRow `json:"technical_history"`
	DataQuality        map[string]string              `json:"data_quality"`
	Evidence           []Evidence                     `json:"evidence"`
}

// CandidateCapitalRiskSummary separates the financing evidence retained for
// audit from the events that are currently actionable. Historical, inactive
// filings must not be presented as a current financing warning.
type CandidateCapitalRiskSummary struct {
	TotalEvents             int        `json:"total_events"`
	ActiveEvents            int        `json:"active_events"`
	RecentInactiveEvents    int        `json:"recent_inactive_events"`
	HistoricalInactiveCount int        `json:"historical_inactive_count"`
	LatestEffectiveAt       *time.Time `json:"latest_effective_at,omitempty"`
}

const candidateCapitalRiskRecentDays = 180

type RecentSECFiling struct {
	FilingID        string     `json:"filing_id"`
	AccessionNumber string     `json:"accession_number"`
	Ticker          string     `json:"ticker"`
	CIK             string     `json:"cik"`
	CompanyName     string     `json:"company_name"`
	FilingType      string     `json:"filing_type"`
	FilingDate      time.Time  `json:"filing_date"`
	PublishedAt     *time.Time `json:"published_at"`
	FilingURL       string     `json:"filing_url"`
	Title           string     `json:"title"`
}

func GetCandidateDetail(ctx context.Context, db *gorm.DB, ticker string) (CandidateDetail, error) {
	result := CandidateDetail{Insiders: []InsiderTransactionSnapshot{}, CapitalRisks: []CapitalRiskSnapshot{}, RecentFilings: []RecentSECFiling{}, TechnicalHistory: []CandidateTechnicalHistoryRow{}, ProfitHistory: ProfitHistory{Quarterly: []ProfitHistoryPoint{}, Annual: []ProfitHistoryPoint{}}, DataQuality: map[string]string{}, Evidence: []Evidence{}}
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
	evidenceBatchID := strings.TrimSpace(batch.UniverseSourceVersion)
	if evidenceBatchID == "" {
		evidenceBatchID = batch.BatchID
	}
	if err := db.WithContext(ctx).First(&result.Score, "batch_id = ? AND ticker = ?", batch.BatchID, symbol).Error; err != nil {
		return result, err
	}
	if err := db.WithContext(ctx).First(&result.Security, result.Score.SecurityID).Error; err != nil {
		return result, err
	}
	profitHistory, err := getProfitHistoryForSecurity(ctx, db, result.Score.SecurityID, result.Score.Ticker, time.Now().UTC())
	if err != nil {
		return result, err
	}
	result.ProfitHistory = profitHistory
	if batch.UniverseSourceVersion != "" {
		var identity SecurityBatchIdentity
		err := db.WithContext(ctx).First(&identity, "batch_id = ? AND security_id = ?", batch.UniverseSourceVersion, result.Score.SecurityID).Error
		if err == nil {
			applyIdentityToSecurity(&result.Security, identity)
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return result, err
		}
	}
	result.Sector = ExplainSectorScore(result.Score, result.Security)
	technicalItems := []CandidateScoreResult{{CandidateScoreSnapshot: result.Score}}
	if technicalItems, err = hydrateCandidatePriceEvidence(ctx, db, batch, technicalItems); err != nil {
		return result, err
	}
	if err = hydrateCandidateTechnicalAnalysis(ctx, db, technicalItems); err != nil {
		return result, err
	}
	result.Technical = technicalItems[0].Technical
	technicalHistory, err := candidateTechnicalPriceHistoryLimit(ctx, db, technicalItems[0], technicalDetailHistoryDays)
	if err != nil {
		return result, err
	}
	result.TechnicalHistory = candidateTechnicalHistoryRows(technicalHistory)
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
	if err := db.WithContext(ctx).First(&financial, "batch_id = ? AND security_id = ?", evidenceBatchID, result.Score.SecurityID).Error; err == nil {
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
	if len(result.ProfitHistory.Quarterly) > 0 || len(result.ProfitHistory.Annual) > 0 {
		result.DataQuality["profit_history"] = QualityStatusValid
	} else {
		result.DataQuality["profit_history"] = QualityStatusMissing
	}
	if err := db.WithContext(ctx).Where("security_id = ?", result.Score.SecurityID).Order("transaction_date DESC").Limit(20).Find(&result.Insiders).Error; err != nil {
		return result, err
	}
	if len(result.Insiders) > 0 {
		result.DataQuality["insider"] = QualityStatusValid
	} else {
		result.DataQuality["insider"] = QualityStatusMissing
	}
	var allCapitalRisks []CapitalRiskSnapshot
	if err := db.WithContext(ctx).Where("batch_id = ? AND security_id = ?", evidenceBatchID, result.Score.SecurityID).Order("active DESC, severity DESC, effective_at DESC").Find(&allCapitalRisks).Error; err != nil {
		return result, err
	}
	result.CapitalRiskSummary, result.CapitalRisks = summarizeCandidateCapitalRisks(allCapitalRisks, time.Now().UTC())
	result.DataQuality["capital_risk"] = QualityStatusValid
	if result.Technical.Status == TechnicalStatusReady {
		result.DataQuality["technical"] = QualityStatusValid
	} else {
		result.DataQuality["technical"] = result.Technical.Status
	}
	var filings []SECFilingSnapshot
	if err := db.WithContext(ctx).Where("security_id = ?", result.Score.SecurityID).Order("filing_date DESC, accepted_at DESC, id DESC").Limit(recentSECFilingLimit).Find(&filings).Error; err != nil {
		return result, err
	}
	result.RecentFilings = recentCandidateFilings(result.Security, result.Score.Ticker, filings)
	if len(result.RecentFilings) > 0 {
		result.DataQuality["recent_filings"] = QualityStatusValid
	} else {
		result.DataQuality["recent_filings"] = QualityStatusMissing
	}
	result.Evidence = candidateDetailEvidence(result)
	return result, nil
}

func recentCandidateFilings(security Security, ticker string, filings []SECFilingSnapshot) []RecentSECFiling {
	result := make([]RecentSECFiling, 0, len(filings))
	for _, filing := range filings {
		title := strings.TrimSpace(filing.FilingType)
		if items := strings.TrimSpace(filing.Items); items != "" {
			title += " — Items " + items
		}
		result = append(result, RecentSECFiling{
			FilingID: fmt.Sprintf("%d:%s", filing.SecurityID, filing.AccessionNumber), AccessionNumber: filing.AccessionNumber,
			Ticker: strings.ToUpper(strings.TrimSpace(ticker)), CIK: security.CIK, CompanyName: security.CompanyName, FilingType: filing.FilingType,
			FilingDate: filing.FilingDate, PublishedAt: filing.AcceptedAt, FilingURL: filing.FilingURL, Title: title,
		})
	}
	return result
}

func applyIdentityToSecurity(security *Security, identity SecurityBatchIdentity) {
	if security == nil {
		return
	}
	if strings.TrimSpace(identity.CompanyName) != "" {
		security.CompanyName = identity.CompanyName
	}
	if identity.SIC != 0 {
		security.SIC = identity.SIC
	}
	if strings.TrimSpace(identity.StateOfIncorporation) != "" {
		security.StateOfIncorporation = identity.StateOfIncorporation
	}
	if strings.TrimSpace(identity.LatestAnnualForm) != "" {
		security.LatestAnnualForm = identity.LatestAnnualForm
	}
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
	if detail.CapitalRiskSummary.TotalEvents > 0 {
		evidence = append(evidence, Evidence{Field: "capital_risk_events", Value: fmt.Sprintf("%d", detail.CapitalRiskSummary.TotalEvents), Source: "capital_risk_snapshots"})
	}
	return evidence
}

func summarizeCandidateCapitalRisks(rows []CapitalRiskSnapshot, now time.Time) (CandidateCapitalRiskSummary, []CapitalRiskSnapshot) {
	summary := CandidateCapitalRiskSummary{}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	cutoff := now.AddDate(0, 0, -candidateCapitalRiskRecentDays)
	current := make([]CapitalRiskSnapshot, 0, len(rows))
	for _, row := range rows {
		summary.TotalEvents++
		if summary.LatestEffectiveAt == nil || row.EffectiveAt.After(*summary.LatestEffectiveAt) {
			latest := row.EffectiveAt
			summary.LatestEffectiveAt = &latest
		}
		if row.Active {
			summary.ActiveEvents++
			current = append(current, row)
			continue
		}
		if !row.EffectiveAt.IsZero() && !row.EffectiveAt.Before(cutoff) {
			summary.RecentInactiveEvents++
			current = append(current, row)
			continue
		}
		summary.HistoricalInactiveCount++
	}
	return summary, current
}
