package discovery

import (
	"encoding/json"
	"math"
	"time"
)

func (row CandidateScoreSnapshot) MarshalJSON() ([]byte, error) {
	type alias CandidateScoreSnapshot
	out := alias(row)
	out.RevenueGrowthPct = finiteFloat(out.RevenueGrowthPct, 0)
	out.CashRunwayMonths = finiteCashRunwayMonths(out.CashRunwayMonths)
	return json.Marshal(out)
}

func (row CandidateScoreResult) MarshalJSON() ([]byte, error) {
	type scoreAlias CandidateScoreSnapshot
	score := scoreAlias(row.CandidateScoreSnapshot)
	score.RevenueGrowthPct = finiteFloat(score.RevenueGrowthPct, 0)
	score.CashRunwayMonths = finiteCashRunwayMonths(score.CashRunwayMonths)
	type alias struct {
		scoreAlias
		PriceCloseUSD        float64                  `json:"price_close_usd"`
		PriceVolume          int64                    `json:"price_volume"`
		PriceTradeDate       *time.Time               `json:"price_trade_date"`
		PriceCurrency        string                   `json:"price_currency"`
		PriceQualityStatus   string                   `json:"price_quality_status"`
		PriceSource          string                   `json:"price_source"`
		SectorCategory       string                   `json:"sector_category"`
		SectorLabel          string                   `json:"sector_label"`
		SectorSIC            int                      `json:"sector_sic"`
		SectorRatingScore    int                      `json:"sector_rating_score"`
		RevenueGrowthInfo    RevenueGrowthExplanation `json:"revenue_growth_explanation"`
		CapitalRiskSummaries []CapitalRiskSummary     `json:"capital_risk_summaries"`
	}
	return json.Marshal(alias{
		scoreAlias:           score,
		PriceCloseUSD:        finiteFloat(row.PriceCloseUSD, 0),
		PriceVolume:          row.PriceVolume,
		PriceTradeDate:       row.PriceTradeDate,
		PriceCurrency:        row.PriceCurrency,
		PriceQualityStatus:   row.PriceQualityStatus,
		PriceSource:          row.PriceSource,
		SectorCategory:       row.SectorCategory,
		SectorLabel:          row.SectorLabel,
		SectorSIC:            row.SectorSIC,
		SectorRatingScore:    row.SectorRatingScore,
		RevenueGrowthInfo:    row.RevenueGrowthInfo,
		CapitalRiskSummaries: row.CapitalRiskSummaries,
	})
}

func (row FinancialMetricSnapshot) MarshalJSON() ([]byte, error) {
	type alias FinancialMetricSnapshot
	out := alias(row)
	out.QuarterlyRevenueYoYPct = finiteFloat(out.QuarterlyRevenueYoYPct, 0)
	out.QuarterlyRevenueQoQPct = finiteFloat(out.QuarterlyRevenueQoQPct, 0)
	out.AnnualRevenueYoYPct = finiteFloat(out.AnnualRevenueYoYPct, 0)
	out.AnnualRevenueQoQPct = finiteFloat(out.AnnualRevenueQoQPct, 0)
	out.AvailableCashUSD = finiteFloat(out.AvailableCashUSD, 0)
	out.TTMOperatingCashFlowUSD = finiteFloat(out.TTMOperatingCashFlowUSD, 0)
	out.TTMCapitalExpenditureUSD = finiteFloat(out.TTMCapitalExpenditureUSD, 0)
	out.CFOBurnMonthlyUSD = finiteFloat(out.CFOBurnMonthlyUSD, 0)
	out.FCFBurnMonthlyUSD = finiteFloat(out.FCFBurnMonthlyUSD, 0)
	out.CashRunwayMonths = finiteCashRunwayMonths(out.CashRunwayMonths)
	return json.Marshal(out)
}

func finiteCashRunwayMonths(value float64) float64 {
	if math.IsInf(value, 1) {
		return MaxCashRunwayMonths
	}
	return finiteFloat(value, 0)
}

func finiteFloat(value float64, fallback float64) float64 {
	if math.IsInf(value, 0) || math.IsNaN(value) {
		return fallback
	}
	return value
}
