package discovery

import (
	"testing"
	"time"
)

func TestBuildCandidateResearchReadiness(t *testing.T) {
	asOf := time.Date(2026, 7, 17, 0, 0, 0, 0, time.UTC)
	recentPeriod := asOf.AddDate(0, 0, -90)
	stalePeriod := asOf.AddDate(0, 0, -191)
	base := CandidateScoreResult{
		CandidateScoreSnapshot: CandidateScoreSnapshot{MarketCapUSD: 100_000_000},
		PriceCloseUSD:          12.5,
		PriceQualityStatus:     QualityStatusValid,
		PriceFreshnessStatus:   PriceFreshnessCurrent,
	}
	metric := FinancialMetricSnapshot{RevenueGrowthAvailable: true, RunwayAvailable: true}
	tests := []struct {
		name                  string
		item                  CandidateScoreResult
		metricFound           bool
		period                time.Time
		insiderAvailable      bool
		insiderSourceDeclared bool
		wantStatus            string
		wantReason            string
	}{
		{name: "complete evidence is ready", item: base, metricFound: true, period: recentPeriod, insiderAvailable: true, insiderSourceDeclared: true, wantStatus: CandidateResearchReadinessReady},
		{name: "stale quarterly financials are research only", item: base, metricFound: true, period: stalePeriod, insiderAvailable: true, insiderSourceDeclared: true, wantStatus: CandidateResearchReadinessResearchOnly, wantReason: "financial_period_stale"},
		{name: "declared insider outage is research only", item: base, metricFound: true, period: recentPeriod, insiderAvailable: false, insiderSourceDeclared: true, wantStatus: CandidateResearchReadinessResearchOnly, wantReason: "insider_source_unavailable"},
		{name: "missing price blocks ranking", item: CandidateScoreResult{CandidateScoreSnapshot: CandidateScoreSnapshot{MarketCapUSD: 100_000_000}, PriceFreshnessStatus: PriceFreshnessMissing}, metricFound: true, period: recentPeriod, insiderAvailable: true, insiderSourceDeclared: true, wantStatus: CandidateResearchReadinessBlocked, wantReason: "market_price_unavailable"},
		{name: "missing financial metrics are research only", item: base, metricFound: false, period: recentPeriod, insiderAvailable: true, insiderSourceDeclared: true, wantStatus: CandidateResearchReadinessResearchOnly, wantReason: "financial_metrics_unavailable"},
		{name: "partial insider coverage is research only", item: base, metricFound: true, period: recentPeriod, insiderAvailable: true, insiderSourceDeclared: true, wantStatus: CandidateResearchReadinessResearchOnly, wantReason: "insider_coverage_partial"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			coverageExpected := tt.name == "partial insider coverage is research only"
			coverage := candidateInsiderCoverage{}
			if coverageExpected {
				coverage.coverageStatus = InsiderCoveragePartial
			}
			got := buildCandidateResearchReadiness(tt.item, metric, tt.metricFound, tt.period, tt.insiderAvailable, tt.insiderSourceDeclared, coverageExpected, coverage, asOf)
			if got.Status != tt.wantStatus {
				t.Fatalf("status=%q want %q (%#v)", got.Status, tt.wantStatus, got)
			}
			if tt.wantReason != "" && !containsString(got.Reasons, tt.wantReason) {
				t.Fatalf("reasons=%#v missing %q", got.Reasons, tt.wantReason)
			}
		})
	}
}

func TestRecommendCandidateResearchNextStep(t *testing.T) {
	tests := []struct {
		name      string
		readiness CandidateResearchReadiness
		technical CandidateTechnicalAnalysis
		priority  string
		action    string
	}{
		{
			name:      "blocked price evidence",
			readiness: CandidateResearchReadiness{Status: CandidateResearchReadinessBlocked, Reasons: []string{"market_price_unavailable"}},
			priority:  "blocked",
			action:    "等待并补齐最近有效收盘价",
		},
		{
			name:      "financial evidence needs review",
			readiness: CandidateResearchReadiness{Status: CandidateResearchReadinessResearchOnly, Reasons: []string{"financial_period_stale"}},
			priority:  "review",
			action:    "核对最新 10-Q / 10-K 财务指标",
		},
		{
			name:      "ready technical catalyst review",
			readiness: CandidateResearchReadiness{Status: CandidateResearchReadinessReady},
			technical: CandidateTechnicalAnalysis{Status: TechnicalStatusReady, Signals: []CandidateTechnicalSignal{{Kind: "breakout_20d_high"}}},
			priority:  "normal",
			action:    "核对技术信号与近期催化剂",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := recommendCandidateResearchNextStep(tt.readiness, tt.technical)
			if got.Priority != tt.priority || got.Action != tt.action {
				t.Fatalf("next step = %#v, want priority=%q action=%q", got, tt.priority, tt.action)
			}
		})
	}
}
