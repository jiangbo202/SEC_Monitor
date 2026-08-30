package discovery

import (
	"context"
	"testing"
	"time"
)

func TestResearchTradeLedgerCalculatesWeightedCostAndPnL(t *testing.T) {
	db := openMigratedTestDatabase(t)
	now := time.Date(2026, 8, 30, 2, 0, 0, 0, time.UTC)
	if err := db.Create(&PriceSnapshot{Source: "test", SourceVersion: "v1", Symbol: "LEDG", TradeDate: now, CloseMicros: 15_000_000, QualityStatus: QualityStatusValid}).Error; err != nil {
		t.Fatal(err)
	}
	decision, err := CreateResearchTradeDecision(context.Background(), db, ResearchTradeDecisionInput{Ticker: "ledg", Action: "open", Rationale: "验证真实成交账本"}, now.Add(-3*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := CreateResearchTradeExecution(context.Background(), db, ResearchTradeExecutionInput{DecisionID: &decision.ID, Ticker: "LEDG", Side: "buy", Shares: 10, PriceUSD: 10, FeesUSD: 1}, now.Add(-2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := CreateResearchTradeExecution(context.Background(), db, ResearchTradeExecutionInput{Ticker: "LEDG", Side: "buy", Shares: 10, PriceUSD: 12, FeesUSD: 1}, now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := CreateResearchTradeExecution(context.Background(), db, ResearchTradeExecutionInput{Ticker: "LEDG", Side: "sell", Shares: 5, PriceUSD: 14, FeesUSD: 1}, now); err != nil {
		t.Fatal(err)
	}
	ledger, err := ListResearchTradeLedger(context.Background(), db, "LEDG", now)
	if err != nil {
		t.Fatal(err)
	}
	if len(ledger.Positions) != 1 || ledger.Positions[0].Shares != 15 || ledger.OpenPositions != 1 {
		t.Fatalf("unexpected positions: %+v", ledger.Positions)
	}
	position := ledger.Positions[0]
	if position.AverageCostUSD < 11.09 || position.AverageCostUSD > 11.11 {
		t.Fatalf("average cost = %f", position.AverageCostUSD)
	}
	if position.RealizedPnLUSD < 13.49 || position.RealizedPnLUSD > 13.51 {
		t.Fatalf("realized pnl = %f", position.RealizedPnLUSD)
	}
	if position.UnrealizedPnLUSD == nil || *position.UnrealizedPnLUSD < 58.49 || *position.UnrealizedPnLUSD > 58.51 {
		t.Fatalf("unrealized pnl = %+v", position.UnrealizedPnLUSD)
	}
}

func TestResearchTradeLedgerRejectsOversellAndMismatchedDecision(t *testing.T) {
	db := openMigratedTestDatabase(t)
	now := time.Now().UTC()
	decision, err := CreateResearchTradeDecision(context.Background(), db, ResearchTradeDecisionInput{Ticker: "SAFE", Action: "open", Rationale: "建立研究观察仓位"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := CreateResearchTradeExecution(context.Background(), db, ResearchTradeExecutionInput{DecisionID: &decision.ID, Ticker: "OTHER", Side: "buy", Shares: 1, PriceUSD: 1}, now); err == nil {
		t.Fatal("expected mismatched decision to fail")
	}
	if _, err := CreateResearchTradeExecution(context.Background(), db, ResearchTradeExecutionInput{Ticker: "SAFE", Side: "sell", Shares: 1, PriceUSD: 1}, now); err == nil {
		t.Fatal("expected oversell to fail")
	}
}
