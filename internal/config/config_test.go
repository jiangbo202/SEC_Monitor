package config

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestLoadDiscoveryDefaults(t *testing.T) {
	t.Setenv("DB_DSN", "")
	for _, key := range []string{
		"SMALL_CAP_DATABASE_TYPE",
		"SMALL_CAP_DATABASE_DSN",
		"SMALL_CAP_CACHE_DIR",
		"SMALL_CAP_NASDAQ_LISTED_URL",
		"SMALL_CAP_NASDAQ_OTHER_LISTED_URL",
		"SMALL_CAP_SEC_TICKER_EXCHANGE_URL",
		"SMALL_CAP_SEC_SUBMISSIONS_URL",
		"SMALL_CAP_SEC_COMPANY_FACTS_URL",
		"SMALL_CAP_STOOQ_URLS",
		"SMALL_CAP_TASK_TIMEOUT_MINUTES",
	} {
		t.Setenv(key, "")
	}

	cfg := Load()
	if cfg.Discovery.Database.Type != "sqlite" {
		t.Fatalf("database type = %q", cfg.Discovery.Database.Type)
	}
	if cfg.Discovery.Database.DSN != filepath.Join("data", "small_cap.db") {
		t.Fatalf("database dsn = %q", cfg.Discovery.Database.DSN)
	}
	if cfg.Discovery.CacheDir != ".cache/discovery" {
		t.Fatalf("cache dir = %q", cfg.Discovery.CacheDir)
	}
	if cfg.Discovery.NasdaqListedURL != "https://www.nasdaqtrader.com/dynamic/SymDir/nasdaqlisted.txt" {
		t.Fatalf("nasdaq listed url = %q", cfg.Discovery.NasdaqListedURL)
	}
	if cfg.Discovery.NasdaqOtherListedURL != "https://www.nasdaqtrader.com/dynamic/SymDir/otherlisted.txt" {
		t.Fatalf("nasdaq other listed url = %q", cfg.Discovery.NasdaqOtherListedURL)
	}
	if cfg.Discovery.SECTickerExchangeURL != "https://www.sec.gov/files/company_tickers_exchange.json" {
		t.Fatalf("sec ticker exchange url = %q", cfg.Discovery.SECTickerExchangeURL)
	}
	if cfg.Discovery.SECSubmissionsURL != "https://www.sec.gov/Archives/edgar/daily-index/bulkdata/submissions.zip" {
		t.Fatalf("sec submissions url = %q", cfg.Discovery.SECSubmissionsURL)
	}
	if cfg.Discovery.SECCompanyFactsURL != "https://www.sec.gov/Archives/edgar/daily-index/xbrl/companyfacts.zip" {
		t.Fatalf("sec company facts url = %q", cfg.Discovery.SECCompanyFactsURL)
	}
	if cfg.Discovery.StooqURLs != nil {
		t.Fatalf("stooq urls = %#v", cfg.Discovery.StooqURLs)
	}
	if cfg.Discovery.TaskTimeoutMin != 60 {
		t.Fatalf("task timeout = %d", cfg.Discovery.TaskTimeoutMin)
	}
}

func TestLoadDiscoveryDatabaseDefaultsToMainDatabaseSibling(t *testing.T) {
	t.Setenv("DB_DSN", filepath.Join("custom", "dated", "sec_monitor.db"))
	t.Setenv("SMALL_CAP_DATABASE_DSN", "")

	if got := Load().Discovery.Database.DSN; got != filepath.Join("custom", "dated", "small_cap.db") {
		t.Fatalf("database dsn = %q", got)
	}
}

func TestLoadDiscoveryOverrides(t *testing.T) {
	t.Setenv("SMALL_CAP_DATABASE_TYPE", "sqlite")
	t.Setenv("SMALL_CAP_DATABASE_DSN", "tmp/discovery.db")
	t.Setenv("SMALL_CAP_CACHE_DIR", "tmp/cache")
	t.Setenv("SMALL_CAP_NASDAQ_LISTED_URL", "https://example.test/nasdaq-listed")
	t.Setenv("SMALL_CAP_NASDAQ_OTHER_LISTED_URL", "https://example.test/nasdaq-other")
	t.Setenv("SMALL_CAP_SEC_TICKER_EXCHANGE_URL", "https://example.test/tickers")
	t.Setenv("SMALL_CAP_SEC_SUBMISSIONS_URL", "https://example.test/submissions")
	t.Setenv("SMALL_CAP_SEC_COMPANY_FACTS_URL", "https://example.test/facts")
	t.Setenv("SMALL_CAP_STOOQ_URLS", " https://example.test/a , ,https://example.test/b  ")
	t.Setenv("SMALL_CAP_TASK_TIMEOUT_MINUTES", "15")

	cfg := Load().Discovery
	if cfg.Database != (DatabaseConfig{Type: "sqlite", DSN: "tmp/discovery.db"}) {
		t.Fatalf("database = %#v", cfg.Database)
	}
	if cfg.CacheDir != "tmp/cache" ||
		cfg.NasdaqListedURL != "https://example.test/nasdaq-listed" ||
		cfg.NasdaqOtherListedURL != "https://example.test/nasdaq-other" ||
		cfg.SECTickerExchangeURL != "https://example.test/tickers" ||
		cfg.SECSubmissionsURL != "https://example.test/submissions" ||
		cfg.SECCompanyFactsURL != "https://example.test/facts" {
		t.Fatalf("source overrides = %#v", cfg)
	}
	if !reflect.DeepEqual(cfg.StooqURLs, []string{"https://example.test/a", "https://example.test/b"}) {
		t.Fatalf("stooq urls = %#v", cfg.StooqURLs)
	}
	if cfg.TaskTimeoutMin != 15 {
		t.Fatalf("task timeout = %d", cfg.TaskTimeoutMin)
	}
}

func TestLoadDiscoveryTaskTimeoutFallsBackForNonPositiveOrInvalidValues(t *testing.T) {
	for _, value := range []string{"0", "-1", "invalid"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("SMALL_CAP_TASK_TIMEOUT_MINUTES", value)
			if got := Load().Discovery.TaskTimeoutMin; got != 60 {
				t.Fatalf("task timeout = %d, want 60", got)
			}
		})
	}
}

func TestLoadTableDrivenEnvOverrides(t *testing.T) {
	tests := []struct {
		name   string
		key    string
		value  string
		assert func(t *testing.T, cfg Config)
	}{
		{name: "server address", key: "APP_ADDR", value: ":9090", assert: func(t *testing.T, cfg Config) {
			if cfg.Server.Address != ":9090" {
				t.Fatalf("address = %q", cfg.Server.Address)
			}
		}},
		{name: "database dsn", key: "DB_DSN", value: "data/test.db", assert: func(t *testing.T, cfg Config) {
			if cfg.Database.DSN != "data/test.db" {
				t.Fatalf("dsn = %q", cfg.Database.DSN)
			}
		}},
		{name: "sec user agent", key: "SEC_USER_AGENT", value: "agent", assert: func(t *testing.T, cfg Config) {
			if cfg.SEC.UserAgent != "agent" {
				t.Fatalf("user agent = %q", cfg.SEC.UserAgent)
			}
		}},
		{name: "retention days", key: "DATA_RETENTION_DAYS", value: "45", assert: func(t *testing.T, cfg Config) {
			if cfg.System.DataRetentionDays != 45 {
				t.Fatalf("retention = %d", cfg.System.DataRetentionDays)
			}
		}},
		{name: "storage by day", key: "STORAGE_BY_DAY", value: "true", assert: func(t *testing.T, cfg Config) {
			if !cfg.System.StorageByDay {
				t.Fatalf("storage by day = false")
			}
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(tt.key, tt.value)
			tt.assert(t, Load())
		})
	}
}

func TestLoadFallsBackForInvalidTypedValues(t *testing.T) {
	t.Setenv("SEC_TIMEOUT_MS", "bad")
	t.Setenv("DATA_RETENTION_DAYS", "bad")
	t.Setenv("STORAGE_BY_DAY", "bad")

	cfg := Load()
	if cfg.SEC.TimeoutMS != 10000 {
		t.Fatalf("timeout = %d", cfg.SEC.TimeoutMS)
	}
	if cfg.System.DataRetentionDays != 30 {
		t.Fatalf("retention = %d", cfg.System.DataRetentionDays)
	}
	if cfg.System.StorageByDay {
		t.Fatalf("storage by day should fall back to false")
	}
}
