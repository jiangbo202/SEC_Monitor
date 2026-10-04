package handler

import (
	"context"
	"path/filepath"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"sec_monitor/internal/discovery"
)

func TestCandidateSummaryInvalidatesAfterORMAndRawMutations(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "research.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	if err := discovery.Migrate(db); err != nil {
		t.Fatal(err)
	}
	h := &AppHandler{DiscoveryDB: db, DB: db}
	if _, err := h.readCandidateSummary(context.Background()); err != nil {
		t.Fatal(err)
	}
	first := h.candidateSummary.at
	if _, err := h.readCandidateSummary(context.Background()); err != nil {
		t.Fatal(err)
	}
	if h.candidateSummary.at != first {
		t.Fatal("read did not reuse shared aggregate")
	}
	row := discovery.Security{CIK: "0000012345", CompanyName: "Test issuer", CatalogStatus: discovery.SecurityCatalogPublished}
	if err := db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	if h.candidateSummary.builtRevision == h.candidateSummary.revision.Load() {
		t.Fatal("committed ORM write did not invalidate cache")
	}
	if _, err := h.readCandidateSummary(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("UPDATE securities SET company_name = ? WHERE id = ?", "Updated issuer", row.ID).Error; err != nil {
		t.Fatal(err)
	}
	if h.candidateSummary.builtRevision == h.candidateSummary.revision.Load() {
		t.Fatal("raw mutation did not invalidate cache")
	}
}
