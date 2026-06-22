package discovery

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"
)

const DefaultNYSECalendarVersion = "nyse-2026-2028-v1"

const (
	maxNYSECalendarSeedBytes = 1 << 20
	maxNYSECalendarSeedRows  = 1000
	calendarReviewClockSkew  = 5 * time.Minute
)

var ErrCalendarYearMissing = errors.New("market calendar year missing")
var ErrCalendarSeedConflict = errors.New("market calendar seed conflict")

//go:embed testdata/calendar/nyse_holidays_2026_2028.csv
var defaultNYSECalendarCSV string

// This manifest is deliberately independent from the embedded CSV. Adding or
// changing a seed version requires reviewing both artifacts against NYSE's
// published calendar.
var nyseCalendarManifest = map[string]map[int][]string{
	DefaultNYSECalendarVersion: {
		2026: {"2026-01-01", "2026-01-19", "2026-02-16", "2026-04-03", "2026-05-25", "2026-06-19", "2026-07-03", "2026-09-07", "2026-11-26", "2026-12-25"},
		2027: {"2027-01-01", "2027-01-18", "2027-02-15", "2027-03-26", "2027-05-31", "2027-06-18", "2027-07-05", "2027-09-06", "2027-11-25", "2027-12-24"},
		2028: {"2028-01-17", "2028-02-21", "2028-04-14", "2028-05-29", "2028-06-19", "2028-07-04", "2028-09-04", "2028-11-23", "2028-12-25"},
	},
}

type MarketCalendar interface {
	IsTradingDate(ctx context.Context, date string) (bool, error)
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

// IsTradingDay interprets day as an instant and evaluates the civil date at
// that instant in America/New_York. Use IsTradingDate for date-only input.
func (calendar *DatabaseMarketCalendar) IsTradingDay(ctx context.Context, day time.Time) (bool, error) {
	localDay := day.In(calendar.newYork)
	return calendar.isTradingDate(ctx, localDay.Format(time.DateOnly), localDay.Year(), localDay.Weekday())
}

// IsTradingDate evaluates a strict YYYY-MM-DD New York civil date without a
// timezone conversion.
func (calendar *DatabaseMarketCalendar) IsTradingDate(ctx context.Context, date string) (bool, error) {
	if len(date) != len(time.DateOnly) {
		return false, fmt.Errorf("invalid trading date %q: want YYYY-MM-DD", date)
	}
	parsed, err := time.ParseInLocation(time.DateOnly, date, calendar.newYork)
	if err != nil || parsed.Format(time.DateOnly) != date {
		return false, fmt.Errorf("invalid trading date %q: want YYYY-MM-DD", date)
	}
	return calendar.isTradingDate(ctx, date, parsed.Year(), parsed.Weekday())
}

func (calendar *DatabaseMarketCalendar) isTradingDate(ctx context.Context, date string, year int, weekday time.Weekday) (trading bool, err error) {
	err = calendar.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var calendarYear MarketCalendarYear
		result := tx.Where("calendar_version = ? AND year = ? AND complete = ?", calendar.version, year, true).
			Limit(1).
			Find(&calendarYear)
		if result.Error != nil {
			return fmt.Errorf("load market calendar year: %w", result.Error)
		}
		if result.RowsAffected == 0 {
			return fmt.Errorf("%w: version %q year %d", ErrCalendarYearMissing, calendar.version, year)
		}
		if manifestYears, knownVersion := nyseCalendarManifest[calendar.version]; knownVersion {
			expectedDates, knownYear := manifestYears[year]
			expectedHash := hashCalendarDates(expectedDates)
			if !knownYear || calendarYear.ExpectedHolidayCount != len(expectedDates) || calendarYear.HolidayDatesSHA256 != expectedHash {
				return fmt.Errorf("%w: version %q year %d stored manifest metadata mismatch", ErrCalendarYearMissing, calendar.version, year)
			}
		}

		var holidays []MarketHoliday
		if queryErr := tx.Where("calendar_version = ? AND date >= ? AND date <= ?", calendar.version, fmt.Sprintf("%04d-01-01", year), fmt.Sprintf("%04d-12-31", year)).
			Order("date").Find(&holidays).Error; queryErr != nil {
			return fmt.Errorf("lookup market holidays: %w", queryErr)
		}
		baseDates := make([]string, 0, len(holidays))
		isHoliday := false
		for _, holiday := range holidays {
			if holiday.CompleteYear {
				baseDates = append(baseDates, holiday.Date)
			}
			if holiday.Date == date {
				isHoliday = true
			}
		}
		if calendarYear.ExpectedHolidayCount > 0 && (len(baseDates) != calendarYear.ExpectedHolidayCount || hashCalendarDates(baseDates) != calendarYear.HolidayDatesSHA256) {
			return fmt.Errorf("%w: version %q year %d holiday manifest mismatch", ErrCalendarYearMissing, calendar.version, year)
		}
		trading = weekday != time.Saturday && weekday != time.Sunday && !isHoliday
		return nil
	})
	return trading, err
}

func SeedDefaultNYSEMarketCalendar(ctx context.Context, db *gorm.DB) error {
	return SeedNYSEMarketCalendar(ctx, db, strings.NewReader(defaultNYSECalendarCSV))
}

func SeedNYSEMarketCalendar(ctx context.Context, db *gorm.DB, input io.Reader) error {
	if db == nil {
		return errors.New("market calendar database is required")
	}
	limited := &io.LimitedReader{R: input, N: maxNYSECalendarSeedBytes + 1}
	reader := csv.NewReader(limited)
	reader.FieldsPerRecord = -1
	header, err := reader.Read()
	if err != nil {
		return fmt.Errorf("read NYSE calendar seed header: %w", err)
	}
	if limited.N <= 0 {
		return fmt.Errorf("NYSE calendar seed exceeds %d bytes", maxNYSECalendarSeedBytes)
	}
	if len(header) == 0 {
		return errors.New("NYSE calendar seed contains no holiday rows")
	}
	wantHeader := []string{"calendar_version", "year", "date", "name", "source_url", "reviewed_by", "reviewed_at", "complete"}
	if len(header) != len(wantHeader) {
		return fmt.Errorf("NYSE calendar seed header has %d columns, want %d", len(header), len(wantHeader))
	}
	for index := range wantHeader {
		if header[index] != wantHeader[index] {
			return fmt.Errorf("NYSE calendar seed header column %d is %q, want %q", index+1, header[index], wantHeader[index])
		}
	}

	holidays := make([]MarketHoliday, 0, 32)
	years := make(map[string]MarketCalendarYear)
	seenDates := make(map[string]struct{})
	for line := 2; ; line++ {
		record, readErr := reader.Read()
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return fmt.Errorf("read NYSE calendar seed line %d: %w", line, readErr)
		}
		if line > maxNYSECalendarSeedRows+1 {
			return fmt.Errorf("NYSE calendar seed exceeds %d rows", maxNYSECalendarSeedRows)
		}
		if len(record) != len(wantHeader) {
			return fmt.Errorf("NYSE calendar seed line %d has %d columns, want %d", line, len(record), len(wantHeader))
		}
		for index := range record {
			record[index] = strings.TrimSpace(record[index])
		}
		version := strings.TrimSpace(record[0])
		year, yearErr := strconv.Atoi(record[1])
		date, dateErr := time.Parse(time.DateOnly, record[2])
		reviewedAt, reviewedErr := time.Parse(time.RFC3339, record[6])
		complete, completeErr := strconv.ParseBool(record[7])
		if version == "" || record[3] == "" || record[4] == "" || record[5] == "" {
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
		if err := validateNYSESourceURL(record[4]); err != nil {
			return fmt.Errorf("NYSE calendar seed line %d has invalid source_url: %w", line, err)
		}
		if reviewedAt.After(time.Now().Add(calendarReviewClockSkew)) {
			return fmt.Errorf("NYSE calendar seed line %d reviewed_at is in the future", line)
		}
		dateKey := version + "\x00" + record[2]
		if _, exists := seenDates[dateKey]; exists {
			return fmt.Errorf("NYSE calendar seed line %d duplicates version %q date %s", line, version, record[2])
		}
		seenDates[dateKey] = struct{}{}
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
	if limited.N <= 0 {
		return fmt.Errorf("NYSE calendar seed exceeds %d bytes", maxNYSECalendarSeedBytes)
	}
	if len(holidays) == 0 {
		return errors.New("NYSE calendar seed contains no holiday rows")
	}
	if err := verifyNYSECalendarManifest(years, holidays); err != nil {
		return err
	}

	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, year := range years {
			if err := createCalendarYearExact(tx, year); err != nil {
				return fmt.Errorf("seed NYSE calendar year %d: %w", year.Year, err)
			}
		}
		for index := range holidays {
			if err := createHolidayExact(tx, holidays[index]); err != nil {
				return fmt.Errorf("seed NYSE holiday %s: %w", holidays[index].Date, err)
			}
		}
		return nil
	})
}

func verifyNYSECalendarManifest(years map[string]MarketCalendarYear, holidays []MarketHoliday) error {
	datesByVersionYear := make(map[string][]string)
	versions := make(map[string]struct{})
	for _, holiday := range holidays {
		key := fmt.Sprintf("%s\x00%s", holiday.CalendarVersion, holiday.Date[:4])
		datesByVersionYear[key] = append(datesByVersionYear[key], holiday.Date)
		versions[holiday.CalendarVersion] = struct{}{}
	}
	for version := range versions {
		manifest, ok := nyseCalendarManifest[version]
		if !ok {
			return fmt.Errorf("NYSE calendar seed version %q has no reviewed manifest", version)
		}
		for year, expectedDates := range manifest {
			key := fmt.Sprintf("%s\x00%d", version, year)
			calendarYear, exists := years[key]
			if !exists || !calendarYear.Complete {
				return fmt.Errorf("NYSE calendar seed version %q is missing complete target year %d", version, year)
			}
			actualDates := datesByVersionYear[key]
			sort.Strings(actualDates)
			if len(actualDates) != len(expectedDates) || hashCalendarDates(actualDates) != hashCalendarDates(expectedDates) {
				return fmt.Errorf("NYSE calendar seed version %q year %d does not match reviewed holiday manifest", version, year)
			}
			calendarYear.ExpectedHolidayCount = len(expectedDates)
			calendarYear.HolidayDatesSHA256 = hashCalendarDates(expectedDates)
			years[key] = calendarYear
		}
		for key := range years {
			if strings.HasPrefix(key, version+"\x00") {
				var year int
				_, _ = fmt.Sscanf(strings.TrimPrefix(key, version+"\x00"), "%d", &year)
				if _, exists := manifest[year]; !exists {
					return fmt.Errorf("NYSE calendar seed version %q contains unexpected year %d", version, year)
				}
			}
		}
	}
	return nil
}

func hashCalendarDates(dates []string) string {
	copyOfDates := append([]string(nil), dates...)
	sort.Strings(copyOfDates)
	return fmt.Sprintf("%x", sha256.Sum256([]byte(strings.Join(copyOfDates, "\n"))))
}

func backfillLegacyNYSECalendarManifest(tx *gorm.DB) error {
	desiredYears, desiredHolidays, err := trustedDefaultNYSECalendarRows()
	if err != nil {
		return err
	}

	var existingYears []MarketCalendarYear
	if err := tx.Where("calendar_version = ?", DefaultNYSECalendarVersion).Find(&existingYears).Error; err != nil {
		return fmt.Errorf("load legacy NYSE calendar years: %w", err)
	}
	if len(existingYears) != len(desiredYears) {
		return legacyCalendarConflict("calendar year set differs from the reviewed seed")
	}
	for _, existing := range existingYears {
		desired, ok := desiredYears[existing.Year]
		if !ok || existing.CalendarVersion != desired.CalendarVersion || existing.Year != desired.Year ||
			existing.Complete != desired.Complete || existing.SourceURL != desired.SourceURL ||
			existing.ReviewedBy != desired.ReviewedBy || !existing.ReviewedAt.Equal(desired.ReviewedAt) {
			return legacyCalendarConflict("calendar year %d audit metadata differs from the reviewed seed", existing.Year)
		}
	}

	var existingHolidays []MarketHoliday
	if err := tx.Where("calendar_version = ? AND complete_year = ?", DefaultNYSECalendarVersion, true).Find(&existingHolidays).Error; err != nil {
		return fmt.Errorf("load legacy NYSE holidays: %w", err)
	}
	if len(existingHolidays) != len(desiredHolidays) {
		return legacyCalendarConflict("complete-year holiday set differs from the reviewed seed")
	}
	for _, existing := range existingHolidays {
		desired, ok := desiredHolidays[existing.Date]
		if !ok || existing != desired {
			return legacyCalendarConflict("holiday %s differs from the reviewed seed", existing.Date)
		}
	}

	for _, desired := range desiredYears {
		result := tx.Model(&MarketCalendarYear{}).
			Where("calendar_version = ? AND year = ?", desired.CalendarVersion, desired.Year).
			Updates(map[string]any{
				"expected_holiday_count": desired.ExpectedHolidayCount,
				"holiday_dates_sha256":   desired.HolidayDatesSHA256,
			})
		if result.Error != nil {
			return fmt.Errorf("backfill legacy NYSE calendar year %d: %w", desired.Year, result.Error)
		}
		if result.RowsAffected != 1 {
			return legacyCalendarConflict("calendar year %d disappeared during backfill", desired.Year)
		}
	}
	return nil
}

func trustedDefaultNYSECalendarRows() (map[int]MarketCalendarYear, map[string]MarketHoliday, error) {
	reader := csv.NewReader(strings.NewReader(defaultNYSECalendarCSV))
	if _, err := reader.Read(); err != nil {
		return nil, nil, fmt.Errorf("read trusted NYSE calendar header: %w", err)
	}
	years := make(map[int]MarketCalendarYear)
	holidays := make(map[string]MarketHoliday)
	for {
		record, err := reader.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, nil, fmt.Errorf("read trusted NYSE calendar row: %w", err)
		}
		if len(record) != 8 {
			return nil, nil, errors.New("trusted NYSE calendar seed is invalid")
		}
		year, yearErr := strconv.Atoi(record[1])
		reviewedAt, reviewedErr := time.Parse(time.RFC3339, record[6])
		complete, completeErr := strconv.ParseBool(record[7])
		if yearErr != nil || reviewedErr != nil || completeErr != nil {
			return nil, nil, errors.New("trusted NYSE calendar seed is invalid")
		}
		dates, knownYear := nyseCalendarManifest[DefaultNYSECalendarVersion][year]
		if record[0] != DefaultNYSECalendarVersion || !knownYear {
			return nil, nil, errors.New("trusted NYSE calendar seed does not match its manifest")
		}
		years[year] = MarketCalendarYear{
			CalendarVersion:      record[0],
			Year:                 year,
			Complete:             complete,
			ExpectedHolidayCount: len(dates),
			HolidayDatesSHA256:   hashCalendarDates(dates),
			SourceURL:            record[4],
			ReviewedBy:           record[5],
			ReviewedAt:           reviewedAt,
		}
		holidays[record[2]] = MarketHoliday{
			Date:            record[2],
			Name:            record[3],
			CalendarVersion: record[0],
			SourceURL:       record[4],
			ReviewedBy:      record[5],
			CompleteYear:    complete,
			ReviewedAt:      reviewedAt,
		}
	}
	return years, holidays, nil
}

func legacyCalendarConflict(format string, args ...any) error {
	return fmt.Errorf("%w: legacy NYSE calendar "+format, append([]any{ErrCalendarSeedConflict}, args...)...)
}

func validateNYSESourceURL(value string) error {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() != "www.nyse.com" || parsed.Path != "/markets/hours-calendars" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("must be https://www.nyse.com/markets/hours-calendars")
	}
	return nil
}

func createCalendarYearExact(tx *gorm.DB, desired MarketCalendarYear) error {
	var existing MarketCalendarYear
	err := tx.First(&existing, "calendar_version = ? AND year = ?", desired.CalendarVersion, desired.Year).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return tx.Create(&desired).Error
	}
	if err != nil {
		return err
	}
	if existing != desired {
		return fmt.Errorf("%w: version %q year %d", ErrCalendarSeedConflict, desired.CalendarVersion, desired.Year)
	}
	return nil
}

func createHolidayExact(tx *gorm.DB, desired MarketHoliday) error {
	var existing MarketHoliday
	err := tx.First(&existing, "calendar_version = ? AND date = ?", desired.CalendarVersion, desired.Date).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return tx.Create(&desired).Error
	}
	if err != nil {
		return err
	}
	if existing != desired {
		return fmt.Errorf("%w: version %q date %s", ErrCalendarSeedConflict, desired.CalendarVersion, desired.Date)
	}
	return nil
}
