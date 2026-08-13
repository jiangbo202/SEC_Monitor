package discovery

import (
	"context"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestTickerEvaluationBuildAndHistory(t *testing.T) {
	asOf := time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC)
	prices := make([]PriceSnapshot, 0, 25)
	for index := 0; index < 25; index++ {
		prices = append(prices, PriceSnapshot{Symbol: "TEST", Source: "test", SourceVersion: "v1", TradeDate: asOf.AddDate(0, 0, -24+index), CloseMicros: int64(10_000_000 + index*100_000), Volume: 300_000, Currency: "USD", QualityStatus: QualityStatusValid})
	}
	result := BuildTickerEvaluationResult("test", "0000000001", "Test Inc", "stock", CandidateScoreSnapshot{Ticker: "TEST", TotalScore: 70, Grade: CandidateGradeB, RevenueGrowthPct: 30, CashRunwayMonths: 18}, FinancialMetricSnapshot{RevenueGrowthAvailable: true, QuarterlyRevenueYoYPct: 30, QualityFlagsJSON: "[]"}, nil, prices, asOf, "available", nil)
	result.Research = TickerEvaluationResearchSnapshot{
		Profile:        CompanyProfile{Ticker: "TEST", CompanyName: "Test Inc", BusinessSummary: "test profile", SummarySource: "test", Status: "available"},
		AnalystRating:  AnalystRatingView{Message: "no coverage", History: []AnalystRatingSnapshot{}},
		MarketResearch: CandidateMarketResearch{EPSForecast: EPSForecastView{History: []EPSForecastSnapshot{}}, InstitutionalHolders: []InstitutionalHolderSnapshot{{HolderName: "Fund", ReportDate: "2026-06-30"}}, FundHolders: []FundHolderSnapshot{}},
		Sources:        []TickerEvaluationResearchSource{{Name: "test", Status: "available"}},
		RefreshNotes:   []string{"partial coverage"},
	}
	if result.CandidateScore.ReviewPriorityScore == 0 {
		t.Fatalf("review score should reuse candidate quality logic")
	}
	if result.CandidateScore.Technical.Status != TechnicalStatusReady {
		t.Fatalf("technical status = %q", result.CandidateScore.Technical.Status)
	}
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&TickerEvaluationSnapshot{}); err != nil {
		t.Fatal(err)
	}
	saved, err := SaveTickerEvaluation(context.Background(), db, result)
	if err != nil {
		t.Fatal(err)
	}
	if saved.ID == 0 {
		t.Fatal("saved id is empty")
	}
	page, err := ListTickerEvaluations(context.Background(), db, TickerEvaluationFilter{Ticker: "TEST", Page: 1, PageSize: 20})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 || len(page.Items) != 1 || page.Items[0].Ticker != "TEST" {
		t.Fatalf("unexpected history: %+v", page)
	}
	if page.Items[0].Research.Profile.BusinessSummary != "test profile" || len(page.Items[0].Research.MarketResearch.InstitutionalHolders) != 1 {
		t.Fatalf("research snapshot was not persisted: %+v", page.Items[0].Research)
	}
}

func TestTickerEvaluationHistoryBackfillsResolvedETFDisplayName(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&TickerEvaluationSnapshot{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	now := time.Date(2026, 8, 12, 5, 0, 0, 0, time.UTC)
	blank := BuildTickerEvaluationResult("EWY", "", "", "etf", CandidateScoreSnapshot{Ticker: "EWY"}, FinancialMetricSnapshot{}, nil, nil, now, "not_applicable", nil)
	if _, err := SaveTickerEvaluation(context.Background(), db, blank); err != nil {
		t.Fatalf("save unnamed ETF: %v", err)
	}
	named := BuildTickerEvaluationResult("EWY", "0000000001", "iShares MSCI South Korea ETF", "etf", CandidateScoreSnapshot{Ticker: "EWY"}, FinancialMetricSnapshot{}, nil, nil, now.Add(time.Minute), "not_applicable", nil)
	if _, err := SaveTickerEvaluation(context.Background(), db, named); err != nil {
		t.Fatalf("save named ETF: %v", err)
	}
	page, err := ListTickerEvaluations(context.Background(), db, TickerEvaluationFilter{Ticker: "EWY", Page: 1, PageSize: 20})
	if err != nil || len(page.Items) != 2 {
		t.Fatalf("history=%+v err=%v", page, err)
	}
	for _, item := range page.Items {
		if item.CompanyName != "iShares MSCI South Korea ETF" {
			t.Fatalf("company_name=%q, want resolved ETF name", item.CompanyName)
		}
	}
}

func TestTickerEvaluationHistorySortsAndFiltersPersistedSnapshotFields(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&TickerEvaluationSnapshot{}); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 12, 0, 0, 0, 0, time.UTC)
	low := TickerEvaluationResult{Ticker: "LOW", Status: TickerEvaluationStatusReady, EvaluatedAt: now, CandidateScore: CandidateScoreResult{CandidateScoreSnapshot: CandidateScoreSnapshot{Ticker: "LOW", TotalScore: 65}, ReviewPriorityScore: 22, Technical: CandidateTechnicalAnalysis{Status: TechnicalStatusReady, DistanceToMA20Pct: -2, DistanceTo20DayHighPct: -8, TradeSetup: CandidateTradeSetup{EntryTrigger: "突破 20 日高点"}}}}
	high := TickerEvaluationResult{Ticker: "HIGH", Status: TickerEvaluationStatusReady, EvaluatedAt: now.Add(time.Minute), CandidateScore: CandidateScoreResult{CandidateScoreSnapshot: CandidateScoreSnapshot{Ticker: "HIGH", TotalScore: 82}, ReviewPriorityScore: 58, Technical: CandidateTechnicalAnalysis{Status: TechnicalStatusReady, DistanceToMA20Pct: 3, DistanceTo20DayHighPct: -1, TradeSetup: CandidateTradeSetup{EntryTrigger: "站稳 MA20"}}}}
	for _, item := range []TickerEvaluationResult{low, high} {
		if _, err := SaveTickerEvaluation(context.Background(), db, item); err != nil {
			t.Fatal(err)
		}
	}
	page, err := ListTickerEvaluations(context.Background(), db, TickerEvaluationFilter{SortBy: "fundamental", SortOrder: "desc", Page: 1, PageSize: 1})
	if err != nil || page.Total != 2 || len(page.Items) != 1 || page.Items[0].Ticker != "HIGH" {
		t.Fatalf("fundamental sort page=%+v err=%v", page, err)
	}
	page, err = ListTickerEvaluations(context.Background(), db, TickerEvaluationFilter{EntryTrigger: "突破 20 日高点", SortBy: "distance_to_ma20", SortOrder: "asc", Page: 1, PageSize: 20})
	if err != nil || page.Total != 1 || len(page.Items) != 1 || page.Items[0].Ticker != "LOW" {
		t.Fatalf("entry filter page=%+v err=%v", page, err)
	}
	triggers, err := ListTickerEvaluationEntryTriggers(context.Background(), db, "")
	if err != nil || len(triggers) != 2 || triggers[0] != "突破 20 日高点" || triggers[1] != "站稳 MA20" {
		t.Fatalf("entry triggers=%+v err=%v", triggers, err)
	}
	blank := low
	blank.Ticker = "BLANK"
	blank.CandidateScore.Ticker = "BLANK"
	blank.CandidateScore.Technical.TradeSetup.EntryTrigger = ""
	if _, err := SaveTickerEvaluation(context.Background(), db, blank); err != nil {
		t.Fatal(err)
	}
	triggers, err = ListTickerEvaluationEntryTriggers(context.Background(), db, "BLANK")
	if err != nil || len(triggers) != 1 || triggers[0] != "等待触发条件" {
		t.Fatalf("fallback entry triggers=%+v err=%v", triggers, err)
	}
	page, err = ListTickerEvaluations(context.Background(), db, TickerEvaluationFilter{Ticker: "BLANK", EntryTrigger: "等待触发条件", Page: 1, PageSize: 20})
	if err != nil || page.Total != 1 || page.Items[0].Ticker != "BLANK" {
		t.Fatalf("fallback entry filter page=%+v err=%v", page, err)
	}
}
