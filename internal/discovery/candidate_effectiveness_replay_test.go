package discovery

import (
	"context"
	"testing"
	"time"
)

func TestReplayCandidateSignalHistoryUsesOnlyPointInTimeSnapshots(t *testing.T) {
	db := openMigratedTestDatabase(t)
	ctx := context.Background()
	base := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	completed1, completed2, completed3 := base.Add(2*time.Hour), base.AddDate(0, 0, 1).Add(2*time.Hour), base.AddDate(0, 0, 2).Add(2*time.Hour)
	security := Security{CIK: "0000008111", CompanyName: "Replay", CatalogStatus: SecurityCatalogPublished}
	futureSecurity := Security{CIK: "0000008112", CompanyName: "Future", CatalogStatus: SecurityCatalogPublished}
	if err := db.Create(&[]Security{security, futureSecurity}).Error; err != nil {
		t.Fatal(err)
	}
	var securities []Security
	if err := db.Order("id ASC").Find(&securities).Error; err != nil {
		t.Fatal(err)
	}
	batches := []UniverseBatch{
		{BatchID: "replay-1", Kind: BatchKindPrescreen, Status: BatchStatusPublished, EffectiveDate: base.Format(time.DateOnly), StartedAt: base, CompletedAt: &completed1},
		{BatchID: "replay-2", Kind: BatchKindPrescreen, Status: BatchStatusPublished, EffectiveDate: base.AddDate(0, 0, 1).Format(time.DateOnly), StartedAt: base.AddDate(0, 0, 1), CompletedAt: &completed2},
		{BatchID: "replay-future", Kind: BatchKindPrescreen, Status: BatchStatusPublished, EffectiveDate: base.AddDate(0, 0, 2).Format(time.DateOnly), StartedAt: base.AddDate(0, 0, 2), CompletedAt: &completed3},
	}
	if err := db.Create(&batches).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&CurrentBatchPointer{Kind: BatchKindPrescreen, BatchID: "replay-2"}).Error; err != nil {
		t.Fatal(err)
	}
	prices := []PriceSnapshot{
		{Source: "test", SourceVersion: "r1", Symbol: "RPLY", TradeDate: base, CloseMicros: 10_000_000, QualityStatus: QualityStatusValid},
		{Source: "test", SourceVersion: "r2", Symbol: "RPLY", TradeDate: base.AddDate(0, 0, 1), CloseMicros: 11_000_000, QualityStatus: QualityStatusValid},
		{Source: "test", SourceVersion: "future", Symbol: "FUTR", TradeDate: base.AddDate(0, 0, 3), CloseMicros: 8_000_000, QualityStatus: QualityStatusValid},
	}
	if err := db.Create(&prices).Error; err != nil {
		t.Fatal(err)
	}
	scores := []CandidateScoreSnapshot{
		{BatchID: "replay-1", SecurityID: securities[0].ID, Ticker: "RPLY", Grade: CandidateGradeB, EligibleB: true, TotalScore: 75, ScoringVersion: "replay-v1", CreatedAt: completed1.Add(-time.Minute)},
		{BatchID: "replay-2", SecurityID: securities[0].ID, Ticker: "RPLY", Grade: CandidateGradeA, EligibleA: true, TotalScore: 90, ScoringVersion: "replay-v1", CreatedAt: completed2.Add(-time.Minute)},
		{BatchID: "replay-future", SecurityID: securities[1].ID, Ticker: "FUTR", Grade: CandidateGradeB, EligibleB: true, TotalScore: 80, ScoringVersion: "replay-v1", CreatedAt: completed3.Add(-time.Minute)},
	}
	if err := db.Create(&scores).Error; err != nil {
		t.Fatal(err)
	}
	snapshots := []UniverseSnapshot{
		{BatchID: "replay-1", SecurityID: securities[0].ID, Ticker: "RPLY", PriceSnapshotID: &prices[0].ID, QualityStatus: QualityStatusValid},
		{BatchID: "replay-2", SecurityID: securities[0].ID, Ticker: "RPLY", PriceSnapshotID: &prices[1].ID, QualityStatus: QualityStatusValid},
		{BatchID: "replay-future", SecurityID: securities[1].ID, Ticker: "FUTR", PriceSnapshotID: &prices[2].ID, QualityStatus: QualityStatusValid},
	}
	if err := db.Create(&snapshots).Error; err != nil {
		t.Fatal(err)
	}

	preview, err := ReplayCandidateSignalHistory(ctx, db, CandidateEffectivenessReplayInput{Confirm: false}, base.AddDate(0, 0, 30))
	if err != nil {
		t.Fatal(err)
	}
	if preview.ScoringVersion != "replay-v1" || preview.SignalCount != 2 || preview.InsertedCount != 0 || preview.Skipped["future_price_rejected"] != 1 {
		t.Fatalf("preview = %+v", preview)
	}
	confirmed, err := ReplayCandidateSignalHistory(ctx, db, CandidateEffectivenessReplayInput{Confirm: true}, base.AddDate(0, 0, 30))
	if err != nil {
		t.Fatal(err)
	}
	if confirmed.InsertedCount != 2 {
		t.Fatalf("confirmed = %+v", confirmed)
	}
	var events []CandidateSignalEvent
	if err := db.Order("signal_date ASC").Find(&events).Error; err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0].EventType != CandidateSignalEnteredB || events[1].EventType != CandidateSignalEnteredA {
		t.Fatalf("events = %+v", events)
	}
	repeated, err := ReplayCandidateSignalHistory(ctx, db, CandidateEffectivenessReplayInput{Confirm: true}, base.AddDate(0, 0, 30))
	if err != nil || repeated.InsertedCount != 0 {
		t.Fatalf("idempotent replay = %+v err=%v", repeated, err)
	}
}
