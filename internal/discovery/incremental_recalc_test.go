package discovery

import (
	"context"
	"testing"
	"time"
)

func TestRefreshCurrentCandidateFinancialsReplacesCurrentMetricAndScore(t *testing.T) {
	db := openMigratedTestDatabase(t)
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	security := Security{CIK: "0000012345", CompanyName: "Growth Co", SIC: 7371, CatalogStatus: SecurityCatalogPublished}
	mustCreate(t, db, &security)
	securityBatch := UniverseBatch{BatchID: "security", Kind: BatchKindSecurity, Status: BatchStatusPublished, StartedAt: now}
	marketBatch := UniverseBatch{BatchID: "market", Kind: BatchKindPrescreen, Status: BatchStatusPublished, UniverseSourceVersion: securityBatch.BatchID, StartedAt: now}
	mustCreate(t, db, &[]UniverseBatch{securityBatch, marketBatch})
	mustCreate(t, db, &CurrentBatchPointer{Kind: BatchKindPrescreen, BatchID: marketBatch.BatchID})
	mustCreate(t, db, &SecurityBatchIdentity{BatchID: securityBatch.BatchID, SecurityID: security.ID, Ticker: "GROW", SIC: security.SIC})
	mustCreate(t, db, &UniverseSnapshot{BatchID: marketBatch.BatchID, SecurityID: security.ID, Ticker: "GROW", MarketCapUSD: 120_000_000, QualityStatus: QualityStatusValid})
	mustCreate(t, db, &FinancialMetricSnapshot{BatchID: securityBatch.BatchID, SecurityID: security.ID, RevenueGrowthAvailable: false})
	mustCreate(t, db, &CandidateScoreSnapshot{BatchID: marketBatch.BatchID, SecurityID: security.ID, Ticker: "GROW", MarketCapUSD: 120_000_000, Grade: CandidateGradeExcluded, ReasonCode: "criteria_not_met"})
	mustCreate(t, db, &[]FinancialFactSnapshot{
		incrementalRevenueFact(security.ID, "2025-01-01", "2025-03-31", 10_000_000, now),
		incrementalRevenueFact(security.ID, "2026-01-01", "2026-03-31", 20_000_000, now),
	})

	updated, err := RefreshCurrentCandidateFinancials(context.Background(), db, []uint{security.ID}, now)
	if err != nil {
		t.Fatalf("RefreshCurrentCandidateFinancials: %v", err)
	}
	if updated != 1 {
		t.Fatalf("updated = %d, want 1", updated)
	}
	var metric FinancialMetricSnapshot
	if err := db.First(&metric, "batch_id = ? AND security_id = ?", securityBatch.BatchID, security.ID).Error; err != nil {
		t.Fatal(err)
	}
	if !metric.RevenueGrowthAvailable || metric.QuarterlyRevenueYoYPct != 100 {
		t.Fatalf("metric = %+v", metric)
	}
	var score CandidateScoreSnapshot
	if err := db.First(&score, "batch_id = ? AND security_id = ?", marketBatch.BatchID, security.ID).Error; err != nil {
		t.Fatal(err)
	}
	if score.Grade != CandidateGradeB || !score.EligibleB || score.TotalScore != 49 || score.RevenueGrowthPct != 100 {
		t.Fatalf("score = %+v", score)
	}
}

func incrementalRevenueFact(securityID uint, start, end string, amount int64, acceptedAt time.Time) FinancialFactSnapshot {
	periodStart, _ := time.Parse("2006-01-02", start)
	periodEnd, _ := time.Parse("2006-01-02", end)
	return FinancialFactSnapshot{
		SecurityID: securityID, Metric: FinancialMetricRevenue, Concept: "us-gaap:Revenues", Unit: "USD",
		PeriodStart: periodStart, PeriodEnd: periodEnd, Accession: "acc-" + end, AmountMicros: amount * 1_000_000,
		Form: "10-Q", QualityStatus: QualityStatusValid, FiledAt: acceptedAt, AcceptedAt: acceptedAt,
	}
}
