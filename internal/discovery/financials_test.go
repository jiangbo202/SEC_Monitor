package discovery

import (
	"archive/zip"
	"math"
	"strings"
	"testing"
	"time"
)

func TestParseSECFinancialFactsJSONParsesOneIssuerAndRejectsMismatch(t *testing.T) {
	body := `{"cik":1234,"facts":{"us-gaap":{"Revenues":{"units":{"USD":[{"val":15000000,"start":"2026-01-01","end":"2026-03-31","filed":"2026-05-01","form":"10-Q","accn":"0000001234-26-000001"}]}}}}}`
	facts, err := ParseSECFinancialFactsJSON(strings.NewReader(body), "0000001234")
	if err != nil {
		t.Fatal(err)
	}
	if len(facts) != 1 || facts[0].Metric != FinancialMetricRevenue {
		t.Fatalf("facts = %#v", facts)
	}
	if _, err := ParseSECFinancialFactsJSON(strings.NewReader(body), "0000009999"); err == nil {
		t.Fatal("expected CIK mismatch error")
	}
}

func TestParseSECFinancialFactsZIPExtractsV1Concepts(t *testing.T) {
	body := `{"cik":1234,"facts":{"us-gaap":{
		"RevenueFromContractWithCustomerExcludingAssessedTax":{"units":{"USD":[{"val":15000000,"start":"2026-01-01","end":"2026-03-31","filed":"2026-05-01","form":"10-Q","accn":"0000001234-26-000001"}]}},
		"CashAndCashEquivalentsAtCarryingValue":{"units":{"USD":[{"val":24000000,"end":"2026-03-31","filed":"2026-05-01","form":"10-Q","accn":"0000001234-26-000001"}]}},
		"NetCashProvidedByUsedInOperatingActivities":{"units":{"USD":[{"val":-3000000,"start":"2026-01-01","end":"2026-03-31","filed":"2026-05-01","form":"10-Q","accn":"0000001234-26-000001"}]}},
		"PaymentsToAcquirePropertyPlantAndEquipment":{"units":{"USD":[{"val":-750000,"start":"2026-01-01","end":"2026-03-31","filed":"2026-05-01","form":"10-Q","accn":"0000001234-26-000001"}]}},
		"Assets":{"units":{"USD":[{"val":999,"end":"2026-03-31","filed":"2026-05-01","form":"10-Q","accn":"ignore"}]}}
	}}}`
	p := zipFile(t, map[string]string{"CIK0000001234.json": body, "CIK0000009999.json": `{"cik":9999}`})
	z, err := zip.OpenReader(p)
	if err != nil {
		t.Fatal(err)
	}
	defer z.Close()

	facts, err := ParseSECFinancialFactsZIP(&z.Reader, map[string]struct{}{"0000001234": {}}, ZIPParseLimits{MaxEntryBytes: 1 << 20, MaxTotalBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	if len(facts) != 4 {
		t.Fatalf("facts len = %d, want 4: %#v", len(facts), facts)
	}
	if facts[0].CIK != "0000001234" || facts[0].Metric == "" || facts[0].AmountMicros == 0 || facts[0].Accession == "" {
		t.Fatalf("fact missing normalized fields: %#v", facts[0])
	}
	assertFinancialFact(t, facts, FinancialMetricRevenue, "us-gaap:RevenueFromContractWithCustomerExcludingAssessedTax", 15_000_000)
	assertFinancialFact(t, facts, FinancialMetricCash, "us-gaap:CashAndCashEquivalentsAtCarryingValue", 24_000_000)
	assertFinancialFact(t, facts, FinancialMetricOperatingCashFlow, "us-gaap:NetCashProvidedByUsedInOperatingActivities", -3_000_000)
	assertFinancialFact(t, facts, FinancialMetricCapitalExpenditure, "us-gaap:PaymentsToAcquirePropertyPlantAndEquipment", 750_000)
}

func TestParseSECFinancialFactsZIPSkipsUnexpectedCurrencyUnits(t *testing.T) {
	body := `{"cik":1234,"facts":{"us-gaap":{
		"Revenues":{"units":{
			"EUR":[{"val":12000000,"start":"2026-01-01","end":"2026-03-31","filed":"2026-05-01","form":"10-Q","accn":"0000001234-26-000001"}],
			"USD":[{"val":15000000,"start":"2026-01-01","end":"2026-03-31","filed":"2026-05-01","form":"10-Q","accn":"0000001234-26-000002"}]
		}}
	}}}`
	p := zipFile(t, map[string]string{"CIK0000001234.json": body})
	z, err := zip.OpenReader(p)
	if err != nil {
		t.Fatal(err)
	}
	defer z.Close()

	facts, err := ParseSECFinancialFactsZIP(&z.Reader, map[string]struct{}{"0000001234": {}}, ZIPParseLimits{MaxEntryBytes: 1 << 20, MaxTotalBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	if len(facts) != 1 {
		t.Fatalf("facts len = %d, want 1: %#v", len(facts), facts)
	}
	assertFinancialFact(t, facts, FinancialMetricRevenue, "us-gaap:Revenues", 15_000_000)
}

func TestParseSECFinancialFactsZIPSkipsMalformedIndividualFacts(t *testing.T) {
	body := `{"cik":1234,"facts":{"us-gaap":{
		"NetCashProvidedByUsedInOperatingActivities":{"units":{"USD":[
			{"val":-1000000,"end":"2026-03-31","filed":"2026-05-01","form":"10-Q","accn":"0000001234-26-000001"},
			{"val":-3000000,"start":"2026-01-01","end":"2026-03-31","filed":"2026-05-01","form":"10-Q","accn":"0000001234-26-000002"}
		]}}
	}}}`
	p := zipFile(t, map[string]string{"CIK0000001234.json": body})
	z, err := zip.OpenReader(p)
	if err != nil {
		t.Fatal(err)
	}
	defer z.Close()

	facts, err := ParseSECFinancialFactsZIP(&z.Reader, map[string]struct{}{"0000001234": {}}, ZIPParseLimits{MaxEntryBytes: 1 << 20, MaxTotalBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	if len(facts) != 1 {
		t.Fatalf("facts len = %d, want 1: %#v", len(facts), facts)
	}
	assertFinancialFact(t, facts, FinancialMetricOperatingCashFlow, "us-gaap:NetCashProvidedByUsedInOperatingActivities", -3_000_000)
}

func TestParseSECFinancialFactsZIPSkipsEmptyCompanyFactsDocuments(t *testing.T) {
	p := zipFile(t, map[string]string{
		"CIK0000001234.json": `{}`,
		"CIK0000005678.json": `{"cik":5678,"facts":{"us-gaap":{"Revenues":{"units":{"USD":[{"val":15000000,"start":"2026-01-01","end":"2026-03-31","filed":"2026-05-01","form":"10-Q","accn":"0000005678-26-000001"}]}}}}}`,
	})
	z, err := zip.OpenReader(p)
	if err != nil {
		t.Fatal(err)
	}
	defer z.Close()

	facts, err := ParseSECFinancialFactsZIP(&z.Reader, map[string]struct{}{"0000001234": {}, "0000005678": {}}, ZIPParseLimits{MaxEntryBytes: 1 << 20, MaxTotalBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	if len(facts) != 1 {
		t.Fatalf("facts len = %d, want 1: %#v", len(facts), facts)
	}
	if facts[0].CIK != "0000005678" {
		t.Fatalf("fact cik = %q, want 0000005678", facts[0].CIK)
	}
}

func TestParseSECFinancialFactsZIPUsesFilenameCIKWhenDocumentCIKIsMissing(t *testing.T) {
	p := zipFile(t, map[string]string{
		"CIK0001790169.json": `{"entityName":"ZeroStack Corp.","facts":{"us-gaap":{"Revenues":{"units":{"USD":[{"val":15000000,"start":"2026-01-01","end":"2026-03-31","filed":"2026-05-01","form":"10-Q","accn":"0001790169-26-000001"}]}}}}}`,
	})
	z, err := zip.OpenReader(p)
	if err != nil {
		t.Fatal(err)
	}
	defer z.Close()

	facts, err := ParseSECFinancialFactsZIP(&z.Reader, map[string]struct{}{"0001790169": {}}, ZIPParseLimits{MaxEntryBytes: 1 << 20, MaxTotalBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	if len(facts) != 1 || facts[0].CIK != "0001790169" {
		t.Fatalf("facts = %#v, want one fact using filename CIK", facts)
	}
}

func TestBuildFinancialSummaryComputesRevenueGrowthAndRunway(t *testing.T) {
	facts := []FinancialFact{
		financialDuration(FinancialMetricRevenue, "2025-01-01", "2025-03-31", 10_000_000),
		financialDuration(FinancialMetricRevenue, "2025-10-01", "2025-12-31", 12_000_000),
		financialDuration(FinancialMetricRevenue, "2026-01-01", "2026-03-31", 15_000_000),
		financialDuration(FinancialMetricGrossProfit, "2026-01-01", "2026-03-31", 9_000_000),
		financialDuration(FinancialMetricRevenue, "2024-01-01", "2024-12-31", 40_000_000),
		financialDuration(FinancialMetricRevenue, "2025-01-01", "2025-12-31", 55_000_000),
		financialInstant(FinancialMetricCash, "2026-03-31", 24_000_000),
		financialInstant(FinancialMetricShortTermInvestments, "2026-03-31", 6_000_000),
		financialDuration(FinancialMetricOperatingCashFlow, "2025-04-01", "2025-06-30", -3_000_000),
		financialDuration(FinancialMetricOperatingCashFlow, "2025-07-01", "2025-09-30", -4_000_000),
		financialDuration(FinancialMetricOperatingCashFlow, "2025-10-01", "2025-12-31", -5_000_000),
		financialDuration(FinancialMetricOperatingCashFlow, "2026-01-01", "2026-03-31", -6_000_000),
		financialDuration(FinancialMetricCapitalExpenditure, "2025-04-01", "2025-06-30", 1_000_000),
		financialDuration(FinancialMetricCapitalExpenditure, "2025-07-01", "2025-09-30", 1_500_000),
		financialDuration(FinancialMetricCapitalExpenditure, "2025-10-01", "2025-12-31", 1_500_000),
		financialDuration(FinancialMetricCapitalExpenditure, "2026-01-01", "2026-03-31", 2_000_000),
	}

	summary := BuildFinancialSummary(facts, time.Date(2026, 6, 29, 0, 0, 0, 0, time.UTC))
	if !summary.RevenueGrowthAvailable || !summary.RunwayAvailable {
		t.Fatalf("summary missing required metrics: %#v", summary)
	}
	assertFloatNear(t, summary.QuarterlyRevenueYoYPct, 50, 0.001)
	assertFloatNear(t, summary.QuarterlyRevenueQoQPct, 25, 0.001)
	assertFloatNear(t, summary.AnnualRevenueYoYPct, 37.5, 0.001)
	assertFloatNear(t, summary.AnnualRevenueQoQPct, 37.5, 0.001)
	if summary.PreviousQuarterRevenueUSD != 12_000_000 {
		t.Fatalf("previous quarter revenue = %v, want 12000000", summary.PreviousQuarterRevenueUSD)
	}
	assertFloatNear(t, summary.CFOBurnMonthlyUSD, 1_500_000, 0.001)
	assertFloatNear(t, summary.FCFBurnMonthlyUSD, 2_000_000, 0.001)
	assertFloatNear(t, summary.CashRunwayMonths, 15, 0.001)
	if summary.AvailableCashUSD != 30_000_000 {
		t.Fatalf("available cash = %v, want 30000000", summary.AvailableCashUSD)
	}
	if !summary.GrossMarginAvailable {
		t.Fatalf("gross margin should be available: %#v", summary)
	}
	assertFloatNear(t, summary.GrossMarginPct, 60, 0.001)
}

func TestBuildFinancialSummaryUsesCostOfRevenueForGrossMargin(t *testing.T) {
	tests := []struct {
		name       string
		grossFacts []FinancialFact
		wantMargin float64
	}{
		{
			name: "uses cost of revenue when gross profit is unavailable",
			grossFacts: []FinancialFact{
				financialDuration(FinancialMetricCostOfRevenue, "2026-01-01", "2026-03-31", 3_500_000),
			},
			wantMargin: 65,
		},
		{
			name: "prefers gross profit when both values exist",
			grossFacts: []FinancialFact{
				financialDuration(FinancialMetricGrossProfit, "2026-01-01", "2026-03-31", 7_000_000),
				financialDuration(FinancialMetricCostOfRevenue, "2026-01-01", "2026-03-31", 3_500_000),
			},
			wantMargin: 70,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			facts := append([]FinancialFact{
				financialDuration(FinancialMetricRevenue, "2025-01-01", "2025-03-31", 8_000_000),
				financialDuration(FinancialMetricRevenue, "2026-01-01", "2026-03-31", 10_000_000),
			}, tt.grossFacts...)
			summary := BuildFinancialSummary(facts, time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC))
			if !summary.GrossMarginAvailable {
				t.Fatalf("gross margin should be available: %#v", summary)
			}
			assertFloatNear(t, summary.GrossMarginPct, tt.wantMargin, 0.001)
		})
	}
}

func TestBuildFinancialSummaryCapsRunwayWhenCompanyIsNotBurningCash(t *testing.T) {
	facts := []FinancialFact{
		financialInstant(FinancialMetricCash, "2026-03-31", 24_000_000),
		financialDuration(FinancialMetricOperatingCashFlow, "2025-04-01", "2025-06-30", 3_000_000),
		financialDuration(FinancialMetricOperatingCashFlow, "2025-07-01", "2025-09-30", 4_000_000),
		financialDuration(FinancialMetricOperatingCashFlow, "2025-10-01", "2025-12-31", 5_000_000),
		financialDuration(FinancialMetricOperatingCashFlow, "2026-01-01", "2026-03-31", 6_000_000),
	}

	summary := BuildFinancialSummary(facts, time.Date(2026, 6, 29, 0, 0, 0, 0, time.UTC))
	if !summary.RunwayAvailable {
		t.Fatalf("summary missing runway: %#v", summary)
	}
	if math.IsInf(summary.CashRunwayMonths, 0) || math.IsNaN(summary.CashRunwayMonths) {
		t.Fatalf("cash runway must be JSON-safe finite value: %#v", summary)
	}
	assertFloatNear(t, summary.CashRunwayMonths, MaxCashRunwayMonths, 0.001)
	if !containsString(summary.QualityFlags, "cash_flow_positive_runway_not_applicable") {
		t.Fatalf("positive cash flow should disclose runway semantics: %#v", summary.QualityFlags)
	}
}

func TestBuildFinancialSummaryFlagsFragileRevenueGrowth(t *testing.T) {
	facts := []FinancialFact{
		financialDuration(FinancialMetricRevenue, "2025-01-01", "2025-03-31", 500_000),
		financialDuration(FinancialMetricRevenue, "2025-10-01", "2025-12-31", 5_000_000),
		financialDuration(FinancialMetricRevenue, "2026-01-01", "2026-03-31", 4_000_000),
		financialDuration(FinancialMetricRevenue, "2024-01-01", "2024-12-31", 15_000_000),
		financialDuration(FinancialMetricRevenue, "2025-01-01", "2025-12-31", 14_000_000),
	}
	summary := BuildFinancialSummary(facts, time.Date(2026, 6, 29, 0, 0, 0, 0, time.UTC))
	for _, flag := range []string{"low_revenue_base", "low_prior_revenue_base", "extreme_revenue_growth", "quarterly_growth_not_confirmed_qoq", "quarterly_growth_conflicts_annual"} {
		if !containsString(summary.QualityFlags, flag) {
			t.Fatalf("quality flags=%#v missing %s", summary.QualityFlags, flag)
		}
	}
}

func assertFinancialFact(t *testing.T, facts []FinancialFact, metric, concept string, amountUSD int64) {
	t.Helper()
	for _, fact := range facts {
		if fact.Metric == metric && fact.Concept == concept {
			if got := fact.AmountMicros / 1_000_000; got != amountUSD {
				t.Fatalf("%s amount = %d, want %d", metric, got, amountUSD)
			}
			return
		}
	}
	t.Fatalf("missing %s/%s in %#v", metric, concept, facts)
}

func financialDuration(metric, start, end string, amountUSD int64) FinancialFact {
	startAt, _ := time.Parse(time.DateOnly, start)
	endAt, _ := time.Parse(time.DateOnly, end)
	return FinancialFact{CIK: "0000001234", Metric: metric, Unit: "USD", PeriodStart: startAt, PeriodEnd: endAt, AmountMicros: amountUSD * 1_000_000, Form: "10-Q", Accession: metric + end}
}

func financialInstant(metric, end string, amountUSD int64) FinancialFact {
	endAt, _ := time.Parse(time.DateOnly, end)
	return FinancialFact{CIK: "0000001234", Metric: metric, Unit: "USD", PeriodEnd: endAt, AmountMicros: amountUSD * 1_000_000, Form: "10-Q", Accession: metric + end}
}

func assertFloatNear(t *testing.T, got, want, tolerance float64) {
	t.Helper()
	delta := got - want
	if delta < 0 {
		delta = -delta
	}
	if delta > tolerance {
		t.Fatalf("got %v, want %v ± %v", got, want, tolerance)
	}
}
