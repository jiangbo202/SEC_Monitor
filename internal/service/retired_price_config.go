package service

import (
	"context"
	"strings"

	"sec_monitor/internal/model"
)

// Old price credentials are deleted without reading or logging their values.
func isRetiredPriceConfig(key string) bool {
	return strings.HasPrefix(key, "discovery.tiingo_") || strings.HasPrefix(key, "discovery.twelve_data_") || strings.HasPrefix(key, "discovery.yahoo_")
}

func (s *ConfigService) removeRetiredPriceConfigs(ctx context.Context) error {
	return s.db.WithContext(ctx).Where("config_key IN ?", []string{
		"discovery.tiingo_api_token", "discovery.tiingo_api_tokens", "discovery.tiingo_base_url", "discovery.tiingo_concurrency", "discovery.tiingo_request_budget", "discovery.tiingo_request_interval_ms",
		"discovery.twelve_data_api_key", "discovery.twelve_data_base_url", "discovery.twelve_data_request_budget", "discovery.twelve_data_request_interval_ms",
		"discovery.yahoo_base_url", "discovery.yahoo_request_budget", "discovery.yahoo_request_interval_ms",
	}).Delete(&model.SystemConfig{}).Error
}
