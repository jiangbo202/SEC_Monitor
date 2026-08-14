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
	if report.SlowSECTargets != 1 || report.SlowDiscoverySteps != 2 || report.Status != "critical" {
		t.Fatalf("report = %+v", report)
	}
	if !hasOperationalIssue(report.Issues, "sec_slow_targets") || !hasOperationalIssue(report.Issues, "discovery_slow_steps") {
		t.Fatalf("issues = %+v", report.Issues)
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
