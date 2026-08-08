package discovery

import (
	"context"
	"testing"
	"time"
)

func TestTradePlanSimulationUsesNextCloseAndTakeProfit(t *testing.T) {
	rows := tradePlanSimulationFixture("PLAN", 126_000_000)
	simulations := buildTradePlanSimulations(rows)
	if len(simulations) != 1 {
		t.Fatalf("simulations = %#v, want one", simulations)
	}
	simulation := simulations[0]
	if simulation.Status != TradePlanSimulationTarget || simulation.EntryDate == nil || simulation.ExitDate == nil {
		t.Fatalf("simulation = %#v", simulation)
	}
	if simulation.EntryDate.Format(time.DateOnly) != rows[200].TradeDate.Format(time.DateOnly) || simulation.EntryPriceUSD != 100 {
		t.Fatalf("entry = %#v, want next day close $100", simulation)
	}
	if simulation.ExitDate.Format(time.DateOnly) != rows[201].TradeDate.Format(time.DateOnly) || simulation.ExitPriceUSD != 126 || simulation.ReturnPct <= 0 || simulation.RMultiple <= 0 || simulation.HoldingDays != 1 {
		t.Fatalf("exit = %#v, want take-profit close", simulation)
	}
}

func TestRebuildTradePlanSimulationsPersistsUniqueSnapshots(t *testing.T) {
	db := openMigratedTestDatabase(t)
	prices := tradePlanSimulationFixture("PLAN", 126_000_000)
	if err := db.Create(&prices).Error; err != nil {
		t.Fatal(err)
	}
	first, err := RebuildTradePlanSimulations(context.Background(), db, []string{"plan"})
	if err != nil {
		t.Fatal(err)
	}
	if first.CreatedCount != 1 || first.TotalCount != 1 || first.ClosedCount != 1 || first.WinRatePct == nil || *first.WinRatePct != 100 {
		t.Fatalf("first result = %#v", first)
	}
	second, err := RebuildTradePlanSimulations(context.Background(), db, []string{"PLAN"})
	if err != nil {
		t.Fatal(err)
	}
	if second.CreatedCount != 0 || second.UpdatedCount != 1 || second.TotalCount != 1 {
		t.Fatalf("second result = %#v", second)
	}
}

func tradePlanSimulationFixture(ticker string, terminalClose int64) []PriceSnapshot {
	base := time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC)
	rows := make([]PriceSnapshot, 0, 202)
	for day := 0; day < 200; day++ {
		volume := int64(100_000)
		if day == 199 {
			volume = 200_000
		}
		rows = append(rows, PriceSnapshot{Source: "test", Symbol: ticker, TradeDate: base.AddDate(0, 0, day), CloseMicros: int64(10_000_000 + day*450_000), Volume: volume, Currency: "USD", QualityStatus: QualityStatusValid})
	}
	rows = append(rows,
		PriceSnapshot{Source: "test", Symbol: ticker, TradeDate: base.AddDate(0, 0, 200), CloseMicros: 100_000_000, Volume: 100_000, Currency: "USD", QualityStatus: QualityStatusValid},
		PriceSnapshot{Source: "test", Symbol: ticker, TradeDate: base.AddDate(0, 0, 201), CloseMicros: terminalClose, Volume: 100_000, Currency: "USD", QualityStatus: QualityStatusValid},
	)
	return rows
}
