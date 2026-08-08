package discovery

import (
	"context"
	"testing"
	"time"
)

func TestLatestDiscoverySyncRunReturnsNewestAndAllowsEmptyState(t *testing.T) {
	db := openMigratedTestDatabase(t)
	if run, err := LatestDiscoverySyncRun(context.Background(), db); err != nil || run.ID != 0 {
		t.Fatalf("empty latest run = %#v, %v", run, err)
	}
	older := DiscoverySyncRun{Kind: "full", Status: "published", Phase: "completed", StartedAt: time.Date(2026, 7, 18, 1, 0, 0, 0, time.UTC)}
	newer := DiscoverySyncRun{Kind: "market", Status: "running", Phase: "market_prescreen", StartedAt: time.Date(2026, 7, 18, 2, 0, 0, 0, time.UTC)}
	if err := db.Create(&older).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&newer).Error; err != nil {
		t.Fatal(err)
	}
	run, err := LatestDiscoverySyncRun(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	if run.ID != newer.ID || run.Kind != "market" || run.Status != "running" {
		t.Fatalf("latest run = %#v", run)
	}
}
