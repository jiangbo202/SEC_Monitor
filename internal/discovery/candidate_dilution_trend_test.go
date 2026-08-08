package discovery

import (
	"context"
	"math"
	"testing"
	"time"
)

func TestBuildCandidateDilutionTrend(t *testing.T) {
	base := time.Date(2025, 1, 15, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		name       string
		rows       []ShareSnapshot
		wantStatus string
		wantPct    float64
	}{
		{
			name: "high dilution after a year",
			rows: []ShareSnapshot{
				{Instant: base, AcceptedAt: base.AddDate(0, 0, 2), Shares: 10_000_000},
				{Instant: base.AddDate(1, 0, 0), AcceptedAt: base.AddDate(1, 0, 2), Shares: 12_600_000},
			},
			wantStatus: "high_dilution",
			wantPct:    26,
		},
		{
			name: "stable share count",
			rows: []ShareSnapshot{
				{Instant: base, AcceptedAt: base.AddDate(0, 0, 2), Shares: 10_000_000},
				{Instant: base.AddDate(0, 6, 0), AcceptedAt: base.AddDate(0, 6, 2), Shares: 10_500_000},
			},
			wantStatus: "stable",
			wantPct:    5,
		},
		{
			name: "not enough separation",
			rows: []ShareSnapshot{
				{Instant: base, AcceptedAt: base.AddDate(0, 0, 2), Shares: 10_000_000},
				{Instant: base.AddDate(0, 1, 0), AcceptedAt: base.AddDate(0, 1, 2), Shares: 14_000_000},
			},
			wantStatus: "missing",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildCandidateDilutionTrend(tt.rows)
			if got.Status != tt.wantStatus {
				t.Fatalf("status = %q, want %q", got.Status, tt.wantStatus)
			}
			if math.Abs(got.ShareChangePct-tt.wantPct) > 0.0001 {
				t.Fatalf("share change = %.2f, want %.2f", got.ShareChangePct, tt.wantPct)
			}
			if tt.wantStatus == "missing" && !containsString(got.Reasons, "share_history_insufficient") {
				t.Fatalf("missing result reasons = %#v", got.Reasons)
			}
		})
	}
}

func TestPersistHistoricalShareSnapshotsBootstrapsDilutionEvidence(t *testing.T) {
	db := openMigratedTestDatabase(t)
	security := Security{CIK: "0000000001", CompanyName: "Acme", CatalogStatus: "published"}
	if err := db.Create(&security).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	base := now.AddDate(-1, 0, 0)
	facts := map[string][]ShareFact{
		security.CIK: {
			{CIK: security.CIK, Concept: "dei:EntityCommonStockSharesOutstanding", Unit: "shares", Form: "10-Q", Accession: "0000000001-25-000001", Instant: base, FiledAt: base.AddDate(0, 0, 2), AcceptedAt: base.AddDate(0, 0, 2), Shares: 10_000_000, SourceURL: "https://example.test/old"},
			{CIK: security.CIK, Concept: "dei:EntityCommonStockSharesOutstanding", Unit: "shares", Form: "10-Q", Accession: "0000000001-26-000001", Instant: now.AddDate(0, 0, -2), FiledAt: now.AddDate(0, 0, -1), AcceptedAt: now.AddDate(0, 0, -1), Shares: 12_000_000, SourceURL: "https://example.test/new"},
		},
	}
	coordinator := Coordinator{DB: db}
	if err := coordinator.persistHistoricalShareSnapshots(context.Background(), map[string]uint{security.CIK: security.ID}, facts, now); err != nil {
		t.Fatal(err)
	}
	if err := coordinator.persistHistoricalShareSnapshots(context.Background(), map[string]uint{security.CIK: security.ID}, facts, now); err != nil {
		t.Fatal(err)
	}
	var snapshots []ShareSnapshot
	if err := db.Where("security_id = ?", security.ID).Find(&snapshots).Error; err != nil {
		t.Fatal(err)
	}
	if len(snapshots) != 2 {
		t.Fatalf("snapshot count = %d, want 2", len(snapshots))
	}
	trend := buildCandidateDilutionTrend(snapshots)
	if trend.Status != "elevated_dilution" || math.Abs(trend.ShareChangePct-20) > 0.0001 {
		t.Fatalf("trend = %#v", trend)
	}
}
