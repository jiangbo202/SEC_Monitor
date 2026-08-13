package service

import (
	"context"
	"errors"
	"strings"
	"time"

	"sec_monitor/internal/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type InAppNotificationInput struct {
	EventKey    string
	Source      string
	Scope       string
	EntityKind  string
	TargetID    uint
	Ticker      string
	CompanyName string
	Severity    string
	Title       string
	Body        string
	Link        string
	OccurredAt  time.Time
}

type InAppNotificationFilter struct {
	UnreadOnly bool
	Page       int
	PageSize   int
}

type InAppNotificationService struct {
	db      *gorm.DB
	configs *ConfigService
	now     func() time.Time
}

func NewInAppNotificationService(db *gorm.DB, configs ...*ConfigService) *InAppNotificationService {
	service := &InAppNotificationService{db: db, now: time.Now}
	if len(configs) > 0 {
		service.configs = configs[0]
	}
	return service
}

// Create records a single immutable event. The event key is the de-duplication
// boundary, making repeated syncs, retries, and concurrent runs safe.
func (s *InAppNotificationService) Create(ctx context.Context, input InAppNotificationInput) (model.InAppNotification, bool, error) {
	if s == nil || s.db == nil {
		return model.InAppNotification{}, false, nil
	}
	input.EventKey = strings.TrimSpace(input.EventKey)
	if input.EventKey == "" || strings.TrimSpace(input.Source) == "" || strings.TrimSpace(input.Title) == "" {
		return model.InAppNotification{}, false, ErrValidation
	}
	if enabled, err := s.sourceEnabled(ctx, input.Source); err != nil {
		return model.InAppNotification{}, false, err
	} else if !enabled {
		return model.InAppNotification{}, false, nil
	}
	occurredAt := input.OccurredAt.UTC()
	if occurredAt.IsZero() {
		occurredAt = s.now().UTC()
	}
	now := s.now().UTC()
	item := model.InAppNotification{
		EventKey: input.EventKey, Source: strings.TrimSpace(input.Source), Scope: defaultInAppValue(input.Scope, "watch_target"),
		EntityKind: defaultInAppValue(input.EntityKind, "ticker"), TargetID: input.TargetID, Ticker: strings.ToUpper(strings.TrimSpace(input.Ticker)),
		CompanyName: strings.TrimSpace(input.CompanyName), Severity: defaultInAppValue(input.Severity, "info"),
		Title: strings.TrimSpace(input.Title), Body: strings.TrimSpace(input.Body), Link: strings.TrimSpace(input.Link),
		OccurredAt: occurredAt, CreatedAt: now, UpdatedAt: now,
	}
	result := s.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&item)
	if result.Error != nil {
		return model.InAppNotification{}, false, result.Error
	}
	if result.RowsAffected == 1 {
		return item, true, nil
	}
	if err := s.db.WithContext(ctx).Where("event_key = ?", input.EventKey).First(&item).Error; err != nil {
		return model.InAppNotification{}, false, err
	}
	return item, false, nil
}

func (s *InAppNotificationService) sourceEnabled(ctx context.Context, source string) (bool, error) {
	if s == nil || s.configs == nil {
		return true, nil
	}
	key, legacyKey, ok := inAppNotificationConfigKeys(source)
	if !ok {
		return true, nil
	}
	value, found, err := s.configs.GetValue(ctx, key)
	if err != nil {
		return true, err
	}
	if !found && legacyKey != "" {
		value, found, err = s.configs.GetValue(ctx, legacyKey)
		if err != nil {
			return true, err
		}
	}
	if !found {
		return true, nil
	}
	return configBool(value, true), nil
}

// inAppNotificationConfigKeys keeps the former event-wide setting as a
// fallback. It lets existing installations retain their preference until the
// new menu-scoped switches are saved for the first time.
func inAppNotificationConfigKeys(source string) (string, string, bool) {
	switch strings.TrimSpace(source) {
	case "earnings_preview_watch_target":
		return "in_app_notification.watch_target_earnings_preview_enabled", "in_app_notification.earnings_preview_enabled", true
	case "earnings_preview_candidate":
		return "in_app_notification.candidate_earnings_preview_enabled", "in_app_notification.earnings_preview_enabled", true
	case "earnings_release_watch_target":
		return "in_app_notification.watch_target_earnings_release_enabled", "in_app_notification.earnings_release_enabled", true
	case "earnings_release_candidate":
		return "in_app_notification.candidate_earnings_release_enabled", "in_app_notification.earnings_release_enabled", true
	case "technical_signal_watch_target":
		return "in_app_notification.watch_target_technical_signal_enabled", "in_app_notification.technical_signal_enabled", true
	case "technical_signal_candidate":
		return "in_app_notification.candidate_technical_signal_enabled", "in_app_notification.technical_signal_enabled", true
	case "major_event_watch_target":
		return "in_app_notification.watch_target_major_event_enabled", "in_app_notification.major_event_enabled", true
	case "insider_trading_watch_target":
		return "in_app_notification.watch_target_insider_trading_enabled", "in_app_notification.insider_trading_enabled", true
	case "ipo_progress":
		return "in_app_notification.ipo_progress_enabled", "", true
	// Retain the names used by historical messages and any external callers.
	case "earnings_preview":
		return "in_app_notification.earnings_preview_enabled", "", true
	case "earnings_release":
		return "in_app_notification.earnings_release_enabled", "", true
	case "technical_signal":
		return "in_app_notification.technical_signal_enabled", "", true
	case "major_event":
		return "in_app_notification.major_event_enabled", "", true
	case "insider_trading":
		return "in_app_notification.insider_trading_enabled", "", true
	default:
		return "", "", false
	}
}

func (s *InAppNotificationService) List(ctx context.Context, filter InAppNotificationFilter) (PageResult[model.InAppNotification], error) {
	page, pageSize := normalizePage(filter.Page, filter.PageSize)
	query := s.db.WithContext(ctx).Model(&model.InAppNotification{})
	if filter.UnreadOnly {
		query = query.Where("read_at IS NULL")
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return PageResult[model.InAppNotification]{}, err
	}
	items := []model.InAppNotification{}
	if err := query.Order("occurred_at DESC, id DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&items).Error; err != nil {
		return PageResult[model.InAppNotification]{}, err
	}
	return newPageResult(items, total, page, pageSize), nil
}

func (s *InAppNotificationService) UnreadCount(ctx context.Context) (int64, error) {
	var count int64
	err := s.db.WithContext(ctx).Model(&model.InAppNotification{}).Where("read_at IS NULL").Count(&count).Error
	return count, err
}

func (s *InAppNotificationService) MarkRead(ctx context.Context, id uint) (bool, error) {
	if id == 0 {
		return false, ErrValidation
	}
	now := s.now().UTC()
	result := s.db.WithContext(ctx).Model(&model.InAppNotification{}).Where("id = ? AND read_at IS NULL", id).Updates(map[string]any{"read_at": &now, "updated_at": now})
	if result.Error != nil {
		return false, result.Error
	}
	if result.RowsAffected > 0 {
		return true, nil
	}
	var existing model.InAppNotification
	if err := s.db.WithContext(ctx).Select("id").First(&existing, id).Error; err != nil {
		return false, mapNotFound(err)
	}
	return false, nil
}

// MarkAllRead marks every unread inbox item as read in one atomic update.
// It intentionally does not delete history, so users can still switch from
// the unread filter to the complete notification timeline afterward.
func (s *InAppNotificationService) MarkAllRead(ctx context.Context) (int64, error) {
	if s == nil || s.db == nil {
		return 0, errors.New("in-app notification service is not configured")
	}
	now := s.now().UTC()
	result := s.db.WithContext(ctx).Model(&model.InAppNotification{}).
		Where("read_at IS NULL").
		Updates(map[string]any{"read_at": &now, "updated_at": now})
	if result.Error != nil {
		return 0, result.Error
	}
	return result.RowsAffected, nil
}

func defaultInAppValue(value, fallback string) string {
	if value = strings.TrimSpace(value); value != "" {
		return value
	}
	return fallback
}
