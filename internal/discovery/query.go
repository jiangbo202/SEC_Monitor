package discovery

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"
)

const maxDiscoveryPageSize = 200

type UniverseQuery struct {
	Page, PageSize int
	Ticker, Status string
	ReasonCode     string
	QualityStatus  string
}

type UniversePage struct {
	Page     int                `json:"page"`
	PageSize int                `json:"page_size"`
	Total    int64              `json:"total"`
	Items    []UniverseSnapshot `json:"items"`
}

type CandidateScoreQuery struct {
	Page, PageSize           int
	Ticker, Grade            string
	SectorCategory           string
	QualityTier              string
	ChangeStatus             string
	TechnicalSignal          string
	ResearchReadiness        string
	ExcludeResearchReadiness []string
	PriceFreshnessStatuses   []string
	RecommendedOnly          bool
	SortBy, SortOrder        string
	MinReviewPriorityScore   int
	ExcludeQualityTags       []string
	EligibleA, EligibleB     *bool
	MaxEVSales               *float64
	MinNetCashToMarketCapPct *float64
	// SkipPerformance keeps the default service behavior complete for callers
	// that need it, while list endpoints can omit the presentation-only
	// historical performance calculation in their compact view.
	SkipPerformance         bool
	UpcomingEarningsTickers []string
	UpcomingEarningsOnly    bool
	FollowedOnly            bool
}

type RevenueGrowthExplanation struct {
	Method                     string  `json:"method"`
	Source                     string  `json:"source"`
	RevenueGrowthAvailable     bool    `json:"revenue_growth_available"`
	QuarterlyRevenueYoYPct     float64 `json:"quarterly_revenue_yoy_pct"`
	QuarterlyRevenueQoQPct     float64 `json:"quarterly_revenue_qoq_pct"`
	AnnualRevenueYoYPct        float64 `json:"annual_revenue_yoy_pct"`
	AnnualRevenueQoQPct        float64 `json:"annual_revenue_qoq_pct"`
	LatestQuarterRevenueUSD    int64   `json:"latest_quarter_revenue_usd"`
	PriorYearQuarterRevenueUSD int64   `json:"prior_year_quarter_revenue_usd"`
	PreviousQuarterRevenueUSD  int64   `json:"previous_quarter_revenue_usd"`
	LatestAnnualRevenueUSD     int64   `json:"latest_annual_revenue_usd"`
	PriorAnnualRevenueUSD      int64   `json:"prior_annual_revenue_usd"`
	SelectedRevenueGrowthPct   float64 `json:"selected_revenue_growth_pct"`
	SelectedRevenueGrowthBasis string  `json:"selected_revenue_growth_basis"`
	QualityFlagsJSON           string  `json:"quality_flags_json"`
}

type CandidateScoreResult struct {
	CandidateScoreSnapshot
	ScoreEffectiveDate    string                         `json:"score_effective_date"`
	PriceCloseUSD         float64                        `json:"price_close_usd"`
	PriceVolume           int64                          `json:"price_volume"`
	PriceTradeDate        *time.Time                     `json:"price_trade_date"`
	PriceFreshnessStatus  string                         `json:"price_freshness_status"`
	PriceAgeCalendarDays  int                            `json:"price_age_calendar_days"`
	PriceCurrency         string                         `json:"price_currency"`
	PriceQualityStatus    string                         `json:"price_quality_status"`
	PriceSource           string                         `json:"price_source"`
	QualityTier           string                         `json:"quality_tier"`
	QualityTags           []string                       `json:"quality_tags"`
	QualityAdjustedScore  int                            `json:"quality_adjusted_score"`
	ReviewPriorityScore   int                            `json:"review_priority_score"`
	ReviewPriorityReasons []ReviewPriorityReason         `json:"review_priority_reasons"`
	ChangeStatus          string                         `json:"change_status"`
	ChangeReasons         []CandidateChangeReason        `json:"change_reasons"`
	PreviousTotalScore    *int                           `json:"previous_total_score"`
	PreviousGrade         string                         `json:"previous_grade"`
	Performance           CandidatePerformance           `json:"performance"`
	SectorCategory        string                         `json:"sector_category"`
	SectorLabel           string                         `json:"sector_label"`
	SectorSIC             int                            `json:"sector_sic"`
	SectorRatingScore     int                            `json:"sector_rating_score"`
	RevenueGrowthInfo     RevenueGrowthExplanation       `json:"revenue_growth_explanation"`
	CapitalRiskSummaries  []CapitalRiskSummary           `json:"capital_risk_summaries"`
	MarketQuality         CandidateMarketQuality         `json:"market_quality"`
	Investability         CandidateInvestability         `json:"investability"`
	DilutionTrend         CandidateDilutionTrend         `json:"dilution_trend"`
	Technical             CandidateTechnicalAnalysis     `json:"technical"`
	ResearchReadiness     CandidateResearchReadiness     `json:"research_readiness"`
	BusinessModel         CandidateBusinessModelEvidence `json:"business_model"`
	Valuation             CandidateValuation             `json:"valuation"`
	Followed              bool                           `json:"followed"`
}

type ReviewPriorityReason struct {
	Label  string `json:"label"`
	Points int    `json:"points"`
	Kind   string `json:"kind"`
}

type CandidateChangeReason struct {
	Field    string `json:"field"`
	Label    string `json:"label"`
	Previous string `json:"previous"`
	Current  string `json:"current"`
	Kind     string `json:"kind"`
}

type CapitalRiskSummary struct {
	Kind        string    `json:"kind"`
	Severity    string    `json:"severity"`
	BlocksA     bool      `json:"blocks_a"`
	BlocksB     bool      `json:"blocks_b"`
	Reason      string    `json:"reason"`
	EffectiveAt time.Time `json:"effective_at"`
}

type CandidatePerformance struct {
	BaseDate  string   `json:"base_date"`
	BaseClose float64  `json:"base_close"`
	Date1D    string   `json:"date_1d"`
	Close1D   float64  `json:"close_1d"`
	Return1D  *float64 `json:"return_1d"`
	Date5D    string   `json:"date_5d"`
	Close5D   float64  `json:"close_5d"`
	Return5D  *float64 `json:"return_5d"`
	Date20D   string   `json:"date_20d"`
	Close20D  float64  `json:"close_20d"`
	Return20D *float64 `json:"return_20d"`
}

type CandidateScorePage struct {
	Page     int                    `json:"page"`
	PageSize int                    `json:"page_size"`
	Total    int64                  `json:"total"`
	Items    []CandidateScoreResult `json:"items"`
}

type CandidateOverview struct {
	BatchID           string                 `json:"batch_id"`
	Total             int64                  `json:"total"`
	GradeCounts       map[string]int         `json:"grade_counts"`
	QualityTierCounts map[string]int         `json:"quality_tier_counts"`
	ChangeCounts      map[string]int         `json:"change_counts"`
	SectorCounts      map[string]int         `json:"sector_counts"`
	QualityTagCounts  map[string]int         `json:"quality_tag_counts"`
	TopCandidates     []CandidateScoreResult `json:"top_candidates"`
}

type BatchQuery struct {
	Page, PageSize int
	Kind, Status   string
}
type BatchPage struct {
	Page     int             `json:"page"`
	PageSize int             `json:"page_size"`
	Total    int64           `json:"total"`
	Items    []UniverseBatch `json:"items"`
}
type ProviderRunQuery struct {
	Page, PageSize            int
	Provider, Status, BatchID string
}
type ProviderRunPage struct {
	Page     int           `json:"page"`
	PageSize int           `json:"page_size"`
	Total    int64         `json:"total"`
	Items    []ProviderRun `json:"items"`
}
type ProviderHealthPage struct {
	Items []ProviderHealth `json:"items"`
}

func normalizePage(page, size int) (int, int, error) {
	if page < 0 || size < 0 {
		return 0, 0, errors.New("page and page_size cannot be negative")
	}
	if page == 0 {
		page = 1
	}
	if size == 0 {
		size = 20
	}
	if size > maxDiscoveryPageSize {
		return 0, 0, errors.New("page_size exceeds 200")
	}
	return page, size, nil
}

func ListUniverse(ctx context.Context, db *gorm.DB, filter UniverseQuery) (UniversePage, error) {
	page, size, err := normalizePage(filter.Page, filter.PageSize)
	if err != nil {
		return UniversePage{}, err
	}
	result := UniversePage{Page: page, PageSize: size, Items: []UniverseSnapshot{}}
	if db == nil {
		return result, errors.New("database is required")
	}
	if ctx == nil {
		return result, errors.New("context is required")
	}
	var pointer CurrentBatchPointer
	err = db.WithContext(ctx).First(&pointer, "kind = ?", BatchKindPrescreen).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return result, nil
	}
	if err != nil {
		return result, err
	}
	var batch UniverseBatch
	if err = db.WithContext(ctx).First(&batch, "batch_id = ? AND kind = ? AND status = ?", pointer.BatchID, BatchKindPrescreen, BatchStatusPublished).Error; errors.Is(err, gorm.ErrRecordNotFound) {
		return result, nil
	} else if err != nil {
		return result, err
	}
	query := db.WithContext(ctx).Model(&UniverseSnapshot{}).Joins("JOIN securities ON securities.id = universe_snapshots.security_id").Where("universe_snapshots.batch_id = ?", batch.BatchID)
	if ticker := strings.ToUpper(strings.TrimSpace(filter.Ticker)); ticker != "" {
		query = query.Where("universe_snapshots.ticker = ?", ticker)
	}
	if status := strings.TrimSpace(filter.Status); status != "" {
		query = query.Where("universe_snapshots.status = ?", status)
	}
	if reason := strings.TrimSpace(filter.ReasonCode); reason != "" {
		query = query.Where("universe_snapshots.reason_code = ?", reason)
	}
	if quality := strings.TrimSpace(filter.QualityStatus); quality != "" {
		query = query.Where("universe_snapshots.quality_status = ?", quality)
	}
	if err = query.Count(&result.Total).Error; err != nil {
		return result, err
	}
	if err = query.Order("universe_snapshots.market_cap_usd DESC").Order("universe_snapshots.ticker ASC").Order("universe_snapshots.id ASC").Offset((page - 1) * size).Limit(size).Find(&result.Items).Error; err != nil {
		return result, err
	}
	return result, nil
}

func ListCandidateScores(ctx context.Context, db *gorm.DB, filter CandidateScoreQuery) (CandidateScorePage, error) {
	page, size, err := normalizePage(filter.Page, filter.PageSize)
	if err != nil {
		return CandidateScorePage{}, err
	}
	result := CandidateScorePage{Page: page, PageSize: size, Items: []CandidateScoreResult{}}
	if db == nil {
		return result, errors.New("database is required")
	}
	if ctx == nil {
		return result, errors.New("context is required")
	}
	var pointer CurrentBatchPointer
	err = db.WithContext(ctx).First(&pointer, "kind = ?", BatchKindPrescreen).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return result, nil
	}
	if err != nil {
		return result, err
	}
	var batch UniverseBatch
	if err = db.WithContext(ctx).First(&batch, "batch_id = ? AND kind = ? AND status = ?", pointer.BatchID, BatchKindPrescreen, BatchStatusPublished).Error; errors.Is(err, gorm.ErrRecordNotFound) {
		return result, nil
	} else if err != nil {
		return result, err
	}
	query := db.WithContext(ctx).Model(&CandidateScoreSnapshot{}).Where("batch_id = ?", batch.BatchID)
	if ticker := strings.ToUpper(strings.TrimSpace(filter.Ticker)); ticker != "" {
		query = query.Where("ticker = ?", ticker)
	}
	if filter.UpcomingEarningsOnly {
		tickers := normalizeTickerFilter(filter.UpcomingEarningsTickers)
		if len(tickers) == 0 {
			query = query.Where("1 = 0")
		} else {
			query = query.Where("ticker IN ?", tickers)
		}
	}
	if filter.FollowedOnly {
		query = query.Where("ticker IN (?)", db.WithContext(ctx).Model(&CandidateWatch{}).
			Select("ticker").Where("status = ?", CandidateWatchStatusActive))
	}
	if grade := normalizeCandidateGradeFilter(filter.Grade); grade != "" {
		query = query.Where("grade = ?", grade)
	} else {
		query = query.Where("grade IN ?", []string{CandidateGradeA, CandidateGradeB})
	}
	if filter.EligibleA != nil {
		query = query.Where("eligible_a = ?", *filter.EligibleA)
	}
	if filter.EligibleB != nil {
		query = query.Where("eligible_b = ?", *filter.EligibleB)
	}
	var scores []CandidateScoreSnapshot
	if err = query.Order("candidate_score_snapshots.grade ASC").Order("candidate_score_snapshots.total_score DESC").Order("candidate_score_snapshots.market_cap_usd ASC").Order("candidate_score_snapshots.ticker ASC").Find(&scores).Error; err != nil {
		return result, err
	}
	items, err := hydrateCandidateSectorEvidence(ctx, db, batch.UniverseSourceVersion, scores)
	if err != nil {
		return result, err
	}
	for i := range items {
		items[i].ScoreEffectiveDate = batch.EffectiveDate
	}
	if category := strings.TrimSpace(filter.SectorCategory); category != "" {
		filtered := items[:0]
		for _, item := range items {
			if item.SectorCategory == category {
				filtered = append(filtered, item)
			}
		}
		items = filtered
	}
	if err = hydrateCandidateBusinessModels(ctx, db, items); err != nil {
		return result, err
	}
	items, err = hydrateCandidateRevenueGrowthEvidence(ctx, db, batch.UniverseSourceVersion, items)
	if err != nil {
		return result, err
	}
	items, err = hydrateCandidatePriceEvidence(ctx, db, batch, items)
	if err != nil {
		return result, err
	}
	if err = hydrateCandidateValuations(ctx, db, batch, batch.UniverseSourceVersion, items); err != nil {
		return result, err
	}
	technicalPriceHistories, err := candidateTechnicalPriceHistories(ctx, db, items, technicalMA200LookbackDays)
	if err != nil {
		return result, err
	}
	hydrateCandidateMarketQualityFromPriceHistories(items, technicalPriceHistories)
	if err = hydrateCandidateTechnicalAnalysisWithPriceHistories(ctx, db, items, technicalPriceHistories); err != nil {
		return result, err
	}
	riskBatchID := strings.TrimSpace(batch.UniverseSourceVersion)
	if riskBatchID == "" {
		riskBatchID = batch.BatchID
	}
	items, err = hydrateCandidateCapitalRiskSummaries(ctx, db, riskBatchID, items)
	if err != nil {
		return result, err
	}
	if err = hydrateCandidateDilutionTrends(ctx, db, batch, items); err != nil {
		return result, err
	}
	for i := range items {
		items[i].Investability = buildCandidateInvestability(items[i])
	}
	if err = hydrateCandidateResearchReadiness(ctx, db, batch, items); err != nil {
		return result, err
	}
	items, err = annotateCandidateChanges(ctx, db, batch, items)
	if err != nil {
		return result, err
	}
	annotateCandidateQuality(items)
	if err = annotateCandidateFollowed(ctx, db, items); err != nil {
		return result, err
	}
	items = filterCandidateScoreResults(items, filter)
	sortCandidateScoreResults(items, filter.SortBy, filter.SortOrder)
	result.Total = int64(len(items))
	start := (page - 1) * size
	if start >= len(items) {
		result.Items = []CandidateScoreResult{}
		return result, nil
	}
	end := start + size
	if end > len(items) {
		end = len(items)
	}
	result.Items = items[start:end]
	// Performance is presentation-only and is only visible in the expanded
	// table. Avoid its per-row historical lookup for the default compact view.
	if !filter.SkipPerformance {
		if err = hydrateCandidatePerformance(ctx, db, result.Items); err != nil {
			return result, err
		}
	}
	return result, nil
}

func annotateCandidateFollowed(ctx context.Context, db *gorm.DB, items []CandidateScoreResult) error {
	if len(items) == 0 {
		return nil
	}
	tickers := make([]string, 0, len(items))
	for _, item := range items {
		tickers = append(tickers, item.Ticker)
	}
	var watches []CandidateWatch
	if err := db.WithContext(ctx).Select("ticker").Where("status = ? AND ticker IN ?", CandidateWatchStatusActive, tickers).Find(&watches).Error; err != nil {
		return err
	}
	followed := make(map[string]struct{}, len(watches))
	for _, watch := range watches {
		followed[watch.Ticker] = struct{}{}
	}
	for i := range items {
		_, items[i].Followed = followed[items[i].Ticker]
	}
	return nil
}

// CurrentCandidateTickers returns the compact current A/B universe used by
// auxiliary local enrichments such as the earnings calendar.
func CurrentCandidateTickers(ctx context.Context, db *gorm.DB) ([]string, error) {
	if db == nil || ctx == nil {
		return nil, errors.New("database and context are required")
	}
	var pointer CurrentBatchPointer
	if err := db.WithContext(ctx).First(&pointer, "kind = ?", BatchKindPrescreen).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return []string{}, nil
		}
		return nil, err
	}
	var tickers []string
	if err := db.WithContext(ctx).Model(&CandidateScoreSnapshot{}).Where("batch_id = ? AND grade IN ?", pointer.BatchID, []string{CandidateGradeA, CandidateGradeB}).Distinct("ticker").Order("ticker ASC").Pluck("ticker", &tickers).Error; err != nil {
		return nil, err
	}
	return tickers, nil
}

func normalizeTickerFilter(values []string) []string {
	seen := map[string]bool{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.ToUpper(strings.TrimSpace(value)); value != "" && !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	return result
}

func BuildCandidateOverview(ctx context.Context, db *gorm.DB) (CandidateOverview, error) {
	result := CandidateOverview{
		GradeCounts:       map[string]int{},
		QualityTierCounts: map[string]int{},
		ChangeCounts:      map[string]int{},
		SectorCounts:      map[string]int{},
		QualityTagCounts:  map[string]int{},
		TopCandidates:     []CandidateScoreResult{},
	}
	for pageNumber := 1; ; pageNumber++ {
		page, err := ListCandidateScores(ctx, db, CandidateScoreQuery{Page: pageNumber, PageSize: maxDiscoveryPageSize})
		if err != nil {
			return result, err
		}
		result.Total = page.Total
		if len(page.Items) > 0 && result.BatchID == "" {
			result.BatchID = page.Items[0].BatchID
		}
		for _, item := range page.Items {
			result.GradeCounts[item.Grade]++
			result.QualityTierCounts[item.QualityTier]++
			result.ChangeCounts[item.ChangeStatus]++
			if item.SectorCategory != "" {
				result.SectorCounts[item.SectorCategory]++
			}
			for _, tag := range item.QualityTags {
				result.QualityTagCounts[tag]++
			}
			if len(result.TopCandidates) < 10 {
				result.TopCandidates = append(result.TopCandidates, item)
			}
		}
		if int64(pageNumber*maxDiscoveryPageSize) >= page.Total || len(page.Items) == 0 {
			break
		}
	}
	exited, err := countExitedCandidates(ctx, db, result.BatchID)
	if err != nil {
		return result, err
	}
	if exited > 0 {
		result.ChangeCounts["exited"] = exited
	}
	return result, nil
}

func countExitedCandidates(ctx context.Context, db *gorm.DB, currentBatchID string) (int, error) {
	if currentBatchID == "" {
		return 0, nil
	}
	var current UniverseBatch
	if err := db.WithContext(ctx).First(&current, "batch_id = ? AND kind = ?", currentBatchID, BatchKindPrescreen).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, nil
		}
		return 0, err
	}
	var previous UniverseBatch
	err := db.WithContext(ctx).
		Where("kind = ? AND status = ? AND started_at < ?", BatchKindPrescreen, BatchStatusPublished, current.StartedAt).
		Order("started_at DESC").
		First(&previous).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	var currentRows []CandidateScoreSnapshot
	if err = db.WithContext(ctx).Where("batch_id = ?", currentBatchID).Find(&currentRows).Error; err != nil {
		return 0, err
	}
	currentTickers := map[string]struct{}{}
	for _, row := range currentRows {
		currentTickers[row.Ticker] = struct{}{}
	}
	var previousRows []CandidateScoreSnapshot
	if err = db.WithContext(ctx).Where("batch_id = ?", previous.BatchID).Find(&previousRows).Error; err != nil {
		return 0, err
	}
	exited := 0
	for _, row := range previousRows {
		if _, ok := currentTickers[row.Ticker]; !ok {
			exited++
		}
	}
	return exited, nil
}

func hydrateCandidateCapitalRiskSummaries(ctx context.Context, db *gorm.DB, batchID string, items []CandidateScoreResult) ([]CandidateScoreResult, error) {
	if len(items) == 0 {
		return items, nil
	}
	securityIDs := make([]uint, 0, len(items))
	for _, item := range items {
		securityIDs = append(securityIDs, item.SecurityID)
	}
	var rows []CapitalRiskSnapshot
	if err := db.WithContext(ctx).Where("batch_id = ? AND security_id IN ? AND active = ?", batchID, securityIDs, true).Order("severity DESC").Order("effective_at DESC").Find(&rows).Error; err != nil {
		return nil, err
	}
	bySecurity := map[uint][]CapitalRiskSummary{}
	for _, row := range rows {
		bySecurity[row.SecurityID] = append(bySecurity[row.SecurityID], CapitalRiskSummary{
			Kind: row.Kind, Severity: row.Severity, BlocksA: row.BlocksA, BlocksB: row.BlocksB, Reason: row.Reason, EffectiveAt: row.EffectiveAt,
		})
	}
	for i := range items {
		items[i].CapitalRiskSummaries = bySecurity[items[i].SecurityID]
		if items[i].CapitalRiskSummaries == nil {
			items[i].CapitalRiskSummaries = []CapitalRiskSummary{}
		}
	}
	return items, nil
}

func hydrateCandidateRevenueGrowthEvidence(ctx context.Context, db *gorm.DB, securityBatchID string, items []CandidateScoreResult) ([]CandidateScoreResult, error) {
	if len(items) == 0 {
		return items, nil
	}
	for i := range items {
		items[i].RevenueGrowthInfo = RevenueGrowthExplanation{
			Method:                   "quarterly_revenue_yoy_pct preferred; annual_revenue_yoy_pct fallback",
			Source:                   "SEC companyfacts / financial_metric_snapshots",
			SelectedRevenueGrowthPct: items[i].RevenueGrowthPct,
		}
	}
	if strings.TrimSpace(securityBatchID) == "" {
		return items, nil
	}
	securityIDs := make([]uint, 0, len(items))
	for _, item := range items {
		securityIDs = append(securityIDs, item.SecurityID)
	}
	var metrics []FinancialMetricSnapshot
	if err := db.WithContext(ctx).Where("batch_id = ? AND security_id IN ?", securityBatchID, securityIDs).Find(&metrics).Error; err != nil {
		return nil, err
	}
	metricBySecurity := map[uint]FinancialMetricSnapshot{}
	for _, metric := range metrics {
		metricBySecurity[metric.SecurityID] = metric
	}
	for i := range items {
		metric, ok := metricBySecurity[items[i].SecurityID]
		if !ok {
			continue
		}
		_, _, basis := selectRevenueGrowth(metric)
		items[i].RevenueGrowthInfo = RevenueGrowthExplanation{
			Method:                     "quarterly_revenue_yoy_pct preferred; annual_revenue_yoy_pct fallback",
			Source:                     "SEC companyfacts / financial_metric_snapshots",
			RevenueGrowthAvailable:     metric.RevenueGrowthAvailable,
			QuarterlyRevenueYoYPct:     metric.QuarterlyRevenueYoYPct,
			QuarterlyRevenueQoQPct:     metric.QuarterlyRevenueQoQPct,
			AnnualRevenueYoYPct:        metric.AnnualRevenueYoYPct,
			AnnualRevenueQoQPct:        metric.AnnualRevenueQoQPct,
			LatestQuarterRevenueUSD:    metric.LatestQuarterRevenueUSD,
			PriorYearQuarterRevenueUSD: metric.PriorYearQuarterRevenueUSD,
			PreviousQuarterRevenueUSD:  metric.PreviousQuarterRevenueUSD,
			LatestAnnualRevenueUSD:     metric.LatestAnnualRevenueUSD,
			PriorAnnualRevenueUSD:      metric.PriorAnnualRevenueUSD,
			SelectedRevenueGrowthPct:   items[i].RevenueGrowthPct,
			SelectedRevenueGrowthBasis: basis,
			QualityFlagsJSON:           metric.QualityFlagsJSON,
		}
	}
	return items, nil
}

func sortCandidateScoreResults(items []CandidateScoreResult, sortBy, sortOrder string) {
	field := strings.TrimSpace(sortBy)
	desc := strings.ToLower(strings.TrimSpace(sortOrder)) != "asc"
	if field == "" {
		field = "default"
	}
	sort.SliceStable(items, func(i, j int) bool {
		if field == "ev_sales" {
			return optionalFloatSortLess(items[i].Valuation.EVSales, items[j].Valuation.EVSales, desc)
		}
		if field == "net_cash_to_market_cap" {
			return optionalFloatSortLess(items[i].Valuation.NetCashToMarketCap, items[j].Valuation.NetCashToMarketCap, desc)
		}
		less := candidateSortLess(items[i], items[j], field)
		if desc && field != "default" {
			return !candidateSortEqual(items[i], items[j], field) && !less
		}
		return less
	})
}

// optionalFloatSortLess always leaves missing valuation evidence at the end,
// irrespective of direction. Missing evidence must not look cheaper or more
// cash-rich than a measurable company.
func optionalFloatSortLess(left, right *float64, desc bool) bool {
	if left == nil {
		return false
	}
	if right == nil {
		return true
	}
	if *left == *right {
		return false
	}
	if desc {
		return *left > *right
	}
	return *left < *right
}

func candidateSortLess(a, b CandidateScoreResult, field string) bool {
	switch field {
	case "ticker":
		return a.Ticker < b.Ticker
	case "total_score":
		return a.TotalScore < b.TotalScore
	case "market_cap_usd":
		return a.MarketCapUSD < b.MarketCapUSD
	case "price_close_usd":
		return a.PriceCloseUSD < b.PriceCloseUSD
	case "price_volume":
		return a.PriceVolume < b.PriceVolume
	case "price_trade_date":
		return timeValueBefore(a.PriceTradeDate, b.PriceTradeDate)
	case "revenue_growth_pct":
		return a.RevenueGrowthPct < b.RevenueGrowthPct
	case "quarterly_revenue_yoy_pct":
		return a.RevenueGrowthInfo.QuarterlyRevenueYoYPct < b.RevenueGrowthInfo.QuarterlyRevenueYoYPct
	case "annual_revenue_yoy_pct":
		return a.RevenueGrowthInfo.AnnualRevenueYoYPct < b.RevenueGrowthInfo.AnnualRevenueYoYPct
	case "quarterly_revenue_qoq_pct":
		return a.RevenueGrowthInfo.QuarterlyRevenueQoQPct < b.RevenueGrowthInfo.QuarterlyRevenueQoQPct
	case "annual_revenue_qoq_pct":
		return a.RevenueGrowthInfo.AnnualRevenueQoQPct < b.RevenueGrowthInfo.AnnualRevenueQoQPct
	case "cash_runway_months":
		return comparableCashRunwayMonths(a.CashRunwayMonths) < comparableCashRunwayMonths(b.CashRunwayMonths)
	case "review_priority_score":
		return a.ReviewPriorityScore < b.ReviewPriorityScore
	default:
		if a.TotalScore != b.TotalScore {
			return a.TotalScore > b.TotalScore
		}
		if a.Grade != b.Grade {
			return a.Grade < b.Grade
		}
		if a.MarketCapUSD != b.MarketCapUSD {
			return a.MarketCapUSD < b.MarketCapUSD
		}
		if a.Ticker != b.Ticker {
			return a.Ticker < b.Ticker
		}
		return a.ID < b.ID
	}
}

func candidateSortEqual(a, b CandidateScoreResult, field string) bool {
	switch field {
	case "ticker":
		return a.Ticker == b.Ticker
	case "total_score":
		return a.TotalScore == b.TotalScore
	case "market_cap_usd":
		return a.MarketCapUSD == b.MarketCapUSD
	case "price_close_usd":
		return a.PriceCloseUSD == b.PriceCloseUSD
	case "price_volume":
		return a.PriceVolume == b.PriceVolume
	case "price_trade_date":
		return equalTimePointers(a.PriceTradeDate, b.PriceTradeDate)
	case "revenue_growth_pct":
		return a.RevenueGrowthPct == b.RevenueGrowthPct
	case "quarterly_revenue_yoy_pct":
		return a.RevenueGrowthInfo.QuarterlyRevenueYoYPct == b.RevenueGrowthInfo.QuarterlyRevenueYoYPct
	case "annual_revenue_yoy_pct":
		return a.RevenueGrowthInfo.AnnualRevenueYoYPct == b.RevenueGrowthInfo.AnnualRevenueYoYPct
	case "quarterly_revenue_qoq_pct":
		return a.RevenueGrowthInfo.QuarterlyRevenueQoQPct == b.RevenueGrowthInfo.QuarterlyRevenueQoQPct
	case "annual_revenue_qoq_pct":
		return a.RevenueGrowthInfo.AnnualRevenueQoQPct == b.RevenueGrowthInfo.AnnualRevenueQoQPct
	case "cash_runway_months":
		return comparableCashRunwayMonths(a.CashRunwayMonths) == comparableCashRunwayMonths(b.CashRunwayMonths)
	case "review_priority_score":
		return a.ReviewPriorityScore == b.ReviewPriorityScore
	default:
		return false
	}
}

// A finite sentinel represents positive operating cash flow, not 999 months
// of literal runway. Cap it for ordering so it does not dominate companies
// whose cash duration is actually measured.
func comparableCashRunwayMonths(value float64) float64 {
	if value >= MaxCashRunwayMonths {
		return 60
	}
	return value
}

func annotateCandidateChanges(ctx context.Context, db *gorm.DB, current UniverseBatch, items []CandidateScoreResult) ([]CandidateScoreResult, error) {
	if len(items) == 0 {
		return items, nil
	}
	var previous UniverseBatch
	query := db.WithContext(ctx).
		Where("kind = ? AND status = ? AND batch_id <> ?", BatchKindPrescreen, BatchStatusPublished, current.BatchID)
	if !current.StartedAt.IsZero() {
		query = query.Where("started_at < ?", current.StartedAt)
	}
	err := query.Order("started_at DESC").Order("batch_id DESC").First(&previous).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		for i := range items {
			items[i].ChangeStatus = "new"
			items[i].ChangeReasons = candidateChangeReasons(nil, items[i].CandidateScoreSnapshot)
		}
		return items, nil
	}
	if err != nil {
		return nil, err
	}
	securityIDs := make([]uint, 0, len(items))
	for _, item := range items {
		securityIDs = append(securityIDs, item.SecurityID)
	}
	var previousScores []CandidateScoreSnapshot
	if err := db.WithContext(ctx).Where("batch_id = ? AND security_id IN ?", previous.BatchID, securityIDs).Find(&previousScores).Error; err != nil {
		return nil, err
	}
	bySecurity := map[uint]CandidateScoreSnapshot{}
	for _, score := range previousScores {
		bySecurity[score.SecurityID] = score
	}
	for i := range items {
		previousScore, ok := bySecurity[items[i].SecurityID]
		if !ok {
			items[i].ChangeStatus = "new"
			items[i].ChangeReasons = candidateChangeReasons(nil, items[i].CandidateScoreSnapshot)
			continue
		}
		score := previousScore.TotalScore
		items[i].PreviousTotalScore = &score
		items[i].PreviousGrade = previousScore.Grade
		items[i].ChangeStatus = candidateChangeStatus(previousScore, items[i].CandidateScoreSnapshot)
		items[i].ChangeReasons = candidateChangeReasons(&previousScore, items[i].CandidateScoreSnapshot)
	}
	return items, nil
}

func candidateChangeReasons(previous *CandidateScoreSnapshot, current CandidateScoreSnapshot) []CandidateChangeReason {
	if previous == nil {
		return []CandidateChangeReason{{Field: "candidate", Label: "首次入选", Current: current.Grade, Kind: "new"}}
	}
	reasons := []CandidateChangeReason{}
	add := func(field, label, previousValue, currentValue, kind string) {
		if previousValue != currentValue {
			reasons = append(reasons, CandidateChangeReason{Field: field, Label: label, Previous: previousValue, Current: currentValue, Kind: kind})
		}
	}
	add("grade", "等级变化", previous.Grade, current.Grade, "grade")
	add("total_score", "总分变化", formatCandidateNumber(float64(previous.TotalScore)), formatCandidateNumber(float64(current.TotalScore)), "score")
	add("revenue_growth_pct", "收入增长变化", formatCandidateNumber(previous.RevenueGrowthPct), formatCandidateNumber(current.RevenueGrowthPct), "growth")
	add("cash_runway_months", "现金 runway 变化", formatCandidateNumber(previous.CashRunwayMonths), formatCandidateNumber(current.CashRunwayMonths), "runway")
	add("risk_blocks", "融资/稀释风险变化", formatCandidateRiskBlocks(*previous), formatCandidateRiskBlocks(current), "risk")
	return reasons
}

func formatCandidateNumber(value float64) string {
	return strconv.FormatFloat(value, 'f', 1, 64)
}

func formatCandidateRiskBlocks(score CandidateScoreSnapshot) string {
	if score.ActiveBlocksB {
		return "阻断B"
	}
	if score.ActiveBlocksA {
		return "阻断A"
	}
	return "无阻断"
}

func candidateChangeStatus(previous, current CandidateScoreSnapshot) string {
	if previous.Grade != current.Grade {
		if candidateGradeRank(current.Grade) < candidateGradeRank(previous.Grade) {
			return "improved"
		}
		return "weakened"
	}
	delta := current.TotalScore - previous.TotalScore
	if delta >= 3 {
		return "improved"
	}
	if delta <= -3 {
		return "weakened"
	}
	if !previous.ActiveBlocksA && current.ActiveBlocksA || !previous.ActiveBlocksB && current.ActiveBlocksB {
		return "weakened"
	}
	return "unchanged"
}

func candidateGradeRank(grade string) int {
	switch strings.ToUpper(strings.TrimSpace(grade)) {
	case CandidateGradeA:
		return 1
	case CandidateGradeB:
		return 2
	case CandidateGradeExcluded:
		return 3
	default:
		return 4
	}
}

func annotateCandidateQuality(items []CandidateScoreResult) {
	for i := range items {
		items[i].QualityTags = candidateQualityTags(items[i])
		items[i].QualityTier = candidateQualityTier(items[i])
		items[i].QualityAdjustedScore = candidateQualityAdjustedScore(items[i])
		items[i].ReviewPriorityReasons = candidateReviewPriorityReasons(items[i])
		items[i].ReviewPriorityScore = candidateReviewPriorityScore(items[i])
	}
}

func filterCandidateScoreResults(items []CandidateScoreResult, filter CandidateScoreQuery) []CandidateScoreResult {
	qualityTier := strings.TrimSpace(filter.QualityTier)
	changeStatus := strings.TrimSpace(filter.ChangeStatus)
	technicalSignal := strings.TrimSpace(filter.TechnicalSignal)
	researchReadiness := strings.TrimSpace(filter.ResearchReadiness)
	excludedReadiness := normalizedStringSet(filter.ExcludeResearchReadiness)
	priceFreshness := normalizedStringSet(filter.PriceFreshnessStatuses)
	excludeTags := normalizedStringSet(filter.ExcludeQualityTags)
	if qualityTier == "" && changeStatus == "" && technicalSignal == "" && researchReadiness == "" && len(excludedReadiness) == 0 && len(priceFreshness) == 0 && !filter.RecommendedOnly && filter.MinReviewPriorityScore == 0 && len(excludeTags) == 0 && filter.MaxEVSales == nil && filter.MinNetCashToMarketCapPct == nil {
		return items
	}
	filtered := items[:0]
	for _, item := range items {
		if filter.RecommendedOnly && !candidatePrimaryRecommendation(item) {
			continue
		}
		if qualityTier != "" && item.QualityTier != qualityTier {
			continue
		}
		if changeStatus != "" && item.ChangeStatus != changeStatus {
			continue
		}
		if technicalSignal != "" && !candidateHasTechnicalSignal(item.Technical, technicalSignal) {
			continue
		}
		if researchReadiness != "" && item.ResearchReadiness.Status != researchReadiness {
			continue
		}
		if _, excluded := excludedReadiness[item.ResearchReadiness.Status]; excluded {
			continue
		}
		if len(priceFreshness) > 0 {
			if _, included := priceFreshness[item.PriceFreshnessStatus]; !included {
				continue
			}
		}
		if filter.MinReviewPriorityScore > 0 && item.ReviewPriorityScore < filter.MinReviewPriorityScore {
			continue
		}
		if hasExcludedQualityTag(item.QualityTags, excludeTags) {
			continue
		}
		if filter.MaxEVSales != nil && (item.Valuation.EVSales == nil || *item.Valuation.EVSales > *filter.MaxEVSales) {
			continue
		}
		if filter.MinNetCashToMarketCapPct != nil && (item.Valuation.NetCashToMarketCap == nil || *item.Valuation.NetCashToMarketCap*100 < *filter.MinNetCashToMarketCapPct) {
			continue
		}
		filtered = append(filtered, item)
	}
	return filtered
}

// candidatePrimaryRecommendation defines the default, conservative view for
// research. The full A/B universe remains available by turning this filter off.
func candidatePrimaryRecommendation(item CandidateScoreResult) bool {
	if item.ResearchReadiness.Status != CandidateResearchReadinessReady {
		return false
	}
	if item.Grade == CandidateGradeA {
		return !hasAnyQualityTag(item.QualityTags, "financials_missing", "active_capital_risk")
	}
	if item.QualityTier != "strong_b" && item.QualityTier != "standard_b" {
		return false
	}
	return !hasAnyQualityTag(item.QualityTags, "financials_missing", "low_revenue_base", "low_liquidity", "active_capital_risk")
}

func candidateQualityAdjustedScore(item CandidateScoreResult) int {
	score := item.TotalScore
	capValue := 100
	if hasAnyQualityTag(item.QualityTags, "financials_missing") {
		capValue = minInt(capValue, 55)
	}
	if hasAnyQualityTag(item.QualityTags, "low_revenue_base", "extreme_revenue_growth") {
		capValue = minInt(capValue, 60)
	}
	if hasAnyQualityTag(item.QualityTags, "low_liquidity") {
		capValue = minInt(capValue, 65)
	}
	if item.ActiveBlocksA || item.ActiveBlocksB || hasAnyQualityTag(item.QualityTags, "active_capital_risk") {
		capValue = minInt(capValue, 62)
	}
	if score > capValue {
		return capValue
	}
	return score
}

func candidateQualityTier(item CandidateScoreResult) string {
	if item.Grade == CandidateGradeExcluded {
		return CandidateGradeExcluded
	}
	if item.Grade == CandidateGradeA {
		return "a"
	}
	if item.ActiveBlocksA || item.ActiveBlocksB || hasAnyQualityTag(item.QualityTags, "low_revenue_base", "extreme_revenue_growth", "low_liquidity", "financials_missing") {
		return "watch_b"
	}
	if item.TotalScore >= 70 && item.CashRunwayMonths >= 12 && item.RevenueGrowthPct >= 40 {
		return "strong_b"
	}
	return "standard_b"
}

func normalizeCandidateGradeFilter(value string) string {
	value = strings.TrimSpace(value)
	if strings.EqualFold(value, CandidateGradeExcluded) {
		return CandidateGradeExcluded
	}
	return strings.ToUpper(value)
}

func candidateQualityTags(item CandidateScoreResult) []string {
	seen := map[string]struct{}{}
	tags := []string{}
	add := func(tag string) {
		tag = strings.TrimSpace(tag)
		if tag == "" {
			return
		}
		if _, ok := seen[tag]; ok {
			return
		}
		seen[tag] = struct{}{}
		tags = append(tags, tag)
	}
	for _, tag := range parseStringArrayJSON(item.RevenueGrowthInfo.QualityFlagsJSON) {
		add(tag)
	}
	if !item.RevenueGrowthInfo.RevenueGrowthAvailable && item.RevenueGrowthInfo.QualityFlagsJSON == "" {
		add("financials_missing")
	}
	if !item.RecentQualifiedInsider {
		add("no_insider_buy")
	}
	if item.ActiveBlocksA || item.ActiveBlocksB || len(item.CapitalRiskSummaries) > 0 {
		add("active_capital_risk")
	}
	if item.PriceVolume > 0 && item.PriceVolume < 100_000 {
		add("low_liquidity")
	}
	if item.PriceSource != "" && item.PriceSource != "tiingo" {
		add("secondary_price_source")
	}
	if item.PriceQualityStatus != "" && item.PriceQualityStatus != QualityStatusValid {
		add("price_quality_" + item.PriceQualityStatus)
	}
	if item.MarketQuality.Status == "risk" {
		if item.MarketQuality.AverageDollarVolume < 500_000 {
			add("low_dollar_volume")
		}
		if item.MarketQuality.VolatilityPct >= 10 {
			add("high_volatility")
		}
		if item.MarketQuality.MomentumPct <= -20 {
			add("negative_momentum")
		}
	}
	if item.DilutionTrend.Status == "high_dilution" {
		add("share_dilution_high")
	} else if item.DilutionTrend.Status == "elevated_dilution" {
		add("share_dilution_elevated")
	}
	for _, risk := range item.CapitalRiskSummaries {
		if risk.Kind == CapitalEventReverseSplit {
			add("reverse_split_risk")
		}
		if risk.Kind == CapitalEventGoingConcern {
			add("going_concern_risk")
		}
		if risk.Kind == CapitalEventWarrants {
			add("warrant_risk")
		}
	}
	if item.RevenueGrowthInfo.RevenueGrowthAvailable && item.RevenueGrowthInfo.QuarterlyRevenueYoYPct < 0 && item.RevenueGrowthInfo.AnnualRevenueYoYPct >= 20 {
		add("quarterly_growth_conflicts_with_annual")
	}
	return tags
}

func candidateReviewPriorityScore(item CandidateScoreResult) int {
	score := 0
	for _, reason := range candidateReviewPriorityReasons(item) {
		score += reason.Points
	}
	if score < 0 {
		return 0
	}
	if score > 100 {
		return 100
	}
	return score
}

func candidateReviewPriorityReasons(item CandidateScoreResult) []ReviewPriorityReason {
	reasons := []ReviewPriorityReason{}
	add := func(label string, points int) {
		if points == 0 {
			return
		}
		kind := "neutral"
		if points > 0 {
			kind = "positive"
		}
		if points < 0 {
			kind = "negative"
		}
		reasons = append(reasons, ReviewPriorityReason{Label: label, Points: points, Kind: kind})
	}
	add("质量调整分", item.QualityAdjustedScore*60/100)
	switch item.QualityTier {
	case "a":
		add("质量：A级", 20)
	case "strong_b":
		add("质量：强B", 12)
	case "standard_b":
		add("质量：普通B", 5)
	case "watch_b":
		add("质量：观察B", -10)
	}
	if item.ChangeStatus == "new" {
		add("变化：新增", 5)
	}
	if item.ChangeStatus == "improved" {
		add("变化：改善", 8)
	}
	if item.RecentQualifiedInsider {
		add("近期合格内幕买入", 8)
	}
	if item.PriceSource == "tiingo" {
		add("价格源：Tiingo", 2)
	}
	if item.PriceVolume >= 500_000 {
		add("成交量：50万以上", 3)
	} else if item.PriceVolume >= 100_000 {
		add("成交量：10万以上", 1)
	}
	if item.MarketCapUSD > 0 && item.MarketCapUSD <= 500_000_000 {
		add("市值：5亿美元以内", 2)
	}
	if item.ActiveBlocksA || item.ActiveBlocksB {
		add("存在阻断风险", -15)
	}
	if penalty := countPenaltyQualityTags(item.QualityTags); penalty > 0 {
		add("质量风险标签", -2*penalty)
	}
	if item.MarketQuality.Status == "risk" {
		add("市场质量风险", -8)
	}
	return reasons
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func countPenaltyQualityTags(tags []string) int {
	count := 0
	for _, tag := range tags {
		switch tag {
		case "low_revenue_base", "extreme_revenue_growth", "low_liquidity", "financials_missing", "active_capital_risk", "share_dilution_high":
			count++
		}
	}
	return count
}

func hasAnyQualityTag(tags []string, candidates ...string) bool {
	for _, tag := range tags {
		for _, candidate := range candidates {
			if tag == candidate {
				return true
			}
		}
	}
	return false
}

func hasExcludedQualityTag(tags []string, excludeTags map[string]struct{}) bool {
	if len(excludeTags) == 0 {
		return false
	}
	for _, tag := range tags {
		if _, ok := excludeTags[strings.TrimSpace(tag)]; ok {
			return true
		}
	}
	return false
}

func normalizedStringSet(values []string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out[value] = struct{}{}
		}
	}
	return out
}

func parseStringArrayJSON(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "null" {
		return []string{}
	}
	var values []string
	if err := json.Unmarshal([]byte(raw), &values); err != nil {
		return []string{}
	}
	return values
}

func timeValueBefore(a, b *time.Time) bool {
	if a == nil {
		return b != nil
	}
	if b == nil {
		return false
	}
	return a.Before(*b)
}

func equalTimePointers(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Equal(*b)
}

func hydrateCandidateSectorEvidence(ctx context.Context, db *gorm.DB, securityBatchID string, scores []CandidateScoreSnapshot) ([]CandidateScoreResult, error) {
	items := make([]CandidateScoreResult, len(scores))
	if len(scores) == 0 {
		return items, nil
	}
	securityIDs := make([]uint, 0, len(scores))
	for i, score := range scores {
		items[i] = CandidateScoreResult{CandidateScoreSnapshot: score}
		securityIDs = append(securityIDs, score.SecurityID)
	}
	if strings.TrimSpace(securityBatchID) == "" {
		for i := range items {
			rating := SectorRatingForSIC(0)
			items[i].SectorSIC = rating.SIC
			items[i].SectorCategory = rating.Category
			items[i].SectorRatingScore = rating.Score
			items[i].SectorLabel = sectorLabel(rating.Score)
		}
		return items, nil
	}
	var identities []SecurityBatchIdentity
	if err := db.WithContext(ctx).Where("batch_id = ? AND security_id IN ?", securityBatchID, securityIDs).Find(&identities).Error; err != nil {
		return nil, err
	}
	ratingBySecurity := map[uint]SectorRating{}
	for _, identity := range identities {
		ratingBySecurity[identity.SecurityID] = SectorRatingForSIC(identity.SIC)
	}
	for i := range items {
		rating, ok := ratingBySecurity[items[i].SecurityID]
		if !ok {
			rating = SectorRatingForSIC(0)
		}
		items[i].SectorSIC = rating.SIC
		items[i].SectorCategory = rating.Category
		items[i].SectorRatingScore = rating.Score
		items[i].SectorLabel = sectorLabel(rating.Score)
	}
	return items, nil
}

func hydrateCandidatePriceEvidence(ctx context.Context, db *gorm.DB, batch UniverseBatch, items []CandidateScoreResult) ([]CandidateScoreResult, error) {
	if len(items) == 0 {
		return items, nil
	}
	securityIDs := make([]uint, 0, len(items))
	for _, item := range items {
		securityIDs = append(securityIDs, item.SecurityID)
	}
	var universeRows []UniverseSnapshot
	if err := db.WithContext(ctx).Where("batch_id = ? AND security_id IN ?", batch.BatchID, securityIDs).Find(&universeRows).Error; err != nil {
		return nil, err
	}
	priceIDs := make([]uint, 0, len(universeRows))
	priceIDBySecurity := map[uint]uint{}
	for _, row := range universeRows {
		if row.PriceSnapshotID == nil {
			continue
		}
		priceIDBySecurity[row.SecurityID] = *row.PriceSnapshotID
		priceIDs = append(priceIDs, *row.PriceSnapshotID)
	}
	var prices []PriceSnapshot
	if len(priceIDs) > 0 {
		if err := db.WithContext(ctx).Where("id IN ?", priceIDs).Find(&prices).Error; err != nil {
			return nil, err
		}
	}
	priceByID := map[uint]PriceSnapshot{}
	for _, price := range prices {
		priceByID[price.ID] = price
	}
	for i := range items {
		items[i].PriceFreshnessStatus, items[i].PriceAgeCalendarDays = candidatePriceFreshness(batch.EffectiveDate, nil)
		priceID, ok := priceIDBySecurity[items[i].SecurityID]
		if !ok {
			continue
		}
		price, ok := priceByID[priceID]
		if !ok {
			continue
		}
		tradeDate := price.TradeDate
		items[i].PriceCloseUSD = float64(price.CloseMicros) / 1_000_000
		items[i].PriceVolume = price.Volume
		items[i].PriceTradeDate = &tradeDate
		items[i].PriceFreshnessStatus, items[i].PriceAgeCalendarDays = candidatePriceFreshness(batch.EffectiveDate, &tradeDate)
		items[i].PriceCurrency = price.Currency
		items[i].PriceQualityStatus = price.QualityStatus
		items[i].PriceSource = price.Source
	}
	// A published batch holds the price that was used to calculate its market
	// cap, but the research list and its technical summary should surface the
	// newest locally available daily quote. This also ensures that the list and
	// technical analysis use the same price source and as-of date after a
	// history backfill.
	tickers := make([]string, 0, len(items))
	preferredSourceByTicker := make(map[string]string, len(items))
	for _, item := range items {
		ticker := strings.ToUpper(strings.TrimSpace(item.Ticker))
		if ticker == "" {
			continue
		}
		tickers = append(tickers, ticker)
		if source := strings.TrimSpace(item.PriceSource); source != "" {
			preferredSourceByTicker[ticker] = source
		}
	}
	if len(tickers) == 0 {
		return items, nil
	}
	// Only a recent local quote can supersede the immutable price selected for
	// the published batch. Reading every historical snapshot for every ticker
	// made the list endpoint scan a multi-GB table just to find today's close.
	// If a symbol has no fresh local quote, its batch price remains the safe
	// fallback already populated above.
	latestStart := time.Now().UTC().AddDate(0, 0, -14)
	if effectiveDate, parseErr := time.Parse(time.DateOnly, batch.EffectiveDate); parseErr == nil {
		latestStart = effectiveDate.AddDate(0, 0, -14)
	}
	var localPrices []PriceSnapshot
	if err := db.WithContext(ctx).
		Where("symbol IN ? AND quality_status = ?", tickers, QualityStatusValid).
		Where("trade_date >= ?", latestStart).
		Order("trade_date DESC, created_at DESC, id DESC").
		Find(&localPrices).Error; err != nil {
		return nil, err
	}
	latestByTicker := map[string]PriceSnapshot{}
	for _, price := range localPrices {
		ticker := strings.ToUpper(strings.TrimSpace(price.Symbol))
		current, found := latestByTicker[ticker]
		if !found || candidatePriceIsNewer(price, current, preferredSourceByTicker[ticker]) {
			latestByTicker[ticker] = price
		}
	}
	for i := range items {
		price, found := latestByTicker[strings.ToUpper(strings.TrimSpace(items[i].Ticker))]
		if !found {
			continue
		}
		applyCandidatePriceEvidence(&items[i], batch, price)
	}
	return items, nil
}

func applyCandidatePriceEvidence(item *CandidateScoreResult, batch UniverseBatch, price PriceSnapshot) {
	if item == nil {
		return
	}
	tradeDate := price.TradeDate
	item.PriceCloseUSD = float64(price.CloseMicros) / 1_000_000
	item.PriceVolume = price.Volume
	item.PriceTradeDate = &tradeDate
	item.PriceFreshnessStatus, item.PriceAgeCalendarDays = candidatePriceFreshness(batch.EffectiveDate, &tradeDate)
	item.PriceCurrency = price.Currency
	item.PriceQualityStatus = price.QualityStatus
	item.PriceSource = price.Source
}

func candidatePriceIsNewer(candidate, current PriceSnapshot, preferredSource string) bool {
	if candidate.TradeDate.After(current.TradeDate) {
		return true
	}
	if candidate.TradeDate.Before(current.TradeDate) {
		return false
	}
	preferredSource = strings.TrimSpace(preferredSource)
	if candidate.Source == preferredSource && current.Source != preferredSource {
		return true
	}
	if candidate.Source != preferredSource && current.Source == preferredSource {
		return false
	}
	if candidate.CreatedAt.After(current.CreatedAt) {
		return true
	}
	return candidate.CreatedAt.Equal(current.CreatedAt) && candidate.ID > current.ID
}

const (
	PriceFreshnessCurrent            = "current"
	PriceFreshnessPreviousTradingDay = "previous_trading_day"
	PriceFreshnessStale              = "stale"
	PriceFreshnessFuture             = "future"
	PriceFreshnessMissing            = "missing"
	PriceFreshnessUnknown            = "unknown"
)

func candidatePriceFreshness(expectedDate string, tradeDate *time.Time) (string, int) {
	return candidatePriceFreshnessAt(expectedDate, tradeDate, time.Now())
}

// candidatePriceFreshnessAt compares a daily close with the most recent
// completed NYSE session. Before the regular US close, yesterday's close is
// the newest valid daily price even though the batch's civil effective date is
// today. This keeps the list from flagging a normal pre-close quote as stale.
func candidatePriceFreshnessAt(expectedDate string, tradeDate *time.Time, now time.Time) (string, int) {
	if strings.TrimSpace(expectedDate) == "" {
		return PriceFreshnessUnknown, 0
	}
	if tradeDate == nil || tradeDate.IsZero() {
		return PriceFreshnessMissing, 0
	}
	expected, err := time.Parse(time.DateOnly, strings.TrimSpace(expectedDate))
	if err != nil {
		return PriceFreshnessUnknown, 0
	}
	actual, err := time.Parse(time.DateOnly, tradeDate.Format(time.DateOnly))
	if err != nil {
		return PriceFreshnessUnknown, 0
	}
	if latestClosed, ok := latestCompletedNYSETradingDate(expected, now); ok && actual.Equal(latestClosed) {
		return PriceFreshnessCurrent, 0
	}
	age := int(expected.Sub(actual).Hours() / 24)
	switch {
	case age == 0:
		return PriceFreshnessCurrent, 0
	case age < 0:
		return PriceFreshnessFuture, age
	case age <= 4:
		// The market input contract permits one prior trading day. A calendar
		// gap of up to four days covers normal weekends and holidays.
		return PriceFreshnessPreviousTradingDay, age
	default:
		return PriceFreshnessStale, age
	}
}

func latestCompletedNYSETradingDate(expected, now time.Time) (time.Time, bool) {
	newYork, err := time.LoadLocation("America/New_York")
	if err != nil {
		return time.Time{}, false
	}
	localNow := now.In(newYork)
	nowDate := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 0, 0, 0, 0, time.UTC)
	reference := expected

	// A batch may be created on a weekend or a holiday. Its civil effective
	// date is still useful for reproducibility, but it must not make the last
	// completed close look one trading day old. Also cap a future batch date at
	// the current NY date so no future session can be treated as completed.
	if reference.After(nowDate) {
		reference = nowDate
	}
	if reference.Equal(nowDate) {
		marketClose := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 16, 0, 0, 0, newYork)
		if localNow.Before(marketClose) {
			reference = reference.AddDate(0, 0, -1)
		}
	}
	for offset := 0; offset <= 14; offset++ {
		candidate := reference.AddDate(0, 0, -offset)
		if knownNYSETradingDate(candidate) {
			return candidate, true
		}
	}
	return time.Time{}, false
}

func knownNYSETradingDate(day time.Time) bool {
	if day.Weekday() == time.Saturday || day.Weekday() == time.Sunday {
		return false
	}
	if holidays, ok := nyseCalendarManifest[DefaultNYSECalendarVersion][day.Year()]; ok {
		date := day.Format(time.DateOnly)
		for _, holiday := range holidays {
			if holiday == date {
				return false
			}
		}
	}
	return true
}

func hydrateCandidatePerformance(ctx context.Context, db *gorm.DB, items []CandidateScoreResult) error {
	for i := range items {
		if items[i].PriceTradeDate == nil || items[i].PriceCloseUSD <= 0 || strings.TrimSpace(items[i].Ticker) == "" {
			continue
		}
		baseDate, baseClose, ok, err := candidatePerformanceBaseline(ctx, db, items[i])
		if err != nil {
			return err
		}
		if !ok {
			baseDate, baseClose = *items[i].PriceTradeDate, items[i].PriceCloseUSD
		}
		performance, err := buildCandidatePerformance(ctx, db, items[i].Ticker, baseDate, baseClose)
		if err != nil {
			return err
		}
		items[i].Performance = performance
	}
	return nil
}

func candidatePerformanceBaseline(ctx context.Context, db *gorm.DB, item CandidateScoreResult) (time.Time, float64, bool, error) {
	var priceID uint
	err := db.WithContext(ctx).
		Table("candidate_score_snapshots AS score").
		Select("universe.price_snapshot_id").
		Joins("JOIN universe_batches AS batch ON batch.batch_id = score.batch_id").
		Joins("JOIN universe_snapshots AS universe ON universe.batch_id = score.batch_id AND universe.security_id = score.security_id").
		Where("score.security_id = ? AND score.grade IN ?", item.SecurityID, []string{CandidateGradeA, CandidateGradeB}).
		Where("batch.kind = ? AND batch.status = ? AND universe.price_snapshot_id IS NOT NULL", BatchKindPrescreen, BatchStatusPublished).
		Order("batch.started_at ASC, score.id ASC").
		Limit(1).
		Scan(&priceID).Error
	if err != nil {
		return time.Time{}, 0, false, err
	}
	if priceID == 0 {
		return time.Time{}, 0, false, nil
	}
	var price PriceSnapshot
	if err := db.WithContext(ctx).First(&price, "id = ? AND quality_status = ?", priceID, QualityStatusValid).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return time.Time{}, 0, false, nil
		}
		return time.Time{}, 0, false, err
	}
	baseClose := float64(price.CloseMicros) / 1_000_000
	if price.TradeDate.IsZero() || baseClose <= 0 {
		return time.Time{}, 0, false, nil
	}
	return price.TradeDate, baseClose, true, nil
}

func buildCandidatePerformance(ctx context.Context, db *gorm.DB, ticker string, baseDate time.Time, baseClose float64) (CandidatePerformance, error) {
	result := CandidatePerformance{BaseDate: baseDate.Format(time.DateOnly), BaseClose: baseClose}
	var rows []PriceSnapshot
	err := db.WithContext(ctx).
		Where("symbol = ? AND trade_date > ? AND quality_status = ?", strings.ToUpper(strings.TrimSpace(ticker)), baseDate, QualityStatusValid).
		Order("trade_date ASC").
		Limit(20).
		Find(&rows).Error
	if err != nil {
		return result, err
	}
	apply := func(index int, date *string, close *float64, ret **float64) {
		if len(rows) <= index || baseClose <= 0 {
			return
		}
		value := float64(rows[index].CloseMicros) / 1_000_000
		*date = rows[index].TradeDate.Format(time.DateOnly)
		*close = value
		returnPct := (value/baseClose - 1) * 100
		*ret = &returnPct
	}
	apply(0, &result.Date1D, &result.Close1D, &result.Return1D)
	apply(4, &result.Date5D, &result.Close5D, &result.Return5D)
	apply(19, &result.Date20D, &result.Close20D, &result.Return20D)
	return result, nil
}

func ListBatches(ctx context.Context, db *gorm.DB, filter BatchQuery) (BatchPage, error) {
	page, size, err := normalizePage(filter.Page, filter.PageSize)
	if err != nil {
		return BatchPage{}, err
	}
	result := BatchPage{Page: page, PageSize: size, Items: []UniverseBatch{}}
	if db == nil {
		return result, errors.New("database is required")
	}
	if ctx == nil {
		return result, errors.New("context is required")
	}
	query := db.WithContext(ctx).Model(&UniverseBatch{})
	if kind := strings.TrimSpace(filter.Kind); kind != "" {
		query = query.Where("kind = ?", kind)
	}
	if status := strings.TrimSpace(filter.Status); status != "" {
		query = query.Where("status = ?", status)
	}
	if err = query.Count(&result.Total).Error; err != nil {
		return result, err
	}
	if err = query.Order("started_at DESC").Order("batch_id DESC").Offset((page - 1) * size).Limit(size).Find(&result.Items).Error; err != nil {
		return result, err
	}
	if err = attachBatchSummaries(ctx, db, result.Items); err != nil {
		return result, err
	}
	return result, nil
}

func attachBatchSummaries(ctx context.Context, db *gorm.DB, items []UniverseBatch) error {
	if len(items) == 0 {
		return nil
	}
	batchIDs := make([]string, 0, len(items))
	for _, item := range items {
		if strings.TrimSpace(item.BatchID) != "" {
			batchIDs = append(batchIDs, item.BatchID)
		}
	}
	if len(batchIDs) == 0 {
		return nil
	}
	candidateCounts, err := candidateCountsByBatch(ctx, db, batchIDs)
	if err != nil {
		return err
	}
	providerSummaries, err := providerSummariesByBatch(ctx, db, batchIDs)
	if err != nil {
		return err
	}
	for i := range items {
		items[i].CandidateCount = candidateCounts[items[i].BatchID]
		if summary, ok := providerSummaries[items[i].BatchID]; ok {
			items[i].ProviderSummary = summary
		}
	}
	return nil
}

func candidateCountsByBatch(ctx context.Context, db *gorm.DB, batchIDs []string) (map[string]int64, error) {
	type row struct {
		BatchID string
		Count   int64
	}
	var rows []row
	if err := db.WithContext(ctx).Model(&CandidateScoreSnapshot{}).Select("batch_id, COUNT(*) AS count").Where("batch_id IN ?", batchIDs).Group("batch_id").Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make(map[string]int64, len(rows))
	for _, row := range rows {
		out[row.BatchID] = row.Count
	}
	return out, nil
}

func providerSummariesByBatch(ctx context.Context, db *gorm.DB, batchIDs []string) (map[string]*BatchProviderSummary, error) {
	var runs []ProviderRun
	if err := db.WithContext(ctx).Where("batch_id IN ?", batchIDs).Order("created_at DESC").Order("id DESC").Find(&runs).Error; err != nil {
		return nil, err
	}
	out := make(map[string]*BatchProviderSummary, len(runs))
	sourceVersions := make([]string, 0, len(runs))
	for _, run := range runs {
		if _, exists := out[run.BatchID]; exists {
			continue
		}
		summary := &BatchProviderSummary{
			Provider:          run.Provider,
			Status:            run.Status,
			ExpectedCount:     run.ExpectedCount,
			RecordCount:       run.RecordCount,
			CoveragePct:       run.CoveragePct,
			Timely:            run.Timely,
			SourceVersion:     run.SourceVersion,
			ErrorMessage:      run.ErrorMessage,
			PriceSourceCounts: map[string]int64{},
		}
		out[run.BatchID] = summary
		if strings.TrimSpace(run.SourceVersion) != "" {
			sourceVersions = append(sourceVersions, run.SourceVersion)
		}
	}
	if len(sourceVersions) == 0 {
		return out, nil
	}
	counts, err := priceSourceCountsByVersion(ctx, db, sourceVersions)
	if err != nil {
		return nil, err
	}
	for _, summary := range out {
		if bySource, ok := counts[summary.SourceVersion]; ok {
			summary.PriceSourceCounts = bySource
		}
	}
	return out, nil
}

func priceSourceCountsByVersion(ctx context.Context, db *gorm.DB, sourceVersions []string) (map[string]map[string]int64, error) {
	type row struct {
		SourceVersion string
		Source        string
		Count         int64
	}
	var rows []row
	if err := db.WithContext(ctx).Model(&PriceSnapshot{}).Select("source_version, source, COUNT(*) AS count").Where("source_version IN ?", sourceVersions).Group("source_version, source").Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make(map[string]map[string]int64)
	for _, row := range rows {
		if _, ok := out[row.SourceVersion]; !ok {
			out[row.SourceVersion] = map[string]int64{}
		}
		out[row.SourceVersion][row.Source] = row.Count
	}
	return out, nil
}

func ListProviderDiagnostics(ctx context.Context, db *gorm.DB, filter ProviderRunQuery) (ProviderRunPage, error) {
	page, size, err := normalizePage(filter.Page, filter.PageSize)
	if err != nil {
		return ProviderRunPage{}, err
	}
	result := ProviderRunPage{Page: page, PageSize: size, Items: []ProviderRun{}}
	if db == nil {
		return result, errors.New("database is required")
	}
	if ctx == nil {
		return result, errors.New("context is required")
	}
	query := db.WithContext(ctx).Model(&ProviderRun{})
	if provider := strings.TrimSpace(filter.Provider); provider != "" {
		query = query.Where("provider = ?", provider)
	}
	if status := strings.TrimSpace(filter.Status); status != "" {
		query = query.Where("status = ?", status)
	}
	if batchID := strings.TrimSpace(filter.BatchID); batchID != "" {
		query = query.Where("batch_id = ?", batchID)
	}
	if err = query.Count(&result.Total).Error; err != nil {
		return result, err
	}
	err = query.Order("created_at DESC").Order("id DESC").Offset((page - 1) * size).Limit(size).Find(&result.Items).Error
	return result, err
}

func ListProviderHealth(ctx context.Context, db *gorm.DB) (ProviderHealthPage, error) {
	result := ProviderHealthPage{Items: []ProviderHealth{}}
	if db == nil {
		return result, errors.New("database is required")
	}
	if ctx == nil {
		return result, errors.New("context is required")
	}
	err := db.WithContext(ctx).Order("provider ASC").Find(&result.Items).Error
	return result, err
}
