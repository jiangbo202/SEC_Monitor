package service

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"sec_monitor/internal/model"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestSQLiteBackupServiceCreatesVerifiedSnapshotsAndPrunesExpiredFiles(t *testing.T) {
	dir := t.TempDir()
	mainPath := filepath.Join(dir, "sec_monitor.db")
	discoveryPath := filepath.Join(dir, "small_cap.db")
	mainDB := openSQLiteBackupTestDB(t, mainPath)
	discoveryDB := openSQLiteBackupTestDB(t, discoveryPath)
	if err := mainDB.AutoMigrate(&model.SystemConfig{}, &model.RecoveryDrill{}, &model.TaskConfig{}, &model.WatchTarget{}, &model.Filing{}); err != nil {
		t.Fatal(err)
	}
	if err := mainDB.Create(&model.SystemConfig{ConfigKey: "example", ConfigValue: "value"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := discoveryDB.Exec("CREATE TABLE universe_batches (id INTEGER PRIMARY KEY)").Error; err != nil {
		t.Fatal(err)
	}
	if err := discoveryDB.Exec("CREATE TABLE securities (id INTEGER PRIMARY KEY, ticker TEXT)").Error; err != nil {
		t.Fatal(err)
	}
	if err := discoveryDB.Exec("CREATE TABLE candidate_score_snapshots (id INTEGER PRIMARY KEY)").Error; err != nil {
		t.Fatal(err)
	}
	backupDir := filepath.Join(dir, "backups")
	if err := os.MkdirAll(backupDir, 0o700); err != nil {
		t.Fatal(err)
	}
	oldBackup := filepath.Join(backupDir, "old.db")
	if err := os.WriteFile(oldBackup, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	oldTime := time.Now().AddDate(0, 0, -10)
	if err := os.Chtimes(oldBackup, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	service := NewSQLiteBackupService(mainDB, discoveryDB, mainPath, discoveryPath, nil)
	result, err := service.Backup(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Directory != filepath.Join(dir, "backups") || result.CompletedAt.IsZero() || result.Deleted != 1 {
		t.Fatalf("backup result = %#v", result)
	}
	for _, name := range []string{"sec_monitor", "small_cap"} {
		path := result.Files[name]
		if path == "" {
			t.Fatalf("missing %s snapshot: %#v", name, result)
		}
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("stat %s snapshot: %v", name, err)
		}
		if err := verifySQLiteBackup(path); err != nil {
			t.Fatalf("verify %s snapshot: %v", name, err)
		}
	}
	health, err := service.Health(context.Background())
	if err != nil || health.CompletePairs != 1 || health.LatestCompleted == nil || health.TotalBytes <= 0 || health.LatestPairBytes <= 0 {
		t.Fatalf("backup health = %#v, %v; want one complete pair", health, err)
	}
	verification, err := service.VerifyLatest(context.Background())
	if err != nil || verification.Files["sec_monitor"] == "" || verification.Files["small_cap"] == "" || verification.VerifiedAt.IsZero() {
		t.Fatalf("backup verification = %#v, %v", verification, err)
	}
	readiness, err := service.CheckRecoveryReadiness(context.Background())
	if err != nil || readiness.Status != "ready" || readiness.Verification == nil {
		t.Fatalf("recovery readiness = %#v, %v", readiness, err)
	}
	var drill model.RecoveryDrill
	if err := mainDB.Order("id DESC").First(&drill).Error; err != nil || drill.Status != "ready" || drill.CompletedAt == nil {
		t.Fatalf("recovery drill = %#v, %v", drill, err)
	}
	if _, err := os.Stat(oldBackup); !os.IsNotExist(err) {
		t.Fatalf("expired snapshot exists: %v", err)
	}
}

func TestSQLiteBackupRecoveryReadinessReportsNoBackupWithoutError(t *testing.T) {
	dir := t.TempDir()
	mainPath := filepath.Join(dir, "sec_monitor.db")
	discoveryPath := filepath.Join(dir, "small_cap.db")
	mainDB := openSQLiteBackupTestDB(t, mainPath)
	if err := mainDB.AutoMigrate(&model.RecoveryDrill{}); err != nil {
		t.Fatal(err)
	}
	service := NewSQLiteBackupService(mainDB, openSQLiteBackupTestDB(t, discoveryPath), mainPath, discoveryPath, nil)
	readiness, err := service.CheckRecoveryReadiness(context.Background())
	if err != nil || readiness.Status != "unavailable" || readiness.Reason == "" {
		t.Fatalf("recovery readiness = %#v, %v", readiness, err)
	}
	var drill model.RecoveryDrill
	if err := mainDB.Order("id DESC").First(&drill).Error; err != nil || drill.Status != "unavailable" {
		t.Fatalf("recovery drill = %#v, %v", drill, err)
	}
}

func TestSQLiteBackupServiceRemovesPartialPairWhenSecondDatabaseFails(t *testing.T) {
	dir := t.TempDir()
	mainPath := filepath.Join(dir, "sec_monitor.db")
	discoveryPath := filepath.Join(dir, "small_cap.db")
	mainDB := openSQLiteBackupTestDB(t, mainPath)
	discoveryDB := openSQLiteBackupTestDB(t, discoveryPath)
	sqlDB, err := discoveryDB.DB()
	if err != nil {
		t.Fatal(err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatal(err)
	}

	service := NewSQLiteBackupService(mainDB, discoveryDB, mainPath, discoveryPath, nil)
	if _, err := service.Backup(context.Background()); err == nil {
		t.Fatal("Backup should fail when the second database is closed")
	}
	backupDir := filepath.Join(dir, "backups")
	entries, err := os.ReadDir(backupDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if filepath.Ext(entry.Name()) == ".db" || filepath.Ext(entry.Name()) == ".partial" {
			t.Fatalf("partial backup was retained: %s", entry.Name())
		}
	}
}

func TestSQLiteBackupHealthReportsIncompletePair(t *testing.T) {
	dir := t.TempDir()
	mainPath := filepath.Join(dir, "sec_monitor.db")
	discoveryPath := filepath.Join(dir, "small_cap.db")
	mainDB := openSQLiteBackupTestDB(t, mainPath)
	discoveryDB := openSQLiteBackupTestDB(t, discoveryPath)
	backupDir := filepath.Join(dir, "backups")
	if err := os.MkdirAll(backupDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(backupDir, "sec_monitor-20260727T010203Z.db"), []byte("orphan"), 0o600); err != nil {
		t.Fatal(err)
	}
	service := NewSQLiteBackupService(mainDB, discoveryDB, mainPath, discoveryPath, nil)
	health, err := service.Health(context.Background())
	if err != nil || health.CompletePairs != 0 || health.IncompletePairs != 1 {
		t.Fatalf("backup health = %#v, %v", health, err)
	}
}

func TestSQLiteBackupServiceCompactsLiveDatabasesAfterVerifiedBackup(t *testing.T) {
	dir := t.TempDir()
	mainPath := filepath.Join(dir, "sec_monitor.db")
	discoveryPath := filepath.Join(dir, "small_cap.db")
	mainDB := openSQLiteBackupTestDB(t, mainPath)
	discoveryDB := openSQLiteBackupTestDB(t, discoveryPath)
	if err := mainDB.AutoMigrate(&model.SQLiteCompactionRun{}); err != nil {
		t.Fatal(err)
	}
	seedCompactionPayload(t, mainDB)
	seedCompactionPayload(t, discoveryDB)
	beforeMain, err := sqliteFileSize(mainPath)
	if err != nil {
		t.Fatal(err)
	}
	beforeDiscovery, err := sqliteFileSize(discoveryPath)
	if err != nil {
		t.Fatal(err)
	}

	service := NewSQLiteBackupService(mainDB, discoveryDB, mainPath, discoveryPath, nil)
	result, err := service.Compact(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "completed" || result.RunID == 0 || result.CompletedAt.IsZero() || len(result.Databases) != 2 {
		t.Fatalf("compaction result = %#v", result)
	}
	if result.Databases[0].BeforeBytes != beforeMain || result.Databases[1].BeforeBytes != beforeDiscovery {
		t.Fatalf("compaction before sizes = %#v, want %d / %d", result.Databases, beforeMain, beforeDiscovery)
	}
	if result.ReclaimedBytes <= 0 {
		t.Fatalf("compaction should reclaim deleted pages: %#v", result)
	}
	if result.Backup.Files["sec_monitor"] == "" || result.Backup.Files["small_cap"] == "" {
		t.Fatalf("compaction did not create backup pair: %#v", result.Backup)
	}
	latest, err := service.LatestCompaction(context.Background())
	if err != nil || latest.ID != result.RunID || latest.Status != "completed" || latest.MainAfterBytes >= latest.MainBeforeBytes || latest.DiscoveryAfterBytes >= latest.DiscoveryBeforeBytes {
		t.Fatalf("latest compaction = %#v, %v", latest, err)
	}
}

func seedCompactionPayload(t *testing.T, db *gorm.DB) {
	t.Helper()
	if err := db.Exec("CREATE TABLE compaction_payload (id INTEGER PRIMARY KEY, payload BLOB NOT NULL)").Error; err != nil {
		t.Fatal(err)
	}
	for range 4 {
		if err := db.Exec("INSERT INTO compaction_payload(payload) VALUES (zeroblob(?))", 2*1024*1024).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Exec("DELETE FROM compaction_payload WHERE id <= 3").Error; err != nil {
		t.Fatal(err)
	}
}

func openSQLiteBackupTestDB(t *testing.T, path string) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(path), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite test db: %v", err)
	}
	t.Cleanup(func() {
		sqlDB, err := db.DB()
		if err == nil {
			_ = sqlDB.Close()
		}
	})
	return db
}
