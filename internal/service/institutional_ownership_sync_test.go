package service

import (
	"context"
	"sec_monitor/internal/config"
	"sec_monitor/internal/discovery"
	"sec_monitor/internal/model"
	"testing"
)

func TestOwnershipQueueDisabledAndFailedIssuerRotation(t *testing.T) {
	db, main := testDiscoveryDB(t), testDB(t)
	if err := db.AutoMigrate(&discovery.LongbridgeResearchRefreshState{}); err != nil {
		t.Fatal(err)
	}
	for _, ticker := range []string{"AAA", "BBB", "CCC", "DDD", "EEE"} {
		if err := main.Create(&model.WatchTarget{Ticker: ticker, TargetType: "stock", Status: "enabled"}).Error; err != nil {
			t.Fatal(err)
		}
	}
	service := NewDiscoverySyncService(db, config.DiscoveryConfig{}).WithWatchTargetDB(main)
	result, err := service.SyncInstitutionalOwnership(context.Background())
	if err != nil || !result.Skipped || result.Attempted != 0 {
		t.Fatalf("disabled result=%+v err=%v", result, err)
	}
	// No credentials: fail before any external request. Failures must still
	// advance the cursor so uncovered issuers do not monopolize the queue.
	service.cfg.LongbridgeWatchTargetResearchEnabled = true
	service.cfg.LongbridgeWatchTargetResearchRequestBudget = 5
	for _, want := range []int{2, 2, 1, 0} {
		result, err = service.SyncInstitutionalOwnership(context.Background())
		if err != nil || result.Attempted != want || result.Failed != want {
			t.Fatalf("rotation want=%d result=%+v err=%v", want, result, err)
		}
	}
}
