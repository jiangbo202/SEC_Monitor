package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"sec_monitor/internal/discovery"
	"sec_monitor/internal/model"
	"sec_monitor/internal/telegram"

	"gorm.io/gorm"
)

type CandidateNotificationService struct {
	db          *gorm.DB
	discoveryDB *gorm.DB
	notifier    telegram.Notifier
	configs     *ConfigService
}

type CandidateNotificationPreview struct {
	Enabled          bool                          `json:"enabled"`
	SuppressedReason string                        `json:"suppressed_reason"`
	Settings         CandidateNotificationSettings `json:"settings"`
	Summary          discovery.CandidateSummary    `json:"summary"`
}

type CandidateNotificationSendInput struct {
	Confirm bool `json:"confirm"`
}

type CandidateNotificationSendResult struct {
	Preview CandidateNotificationPreview `json:"preview"`
	Batch   model.NotificationBatch      `json:"batch"`
}

func NewCandidateNotificationService(db *gorm.DB, discoveryDB *gorm.DB, notifier telegram.Notifier, configs *ConfigService) *CandidateNotificationService {
	return &CandidateNotificationService{db: db, discoveryDB: discoveryDB, notifier: notifier, configs: configs}
}

func (s *CandidateNotificationService) Preview(ctx context.Context) (CandidateNotificationPreview, error) {
	if s == nil || s.discoveryDB == nil || s.configs == nil {
		return CandidateNotificationPreview{}, errors.New("candidate notification service is not configured")
	}
	settings, err := s.configs.CandidateNotificationSettings(ctx)
	if err != nil {
		return CandidateNotificationPreview{}, err
	}
	result := CandidateNotificationPreview{Enabled: settings.Enabled, Settings: settings}
	if !settings.Enabled {
		result.SuppressedReason = "candidate_notification_disabled"
		result.Summary = discovery.CandidateSummary{
			ItemsA:  []discovery.CandidateScoreSnapshot{},
			ItemsB:  []discovery.CandidateScoreSnapshot{},
			Message: "候选通知未启用；本次为 dry-run 预检，未发送 Telegram。",
		}
		return result, nil
	}
	summary, err := discovery.BuildCandidateSummaryWithOptions(ctx, s.discoveryDB, discovery.CandidateSummaryOptions{
		LimitPerGrade: settings.MaxPerGrade,
		IncludeA:      settings.NotifyA,
		IncludeB:      settings.NotifyB,
	})
	if err != nil {
		return CandidateNotificationPreview{}, err
	}
	result.Summary = summary
	if !settings.NotifyA && !settings.NotifyB {
		result.SuppressedReason = "candidate_notification_grades_disabled"
	}
	return result, nil
}

func (s *CandidateNotificationService) Send(ctx context.Context, input CandidateNotificationSendInput) (CandidateNotificationSendResult, error) {
	if !input.Confirm {
		return CandidateNotificationSendResult{}, fmt.Errorf("%w: confirm is required", ErrValidation)
	}
	if s == nil || s.db == nil || s.notifier == nil || s.configs == nil {
		return CandidateNotificationSendResult{}, errors.New("candidate notification delivery is not configured")
	}
	preview, err := s.Preview(ctx)
	if err != nil {
		return CandidateNotificationSendResult{}, err
	}
	if preview.SuppressedReason != "" {
		return CandidateNotificationSendResult{}, fmt.Errorf("%w: %s", ErrValidation, preview.SuppressedReason)
	}
	candidates := notificationCandidatesFromCandidateSummary(preview.Summary)
	if len(candidates) == 0 {
		return CandidateNotificationSendResult{}, fmt.Errorf("%w: no candidate notification items", ErrValidation)
	}
	batch, err := NewNotificationBatchService(s.db, s.notifier, s.configs).Deliver(ctx, NotificationBatchInput{
		Source: "candidate", Trigger: "manual", Candidates: candidates, SummaryText: preview.Summary.Message,
	})
	if err != nil {
		return CandidateNotificationSendResult{}, err
	}
	return CandidateNotificationSendResult{Preview: preview, Batch: batch}, nil
}

func notificationCandidatesFromCandidateSummary(summary discovery.CandidateSummary) []NotificationCandidate {
	now := time.Now().UTC()
	candidates := make([]NotificationCandidate, 0, len(summary.ItemsA)+len(summary.ItemsB))
	for _, item := range summary.ItemsA {
		candidates = append(candidates, notificationCandidateFromScore(summary.BatchID, item, "A", now))
	}
	for _, item := range summary.ItemsB {
		candidates = append(candidates, notificationCandidateFromScore(summary.BatchID, item, "B", now))
	}
	return candidates
}

func notificationCandidateFromScore(batchID string, item discovery.CandidateScoreSnapshot, grade string, now time.Time) NotificationCandidate {
	return NotificationCandidate{
		EntityKind:  "candidate",
		FilingID:    fmt.Sprintf("%s:%s:%d", batchID, item.Ticker, item.ID),
		Ticker:      item.Ticker,
		CompanyName: item.Ticker,
		FilingType:  grade,
		Title:       fmt.Sprintf("%s级候选，%d分", grade, item.TotalScore),
		Status:      item.Grade,
		Reason:      "eligible",
		EventAt:     now,
	}
}
