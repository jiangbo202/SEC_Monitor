package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"sec_monitor/internal/model"
	"sec_monitor/internal/telegram"

	"gorm.io/gorm"
)

var notificationRetryDelays = []time.Duration{5 * time.Minute, 15 * time.Minute, 45 * time.Minute, 2 * time.Hour, 6 * time.Hour}

const notificationRetryLeaseDuration = 30 * time.Minute

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
		"retry_lease_until": nullableNotificationTime(batch.RetryLeaseUntil), "retry_lease_token": batch.RetryLeaseToken,
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
		Where("retry_lease_until IS NULL OR retry_lease_until <= ?", now).
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
	batch, items, leaseToken, claimed, err := s.claimDueBatch(ctx, batchID, now)
	if err != nil || !claimed {
		return "", false, err
	}
	candidates := notificationCandidatesFromBatchItems(items)
	message := telegram.Message{Text: truncateTelegramMessage(renderNotificationBatchSummary(batch.Source, candidates), 4000)}
	batch.LastAttemptAt = &now
	batch.RetryCount++
	batch.RetryLeaseUntil = nil
	batch.RetryLeaseToken = ""
	if err := sendWithRetry(ctx, s.notifier, message, 3); err != nil {
		batch.ErrorMessage = SanitizeSensitiveError(err.Error())
		batch.FailedCount = len(items)
		if batch.RetryCount > len(notificationRetryDelays) {
			batch.Status = "dead_letter"
			batch.NextRetryAt = nil
			if err := s.finishClaimedRetry(ctx, batch, candidates, leaseToken, false); err != nil {
				return "", true, err
			}
			return "dead_letter", true, nil
		}
		batch.Status = "failed"
		batch.NextRetryAt = nextNotificationRetryAt(batch.RetryCount, now)
		if err := s.finishClaimedRetry(ctx, batch, candidates, leaseToken, false); err != nil {
			return "", true, err
		}
		return "failed", true, nil
	}
	batch.Status = "sent"
	batch.SentCount = len(items)
	batch.FailedCount = 0
	batch.ErrorMessage = ""
	batch.NextRetryAt = nil
	batch.SentAt = &now
	if err := s.finishClaimedRetry(ctx, batch, candidates, leaseToken, true); err != nil {
		return "", true, err
	}
	return "sent", true, nil
}

// claimDueBatch takes durable ownership before a notification is sent. SQLite
// ignores FOR UPDATE, so an UPDATE with the due and lease predicates is used as
// the compare-and-swap instead.
func (s *NotificationBatchService) claimDueBatch(ctx context.Context, batchID uint, now time.Time) (model.NotificationBatch, []model.NotificationBatchItem, string, bool, error) {
	leaseToken, err := notificationRetryLeaseToken()
	if err != nil {
		return model.NotificationBatch{}, nil, "", false, err
	}
	leaseUntil := now.Add(notificationRetryLeaseDuration)
	claim := s.db.WithContext(ctx).Model(&model.NotificationBatch{}).
		Where("id = ? AND status = ? AND next_retry_at IS NOT NULL AND next_retry_at <= ?", batchID, "failed", now).
		Where("retry_lease_until IS NULL OR retry_lease_until <= ?", now).
		Updates(map[string]any{"retry_lease_until": leaseUntil, "retry_lease_token": leaseToken, "updated_at": time.Now().UTC()})
	if claim.Error != nil {
		return model.NotificationBatch{}, nil, "", false, claim.Error
	}
	if claim.RowsAffected == 0 {
		return model.NotificationBatch{}, nil, "", false, nil
	}
	var batch model.NotificationBatch
	if err := s.db.WithContext(ctx).Where("id = ? AND retry_lease_token = ?", batchID, leaseToken).First(&batch).Error; err != nil {
		return model.NotificationBatch{}, nil, "", false, err
	}
	var items []model.NotificationBatchItem
	if err := s.db.WithContext(ctx).Where("batch_id = ? AND status = ?", batch.ID, "failed").Order("id ASC").Find(&items).Error; err != nil {
		return model.NotificationBatch{}, nil, "", false, err
	}
	if len(items) == 0 {
		return model.NotificationBatch{}, nil, "", false, fmt.Errorf("notification batch %d has no failed items", batch.ID)
	}
	return batch, items, leaseToken, true, nil
}

func notificationRetryLeaseToken() (string, error) {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}

func (s *NotificationBatchService) finishClaimedRetry(ctx context.Context, batch model.NotificationBatch, candidates []NotificationCandidate, leaseToken string, sent bool) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&model.NotificationBatch{}).
			Where("id = ? AND status = ? AND retry_lease_token = ?", batch.ID, "failed", leaseToken).
			Updates(batchResultUpdates(batch))
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return fmt.Errorf("notification batch %d retry lease was lost", batch.ID)
		}
		if !sent {
			return nil
		}
		if err := tx.Model(&model.NotificationBatchItem{}).Where("batch_id = ? AND status = ?", batch.ID, "failed").Updates(map[string]any{"status": "sent", "reason": "eligible", "updated_at": batch.LastAttemptAt}).Error; err != nil {
			return err
		}
		return markNotificationCandidatesNotified(tx, candidates, batch.SentAt)
	})
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
	if err := s.db.WithContext(ctx).First(&batch, batchID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return model.NotificationBatch{}, fmt.Errorf("%w: notification batch not found", ErrNotFound)
		}
		return model.NotificationBatch{}, sanitizeNotificationBatchError(err)
	}
	if batch.Status != "failed" && batch.Status != "dead_letter" {
		return model.NotificationBatch{}, fmt.Errorf("%w: only failed or dead-letter notification batches can be requeued", ErrValidation)
	}
	if batch.RetryLeaseUntil != nil && batch.RetryLeaseUntil.After(now) {
		return model.NotificationBatch{}, fmt.Errorf("%w: notification batch is currently being retried", ErrValidation)
	}
	batch.Status = "failed"
	batch.RetryCount = 0
	batch.NextRetryAt = &now
	batch.LastAttemptAt = nil
	batch.RetryLeaseUntil = nil
	batch.RetryLeaseToken = ""
	batch.ErrorMessage = ""
	result := s.db.WithContext(ctx).Model(&model.NotificationBatch{}).
		Where("id = ? AND status IN ?", batch.ID, []string{"failed", "dead_letter"}).
		Where("retry_lease_until IS NULL OR retry_lease_until <= ?", now).
		Updates(batchResultUpdates(batch))
	err := result.Error
	if err == nil && result.RowsAffected == 0 {
		err = fmt.Errorf("%w: notification batch is currently being retried", ErrValidation)
	}
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
