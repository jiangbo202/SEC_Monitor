package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"sec_monitor/internal/model"
	"sec_monitor/internal/telegram"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var notificationRetryDelays = []time.Duration{5 * time.Minute, 15 * time.Minute, 45 * time.Minute, 2 * time.Hour, 6 * time.Hour}

const (
	notificationRetryLeaseDuration      = 30 * time.Minute
	notificationDeadLetterRecoveryDelay = 24 * time.Hour
	notificationDeadLetterRecoveryLimit = 50
)

type NotificationCandidate struct {
	EntityKind  string
	FilingID    string
	TargetID    uint
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

// NotificationMessageInput represents a system-level event (health report,
// connection test, etc.) that has no natural filing/candidate collection but
// must still use the same durable Telegram queue and delivery log.
type NotificationMessageInput struct {
	Source      string
	Trigger     string
	EventKey    string
	EntityKind  string
	Title       string
	SummaryText string
	EventAt     time.Time
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

// NotificationDeadLetterRecoveryResult reports only batches that were safely
// returned to the durable retry queue. Permanent delivery errors deliberately
// remain visible for operator action.
type NotificationDeadLetterRecoveryResult struct {
	Requeued int `json:"requeued"`
}

// NotificationBulkRequeueResult makes a delivery incident recoverable without
// asking an operator to click through every failed batch. It only moves the
// selected durable batches back to the normal retry ladder; it never sends a
// message synchronously and therefore preserves the existing de-duplication
// and lease guarantees.
type NotificationBulkRequeueResult struct {
	Requeued int `json:"requeued"`
	Skipped  int `json:"skipped"`
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
	batch := model.NotificationBatch{
		SyncRunID: input.SyncRunID,
		Source:    strings.TrimSpace(input.Source),
		Trigger:   strings.TrimSpace(input.Trigger),
		Channel:   "telegram",
		Status:    "pending",
		CreatedAt: now,
		UpdatedAt: now,
	}
	accepted := make([]NotificationCandidate, 0, len(input.Candidates))
	if err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&batch).Error; err != nil {
			return err
		}
		for _, candidate := range input.Candidates {
			status := "suppressed"
			if candidate.Reason == "eligible" {
				status = "pending"
			}
			eventKey := notificationBatchEventKey(batch.Source, candidate)
			item := model.NotificationBatchItem{
				BatchID: batch.ID, TargetID: candidate.TargetID, EntityKind: candidate.EntityKind, FilingID: candidate.FilingID,
				EventKey: &eventKey,
				Ticker:   candidate.Ticker, CIK: candidate.CIK, CompanyName: candidate.CompanyName,
				FilingType: candidate.FilingType, Title: candidate.Title, FilingURL: candidate.FilingURL,
				EventAt: candidate.EventAt, Status: status, Reason: candidate.Reason, CreatedAt: now, UpdatedAt: now,
			}
			result := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&item)
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected == 1 {
				accepted = append(accepted, candidate)
			}
		}
		if len(accepted) == 0 {
			return tx.Delete(&batch).Error
		}
		return nil
	}); err != nil {
		return model.NotificationBatch{}, err
	}
	if len(accepted) == 0 {
		return model.NotificationBatch{}, nil
	}
	eligible := make([]NotificationCandidate, 0, len(accepted))
	reasonCounts := map[string]int{}
	for _, candidate := range accepted {
		if candidate.Reason == "eligible" {
			eligible = append(eligible, candidate)
		} else {
			reasonCounts[candidate.Reason]++
		}
	}
	summaryText := strings.TrimSpace(input.SummaryText)
	// A custom summary may contain identifiers from duplicate candidates. Once
	// anything was filtered, rebuild it from the accepted immutable events.
	if summaryText == "" || len(accepted) != len(input.Candidates) {
		summaryText = renderNotificationBatchSummary(batch.Source, eligible)
	}
	batch.ItemCount = len(accepted)
	batch.SuppressedCount = len(accepted) - len(eligible)
	batch.SuppressionSummary = formatReasonCounts(reasonCounts)
	batch.MessageText = truncateTelegramMessage(summaryText, 4000)
	if len(eligible) == 0 {
		batch.Status = "suppressed"
	}
	if err := s.db.WithContext(ctx).Model(&model.NotificationBatch{}).Where("id = ?", batch.ID).Updates(map[string]any{
		"status": batch.Status, "item_count": batch.ItemCount, "suppressed_count": batch.SuppressedCount,
		"suppression_summary": batch.SuppressionSummary, "message_text": batch.MessageText, "updated_at": now,
	}).Error; err != nil {
		return model.NotificationBatch{}, err
	}
	if len(eligible) == 0 {
		return batch, nil
	}
	channelEnabled, err := s.telegramEventEnabled(ctx, batch.Source)
	if err != nil {
		return model.NotificationBatch{}, err
	}
	if !channelEnabled {
		return s.finishSuppressed(ctx, batch, eligible, "event_channel_disabled")
	}

	cfg, err := s.configs.Telegram(ctx)
	if err != nil {
		return model.NotificationBatch{}, err
	}
	batch.Target = cfg.ChatID
	if !cfg.Enabled || cfg.BotToken == "" || cfg.ChatID == "" {
		return s.finishSuppressed(ctx, batch, eligible, "notification_disabled")
	}

	message := telegram.Message{Text: batch.MessageText}
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

// notificationBatchEventKey is the global de-duplication boundary of the
// Telegram queue. It intentionally includes the source: one SEC filing may
// legitimately be reported by distinct product workflows, while retries and
// repeated scans of the same workflow must reuse the existing delivery item.
func notificationBatchEventKey(source string, candidate NotificationCandidate) string {
	identity := strings.TrimSpace(candidate.FilingID)
	if identity == "" {
		identity = fmt.Sprintf("%d:%s:%s:%s", candidate.TargetID, strings.TrimSpace(candidate.Ticker), strings.TrimSpace(candidate.CIK), candidate.EventAt.UTC().Format(time.RFC3339Nano))
	}
	// TargetID is part of the identity: one filing may be intentionally
	// associated with several monitored targets (for example, a fund issuer),
	// and each association must remain visible in its target-specific audit.
	raw := strings.Join([]string{strings.TrimSpace(source), strings.TrimSpace(candidate.EntityKind), strconv.FormatUint(uint64(candidate.TargetID), 10), identity}, "\x1f")
	sum := sha256.Sum256([]byte(raw))
	return fmt.Sprintf("tg:%x", sum[:])
}

// telegramEventEnabled is the channel-level control for the research events
// exposed in System Config. Sources not in the mapping deliberately remain
// enabled so existing operations, IPO and candidate-summary notifications keep
// their current behavior.
func (s *NotificationBatchService) telegramEventEnabled(ctx context.Context, source string) (bool, error) {
	if s == nil || s.configs == nil {
		return true, nil
	}
	key := ""
	legacyKey := ""
	switch strings.TrimSpace(source) {
	case "earnings_preview_watch_target":
		key, legacyKey = "telegram_notification.watch_target_earnings_preview_enabled", "telegram_notification.earnings_preview_enabled"
	case "earnings_preview_candidate":
		key, legacyKey = "telegram_notification.candidate_earnings_preview_enabled", "telegram_notification.earnings_preview_enabled"
	case "earnings_release_watch_target":
		key, legacyKey = "telegram_notification.watch_target_earnings_release_enabled", "telegram_notification.earnings_release_enabled"
	case "earnings_release_candidate":
		key, legacyKey = "telegram_notification.candidate_earnings_release_enabled", "telegram_notification.earnings_release_enabled"
	case "technical_signal_watch_target":
		key, legacyKey = "telegram_notification.watch_target_technical_signal_enabled", "telegram_notification.technical_signal_enabled"
	case "technical_signal_candidate":
		key, legacyKey = "telegram_notification.candidate_technical_signal_enabled", "telegram_notification.technical_signal_enabled"
	case "major_event_watch_target":
		key, legacyKey = "telegram_notification.watch_target_major_event_enabled", "telegram_notification.major_event_enabled"
	case "insider_trading_watch_target":
		key, legacyKey = "telegram_notification.watch_target_insider_trading_enabled", "telegram_notification.insider_trading_enabled"
	case "ipo_progress":
		key = "telegram_notification.ipo_progress_enabled"
	case "earnings_preview":
		key = "telegram_notification.earnings_preview_enabled"
	case "earnings_release":
		key = "telegram_notification.earnings_release_enabled"
	case "trade_setup", "technical_signal":
		key = "telegram_notification.technical_signal_enabled"
	case "major_event":
		key = "telegram_notification.major_event_enabled"
	case "insider_trading":
		key = "telegram_notification.insider_trading_enabled"
	}
	if key == "" {
		return true, nil
	}
	raw, configured, err := s.configs.GetValue(ctx, key)
	if err != nil {
		return true, err
	}
	if !configured && legacyKey != "" {
		raw, configured, err = s.configs.GetValue(ctx, legacyKey)
		if err != nil {
			return true, err
		}
	}
	if !configured {
		return true, nil
	}
	enabled, err := strconv.ParseBool(raw)
	if err != nil {
		return true, nil
	}
	return enabled, nil
}

// DeliverMessage turns a system event into a one-item notification batch.
// Business modules should use this instead of calling Telegram directly.
func (s *NotificationBatchService) DeliverMessage(ctx context.Context, input NotificationMessageInput) (model.NotificationBatch, error) {
	source, eventKey, text := strings.TrimSpace(input.Source), strings.TrimSpace(input.EventKey), strings.TrimSpace(input.SummaryText)
	if source == "" || eventKey == "" || text == "" {
		return model.NotificationBatch{}, fmt.Errorf("%w: notification source, event key, and message are required", ErrValidation)
	}
	eventAt := input.EventAt.UTC()
	if eventAt.IsZero() {
		eventAt = time.Now().UTC()
	}
	entityKind := strings.TrimSpace(input.EntityKind)
	if entityKind == "" {
		entityKind = "system_event"
	}
	return s.Deliver(ctx, NotificationBatchInput{
		Source: source, Trigger: strings.TrimSpace(input.Trigger), SummaryText: text,
		Candidates: []NotificationCandidate{{
			EntityKind: entityKind, FilingID: eventKey, CompanyName: source, FilingType: entityKind,
			Title: strings.TrimSpace(input.Title), Reason: "eligible", EventAt: eventAt,
		}},
	})
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
		"message_text":  batch.MessageText,
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

// RecoverTransientDeadLetters gives temporary provider failures one delayed,
// automated recovery cycle. The normal retry ladder has already exhausted
// five attempts by the time a batch becomes a dead letter; only errors that
// look transient (timeouts, rate limits, upstream 5xx) are requeued after a
// full day. Authentication, configuration, and payload failures stay in the
// visible dead-letter queue instead of being retried indefinitely.
func (s *NotificationBatchService) RecoverTransientDeadLetters(ctx context.Context, now time.Time) (NotificationDeadLetterRecoveryResult, error) {
	now = now.UTC()
	cutoff := now.Add(-notificationDeadLetterRecoveryDelay)
	var batches []model.NotificationBatch
	if err := s.db.WithContext(ctx).
		Where("status = ? AND updated_at <= ?", "dead_letter", cutoff).
		Order("updated_at ASC, id ASC").
		Limit(notificationDeadLetterRecoveryLimit).
		Find(&batches).Error; err != nil {
		return NotificationDeadLetterRecoveryResult{}, sanitizeNotificationBatchError(err)
	}
	result := NotificationDeadLetterRecoveryResult{}
	for _, batch := range batches {
		if !isTransientNotificationDeliveryError(batch.ErrorMessage) {
			continue
		}
		if _, err := s.Requeue(ctx, batch.ID, now); err != nil {
			return result, sanitizeNotificationBatchError(err)
		}
		result.Requeued++
	}
	return result, nil
}

func isTransientNotificationDeliveryError(message string) bool {
	message = strings.ToLower(strings.TrimSpace(message))
	if message == "" {
		return false
	}
	for _, token := range []string{
		"timeout", "deadline exceeded", "context canceled", "connection reset",
		"temporarily unavailable", "temporary failure", "rate limit", "too many requests",
		" 429", "status: 5", "status 5", "bad gateway", "service unavailable", "internal server error",
	} {
		if strings.Contains(message, token) {
			return true
		}
	}
	return false
}

func (s *NotificationBatchService) retryBatch(ctx context.Context, batchID uint, now time.Time) (string, bool, error) {
	batch, items, leaseToken, claimed, err := s.claimDueBatch(ctx, batchID, now)
	if err != nil || !claimed {
		return "", false, err
	}
	candidates := notificationCandidatesFromBatchItems(items)
	messageText := strings.TrimSpace(batch.MessageText)
	if messageText == "" {
		messageText = renderNotificationBatchSummary(batch.Source, candidates)
	}
	message := telegram.Message{Text: truncateTelegramMessage(messageText, 4000)}
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
			EntityKind: item.EntityKind, FilingID: item.FilingID, TargetID: item.TargetID, Ticker: item.Ticker, CIK: item.CIK,
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

// RequeueFailed returns a bounded set of failed/dead-letter batches to the
// normal durable retry queue. A limit is mandatory so a configuration error
// cannot create an unbounded burst after an operator action.
func (s *NotificationBatchService) RequeueFailed(ctx context.Context, now time.Time, limit int) (NotificationBulkRequeueResult, error) {
	if limit < 1 || limit > 500 {
		return NotificationBulkRequeueResult{}, fmt.Errorf("%w: notification requeue limit must be between 1 and 500", ErrValidation)
	}
	now = now.UTC()
	var ids []uint
	if err := s.db.WithContext(ctx).Model(&model.NotificationBatch{}).
		Where("status IN ?", []string{"failed", "dead_letter"}).
		Where("retry_lease_until IS NULL OR retry_lease_until <= ?", now).
		Order("updated_at ASC, id ASC").Limit(limit).Pluck("id", &ids).Error; err != nil {
		return NotificationBulkRequeueResult{}, sanitizeNotificationBatchError(err)
	}
	result := NotificationBulkRequeueResult{}
	for _, id := range ids {
		if _, err := s.Requeue(ctx, id, now); err != nil {
			if errors.Is(err, ErrValidation) {
				result.Skipped++
				continue
			}
			return result, err
		}
		result.Requeued++
	}
	return result, nil
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
	switch source {
	case "earnings_preview", "earnings_preview_watch_target", "earnings_preview_candidate":
		title = "财报预告"
	case "earnings_release", "earnings_release_watch_target", "earnings_release_candidate":
		title = "财报已发布"
	case "trade_setup", "technical_signal", "technical_signal_watch_target", "technical_signal_candidate":
		title = "技术信号变化"
	case "major_event", "major_event_watch_target":
		title = "重大事件"
	case "insider_trading", "insider_trading_watch_target":
		title = "内幕交易"
	case "ipo_progress":
		title = "关注 IPO 进展"
	case "ipo":
		title = "IPO 监控"
	case "ipo_offering":
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
		items[i].MessageText = truncateTelegramMessage(items[i].MessageText, 4000)
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
