package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"sec_monitor/internal/discovery"
	"sec_monitor/internal/model"

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

	result, err := NewCandidateNotificationService(db, discoveryDB, nil, configs).Preview(context.Background())
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

	result, err := NewCandidateNotificationService(db, discoveryDB, nil, configs).Preview(context.Background())
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

func TestCandidateNotificationSendRequiresConfirmation(t *testing.T) {
	db := testDB(t)
	discoveryDB := testDiscoveryDB(t)
	configs := NewConfigService(db, NewAuditService(db))
	if err := configs.EnsureDefaults(context.Background()); err != nil {
		t.Fatalf("EnsureDefaults: %v", err)
	}

	_, err := NewCandidateNotificationService(db, discoveryDB, &fakeNotifier{}, configs).Send(context.Background(), CandidateNotificationSendInput{})
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("Send error = %v, want validation", err)
	}
}

func TestCandidateNotificationSendCreatesBatchAndCallsNotifier(t *testing.T) {
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
		{Key: "telegram.enabled", Value: "true", ValueType: "bool", Category: "telegram"},
		{Key: "telegram.bot_token", Value: "token", ValueType: "string", Category: "telegram"},
		{Key: "telegram.chat_id", Value: "chat", ValueType: "string", Category: "telegram"},
	}, "test"); err != nil {
		t.Fatalf("UpsertMany: %v", err)
	}
	seedCandidateScores(t, discoveryDB)
	notifier := &fakeNotifier{}

	result, err := NewCandidateNotificationService(db, discoveryDB, notifier, configs).Send(context.Background(), CandidateNotificationSendInput{Confirm: true})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if result.Batch.Status != "sent" || result.Batch.Source != "candidate" || result.Batch.SentCount != 1 || result.Batch.ItemCount != 1 {
		t.Fatalf("batch = %+v", result.Batch)
	}
	if notifier.calls != 1 || len(notifier.messages) != 1 || !strings.Contains(notifier.messages[0].Text, "AAA") || !strings.Contains(notifier.messages[0].Text, "小盘股研究候选摘要") {
		t.Fatalf("notifier calls=%d messages=%+v", notifier.calls, notifier.messages)
	}
	var item model.NotificationBatchItem
	if err := db.Where("batch_id = ?", result.Batch.ID).First(&item).Error; err != nil {
		t.Fatalf("load item: %v", err)
	}
	if item.EntityKind != "candidate" || item.Ticker != "AAA" || item.FilingID == "" {
		t.Fatalf("item = %+v", item)
	}
}

func TestCandidateNotificationSendBlocksDuplicateBatchToday(t *testing.T) {
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
		{Key: "telegram.enabled", Value: "true", ValueType: "bool", Category: "telegram"},
		{Key: "telegram.bot_token", Value: "token", ValueType: "string", Category: "telegram"},
		{Key: "telegram.chat_id", Value: "chat", ValueType: "string", Category: "telegram"},
	}, "test"); err != nil {
		t.Fatalf("UpsertMany: %v", err)
	}
	seedCandidateScores(t, discoveryDB)
	now := time.Now().UTC()
	batch := model.NotificationBatch{Source: "candidate", Trigger: "manual", Channel: "telegram", Status: "sent", ItemCount: 1, SentCount: 1, CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&batch).Error; err != nil {
		t.Fatalf("seed batch: %v", err)
	}
	if err := db.Create(&model.NotificationBatchItem{BatchID: batch.ID, EntityKind: "candidate", FilingID: "current:AAA:1", Ticker: "AAA", Status: "sent", Reason: "eligible", EventAt: now}).Error; err != nil {
		t.Fatalf("seed item: %v", err)
	}
	notifier := &fakeNotifier{}

	_, err := NewCandidateNotificationService(db, discoveryDB, notifier, configs).Send(context.Background(), CandidateNotificationSendInput{Confirm: true})
	if !errors.Is(err, ErrValidation) || !strings.Contains(err.Error(), "candidate_notification_duplicate") {
		t.Fatalf("Send error = %v, want duplicate validation", err)
	}
	if notifier.calls != 0 {
		t.Fatalf("notifier calls = %d, want 0", notifier.calls)
	}
}

func TestCandidateNotificationSendForceAllowsDuplicateBatchToday(t *testing.T) {
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
		{Key: "telegram.enabled", Value: "true", ValueType: "bool", Category: "telegram"},
		{Key: "telegram.bot_token", Value: "token", ValueType: "string", Category: "telegram"},
		{Key: "telegram.chat_id", Value: "chat", ValueType: "string", Category: "telegram"},
	}, "test"); err != nil {
		t.Fatalf("UpsertMany: %v", err)
	}
	seedCandidateScores(t, discoveryDB)
	now := time.Now().UTC()
	batch := model.NotificationBatch{Source: "candidate", Trigger: "manual", Channel: "telegram", Status: "sent", ItemCount: 1, SentCount: 1, CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&batch).Error; err != nil {
		t.Fatalf("seed batch: %v", err)
	}
	if err := db.Create(&model.NotificationBatchItem{BatchID: batch.ID, EntityKind: "candidate", FilingID: "current:AAA:1", Ticker: "AAA", Status: "sent", Reason: "eligible", EventAt: now}).Error; err != nil {
		t.Fatalf("seed item: %v", err)
	}
	notifier := &fakeNotifier{}

	result, err := NewCandidateNotificationService(db, discoveryDB, notifier, configs).Send(context.Background(), CandidateNotificationSendInput{Confirm: true, Force: true})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if result.Batch.Status != "sent" || result.Batch.ID == batch.ID {
		t.Fatalf("batch = %+v", result.Batch)
	}
	if notifier.calls != 1 {
		t.Fatalf("notifier calls = %d, want 1", notifier.calls)
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
