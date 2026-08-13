package service

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"sec_monitor/internal/config"
	"sec_monitor/internal/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type ConfigService struct {
	db                 *gorm.DB
	audit              *AuditService
	encryptionKey      []byte
	encryptionKeyError string
	encryptionEnforced bool
}

type ConfigInput struct {
	Key       string `json:"key"`
	Value     string `json:"value"`
	ValueType string `json:"value_type"`
	Category  string `json:"category"`
	Encrypted bool   `json:"encrypted"`
}

type TelegramConfig struct {
	Enabled    bool   `json:"enabled"`
	BotToken   string `json:"bot_token"`
	ChatID     string `json:"chat_id"`
	APIBaseURL string `json:"api_base_url"`
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
	Enabled                              bool
	FormTypes                            []string
	LookbackDays                         int
	MaxResults                           int
	NotifyEnabled                        bool
	NotifyFormTypes                      []string
	Keywords                             []string
	LifecycleSweepEnabled                bool
	LifecycleMaxCIKs                     int
	LifecycleRecheckHours                int
	LongbridgeListingVerificationEnabled bool
	LongbridgeListingRequestBudget       int
	LongbridgeListingRecheckHours        int
	LongbridgeIPOCalendarEnabled         bool
	LongbridgeIPOCalendarLookbackDays    int
	LongbridgeIPOCalendarLookaheadDays   int
	LongbridgeIPOCalendarMaxPages        int
}

type CandidateNotificationSettings struct {
	Enabled                bool   `json:"enabled"`
	ShadowMode             bool   `json:"shadow_mode"`
	NotifyA                bool   `json:"notify_a"`
	NotifyB                bool   `json:"notify_b"`
	SendTime               string `json:"send_time"`
	MaxPerGrade            int    `json:"max_per_grade"`
	ActionableOnly         bool   `json:"actionable_only"`
	MinReviewPriorityScore int    `json:"min_review_priority_score"`
}

type TradeSetupNotificationSettings struct {
	Enabled           bool `json:"enabled"`
	ShadowMode        bool `json:"shadow_mode"`
	NotifyEntry       bool `json:"notify_entry"`
	NotifyExit        bool `json:"notify_exit"`
	NotifyInvalidated bool `json:"notify_invalidated"`
	MaxPerRun         int  `json:"max_per_run"`
}

type SocialHeatSettings struct {
	Enabled       bool
	Provider      string
	LookbackHours int
	BaselineDays  int
}

const maskedSecretMarker = "******"

var sensitiveConfigKeys = map[string]struct{}{
	"telegram.bot_token":                {},
	"discovery.tiingo_api_token":        {},
	"discovery.tiingo_api_tokens":       {},
	"discovery.twelve_data_api_key":     {},
	"discovery.longbridge_app_key":      {},
	"discovery.longbridge_app_secret":   {},
	"discovery.longbridge_access_token": {},
}

func NewConfigService(db *gorm.DB, audit *AuditService, system ...config.SystemConfig) *ConfigService {
	service := &ConfigService{db: db, audit: audit}
	if len(system) > 0 {
		service.encryptionKey = append([]byte(nil), system[0].EncryptionKey...)
		service.encryptionKeyError = system[0].EncryptionKeyError
		service.encryptionEnforced = true
	}
	return service
}

func (s *ConfigService) EncryptionHealth() EncryptionHealth {
	if len(s.encryptionKey) == 32 && s.encryptionKeyError == "" {
		return EncryptionHealth{Status: "ok", Message: "configuration encryption is enabled", Configured: true}
	}
	message := s.encryptionKeyError
	if message == "" {
		message = "CONFIG_ENCRYPTION_KEY is not configured"
	}
	return EncryptionHealth{Status: "critical", Message: message, Configured: false}
}

func (s *ConfigService) MigrateEncryptedValues(ctx context.Context) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if s.EncryptionHealth().Status == "ok" {
			var configs []model.SystemConfig
			if err := tx.Where("encrypted = ? AND config_value <> ?", true, "").Find(&configs).Error; err != nil {
				return err
			}
			for _, cfg := range configs {
				if strings.HasPrefix(cfg.ConfigValue, encryptedSecretPrefix) {
					continue
				}
				encrypted, err := s.encryptSecret(cfg.ConfigValue)
				if err != nil {
					return err
				}
				if err := tx.Model(&model.SystemConfig{}).Where("id = ?", cfg.ID).Update("config_value", encrypted).Error; err != nil {
					return err
				}
			}
		}
		if err := sanitizeStoredNotificationErrors(tx); err != nil {
			return err
		}
		if err := sanitizeStoredOperationLogs(tx); err != nil {
			return err
		}
		return nil
	})
}

func sanitizeStoredOperationLogs(tx *gorm.DB) error {
	if !tx.Migrator().HasTable(&model.OperationLog{}) {
		return nil
	}
	var logs []model.OperationLog
	if err := tx.Where("object_type = ?", "system_config").Find(&logs).Error; err != nil {
		return err
	}
	for _, entry := range logs {
		beforeData := sanitizeOperationLogData(entry.BeforeData)
		afterData := sanitizeOperationLogData(entry.AfterData)
		if beforeData == entry.BeforeData && afterData == entry.AfterData {
			continue
		}
		if err := tx.Model(&model.OperationLog{}).Where("id = ?", entry.ID).Updates(map[string]any{
			"before_data": beforeData,
			"after_data":  afterData,
		}).Error; err != nil {
			return err
		}
	}
	return nil
}

func sanitizeStoredNotificationErrors(tx *gorm.DB) error {
	if !tx.Migrator().HasTable(&model.NotificationBatch{}) || !tx.Migrator().HasTable(&model.NotificationLog{}) {
		return nil
	}
	var batches []model.NotificationBatch
	if err := tx.Where("error_message <> ?", "").Find(&batches).Error; err != nil {
		return err
	}
	for _, batch := range batches {
		value := SanitizeSensitiveError(batch.ErrorMessage)
		if value != batch.ErrorMessage {
			if err := tx.Model(&model.NotificationBatch{}).Where("id = ?", batch.ID).Update("error_message", value).Error; err != nil {
				return err
			}
		}
	}
	var logs []model.NotificationLog
	if err := tx.Where("error_message <> ?", "").Find(&logs).Error; err != nil {
		return err
	}
	for _, log := range logs {
		value := SanitizeSensitiveError(log.ErrorMessage)
		if value != log.ErrorMessage {
			if err := tx.Model(&model.NotificationLog{}).Where("id = ?", log.ID).Update("error_message", value).Error; err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *ConfigService) EnsureDefaults(ctx context.Context) error {
	return s.UpsertMissing(ctx, []ConfigInput{
		{Key: "sec.user_agent", Value: "", ValueType: "string", Category: "sec"},
		{Key: "sec.initial_fetch_days", Value: "30", ValueType: "int", Category: "sec"},
		{Key: "sec.sync_window_days", Value: "30", ValueType: "int", Category: "sec"},
		{Key: "sec.max_fetch_count", Value: "300", ValueType: "int", Category: "sec"},
		{Key: "sec.fetch_full_history", Value: "false", ValueType: "bool", Category: "sec"},
		{Key: "system.data_retention_days", Value: "30", ValueType: "int", Category: "system"},
		// Operational history is separate from filing retention. It may be
		// pruned safely because it contains run diagnostics only, never filings
		// or published small-cap research output.
		{Key: "system.operation_history_retention_days", Value: "90", ValueType: "int", Category: "system"},
		{Key: "system.backup_retention_days", Value: "7", ValueType: "int", Category: "system"},
		{Key: "system.backup_dir", Value: "", ValueType: "string", Category: "system"},
		{Key: "system.storage_warning_pct", Value: "80", ValueType: "int", Category: "system"},
		{Key: "system.storage_by_day", Value: "false", ValueType: "bool", Category: "system"},
		{Key: "scheduler.timezone", Value: "UTC", ValueType: "string", Category: "scheduler"},
		{Key: "ui.default_locale", Value: "zh-CN", ValueType: "string", Category: "ui"},
		{Key: "ui.onboarding_completed", Value: "false", ValueType: "bool", Category: "ui"},
		// The landing page reads this local preference together with its compact
		// aggregate. It only controls widget visibility; collection jobs and
		// cached research snapshots are intentionally unaffected.
		{Key: "ui.dashboard_hidden_modules", Value: "[]", ValueType: "json", Category: "ui"},
		{Key: "telegram.api_base_url", Value: "https://api.telegram.org", ValueType: "string", Category: "telegram"},
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
		{Key: "ipo.lifecycle_sweep_enabled", Value: "true", ValueType: "bool", Category: "ipo"},
		{Key: "ipo.lifecycle_max_ciks", Value: "50", ValueType: "int", Category: "ipo"},
		{Key: "ipo.lifecycle_recheck_hours", Value: "12", ValueType: "int", Category: "ipo"},
		{Key: "ipo.longbridge_listing_verification_enabled", Value: "true", ValueType: "bool", Category: "ipo"},
		{Key: "ipo.longbridge_listing_request_budget", Value: "20", ValueType: "int", Category: "ipo"},
		{Key: "ipo.longbridge_listing_recheck_hours", Value: "24", ValueType: "int", Category: "ipo"},
		{Key: "ipo.longbridge_calendar_enabled", Value: "true", ValueType: "bool", Category: "ipo"},
		{Key: "ipo.longbridge_calendar_lookback_days", Value: "14", ValueType: "int", Category: "ipo"},
		{Key: "ipo.longbridge_calendar_lookahead_days", Value: "30", ValueType: "int", Category: "ipo"},
		{Key: "ipo.longbridge_calendar_max_pages", Value: "5", ValueType: "int", Category: "ipo"},
		{Key: "candidate_notification.enabled", Value: "false", ValueType: "bool", Category: "candidate_notification"},
		{Key: "candidate_notification.shadow_mode", Value: "false", ValueType: "bool", Category: "candidate_notification"},
		{Key: "candidate_notification.notify_a", Value: "false", ValueType: "bool", Category: "candidate_notification"},
		{Key: "candidate_notification.notify_b", Value: "false", ValueType: "bool", Category: "candidate_notification"},
		{Key: "candidate_notification.send_time", Value: "09:30", ValueType: "string", Category: "candidate_notification"},
		{Key: "candidate_notification.max_per_grade", Value: "5", ValueType: "int", Category: "candidate_notification"},
		{Key: "candidate_notification.actionable_only", Value: "true", ValueType: "bool", Category: "candidate_notification"},
		{Key: "candidate_notification.min_review_priority_score", Value: "0", ValueType: "int", Category: "candidate_notification"},
		{Key: "trade_setup_notification.enabled", Value: "false", ValueType: "bool", Category: "trade_setup_notification"},
		{Key: "trade_setup_notification.shadow_mode", Value: "false", ValueType: "bool", Category: "trade_setup_notification"},
		{Key: "trade_setup_notification.notify_entry", Value: "true", ValueType: "bool", Category: "trade_setup_notification"},
		{Key: "trade_setup_notification.notify_exit", Value: "true", ValueType: "bool", Category: "trade_setup_notification"},
		{Key: "trade_setup_notification.notify_invalidated", Value: "true", ValueType: "bool", Category: "trade_setup_notification"},
		{Key: "trade_setup_notification.max_per_run", Value: "10", ValueType: "int", Category: "trade_setup_notification"},
		// 站内消息与 Telegram 投递完全解耦；默认开启所有已支持的
		// 研究事件，用户可在系统配置中按事件类型关闭后续提醒。
		{Key: "in_app_notification.earnings_preview_enabled", Value: "true", ValueType: "bool", Category: "in_app_notification"},
		{Key: "in_app_notification.earnings_release_enabled", Value: "true", ValueType: "bool", Category: "in_app_notification"},
		{Key: "in_app_notification.technical_signal_enabled", Value: "true", ValueType: "bool", Category: "in_app_notification"},
		{Key: "in_app_notification.major_event_enabled", Value: "true", ValueType: "bool", Category: "in_app_notification"},
		{Key: "in_app_notification.insider_trading_enabled", Value: "true", ValueType: "bool", Category: "in_app_notification"},
		{Key: "in_app_notification.ipo_progress_enabled", Value: "true", ValueType: "bool", Category: "in_app_notification"},
		// Telegram shares the durable notification queue with every business
		// module. These event switches are a final, channel-specific gate: the
		// underlying module's own scope, threshold and quiet-hour rules still
		// apply before an event reaches the queue.
		{Key: "telegram_notification.earnings_preview_enabled", Value: "true", ValueType: "bool", Category: "telegram_notification"},
		{Key: "telegram_notification.earnings_release_enabled", Value: "true", ValueType: "bool", Category: "telegram_notification"},
		{Key: "telegram_notification.technical_signal_enabled", Value: "true", ValueType: "bool", Category: "telegram_notification"},
		{Key: "telegram_notification.major_event_enabled", Value: "true", ValueType: "bool", Category: "telegram_notification"},
		{Key: "telegram_notification.insider_trading_enabled", Value: "true", ValueType: "bool", Category: "telegram_notification"},
		{Key: "telegram_notification.ipo_progress_enabled", Value: "true", ValueType: "bool", Category: "telegram_notification"},
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
		{Key: "discovery.longbridge_app_key", Value: "", ValueType: "string", Category: "discovery", Encrypted: true},
		{Key: "discovery.longbridge_app_secret", Value: "", ValueType: "string", Category: "discovery", Encrypted: true},
		{Key: "discovery.longbridge_access_token", Value: "", ValueType: "string", Category: "discovery", Encrypted: true},
		{Key: "discovery.longbridge_company_profile_enabled", Value: "true", ValueType: "bool", Category: "discovery"},
		{Key: "discovery.longbridge_company_profile_request_budget", Value: "20", ValueType: "int", Category: "discovery"},
		{Key: "discovery.longbridge_company_profile_ttl_days", Value: "30", ValueType: "int", Category: "discovery"},
		{Key: "discovery.longbridge_analyst_rating_enabled", Value: "true", ValueType: "bool", Category: "discovery"},
		{Key: "discovery.longbridge_analyst_rating_request_budget", Value: "20", ValueType: "int", Category: "discovery"},
		{Key: "discovery.longbridge_analyst_rating_target_change_pct", Value: "5", ValueType: "float", Category: "discovery"},
		{Key: "discovery.longbridge_candidate_research_enabled", Value: "true", ValueType: "bool", Category: "discovery"},
		{Key: "discovery.longbridge_candidate_research_request_budget", Value: "5", ValueType: "int", Category: "discovery"},
		{Key: "discovery.longbridge_watch_target_research_enabled", Value: "true", ValueType: "bool", Category: "discovery"},
		{Key: "discovery.longbridge_watch_target_research_request_budget", Value: "5", ValueType: "int", Category: "discovery"},
		{Key: "discovery.longbridge_candidate_valuation_enabled", Value: "true", ValueType: "bool", Category: "discovery"},
		{Key: "discovery.longbridge_candidate_valuation_request_budget", Value: "3", ValueType: "int", Category: "discovery"},
		{Key: "discovery.longbridge_watch_target_valuation_enabled", Value: "true", ValueType: "bool", Category: "discovery"},
		{Key: "discovery.longbridge_watch_target_valuation_request_budget", Value: "3", ValueType: "int", Category: "discovery"},
		{Key: "discovery.longbridge_option_research_enabled", Value: "true", ValueType: "bool", Category: "discovery"},
		{Key: "discovery.longbridge_candidate_option_research_budget", Value: "5", ValueType: "int", Category: "discovery"},
		{Key: "discovery.longbridge_watch_target_option_research_budget", Value: "5", ValueType: "int", Category: "discovery"},
		{Key: "analyst_rating.notify_enabled", Value: "false", ValueType: "bool", Category: "analyst_rating"},
		// 财报预告只使用 Longbridge 的公开市场日历/共识结果，并写入本地
		// 缓存。默认关闭推送，避免首次同步把已有监控标的一次性发送给用户。
		{Key: "earnings_preview.enabled", Value: "true", ValueType: "bool", Category: "earnings_preview"},
		{Key: "earnings_preview.lookahead_days", Value: "90", ValueType: "int", Category: "earnings_preview"},
		{Key: "earnings_preview.max_calendar_pages", Value: "20", ValueType: "int", Category: "earnings_preview"},
		{Key: "earnings_preview.notify_enabled", Value: "false", ValueType: "bool", Category: "earnings_preview"},
		{Key: "earnings_preview.reminder_days", Value: "7,3,1,0", ValueType: "string", Category: "earnings_preview"},
		{Key: "discovery.min_publish_coverage_pct", Value: "85", ValueType: "float", Category: "discovery"},
		{Key: "discovery.research_mode", Value: "true", ValueType: "bool", Category: "discovery"},
		{Key: "discovery.auto_technical_history_warmup", Value: "true", ValueType: "bool", Category: "discovery"},
		{Key: "discovery.task_timeout_minutes", Value: "60", ValueType: "int", Category: "discovery"},
		{Key: "discovery.download_idle_timeout_seconds", Value: "90", ValueType: "int", Category: "discovery"},
		{Key: "discovery.sec_bulk_cache_ttl_hours", Value: "12", ValueType: "int", Category: "discovery"},
		{Key: "discovery.cache_retention_days", Value: "14", ValueType: "int", Category: "discovery"},
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
			value, err := s.encryptConfigInput(input)
			if err != nil {
				return err
			}
			cfg := model.SystemConfig{
				ConfigKey:   strings.TrimSpace(input.Key),
				ConfigValue: value,
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
		effectiveInputs := make([]ConfigInput, 0, len(inputs))
		for _, input := range inputs {
			key := strings.TrimSpace(input.Key)
			if err := validateConfigInput(key, input.Value); err != nil {
				return err
			}
			var existing model.SystemConfig
			err := tx.Where("config_key = ?", key).First(&existing).Error
			if err != nil && err != gorm.ErrRecordNotFound {
				return err
			}
			input.Key = key
			input.Encrypted = input.Encrypted || isSensitiveConfigKey(key) || (err == nil && existing.Encrypted)
			if input.Encrypted && IsMaskedSecret(input.Value) && err == nil {
				effectiveInputs = append(effectiveInputs, input)
				continue
			}
			value, err := s.encryptConfigInput(input)
			if err != nil {
				return err
			}
			cfg := model.SystemConfig{
				ConfigKey:   key,
				ConfigValue: value,
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
			effectiveInputs = append(effectiveInputs, input)
		}
		return NewAuditService(tx).Record(ctx, operator, "update", "system_config", "batch", nil, effectiveInputs)
	})
}

func isSensitiveConfigKey(key string) bool {
	_, ok := sensitiveConfigKeys[strings.TrimSpace(key)]
	return ok
}

func validateConfigInput(key string, value string) error {
	switch key {
	case "scheduler.timezone":
		if _, err := schedulerLocationFromValue(value); err != nil {
			return err
		}
	case "ipo.lifecycle_max_ciks":
		parsed, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil || parsed < 1 || parsed > 200 {
			return ErrValidation
		}
	case "ipo.lifecycle_recheck_hours":
		parsed, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil || parsed < 1 || parsed > 168 {
			return ErrValidation
		}
	case "ipo.longbridge_listing_request_budget":
		parsed, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil || parsed < 0 || parsed > 200 {
			return ErrValidation
		}
	case "ipo.longbridge_listing_recheck_hours":
		parsed, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil || parsed < 1 || parsed > 168 {
			return ErrValidation
		}
	case "ipo.longbridge_calendar_lookback_days", "ipo.longbridge_calendar_lookahead_days":
		parsed, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil || parsed < 0 || parsed > 365 {
			return ErrValidation
		}
	case "ipo.longbridge_calendar_max_pages":
		parsed, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil || parsed < 1 || parsed > 20 {
			return ErrValidation
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
				configs[i].ConfigValue = maskedSecretMarker
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
	if !cfg.Encrypted {
		return cfg.ConfigValue, true, nil
	}
	value, err := s.decryptSecret(cfg.ConfigValue)
	if err != nil {
		return "", false, err
	}
	return value, true, nil
}

func (s *ConfigService) encryptConfigInput(input ConfigInput) (string, error) {
	if !input.Encrypted || input.Value == "" {
		return input.Value, nil
	}
	if !s.encryptionEnforced {
		return input.Value, nil
	}
	if s.encryptionEnforced && s.EncryptionHealth().Status != "ok" {
		return "", fmt.Errorf("%w: %s", ErrValidation, s.EncryptionHealth().Message)
	}
	return s.encryptSecret(input.Value)
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
	baseURL, ok, err := s.GetValue(ctx, "telegram.api_base_url")
	if err != nil {
		return TelegramConfig{}, err
	}
	if !ok || strings.TrimSpace(baseURL) == "" {
		baseURL = "https://api.telegram.org"
	}
	enabled, _ := strconv.ParseBool(enabledRaw)
	return TelegramConfig{Enabled: enabled, BotToken: token, ChatID: chatID, APIBaseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/")}, nil
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
	lifecycleSweepRaw, _, err := s.GetValue(ctx, "ipo.lifecycle_sweep_enabled")
	if err != nil {
		return IPORadarSettings{}, err
	}
	lifecycleMaxRaw, _, err := s.GetValue(ctx, "ipo.lifecycle_max_ciks")
	if err != nil {
		return IPORadarSettings{}, err
	}
	lifecycleRecheckRaw, _, err := s.GetValue(ctx, "ipo.lifecycle_recheck_hours")
	if err != nil {
		return IPORadarSettings{}, err
	}
	longbridgeListingEnabledRaw, _, err := s.GetValue(ctx, "ipo.longbridge_listing_verification_enabled")
	if err != nil {
		return IPORadarSettings{}, err
	}
	longbridgeListingBudgetRaw, _, err := s.GetValue(ctx, "ipo.longbridge_listing_request_budget")
	if err != nil {
		return IPORadarSettings{}, err
	}
	longbridgeListingRecheckRaw, _, err := s.GetValue(ctx, "ipo.longbridge_listing_recheck_hours")
	if err != nil {
		return IPORadarSettings{}, err
	}
	longbridgeCalendarEnabledRaw, _, err := s.GetValue(ctx, "ipo.longbridge_calendar_enabled")
	if err != nil {
		return IPORadarSettings{}, err
	}
	longbridgeCalendarLookbackRaw, _, err := s.GetValue(ctx, "ipo.longbridge_calendar_lookback_days")
	if err != nil {
		return IPORadarSettings{}, err
	}
	longbridgeCalendarLookaheadRaw, _, err := s.GetValue(ctx, "ipo.longbridge_calendar_lookahead_days")
	if err != nil {
		return IPORadarSettings{}, err
	}
	longbridgeCalendarMaxPagesRaw, _, err := s.GetValue(ctx, "ipo.longbridge_calendar_max_pages")
	if err != nil {
		return IPORadarSettings{}, err
	}
	enabled, _ := strconv.ParseBool(enabledRaw)
	notify, _ := strconv.ParseBool(notifyRaw)
	lookback, _ := strconv.Atoi(lookbackRaw)
	maxResults, _ := strconv.Atoi(maxRaw)
	lifecycleSweepEnabled, _ := strconv.ParseBool(lifecycleSweepRaw)
	lifecycleMaxCIKs, _ := strconv.Atoi(lifecycleMaxRaw)
	lifecycleRecheckHours, _ := strconv.Atoi(lifecycleRecheckRaw)
	longbridgeListingEnabled, _ := strconv.ParseBool(longbridgeListingEnabledRaw)
	longbridgeListingBudget, _ := strconv.Atoi(longbridgeListingBudgetRaw)
	longbridgeListingRecheckHours, _ := strconv.Atoi(longbridgeListingRecheckRaw)
	longbridgeCalendarEnabled, _ := strconv.ParseBool(longbridgeCalendarEnabledRaw)
	longbridgeCalendarLookback, _ := strconv.Atoi(longbridgeCalendarLookbackRaw)
	longbridgeCalendarLookahead, _ := strconv.Atoi(longbridgeCalendarLookaheadRaw)
	longbridgeCalendarMaxPages, _ := strconv.Atoi(longbridgeCalendarMaxPagesRaw)
	if lookback <= 0 {
		lookback = 7
	}
	if maxResults <= 0 || maxResults > 200 {
		maxResults = 100
	}
	if lifecycleMaxCIKs < 1 || lifecycleMaxCIKs > 200 {
		lifecycleMaxCIKs = 50
	}
	if lifecycleRecheckHours < 1 || lifecycleRecheckHours > 168 {
		lifecycleRecheckHours = 12
	}
	if longbridgeListingBudget < 0 || longbridgeListingBudget > 200 {
		longbridgeListingBudget = 20
	}
	if longbridgeListingRecheckHours < 1 || longbridgeListingRecheckHours > 168 {
		longbridgeListingRecheckHours = 24
	}
	if longbridgeCalendarLookback < 0 || longbridgeCalendarLookback > 365 {
		longbridgeCalendarLookback = 14
	}
	if longbridgeCalendarLookahead < 0 || longbridgeCalendarLookahead > 365 {
		longbridgeCalendarLookahead = 30
	}
	if longbridgeCalendarMaxPages < 1 || longbridgeCalendarMaxPages > 20 {
		longbridgeCalendarMaxPages = 5
	}
	formTypes := splitConfigList(formTypesRaw)
	if len(formTypes) == 0 {
		formTypes = []string{"S-1", "S-1/A", "F-1", "F-1/A", "S-1MEF"}
	}
	return IPORadarSettings{
		Enabled:                              enabled,
		FormTypes:                            formTypes,
		LookbackDays:                         lookback,
		MaxResults:                           maxResults,
		NotifyEnabled:                        notify,
		NotifyFormTypes:                      splitConfigList(notifyFormTypesRaw),
		Keywords:                             splitConfigList(keywordsRaw),
		LifecycleSweepEnabled:                lifecycleSweepEnabled,
		LifecycleMaxCIKs:                     lifecycleMaxCIKs,
		LifecycleRecheckHours:                lifecycleRecheckHours,
		LongbridgeListingVerificationEnabled: longbridgeListingEnabled,
		LongbridgeListingRequestBudget:       longbridgeListingBudget,
		LongbridgeListingRecheckHours:        longbridgeListingRecheckHours,
		LongbridgeIPOCalendarEnabled:         longbridgeCalendarEnabled,
		LongbridgeIPOCalendarLookbackDays:    longbridgeCalendarLookback,
		LongbridgeIPOCalendarLookaheadDays:   longbridgeCalendarLookahead,
		LongbridgeIPOCalendarMaxPages:        longbridgeCalendarMaxPages,
	}, nil
}

func (s *ConfigService) CandidateNotificationSettings(ctx context.Context) (CandidateNotificationSettings, error) {
	enabledRaw, _, err := s.GetValue(ctx, "candidate_notification.enabled")
	if err != nil {
		return CandidateNotificationSettings{}, err
	}
	shadowRaw, shadowConfigured, err := s.GetValue(ctx, "candidate_notification.shadow_mode")
	if err != nil {
		return CandidateNotificationSettings{}, err
	}
	if !shadowConfigured {
		// Keep the existing delivery behavior for installations created before
		// shadow mode was introduced. Operators opt in explicitly from settings.
		shadowRaw = "false"
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
	shadow, _ := strconv.ParseBool(shadowRaw)
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
		ShadowMode:             shadow,
		NotifyA:                notifyA,
		NotifyB:                notifyB,
		SendTime:               valueOrDefault(sendTime, "09:30"),
		MaxPerGrade:            maxPerGrade,
		ActionableOnly:         actionableOnly,
		MinReviewPriorityScore: minPriority,
	}, nil
}

func (s *ConfigService) TradeSetupNotificationSettings(ctx context.Context) (TradeSetupNotificationSettings, error) {
	keys := []string{
		"trade_setup_notification.enabled",
		"trade_setup_notification.shadow_mode",
		"trade_setup_notification.notify_entry",
		"trade_setup_notification.notify_exit",
		"trade_setup_notification.notify_invalidated",
		"trade_setup_notification.max_per_run",
	}
	values := make(map[string]string, len(keys))
	for _, key := range keys {
		value, _, err := s.GetValue(ctx, key)
		if err != nil {
			return TradeSetupNotificationSettings{}, err
		}
		values[key] = value
	}
	maxPerRun, _ := strconv.Atoi(values["trade_setup_notification.max_per_run"])
	if maxPerRun <= 0 {
		maxPerRun = 10
	}
	if maxPerRun > 50 {
		maxPerRun = 50
	}
	enabled, _ := strconv.ParseBool(values["trade_setup_notification.enabled"])
	shadow, _ := strconv.ParseBool(values["trade_setup_notification.shadow_mode"])
	notifyEntry, _ := strconv.ParseBool(values["trade_setup_notification.notify_entry"])
	notifyExit, _ := strconv.ParseBool(values["trade_setup_notification.notify_exit"])
	notifyInvalidated, _ := strconv.ParseBool(values["trade_setup_notification.notify_invalidated"])
	return TradeSetupNotificationSettings{
		Enabled: enabled, ShadowMode: shadow, NotifyEntry: notifyEntry, NotifyExit: notifyExit,
		NotifyInvalidated: notifyInvalidated, MaxPerRun: maxPerRun,
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
	if appKey, ok, err := s.GetValue(ctx, "discovery.longbridge_app_key"); err != nil {
		return cfg, err
	} else if ok && strings.TrimSpace(appKey) != "" && !IsMaskedSecret(appKey) {
		cfg.LongbridgeAppKey = strings.TrimSpace(appKey)
	}
	if appSecret, ok, err := s.GetValue(ctx, "discovery.longbridge_app_secret"); err != nil {
		return cfg, err
	} else if ok && strings.TrimSpace(appSecret) != "" && !IsMaskedSecret(appSecret) {
		cfg.LongbridgeAppSecret = strings.TrimSpace(appSecret)
	}
	if accessToken, ok, err := s.GetValue(ctx, "discovery.longbridge_access_token"); err != nil {
		return cfg, err
	} else if ok && strings.TrimSpace(accessToken) != "" && !IsMaskedSecret(accessToken) {
		cfg.LongbridgeAccessToken = strings.TrimSpace(accessToken)
	}
	if enabled, ok, err := s.GetValue(ctx, "discovery.longbridge_company_profile_enabled"); err != nil {
		return cfg, err
	} else if ok && strings.TrimSpace(enabled) != "" {
		if parsed, parseErr := strconv.ParseBool(strings.TrimSpace(enabled)); parseErr == nil {
			cfg.LongbridgeCompanyProfileEnabled = parsed
		}
	}
	if budget, ok, err := s.GetValue(ctx, "discovery.longbridge_company_profile_request_budget"); err != nil {
		return cfg, err
	} else if ok && strings.TrimSpace(budget) != "" {
		if parsed, parseErr := strconv.Atoi(strings.TrimSpace(budget)); parseErr == nil && parsed >= 0 {
			cfg.LongbridgeCompanyProfileRequestBudget = parsed
		}
	}
	if ttlDays, ok, err := s.GetValue(ctx, "discovery.longbridge_company_profile_ttl_days"); err != nil {
		return cfg, err
	} else if ok && strings.TrimSpace(ttlDays) != "" {
		if parsed, parseErr := strconv.Atoi(strings.TrimSpace(ttlDays)); parseErr == nil && parsed > 0 {
			cfg.LongbridgeCompanyProfileTTLDays = parsed
		}
	}
	if enabled, ok, err := s.GetValue(ctx, "discovery.longbridge_analyst_rating_enabled"); err != nil {
		return cfg, err
	} else if ok && strings.TrimSpace(enabled) != "" {
		if parsed, parseErr := strconv.ParseBool(strings.TrimSpace(enabled)); parseErr == nil {
			cfg.LongbridgeAnalystRatingEnabled = parsed
		}
	}
	if budget, ok, err := s.GetValue(ctx, "discovery.longbridge_analyst_rating_request_budget"); err != nil {
		return cfg, err
	} else if ok && strings.TrimSpace(budget) != "" {
		if parsed, parseErr := strconv.Atoi(strings.TrimSpace(budget)); parseErr == nil && parsed >= 0 {
			cfg.LongbridgeAnalystRatingRequestBudget = parsed
		}
	}
	if threshold, ok, err := s.GetValue(ctx, "discovery.longbridge_analyst_rating_target_change_pct"); err != nil {
		return cfg, err
	} else if ok && strings.TrimSpace(threshold) != "" {
		if parsed, parseErr := strconv.ParseFloat(strings.TrimSpace(threshold), 64); parseErr == nil && parsed >= 0 {
			cfg.LongbridgeAnalystRatingTargetChangePct = parsed
		}
	}
	if enabled, ok, err := s.GetValue(ctx, "discovery.longbridge_candidate_research_enabled"); err != nil {
		return cfg, err
	} else if ok && strings.TrimSpace(enabled) != "" {
		if parsed, parseErr := strconv.ParseBool(strings.TrimSpace(enabled)); parseErr == nil {
			cfg.LongbridgeCandidateResearchEnabled = parsed
		}
	}
	if budget, ok, err := s.GetValue(ctx, "discovery.longbridge_candidate_research_request_budget"); err != nil {
		return cfg, err
	} else if ok && strings.TrimSpace(budget) != "" {
		if parsed, parseErr := strconv.Atoi(strings.TrimSpace(budget)); parseErr == nil && parsed >= 0 {
			cfg.LongbridgeCandidateResearchRequestBudget = parsed
		}
	}
	if enabled, ok, err := s.GetValue(ctx, "discovery.longbridge_watch_target_research_enabled"); err != nil {
		return cfg, err
	} else if ok && strings.TrimSpace(enabled) != "" {
		if parsed, parseErr := strconv.ParseBool(strings.TrimSpace(enabled)); parseErr == nil {
			cfg.LongbridgeWatchTargetResearchEnabled = parsed
		}
	}
	if budget, ok, err := s.GetValue(ctx, "discovery.longbridge_watch_target_research_request_budget"); err != nil {
		return cfg, err
	} else if ok && strings.TrimSpace(budget) != "" {
		if parsed, parseErr := strconv.Atoi(strings.TrimSpace(budget)); parseErr == nil && parsed >= 0 {
			cfg.LongbridgeWatchTargetResearchRequestBudget = parsed
		}
	}
	if enabled, ok, err := s.GetValue(ctx, "discovery.longbridge_candidate_valuation_enabled"); err != nil {
		return cfg, err
	} else if ok && strings.TrimSpace(enabled) != "" {
		if parsed, parseErr := strconv.ParseBool(strings.TrimSpace(enabled)); parseErr == nil {
			cfg.LongbridgeCandidateValuationEnabled = parsed
		}
	}
	if budget, ok, err := s.GetValue(ctx, "discovery.longbridge_candidate_valuation_request_budget"); err != nil {
		return cfg, err
	} else if ok && strings.TrimSpace(budget) != "" {
		if parsed, parseErr := strconv.Atoi(strings.TrimSpace(budget)); parseErr == nil && parsed >= 0 {
			cfg.LongbridgeCandidateValuationRequestBudget = parsed
		}
	}
	if enabled, ok, err := s.GetValue(ctx, "discovery.longbridge_watch_target_valuation_enabled"); err != nil {
		return cfg, err
	} else if ok && strings.TrimSpace(enabled) != "" {
		if parsed, parseErr := strconv.ParseBool(strings.TrimSpace(enabled)); parseErr == nil {
			cfg.LongbridgeWatchTargetValuationEnabled = parsed
		}
	}
	if budget, ok, err := s.GetValue(ctx, "discovery.longbridge_watch_target_valuation_request_budget"); err != nil {
		return cfg, err
	} else if ok && strings.TrimSpace(budget) != "" {
		if parsed, parseErr := strconv.Atoi(strings.TrimSpace(budget)); parseErr == nil && parsed >= 0 {
			cfg.LongbridgeWatchTargetValuationRequestBudget = parsed
		}
	}
	if enabled, ok, err := s.GetValue(ctx, "discovery.longbridge_option_research_enabled"); err != nil {
		return cfg, err
	} else if ok && strings.TrimSpace(enabled) != "" {
		if parsed, parseErr := strconv.ParseBool(strings.TrimSpace(enabled)); parseErr == nil {
			cfg.LongbridgeOptionResearchEnabled = parsed
		}
	}
	if budget, ok, err := s.GetValue(ctx, "discovery.longbridge_candidate_option_research_budget"); err != nil {
		return cfg, err
	} else if ok && strings.TrimSpace(budget) != "" {
		if parsed, parseErr := strconv.Atoi(strings.TrimSpace(budget)); parseErr == nil && parsed >= 0 {
			cfg.LongbridgeCandidateOptionResearchBudget = parsed
		}
	}
	if budget, ok, err := s.GetValue(ctx, "discovery.longbridge_watch_target_option_research_budget"); err != nil {
		return cfg, err
	} else if ok && strings.TrimSpace(budget) != "" {
		if parsed, parseErr := strconv.Atoi(strings.TrimSpace(budget)); parseErr == nil && parsed >= 0 {
			cfg.LongbridgeWatchTargetOptionResearchBudget = parsed
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
	if enabled, ok, err := s.GetValue(ctx, "discovery.auto_technical_history_warmup"); err != nil {
		return cfg, err
	} else if ok && strings.TrimSpace(enabled) != "" {
		parsed, parseErr := strconv.ParseBool(strings.TrimSpace(enabled))
		if parseErr == nil {
			cfg.AutoTechnicalHistoryWarmup = parsed
		}
	}
	if value, ok, err := s.GetValue(ctx, "discovery.task_timeout_minutes"); err != nil {
		return cfg, err
	} else if ok && strings.TrimSpace(value) != "" {
		if parsed, parseErr := strconv.Atoi(strings.TrimSpace(value)); parseErr == nil && parsed > 0 {
			cfg.TaskTimeoutMin = parsed
		}
	}
	if value, ok, err := s.GetValue(ctx, "discovery.download_idle_timeout_seconds"); err != nil {
		return cfg, err
	} else if ok && strings.TrimSpace(value) != "" {
		if parsed, parseErr := strconv.Atoi(strings.TrimSpace(value)); parseErr == nil && parsed > 0 {
			cfg.DownloadIdleTimeoutSec = parsed
		}
	}
	if value, ok, err := s.GetValue(ctx, "discovery.sec_bulk_cache_ttl_hours"); err != nil {
		return cfg, err
	} else if ok && strings.TrimSpace(value) != "" {
		if parsed, parseErr := strconv.Atoi(strings.TrimSpace(value)); parseErr == nil && parsed > 0 {
			cfg.SECBulkCacheTTLHours = parsed
		}
	}
	if value, ok, err := s.GetValue(ctx, "discovery.cache_retention_days"); err != nil {
		return cfg, err
	} else if ok && strings.TrimSpace(value) != "" {
		parsed, parseErr := strconv.Atoi(strings.TrimSpace(value))
		if parseErr == nil && parsed > 0 {
			cfg.CacheRetentionDays = parsed
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
