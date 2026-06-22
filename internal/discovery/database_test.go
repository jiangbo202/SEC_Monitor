package discovery

import (
	"errors"
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
		name      string
		model     any
		index     string
		first     any
		duplicate any
	}{
		{
			name:  "listing security ticker valid from",
			model: &Listing{},
			index: "idx_listing_security_ticker_from",
			first: &Listing{
				SecurityID: 1, Ticker: "ACME", ValidFrom: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
			},
			duplicate: &Listing{
				SecurityID: 1, Ticker: "ACME", ProviderTicker: "ACME.US", ValidFrom: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
			},
		},
		{
			name:      "classification batch security",
			model:     &ClassificationSnapshot{},
			index:     "idx_classification_batch_security",
			first:     &ClassificationSnapshot{BatchID: "batch-1", SecurityID: 1},
			duplicate: &ClassificationSnapshot{BatchID: "batch-1", SecurityID: 1, Included: true},
		},
		{
			name:  "price source version symbol trade date",
			model: &PriceSnapshot{},
			index: "idx_price_source_version_symbol_date",
			first: &PriceSnapshot{
				Source: "prices", SourceVersion: "v1", Symbol: "ACME", TradeDate: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC),
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
			duplicate: &ShareSnapshot{
				SecurityID: 1, Instant: time.Date(2025, 12, 31, 0, 0, 0, 0, time.UTC), Accession: "0001", Shares: 10,
			},
		},
		{
			name:      "universe batch security",
			model:     &UniverseSnapshot{},
			index:     "idx_universe_batch_security",
			first:     &UniverseSnapshot{BatchID: "batch-1", SecurityID: 1},
			duplicate: &UniverseSnapshot{BatchID: "batch-1", SecurityID: 1, Included: true},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := openMigratedTestDatabase(t)
			if !db.Migrator().HasIndex(test.model, test.index) {
				t.Fatalf("Migrate did not create unique index %s", test.index)
			}
			if err := db.Create(test.first).Error; err != nil {
				t.Fatalf("create first record: %v", err)
			}
			if err := db.Create(test.duplicate).Error; err == nil {
				t.Fatal("creating duplicate record succeeded, want unique constraint error")
			}
		})
	}
}

func TestListingUniquenessAllowsTickerHistory(t *testing.T) {
	db := openMigratedTestDatabase(t)
	listings := []Listing{
		{SecurityID: 1, Ticker: "ACME", ValidFrom: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)},
		{SecurityID: 1, Ticker: "ACME", ValidFrom: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)},
	}
	if err := db.Create(&listings).Error; err != nil {
		t.Fatalf("create ticker history: %v", err)
	}
}

func TestDraftAndFailedBatchOperationsDoNotChangePublishedBatch(t *testing.T) {
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
