package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"sec_monitor/internal/discovery"
	"sec_monitor/internal/telegram"

	"gorm.io/gorm"
)

// AnalystRatingNotificationService turns already-persisted semantic consensus
// changes into the project's normal Telegram notification batches. It never
// calls Longbridge itself, so a notification retry cannot consume market-data
// quota or accidentally refresh a rating.
type AnalystRatingNotificationService struct {
	db          *gorm.DB
	discoveryDB *gorm.DB
	configs     *ConfigService
	batches     *NotificationBatchService
}

func NewAnalystRatingNotificationService(db, discoveryDB *gorm.DB, notifier telegram.Notifier, configs *ConfigService) *AnalystRatingNotificationService {
	return &AnalystRatingNotificationService{db: db, discoveryDB: discoveryDB, configs: configs, batches: NewNotificationBatchService(db, notifier, configs)}
}

func (s *AnalystRatingNotificationService) WithNotificationCenter(center *NotificationBatchService) *AnalystRatingNotificationService {
	if s != nil && center != nil {
		s.batches = center
	}
	return s
}

func (s *AnalystRatingNotificationService) DeliverPending(ctx context.Context, snapshots []discovery.AnalystRatingSnapshot) error {
	if s == nil || s.db == nil || s.discoveryDB == nil || s.batches == nil || s.configs == nil || len(snapshots) == 0 {
		return nil
	}
	enabled, _, err := s.configs.GetValue(ctx, "analyst_rating.notify_enabled")
	if err != nil {
		return err
	}
	if strings.ToLower(strings.TrimSpace(enabled)) != "true" {
		return nil
	}
	candidates := make([]NotificationCandidate, 0, len(snapshots))
	ids := make([]uint, 0, len(snapshots))
	for _, snapshot := range snapshots {
		if snapshot.ID == 0 || snapshot.ChangeSummary == "" || snapshot.NotificationStatus != "pending" {
			continue
		}
		ids = append(ids, snapshot.ID)
		candidates = append(candidates, NotificationCandidate{
			EntityKind: "analyst_rating", FilingID: fmt.Sprintf("analyst-rating:%d", snapshot.ID), Ticker: snapshot.Ticker,
			CompanyName: snapshot.Ticker, FilingType: "analyst_rating", Title: snapshot.ChangeSummary,
			Reason: "eligible", EventAt: snapshot.FetchedAt,
		})
	}
	if len(candidates) == 0 {
		return nil
	}
	batch, err := s.batches.Deliver(ctx, NotificationBatchInput{
		Source: "analyst_rating", Trigger: "scheduler", Candidates: candidates,
		SummaryText: renderAnalystRatingNotification(candidates),
	})
	if err != nil {
		return err
	}
	if batch.Status == "sent" || batch.Status == "failed" {
		updates := map[string]any{"notification_status": batch.Status}
		if batch.Status == "sent" {
			now := time.Now().UTC()
			updates["notified_at"] = &now
		}
		return s.discoveryDB.WithContext(ctx).Model(&discovery.AnalystRatingSnapshot{}).Where("id IN ?", ids).Updates(updates).Error
	}
	return nil
}

// DeliverQueued resumes notifications that were persisted before a process
// restart or transient Telegram failure. It performs no provider request.
func (s *AnalystRatingNotificationService) DeliverQueued(ctx context.Context, limit int) error {
	if s == nil || s.discoveryDB == nil {
		return nil
	}
	snapshots, err := discovery.PendingAnalystRatingNotifications(ctx, s.discoveryDB, limit)
	if err != nil {
		return err
	}
	return s.DeliverPending(ctx, snapshots)
}

func renderAnalystRatingNotification(candidates []NotificationCandidate) string {
	lines := []string{"分析师共识更新（Longbridge）："}
	for _, candidate := range candidates {
		lines = append(lines, fmt.Sprintf("- %s｜%s", candidate.Ticker, candidate.Title))
	}
	return strings.Join(lines, "\n")
}
