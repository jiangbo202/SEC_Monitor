package discovery

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestBuildCandidateEffectivenessCalculatesCohortsAndOptionalBenchmark(t *testing.T) {
	db := openMigratedTestDatabase(t)
	base := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	securities := []Security{
		{CIK: "0000007101", CompanyName: "Alpha", CatalogStatus: SecurityCatalogPublished},
		{CIK: "0000007102", CompanyName: "Beta", CatalogStatus: SecurityCatalogPublished},
	}
	if err := db.Create(&securities).Error; err != nil {
		t.Fatal(err)
	}
	batch := UniverseBatch{BatchID: "effectiveness", Kind: BatchKindPrescreen, Status: BatchStatusPublished, StartedAt: base}
	if err := db.Create(&batch).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&[]CandidateScoreSnapshot{
		{BatchID: batch.BatchID, SecurityID: securities[0].ID, Ticker: "ALPH", Grade: CandidateGradeA, EligibleA: true},
		{BatchID: batch.BatchID, SecurityID: securities[1].ID, Ticker: "BETA", Grade: CandidateGradeB, EligibleB: true},
	}).Error; err != nil {
		t.Fatal(err)
	}
	prices := []PriceSnapshot{}
	for day := 0; day <= 20; day++ {
		date := base.AddDate(0, 0, day)
		prices = append(prices,
			PriceSnapshot{Source: "tiingo", SourceVersion: "alph", Symbol: "ALPH", TradeDate: date, CloseMicros: int64(1_000_000 + day*10_000), QualityStatus: QualityStatusValid},
			PriceSnapshot{Source: "tiingo", SourceVersion: "beta", Symbol: "BETA", TradeDate: date, CloseMicros: int64(1_000_000 - day*5_000), QualityStatus: QualityStatusValid},
			PriceSnapshot{Source: "tiingo", SourceVersion: "iwm", Symbol: "IWM", TradeDate: date, CloseMicros: int64(2_000_000 + day*2_000), QualityStatus: QualityStatusValid},
		)
	}
	if err := db.Create(&prices).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&[]UniverseSnapshot{
		{BatchID: batch.BatchID, SecurityID: securities[0].ID, Ticker: "ALPH", PriceSnapshotID: &prices[0].ID, QualityStatus: QualityStatusValid},
		{BatchID: batch.BatchID, SecurityID: securities[1].ID, Ticker: "BETA", PriceSnapshotID: &prices[1].ID, QualityStatus: QualityStatusValid},
	}).Error; err != nil {
		t.Fatal(err)
	}

	report, err := BuildCandidateEffectiveness(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	if !report.BenchmarkAvailable || report.BenchmarkTicker != "IWM" || len(report.Cohorts) != 3 {
		t.Fatalf("report metadata = %#v", report)
	}
	all := report.Cohorts[0]
	if all.Grade != "all" || all.CandidateCount != 2 || len(all.Windows) != 4 || all.Windows[3].HorizonDays != 60 || all.Windows[3].SampleCount != 0 {
		t.Fatalf("all cohort = %#v", all)
	}
	window20 := all.Windows[2]
	if window20.HorizonDays != 20 || window20.SampleCount != 2 || window20.AverageReturnPct == nil || *window20.AverageReturnPct < 4.9 || *window20.AverageReturnPct > 5.1 || window20.WinRatePct == nil || *window20.WinRatePct != 50 || window20.MaxDrawdownPct == nil || *window20.MaxDrawdownPct > -9.9 || window20.BenchmarkReturnPct == nil || window20.ExcessReturnPct == nil {
		t.Fatalf("20-day cohort = %#v", window20)
	}
	if report.Status != "validating" || window20.VerificationStatus != "validating" || window20.BenchmarkSampleCount != 2 || report.MinimumSampleCount != candidateEffectivenessMinimumSamples {
		t.Fatalf("verification state = report:%+v window:%+v", report, window20)
	}
	if report.ValidationHorizonDays != 20 || report.ValidationSampleCount != 2 || report.RemainingSampleCount != 28 || report.RemainingSignalDates != 4 || report.RemainingBenchmarkCount != 0 {
		t.Fatalf("validation progress = %+v", report)
	}
}

func TestBuildCandidateEffectivenessMarksBenchmarkUnavailableWithoutIWM(t *testing.T) {
	db := openMigratedTestDatabase(t)
	report, err := BuildCandidateEffectiveness(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	if report.BenchmarkAvailable || report.Status != "unverified" || len(report.Cohorts) != 3 || len(report.Cohorts[0].Windows) != 4 {
		t.Fatalf("empty report = %#v", report)
	}
}

func TestBuildCandidateEffectivenessRequiresIndependentSignalDates(t *testing.T) {
	db := openMigratedTestDatabase(t)
	base := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	batch := UniverseBatch{BatchID: "same-day-signals", Kind: BatchKindPrescreen, Status: BatchStatusPublished, EffectiveDate: "2026-07-21", StartedAt: base}
	if err := db.Create(&batch).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&CurrentBatchPointer{Kind: BatchKindPrescreen, BatchID: batch.BatchID}).Error; err != nil {
		t.Fatal(err)
	}
	prices := make([]PriceSnapshot, 0, 31*21)
	for day := 0; day <= 20; day++ {
		date := base.AddDate(0, 0, day)
		closeMicros := int64(2_000_000 + day*2_000)
		prices = append(prices, PriceSnapshot{Source: "test", SourceVersion: "iwm", Symbol: "IWM", TradeDate: date, OpenMicros: closeMicros, HighMicros: closeMicros + 10, LowMicros: closeMicros - 10, CloseMicros: closeMicros, QualityStatus: QualityStatusValid})
	}
	for index := 0; index < candidateEffectivenessMinimumSamples; index++ {
		ticker := fmt.Sprintf("S%02d", index)
		if err := db.Create(&CandidateSignalEvent{BatchID: batch.BatchID, SecurityID: uint(index + 1), Ticker: ticker, Grade: CandidateGradeB, EventType: CandidateSignalEnteredB, SignalDate: base, BaselineTradeDate: base, BaselineCloseMicros: 1_000_000}).Error; err != nil {
			t.Fatal(err)
		}
		for day := 1; day <= 20; day++ {
			date := base.AddDate(0, 0, day)
			closeMicros := int64(1_000_000 + day*1_000)
			prices = append(prices, PriceSnapshot{Source: "test", SourceVersion: ticker, Symbol: ticker, TradeDate: date, CloseMicros: closeMicros, QualityStatus: QualityStatusValid})
		}
	}
	if err := db.Create(&prices).Error; err != nil {
		t.Fatal(err)
	}
	report, err := BuildCandidateEffectiveness(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	window20 := report.Cohorts[0].Windows[2]
	if window20.SampleCount != candidateEffectivenessMinimumSamples || window20.DistinctSignalDates != 1 || window20.VerificationStatus != "validating" || report.DistinctSignalDates != 1 {
		t.Fatalf("same-day cohort should remain validating: report=%+v window=%+v", report, window20)
	}
}

func TestRefreshCandidateSignalOutcomesPersistsDailyMaturity(t *testing.T) {
	db := openMigratedTestDatabase(t)
	base := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	batch := UniverseBatch{BatchID: "outcome-batch", Kind: BatchKindPrescreen, Status: BatchStatusPublished, EffectiveDate: base.Format(time.DateOnly), StartedAt: base}
	if err := db.Create(&batch).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&CurrentBatchPointer{Kind: BatchKindPrescreen, BatchID: batch.BatchID}).Error; err != nil {
		t.Fatal(err)
	}
	security := Security{CIK: "0000007177", CompanyName: "Loop", CatalogStatus: SecurityCatalogPublished}
	if err := db.Create(&security).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&CandidateScoreSnapshot{BatchID: batch.BatchID, SecurityID: security.ID, Ticker: "LOOP", Grade: CandidateGradeA, ScoringVersion: "score-v3"}).Error; err != nil {
		t.Fatal(err)
	}
	event := CandidateSignalEvent{
		BatchID: batch.BatchID, SecurityID: security.ID, Ticker: "LOOP", Grade: CandidateGradeA,
		EventType: CandidateSignalEnteredA, ScoringVersion: "score-v3", SignalDate: base,
		BaselineTradeDate: base, BaselineCloseMicros: 10_000_000,
	}
	if err := db.Create(&event).Error; err != nil {
		t.Fatal(err)
	}
	prices := []PriceSnapshot{{Source: "test", SourceVersion: "iwm", Symbol: "IWM", TradeDate: base, CloseMicros: 20_000_000, QualityStatus: QualityStatusValid}}
	for day := 1; day <= 20; day++ {
		date := base.AddDate(0, 0, day)
		prices = append(prices,
			PriceSnapshot{Source: "test", SourceVersion: "loop", Symbol: "LOOP", TradeDate: date, CloseMicros: int64(10_000_000 + day*100_000), QualityStatus: QualityStatusValid},
			PriceSnapshot{Source: "test", SourceVersion: "iwm", Symbol: "IWM", TradeDate: date, CloseMicros: int64(20_000_000 + day*50_000), QualityStatus: QualityStatusValid},
		)
	}
	if err := db.Create(&prices).Error; err != nil {
		t.Fatal(err)
	}
	now := base.AddDate(0, 1, 0)
	result, err := RefreshCandidateSignalOutcomes(context.Background(), db, now)
	if err != nil {
		t.Fatal(err)
	}
	if result.SignalCount != 1 || result.TrackedCount != 4 || result.MatureCount != 3 || result.PendingCount != 1 || result.BenchmarkMissing != 0 {
		t.Fatalf("refresh result = %+v", result)
	}
	var outcomes []CandidateSignalOutcome
	if err := db.Order("horizon_days ASC").Find(&outcomes).Error; err != nil {
		t.Fatal(err)
	}
	if len(outcomes) != 4 || outcomes[2].HorizonDays != 20 || outcomes[2].Status != CandidateSignalOutcomeMature || outcomes[2].ReturnPct == nil || outcomes[2].ExcessReturnPct == nil || outcomes[3].Status != CandidateSignalOutcomePending {
		t.Fatalf("outcomes = %+v", outcomes)
	}
	firstMaturedAt := outcomes[0].MaturedAt
	if _, err := RefreshCandidateSignalOutcomes(context.Background(), db, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	var repeated CandidateSignalOutcome
	if err := db.Where("signal_event_id = ? AND horizon_days = ?", event.ID, 1).First(&repeated).Error; err != nil {
		t.Fatal(err)
	}
	if firstMaturedAt == nil || repeated.MaturedAt == nil || !repeated.MaturedAt.Equal(*firstMaturedAt) {
		t.Fatalf("matured_at changed across idempotent refresh: first=%v repeated=%v", firstMaturedAt, repeated.MaturedAt)
	}
	report, err := BuildCandidateEffectiveness(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	if report.ScoringVersion != "score-v3" || report.OutcomeTrackingStatus != "current" || report.TrackedOutcomeCount != 4 || report.MatureOutcomeCount != 3 || report.PendingOutcomeCount != 1 {
		t.Fatalf("persisted outcome report = %+v", report)
	}
}
