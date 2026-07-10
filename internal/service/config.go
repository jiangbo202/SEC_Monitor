package service

import (
	"context"
	"strconv"
	"strings"
	"time"

	"sec_monitor/internal/config"
	"sec_monitor/internal/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type ConfigService struct {
	db    *gorm.DB
	audit *AuditService
}

type ConfigInput struct {
	Key       string `json:"key"`
	Value     string `json:"value"`
	ValueType string `json:"value_type"`
	Category  string `json:"category"`
	Encrypted bool   `json:"encrypted"`
}

type TelegramConfig struct {
	Enabled  bool   `json:"enabled"`
	BotToken string `json:"bot_token"`
	ChatID   string `json:"chat_id"`
}

type SECFetchSettings struct {
	InitialFetchDays int
	SyncWindowDays   int
	MaxFetchCount    int
	FetchFullHistory bool
}

type NotificationSettings struct {
	ImportantOnly     bool
	FilingTypes       []string
	Keywords          []string
	QuietHoursEnabled bool
	QuietHoursStart   string
	QuietHoursEnd     string
}

type IPORadarSettings struct {
	Enabled         bool
	FormTypes       []string
	LookbackDays    int
	MaxResults      int
	NotifyEnabled   bool
	NotifyFormTypes []string
	Keywords        []string
}

type CandidateNotificationSettings struct {
	Enabled                bool   `json:"enabled"`
	NotifyA                bool   `json:"notify_a"`
	NotifyB                bool   `json:"notify_b"`
	SendTime               string `json:"send_time"`
	MaxPerGrade            int    `json:"max_per_grade"`
	ActionableOnly         bool   `json:"actionable_only"`
	MinReviewPriorityScore int    `json:"min_review_priority_score"`
}

type SocialHeatSettings struct {
	Enabled       bool
	Provider      string
	LookbackHours int
	BaselineDays  int
}

const maskedSecretMarker = "******"

func NewConfigService(db *gorm.DB, audit *AuditService) *ConfigService {
	return &ConfigService{db: db, audit: audit}
}

func (s *ConfigService) EnsureDefaults(ctx context.Context) error {
	return s.UpsertMissing(ctx, []ConfigInput{
		{Key: "sec.initial_fetch_days", Value: "30", ValueType: "int", Category: "sec"},
		{Key: "sec.sync_window_days", Value: "30", ValueType: "int", Category: "sec"},
		{Key: "sec.max_fetch_count", Value: "300", ValueType: "int", Category: "sec"},
		{Key: "sec.fetch_full_history", Value: "false", ValueType: "bool", Category: "sec"},
		{Key: "system.data_retention_days", Value: "30", ValueType: "int", Category: "system"},
		{Key: "system.storage_by_day", Value: "false", ValueType: "bool", Category: "system"},
		{Key: "scheduler.timezone", Value: "UTC", ValueType: "string", Category: "scheduler"},
		{Key: "ui.default_locale", Value: "zh-CN", ValueType: "string", Category: "ui"},
		{Key: "ui.onboarding_completed", Value: "false", ValueType: "bool", Category: "ui"},
		{Key: "notification.important_only", Value: "false", ValueType: "bool", Category: "notification"},
		{Key: "notification.filing_types", Value: "", ValueType: "string", Category: "notification"},
		{Key: "notification.keywords", Value: "", ValueType: "string", Category: "notification"},
		{Key: "notification.quiet_hours_enabled", Value: "false", ValueType: "bool", Category: "notification"},
		{Key: "notification.quiet_hours_start", Value: "22:00", ValueType: "string", Category: "notification"},
		{Key: "notification.quiet_hours_end", Value: "08:00", ValueType: "string", Category: "notification"},
		{Key: "ipo.enabled", Value: "true", ValueType: "bool", Category: "ipo"},
		{Key: "ipo.form_types", Value: "S-1,S-1/A,F-1,F-1/A,S-1MEF", ValueType: "string", Category: "ipo"},
		{Key: "ipo.lookback_days", Value: "7", ValueType: "int", Category: "ipo"},
		{Key: "ipo.max_results", Value: "100", ValueType: "int", Category: "ipo"},
		{Key: "ipo.notify_enabled", Value: "true", ValueType: "bool", Category: "ipo"},
		{Key: "ipo.notify_form_types", Value: "", ValueType: "string", Category: "ipo"},
		{Key: "ipo.keywords", Value: "", ValueType: "string", Category: "ipo"},
		{Key: "candidate_notification.enabled", Value: "false", ValueType: "bool", Category: "candidate_notification"},
		{Key: "candidate_notification.notify_a", Value: "false", ValueType: "bool", Category: "candidate_notification"},
		{Key: "candidate_notification.notify_b", Value: "false", ValueType: "bool", Category: "candidate_notification"},
		{Key: "candidate_notification.send_time", Value: "09:30", ValueType: "string", Category: "candidate_notification"},
		{Key: "candidate_notification.max_per_grade", Value: "5", ValueType: "int", Category: "candidate_notification"},
		{Key: "candidate_notification.actionable_only", Value: "true", ValueType: "bool", Category: "candidate_notification"},
		{Key: "candidate_notification.min_review_priority_score", Value: "0", ValueType: "int", Category: "candidate_notification"},
		{Key: "discovery.price_provider", Value: "", ValueType: "string", Category: "discovery"},
		{Key: "discovery.stooq_urls", Value: "", ValueType: "string", Category: "discovery"},
		{Key: "discovery.tiingo_api_token", Value: "", ValueType: "string", Category: "discovery", Encrypted: true},
		{Key: "discovery.tiingo_api_tokens", Value: "", ValueType: "string", Category: "discovery", Encrypted: true},
		{Key: "discovery.tiingo_base_url", Value: "https://api.tiingo.com", ValueType: "string", Category: "discovery"},
		{Key: "discovery.tiingo_request_budget", Value: "45", ValueType: "int", Category: "discovery"},
		{Key: "discovery.twelve_data_api_key", Value: "", ValueType: "string", Category: "discovery", Encrypted: true},
		{Key: "discovery.twelve_data_base_url", Value: "https://api.twelvedata.com", ValueType: "string", Category: "discovery"},
		{Key: "discovery.twelve_data_request_budget", Value: "700", ValueType: "int", Category: "discovery"},
		{Key: "discovery.twelve_data_request_interval_ms", Value: "8000", ValueType: "int", Category: "discovery"},
		{Key: "discovery.yahoo_base_url", Value: "https://query1.finance.yahoo.com", ValueType: "string", Category: "discovery"},
		{Key: "discovery.yahoo_request_budget", Value: "45", ValueType: "int", Category: "discovery"},
		{Key: "discovery.min_publish_coverage_pct", Value: "85", ValueType: "float", Category: "discovery"},
		{Key: "discovery.research_mode", Value: "true", ValueType: "bool", Category: "discovery"},
		{Key: "social_heat.enabled", Value: "false", ValueType: "bool", Category: "social_heat"},
		{Key: "social_heat.provider", Value: "manual", ValueType: "string", Category: "social_heat"},
		{Key: "social_heat.lookback_hours", Value: "24", ValueType: "int", Category: "social_heat"},
		{Key: "social_heat.baseline_days", Value: "30", ValueType: "int", Category: "social_heat"},
	}, "system")
}

func (s *ConfigService) UpsertMissing(ctx context.Context, inputs []ConfigInput, operator string) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		inserted := int64(0)
		for _, input := range inputs {
			cfg := model.SystemConfig{
				ConfigKey:   strings.TrimSpace(input.Key),
				ConfigValue: input.Value,
				ValueType:   valueOrDefault(input.ValueType, "string"),
				Category:    strings.TrimSpace(input.Category),
				Encrypted:   input.Encrypted,
			}
			if cfg.ConfigKey == "" || cfg.Category == "" {
				return ErrValidation
			}
			res := tx.Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "config_key"}},
				DoNothing: true,
			}).Create(&cfg)
			if res.Error != nil {
				return res.Error
			}
			inserted += res.RowsAffected
		}
		if inserted == 0 {
			return nil
		}
		return NewAuditService(tx).Record(ctx, operator, "create", "system_config", "defaults", nil, inputs)
	})
}

func (s *ConfigService) UpsertMany(ctx context.Context, inputs []ConfigInput, operator string) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, input := range inputs {
			key := strings.TrimSpace(input.Key)
			if err := validateConfigInput(key, input.Value); err != nil {
				return err
			}
			if input.Encrypted && IsMaskedSecret(input.Value) {
				var existing model.SystemConfig
				err := tx.Where("config_key = ?", key).First(&existing).Error
				if err == nil {
					continue
				}
				if err != gorm.ErrRecordNotFound {
					return err
				}
			}
			cfg := model.SystemConfig{
				ConfigKey:   key,
				ConfigValue: input.Value,
				ValueType:   valueOrDefault(input.ValueType, "string"),
				Category:    strings.TrimSpace(input.Category),
				Encrypted:   input.Encrypted,
			}
			if cfg.ConfigKey == "" || cfg.Category == "" {
				return ErrValidation
			}
			if err := tx.Clauses(clause.OnConflict{
				Columns: []clause.Column{{Name: "config_key"}},
				DoUpdates: clause.AssignmentColumns([]string{
					"config_value", "value_type", "category", "encrypted", "updated_at",
				}),
			}).Create(&cfg).Error; err != nil {
				return err
			}
		}
		return NewAuditService(tx).Record(ctx, operator, "update", "system_config", "batch", nil, inputs)
	})
}

func validateConfigInput(key string, value string) error {
	switch key {
	case "scheduler.timezone":
		if _, err := schedulerLocationFromValue(value); err != nil {
			return err
		}
	}
	return nil
}

func (s *ConfigService) List(ctx context.Context, category string, maskSensitive bool) ([]model.SystemConfig, error) {
	query := s.db.WithContext(ctx).Model(&model.SystemConfig{})
	if category != "" {
		query = query.Where("category = ?", category)
	}
	var configs []model.SystemConfig
	if err := query.Order("category ASC, config_key ASC").Find(&configs).Error; err != nil {
		return nil, err
	}
	if maskSensitive {
		for i := range configs {
			if configs[i].Encrypted {
				configs[i].ConfigValue = maskSecret(configs[i].ConfigValue)
			}
		}
	}
	return configs, nil
}

func (s *ConfigService) GetValue(ctx context.Context, key string) (string, bool, error) {
	var cfg model.SystemConfig
	err := s.db.WithContext(ctx).Where("config_key = ?", key).First(&cfg).Error
	if err == gorm.ErrRecordNotFound {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return cfg.ConfigValue, true, nil
}

func (s *ConfigService) SchedulerTimezone(ctx context.Context) (*time.Location, string, error) {
	value, ok, err := s.GetValue(ctx, "scheduler.timezone")
	if err != nil {
		return nil, "", err
	}
	if !ok {
		value = "UTC"
	}
	location, err := schedulerLocationFromValue(value)
	if err != nil {
		return nil, "", err
	}
	return location, location.String(), nil
}

func schedulerLocationFromValue(value string) (*time.Location, error) {
	name := strings.TrimSpace(value)
	if name == "" {
		name = "UTC"
	}
	location, err := time.LoadLocation(name)
	if err != nil {
		return nil, ErrValidation
	}
	return location, nil
}

func (s *ConfigService) Telegram(ctx context.Context) (TelegramConfig, error) {
	enabledRaw, _, err := s.GetValue(ctx, "telegram.enabled")
	if err != nil {
		return TelegramConfig{}, err
	}
	token, _, err := s.GetValue(ctx, "telegram.bot_token")
	if err != nil {
		return TelegramConfig{}, err
	}
	chatID, _, err := s.GetValue(ctx, "telegram.chat_id")
	if err != nil {
		return TelegramConfig{}, err
	}
	enabled, _ := strconv.ParseBool(enabledRaw)
	return TelegramConfig{Enabled: enabled, BotToken: token, ChatID: chatID}, nil
}

func (s *ConfigService) SECFetchSettings(ctx context.Context) (SECFetchSettings, error) {
	initialDaysRaw, _, err := s.GetValue(ctx, "sec.initial_fetch_days")
	if err != nil {
		return SECFetchSettings{}, err
	}
	syncWindowRaw, _, err := s.GetValue(ctx, "sec.sync_window_days")
	if err != nil {
		return SECFetchSettings{}, err
	}
	maxCountRaw, _, err := s.GetValue(ctx, "sec.max_fetch_count")
	if err != nil {
		return SECFetchSettings{}, err
	}
	fullHistoryRaw, _, err := s.GetValue(ctx, "sec.fetch_full_history")
	if err != nil {
		return SECFetchSettings{}, err
	}
	initialDays, _ := strconv.Atoi(initialDaysRaw)
	syncWindowDays, _ := strconv.Atoi(syncWindowRaw)
	maxCount, _ := strconv.Atoi(maxCountRaw)
	fullHistory, _ := strconv.ParseBool(fullHistoryRaw)
	return SECFetchSettings{
		InitialFetchDays: initialDays,
		SyncWindowDays:   syncWindowDays,
		MaxFetchCount:    maxCount,
		FetchFullHistory: fullHistory,
	}, nil
}

func (s *ConfigService) NotificationSettings(ctx context.Context) (NotificationSettings, error) {
	importantOnlyRaw, _, err := s.GetValue(ctx, "notification.important_only")
	if err != nil {
		return NotificationSettings{}, err
	}
	filingTypesRaw, _, err := s.GetValue(ctx, "notification.filing_types")
	if err != nil {
		return NotificationSettings{}, err
	}
	keywordsRaw, _, err := s.GetValue(ctx, "notification.keywords")
	if err != nil {
		return NotificationSettings{}, err
	}
	quietEnabledRaw, _, err := s.GetValue(ctx, "notification.quiet_hours_enabled")
	if err != nil {
		return NotificationSettings{}, err
	}
	quietStart, _, err := s.GetValue(ctx, "notification.quiet_hours_start")
	if err != nil {
		return NotificationSettings{}, err
	}
	quietEnd, _, err := s.GetValue(ctx, "notification.quiet_hours_end")
	if err != nil {
		return NotificationSettings{}, err
	}
	importantOnly, _ := strconv.ParseBool(importantOnlyRaw)
	quietEnabled, _ := strconv.ParseBool(quietEnabledRaw)
	return NotificationSettings{
		ImportantOnly:     importantOnly,
		FilingTypes:       splitConfigList(filingTypesRaw),
		Keywords:          splitConfigList(keywordsRaw),
		QuietHoursEnabled: quietEnabled,
		QuietHoursStart:   valueOrDefault(quietStart, "22:00"),
		QuietHoursEnd:     valueOrDefault(quietEnd, "08:00"),
	}, nil
}

func (s *ConfigService) IPORadarSettings(ctx context.Context) (IPORadarSettings, error) {
	enabledRaw, _, err := s.GetValue(ctx, "ipo.enabled")
	if err != nil {
		return IPORadarSettings{}, err
	}
	formTypesRaw, _, err := s.GetValue(ctx, "ipo.form_types")
	if err != nil {
		return IPORadarSettings{}, err
	}
	lookbackRaw, _, err := s.GetValue(ctx, "ipo.lookback_days")
	if err != nil {
		return IPORadarSettings{}, err
	}
	maxRaw, _, err := s.GetValue(ctx, "ipo.max_results")
	if err != nil {
		return IPORadarSettings{}, err
	}
	notifyRaw, _, err := s.GetValue(ctx, "ipo.notify_enabled")
	if err != nil {
		return IPORadarSettings{}, err
	}
	keywordsRaw, _, err := s.GetValue(ctx, "ipo.keywords")
	if err != nil {
		return IPORadarSettings{}, err
	}
	notifyFormTypesRaw, _, err := s.GetValue(ctx, "ipo.notify_form_types")
	if err != nil {
		return IPORadarSettings{}, err
	}
	enabled, _ := strconv.ParseBool(enabledRaw)
	notify, _ := strconv.ParseBool(notifyRaw)
	lookback, _ := strconv.Atoi(lookbackRaw)
	maxResults, _ := strconv.Atoi(maxRaw)
	if lookback <= 0 {
		lookback = 7
	}
	if maxResults <= 0 || maxResults > 200 {
		maxResults = 100
	}
	formTypes := splitConfigList(formTypesRaw)
	if len(formTypes) == 0 {
		formTypes = []string{"S-1", "S-1/A", "F-1", "F-1/A", "S-1MEF"}
	}
	return IPORadarSettings{
		Enabled:         enabled,
		FormTypes:       formTypes,
		LookbackDays:    lookback,
		MaxResults:      maxResults,
		NotifyEnabled:   notify,
		NotifyFormTypes: splitConfigList(notifyFormTypesRaw),
		Keywords:        splitConfigList(keywordsRaw),
	}, nil
}

func (s *ConfigService) CandidateNotificationSettings(ctx context.Context) (CandidateNotificationSettings, error) {
	enabledRaw, _, err := s.GetValue(ctx, "candidate_notification.enabled")
	if err != nil {
		return CandidateNotificationSettings{}, err
	}
	notifyARaw, _, err := s.GetValue(ctx, "candidate_notification.notify_a")
	if err != nil {
		return CandidateNotificationSettings{}, err
	}
	notifyBRaw, _, err := s.GetValue(ctx, "candidate_notification.notify_b")
	if err != nil {
		return CandidateNotificationSettings{}, err
	}
	sendTime, _, err := s.GetValue(ctx, "candidate_notification.send_time")
	if err != nil {
		return CandidateNotificationSettings{}, err
	}
	maxRaw, _, err := s.GetValue(ctx, "candidate_notification.max_per_grade")
	if err != nil {
		return CandidateNotificationSettings{}, err
	}
	actionableRaw, _, err := s.GetValue(ctx, "candidate_notification.actionable_only")
	if err != nil {
		return CandidateNotificationSettings{}, err
	}
	minPriorityRaw, _, err := s.GetValue(ctx, "candidate_notification.min_review_priority_score")
	if err != nil {
		return CandidateNotificationSettings{}, err
	}
	enabled, _ := strconv.ParseBool(enabledRaw)
	notifyA, _ := strconv.ParseBool(notifyARaw)
	notifyB, _ := strconv.ParseBool(notifyBRaw)
	actionableOnly, _ := strconv.ParseBool(actionableRaw)
	maxPerGrade, _ := strconv.Atoi(maxRaw)
	if maxPerGrade <= 0 {
		maxPerGrade = 5
	}
	if maxPerGrade > 20 {
		maxPerGrade = 20
	}
	minPriority, _ := strconv.Atoi(minPriorityRaw)
	if minPriority < 0 {
		minPriority = 0
	}
	return CandidateNotificationSettings{
		Enabled:                enabled,
		NotifyA:                notifyA,
		NotifyB:                notifyB,
		SendTime:               valueOrDefault(sendTime, "09:30"),
		MaxPerGrade:            maxPerGrade,
		ActionableOnly:         actionableOnly,
		MinReviewPriorityScore: minPriority,
	}, nil
}

func (s *ConfigService) SocialHeatSettings(ctx context.Context) (SocialHeatSettings, error) {
	enabledRaw, _, err := s.GetValue(ctx, "social_heat.enabled")
	if err != nil {
		return SocialHeatSettings{}, err
	}
	provider, _, err := s.GetValue(ctx, "social_heat.provider")
	if err != nil {
		return SocialHeatSettings{}, err
	}
	lookbackRaw, _, err := s.GetValue(ctx, "social_heat.lookback_hours")
	if err != nil {
		return SocialHeatSettings{}, err
	}
	baselineRaw, _, err := s.GetValue(ctx, "social_heat.baseline_days")
	if err != nil {
		return SocialHeatSettings{}, err
	}
	enabled, _ := strconv.ParseBool(enabledRaw)
	lookback, _ := strconv.Atoi(lookbackRaw)
	baseline, _ := strconv.Atoi(baselineRaw)
	if lookback <= 0 {
		lookback = 24
	}
	if baseline <= 0 {
		baseline = 30
	}
	return SocialHeatSettings{
		Enabled:       enabled,
		Provider:      valueOrDefault(provider, "manual"),
		LookbackHours: lookback,
		BaselineDays:  baseline,
	}, nil
}

func (s *ConfigService) ApplyDiscoveryConfig(ctx context.Context, cfg config.DiscoveryConfig) (config.DiscoveryConfig, error) {
	if s == nil {
		return cfg, nil
	}
	if provider, ok, err := s.GetValue(ctx, "discovery.price_provider"); err != nil {
		return cfg, err
	} else if ok && strings.TrimSpace(provider) != "" {
		cfg.PriceProvider = strings.ToLower(strings.TrimSpace(provider))
	}
	if urls, ok, err := s.GetValue(ctx, "discovery.stooq_urls"); err != nil {
		return cfg, err
	} else if ok && strings.TrimSpace(urls) != "" {
		cfg.StooqURLs = commaSeparatedConfigValues(urls)
	}
	if token, ok, err := s.GetValue(ctx, "discovery.tiingo_api_token"); err != nil {
		return cfg, err
	} else if ok && strings.TrimSpace(token) != "" && !IsMaskedSecret(token) {
		cfg.TiingoAPIToken = strings.TrimSpace(token)
	}
	if tokens, ok, err := s.GetValue(ctx, "discovery.tiingo_api_tokens"); err != nil {
		return cfg, err
	} else if ok && strings.TrimSpace(tokens) != "" && !IsMaskedSecret(tokens) {
		cfg.TiingoAPITokens = commaSeparatedConfigValues(tokens)
	}
	if baseURL, ok, err := s.GetValue(ctx, "discovery.tiingo_base_url"); err != nil {
		return cfg, err
	} else if ok && strings.TrimSpace(baseURL) != "" {
		cfg.TiingoBaseURL = strings.TrimSpace(baseURL)
	}
	if budget, ok, err := s.GetValue(ctx, "discovery.tiingo_request_budget"); err != nil {
		return cfg, err
	} else if ok && strings.TrimSpace(budget) != "" {
		parsed, parseErr := strconv.Atoi(strings.TrimSpace(budget))
		if parseErr == nil && parsed >= 0 {
			cfg.TiingoRequestBudget = parsed
		}
	}
	if apiKey, ok, err := s.GetValue(ctx, "discovery.twelve_data_api_key"); err != nil {
		return cfg, err
	} else if ok && strings.TrimSpace(apiKey) != "" && !IsMaskedSecret(apiKey) {
		cfg.TwelveDataAPIKey = strings.TrimSpace(apiKey)
	}
	if baseURL, ok, err := s.GetValue(ctx, "discovery.twelve_data_base_url"); err != nil {
		return cfg, err
	} else if ok && strings.TrimSpace(baseURL) != "" {
		cfg.TwelveDataBaseURL = strings.TrimSpace(baseURL)
	}
	if budget, ok, err := s.GetValue(ctx, "discovery.twelve_data_request_budget"); err != nil {
		return cfg, err
	} else if ok && strings.TrimSpace(budget) != "" {
		parsed, parseErr := strconv.Atoi(strings.TrimSpace(budget))
		if parseErr == nil && parsed >= 0 {
			cfg.TwelveDataRequestBudget = parsed
		}
	}
	if interval, ok, err := s.GetValue(ctx, "discovery.twelve_data_request_interval_ms"); err != nil {
		return cfg, err
	} else if ok && strings.TrimSpace(interval) != "" {
		parsed, parseErr := strconv.Atoi(strings.TrimSpace(interval))
		if parseErr == nil && parsed > 0 {
			cfg.TwelveDataRequestIntervalMS = parsed
		}
	}
	if baseURL, ok, err := s.GetValue(ctx, "discovery.yahoo_base_url"); err != nil {
		return cfg, err
	} else if ok && strings.TrimSpace(baseURL) != "" {
		cfg.YahooBaseURL = strings.TrimSpace(baseURL)
	}
	if budget, ok, err := s.GetValue(ctx, "discovery.yahoo_request_budget"); err != nil {
		return cfg, err
	} else if ok && strings.TrimSpace(budget) != "" {
		parsed, parseErr := strconv.Atoi(strings.TrimSpace(budget))
		if parseErr == nil && parsed >= 0 {
			cfg.YahooRequestBudget = parsed
		}
	}
	if coverage, ok, err := s.GetValue(ctx, "discovery.min_publish_coverage_pct"); err != nil {
		return cfg, err
	} else if ok && strings.TrimSpace(coverage) != "" {
		parsed, parseErr := strconv.ParseFloat(strings.TrimSpace(coverage), 64)
		if parseErr == nil && parsed >= 0 {
			cfg.MinPublishCoveragePct = parsed
		}
	}
	if researchMode, ok, err := s.GetValue(ctx, "discovery.research_mode"); err != nil {
		return cfg, err
	} else if ok && strings.TrimSpace(researchMode) != "" {
		parsed, parseErr := strconv.ParseBool(strings.TrimSpace(researchMode))
		if parseErr == nil {
			cfg.ResearchMode = parsed
		}
	}
	return cfg, nil
}

func commaSeparatedConfigValues(value string) []string {
	var values []string
	for _, part := range strings.Split(value, ",") {
		if part = strings.TrimSpace(part); part != "" {
			values = append(values, part)
		}
	}
	return values
}

func maskSecret(value string) string {
	if value == "" {
		return ""
	}
	if len(value) <= 6 {
		return maskedSecretMarker
	}
	return value[:3] + maskedSecretMarker + value[len(value)-3:]
}

func IsMaskedSecret(value string) bool {
	return strings.Contains(value, maskedSecretMarker)
}

func valueOrDefault(value string, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func splitConfigList(value string) []string {
	parts := strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == '\n' || r == ';'
	})
	items := make([]string, 0, len(parts))
	for _, part := range parts {
		item := strings.TrimSpace(part)
		if item != "" {
			items = append(items, item)
		}
	}
	return items
}
