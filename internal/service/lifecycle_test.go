package service

import (
	"context"
	"testing"
	"time"

	"sec_monitor/internal/discovery"
	"sec_monitor/internal/model"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestLifecycleServicePreviewAndCleanupPreservesActiveAndResearchData(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 28, 9, 0, 0, 0, time.UTC)
	mainDB := testDB(t)
	discoveryDB, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	if err := discovery.Migrate(discoveryDB); err != nil {
		t.Fatal(err)
	}
	configs := NewConfigService(mainDB, NewAuditService(mainDB))
	if err := configs.EnsureDefaults(ctx); err != nil {
		t.Fatal(err)
	}
	if err := configs.UpsertMany(ctx, []ConfigInput{{Key: "system.operation_history_retention_days", Value: "90", ValueType: "int", Category: "system"}}, "tester"); err != nil {
		t.Fatal(err)
	}
	oldFinished := now.AddDate(0, 0, -91)
	oldRun := model.SyncRun{StartedAt: oldFinished.Add(-time.Minute), FinishedAt: &oldFinished, Status: "success", Trigger: "scheduler"}
	activeRun := model.SyncRun{StartedAt: oldFinished, Status: "running", Trigger: "scheduler"}
	if err := mainDB.Create(&oldRun).Error; err != nil {
		t.Fatal(err)
	}
	if err := mainDB.Create(&activeRun).Error; err != nil {
		t.Fatal(err)
	}
	if err := mainDB.Create(&model.SyncRunDetail{SyncRunID: oldRun.ID, Ticker: "OLD", Status: "success", StartedAt: oldFinished, FinishedAt: &oldFinished}).Error; err != nil {
		t.Fatal(err)
	}
	if err := mainDB.Create(&model.SyncRunDetail{SyncRunID: activeRun.ID, Ticker: "LIVE", Status: "running", StartedAt: oldFinished}).Error; err != nil {
		t.Fatal(err)
	}
	oldTaskExecution := model.TaskExecution{TaskName: "market_trend_sync", Trigger: "scheduled", Status: "success", StartedAt: oldFinished.Add(-time.Minute), FinishedAt: &oldFinished}
	activeTaskExecution := model.TaskExecution{TaskName: "market_trend_sync", Trigger: "scheduled", Status: "running", StartedAt: oldFinished}
	if err := mainDB.Create(&[]model.TaskExecution{oldTaskExecution, activeTaskExecution}).Error; err != nil {
		t.Fatal(err)
	}
	if err := mainDB.Create(&model.OperationalAlertDelivery{Fingerprint: "old", Severity: "warning", UpdatedAt: oldFinished}).Error; err != nil {
		t.Fatal(err)
	}
	batches := []model.NotificationBatch{
		{SyncRunID: oldRun.ID, Source: "ipo", Trigger: "scheduled", Channel: "telegram", Status: "dead_letter", UpdatedAt: oldFinished},
		{SyncRunID: activeRun.ID, Source: "candidate", Trigger: "scheduled", Channel: "telegram", Status: "sent", UpdatedAt: now},
	}
	if err := mainDB.Create(&batches).Error; err != nil {
		t.Fatal(err)
	}
	if err := mainDB.Create(&[]model.NotificationBatchItem{{BatchID: batches[0].ID, EntityKind: "ipo_filing", FilingID: "old", Status: "failed", Reason: "delivery_failed"}, {BatchID: batches[1].ID, EntityKind: "candidate", FilingID: "live", Status: "sent", Reason: "eligible"}}).Error; err != nil {
		t.Fatal(err)
	}
	oldDiscovery := discovery.DiscoverySyncRun{Kind: "incremental", Status: "published", Phase: "complete", StartedAt: oldFinished.Add(-time.Minute), CompletedAt: &oldFinished}
	activeDiscovery := discovery.DiscoverySyncRun{Kind: "incremental", Status: "running", Phase: "market", StartedAt: oldFinished}
	if err := discoveryDB.Create(&oldDiscovery).Error; err != nil {
		t.Fatal(err)
	}
	if err := discoveryDB.Create(&activeDiscovery).Error; err != nil {
		t.Fatal(err)
	}
	if err := discoveryDB.Create(&discovery.DiscoverySyncStep{RunID: oldDiscovery.ID, Sequence: 1, Phase: "complete", Status: "completed", StartedAt: oldFinished, CompletedAt: &oldFinished}).Error; err != nil {
		t.Fatal(err)
	}
	if err := discoveryDB.Create(&discovery.DiscoverySyncStep{RunID: activeDiscovery.ID, Sequence: 1, Phase: "market", Status: "running", StartedAt: oldFinished}).Error; err != nil {
		t.Fatal(err)
	}
	// A published batch is intentionally not part of lifecycle cleanup.
	if err := discoveryDB.Create(&discovery.UniverseBatch{BatchID: "published-batch", Kind: "market-prescreen", Status: "published", EffectiveDate: now.Format("2006-01-02"), StartedAt: oldFinished}).Error; err != nil {
		t.Fatal(err)
	}

	svc := NewLifecycleService(mainDB, discoveryDB, configs)
	preview, err := svc.Preview(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if preview.SyncRuns != 1 || preview.SyncRunDetails != 1 || preview.TaskExecutions != 1 || preview.NotificationBatches != 1 || preview.NotificationBatchItems != 1 || preview.OperationalAlertDeliveries != 1 || preview.DiscoverySyncRuns != 1 || preview.DiscoverySyncSteps != 1 || preview.Total != 8 {
		t.Fatalf("preview = %#v", preview)
	}
	if _, err := svc.Cleanup(ctx, now); err != nil {
		t.Fatal(err)
	}
	var cleanupRun model.LifecycleCleanupRun
	if err := mainDB.Order("id DESC").First(&cleanupRun).Error; err != nil || cleanupRun.Status != "completed" || cleanupRun.MainStatus != "completed" || cleanupRun.DiscoveryStatus != "completed" {
		t.Fatalf("cleanup run = %#v, %v", cleanupRun, err)
	}
	for _, check := range []struct {
		db    *gorm.DB
		model any
		want  int64
	}{
		{mainDB, &model.SyncRun{}, 1},
		{mainDB, &model.SyncRunDetail{}, 1},
		{mainDB, &model.TaskExecution{}, 1},
		{mainDB, &model.NotificationBatch{}, 1},
		{mainDB, &model.NotificationBatchItem{}, 1},
		{mainDB, &model.OperationalAlertDelivery{}, 0},
		{discoveryDB, &discovery.DiscoverySyncRun{}, 1},
		{discoveryDB, &discovery.DiscoverySyncStep{}, 1},
		{discoveryDB, &discovery.UniverseBatch{}, 1},
	} {
		var got int64
		if err := check.db.Model(check.model).Count(&got).Error; err != nil {
			t.Fatal(err)
		}
		if got != check.want {
			t.Fatalf("%T count=%d, want %d", check.model, got, check.want)
		}
	}
}

func TestLifecycleCleanupPrunesOnlySupersededMarketRepairSnapshots(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 28, 9, 0, 0, 0, time.UTC)
	old := now.AddDate(0, 0, -91)
	mainDB := testDB(t)
	discoveryDB, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	if err := discovery.Migrate(discoveryDB); err != nil {
		t.Fatal(err)
	}
	configs := NewConfigService(mainDB, NewAuditService(mainDB))
	if err := configs.EnsureDefaults(ctx); err != nil {
		t.Fatal(err)
	}
	if err := configs.UpsertMany(ctx, []ConfigInput{{Key: "system.operation_history_retention_days", Value: "90", ValueType: "int", Category: "system"}}, "tester"); err != nil {
		t.Fatal(err)
	}
	security := discovery.Security{CIK: "0000001234", CompanyName: "Repair", CatalogStatus: discovery.SecurityCatalogPublished}
	if err := discoveryDB.Create(&security).Error; err != nil {
		t.Fatal(err)
	}
	completedNow := now
	oldRepair := discovery.UniverseBatch{
		BatchID: "old-repair", Kind: "market-prescreen", Status: "published", EffectiveDate: old.Format(time.DateOnly),
		SourceVersionsJSON: `[{"source":"price-repair:repr","version":"longbridge:1"}]`, ContentSHA256: "old", StartedAt: old, CompletedAt: &old,
	}
	current := discovery.UniverseBatch{
		BatchID: "current-market", Kind: "market-prescreen", Status: "published", EffectiveDate: now.Format(time.DateOnly),
		SourceVersionsJSON: `[{"source":"price:longbridge","version":"today"}]`, ContentSHA256: "current", StartedAt: now, CompletedAt: &completedNow,
	}
	if err := discoveryDB.Create(&[]discovery.UniverseBatch{oldRepair, current}).Error; err != nil {
		t.Fatal(err)
	}
	if err := discoveryDB.Create(&discovery.CurrentBatchPointer{Kind: "market-prescreen", BatchID: current.BatchID, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := discoveryDB.Create(&[]discovery.UniverseSnapshot{
		{BatchID: oldRepair.BatchID, SecurityID: security.ID, Ticker: "REPR", MarketCapUSD: 100_000_000},
		{BatchID: current.BatchID, SecurityID: security.ID, Ticker: "REPR", MarketCapUSD: 101_000_000},
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := discoveryDB.Create(&[]discovery.CandidateScoreSnapshot{
		{BatchID: oldRepair.BatchID, SecurityID: security.ID, Ticker: "REPR", Grade: "B"},
		{BatchID: current.BatchID, SecurityID: security.ID, Ticker: "REPR", Grade: "B"},
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := discoveryDB.Create(&discovery.CandidateSignalEvent{BatchID: oldRepair.BatchID, SecurityID: security.ID, Ticker: "REPR", Grade: "B", EventType: "entered_b", SignalDate: old, BaselineTradeDate: old, BaselineCloseMicros: 1_000_000}).Error; err != nil {
		t.Fatal(err)
	}

	svc := NewLifecycleService(mainDB, discoveryDB, configs)
	preview, err := svc.Preview(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if preview.SupersededMarketRepairs != 1 || preview.MarketRepairUniverseRows != 1 || preview.MarketRepairScoreRows != 1 || preview.Total != 3 {
		t.Fatalf("preview = %#v", preview)
	}
	if _, err := svc.Cleanup(ctx, now); err != nil {
		t.Fatal(err)
	}
	for _, check := range []struct {
		model any
		where string
		args  []any
		want  int64
	}{
		{&discovery.UniverseBatch{}, "batch_id = ?", []any{oldRepair.BatchID}, 0},
		{&discovery.UniverseSnapshot{}, "batch_id = ?", []any{oldRepair.BatchID}, 0},
		{&discovery.CandidateScoreSnapshot{}, "batch_id = ?", []any{oldRepair.BatchID}, 0},
		{&discovery.UniverseBatch{}, "batch_id = ?", []any{current.BatchID}, 1},
		{&discovery.CandidateSignalEvent{}, "batch_id = ?", []any{oldRepair.BatchID}, 1},
	} {
		var got int64
		if err := discoveryDB.Model(check.model).Where(check.where, check.args...).Count(&got).Error; err != nil {
			t.Fatal(err)
		}
		if got != check.want {
			t.Fatalf("%T count=%d, want %d", check.model, got, check.want)
		}
	}
}
