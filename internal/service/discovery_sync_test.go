package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"sec_monitor/internal/config"
	"sec_monitor/internal/discovery"
)

type fakeDiscoveryRunner struct {
	securityBatch discovery.UniverseBatch
	marketBatch   discovery.UniverseBatch
	securityErr   error
	marketErr     error
	securityCalls int
	marketCalls   int
}

type stubServiceCalendar struct{}

func (s *stubServiceCalendar) IsTradingDate(context.Context, string) (bool, error) { return true, nil }
func (s *stubServiceCalendar) IsTradingDay(context.Context, time.Time) (bool, error) {
	return true, nil
}

func (f *fakeDiscoveryRunner) SyncSecurityUniverse(ctx context.Context) (discovery.UniverseBatch, error) {
	f.securityCalls++
	return f.securityBatch, f.securityErr
}

func (f *fakeDiscoveryRunner) SyncMarketPrices(ctx context.Context) (discovery.UniverseBatch, error) {
	f.marketCalls++
	return f.marketBatch, f.marketErr
}

func TestDiscoverySyncServiceRunsSecurityAndMarket(t *testing.T) {
	discoveryDB := testDiscoveryDB(t)
	runner := &fakeDiscoveryRunner{
		securityBatch: discovery.UniverseBatch{BatchID: "security", Kind: discovery.BatchKindSecurity, Status: discovery.BatchStatusPublished, StartedAt: time.Now()},
		marketBatch:   discovery.UniverseBatch{BatchID: "market", Kind: discovery.BatchKindPrescreen, Status: discovery.BatchStatusPublished, StartedAt: time.Now()},
	}
	result, err := NewDiscoverySyncService(discoveryDB, config.DiscoveryConfig{}).withRunner(runner).Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != DiscoverySyncStatusPublished || result.SecurityBatchID != "security" || result.MarketBatchID != "market" {
		t.Fatalf("result = %#v", result)
	}
	if runner.securityCalls != 1 || runner.marketCalls != 1 {
		t.Fatalf("calls security=%d market=%d", runner.securityCalls, runner.marketCalls)
	}
}

func TestDiscoverySyncServiceReportsMarketFailureAfterSecuritySync(t *testing.T) {
	discoveryDB := testDiscoveryDB(t)
	runner := &fakeDiscoveryRunner{
		securityBatch: discovery.UniverseBatch{BatchID: "security", Kind: discovery.BatchKindSecurity, Status: discovery.BatchStatusPublished},
		marketErr:     errors.New("provider inactive"),
	}
	result, err := NewDiscoverySyncService(discoveryDB, config.DiscoveryConfig{}).withRunner(runner).Run(context.Background())
	if err == nil || !errors.Is(err, ErrDiscoveryMarketSync) {
		t.Fatalf("err = %v, want market sync wrapper", err)
	}
	if result.Status != DiscoverySyncStatusMarketFailed || result.SecurityBatchID != "security" {
		t.Fatalf("result = %#v", result)
	}
}

func TestDiscoverySyncServiceBuildsRunnerWithoutMarketURL(t *testing.T) {
	discoveryDB := testDiscoveryDB(t)
	runner, err := NewDiscoverySyncService(discoveryDB, config.DiscoveryConfig{}).buildRunner()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runner.SyncMarketPrices(context.Background()); err == nil || !strings.Contains(err.Error(), "SMALL_CAP_STOOQ_URLS") {
		t.Fatalf("market err = %v, want missing SMALL_CAP_STOOQ_URLS", err)
	}
}

func TestDiscoverySyncServiceBuildsTiingoRunnerFromToken(t *testing.T) {
	discoveryDB := testDiscoveryDB(t)
	runner, err := NewDiscoverySyncService(discoveryDB, config.DiscoveryConfig{
		PriceProvider:  "tiingo",
		TiingoAPIToken: "test-token",
		TiingoBaseURL:  "https://api.tiingo.com",
	}).buildRunner()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runner.SyncMarketPrices(context.Background()); err == nil || strings.Contains(err.Error(), "SMALL_CAP_STOOQ_URLS") {
		t.Fatalf("market err = %v, want tiingo runner without stooq config error", err)
	}
}

func TestDiscoverySyncServiceUsesStoredDiscoveryConfig(t *testing.T) {
	mainDB := testDB(t)
	configs := NewConfigService(mainDB, NewAuditService(mainDB))
	if err := configs.UpsertMany(context.Background(), []ConfigInput{
		{Key: "discovery.price_provider", Value: "tiingo", ValueType: "string", Category: "discovery"},
		{Key: "discovery.tiingo_api_token", Value: "stored-token", ValueType: "string", Category: "discovery", Encrypted: true},
		{Key: "discovery.tiingo_base_url", Value: "https://api.tiingo.com", ValueType: "string", Category: "discovery"},
	}, "tester"); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	discoveryDB := testDiscoveryDB(t)
	runner, err := NewDiscoverySyncService(discoveryDB, config.DiscoveryConfig{}).WithConfigService(configs).buildRunner()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runner.SyncMarketPrices(context.Background()); err == nil || strings.Contains(err.Error(), "SMALL_CAP_STOOQ_URLS") {
		t.Fatalf("market err = %v, want stored tiingo config without stooq config error", err)
	}
}

func TestDiscoverySyncServiceRejectsTiingoWithoutToken(t *testing.T) {
	discoveryDB := testDiscoveryDB(t)
	_, err := NewDiscoverySyncService(discoveryDB, config.DiscoveryConfig{
		PriceProvider:  "tiingo",
		TiingoBaseURL:  "https://api.tiingo.com",
		TaskTimeoutMin: 1,
	}).buildRunner()
	if err == nil || !strings.Contains(err.Error(), "TIINGO_API_TOKEN") {
		t.Fatalf("err = %v, want TIINGO_API_TOKEN error", err)
	}
}

func TestDiscoverySyncServiceBuildsProviderChain(t *testing.T) {
	discoveryDB := testDiscoveryDB(t)
	provider, marketErr, err := NewDiscoverySyncService(discoveryDB, config.DiscoveryConfig{
		PriceProvider:           "tiingo,twelvedata,yahoo",
		TiingoAPIToken:          "test-token",
		TiingoBaseURL:           "https://api.tiingo.com",
		TwelveDataAPIKey:        "td-key",
		TwelveDataBaseURL:       "https://api.twelvedata.com",
		TwelveDataRequestBudget: 10,
		YahooBaseURL:            "https://query1.finance.yahoo.com",
		TiingoRequestBudget:     10,
		YahooRequestBudget:      10,
	}).buildPriceProvider(config.DiscoveryConfig{
		PriceProvider:           "tiingo,twelvedata,yahoo",
		TiingoAPIToken:          "test-token",
		TiingoBaseURL:           "https://api.tiingo.com",
		TwelveDataAPIKey:        "td-key",
		TwelveDataBaseURL:       "https://api.twelvedata.com",
		TwelveDataRequestBudget: 10,
		YahooBaseURL:            "https://query1.finance.yahoo.com",
		TiingoRequestBudget:     10,
		YahooRequestBudget:      10,
	}, nil, &stubServiceCalendar{})
	if err != nil || marketErr != nil {
		t.Fatalf("buildPriceProvider err=%v marketErr=%v", err, marketErr)
	}
	named, ok := provider.(discovery.NamedPriceProvider)
	if !ok || named.ProviderName() != "chain" {
		t.Fatalf("provider=%T", provider)
	}
	allowlist, ok := provider.(discovery.RecordSourceAllowlistProvider)
	if !ok || strings.Join(allowlist.AllowedRecordSources(), ",") != "tiingo,twelvedata,yahoo" {
		t.Fatalf("allowed sources = %#v", allowlist.AllowedRecordSources())
	}
}
