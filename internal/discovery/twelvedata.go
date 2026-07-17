package discovery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const defaultTwelveDataProvider = "twelvedata"

type TwelveDataPriceProviderOptions struct {
	Provider        string
	APIKey          string
	BaseURL         string
	Client          *http.Client
	Now             func() time.Time
	Calendar        MarketCalendar
	CacheDir        string
	RequestBudget   int
	RequestInterval time.Duration
	ProgressEvery   int
	Progress        func(TwelveDataProgress)
}

type TwelveDataProgress struct {
	Processed   int
	Total       int
	Records     int
	Skipped     int
	SkipReasons map[string]int
	Elapsed     time.Duration
}

type TwelveDataPriceProvider struct {
	options TwelveDataPriceProviderOptions
	baseURL *url.URL
}

func NewTwelveDataPriceProvider(options TwelveDataPriceProviderOptions) (*TwelveDataPriceProvider, error) {
	options.Provider = strings.ToLower(strings.TrimSpace(options.Provider))
	if options.Provider == "" {
		options.Provider = defaultTwelveDataProvider
	}
	options.APIKey = strings.TrimSpace(options.APIKey)
	if options.APIKey == "" {
		return nil, errors.New("twelve data API key is required")
	}
	if strings.TrimSpace(options.BaseURL) == "" {
		options.BaseURL = "https://api.twelvedata.com"
	}
	parsed, err := url.Parse(options.BaseURL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
		return nil, errors.New("twelve data base URL must be HTTPS without user info")
	}
	if options.Client == nil {
		options.Client = &http.Client{Timeout: 30 * time.Second}
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.Calendar == nil {
		return nil, errors.New("twelve data market calendar is required")
	}
	return &TwelveDataPriceProvider{options: options, baseURL: parsed}, nil
}

func (p *TwelveDataPriceProvider) ProviderName() string { return p.options.Provider }

func (p *TwelveDataPriceProvider) Load(ctx context.Context, expected []Listing) ([]PriceRecord, ProviderResult, error) {
	target, err := nyCivilDate(p.options.Now())
	if err != nil {
		return nil, ProviderResult{}, err
	}
	return p.load(ctx, expected, target)
}

func (p *TwelveDataPriceProvider) LoadForDate(ctx context.Context, expected []Listing, effectiveDate string) ([]PriceRecord, ProviderResult, error) {
	return p.load(ctx, expected, effectiveDate)
}

func (p *TwelveDataPriceProvider) load(ctx context.Context, expected []Listing, effectiveDate string) ([]PriceRecord, ProviderResult, error) {
	target, err := parseNYCivilDate(effectiveDate)
	if err != nil {
		return nil, ProviderResult{}, err
	}
	previous, err := previousTradingDate(ctx, p.options.Calendar, target)
	if err != nil {
		return nil, ProviderResult{}, fmt.Errorf("find previous trading date: %w", err)
	}
	limit := len(expected)
	if p.options.RequestBudget > 0 && p.options.RequestBudget < limit {
		limit = p.options.RequestBudget
	}
	records := make([]PriceRecord, 0, limit)
	rateLimited := false
	started := time.Now()
	skipReasons := make(map[string]int)
	processed := 0
	for index, listing := range expected {
		if index >= limit || rateLimited {
			break
		}
		if err := ctx.Err(); err != nil {
			return nil, ProviderResult{}, err
		}
		if index > 0 && p.options.RequestInterval > 0 {
			timer := time.NewTimer(p.options.RequestInterval)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil, ProviderResult{}, ctx.Err()
			case <-timer.C:
			}
		}
		record, ok, reason, err := p.loadListing(ctx, listing, target)
		processed++
		if err != nil {
			return nil, ProviderResult{}, err
		}
		if reason == "rate_limited" {
			skipReasons[reason]++
			rateLimited = true
			p.emitProgress(processed, limit, len(records), skipReasons, started, true)
			break
		}
		if ok {
			reason, accepted, dateErr := twelveDataRecordDateStatus(ctx, record, target, previous, p.options.Calendar)
			if dateErr != nil {
				return nil, ProviderResult{}, dateErr
			}
			if accepted {
				records = append(records, record)
			} else {
				skipReasons[reason]++
			}
		} else {
			skipReasons[reasonOrDefault(reason, "no_record")]++
		}
		p.emitProgress(processed, limit, len(records), skipReasons, started, false)
	}
	if len(records) == 0 {
		if rateLimited {
			return nil, ProviderResult{}, errors.New("twelve data rate limited")
		}
		return nil, ProviderResult{}, errors.New("twelve data returned no current or previous-trading-day price records")
	}
	priceDate := latestPriceRecordDate(records)
	result, err := validatePriceBatch(ctx, records, PriceValidationOptions{
		Provider:                      p.options.Provider,
		SourceVersion:                 p.options.Provider + ":" + priceDate.Format(time.DateOnly),
		SourceURL:                     strings.TrimRight(p.baseURL.String(), "/"),
		EffectiveDate:                 target,
		Now:                           p.options.Now(),
		Calendar:                      p.options.Calendar,
		Expected:                      expected,
		AllowPreviousTradingDatePrice: true,
	})
	if err != nil {
		return nil, ProviderResult{}, err
	}
	result.SHA256 = hashTwelveDataPriceRecords(records)
	return records, result, nil
}

// twelveDataRecordDateStatus keeps a stale response for the provider cache,
// but prevents it from invalidating every other symbol in the provider run.
// A later coordinator step may explicitly reuse a locally persisted previous
// trading-day quote; provider responses older than that are never accepted.
func twelveDataRecordDateStatus(ctx context.Context, record PriceRecord, target, previous time.Time, calendar MarketCalendar) (string, bool, error) {
	date := record.TradeDate.Format(time.DateOnly)
	targetDate := target.Format(time.DateOnly)
	if date > targetDate {
		return "future_trade_date", false, nil
	}
	trading, err := calendar.IsTradingDate(ctx, date)
	if err != nil {
		return "", false, fmt.Errorf("validate twelve data trade date %s: %w", date, err)
	}
	if !trading {
		return "non_trading_date", false, nil
	}
	if date < previous.Format(time.DateOnly) {
		return "stale_trade_date", false, nil
	}
	return "", true, nil
}

func (p *TwelveDataPriceProvider) emitProgress(processed, total, records int, skipReasons map[string]int, started time.Time, force bool) {
	if p.options.Progress == nil {
		return
	}
	every := p.options.ProgressEvery
	if every <= 0 {
		every = 25
	}
	if !force && processed < total && processed%every != 0 {
		return
	}
	reasons := make(map[string]int, len(skipReasons))
	skipped := 0
	for reason, count := range skipReasons {
		reasons[reason] = count
		skipped += count
	}
	p.options.Progress(TwelveDataProgress{
		Processed:   processed,
		Total:       total,
		Records:     records,
		Skipped:     skipped,
		SkipReasons: reasons,
		Elapsed:     time.Since(started),
	})
}

func (p *TwelveDataPriceProvider) loadListing(ctx context.Context, listing Listing, target time.Time) (PriceRecord, bool, string, error) {
	symbol := strings.TrimSpace(listing.ProviderTicker)
	if symbol == "" {
		symbol = strings.TrimSpace(listing.Ticker)
	}
	if symbol == "" {
		return PriceRecord{}, false, "blank_symbol", nil
	}
	if record, ok := p.loadCachedRecord(listing, symbol, target); ok {
		return record, true, "", nil
	}
	end := target
	start := target.AddDate(0, 0, -10)
	endpoint := *p.baseURL
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + "/time_series"
	query := endpoint.Query()
	query.Set("symbol", symbol)
	query.Set("interval", "1day")
	query.Set("start_date", start.Format(time.DateOnly))
	query.Set("end_date", end.Format(time.DateOnly))
	query.Set("outputsize", "10")
	query.Set("apikey", p.options.APIKey)
	endpoint.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return PriceRecord{}, false, "", err
	}
	resp, err := p.options.Client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return PriceRecord{}, false, "", ctx.Err()
		}
		return PriceRecord{}, false, "network_error", nil
	}
	defer resp.Body.Close()
	payload, readErr := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode == http.StatusTooManyRequests {
		return PriceRecord{}, false, "rate_limited", nil
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return PriceRecord{}, false, "", fmt.Errorf("twelve data authentication failed with HTTP %d", resp.StatusCode)
	}
	if resp.StatusCode >= 500 {
		return PriceRecord{}, false, "", fmt.Errorf("twelve data server error HTTP %d", resp.StatusCode)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return PriceRecord{}, false, fmt.Sprintf("http_%d", resp.StatusCode), nil
	}
	if readErr != nil {
		return PriceRecord{}, false, "read_error", nil
	}
	record, ok, reason, err := parseTwelveDataRecord(payload, listing.Ticker, p.options.Provider, target)
	if err != nil {
		return PriceRecord{}, false, reasonOrDefault(reason, "invalid_record"), nil
	}
	if !ok {
		return PriceRecord{}, false, reason, nil
	}
	p.storeCachedRecord(listing, symbol, target, record)
	return record, true, "", nil
}

func (p *TwelveDataPriceProvider) cachePath(listing Listing, providerSymbol string, target time.Time) string {
	cacheDir := strings.TrimSpace(p.options.CacheDir)
	if cacheDir == "" {
		return ""
	}
	canonical := strings.ToUpper(strings.TrimSpace(listing.Ticker))
	if canonical == "" {
		return ""
	}
	digest := sha256.Sum256([]byte(p.options.Provider + "\x00" + canonical + "\x00" + strings.ToUpper(strings.TrimSpace(providerSymbol)) + "\x00" + target.Format(time.DateOnly)))
	return filepath.Join(cacheDir, "twelvedata-price-"+hex.EncodeToString(digest[:])+".json")
}

func (p *TwelveDataPriceProvider) loadCachedRecord(listing Listing, providerSymbol string, target time.Time) (PriceRecord, bool) {
	path := p.cachePath(listing, providerSymbol, target)
	if path == "" {
		return PriceRecord{}, false
	}
	payload, err := os.ReadFile(path)
	if err != nil {
		return PriceRecord{}, false
	}
	var record PriceRecord
	if err := json.Unmarshal(payload, &record); err != nil {
		return PriceRecord{}, false
	}
	if record.Symbol == "" || record.TradeDate.IsZero() || record.Source != p.options.Provider {
		return PriceRecord{}, false
	}
	return record, true
}

func (p *TwelveDataPriceProvider) storeCachedRecord(listing Listing, providerSymbol string, target time.Time, record PriceRecord) {
	path := p.cachePath(listing, providerSymbol, target)
	if path == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return
	}
	payload, err := json.Marshal(record)
	if err != nil {
		return
	}
	_ = os.WriteFile(path, payload, 0o600)
}

type twelveDataResponse struct {
	Values []struct {
		Datetime string          `json:"datetime"`
		Open     json.RawMessage `json:"open"`
		High     json.RawMessage `json:"high"`
		Low      json.RawMessage `json:"low"`
		Close    json.RawMessage `json:"close"`
		Volume   json.RawMessage `json:"volume"`
	} `json:"values"`
	Status  string `json:"status"`
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func parseTwelveDataRecord(payload []byte, ticker, provider string, target time.Time) (PriceRecord, bool, string, error) {
	var response twelveDataResponse
	if err := json.Unmarshal(payload, &response); err != nil {
		return PriceRecord{}, false, "invalid_json", err
	}
	if strings.EqualFold(response.Status, "error") {
		if response.Code == http.StatusTooManyRequests || strings.Contains(strings.ToLower(response.Message), "credit") || strings.Contains(strings.ToLower(response.Message), "limit") {
			return PriceRecord{}, false, "rate_limited", nil
		}
		return PriceRecord{}, false, "api_error", nil
	}
	targetText := target.Format(time.DateOnly)
	var selected PriceRecord
	var selectedDate string
	for _, row := range response.Values {
		date := strings.TrimSpace(row.Datetime)
		if date == "" || date > targetText || date < selectedDate {
			continue
		}
		openMicros, err := parsePriceMicros(rawJSONScalar(row.Open))
		if err != nil {
			return PriceRecord{}, false, "invalid_open", err
		}
		highMicros, err := parsePriceMicros(rawJSONScalar(row.High))
		if err != nil {
			return PriceRecord{}, false, "invalid_high", err
		}
		lowMicros, err := parsePriceMicros(rawJSONScalar(row.Low))
		if err != nil {
			return PriceRecord{}, false, "invalid_low", err
		}
		closeMicros, err := parsePriceMicros(rawJSONScalar(row.Close))
		if err != nil {
			return PriceRecord{}, false, "invalid_close", err
		}
		volume, err := parseTwelveDataVolume(row.Volume)
		if err != nil {
			return PriceRecord{}, false, "invalid_volume", err
		}
		tradeDate, err := parseNYCivilDate(date)
		if err != nil {
			return PriceRecord{}, false, "invalid_date", err
		}
		selectedDate = date
		selected = PriceRecord{
			Symbol:      strings.ToUpper(strings.TrimSpace(ticker)),
			TradeDate:   tradeDate,
			OpenMicros:  openMicros,
			HighMicros:  highMicros,
			LowMicros:   lowMicros,
			CloseMicros: closeMicros,
			Volume:      volume,
			Currency:    "USD",
			Adjusted:    false,
			Source:      provider,
		}
	}
	if selected.Symbol == "" {
		return PriceRecord{}, false, "no_matching_trade_date", nil
	}
	return selected, true, "", nil
}

func rawJSONScalar(value json.RawMessage) string {
	text := strings.TrimSpace(string(value))
	text = strings.Trim(text, `"`)
	return text
}

func reasonOrDefault(reason, fallback string) string {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return fallback
	}
	return reason
}

func parseTwelveDataVolume(value json.RawMessage) (int64, error) {
	text := rawJSONScalar(value)
	if text == "" {
		return 0, errors.New("missing volume")
	}
	if strings.Contains(text, ".") {
		f, err := strconv.ParseFloat(text, 64)
		if err != nil || f < 0 {
			return 0, errors.New("invalid volume")
		}
		return int64(f), nil
	}
	volume, err := strconv.ParseInt(text, 10, 64)
	if err != nil || volume < 0 {
		return 0, errors.New("invalid volume")
	}
	return volume, nil
}

func hashTwelveDataPriceRecords(records []PriceRecord) string {
	h := sha256.New()
	for _, record := range records {
		_, _ = h.Write([]byte(record.Source))
		_, _ = h.Write([]byte{0})
		_, _ = h.Write([]byte(record.Symbol))
		_, _ = h.Write([]byte{0})
		_, _ = h.Write([]byte(record.TradeDate.Format(time.DateOnly)))
		_, _ = h.Write([]byte{0})
		_, _ = h.Write([]byte(strconv.FormatInt(record.CloseMicros, 10)))
		_, _ = h.Write([]byte{0})
		_, _ = h.Write([]byte(strconv.FormatInt(record.Volume, 10)))
	}
	return hex.EncodeToString(h.Sum(nil))
}
