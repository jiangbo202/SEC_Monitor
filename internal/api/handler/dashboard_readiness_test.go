package handler

import (
	"context"
	"testing"
)

func TestDashboardDecisionReadinessBlocksWhenResearchStoreIsUnavailable(t *testing.T) {
	freshness := DashboardDataFreshness{
		Status: "fresh", AsOf: "2026-08-28", ExpectedTradeDate: "2026-08-28", Detail: "latest completed session is available",
	}
	result := buildDashboardDecisionReadiness(context.Background(), nil, freshness, nil)
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
	}, nil)
	if result.Status != "blocked" || result.Label != "当日数据不可用于交易判断" {
		t.Fatalf("readiness = %+v", result)
	}
	if len(result.Reasons) < 2 || result.Reasons[0].Key != "market_unavailable" {
		t.Fatalf("reasons = %+v, want explicit market unavailable reason", result.Reasons)
	}
}
