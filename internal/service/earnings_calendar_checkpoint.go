package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	lbcalendar "github.com/longbridge/openapi-go/calendar"
	"gorm.io/gorm"
	"sec_monitor/internal/model"
	"strings"
	"time"
)

func (s *EarningsPreviewService) loadResumableEarningsEvents(ctx context.Context, client longbridgeEarningsClient, now time.Time, settings EarningsPreviewSettings, scope string) ([]earningsCalendarEvent, bool, error) {
	start, end := now.Format(time.DateOnly), now.AddDate(0, 0, settings.LookaheadDays).Format(time.DateOnly)
	var checkpoint model.EarningsCalendarCheckpoint
	err := s.db.WithContext(ctx).First(&checkpoint, "scope = ?", scope).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, false, err
	}
	var events []earningsCalendarEvent
	// Keep incomplete windows across restarts. A normal completed run starts
	// a fresh scan; compensation can reuse its complete calendar cache.
	resume := err == nil && checkpoint.WindowStart == start && checkpoint.WindowEnd == end && (!checkpoint.Complete || IsTaskRetry(ctx))
	if resume {
		if err := json.Unmarshal([]byte(checkpoint.EventsJSON), &events); err != nil {
			return nil, false, fmt.Errorf("decode earnings calendar checkpoint: %w", err)
		}
		if checkpoint.Complete {
			return events, true, nil
		}
		start = checkpoint.NextDate
	} else {
		checkpoint = model.EarningsCalendarCheckpoint{Scope: scope, WindowStart: start, WindowEnd: end, NextDate: start}
	}
	market := "US"
	for page := 0; page < settings.MaxCalendarPages; page++ {
		response, err := client.FinanceCalendar(ctx, lbcalendar.CalendarCategoryReport, start, end, &market)
		if err != nil {
			return events, false, err
		}
		if response == nil {
			return events, false, errors.New("empty earnings calendar response")
		}
		next := strings.TrimSpace(response.NextDate)
		if next != "" && next <= start {
			return events, false, errors.New("earnings calendar cursor did not advance; coverage remains incomplete")
		}
		for _, group := range response.List {
			for _, item := range group.Infos {
				ticker := normalizeEarningsTicker(item.Symbol)
				date, valid := parseLongbridgeEventDate(item.Date, item.Datetime, group.Date)
				if ticker != "" && valid {
					events = append(events, earningsCalendarEvent{ID: strings.TrimSpace(item.ID), Ticker: ticker, ReportAt: date, Session: firstNonEmpty(item.FinancialMarketTime, item.DateType), Content: item.Content, Currency: item.Currency})
				}
			}
		}
		checkpoint.NextDate, checkpoint.Complete = next, next == "" || next > end
		data, err := json.Marshal(events)
		if err != nil {
			return events, false, err
		}
		checkpoint.EventsJSON = string(data)
		if err := s.db.WithContext(ctx).Save(&checkpoint).Error; err != nil {
			return events, false, err
		}
		if checkpoint.Complete {
			return events, true, nil
		}
		start = next
	}
	return events, false, nil
}
