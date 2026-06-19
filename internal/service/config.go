package service

import (
	"context"
	"strconv"
	"strings"

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

func maskSecret(value string) string {
	if value == "" {
		return ""
	}
	if len(value) <= 6 {
		return "******"
	}
	return value[:3] + "******" + value[len(value)-3:]
}

func IsMaskedSecret(value string) bool {
	return strings.Contains(value, "******")
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
