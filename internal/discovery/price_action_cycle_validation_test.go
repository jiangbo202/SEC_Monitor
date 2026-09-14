package discovery

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func priceActionValidationDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&PriceSnapshot{}, &Listing{}, &Security{}, &CapitalRiskSnapshot{}, &CandidateScoreSnapshot{}, &UniverseBatch{}, &PriceActionReplayEvent{}, &PriceActionCycleSetting{}, &PriceActionPhaseSnapshot{}, &PriceActionEffectivenessSnapshot{}); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestReplayPriceActionCycleHistoryIsPointInTimeAndIdempotent(t *testing.T) {
	db := priceActionValidationDB(t)
	base := time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC)
	rows := make([]PriceSnapshot, 0, 280)
	for day := 0; day < 140; day++ {
		date := base.AddDate(0, 0, day)
		for _, symbol := range []string{"TEST", "IWM"} {
			close := 10.0 + float64(day)*0.05
			if symbol == "IWM" {
				close = 200 + float64(day)*0.1
			}
			micros := int64(close * 1_000_000)
			rows = append(rows, PriceSnapshot{Source: "test", SourceVersion: "v1", Symbol: symbol, TradeDate: date, OpenMicros: micros - 10_000, HighMicros: micros + 30_000, LowMicros: micros - 30_000, CloseMicros: micros, Volume: 1_000_000, Currency: "USD", Adjusted: true, QualityStatus: QualityStatusValid})
		}
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	result, err := ReplayPriceActionCycleHistory(context.Background(), db, []PriceActionReplayScope{{Ticker: "TEST", Source: "candidate"}}, base.AddDate(0, 0, 150))
	if err != nil {
		t.Fatal(err)
	}
	if result.EventCount == 0 || result.ProfileCount != 3 || result.TimelineCount == 0 || result.TimelineCreated == 0 {
		t.Fatalf("unexpected replay result: %#v", result)
	}
	timeline, err := GetPriceActionTimeline(context.Background(), db, "test", PriceActionCycleRuleVersion, "", "", false, 260)
	if err != nil {
		t.Fatal(err)
	}
	if timeline.Total != 91 || len(timeline.Items) != 91 || len(timeline.AvailableRuleVersions) != 3 {
		t.Fatalf("unexpected daily timeline: %#v", timeline)
	}
	latest := timeline.Items[len(timeline.Items)-1]
	if latest.CloseUSD <= 0 || latest.RSI14 == nil || latest.KDJ_K == nil || latest.KDJ_D == nil || latest.KDJ_J == nil || latest.EMA20USD <= 0 || latest.RuleVersion != PriceActionCycleRuleVersion {
		t.Fatalf("timeline context is incomplete: %#v", latest)
	}
	changes, err := GetPriceActionTimeline(context.Background(), db, "TEST", PriceActionCycleRuleVersion, "", "", true, 260)
	if err != nil {
		t.Fatal(err)
	}
	if changes.Total == 0 || changes.Total > timeline.Total {
		t.Fatalf("unexpected transition timeline: %#v", changes)
	}
	var cachedReports int64
	if err := db.Model(&PriceActionEffectivenessSnapshot{}).Count(&cachedReports).Error; err != nil || cachedReports != int64(len(PriceActionRuleProfiles())) {
		t.Fatalf("cached reports = %d, err = %v", cachedReports, err)
	}
	var firstCount int64
	db.Model(&PriceActionReplayEvent{}).Count(&firstCount)
	second, err := ReplayPriceActionCycleHistory(context.Background(), db, []PriceActionReplayScope{{Ticker: "TEST", Source: "candidate"}}, base.AddDate(0, 0, 151))
	if err != nil {
		t.Fatal(err)
	}
	if second.TimelineCreated != 0 {
		t.Fatalf("daily timeline replay must be idempotent: %#v", second)
	}
	var secondCount int64
	db.Model(&PriceActionReplayEvent{}).Count(&secondCount)
	if firstCount != secondCount {
		t.Fatalf("replay not idempotent: %d -> %d", firstCount, secondCount)
	}
}

func TestReplayPriceActionCycleHistoryMarksShortHistoryAsMissing(t *testing.T) {
	db := priceActionValidationDB(t)
	base := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	rows := make([]PriceSnapshot, 0, 109)
	for day := 0; day < 60; day++ {
		date := base.AddDate(0, 0, day)
		close := 200.0 + float64(day)
		micros := int64(close * 1_000_000)
		rows = append(rows, PriceSnapshot{Source: "test", SourceVersion: "v1", Symbol: "IWM", TradeDate: date, OpenMicros: micros - 10_000, HighMicros: micros + 30_000, LowMicros: micros - 30_000, CloseMicros: micros, Volume: 1_000_000, Currency: "USD", Adjusted: true, QualityStatus: QualityStatusValid})
		if day < 49 {
			rows = append(rows, PriceSnapshot{Source: "test", SourceVersion: "v1", Symbol: "SHORT", TradeDate: date, OpenMicros: micros - 10_000, HighMicros: micros + 30_000, LowMicros: micros - 30_000, CloseMicros: micros, Volume: 1_000_000, Currency: "USD", Adjusted: true, QualityStatus: QualityStatusValid})
		}
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	result, err := ReplayPriceActionCycleHistory(context.Background(), db, []PriceActionReplayScope{{Ticker: "SHORT", Source: "watch"}}, base.AddDate(0, 0, 61))
	if err != nil {
		t.Fatal(err)
	}
	if result.ProductStatus != "degraded" || result.CurrentCount != 0 || result.MissingCount != 1 || len(result.MissingTickers) != 1 || result.MissingTickers[0] != "SHORT" {
		t.Fatalf("short history must be explicitly gated: %#v", result)
	}
}

func TestPriceActionEffectivenessGatesResearchPriority(t *testing.T) {
	db := priceActionValidationDB(t)
	profile := priceActionProfile(PriceActionProfileConservative)
	for index := 0; index < priceActionMinimumSamples; index++ {
		value, benchmark, excess := 4.0, 1.0, 3.0
		outcomes, _ := json.Marshal([]PriceActionOutcome{{HorizonDays: 20, Status: "mature", OutcomeDate: "2026-06-30", ReturnPct: &value, BenchmarkReturnPct: &benchmark, ExcessReturnPct: &excess}})
		event := PriceActionReplayEvent{Ticker: fmt.Sprintf("T%02d", index), Source: "candidate", RuleVersion: profile.RuleVersion, SignalDate: fmt.Sprintf("2026-06-%02d", index%priceActionMinimumSignalDates+1), Phase: PriceActionPhaseBaseBreak, Confidence: 70, OutcomesJSON: string(outcomes), TransitionMature: true, TransitionSucceeded: true}
		if err := db.Create(&event).Error; err != nil {
			t.Fatal(err)
		}
	}
	report, err := BuildPriceActionEffectiveness(context.Background(), db, profile.Name)
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != "validated" || !report.CanInfluencePriority || report.BenchmarkCoveragePct != 100 {
		t.Fatalf("unexpected report: %#v", report)
	}
	updated, err := UpdatePriceActionCycleConfig(context.Background(), db, PriceActionProfileConservative, PriceActionProfileStandard)
	if err != nil {
		t.Fatal(err)
	}
	if updated.ActiveProfile != PriceActionProfileConservative || updated.PreviousProfile != PriceActionProfileStandard {
		t.Fatalf("unexpected config: %#v", updated)
	}
	rolledBack, err := RollbackPriceActionCycleConfig(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	if rolledBack.ActiveProfile != PriceActionProfileStandard {
		t.Fatalf("rollback failed: %#v", rolledBack)
	}
}

func TestPriceActionProfilePromotionRequiresValidation(t *testing.T) {
	db := priceActionValidationDB(t)
	_, err := UpdatePriceActionCycleConfig(context.Background(), db, PriceActionProfileSensitive, PriceActionProfileStandard)
	if err == nil {
		t.Fatal("expected unvalidated profile promotion to be blocked")
	}
}
