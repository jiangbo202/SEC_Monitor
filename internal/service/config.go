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
