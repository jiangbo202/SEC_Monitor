package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"sec_monitor/internal/discovery"
	"sec_monitor/internal/model"
	"sec_monitor/internal/service"

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
	if got := recorder.Header().Get("X-Dashboard-Cache"); got != "miss" {
		t.Fatalf("first request cache=%q", got)
	}

	cachedRecorder := httptest.NewRecorder()
	r.ServeHTTP(cachedRecorder, httptest.NewRequest(http.MethodGet, "/dashboard-summary", nil))
	if got := cachedRecorder.Header().Get("X-Dashboard-Cache"); got != "hit" {
		t.Fatalf("second request cache=%q", got)
	}

	forcedRecorder := httptest.NewRecorder()
	r.ServeHTTP(forcedRecorder, httptest.NewRequest(http.MethodGet, "/dashboard-summary?refresh=1", nil))
	if got := forcedRecorder.Header().Get("X-Dashboard-Cache"); got != "miss" {
		t.Fatalf("forced request cache=%q", got)
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

func TestDashboardCandidateGateSeparatesFallbackPriceFromUsableCandidate(t *testing.T) {
	item := discovery.CandidateScoreResult{
		CandidateScoreSnapshot: discovery.CandidateScoreSnapshot{Ticker: "TEST", MarketCapUSD: 120_000_000},
		PriceCloseUSD:          4.25,
		PriceFreshnessStatus:   discovery.PriceFreshnessPreviousTradingDay,
		PriceQualityStatus:     discovery.QualityStatusValid,
		ResearchReadiness:      discovery.CandidateResearchReadiness{Status: discovery.CandidateResearchReadinessReady},
	}
	reason, action := dashboardCandidateGate(item, false, "")
	if reason != "行情仅到前一交易日" || action != "补齐最近完成交易日的有效收盘价" {
		t.Fatalf("gate=(%q, %q)", reason, action)
	}
}

func TestDashboardCandidateGateRequiresCurrentEntryPlanForTradeReady(t *testing.T) {
	item := discovery.CandidateScoreResult{
		CandidateScoreSnapshot: discovery.CandidateScoreSnapshot{Ticker: "TEST", MarketCapUSD: 120_000_000},
		PriceCloseUSD:          4.25, PriceFreshnessStatus: discovery.PriceFreshnessCurrent,
		PriceQualityStatus: discovery.QualityStatusValid,
		ResearchReadiness:  discovery.CandidateResearchReadiness{Status: discovery.CandidateResearchReadinessReady},
	}
	reason, _ := dashboardCandidateGate(item, true, discovery.TradeSetupInvalidated)
	if reason != "研究证据可用，但原交易计划已失效" {
		t.Fatalf("reason=%q", reason)
	}
	reason, _ = dashboardCandidateGate(item, true, discovery.TradeSetupEntryCandidate)
	if reason != "研究证据与入场计划均已就绪" {
		t.Fatalf("reason=%q", reason)
	}
}

func TestDashboardOperationalIPOFaultDoesNotBelongToDecisionEvidence(t *testing.T) {
	issue := service.OperationalIssue{Key: "task_failed:ipo_radar_sync", Severity: "critical", Title: "调度任务失败", Detail: "raw provider error", Action: "scheduler"}
	if got := dashboardOperationalEvidenceDomain(issue); got != "ipo" {
		t.Fatalf("domain=%q", got)
	}
	summary := dashboardOperationalIssueSummary(issue)
	if strings.Contains(summary.Detail, "raw provider error") || !strings.Contains(summary.Detail, "普通候选研究") {
		t.Fatalf("summary=%+v", summary)
	}
}

func TestDashboardOperationalReportSummaryHidesRawProviderError(t *testing.T) {
	report := dashboardOperationalReportSummary(service.OperationalReport{
		Status: "critical",
		Issues: []service.OperationalIssue{{
			Key: "task_failed:ipo_radar_sync", Severity: "critical", Title: "调度任务失败",
			Detail: `Get "https://www.sec.gov": context deadline exceeded trace_id=secret`,
		}},
	})
	if strings.Contains(report.Summary, "https://") || strings.Contains(report.Issues[0].Detail, "trace_id") {
		t.Fatalf("raw technical error leaked: summary=%q issue=%+v", report.Summary, report.Issues[0])
	}
	if report.Issues[0].Title != "IPO 新申报扫描异常" {
		t.Fatalf("title=%q", report.Issues[0].Title)
	}
}

func TestDashboardActionWorkflowMakesExitWorkExplicit(t *testing.T) {
	priority, action, due := dashboardActionWorkflow(discovery.TradeSetupExitWarning)
	if priority != "high" || action == "" || due != "开盘前" {
		t.Fatalf("workflow=(%q, %q, %q)", priority, action, due)
	}
}
