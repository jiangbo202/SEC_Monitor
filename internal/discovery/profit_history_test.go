package discovery

import (
	"context"
	"testing"
	"time"
)

func TestGetProfitHistoryBuildsQuarterlyAnnualAndDerivedFourthQuarter(t *testing.T) {
	db := openMigratedTestDatabase(t)
	security := Security{CIK: "0000012345", CompanyName: "Profit Co", CatalogStatus: SecurityCatalogPublished}
	if err := db.Create(&security).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&UniverseBatch{BatchID: "profit-batch", Kind: BatchKindSecurity, Status: BatchStatusPublished, EffectiveDate: "2025-12-31", StartedAt: time.Now()}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&SecurityBatchIdentity{BatchID: "profit-batch", SecurityID: security.ID, Ticker: "PROF", MappingStatus: MappingStatusCurrent, CreatedAt: time.Now()}).Error; err != nil {
		t.Fatal(err)
	}
	utc := time.UTC
	facts := []FinancialFactSnapshot{
		profitFact(security.ID, "2025-01-01", "2025-03-31", 10_000_000),
		profitFact(security.ID, "2025-04-01", "2025-06-30", 20_000_000),
		profitFact(security.ID, "2025-07-01", "2025-09-30", 30_000_000),
		profitFact(security.ID, "2025-01-01", "2025-12-31", 100_000_000),
	}
	for index := range facts {
		facts[index].CreatedAt = time.Date(2026, 1, 1, 0, 0, 0, 0, utc)
	}
	if err := db.Create(&facts).Error; err != nil {
		t.Fatal(err)
	}

	history, err := getProfitHistoryForSecurity(context.Background(), db, security.ID, "PROF", time.Date(2026, 1, 2, 0, 0, 0, 0, utc))
	if err != nil {
		t.Fatal(err)
	}
	if len(history.Annual) != 1 || history.Annual[0].NetIncomeUSD != 100 || history.Annual[0].Period != "FY 2025" {
		t.Fatalf("annual=%#v", history.Annual)
	}
	if len(history.Quarterly) != 4 {
		t.Fatalf("quarterly=%#v", history.Quarterly)
	}
	last := history.Quarterly[3]
	if last.Period != "2025 Q4" || !last.Derived || last.NetIncomeUSD != 40 {
		t.Fatalf("derived Q4=%#v", last)
	}
}

func profitFact(securityID uint, start, end string, amountMicros int64) FinancialFactSnapshot {
	periodStart, _ := time.Parse(time.DateOnly, start)
	periodEnd, _ := time.Parse(time.DateOnly, end)
	return FinancialFactSnapshot{
		SecurityID: securityID, Metric: FinancialMetricNetIncomeCommon, Concept: "us-gaap:NetIncomeLoss", PeriodStart: periodStart, PeriodEnd: periodEnd,
		Accession: start + end, Unit: "USD", AmountMicros: amountMicros, Form: "10-Q", SourceURL: "https://sec.example/profit", QualityStatus: QualityStatusValid,
	}
}
