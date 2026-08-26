package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"sec_monitor/internal/discovery"
	"sec_monitor/internal/model"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestDashboardSummaryReadsLocalSnapshotsOnly(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mainDB, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open main database: %v", err)
	}
	if err := mainDB.AutoMigrate(&model.WatchTarget{}, &model.Filing{}, &model.EarningsPreview{}, &model.CandidateEarningsPreview{}, &model.MacroRelease{}); err != nil {
		t.Fatalf("migrate main database: %v", err)
	}
	discoveryDB, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open discovery database: %v", err)
	}
	if err := discoveryDB.AutoMigrate(&discovery.TradeSetupStatusEvent{}, &discovery.CurrentBatchPointer{}, &discovery.CandidateScoreSnapshot{}, &discovery.CandidateWatch{}); err != nil {
		t.Fatalf("migrate discovery database: %v", err)
	}
	target := model.WatchTarget{Ticker: "RKLB", CompanyName: "Rocket Lab", Status: "enabled", TargetType: "stock"}
	if err := mainDB.Create(&target).Error; err != nil {
		t.Fatalf("seed target: %v", err)
	}
	fetchedAt := time.Now().UTC()
	if err := mainDB.Create(&model.EarningsPreview{TargetID: target.ID, Ticker: target.Ticker, Provider: "longbridge", Status: "no_coverage", FetchedAt: &fetchedAt}).Error; err != nil {
		t.Fatalf("seed earnings coverage: %v", err)
	}
	if err := mainDB.Create(&model.Filing{FilingID: "dashboard-filing", Ticker: "RKLB", CompanyName: "Rocket Lab", FilingType: "8-K", FilingDate: time.Now().UTC(), PulledAt: time.Now().UTC()}).Error; err != nil {
		t.Fatalf("seed filing: %v", err)
	}

	h := &AppHandler{DB: mainDB, DiscoveryDB: discoveryDB}
	r := gin.New()
	r.GET("/dashboard-summary", h.GetDashboardSummary)
	recorder := httptest.NewRecorder()
	r.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/dashboard-summary", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Code int              `json:"code"`
		Data DashboardSummary `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Code != 0 || response.Data.Monitoring.EnabledTargets != 1 {
		t.Fatalf("unexpected summary: %+v", response)
	}
	if response.Data.Monitoring.EarningsCoverageStatus != "complete" || response.Data.Monitoring.EarningsCoveredTargets != 1 || response.Data.Monitoring.UpcomingEarnings != 0 {
		t.Fatalf("earnings coverage=%+v", response.Data.Monitoring)
	}
	if len(response.Data.Monitoring.RecentFilings) != 1 || response.Data.Monitoring.RecentFilings[0].Ticker != "RKLB" {
		t.Fatalf("recent filings=%+v", response.Data.Monitoring.RecentFilings)
	}
}

func TestDashboardFreshnessUsesTradingCalendarAcrossWeekend(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&discovery.MarketHoliday{}, &discovery.MarketCalendarYear{}); err != nil {
		t.Fatal(err)
	}
	if err := discovery.SeedDefaultNYSEMarketCalendar(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	lastFetched := time.Date(2026, 8, 21, 21, 45, 0, 0, time.UTC)
	now := time.Date(2026, 8, 23, 23, 30, 0, 0, time.UTC)
	got := dashboardDataFreshness(t.Context(), db, "2026-08-21", "longbridge", &lastFetched, now)
	if got.Status != "fresh" || got.ExpectedTradeDate != "2026-08-21" || got.QualityStatus != discovery.QualityStatusValid {
		t.Fatalf("freshness=%+v", got)
	}
}

func TestDashboardFreshnessExpiresAfterTwoMissedTradingSessions(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&discovery.MarketHoliday{}, &discovery.MarketCalendarYear{}); err != nil {
		t.Fatal(err)
	}
	if err := discovery.SeedDefaultNYSEMarketCalendar(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	lastFetched := time.Date(2026, 8, 20, 21, 45, 0, 0, time.UTC)
	now := time.Date(2026, 8, 24, 21, 0, 0, 0, time.UTC)
	got := dashboardDataFreshness(t.Context(), db, "2026-08-20", "longbridge", &lastFetched, now)
	if got.Status != "expired" || got.ExpectedTradeDate != "2026-08-24" {
		t.Fatalf("freshness=%+v", got)
	}
}
