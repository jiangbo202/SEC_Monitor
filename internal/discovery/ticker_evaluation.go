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

const (
	TickerEvaluationStatusReady   = "ready"
	TickerEvaluationStatusPartial = "partial"
)

// TickerEvaluationResult is the complete, auditable output returned for one
// explicit symbol evaluation. CandidateScore reuses the same fundamental and
// short-term-review calculations as the small-cap candidate list.
type TickerEvaluationResult struct {
	ID                uint                             `json:"id,omitempty"`
	Ticker            string                           `json:"ticker"`
	CIK               string                           `json:"cik,omitempty"`
	CompanyName       string                           `json:"company_name,omitempty"`
	TargetType        string                           `json:"target_type"`
	Status            string                           `json:"status"`
	DataSource        string                           `json:"data_source"`
	EvaluatedAt       time.Time                        `json:"evaluated_at"`
	Warnings          []string                         `json:"warnings"`
	CandidateScore    CandidateScoreResult             `json:"candidate_score"`
	FundamentalStatus string                           `json:"fundamental_status"`
	InsiderCoverage   *InsiderCoverage                 `json:"insider_coverage,omitempty"`
	Research          TickerEvaluationResearchSnapshot `json:"research"`
}

// TickerEvaluationResearchSnapshot is the non-scoring research context saved
// with an explicit assessment.  It keeps a historical row self-contained:
// opening an old result never calls Longbridge, SEC, or a market-data source.
// The provider observations deliberately remain separate from the candidate
// score so that a missing coverage result cannot silently affect a score.
type TickerEvaluationResearchSnapshot struct {
	Profile           CompanyProfile                   `json:"profile"`
	AnalystRating     AnalystRatingView                `json:"analyst_rating"`
	MarketResearch    CandidateMarketResearch          `json:"market_research"`
	OptionResearch    OptionResearchView               `json:"option_research"`
	ValuationResearch CandidateValuationResearch       `json:"valuation_research"`
	Sources           []TickerEvaluationResearchSource `json:"sources"`
	HoldingsScopeNote string                           `json:"holdings_scope_note,omitempty"`
	RefreshNotes      []string                         `json:"refresh_notes"`
}

// TickerEvaluationResearchSource makes coverage and freshness explicit in the
// expandable history panel rather than implying that every provider covers
// every stock, fund, or ETF.
type TickerEvaluationResearchSource struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	AsOf   string `json:"as_of,omitempty"`
	Note   string `json:"note,omitempty"`
}

type TickerEvaluationPage struct {
	Page     int                      `json:"page"`
	PageSize int                      `json:"page_size"`
	Total    int64                    `json:"total"`
	Items    []TickerEvaluationResult `json:"items"`
}

// TickerEvaluationFilter keeps history pagination, sorting, and the trade
// setup filter in the database so comparisons remain correct beyond one page.
// Snapshot fields that are not separately indexed are read from SQLite's
// persisted JSON payload with a fixed, allow-listed expression.
type TickerEvaluationFilter struct {
	Ticker       string
	EntryTrigger string
	SortBy       string
	SortOrder    string
	Page         int
	PageSize     int
}

const tickerEvaluationEntryTriggerExpression = "COALESCE(NULLIF(json_extract(result_json, '$.candidate_score.technical.trade_setup.entry_trigger'), ''), '等待触发条件')"

// BuildTickerEvaluationResult applies the existing candidate quality and
// review-priority policy to an ad-hoc symbol without publishing a batch.
func BuildTickerEvaluationResult(ticker, cik, companyName, targetType string, score CandidateScoreSnapshot, financial FinancialMetricSnapshot, risks []CapitalRiskSnapshot, prices []PriceSnapshot, evaluatedAt time.Time, fundamentalStatus string, warnings []string) TickerEvaluationResult {
	ticker = strings.ToUpper(strings.TrimSpace(ticker))
	if evaluatedAt.IsZero() {
		evaluatedAt = time.Now().UTC()
	}
	rows := append([]PriceSnapshot(nil), prices...)
	sort.Slice(rows, func(i, j int) bool { return rows[i].TradeDate.Before(rows[j].TradeDate) })
	_, _, basis := selectRevenueGrowth(financial)
	item := CandidateScoreResult{CandidateScoreSnapshot: score, ScoreEffectiveDate: evaluatedAt.Format(time.DateOnly), MarketQuality: buildCandidateMarketQuality(rows), Technical: buildCandidateTechnicalAnalysis(rows), RevenueGrowthInfo: RevenueGrowthExplanation{
		Method: "quarterly_revenue_yoy_pct preferred; annual_revenue_yoy_pct fallback", Source: "SEC companyfacts", RevenueGrowthAvailable: financial.RevenueGrowthAvailable,
		QuarterlyRevenueYoYPct: financial.QuarterlyRevenueYoYPct, QuarterlyRevenueQoQPct: financial.QuarterlyRevenueQoQPct, AnnualRevenueYoYPct: financial.AnnualRevenueYoYPct,
		AnnualRevenueQoQPct: financial.AnnualRevenueQoQPct, LatestQuarterRevenueUSD: financial.LatestQuarterRevenueUSD, PriorYearQuarterRevenueUSD: financial.PriorYearQuarterRevenueUSD,
		PreviousQuarterRevenueUSD: financial.PreviousQuarterRevenueUSD, LatestAnnualRevenueUSD: financial.LatestAnnualRevenueUSD, PriorAnnualRevenueUSD: financial.PriorAnnualRevenueUSD,
		SelectedRevenueGrowthPct: score.RevenueGrowthPct, SelectedRevenueGrowthBasis: basis, QualityFlagsJSON: financial.QualityFlagsJSON,
	}}
	for _, risk := range risks {
		if risk.Active {
			item.CapitalRiskSummaries = append(item.CapitalRiskSummaries, CapitalRiskSummary{Kind: risk.Kind, Severity: risk.Severity, BlocksA: risk.BlocksA, BlocksB: risk.BlocksB, Reason: risk.Reason, EffectiveAt: risk.EffectiveAt})
		}
	}
	if len(rows) > 0 {
		latest := rows[len(rows)-1]
		item.PriceCloseUSD = float64(latest.CloseMicros) / 1_000_000
		item.PriceVolume = latest.Volume
		tradeDate := latest.TradeDate
		item.PriceTradeDate = &tradeDate
		item.PriceSource = latest.Source
		item.PriceCurrency = latest.Currency
		item.PriceQualityStatus = latest.QualityStatus
		item.PriceFreshnessStatus = PriceFreshnessCurrent
	} else {
		item.PriceFreshnessStatus = PriceFreshnessMissing
	}
	items := []CandidateScoreResult{item}
	annotateCandidateQuality(items)
	item = items[0]
	status := TickerEvaluationStatusReady
	if fundamentalStatus != "available" || item.Technical.Status != TechnicalStatusReady {
		status = TickerEvaluationStatusPartial
	}
	return TickerEvaluationResult{Ticker: ticker, CIK: cik, CompanyName: companyName, TargetType: targetType, Status: status, DataSource: "ad_hoc_evaluation", EvaluatedAt: evaluatedAt, Warnings: append([]string{}, warnings...), CandidateScore: item, FundamentalStatus: fundamentalStatus}
}

func SaveTickerEvaluation(ctx context.Context, db *gorm.DB, result TickerEvaluationResult) (TickerEvaluationResult, error) {
	if db == nil {
		return result, errors.New("database is required")
	}
	payload, err := json.Marshal(result)
	if err != nil {
		return result, err
	}
	row := TickerEvaluationSnapshot{Ticker: result.Ticker, CIK: result.CIK, CompanyName: result.CompanyName, TargetType: result.TargetType, Status: result.Status, TotalScore: result.CandidateScore.TotalScore, ReviewScore: result.CandidateScore.ReviewPriorityScore, ResultJSON: string(payload), EvaluatedAt: result.EvaluatedAt}
	if err := db.WithContext(ctx).Create(&row).Error; err != nil {
		return result, err
	}
	// Identity enrichment is not a scoring change. Once a previously unnamed
	// ETF has been resolved, make its earlier immutable score snapshots readable
	// as well; ListTickerEvaluations still returns each snapshot's original
	// score/result JSON.
	if strings.TrimSpace(result.CompanyName) != "" {
		if err := db.WithContext(ctx).Model(&TickerEvaluationSnapshot{}).
			Where("ticker = ? AND id <> ? AND TRIM(company_name) = ''", result.Ticker, row.ID).
			Update("company_name", result.CompanyName).Error; err != nil {
			return result, err
		}
	}
	result.ID = row.ID
	return result, nil
}

func ListTickerEvaluations(ctx context.Context, db *gorm.DB, filter TickerEvaluationFilter) (TickerEvaluationPage, error) {
	result := TickerEvaluationPage{Page: filter.Page, PageSize: filter.PageSize, Items: []TickerEvaluationResult{}}
	if db == nil {
		return result, errors.New("database is required")
	}
	if result.Page < 1 {
		result.Page = 1
	}
	if result.PageSize < 1 || result.PageSize > 100 {
		result.PageSize = 20
	}
	query := db.WithContext(ctx).Model(&TickerEvaluationSnapshot{})
	if ticker := strings.ToUpper(strings.TrimSpace(filter.Ticker)); ticker != "" {
		query = query.Where("ticker = ?", ticker)
	}
	if entryTrigger := strings.TrimSpace(filter.EntryTrigger); entryTrigger != "" {
		query = query.Where(tickerEvaluationEntryTriggerExpression+" = ?", entryTrigger)
	}
	if err := query.Count(&result.Total).Error; err != nil {
		return result, err
	}
	var rows []TickerEvaluationSnapshot
	if err := query.Order(tickerEvaluationOrder(filter.SortBy, filter.SortOrder)).Offset((result.Page - 1) * result.PageSize).Limit(result.PageSize).Find(&rows).Error; err != nil {
		return result, err
	}
	for _, row := range rows {
		var item TickerEvaluationResult
		if err := json.Unmarshal([]byte(row.ResultJSON), &item); err != nil {
			return result, err
		}
		// The relational identity fields may be corrected after the original
		// evaluation (for example, a newly resolved ETF display name). Prefer the
		// current stored identity without altering the historical score snapshot.
		if strings.TrimSpace(item.CompanyName) == "" {
			item.CompanyName = row.CompanyName
		}
		if strings.TrimSpace(item.CIK) == "" {
			item.CIK = row.CIK
		}
		item.ID = row.ID
		result.Items = append(result.Items, item)
	}
	return result, nil
}

// ListTickerEvaluationEntryTriggers returns the distinct persisted trade-entry
// instructions available for the history filter. It does not derive labels at
// read time, so users always filter against the actual evaluation snapshot.
func ListTickerEvaluationEntryTriggers(ctx context.Context, db *gorm.DB, ticker string) ([]string, error) {
	if db == nil {
		return nil, errors.New("database is required")
	}
	query := db.WithContext(ctx).Model(&TickerEvaluationSnapshot{}).
		Select("DISTINCT " + tickerEvaluationEntryTriggerExpression + " AS entry_trigger")
	if ticker = strings.ToUpper(strings.TrimSpace(ticker)); ticker != "" {
		query = query.Where("ticker = ?", ticker)
	}
	type row struct {
		EntryTrigger string `gorm:"column:entry_trigger"`
	}
	var rows []row
	if err := query.Order("entry_trigger ASC").Scan(&rows).Error; err != nil {
		return nil, err
	}
	items := make([]string, 0, len(rows))
	for _, item := range rows {
		if value := strings.TrimSpace(item.EntryTrigger); value != "" {
			items = append(items, value)
		}
	}
	return items, nil
}

func tickerEvaluationOrder(sortBy, sortOrder string) string {
	// Every expression is static; never insert a client supplied identifier into
	// ORDER BY. The primary score columns remain materialized for fast sorting.
	column := "evaluated_at"
	switch strings.TrimSpace(sortBy) {
	case "fundamental":
		column = "total_score"
	case "review":
		column = "review_score"
	case "technical_status":
		column = "COALESCE(json_extract(result_json, '$.candidate_score.technical.status'), '')"
	case "distance_to_ma20":
		column = "CAST(json_extract(result_json, '$.candidate_score.technical.distance_to_ma20_pct') AS REAL)"
	case "distance_to_20d_high":
		column = "CAST(json_extract(result_json, '$.candidate_score.technical.distance_to_20d_high_pct') AS REAL)"
	case "evaluated_at":
		column = "evaluated_at"
	}
	direction := "DESC"
	if strings.EqualFold(strings.TrimSpace(sortOrder), "asc") {
		direction = "ASC"
	}
	return column + " " + direction + ", id " + direction
}

func TickerEvaluationPriceHistory(ctx context.Context, db *gorm.DB, ticker string) ([]PriceSnapshot, error) {
	if db == nil {
		return nil, errors.New("database is required")
	}
	return technicalPriceHistoryForSymbol(ctx, db, strings.ToUpper(strings.TrimSpace(ticker)), "", technicalDetailHistoryDays, nil)
}
