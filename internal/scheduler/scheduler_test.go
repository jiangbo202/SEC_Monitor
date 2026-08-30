package scheduler

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"sec_monitor/internal/config"
	"sec_monitor/internal/discovery"
	"sec_monitor/internal/model"
	"sec_monitor/internal/sec"
	"sec_monitor/internal/service"
	"sec_monitor/internal/telegram"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type fakeSECClient struct{}

func (f fakeSECClient) LookupCIK(ctx context.Context, ticker string) (string, string, error) {
	return "", "", nil
}

func (f fakeSECClient) ListFilings(ctx context.Context, query sec.FilingQuery) ([]sec.FilingResult, error) {
	return nil, nil
}

func (f fakeSECClient) ListCurrentFilings(ctx context.Context, query sec.CurrentFilingQuery) ([]sec.CurrentFilingResult, error) {
	return []sec.CurrentFilingResult{{
		FilingID:    "ipo-1",
		CompanyName: "IPO Corp.",
		FilingType:  "S-1",
		FilingDate:  nowUTC(),
		FilingURL:   "https://www.sec.gov/ipo",
	}}, nil
}

type fakeNotifier struct{}

func (f fakeNotifier) Send(ctx context.Context, message telegram.Message) error {
	return nil
}

type countingNotifier struct {
	mu    sync.Mutex
	sends int
}

func (n *countingNotifier) Send(ctx context.Context, message telegram.Message) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.sends++
	return nil
}

func (n *countingNotifier) sendCount() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.sends
}

type blockingDiscoveryRunner struct {
	started chan struct{}
	unblock chan struct{}
	mu      sync.Mutex
	calls   int
}

type blockingIPOSECClient struct {
	fakeSECClient
	started chan struct{}
	unblock chan struct{}
}

func (c blockingIPOSECClient) ListCurrentFilings(ctx context.Context, query sec.CurrentFilingQuery) ([]sec.CurrentFilingResult, error) {
	c.started <- struct{}{}
	select {
	case <-c.unblock:
		return c.fakeSECClient.ListCurrentFilings(ctx, query)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (r *blockingDiscoveryRunner) SyncSecurityUniverse(ctx context.Context) (discovery.UniverseBatch, error) {
	r.mu.Lock()
	r.calls++
	r.mu.Unlock()
	r.started <- struct{}{}
	select {
	case <-r.unblock:
		return discovery.UniverseBatch{BatchID: "security"}, nil
	case <-ctx.Done():
		return discovery.UniverseBatch{}, ctx.Err()
	}
}

func (r *blockingDiscoveryRunner) SyncMarketPrices(ctx context.Context) (discovery.UniverseBatch, error) {
	return discovery.UniverseBatch{BatchID: "market"}, nil
}

func (r *blockingDiscoveryRunner) securityCalls() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

func testDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(
		&model.WatchTarget{}, &model.Filing{}, &model.SyncRun{}, &model.SyncRunDetail{}, &model.TaskConfig{}, &model.TaskExecution{},
		&model.SystemConfig{}, &model.OperationLog{}, &model.NotificationLog{}, &model.OperationalAlertDelivery{}, &model.RecoveryDrill{}, &model.LifecycleCleanupRun{},
		&model.NotificationBatch{}, &model.NotificationBatchItem{},
		&model.IPOFiling{}, &model.IPOCompanyOverride{}, &model.IPOCompanyMarketData{}, &model.IPOOfferingEvent{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	// SQLite's :memory: databases are scoped to one physical connection. The
	// scheduler executes jobs in a goroutine, so keep the test database on one
	// connection; otherwise a concurrent query can see a fresh database without
	// the migrated task_configs table.
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sql db: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)
	return db
}

func testDiscoveryDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open discovery db: %v", err)
	}
	if err := discovery.Migrate(db); err != nil {
		t.Fatalf("migrate discovery db: %v", err)
	}
	return db
}

func seedCandidate(t *testing.T, db *gorm.DB) {
	t.Helper()
	security := discovery.Security{CIK: "0000007001", CompanyName: "Candidate", CatalogStatus: discovery.SecurityCatalogPublished}
	if err := db.Create(&security).Error; err != nil {
		t.Fatalf("seed security: %v", err)
	}
	batch := discovery.UniverseBatch{BatchID: "sched-current", Kind: discovery.BatchKindPrescreen, Status: discovery.BatchStatusPublished, StartedAt: time.Now()}
	if err := db.Create(&batch).Error; err != nil {
		t.Fatalf("seed batch: %v", err)
	}
	if err := db.Create(&discovery.CurrentBatchPointer{Kind: discovery.BatchKindPrescreen, BatchID: batch.BatchID}).Error; err != nil {
		t.Fatalf("seed pointer: %v", err)
	}
	if err := db.Create(&discovery.CandidateScoreSnapshot{BatchID: batch.BatchID, SecurityID: security.ID, Ticker: "AUTO", Grade: discovery.CandidateGradeA, EligibleA: true, TotalScore: 91}).Error; err != nil {
		t.Fatalf("seed score: %v", err)
	}
}

func TestSchedulerTableDriven(t *testing.T) {
	tests := []struct {
		name    string
		seed    []model.TaskConfig
		run     func(context.Context, *Scheduler) error
		wantErr bool
	}{
		{
			name: "reloads enabled task",
			seed: []model.TaskConfig{{TaskName: "sec_filing_sync", CronExpr: "*/5 * * * *", Enabled: true}},
			run: func(ctx context.Context, sched *Scheduler) error {
				return sched.Reload(ctx)
			},
		},
		{
			name: "rejects invalid cron",
			seed: []model.TaskConfig{{TaskName: "sec_filing_sync", CronExpr: "bad cron", Enabled: true}},
			run: func(ctx context.Context, sched *Scheduler) error {
				return sched.Reload(ctx)
			},
			wantErr: true,
		},
		{
			name: "run once delegates refresh",
			run: func(ctx context.Context, sched *Scheduler) error {
				return sched.RunOnce(ctx)
			},
		},
		{
			name: "run ipo radar task",
			seed: []model.TaskConfig{{TaskName: "ipo_radar_sync", CronExpr: "*/30 * * * *", Enabled: true}},
			run: func(ctx context.Context, sched *Scheduler) error {
				return sched.RunTask(ctx, "ipo_radar_sync")
			},
		},
		{
			name: "run candidate notification task",
			seed: []model.TaskConfig{{TaskName: "candidate_notification_sync", CronExpr: "30 9 * * *", Enabled: true}},
			run: func(ctx context.Context, sched *Scheduler) error {
				return sched.RunTask(ctx, "candidate_notification_sync")
			},
		},
		{
			name: "run once records task status",
			seed: []model.TaskConfig{{TaskName: "sec_filing_sync", CronExpr: "*/5 * * * *", Enabled: true}},
			run: func(ctx context.Context, sched *Scheduler) error {
				return sched.RunOnce(ctx)
			},
		},
		{
			name: "start and stop lifecycle",
			seed: []model.TaskConfig{{TaskName: "sec_filing_sync", CronExpr: "*/5 * * * *", Enabled: false}},
			run: func(ctx context.Context, sched *Scheduler) error {
				if err := sched.Start(ctx); err != nil {
					return err
				}
				<-sched.Stop().Done()
				return nil
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := testDB(t)
			if len(tt.seed) > 0 {
				if err := db.Create(&tt.seed).Error; err != nil {
					t.Fatalf("seed tasks: %v", err)
				}
			}
			audit := service.NewAuditService(db)
			configs := service.NewConfigService(db, audit)
			if err := configs.EnsureDefaults(context.Background()); err != nil {
				t.Fatalf("EnsureDefaults: %v", err)
			}
			tasks := service.NewTaskConfigService(db, audit)
			filings := service.NewFilingService(db, fakeSECClient{}, fakeNotifier{}, configs)
			ipoRadar := service.NewIPORadarService(db, fakeSECClient{}, fakeNotifier{}, configs)
			discoveryDB := testDiscoveryDB(t)
			seedCandidate(t, discoveryDB)
			if err := configs.UpsertMany(context.Background(), []service.ConfigInput{
				{Key: "candidate_notification.enabled", Value: "true", ValueType: "bool", Category: "candidate_notification"},
				{Key: "candidate_notification.notify_a", Value: "true", ValueType: "bool", Category: "candidate_notification"},
				{Key: "telegram.enabled", Value: "true", ValueType: "bool", Category: "telegram"},
				{Key: "telegram.bot_token", Value: "token", ValueType: "string", Category: "telegram"},
				{Key: "telegram.chat_id", Value: "chat", ValueType: "string", Category: "telegram"},
			}, "test"); err != nil {
				t.Fatalf("candidate configs: %v", err)
			}
			candidateNotifications := service.NewCandidateNotificationService(db, discoveryDB, fakeNotifier{}, configs)
			err := tt.run(context.Background(), New(tasks, filings, ipoRadar, candidateNotifications))
			if tt.wantErr && err == nil {
				t.Fatalf("expected error")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("run: %v", err)
			}
			if tt.name == "run once records task status" {
				var task model.TaskConfig
				if err := db.Where("task_name = ?", "sec_filing_sync").First(&task).Error; err != nil {
					t.Fatalf("load task: %v", err)
				}
				if task.LastRunAt == nil {
					t.Fatalf("LastRunAt is nil")
				}
				if task.Running {
					t.Fatalf("Running = true, want false after completion")
				}
			}
			if tt.name == "run ipo radar task" {
				var count int64
				if err := db.Model(&model.IPOFiling{}).Count(&count).Error; err != nil {
					t.Fatalf("count ipo filings: %v", err)
				}
				if count != 1 {
					t.Fatalf("ipo filings = %d, want 1", count)
				}
				var run model.SyncRun
				if err := db.Order("id DESC").First(&run).Error; err != nil {
					t.Fatalf("load sync run: %v", err)
				}
				if run.Trigger != "ipo_scheduler" || run.NewFilings != 1 || run.Status != "success" {
					t.Fatalf("sync run = %+v, want ipo_scheduler success with one new filing", run)
				}
			}
			if tt.name == "run candidate notification task" {
				var batch model.NotificationBatch
				if err := db.Where("source = ?", "candidate").First(&batch).Error; err != nil {
					t.Fatalf("load candidate batch: %v", err)
				}
				if batch.Status != "sent" || batch.SentCount != 1 {
					t.Fatalf("candidate batch = %+v", batch)
				}
			}
		})
	}
}

func TestSchedulerReloadPersistsNextRunPlan(t *testing.T) {
	db := testDB(t)
	seed := []model.TaskConfig{
		{TaskName: "sec_filing_sync", CronExpr: "0 7 * * 2-6", Enabled: true},
		{TaskName: "ipo_radar_sync", CronExpr: "0 7 * * *", Enabled: false},
	}
	if err := db.Create(&seed).Error; err != nil {
		t.Fatalf("seed tasks: %v", err)
	}
	audit := service.NewAuditService(db)
	configs := service.NewConfigService(db, audit)
	if err := configs.EnsureDefaults(context.Background()); err != nil {
		t.Fatalf("ensure defaults: %v", err)
	}
	tasks := service.NewTaskConfigService(db, audit)
	filings := service.NewFilingService(db, fakeSECClient{}, fakeNotifier{}, configs)
	sched := New(tasks, filings, configs)
	if err := sched.Reload(context.Background()); err != nil {
		t.Fatalf("reload: %v", err)
	}

	enabled, err := tasks.GetByTaskName(context.Background(), "sec_filing_sync")
	if err != nil {
		t.Fatalf("load enabled task: %v", err)
	}
	if enabled.NextRunAt == nil || !enabled.NextRunAt.After(time.Now().UTC()) {
		t.Fatalf("enabled task next run = %v, want a future timestamp", enabled.NextRunAt)
	}
	disabled, err := tasks.GetByTaskName(context.Background(), "ipo_radar_sync")
	if err != nil {
		t.Fatalf("load disabled task: %v", err)
	}
	if disabled.NextRunAt != nil {
		t.Fatalf("disabled task next run = %v, want nil", disabled.NextRunAt)
	}
}

func TestSchedulerReloadAfterStartRunsNewCron(t *testing.T) {
	db := testDB(t)
	if err := db.Create(&model.TaskConfig{TaskName: "sec_filing_sync", CronExpr: "@every 1s", Enabled: false}).Error; err != nil {
		t.Fatalf("seed task: %v", err)
	}
	audit := service.NewAuditService(db)
	configs := service.NewConfigService(db, audit)
	if err := configs.EnsureDefaults(context.Background()); err != nil {
		t.Fatalf("EnsureDefaults: %v", err)
	}
	tasks := service.NewTaskConfigService(db, audit)
	filings := service.NewFilingService(db, fakeSECClient{}, fakeNotifier{}, configs)
	sched := New(tasks, filings)
	if err := sched.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { <-sched.Stop().Done() }()

	if err := db.Model(&model.TaskConfig{}).Where("task_name = ?", "sec_filing_sync").Updates(map[string]any{
		"enabled": true,
	}).Error; err != nil {
		t.Fatalf("enable task: %v", err)
	}
	if err := sched.Reload(context.Background()); err != nil {
		t.Fatalf("Reload: %v", err)
	}

	deadline := time.After(2500 * time.Millisecond)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-deadline:
			var task model.TaskConfig
			_ = db.Where("task_name = ?", "sec_filing_sync").First(&task).Error
			t.Fatalf("task did not run after reload; LastRunAt=%v", task.LastRunAt)
		case <-ticker.C:
			var task model.TaskConfig
			if err := db.Where("task_name = ?", "sec_filing_sync").First(&task).Error; err != nil {
				t.Fatalf("load task: %v", err)
			}
			if task.LastRunAt != nil {
				return
			}
		}
	}
}

func TestSchedulerUsesConfiguredTimezone(t *testing.T) {
	db := testDB(t)
	audit := service.NewAuditService(db)
	configs := service.NewConfigService(db, audit)
	if err := configs.EnsureDefaults(context.Background()); err != nil {
		t.Fatalf("EnsureDefaults: %v", err)
	}
	if err := configs.UpsertMany(context.Background(), []service.ConfigInput{{Key: "scheduler.timezone", Value: "Asia/Shanghai", ValueType: "string", Category: "scheduler"}}, "test"); err != nil {
		t.Fatalf("timezone config: %v", err)
	}
	tasks := service.NewTaskConfigService(db, audit)
	filings := service.NewFilingService(db, fakeSECClient{}, fakeNotifier{}, configs)
	sched := New(tasks, filings, configs)
	location, err := sched.schedulerLocation(context.Background())
	if err != nil {
		t.Fatalf("schedulerLocation: %v", err)
	}
	if location.String() != "Asia/Shanghai" {
		t.Fatalf("location = %s, want Asia/Shanghai", location)
	}
}

func TestSchedulerAllowsDifferentTasksConcurrently(t *testing.T) {
	db := testDB(t)
	if err := db.Create([]model.TaskConfig{
		{TaskName: smallCapDiscoverySyncTaskName, CronExpr: "0 8 * * 1-5", Enabled: true},
		{TaskName: ipoRadarSyncTaskName, CronExpr: "*/30 * * * *", Enabled: true},
	}).Error; err != nil {
		t.Fatalf("seed tasks: %v", err)
	}
	audit := service.NewAuditService(db)
	configs := service.NewConfigService(db, audit)
	if err := configs.EnsureDefaults(context.Background()); err != nil {
		t.Fatalf("EnsureDefaults: %v", err)
	}
	tasks := service.NewTaskConfigService(db, audit)
	filings := service.NewFilingService(db, fakeSECClient{}, fakeNotifier{}, configs)
	ipo := service.NewIPORadarService(db, fakeSECClient{}, fakeNotifier{}, configs)
	discoveryDB := testDiscoveryDB(t)
	runner := &blockingDiscoveryRunner{started: make(chan struct{}, 2), unblock: make(chan struct{})}
	discoverySync := service.NewDiscoverySyncService(discoveryDB, config.DiscoveryConfig{}).WithRunner(runner)
	sched := New(tasks, filings, ipo, discoverySync)

	discoveryDone := make(chan error, 1)
	go func() { discoveryDone <- sched.RunTask(context.Background(), smallCapDiscoverySyncTaskName) }()
	select {
	case <-runner.started:
	case <-time.After(time.Second):
		t.Fatal("discovery task did not start")
	}

	ipoDone := make(chan error, 1)
	go func() { ipoDone <- sched.RunTask(context.Background(), ipoRadarSyncTaskName) }()
	select {
	case err := <-ipoDone:
		if err != nil {
			t.Fatalf("ipo task: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("ipo task was blocked by the running discovery task")
	}
	var ipoFilings int64
	if err := db.Model(&model.IPOFiling{}).Count(&ipoFilings).Error; err != nil {
		t.Fatalf("count ipo filings: %v", err)
	}
	if ipoFilings != 1 {
		t.Fatalf("ipo filings = %d, want 1 before discovery unblocks", ipoFilings)
	}

	close(runner.unblock)
	if err := <-discoveryDone; err != nil {
		t.Fatalf("discovery task: %v", err)
	}
}

func TestSchedulerSuppressesDuplicateTaskRun(t *testing.T) {
	db := testDB(t)
	if err := db.Create(&model.TaskConfig{TaskName: smallCapDiscoverySyncTaskName, CronExpr: "0 8 * * 1-5", Enabled: true}).Error; err != nil {
		t.Fatalf("seed task: %v", err)
	}
	audit := service.NewAuditService(db)
	tasks := service.NewTaskConfigService(db, audit)
	filings := service.NewFilingService(db, fakeSECClient{}, fakeNotifier{}, nil)
	discoveryDB := testDiscoveryDB(t)
	runner := &blockingDiscoveryRunner{started: make(chan struct{}, 2), unblock: make(chan struct{})}
	discoverySync := service.NewDiscoverySyncService(discoveryDB, config.DiscoveryConfig{}).WithRunner(runner)
	sched := New(tasks, filings, discoverySync)

	firstDone := make(chan error, 1)
	go func() { firstDone <- sched.RunTask(context.Background(), smallCapDiscoverySyncTaskName) }()
	select {
	case <-runner.started:
	case <-time.After(time.Second):
		t.Fatal("first discovery task did not start")
	}

	duplicateDone := make(chan error, 1)
	go func() { duplicateDone <- sched.RunTask(context.Background(), smallCapDiscoverySyncTaskName) }()
	select {
	case err := <-duplicateDone:
		if !errors.Is(err, service.ErrTaskAlreadyRunning) {
			t.Fatalf("duplicate task error = %v, want ErrTaskAlreadyRunning", err)
		}
	case <-time.After(time.Second):
		close(runner.unblock)
		<-firstDone
		t.Fatal("duplicate task did not return while the first task was running")
	}
	if calls := runner.securityCalls(); calls != 1 {
		t.Fatalf("discovery security calls = %d, want 1", calls)
	}

	close(runner.unblock)
	if err := <-firstDone; err != nil {
		t.Fatalf("first task: %v", err)
	}
}

func TestSchedulerSkipsScheduledIPORadarAfterRecentManualSuccess(t *testing.T) {
	db := testDB(t)
	if err := db.Create(&model.TaskConfig{TaskName: ipoRadarSyncTaskName, CronExpr: "*/30 * * * *", Enabled: true}).Error; err != nil {
		t.Fatalf("seed task: %v", err)
	}
	audit := service.NewAuditService(db)
	configs := service.NewConfigService(db, audit)
	if err := configs.EnsureDefaults(context.Background()); err != nil {
		t.Fatalf("EnsureDefaults: %v", err)
	}
	tasks := service.NewTaskConfigService(db, audit)
	finishedAt := time.Now().UTC().Add(-time.Minute)
	if err := db.Create(&model.TaskExecution{TaskName: ipoRadarSyncTaskName, Trigger: "manual", Status: "success", StartedAt: finishedAt.Add(-time.Second), FinishedAt: &finishedAt}).Error; err != nil {
		t.Fatalf("seed manual execution: %v", err)
	}
	sched := New(tasks, service.NewFilingService(db, fakeSECClient{}, fakeNotifier{}, configs), service.NewIPORadarService(db, fakeSECClient{}, fakeNotifier{}, configs))
	if err := sched.runTaskWithTrigger(context.Background(), ipoRadarSyncTaskName, "scheduled"); err != nil {
		t.Fatalf("scheduled IPO task: %v", err)
	}
	var filings int64
	if err := db.Model(&model.IPOFiling{}).Count(&filings).Error; err != nil {
		t.Fatal(err)
	}
	if filings != 0 {
		t.Fatalf("IPO filings = %d, want 0 because scheduled scan should be skipped", filings)
	}
	var execution model.TaskExecution
	if err := db.Where("task_name = ? AND trigger = ?", ipoRadarSyncTaskName, "scheduled").Order("id DESC").First(&execution).Error; err != nil {
		t.Fatal(err)
	}
	if execution.Status != "skipped" {
		t.Fatalf("scheduled execution status = %q, want skipped", execution.Status)
	}
}

func TestSchedulerSerializesLiveSECTasks(t *testing.T) {
	db := testDB(t)
	if err := db.Create([]model.TaskConfig{
		{TaskName: secFilingSyncTaskName, CronExpr: "*/5 * * * *", Enabled: true},
		{TaskName: ipoRadarSyncTaskName, CronExpr: "*/30 * * * *", Enabled: true},
	}).Error; err != nil {
		t.Fatalf("seed tasks: %v", err)
	}
	audit := service.NewAuditService(db)
	configs := service.NewConfigService(db, audit)
	if err := configs.EnsureDefaults(context.Background()); err != nil {
		t.Fatalf("EnsureDefaults: %v", err)
	}
	tasks := service.NewTaskConfigService(db, audit)
	blockingClient := blockingIPOSECClient{started: make(chan struct{}, 1), unblock: make(chan struct{})}
	filings := service.NewFilingService(db, fakeSECClient{}, fakeNotifier{}, configs)
	ipo := service.NewIPORadarService(db, blockingClient, fakeNotifier{}, configs)
	sched := New(tasks, filings, configs, ipo)

	ipoDone := make(chan error, 1)
	go func() { ipoDone <- sched.RunTask(context.Background(), ipoRadarSyncTaskName) }()
	select {
	case <-blockingClient.started:
	case <-time.After(time.Second):
		t.Fatal("IPO task did not start SEC request")
	}

	err := sched.RunTask(context.Background(), secFilingSyncTaskName)
	if !errors.Is(err, service.ErrTaskResourceBusy) {
		t.Fatalf("SEC filing task error = %v, want ErrTaskResourceBusy", err)
	}

	close(blockingClient.unblock)
	if err := <-ipoDone; err != nil {
		t.Fatalf("IPO task: %v", err)
	}
}

func TestSchedulerRunsDueNotificationRetries(t *testing.T) {
	db := testDB(t)
	if err := db.Create(&model.TaskConfig{TaskName: "notification_retry_sync", CronExpr: "*/10 * * * *", Enabled: true}).Error; err != nil {
		t.Fatalf("seed task: %v", err)
	}
	dueAt := time.Now().UTC().Add(-time.Minute)
	batch := model.NotificationBatch{Source: "filing", Channel: "telegram", Status: "failed", ItemCount: 1, FailedCount: 1, RetryCount: 1, NextRetryAt: &dueAt}
	if err := db.Create(&batch).Error; err != nil {
		t.Fatalf("seed batch: %v", err)
	}
	if err := db.Create(&model.NotificationBatchItem{BatchID: batch.ID, EntityKind: "filing", FilingID: "retry-1", Ticker: "RETRY", Status: "failed", Reason: "delivery_failed"}).Error; err != nil {
		t.Fatalf("seed batch item: %v", err)
	}
	audit := service.NewAuditService(db)
	configs := service.NewConfigService(db, audit)
	notifier := &countingNotifier{}
	tasks := service.NewTaskConfigService(db, audit)
	filings := service.NewFilingService(db, fakeSECClient{}, notifier, configs)
	batches := service.NewNotificationBatchService(db, notifier, configs)
	sched := New(tasks, filings, batches)

	if err := sched.RunTask(context.Background(), "notification_retry_sync"); err != nil {
		t.Fatalf("run notification retry task: %v", err)
	}
	if notifier.sendCount() != 1 {
		t.Fatalf("notification sends = %d, want 1", notifier.sendCount())
	}
	if err := db.First(&batch, batch.ID).Error; err != nil {
		t.Fatalf("load batch: %v", err)
	}
	if batch.Status != "sent" || batch.SentCount != 1 {
		t.Fatalf("batch after retry = %+v, want sent batch", batch)
	}
}

func TestSchedulerMarksDisabledCandidateNotificationAsSkipped(t *testing.T) {
	db := testDB(t)
	if err := db.Create(&model.TaskConfig{TaskName: candidateNotificationSyncTaskName, CronExpr: "30 9 * * *", Enabled: true}).Error; err != nil {
		t.Fatalf("seed task: %v", err)
	}
	audit := service.NewAuditService(db)
	configs := service.NewConfigService(db, audit)
	if err := configs.EnsureDefaults(context.Background()); err != nil {
		t.Fatalf("ensure defaults: %v", err)
	}
	discoveryDB := testDiscoveryDB(t)
	tasks := service.NewTaskConfigService(db, audit)
	filings := service.NewFilingService(db, fakeSECClient{}, fakeNotifier{}, configs)
	candidateNotifications := service.NewCandidateNotificationService(db, discoveryDB, fakeNotifier{}, configs)
	sched := New(tasks, filings, candidateNotifications)

	if err := sched.RunTask(context.Background(), candidateNotificationSyncTaskName); err != nil {
		t.Fatalf("run disabled notification task: %v", err)
	}
	var task model.TaskConfig
	if err := db.Where("task_name = ?", candidateNotificationSyncTaskName).First(&task).Error; err != nil {
		t.Fatalf("load task: %v", err)
	}
	if task.Running || task.LastStatus != "skipped" || task.ConsecutiveFailures != 0 || task.LastErrorMessage == "" {
		t.Fatalf("task=%+v, want persisted skipped outcome without failures", task)
	}
}

func nowUTC() time.Time {
	return time.Now().UTC()
}

func TestSchedulerRetriesDailyTaskWithoutWaitingForCron(t *testing.T) {
	db := testDB(t)
	due := time.Now().UTC().Add(-time.Minute)
	if err := db.Create(&model.TaskConfig{TaskName: secFilingSyncTaskName, CronExpr: "0 0 * * *", Enabled: true, LastStatus: "failed", RetryNotBefore: &due}).Error; err != nil {
		t.Fatal(err)
	}
	configs := service.NewConfigService(db, service.NewAuditService(db))
	tasks := service.NewTaskConfigService(db, nil)
	sched := New(tasks, service.NewFilingService(db, fakeSECClient{}, fakeNotifier{}, configs))
	if err := sched.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	sched.runDueRetries(context.Background(), time.Now().UTC())
	var execution model.TaskExecution
	if err := db.Where("task_name = ?", secFilingSyncTaskName).First(&execution).Error; err != nil {
		t.Fatal(err)
	}
	if execution.Trigger != "retry" || execution.Status != "success" {
		t.Fatalf("retry execution: %+v", execution)
	}
	sched.runDueRetries(context.Background(), time.Now().UTC())
	var count int64
	db.Model(&model.TaskExecution{}).Count(&count)
	if count != 1 {
		t.Fatalf("duplicate retry executed: %d", count)
	}
}

func TestSchedulerPersistsOutcomeAfterCancellation(t *testing.T) {
	db := testDB(t)
	if err := db.Create(&model.TaskConfig{TaskName: ipoRadarSyncTaskName, CronExpr: "0 0 * * *", Enabled: true}).Error; err != nil {
		t.Fatal(err)
	}
	configs := service.NewConfigService(db, service.NewAuditService(db))
	client := blockingIPOSECClient{started: make(chan struct{}, 1), unblock: make(chan struct{})}
	if err := configs.EnsureDefaults(context.Background()); err != nil {
		t.Fatal(err)
	}
	ipo := service.NewIPORadarService(db, client, fakeNotifier{}, configs)
	sched := New(service.NewTaskConfigService(db, nil), nil, ipo)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- sched.RunTask(ctx, ipoRadarSyncTaskName) }()
	select {
	case <-client.started:
	case <-time.After(5 * time.Second):
		t.Fatal("task did not start")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("task did not finish")
	}
	var task model.TaskConfig
	db.First(&task)
	if task.Running || task.LastStatus != "failed" || task.RetryNotBefore == nil {
		t.Fatalf("canceled task left stale status: %+v", task)
	}
	var execution model.TaskExecution
	db.First(&execution)
	if execution.Status != "failed" || execution.FinishedAt == nil {
		t.Fatalf("canceled execution not finalized: %+v", execution)
	}
}

func TestResearchWarningsDoNotCauseUnboundedRetries(t *testing.T) {
	var partial *service.TaskPartialError
	if err := researchPartialOutcome(5, 5, 0, []string{"EPS 暂无覆盖"}); !errors.As(err, &partial) || partial.Retryable {
		t.Fatalf("coverage warning retried: %v", err)
	}
	if err := researchPartialOutcome(4, 5, 1, []string{"TEST timeout"}); !errors.As(err, &partial) || !partial.Retryable || partial.PendingCount == nil || *partial.PendingCount != 1 {
		t.Fatalf("missing retry: %v", err)
	}
}
