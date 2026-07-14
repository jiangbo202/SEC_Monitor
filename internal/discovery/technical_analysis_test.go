package discovery

import (
	"testing"
	"time"
)

func TestBuildCandidateTechnicalAnalysis(t *testing.T) {
	base := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	rows := make([]PriceSnapshot, 0, technicalMinimumSamples)
	for day := 0; day < technicalMinimumSamples; day++ {
		close := int64(10_000_000)
		volume := int64(100)
		if day == technicalLookbackDays-1 {
			close = 9_000_000
		}
		if day == technicalLookbackDays {
			close = 12_000_000
			volume = 200
		}
		rows = append(rows, PriceSnapshot{TradeDate: base.AddDate(0, 0, day), CloseMicros: close, Volume: volume})
	}

	analysis := buildCandidateTechnicalAnalysis(rows)
	if analysis.Status != TechnicalStatusReady {
		t.Fatalf("status = %q, want %q", analysis.Status, TechnicalStatusReady)
	}
	if analysis.SampleDays != technicalMinimumSamples {
		t.Fatalf("sample days = %d, want %d", analysis.SampleDays, technicalMinimumSamples)
	}
	for _, kind := range []string{TechnicalSignalCrossAboveMA20, TechnicalSignalBreakout20DayHigh, TechnicalSignalVolumeBackedBreakout} {
		if !candidateHasTechnicalSignal(analysis, kind) {
			t.Errorf("missing signal %q: %+v", kind, analysis.Signals)
		}
	}
}

func TestBuildCandidateTechnicalAnalysisInsufficientHistory(t *testing.T) {
	rows := make([]PriceSnapshot, technicalLookbackDays)
	for index := range rows {
		rows[index] = PriceSnapshot{TradeDate: time.Date(2026, 6, index+1, 0, 0, 0, 0, time.UTC), CloseMicros: 10_000_000, Volume: 100}
	}

	analysis := buildCandidateTechnicalAnalysis(rows)
	if analysis.Status != TechnicalStatusDataInsufficient {
		t.Fatalf("status = %q, want %q", analysis.Status, TechnicalStatusDataInsufficient)
	}
	if len(analysis.Signals) != 0 {
		t.Fatalf("signals = %+v, want none", analysis.Signals)
	}
}

func TestFilterCandidateScoreResultsByTechnicalSignal(t *testing.T) {
	items := []CandidateScoreResult{
		{CandidateScoreSnapshot: CandidateScoreSnapshot{Ticker: "BREAK"}, Technical: CandidateTechnicalAnalysis{Signals: []CandidateTechnicalSignal{{Kind: TechnicalSignalCrossAboveMA20}}}},
		{CandidateScoreSnapshot: CandidateScoreSnapshot{Ticker: "QUIET"}},
	}

	filtered := filterCandidateScoreResults(items, CandidateScoreQuery{TechnicalSignal: TechnicalSignalCrossAboveMA20})
	if len(filtered) != 1 || filtered[0].Ticker != "BREAK" {
		t.Fatalf("filtered = %+v, want BREAK only", filtered)
	}
}
