package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"sec_monitor/internal/discovery"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestCandidateNotificationPreviewUsesSettingsWithoutSending(t *testing.T) {
	db := testDB(t)
	discoveryDB := testDiscoveryDB(t)
	configs := NewConfigService(db, NewAuditService(db))
	if err := configs.EnsureDefaults(context.Background()); err != nil {
		t.Fatalf("EnsureDefaults: %v", err)
	}
	if err := configs.UpsertMany(context.Background(), []ConfigInput{
		{Key: "candidate_notification.enabled", Value: "true", ValueType: "bool", Category: "candidate_notification"},
		{Key: "candidate_notification.notify_a", Value: "true", ValueType: "bool", Category: "candidate_notification"},
		{Key: "candidate_notification.notify_b", Value: "false", ValueType: "bool", Category: "candidate_notification"},
		{Key: "candidate_notification.max_per_grade", Value: "1", ValueType: "int", Category: "candidate_notification"},
	}, "test"); err != nil {
		t.Fatalf("UpsertMany: %v", err)
	}
	seedCandidateScores(t, discoveryDB)

	result, err := NewCandidateNotificationService(discoveryDB, configs).Preview(context.Background())
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	if !result.Enabled || result.SuppressedReason != "" || !result.Settings.NotifyA || result.Settings.NotifyB {
		t.Fatalf("result = %+v", result)
	}
	if result.Summary.TotalA != 2 || len(result.Summary.ItemsA) != 1 || result.Summary.ItemsA[0].Ticker != "AAA" {
		t.Fatalf("summary A = %#v", result.Summary)
	}
	if result.Summary.TotalB != 0 || len(result.Summary.ItemsB) != 0 || strings.Contains(result.Summary.Message, "BBB") {
		t.Fatalf("summary B = %#v", result.Summary)
	}
}

func TestCandidateNotificationPreviewSuppressesWhenDisabled(t *testing.T) {
	db := testDB(t)
	discoveryDB := testDiscoveryDB(t)
	configs := NewConfigService(db, NewAuditService(db))
	if err := configs.EnsureDefaults(context.Background()); err != nil {
		t.Fatalf("EnsureDefaults: %v", err)
	}
	seedCandidateScores(t, discoveryDB)

	result, err := NewCandidateNotificationService(discoveryDB, configs).Preview(context.Background())
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	if result.Enabled || result.SuppressedReason != "candidate_notification_disabled" || result.Summary.Message == "" {
		t.Fatalf("result = %+v", result)
	}
	if len(result.Summary.ItemsA) != 0 || len(result.Summary.ItemsB) != 0 {
		t.Fatalf("summary should be empty when disabled: %#v", result.Summary)
	}
}

func testDiscoveryDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open discovery db: %v", err)
	}
	if err := discovery.Migrate(db); err != nil {
		t.Fatalf("migrate discovery db: %v", err)
	}
	return db
}

func seedCandidateScores(t *testing.T, db *gorm.DB) {
	t.Helper()
	securities := []discovery.Security{
		{CIK: "0000009001", CompanyName: "Alpha", CatalogStatus: discovery.SecurityCatalogPublished},
		{CIK: "0000009002", CompanyName: "Beta", CatalogStatus: discovery.SecurityCatalogPublished},
		{CIK: "0000009003", CompanyName: "Bravo", CatalogStatus: discovery.SecurityCatalogPublished},
	}
	if err := db.Create(&securities).Error; err != nil {
		t.Fatalf("seed securities: %v", err)
	}
	batch := discovery.UniverseBatch{BatchID: "current", Kind: discovery.BatchKindPrescreen, Status: discovery.BatchStatusPublished, StartedAt: time.Now()}
	if err := db.Create(&batch).Error; err != nil {
		t.Fatalf("seed batch: %v", err)
	}
	if err := db.Create(&discovery.CurrentBatchPointer{Kind: discovery.BatchKindPrescreen, BatchID: batch.BatchID}).Error; err != nil {
		t.Fatalf("seed pointer: %v", err)
	}
	rows := []discovery.CandidateScoreSnapshot{
		{BatchID: batch.BatchID, SecurityID: securities[0].ID, Ticker: "AAA", Grade: discovery.CandidateGradeA, EligibleA: true, TotalScore: 90, MarketCapUSD: 120_000_000},
		{BatchID: batch.BatchID, SecurityID: securities[1].ID, Ticker: "AAB", Grade: discovery.CandidateGradeA, EligibleA: true, TotalScore: 80, MarketCapUSD: 130_000_000},
		{BatchID: batch.BatchID, SecurityID: securities[2].ID, Ticker: "BBB", Grade: discovery.CandidateGradeB, EligibleB: true, TotalScore: 70, MarketCapUSD: 600_000_000},
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatalf("seed candidate scores: %v", err)
	}
}
