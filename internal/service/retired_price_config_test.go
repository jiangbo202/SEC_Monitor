package service

import (
	"context"
	"errors"
	"testing"

	"sec_monitor/internal/model"
)

func TestRetiredPriceConfigsAreRemovedWithoutTouchingActiveSources(t *testing.T) {
	db := testDB(t)
	svc := NewConfigService(db, NewAuditService(db))
	ctx := context.Background()
	rows := []model.SystemConfig{
		{ConfigKey: "discovery.tiingo_api_token", ConfigValue: "obsolete-secret", Category: "discovery", ValueType: "string", Encrypted: true},
		{ConfigKey: "discovery.tiingo_api_tokens", ConfigValue: "old-tokens", Category: "discovery", ValueType: "string", Encrypted: true},
		{ConfigKey: "discovery.twelve_data_api_key", ConfigValue: "old-key", Category: "discovery", ValueType: "string", Encrypted: true},
		{ConfigKey: "discovery.twelve_data_request_budget", ConfigValue: "700", Category: "discovery", ValueType: "int"},
		{ConfigKey: "discovery.yahoo_request_budget", ConfigValue: "45", Category: "discovery", ValueType: "int"},
		{ConfigKey: "discovery.yahoo_base_url", ConfigValue: "https://futures.test", Category: "discovery", ValueType: "string"},
		{ConfigKey: "discovery.price_provider", ConfigValue: "longbridge", Category: "discovery", ValueType: "string"},
		{ConfigKey: "discovery.longbridge_app_key", ConfigValue: "active-key", Category: "discovery", ValueType: "string", Encrypted: true},
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	// Idempotent startup migration must neither decrypt obsolete secrets nor
	// change the saved stock route or active provider credentials.
	for i := 0; i < 2; i++ {
		if err := svc.EnsureDefaults(ctx); err != nil {
			t.Fatal(err)
		}
	}
	configs, err := svc.List(ctx, "discovery", true)
	if err != nil {
		t.Fatal(err)
	}
	for _, cfg := range configs {
		if isRetiredPriceConfig(cfg.ConfigKey) {
			t.Fatalf("retired config survived: %s", cfg.ConfigKey)
		}
	}
	for key, want := range map[string]string{"discovery.price_provider": "longbridge", "discovery.longbridge_app_key": "active-key"} {
		got, ok, err := svc.GetValue(ctx, key)
		if err != nil || !ok || got != want {
			t.Fatalf("active config changed: %s", key)
		}
	}
	for _, key := range []string{"discovery.tiingo_api_token", "discovery.twelve_data_api_key", "discovery.yahoo_request_budget", "discovery.yahoo_base_url"} {
		if err := svc.UpsertMany(ctx, []ConfigInput{{Key: key, Value: "old-client-value", Category: "discovery"}}, "test"); !errors.Is(err, ErrValidation) {
			t.Fatalf("retired config accepted: %s, %v", key, err)
		}
	}
	for _, route := range []string{"tiingo", "twelvedata", "yahoo", "longbridge,yahoo"} {
		if err := svc.UpsertMany(ctx, []ConfigInput{{Key: "discovery.price_provider", Value: route, Category: "discovery"}}, "test"); !errors.Is(err, ErrValidation) {
			t.Fatalf("retired stock route accepted: %s, %v", route, err)
		}
	}
}
