package scheduler

import (
	"context"
	"testing"
	"time"

	"sec_monitor/internal/discovery"
	"sec_monitor/internal/model"
	"sec_monitor/internal/sec"
	"sec_monitor/internal/service"
	"sec_monitor/internal/telegram"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
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

func testDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(
		&model.WatchTarget{}, &model.Filing{}, &model.SyncRun{}, &model.SyncRunDetail{}, &model.TaskConfig{},
		&model.SystemConfig{}, &model.OperationLog{}, &model.NotificationLog{},
		&model.NotificationBatch{}, &model.NotificationBatchItem{},
		&model.IPOFiling{}, &model.IPOCompanyOverride{}, &model.IPOCompanyMarketData{}, &model.IPOOfferingEvent{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
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

func nowUTC() time.Time {
	return time.Now().UTC()
}
