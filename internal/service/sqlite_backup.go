package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"sec_monitor/internal/model"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// SQLiteBackupService creates a consistent SQLite snapshot through VACUUM
// INTO. A filesystem copy is not safe while WAL is active, whereas VACUUM
// INTO produces a standalone, checkpointed snapshot.
type SQLiteBackupService struct {
	mainDB, discoveryDB   *gorm.DB
	mainDSN, discoveryDSN string
	configs               *ConfigService
	mu                    sync.Mutex
}

type SQLiteBackupResult struct {
	StartedAt   time.Time         `json:"started_at"`
	CompletedAt time.Time         `json:"completed_at"`
	Directory   string            `json:"directory"`
	Files       map[string]string `json:"files"`
	Deleted     int               `json:"deleted"`
}

// SQLiteCompactionDatabaseResult describes the physical size change for one
// live SQLite database after VACUUM. It never reports a negative reclaim when
// SQLite temporarily needs to grow a file to rewrite it.
type SQLiteCompactionDatabaseResult struct {
	Name           string `json:"name"`
	Path           string `json:"path"`
	BeforeBytes    int64  `json:"before_bytes"`
	AfterBytes     int64  `json:"after_bytes"`
	ReclaimedBytes int64  `json:"reclaimed_bytes"`
}

// SQLiteCompactionResult is recorded in the operational database and returned
// to the user after a manual low-traffic compaction. A fresh paired backup is
// always created before the live databases are rewritten.
type SQLiteCompactionResult struct {
	RunID          uint                             `json:"run_id"`
	Status         string                           `json:"status"`
	StartedAt      time.Time                        `json:"started_at"`
	CompletedAt    time.Time                        `json:"completed_at"`
	DurationMS     int64                            `json:"duration_ms"`
	Backup         SQLiteBackupResult               `json:"backup"`
	Databases      []SQLiteCompactionDatabaseResult `json:"databases"`
	ReclaimedBytes int64                            `json:"reclaimed_bytes"`
	ErrorMessage   string                           `json:"error_message,omitempty"`
}

// SQLiteBackupHealth reports the most recent complete pair of snapshots. A
// pair is required because SEC Monitor has separate operational and research
// databases; a single file is not a recoverable application checkpoint.
type SQLiteBackupHealth struct {
	Directory       string     `json:"directory"`
	CompletePairs   int        `json:"complete_pairs"`
	IncompletePairs int        `json:"incomplete_pairs"`
	TotalBytes      int64      `json:"total_bytes"`
	LatestPairBytes int64      `json:"latest_pair_bytes"`
	LatestCompleted *time.Time `json:"latest_completed,omitempty"`
}

// SQLiteBackupVerification is a read-only integrity check for the most
// recent complete snapshot pair. It never touches the live databases.
type SQLiteBackupVerification struct {
	Directory  string            `json:"directory"`
	Files      map[string]string `json:"files"`
	VerifiedAt time.Time         `json:"verified_at"`
}

// SQLiteRecoveryReadiness is a non-destructive recovery drill. It confirms a
// latest *pair* exists and can be opened independently of the live databases.
// It does not copy, replace, or restart either application database.
type SQLiteRecoveryReadiness struct {
	Status       string                    `json:"status"`
	CheckedAt    time.Time                 `json:"checked_at"`
	Backup       SQLiteBackupHealth        `json:"backup"`
	Verification *SQLiteBackupVerification `json:"verification,omitempty"`
	Reason       string                    `json:"reason,omitempty"`
}

func NewSQLiteBackupService(mainDB, discoveryDB *gorm.DB, mainDSN, discoveryDSN string, configs *ConfigService) *SQLiteBackupService {
	return &SQLiteBackupService{mainDB: mainDB, discoveryDB: discoveryDB, mainDSN: mainDSN, discoveryDSN: discoveryDSN, configs: configs}
}

func (s *SQLiteBackupService) Backup(ctx context.Context) (SQLiteBackupResult, error) {
	if s == nil || s.mainDB == nil || s.discoveryDB == nil {
		return SQLiteBackupResult{}, errors.New("SQLite backup service is not configured")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.backupLocked(ctx)
}

func (s *SQLiteBackupService) backupLocked(ctx context.Context) (SQLiteBackupResult, error) {
	result := SQLiteBackupResult{StartedAt: time.Now().UTC(), Files: map[string]string{}}
	dir, retention, err := s.settings(ctx)
	if err != nil {
		return result, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return result, fmt.Errorf("create backup directory: %w", err)
	}
	result.Directory = dir
	stamp := result.StartedAt.Format("20060102T150405Z")
	items := []struct {
		name string
		db   *gorm.DB
	}{{"sec_monitor", s.mainDB}, {"small_cap", s.discoveryDB}}
	staged := map[string]string{}
	published := map[string]string{}
	cleanup := func() {
		for _, path := range staged {
			_ = os.Remove(path)
		}
		for _, path := range published {
			_ = os.Remove(path)
		}
	}
	for _, item := range items {
		path := filepath.Join(dir, item.name+"-"+stamp+".db")
		if _, err := os.Stat(path); err == nil {
			return result, fmt.Errorf("backup snapshot already exists: %s", filepath.Base(path))
		} else if !errors.Is(err, os.ErrNotExist) {
			return result, fmt.Errorf("inspect backup snapshot path: %w", err)
		}
		stagedPath := path + ".partial"
		if err := os.Remove(stagedPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return result, fmt.Errorf("remove stale partial backup: %w", err)
		}
		staged[item.name] = stagedPath
		if err := vacuumInto(ctx, item.db, stagedPath); err != nil {
			cleanup()
			return result, fmt.Errorf("backup %s: %w", item.name, err)
		}
		if err := verifySQLiteBackup(stagedPath); err != nil {
			cleanup()
			return result, fmt.Errorf("verify %s: %w", item.name, err)
		}
	}
	// Publish only after both database snapshots have passed integrity checks.
	// A later failure therefore cannot leave a half-published restore point.
	for _, item := range items {
		path := strings.TrimSuffix(staged[item.name], ".partial")
		if err := os.Rename(staged[item.name], path); err != nil {
			cleanup()
			return result, fmt.Errorf("publish %s backup: %w", item.name, err)
		}
		published[item.name] = path
		result.Files[item.name] = path
	}
	deleted, err := pruneSQLiteBackups(dir, retention, result.StartedAt)
	if err != nil {
		return result, err
	}
	result.Deleted, result.CompletedAt = deleted, time.Now().UTC()
	return result, nil
}

// Compact creates a fresh, verified paired backup and then runs SQLite VACUUM
// on both live databases. It is deliberately manual: VACUUM requires a write
// lock and should be performed during a low-traffic window. Concurrent backup
// or compaction operations are rejected rather than queued behind a long run.
func (s *SQLiteBackupService) Compact(ctx context.Context) (SQLiteCompactionResult, error) {
	result := SQLiteCompactionResult{Status: "failed", StartedAt: time.Now().UTC(), Databases: []SQLiteCompactionDatabaseResult{}}
	if s == nil || s.mainDB == nil || s.discoveryDB == nil {
		return result, errors.New("SQLite backup service is not configured")
	}
	if !s.mu.TryLock() {
		return result, TaskAlreadyRunning("sqlite_compaction")
	}
	defer s.mu.Unlock()
	run := model.SQLiteCompactionRun{Status: "running", StartedAt: result.StartedAt}
	if err := s.mainDB.WithContext(ctx).Create(&run).Error; err != nil {
		return result, fmt.Errorf("record SQLite compaction start: %w", err)
	}
	result.RunID = run.ID
	finish := func(status string, runErr error) {
		result.Status = status
		result.CompletedAt = time.Now().UTC()
		result.DurationMS = result.CompletedAt.Sub(result.StartedAt).Milliseconds()
		if runErr != nil {
			result.ErrorMessage = SanitizeSensitiveError(runErr.Error())
		}
		values := map[string]any{
			"status": status, "completed_at": &result.CompletedAt, "duration_ms": result.DurationMS,
			"error_message": result.ErrorMessage,
		}
		for _, item := range result.Databases {
			switch item.Name {
			case "sec_monitor":
				values["main_before_bytes"], values["main_after_bytes"] = item.BeforeBytes, item.AfterBytes
			case "small_cap":
				values["discovery_before_bytes"], values["discovery_after_bytes"] = item.BeforeBytes, item.AfterBytes
			}
		}
		_ = s.mainDB.WithContext(context.Background()).Model(&model.SQLiteCompactionRun{}).Where("id = ?", run.ID).Updates(values).Error
	}
	backup, err := s.backupLocked(ctx)
	result.Backup = backup
	if err != nil {
		finish("failed", err)
		return result, fmt.Errorf("create verified backup before compaction: %w", err)
	}
	compactCtx, cancel := context.WithTimeout(ctx, 20*time.Minute)
	defer cancel()
	for _, item := range []struct {
		name string
		dsn  string
		db   *gorm.DB
	}{{"sec_monitor", s.mainDSN, s.mainDB}, {"small_cap", s.discoveryDSN, s.discoveryDB}} {
		compacted, compactErr := compactSQLiteDatabase(compactCtx, item.name, item.dsn, item.db)
		result.Databases = append(result.Databases, compacted)
		result.ReclaimedBytes += compacted.ReclaimedBytes
		if compactErr != nil {
			finish("partial", compactErr)
			return result, fmt.Errorf("compact %s database: %w", item.name, compactErr)
		}
	}
	finish("completed", nil)
	return result, nil
}

func (s *SQLiteBackupService) LatestCompaction(ctx context.Context) (model.SQLiteCompactionRun, error) {
	if s == nil || s.mainDB == nil {
		return model.SQLiteCompactionRun{}, errors.New("SQLite backup service is not configured")
	}
	var run model.SQLiteCompactionRun
	err := s.mainDB.WithContext(ctx).Order("started_at DESC, id DESC").First(&run).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return model.SQLiteCompactionRun{}, nil
	}
	return run, err
}

func (s *SQLiteBackupService) Health(ctx context.Context) (SQLiteBackupHealth, error) {
	if s == nil {
		return SQLiteBackupHealth{}, errors.New("SQLite backup service is not configured")
	}
	dir, _, err := s.settings(ctx)
	if err != nil {
		return SQLiteBackupHealth{}, err
	}
	health := SQLiteBackupHealth{Directory: dir}
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return health, nil
	}
	if err != nil {
		return health, err
	}
	type backupFile struct {
		modifiedAt time.Time
		bytes      int64
	}
	pairs := map[string]map[string]backupFile{}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name, stamp, ok := parseSQLiteBackupName(entry.Name())
		if !ok {
			continue
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			return health, infoErr
		}
		if pairs[stamp] == nil {
			pairs[stamp] = map[string]backupFile{}
		}
		pairs[stamp][name] = backupFile{modifiedAt: info.ModTime().UTC(), bytes: info.Size()}
	}
	for _, pair := range pairs {
		mainAt, hasMain := pair["sec_monitor"]
		discoveryAt, hasDiscovery := pair["small_cap"]
		if !hasMain || !hasDiscovery {
			health.IncompletePairs++
			continue
		}
		health.CompletePairs++
		pairBytes := mainAt.bytes + discoveryAt.bytes
		health.TotalBytes += pairBytes
		completed := mainAt.modifiedAt
		if discoveryAt.modifiedAt.Before(completed) {
			completed = discoveryAt.modifiedAt
		}
		if health.LatestCompleted == nil || completed.After(*health.LatestCompleted) {
			value := completed
			health.LatestCompleted = &value
			health.LatestPairBytes = pairBytes
		}
	}
	return health, nil
}

func (s *SQLiteBackupService) VerifyLatest(ctx context.Context) (SQLiteBackupVerification, error) {
	if s == nil {
		return SQLiteBackupVerification{}, errors.New("SQLite backup service is not configured")
	}
	dir, _, err := s.settings(ctx)
	if err != nil {
		return SQLiteBackupVerification{}, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return SQLiteBackupVerification{}, errors.New("no complete SQLite backup is available")
		}
		return SQLiteBackupVerification{}, err
	}
	pairs := map[string]map[string]string{}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name, stamp, ok := parseSQLiteBackupName(entry.Name())
		if !ok {
			continue
		}
		if pairs[stamp] == nil {
			pairs[stamp] = map[string]string{}
		}
		pairs[stamp][name] = filepath.Join(dir, entry.Name())
	}
	latestStamp := ""
	for stamp, pair := range pairs {
		if pair["sec_monitor"] != "" && pair["small_cap"] != "" && stamp > latestStamp {
			latestStamp = stamp
		}
	}
	if latestStamp == "" {
		return SQLiteBackupVerification{}, errors.New("no complete SQLite backup is available")
	}
	files := pairs[latestStamp]
	for _, name := range []string{"sec_monitor", "small_cap"} {
		if err := verifySQLiteBackup(files[name]); err != nil {
			return SQLiteBackupVerification{}, fmt.Errorf("verify %s snapshot: %w", name, err)
		}
	}
	return SQLiteBackupVerification{Directory: dir, Files: files, VerifiedAt: time.Now().UTC()}, nil
}

func (s *SQLiteBackupService) CheckRecoveryReadiness(ctx context.Context) (SQLiteRecoveryReadiness, error) {
	checkedAt := time.Now().UTC()
	result := SQLiteRecoveryReadiness{Status: "unavailable", CheckedAt: checkedAt}
	if s == nil || s.mainDB == nil {
		return result, errors.New("SQLite backup service is not configured")
	}
	defer func() { _ = s.recordRecoveryDrill(ctx, result, checkedAt) }()
	health, err := s.Health(ctx)
	if err != nil {
		result.Status, result.Reason = "failed", SanitizeSensitiveError(err.Error())
		return result, nil
	}
	result.Backup = health
	if health.LatestCompleted == nil {
		result.Reason = "no complete SQLite backup is available"
		return result, nil
	}
	verification, err := s.VerifyLatest(ctx)
	if err != nil {
		result.Status, result.Reason = "failed", SanitizeSensitiveError(err.Error())
		return result, nil
	}
	if err := rehearseSQLiteRestorePair(verification.Files); err != nil {
		result.Status, result.Reason = "failed", SanitizeSensitiveError(err.Error())
		return result, nil
	}
	result.Status = "ready"
	result.Verification = &verification
	return result, nil
}

func (s *SQLiteBackupService) LatestRecoveryDrill(ctx context.Context) (model.RecoveryDrill, error) {
	if s == nil || s.mainDB == nil {
		return model.RecoveryDrill{}, errors.New("SQLite backup service is not configured")
	}
	var drill model.RecoveryDrill
	err := s.mainDB.WithContext(ctx).Order("started_at DESC, id DESC").First(&drill).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return model.RecoveryDrill{}, nil
	}
	return drill, err
}

func (s *SQLiteBackupService) recordRecoveryDrill(ctx context.Context, result SQLiteRecoveryReadiness, startedAt time.Time) error {
	completedAt := time.Now().UTC()
	drill := model.RecoveryDrill{
		Status: result.Status, StartedAt: startedAt, CompletedAt: &completedAt,
		DurationMS: completedAt.Sub(startedAt).Milliseconds(), ErrorMessage: result.Reason,
	}
	if result.Backup.LatestCompleted != nil {
		value := *result.Backup.LatestCompleted
		drill.BackupTimestamp = &value
	}
	return s.mainDB.WithContext(context.Background()).Create(&drill).Error
}

func (s *SQLiteBackupService) settings(ctx context.Context) (string, int, error) {
	dir := filepath.Join(filepath.Dir(sqlitePath(s.mainDSN)), "backups")
	retention := 7
	if s.configs == nil {
		return dir, retention, nil
	}
	if value, ok, err := s.configs.GetValue(ctx, "system.backup_dir"); err != nil {
		return "", 0, err
	} else if ok && strings.TrimSpace(value) != "" {
		dir = strings.TrimSpace(value)
	}
	if value, ok, err := s.configs.GetValue(ctx, "system.backup_retention_days"); err != nil {
		return "", 0, err
	} else if ok {
		if parsed, parseErr := strconv.Atoi(strings.TrimSpace(value)); parseErr == nil && parsed > 0 {
			retention = parsed
		}
	}
	return dir, retention, nil
}

func sqlitePath(dsn string) string {
	if i := strings.Index(dsn, "?"); i >= 0 {
		return dsn[:i]
	}
	return dsn
}
func quoteSQLiteString(value string) string { return "'" + strings.ReplaceAll(value, "'", "''") + "'" }
func vacuumInto(ctx context.Context, db *gorm.DB, path string) error {
	return db.WithContext(ctx).Exec("VACUUM INTO " + quoteSQLiteString(path)).Error
}

func compactSQLiteDatabase(ctx context.Context, name, dsn string, db *gorm.DB) (SQLiteCompactionDatabaseResult, error) {
	result := SQLiteCompactionDatabaseResult{Name: name, Path: sqlitePath(dsn)}
	before, err := sqliteFileSize(result.Path)
	if err != nil {
		return result, err
	}
	result.BeforeBytes = before
	if err := db.WithContext(ctx).Exec("VACUUM").Error; err != nil {
		return result, err
	}
	after, err := sqliteFileSize(result.Path)
	if err != nil {
		return result, err
	}
	result.AfterBytes = after
	if before > after {
		result.ReclaimedBytes = before - after
	}
	return result, nil
}

func sqliteFileSize(path string) (int64, error) {
	path = strings.TrimSpace(path)
	if path == "" || path == ":memory:" {
		return 0, errors.New("SQLite compaction requires a file-backed database")
	}
	info, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	return info.Size(), nil
}
func verifySQLiteBackup(path string) error {
	db, err := gorm.Open(sqlite.Open(path+"?mode=ro"), &gorm.Config{})
	if err != nil {
		return err
	}
	sqlDB, _ := db.DB()
	if sqlDB != nil {
		defer sqlDB.Close()
	}
	var check string
	if err := db.Raw("PRAGMA integrity_check").Scan(&check).Error; err != nil {
		return err
	}
	if strings.ToLower(strings.TrimSpace(check)) != "ok" {
		return fmt.Errorf("integrity_check=%s", check)
	}
	return nil
}

// rehearseSQLiteRestorePair copies the snapshots into an isolated temporary
// directory and validates that a restored application would find its minimum
// operational and research schema. It never changes a live database or the
// published backup files.
func rehearseSQLiteRestorePair(files map[string]string) error {
	dir, err := os.MkdirTemp("", "sec-monitor-recovery-")
	if err != nil {
		return fmt.Errorf("create recovery rehearsal directory: %w", err)
	}
	defer os.RemoveAll(dir)
	for _, name := range []string{"sec_monitor", "small_cap"} {
		source := strings.TrimSpace(files[name])
		if source == "" {
			return fmt.Errorf("recovery rehearsal missing %s snapshot", name)
		}
		destination := filepath.Join(dir, name+".db")
		if err := copyFile(source, destination); err != nil {
			return fmt.Errorf("stage %s recovery snapshot: %w", name, err)
		}
		if err := verifySQLiteBackup(destination); err != nil {
			return fmt.Errorf("verify staged %s recovery snapshot: %w", name, err)
		}
	}
	if err := verifySQLiteSchema(filepath.Join(dir, "sec_monitor.db"), []string{"system_configs", "task_configs", "watch_targets", "filings"}); err != nil {
		return fmt.Errorf("validate operational recovery schema: %w", err)
	}
	if err := verifySQLiteSchema(filepath.Join(dir, "small_cap.db"), []string{"universe_batches", "securities", "candidate_score_snapshots"}); err != nil {
		return fmt.Errorf("validate research recovery schema: %w", err)
	}
	return nil
}

func copyFile(source, destination string) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

func verifySQLiteSchema(path string, requiredTables []string) error {
	db, err := gorm.Open(sqlite.Open(path+"?mode=ro"), &gorm.Config{})
	if err != nil {
		return err
	}
	sqlDB, _ := db.DB()
	if sqlDB != nil {
		defer sqlDB.Close()
	}
	for _, table := range requiredTables {
		var count int64
		if err := db.Raw("SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?", table).Scan(&count).Error; err != nil {
			return err
		}
		if count != 1 {
			return fmt.Errorf("missing required table %s", table)
		}
	}
	return nil
}

func pruneSQLiteBackups(dir string, retentionDays int, now time.Time) (int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, err
	}
	cutoff := now.AddDate(0, 0, -retentionDays)
	deleted := 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".db") {
			continue
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			return deleted, infoErr
		}
		if info.ModTime().Before(cutoff) {
			if err := os.Remove(filepath.Join(dir, entry.Name())); err != nil {
				return deleted, err
			}
			deleted++
		}
	}
	return deleted, nil
}

func parseSQLiteBackupName(filename string) (name, stamp string, ok bool) {
	for _, prefix := range []string{"sec_monitor-", "small_cap-"} {
		if !strings.HasPrefix(filename, prefix) || !strings.HasSuffix(filename, ".db") {
			continue
		}
		stamp = strings.TrimSuffix(strings.TrimPrefix(filename, prefix), ".db")
		if _, err := time.Parse("20060102T150405Z", stamp); err != nil {
			return "", "", false
		}
		return strings.TrimSuffix(prefix, "-"), stamp, true
	}
	return "", "", false
}
