package discovery

import (
	"context"
	"strings"
	"time"

	"gorm.io/gorm"
)

const (
	CandidateResearchReadinessReady        = "ready"
	CandidateResearchReadinessResearchOnly = "research_only"
	CandidateResearchReadinessBlocked      = "blocked"

	defaultQuarterlyFinancialStalenessDays = 190
	defaultAnnualFinancialStalenessDays    = 400
)

// CandidateResearchReadiness deliberately describes evidence availability,
// rather than company quality. A low-quality but fully evidenced company can
// be ready; an otherwise strong candidate with unavailable pricing cannot.
type CandidateResearchReadiness struct {
	Status                 string   `json:"status"`
	Reasons                []string `json:"reasons"`
	FinancialStalenessDays int      `json:"financial_staleness_days"`
	FinancialPeriodEnd     string   `json:"financial_period_end"`
	InsiderEvidenceStatus  string   `json:"insider_evidence_status"`
}

// CandidateResearchNextStep turns evidence state into one concrete research
// action. It is a workflow aid, not an investment recommendation: a ready
// candidate still needs an independent thesis and risk review.
type CandidateResearchNextStep struct {
	Status    string   `json:"status"`
	Priority  string   `json:"priority"`
	Action    string   `json:"action"`
	Rationale string   `json:"rationale"`
	Reasons   []string `json:"reasons"`
}

func recommendCandidateResearchNextStep(readiness CandidateResearchReadiness, technical CandidateTechnicalAnalysis) CandidateResearchNextStep {
	result := CandidateResearchNextStep{Status: readiness.Status, Priority: "normal", Reasons: append([]string{}, readiness.Reasons...)}
	hasReason := func(want string) bool {
		for _, reason := range readiness.Reasons {
			if reason == want {
				return true
			}
		}
		return false
	}
	switch readiness.Status {
	case CandidateResearchReadinessBlocked:
		result.Priority = "blocked"
		switch {
		case hasReason("market_price_unavailable"), hasReason("market_price_freshness_unknown"), hasReason("market_price_not_current"):
			result.Action = "等待并补齐最近有效收盘价"
			result.Rationale = "当前市场价格证据不可用或不够新，市值、估值与技术判断均不应据此作出结论。"
		case hasReason("market_cap_unavailable"):
			result.Action = "核对股本与市值数据"
			result.Rationale = "缺少可靠市值，无法确认是否仍在设定的小盘候选范围内。"
		case hasReason("investability_blocked"):
			result.Action = "先复核流动性与可交易性限制"
			result.Rationale = "当前流动性证据触发阻断，不应进入默认研究通知或仓位讨论。"
		default:
			result.Action = "先复核阻断证据"
			result.Rationale = "存在阻断项；在证据恢复前仅保留历史研究记录。"
		}
		return result
	case CandidateResearchReadinessResearchOnly:
		result.Priority = "review"
		switch {
		case hasReason("biotech_business_model_unconfirmed"), hasReason("biotech_business_model_review_due"):
			result.Action = "确认业务模型与收入可重复性"
			result.Rationale = "生物医药收入的可持续性尚未人工确认，收入分与候选可行动性需要复核。"
		case hasReason("financial_metrics_unavailable"), hasReason("financial_period_stale"):
			result.Action = "核对最新 10-Q / 10-K 财务指标"
			result.Rationale = "收入增长或现金 runway 证据缺失/过期，先确认最新财务期再更新研究判断。"
		case hasReason("insider_source_unavailable"), hasReason("insider_coverage_missing"), hasReason("insider_coverage_partial"), hasReason("insider_coverage_unavailable"):
			result.Action = "复核 Form 4 覆盖情况"
			result.Rationale = "内幕交易证据未完整覆盖，不能把“未发现买入”解释为“没有买入”。"
		case hasReason("share_dilution_high"):
			result.Action = "复核股本变化与融资文件"
			result.Rationale = "检测到较高稀释趋势，应先判断是否存在持续融资或可转股压力。"
		case hasReason("investability_constrained"):
			result.Action = "复核流动性约束"
			result.Rationale = "标的可研究但流动性受限，应以成交额和参与上限约束后续观察。"
		default:
			result.Action = "补齐待核验证据"
			result.Rationale = "基础筛选通过，但关键证据尚不完整，因此不进入默认候选通知。"
		}
		return result
	default:
		if technical.Status == TechnicalStatusReady && len(technical.Signals) > 0 {
			result.Action = "核对技术信号与近期催化剂"
			result.Rationale = "技术事件出现后，应结合最新 SEC 文件确认催化剂、失效条件与流动性。"
			return result
		}
		result.Action = "阅读近期 SEC 文件并设定研究论点"
		result.Rationale = "关键证据已完整；下一步是形成可证伪的论点、催化剂和失效条件。"
		return result
	}
}

func hydrateCandidateResearchReadiness(ctx context.Context, db *gorm.DB, batch UniverseBatch, items []CandidateScoreResult) error {
	if len(items) == 0 {
		return nil
	}
	// Pre-readiness historical batches (and unit-test fixtures) did not always
	// retain a market effective date/universe evidence. Keep those rows visible
	// as legacy-ready instead of retroactively treating an absent snapshot as a
	// failed provider. Newly published market batches always carry both values.
	var universeEvidenceCount int64
	if err := db.WithContext(ctx).Model(&UniverseSnapshot{}).Where("batch_id = ?", batch.BatchID).Count(&universeEvidenceCount).Error; err != nil {
		return err
	}
	if strings.TrimSpace(batch.EffectiveDate) == "" || universeEvidenceCount == 0 {
		for i := range items {
			items[i].ResearchReadiness = CandidateResearchReadiness{Status: CandidateResearchReadinessReady, Reasons: []string{}, InsiderEvidenceStatus: "legacy_not_evaluated"}
		}
		return nil
	}
	securityBatchID := strings.TrimSpace(batch.UniverseSourceVersion)
	if securityBatchID == "" {
		securityBatchID = batch.BatchID
	}
	securityIDs := make([]uint, 0, len(items))
	for _, item := range items {
		securityIDs = append(securityIDs, item.SecurityID)
	}
	var metrics []FinancialMetricSnapshot
	if err := db.WithContext(ctx).Where("batch_id = ? AND security_id IN ?", securityBatchID, securityIDs).Find(&metrics).Error; err != nil {
		return err
	}
	metricsBySecurity := make(map[uint]FinancialMetricSnapshot, len(metrics))
	for _, metric := range metrics {
		metricsBySecurity[metric.SecurityID] = metric
	}
	// Readiness only needs the newest financial reporting period. Loading every
	// historical fact (including source URLs and amounts) for every candidate
	// made the compact list wait on tens of thousands of rows from the local
	// research store.
	type latestFinancialPeriod struct {
		SecurityID uint
		PeriodEnd  string
	}
	var latestPeriods []latestFinancialPeriod
	if err := db.WithContext(ctx).Model(&FinancialFactSnapshot{}).
		Select("security_id, MAX(period_end) AS period_end").
		Where("security_id IN ? AND metric IN ? AND quality_status = ?", securityIDs, []string{FinancialMetricRevenue, FinancialMetricCash}, QualityStatusValid).
		Group("security_id").
		Scan(&latestPeriods).Error; err != nil {
		return err
	}
	latestPeriodBySecurity := map[uint]time.Time{}
	for _, period := range latestPeriods {
		latestPeriodBySecurity[period.SecurityID] = parseCandidateReadinessTime(period.PeriodEnd)
	}
	insiderAvailable, err := candidateInsiderDataAvailable(ctx, db, batch)
	if err != nil {
		return err
	}
	insiderCoverageExpected, err := candidateInsiderCoverageExpected(ctx, db, batch)
	if err != nil {
		return err
	}
	insiderCoverageBySecurity, err := candidateInsiderCoverageBySecurity(ctx, db, securityBatchID, candidateScoreSnapshots(items))
	if err != nil {
		return err
	}
	// Older test/legacy batches did not persist source versions. Treat this as
	// unknown rather than a provider failure; a declared source failure still
	// moves the candidate to research-only.
	insiderSourceDeclared := strings.TrimSpace(batch.SourceVersionsJSON) != ""
	if !insiderSourceDeclared && strings.TrimSpace(batch.UniverseSourceVersion) != "" {
		var securityBatch UniverseBatch
		if err := db.WithContext(ctx).First(&securityBatch, "batch_id = ?", batch.UniverseSourceVersion).Error; err == nil {
			insiderSourceDeclared = strings.TrimSpace(securityBatch.SourceVersionsJSON) != ""
		}
	}
	asOf := readinessAsOf(batch)
	for i := range items {
		metric, found := metricsBySecurity[items[i].SecurityID]
		coverage := insiderCoverageBySecurity[items[i].SecurityID]
		items[i].ResearchReadiness = buildCandidateResearchReadiness(items[i], metric, found, latestPeriodBySecurity[items[i].SecurityID], insiderAvailable, insiderSourceDeclared, insiderCoverageExpected, coverage, asOf)
	}
	return nil
}

func parseCandidateReadinessTime(value string) time.Time {
	value = strings.TrimSpace(value)
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05.999999999Z07:00", time.DateTime, time.DateOnly} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed
		}
	}
	return time.Time{}
}

func readinessAsOf(batch UniverseBatch) time.Time {
	if parsed, err := time.Parse(time.DateOnly, strings.TrimSpace(batch.EffectiveDate)); err == nil {
		return parsed
	}
	if !batch.StartedAt.IsZero() {
		return batch.StartedAt
	}
	return time.Now().UTC()
}

func candidateScoreSnapshots(items []CandidateScoreResult) []CandidateScoreSnapshot {
	result := make([]CandidateScoreSnapshot, 0, len(items))
	for _, item := range items {
		result = append(result, item.CandidateScoreSnapshot)
	}
	return result
}

func buildCandidateResearchReadiness(item CandidateScoreResult, metric FinancialMetricSnapshot, metricFound bool, latestFinancialPeriod time.Time, insiderAvailable, insiderSourceDeclared, insiderCoverageExpected bool, insiderCoverage candidateInsiderCoverage, asOf time.Time) CandidateResearchReadiness {
	result := CandidateResearchReadiness{Status: CandidateResearchReadinessReady, Reasons: []string{}, InsiderEvidenceStatus: "legacy_no_coverage"}
	blocked := false
	researchOnly := false
	add := func(reason string) {
		result.Reasons = append(result.Reasons, reason)
	}
	if item.MarketCapUSD <= 0 {
		blocked = true
		add("market_cap_unavailable")
	}
	if item.PriceCloseUSD <= 0 || item.PriceQualityStatus != QualityStatusValid {
		blocked = true
		add("market_price_unavailable")
	}
	switch item.PriceFreshnessStatus {
	case PriceFreshnessCurrent, PriceFreshnessPreviousTradingDay, PriceFreshnessFuture:
	case "":
		// A legacy response without price evidence is already handled above.
		blocked = true
		add("market_price_freshness_unknown")
	default:
		blocked = true
		add("market_price_not_current")
	}
	if !metricFound || (!metric.RevenueGrowthAvailable && !metric.RunwayAvailable) {
		researchOnly = true
		add("financial_metrics_unavailable")
	} else if !latestFinancialPeriod.IsZero() {
		age := int(asOf.Sub(latestFinancialPeriod).Hours() / 24)
		if age < 0 {
			age = 0
		}
		result.FinancialStalenessDays = age
		result.FinancialPeriodEnd = latestFinancialPeriod.Format(time.DateOnly)
		maximumAge := defaultAnnualFinancialStalenessDays
		if metric.RevenueGrowthAvailable {
			maximumAge = defaultQuarterlyFinancialStalenessDays
		}
		if age > maximumAge {
			researchOnly = true
			add("financial_period_stale")
		}
	}
	if insiderSourceDeclared && !insiderAvailable {
		researchOnly = true
		result.InsiderEvidenceStatus = "source_unavailable"
		add("insider_source_unavailable")
	} else if insiderCoverageExpected {
		result.InsiderEvidenceStatus = insiderCoverage.coverageStatus
		if result.InsiderEvidenceStatus == "" {
			result.InsiderEvidenceStatus = "coverage_missing"
			researchOnly = true
			add("insider_coverage_missing")
		} else if result.InsiderEvidenceStatus == InsiderCoveragePartial {
			researchOnly = true
			add("insider_coverage_partial")
		} else if result.InsiderEvidenceStatus == InsiderCoverageUnavailable {
			researchOnly = true
			add("insider_coverage_unavailable")
		} else if insiderCoverage.qualified {
			result.InsiderEvidenceStatus = "covered_qualified_purchase"
		} else if result.InsiderEvidenceStatus == InsiderCoverageCoveredTransactions {
			result.InsiderEvidenceStatus = "covered_no_qualified_purchase"
		}
	} else if insiderAvailable {
		result.InsiderEvidenceStatus = "legacy_no_coverage"
	}
	if item.BusinessModel.Model == CandidateBusinessModelUnknown && item.SectorCategory == "生物医药" {
		researchOnly = true
		add("biotech_business_model_unconfirmed")
	}
	if item.BusinessModel.RequiresReview && item.SectorCategory == "生物医药" {
		researchOnly = true
		add("biotech_business_model_review_due")
	}
	switch item.Investability.Status {
	case InvestabilityBlocked:
		blocked = true
		add("investability_blocked")
	case InvestabilityConstrained:
		researchOnly = true
		add("investability_constrained")
	}
	if item.DilutionTrend.Status == "high_dilution" {
		researchOnly = true
		add("share_dilution_high")
	}
	if blocked {
		result.Status = CandidateResearchReadinessBlocked
	} else if researchOnly {
		result.Status = CandidateResearchReadinessResearchOnly
	}
	return result
}
