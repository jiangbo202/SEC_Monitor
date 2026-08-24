package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"sec_monitor/internal/discovery"
	"sec_monitor/internal/model"
)

func TestOperationalHealthReportAndNotificationDedup(t *testing.T) {
	db := testDB(t)
	now := time.Now().UTC()
	nextRun := now.Add(4 * time.Minute)
	if err := db.Create(&model.TaskConfig{
		TaskName: "sec_filing_sync", CronExpr: "*/5 * * * *", Enabled: true,
		LastStatus: "failed", LastRunAt: ptrOperationalTime(now.Add(-time.Minute)), NextRunAt: &nextRun, ConsecutiveFailures: 3,
		LastErrorMessage: "SEC timeout token=should-not-leak",
	}).Error; err != nil {
		t.Fatalf("seed task: %v", err)
	}
	if err := db.Create(&model.SyncRunDetail{SyncRunID: 1, TargetID: 1, Ticker: "RETRY", Status: "failed", Retryable: true, NextRetryAt: ptrOperationalTime(now.Add(-time.Minute)), StartedAt: now.Add(-time.Minute)}).Error; err != nil {
		t.Fatalf("seed retry detail: %v", err)
	}
	if err := db.Create(&model.SyncRunDetail{SyncRunID: 1, TargetID: 2, Ticker: "HOLD", Status: "deferred", StartedAt: now.Add(-time.Minute)}).Error; err != nil {
		t.Fatalf("seed deferred detail: %v", err)
	}
	configs := NewConfigService(db, NewAuditService(db))
	if err := configs.UpsertMany(context.Background(), []ConfigInput{
		{Key: "telegram.enabled", Value: "true", ValueType: "bool", Category: "telegram"},
		{Key: "telegram.bot_token", Value: "token", ValueType: "string", Category: "telegram"},
		{Key: "telegram.chat_id", Value: "chat", ValueType: "string", Category: "telegram"},
	}, "tester"); err != nil {
		t.Fatalf("seed telegram config: %v", err)
	}
	notifier := &fakeNotifier{}
	svc := NewOperationalHealthService(db, nil, notifier, configs)
	report, err := svc.ReportAt(context.Background(), now)
	if err != nil {
		t.Fatalf("ReportAt: %v", err)
	}
	if report.Status != "critical" || report.RetryableTargets != 1 || report.DeferredTargets != 1 {
		t.Fatalf("report = %+v", report)
	}
	if len(report.Issues) < 3 {
		t.Fatalf("issues = %+v, want task/retry/deferred issues", report.Issues)
	}
	if len(report.Tasks) != 1 || report.Tasks[0].NextRunAt == nil || !report.Tasks[0].NextRunAt.Equal(nextRun) {
		t.Fatalf("task execution plan = %+v, want next run %s", report.Tasks, nextRun)
	}
	first, err := svc.Notify(context.Background())
	if err != nil || !first.Sent || notifier.calls != 1 {
		t.Fatalf("first notify = %+v, %v; calls=%d", first, err, notifier.calls)
	}
	second, err := svc.Notify(context.Background())
	if !errors.Is(err, ErrTaskSkipped) || !second.Suppressed || second.Reason != "duplicate_within_12h" || notifier.calls != 1 {
		t.Fatalf("second notify = %+v, %v; calls=%d", second, err, notifier.calls)
	}
}

func TestOperationalHealthReportsProviderCoverageAndProfileRecovery(t *testing.T) {
	db := testDB(t)
	discoveryDB := testDiscoveryDB(t)
	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	batch := discovery.UniverseBatch{BatchID: "market", Kind: discovery.BatchKindPrescreen, Status: discovery.BatchStatusPublished, EffectiveDate: "2026-07-27", StartedAt: now}
	if err := discoveryDB.Create(&batch).Error; err != nil {
		t.Fatal(err)
	}
	if err := discoveryDB.Create(&discovery.ProviderHealth{Provider: "longbridge", Status: discovery.ProviderStatusActive, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := discoveryDB.Create(&discovery.ProviderRun{BatchID: batch.BatchID, Provider: "longbridge", Status: discovery.ProviderStatusActive, ExpectedCount: 100, RecordCount: 10, CoveragePct: 10, ErrorMessage: "rate limited HTTP 429", CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	security := discovery.Security{CIK: "0000000999", CompanyName: "Profile retry"}
	if err := discoveryDB.Create(&security).Error; err != nil {
		t.Fatal(err)
	}
	if err := discoveryDB.Create(&discovery.CompanyProfileSnapshot{SecurityID: security.ID, Provider: "longbridge", Ticker: "RETRY", LastError: "HTTP 429", RetryCount: 1}).Error; err != nil {
		t.Fatal(err)
	}
	report, err := NewOperationalHealthService(db, discoveryDB, nil, nil).ReportAt(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}
	if report.LowCoverageProviders != 1 || report.CompanyProfileRetryDue != 1 {
		t.Fatalf("report = %+v", report)
	}
	if !hasOperationalIssue(report.Issues, "provider_coverage:longbridge") || !hasOperationalIssue(report.Issues, "company_profile_retry_due") {
		t.Fatalf("issues = %+v", report.Issues)
	}
}

func TestOperationalHealthReportsTechnicalHistoryRetryQueue(t *testing.T) {
	db := testDB(t)
	discoveryDB := testDiscoveryDB(t)
	now := time.Date(2026, time.August, 24, 12, 0, 0, 0, time.UTC)
	if err := discoveryDB.Create(&discovery.CurrentBatchPointer{Kind: discovery.BatchKindPrescreen, BatchID: "market-retry"}).Error; err != nil {
		t.Fatal(err)
	}
	due := now.Add(-time.Minute)
	later := now.Add(6 * time.Hour)
	states := []discovery.TechnicalHistoryRetryState{
		{Ticker: "DUE", BatchID: "market-retry", Status: discovery.TechnicalHistoryRetryBackoff, Reason: "provider_request_failed", FailureCount: 2, NextRetryAt: &due, LastAttemptAt: now.Add(-time.Hour)},
		{Ticker: "WAIT", BatchID: "market-retry", Status: discovery.TechnicalHistoryRetryBackoff, Reason: "no_usable_records", FailureCount: 3, NextRetryAt: &later, LastAttemptAt: now.Add(-time.Hour)},
		{Ticker: "HARD", BatchID: "market-retry", Status: discovery.TechnicalHistoryRetryDeferred, Reason: "no_usable_records", FailureCount: 5, NextRetryAt: &later, LastAttemptAt: now.Add(-time.Hour)},
		{Ticker: "OLD", BatchID: "old-market", Status: discovery.TechnicalHistoryRetryDeferred, Reason: "no_usable_records", FailureCount: 8, NextRetryAt: &due, LastAttemptAt: now.Add(-time.Hour)},
	}
	if err := discoveryDB.Create(&states).Error; err != nil {
		t.Fatal(err)
	}
	report, err := NewOperationalHealthService(db, discoveryDB, nil, nil).ReportAt(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}
	if report.TechnicalHistoryPending != 3 || report.TechnicalHistoryRetryDue != 1 || report.TechnicalHistoryDeferred != 1 {
		t.Fatalf("report = %+v", report)
	}
	if report.Status != "critical" || !hasOperationalIssue(report.Issues, "technical_history_retry_queue") {
		t.Fatalf("issues = %+v", report.Issues)
	}
}

func TestOperationalHealthTreatsProviderValidationAsWarningAndReportsMissingMacroCoverage(t *testing.T) {
	db := testDB(t)
	discoveryDB := testDiscoveryDB(t)
	now := time.Date(2026, time.August, 14, 12, 0, 0, 0, time.UTC)
	if err := db.Create(&model.TaskConfig{TaskName: "macro_calendar_sync", CronExpr: "45 20 * * 1-5", Enabled: true, LastStatus: "success", LastRunAt: ptrOperationalTime(now.Add(-time.Hour))}).Error; err != nil {
		t.Fatal(err)
	}
	if err := discoveryDB.Create(&discovery.ProviderHealth{Provider: "chain", Status: discovery.ProviderStatusValidation, QualifiedTradingDays: 20, FailureStreak: 31, LastTradeDate: "2026-08-13", UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	report, err := NewOperationalHealthService(db, discoveryDB, nil, nil).ReportAt(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}
	if !hasOperationalIssue(report.Issues, "macro_coverage:employment") || !hasOperationalIssue(report.Issues, "macro_coverage:ppi") {
		t.Fatalf("macro issues = %+v", report.Issues)
	}
	for _, issue := range report.Issues {
		if issue.Key == "provider:chain" && issue.Severity != "warning" {
			t.Fatalf("validation provider should be warning, issue=%+v", issue)
		}
	}
}

func TestOperationalHealthReportsSuccessfulProviderFallback(t *testing.T) {
	db := testDB(t)
	discoveryDB := testDiscoveryDB(t)
	now := time.Date(2026, time.August, 20, 12, 0, 0, 0, time.UTC)
	batch := discovery.UniverseBatch{BatchID: "fallback-market", Kind: discovery.BatchKindPrescreen, Status: discovery.BatchStatusPublished, EffectiveDate: "2026-08-19", StartedAt: now}
	if err := discoveryDB.Create(&batch).Error; err != nil {
		t.Fatal(err)
	}
	if err := discoveryDB.Create(&discovery.ProviderHealth{Provider: "longbridge,tiingo", Status: discovery.ProviderStatusActive, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := discoveryDB.Create(&discovery.ProviderRun{BatchID: batch.BatchID, Provider: "longbridge,tiingo", Status: discovery.ProviderStatusActive, ExpectedCount: 100, RecordCount: 100, CoveragePct: 100, FallbackUsed: true, CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	report, err := NewOperationalHealthService(db, discoveryDB, nil, nil).ReportAt(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}
	if report.ProviderWarnings != 1 || !hasOperationalIssue(report.Issues, "provider_fallback:longbridge,tiingo") {
		t.Fatalf("report = %+v", report)
	}
}

func TestClassifyOperationalExternalFailure(t *testing.T) {
	for _, test := range []struct{ message, want string }{
		{"HTTP 429 rate limited", "限流"},
		{"context deadline exceeded", "超时或上游暂不可用"},
		{"HTTP 403 forbidden", "认证或权限"},
		{"HTTP 404 not found", "资源不存在"},
	} {
		if got, _ := classifyOperationalExternalFailure(test.message); got != test.want {
			t.Fatalf("classify(%q) = %q, want %q", test.message, got, test.want)
		}
	}
}

func TestOperationalHealthReportsSlowSECAndDiscoverySteps(t *testing.T) {
	db := testDB(t)
	discoveryDB := testDiscoveryDB(t)
	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	finishedAt := now.Add(-time.Minute)
	if err := db.Create(&model.SyncRunDetail{
		SyncRunID: 1, TargetID: 1, Ticker: "SLOW", Status: "success", StartedAt: finishedAt.Add(-3 * time.Minute), FinishedAt: &finishedAt,
		DurationMS: (3 * time.Minute).Milliseconds(),
	}).Error; err != nil {
		t.Fatal(err)
	}
	completedAt := now.Add(-time.Minute)
	if err := discoveryDB.Create(&[]discovery.DiscoverySyncRun{
		{ID: 1, Kind: "full", Status: DiscoverySyncRunStatusFailed, Phase: "failed", StartedAt: now.Add(-2 * time.Hour), CompletedAt: &completedAt},
		{ID: 2, Kind: "full", Status: "running", Phase: "technical_history", StartedAt: now.Add(-31 * time.Minute)},
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := discoveryDB.Create(&[]discovery.DiscoverySyncStep{
		{RunID: 1, Sequence: 1, Phase: "market_prescreen", Status: "completed", StartedAt: completedAt.Add(-100 * time.Minute), CompletedAt: &completedAt},
		{RunID: 2, Sequence: 1, Phase: "technical_history", Status: "running", StartedAt: now.Add(-31 * time.Minute)},
	}).Error; err != nil {
		t.Fatal(err)
	}
	report, err := NewOperationalHealthService(db, discoveryDB, nil, nil).ReportAt(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}
	if report.SlowSECTargets != 1 || report.SlowDiscoverySteps != 1 || report.Status != "critical" {
		t.Fatalf("report = %+v", report)
	}
	if !hasOperationalIssue(report.Issues, "sec_slow_targets") || !hasOperationalIssue(report.Issues, "discovery_slow_steps") {
		t.Fatalf("issues = %+v", report.Issues)
	}
}

func TestOperationalHealthReconcilesNewerPublishedDirectFullRun(t *testing.T) {
	db := testDB(t)
	discoveryDB := testDiscoveryDB(t)
	now := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	scheduledAt := now.Add(-2 * time.Hour)
	if err := db.Create(&model.TaskConfig{
		TaskName: "small_cap_discovery_full_sync", CronExpr: "30 9 * * 6", Enabled: true,
		LastStatus: "interrupted", LastRunAt: &scheduledAt, ConsecutiveFailures: 2, LastErrorMessage: "service restarted",
	}).Error; err != nil {
		t.Fatal(err)
	}
	completedAt := now.Add(-time.Minute)
	if err := discoveryDB.Create(&discovery.DiscoverySyncRun{
		Kind: "full", Status: DiscoverySyncStatusPublished, Phase: "completed", StartedAt: now.Add(-20 * time.Minute), CompletedAt: &completedAt,
	}).Error; err != nil {
		t.Fatal(err)
	}
	report, err := NewOperationalHealthService(db, discoveryDB, nil, nil).ReportAt(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Tasks) != 1 || report.Tasks[0].LastStatus != "success" || report.Tasks[0].ConsecutiveFailures != 0 || report.Tasks[0].LastRunAt == nil || !report.Tasks[0].LastRunAt.Equal(completedAt) {
		t.Fatalf("tasks = %+v", report.Tasks)
	}
	if hasOperationalIssue(report.Issues, "task_failed:small_cap_discovery_full_sync") {
		t.Fatalf("stale scheduler failure should be superseded: %+v", report.Issues)
	}
}

func hasOperationalIssue(items []OperationalIssue, key string) bool {
	for _, item := range items {
		if item.Key == key {
			return true
		}
	}
	return false
}

func ptrOperationalTime(value time.Time) *time.Time { return &value }
