package discovery

import "testing"

func TestBuildCandidateTradeSetup(t *testing.T) {
	base := CandidateTechnicalAnalysis{
		Status:                  TechnicalStatusReady,
		CloseUSD:                100,
		MA20USD:                 95,
		MA50USD:                 90,
		MA200USD:                80,
		MA200Available:          true,
		High200DayUSD:           110,
		DistanceTo200DayHighPct: -9.1,
		AverageDollarVolume20:   tradeSetupMinimumAverageDollarVolumeUSD,
		Signals: []CandidateTechnicalSignal{
			{Kind: TechnicalSignalBreakout20DayHigh},
			{Kind: TechnicalSignalVolumeBackedBreakout},
		},
	}
	tests := []struct {
		name           string
		analysis       CandidateTechnicalAnalysis
		wantStatus     string
		wantEntry      string
		wantExitReason string
	}{
		{name: "insufficient history", analysis: CandidateTechnicalAnalysis{Status: TechnicalStatusDataInsufficient}, wantStatus: TradeSetupUnavailable},
		{name: "below ma50 invalidates trend", analysis: withTechnicalClose(base, 89), wantStatus: TradeSetupInvalidated, wantExitReason: "收盘跌破 50 日均线，趋势条件失效"},
		{name: "below ma20 warns to exit", analysis: withTechnicalClose(base, 94), wantStatus: TradeSetupExitWarning, wantExitReason: "收盘跌破 20 日均线，减仓或等待下一日确认"},
		{name: "breakout is entry candidate", analysis: base, wantStatus: TradeSetupEntryCandidate, wantEntry: "放量突破 20 日最高收盘价"},
		{name: "trend without trigger stays watching", analysis: withoutTechnicalSignals(base), wantStatus: TradeSetupWatching},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := buildCandidateTradeSetup(test.analysis)
			if got.Status != test.wantStatus || got.EntryTrigger != test.wantEntry || got.ExitReason != test.wantExitReason {
				t.Fatalf("trade setup = %+v, want status=%q entry=%q exit=%q", got, test.wantStatus, test.wantEntry, test.wantExitReason)
			}
			if test.wantStatus == TradeSetupEntryCandidate && (got.StopLossUSD <= 0 || got.StopLossUSD >= test.analysis.CloseUSD || got.RiskPct <= 0 || got.RiskPct > tradeSetupMaximumInitialRiskPct) {
				t.Fatalf("entry risk plan = %+v", got)
			}
		})
	}
}

func withTechnicalClose(analysis CandidateTechnicalAnalysis, close float64) CandidateTechnicalAnalysis {
	analysis.CloseUSD = close
	return analysis
}

func withoutTechnicalSignals(analysis CandidateTechnicalAnalysis) CandidateTechnicalAnalysis {
	analysis.Signals = []CandidateTechnicalSignal{}
	return analysis
}
