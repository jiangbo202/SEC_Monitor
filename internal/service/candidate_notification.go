package service

import (
	"context"
	"errors"

	"sec_monitor/internal/discovery"

	"gorm.io/gorm"
)

type CandidateNotificationService struct {
	discoveryDB *gorm.DB
	configs     *ConfigService
}

type CandidateNotificationPreview struct {
	Enabled          bool                          `json:"enabled"`
	SuppressedReason string                        `json:"suppressed_reason"`
	Settings         CandidateNotificationSettings `json:"settings"`
	Summary          discovery.CandidateSummary    `json:"summary"`
}

func NewCandidateNotificationService(discoveryDB *gorm.DB, configs *ConfigService) *CandidateNotificationService {
	return &CandidateNotificationService{discoveryDB: discoveryDB, configs: configs}
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
