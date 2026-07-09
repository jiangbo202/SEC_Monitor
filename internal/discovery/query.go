package discovery

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
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
	Page, PageSize int                `json:"page"`
	Total          int64              `json:"total"`
	Items          []UniverseSnapshot `json:"items"`
}

type CandidateScoreQuery struct {
	Page, PageSize       int
	Ticker, Grade        string
	SectorCategory       string
	SortBy, SortOrder    string
	EligibleA, EligibleB *bool
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
	PriceCloseUSD        float64                  `json:"price_close_usd"`
	PriceVolume          int64                    `json:"price_volume"`
	PriceTradeDate       *time.Time               `json:"price_trade_date"`
	PriceCurrency        string                   `json:"price_currency"`
	PriceQualityStatus   string                   `json:"price_quality_status"`
	PriceSource          string                   `json:"price_source"`
	QualityTier          string                   `json:"quality_tier"`
	QualityTags          []string                 `json:"quality_tags"`
	ReviewPriorityScore  int                      `json:"review_priority_score"`
	ChangeStatus         string                   `json:"change_status"`
	PreviousTotalScore   *int                     `json:"previous_total_score"`
	PreviousGrade        string                   `json:"previous_grade"`
	SectorCategory       string                   `json:"sector_category"`
	SectorLabel          string                   `json:"sector_label"`
	SectorSIC            int                      `json:"sector_sic"`
	SectorRatingScore    int                      `json:"sector_rating_score"`
	RevenueGrowthInfo    RevenueGrowthExplanation `json:"revenue_growth_explanation"`
	CapitalRiskSummaries []CapitalRiskSummary     `json:"capital_risk_summaries"`
}

type CapitalRiskSummary struct {
	Kind        string    `json:"kind"`
	Severity    string    `json:"severity"`
	BlocksA     bool      `json:"blocks_a"`
	BlocksB     bool      `json:"blocks_b"`
	Reason      string    `json:"reason"`
	EffectiveAt time.Time `json:"effective_at"`
}

type CandidateScorePage struct {
	Page, PageSize int                    `json:"page"`
	Total          int64                  `json:"total"`
	Items          []CandidateScoreResult `json:"items"`
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
	Page, PageSize int             `json:"page"`
	Total          int64           `json:"total"`
	Items          []UniverseBatch `json:"items"`
}
type ProviderRunQuery struct {
	Page, PageSize            int
	Provider, Status, BatchID string
}
type ProviderRunPage struct {
	Page, PageSize int           `json:"page"`
	Total          int64         `json:"total"`
	Items          []ProviderRun `json:"items"`
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
	if grade := strings.ToUpper(strings.TrimSpace(filter.Grade)); grade != "" {
		query = query.Where("grade = ?", grade)
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
	if category := strings.TrimSpace(filter.SectorCategory); category != "" {
		filtered := items[:0]
		for _, item := range items {
			if item.SectorCategory == category {
				filtered = append(filtered, item)
			}
		}
		items = filtered
	}
	items, err = hydrateCandidateRevenueGrowthEvidence(ctx, db, batch.UniverseSourceVersion, items)
	if err != nil {
		return result, err
	}
	items, err = hydrateCandidatePriceEvidence(ctx, db, batch.BatchID, items)
	if err != nil {
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
	items, err = annotateCandidateChanges(ctx, db, batch, items)
	if err != nil {
		return result, err
	}
	annotateCandidateQuality(items)
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
	return result, nil
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
	return result, nil
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
			Method:                   "selected = max(quarterly_revenue_yoy_pct, annual_revenue_yoy_pct)",
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
		basis := "missing"
		if metric.RevenueGrowthAvailable {
			if metric.QuarterlyRevenueYoYPct >= metric.AnnualRevenueYoYPct {
				basis = "quarterly_revenue_yoy_pct"
			} else {
				basis = "annual_revenue_yoy_pct"
			}
		}
		items[i].RevenueGrowthInfo = RevenueGrowthExplanation{
			Method:                     "selected = max(quarterly_revenue_yoy_pct, annual_revenue_yoy_pct)",
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
		less := candidateSortLess(items[i], items[j], field)
		if desc && field != "default" {
			return !candidateSortEqual(items[i], items[j], field) && !less
		}
		return less
	})
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
		return a.CashRunwayMonths < b.CashRunwayMonths
	case "review_priority_score":
		return a.ReviewPriorityScore < b.ReviewPriorityScore
	default:
		if a.ReviewPriorityScore != b.ReviewPriorityScore {
			return a.ReviewPriorityScore > b.ReviewPriorityScore
		}
		if a.Grade != b.Grade {
			return a.Grade < b.Grade
		}
		if a.TotalScore != b.TotalScore {
			return a.TotalScore > b.TotalScore
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
		return a.CashRunwayMonths == b.CashRunwayMonths
	case "review_priority_score":
		return a.ReviewPriorityScore == b.ReviewPriorityScore
	default:
		return false
	}
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
			continue
		}
		score := previousScore.TotalScore
		items[i].PreviousTotalScore = &score
		items[i].PreviousGrade = previousScore.Grade
		items[i].ChangeStatus = candidateChangeStatus(previousScore, items[i].CandidateScoreSnapshot)
	}
	return items, nil
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
		items[i].ReviewPriorityScore = candidateReviewPriorityScore(items[i])
	}
}

func candidateQualityTier(item CandidateScoreResult) string {
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
	return tags
}

func candidateReviewPriorityScore(item CandidateScoreResult) int {
	score := item.TotalScore * 10
	switch item.QualityTier {
	case "a":
		score += 120
	case "strong_b":
		score += 80
	case "standard_b":
		score += 30
	case "watch_b":
		score -= 30
	}
	if item.ChangeStatus == "new" {
		score += 35
	}
	if item.ChangeStatus == "improved" {
		score += 45
	}
	if item.RecentQualifiedInsider {
		score += 50
	}
	if item.PriceSource == "tiingo" {
		score += 20
	}
	if item.PriceVolume >= 500_000 {
		score += 25
	} else if item.PriceVolume >= 100_000 {
		score += 10
	}
	if item.MarketCapUSD > 0 && item.MarketCapUSD <= 500_000_000 {
		score += 20
	}
	if item.ActiveBlocksA || item.ActiveBlocksB {
		score -= 80
	}
	score -= 10 * countPenaltyQualityTags(item.QualityTags)
	return score
}

func countPenaltyQualityTags(tags []string) int {
	count := 0
	for _, tag := range tags {
		switch tag {
		case "low_revenue_base", "extreme_revenue_growth", "low_liquidity", "financials_missing", "active_capital_risk":
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

func hydrateCandidatePriceEvidence(ctx context.Context, db *gorm.DB, batchID string, items []CandidateScoreResult) ([]CandidateScoreResult, error) {
	if len(items) == 0 {
		return items, nil
	}
	securityIDs := make([]uint, 0, len(items))
	for _, item := range items {
		securityIDs = append(securityIDs, item.SecurityID)
	}
	var universeRows []UniverseSnapshot
	if err := db.WithContext(ctx).Where("batch_id = ? AND security_id IN ?", batchID, securityIDs).Find(&universeRows).Error; err != nil {
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
	if len(priceIDs) == 0 {
		return items, nil
	}
	var prices []PriceSnapshot
	if err := db.WithContext(ctx).Where("id IN ?", priceIDs).Find(&prices).Error; err != nil {
		return nil, err
	}
	priceByID := map[uint]PriceSnapshot{}
	for _, price := range prices {
		priceByID[price.ID] = price
	}
	for i := range items {
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
		items[i].PriceCurrency = price.Currency
		items[i].PriceQualityStatus = price.QualityStatus
		items[i].PriceSource = price.Source
	}
	return items, nil
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
