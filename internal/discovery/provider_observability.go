package discovery

import (
	"context"
	"errors"
	"strings"
	"time"

	"sec_monitor/internal/config"

	"gorm.io/gorm"
)

// ProviderObservability is a read-only, local view of the configured market
// data chain. It deliberately reports application-side safety budgets rather
// than guessing at a vendor account's remaining quota.
type ProviderObservability struct {
	GeneratedAt             time.Time                   `json:"generated_at"`
	PriceProviderChain      string                      `json:"price_provider_chain"`
	CalendarVersion         string                      `json:"calendar_version"`
	CalendarYears           []CalendarCoverage          `json:"calendar_years"`
	LatestRun               *ProviderRun                `json:"latest_run,omitempty"`
	ChainHealth             *ProviderHealth             `json:"chain_health,omitempty"`
	LatestPriceSourceCounts map[string]int64            `json:"latest_price_source_counts"`
	Providers               []ProviderObservabilityItem `json:"providers"`
	BudgetNotice            string                      `json:"budget_notice"`
}

// CalendarCoverage contains only the availability information needed by the
// UI; source URLs and reviewer details remain in the calendar records.
type CalendarCoverage struct {
	Year     int  `json:"year"`
	Complete bool `json:"complete"`
}

type ProviderObservabilityItem struct {
	Provider                string           `json:"provider"`
	Configured              bool             `json:"configured"`
	ConfiguredCredential    bool             `json:"configured_credential"`
	TokenCount              int              `json:"token_count"`
	LocalRequestBudget      int              `json:"local_request_budget"`
	BudgetScope             string           `json:"budget_scope"`
	LatestSourceRecordCount int64            `json:"latest_source_record_count"`
	Health                  *ProviderHealth  `json:"health,omitempty"`
	LatestAttempt           *ProviderAttempt `json:"latest_attempt,omitempty"`
}

const providerBudgetNotice = "本地请求预算是本系统单次运行的保护阈值，不代表供应商账户剩余额度；请以供应商后台为准。"

// GetProviderObservability never issues an external provider request. It is
// safe to call from page refreshes and gives operators enough context to
// distinguish configuration limits from provider-side quota errors.
func GetProviderObservability(ctx context.Context, db *gorm.DB, cfg config.DiscoveryConfig) (ProviderObservability, error) {
	result := ProviderObservability{
		GeneratedAt:             time.Now().UTC(),
		PriceProviderChain:      normalizedPriceProviderChain(cfg),
		CalendarVersion:         DefaultNYSECalendarVersion,
		CalendarYears:           []CalendarCoverage{},
		LatestPriceSourceCounts: map[string]int64{},
		Providers:               []ProviderObservabilityItem{},
		BudgetNotice:            providerBudgetNotice,
	}
	if db == nil {
		return result, errors.New("database is required")
	}
	if ctx == nil {
		return result, errors.New("context is required")
	}

	var years []MarketCalendarYear
	if err := db.WithContext(ctx).Where("calendar_version = ?", DefaultNYSECalendarVersion).Order("year ASC").Find(&years).Error; err != nil {
		return result, err
	}
	for _, year := range years {
		result.CalendarYears = append(result.CalendarYears, CalendarCoverage{Year: year.Year, Complete: year.Complete})
	}

	var latest ProviderRun
	err := db.WithContext(ctx).
		Table("provider_runs AS provider_runs").
		Select("provider_runs.*").
		Joins("JOIN universe_batches AS universe_batches ON universe_batches.batch_id = provider_runs.batch_id").
		Where("universe_batches.kind = ?", BatchKindPrescreen).
		Order("provider_runs.created_at DESC").
		Order("provider_runs.id DESC").
		First(&latest).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return result, err
	}
	if err == nil {
		if hydrateErr := hydrateProviderRunAttempts(&latest); hydrateErr != nil {
			return result, hydrateErr
		}
		result.LatestRun = &latest
		if byVersion, countErr := priceSourceCountsByVersion(ctx, db, []string{latest.SourceVersion}); countErr != nil {
			return result, countErr
		} else if counts := byVersion[latest.SourceVersion]; counts != nil {
			result.LatestPriceSourceCounts = counts
		}
	}

	var healthRows []ProviderHealth
	if err := db.WithContext(ctx).Find(&healthRows).Error; err != nil {
		return result, err
	}
	healthByProvider := make(map[string]ProviderHealth, len(healthRows))
	for _, health := range healthRows {
		healthByProvider[strings.ToLower(strings.TrimSpace(health.Provider))] = health
	}
	attemptByProvider := make(map[string]ProviderAttempt, len(latest.Attempts))
	for _, attempt := range latest.Attempts {
		attemptByProvider[strings.ToLower(strings.TrimSpace(attempt.Provider))] = attempt
	}
	if health, ok := healthByProvider[result.PriceProviderChain]; ok {
		healthCopy := health
		result.ChainHealth = &healthCopy
	}

	for _, provider := range priceProviderNames(result.PriceProviderChain) {
		item := providerObservabilityConfig(provider, cfg)
		if count, ok := result.LatestPriceSourceCounts[provider]; ok {
			item.LatestSourceRecordCount = count
		}
		if health, ok := healthByProvider[provider]; ok {
			healthCopy := health
			item.Health = &healthCopy
		}
		if attempt, ok := attemptByProvider[provider]; ok {
			attemptCopy := attempt
			item.LatestAttempt = &attemptCopy
		}
		result.Providers = append(result.Providers, item)
	}
	return result, nil
}

func normalizedPriceProviderChain(cfg config.DiscoveryConfig) string {
	parts := priceProviderNames(cfg.PriceProvider)
	if len(parts) > 0 {
		return strings.Join(parts, ",")
	}
	if len(normalizeTiingoTokens(cfg.TiingoAPIToken, cfg.TiingoAPITokens)) > 0 {
		return "tiingo"
	}
	if strings.TrimSpace(cfg.TwelveDataAPIKey) != "" {
		return "twelvedata"
	}
	if len(cfg.StooqURLs) > 0 {
		return "stooq"
	}
	return ""
}

func priceProviderNames(chain string) []string {
	seen := map[string]struct{}{}
	items := make([]string, 0)
	for _, raw := range strings.Split(strings.ToLower(strings.TrimSpace(chain)), ",") {
		provider := strings.TrimSpace(raw)
		if provider == "" {
			continue
		}
		if _, exists := seen[provider]; exists {
			continue
		}
		seen[provider] = struct{}{}
		items = append(items, provider)
	}
	return items
}

func providerObservabilityConfig(provider string, cfg config.DiscoveryConfig) ProviderObservabilityItem {
	item := ProviderObservabilityItem{Provider: provider, Configured: true, BudgetScope: "provider_managed"}
	switch provider {
	case "tiingo":
		item.TokenCount = len(normalizeTiingoTokens(cfg.TiingoAPIToken, cfg.TiingoAPITokens))
		item.ConfiguredCredential = item.TokenCount > 0
		item.LocalRequestBudget = cfg.TiingoRequestBudget * item.TokenCount
		item.BudgetScope = "per_token_per_run"
	case "twelvedata":
		item.ConfiguredCredential = strings.TrimSpace(cfg.TwelveDataAPIKey) != ""
		item.LocalRequestBudget = cfg.TwelveDataRequestBudget
		item.BudgetScope = "per_run"
	case "yahoo":
		item.ConfiguredCredential = true
		item.LocalRequestBudget = cfg.YahooRequestBudget
		item.BudgetScope = "per_run"
	case "longbridge":
		item.ConfiguredCredential = strings.TrimSpace(cfg.LongbridgeAppKey) != "" && strings.TrimSpace(cfg.LongbridgeAppSecret) != "" && strings.TrimSpace(cfg.LongbridgeAccessToken) != ""
		item.BudgetScope = "provider_managed"
	case "stooq":
		item.ConfiguredCredential = len(cfg.StooqURLs) > 0
		item.BudgetScope = "download"
	default:
		item.ConfiguredCredential = false
		item.BudgetScope = "unknown"
	}
	return item
}
