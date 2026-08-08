package discovery

import (
	"context"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"
)

// CandidateValuation is deliberately evidence-dated. Values are omitted (not
// zeroed) whenever the available SEC facts cannot support the calculation.
type CandidateValuation struct {
	Status             string   `json:"status"`
	Reasons            []string `json:"reasons"`
	MarketCapUSD       *float64 `json:"market_cap_usd"`
	CashUSD            *float64 `json:"cash_usd"`
	TotalDebtUSD       *float64 `json:"total_debt_usd"`
	EnterpriseValueUSD *float64 `json:"enterprise_value_usd"`
	TTMRevenueUSD      *float64 `json:"ttm_revenue_usd"`
	TTMGrossProfitUSD  *float64 `json:"ttm_gross_profit_usd"`
	EVSales            *float64 `json:"ev_sales"`
	EVGrossProfit      *float64 `json:"ev_gross_profit"`
	PriceToSales       *float64 `json:"price_to_sales"`
	NetCashToMarketCap *float64 `json:"net_cash_to_market_cap"`
	PriceTradeDate     string   `json:"price_trade_date"`
	FinancialPeriodEnd string   `json:"financial_period_end"`
	ShareInstant       string   `json:"share_instant"`
}

func hydrateCandidateValuations(ctx context.Context, db *gorm.DB, batch UniverseBatch, _ string, items []CandidateScoreResult) error {
	if len(items) == 0 {
		return nil
	}
	securityIDs := make([]uint, 0, len(items))
	for _, item := range items {
		securityIDs = append(securityIDs, item.SecurityID)
	}
	asOf := readinessAsOf(batch).Add(24*time.Hour - time.Nanosecond)
	var facts []FinancialFactSnapshot
	if err := db.WithContext(ctx).
		Where("security_id IN ? AND metric IN ? AND quality_status = ? AND (accepted_at = ? OR accepted_at <= ?)", securityIDs,
			[]string{FinancialMetricRevenue, FinancialMetricGrossProfit, FinancialMetricCash, FinancialMetricShortTermInvestments, FinancialMetricDebtCurrent, FinancialMetricDebtNonCurrent}, QualityStatusValid, time.Time{}, asOf).
		Find(&facts).Error; err != nil {
		return err
	}
	factsBySecurity := map[uint][]FinancialFactSnapshot{}
	for _, fact := range facts {
		factsBySecurity[fact.SecurityID] = append(factsBySecurity[fact.SecurityID], fact)
	}
	var universeRows []UniverseSnapshot
	if err := db.WithContext(ctx).Where("batch_id = ? AND security_id IN ?", batch.BatchID, securityIDs).Find(&universeRows).Error; err != nil {
		return err
	}
	shareIDs := make([]uint, 0, len(universeRows))
	shareIDBySecurity := map[uint]uint{}
	for _, row := range universeRows {
		if row.ShareSnapshotID != nil {
			shareIDBySecurity[row.SecurityID] = *row.ShareSnapshotID
			shareIDs = append(shareIDs, *row.ShareSnapshotID)
		}
	}
	var shares []ShareSnapshot
	if len(shareIDs) > 0 {
		if err := db.WithContext(ctx).Where("id IN ?", shareIDs).Find(&shares).Error; err != nil {
			return err
		}
	}
	shareByID := map[uint]ShareSnapshot{}
	for _, share := range shares {
		shareByID[share.ID] = share
	}
	for i := range items {
		items[i].Valuation = buildCandidateValuation(items[i], factsBySecurity[items[i].SecurityID], shareByID[shareIDBySecurity[items[i].SecurityID]])
	}
	return nil
}

func buildCandidateValuation(item CandidateScoreResult, facts []FinancialFactSnapshot, share ShareSnapshot) CandidateValuation {
	value := CandidateValuation{Status: "ready", Reasons: []string{}}
	add := func(reason string) { value.Reasons = append(value.Reasons, reason) }
	if item.MarketCapUSD <= 0 || item.PriceCloseUSD <= 0 {
		value.Status = "insufficient"
		add("market_cap_or_price_unavailable")
		return value
	}
	marketCap := float64(item.MarketCapUSD)
	value.MarketCapUSD = &marketCap
	if item.PriceTradeDate != nil {
		value.PriceTradeDate = item.PriceTradeDate.Format(time.DateOnly)
	}
	if !share.Instant.IsZero() {
		value.ShareInstant = share.Instant.Format(time.DateOnly)
	}

	cash, cashOK := latestInstantFinancialAmount(facts, FinancialMetricCash)
	investments, investmentsOK := latestInstantFinancialAmount(facts, FinancialMetricShortTermInvestments)
	if cashOK {
		cashUSD := float64(cash) / 1_000_000
		if investmentsOK {
			cashUSD += float64(investments) / 1_000_000
		}
		value.CashUSD = &cashUSD
	} else {
		add("cash_unavailable")
	}
	currentDebt, currentDebtOK := latestInstantFinancialAmount(facts, FinancialMetricDebtCurrent)
	nonCurrentDebt, nonCurrentDebtOK := latestInstantFinancialAmount(facts, FinancialMetricDebtNonCurrent)
	if currentDebtOK && nonCurrentDebtOK {
		debtUSD := float64(currentDebt+nonCurrentDebt) / 1_000_000
		value.TotalDebtUSD = &debtUSD
	} else {
		add("total_debt_unavailable")
	}
	if value.CashUSD != nil && value.TotalDebtUSD != nil {
		ev := marketCap + *value.TotalDebtUSD - *value.CashUSD
		value.EnterpriseValueUSD = &ev
		netCashRatio := (*value.CashUSD - *value.TotalDebtUSD) / marketCap
		value.NetCashToMarketCap = &netCashRatio
	}
	revenue, revenueEnd, revenueOK := trailingTwelveMonths(facts, FinancialMetricRevenue)
	if revenueOK && revenue > 0 {
		value.TTMRevenueUSD = &revenue
		value.FinancialPeriodEnd = revenueEnd.Format(time.DateOnly)
		ps := marketCap / revenue
		value.PriceToSales = &ps
		if value.EnterpriseValueUSD != nil {
			evSales := *value.EnterpriseValueUSD / revenue
			value.EVSales = &evSales
		}
	} else {
		add("ttm_revenue_unavailable")
	}
	grossProfit, _, grossProfitOK := trailingTwelveMonths(facts, FinancialMetricGrossProfit)
	if grossProfitOK && grossProfit > 0 {
		value.TTMGrossProfitUSD = &grossProfit
		if value.EnterpriseValueUSD != nil {
			evGrossProfit := *value.EnterpriseValueUSD / grossProfit
			value.EVGrossProfit = &evGrossProfit
		}
	} else {
		add("ttm_gross_profit_unavailable")
	}
	if len(value.Reasons) > 0 {
		value.Status = "partial"
	}
	return value
}

func latestInstantFinancialAmount(facts []FinancialFactSnapshot, metric string) (int64, bool) {
	var selected FinancialFactSnapshot
	found := false
	for _, fact := range facts {
		if fact.Metric != metric || fact.PeriodStart.IsZero() == false || fact.PeriodEnd.IsZero() {
			continue
		}
		if !found || fact.PeriodEnd.After(selected.PeriodEnd) || (fact.PeriodEnd.Equal(selected.PeriodEnd) && financialFactIsNewer(fact, selected)) {
			selected, found = fact, true
		}
	}
	return selected.AmountMicros, found
}

func trailingTwelveMonths(facts []FinancialFactSnapshot, metric string) (float64, time.Time, bool) {
	filtered := make([]FinancialFactSnapshot, 0, len(facts))
	for _, fact := range facts {
		if fact.Metric == metric {
			filtered = append(filtered, fact)
		}
	}
	quarters, _ := buildProfitHistory(filtered, time.Time{})
	if len(quarters) < 4 {
		return 0, time.Time{}, false
	}
	// The helper yields one observation per period end and derives Q4 only when
	// the FY and preceding three quarters are all present.
	sort.Slice(quarters, func(i, j int) bool { return quarters[i].PeriodEnd.Before(quarters[j].PeriodEnd) })
	last := quarters[len(quarters)-4:]
	for index := 1; index < len(last); index++ {
		if last[index].PeriodEnd.Sub(last[index-1].PeriodEnd) > 130*24*time.Hour {
			return 0, time.Time{}, false
		}
	}
	total := 0.0
	for _, point := range last {
		total += point.NetIncomeUSD
	}
	return total, last[len(last)-1].PeriodEnd, true
}

func candidateValuationReasonLabel(reason string) string {
	return strings.ReplaceAll(reason, "_", " ")
}
