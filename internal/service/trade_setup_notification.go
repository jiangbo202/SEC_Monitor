package service

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"sec_monitor/internal/discovery"
	"sec_monitor/internal/model"
	"sec_monitor/internal/telegram"

	"gorm.io/gorm"
)

const tradeSetupNotificationSource = "trade_setup"

type TradeSetupNotificationEvent struct {
	TargetID       uint    `json:"target_id"`
	Ticker         string  `json:"ticker"`
	CompanyName    string  `json:"company_name"`
	PreviousStatus string  `json:"previous_status"`
	Status         string  `json:"status"`
	TradeDate      string  `json:"trade_date"`
	EntryTrigger   string  `json:"entry_trigger"`
	StopLossUSD    float64 `json:"stop_loss_usd"`
	RiskPct        float64 `json:"risk_pct"`
	ExitReason     string  `json:"exit_reason"`
	Reason         string  `json:"reason"`
}

type TradeSetupNotificationPreview struct {
	Enabled          bool                           `json:"enabled"`
	SuppressedReason string                         `json:"suppressed_reason"`
	Settings         TradeSetupNotificationSettings `json:"settings"`
	Events           []TradeSetupNotificationEvent  `json:"events"`
	ObservedCount    int                            `json:"observed_count"`
	EligibleCount    int                            `json:"eligible_count"`
}

type TradeSetupNotificationSendInput struct {
	Confirm bool `json:"confirm"`
}

type TradeSetupNotificationSendResult struct {
	Preview TradeSetupNotificationPreview `json:"preview"`
	Batch   model.NotificationBatch       `json:"batch"`
}

type tradeSetupObservation struct {
	TargetID  uint
	Ticker    string
	Status    string
	TradeDate string
}

// TradeSetupNotificationService delivers transition-based daily trade-plan
// alerts for enabled monitoring targets. It never interprets a plan as an
// order instruction and defaults to disabled.
type TradeSetupNotificationService struct {
	db          *gorm.DB
	discoveryDB *gorm.DB
	notifier    telegram.Notifier
	configs     *ConfigService
}

func NewTradeSetupNotificationService(db, discoveryDB *gorm.DB, notifier telegram.Notifier, configs *ConfigService) *TradeSetupNotificationService {
	return &TradeSetupNotificationService{db: db, discoveryDB: discoveryDB, notifier: notifier, configs: configs}
}

func (s *TradeSetupNotificationService) Preview(ctx context.Context) (TradeSetupNotificationPreview, []tradeSetupObservation, error) {
	if s == nil || s.db == nil || s.discoveryDB == nil || s.configs == nil {
		return TradeSetupNotificationPreview{}, nil, errors.New("trade setup notification service is not configured")
	}
	settings, err := s.configs.TradeSetupNotificationSettings(ctx)
	if err != nil {
		return TradeSetupNotificationPreview{}, nil, err
	}
	result := TradeSetupNotificationPreview{Enabled: settings.Enabled, Settings: settings, Events: []TradeSetupNotificationEvent{}}
	if !settings.Enabled {
		result.SuppressedReason = "trade_setup_notification_disabled"
		return result, nil, nil
	}
	if settings.ShadowMode {
		result.SuppressedReason = "trade_setup_notification_shadow_mode"
	}
	var targets []model.WatchTarget
	if err := s.db.WithContext(ctx).Where("status = ?", "enabled").Order("ticker ASC").Find(&targets).Error; err != nil {
		return TradeSetupNotificationPreview{}, nil, err
	}
	states := map[uint]model.TradeSetupNotificationState{}
	if len(targets) > 0 {
		ids := make([]uint, 0, len(targets))
		for _, target := range targets {
			ids = append(ids, target.ID)
		}
		var stored []model.TradeSetupNotificationState
		if err := s.db.WithContext(ctx).Where("target_id IN ?", ids).Find(&stored).Error; err != nil {
			return TradeSetupNotificationPreview{}, nil, err
		}
		for _, state := range stored {
			states[state.TargetID] = state
		}
	}
	observations := make([]tradeSetupObservation, 0, len(targets))
	for _, target := range targets {
		history, err := discovery.GetTickerTechnicalHistory(ctx, s.discoveryDB, target.Ticker)
		if err != nil {
			return TradeSetupNotificationPreview{}, nil, err
		}
		setup := history.Technical.TradeSetup
		if setup.Status == discovery.TradeSetupUnavailable || setup.Status == "" {
			continue
		}
		observations = append(observations, tradeSetupObservation{TargetID: target.ID, Ticker: target.Ticker, Status: setup.Status, TradeDate: history.Technical.TradeDate})
		result.ObservedCount++
		if !tradeSetupStatusActionable(setup.Status) {
			continue
		}
		previous, found := states[target.ID]
		event := TradeSetupNotificationEvent{
			TargetID: target.ID, Ticker: target.Ticker, CompanyName: target.CompanyName,
			Status: setup.Status, TradeDate: history.Technical.TradeDate, EntryTrigger: setup.EntryTrigger,
			StopLossUSD: setup.StopLossUSD, RiskPct: setup.RiskPct, ExitReason: setup.ExitReason,
		}
		if found {
			event.PreviousStatus = previous.Status
			if previous.Status == setup.Status {
				event.Reason = "unchanged"
			} else {
				event.Reason = tradeSetupNotificationReason(settings, setup.Status)
			}
		} else if setup.Status == discovery.TradeSetupEntryCandidate {
			event.Reason = tradeSetupNotificationReason(settings, setup.Status)
		} else {
			event.Reason = "initial_state_baseline"
		}
		if event.Reason == "eligible" {
			result.EligibleCount++
		}
		result.Events = append(result.Events, event)
	}
	sort.Slice(result.Events, func(i, j int) bool {
		left, right := tradeSetupEventPriority(result.Events[i].Status), tradeSetupEventPriority(result.Events[j].Status)
		if left != right {
			return left < right
		}
		return result.Events[i].Ticker < result.Events[j].Ticker
	})
	if result.EligibleCount > settings.MaxPerRun {
		remaining := settings.MaxPerRun
		for index := range result.Events {
			if result.Events[index].Reason != "eligible" {
				continue
			}
			if remaining > 0 {
				remaining--
				continue
			}
			result.Events[index].Reason = "max_per_run"
			result.EligibleCount--
		}
	}
	return result, observations, nil
}

func (s *TradeSetupNotificationService) Send(ctx context.Context, input TradeSetupNotificationSendInput) (TradeSetupNotificationSendResult, error) {
	if !input.Confirm {
		return TradeSetupNotificationSendResult{}, fmt.Errorf("%w: confirm is required", ErrValidation)
	}
	if s == nil || s.notifier == nil {
		return TradeSetupNotificationSendResult{}, errors.New("trade setup notification delivery is not configured")
	}
	preview, observations, err := s.Preview(ctx)
	if err != nil {
		return TradeSetupNotificationSendResult{}, err
	}
	if preview.SuppressedReason == "trade_setup_notification_shadow_mode" {
		return TradeSetupNotificationSendResult{Preview: preview}, nil
	}
	if preview.SuppressedReason != "" {
		return TradeSetupNotificationSendResult{}, fmt.Errorf("%w: %s", ErrValidation, preview.SuppressedReason)
	}
	candidates := notificationCandidatesFromTradeSetupEvents(preview.Events)
	if len(candidates) == 0 {
		if err := s.persistObservations(ctx, observations); err != nil {
			return TradeSetupNotificationSendResult{}, err
		}
		return TradeSetupNotificationSendResult{Preview: preview}, nil
	}
	batch, err := NewNotificationBatchService(s.db, s.notifier, s.configs).Deliver(ctx, NotificationBatchInput{
		Source: tradeSetupNotificationSource, Trigger: "manual", Candidates: candidates, SummaryText: renderTradeSetupNotificationMessage(preview.Events),
	})
	if err != nil {
		return TradeSetupNotificationSendResult{}, err
	}
	if batch.Status == "sent" {
		if err := s.persistObservations(ctx, observations); err != nil {
			return TradeSetupNotificationSendResult{}, err
		}
	}
	return TradeSetupNotificationSendResult{Preview: preview, Batch: batch}, nil
}

func (s *TradeSetupNotificationService) persistObservations(ctx context.Context, observations []tradeSetupObservation) error {
	if len(observations) == 0 {
		return nil
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, observation := range observations {
			state := model.TradeSetupNotificationState{TargetID: observation.TargetID, Ticker: observation.Ticker, Status: observation.Status, TradeDate: observation.TradeDate, UpdatedAt: time.Now().UTC()}
			if err := tx.Where("target_id = ?", observation.TargetID).Assign(state).FirstOrCreate(&state).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func tradeSetupNotificationReason(settings TradeSetupNotificationSettings, status string) string {
	switch status {
	case discovery.TradeSetupEntryCandidate:
		if settings.NotifyEntry {
			return "eligible"
		}
	case discovery.TradeSetupExitWarning:
		if settings.NotifyExit {
			return "eligible"
		}
	case discovery.TradeSetupInvalidated:
		if settings.NotifyInvalidated {
			return "eligible"
		}
	}
	return "status_disabled"
}

func tradeSetupStatusActionable(status string) bool {
	return status == discovery.TradeSetupEntryCandidate || status == discovery.TradeSetupExitWarning || status == discovery.TradeSetupInvalidated
}

func tradeSetupEventPriority(status string) int {
	switch status {
	case discovery.TradeSetupInvalidated:
		return 0
	case discovery.TradeSetupExitWarning:
		return 1
	default:
		return 2
	}
}

func notificationCandidatesFromTradeSetupEvents(events []TradeSetupNotificationEvent) []NotificationCandidate {
	result := make([]NotificationCandidate, 0, len(events))
	for _, event := range events {
		if event.Reason != "eligible" {
			continue
		}
		title := tradeSetupStatusLabel(event.Status)
		if event.EntryTrigger != "" {
			title += "｜" + event.EntryTrigger
		} else if event.ExitReason != "" {
			title += "｜" + event.ExitReason
		}
		result = append(result, NotificationCandidate{
			TargetID: event.TargetID, EntityKind: "trade_setup", FilingID: fmt.Sprintf("trade_setup:%d:%s:%s", event.TargetID, event.Status, event.TradeDate),
			Ticker: event.Ticker, CompanyName: event.CompanyName, FilingType: event.Status, Title: title, Status: event.Status,
			Reason: "eligible", EventAt: time.Now().UTC(),
		})
	}
	return result
}

func renderTradeSetupNotificationMessage(events []TradeSetupNotificationEvent) string {
	lines := []string{"交易计划状态变更（日线收盘价规则）："}
	for _, event := range events {
		if event.Reason != "eligible" {
			continue
		}
		line := fmt.Sprintf("- %s｜%s", event.Ticker, tradeSetupStatusLabel(event.Status))
		if event.EntryTrigger != "" {
			line += "｜" + event.EntryTrigger
		}
		if event.StopLossUSD > 0 {
			line += fmt.Sprintf("｜止损 $%.2f（风险 %.1f%%）", event.StopLossUSD, event.RiskPct)
		}
		if event.ExitReason != "" {
			line += "｜" + event.ExitReason
		}
		lines = append(lines, line)
	}
	lines = append(lines, "仅供研究，不构成自动下单指令。")
	return strings.Join(lines, "\n")
}

func tradeSetupStatusLabel(status string) string {
	switch status {
	case discovery.TradeSetupEntryCandidate:
		return "入场候选"
	case discovery.TradeSetupExitWarning:
		return "离场预警"
	case discovery.TradeSetupInvalidated:
		return "趋势失效"
	default:
		return status
	}
}
