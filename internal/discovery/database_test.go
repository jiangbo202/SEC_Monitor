package discovery

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"sec_monitor/internal/config"

	"gorm.io/gorm"
)

func TestOpenDatabaseRejectsUnsupportedType(t *testing.T) {
	_, err := OpenDatabase(config.DatabaseConfig{Type: "postgres"})
	if err == nil || !strings.Contains(err.Error(), "unsupported discovery database type: postgres") {
		t.Fatalf("OpenDatabase error = %v", err)
	}
}

func TestOpenDatabaseEnablesForeignKeysForMemoryAndFileDSNs(t *testing.T) {
	tests := []struct {
		name string
		dsn  string
	}{
		{name: "memory", dsn: ":memory:"},
		{name: "file", dsn: filepath.Join(t.TempDir(), "discovery.db")},
		{name: "file with query", dsn: filepath.Join(t.TempDir(), "discovery-query.db") + "?_busy_timeout=5000"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db, err := OpenDatabase(config.DatabaseConfig{Type: "sqlite", DSN: test.dsn})
			if err != nil {
				t.Fatalf("OpenDatabase: %v", err)
			}
			var enabled int
			if err := db.Raw("PRAGMA foreign_keys").Scan(&enabled).Error; err != nil {
				t.Fatalf("read foreign_keys pragma: %v", err)
			}
			if enabled != 1 {
				t.Fatalf("PRAGMA foreign_keys = %d, want 1", enabled)
			}
		})
	}
}

func TestMigrateCreatesDiscoveryTables(t *testing.T) {
	db := openMigratedTestDatabase(t)

	tables := []struct {
		name  string
		model any
	}{
		{name: "securities", model: &Security{}},
		{name: "listings", model: &Listing{}},
		{name: "classification_snapshots", model: &ClassificationSnapshot{}},
		{name: "provider_runs", model: &ProviderRun{}},
		{name: "market_holidays", model: &MarketHoliday{}},
		{name: "price_snapshots", model: &PriceSnapshot{}},
		{name: "share_snapshots", model: &ShareSnapshot{}},
		{name: "universe_batches", model: &UniverseBatch{}},
		{name: "universe_snapshots", model: &UniverseSnapshot{}},
		{name: "manual_security_overrides", model: &ManualSecurityOverride{}},
	}

	for _, table := range tables {
		t.Run(table.name, func(t *testing.T) {
			if !db.Migrator().HasTable(table.model) {
				t.Fatalf("Migrate did not create %s", table.name)
			}
		})
	}
	for _, dtoTable := range []string{"evidences", "source_versions"} {
		if db.Migrator().HasTable(dtoTable) {
			t.Fatalf("Migrate created DTO table %s", dtoTable)
		}
	}
}

func TestMigrateEnforcesCompositeUniqueness(t *testing.T) {
	tests := []struct {
		name       string
		model      any
		index      string
		first      any
		variations []any
		duplicate  any
	}{
		{
			name:  "listing security ticker valid from",
			model: &Listing{},
			index: "idx_listing_security_ticker_from",
			first: &Listing{
				SecurityID: 1, Ticker: "ACME", ValidFrom: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
			},
			variations: []any{
				&Listing{SecurityID: 2, Ticker: "ACME", ValidFrom: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)},
				&Listing{SecurityID: 1, Ticker: "OTHER", ValidFrom: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)},
				&Listing{SecurityID: 1, Ticker: "ACME", ValidFrom: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)},
			},
			duplicate: &Listing{
				SecurityID: 1, Ticker: "ACME", ProviderTicker: "ACME.US", ValidFrom: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
			},
		},
		{
			name:  "classification batch security",
			model: &ClassificationSnapshot{},
			index: "idx_classification_batch_security",
			first: &ClassificationSnapshot{BatchID: "batch-1", SecurityID: 1},
			variations: []any{
				&ClassificationSnapshot{BatchID: "batch-2", SecurityID: 1},
				&ClassificationSnapshot{BatchID: "batch-1", SecurityID: 2},
			},
			duplicate: &ClassificationSnapshot{BatchID: "batch-1", SecurityID: 1, Included: true},
		},
		{
			name:  "price source version symbol trade date",
			model: &PriceSnapshot{},
			index: "idx_price_source_version_symbol_date",
			first: &PriceSnapshot{
				Source: "prices", SourceVersion: "v1", Symbol: "ACME", TradeDate: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC),
			},
			variations: []any{
				&PriceSnapshot{Source: "other", SourceVersion: "v1", Symbol: "ACME", TradeDate: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)},
				&PriceSnapshot{Source: "prices", SourceVersion: "v2", Symbol: "ACME", TradeDate: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)},
				&PriceSnapshot{Source: "prices", SourceVersion: "v1", Symbol: "OTHER", TradeDate: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)},
				&PriceSnapshot{Source: "prices", SourceVersion: "v1", Symbol: "ACME", TradeDate: time.Date(2026, 1, 3, 0, 0, 0, 0, time.UTC)},
			},
			duplicate: &PriceSnapshot{
				Source: "prices", SourceVersion: "v1", Symbol: "ACME", TradeDate: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC), Adjusted: true,
			},
		},
		{
			name:  "share security instant accession",
			model: &ShareSnapshot{},
			index: "idx_share_security_instant_accession",
			first: &ShareSnapshot{
				SecurityID: 1, Instant: time.Date(2025, 12, 31, 0, 0, 0, 0, time.UTC), Accession: "0001",
			},
			variations: []any{
				&ShareSnapshot{SecurityID: 2, Instant: time.Date(2025, 12, 31, 0, 0, 0, 0, time.UTC), Accession: "0001"},
				&ShareSnapshot{SecurityID: 1, Instant: time.Date(2026, 3, 31, 0, 0, 0, 0, time.UTC), Accession: "0001"},
				&ShareSnapshot{SecurityID: 1, Instant: time.Date(2025, 12, 31, 0, 0, 0, 0, time.UTC), Accession: "0002"},
			},
			duplicate: &ShareSnapshot{
				SecurityID: 1, Instant: time.Date(2025, 12, 31, 0, 0, 0, 0, time.UTC), Accession: "0001", Shares: 10,
			},
		},
		{
			name:  "universe batch security",
			model: &UniverseSnapshot{},
			index: "idx_universe_batch_security",
			first: &UniverseSnapshot{BatchID: "batch-1", SecurityID: 1},
			variations: []any{
				&UniverseSnapshot{BatchID: "batch-2", SecurityID: 1},
				&UniverseSnapshot{BatchID: "batch-1", SecurityID: 2},
			},
			duplicate: &UniverseSnapshot{BatchID: "batch-1", SecurityID: 1, Included: true},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := openMigratedTestDatabase(t)
			seedIntegrityFixtures(t, db)
			if !db.Migrator().HasIndex(test.model, test.index) {
				t.Fatalf("Migrate did not create unique index %s", test.index)
			}
			if err := db.Create(test.first).Error; err != nil {
				t.Fatalf("create first record: %v", err)
			}
			for i, variation := range test.variations {
				if err := db.Create(variation).Error; err != nil {
					t.Fatalf("create variation %d: %v", i, err)
				}
			}
			if err := db.Create(test.duplicate).Error; err == nil {
				t.Fatal("creating duplicate record succeeded, want unique constraint error")
			}
		})
	}
}

func TestMarketHolidayIdentityIncludesCalendarVersion(t *testing.T) {
	db := openMigratedTestDatabase(t)
	holiday := MarketHoliday{Date: "2026-01-01", CalendarVersion: "v1", Name: "New Year's Day"}
	if err := db.Create(&holiday).Error; err != nil {
		t.Fatalf("create first calendar version: %v", err)
	}
	if err := db.Create(&MarketHoliday{Date: holiday.Date, CalendarVersion: "v2", Name: holiday.Name}).Error; err != nil {
		t.Fatalf("create second calendar version: %v", err)
	}
	if err := db.Create(&MarketHoliday{Date: holiday.Date, CalendarVersion: holiday.CalendarVersion}).Error; err == nil {
		t.Fatal("create duplicate calendar version and date succeeded")
	}
}

func TestDiscoveryForeignKeysRejectOrphans(t *testing.T) {
	db := openMigratedTestDatabase(t)
	seedIntegrityFixtures(t, db)
	var foreignKeysEnabled int
	if err := db.Raw("PRAGMA foreign_keys").Scan(&foreignKeysEnabled).Error; err != nil {
		t.Fatalf("read foreign_keys pragma: %v", err)
	}
	if foreignKeysEnabled != 1 {
		t.Fatalf("PRAGMA foreign_keys = %d, want 1", foreignKeysEnabled)
	}

	missingID := uint(999)
	tests := []struct {
		name   string
		record any
	}{
		{name: "listing security", record: &Listing{SecurityID: missingID, Ticker: "ORPHAN", ValidFrom: time.Now()}},
		{name: "classification security", record: &ClassificationSnapshot{BatchID: "orphan", SecurityID: missingID}},
		{name: "share security", record: &ShareSnapshot{SecurityID: missingID, Instant: time.Now(), Accession: "orphan"}},
		{name: "universe batch", record: &UniverseSnapshot{BatchID: "missing", SecurityID: 1}},
		{name: "universe security", record: &UniverseSnapshot{BatchID: "batch-1", SecurityID: missingID}},
		{name: "universe price evidence", record: &UniverseSnapshot{BatchID: "batch-1", SecurityID: 1, PriceSnapshotID: &missingID}},
		{name: "universe share evidence", record: &UniverseSnapshot{BatchID: "batch-1", SecurityID: 1, ShareSnapshotID: &missingID}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := db.Create(test.record).Error; err == nil {
				t.Fatal("orphan insert succeeded")
			}
		})
	}
}

func TestDiscoveryForeignKeysRestrictReferencedDeletes(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*testing.T, *gorm.DB) any
	}{
		{
			name: "listing security",
			setup: func(t *testing.T, db *gorm.DB) any {
				mustCreate(t, db, &Listing{SecurityID: 1, Ticker: "ACME", ValidFrom: time.Now()})
				return &Security{ID: 1}
			},
		},
		{
			name: "classification security",
			setup: func(t *testing.T, db *gorm.DB) any {
				mustCreate(t, db, &ClassificationSnapshot{BatchID: "classification", SecurityID: 1})
				return &Security{ID: 1}
			},
		},
		{
			name: "share security",
			setup: func(t *testing.T, db *gorm.DB) any {
				mustCreate(t, db, &ShareSnapshot{SecurityID: 1, Instant: time.Now(), Accession: "security-ref"})
				return &Security{ID: 1}
			},
		},
		{
			name: "universe security",
			setup: func(t *testing.T, db *gorm.DB) any {
				mustCreate(t, db, &UniverseSnapshot{BatchID: "batch-1", SecurityID: 1})
				return &Security{ID: 1}
			},
		},
		{
			name: "universe batch",
			setup: func(t *testing.T, db *gorm.DB) any {
				mustCreate(t, db, &UniverseSnapshot{BatchID: "batch-1", SecurityID: 1})
				return &UniverseBatch{BatchID: "batch-1"}
			},
		},
		{
			name: "universe price evidence",
			setup: func(t *testing.T, db *gorm.DB) any {
				price := PriceSnapshot{Source: "prices", SourceVersion: "v1", Symbol: "ACME", TradeDate: time.Now()}
				mustCreate(t, db, &price)
				mustCreate(t, db, &UniverseSnapshot{BatchID: "batch-1", SecurityID: 1, PriceSnapshotID: &price.ID})
				return &price
			},
		},
		{
			name: "universe share evidence",
			setup: func(t *testing.T, db *gorm.DB) any {
				share := ShareSnapshot{SecurityID: 1, Instant: time.Now(), Accession: "evidence-ref"}
				mustCreate(t, db, &share)
				mustCreate(t, db, &UniverseSnapshot{BatchID: "batch-1", SecurityID: 1, ShareSnapshotID: &share.ID})
				return &share
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := openMigratedTestDatabase(t)
			seedIntegrityFixtures(t, db)
			referenced := test.setup(t, db)
			if err := db.Delete(referenced).Error; err == nil {
				t.Fatal("referenced delete succeeded")
			}
		})
	}
}

func TestUniverseSnapshotAllowsMissingOptionalEvidence(t *testing.T) {
	db := openMigratedTestDatabase(t)
	seedIntegrityFixtures(t, db)
	snapshot := UniverseSnapshot{BatchID: "batch-1", SecurityID: 1, PriceSnapshotID: nil, ShareSnapshotID: nil}
	if err := db.Create(&snapshot).Error; err != nil {
		t.Fatalf("create snapshot without optional evidence: %v", err)
	}
}

func TestListingUniquenessAllowsTickerHistory(t *testing.T) {
	db := openMigratedTestDatabase(t)
	mustCreate(t, db, &Security{ID: 1, CIK: "0000000001"})
	listings := []Listing{
		{SecurityID: 1, Ticker: "ACME", ValidFrom: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)},
		{SecurityID: 1, Ticker: "ACME", ValidFrom: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)},
	}
	if err := db.Create(&listings).Error; err != nil {
		t.Fatalf("create ticker history: %v", err)
	}
}

func TestScopedDraftAndFailedBatchWritesLeaveOtherBatchUnchanged(t *testing.T) {
	db := openMigratedTestDatabase(t)
	startedAt := time.Date(2026, 6, 22, 1, 0, 0, 0, time.UTC)
	publishedCompletedAt := startedAt.Add(time.Minute)
	batches := []UniverseBatch{
		{BatchID: "published", Status: BatchStatusPublished, StartedAt: startedAt, CompletedAt: &publishedCompletedAt},
		{BatchID: "draft", Status: BatchStatusDraft, StartedAt: startedAt},
		{BatchID: "failed", Status: BatchStatusFailed, StartedAt: startedAt, ErrorMessage: "initial error"},
	}
	if err := db.Create(&batches).Error; err != nil {
		t.Fatalf("create batches: %v", err)
	}

	if err := db.Where("batch_id = ? AND status = ?", "draft", BatchStatusDraft).Delete(&UniverseBatch{}).Error; err != nil {
		t.Fatalf("delete draft batch: %v", err)
	}
	if err := db.Model(&UniverseBatch{}).
		Where("batch_id = ? AND status = ?", "failed", BatchStatusFailed).
		Update("error_message", "retry exhausted").Error; err != nil {
		t.Fatalf("update failed batch: %v", err)
	}

	var published UniverseBatch
	if err := db.First(&published, "batch_id = ?", "published").Error; err != nil {
		t.Fatalf("read published batch: %v", err)
	}
	if published.Status != BatchStatusPublished || published.ErrorMessage != "" {
		t.Fatalf("published batch changed: %+v", published)
	}
	if published.CompletedAt == nil || !published.CompletedAt.Equal(publishedCompletedAt) {
		t.Fatalf("published completed_at = %v, want %v", published.CompletedAt, publishedCompletedAt)
	}
	if err := db.First(&UniverseBatch{}, "batch_id = ?", "draft").Error; !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("draft batch lookup error = %v, want record not found", err)
	}
}

func seedIntegrityFixtures(t *testing.T, db *gorm.DB) {
	t.Helper()
	mustCreate(t, db, &[]Security{{ID: 1, CIK: "0000000001"}, {ID: 2, CIK: "0000000002"}})
	mustCreate(t, db, &[]UniverseBatch{{BatchID: "batch-1"}, {BatchID: "batch-2"}})
}

func mustCreate(t *testing.T, db *gorm.DB, value any) {
	t.Helper()
	if err := db.Create(value).Error; err != nil {
		t.Fatalf("create fixture: %v", err)
	}
}

func openMigratedTestDatabase(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := OpenDatabase(config.DatabaseConfig{Type: "sqlite", DSN: ":memory:"})
	if err != nil {
		t.Fatalf("OpenDatabase: %v", err)
	}
	if err := Migrate(db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return db
}
