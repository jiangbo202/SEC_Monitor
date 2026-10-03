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
	attemptsJSON, err := encodeProviderAttempts([]ProviderAttempt{{Provider: "longbridge", Status: "partial", SourceVersion: "longbridge-v1", Expected: 2, Records: 1, Remaining: 1, CoveragePct: 50, ElapsedMS: 120}, {Provider: "futu", Status: "success", SourceVersion: "futu-v1", Expected: 1, Records: 1, Remaining: 0, CoveragePct: 100, ElapsedMS: 80}})
	if err != nil {
		t.Fatal(err)
	}
	run := ProviderRun{BatchID: batch.BatchID, Provider: "longbridge,futu", Status: ProviderStatusActive, SourceVersion: "chain-v1", EffectiveDate: date, ExpectedCount: 2, RecordCount: 2, CoveragePct: 100, Timely: true, AttemptsJSON: attemptsJSON, FallbackUsed: true, CreatedAt: date.Add(time.Hour)}
	if err := db.Create(&run).Error; err != nil {
		t.Fatalf("create run: %v", err)
	}
	prices := []PriceSnapshot{
		{Source: "longbridge", SourceVersion: run.SourceVersion, Symbol: "ALPH", TradeDate: date, CloseMicros: 1_000_000, Currency: "USD", QualityStatus: QualityStatusValid},
		{Source: "futu", SourceVersion: run.SourceVersion, Symbol: "BETA", TradeDate: date, CloseMicros: 2_000_000, Currency: "USD", QualityStatus: QualityStatusValid},
	}
	if err := db.Create(&prices).Error; err != nil {
		t.Fatalf("create price snapshots: %v", err)
	}
	if err := db.Create(&[]ProviderHealth{
		{Provider: "longbridge", Status: ProviderStatusActive, LastTradeDate: "2026-07-27", QualifiedTradingDays: 3, UpdatedAt: date},
		{Provider: "longbridge,futu", Status: ProviderStatusValidation, LastTradeDate: "2026-07-27", QualifiedTradingDays: 1, UpdatedAt: date},
	}).Error; err != nil {
		t.Fatalf("create provider health: %v", err)
	}

	result, err := GetProviderObservability(ctx, db, config.DiscoveryConfig{
		PriceProvider:    "longbridge,futu",
		FutuConfigured:   true,
		LongbridgeAppKey: "fake-key", LongbridgeAppSecret: "fake-secret", LongbridgeAccessToken: "fake-token",
	})
	if err != nil {
		t.Fatalf("GetProviderObservability: %v", err)
	}
	if result.LatestRun == nil || result.LatestRun.BatchID != batch.BatchID || !result.LatestRun.FallbackUsed || len(result.LatestRun.Attempts) != 2 {
		t.Fatalf("latest run = %+v, want market batch", result.LatestRun)
	}
	if result.ChainHealth == nil || result.ChainHealth.Provider != "longbridge,futu" {
		t.Fatalf("chain health = %+v", result.ChainHealth)
	}
	if result.LatestPriceSourceCounts["longbridge"] != 1 || result.LatestPriceSourceCounts["futu"] != 1 {
		t.Fatalf("source counts = %+v", result.LatestPriceSourceCounts)
	}
	if len(result.CalendarYears) < 1 || result.CalendarYears[0].Year != 2026 || !result.CalendarYears[0].Complete {
		t.Fatalf("calendar years = %+v", result.CalendarYears)
	}
	if len(result.Providers) != 2 {
		t.Fatalf("providers = %+v", result.Providers)
	}
	longbridge := result.Providers[0]
	if longbridge.Provider != "longbridge" || !longbridge.ConfiguredCredential || longbridge.BudgetScope != "provider_managed" || longbridge.Health == nil || longbridge.Health.Status != ProviderStatusActive || longbridge.LatestAttempt == nil || longbridge.LatestAttempt.Status != "partial" {
		t.Fatalf("longbridge observability = %+v", longbridge)
	}
	if longbridge.RecentAttemptCount != 1 || longbridge.RecentUsableCount != 1 || longbridge.RecentCompleteCount != 0 || longbridge.UsableRatePct != 100 {
		t.Fatalf("longbridge recent SLA = %+v", longbridge)
	}
	futu := result.Providers[1]
	if futu.Provider != "futu" || !futu.ConfiguredCredential || futu.BudgetScope != "provider_daily_local" || futu.LatestSourceRecordCount != 1 || futu.LatestAttempt == nil || futu.LatestAttempt.Status != "success" {
		t.Fatalf("futu observability = %+v", futu)
	}
	if futu.RecentAttemptCount != 1 || futu.RecentCompleteCount != 1 || futu.CompleteRatePct != 100 {
		t.Fatalf("futu recent SLA = %+v", futu)
	}
	if !strings.Contains(result.BudgetNotice, "不代表") {
		t.Fatalf("budget notice must clarify it is not account quota: %q", result.BudgetNotice)
	}
}
