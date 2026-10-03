package discovery

import (
	"context"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"
	"sec_monitor/internal/config"
)

func TestCapitalRiskStorageSharesContentAndPreservesBatchVersions(t *testing.T) {
	db := openMigratedTestDatabase(t)
	security := Security{CIK: "0000012345", CompanyName: "Shared risk"}
	if err := db.Create(&security).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 3, 1, 2, 3, 400, time.FixedZone("HK", 8*3600))
	first := CapitalRiskSnapshot{ID: 11, BatchID: "first", SecurityID: security.ID, Kind: CapitalEventATMProgram, Accession: "one", EffectiveAt: now.Add(-time.Hour), AcceptedAt: now, ActiveUntil: now.Add(time.Hour), Active: true, BlocksA: true, Severity: CapitalRiskSeverityHigh, Reason: "Original evidence", CreatedAt: now}
	second := first
	second.ID = 12
	second.BatchID = "second"
	second.CreatedAt = now.Add(time.Hour)
	third := second
	third.ID = 13
	third.BatchID = "third"
	third.Active = false
	third.Reason = "Closed program"
	third.CreatedAt = now.Add(2 * time.Hour)
	if err := PersistCapitalRiskSnapshots(context.Background(), db, []CapitalRiskSnapshot{first, second, third}); err != nil {
		t.Fatal(err)
	}
	var identities, versions int64
	db.Model(&CapitalRiskIdentity{}).Count(&identities)
	db.Model(&CapitalRiskVersion{}).Count(&versions)
	if identities != 1 || versions != 2 {
		t.Fatalf("identities=%d versions=%d", identities, versions)
	}
	var actual []CapitalRiskSnapshot
	if err := db.Order("id").Find(&actual).Error; err != nil {
		t.Fatal(err)
	}
	for i, want := range []CapitalRiskSnapshot{first, second, third} {
		normalizeRiskTimes(&want)
		normalizeRiskTimes(&actual[i])
		if !reflect.DeepEqual(want, actual[i]) {
			t.Fatalf("snapshot %d differs: %+v / %+v", i, want, actual[i])
		}
	}
	duplicate := first
	duplicate.ID = 0
	duplicate.Reason = "Should not replace original"
	if err := PersistCapitalRiskSnapshots(context.Background(), db, []CapitalRiskSnapshot{duplicate}); err != nil {
		t.Fatal(err)
	}
	var stored CapitalRiskSnapshot
	if err := db.Where("batch_id = ?", "first").First(&stored).Error; err != nil {
		t.Fatal(err)
	}
	if stored.ID != 11 || stored.Reason != first.Reason {
		t.Fatalf("first write replaced: %+v", stored)
	}
	// The full schema migration must not recreate a physical snapshot table.
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	var kind string
	db.Raw("SELECT type FROM sqlite_master WHERE name='capital_risk_snapshots'").Scan(&kind)
	if kind != "view" {
		t.Fatalf("snapshot relation = %s", kind)
	}
}

func legacyRiskTestDB(t *testing.T, n int) (*gorm.DB, []CapitalRiskSnapshot) {
	t.Helper()
	db, err := OpenDatabase(config.DatabaseConfig{Type: "sqlite", DSN: filepath.Join(t.TempDir(), "legacy.db")})
	if err != nil {
		t.Fatal(err)
	}
	connection, _ := db.DB()
	t.Cleanup(func() { connection.Close() })
	if err := db.AutoMigrate(&Security{}, &CapitalRiskSnapshot{}); err != nil {
		t.Fatal(err)
	}
	security := Security{CIK: "0000012345"}
	if err := db.Create(&security).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 3, 1, 2, 3, 456, time.UTC)
	rows := make([]CapitalRiskSnapshot, n)
	for i := range rows {
		rows[i] = CapitalRiskSnapshot{ID: uint(100 + i), BatchID: fmt.Sprintf("batch-%d", i%3), SecurityID: security.ID, Kind: CapitalEventATMProgram, Accession: fmt.Sprintf("filing-%d", i/3), EffectiveAt: now, AcceptedAt: now.Add(-time.Hour), Active: true, BlocksA: true, Reason: "Exact historical evidence", CreatedAt: now.Add(time.Duration(i) * time.Minute)}
	}
	if err := db.CreateInBatches(&rows, 50).Error; err != nil {
		t.Fatal(err)
	}
	return db, rows
}

func TestCapitalRiskConsolidationResumesAndVerifiesAllHistoricalRows(t *testing.T) {
	db, want := legacyRiskTestDB(t, 2003)
	ctx, cancel := context.WithCancel(context.Background())
	_, err := ConsolidateCapitalRiskStorage(ctx, db, func(done, total int64) { cancel() })
	if err == nil {
		t.Fatal("expected canceled maintenance")
	}
	if !db.Migrator().HasTable(legacyCapitalRiskTable) {
		t.Fatal("legacy evidence removed before verification")
	}
	var count int64
	db.Model(&CapitalRiskSnapshot{}).Count(&count)
	if count != int64(len(want)) {
		t.Fatalf("partial migration exposes %d rows", count)
	}
	// New writes cannot reuse a legacy snapshot ID while migration is incomplete.
	extra := want[0]
	extra.ID = 0
	extra.BatchID = "new-batch"
	if err := PersistCapitalRiskSnapshots(context.Background(), db, []CapitalRiskSnapshot{extra}); err != nil {
		t.Fatal(err)
	}
	var inserted CapitalRiskSnapshot
	db.Where("batch_id = ?", extra.BatchID).First(&inserted)
	if inserted.ID <= want[len(want)-1].ID {
		t.Fatal("live write reused a legacy ID")
	}
	result, err := ConsolidateCapitalRiskStorage(context.Background(), db, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !result.LegacyRemoved || result.MigratedRows != int64(len(want)) || len(result.SnapshotSHA256) != 64 {
		t.Fatalf("result=%+v", result)
	}
	var actual []CapitalRiskSnapshot
	db.Where("batch_id <> ?", extra.BatchID).Order("id").Find(&actual)
	if len(actual) != len(want) {
		t.Fatalf("rows=%d want=%d", len(actual), len(want))
	}
	for i := range want {
		normalizeRiskTimes(&want[i])
		normalizeRiskTimes(&actual[i])
		if !reflect.DeepEqual(want[i], actual[i]) {
			t.Fatalf("historical row %d changed", i)
		}
	}
	var foreignKeyErrors []map[string]any
	if err := db.Raw("PRAGMA foreign_key_check").Scan(&foreignKeyErrors).Error; err != nil || len(foreignKeyErrors) > 0 {
		t.Fatalf("foreign keys: %v %v", foreignKeyErrors, err)
	}
	if db.Migrator().HasTable(legacyCapitalRiskTable) {
		t.Fatal("legacy table retained after verified consolidation")
	}
	again, err := ConsolidateCapitalRiskStorage(context.Background(), db, nil)
	if err != nil || !again.LegacyRemoved {
		t.Fatalf("repeat=%+v %v", again, err)
	}
}

func TestCapitalRiskConsolidationKeepsLegacyOnContentMismatch(t *testing.T) {
	db, _ := legacyRiskTestDB(t, 3)
	_, err := ConsolidateCapitalRiskStorage(context.Background(), db, func(done, total int64) {
		if err := db.Model(&CapitalRiskVersion{}).Where("1=1").Update("reason", "tampered").Error; err != nil {
			t.Fatal(err)
		}
	})
	if err == nil || !strings.Contains(err.Error(), "digest differs") {
		t.Fatalf("expected digest mismatch, got %v", err)
	}
	if !db.Migrator().HasTable(legacyCapitalRiskTable) {
		t.Fatal("original evidence was removed")
	}
}

func normalizeRiskTimes(r *CapitalRiskSnapshot) {
	r.EffectiveAt = r.EffectiveAt.UTC()
	r.AcceptedAt = r.AcceptedAt.UTC()
	r.ActiveUntil = r.ActiveUntil.UTC()
	r.CreatedAt = r.CreatedAt.UTC()
}
