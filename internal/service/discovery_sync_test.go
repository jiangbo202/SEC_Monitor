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

func TestDiscoverySyncServiceRunMarketOnlyTableDriven(t *testing.T) {
	tests := []struct {
		name       string
		marketErr  error
		wantStatus string
		wantErr    bool
	}{
		{name: "publishes market batch", wantStatus: DiscoverySyncStatusPublished},
		{name: "reports market failure", marketErr: errors.New("market unavailable"), wantStatus: DiscoverySyncStatusMarketFailed, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			discoveryDB := testDiscoveryDB(t)
			runner := &fakeDiscoveryRunner{
				marketBatch: discovery.UniverseBatch{BatchID: "market", Kind: discovery.BatchKindPrescreen, Status: discovery.BatchStatusPublished, StartedAt: time.Now()},
				marketErr:   tt.marketErr,
			}
			result, err := NewDiscoverySyncService(discoveryDB, config.DiscoveryConfig{}).withRunner(runner).RunMarketOnly(context.Background())
			if (err != nil) != tt.wantErr {
				t.Fatalf("RunMarketOnly err=%v wantErr=%v", err, tt.wantErr)
			}
			if tt.wantErr && !errors.Is(err, ErrDiscoveryMarketSync) {
				t.Fatalf("RunMarketOnly err=%v, want ErrDiscoveryMarketSync", err)
			}
			if result.Status != tt.wantStatus || result.MarketBatchID != "market" || result.SecurityBatchID != "" {
				t.Fatalf("result = %#v, want status=%s market only", result, tt.wantStatus)
			}
			if runner.securityCalls != 0 || runner.marketCalls != 1 {
				t.Fatalf("calls security=%d market=%d", runner.securityCalls, runner.marketCalls)
			}
		})
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

func TestDiscoverySyncServiceBuildSinglePriceProviderTableDriven(t *testing.T) {
	tests := []struct {
		name        string
		cfg         config.DiscoveryConfig
		wantName    string
		wantMarket  string
		wantSetup   string
		wantNilProv bool
	}{
		{
			name:     "builds stooq zip provider",
			cfg:      config.DiscoveryConfig{PriceProvider: "stooq", StooqURLs: []string{"https://prices.test/stooq.zip"}},
			wantName: "stooq",
		},
		{
			name:     "builds yahoo provider",
			cfg:      config.DiscoveryConfig{PriceProvider: "yahoo", YahooBaseURL: "https://query1.finance.yahoo.com", YahooRequestBudget: 10},
			wantName: "yahoo",
		},
		{
			name:        "missing twelvedata key returns setup error",
			cfg:         config.DiscoveryConfig{PriceProvider: "twelvedata", TwelveDataBaseURL: "https://api.twelvedata.com"},
			wantSetup:   "TWELVE_DATA_API_KEY",
			wantNilProv: true,
		},
		{
			name:        "unsupported provider returns setup error",
			cfg:         config.DiscoveryConfig{PriceProvider: "unknown"},
			wantSetup:   "unsupported SMALL_CAP_PRICE_PROVIDER",
			wantNilProv: true,
		},
		{
			name:        "stooq without url returns market error",
			cfg:         config.DiscoveryConfig{PriceProvider: "stooq"},
			wantMarket:  "SMALL_CAP_STOOQ_URLS",
			wantNilProv: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			discoveryDB := testDiscoveryDB(t)
			provider, marketErr, setupErr := NewDiscoverySyncService(discoveryDB, config.DiscoveryConfig{}).buildSinglePriceProvider(tt.cfg, &discovery.Downloader{}, &stubServiceCalendar{})
			if tt.wantMarket != "" {
				if marketErr == nil || !strings.Contains(marketErr.Error(), tt.wantMarket) {
					t.Fatalf("marketErr = %v, want %q", marketErr, tt.wantMarket)
				}
			} else if marketErr != nil {
				t.Fatalf("marketErr = %v", marketErr)
			}
			if tt.wantSetup != "" {
				if setupErr == nil || !strings.Contains(setupErr.Error(), tt.wantSetup) {
					t.Fatalf("setupErr = %v, want %q", setupErr, tt.wantSetup)
				}
			} else if setupErr != nil {
				t.Fatalf("setupErr = %v", setupErr)
			}
			if (provider == nil) != tt.wantNilProv {
				t.Fatalf("provider = %T, want nil=%v", provider, tt.wantNilProv)
			}
			if tt.wantName != "" {
				named, ok := provider.(discovery.NamedPriceProvider)
				if !ok || named.ProviderName() != tt.wantName {
					t.Fatalf("provider name = %T %#v, want %s", provider, provider, tt.wantName)
				}
			}
		})
	}
}

func TestDiscoverySyncServiceBuildPriceProviderChainErrorsWhenNoUsableProvider(t *testing.T) {
	discoveryDB := testDiscoveryDB(t)
	_, marketErr, setupErr := NewDiscoverySyncService(discoveryDB, config.DiscoveryConfig{}).buildPriceProvider(config.DiscoveryConfig{
		PriceProvider: "twelvedata,unknown",
	}, nil, &stubServiceCalendar{})
	if marketErr != nil {
		t.Fatalf("marketErr = %v", marketErr)
	}
	if setupErr == nil || !strings.Contains(setupErr.Error(), "price provider chain has no usable provider") {
		t.Fatalf("setupErr = %v, want no usable provider", setupErr)
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
