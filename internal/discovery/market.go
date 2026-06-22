package discovery

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	PriceFormatNormalized PriceFormat = "normalized"
	PriceFormatStooq      PriceFormat = "stooq"
	PriceFormatStooqZIP   PriceFormat = "stooq_zip"

	maxPriceCSVBytes = 64 << 20
	maxPriceCSVRows  = 2_000_000

	ProviderActivationTradingDays = 20
	ProviderDegradedFailureDays   = 3
	MinimumIndependentGoldRows    = 100
	DefaultPriceCoveragePct       = 95.0
	DefaultValidationErrorPct     = 0.0
	DefaultIndependentErrorPct    = 1.0
)

type PriceFormat string

type PriceRecord struct {
	Symbol                            string
	TradeDate                         time.Time
	OpenMicros, HighMicros, LowMicros int64
	CloseMicros, Volume               int64
	Currency                          string
	Adjusted                          bool
	Source                            string
}

type ProviderResult struct {
	Provider, Status, SourceVersion, SHA256 string
	EffectiveDate                           time.Time
	Records, Expected                       int
	CoveragePct, ValidationErrorPct         float64
	Timely                                  bool
}

type PriceProvider interface {
	Load(ctx context.Context, expected []Listing) ([]PriceRecord, ProviderResult, error)
}

type PriceValidationOptions struct {
	Provider      string
	SourceVersion string
	SourceURL     string
	EffectiveDate time.Time
	Now           time.Time
	Calendar      MarketCalendar
	Expected      []Listing
}

type GoldProvenance struct {
	Provider  string
	SourceURL string
	SHA256    string
}

// ParsePriceCSV parses the complete input before returning any records. Dates
// are New York civil midnights, not UTC instants converted into New York.
func ParsePriceCSV(ctx context.Context, input io.Reader, format PriceFormat, options PriceValidationOptions) ([]PriceRecord, ProviderResult, error) {
	payload, err := readBoundedPriceInput(input)
	if err != nil {
		return nil, ProviderResult{}, err
	}
	hash := sha256.Sum256(payload)
	sha := hex.EncodeToString(hash[:])
	csvPayload := payload
	if format == PriceFormatStooqZIP {
		csvPayload, err = readSingleCSVFromZIP(payload)
		if err != nil {
			return nil, ProviderResult{}, err
		}
		format = PriceFormatStooq
	}
	records, err := parsePriceRecords(ctx, csvPayload, format, options)
	if err != nil {
		return nil, ProviderResult{}, err
	}
	result, err := validatePriceBatch(ctx, records, options)
	if err != nil {
		return nil, ProviderResult{}, err
	}
	result.SHA256 = sha
	if strings.TrimSpace(result.SourceVersion) == "" {
		result.SourceVersion = sha
	}
	return records, result, nil
}

func ImportPriceCSV(ctx context.Context, db *gorm.DB, input io.Reader, format PriceFormat, options PriceValidationOptions) (ProviderResult, error) {
	if db == nil {
		return ProviderResult{}, errors.New("price database is required")
	}
	records, result, err := ParsePriceCSV(ctx, input, format, options)
	if err != nil {
		return ProviderResult{}, err
	}
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, record := range records {
			snapshot := PriceSnapshot{
				Source: result.Provider, SourceVersion: result.SourceVersion, Symbol: record.Symbol,
				TradeDate: record.TradeDate, CloseMicros: record.CloseMicros, Volume: record.Volume,
				Currency: record.Currency, Adjusted: record.Adjusted, QualityStatus: QualityStatusValid,
			}
			if createErr := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&snapshot).Error; createErr != nil {
				return fmt.Errorf("persist price snapshot: %w", createErr)
			}
		}
		return nil
	})
	if err != nil {
		return ProviderResult{}, err
	}
	return result, nil
}

func readBoundedPriceInput(input io.Reader) ([]byte, error) {
	if input == nil {
		return nil, errors.New("price input is required")
	}
	limited := &io.LimitedReader{R: input, N: maxPriceCSVBytes + 1}
	payload, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("read price input: %w", err)
	}
	if limited.N <= 0 {
		return nil, fmt.Errorf("price input exceeds %d bytes", maxPriceCSVBytes)
	}
	return payload, nil
}

func readSingleCSVFromZIP(payload []byte) ([]byte, error) {
	reader, err := zip.NewReader(bytes.NewReader(payload), int64(len(payload)))
	if err != nil {
		return nil, fmt.Errorf("open price ZIP: %w", err)
	}
	var selected *zip.File
	for _, file := range reader.File {
		if file.FileInfo().IsDir() {
			continue
		}
		if !strings.HasSuffix(strings.ToLower(file.Name), ".csv") || selected != nil {
			return nil, errors.New("price ZIP must contain exactly one CSV file")
		}
		selected = file
	}
	if selected == nil {
		return nil, errors.New("price ZIP contains no CSV file")
	}
	if selected.UncompressedSize64 > maxPriceCSVBytes {
		return nil, fmt.Errorf("price ZIP CSV exceeds %d bytes", maxPriceCSVBytes)
	}
	opened, err := selected.Open()
	if err != nil {
		return nil, fmt.Errorf("open price ZIP CSV: %w", err)
	}
	defer opened.Close()
	return readBoundedPriceInput(opened)
}

func parsePriceRecords(ctx context.Context, payload []byte, format PriceFormat, options PriceValidationOptions) ([]PriceRecord, error) {
	if format != PriceFormatNormalized && format != PriceFormatStooq {
		return nil, fmt.Errorf("unsupported price format %q", format)
	}
	newYork, err := time.LoadLocation("America/New_York")
	if err != nil {
		return nil, fmt.Errorf("load America/New_York: %w", err)
	}
	symbols, err := expectedSymbolMapping(options.Expected)
	if err != nil {
		return nil, err
	}
	reader := csv.NewReader(bytes.NewReader(payload))
	reader.FieldsPerRecord = -1
	header, err := reader.Read()
	if err != nil {
		return nil, fmt.Errorf("read price CSV header: %w", err)
	}
	wantHeader := []string{"symbol", "trade_date", "open", "high", "low", "close", "volume", "currency", "is_adjusted"}
	stooqExtended := false
	if format == PriceFormatStooq {
		wantHeader = []string{"<TICKER>", "<PER>", "<DATE>", "<OPEN>", "<HIGH>", "<LOW>", "<CLOSE>", "<VOL>"}
		extendedHeader := []string{"<TICKER>", "<PER>", "<DATE>", "<TIME>", "<OPEN>", "<HIGH>", "<LOW>", "<CLOSE>", "<VOL>", "<OPENINT>"}
		stooqExtended = equalCSVHeader(header, extendedHeader)
		if stooqExtended {
			wantHeader = extendedHeader
		}
	}
	if !equalCSVHeader(header, wantHeader) {
		return nil, fmt.Errorf("price CSV header does not match %s schema", format)
	}

	records := make([]PriceRecord, 0)
	seen := make(map[string]struct{})
	for line := 2; ; line++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		row, readErr := reader.Read()
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return nil, fmt.Errorf("read price CSV line %d: %w", line, readErr)
		}
		if len(records) >= maxPriceCSVRows {
			return nil, fmt.Errorf("price CSV exceeds %d rows", maxPriceCSVRows)
		}
		if len(row) != len(wantHeader) {
			return nil, fmt.Errorf("price CSV line %d has %d columns, want %d", line, len(row), len(wantHeader))
		}
		for i := range row {
			row[i] = strings.TrimSpace(row[i])
		}
		if stooqExtended {
			row = []string{row[0], row[1], row[2], row[4], row[5], row[6], row[7], row[8]}
		}
		record, parseErr := parsePriceRow(row, format, newYork, symbols, options.Provider)
		if parseErr != nil {
			return nil, fmt.Errorf("price CSV line %d: %w", line, parseErr)
		}
		key := record.Symbol + "\x00" + record.TradeDate.Format(time.DateOnly)
		if _, exists := seen[key]; exists {
			return nil, fmt.Errorf("price CSV line %d has duplicate symbol and trade date", line)
		}
		seen[key] = struct{}{}
		records = append(records, record)
	}
	if len(records) == 0 {
		return nil, errors.New("price CSV contains no records")
	}
	return records, nil
}

func parsePriceRow(row []string, format PriceFormat, newYork *time.Location, symbols map[string]string, source string) (PriceRecord, error) {
	symbol, dateText := row[0], row[1]
	priceOffset := 2
	currency := "USD"
	adjusted := false
	if format == PriceFormatStooq {
		if row[1] != "D" {
			return PriceRecord{}, fmt.Errorf("period %q is not daily", row[1])
		}
		dateText = row[2]
		priceOffset = 3
		if len(dateText) != 8 {
			return PriceRecord{}, fmt.Errorf("invalid trade date %q", dateText)
		}
		dateText = dateText[:4] + "-" + dateText[4:6] + "-" + dateText[6:]
	} else {
		currency = row[7]
		var err error
		adjusted, err = strconv.ParseBool(row[8])
		if err != nil {
			return PriceRecord{}, fmt.Errorf("invalid is_adjusted %q", row[8])
		}
	}
	if canonical, exists := symbols[symbol]; exists {
		symbol = canonical
	}
	if symbol == "" {
		return PriceRecord{}, errors.New("symbol is required")
	}
	tradeDate, err := parseCivilDate(dateText, newYork)
	if err != nil {
		return PriceRecord{}, err
	}
	prices := make([]int64, 4)
	for i := range prices {
		prices[i], err = parsePriceMicros(row[priceOffset+i])
		if err != nil {
			return PriceRecord{}, fmt.Errorf("invalid %s price: %w", []string{"open", "high", "low", "close"}[i], err)
		}
	}
	volumeIndex := 6
	if format == PriceFormatStooq {
		volumeIndex = 7
	}
	volume, err := strconv.ParseInt(row[volumeIndex], 10, 64)
	if err != nil || volume < 0 {
		return PriceRecord{}, fmt.Errorf("invalid volume %q", row[volumeIndex])
	}
	record := PriceRecord{Symbol: symbol, TradeDate: tradeDate, OpenMicros: prices[0], HighMicros: prices[1], LowMicros: prices[2], CloseMicros: prices[3], Volume: volume, Currency: currency, Adjusted: adjusted, Source: source}
	if err := validateOHLC(record); err != nil {
		return PriceRecord{}, err
	}
	if record.Adjusted {
		return PriceRecord{}, errors.New("adjusted prices are not accepted")
	}
	if record.Currency != "USD" {
		return PriceRecord{}, fmt.Errorf("currency %q is not USD", record.Currency)
	}
	return record, nil
}

func parsePriceMicros(value string) (int64, error) {
	if value == "" || strings.HasPrefix(value, "-") {
		return 0, errors.New("price must be positive")
	}
	parts := strings.Split(value, ".")
	if len(parts) > 2 || parts[0] == "" {
		return 0, errors.New("invalid decimal")
	}
	fraction := ""
	if len(parts) == 2 {
		fraction = parts[1]
		if fraction == "" {
			return 0, errors.New("invalid decimal")
		}
	}
	if len(fraction) > 6 {
		return 0, errors.New("price precision exceeds 6 decimal places")
	}
	whole, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || whole > (int64(^uint64(0)>>1)-999999)/1_000_000 {
		return 0, errors.New("price is out of range")
	}
	fraction += strings.Repeat("0", 6-len(fraction))
	frac := int64(0)
	if fraction != "" {
		frac, err = strconv.ParseInt(fraction, 10, 64)
		if err != nil {
			return 0, errors.New("invalid decimal")
		}
	}
	micros := whole*1_000_000 + frac
	if micros <= 0 {
		return 0, errors.New("price must be positive")
	}
	return micros, nil
}

func validateOHLC(record PriceRecord) error {
	if record.OpenMicros <= 0 || record.HighMicros <= 0 || record.LowMicros <= 0 || record.CloseMicros <= 0 {
		return errors.New("OHLC prices must be positive")
	}
	if record.HighMicros < record.OpenMicros || record.HighMicros < record.LowMicros || record.HighMicros < record.CloseMicros ||
		record.LowMicros > record.OpenMicros || record.LowMicros > record.HighMicros || record.LowMicros > record.CloseMicros {
		return errors.New("invalid OHLC relationship")
	}
	return nil
}

func parseCivilDate(value string, location *time.Location) (time.Time, error) {
	if len(value) != len(time.DateOnly) {
		return time.Time{}, fmt.Errorf("invalid trade date %q: want YYYY-MM-DD", value)
	}
	parsed, err := time.ParseInLocation(time.DateOnly, value, location)
	if err != nil || parsed.Format(time.DateOnly) != value {
		return time.Time{}, fmt.Errorf("invalid trade date %q: want YYYY-MM-DD", value)
	}
	return parsed, nil
}

func expectedSymbolMapping(expected []Listing) (map[string]string, error) {
	mapping := make(map[string]string)
	for _, listing := range expected {
		canonical := strings.TrimSpace(listing.Ticker)
		provider := strings.TrimSpace(listing.ProviderTicker)
		if canonical == "" {
			continue
		}
		for _, key := range []string{canonical, provider} {
			if key == "" {
				continue
			}
			if existing, exists := mapping[key]; exists && existing != canonical {
				return nil, fmt.Errorf("provider ticker %q maps to both %q and %q", key, existing, canonical)
			}
			mapping[key] = canonical
		}
	}
	return mapping, nil
}

func validatePriceBatch(ctx context.Context, records []PriceRecord, options PriceValidationOptions) (ProviderResult, error) {
	if strings.TrimSpace(options.Provider) == "" {
		return ProviderResult{}, errors.New("price provider is required")
	}
	newYork, err := time.LoadLocation("America/New_York")
	if err != nil {
		return ProviderResult{}, err
	}
	effectiveText := options.EffectiveDate.Format(time.DateOnly)
	effectiveDate, err := parseCivilDate(effectiveText, newYork)
	if err != nil || options.EffectiveDate.IsZero() {
		return ProviderResult{}, errors.New("effective date is required")
	}
	if options.Calendar == nil {
		return ProviderResult{}, errors.New("market calendar is required")
	}
	trading, err := options.Calendar.IsTradingDate(ctx, effectiveText)
	if err != nil {
		return ProviderResult{}, fmt.Errorf("validate effective trading date: %w", err)
	}
	if !trading {
		return ProviderResult{}, fmt.Errorf("effective date %s is not a trading date", effectiveText)
	}
	covered := make(map[string]struct{})
	for _, record := range records {
		date := record.TradeDate.Format(time.DateOnly)
		isTrading, calendarErr := options.Calendar.IsTradingDate(ctx, date)
		if calendarErr != nil {
			return ProviderResult{}, fmt.Errorf("validate trade date %s: %w", date, calendarErr)
		}
		if !isTrading {
			return ProviderResult{}, fmt.Errorf("trade date %s is not a trading date", date)
		}
		if date < effectiveText {
			return ProviderResult{}, fmt.Errorf("stale trade date %s before effective date %s", date, effectiveText)
		}
		if date > effectiveText {
			return ProviderResult{}, fmt.Errorf("future trade date %s after effective date %s", date, effectiveText)
		}
		covered[record.Symbol] = struct{}{}
	}
	expectedSet := make(map[string]struct{})
	for _, listing := range options.Expected {
		if ticker := strings.TrimSpace(listing.Ticker); ticker != "" {
			expectedSet[ticker] = struct{}{}
		}
	}
	coveredExpected := 0
	for symbol := range expectedSet {
		if _, exists := covered[symbol]; exists {
			coveredExpected++
		}
	}
	coverage := 100.0
	if len(expectedSet) > 0 {
		coverage = float64(coveredExpected) * 100 / float64(len(expectedSet))
	}
	now := options.Now
	if now.IsZero() {
		return ProviderResult{}, errors.New("validation clock is required")
	}
	deadline := time.Date(effectiveDate.Year(), effectiveDate.Month(), effectiveDate.Day()+1, 12, 0, 0, 0, newYork)
	return ProviderResult{
		Provider: options.Provider, Status: ProviderStatusValidation, SourceVersion: options.SourceVersion,
		EffectiveDate: effectiveDate, Records: len(records), Expected: len(expectedSet), CoveragePct: coverage,
		ValidationErrorPct: 0, Timely: !now.After(deadline),
	}, nil
}

func equalCSVHeader(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range want {
		if strings.TrimSpace(got[i]) != want[i] {
			return false
		}
	}
	return true
}

func CompareIndependentPrices(primary, gold []PriceRecord, provenance GoldProvenance) (float64, error) {
	if err := validateGoldProvenance(provenance); err != nil {
		return 0, errors.New("independent gold provenance requires provider, HTTPS source URL, and SHA256")
	}
	goldByKey := make(map[string]PriceRecord, len(gold))
	for _, record := range gold {
		goldByKey[record.Symbol+"\x00"+record.TradeDate.Format(time.DateOnly)] = record
	}
	comparisons := 0
	totalError := float64(0)
	for _, record := range primary {
		goldRecord, exists := goldByKey[record.Symbol+"\x00"+record.TradeDate.Format(time.DateOnly)]
		if !exists || goldRecord.CloseMicros <= 0 {
			continue
		}
		difference := record.CloseMicros - goldRecord.CloseMicros
		if difference < 0 {
			difference = -difference
		}
		totalError += float64(difference) * 100 / float64(goldRecord.CloseMicros)
		comparisons++
	}
	if comparisons < MinimumIndependentGoldRows {
		return 0, fmt.Errorf("independent gold comparison has %d rows, want at least %d", comparisons, MinimumIndependentGoldRows)
	}
	return totalError / float64(comparisons), nil
}

type DownloadedPriceProviderOptions struct {
	Provider   string
	URL        string
	CacheKey   string
	Format     PriceFormat
	Downloader *Downloader
	Validation PriceValidationOptions
}

type DownloadedPriceProvider struct {
	options DownloadedPriceProviderOptions
	prior   *CacheMetadata
	mu      sync.Mutex
}

func NewDownloadedPriceProvider(options DownloadedPriceProviderOptions) (*DownloadedPriceProvider, error) {
	parsed, err := url.Parse(options.URL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
		return nil, errors.New("price provider URL must be HTTPS without user info")
	}
	if strings.TrimSpace(options.Provider) == "" || strings.TrimSpace(options.CacheKey) == "" || options.Downloader == nil {
		return nil, errors.New("price provider, cache key, and downloader are required")
	}
	if options.Format == "" {
		options.Format = PriceFormatNormalized
	}
	return &DownloadedPriceProvider{options: options}, nil
}

func (provider *DownloadedPriceProvider) Load(ctx context.Context, expected []Listing) ([]PriceRecord, ProviderResult, error) {
	provider.mu.Lock()
	defer provider.mu.Unlock()

	download, err := provider.options.Downloader.Download(ctx, provider.options.URL, provider.options.CacheKey, provider.prior)
	if err != nil {
		return nil, ProviderResult{}, err
	}
	file, err := os.Open(download.Path)
	if err != nil {
		return nil, ProviderResult{}, fmt.Errorf("open downloaded price file: %w", err)
	}
	defer file.Close()
	validation := provider.options.Validation
	validation.Provider = provider.options.Provider
	validation.SourceURL = provider.options.URL
	validation.Expected = expected
	if validation.SourceVersion == "" {
		validation.SourceVersion = strings.Trim(download.ETag, `"`)
		if validation.SourceVersion == "" {
			validation.SourceVersion = download.SHA256
		}
	}
	records, result, err := ParsePriceCSV(ctx, file, provider.options.Format, validation)
	if err != nil {
		return nil, ProviderResult{}, fmt.Errorf("validate downloaded price file: %w", err)
	}
	if result.SHA256 != download.SHA256 {
		return nil, ProviderResult{}, errors.New("downloaded price SHA256 changed during validation")
	}
	provider.prior = &CacheMetadata{
		Path: download.Path, SourceURL: download.SourceURL, CacheKey: download.CacheKey, FinalURL: download.FinalURL,
		ETag: download.ETag, LastModified: download.LastModified, SHA256: download.SHA256,
		ContentType: download.ContentType, Size: download.Size,
	}
	return records, result, nil
}

type ProviderDayResult struct {
	TradeDate time.Time
	qualified bool
}

// EvaluateProviderDay is the only constructor for external callers. A day is
// qualified only when every documented activation threshold passes and the
// independent comparison carries auditable provenance.
func EvaluateProviderDay(result ProviderResult, goldRows int, goldErrorPct float64, provenance GoldProvenance) (ProviderDayResult, error) {
	day := ProviderDayResult{TradeDate: result.EffectiveDate}
	if result.EffectiveDate.IsZero() {
		return day, errors.New("provider effective date is required")
	}
	if err := validateGoldProvenance(provenance); err != nil {
		return day, err
	}
	if goldRows < MinimumIndependentGoldRows {
		return day, fmt.Errorf("independent gold comparison has %d rows, want at least %d", goldRows, MinimumIndependentGoldRows)
	}
	if goldErrorPct < 0 {
		return day, errors.New("independent gold error percentage cannot be negative")
	}
	day.qualified = result.CoveragePct >= DefaultPriceCoveragePct &&
		result.ValidationErrorPct <= DefaultValidationErrorPct && result.Timely &&
		goldErrorPct <= DefaultIndependentErrorPct
	return day, nil
}

func validateGoldProvenance(provenance GoldProvenance) error {
	parsedURL, err := url.Parse(provenance.SourceURL)
	if strings.TrimSpace(provenance.Provider) == "" || err != nil || parsedURL.Scheme != "https" || parsedURL.Host == "" || parsedURL.User != nil || len(provenance.SHA256) != 64 {
		return errors.New("independent gold provenance requires provider, HTTPS source URL, and SHA256")
	}
	if _, err := hex.DecodeString(provenance.SHA256); err != nil {
		return errors.New("independent gold provenance SHA256 is invalid")
	}
	return nil
}

func AdvanceProviderHealth(state ProviderHealth, day ProviderDayResult) ProviderHealth {
	date := day.TradeDate.Format(time.DateOnly)
	if date == "0001-01-01" || (state.LastTradeDate != "" && date <= state.LastTradeDate) {
		return state
	}
	state.LastTradeDate = date
	if state.Status == "" {
		state.Status = ProviderStatusValidation
	}
	if day.qualified {
		state.QualifiedTradingDays++
		state.FailureStreak = 0
		if state.Status == ProviderStatusValidation && state.QualifiedTradingDays >= ProviderActivationTradingDays {
			state.Status = ProviderStatusActive
		}
	} else {
		state.FailureStreak++
		if state.Status == ProviderStatusActive && state.FailureStreak >= ProviderDegradedFailureDays {
			state.Status = ProviderStatusDegraded
		}
	}
	return state
}
