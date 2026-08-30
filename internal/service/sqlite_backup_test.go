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

func TestSQLiteBackupReplicaDirectoryFallsBackToDeploymentEnvironment(t *testing.T) {
	t.Setenv("SEC_MONITOR_BACKUP_REPLICA_DIR", "/app/backup-replica")
	service := &SQLiteBackupService{}
	directory, err := service.replicaDirectory(context.Background(), "/app/data/backups")
	if err != nil || directory != "/app/backup-replica" {
		t.Fatalf("replica directory = %q, %v", directory, err)
	}
	t.Setenv("SEC_MONITOR_BACKUP_REPLICA_DIR", "/app/data/backups")
	if _, err := service.replicaDirectory(context.Background(), "/app/data/backups"); err == nil {
		t.Fatal("same local and replica directory should be rejected")
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

func TestRecoveryDrillIndependentlyChecksReplicaAndDetectsCorruption(t *testing.T) {
	dir := t.TempDir()
	mainPath, researchPath := filepath.Join(dir, "main.db"), filepath.Join(dir, "research.db")
	mainDB, researchDB := openSQLiteBackupTestDB(t, mainPath), openSQLiteBackupTestDB(t, researchPath)
	if err := mainDB.AutoMigrate(&model.SystemConfig{}, &model.RecoveryDrill{}, &model.TaskConfig{}, &model.WatchTarget{}, &model.Filing{}); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"universe_batches", "securities", "candidate_score_snapshots"} {
		if err := researchDB.Exec("CREATE TABLE " + table + " (id INTEGER PRIMARY KEY)").Error; err != nil {
			t.Fatal(err)
		}
	}
	replicaDir := filepath.Join(dir, "replica")
	t.Setenv("SEC_MONITOR_BACKUP_REPLICA_DIR", replicaDir)
	svc := NewSQLiteBackupService(mainDB, researchDB, mainPath, researchPath, nil)
	backup, err := svc.Backup(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	result, err := svc.CheckRecoveryReadiness(context.Background())
	if err != nil || result.Status != "ready" || result.LocalStatus != "ready" || result.ReplicaStatus != "ready" || result.ReplicaVerification == nil {
		t.Fatalf("drill: %+v %v", result, err)
	}
	for name, hash := range result.Verification.SHA256 {
		if len(hash) != 64 || result.ReplicaVerification.SHA256[name] != hash {
			t.Fatal("replica checksum mismatch")
		}
	}
	// These are disposable test snapshots, never production backups.
	if err := os.WriteFile(backup.ReplicaFiles["small_cap"], []byte("corrupt snapshot"), 0600); err != nil {
		t.Fatal(err)
	}
	result, err = svc.CheckRecoveryReadiness(context.Background())
	if err != nil || result.Status != "failed" || result.LocalStatus != "ready" || result.ReplicaStatus != "failed" || result.ReplicaReason == "" {
		t.Fatalf("corrupt replica reported healthy: %+v %v", result, err)
	}
	var drill model.RecoveryDrill
	if err := mainDB.Order("id DESC").First(&drill).Error; err != nil {
		t.Fatal(err)
	}
	if drill.LocalStatus != "ready" || drill.ReplicaStatus != "failed" {
		t.Fatalf("drill statuses not persisted: %+v", drill)
	}
	if err := os.Remove(backup.Files["sec_monitor"]); err != nil {
		t.Fatal(err)
	}
	if err := copyFile(backup.Files["small_cap"], filepath.Join(dir, "copy.db")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(backup.ReplicaFiles["small_cap"], mustReadBackupTestFile(t, backup.Files["small_cap"]), 0600); err != nil {
		t.Fatal(err)
	}
	result, err = svc.CheckRecoveryReadiness(context.Background())
	if err != nil || result.LocalStatus == "ready" || result.ReplicaStatus != "ready" {
		t.Fatalf("local loss prevented independent replica drill: %+v %v", result, err)
	}
}

func mustReadBackupTestFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestRestoreRehearsalRejectsChecksumMismatch(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "source.db")
	openSQLiteBackupTestDB(t, source)
	err := rehearseVerifiedSQLitePair(SQLiteBackupVerification{Files: map[string]string{"sec_monitor": source}, SHA256: map[string]string{"sec_monitor": "wrong"}})
	if err == nil {
		t.Fatal("checksum mismatch accepted")
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

func TestSQLiteBackupServiceReplicatesVerifiedPairToExternalDirectory(t *testing.T) {
	dir := t.TempDir()
	mainPath := filepath.Join(dir, "sec_monitor.db")
	discoveryPath := filepath.Join(dir, "small_cap.db")
	mainDB := openSQLiteBackupTestDB(t, mainPath)
	discoveryDB := openSQLiteBackupTestDB(t, discoveryPath)
	if err := mainDB.AutoMigrate(&model.SystemConfig{}, &model.RecoveryDrill{}); err != nil {
		t.Fatal(err)
	}
	if err := mainDB.Exec("CREATE TABLE local_payload (id INTEGER PRIMARY KEY)").Error; err != nil {
		t.Fatal(err)
	}
	if err := discoveryDB.Exec("CREATE TABLE research_payload (id INTEGER PRIMARY KEY)").Error; err != nil {
		t.Fatal(err)
	}
	replicaDir := filepath.Join(dir, "external-volume")
	if err := mainDB.Create(&model.SystemConfig{ConfigKey: "system.backup_replica_dir", ConfigValue: replicaDir, ValueType: "string", Category: "system"}).Error; err != nil {
		t.Fatal(err)
	}
	configs := NewConfigService(mainDB, nil)
	service := NewSQLiteBackupService(mainDB, discoveryDB, mainPath, discoveryPath, configs)
	result, err := service.Backup(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.ReplicaDirectory != replicaDir || len(result.ReplicaFiles) != 2 {
		t.Fatalf("replica result = %#v", result)
	}
	for _, name := range []string{"sec_monitor", "small_cap"} {
		if err := verifySQLiteBackup(result.ReplicaFiles[name]); err != nil {
			t.Fatalf("verify replica %s: %v", name, err)
		}
	}
	health, err := service.Health(context.Background())
	if err != nil || !health.Replica.Enabled || health.Replica.Status != "ready" || health.Replica.CompletePairs != 1 {
		t.Fatalf("replica health = %#v, %v", health.Replica, err)
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
