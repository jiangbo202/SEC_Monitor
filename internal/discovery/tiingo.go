package discovery

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

const defaultTiingoProvider = "tiingo"

type TiingoPriceProviderOptions struct {
	Provider string
	Token    string
	BaseURL  string
	Client   *http.Client
	Now      func() time.Time
	Calendar MarketCalendar
}

type TiingoPriceProvider struct {
	options TiingoPriceProviderOptions
	baseURL *url.URL
}

func NewTiingoPriceProvider(options TiingoPriceProviderOptions) (*TiingoPriceProvider, error) {
	options.Provider = strings.TrimSpace(options.Provider)
	if options.Provider == "" {
		options.Provider = defaultTiingoProvider
	}
	if strings.TrimSpace(options.Token) == "" {
		return nil, errors.New("tiingo token is required")
	}
	if strings.TrimSpace(options.BaseURL) == "" {
		options.BaseURL = "https://api.tiingo.com"
	}
	parsed, err := url.Parse(options.BaseURL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
		return nil, errors.New("tiingo base URL must be HTTPS without user info")
	}
	if options.Client == nil {
		options.Client = &http.Client{Timeout: 30 * time.Second}
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.Calendar == nil {
		return nil, errors.New("tiingo market calendar is required")
	}
	return &TiingoPriceProvider{options: options, baseURL: parsed}, nil
}

func (provider *TiingoPriceProvider) ProviderName() string {
	return provider.options.Provider
}

func (provider *TiingoPriceProvider) Load(ctx context.Context, expected []Listing) ([]PriceRecord, ProviderResult, error) {
	if _, err := expectedSymbolMapping(expected); err != nil {
		return nil, ProviderResult{}, err
	}
	requests := uniqueTiingoRequests(expected)
	records := make([]PriceRecord, 0, len(requests))
	for _, request := range requests {
		record, ok, err := provider.loadSymbol(ctx, request)
		if err != nil {
			return nil, ProviderResult{}, err
		}
		if ok {
			records = append(records, record)
		}
	}
	if len(records) == 0 {
		return nil, ProviderResult{}, errors.New("tiingo returned no usable price records")
	}
	effectiveDate := latestPriceRecordDate(records)
	records = filterPriceRecordsByDate(records, effectiveDate)
	sort.SliceStable(records, func(i, j int) bool {
		return records[i].Symbol < records[j].Symbol
	})
	validation := PriceValidationOptions{
		Provider:      provider.options.Provider,
		SourceURL:     strings.TrimRight(provider.baseURL.String(), "/"),
		SourceVersion: "tiingo:" + effectiveDate.Format(time.DateOnly),
		EffectiveDate: effectiveDate,
		Now:           provider.options.Now().UTC(),
		Calendar:      provider.options.Calendar,
		Expected:      expected,
	}
	result, err := validatePriceBatch(ctx, records, validation)
	if err != nil {
		return nil, ProviderResult{}, err
	}
	result.SHA256 = hashTiingoPriceRecords(records)
	if result.SourceVersion == "" {
		result.SourceVersion = validation.SourceVersion
	}
	return records, result, nil
}

type tiingoRequest struct {
	canonical string
	provider  string
}

func uniqueTiingoRequests(expected []Listing) []tiingoRequest {
	seen := make(map[string]struct{})
	requests := make([]tiingoRequest, 0, len(expected))
	for _, listing := range expected {
		canonical := strings.ToUpper(strings.TrimSpace(listing.Ticker))
		if canonical == "" {
			continue
		}
		if _, exists := seen[canonical]; exists {
			continue
		}
		providerTicker := strings.TrimSpace(listing.ProviderTicker)
		if providerTicker == "" {
			providerTicker = canonical
		}
		requests = append(requests, tiingoRequest{
			canonical: canonical,
			provider:  strings.ToLower(providerTicker),
		})
		seen[canonical] = struct{}{}
	}
	return requests
}

func (provider *TiingoPriceProvider) loadSymbol(ctx context.Context, request tiingoRequest) (PriceRecord, bool, error) {
	end := provider.options.Now().UTC()
	start := end.AddDate(0, 0, -10)
	endpoint := *provider.baseURL
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + "/tiingo/daily/" + url.PathEscape(request.provider) + "/prices"
	query := endpoint.Query()
	query.Set("startDate", start.Format(time.DateOnly))
	query.Set("endDate", end.Format(time.DateOnly))
	query.Set("format", "json")
	endpoint.RawQuery = query.Encode()

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return PriceRecord{}, false, err
	}
	httpReq.Header.Set("Authorization", "Token "+strings.TrimSpace(provider.options.Token))
	httpReq.Header.Set("Accept", "application/json")
	resp, err := provider.options.Client.Do(httpReq)
	if err != nil {
		if ctx.Err() != nil {
			return PriceRecord{}, false, ctx.Err()
		}
		return PriceRecord{}, false, nil
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return PriceRecord{}, false, fmt.Errorf("tiingo authentication failed with HTTP %d", resp.StatusCode)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1024))
		return PriceRecord{}, false, nil
	}
	payload, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return PriceRecord{}, false, nil
	}
	record, ok := parseLatestTiingoRecord(payload, request.canonical, provider.options.Provider)
	return record, ok, nil
}

type tiingoDailyPrice struct {
	Date   string      `json:"date"`
	Open   json.Number `json:"open"`
	High   json.Number `json:"high"`
	Low    json.Number `json:"low"`
	Close  json.Number `json:"close"`
	Volume json.Number `json:"volume"`
}

func parseLatestTiingoRecord(payload []byte, canonical string, source string) (PriceRecord, bool) {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	var rows []tiingoDailyPrice
	if err := decoder.Decode(&rows); err != nil || len(rows) == 0 {
		return PriceRecord{}, false
	}
	var selected PriceRecord
	for _, row := range rows {
		record, err := tiingoDailyPriceToRecord(row, canonical, source)
		if err != nil {
			continue
		}
		if selected.TradeDate.IsZero() || record.TradeDate.After(selected.TradeDate) {
			selected = record
		}
	}
	if selected.TradeDate.IsZero() {
		return PriceRecord{}, false
	}
	return selected, true
}

func tiingoDailyPriceToRecord(row tiingoDailyPrice, canonical string, source string) (PriceRecord, error) {
	dateText := strings.TrimSpace(row.Date)
	if len(dateText) >= len(time.DateOnly) {
		dateText = dateText[:len(time.DateOnly)]
	}
	newYork, err := time.LoadLocation("America/New_York")
	if err != nil {
		return PriceRecord{}, err
	}
	tradeDate, err := parseCivilDate(dateText, newYork)
	if err != nil {
		return PriceRecord{}, err
	}
	openMicros, err := parsePriceMicros(row.Open.String())
	if err != nil {
		return PriceRecord{}, err
	}
	highMicros, err := parsePriceMicros(row.High.String())
	if err != nil {
		return PriceRecord{}, err
	}
	lowMicros, err := parsePriceMicros(row.Low.String())
	if err != nil {
		return PriceRecord{}, err
	}
	closeMicros, err := parsePriceMicros(row.Close.String())
	if err != nil {
		return PriceRecord{}, err
	}
	volume, err := parseTiingoVolume(row.Volume)
	if err != nil {
		return PriceRecord{}, err
	}
	record := PriceRecord{
		Symbol:      strings.ToUpper(strings.TrimSpace(canonical)),
		TradeDate:   tradeDate,
		OpenMicros:  openMicros,
		HighMicros:  highMicros,
		LowMicros:   lowMicros,
		CloseMicros: closeMicros,
		Volume:      volume,
		Currency:    "USD",
		Adjusted:    false,
		Source:      source,
	}
	if err := validateOHLC(record); err != nil {
		return PriceRecord{}, err
	}
	return record, nil
}

func parseTiingoVolume(value json.Number) (int64, error) {
	if strings.TrimSpace(value.String()) == "" {
		return 0, nil
	}
	volume, err := strconv.ParseInt(value.String(), 10, 64)
	if err == nil && volume >= 0 {
		return volume, nil
	}
	floatVolume, err := strconv.ParseFloat(value.String(), 64)
	if err != nil || floatVolume < 0 {
		return 0, errors.New("invalid tiingo volume")
	}
	return int64(floatVolume), nil
}

func latestPriceRecordDate(records []PriceRecord) time.Time {
	var latest time.Time
	for _, record := range records {
		if latest.IsZero() || record.TradeDate.After(latest) {
			latest = record.TradeDate
		}
	}
	return latest
}

func filterPriceRecordsByDate(records []PriceRecord, date time.Time) []PriceRecord {
	filtered := records[:0]
	target := date.Format(time.DateOnly)
	for _, record := range records {
		if record.TradeDate.Format(time.DateOnly) == target {
			filtered = append(filtered, record)
		}
	}
	return filtered
}

func hashTiingoPriceRecords(records []PriceRecord) string {
	type row struct {
		Symbol      string `json:"symbol"`
		TradeDate   string `json:"trade_date"`
		OpenMicros  int64  `json:"open_micros"`
		HighMicros  int64  `json:"high_micros"`
		LowMicros   int64  `json:"low_micros"`
		CloseMicros int64  `json:"close_micros"`
		Volume      int64  `json:"volume"`
		Source      string `json:"source"`
	}
	rows := make([]row, 0, len(records))
	for _, record := range records {
		rows = append(rows, row{
			Symbol:      record.Symbol,
			TradeDate:   record.TradeDate.Format(time.DateOnly),
			OpenMicros:  record.OpenMicros,
			HighMicros:  record.HighMicros,
			LowMicros:   record.LowMicros,
			CloseMicros: record.CloseMicros,
			Volume:      record.Volume,
			Source:      record.Source,
		})
	}
	payload, _ := json.Marshal(rows)
	hash := sha256.Sum256(payload)
	return hex.EncodeToString(hash[:])
}
