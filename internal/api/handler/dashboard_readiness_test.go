package handler

import (
	"context"
	"testing"
)

func TestDashboardDecisionReadinessBlocksWhenResearchStoreIsUnavailable(t *testing.T) {
	freshness := DashboardDataFreshness{
		Status: "fresh", AsOf: "2026-08-28", ExpectedTradeDate: "2026-08-28", Detail: "latest completed session is available",
	}
	result := buildDashboardDecisionReadiness(context.Background(), nil, freshness, nil, nil)
	if result.Status != "blocked" || result.ResearchUsable || result.NewTradePlanAllowed {
		t.Fatalf("readiness = %+v, want blocked", result)
	}
	if len(result.Reasons) != 1 || result.Reasons[0].Key != "discovery_db_unavailable" || result.Reasons[0].Severity != "critical" {
		t.Fatalf("reasons = %+v", result.Reasons)
	}
}

func TestDashboardDecisionReadinessNeverTreatsMissingMarketDataAsNoSignal(t *testing.T) {
	result := buildDashboardDecisionReadiness(context.Background(), nil, DashboardDataFreshness{
		Status: "unavailable", Detail: "no local market snapshot",
	}, nil, nil)
	if result.Status != "blocked" || result.Label != "当日数据不可用于交易判断" {
		t.Fatalf("readiness = %+v", result)
	}
	if len(result.Reasons) < 2 || result.Reasons[0].Key != "market_unavailable" {
		t.Fatalf("reasons = %+v, want explicit market unavailable reason", result.Reasons)
	}
}

func TestApplyDashboardCandidateAvailabilitySeparatesResearchFromTrading(t *testing.T) {
	readiness := DashboardDecisionReadiness{Status: "ready", Label: "今日数据可用", ResearchUsable: true, NewTradePlanAllowed: true}
	applyDashboardCandidateAvailability(&readiness, DashboardCandidateAvailability{Total: 143, ResearchOnly: 136, Blocked: 7})
	if readiness.Status != "research_only" || !readiness.ResearchUsable || readiness.NewTradePlanAllowed {
		t.Fatalf("readiness = %+v, want research-only with new plans disabled", readiness)
	}
	if readiness.Label != "数据可研究，暂无可行动候选" || len(readiness.Reasons) != 1 || readiness.Reasons[0].Key != "candidate_universe_gated" {
		t.Fatalf("readiness = %+v", readiness)
	}
}

func TestApplyDashboardCandidateAvailabilityKeepsTradingReadyWhenEligibleExists(t *testing.T) {
	readiness := DashboardDecisionReadiness{Status: "ready", Label: "今日数据可用", ResearchUsable: true, NewTradePlanAllowed: true}
	applyDashboardCandidateAvailability(&readiness, DashboardCandidateAvailability{Total: 143, Eligible: 1, ResearchOnly: 135, Blocked: 7})
	if readiness.Status != "ready" || !readiness.NewTradePlanAllowed || len(readiness.Reasons) != 0 {
		t.Fatalf("readiness = %+v, want unchanged", readiness)
	}
}
