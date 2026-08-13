package service

import (
	"context"
	"sort"
	"strings"
	"time"
	"unicode"

	"sec_monitor/internal/model"
)

// canonicalMacroEventKey groups one release by event family and US calendar
// date. It is intentionally conservative: unknown Longbridge titles remain
// separate rather than risking a misleading association with official data.
func canonicalMacroEventKey(category, title string, scheduledAt *time.Time) string {
	family := canonicalMacroEventFamily(category, title)
	date := "undated"
	if scheduledAt != nil && !scheduledAt.IsZero() {
		date = scheduledAt.UTC().Format(time.DateOnly)
	}
	return family + ":" + date
}

func canonicalMacroEventFamily(category, title string) string {
	text := strings.ToLower(strings.TrimSpace(title))
	compact := strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return r
		}
		return ' '
	}, text)
	contains := func(values ...string) bool {
		for _, value := range values {
			if strings.Contains(compact, value) {
				return true
			}
		}
		return false
	}
	switch {
	case contains("consumer price index", "cpi", "消费者价格", "消费物价"):
		return "cpi"
	case contains("producer price index", "ppi", "生产者价格"):
		return "ppi"
	case contains("employment situation", "nonfarm", "non farm", "payroll", "就业"):
		return "employment"
	// GDP releases can mention personal income in their long official title.
	// Prefer the principal GDP family before the PCE/income keyword match.
	case contains("gross domestic product", "gdp", "国内生产总值"):
		return "gdp"
	case contains("personal income", "personal outlays", "pce", "个人收入", "个人支出"):
		return "personal_income_outlays"
	case contains("fomc", "federal reserve", "联邦公开市场", "美联储利率"):
		return "fomc"
	case contains("retail sales", "零售销售"):
		return "retail_sales"
	case contains("jobless claims", "initial claims", "初请失业"):
		return "initial_claims"
	case contains("jolts", "job openings", "职位空缺"):
		return "jolts"
	case contains("durable goods", "耐用品"):
		return "durable_goods"
	case contains("housing starts", "building permits", "新屋开工", "营建许可"):
		return "housing_starts"
	case contains("new home sales", "新屋销售"):
		return "new_home_sales"
	case contains("international trade", "trade balance", "国际贸易", "贸易帐"):
		return "international_trade"
	case contains("petroleum", "crude oil inventories", "原油库存", "石油库存"):
		return "petroleum_inventories"
	}
	category = strings.TrimSpace(category)
	if category != "" && category != "market_calendar" {
		return category
	}
	return "market_" + compactMacroTitle(compact)
}

func compactMacroTitle(value string) string {
	parts := strings.Fields(value)
	if len(parts) == 0 {
		return "event"
	}
	if len(parts) > 8 {
		parts = parts[:8]
	}
	return strings.Join(parts, "_")
}

func (s *MacroCalendarService) attachMacroReleaseSources(ctx context.Context, items []MacroReleaseItem) error {
	if len(items) == 0 {
		return nil
	}
	var earliest, latest time.Time
	for _, item := range items {
		if item.ScheduledAt == nil || item.ScheduledAt.IsZero() {
			continue
		}
		at := item.ScheduledAt.UTC()
		if earliest.IsZero() || at.Before(earliest) {
			earliest = at
		}
		if latest.IsZero() || at.After(latest) {
			latest = at
		}
	}
	if earliest.IsZero() {
		return nil
	}
	var releases []model.MacroRelease
	if err := s.db.WithContext(ctx).
		Where("scheduled_at >= ? AND scheduled_at < ?", earliest.Truncate(24*time.Hour), latest.Truncate(24*time.Hour).Add(24*time.Hour)).
		Order("scheduled_at ASC, id ASC").
		Find(&releases).Error; err != nil {
		return err
	}
	byKey := make(map[string][]model.MacroRelease)
	for _, release := range releases {
		key := release.CanonicalEventKey
		if key == "" {
			key = canonicalMacroEventKey(release.Category, release.Title, release.ScheduledAt)
		}
		byKey[key] = append(byKey[key], release)
	}
	for index := range items {
		key := items[index].CanonicalEventKey
		for _, release := range byKey[key] {
			items[index].RelatedSources = append(items[index].RelatedSources, MacroReleaseSource{
				Provider: release.Provider, Category: release.Category, Title: release.Title, Status: release.Status,
				ScheduledAt: release.ScheduledAt, PublishedAt: release.PublishedAt, SourceURL: release.SourceURL,
				Official: release.Provider != macroProviderLongbridge,
			})
		}
		sort.SliceStable(items[index].RelatedSources, func(i, j int) bool {
			if items[index].RelatedSources[i].Official != items[index].RelatedSources[j].Official {
				return items[index].RelatedSources[i].Official
			}
			return items[index].RelatedSources[i].Provider < items[index].RelatedSources[j].Provider
		})
	}
	return nil
}
