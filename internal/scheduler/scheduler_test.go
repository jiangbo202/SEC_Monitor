package scheduler

import (
	"context"
	"testing"
	"time"

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
		&model.IPOFiling{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
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
			err := tt.run(context.Background(), New(tasks, filings, ipoRadar))
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
			}
		})
	}
}

func nowUTC() time.Time {
	return time.Now().UTC()
}
