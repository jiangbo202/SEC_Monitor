package discovery

import (
	"context"
	"testing"
	"time"
)

func TestRecordTradeSetupStatusTransitionsRecordsOnlyChanges(t *testing.T) {
	db := openMigratedTestDatabase(t)
	start := time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC)
	rows := make([]PriceSnapshot, 0, technicalMA200LookbackDays)
	for day := 0; day < technicalMA200LookbackDays; day++ {
		rows = append(rows, PriceSnapshot{
			Source: "test", SourceVersion: "baseline", Symbol: "PLAN",
			TradeDate: start.AddDate(0, 0, day), CloseMicros: 100_000_000,
			Volume: 1_000_000, QualityStatus: QualityStatusValid,
		})
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	recordedAt := start.AddDate(0, 0, technicalMA200LookbackDays).Add(4 * time.Hour)
	created, err := RecordTradeSetupStatusTransitions(context.Background(), db, []string{"plan"}, recordedAt)
	if err != nil || created != 1 {
		t.Fatalf("initial record = %d, %v; want one event", created, err)
	}
	created, err = RecordTradeSetupStatusTransitions(context.Background(), db, []string{"PLAN"}, recordedAt.Add(time.Hour))
	if err != nil || created != 0 {
		t.Fatalf("repeat record = %d, %v; want no duplicate", created, err)
	}
	if err := db.Create(&PriceSnapshot{
		Source: "test", SourceVersion: "next", Symbol: "PLAN",
		TradeDate: start.AddDate(0, 0, technicalMA200LookbackDays), CloseMicros: 120_000_000,
		Volume: 1_000_000, QualityStatus: QualityStatusValid,
	}).Error; err != nil {
		t.Fatal(err)
	}
	created, err = RecordTradeSetupStatusTransitions(context.Background(), db, []string{"PLAN"}, recordedAt.AddDate(0, 0, 1))
	if err != nil || created != 1 {
		t.Fatalf("changed record = %d, %v; want one event", created, err)
	}
	history, err := GetTradeSetupStatusHistory(context.Background(), db, "PLAN", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 2 {
		t.Fatalf("history = %+v, want two events", history)
	}
	if history[0].Status != TradeSetupEntryCandidate || history[0].PreviousStatus != TradeSetupInvalidated {
		t.Fatalf("latest = %+v, want entry candidate after invalidated", history[0])
	}
	if history[0].StartedAt.Format(time.DateOnly) != start.AddDate(0, 0, technicalMA200LookbackDays).Format(time.DateOnly) {
		t.Fatalf("started at = %s, want latest trade date", history[0].StartedAt)
	}
}
