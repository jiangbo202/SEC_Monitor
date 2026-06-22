package discovery

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"sec_monitor/internal/config"

	"gorm.io/gorm"
)

func TestMarketCalendarTradingDays(t *testing.T) {
	db := openMigratedTestDatabase(t)
	calendar, err := NewDatabaseMarketCalendar(db, DefaultNYSECalendarVersion)
	if err != nil {
		t.Fatalf("NewDatabaseMarketCalendar: %v", err)
	}

	tests := []struct {
		name string
		day  time.Time
		want bool
	}{
		{name: "normal weekday", day: parseCalendarTime(t, "2026-06-18T12:00:00-04:00"), want: true},
		{name: "weekend", day: parseCalendarTime(t, "2026-06-20T12:00:00-04:00"), want: false},
		{name: "early close remains trading day", day: parseCalendarTime(t, "2026-11-27T12:00:00-05:00"), want: true},
		{name: "UTC instant resolves to prior NY holiday", day: parseCalendarTime(t, "2026-07-04T02:30:00Z"), want: false},
		{name: "DST instant resolves to NY Sunday", day: parseCalendarTime(t, "2026-03-09T03:30:00Z"), want: false},
		{name: "2027 December 31 belongs to complete 2027 and is open", day: parseCalendarTime(t, "2027-12-31T12:00:00-05:00"), want: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := calendar.IsTradingDay(context.Background(), test.day)
			if err != nil {
				t.Fatalf("IsTradingDay: %v", err)
			}
			if got != test.want {
				t.Fatalf("IsTradingDay(%s) = %t, want %t", test.day, got, test.want)
			}
		})
	}
}

func TestMarketCalendarSeededHolidays(t *testing.T) {
	db := openMigratedTestDatabase(t)
	calendar, err := NewDatabaseMarketCalendar(db, DefaultNYSECalendarVersion)
	if err != nil {
		t.Fatalf("NewDatabaseMarketCalendar: %v", err)
	}
	var holidays []MarketHoliday
	if err := db.Where("calendar_version = ?", DefaultNYSECalendarVersion).Order("date").Find(&holidays).Error; err != nil {
		t.Fatalf("load seeded holidays: %v", err)
	}
	if len(holidays) != 29 {
		t.Fatalf("seeded holiday count = %d, want 29", len(holidays))
	}
	for _, holiday := range holidays {
		t.Run(holiday.Date, func(t *testing.T) {
			day := parseCalendarTime(t, holiday.Date+"T12:00:00-05:00")
			got, err := calendar.IsTradingDay(context.Background(), day)
			if err != nil {
				t.Fatalf("IsTradingDay: %v", err)
			}
			if got {
				t.Fatalf("seeded holiday %s (%s) reported as trading day", holiday.Date, holiday.Name)
			}
		})
	}
}

func TestMarketCalendarManualExceptionalClosure(t *testing.T) {
	db := openMigratedTestDatabase(t)
	manual := MarketHoliday{
		Date:            "2026-08-14",
		Name:            "Exceptional closure",
		CalendarVersion: DefaultNYSECalendarVersion,
		SourceURL:       "https://example.test/closure-notice",
		ReviewedBy:      "operations-reviewer",
		ReviewedAt:      time.Date(2026, 8, 13, 18, 0, 0, 0, time.UTC),
	}
	if err := db.Create(&manual).Error; err != nil {
		t.Fatalf("insert manual closure: %v", err)
	}
	calendar, err := NewDatabaseMarketCalendar(db, DefaultNYSECalendarVersion)
	if err != nil {
		t.Fatalf("NewDatabaseMarketCalendar: %v", err)
	}
	got, err := calendar.IsTradingDay(context.Background(), parseCalendarTime(t, "2026-08-14T12:00:00-04:00"))
	if err != nil {
		t.Fatalf("IsTradingDay: %v", err)
	}
	if got {
		t.Fatal("manual exceptional closure reported as trading day")
	}
}

func TestMarketCalendarFailsClosedForMissingOrIncompleteYear(t *testing.T) {
	db := openMigratedTestDatabase(t)
	calendar, err := NewDatabaseMarketCalendar(db, DefaultNYSECalendarVersion)
	if err != nil {
		t.Fatalf("NewDatabaseMarketCalendar: %v", err)
	}
	if err := db.Model(&MarketCalendarYear{}).
		Where("calendar_version = ? AND year = ?", DefaultNYSECalendarVersion, 2028).
		Update("complete", false).Error; err != nil {
		t.Fatalf("mark year incomplete: %v", err)
	}

	for _, day := range []string{"2025-06-14T12:00:00-04:00", "2028-06-17T12:00:00-04:00"} {
		t.Run(day, func(t *testing.T) {
			got, err := calendar.IsTradingDay(context.Background(), parseCalendarTime(t, day))
			if got || !errors.Is(err, ErrCalendarYearMissing) {
				t.Fatalf("IsTradingDay(%s) = (%t, %v), want false and ErrCalendarYearMissing", day, got, err)
			}
		})
	}
}

func TestMarketCalendarVersionIsolation(t *testing.T) {
	db := openMigratedTestDatabase(t)
	otherVersion := "review-draft-v2"
	if err := db.Create(&MarketCalendarYear{CalendarVersion: otherVersion, Year: 2026, Complete: true, SourceURL: "https://example.test/calendar", ReviewedBy: "reviewer", ReviewedAt: time.Now()}).Error; err != nil {
		t.Fatalf("insert other calendar year: %v", err)
	}
	if err := db.Create(&MarketHoliday{Date: "2026-06-18", CalendarVersion: otherVersion, Name: "Draft closure", SourceURL: "https://example.test/calendar", ReviewedBy: "reviewer", ReviewedAt: time.Now()}).Error; err != nil {
		t.Fatalf("insert other-version holiday: %v", err)
	}
	defaultCalendar, _ := NewDatabaseMarketCalendar(db, DefaultNYSECalendarVersion)
	otherCalendar, _ := NewDatabaseMarketCalendar(db, otherVersion)
	day := parseCalendarTime(t, "2026-06-18T12:00:00-04:00")
	defaultOpen, defaultErr := defaultCalendar.IsTradingDay(context.Background(), day)
	otherOpen, otherErr := otherCalendar.IsTradingDay(context.Background(), day)
	if defaultErr != nil || !defaultOpen || otherErr != nil || otherOpen {
		t.Fatalf("version isolation: default=(%t,%v), other=(%t,%v)", defaultOpen, defaultErr, otherOpen, otherErr)
	}
}

func TestMarketCalendarSeedIsIdempotentAndPreservesReview(t *testing.T) {
	db := openMigratedTestDatabase(t)
	customReview := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	if err := db.Model(&MarketCalendarYear{}).
		Where("calendar_version = ? AND year = ?", DefaultNYSECalendarVersion, 2026).
		Updates(map[string]any{"reviewed_by": "local-reviewer", "reviewed_at": customReview}).Error; err != nil {
		t.Fatalf("customize review metadata: %v", err)
	}
	if err := SeedDefaultNYSEMarketCalendar(context.Background(), db); err != nil {
		t.Fatalf("second seed: %v", err)
	}
	var year MarketCalendarYear
	if err := db.First(&year, "calendar_version = ? AND year = ?", DefaultNYSECalendarVersion, 2026).Error; err != nil {
		t.Fatalf("load year: %v", err)
	}
	if year.ReviewedBy != "local-reviewer" || !year.ReviewedAt.Equal(customReview) {
		t.Fatalf("seed overwrote review metadata: %+v", year)
	}
	var count int64
	if err := db.Model(&MarketHoliday{}).Where("calendar_version = ?", DefaultNYSECalendarVersion).Count(&count).Error; err != nil {
		t.Fatalf("count holidays: %v", err)
	}
	if count != 29 {
		t.Fatalf("holiday count after reseed = %d, want 29", count)
	}
}

func TestMarketCalendarSeedRejectsInvalidRows(t *testing.T) {
	tests := []struct {
		name string
		csv  string
	}{
		{name: "invalid date", csv: "calendar_version,year,date,name,source_url,reviewed_by,reviewed_at,complete\nv1,2026,not-a-date,Holiday,https://example.test,reviewer,2026-01-01T00:00:00Z,true\n"},
		{name: "date year mismatch", csv: "calendar_version,year,date,name,source_url,reviewed_by,reviewed_at,complete\nv1,2026,2027-01-01,Holiday,https://example.test,reviewer,2026-01-01T00:00:00Z,true\n"},
		{name: "missing audit field", csv: "calendar_version,year,date,name,source_url,reviewed_by,reviewed_at,complete\nv1,2026,2026-01-01,Holiday,,reviewer,2026-01-01T00:00:00Z,true\n"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := openUnmigratedCalendarDatabase(t)
			if err := db.AutoMigrate(&MarketHoliday{}, &MarketCalendarYear{}); err != nil {
				t.Fatalf("AutoMigrate: %v", err)
			}
			if err := SeedNYSEMarketCalendar(context.Background(), db, strings.NewReader(test.csv)); err == nil {
				t.Fatal("SeedNYSEMarketCalendar succeeded for invalid CSV")
			}
			var count int64
			if err := db.Model(&MarketHoliday{}).Count(&count).Error; err != nil {
				t.Fatalf("count holidays: %v", err)
			}
			if count != 0 {
				t.Fatalf("invalid seed inserted %d holidays", count)
			}
		})
	}
}

func openUnmigratedCalendarDatabase(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := OpenDatabase(config.DatabaseConfig{Type: "sqlite", DSN: ":memory:"})
	if err != nil {
		t.Fatalf("OpenDatabase: %v", err)
	}
	return db
}

func parseCalendarTime(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		t.Fatalf("parse time %q: %v", value, err)
	}
	return parsed
}
