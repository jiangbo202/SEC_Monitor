package discovery

import (
	"testing"
	"time"
)

func TestBuildCandidateValuationUsesOnlyCompleteEvidence(t *testing.T) {
	base := time.Date(2025, 4, 1, 0, 0, 0, 0, time.UTC)
	facts := []FinancialFactSnapshot{
		{Metric: FinancialMetricCash, AmountMicros: 30_000_000_000_000, PeriodEnd: base.AddDate(1, 0, -1)},
		{Metric: FinancialMetricShortTermInvestments, AmountMicros: 5_000_000_000_000, PeriodEnd: base.AddDate(1, 0, -1)},
		{Metric: FinancialMetricDebtCurrent, AmountMicros: 2_000_000_000_000, PeriodEnd: base.AddDate(1, 0, -1)},
		{Metric: FinancialMetricDebtNonCurrent, AmountMicros: 8_000_000_000_000, PeriodEnd: base.AddDate(1, 0, -1)},
	}
	for quarter := 0; quarter < 4; quarter++ {
		start := base.AddDate(0, quarter*3, 0)
		end := start.AddDate(0, 3, -1)
		facts = append(facts,
			FinancialFactSnapshot{Metric: FinancialMetricRevenue, AmountMicros: 10_000_000_000_000, PeriodStart: start, PeriodEnd: end},
			FinancialFactSnapshot{Metric: FinancialMetricGrossProfit, AmountMicros: 6_000_000_000_000, PeriodStart: start, PeriodEnd: end},
		)
	}
	priceDate := base.AddDate(1, 0, 0)
	item := CandidateScoreResult{CandidateScoreSnapshot: CandidateScoreSnapshot{MarketCapUSD: 100_000_000}, PriceCloseUSD: 10, PriceTradeDate: &priceDate}
	got := buildCandidateValuation(item, facts, ShareSnapshot{Instant: priceDate})
	if got.Status != "ready" || got.EnterpriseValueUSD == nil || *got.EnterpriseValueUSD != 75_000_000 || got.TTMRevenueUSD == nil || *got.TTMRevenueUSD != 40_000_000 || got.EVSales == nil || *got.EVSales != 1.875 || got.NetCashToMarketCap == nil || *got.NetCashToMarketCap != 0.25 {
		t.Fatalf("valuation = %#v", got)
	}

	withoutDebt := append([]FinancialFactSnapshot(nil), facts[:2]...)
	withoutDebt = append(withoutDebt, facts[4:]...)
	partial := buildCandidateValuation(item, withoutDebt, ShareSnapshot{})
	if partial.Status != "partial" || partial.EnterpriseValueUSD != nil || partial.EVSales != nil || partial.PriceToSales == nil {
		t.Fatalf("partial valuation = %#v", partial)
	}
}

func TestCandidateValuationFiltersAndSortKeepMissingEvidenceLast(t *testing.T) {
	value := func(number float64) *float64 { return &number }
	items := []CandidateScoreResult{
		{CandidateScoreSnapshot: CandidateScoreSnapshot{Ticker: "HIGH"}, Valuation: CandidateValuation{EVSales: value(8), NetCashToMarketCap: value(0.10)}},
		{CandidateScoreSnapshot: CandidateScoreSnapshot{Ticker: "LOW"}, Valuation: CandidateValuation{EVSales: value(2), NetCashToMarketCap: value(0.35)}},
		{CandidateScoreSnapshot: CandidateScoreSnapshot{Ticker: "NONE"}, Valuation: CandidateValuation{}},
	}
	maxEVSales, minNetCash := 3.0, 20.0
	filtered := filterCandidateScoreResults(append([]CandidateScoreResult(nil), items...), CandidateScoreQuery{MaxEVSales: &maxEVSales, MinNetCashToMarketCapPct: &minNetCash})
	if len(filtered) != 1 || filtered[0].Ticker != "LOW" {
		t.Fatalf("valuation filtered = %#v", filtered)
	}
	sortCandidateScoreResults(items, "ev_sales", "desc")
	if items[0].Ticker != "HIGH" || items[1].Ticker != "LOW" || items[2].Ticker != "NONE" {
		t.Fatalf("EV/Sales sort = %#v", items)
	}
	sortCandidateScoreResults(items, "net_cash_to_market_cap", "desc")
	if items[0].Ticker != "LOW" || items[1].Ticker != "HIGH" || items[2].Ticker != "NONE" {
		t.Fatalf("net cash sort = %#v", items)
	}
}
