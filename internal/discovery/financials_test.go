package discovery

import (
	"archive/zip"
	"testing"
	"time"
)

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

func TestBuildFinancialSummaryComputesRevenueGrowthAndRunway(t *testing.T) {
	facts := []FinancialFact{
		financialDuration(FinancialMetricRevenue, "2025-01-01", "2025-03-31", 10_000_000),
		financialDuration(FinancialMetricRevenue, "2026-01-01", "2026-03-31", 15_000_000),
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
	assertFloatNear(t, summary.AnnualRevenueYoYPct, 37.5, 0.001)
	assertFloatNear(t, summary.CFOBurnMonthlyUSD, 1_500_000, 0.001)
	assertFloatNear(t, summary.FCFBurnMonthlyUSD, 2_000_000, 0.001)
	assertFloatNear(t, summary.CashRunwayMonths, 15, 0.001)
	if summary.AvailableCashUSD != 30_000_000 {
		t.Fatalf("available cash = %v, want 30000000", summary.AvailableCashUSD)
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
