package scheduler

import (
	"context"
	"errors"
	"testing"
	"time"

	"sec_monitor/internal/config"
	"sec_monitor/internal/discovery"
	"sec_monitor/internal/model"
	"sec_monitor/internal/service"
)

func TestSchedulerFullSyncReportsPersistedIncrementalLease(t *testing.T) {
	db := testDB(t)
	if err := db.Create(&model.TaskConfig{TaskName: smallCapDiscoveryFullSyncTaskName, CronExpr: "0 8 * * 6", Enabled: true}).Error; err != nil {
		t.Fatal(err)
	}
	discoveryDB := testDiscoveryDB(t)
	active := discovery.DiscoverySyncRun{Kind: "incremental", Status: "running", Phase: "market_prescreen", StartedAt: time.Now(), UpdatedAt: time.Now()}
	if err := discoveryDB.Create(&active).Error; err != nil {
		t.Fatal(err)
	}
	tasks := service.NewTaskConfigService(db, service.NewAuditService(db))
	syncer := service.NewDiscoverySyncService(discoveryDB, config.DiscoveryConfig{})
	sched := New(tasks, service.NewFilingService(db, fakeSECClient{}, fakeNotifier{}, nil), syncer)

	err := sched.RunTask(context.Background(), smallCapDiscoveryFullSyncTaskName)
	if !errors.Is(err, service.ErrTaskAlreadyRunning) {
		t.Fatalf("RunTask err=%v, want ErrTaskAlreadyRunning", err)
	}
}
