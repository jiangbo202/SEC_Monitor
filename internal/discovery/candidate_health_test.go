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
	batch := UniverseBatch{BatchID: "health-current", Kind: BatchKindPrescreen, Status: BatchStatusPublished, StartedAt: time.Now()}
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

	health, err := BuildCandidateHealth(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	if health.BatchID != batch.BatchID || health.TotalCandidates != 2 || health.MissingFinancials != 1 || health.MissingInsiders != 1 || health.MissingMarketCap != 1 {
		t.Fatalf("health = %#v", health)
	}
	if health.Status != CandidateHealthDegraded || len(health.Issues) == 0 {
		t.Fatalf("status=%s issues=%#v", health.Status, health.Issues)
	}
}
