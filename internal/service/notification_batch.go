package service

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"sec_monitor/internal/model"
	"sec_monitor/internal/telegram"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var notificationRetryDelays = []time.Duration{5 * time.Minute, 15 * time.Minute, 45 * time.Minute, 2 * time.Hour, 6 * time.Hour}

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
	SyncRunID   uint
	Source      string
	Trigger     string
	Candidates  []NotificationCandidate
	SummaryText string
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

type NotificationRetryResult struct {
	Attempted  int `json:"attempted"`
	Sent       int `json:"sent"`
	Failed     int `json:"failed"`
	DeadLetter int `json:"dead_letter"`
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

	summaryText := strings.TrimSpace(input.SummaryText)
	if summaryText == "" {
		summaryText = renderNotificationBatchSummary(input.Source, eligible)
	}
	message := telegram.Message{Text: truncateTelegramMessage(summaryText, 4000)}
	if err := sendWithRetry(ctx, s.notifier, message, 3); err != nil {
		attemptedAt := time.Now().UTC()
		batch.Status = "failed"
		batch.FailedCount = len(eligible)
		batch.RetryCount = 1
		batch.LastAttemptAt = &attemptedAt
		batch.NextRetryAt = nextNotificationRetryAt(batch.RetryCount, attemptedAt)
		batch.ErrorMessage = SanitizeSensitiveError(err.Error())
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
	nextRetryAt := nullableNotificationTime(batch.NextRetryAt)
	lastAttemptAt := nullableNotificationTime(batch.LastAttemptAt)
	sentAt := nullableNotificationTime(batch.SentAt)
	return map[string]any{
		"target": batch.Target, "status": batch.Status, "sent_count": batch.SentCount,
		"suppressed_count": batch.SuppressedCount, "failed_count": batch.FailedCount,
		"retry_count": batch.RetryCount, "suppression_summary": batch.SuppressionSummary,
		"error_message": SanitizeSensitiveError(batch.ErrorMessage), "sent_at": sentAt,
		"next_retry_at": nextRetryAt, "last_attempt_at": lastAttemptAt, "updated_at": time.Now().UTC(),
	}
}

func nullableNotificationTime(value *time.Time) any {
	if value == nil {
		return gorm.Expr("NULL")
	}
	return value
}

func nextNotificationRetryAt(retryCount int, attemptedAt time.Time) *time.Time {
	if retryCount < 1 || retryCount > len(notificationRetryDelays) {
		return nil
	}
	next := attemptedAt.Add(notificationRetryDelays[retryCount-1])
	return &next
}

// RetryDue delivers failed batches whose durable retry time has arrived.
func (s *NotificationBatchService) RetryDue(ctx context.Context, now time.Time) (NotificationRetryResult, error) {
	now = now.UTC()
	var ids []uint
	if err := s.db.WithContext(ctx).Model(&model.NotificationBatch{}).
		Where("status = ? AND next_retry_at IS NOT NULL AND next_retry_at <= ?", "failed", now).
		Order("next_retry_at ASC, id ASC").Pluck("id", &ids).Error; err != nil {
		return NotificationRetryResult{}, sanitizeNotificationBatchError(err)
	}
	result := NotificationRetryResult{}
	for _, id := range ids {
		outcome, attempted, err := s.retryBatch(ctx, id, now)
		if err != nil {
			return result, sanitizeNotificationBatchError(err)
		}
		if !attempted {
			continue
		}
		result.Attempted++
		switch outcome {
		case "sent":
			result.Sent++
		case "dead_letter":
			result.DeadLetter++
		default:
			result.Failed++
		}
	}
	return result, nil
}

func (s *NotificationBatchService) retryBatch(ctx context.Context, batchID uint, now time.Time) (string, bool, error) {
	outcome := ""
	attempted := false
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var batch model.NotificationBatch
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ? AND status = ? AND next_retry_at IS NOT NULL AND next_retry_at <= ?", batchID, "failed", now).
			First(&batch).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			return err
		}
		var items []model.NotificationBatchItem
		if err := tx.Where("batch_id = ? AND status = ?", batch.ID, "failed").Order("id ASC").Find(&items).Error; err != nil {
			return err
		}
		if len(items) == 0 {
			return fmt.Errorf("notification batch %d has no failed items", batch.ID)
		}
		attempted = true
		candidates := notificationCandidatesFromBatchItems(items)
		message := telegram.Message{Text: truncateTelegramMessage(renderNotificationBatchSummary(batch.Source, candidates), 4000)}
		batch.LastAttemptAt = &now
		batch.RetryCount++
		if err := sendWithRetry(ctx, s.notifier, message, 3); err != nil {
			batch.ErrorMessage = SanitizeSensitiveError(err.Error())
			batch.FailedCount = len(items)
			if batch.RetryCount >= len(notificationRetryDelays) {
				batch.Status = "dead_letter"
				batch.NextRetryAt = nil
				outcome = "dead_letter"
			} else {
				batch.Status = "failed"
				batch.NextRetryAt = nextNotificationRetryAt(batch.RetryCount, now)
				outcome = "failed"
			}
			return tx.Model(&model.NotificationBatch{}).Where("id = ?", batch.ID).Updates(batchResultUpdates(batch)).Error
		}
		batch.Status = "sent"
		batch.SentCount = len(items)
		batch.FailedCount = 0
		batch.ErrorMessage = ""
		batch.NextRetryAt = nil
		batch.SentAt = &now
		if err := tx.Model(&model.NotificationBatch{}).Where("id = ?", batch.ID).Updates(batchResultUpdates(batch)).Error; err != nil {
			return err
		}
		if err := tx.Model(&model.NotificationBatchItem{}).Where("batch_id = ? AND status = ?", batch.ID, "failed").Updates(map[string]any{"status": "sent", "reason": "eligible", "updated_at": now}).Error; err != nil {
			return err
		}
		if err := markNotificationCandidatesNotified(tx, candidates, &now); err != nil {
			return err
		}
		outcome = "sent"
		return nil
	})
	return outcome, attempted, err
}

func notificationCandidatesFromBatchItems(items []model.NotificationBatchItem) []NotificationCandidate {
	candidates := make([]NotificationCandidate, 0, len(items))
	for _, item := range items {
		candidates = append(candidates, NotificationCandidate{
			EntityKind: item.EntityKind, FilingID: item.FilingID, Ticker: item.Ticker, CIK: item.CIK,
			CompanyName: item.CompanyName, FilingType: item.FilingType, Title: item.Title, FilingURL: item.FilingURL,
			Status: item.Status, Reason: "eligible", EventAt: item.EventAt,
		})
	}
	return candidates
}

func markNotificationCandidatesNotified(tx *gorm.DB, candidates []NotificationCandidate, notifiedAt *time.Time) error {
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
}

// Requeue makes a failed or dead-letter batch eligible for an immediate new retry cycle.
func (s *NotificationBatchService) Requeue(ctx context.Context, batchID uint, now time.Time) (model.NotificationBatch, error) {
	now = now.UTC()
	var batch model.NotificationBatch
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&batch, batchID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("%w: notification batch not found", ErrNotFound)
			}
			return err
		}
		if batch.Status != "failed" && batch.Status != "dead_letter" {
			return fmt.Errorf("%w: only failed or dead-letter notification batches can be requeued", ErrValidation)
		}
		batch.Status = "failed"
		batch.RetryCount = 0
		batch.NextRetryAt = &now
		batch.LastAttemptAt = nil
		batch.ErrorMessage = ""
		return tx.Model(&model.NotificationBatch{}).Where("id = ?", batch.ID).Updates(batchResultUpdates(batch)).Error
	})
	if err != nil {
		return model.NotificationBatch{}, sanitizeNotificationBatchError(err)
	}
	batch.ErrorMessage = SanitizeSensitiveError(batch.ErrorMessage)
	return batch, nil
}

func sanitizeNotificationBatchError(err error) error {
	if err == nil {
		return nil
	}
	message := SanitizeSensitiveError(err.Error())
	if errors.Is(err, ErrNotFound) {
		return fmt.Errorf("%w: %s", ErrNotFound, message)
	}
	if errors.Is(err, ErrValidation) {
		return fmt.Errorf("%w: %s", ErrValidation, message)
	}
	return errors.New(message)
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
	for i := range items {
		items[i].ErrorMessage = SanitizeSensitiveError(items[i].ErrorMessage)
	}
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
