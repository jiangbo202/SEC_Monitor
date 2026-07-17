package discovery

import (
	"context"
	"testing"
	"time"
)

func TestRecordCandidateRecalcEventForFilingMarksCurrentCandidateDirty(t *testing.T) {
	db := openMigratedTestDatabase(t)
	now := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	securityBatch := UniverseBatch{BatchID: "security", Kind: BatchKindSecurity, Status: BatchStatusPublished, StartedAt: now}
	marketBatch := UniverseBatch{BatchID: "market", Kind: BatchKindPrescreen, Status: BatchStatusPublished, UniverseSourceVersion: securityBatch.BatchID, StartedAt: now}
	if err := db.Create(&[]UniverseBatch{securityBatch, marketBatch}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&CurrentBatchPointer{Kind: BatchKindPrescreen, BatchID: marketBatch.BatchID}).Error; err != nil {
		t.Fatal(err)
	}
	security := Security{CIK: "0000001234", CatalogStatus: SecurityCatalogPublished}
	if err := db.Create(&security).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&SecurityBatchIdentity{BatchID: securityBatch.BatchID, SecurityID: security.ID, CIK: security.CIK, Ticker: "EVNT", MappingStatus: MappingStatusCurrent}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&CandidateScoreSnapshot{BatchID: marketBatch.BatchID, SecurityID: security.ID, Ticker: "EVNT", Grade: CandidateGradeB}).Error; err != nil {
		t.Fatal(err)
	}

	created, err := RecordCandidateRecalcEventForFiling(context.Background(), db, CandidateRecalcFilingInput{
		FilingID: "filing-1", AccessionNumber: "0000001234-26-000001", Ticker: "EVNT", CIK: security.CIK, FilingType: "10-Q", FilingDate: now,
	})
	if err != nil || !created {
		t.Fatalf("created=%v err=%v", created, err)
	}
	again, err := RecordCandidateRecalcEventForFiling(context.Background(), db, CandidateRecalcFilingInput{
		FilingID: "filing-1", AccessionNumber: "0000001234-26-000001", Ticker: "EVNT", CIK: security.CIK, FilingType: "10-Q", FilingDate: now,
	})
	if err != nil || again {
		t.Fatalf("again=%v err=%v", again, err)
	}
	var event CandidateRecalcEvent
	if err := db.First(&event, "filing_id = ?", "filing-1").Error; err != nil {
		t.Fatal(err)
	}
	if event.Status != CandidateRecalcStatusDirty || event.Ticker != "EVNT" || event.SecurityID != security.ID {
		t.Fatalf("event = %#v", event)
	}
	nonFinancial, err := RecordCandidateRecalcEventForFiling(context.Background(), db, CandidateRecalcFilingInput{FilingID: "filing-8k", Ticker: "EVNT", FilingType: "8-K"})
	if err != nil || nonFinancial {
		t.Fatalf("non-financial created=%v err=%v", nonFinancial, err)
	}
	missing, err := RecordCandidateRecalcEventForFiling(context.Background(), db, CandidateRecalcFilingInput{FilingID: "filing-2", Ticker: "MISS"})
	if err != nil || missing {
		t.Fatalf("missing=%v err=%v", missing, err)
	}
}
