package discovery

import (
	"context"
	"strings"
	"testing"
	"time"

	"sec_monitor/internal/config"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestGetProviderObservabilityUsesRecordedDataWithoutCredentials(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := Migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	ctx := context.Background()
	date := time.Date(2026, 7, 27, 0, 0, 0, 0, time.UTC)
	batch := UniverseBatch{BatchID: "market-1", Kind: BatchKindPrescreen, Status: BatchStatusPublished, EffectiveDate: "2026-07-27", SourceVersionsJSON: "[]", ContentSHA256: strings.Repeat("a", 64), StartedAt: date}
	if err := db.Create(&batch).Error; err != nil {
		t.Fatalf("create batch: %v", err)
	}
	run := ProviderRun{BatchID: batch.BatchID, Provider: "tiingo,twelvedata", Status: ProviderStatusActive, SourceVersion: "chain-v1", EffectiveDate: date, ExpectedCount: 2, RecordCount: 2, CoveragePct: 100, Timely: true, CreatedAt: date.Add(time.Hour)}
	if err := db.Create(&run).Error; err != nil {
		t.Fatalf("create run: %v", err)
	}
	prices := []PriceSnapshot{
		{Source: "tiingo", SourceVersion: run.SourceVersion, Symbol: "ALPH", TradeDate: date, CloseMicros: 1_000_000, Currency: "USD", QualityStatus: QualityStatusValid},
		{Source: "twelvedata", SourceVersion: run.SourceVersion, Symbol: "BETA", TradeDate: date, CloseMicros: 2_000_000, Currency: "USD", QualityStatus: QualityStatusValid},
	}
	if err := db.Create(&prices).Error; err != nil {
		t.Fatalf("create price snapshots: %v", err)
	}
	if err := db.Create(&[]ProviderHealth{
		{Provider: "tiingo", Status: ProviderStatusActive, LastTradeDate: "2026-07-27", QualifiedTradingDays: 3, UpdatedAt: date},
		{Provider: "tiingo,twelvedata", Status: ProviderStatusValidation, LastTradeDate: "2026-07-27", QualifiedTradingDays: 1, UpdatedAt: date},
	}).Error; err != nil {
		t.Fatalf("create provider health: %v", err)
	}

	result, err := GetProviderObservability(ctx, db, config.DiscoveryConfig{
		PriceProvider:           "tiingo,twelvedata",
		TiingoAPIToken:          "primary-token",
		TiingoAPITokens:         []string{"secondary-token", "primary-token"},
		TiingoRequestBudget:     45,
		TwelveDataAPIKey:        "twelve-key",
		TwelveDataRequestBudget: 700,
	})
	if err != nil {
		t.Fatalf("GetProviderObservability: %v", err)
	}
	if result.LatestRun == nil || result.LatestRun.BatchID != batch.BatchID {
		t.Fatalf("latest run = %+v, want market batch", result.LatestRun)
	}
	if result.ChainHealth == nil || result.ChainHealth.Provider != "tiingo,twelvedata" {
		t.Fatalf("chain health = %+v", result.ChainHealth)
	}
	if result.LatestPriceSourceCounts["tiingo"] != 1 || result.LatestPriceSourceCounts["twelvedata"] != 1 {
		t.Fatalf("source counts = %+v", result.LatestPriceSourceCounts)
	}
	if len(result.CalendarYears) < 1 || result.CalendarYears[0].Year != 2026 || !result.CalendarYears[0].Complete {
		t.Fatalf("calendar years = %+v", result.CalendarYears)
	}
	if len(result.Providers) != 2 {
		t.Fatalf("providers = %+v", result.Providers)
	}
	tiingo := result.Providers[0]
	if tiingo.Provider != "tiingo" || tiingo.TokenCount != 2 || tiingo.LocalRequestBudget != 90 || tiingo.Health == nil || tiingo.Health.Status != ProviderStatusActive {
		t.Fatalf("tiingo observability = %+v", tiingo)
	}
	twelve := result.Providers[1]
	if twelve.Provider != "twelvedata" || !twelve.ConfiguredCredential || twelve.LocalRequestBudget != 700 || twelve.LatestSourceRecordCount != 1 {
		t.Fatalf("twelve observability = %+v", twelve)
	}
	if !strings.Contains(result.BudgetNotice, "不代表") {
		t.Fatalf("budget notice must clarify it is not account quota: %q", result.BudgetNotice)
	}
}

func TestProviderObservabilityConfigDoesNotExposeOrCountDuplicateTokens(t *testing.T) {
	item := providerObservabilityConfig("tiingo", config.DiscoveryConfig{
		TiingoAPIToken:      "same",
		TiingoAPITokens:     []string{"same,other", "other"},
		TiingoRequestBudget: 10,
	})
	if item.TokenCount != 2 || item.LocalRequestBudget != 20 || !item.ConfiguredCredential {
		t.Fatalf("tiingo config = %+v", item)
	}
	if item.BudgetScope != "per_token_per_run" {
		t.Fatalf("budget scope = %q", item.BudgetScope)
	}
}
