package discovery

import (
	"context"
	"testing"
	"time"
)

func TestGetCandidateDetailReturnsCurrentEvidence(t *testing.T) {
	db := openMigratedTestDatabase(t)
	security := Security{CIK: "0000012345", CompanyName: "", SIC: 0, CatalogStatus: SecurityCatalogPublished}
	if err := db.Create(&security).Error; err != nil {
		t.Fatal(err)
	}
	securityBatch := UniverseBatch{BatchID: "security-current", Kind: BatchKindSecurity, Status: BatchStatusPublished, StartedAt: time.Now()}
	if err := db.Create(&securityBatch).Error; err != nil {
		t.Fatal(err)
	}
	batch := UniverseBatch{BatchID: "current", Kind: BatchKindPrescreen, Status: BatchStatusPublished, StartedAt: time.Now()}
	batch.UniverseSourceVersion = securityBatch.BatchID
	if err := db.Create(&batch).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&SecurityBatchIdentity{BatchID: securityBatch.BatchID, SecurityID: security.ID, CIK: security.CIK, Ticker: "DTCO", CompanyName: "Detail Co", SIC: 2834, StateOfIncorporation: "DE", LatestAnnualForm: "10-K", MappingStatus: MappingStatusCurrent, CreatedAt: time.Now()}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&CurrentBatchPointer{Kind: BatchKindPrescreen, BatchID: batch.BatchID}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&UniverseSnapshot{BatchID: batch.BatchID, SecurityID: security.ID, Ticker: "DTCO", MarketCapUSD: 220_000_000, Status: EffectiveStatusPrescreen, QualityStatus: QualityStatusValid, ReasonCode: ReasonQualifiedSmallCap}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&CandidateScoreSnapshot{BatchID: batch.BatchID, SecurityID: security.ID, Ticker: "DTCO", Grade: CandidateGradeA, EligibleA: true, TotalScore: 88, RevenueGrowthPct: 54, CashRunwayMonths: 18, RecentQualifiedInsider: true}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&FinancialMetricSnapshot{BatchID: securityBatch.BatchID, SecurityID: security.ID, RevenueGrowthAvailable: true, RunwayAvailable: true, QuarterlyRevenueYoYPct: 54, CashRunwayMonths: 18, QualityFlagsJSON: `["low_revenue_base"]`}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&InsiderTransactionSnapshot{SecurityID: security.ID, Accession: "0001", OwnerName: "CEO", OfficerTitle: "Chief Executive Officer", Role: InsiderRoleCEO, TransactionDate: time.Now().AddDate(0, 0, -5), TransactionCode: "P", AcquiredDisposedCode: "A", Qualified: true, ValueMicros: 1_000_000_000}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&CapitalRiskSnapshot{BatchID: securityBatch.BatchID, SecurityID: security.ID, Kind: CapitalEventATMProgram, Active: true, BlocksA: true, BlocksB: false, Severity: CapitalRiskSeverityHigh, Reason: "ATM program active"}).Error; err != nil {
		t.Fatal(err)
	}

	detail, err := GetCandidateDetail(context.Background(), db, "dtco")
	if err != nil {
		t.Fatal(err)
	}
	if detail.Score.Ticker != "DTCO" || detail.Security.CompanyName != "Detail Co" {
		t.Fatalf("detail identity = %#v", detail)
	}
	if detail.Security.SIC != 2834 || detail.Sector.Category != "生物医药" {
		t.Fatalf("detail sector = %#v security=%#v", detail.Sector, detail.Security)
	}
	if detail.Financial == nil || detail.Financial.QuarterlyRevenueYoYPct != 54 {
		t.Fatalf("financial = %#v", detail.Financial)
	}
	if len(detail.Insiders) != 1 || detail.Insiders[0].OwnerName != "CEO" {
		t.Fatalf("insiders = %#v", detail.Insiders)
	}
	if len(detail.CapitalRisks) != 1 || !detail.CapitalRisks[0].BlocksA {
		t.Fatalf("risks = %#v", detail.CapitalRisks)
	}
	if detail.DataQuality["financial"] != QualityStatusValid || detail.DataQuality["universe"] != QualityStatusValid {
		t.Fatalf("quality = %#v", detail.DataQuality)
	}
}

func TestGetCandidateDetailMissingTicker(t *testing.T) {
	db := openMigratedTestDatabase(t)
	if _, err := GetCandidateDetail(context.Background(), db, "missing"); err == nil {
		t.Fatalf("expected missing ticker error")
	}
}
