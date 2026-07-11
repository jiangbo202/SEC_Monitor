package discovery

import (
	"context"
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
}

func TestBuildCandidateEffectivenessMarksBenchmarkUnavailableWithoutIWM(t *testing.T) {
	db := openMigratedTestDatabase(t)
	report, err := BuildCandidateEffectiveness(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	if report.BenchmarkAvailable || len(report.Cohorts) != 3 || len(report.Cohorts[0].Windows) != 4 {
		t.Fatalf("empty report = %#v", report)
	}
}
