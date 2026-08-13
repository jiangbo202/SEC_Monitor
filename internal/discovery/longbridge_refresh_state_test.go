package discovery

import (
	"context"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestFreshLongbridgeResearchTickersUsesShanghaiDayAndSuccessOnly(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&LongbridgeResearchRefreshState{}); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 11, 1, 0, 0, 0, time.UTC) // 09:00 Shanghai
	if err := MarkLongbridgeResearchSuccess(context.Background(), db, LongbridgeRefreshFamilyMarketResearch, "nvda", now); err != nil {
		t.Fatal(err)
	}
	prior := now.Add(-24 * time.Hour)
	if err := db.Create(&LongbridgeResearchRefreshState{Ticker: "OLD", Family: LongbridgeRefreshFamilyMarketResearch, LastAttemptAt: prior, LastSuccessAt: &prior, Status: "success"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&LongbridgeResearchRefreshState{Ticker: "FAILED", Family: LongbridgeRefreshFamilyMarketResearch, LastAttemptAt: now, Status: "failed"}).Error; err != nil {
		t.Fatal(err)
	}
	fresh, err := FreshLongbridgeResearchTickers(context.Background(), db, LongbridgeRefreshFamilyMarketResearch, now)
	if err != nil {
		t.Fatal(err)
	}
	if !fresh["NVDA"] || fresh["OLD"] || fresh["FAILED"] {
		t.Fatalf("fresh = %#v", fresh)
	}
}
