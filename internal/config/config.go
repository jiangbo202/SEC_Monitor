package config

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type Config struct {
	Server    ServerConfig
	Database  DatabaseConfig
	Discovery DiscoveryConfig
	SEC       SECConfig
	System    SystemConfig
}

type ServerConfig struct {
	Address string
}

type DatabaseConfig struct {
	Type string
	DSN  string
}

type DiscoveryConfig struct {
	Database                    DatabaseConfig
	CacheDir                    string
	UserAgent                   string
	NasdaqListedURL             string
	NasdaqOtherListedURL        string
	SECTickerExchangeURL        string
	SECSubmissionsURL           string
	SECCompanyFactsURL          string
	PriceProvider               string
	StooqURLs                   []string
	TiingoAPIToken              string
	TiingoAPITokens             []string
	TiingoBaseURL               string
	TiingoConcurrency           int
	TiingoRequestBudget         int
	TiingoRequestIntervalMS     int
	TwelveDataAPIKey            string
	TwelveDataBaseURL           string
	TwelveDataRequestBudget     int
	TwelveDataRequestIntervalMS int
	YahooBaseURL                string
	YahooRequestBudget          int
	YahooRequestIntervalMS      int
	ResearchMode                bool
	AutoTechnicalHistoryWarmup  bool
	MinPublishCoveragePct       float64
	TaskTimeoutMin              int
	DownloadIdleTimeoutSec      int
	SECBulkCacheTTLHours        int
}

type SECConfig struct {
	BaseURL   string
	UserAgent string
	TimeoutMS int
}

type SystemConfig struct {
	LogLevel           string
	DataRetentionDays  int
	StorageByDay       bool
	EncryptionKey      []byte
	EncryptionKeyError string
}

func Load() Config {
	encryptionKey, encryptionKeyError := encryptionKeyFromEnvironment()
	database := DatabaseConfig{
		Type: valueOrDefault("DB_TYPE", "sqlite"),
		DSN:  valueOrDefault("DB_DSN", "data/sec_monitor.db"),
	}
	return Config{
		Server: ServerConfig{
			Address: valueOrDefault("APP_ADDR", ":8080"),
		},
		Database: database,
		Discovery: DiscoveryConfig{
			Database: DatabaseConfig{
				Type: valueOrDefault("SMALL_CAP_DATABASE_TYPE", "sqlite"),
				DSN:  valueOrDefault("SMALL_CAP_DATABASE_DSN", filepath.Join(filepath.Dir(database.DSN), "small_cap.db")),
			},
			CacheDir:                    valueOrDefault("SMALL_CAP_CACHE_DIR", ".cache/discovery"),
			UserAgent:                   valueOrDefault("SEC_USER_AGENT", "sec-monitor/0.1 contact@example.com"),
			NasdaqListedURL:             valueOrDefault("SMALL_CAP_NASDAQ_LISTED_URL", "https://www.nasdaqtrader.com/dynamic/SymDir/nasdaqlisted.txt"),
			NasdaqOtherListedURL:        valueOrDefault("SMALL_CAP_NASDAQ_OTHER_LISTED_URL", "https://www.nasdaqtrader.com/dynamic/SymDir/otherlisted.txt"),
			SECTickerExchangeURL:        valueOrDefault("SMALL_CAP_SEC_TICKER_EXCHANGE_URL", "https://www.sec.gov/files/company_tickers_exchange.json"),
			SECSubmissionsURL:           valueOrDefault("SMALL_CAP_SEC_SUBMISSIONS_URL", "https://www.sec.gov/Archives/edgar/daily-index/bulkdata/submissions.zip"),
			SECCompanyFactsURL:          valueOrDefault("SMALL_CAP_SEC_COMPANY_FACTS_URL", "https://www.sec.gov/Archives/edgar/daily-index/xbrl/companyfacts.zip"),
			PriceProvider:               strings.ToLower(strings.TrimSpace(os.Getenv("SMALL_CAP_PRICE_PROVIDER"))),
			StooqURLs:                   commaSeparatedValues("SMALL_CAP_STOOQ_URLS"),
			TiingoAPIToken:              strings.TrimSpace(os.Getenv("TIINGO_API_TOKEN")),
			TiingoAPITokens:             commaSeparatedValues("TIINGO_API_TOKENS"),
			TiingoBaseURL:               valueOrDefault("SMALL_CAP_TIINGO_BASE_URL", "https://api.tiingo.com"),
			TiingoConcurrency:           positiveIntOrDefault("SMALL_CAP_TIINGO_CONCURRENCY", 1),
			TiingoRequestBudget:         intOrDefault("SMALL_CAP_TIINGO_REQUEST_BUDGET", 45),
			TiingoRequestIntervalMS:     positiveIntOrDefault("SMALL_CAP_TIINGO_REQUEST_INTERVAL_MS", 1000),
			TwelveDataAPIKey:            strings.TrimSpace(os.Getenv("TWELVE_DATA_API_KEY")),
			TwelveDataBaseURL:           valueOrDefault("SMALL_CAP_TWELVE_DATA_BASE_URL", "https://api.twelvedata.com"),
			TwelveDataRequestBudget:     intOrDefault("SMALL_CAP_TWELVE_DATA_REQUEST_BUDGET", 700),
			TwelveDataRequestIntervalMS: positiveIntOrDefault("SMALL_CAP_TWELVE_DATA_REQUEST_INTERVAL_MS", 8000),
			YahooBaseURL:                valueOrDefault("SMALL_CAP_YAHOO_BASE_URL", "https://query1.finance.yahoo.com"),
			YahooRequestBudget:          intOrDefault("SMALL_CAP_YAHOO_REQUEST_BUDGET", 45),
			YahooRequestIntervalMS:      positiveIntOrDefault("SMALL_CAP_YAHOO_REQUEST_INTERVAL_MS", 1000),
			ResearchMode:                boolOrDefault("SMALL_CAP_RESEARCH_MODE", true),
			AutoTechnicalHistoryWarmup:  boolOrDefault("SMALL_CAP_AUTO_TECHNICAL_HISTORY_WARMUP", true),
			MinPublishCoveragePct:       floatOrDefault("SMALL_CAP_MIN_PUBLISH_COVERAGE_PCT", 85),
			TaskTimeoutMin:              positiveIntOrDefault("SMALL_CAP_TASK_TIMEOUT_MINUTES", 60),
			DownloadIdleTimeoutSec:      positiveIntOrDefault("SMALL_CAP_DOWNLOAD_IDLE_TIMEOUT_SECONDS", 90),
			SECBulkCacheTTLHours:        positiveIntOrDefault("SMALL_CAP_SEC_BULK_CACHE_TTL_HOURS", 12),
		},
		SEC: SECConfig{
			BaseURL:   valueOrDefault("SEC_BASE_URL", "https://data.sec.gov"),
			UserAgent: valueOrDefault("SEC_USER_AGENT", "sec-monitor/0.1 contact@example.com"),
			TimeoutMS: intOrDefault("SEC_TIMEOUT_MS", 10000),
		},
		System: SystemConfig{
			LogLevel:           valueOrDefault("LOG_LEVEL", "info"),
			DataRetentionDays:  intOrDefault("DATA_RETENTION_DAYS", 30),
			StorageByDay:       boolOrDefault("STORAGE_BY_DAY", false),
			EncryptionKey:      encryptionKey,
			EncryptionKeyError: encryptionKeyError,
		},
	}
}

func encryptionKeyFromEnvironment() ([]byte, string) {
	encoded := strings.TrimSpace(os.Getenv("CONFIG_ENCRYPTION_KEY"))
	if encoded == "" {
		return nil, "CONFIG_ENCRYPTION_KEY is not configured"
	}
	key, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(key) != 32 {
		return nil, "CONFIG_ENCRYPTION_KEY must be Base64-encoded 32 bytes"
	}
	return key, ""
}

func commaSeparatedValues(key string) []string {
	var values []string
	for _, value := range strings.Split(os.Getenv(key), ",") {
		if value = strings.TrimSpace(value); value != "" {
			values = append(values, value)
		}
	}
	return values
}

func valueOrDefault(key string, fallback string) string {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	return value
}

func intOrDefault(key string, fallback int) int {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func floatOrDefault(key string, fallback float64) float64 {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return fallback
	}
	return parsed
}

func positiveIntOrDefault(key string, fallback int) int {
	value := intOrDefault(key, fallback)
	if value <= 0 {
		return fallback
	}
	return value
}

func boolOrDefault(key string, fallback bool) bool {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return fallback
	}
	return parsed
}
