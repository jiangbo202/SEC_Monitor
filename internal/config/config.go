package config

import (
	"os"
	"strconv"
)

type Config struct {
	Server   ServerConfig
	Database DatabaseConfig
	SEC      SECConfig
	System   SystemConfig
}

type ServerConfig struct {
	Address string
}

type DatabaseConfig struct {
	Type string
	DSN  string
}

type SECConfig struct {
	BaseURL   string
	UserAgent string
	TimeoutMS int
}

type SystemConfig struct {
	LogLevel          string
	DataRetentionDays int
	StorageByDay      bool
}

func Load() Config {
	return Config{
		Server: ServerConfig{
			Address: valueOrDefault("APP_ADDR", ":8080"),
		},
		Database: DatabaseConfig{
			Type: valueOrDefault("DB_TYPE", "sqlite"),
			DSN:  valueOrDefault("DB_DSN", "data/sec_monitor.db"),
		},
		SEC: SECConfig{
			BaseURL:   valueOrDefault("SEC_BASE_URL", "https://data.sec.gov"),
			UserAgent: valueOrDefault("SEC_USER_AGENT", "sec-monitor/0.1 contact@example.com"),
			TimeoutMS: intOrDefault("SEC_TIMEOUT_MS", 10000),
		},
		System: SystemConfig{
			LogLevel:          valueOrDefault("LOG_LEVEL", "info"),
			DataRetentionDays: intOrDefault("DATA_RETENTION_DAYS", 30),
			StorageByDay:      boolOrDefault("STORAGE_BY_DAY", false),
		},
	}
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
