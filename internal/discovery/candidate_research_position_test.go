package discovery

import (
	"context"
	"math"
	"testing"
	"time"
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
	if portfolio.DataGapCount != 1 || len(portfolio.Items) != 1 || len(portfolio.Items[0].RiskFlags) == 0 || portfolio.Items[0].Quality.QualityStatus != QualityStatusMissing {
		t.Fatalf("portfolio risk enrichment=%#v", portfolio)
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

func TestCandidateResearchPortfolioAggregatesRiskWeightsAndConcentration(t *testing.T) {
	db := openMigratedTestDatabase(t)
	positions := []CandidateResearchPosition{
		{Ticker: "ALPHA", MaxWeightPct: 30, MaxDailyVolumeParticipation: 5},
		{Ticker: "BETA", MaxWeightPct: 20},
		{Ticker: "GAMMA", MaxWeightPct: 10},
	}
	if err := db.Create(&positions).Error; err != nil {
		t.Fatal(err)
	}
	portfolio, err := ListCandidateResearchPositions(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	if portfolio.LargestPosition != "ALPHA" || portfolio.LargestPositionWeight != 30 || portfolio.TopThreeWeightPct != 60 {
		t.Fatalf("position concentration = %+v", portfolio)
	}
	wantHHI := (0.5*0.5 + (1.0/3)*(1.0/3) + (1.0/6)*(1.0/6)) * 100
	if math.Abs(portfolio.ConcentrationIndex-wantHHI) > 0.001 {
		t.Fatalf("concentration index = %f, want %f", portfolio.ConcentrationIndex, wantHHI)
	}
	if portfolio.DataGapWeightPct != 60 || portfolio.RiskCoverage["market_beta"] != "missing" {
		t.Fatalf("risk coverage = %+v", portfolio)
	}
}

func TestCandidatePortfolioRiskUsesPointInTimeIWMReturns(t *testing.T) {
	db := openMigratedTestDatabase(t)
	if err := db.Create(&CandidateResearchPosition{Ticker: "RISK", MaxWeightPct: 10}).Error; err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	for day := 0; day < 30; day++ {
		date := start.AddDate(0, 0, day)
		benchmark := int64((100.0 + float64(day)) * 1_000_000)
		asset := int64((50.0 + float64(day)) * 1_000_000)
		rows := []PriceSnapshot{{Source: "test", SourceVersion: "v1", Symbol: "IWM", TradeDate: date, CloseMicros: benchmark, QualityStatus: QualityStatusValid}, {Source: "test", SourceVersion: "v1", Symbol: "RISK", TradeDate: date, CloseMicros: asset, QualityStatus: QualityStatusValid}}
		if err := db.Create(&rows).Error; err != nil {
			t.Fatal(err)
		}
	}
	portfolio, err := ListCandidateResearchPositions(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	if portfolio.RiskAnalysis.WeightedMarketBeta == nil || portfolio.RiskAnalysis.BetaCoveredWeight != 10 || len(portfolio.RiskAnalysis.Scenarios) != 4 {
		t.Fatalf("risk analysis = %+v", portfolio.RiskAnalysis)
	}
}

func TestResearchPositionRiskGateAllowsReductionsAndRequiresOverrideForIncreases(t *testing.T) {
	cost := 10.0
	before := CandidateResearchPosition{Ticker: "PLAN", MaxWeightPct: 5, ReferenceCostUSD: &cost}
	if !ResearchPositionIncreasesRisk(CandidateResearchPosition{}, false, CandidateResearchPositionInput{Ticker: "NEW", MaxWeightPct: 1}) {
		t.Fatal("new positive research allocation must be gated")
	}
	if !ResearchPositionIncreasesRisk(before, true, CandidateResearchPositionInput{Ticker: "PLAN", MaxWeightPct: 6}) {
		t.Fatal("increased allocation must be gated")
	}
	if ResearchPositionIncreasesRisk(before, true, CandidateResearchPositionInput{Ticker: "PLAN", MaxWeightPct: 4, Note: "reduce while documenting risk"}) {
		t.Fatal("reductions and notes-only changes must remain available")
	}

	db := openMigratedTestDatabase(t)
	gate, err := BuildResearchActionGate(context.Background(), db, time.Date(2026, 8, 24, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if gate.Allowed || gate.Status != ResearchActionGateBlocked || len(gate.Reasons) == 0 {
		t.Fatalf("empty local facts must block new research action: %+v", gate)
	}
}
