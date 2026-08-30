package discovery

import (
	"context"
	"math"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"
)

const portfolioRiskMinimumObservations = 20

type datedReturn struct {
	date  string
	value float64
}

func buildCandidatePortfolioRiskAnalysis(ctx context.Context, db *gorm.DB, items []CandidateResearchPositionView, now time.Time) CandidatePortfolioRiskAnalysis {
	result := CandidatePortfolioRiskAnalysis{Benchmark: "IWM", FactorExposures: []CandidatePortfolioFactorExposure{}, PositionMetrics: []CandidatePortfolioPositionRisk{}, Correlations: []CandidatePortfolioCorrelation{}, Scenarios: []CandidatePortfolioStressScenario{}, SharedEventRisks: []CandidatePortfolioSharedEventRisk{}, Warnings: []string{}}
	benchmark := loadRiskReturns(ctx, db, "IWM", 100)
	weightedBeta, betaWeight := 0.0, 0.0
	weightedMomentum, momentumWeight := 0.0, 0.0
	weightedVolatility, volatilityWeight := 0.0, 0.0
	series := map[string][]datedReturn{}
	for _, item := range items {
		returns := loadRiskReturns(ctx, db, item.Ticker, 100)
		series[item.Ticker] = returns
		metric := CandidatePortfolioPositionRisk{Ticker: item.Ticker, WeightPct: item.MaxWeightPct, AverageDollarVolume: item.AverageDollarVolumeUSD, ObservationDays: len(returns), Status: "insufficient"}
		if len(returns) >= portfolioRiskMinimumObservations {
			vol := annualizedVolatility(returns)
			metric.AnnualVolatility, metric.Status = &vol, "available"
			weightedVolatility += vol * item.MaxWeightPct
			volatilityWeight += item.MaxWeightPct
		}
		if beta, observations, ok := alignedBeta(returns, benchmark); ok {
			metric.MarketBeta, metric.ObservationDays = &beta, observations
			weightedBeta += beta * item.MaxWeightPct
			betaWeight += item.MaxWeightPct
		}
		if momentum, ok := priceMomentum(ctx, db, item.Ticker, 20); ok {
			metric.Momentum20Day = &momentum
			weightedMomentum += momentum * item.MaxWeightPct
			momentumWeight += item.MaxWeightPct
		}
		result.PositionMetrics = append(result.PositionMetrics, metric)
	}
	if betaWeight > 0 {
		value := weightedBeta / betaWeight
		result.WeightedMarketBeta, result.BetaCoveredWeight = &value, betaWeight
	}
	if momentumWeight > 0 {
		result.FactorExposures = append(result.FactorExposures, CandidatePortfolioFactorExposure{Factor: "momentum_20d", Value: weightedMomentum / momentumWeight, Unit: "%", CoveragePct: momentumWeight, Meaning: "按研究权重加权的近 20 个交易日价格动量"})
	}
	if volatilityWeight > 0 {
		result.FactorExposures = append(result.FactorExposures, CandidatePortfolioFactorExposure{Factor: "annualized_volatility", Value: weightedVolatility / volatilityWeight, Unit: "%", CoveragePct: volatilityWeight, Meaning: "按研究权重加权的日收益年化波动率"})
	}
	result.FactorExposures = append(result.FactorExposures, CandidatePortfolioFactorExposure{Factor: "largest_sector", Value: largestSectorWeight(items), Unit: "%NAV", CoveragePct: totalPositionWeight(items), Meaning: "最大赛道研究权重，反映行业集中度"})
	for i := 0; i < len(items); i++ {
		for j := i + 1; j < len(items); j++ {
			corr, observations, ok := alignedCorrelation(series[items[i].Ticker], series[items[j].Ticker])
			row := CandidatePortfolioCorrelation{Left: items[i].Ticker, Right: items[j].Ticker, ObservationDays: observations, Status: "insufficient"}
			if ok {
				row.Correlation, row.Status = &corr, "available"
			}
			result.Correlations = append(result.Correlations, row)
		}
	}
	marketScenario := CandidatePortfolioStressScenario{Key: "market_down_10", Label: "市场下跌 10%", ShockPct: -10, CoveredWeightPct: betaWeight, Method: "各标的 IWM Beta × 研究权重", Status: "insufficient"}
	if betaWeight > 0 {
		loss := weightedBeta / 100 * -10
		marketScenario.EstimatedLossPct, marketScenario.Status = &loss, "available"
	}
	sectorWeight := largestSectorWeight(items)
	sectorLoss := sectorWeight / 100 * -12
	liquidityWeight := riskFlagWeight(items, "investability_constrained") + riskFlagWeight(items, "investability_blocked")
	liquidityLoss := liquidityWeight / 100 * -15
	eventWeight := riskFlagWeight(items, "catalyst_within_14_days") + riskFlagWeight(items, "manual_event_risk")
	eventLoss := eventWeight / 100 * -20
	result.Scenarios = append(result.Scenarios, marketScenario,
		CandidatePortfolioStressScenario{Key: "largest_sector_down_12", Label: "最大赛道下跌 12%", ShockPct: -12, EstimatedLossPct: &sectorLoss, CoveredWeightPct: sectorWeight, Method: "最大赛道研究权重 × 情景冲击", Status: availabilityForWeight(sectorWeight)},
		CandidatePortfolioStressScenario{Key: "liquidity_haircut_15", Label: "受限流动性折价 15%", ShockPct: -15, EstimatedLossPct: &liquidityLoss, CoveredWeightPct: liquidityWeight, Method: "受限/阻断标的研究权重 × 流动性折价", Status: availabilityForWeight(liquidityWeight)}, // gitleaks:allow -- public scenario identifier, not a credential
		CandidatePortfolioStressScenario{Key: "shared_event_down_20", Label: "临近或人工事件风险下跌 20%", ShockPct: -20, EstimatedLossPct: &eventLoss, CoveredWeightPct: eventWeight, Method: "14 日催化及人工事件标记权重 × 事件冲击", Status: availabilityForWeight(eventWeight)},
	)
	if eventWeight > 0 {
		result.SharedEventRisks = append(result.SharedEventRisks, CandidatePortfolioSharedEventRisk{Key: "near_term_events", Label: "未来 14 日催化或人工事件风险", Count: riskFlagCount(items, "catalyst_within_14_days") + riskFlagCount(items, "manual_event_risk"), WeightPct: eventWeight, ReviewBy: now.AddDate(0, 0, 1).Format(time.DateOnly)})
	}
	if len(benchmark) > 0 {
		result.ObservationDays = len(benchmark)
		result.AsOf = benchmark[len(benchmark)-1].date
	} else {
		result.Warnings = append(result.Warnings, "iwm_benchmark_history_missing")
	}
	if betaWeight < totalPositionWeight(items) {
		result.Warnings = append(result.Warnings, "partial_beta_coverage")
	}
	return result
}

func loadRiskReturns(ctx context.Context, db *gorm.DB, ticker string, limit int) []datedReturn {
	var rows []PriceSnapshot
	symbols := []string{strings.ToUpper(ticker), "." + strings.ToUpper(ticker) + ".US", strings.ToUpper(ticker) + ".US"}
	if err := db.WithContext(ctx).Where("symbol IN ? AND close_micros > 0 AND quality_status = ?", symbols, QualityStatusValid).Order("trade_date DESC, id DESC").Limit(limit + 1).Find(&rows).Error; err != nil {
		return nil
	}
	byDate := map[string]float64{}
	for _, row := range rows {
		date := row.TradeDate.UTC().Format(time.DateOnly)
		if _, exists := byDate[date]; !exists {
			byDate[date] = float64(row.CloseMicros) / 1_000_000
		}
	}
	dates := make([]string, 0, len(byDate))
	for date := range byDate {
		dates = append(dates, date)
	}
	sort.Strings(dates)
	if len(dates) < 2 {
		return nil
	}
	result := make([]datedReturn, 0, len(dates)-1)
	for i := 1; i < len(dates); i++ {
		if byDate[dates[i-1]] > 0 {
			result = append(result, datedReturn{date: dates[i], value: byDate[dates[i]]/byDate[dates[i-1]] - 1})
		}
	}
	return result
}

func alignedValues(left, right []datedReturn) ([]float64, []float64) {
	rightByDate := map[string]float64{}
	for _, row := range right {
		rightByDate[row.date] = row.value
	}
	a, b := []float64{}, []float64{}
	for _, row := range left {
		if value, ok := rightByDate[row.date]; ok {
			a, b = append(a, row.value), append(b, value)
		}
	}
	return a, b
}

func alignedBeta(asset, benchmark []datedReturn) (float64, int, bool) {
	a, b := alignedValues(asset, benchmark)
	if len(a) < portfolioRiskMinimumObservations {
		return 0, len(a), false
	}
	cov, variance := covariance(a, b), covariance(b, b)
	if variance <= 0 {
		return 0, len(a), false
	}
	return cov / variance, len(a), true
}

func alignedCorrelation(left, right []datedReturn) (float64, int, bool) {
	a, b := alignedValues(left, right)
	if len(a) < portfolioRiskMinimumObservations {
		return 0, len(a), false
	}
	denominator := math.Sqrt(covariance(a, a) * covariance(b, b))
	if denominator <= 0 {
		return 0, len(a), false
	}
	return covariance(a, b) / denominator, len(a), true
}

func covariance(a, b []float64) float64 {
	if len(a) == 0 || len(a) != len(b) {
		return 0
	}
	ma, mb := 0.0, 0.0
	for i := range a {
		ma += a[i]
		mb += b[i]
	}
	ma, mb = ma/float64(len(a)), mb/float64(len(b))
	value := 0.0
	for i := range a {
		value += (a[i] - ma) * (b[i] - mb)
	}
	return value / float64(len(a)-1)
}

func annualizedVolatility(values []datedReturn) float64 {
	series := make([]float64, len(values))
	for i := range values {
		series[i] = values[i].value
	}
	return math.Sqrt(math.Max(covariance(series, series), 0)*252) * 100
}

func priceMomentum(ctx context.Context, db *gorm.DB, ticker string, days int) (float64, bool) {
	var rows []PriceSnapshot
	symbols := []string{strings.ToUpper(ticker), "." + strings.ToUpper(ticker) + ".US", strings.ToUpper(ticker) + ".US"}
	if err := db.WithContext(ctx).Where("symbol IN ? AND close_micros > 0 AND quality_status = ?", symbols, QualityStatusValid).Order("trade_date DESC, id DESC").Limit(days + 1).Find(&rows).Error; err != nil || len(rows) < days+1 {
		return 0, false
	}
	return (float64(rows[0].CloseMicros)/float64(rows[len(rows)-1].CloseMicros) - 1) * 100, true
}

func totalPositionWeight(items []CandidateResearchPositionView) float64 {
	value := 0.0
	for _, item := range items {
		value += item.MaxWeightPct
	}
	return value
}
func largestSectorWeight(items []CandidateResearchPositionView) float64 {
	weights := map[string]float64{}
	max := 0.0
	for _, item := range items {
		weights[item.SectorCategory] += item.MaxWeightPct
		if weights[item.SectorCategory] > max {
			max = weights[item.SectorCategory]
		}
	}
	return max
}
func riskFlagWeight(items []CandidateResearchPositionView, flag string) float64 {
	value := 0.0
	for _, item := range items {
		for _, current := range item.RiskFlags {
			if current == flag {
				value += item.MaxWeightPct
				break
			}
		}
	}
	return value
}
func riskFlagCount(items []CandidateResearchPositionView, flag string) int {
	value := 0
	for _, item := range items {
		for _, current := range item.RiskFlags {
			if current == flag {
				value++
				break
			}
		}
	}
	return value
}
func availabilityForWeight(value float64) string {
	if value > 0 {
		return "available"
	}
	return "not_applicable"
}
func coverageStatus(covered, total float64) string {
	if total <= 0 || covered <= 0 {
		return "missing"
	}
	if covered+1e-9 < total {
		return "partial"
	}
	return "available"
}
