package discovery

import "testing"

func TestBuildCandidateFairValueEstimateSeparatesConsensusAndLocalScenario(t *testing.T) {
	value := func(input float64) *float64 { return &input }
	technical := CandidateTechnicalAnalysis{CloseUSD: 10, TradeDate: "2026-08-10"}
	analyst := &AnalystRatingSnapshot{Status: AnalystRatingStatusAvailable, Currency: "USD", AnalystCount: 8, TargetAverageMicros: 15_000_000, TargetLowMicros: 12_000_000, TargetHighMicros: 20_000_000}
	valuation := &ValuationResearchSnapshot{Metrics: ValuationResearchMetrics{
		PE: ValuationMetric{Current: value(10), Low: value(5), Median: value(8), High: value(15)},
		PB: ValuationMetric{Current: value(2), Low: value(1), Median: value(1.5), High: value(3)},
		PS: ValuationMetric{Current: value(-1), Low: value(1), Median: value(2), High: value(3)},
	}}
	got := buildCandidateFairValueEstimate(technical, analyst, valuation)
	if got.Status != CandidateFairValueStatusAvailable || got.MarketConsensusTarget == nil || *got.MarketConsensusTarget != 15 || got.MarketConsensusUpsidePct == nil || *got.MarketConsensusUpsidePct != 50 {
		t.Fatalf("estimate = %+v", got)
	}
	if got.LocalHistoricalScenario == nil || got.LocalHistoricalScenario.Metrics != 2 || got.LocalHistoricalScenario.Low != 5 || got.LocalHistoricalScenario.Mid != 7.75 || got.LocalHistoricalScenario.High != 15 {
		t.Fatalf("local scenario = %+v", got.LocalHistoricalScenario)
	}
	if len(got.MetricScenarios) != 2 || got.MetricScenarios[0].Metric != "PE" || got.MetricScenarios[1].Metric != "PB" {
		t.Fatalf("metric scenarios = %+v", got.MetricScenarios)
	}
}

func TestBuildCandidateFairValueEstimateExplainsMissingInputs(t *testing.T) {
	got := buildCandidateFairValueEstimate(CandidateTechnicalAnalysis{}, nil, nil)
	if got.Status != CandidateFairValueStatusInsufficient || got.Message == "" || got.MarketConsensusTarget != nil || got.LocalHistoricalScenario != nil {
		t.Fatalf("estimate = %+v", got)
	}
}

func TestBuildCandidateFairValueEstimateExplainsWhenOnlyConsensusIsAvailable(t *testing.T) {
	got := buildCandidateFairValueEstimate(CandidateTechnicalAnalysis{CloseUSD: 10}, &AnalystRatingSnapshot{Status: AnalystRatingStatusAvailable, TargetAverageMicros: 12_000_000}, nil)
	if got.Status != CandidateFairValueStatusAvailable || got.LocalHistoricalScenario != nil || got.Message == "" {
		t.Fatalf("estimate = %+v", got)
	}
}
