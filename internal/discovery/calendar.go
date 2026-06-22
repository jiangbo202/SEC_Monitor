package discovery

import (
	"context"
	_ "embed"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const DefaultNYSECalendarVersion = "nyse-2026-2028-v1"

var ErrCalendarYearMissing = errors.New("market calendar year missing")

//go:embed testdata/calendar/nyse_holidays_2026_2028.csv
var defaultNYSECalendarCSV string

type MarketCalendar interface {
	IsTradingDay(ctx context.Context, day time.Time) (bool, error)
}

type DatabaseMarketCalendar struct {
	db      *gorm.DB
	version string
	newYork *time.Location
}

func NewDatabaseMarketCalendar(db *gorm.DB, calendarVersion string) (*DatabaseMarketCalendar, error) {
	if db == nil {
		return nil, errors.New("market calendar database is required")
	}
	if strings.TrimSpace(calendarVersion) == "" {
		return nil, errors.New("calendar version is required")
	}
	newYork, err := time.LoadLocation("America/New_York")
	if err != nil {
		return nil, fmt.Errorf("load America/New_York: %w", err)
	}
	return &DatabaseMarketCalendar{db: db, version: calendarVersion, newYork: newYork}, nil
}

func (calendar *DatabaseMarketCalendar) IsTradingDay(ctx context.Context, day time.Time) (bool, error) {
	localDay := day.In(calendar.newYork)
	year := localDay.Year()
	var calendarYear MarketCalendarYear
	result := calendar.db.WithContext(ctx).
		Where("calendar_version = ? AND year = ? AND complete = ?", calendar.version, year, true).
		Limit(1).
		Find(&calendarYear)
	if result.Error != nil {
		return false, fmt.Errorf("load market calendar year: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return false, fmt.Errorf("%w: version %q year %d", ErrCalendarYearMissing, calendar.version, year)
	}

	if localDay.Weekday() == time.Saturday || localDay.Weekday() == time.Sunday {
		return false, nil
	}
	date := localDay.Format(time.DateOnly)
	var count int64
	if err := calendar.db.WithContext(ctx).Model(&MarketHoliday{}).
		Where("calendar_version = ? AND date = ?", calendar.version, date).
		Count(&count).Error; err != nil {
		return false, fmt.Errorf("lookup market holiday: %w", err)
	}
	return count == 0, nil
}

func SeedDefaultNYSEMarketCalendar(ctx context.Context, db *gorm.DB) error {
	return SeedNYSEMarketCalendar(ctx, db, strings.NewReader(defaultNYSECalendarCSV))
}

func SeedNYSEMarketCalendar(ctx context.Context, db *gorm.DB, input io.Reader) error {
	if db == nil {
		return errors.New("market calendar database is required")
	}
	reader := csv.NewReader(input)
	records, err := reader.ReadAll()
	if err != nil {
		return fmt.Errorf("read NYSE calendar seed: %w", err)
	}
	if len(records) < 2 {
		return errors.New("NYSE calendar seed contains no holiday rows")
	}
	wantHeader := []string{"calendar_version", "year", "date", "name", "source_url", "reviewed_by", "reviewed_at", "complete"}
	if len(records[0]) != len(wantHeader) {
		return fmt.Errorf("NYSE calendar seed header has %d columns, want %d", len(records[0]), len(wantHeader))
	}
	for index := range wantHeader {
		if records[0][index] != wantHeader[index] {
			return fmt.Errorf("NYSE calendar seed header column %d is %q, want %q", index+1, records[0][index], wantHeader[index])
		}
	}

	holidays := make([]MarketHoliday, 0, len(records)-1)
	years := make(map[string]MarketCalendarYear)
	for index, record := range records[1:] {
		line := index + 2
		if len(record) != len(wantHeader) {
			return fmt.Errorf("NYSE calendar seed line %d has %d columns, want %d", line, len(record), len(wantHeader))
		}
		version := strings.TrimSpace(record[0])
		year, yearErr := strconv.Atoi(record[1])
		date, dateErr := time.Parse(time.DateOnly, record[2])
		reviewedAt, reviewedErr := time.Parse(time.RFC3339, record[6])
		complete, completeErr := strconv.ParseBool(record[7])
		if version == "" || strings.TrimSpace(record[3]) == "" || strings.TrimSpace(record[4]) == "" || strings.TrimSpace(record[5]) == "" {
			return fmt.Errorf("NYSE calendar seed line %d is missing a required audit field", line)
		}
		if yearErr != nil || year < 1 {
			return fmt.Errorf("NYSE calendar seed line %d has invalid year %q", line, record[1])
		}
		if dateErr != nil {
			return fmt.Errorf("NYSE calendar seed line %d has invalid date %q", line, record[2])
		}
		if date.Year() != year {
			return fmt.Errorf("NYSE calendar seed line %d date year %d does not match declared year %d", line, date.Year(), year)
		}
		if reviewedErr != nil || reviewedAt.IsZero() {
			return fmt.Errorf("NYSE calendar seed line %d has invalid reviewed_at %q", line, record[6])
		}
		if completeErr != nil {
			return fmt.Errorf("NYSE calendar seed line %d has invalid complete value %q", line, record[7])
		}
		calendarYear := MarketCalendarYear{
			CalendarVersion: version,
			Year:            year,
			Complete:        complete,
			SourceURL:       record[4],
			ReviewedBy:      record[5],
			ReviewedAt:      reviewedAt,
		}
		key := fmt.Sprintf("%s\x00%d", version, year)
		if existing, ok := years[key]; ok && existing != calendarYear {
			return fmt.Errorf("NYSE calendar seed line %d conflicts with audit metadata for version %q year %d", line, version, year)
		}
		years[key] = calendarYear
		holidays = append(holidays, MarketHoliday{
			Date:            date.Format(time.DateOnly),
			Name:            record[3],
			CalendarVersion: version,
			SourceURL:       record[4],
			ReviewedBy:      record[5],
			CompleteYear:    complete,
			ReviewedAt:      reviewedAt,
		})
	}

	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, year := range years {
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&year).Error; err != nil {
				return fmt.Errorf("seed NYSE calendar year %d: %w", year.Year, err)
			}
		}
		for index := range holidays {
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&holidays[index]).Error; err != nil {
				return fmt.Errorf("seed NYSE holiday %s: %w", holidays[index].Date, err)
			}
		}
		return nil
	})
}
