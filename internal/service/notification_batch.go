package service

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"sec_monitor/internal/model"
	"sec_monitor/internal/telegram"

	"gorm.io/gorm"
)

type NotificationCandidate struct {
	EntityKind  string
	FilingID    string
	Ticker      string
	CIK         string
	CompanyName string
	FilingType  string
	Title       string
	FilingURL   string
	Status      string
	Reason      string
	EventAt     time.Time
}

type NotificationBatchInput struct {
	SyncRunID  uint
	Source     string
	Trigger    string
	Candidates []NotificationCandidate
}

type NotificationBatchFilter struct {
	Source   string
	Status   string
	Trigger  string
	DateFrom *time.Time
	DateTo   *time.Time
	Page     int
	PageSize int
}

type NotificationBatchService struct {
	db       *gorm.DB
	notifier telegram.Notifier
	configs  *ConfigService
}

func NewNotificationBatchService(db *gorm.DB, notifier telegram.Notifier, configs *ConfigService) *NotificationBatchService {
	return &NotificationBatchService{db: db, notifier: notifier, configs: configs}
}

func (s *NotificationBatchService) Deliver(ctx context.Context, input NotificationBatchInput) (model.NotificationBatch, error) {
	if len(input.Candidates) == 0 {
		return model.NotificationBatch{}, nil
	}
	now := time.Now().UTC()
	eligible := make([]NotificationCandidate, 0, len(input.Candidates))
	reasonCounts := map[string]int{}
	for _, candidate := range input.Candidates {
		if candidate.Reason == "eligible" {
			eligible = append(eligible, candidate)
		} else {
			reasonCounts[candidate.Reason]++
		}
	}
	batch := model.NotificationBatch{
		SyncRunID:          input.SyncRunID,
		Source:             strings.TrimSpace(input.Source),
		Trigger:            strings.TrimSpace(input.Trigger),
		Channel:            "telegram",
		Status:             "suppressed",
		ItemCount:          len(input.Candidates),
		SuppressedCount:    len(input.Candidates) - len(eligible),
		SuppressionSummary: formatReasonCounts(reasonCounts),
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	if len(eligible) > 0 {
		batch.Status = "pending"
	}
	if err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&batch).Error; err != nil {
			return err
		}
		items := make([]model.NotificationBatchItem, 0, len(input.Candidates))
		for _, candidate := range input.Candidates {
			status := "suppressed"
			if candidate.Reason == "eligible" {
				status = "pending"
			}
			items = append(items, model.NotificationBatchItem{
				BatchID: batch.ID, EntityKind: candidate.EntityKind, FilingID: candidate.FilingID,
				Ticker: candidate.Ticker, CIK: candidate.CIK, CompanyName: candidate.CompanyName,
				FilingType: candidate.FilingType, Title: candidate.Title, FilingURL: candidate.FilingURL,
				EventAt: candidate.EventAt, Status: status, Reason: candidate.Reason, CreatedAt: now, UpdatedAt: now,
			})
		}
		return tx.Create(&items).Error
	}); err != nil {
		return model.NotificationBatch{}, err
	}
	if len(eligible) == 0 {
		return batch, nil
	}

	cfg, err := s.configs.Telegram(ctx)
	if err != nil {
		return model.NotificationBatch{}, err
	}
	batch.Target = cfg.ChatID
	if !cfg.Enabled || cfg.BotToken == "" || cfg.ChatID == "" {
		return s.finishSuppressed(ctx, batch, eligible, "notification_disabled")
	}

	message := telegram.Message{Text: renderNotificationBatchSummary(input.Source, eligible)}
	if err := sendWithRetry(ctx, s.notifier, message, 3); err != nil {
		batch.Status = "failed"
		batch.FailedCount = len(eligible)
		batch.RetryCount = 3
		batch.ErrorMessage = err.Error()
		if dbErr := s.updateBatchResult(ctx, batch, eligible, "failed", nil); dbErr != nil {
			return model.NotificationBatch{}, dbErr
		}
		return batch, nil
	}

	batch.Status = "sent"
	batch.SentCount = len(eligible)
	batch.SentAt = &now
	if err := s.updateBatchResult(ctx, batch, eligible, "sent", &now); err != nil {
		return model.NotificationBatch{}, err
	}
	return batch, nil
}

func (s *NotificationBatchService) finishSuppressed(ctx context.Context, batch model.NotificationBatch, candidates []NotificationCandidate, reason string) (model.NotificationBatch, error) {
	batch.Status = "suppressed"
	batch.SuppressedCount += len(candidates)
	counts := map[string]int{reason: len(candidates)}
	if batch.SuppressionSummary != "" {
		batch.SuppressionSummary += ", "
	}
	batch.SuppressionSummary += formatReasonCounts(counts)
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&model.NotificationBatch{}).Where("id = ?", batch.ID).Updates(batchResultUpdates(batch)).Error; err != nil {
			return err
		}
		return tx.Model(&model.NotificationBatchItem{}).Where("batch_id = ? AND status = ?", batch.ID, "pending").Updates(map[string]any{"status": "suppressed", "reason": reason, "updated_at": time.Now().UTC()}).Error
	})
	return batch, err
}

func (s *NotificationBatchService) updateBatchResult(ctx context.Context, batch model.NotificationBatch, candidates []NotificationCandidate, itemStatus string, notifiedAt *time.Time) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&model.NotificationBatch{}).Where("id = ?", batch.ID).Updates(batchResultUpdates(batch)).Error; err != nil {
			return err
		}
		reason := "delivery_failed"
		if itemStatus == "sent" {
			reason = "eligible"
		}
		if err := tx.Model(&model.NotificationBatchItem{}).Where("batch_id = ? AND status = ?", batch.ID, "pending").Updates(map[string]any{"status": itemStatus, "reason": reason, "updated_at": time.Now().UTC()}).Error; err != nil {
			return err
		}
		if notifiedAt == nil {
			return nil
		}
		for _, candidate := range candidates {
			var target any
			switch candidate.EntityKind {
			case "filing":
				target = &model.Filing{}
			case "ipo_filing":
				target = &model.IPOFiling{}
			case "ipo_offering":
				target = &model.IPOOfferingEvent{}
			default:
				continue
			}
			if err := tx.Model(target).Where("filing_id = ?", candidate.FilingID).Update("notified_at", notifiedAt).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func batchResultUpdates(batch model.NotificationBatch) map[string]any {
	return map[string]any{
		"target": batch.Target, "status": batch.Status, "sent_count": batch.SentCount,
		"suppressed_count": batch.SuppressedCount, "failed_count": batch.FailedCount,
		"retry_count": batch.RetryCount, "suppression_summary": batch.SuppressionSummary,
		"error_message": batch.ErrorMessage, "sent_at": batch.SentAt, "updated_at": time.Now().UTC(),
	}
}

func renderNotificationBatchSummary(source string, candidates []NotificationCandidate) string {
	title := "SEC 公告"
	if source == "ipo" {
		title = "IPO 监控"
	} else if source == "ipo_offering" {
		title = "IPO 定价"
	}
	counts := map[string]int{}
	for _, candidate := range candidates {
		key := valueOrDefault(strings.TrimSpace(candidate.Ticker), strings.TrimSpace(candidate.CompanyName))
		if key == "" {
			key = valueOrDefault(candidate.CIK, "Unknown")
		}
		counts[key]++
	}
	keys := make([]string, 0, len(counts))
	for key := range counts {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	groups := make([]string, 0, len(keys))
	for _, key := range keys {
		groups = append(groups, fmt.Sprintf("%s %d", key, counts[key]))
	}
	var builder strings.Builder
	fmt.Fprintf(&builder, "%s同步摘要：新增 %d 条\n%s", title, len(candidates), strings.Join(groups, " | "))
	limit := len(candidates)
	if limit > 10 {
		limit = 10
	}
	for index := 0; index < limit; index++ {
		candidate := candidates[index]
		label := valueOrDefault(candidate.Ticker, candidate.CompanyName)
		fmt.Fprintf(&builder, "\n\n%d. %s %s %s\n%s", index+1, label, candidate.FilingType, strings.TrimSpace(candidate.Title), candidate.FilingURL)
	}
	if len(candidates) > limit {
		fmt.Fprintf(&builder, "\n\n另有 %d 条，请在 SEC Monitor 中查看。", len(candidates)-limit)
	}
	return truncateTelegramMessage(builder.String(), 4000)
}

func truncateTelegramMessage(message string, maxRunes int) string {
	runes := []rune(message)
	if len(runes) <= maxRunes {
		return message
	}
	suffix := "\n\n内容过长，更多详情请在 SEC Monitor 中查看。"
	suffixRunes := []rune(suffix)
	if maxRunes <= len(suffixRunes) {
		return string(suffixRunes[:maxRunes])
	}
	return string(runes[:maxRunes-len(suffixRunes)]) + suffix
}

func formatReasonCounts(counts map[string]int) string {
	keys := make([]string, 0, len(counts))
	for key := range counts {
		if key != "" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%s:%d", key, counts[key]))
	}
	return strings.Join(parts, ", ")
}

func (s *NotificationBatchService) List(ctx context.Context, filter NotificationBatchFilter) (PageResult[model.NotificationBatch], error) {
	page, pageSize := normalizePage(filter.Page, filter.PageSize)
	query := s.db.WithContext(ctx).Model(&model.NotificationBatch{})
	if value := strings.TrimSpace(filter.Source); value != "" {
		query = query.Where("source = ?", value)
	}
	if value := strings.TrimSpace(filter.Status); value != "" {
		query = query.Where("status = ?", value)
	}
	if value := strings.TrimSpace(filter.Trigger); value != "" {
		query = query.Where("trigger = ?", value)
	}
	if filter.DateFrom != nil {
		query = query.Where("created_at >= ?", *filter.DateFrom)
	}
	if filter.DateTo != nil {
		query = query.Where("created_at < ?", filter.DateTo.AddDate(0, 0, 1))
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return PageResult[model.NotificationBatch]{}, err
	}
	var items []model.NotificationBatch
	err := query.Order("created_at DESC, id DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&items).Error
	return newPageResult(items, total, page, pageSize), err
}

func (s *NotificationBatchService) ListItems(ctx context.Context, batchID uint, page int, pageSize int) (PageResult[model.NotificationBatchItem], error) {
	page, pageSize = normalizePage(page, pageSize)
	query := s.db.WithContext(ctx).Model(&model.NotificationBatchItem{}).Where("batch_id = ?", batchID)
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return PageResult[model.NotificationBatchItem]{}, err
	}
	var items []model.NotificationBatchItem
	err := query.Order("event_at DESC, id DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&items).Error
	return newPageResult(items, total, page, pageSize), err
}
