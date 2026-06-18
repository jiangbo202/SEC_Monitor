package config

import "testing"

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
