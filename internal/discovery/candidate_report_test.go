package discovery

import (
	"context"
	"testing"
	"time"
)

func TestBuildCandidateReportIncludesSummaryAndHealth(t *testing.T) {
	db := openMigratedTestDatabase(t)
	security := Security{CIK: "0000013001", CompanyName: "Report Co", CatalogStatus: SecurityCatalogPublished}
	if err := db.Create(&security).Error; err != nil {
		t.Fatal(err)
	}
	batch := UniverseBatch{BatchID: "report-current", Kind: BatchKindPrescreen, Status: BatchStatusPublished, EffectiveDate: "2026-06-30", StartedAt: time.Now()}
	if err := db.Create(&batch).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&CurrentBatchPointer{Kind: BatchKindPrescreen, BatchID: batch.BatchID}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&CandidateScoreSnapshot{BatchID: batch.BatchID, SecurityID: security.ID, Ticker: "RPT", Grade: CandidateGradeA, EligibleA: true, TotalScore: 91, MarketCapUSD: 180_000_000}).Error; err != nil {
		t.Fatal(err)
	}

	report, err := BuildCandidateReport(context.Background(), db, "2026-06-30")
	if err != nil {
		t.Fatal(err)
	}
	if report.Date != "2026-06-30" || report.Batch.BatchID != batch.BatchID || report.Summary.TotalA != 1 {
		t.Fatalf("report = %#v", report)
	}
}
