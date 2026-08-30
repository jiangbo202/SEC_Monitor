package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"sec_monitor/internal/model"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestTaskFailureCircuitBacksOffAndSuccessRecovers(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:task-failure-circuit?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.TaskConfig{}, &model.OperationLog{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.TaskConfig{TaskName: "market_trend_sync", CronExpr: "0 * * * *", Enabled: true}).Error; err != nil {
		t.Fatal(err)
	}
	service := NewTaskConfigService(db, NewAuditService(db))
	failedAt := time.Date(2026, 8, 30, 0, 0, 0, 0, time.UTC)
	if err := service.MarkRunOutcome(context.Background(), "market_trend_sync", failedAt, errors.New("provider unavailable")); err != nil {
		t.Fatal(err)
	}
	allowed, retryAt, err := service.ScheduledRunAllowed(context.Background(), "market_trend_sync", failedAt.Add(time.Minute))
	if err != nil || allowed || retryAt == nil || !retryAt.Equal(failedAt.Add(5*time.Minute)) {
		t.Fatalf("first failure circuit = allowed %v retry %v err %v", allowed, retryAt, err)
	}
	if err := service.MarkRunOutcome(context.Background(), "market_trend_sync", failedAt.Add(6*time.Minute), errors.New("provider unavailable again")); err != nil {
		t.Fatal(err)
	}
	var task model.TaskConfig
	if err := db.Where("task_name = ?", "market_trend_sync").First(&task).Error; err != nil {
		t.Fatal(err)
	}
	if task.ConsecutiveFailures != 2 || task.RetryNotBefore == nil || !task.RetryNotBefore.Equal(failedAt.Add(21*time.Minute)) {
		t.Fatalf("second failure task = %+v", task)
	}
	if err := service.MarkRunOutcome(context.Background(), "market_trend_sync", failedAt.Add(7*time.Minute), nil); err != nil {
		t.Fatal(err)
	}
	task = model.TaskConfig{}
	if err := db.Where("task_name = ?", "market_trend_sync").First(&task).Error; err != nil {
		t.Fatal(err)
	}
	if task.ConsecutiveFailures != 0 || task.RetryNotBefore != nil || task.LastStatus != "success" {
		t.Fatalf("success did not close circuit: %+v", task)
	}
}

func TestPartialTaskDoesNotTripTaskCircuit(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:task-partial-circuit?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.TaskConfig{}, &model.OperationLog{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.TaskConfig{TaskName: "sec_filing_sync", CronExpr: "*/5 * * * *", Enabled: true, ConsecutiveFailures: 2}).Error; err != nil {
		t.Fatal(err)
	}
	service := NewTaskConfigService(db, NewAuditService(db))
	if err := service.MarkRunOutcome(context.Background(), "sec_filing_sync", time.Now().UTC(), PartialTask("2 records remain in their bounded retry queue")); err != nil {
		t.Fatal(err)
	}
	var task model.TaskConfig
	if err := db.Where("task_name = ?", "sec_filing_sync").First(&task).Error; err != nil {
		t.Fatal(err)
	}
	if task.LastStatus != "partial" || task.ConsecutiveFailures != 0 || task.RetryNotBefore != nil {
		t.Fatalf("partial outcome incorrectly opened task circuit: %+v", task)
	}
}

func TestEnsureDefaultReconcilesLegacyPartialFailureStreak(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:task-partial-reconcile?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.TaskConfig{}, &model.OperationLog{}); err != nil {
		t.Fatal(err)
	}
	retryAt := time.Now().UTC().Add(time.Hour)
	if err := db.Create(&model.TaskConfig{TaskName: "watch_target_market_sync", CronExpr: "35 5 * * 2-6", Enabled: true, LastStatus: "partial", LastErrorMessage: "3/6 updated", ConsecutiveFailures: 6, RetryNotBefore: &retryAt}).Error; err != nil {
		t.Fatal(err)
	}
	service := NewTaskConfigService(db, NewAuditService(db))
	if err := service.EnsureDefault(context.Background()); err != nil {
		t.Fatal(err)
	}
	var task model.TaskConfig
	if err := db.Where("task_name = ?", "watch_target_market_sync").First(&task).Error; err != nil {
		t.Fatal(err)
	}
	if task.LastStatus != "partial" || task.LastErrorMessage != "3/6 updated" || task.ConsecutiveFailures != 0 || task.RetryNotBefore != nil {
		t.Fatalf("legacy partial failure was not reconciled safely: %+v", task)
	}
}

func TestPersistentRetriesAreClaimedOnceAndBounded(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.TaskConfig{}); err != nil {
		t.Fatal(err)
	}
	name := "watch_target_market_sync"
	if err := db.Create(&model.TaskConfig{TaskName: name, Enabled: true, CronExpr: "0 0 * * *"}).Error; err != nil {
		t.Fatal(err)
	}
	svc := NewTaskConfigService(db, nil)
	now := time.Now().UTC()
	count := 2
	if err := svc.MarkRunOutcome(context.Background(), name, now, PendingTask("2 missing", &count)); err != nil {
		t.Fatal(err)
	}
	if rows, err := svc.DueRetries(context.Background(), now); err != nil || len(rows) != 0 {
		t.Fatalf("premature retry: %+v %v", rows, err)
	}
	// A freshly constructed service sees the queue left by the previous process.
	svc = NewTaskConfigService(db, nil)
	for attempt := 1; attempt <= MaxTaskAutoRetries; attempt++ {
		now = now.Add(25 * time.Hour)
		rows, err := svc.DueRetries(context.Background(), now)
		if err != nil || len(rows) != 1 {
			t.Fatalf("due = %+v, %v", rows, err)
		}
		if ok, err := svc.ClaimRetry(context.Background(), name, now); err != nil || !ok {
			t.Fatalf("claim %v %v", ok, err)
		}
		if ok, err := svc.ClaimRetry(context.Background(), name, now); err != nil || ok {
			t.Fatalf("duplicate claim %v %v", ok, err)
		}
		if err := svc.MarkRunOutcome(context.Background(), name, now, PendingTask("2 missing", &count)); err != nil {
			t.Fatal(err)
		}
	}
	if rows, err := svc.DueRetries(context.Background(), now.Add(7*24*time.Hour)); err != nil || len(rows) != 0 {
		t.Fatalf("unbounded retries: %+v %v", rows, err)
	}
	var task model.TaskConfig
	db.First(&task)
	if task.AutoRetryAttempts != 3 || task.PendingCount == nil || *task.PendingCount != 2 || task.LastStatus != "partial" {
		t.Fatalf("lost pending state: %+v", task)
	}
	if err := svc.MarkRunStarted(context.Background(), name); err != nil {
		t.Fatal(err)
	}
	if err := svc.MarkRunOutcome(context.Background(), name, now, nil); err != nil {
		t.Fatal(err)
	}
	task = model.TaskConfig{}
	db.First(&task)
	if task.AutoRetryAttempts != 0 || task.PendingCount != nil || task.RetryNotBefore != nil {
		t.Fatalf("success left retry state: %+v", task)
	}
}

func TestRetryQueueHonorsDisabledTasksAndRecovery(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	db.AutoMigrate(&model.TaskConfig{})
	now := time.Now().UTC()
	db.Create(&model.TaskConfig{TaskName: "disabled", RetryNotBefore: &now, LastStatus: "failed"})
	db.Create(&model.TaskConfig{TaskName: "interrupted", Enabled: true, Running: true})
	svc := NewTaskConfigService(db, nil)
	if _, err := svc.RecoverInterrupted(context.Background()); err != nil {
		t.Fatal(err)
	}
	rows, err := svc.DueRetries(context.Background(), now.Add(time.Hour))
	if err != nil || len(rows) != 1 || rows[0].TaskName != "interrupted" {
		t.Fatalf("recovered queue: %+v %v", rows, err)
	}
	if ok, err := svc.ClaimRetry(context.Background(), "disabled", now.Add(time.Hour)); err != nil || ok {
		t.Fatalf("disabled task claimed: %v %v", ok, err)
	}
	if normalizedTaskTrigger("retry") != "retry" {
		t.Fatal("retry history mislabeled")
	}
}
