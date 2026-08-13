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
	CompanyProfile     CompanyProfile                 `json:"company_profile"`
	AnalystRating      AnalystRatingView              `json:"analyst_rating"`
	MarketResearch     CandidateMarketResearch        `json:"market_research"`
	OptionResearch     OptionResearchView             `json:"option_research"`
	Universe           *UniverseSnapshot              `json:"universe,omitempty"`
	Score              CandidateScoreSnapshot         `json:"score"`
	ScoreHistory       []CandidateScoreHistoryPoint   `json:"score_history"`
	SignalEvents       []CandidateSignalEvent         `json:"signal_events"`
	Financial          *FinancialMetricSnapshot       `json:"financial,omitempty"`
	ProfitHistory      ProfitHistory                  `json:"profit_history"`
	Insiders           []InsiderTransactionSnapshot   `json:"insiders"`
	InsiderCoverage    *InsiderCoverageSnapshot       `json:"insider_coverage,omitempty"`
	CapitalRisks       []CapitalRiskSnapshot          `json:"capital_risks"`
	CapitalRiskSummary CandidateCapitalRiskSummary    `json:"capital_risk_summary"`
	RecentFilings      []RecentSECFiling              `json:"recent_filings"`
	Sector             SectorExplanation              `json:"sector"`
	BusinessModel      CandidateBusinessModelEvidence `json:"business_model"`
	Valuation          CandidateValuation             `json:"valuation"`
	ValuationResearch  CandidateValuationResearch     `json:"valuation_research"`
	FairValue          CandidateFairValueEstimate     `json:"fair_value"`
	Research           *CandidateWatch                `json:"research,omitempty"`
	ResearchVersions   []CandidateResearchMemoVersion `json:"research_versions"`
	ResearchReadiness  CandidateResearchReadiness     `json:"research_readiness"`
	ResearchNextStep   CandidateResearchNextStep      `json:"research_next_step"`
	Technical          CandidateTechnicalAnalysis     `json:"technical"`
	TradeSetupHistory  []TradeSetupStatusEvent        `json:"trade_setup_history"`
	Investability      CandidateInvestability         `json:"investability"`
	DilutionTrend      CandidateDilutionTrend         `json:"dilution_trend"`
	TechnicalHistory   []CandidateTechnicalHistoryRow `json:"technical_history"`
	DataQuality        map[string]string              `json:"data_quality"`
	DataLineage        CandidateDataLineage           `json:"data_lineage"`
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
	result := CandidateDetail{ScoreHistory: []CandidateScoreHistoryPoint{}, SignalEvents: []CandidateSignalEvent{}, Insiders: []InsiderTransactionSnapshot{}, CapitalRisks: []CapitalRiskSnapshot{}, RecentFilings: []RecentSECFiling{}, ResearchVersions: []CandidateResearchMemoVersion{}, TechnicalHistory: []CandidateTechnicalHistoryRow{}, TradeSetupHistory: []TradeSetupStatusEvent{}, ProfitHistory: ProfitHistory{Quarterly: []ProfitHistoryPoint{}, Annual: []ProfitHistoryPoint{}}, AnalystRating: AnalystRatingView{History: []AnalystRatingSnapshot{}}, MarketResearch: CandidateMarketResearch{EPSForecast: EPSForecastView{History: []EPSForecastSnapshot{}}, Anomalies: []MarketAnomalySnapshot{}, InstitutionalHolders: []InstitutionalHolderSnapshot{}, FundHolders: []FundHolderSnapshot{}}, OptionResearch: OptionResearchView{History: []OptionResearchSnapshot{}}, ValuationResearch: CandidateValuationResearch{History: []ValuationResearchSnapshot{}}, DataQuality: map[string]string{}, Evidence: []Evidence{}}
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
	var research CandidateWatch
	if err := db.WithContext(ctx).First(&research, "ticker = ? AND status = ?", symbol, CandidateWatchStatusActive).Error; err == nil {
		result.Research = &research
		if err := db.WithContext(ctx).Where("ticker = ?", symbol).Order("version DESC").Limit(20).Find(&result.ResearchVersions).Error; err != nil {
			return result, err
		}
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return result, err
	}
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
	if result.ScoreHistory, err = candidateScoreHistory(ctx, db, result.Score.SecurityID); err != nil {
		return result, err
	}
	if result.SignalEvents, err = candidateSignalEvents(ctx, db, result.Score.SecurityID); err != nil {
		return result, err
	}
	if result.TradeSetupHistory, err = GetTradeSetupStatusHistory(ctx, db, result.Score.Ticker, 100); err != nil {
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
	profile, profileErr := GetCompanyProfile(ctx, db, result.Score.Ticker, result.Security.CIK)
	if profileErr != nil {
		return result, profileErr
	}
	result.CompanyProfile = profile
	analystRating, analystRatingErr := GetAnalystRating(ctx, db, result.Score.Ticker)
	if analystRatingErr != nil {
		return result, analystRatingErr
	}
	result.AnalystRating = analystRating
	if analystRating.Latest != nil {
		result.DataQuality["analyst_rating"] = analystRating.Latest.Status
	} else {
		result.DataQuality["analyst_rating"] = QualityStatusMissing
	}
	marketResearch, marketResearchErr := GetCandidateMarketResearch(ctx, db, result.Score.Ticker)
	if marketResearchErr != nil {
		return result, marketResearchErr
	}
	result.MarketResearch = marketResearch
	if optionResearch, optionResearchErr := GetOptionResearch(ctx, db, result.Score.Ticker); optionResearchErr == nil {
		result.OptionResearch = optionResearch
	} else {
		result.DataQuality["option_research"] = "local option research unavailable"
	}
	if marketResearch.EPSForecast.Latest != nil {
		result.DataQuality["eps_forecast"] = QualityStatusValid
	} else {
		result.DataQuality["eps_forecast"] = QualityStatusMissing
	}
	result.Sector = ExplainSectorScore(result.Score, result.Security)
	businessModels, err := activeCandidateBusinessModels(ctx, db, []uint{result.Score.SecurityID})
	if err != nil {
		return result, err
	}
	var businessModelOverride *CandidateBusinessModelOverride
	if row, ok := businessModels[result.Score.SecurityID]; ok {
		businessModelOverride = &row
	}
	result.BusinessModel = candidateBusinessModelEvidence(businessModelOverride, result.Sector.Category == "生物医药")
	technicalItems := []CandidateScoreResult{{CandidateScoreSnapshot: result.Score}}
	if technicalItems, err = hydrateCandidatePriceEvidence(ctx, db, batch, technicalItems); err != nil {
		return result, err
	}
	if err = hydrateCandidateValuations(ctx, db, batch, batch.UniverseSourceVersion, technicalItems); err != nil {
		return result, err
	}
	result.Valuation = technicalItems[0].Valuation
	valuationResearch, valuationResearchErr := GetCandidateValuationResearch(ctx, db, result.Score.Ticker)
	if valuationResearchErr != nil {
		return result, valuationResearchErr
	}
	result.ValuationResearch = valuationResearch
	if valuationResearch.Latest != nil {
		result.DataQuality["longbridge_valuation"] = QualityStatusValid
	} else {
		result.DataQuality["longbridge_valuation"] = QualityStatusMissing
	}
	if err = hydrateCandidateTechnicalAnalysis(ctx, db, technicalItems); err != nil {
		return result, err
	}
	result.Technical = technicalItems[0].Technical
	result.FairValue = buildCandidateFairValueEstimate(result.Technical, result.AnalystRating.Latest, result.ValuationResearch.Latest)
	result.DataQuality["fair_value"] = result.FairValue.Status
	technicalHistory, err := candidateTechnicalPriceHistoryLimit(ctx, db, technicalItems[0], technicalDetailHistoryDays)
	if err != nil {
		return result, err
	}
	result.TechnicalHistory = candidateTechnicalHistoryRows(technicalHistory)
	var universe UniverseSnapshot
	var shareEvidence *ShareSnapshot
	if err := db.WithContext(ctx).First(&universe, "batch_id = ? AND security_id = ?", batch.BatchID, result.Score.SecurityID).Error; err == nil {
		result.Universe = &universe
		result.DataQuality["universe"] = stringOrDefault(universe.QualityStatus, QualityStatusMissing)
		if universe.ShareSnapshotID != nil {
			var share ShareSnapshot
			if shareErr := db.WithContext(ctx).First(&share, *universe.ShareSnapshotID).Error; shareErr == nil {
				shareEvidence = &share
			} else if !errors.Is(shareErr, gorm.ErrRecordNotFound) {
				return result, shareErr
			}
		}
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
	var insiderCoverage InsiderCoverageSnapshot
	if err := db.WithContext(ctx).First(&insiderCoverage, "batch_id = ? AND security_id = ?", evidenceBatchID, result.Score.SecurityID).Error; err == nil {
		result.InsiderCoverage = &insiderCoverage
		if insiderCoverage.Status == InsiderCoveragePartial || insiderCoverage.Status == InsiderCoverageUnavailable {
			result.DataQuality["insider_coverage"] = insiderCoverage.Status
		} else {
			result.DataQuality["insider_coverage"] = QualityStatusValid
		}
	} else if errors.Is(err, gorm.ErrRecordNotFound) {
		result.DataQuality["insider_coverage"] = "legacy_not_evaluated"
	} else {
		return result, err
	}
	var allCapitalRisks []CapitalRiskSnapshot
	if err := db.WithContext(ctx).Where("batch_id = ? AND security_id = ?", evidenceBatchID, result.Score.SecurityID).Order("active DESC, severity DESC, effective_at DESC").Find(&allCapitalRisks).Error; err != nil {
		return result, err
	}
	result.CapitalRiskSummary, result.CapitalRisks = summarizeCandidateCapitalRisks(allCapitalRisks, time.Now().UTC())
	technicalItems[0].CapitalRiskSummaries = make([]CapitalRiskSummary, 0, len(result.CapitalRisks))
	for _, risk := range result.CapitalRisks {
		technicalItems[0].CapitalRiskSummaries = append(technicalItems[0].CapitalRiskSummaries, CapitalRiskSummary{Kind: risk.Kind, Severity: risk.Severity, BlocksA: risk.BlocksA, BlocksB: risk.BlocksB, Reason: risk.Reason, EffectiveAt: risk.EffectiveAt})
	}
	if err = hydrateCandidateMarketQuality(ctx, db, technicalItems); err != nil {
		return result, err
	}
	if err = hydrateCandidateDilutionTrends(ctx, db, batch, technicalItems); err != nil {
		return result, err
	}
	result.Investability = buildCandidateInvestability(technicalItems[0])
	result.DilutionTrend = technicalItems[0].DilutionTrend
	technicalItems[0].BusinessModel = result.BusinessModel
	technicalItems[0].Investability = result.Investability
	technicalItems[0].DilutionTrend = result.DilutionTrend
	if err = hydrateCandidateResearchReadiness(ctx, db, batch, technicalItems); err != nil {
		return result, err
	}
	result.ResearchReadiness = technicalItems[0].ResearchReadiness
	result.ResearchNextStep = recommendCandidateResearchNextStep(result.ResearchReadiness, result.Technical)
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
	result.DataLineage = buildCandidateDataLineage(result, batch, evidenceBatchID, technicalItems[0], shareEvidence)
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
	if strings.TrimSpace(identity.SICDescription) != "" {
		security.SICDescription = identity.SICDescription
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
	if detail.InsiderCoverage != nil {
		evidence = append(evidence, Evidence{Field: "insider_coverage", Value: detail.InsiderCoverage.Status, Source: "insider_coverage_snapshots"})
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
