package discovery

import (
	"context"
	"testing"
)

func TestCandidateResearchPositionLifecycle(t *testing.T) {
	db := openMigratedTestDatabase(t)
	cost := 12.5
	position, err := UpsertCandidateResearchPosition(context.Background(), db, CandidateResearchPositionInput{
		Ticker: "plan", MaxWeightPct: 4, ReferenceCostUSD: &cost, MaxDailyVolumeParticipation: 8,
		EventRiskNote: "财报周不调整研究上限", LiquidityNote: "仅作为人工研究约束", Note: "不连接券商",
	})
	if err != nil {
		t.Fatal(err)
	}
	if position.Ticker != "PLAN" || position.ReferenceCostUSD == nil || *position.ReferenceCostUSD != cost {
		t.Fatalf("position = %#v", position)
	}
	portfolio, err := ListCandidateResearchPositions(context.Background(), db)
	if err != nil || portfolio.PositionCount != 1 || portfolio.TotalMaxWeightPct != 4 {
		t.Fatalf("portfolio=%#v err=%v", portfolio, err)
	}
	position, err = UpsertCandidateResearchPosition(context.Background(), db, CandidateResearchPositionInput{Ticker: "PLAN", MaxWeightPct: 6, MaxDailyVolumeParticipation: 10, ClearReferenceCostUSD: true})
	if err != nil || position.MaxWeightPct != 6 || position.ReferenceCostUSD != nil {
		t.Fatalf("updated position=%#v err=%v", position, err)
	}
	if _, err := UpsertCandidateResearchPosition(context.Background(), db, CandidateResearchPositionInput{Ticker: "BAD", MaxWeightPct: 101}); err == nil {
		t.Fatal("expected validation error")
	}
	if err := DeleteCandidateResearchPosition(context.Background(), db, position.ID); err != nil {
		t.Fatal(err)
	}
}
