package discovery

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"
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

func TestBuildCandidateReportBranchesTableDriven(t *testing.T) {
	tests := []struct {
		name    string
		setup   func(t *testing.T, db *gorm.DB) string
		wantErr error
		wantID  string
	}{
		{
			name:    "missing database returns error",
			setup:   nil,
			wantErr: errors.New("database is required"),
		},
		{
			name:  "missing batch returns normal empty state",
			setup: func(t *testing.T, db *gorm.DB) string { return "2026-06-30" },
		},
		{
			name: "falls back to current published batch when date misses",
			setup: func(t *testing.T, db *gorm.DB) string {
				batch := UniverseBatch{BatchID: "fallback-current", Kind: BatchKindPrescreen, Status: BatchStatusPublished, EffectiveDate: "2026-06-29", StartedAt: time.Now()}
				if err := db.Create(&batch).Error; err != nil {
					t.Fatal(err)
				}
				if err := db.Create(&CurrentBatchPointer{Kind: BatchKindPrescreen, BatchID: batch.BatchID}).Error; err != nil {
					t.Fatal(err)
				}
				return "2026-06-30"
			},
			wantID: "fallback-current",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var db *gorm.DB
			date := "2026-06-30"
			if tt.setup != nil {
				db = openMigratedTestDatabase(t)
				date = tt.setup(t, db)
			}
			report, err := BuildCandidateReport(context.Background(), db, date)
			if tt.wantErr != nil {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr.Error()) {
					t.Fatalf("BuildCandidateReport err=%v, want %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("BuildCandidateReport: %v", err)
			}
			if tt.wantID == "" {
				if report.Status != "empty" || report.Available || report.Message == "" {
					t.Fatalf("empty report = %#v", report)
				}
				return
			}
			if report.Batch.BatchID != tt.wantID {
				t.Fatalf("report batch = %q, want %q", report.Batch.BatchID, tt.wantID)
			}
		})
	}
}

func TestBuildCandidateReportPinsHistoricalBatchInsteadOfCurrentPointer(t *testing.T) {
	db := openMigratedTestDatabase(t)
	oldBatch := UniverseBatch{BatchID: "report-old", Kind: BatchKindPrescreen, Status: BatchStatusPublished, EffectiveDate: "2026-06-29", StartedAt: time.Date(2026, 6, 29, 12, 0, 0, 0, time.UTC)}
	currentBatch := UniverseBatch{BatchID: "report-new", Kind: BatchKindPrescreen, Status: BatchStatusPublished, EffectiveDate: "2026-06-30", StartedAt: time.Date(2026, 6, 30, 12, 0, 0, 0, time.UTC)}
	if err := db.Create(&[]UniverseBatch{oldBatch, currentBatch}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&CurrentBatchPointer{Kind: BatchKindPrescreen, BatchID: currentBatch.BatchID}).Error; err != nil {
		t.Fatal(err)
	}

	report, err := BuildCandidateReport(context.Background(), db, oldBatch.EffectiveDate)
	if err != nil {
		t.Fatal(err)
	}
	if report.Batch.BatchID != oldBatch.BatchID || report.Summary.BatchID != oldBatch.BatchID || report.Health.BatchID != oldBatch.BatchID {
		t.Fatalf("historical report mixed batches: batch=%q summary=%q health=%q", report.Batch.BatchID, report.Summary.BatchID, report.Health.BatchID)
	}
	if report.SnapshotID == 0 || report.SchemaVersion != CandidateReportSchemaV1 || len(report.ContentSHA256) != 64 {
		t.Fatalf("archive metadata = %#v", report)
	}
}

func TestBuildCandidateReportSnapshotIsIdempotentAndImmutable(t *testing.T) {
	db := openMigratedTestDatabase(t)
	batch := UniverseBatch{BatchID: "report-immutable", Kind: BatchKindPrescreen, Status: BatchStatusPublished, EffectiveDate: "2026-06-30", StartedAt: time.Now()}
	if err := db.Create(&batch).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&CurrentBatchPointer{Kind: BatchKindPrescreen, BatchID: batch.BatchID}).Error; err != nil {
		t.Fatal(err)
	}

	first, err := BuildAndPersistCandidateReport(context.Background(), db, batch.BatchID)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&UniverseBatch{}).Where("batch_id = ?", batch.BatchID).Update("effective_date", "2099-01-01").Error; err != nil {
		t.Fatal(err)
	}
	second, err := BuildAndPersistCandidateReport(context.Background(), db, batch.BatchID)
	if err != nil {
		t.Fatal(err)
	}
	if second.SnapshotID != first.SnapshotID || second.ContentSHA256 != first.ContentSHA256 || !second.Generated.Equal(first.Generated) {
		t.Fatalf("snapshot changed across retry: first=%#v second=%#v", first, second)
	}
	if second.Date != "2026-06-30" || second.Batch.EffectiveDate != "2026-06-30" {
		t.Fatalf("snapshot was rewritten from mutable source: %#v", second)
	}
	var count int64
	if err := db.Model(&CandidateReportSnapshot{}).Where("batch_id = ?", batch.BatchID).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("snapshot count = %d, want 1", count)
	}
}
