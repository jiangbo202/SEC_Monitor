package discovery

import (
	"context"
	"testing"
	"time"
)

func TestCheckSmallCapEligibilityExplainsCurrentRulesAndPersistsHistory(t *testing.T) {
	db := openMigratedTestDatabase(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 3, 0, 0, 0, 0, time.UTC)
	security := Security{CIK: "0000000999", CompanyName: "Eligible Co", CatalogStatus: SecurityCatalogPublished}
	mustCreate(t, db, &security)
	securityBatch := UniverseBatch{BatchID: "security-eligibility", Kind: BatchKindSecurity, Status: BatchStatusPublished, EffectiveDate: "2026-08-02", StartedAt: now}
	marketBatch := UniverseBatch{BatchID: "market-eligibility", Kind: BatchKindPrescreen, Status: BatchStatusPublished, EffectiveDate: "2026-08-02", UniverseSourceVersion: securityBatch.BatchID, StartedAt: now}
	mustCreate(t, db, &securityBatch)
	mustCreate(t, db, &marketBatch)
	mustCreate(t, db, &CurrentBatchPointer{Kind: BatchKindPrescreen, BatchID: marketBatch.BatchID, UpdatedAt: now})
	mustCreate(t, db, &SecurityBatchIdentity{BatchID: securityBatch.BatchID, SecurityID: security.ID, CIK: security.CIK, Ticker: "PASS", MappingStatus: MappingStatusCurrent, CompanyName: security.CompanyName, SIC: 3674})
	mustCreate(t, db, &ClassificationSnapshot{BatchID: securityBatch.BatchID, SecurityID: security.ID, Included: true, Status: EffectiveStatusIncluded})
	mustCreate(t, db, &UniverseSnapshot{BatchID: marketBatch.BatchID, SecurityID: security.ID, Ticker: "PASS", MarketCapUSD: 120_000_000, QualityStatus: QualityStatusValid, Included: true, Status: EffectiveStatusPrescreen})
	mustCreate(t, db, &FinancialMetricSnapshot{BatchID: securityBatch.BatchID, SecurityID: security.ID, RevenueGrowthAvailable: true, QuarterlyRevenueYoYPct: 56, LatestQuarterRevenueUSD: 156_000_000, PriorYearQuarterRevenueUSD: 100_000_000, RunwayAvailable: true, CashRunwayMonths: 18, AvailableCashUSD: 100_000_000})
	mustCreate(t, db, &InsiderCoverageSnapshot{BatchID: securityBatch.BatchID, SecurityID: security.ID, Status: QualityStatusValid, ParsedDocuments: 1, CheckedAt: now})
	mustCreate(t, db, &InsiderTransactionSnapshot{SecurityID: security.ID, Accession: "0000000999-26-000001", Role: InsiderRoleCEO, TransactionDate: now.AddDate(0, 0, -10), TransactionCode: "P", Qualified: true})

	result, err := CheckSmallCapEligibility(ctx, db, SmallCapEligibilityCheckInput{Ticker: " pass "}, now)
	if err != nil {
		t.Fatalf("CheckSmallCapEligibility: %v", err)
	}
	if !result.InSmallCapPool || !result.EligibleA || !result.EligibleB || result.Grade != CandidateGradeA {
		t.Fatalf("unexpected eligibility result: %+v", result)
	}
	if len(result.Conditions) < 8 {
		t.Fatalf("conditions = %d, want complete explanation", len(result.Conditions))
	}
	if result.Evidence == nil || result.Evidence.Market == nil || result.Evidence.Financial == nil || result.Evidence.InsiderCoverage == nil {
		t.Fatalf("evidence snapshot was not retained: %+v", result.Evidence)
	}
	history, err := ListSmallCapEligibilityCheckHistory(ctx, db, 1, 20, "PASS")
	if err != nil {
		t.Fatalf("ListSmallCapEligibilityCheckHistory: %v", err)
	}
	if history.Total != 1 || len(history.Items) != 1 || history.Items[0].Result.Grade != CandidateGradeA || history.Items[0].Result.Evidence == nil || history.Items[0].Result.Evidence.Financial == nil {
		t.Fatalf("unexpected history: %+v", history)
	}

	if err := db.Model(&UniverseSnapshot{}).Where("batch_id = ? AND security_id = ?", marketBatch.BatchID, security.ID).Update("market_cap_usd", 200_000_000).Error; err != nil {
		t.Fatalf("update market cap: %v", err)
	}
	next, err := CheckSmallCapEligibility(ctx, db, SmallCapEligibilityCheckInput{Ticker: "PASS"}, now.AddDate(0, 1, 0))
	if err != nil {
		t.Fatalf("second CheckSmallCapEligibility: %v", err)
	}
	if next.Comparison == nil || next.Comparison.PreviousCheckedAt != now {
		t.Fatalf("missing comparison: %+v", next.Comparison)
	}
	foundMarketCapChange := false
	for _, change := range next.Comparison.Changes {
		if change.Key == "small_cap_pool" && change.PreviousActual == "$120M" && change.CurrentActual == "$200M" {
			foundMarketCapChange = true
		}
	}
	if !foundMarketCapChange {
		t.Fatalf("market-cap comparison missing: %+v", next.Comparison.Changes)
	}
}

func TestCheckSmallCapEligibilityRecordsUnknownTicker(t *testing.T) {
	db := openMigratedTestDatabase(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 3, 0, 0, 0, 0, time.UTC)
	marketBatch := UniverseBatch{BatchID: "market-unknown", Kind: BatchKindPrescreen, Status: BatchStatusPublished, EffectiveDate: "2026-08-02", UniverseSourceVersion: "security-unknown", StartedAt: now}
	mustCreate(t, db, &marketBatch)
	mustCreate(t, db, &CurrentBatchPointer{Kind: BatchKindPrescreen, BatchID: marketBatch.BatchID, UpdatedAt: now})

	result, err := CheckSmallCapEligibility(ctx, db, SmallCapEligibilityCheckInput{Ticker: "none"}, now)
	if err != nil {
		t.Fatalf("CheckSmallCapEligibility: %v", err)
	}
	if result.InSmallCapPool || result.Grade != CandidateGradeExcluded || len(result.Conditions) != 2 {
		t.Fatalf("unexpected unknown ticker result: %+v", result)
	}
	if result.Conditions[0].Status != EligibilityStatusUnavailable {
		t.Fatalf("status = %q", result.Conditions[0].Status)
	}
}
