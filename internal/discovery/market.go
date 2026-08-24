package discovery

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/binary"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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
	maxPriceZIPFiles = 16

	ProviderActivationTradingDays = 20
	ProviderDegradedFailureDays   = 3
	MinimumIndependentGoldRows    = 100
	DefaultPriceCoveragePct       = 98.0
	DefaultPriceTimelyPct         = 95.0
	DefaultValidationErrorPct     = 0.0
	DefaultIndependentErrorPct    = 1.0
)

var (
	ErrPriceImportConflict    = errors.New("price import conflict")
	ErrGoldEvidenceIncomplete = errors.New("independent gold evidence incomplete")
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
	Attempts                                []ProviderAttempt
	FallbackUsed                            bool
}

type PriceProvider interface {
	Load(ctx context.Context, expected []Listing) ([]PriceRecord, ProviderResult, error)
}

type DatedPriceProvider interface {
	PriceProvider
	LoadForDate(ctx context.Context, expected []Listing, effectiveDate string) ([]PriceRecord, ProviderResult, error)
}

// HistoricalPriceProvider loads a bounded daily-price window for one-time
// research warm-up. It is deliberately separate from PriceProvider so normal
// market publishing continues to validate only its effective trading day.
type HistoricalPriceProvider interface {
	PriceProvider
	LoadHistory(ctx context.Context, expected []Listing, effectiveDate string, lookbackDays int) ([]PriceRecord, error)
}

type NamedPriceProvider interface {
	PriceProvider
	ProviderName() string
}

type RecordSourceAllowlistProvider interface {
	AllowedRecordSources() []string
}

type PriceValidationOptions struct {
	Provider                      string
	SourceVersion                 string
	SourceURL                     string
	EffectiveDate                 time.Time
	Now                           time.Time
	Calendar                      MarketCalendar
	Expected                      []Listing
	AllowPreviousTradingDatePrice bool
}

type GoldValidationResult struct {
	ready     bool
	rows      int
	errorPct  float64
	sha256    string
	reviewers int
	caseTypes int
}

//go:embed testdata/gold/market_price_validation.csv
var frozenMarketGoldCSV []byte

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
	snapshots := make([]PriceSnapshot, len(records))
	for index, record := range records {
		snapshots[index] = PriceSnapshot{
			Source: record.Source, SourceVersion: result.SourceVersion, Symbol: record.Symbol,
			TradeDate: record.TradeDate, OpenMicros: record.OpenMicros, HighMicros: record.HighMicros,
			LowMicros: record.LowMicros, CloseMicros: record.CloseMicros, Volume: record.Volume,
			Currency: record.Currency, Adjusted: record.Adjusted, QualityStatus: QualityStatusValid,
		}
	}
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return persistPriceSnapshotsInBatches(tx, snapshots)
	})
	if err != nil {
		return ProviderResult{}, err
	}
	return result, nil
}

const priceImportBatchSize = 400

func persistPriceSnapshotsInBatches(tx *gorm.DB, snapshots []PriceSnapshot) error {
	type sourceKey struct{ source, version string }
	groups := make(map[sourceKey][]PriceSnapshot)
	groupOrder := make([]sourceKey, 0)
	for _, snapshot := range snapshots {
		key := sourceKey{source: snapshot.Source, version: snapshot.SourceVersion}
		if _, exists := groups[key]; !exists {
			groupOrder = append(groupOrder, key)
		}
		groups[key] = append(groups[key], snapshot)
	}
	for _, key := range groupOrder {
		group := groups[key]
		for start := 0; start < len(group); start += priceImportBatchSize {
			end := start + priceImportBatchSize
			if end > len(group) {
				end = len(group)
			}
			chunk := group[start:end]
			if err := tx.Clauses(priceSnapshotConflictClause()).CreateInBatches(chunk, priceImportBatchSize).Error; err != nil {
				return fmt.Errorf("persist price snapshot batch: %w", err)
			}
			persisted, err := loadPriceSnapshotChunk(tx, chunk)
			if err != nil {
				return err
			}
			for _, expected := range chunk {
				actual, exists := persisted[priceSnapshotDayKey(expected.Symbol, expected.TradeDate)]
				if !exists {
					return fmt.Errorf("persisted price snapshot is missing for %s %s", expected.Symbol, expected.TradeDate.Format(time.DateOnly))
				}
				if actual.OpenMicros != expected.OpenMicros || actual.HighMicros != expected.HighMicros || actual.LowMicros != expected.LowMicros || actual.CloseMicros != expected.CloseMicros || actual.Volume != expected.Volume || actual.Currency != expected.Currency || actual.Adjusted != expected.Adjusted || actual.QualityStatus != expected.QualityStatus {
					return fmt.Errorf(
						"%w for %s %s (source=%s version=%s; existing ohlc=%d/%d/%d/%d volume=%d currency=%s adjusted=%t quality=%s; incoming ohlc=%d/%d/%d/%d volume=%d currency=%s adjusted=%t quality=%s)",
						ErrPriceImportConflict,
						expected.Symbol,
						expected.TradeDate.Format(time.DateOnly),
						expected.Source,
						expected.SourceVersion,
						actual.OpenMicros,
						actual.HighMicros,
						actual.LowMicros,
						actual.CloseMicros,
						actual.Volume,
						actual.Currency,
						actual.Adjusted,
						actual.QualityStatus,
						expected.OpenMicros,
						expected.HighMicros,
						expected.LowMicros,
						expected.CloseMicros,
						expected.Volume,
						expected.Currency,
						expected.Adjusted,
						expected.QualityStatus,
					)
				}
			}
		}
	}
	return nil
}

// persistPriceSnapshotsWithQuarantine is used by automated provider jobs.
// One non-deterministic provider row must not roll back hundreds of unrelated
// valid symbols. Manual CSV imports keep using the strict function above so an
// operator still receives immediate feedback about a conflicting file.
func persistPriceSnapshotsWithQuarantine(tx *gorm.DB, snapshots []PriceSnapshot, observedAt time.Time) (int, error) {
	quarantined := 0
	for index := range snapshots {
		snapshot := snapshots[index]
		err := persistPriceSnapshotsInBatches(tx, []PriceSnapshot{snapshot})
		if err == nil {
			if resolveErr := resolvePriceQualityIncidents(tx, snapshot, observedAt); resolveErr != nil {
				return quarantined, resolveErr
			}
			continue
		}
		if !errors.Is(err, ErrPriceImportConflict) {
			return quarantined, err
		}
		quarantined++
		if recordErr := recordPriceQualityIncident(tx, snapshot, err, observedAt); recordErr != nil {
			return quarantined, recordErr
		}
	}
	return quarantined, nil
}

func resolvePriceQualityIncidents(tx *gorm.DB, snapshot PriceSnapshot, resolvedAt time.Time) error {
	if resolvedAt.IsZero() {
		resolvedAt = time.Now().UTC()
	}
	entityKey := strings.ToUpper(strings.TrimSpace(snapshot.Symbol)) + ":" + snapshot.TradeDate.Format(time.DateOnly)
	return tx.Model(&DataQualityIncident{}).
		Where("domain = ? AND entity_key = ? AND source = ? AND status = ?", "price", entityKey, snapshot.Source, DataQualityIncidentOpen).
		Updates(map[string]interface{}{"status": DataQualityIncidentResolved, "retryable": false, "resolved_at": resolvedAt.UTC(), "updated_at": resolvedAt.UTC()}).Error
}

func recordPriceQualityIncident(tx *gorm.DB, snapshot PriceSnapshot, conflict error, observedAt time.Time) error {
	if observedAt.IsZero() {
		observedAt = time.Now().UTC()
	}
	entityKey := strings.ToUpper(strings.TrimSpace(snapshot.Symbol)) + ":" + snapshot.TradeDate.Format(time.DateOnly)
	fingerprintPayload := strings.Join([]string{"price", snapshot.Source, snapshot.SourceVersion, entityKey, ReasonPriceConflict}, "\x00")
	fingerprintBytes := sha256.Sum256([]byte(fingerprintPayload))
	incident := DataQualityIncident{
		Fingerprint: hex.EncodeToString(fingerprintBytes[:]), Layer: DataLayerFact, Domain: "price", EntityKey: entityKey,
		Reason: ReasonPriceConflict, Source: snapshot.Source, SourceVersion: snapshot.SourceVersion,
		Status: DataQualityIncidentOpen, Retryable: true, OccurrenceCount: 1,
		Detail: conflict.Error(), FirstObservedAt: observedAt.UTC(), LastObservedAt: observedAt.UTC(),
	}
	return tx.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "fingerprint"}},
		DoUpdates: clause.Assignments(map[string]interface{}{
			"status": DataQualityIncidentOpen, "retryable": true, "detail": incident.Detail,
			"last_observed_at": incident.LastObservedAt, "resolved_at": nil,
			"occurrence_count": gorm.Expr("occurrence_count + 1"), "updated_at": incident.LastObservedAt,
		}),
	}).Create(&incident).Error
}

// priceSnapshotConflictClause keeps provider snapshots immutable, with one
// deliberately narrow exception for rows written before OHLC persistence was
// introduced. A replay may fill the previously empty open/high/low columns
// only when every already-persisted value still matches the incoming record.
// The validation below the insert continues to reject every other conflict.
func priceSnapshotConflictClause() clause.OnConflict {
	return clause.OnConflict{
		Columns: []clause.Column{
			{Name: "source"},
			{Name: "source_version"},
			{Name: "symbol"},
			{Name: "trade_date"},
		},
		DoUpdates: clause.Assignments(map[string]interface{}{
			"open_micros": gorm.Expr("excluded.open_micros"),
			"high_micros": gorm.Expr("excluded.high_micros"),
			"low_micros":  gorm.Expr("excluded.low_micros"),
		}),
		Where: clause.Where{Exprs: []clause.Expression{clause.Expr{SQL: `
			COALESCE(open_micros, 0) = 0 AND COALESCE(high_micros, 0) = 0 AND COALESCE(low_micros, 0) = 0
			AND excluded.open_micros > 0 AND excluded.high_micros > 0 AND excluded.low_micros > 0
			AND close_micros = excluded.close_micros
			AND volume = excluded.volume
			AND currency = excluded.currency
			AND adjusted = excluded.adjusted
			AND quality_status = excluded.quality_status
		`}}},
	}
}

func loadPriceSnapshotChunk(tx *gorm.DB, chunk []PriceSnapshot) (map[string]PriceSnapshot, error) {
	where := make([]string, 0, len(chunk))
	arguments := make([]any, 0, len(chunk)*2+2)
	arguments = append(arguments, chunk[0].Source, chunk[0].SourceVersion)
	for _, snapshot := range chunk {
		where = append(where, "(symbol = ? AND trade_date = ?)")
		arguments = append(arguments, snapshot.Symbol, snapshot.TradeDate)
	}
	var rows []PriceSnapshot
	query := "source = ? AND source_version = ? AND (" + strings.Join(where, " OR ") + ")"
	if err := tx.Where(query, arguments...).Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("read persisted price snapshot batch: %w", err)
	}
	result := make(map[string]PriceSnapshot, len(rows))
	for _, row := range rows {
		result[priceSnapshotDayKey(row.Symbol, row.TradeDate)] = row
	}
	return result, nil
}

func priceSnapshotDayKey(symbol string, date time.Time) string {
	return symbol + "\x00" + date.Format(time.DateOnly)
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
	entryCount, err := zipCentralDirectoryEntryCount(payload)
	if err != nil {
		return nil, err
	}
	if entryCount > maxPriceZIPFiles {
		return nil, fmt.Errorf("price ZIP exceeds %d entries", maxPriceZIPFiles)
	}
	reader, err := zip.NewReader(bytes.NewReader(payload), int64(len(payload)))
	if err != nil {
		return nil, fmt.Errorf("open price ZIP: %w", err)
	}
	if len(reader.File) > maxPriceZIPFiles {
		return nil, fmt.Errorf("price ZIP exceeds %d entries", maxPriceZIPFiles)
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

func zipCentralDirectoryEntryCount(payload []byte) (int, error) {
	const (
		eocdSignature = uint32(0x06054b50)
		eocdSize      = 22
		maxComment    = 1<<16 - 1
	)
	start := len(payload) - eocdSize
	minimum := start - maxComment
	if minimum < 0 {
		minimum = 0
	}
	for offset := start; offset >= minimum; offset-- {
		if binary.LittleEndian.Uint32(payload[offset:offset+4]) != eocdSignature {
			continue
		}
		commentLength := int(binary.LittleEndian.Uint16(payload[offset+20 : offset+22]))
		if offset+eocdSize+commentLength != len(payload) {
			continue
		}
		if binary.LittleEndian.Uint16(payload[offset+4:offset+6]) != 0 || binary.LittleEndian.Uint16(payload[offset+6:offset+8]) != 0 {
			return 0, errors.New("multi-disk price ZIPs are not supported")
		}
		entriesOnDisk := binary.LittleEndian.Uint16(payload[offset+8 : offset+10])
		count := binary.LittleEndian.Uint16(payload[offset+10 : offset+12])
		centralSize := binary.LittleEndian.Uint32(payload[offset+12 : offset+16])
		centralOffset := binary.LittleEndian.Uint32(payload[offset+16 : offset+20])
		if count == ^uint16(0) || centralSize == ^uint32(0) || centralOffset == ^uint32(0) {
			return 0, errors.New("ZIP64 price archives are not supported")
		}
		if entriesOnDisk != count || uint64(centralOffset)+uint64(centralSize) != uint64(offset) {
			return 0, errors.New("price ZIP central directory is inconsistent")
		}
		if int(count) > maxPriceZIPFiles {
			return int(count), nil
		}
		if !validateZIPCentralDirectory(payload, int(centralOffset), int(centralSize), int(count)) {
			return 0, errors.New("price ZIP central directory is invalid")
		}
		return int(count), nil
	}
	return 0, errors.New("price ZIP end record is missing")
}

func validateZIPCentralDirectory(payload []byte, offset, size, entries int) bool {
	if offset < 0 || size < 0 || offset > len(payload) || size > len(payload)-offset {
		return false
	}
	position := offset
	end := offset + size
	for entry := 0; entry < entries; entry++ {
		if position > end-46 || binary.LittleEndian.Uint32(payload[position:position+4]) != 0x02014b50 {
			return false
		}
		nameLength := int(binary.LittleEndian.Uint16(payload[position+28 : position+30]))
		extraLength := int(binary.LittleEndian.Uint16(payload[position+30 : position+32]))
		commentLength := int(binary.LittleEndian.Uint16(payload[position+32 : position+34]))
		position += 46 + nameLength + extraLength + commentLength
		if position > end {
			return false
		}
	}
	return position == end
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
	wantHeader := []string{"symbol", "trade_date", "open", "high", "low", "close", "volume", "currency", "is_adjusted", "source"}
	stooqExtended := false
	normalizedCloseOnly := false
	if format == PriceFormatStooq {
		wantHeader = []string{"<TICKER>", "<PER>", "<DATE>", "<OPEN>", "<HIGH>", "<LOW>", "<CLOSE>", "<VOL>"}
		extendedHeader := []string{"<TICKER>", "<PER>", "<DATE>", "<TIME>", "<OPEN>", "<HIGH>", "<LOW>", "<CLOSE>", "<VOL>", "<OPENINT>"}
		stooqExtended = equalCSVHeader(header, extendedHeader)
		if stooqExtended {
			wantHeader = extendedHeader
		}
	} else if equalCSVHeader(header, []string{"symbol", "trade_date", "close", "currency", "is_adjusted", "source"}) {
		wantHeader = []string{"symbol", "trade_date", "close", "currency", "is_adjusted", "source"}
		normalizedCloseOnly = true
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
		record, parseErr := parsePriceRow(row, format, normalizedCloseOnly, newYork, symbols, options.Provider)
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

func parsePriceRow(row []string, format PriceFormat, normalizedCloseOnly bool, newYork *time.Location, symbols map[string]string, source string) (PriceRecord, error) {
	symbol, dateText := strings.ToUpper(row[0]), row[1]
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
	} else if normalizedCloseOnly {
		currency = row[3]
		var err error
		adjusted, err = strconv.ParseBool(row[4])
		if err != nil {
			return PriceRecord{}, fmt.Errorf("invalid is_adjusted %q", row[4])
		}
		source = row[5]
	} else {
		currency = row[7]
		var err error
		adjusted, err = strconv.ParseBool(row[8])
		if err != nil {
			return PriceRecord{}, fmt.Errorf("invalid is_adjusted %q", row[8])
		}
		source = row[9]
	}
	if canonical, exists := symbols[symbol]; exists {
		symbol = canonical
	} else if len(symbols) > 0 {
		return PriceRecord{}, fmt.Errorf("symbol %q has no active ticker mapping", symbol)
	}
	if symbol == "" {
		return PriceRecord{}, errors.New("symbol is required")
	}
	tradeDate, err := parseCivilDate(dateText, newYork)
	if err != nil {
		return PriceRecord{}, err
	}
	prices := make([]int64, 4)
	volume := int64(0)
	if normalizedCloseOnly {
		prices[3], err = parsePriceMicros(row[2])
		if err != nil {
			return PriceRecord{}, fmt.Errorf("invalid close price: %w", err)
		}
		prices[0], prices[1], prices[2] = prices[3], prices[3], prices[3]
	} else {
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
		volume, err = strconv.ParseInt(row[volumeIndex], 10, 64)
		if err != nil || volume < 0 {
			return PriceRecord{}, fmt.Errorf("invalid volume %q", row[volumeIndex])
		}
	}
	source = strings.TrimSpace(source)
	if source == "" {
		return PriceRecord{}, errors.New("source is required")
	}
	if len(source) > 64 {
		return PriceRecord{}, errors.New("source exceeds 64 bytes")
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
	if len(parts) > 2 || !asciiDigits(parts[0]) {
		return 0, errors.New("invalid decimal")
	}
	fraction := ""
	if len(parts) == 2 {
		fraction = parts[1]
		if !asciiDigits(fraction) {
			return 0, errors.New("invalid decimal")
		}
	}
	whole, err := strconv.ParseInt(parts[0], 10, 64)
	const maxInt64 = int64(^uint64(0) >> 1)
	if err != nil || whole > maxInt64/1_000_000 {
		return 0, errors.New("price is out of range")
	}
	roundUp := false
	if len(fraction) > 6 {
		roundUp = fraction[6] >= '5'
		fraction = fraction[:6]
	}
	fraction += strings.Repeat("0", 6-len(fraction))
	frac := int64(0)
	if fraction != "" {
		frac, err = strconv.ParseInt(fraction, 10, 64)
		if err != nil {
			return 0, errors.New("invalid decimal")
		}
	}
	if roundUp {
		frac++
		if frac == 1_000_000 {
			if whole >= maxInt64/1_000_000 {
				return 0, errors.New("price is out of range")
			}
			whole++
			frac = 0
		}
	}
	if whole == maxInt64/1_000_000 && frac > maxInt64%1_000_000 {
		return 0, errors.New("price is out of range")
	}
	micros := whole*1_000_000 + frac
	if micros <= 0 {
		return 0, errors.New("price must be positive")
	}
	return micros, nil
}

func asciiDigits(value string) bool {
	if value == "" {
		return false
	}
	for index := 0; index < len(value); index++ {
		if value[index] < '0' || value[index] > '9' {
			return false
		}
	}
	return true
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
		if listing.MappingStatus == MappingStatusConflict {
			return nil, fmt.Errorf("ticker %q has mapping conflict", listing.Ticker)
		}
		if listing.MappingStatus == MappingStatusExpired {
			return nil, fmt.Errorf("ticker %q mapping is expired", listing.Ticker)
		}
		canonical := strings.ToUpper(strings.TrimSpace(listing.Ticker))
		provider := strings.ToUpper(strings.TrimSpace(listing.ProviderTicker))
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
	if !trading && !options.AllowPreviousTradingDatePrice {
		return ProviderResult{}, fmt.Errorf("effective date %s is not a trading date", effectiveText)
	}
	covered := make(map[string]struct{})
	previousDate := ""
	if options.AllowPreviousTradingDatePrice {
		previous, previousErr := previousTradingDate(ctx, options.Calendar, effectiveDate)
		if previousErr != nil {
			return ProviderResult{}, fmt.Errorf("find previous trading date: %w", previousErr)
		}
		previousDate = previous.Format(time.DateOnly)
	}
	for _, record := range records {
		date := record.TradeDate.Format(time.DateOnly)
		isTrading, calendarErr := options.Calendar.IsTradingDate(ctx, date)
		if calendarErr != nil {
			return ProviderResult{}, fmt.Errorf("validate trade date %s: %w", date, calendarErr)
		}
		if !isTrading {
			return ProviderResult{}, fmt.Errorf("trade date %s is not a trading date", date)
		}
		if date < effectiveText && (!options.AllowPreviousTradingDatePrice || date != previousDate) {
			return ProviderResult{}, fmt.Errorf("stale trade date %s before effective date %s", date, effectiveText)
		}
		if date > effectiveText {
			return ProviderResult{}, fmt.Errorf("future trade date %s after effective date %s", date, effectiveText)
		}
		covered[record.Symbol] = struct{}{}
	}
	expectedSet := make(map[string]struct{})
	for _, listing := range options.Expected {
		if ticker := strings.ToUpper(strings.TrimSpace(listing.Ticker)); ticker != "" {
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
	nextDate, err := nextTradingDate(ctx, options.Calendar, effectiveDate)
	if err != nil {
		return ProviderResult{}, fmt.Errorf("find next trading date: %w", err)
	}
	deadline := time.Date(nextDate.Year(), nextDate.Month(), nextDate.Day(), 12, 0, 0, 0, newYork)
	return ProviderResult{
		Provider: options.Provider, Status: ProviderStatusValidation, SourceVersion: options.SourceVersion,
		EffectiveDate: effectiveDate, Records: len(records), Expected: len(expectedSet), CoveragePct: coverage,
		ValidationErrorPct: 0, Timely: !now.After(deadline),
	}, nil
}

func previousTradingDate(ctx context.Context, calendar MarketCalendar, before time.Time) (time.Time, error) {
	for offset := 1; offset <= 14; offset++ {
		candidate := before.AddDate(0, 0, -offset)
		trading, err := calendar.IsTradingDate(ctx, candidate.Format(time.DateOnly))
		if err != nil {
			return time.Time{}, err
		}
		if trading {
			return candidate, nil
		}
	}
	return time.Time{}, errors.New("previous trading date not found")
}

func nextTradingDate(ctx context.Context, calendar MarketCalendar, after time.Time) (time.Time, error) {
	for offset := 1; offset <= 14; offset++ {
		candidate := after.AddDate(0, 0, offset)
		trading, err := calendar.IsTradingDate(ctx, candidate.Format(time.DateOnly))
		if err != nil {
			return time.Time{}, err
		}
		if trading {
			return candidate, nil
		}
	}
	return time.Time{}, errors.New("no trading date found within 14 calendar days")
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

func LoadFrozenMarketGold(primary []PriceRecord, primaryProvider string, now time.Time) (GoldValidationResult, error) {
	// primary is retained for source compatibility. Frozen audit evidence is
	// intentionally self-contained and must not be joined to today's records.
	gold, err := validateIndependentGoldCSV(bytes.NewReader(frozenMarketGoldCSV), primaryProvider, now)
	if err != nil && strings.Contains(err.Error(), "primary provider does not match provider result") {
		return gold, fmt.Errorf("%w: %v", ErrGoldEvidenceIncomplete, err)
	}
	return gold, err
}

func validateIndependentGoldCSV(input io.Reader, primaryProvider string, now time.Time) (GoldValidationResult, error) {
	payload, err := readBoundedPriceInput(input)
	if err != nil {
		return GoldValidationResult{}, err
	}
	if strings.TrimSpace(primaryProvider) == "" || now.IsZero() {
		return GoldValidationResult{}, errors.New("primary provider and validation clock are required")
	}
	reader := csv.NewReader(bytes.NewReader(payload))
	reader.FieldsPerRecord = 12
	header, err := reader.Read()
	if err != nil || !equalCSVHeader(header, []string{"symbol", "trade_date", "primary_close", "expected_close", "primary_provider", "source_url", "observed_at", "reviewer", "source_provider", "source_tier", "fallback_reason", "case_type"}) {
		return GoldValidationResult{}, errors.New("independent gold CSV header is invalid")
	}
	newYork, err := time.LoadLocation("America/New_York")
	if err != nil {
		return GoldValidationResult{}, err
	}
	seen := make(map[string]struct{})
	symbols := make(map[string]struct{})
	reviewers := make(map[string]struct{})
	caseTypes := make(map[string]struct{})
	maximumError := float64(0)
	rows := 0
	for line := 2; ; line++ {
		row, readErr := reader.Read()
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return GoldValidationResult{}, fmt.Errorf("read independent gold line %d: %w", line, readErr)
		}
		for i := range row {
			row[i] = strings.TrimSpace(row[i])
		}
		symbol := strings.ToUpper(row[0])
		tradeDate, dateErr := parseCivilDate(row[1], newYork)
		primaryClose, primaryPriceErr := parsePriceMicros(row[2])
		expectedClose, priceErr := parsePriceMicros(row[3])
		sourceURL, urlErr := url.Parse(row[5])
		observedAt, observedErr := time.Parse(time.RFC3339, row[6])
		if symbol == "" || dateErr != nil || primaryPriceErr != nil || priceErr != nil || urlErr != nil || sourceURL.Scheme != "https" || sourceURL.Host == "" || sourceURL.User != nil || observedErr != nil || observedAt.After(now) {
			return GoldValidationResult{}, fmt.Errorf("independent gold line %d has invalid evidence", line)
		}
		if !strings.EqualFold(row[4], primaryProvider) {
			return GoldValidationResult{}, fmt.Errorf("independent gold line %d primary provider does not match provider result", line)
		}
		if row[7] == "" || row[8] == "" || strings.EqualFold(row[8], primaryProvider) {
			return GoldValidationResult{}, fmt.Errorf("independent gold line %d lacks an independent provider or reviewer", line)
		}
		switch row[9] {
		case "exchange", "issuer_ir":
			if row[10] != "" {
				return GoldValidationResult{}, fmt.Errorf("independent gold line %d has an unexpected fallback reason", line)
			}
		case "other":
			if row[10] == "" {
				return GoldValidationResult{}, fmt.Errorf("independent gold line %d requires a fallback reason", line)
			}
		default:
			return GoldValidationResult{}, fmt.Errorf("independent gold line %d has invalid source tier", line)
		}
		switch row[11] {
		case "split", "ticker_change", "multi_class", "delisted":
		default:
			return GoldValidationResult{}, fmt.Errorf("independent gold line %d has invalid case type", line)
		}
		key := symbol + "\x00" + tradeDate.Format(time.DateOnly)
		if _, exists := seen[key]; exists {
			return GoldValidationResult{}, fmt.Errorf("independent gold line %d is duplicated", line)
		}
		seen[key] = struct{}{}
		difference := primaryClose - expectedClose
		if difference < 0 {
			difference = -difference
		}
		errorPct := float64(difference) * 100 / float64(expectedClose)
		if errorPct > maximumError {
			maximumError = errorPct
		}
		symbols[symbol] = struct{}{}
		reviewers[row[7]] = struct{}{}
		caseTypes[row[11]] = struct{}{}
		rows++
	}
	if rows < MinimumIndependentGoldRows {
		return GoldValidationResult{}, fmt.Errorf("%w: comparison has %d rows, want at least %d", ErrGoldEvidenceIncomplete, rows, MinimumIndependentGoldRows)
	}
	if len(symbols) < MinimumIndependentGoldRows {
		return GoldValidationResult{}, fmt.Errorf("%w: covers %d securities, want at least %d", ErrGoldEvidenceIncomplete, len(symbols), MinimumIndependentGoldRows)
	}
	if len(caseTypes) != 4 {
		return GoldValidationResult{}, fmt.Errorf("%w: required case types are missing", ErrGoldEvidenceIncomplete)
	}
	hash := sha256.Sum256(payload)
	return GoldValidationResult{ready: maximumError <= DefaultIndependentErrorPct, rows: rows, errorPct: maximumError, sha256: hex.EncodeToString(hash[:]), reviewers: len(reviewers), caseTypes: len(caseTypes)}, nil
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
	options     DownloadedPriceProviderOptions
	prior       *CacheMetadata
	initialized bool
}

func (provider *DownloadedPriceProvider) ProviderName() string { return provider.options.Provider }

var priceProviderLifecycleLocks Downloader

func NewDownloadedPriceProvider(options DownloadedPriceProviderOptions) (*DownloadedPriceProvider, error) {
	parsed, err := url.Parse(options.URL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
		return nil, errors.New("price provider URL must be HTTPS without user info")
	}
	if strings.TrimSpace(options.Provider) == "" || !safeCacheKey(options.CacheKey) || options.Downloader == nil {
		return nil, errors.New("price provider, cache key, and downloader are required")
	}
	if options.Format == "" {
		options.Format = PriceFormatNormalized
	}
	return &DownloadedPriceProvider{options: options}, nil
}

func (provider *DownloadedPriceProvider) Load(ctx context.Context, expected []Listing) ([]PriceRecord, ProviderResult, error) {
	unlock, err := priceProviderLifecycleLocks.lockCachePath(ctx, provider.candidateDataPath())
	if err != nil {
		return nil, ProviderResult{}, fmt.Errorf("wait for price provider cache lifecycle: %w", err)
	}
	defer unlock()
	if err := provider.initializeValidatedCache(); err != nil {
		return nil, ProviderResult{}, err
	}

	candidateKey := provider.options.CacheKey + ".candidate"
	download, err := provider.options.Downloader.Download(ctx, provider.options.URL, candidateKey, provider.prior)
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
		_ = provider.restoreValidatedCandidate()
		return nil, ProviderResult{}, fmt.Errorf("validate downloaded price file: %w", err)
	}
	if result.SHA256 != download.SHA256 {
		_ = provider.restoreValidatedCandidate()
		return nil, ProviderResult{}, errors.New("downloaded price SHA256 changed during validation")
	}
	if err := provider.promoteValidatedDownload(download); err != nil {
		_ = provider.restoreValidatedCandidate()
		return nil, ProviderResult{}, err
	}
	provider.prior = &CacheMetadata{
		Path: download.Path, SourceURL: download.SourceURL, CacheKey: download.CacheKey, FinalURL: download.FinalURL,
		ETag: download.ETag, LastModified: download.LastModified, SHA256: download.SHA256,
		ContentType: download.ContentType, Size: download.Size,
	}
	return records, result, nil
}

type validatedPriceCacheState struct {
	SourceURL, FinalURL, ETag, LastModified, SHA256, ContentType string
	Size                                                         int64
}

func (provider *DownloadedPriceProvider) initializeValidatedCache() error {
	if provider.initialized {
		return nil
	}
	stateBytes, err := os.ReadFile(provider.validatedStatePath())
	if errors.Is(err, os.ErrNotExist) {
		provider.initialized = true
		return nil
	}
	if err != nil {
		return fmt.Errorf("read validated price cache metadata: %w", err)
	}
	var state validatedPriceCacheState
	if err := json.Unmarshal(stateBytes, &state); err != nil {
		return fmt.Errorf("decode validated price cache metadata: %w", err)
	}
	if state.SourceURL != provider.options.URL || !validSHA256(state.SHA256) || state.Size < 0 {
		return errors.New("validated price cache metadata does not match provider")
	}
	validatedPath := provider.validatedDataPathForSHA(state.SHA256)
	validated := CacheMetadata{Path: validatedPath, SourceURL: state.SourceURL, CacheKey: provider.options.CacheKey + ".validated." + state.SHA256, FinalURL: state.FinalURL, ETag: state.ETag, LastModified: state.LastModified, SHA256: state.SHA256, ContentType: state.ContentType, Size: state.Size}
	if err := verifyCachedFile(&validated, validated.Path, validated.SourceURL, validated.CacheKey); err != nil {
		return fmt.Errorf("verify validated price cache: %w", err)
	}
	if err := copyFileAtomically(validated.Path, provider.candidateDataPath()); err != nil {
		return fmt.Errorf("restore validated price candidate: %w", err)
	}
	provider.prior = &CacheMetadata{Path: provider.candidateDataPath(), SourceURL: state.SourceURL, CacheKey: provider.options.CacheKey + ".candidate", FinalURL: state.FinalURL, ETag: state.ETag, LastModified: state.LastModified, SHA256: state.SHA256, ContentType: state.ContentType, Size: state.Size}
	provider.initialized = true
	return nil
}

func (provider *DownloadedPriceProvider) promoteValidatedDownload(download DownloadResult) error {
	if err := copyFileAtomically(download.Path, provider.validatedDataPathForSHA(download.SHA256)); err != nil {
		return fmt.Errorf("promote validated price cache: %w", err)
	}
	state := validatedPriceCacheState{SourceURL: provider.options.URL, FinalURL: download.FinalURL, ETag: download.ETag, LastModified: download.LastModified, SHA256: download.SHA256, ContentType: download.ContentType, Size: download.Size}
	encoded, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("encode validated price cache metadata: %w", err)
	}
	if err := writeFileAtomically(provider.validatedStatePath(), encoded); err != nil {
		return fmt.Errorf("persist validated price cache metadata: %w", err)
	}
	return nil
}

func (provider *DownloadedPriceProvider) restoreValidatedCandidate() error {
	if provider.prior == nil {
		_ = os.Remove(provider.candidateDataPath())
		return nil
	}
	return copyFileAtomically(provider.validatedDataPathForSHA(provider.prior.SHA256), provider.candidateDataPath())
}

func (provider *DownloadedPriceProvider) candidateDataPath() string {
	return absolutePriceCachePath(provider.options.Downloader.CacheDir, provider.options.CacheKey+".candidate")
}

func (provider *DownloadedPriceProvider) validatedDataPath() string {
	if provider.prior == nil {
		return provider.validatedDataPathForSHA("")
	}
	return provider.validatedDataPathForSHA(provider.prior.SHA256)
}

func (provider *DownloadedPriceProvider) validatedDataPathForSHA(sha string) string {
	return absolutePriceCachePath(provider.options.Downloader.CacheDir, provider.options.CacheKey+".validated."+sha)
}

func (provider *DownloadedPriceProvider) validatedStatePath() string {
	return absolutePriceCachePath(provider.options.Downloader.CacheDir, provider.options.CacheKey+".validated.json")
}

func absolutePriceCachePath(directory, name string) string {
	path, err := filepath.Abs(filepath.Join(directory, name))
	if err != nil {
		return filepath.Join(directory, name)
	}
	return path
}

func validSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func copyFileAtomically(source, destination string) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	info, err := input.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return errors.New("price cache source is not a regular file")
	}
	temp, err := os.CreateTemp(filepath.Dir(destination), ".price-cache-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer func() { _ = os.Remove(tempPath) }()
	if _, err := io.Copy(temp, input); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tempPath, destination); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(destination))
}

func writeFileAtomically(destination string, payload []byte) error {
	temp, err := os.CreateTemp(filepath.Dir(destination), ".price-state-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer func() { _ = os.Remove(tempPath) }()
	if _, err := temp.Write(payload); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tempPath, destination); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(destination))
}

type ProviderDayResult struct {
	TradeDate    time.Time
	coveragePct  float64
	timely       bool
	validationOK bool
	goldReady    bool
	goldSHA256   string
}

// EvaluateProviderDay validates only the compiled frozen-gold file and binds
// it to result.Provider and the parsed primary records. Incomplete evidence is
// a normal validation state, not an activation error.
func EvaluateProviderDay(result ProviderResult, primary []PriceRecord, now time.Time) (ProviderDayResult, error) {
	hash := sha256.Sum256(frozenMarketGoldCSV)
	tradeDate := result.EffectiveDate
	if len(primary) > 0 {
		tradeDate = latestPriceRecordDate(primary)
	}
	day := ProviderDayResult{
		TradeDate: tradeDate, coveragePct: result.CoveragePct, timely: result.Timely,
		validationOK: result.ValidationErrorPct <= DefaultValidationErrorPct, goldSHA256: hex.EncodeToString(hash[:]),
	}
	if result.EffectiveDate.IsZero() {
		return day, errors.New("provider effective date is required")
	}
	if result.Expected <= 0 {
		return day, errors.New("provider activation requires a non-empty expected universe")
	}
	gold, err := LoadFrozenMarketGold(primary, result.Provider, now)
	if err != nil {
		if errors.Is(err, ErrGoldEvidenceIncomplete) {
			return day, nil
		}
		return day, fmt.Errorf("validate frozen market gold: %w", err)
	}
	day.goldReady = gold.ready && gold.rows >= MinimumIndependentGoldRows && gold.errorPct >= 0 && gold.errorPct <= DefaultIndependentErrorPct && gold.caseTypes == 4
	return day, nil
}

type providerWindowDay struct {
	Date         string  `json:"date"`
	CoveragePct  float64 `json:"coverage_pct"`
	Timely       bool    `json:"timely"`
	ValidationOK bool    `json:"validation_ok"`
	GoldReady    bool    `json:"gold_ready"`
}

func AdvanceProviderHealth(ctx context.Context, calendar MarketCalendar, state ProviderHealth, day ProviderDayResult) (ProviderHealth, error) {
	if calendar == nil {
		return state, errors.New("market calendar is required")
	}
	date := day.TradeDate.Format(time.DateOnly)
	if date == "0001-01-01" {
		return state, errors.New("provider trade date is required")
	}
	trading, err := calendar.IsTradingDate(ctx, date)
	if err != nil {
		return state, fmt.Errorf("validate provider trading date: %w", err)
	}
	if !trading {
		return state, fmt.Errorf("provider date %s is not a trading date", date)
	}
	if state.LastTradeDate != "" && date <= state.LastTradeDate {
		return state, fmt.Errorf("provider date %s must be after %s", date, state.LastTradeDate)
	}
	var window []providerWindowDay
	if state.WindowJSON != "" {
		if err := json.Unmarshal([]byte(state.WindowJSON), &window); err != nil {
			return state, fmt.Errorf("decode provider health window: %w", err)
		}
	}
	if err := validateProviderHealthWindow(ctx, calendar, state, window); err != nil {
		return state, err
	}
	if state.Status == "" {
		state.Status = ProviderStatusValidation
	}
	if !validSHA256(day.goldSHA256) {
		return state, errors.New("provider day requires frozen gold SHA256")
	}
	if state.GoldSHA256 != "" && state.GoldSHA256 != day.goldSHA256 {
		state.Status = ProviderStatusValidation
		state.QualifiedTradingDays = 0
		state.FailureStreak = 0
		state.LastTradeDate = ""
		state.WindowJSON = ""
		state.GoldEvidenceReady = false
		window = nil
	}
	state.GoldSHA256 = day.goldSHA256
	state.GoldEvidenceReady = day.goldReady
	if state.LastTradeDate != "" {
		newYork, loadErr := time.LoadLocation("America/New_York")
		if loadErr != nil {
			return state, loadErr
		}
		lastDate, parseErr := parseCivilDate(state.LastTradeDate, newYork)
		if parseErr != nil {
			return state, fmt.Errorf("invalid provider last trade date: %w", parseErr)
		}
		cursor := lastDate
		for missing := 0; ; missing++ {
			if missing > 370 {
				return state, errors.New("provider trading-date gap exceeds one year")
			}
			expectedNext, nextErr := nextTradingDate(ctx, calendar, cursor)
			if nextErr != nil {
				return state, fmt.Errorf("find consecutive provider trading date: %w", nextErr)
			}
			nextText := expectedNext.Format(time.DateOnly)
			if nextText == date {
				break
			}
			if nextText > date {
				return state, fmt.Errorf("provider date %s precedes next trading date %s", date, nextText)
			}
			applyProviderHealthDay(&state, &window, providerWindowDay{Date: nextText})
			cursor = expectedNext
		}
	}
	applyProviderHealthDay(&state, &window, providerWindowDay{Date: date, CoveragePct: day.coveragePct, Timely: day.timely, ValidationOK: day.validationOK, GoldReady: day.goldReady})
	encoded, err := json.Marshal(window)
	if err != nil {
		return state, fmt.Errorf("encode provider health window: %w", err)
	}
	state.WindowJSON = string(encoded)
	return state, nil
}

func validateProviderHealthWindow(ctx context.Context, calendar MarketCalendar, state ProviderHealth, window []providerWindowDay) error {
	switch state.Status {
	case "", ProviderStatusValidation, ProviderStatusActive, ProviderStatusDegraded, ProviderStatusFailed:
	default:
		return fmt.Errorf("provider health has invalid status %q", state.Status)
	}
	if state.FailureStreak < 0 {
		return errors.New("provider health failure streak is invalid")
	}
	if state.GoldEvidenceReady && !validSHA256(state.GoldSHA256) {
		return errors.New("provider health gold evidence lacks a valid SHA256")
	}
	if len(window) > ProviderActivationTradingDays || state.QualifiedTradingDays != len(window) {
		return errors.New("provider health window length is inconsistent")
	}
	if len(window) == 0 {
		if state.LastTradeDate != "" || state.Status == ProviderStatusActive {
			return errors.New("provider health has active or dated state without a window")
		}
		return nil
	}
	newYork, err := time.LoadLocation("America/New_York")
	if err != nil {
		return err
	}
	var previous time.Time
	for index, entry := range window {
		if entry.CoveragePct < 0 || entry.CoveragePct > 100 {
			return fmt.Errorf("provider health window entry %d has invalid coverage", index)
		}
		date, parseErr := parseCivilDate(entry.Date, newYork)
		if parseErr != nil {
			return fmt.Errorf("provider health window entry %d: %w", index, parseErr)
		}
		trading, calendarErr := calendar.IsTradingDate(ctx, entry.Date)
		if calendarErr != nil {
			return fmt.Errorf("validate provider health window entry %d: %w", index, calendarErr)
		}
		if !trading {
			return fmt.Errorf("provider health window entry %d is not a trading date", index)
		}
		if index > 0 {
			next, nextErr := nextTradingDate(ctx, calendar, previous)
			if nextErr != nil || next.Format(time.DateOnly) != entry.Date {
				return fmt.Errorf("provider health window entry %d is not consecutive", index)
			}
		}
		previous = date
	}
	if window[len(window)-1].Date != state.LastTradeDate {
		return errors.New("provider health last date does not match its window")
	}
	if state.Status == ProviderStatusActive {
		if len(window) != ProviderActivationTradingDays || !validSHA256(state.GoldSHA256) || state.FailureStreak >= ProviderDegradedFailureDays {
			return errors.New("active provider health lacks a complete validated window")
		}
		if !providerWindowPasses(window) && (state.FailureStreak == 0 || trailingProviderFailures(window) < state.FailureStreak) {
			return errors.New("active provider health window does not support its status")
		}
	}
	return nil
}

func applyProviderHealthDay(state *ProviderHealth, window *[]providerWindowDay, day providerWindowDay) {
	state.LastTradeDate = day.Date
	*window = append(*window, day)
	if len(*window) > ProviderActivationTradingDays {
		*window = (*window)[len(*window)-ProviderActivationTradingDays:]
	}
	state.QualifiedTradingDays = len(*window)
	dailyPass := providerWindowDayPasses(day)
	if dailyPass {
		state.FailureStreak = 0
	} else {
		state.FailureStreak++
		if state.Status == ProviderStatusActive && state.FailureStreak >= ProviderDegradedFailureDays {
			state.Status = ProviderStatusDegraded
		}
	}
	if state.Status == ProviderStatusValidation && len(*window) == ProviderActivationTradingDays && state.GoldEvidenceReady && providerWindowPasses(*window) {
		state.Status = ProviderStatusActive
	}
}

func providerWindowPasses(window []providerWindowDay) bool {
	if len(window) != ProviderActivationTradingDays {
		return false
	}
	coverageTotal := float64(0)
	timely := 0
	for _, day := range window {
		coverageTotal += day.CoveragePct
		if day.Timely {
			timely++
		}
		if !day.ValidationOK || !day.GoldReady {
			return false
		}
	}
	return coverageTotal/float64(len(window)) >= DefaultPriceCoveragePct &&
		float64(timely)*100/float64(len(window)) >= DefaultPriceTimelyPct
}

func providerWindowDayPasses(day providerWindowDay) bool {
	return day.CoveragePct >= DefaultPriceCoveragePct && day.Timely && day.ValidationOK && day.GoldReady
}

func trailingProviderFailures(window []providerWindowDay) int {
	failures := 0
	for index := len(window) - 1; index >= 0; index-- {
		if providerWindowDayPasses(window[index]) {
			break
		}
		failures++
	}
	return failures
}
