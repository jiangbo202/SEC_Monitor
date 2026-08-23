package discovery

import (
	"context"
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

func TestBuildCandidateTechnicalAnalysisIncludesMA200WhenHistoryIsAvailable(t *testing.T) {
	base := time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC)
	rows := make([]PriceSnapshot, 0, technicalMA200LookbackDays)
	for day := 0; day < technicalMA200LookbackDays; day++ {
		rows = append(rows, PriceSnapshot{TradeDate: base.AddDate(0, 0, day), CloseMicros: int64(10_000_000 + day*100_000), Volume: 100})
	}
	analysis := buildCandidateTechnicalAnalysis(rows)
	if !analysis.MA200Available || analysis.MA200USD <= 0 || analysis.MA50USD <= 0 {
		t.Fatalf("long moving averages = %+v, want MA50 and MA200", analysis)
	}
}

func TestCalculateCloseOscillatorsUsesWilderRSIAndCloseRangeKDJ(t *testing.T) {
	base := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	rising := make([]PriceSnapshot, 0, 20)
	for day := 0; day < 20; day++ {
		rising = append(rising, PriceSnapshot{TradeDate: base.AddDate(0, 0, day), CloseMicros: int64(10_000_000 + day*1_000_000)})
	}
	points := calculateCloseOscillators(rising)
	latest := points[len(points)-1]
	if latest.RSI14 == nil || *latest.RSI14 != 100 {
		t.Fatalf("rising RSI = %v, want 100", latest.RSI14)
	}
	if latest.K == nil || latest.D == nil || latest.J == nil || *latest.K <= *latest.D {
		t.Fatalf("rising close-range KDJ = %+v, want K above D", latest)
	}
	if points[technicalRSIPeriod-1].RSI14 != nil || points[technicalKDJPeriod-2].K != nil {
		t.Fatalf("indicators became available before their minimum windows")
	}
}

func TestCalculateCloseOscillatorsFlatSeriesIsNeutral(t *testing.T) {
	base := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	rows := make([]PriceSnapshot, 0, technicalMinimumSamples)
	for day := 0; day < technicalMinimumSamples; day++ {
		rows = append(rows, PriceSnapshot{TradeDate: base.AddDate(0, 0, day), CloseMicros: 10_000_000})
	}
	analysis := buildCandidateOscillatorAnalysis(rows)
	if analysis.Status != TechnicalStatusReady || analysis.RSI14 == nil || *analysis.RSI14 != 50 {
		t.Fatalf("flat oscillator analysis = %+v", analysis)
	}
	if analysis.K == nil || analysis.D == nil || analysis.J == nil || *analysis.K != 50 || *analysis.D != 50 || *analysis.J != 50 {
		t.Fatalf("flat KDJ = %+v, want 50/50/50", analysis)
	}
	if analysis.Signal != "neutral" {
		t.Fatalf("flat signal = %q, want neutral", analysis.Signal)
	}
}

func TestCalculateOscillatorsUsesStandardOHLCForKDJ(t *testing.T) {
	base := time.Date(2026, 2, 2, 0, 0, 0, 0, time.UTC)
	rows := make([]PriceSnapshot, 0, 20)
	for day := 0; day < 20; day++ {
		closeMicros := int64(10_000_000 + day*100_000)
		rows = append(rows, PriceSnapshot{
			TradeDate: base.AddDate(0, 0, day), OpenMicros: closeMicros - 50_000,
			HighMicros: closeMicros + 500_000, LowMicros: closeMicros - 500_000, CloseMicros: closeMicros,
		})
	}
	analysis := buildCandidateOscillatorAnalysis(rows)
	if analysis.Status != TechnicalStatusReady || analysis.KDJMethod != standardKDJMethod {
		t.Fatalf("standard OHLC oscillator = %+v", analysis)
	}
	history := candidateTechnicalHistoryRows(rows)
	if !history[0].OHLCAvailable || history[0].KDJMethod != standardKDJMethod || history[0].OpenUSD <= 0 || history[0].HighUSD <= history[0].LowUSD {
		t.Fatalf("standard OHLC history = %+v", history[0])
	}
}

func TestCandidateTechnicalHistoryRowsIncludeOscillators(t *testing.T) {
	base := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	rows := make([]PriceSnapshot, 0, 16)
	for day := 0; day < 16; day++ {
		rows = append(rows, PriceSnapshot{TradeDate: base.AddDate(0, 0, day), CloseMicros: int64(10_000_000 + day*100_000)})
	}
	history := candidateTechnicalHistoryRows(rows)
	if len(history) != len(rows) || history[0].RSI14 == nil || history[0].K == nil || history[0].D == nil || history[0].J == nil {
		t.Fatalf("latest history row missing oscillators: %+v", history[0])
	}
	if history[len(history)-1].RSI14 != nil || history[len(history)-1].K != nil {
		t.Fatalf("oldest row should not have enough history: %+v", history[len(history)-1])
	}
}

func TestBuildCandidateTechnicalAnalysisUsesImmediatePriorWindowAndLiquidity(t *testing.T) {
	base := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	rows := make([]PriceSnapshot, 0, 41)
	for day := 0; day < 40; day++ {
		close := int64(100_000_000)
		if day >= 20 {
			close = 10_000_000
		}
		rows = append(rows, PriceSnapshot{TradeDate: base.AddDate(0, 0, day), CloseMicros: close, Volume: 1_000})
	}
	rows = append(rows, PriceSnapshot{TradeDate: base.AddDate(0, 0, 40), CloseMicros: 30_000_000, Volume: 2_000})

	analysis := buildCandidateTechnicalAnalysis(rows)
	if analysis.Prior20DayHighUSD != 10 || analysis.PriorMA20USD != 10 {
		t.Fatalf("prior window used stale data: %+v", analysis)
	}
	if analysis.DollarVolumeUSD != 60_000 || analysis.AverageDollarVolume20 != 10_000 || analysis.DollarVolumeRatio20 != 6 {
		t.Fatalf("dollar volume = %+v", analysis)
	}
	if analysis.LiquidityStatus != "low" || !candidateHasTechnicalSignal(analysis, TechnicalSignalBreakout20DayHigh) {
		t.Fatalf("liquidity/signals = %+v", analysis)
	}
}

func TestBuildCandidateAnchoredVWAPUsesOnlyEventToPublishedPriceWindow(t *testing.T) {
	base := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	rows := []PriceSnapshot{
		{TradeDate: base, CloseMicros: 10_000_000, Volume: 100},
		{TradeDate: base.AddDate(0, 0, 1), CloseMicros: 12_000_000, Volume: 200},
		{TradeDate: base.AddDate(0, 0, 2), CloseMicros: 14_000_000, Volume: 100},
		{TradeDate: base.AddDate(0, 0, 3), CloseMicros: 16_000_000, Volume: 100},
	}
	cutoff := base.AddDate(0, 0, 3)
	value := buildCandidateAnchoredVWAP(rows, []CandidateSignalEvent{{EventType: CandidateSignalEnteredB, SignalDate: base.AddDate(0, 0, 1), BaselineTradeDate: base.AddDate(0, 0, 1)}}, &cutoff, "test")
	if value.Status != "ready" || value.TradingDays != 3 || value.AnchorLabel != "进入 B 级候选" {
		t.Fatalf("anchored vwap = %#v", value)
	}
	// (12*200 + 14*100 + 16*100) / 400 = 13.5; last close is 16.
	if value.ApproximateVWAPUSD != 13.5 || value.DistancePct < 18.5 || value.DistancePct > 18.6 {
		t.Fatalf("anchored vwap values = %#v", value)
	}
}

func TestBuildCandidateAnchoredVWAPDoesNotUseFutureEvent(t *testing.T) {
	base := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	cutoff := base.AddDate(0, 0, 2)
	value := buildCandidateAnchoredVWAP([]PriceSnapshot{{TradeDate: base, CloseMicros: 10_000_000, Volume: 100}}, []CandidateSignalEvent{{EventType: CandidateSignalEnteredA, BaselineTradeDate: base.AddDate(0, 0, 3)}}, &cutoff, "test")
	if value.Status != "anchor_unavailable" {
		t.Fatalf("future anchor = %#v", value)
	}
}

func TestBuildCandidateRelativeStrengthUsesMatchedLocalTradingDays(t *testing.T) {
	db := openMigratedTestDatabase(t)
	base := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	prices := make([]PriceSnapshot, 0, (technicalRelativeLongDays+1)*2)
	candidateRows := make([]PriceSnapshot, 0, technicalRelativeLongDays+1)
	for day := 0; day <= technicalRelativeLongDays; day++ {
		date := base.AddDate(0, 0, day)
		candidate := PriceSnapshot{Source: "test", Symbol: "REL", TradeDate: date, CloseMicros: int64(10_000_000 + day*200_000), QualityStatus: QualityStatusValid}
		benchmark := PriceSnapshot{Source: "test", Symbol: "IWM", TradeDate: date, CloseMicros: int64(20_000_000 + day*100_000), QualityStatus: QualityStatusValid}
		candidateRows = append(candidateRows, candidate)
		prices = append(prices, candidate, benchmark)
	}
	if err := db.Create(&prices).Error; err != nil {
		t.Fatal(err)
	}
	cutoff := base.AddDate(0, 0, technicalRelativeLongDays)
	relative, err := buildCandidateRelativeStrength(context.Background(), db, CandidateScoreResult{
		CandidateScoreSnapshot: CandidateScoreSnapshot{Ticker: "REL"}, PriceTradeDate: &cutoff, PriceSource: "test",
	}, candidateRows)
	if err != nil {
		t.Fatal(err)
	}
	if relative.Status != "ready" || relative.MatchedSampleDays != technicalRelativeLongDays+1 || relative.ExcessReturn20DPct == nil || relative.ExcessReturn60DPct == nil || *relative.ExcessReturn20DPct <= 0 {
		t.Fatalf("relative strength = %+v, want positive ready result", relative)
	}
}

func TestCandidateTechnicalHistoryRows(t *testing.T) {
	base := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	rows := []PriceSnapshot{
		{TradeDate: base, CloseMicros: 10_000_000, Volume: 100, Source: "tiingo", SourceVersion: "tiingo:technical-history:2026-06-30"},
		{TradeDate: base.AddDate(0, 0, 1), CloseMicros: 11_000_000, Volume: 120, Source: "tiingo", SourceVersion: "tiingo:2026-06-02"},
	}

	history := candidateTechnicalHistoryRows(rows)
	if len(history) != 2 {
		t.Fatalf("history count = %d, want 2", len(history))
	}
	if history[0].TradeDate != "2026-06-02" || history[0].Backfilled {
		t.Fatalf("latest history = %+v, want non-backfilled 2026-06-02", history[0])
	}
	if history[1].TradeDate != "2026-06-01" || !history[1].Backfilled {
		t.Fatalf("older history = %+v, want backfilled 2026-06-01", history[1])
	}
}

func TestCandidateTechnicalPriceHistoryUsesPublishedPriceDateCutoff(t *testing.T) {
	db := openMigratedTestDatabase(t)
	base := time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC)
	prices := []PriceSnapshot{
		{Source: "tiingo", SourceVersion: "tiingo:old", Symbol: "CUT", TradeDate: base, CloseMicros: 10_000_000, Volume: 100, Currency: "USD", QualityStatus: QualityStatusValid},
		{Source: "tiingo", SourceVersion: "tiingo:old", Symbol: "CUT", TradeDate: base.AddDate(0, 0, 1), CloseMicros: 11_000_000, Volume: 100, Currency: "USD", QualityStatus: QualityStatusValid},
		{Source: "tiingo", SourceVersion: "tiingo:backfill", Symbol: "CUT", TradeDate: base.AddDate(0, 0, 2), CloseMicros: 12_000_000, Volume: 100, Currency: "USD", QualityStatus: QualityStatusValid},
	}
	if err := db.Create(&prices).Error; err != nil {
		t.Fatal(err)
	}
	cutoff := base.AddDate(0, 0, 1)
	rows, err := candidateTechnicalPriceHistory(context.Background(), db, CandidateScoreResult{
		CandidateScoreSnapshot: CandidateScoreSnapshot{Ticker: "CUT"},
		PriceTradeDate:         &cutoff,
		PriceSource:            "tiingo",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || !rows[len(rows)-1].TradeDate.Equal(cutoff) {
		t.Fatalf("rows = %+v, want no record after %s", rows, cutoff.Format(time.DateOnly))
	}
}

func TestTechnicalPriceHistoryFallsBackWhenPreferredLocalCacheIsIncomplete(t *testing.T) {
	db := openMigratedTestDatabase(t)
	base := time.Date(2026, 7, 14, 0, 0, 0, 0, time.UTC)
	prices := []PriceSnapshot{
		{Source: "longbridge", SourceVersion: "longbridge:history", Symbol: "CACHE", TradeDate: base, CloseMicros: 10_000_000, Volume: 100, Currency: "USD", QualityStatus: QualityStatusValid},
		{Source: "longbridge", SourceVersion: "longbridge:history", Symbol: "CACHE", TradeDate: base.AddDate(0, 0, 1), CloseMicros: 11_000_000, Volume: 100, Currency: "USD", QualityStatus: QualityStatusValid},
		{Source: "longbridge", SourceVersion: "longbridge:history", Symbol: "CACHE", TradeDate: base.AddDate(0, 0, 2), CloseMicros: 12_000_000, Volume: 100, Currency: "USD", QualityStatus: QualityStatusValid},
		{Source: PriceSourceLocalCache, SourceVersion: "cache:latest", Symbol: "CACHE", TradeDate: base.AddDate(0, 0, 2), CloseMicros: 12_100_000, Volume: 100, Currency: "USD", QualityStatus: QualityStatusValid},
	}
	if err := db.Create(&prices).Error; err != nil {
		t.Fatal(err)
	}
	cutoff := base.AddDate(0, 0, 2)
	rows, err := technicalPriceHistoryForSymbol(context.Background(), db, "CACHE", PriceSourceLocalCache, 3, &cutoff)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("history rows = %d, want 3", len(rows))
	}
	if rows[len(rows)-1].Source != PriceSourceLocalCache {
		t.Fatalf("latest source = %q, want %q", rows[len(rows)-1].Source, PriceSourceLocalCache)
	}
}

func TestCandidateTechnicalPriceHistoryReadsEnoughRowsForMA200(t *testing.T) {
	db := openMigratedTestDatabase(t)
	base := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	prices := make([]PriceSnapshot, 0, 220)
	for index := 0; index < 220; index++ {
		prices = append(prices, PriceSnapshot{
			Source: "longbridge", SourceVersion: "longbridge:technical-history", Symbol: "LONG",
			TradeDate: base.AddDate(0, 0, index), CloseMicros: 10_000_000 + int64(index), Volume: 100,
			Currency: "USD", QualityStatus: QualityStatusValid,
		})
	}
	if err := db.Create(&prices).Error; err != nil {
		t.Fatal(err)
	}
	cutoff := prices[len(prices)-1].TradeDate
	rows, err := candidateTechnicalPriceHistory(context.Background(), db, CandidateScoreResult{
		CandidateScoreSnapshot: CandidateScoreSnapshot{Ticker: "LONG"}, PriceTradeDate: &cutoff, PriceSource: "longbridge",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != technicalMA200LookbackDays {
		t.Fatalf("history row count = %d, want %d", len(rows), technicalMA200LookbackDays)
	}
	if got := buildCandidateTechnicalAnalysis(rows); !got.MA200Available {
		t.Fatalf("MA200 should be available after %d samples: %+v", technicalMA200LookbackDays, got)
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
