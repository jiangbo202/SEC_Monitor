package discovery

import (
	"context"
	"errors"
	"fmt"
	"reflect"
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

func TestMarketCalendarCivilDateDoesNotShiftToPriorNewYorkDate(t *testing.T) {
	db := openMigratedTestDatabase(t)
	calendar, err := NewDatabaseMarketCalendar(db, DefaultNYSECalendarVersion)
	if err != nil {
		t.Fatalf("NewDatabaseMarketCalendar: %v", err)
	}

	for _, test := range []struct {
		date string
		want bool
	}{
		{date: "2026-07-03", want: false},
		{date: "2026-07-06", want: true},
	} {
		got, err := calendar.IsTradingDate(context.Background(), test.date)
		if err != nil {
			t.Fatalf("IsTradingDate(%q): %v", test.date, err)
		}
		if got != test.want {
			t.Fatalf("IsTradingDate(%q) = %t, want %t", test.date, got, test.want)
		}
	}

	utcMidnight, err := time.Parse(time.DateOnly, "2026-07-03")
	if err != nil {
		t.Fatal(err)
	}
	gotInstant, err := calendar.IsTradingDay(context.Background(), utcMidnight)
	if err != nil {
		t.Fatalf("IsTradingDay: %v", err)
	}
	if !gotInstant {
		t.Fatal("IsTradingDay must retain instant semantics: UTC midnight is July 2 in New York")
	}
}

func TestMarketCalendarRejectsInvalidCivilDate(t *testing.T) {
	db := openMigratedTestDatabase(t)
	calendar, _ := NewDatabaseMarketCalendar(db, DefaultNYSECalendarVersion)
	for _, date := range []string{"2026-7-03", "2026-07-03T00:00:00Z", " 2026-07-03 ", "2026-02-30"} {
		if _, err := calendar.IsTradingDate(context.Background(), date); err == nil {
			t.Fatalf("IsTradingDate(%q) succeeded, want strict YYYY-MM-DD error", date)
		}
	}
}

func TestMarketCalendarSeededHolidays(t *testing.T) {
	db := openMigratedTestDatabase(t)
	calendar, err := NewDatabaseMarketCalendar(db, DefaultNYSECalendarVersion)
	if err != nil {
		t.Fatalf("NewDatabaseMarketCalendar: %v", err)
	}
	var holidays []MarketHoliday
	if err := db.Where("calendar_version = ? AND complete_year = ?", DefaultNYSECalendarVersion, true).Order("date").Find(&holidays).Error; err != nil {
		t.Fatalf("load seeded holidays: %v", err)
	}
	wantDates := officialNYSEHolidayDatesForTest()
	if len(holidays) != len(wantDates) {
		t.Fatalf("seeded holiday count = %d, want %d", len(holidays), len(wantDates))
	}
	for index, holiday := range holidays {
		if holiday.Date != wantDates[index] {
			t.Fatalf("seeded holiday[%d] = %q, want independently specified %q", index, holiday.Date, wantDates[index])
		}
		t.Run(holiday.Date, func(t *testing.T) {
			got, err := calendar.IsTradingDate(context.Background(), holiday.Date)
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

func TestMarketCalendarFailsClosedWhenCompleteYearManifestIsCorrupt(t *testing.T) {
	for _, test := range []struct {
		name   string
		tamper func(*gorm.DB) error
	}{
		{
			name: "stored manifest metadata",
			tamper: func(db *gorm.DB) error {
				return db.Model(&MarketCalendarYear{}).
					Where("calendar_version = ? AND year = ?", DefaultNYSECalendarVersion, 2026).
					Updates(map[string]any{"expected_holiday_count": 0, "holiday_dates_sha256": ""}).Error
			},
		},
		{
			name: "base holiday set",
			tamper: func(db *gorm.DB) error {
				return db.Delete(&MarketHoliday{}, "calendar_version = ? AND date = ?", DefaultNYSECalendarVersion, "2026-07-03").Error
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			db := openMigratedTestDatabase(t)
			if err := test.tamper(db); err != nil {
				t.Fatal(err)
			}
			calendar, _ := NewDatabaseMarketCalendar(db, DefaultNYSECalendarVersion)
			got, err := calendar.IsTradingDate(context.Background(), "2026-07-06")
			if got || !errors.Is(err, ErrCalendarYearMissing) {
				t.Fatalf("IsTradingDate after manifest corruption = (%t, %v), want false and ErrCalendarYearMissing", got, err)
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
	if err := SeedDefaultNYSEMarketCalendar(context.Background(), db); err != nil {
		t.Fatalf("second seed: %v", err)
	}
	var count int64
	if err := db.Model(&MarketHoliday{}).Where("calendar_version = ?", DefaultNYSECalendarVersion).Count(&count).Error; err != nil {
		t.Fatalf("count holidays: %v", err)
	}
	if count != 29 {
		t.Fatalf("holiday count after reseed = %d, want 29", count)
	}
}

func TestMarketCalendarMigrationIsIdempotentAndRejectsDatabaseConflicts(t *testing.T) {
	for _, test := range []struct {
		name   string
		tamper func(*gorm.DB) error
	}{
		{
			name: "calendar year metadata",
			tamper: func(db *gorm.DB) error {
				return db.Model(&MarketCalendarYear{}).Where("calendar_version = ? AND year = ?", DefaultNYSECalendarVersion, 2026).Update("reviewed_by", "tampered").Error
			},
		},
		{
			name: "holiday content",
			tamper: func(db *gorm.DB) error {
				return db.Model(&MarketHoliday{}).Where("calendar_version = ? AND date = ?", DefaultNYSECalendarVersion, "2026-07-03").Update("name", "tampered").Error
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			db := openMigratedTestDatabase(t)
			if err := Migrate(db); err != nil {
				t.Fatalf("repeat Migrate: %v", err)
			}
			if err := test.tamper(db); err != nil {
				t.Fatalf("tamper: %v", err)
			}
			err := Migrate(db)
			if !errors.Is(err, ErrCalendarSeedConflict) {
				t.Fatalf("Migrate after tamper error = %v, want ErrCalendarSeedConflict", err)
			}
		})
	}
}

func TestMarketCalendarSeedRequiresCompleteManifest(t *testing.T) {
	valid := defaultNYSECalendarCSV
	tests := []struct {
		name string
		csv  string
	}{
		{name: "missing target year", csv: strings.Join(strings.Split(valid, "\n")[:11], "\n") + "\n"},
		{name: "missing holiday", csv: strings.Replace(valid, "nyse-2026-2028-v1,2026,2026-07-03,Independence Day (observed),https://www.nyse.com/markets/hours-calendars,sec-monitor-maintainers,2026-06-22T00:00:00Z,true\n", "", 1)},
		{name: "wrong holiday", csv: strings.Replace(valid, "2026-07-03", "2026-07-02", 1)},
		{name: "duplicate holiday", csv: valid + strings.Split(valid, "\n")[1] + "\n"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := openUnmigratedCalendarDatabase(t)
			if err := db.AutoMigrate(&MarketHoliday{}, &MarketCalendarYear{}); err != nil {
				t.Fatal(err)
			}
			if err := SeedNYSEMarketCalendar(context.Background(), db, strings.NewReader(test.csv)); err == nil {
				t.Fatal("invalid manifest seed succeeded")
			}
		})
	}
}

func TestMarketCalendarSeedValidatesAuditMetadata(t *testing.T) {
	valid := defaultNYSECalendarCSV
	tests := []struct {
		name string
		csv  string
	}{
		{name: "non HTTPS source", csv: strings.Replace(valid, "https://www.nyse.com/markets/hours-calendars", "http://www.nyse.com/markets/hours-calendars", 1)},
		{name: "non NYSE source", csv: strings.Replace(valid, "https://www.nyse.com/markets/hours-calendars", "https://example.test/calendar", 1)},
		{name: "future review", csv: strings.Replace(valid, "2026-06-22T00:00:00Z", "2099-01-01T00:00:00Z", 1)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := openUnmigratedCalendarDatabase(t)
			if err := db.AutoMigrate(&MarketHoliday{}, &MarketCalendarYear{}); err != nil {
				t.Fatal(err)
			}
			if err := SeedNYSEMarketCalendar(context.Background(), db, strings.NewReader(test.csv)); err == nil {
				t.Fatal("invalid audit seed succeeded")
			}
		})
	}
}

func TestMarketCalendarSeedTrimsAuditMetadata(t *testing.T) {
	seed := strings.ReplaceAll(defaultNYSECalendarCSV, "https://www.nyse.com/markets/hours-calendars", "  https://www.nyse.com/markets/hours-calendars  ")
	seed = strings.ReplaceAll(seed, "sec-monitor-maintainers", "  sec-monitor-maintainers  ")
	db := openUnmigratedCalendarDatabase(t)
	if err := db.AutoMigrate(&MarketHoliday{}, &MarketCalendarYear{}); err != nil {
		t.Fatal(err)
	}
	if err := SeedNYSEMarketCalendar(context.Background(), db, strings.NewReader(seed)); err != nil {
		t.Fatalf("SeedNYSEMarketCalendar: %v", err)
	}
	var holiday MarketHoliday
	if err := db.First(&holiday, "calendar_version = ? AND date = ?", DefaultNYSECalendarVersion, "2026-01-01").Error; err != nil {
		t.Fatal(err)
	}
	if holiday.SourceURL != "https://www.nyse.com/markets/hours-calendars" || holiday.ReviewedBy != "sec-monitor-maintainers" {
		t.Fatalf("audit metadata was not trimmed: %+v", holiday)
	}
}

func TestMarketCalendarReadsCompletenessAndHolidaysInTransaction(t *testing.T) {
	db := openMigratedTestDatabase(t)
	calendar, _ := NewDatabaseMarketCalendar(db, DefaultNYSECalendarVersion)
	var queryPools []string
	callbackName := "test:record-calendar-query-pools"
	if err := db.Callback().Query().Before("gorm:query").Register(callbackName, func(tx *gorm.DB) {
		if tx.Statement.Table == "market_calendar_years" || tx.Statement.Table == "market_holidays" {
			queryPools = append(queryPools, reflect.TypeOf(tx.Statement.ConnPool).String())
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Callback().Query().Remove(callbackName) })
	if _, err := calendar.IsTradingDate(context.Background(), "2026-07-06"); err != nil {
		t.Fatal(err)
	}
	if len(queryPools) < 2 {
		t.Fatalf("calendar issued %d tracked queries, want completeness and holidays", len(queryPools))
	}
	for _, pool := range queryPools {
		if pool != "*sql.Tx" {
			t.Fatalf("calendar query used %s, want *sql.Tx consistent snapshot; all=%v", pool, queryPools)
		}
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
		{name: "oversized seed", csv: strings.Repeat("x", 2<<20)},
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

func officialNYSEHolidayDatesForTest() []string {
	return []string{
		"2026-01-01", "2026-01-19", "2026-02-16", "2026-04-03", "2026-05-25", "2026-06-19", "2026-07-03", "2026-09-07", "2026-11-26", "2026-12-25",
		"2027-01-01", "2027-01-18", "2027-02-15", "2027-03-26", "2027-05-31", "2027-06-18", "2027-07-05", "2027-09-06", "2027-11-25", "2027-12-24",
		"2028-01-17", "2028-02-21", "2028-04-14", "2028-05-29", "2028-06-19", "2028-07-04", "2028-09-04", "2028-11-23", "2028-12-25",
	}
}

func ExampleDatabaseMarketCalendar_IsTradingDate() {
	fmt.Println("Use IsTradingDate for YYYY-MM-DD civil dates; IsTradingDay interprets a time.Time as an instant.")
	// Output: Use IsTradingDate for YYYY-MM-DD civil dates; IsTradingDay interprets a time.Time as an instant.
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
