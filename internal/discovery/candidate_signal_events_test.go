package discovery

import (
	"context"
	"testing"
	"time"
)

func TestPersistCandidateSignalEventsStoresImmutablePriceBaseline(t *testing.T) {
	db := openMigratedTestDatabase(t)
	now := time.Date(2026, 7, 17, 8, 0, 0, 0, time.UTC)
	security := Security{CIK: "0000099001", CompanyName: "Signal Co", CatalogStatus: SecurityCatalogPublished}
	mustCreate(t, db, &security)

	previous := UniverseBatch{BatchID: "market-previous", Kind: BatchKindPrescreen, Status: BatchStatusPublished, EffectiveDate: "2026-07-16", StartedAt: now.AddDate(0, 0, -1)}
	current := UniverseBatch{BatchID: "market-current", Kind: BatchKindPrescreen, Status: BatchStatusDraft, EffectiveDate: "2026-07-17", StartedAt: now}
	mustCreate(t, db, &previous)
	mustCreate(t, db, &current)
	mustCreate(t, db, &CurrentBatchPointer{Kind: BatchKindPrescreen, BatchID: previous.BatchID, UpdatedAt: now.AddDate(0, 0, -1)})
	mustCreate(t, db, &CandidateScoreSnapshot{BatchID: previous.BatchID, SecurityID: security.ID, Ticker: "SIGN", Grade: CandidateGradeB, EligibleB: true, TotalScore: 55})
	mustCreate(t, db, &CandidateScoreSnapshot{BatchID: current.BatchID, SecurityID: security.ID, Ticker: "SIGN", Grade: CandidateGradeA, EligibleA: true, EligibleB: true, TotalScore: 72, ScoringVersion: "v1"})
	price := PriceSnapshot{Source: "tiingo", SourceVersion: "signal", Symbol: "SIGN", TradeDate: now, CloseMicros: 12_345_678, QualityStatus: QualityStatusValid}
	mustCreate(t, db, &price)
	mustCreate(t, db, &UniverseSnapshot{BatchID: current.BatchID, SecurityID: security.ID, Ticker: "SIGN", PriceSnapshotID: &price.ID, QualityStatus: QualityStatusValid})

	if err := persistCandidateSignalEvents(context.Background(), db, current, now); err != nil {
		t.Fatal(err)
	}
	// The insert is deliberately idempotent so a publish retry cannot duplicate
	// a signal or change the baseline that was originally observed.
	if err := persistCandidateSignalEvents(context.Background(), db, current, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	var events []CandidateSignalEvent
	if err := db.Find(&events).Error; err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("events = %#v, want one", events)
	}
	event := events[0]
	if event.EventType != CandidateSignalEnteredA || event.SignalDate.Format(time.DateOnly) != "2026-07-17" || event.BaselineTradeDate != price.TradeDate || event.BaselineCloseMicros != price.CloseMicros || event.PriceSource != "tiingo" {
		t.Fatalf("event = %#v", event)
	}
}

func TestCandidateSignalEventType(t *testing.T) {
	tests := []struct {
		name     string
		previous CandidateScoreSnapshot
		current  CandidateScoreSnapshot
		want     string
	}{
		{name: "first A", current: CandidateScoreSnapshot{EligibleA: true, Grade: CandidateGradeA}, want: CandidateSignalEnteredA},
		{name: "first B", current: CandidateScoreSnapshot{EligibleB: true, Grade: CandidateGradeB}, want: CandidateSignalEnteredB},
		{name: "quality upgrade", previous: CandidateScoreSnapshot{EligibleB: true, TotalScore: 55}, current: CandidateScoreSnapshot{EligibleB: true, TotalScore: 65}, want: CandidateSignalUpgradedQuality},
		{name: "no event", previous: CandidateScoreSnapshot{EligibleB: true, TotalScore: 55}, current: CandidateScoreSnapshot{EligibleB: true, TotalScore: 60}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := candidateSignalEventType(test.previous, test.current); got != test.want {
				t.Fatalf("candidateSignalEventType() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestBuildCandidateEffectivenessPrefersSignalEvents(t *testing.T) {
	db := openMigratedTestDatabase(t)
	base := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	mustCreate(t, db, &CandidateSignalEvent{BatchID: "event-batch", SecurityID: 900, Ticker: "EVNT", Grade: CandidateGradeB, EventType: CandidateSignalEnteredB, SignalDate: base, BaselineTradeDate: base, BaselineCloseMicros: 1_000_000})
	prices := make([]PriceSnapshot, 0, 42)
	for day := 1; day <= 21; day++ {
		date := base.AddDate(0, 0, day)
		prices = append(prices,
			PriceSnapshot{Source: "test", SourceVersion: "event", Symbol: "EVNT", TradeDate: date, CloseMicros: int64(1_000_000 + day*10_000), QualityStatus: QualityStatusValid},
			PriceSnapshot{Source: "test", SourceVersion: "iwm", Symbol: "IWM", TradeDate: date, CloseMicros: int64(2_000_000 + day*2_000), QualityStatus: QualityStatusValid},
		)
	}
	mustCreate(t, db, &prices)
	report, err := BuildCandidateEffectiveness(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	if report.CohortSource != "signal_events" || report.Cohorts[0].CandidateCount != 1 || report.Cohorts[0].Windows[2].SampleCount != 1 {
		t.Fatalf("report = %#v", report)
	}
}
