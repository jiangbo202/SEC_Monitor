package service

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"sec_monitor/internal/model"

	lbcalendar "github.com/longbridge/openapi-go/calendar"
	lbconfig "github.com/longbridge/openapi-go/config"
	"gorm.io/gorm/clause"
)

const macroProviderLongbridge = "longbridge"

// syncLongbridgeMacroCalendar stores three-star US events as independently
// labelled market-calendar rows. It never modifies an agency's release or
// replaces an official actual value.
func (s *MacroCalendarService) syncLongbridgeMacroCalendar(ctx context.Context, result *MacroCalendarSyncResult) error {
	if s == nil || s.configs == nil {
		return nil
	}
	cfg, err := s.configs.ApplyDiscoveryConfig(ctx, s.runtime)
	if err != nil {
		return err
	}
	if strings.TrimSpace(cfg.LongbridgeAppKey) == "" || strings.TrimSpace(cfg.LongbridgeAppSecret) == "" || strings.TrimSpace(cfg.LongbridgeAccessToken) == "" {
		return nil
	}
	clientCfg, err := lbconfig.New(lbconfig.WithConfigKey(cfg.LongbridgeAppKey, cfg.LongbridgeAppSecret, cfg.LongbridgeAccessToken))
	if err != nil {
		return err
	}
	calendar, err := lbcalendar.NewFromCfg(clientCfg)
	if err != nil {
		return err
	}
	now := s.now().UTC()
	response, err := calendar.FinanceCalendar(ctx, lbcalendar.CalendarCategoryMacroData, now.AddDate(0, 0, -7).Format(time.DateOnly), now.AddDate(0, 0, 35).Format(time.DateOnly), stringPointer("US"))
	if err != nil {
		return err
	}
	for _, group := range response.List {
		for _, event := range group.Infos {
			if event.Star < 3 {
				continue
			}
			scheduledAt, ok := longbridgeCalendarTime(event.Datetime, group.Date, event.Date)
			if !ok {
				continue
			}
			key := strings.TrimSpace(event.ID)
			if key == "" {
				key = fmt.Sprintf("%s|%s|%s", event.Content, event.Market, scheduledAt.Format(time.RFC3339))
			}
			previous, forecast, actual := longbridgeMacroValues(event.DataKV)
			status := MacroReleaseScheduled
			var publishedAt *time.Time
			if actual != nil {
				status = MacroReleasePublished
				value := scheduledAt
				publishedAt = &value
			}
			sourceURL := "https://open.longbridge.com/docs/market/calendar/macro-calendar#" + key
			row := model.MacroRelease{Provider: macroProviderLongbridge, Category: "market_calendar", CanonicalEventKey: canonicalMacroEventKey("market_calendar", event.Content, &scheduledAt), Title: strings.TrimSpace(event.Content), Status: status, ScheduledAt: &scheduledAt, PublishedAt: publishedAt, SourceURL: sourceURL, MarketImportance: int(event.Star), FetchedAt: now}
			if err := s.db.WithContext(ctx).Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "provider"}, {Name: "source_url"}}, DoUpdates: clause.AssignmentColumns([]string{"canonical_event_key", "title", "status", "scheduled_at", "published_at", "market_importance", "fetched_at", "last_error", "updated_at"})}).Create(&row).Error; err != nil {
				return err
			}
			var saved model.MacroRelease
			if err := s.db.WithContext(ctx).Where("provider = ? AND source_url = ?", macroProviderLongbridge, sourceURL).First(&saved).Error; err != nil {
				return err
			}
			observation := model.MacroObservation{ReleaseID: saved.ID, IndicatorCode: "longbridge_" + key, IndicatorName: strings.TrimSpace(event.Content), Frequency: "event", PreviousValue: previous, ForecastValue: forecast, ActualValue: actual, SourceField: "Longbridge Macro Calendar", SourceURL: sourceURL, ProviderUpdatedAt: publishedAt, FetchedAt: now}
			if err := s.db.WithContext(ctx).Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "release_id"}, {Name: "indicator_code"}}, DoUpdates: clause.AssignmentColumns([]string{"indicator_name", "frequency", "actual_value", "previous_value", "forecast_value", "source_field", "source_url", "provider_updated_at", "fetched_at", "updated_at"})}).Create(&observation).Error; err != nil {
				return err
			}
			result.ScheduledFound++
			result.ReleasesSaved++
			result.Observations++
			if status == MacroReleasePublished {
				result.Published++
			}
		}
	}
	return nil
}

func stringPointer(value string) *string { return &value }

func longbridgeCalendarTime(timestamp, groupDate, eventDate string) (time.Time, bool) {
	if unix, err := strconv.ParseInt(strings.TrimSpace(timestamp), 10, 64); err == nil && unix > 0 {
		return time.Unix(unix, 0).UTC(), true
	}
	for _, value := range []string{eventDate, groupDate} {
		if parsed, err := time.Parse(time.DateOnly, strings.TrimSpace(value)); err == nil {
			return parsed.UTC(), true
		}
	}
	return time.Time{}, false
}

func longbridgeMacroValues(values []lbcalendar.CalendarDataKv) (previous, forecast, actual *float64) {
	for _, value := range values {
		parsed := longbridgeMacroNumber(value)
		if parsed == nil {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(value.ValueType)) {
		case "previous":
			previous = parsed
		case "estimate", "forecast":
			forecast = parsed
		case "actual":
			actual = parsed
		}
	}
	return previous, forecast, actual
}

func longbridgeMacroNumber(value lbcalendar.CalendarDataKv) *float64 {
	if value.ValueRaw != nil {
		if parsed, err := strconv.ParseFloat(value.ValueRaw.String(), 64); err == nil {
			return &parsed
		}
	}
	text := strings.TrimSpace(strings.TrimSuffix(strings.ReplaceAll(value.Value, ",", ""), "%"))
	if parsed, err := strconv.ParseFloat(text, 64); err == nil {
		return &parsed
	}
	return nil
}
