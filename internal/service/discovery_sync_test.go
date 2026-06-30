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
