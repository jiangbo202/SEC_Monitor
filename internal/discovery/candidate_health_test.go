package discovery

import (
	"context"
	"testing"
	"time"
)

func TestBuildCandidateHealthSummarizesMissingData(t *testing.T) {
	db := openMigratedTestDatabase(t)
	ready := Security{CIK: "0000011001", CompanyName: "Ready Co", CatalogStatus: SecurityCatalogPublished}
	missing := Security{CIK: "0000011002", CompanyName: "Missing Co", CatalogStatus: SecurityCatalogPublished}
	if err := db.Create(&ready).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&missing).Error; err != nil {
		t.Fatal(err)
	}
	batch := UniverseBatch{BatchID: "health-current", Kind: BatchKindPrescreen, Status: BatchStatusPublished, SourceVersionsJSON: `[{"source":"insiders:sec-form4","version":"v1"}]`, StartedAt: time.Now()}
	if err := db.Create(&batch).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&CurrentBatchPointer{Kind: BatchKindPrescreen, BatchID: batch.BatchID}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&[]CandidateScoreSnapshot{
		{BatchID: batch.BatchID, SecurityID: ready.ID, Ticker: "RDY", Grade: CandidateGradeA, EligibleA: true, MarketCapUSD: 100_000_000, TotalScore: 90},
		{BatchID: batch.BatchID, SecurityID: missing.ID, Ticker: "MISS", Grade: CandidateGradeB, EligibleB: true, TotalScore: 65},
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&FinancialMetricSnapshot{BatchID: batch.BatchID, SecurityID: ready.ID, RevenueGrowthAvailable: true, RunwayAvailable: true}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&InsiderTransactionSnapshot{SecurityID: ready.ID, Accession: "h1", TransactionDate: time.Now(), TransactionCode: "P", Qualified: true}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&CapitalRiskSnapshot{BatchID: batch.BatchID, SecurityID: ready.ID, Kind: CapitalEventATMProgram, Accession: "risk-1", EffectiveAt: time.Now(), Active: true}).Error; err != nil {
		t.Fatal(err)
	}

	health, err := BuildCandidateHealth(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	if health.BatchID != batch.BatchID || health.TotalCandidates != 2 || health.MissingFinancials != 1 || health.MissingInsiders != 0 || health.CandidatesWithInsiderRecords != 1 || health.InsiderRecordCoveragePct != 50 || health.NoQualifiedInsiderCandidates != 1 || health.QualifiedInsiderCandidates != 1 || health.MissingMarketCap != 1 || health.ActiveRiskEvents != 1 {
		t.Fatalf("health = %#v", health)
	}
	if health.Status != CandidateHealthDegraded || len(health.Issues) == 0 {
		t.Fatalf("status=%s issues=%#v", health.Status, health.Issues)
	}
}

func TestBuildCandidateHealthReadsFinancialsFromSecurityBatch(t *testing.T) {
	db := openMigratedTestDatabase(t)
	security := Security{CIK: "0000008801", CompanyName: "Healthy Co", CatalogStatus: SecurityCatalogPublished}
	if err := db.Create(&security).Error; err != nil {
		t.Fatal(err)
	}
	securityBatch := UniverseBatch{BatchID: "security-health", Kind: BatchKindSecurity, Status: BatchStatusPublished, SourceVersionsJSON: `[{"source":"insiders:sec-form4","version":"v1"}]`, StartedAt: time.Now().Add(-time.Minute)}
	marketBatch := UniverseBatch{BatchID: "market-health", Kind: BatchKindPrescreen, Status: BatchStatusPublished, UniverseSourceVersion: securityBatch.BatchID, StartedAt: time.Now()}
	if err := db.Create(&[]UniverseBatch{securityBatch, marketBatch}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&CurrentBatchPointer{Kind: BatchKindPrescreen, BatchID: marketBatch.BatchID}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&CandidateScoreSnapshot{BatchID: marketBatch.BatchID, SecurityID: security.ID, Ticker: "HLTH", Grade: CandidateGradeB, EligibleB: true, MarketCapUSD: 120_000_000}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&FinancialMetricSnapshot{BatchID: securityBatch.BatchID, SecurityID: security.ID, RevenueGrowthAvailable: true, RunwayAvailable: true}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&InsiderTransactionSnapshot{SecurityID: security.ID, Accession: "h2", TransactionDate: time.Now(), TransactionCode: "P", Qualified: true}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&SECFilingSnapshot{SecurityID: security.ID, AccessionNumber: "0000008801-26-000001", FilingType: "10-Q", FilingDate: time.Now()}).Error; err != nil {
		t.Fatal(err)
	}

	health, err := BuildCandidateHealth(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	if health.MissingFinancials != 0 || health.MissingInsiders != 0 || health.CandidatesWithInsiderRecords != 1 || health.InsiderRecordCoveragePct != 100 || health.QualifiedInsiderCandidates != 1 || health.NoQualifiedInsiderCandidates != 0 || health.InsiderDataStatus != "available" || health.Status != CandidateHealthOK {
		t.Fatalf("health = %#v", health)
	}
}

func TestBuildCandidateHealthDistinguishesInsiderDataFromNoSignal(t *testing.T) {
	tests := []struct {
		name                 string
		sourceVersionsJSON   string
		qualified            bool
		wantDataStatus       string
		wantMissing          int
		wantQualified        int
		wantWithoutQualified int
		wantStatus           string
	}{
		{name: "source available without candidate records", sourceVersionsJSON: `[{"source":"insiders:sec-form4","version":"v1"}]`, wantDataStatus: "available", wantWithoutQualified: 1, wantStatus: CandidateHealthDegraded},
		{name: "source missing", sourceVersionsJSON: `[]`, wantDataStatus: "missing", wantMissing: 1, wantStatus: CandidateHealthDegraded},
		{name: "source available with qualified purchase", sourceVersionsJSON: `[{"source":"insiders:sec-form4","version":"v1"}]`, qualified: true, wantDataStatus: "available", wantQualified: 1, wantStatus: CandidateHealthOK},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := openMigratedTestDatabase(t)
			now := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
			security := Security{CIK: "0000008811", CompanyName: "Health Signal Co", CatalogStatus: SecurityCatalogPublished}
			if err := db.Create(&security).Error; err != nil {
				t.Fatal(err)
			}
			securityBatch := UniverseBatch{BatchID: "security-health-signal", Kind: BatchKindSecurity, Status: BatchStatusPublished, SourceVersionsJSON: tt.sourceVersionsJSON, StartedAt: now.Add(-time.Minute)}
			marketBatch := UniverseBatch{BatchID: "market-health-signal", Kind: BatchKindPrescreen, Status: BatchStatusPublished, UniverseSourceVersion: securityBatch.BatchID, StartedAt: now}
			if err := db.Create(&[]UniverseBatch{securityBatch, marketBatch}).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Create(&CurrentBatchPointer{Kind: BatchKindPrescreen, BatchID: marketBatch.BatchID}).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Create(&CandidateScoreSnapshot{BatchID: marketBatch.BatchID, SecurityID: security.ID, Ticker: "SIG", Grade: CandidateGradeB, EligibleB: true, MarketCapUSD: 120_000_000}).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Create(&FinancialMetricSnapshot{BatchID: securityBatch.BatchID, SecurityID: security.ID, RevenueGrowthAvailable: true, RunwayAvailable: true}).Error; err != nil {
				t.Fatal(err)
			}
			if tt.qualified {
				if err := db.Create(&InsiderTransactionSnapshot{SecurityID: security.ID, Accession: "signal", TransactionDate: now, TransactionCode: "P", Qualified: true}).Error; err != nil {
					t.Fatal(err)
				}
				if err := db.Create(&SECFilingSnapshot{SecurityID: security.ID, AccessionNumber: "0000008811-26-000001", FilingType: "8-K", FilingDate: now}).Error; err != nil {
					t.Fatal(err)
				}
			}

			health, err := BuildCandidateHealth(context.Background(), db)
			if err != nil {
				t.Fatal(err)
			}
			if health.InsiderDataStatus != tt.wantDataStatus || health.MissingInsiders != tt.wantMissing || health.QualifiedInsiderCandidates != tt.wantQualified || health.NoQualifiedInsiderCandidates != tt.wantWithoutQualified || health.Status != tt.wantStatus {
				t.Fatalf("health = %#v", health)
			}
		})
	}
}

func TestBuildCandidateHealthRecoversAvailabilityFromBatchCoverageSnapshots(t *testing.T) {
	db := openMigratedTestDatabase(t)
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	security := Security{CIK: "0000009911", CompanyName: "Coverage Lineage Co", CatalogStatus: SecurityCatalogPublished}
	if err := db.Create(&security).Error; err != nil {
		t.Fatal(err)
	}
	securityBatch := UniverseBatch{BatchID: "security-coverage-lineage", Kind: BatchKindSecurity, Status: BatchStatusPublished, SourceVersionsJSON: `[]`, StartedAt: now.Add(-time.Minute)}
	marketBatch := UniverseBatch{BatchID: "market-coverage-lineage", Kind: BatchKindPrescreen, Status: BatchStatusPublished, UniverseSourceVersion: securityBatch.BatchID, EffectiveDate: "2026-08-25", StartedAt: now}
	if err := db.Create(&[]UniverseBatch{securityBatch, marketBatch}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&CurrentBatchPointer{Kind: BatchKindPrescreen, BatchID: marketBatch.BatchID}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&CandidateScoreSnapshot{BatchID: marketBatch.BatchID, SecurityID: security.ID, Ticker: "LINE", Grade: CandidateGradeB, EligibleB: true, MarketCapUSD: 120_000_000}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&FinancialMetricSnapshot{BatchID: securityBatch.BatchID, SecurityID: security.ID, RevenueGrowthAvailable: true, RunwayAvailable: true}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&InsiderCoverageSnapshot{BatchID: securityBatch.BatchID, SecurityID: security.ID, CIK: security.CIK, Status: InsiderCoverageCoveredNoFilings, CheckedAt: now}).Error; err != nil {
		t.Fatal(err)
	}

	health, err := BuildCandidateHealth(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	if health.InsiderDataStatus != "available" || health.InsiderLineageStatus != "coverage_snapshot" || health.MissingInsiders != 0 || health.NoQualifiedInsiderCandidates != 1 {
		t.Fatalf("health = %#v", health)
	}
	if health.Status != CandidateHealthDegraded || !containsString(health.Issues, "insider_source_lineage_recovered_from_coverage") {
		t.Fatalf("health issues = %#v", health)
	}
}
