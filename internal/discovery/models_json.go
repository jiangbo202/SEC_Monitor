package discovery

import (
	"encoding/json"
	"math"
)

func (row CandidateScoreSnapshot) MarshalJSON() ([]byte, error) {
	type alias CandidateScoreSnapshot
	out := alias(row)
	out.RevenueGrowthPct = finiteFloat(out.RevenueGrowthPct, 0)
	out.CashRunwayMonths = finiteCashRunwayMonths(out.CashRunwayMonths)
	return json.Marshal(out)
}

func (row FinancialMetricSnapshot) MarshalJSON() ([]byte, error) {
	type alias FinancialMetricSnapshot
	out := alias(row)
	out.QuarterlyRevenueYoYPct = finiteFloat(out.QuarterlyRevenueYoYPct, 0)
	out.AnnualRevenueYoYPct = finiteFloat(out.AnnualRevenueYoYPct, 0)
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
