package discovery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"

	lbconfig "github.com/longbridge/openapi-go/config"
	lbquote "github.com/longbridge/openapi-go/quote"
)

const (
	defaultLongbridgeProvider = "longbridge"
	longbridgeQuoteBatchSize  = 500
	longbridgeQuoteEndpoint   = "wss://openapi-quote.longbridge.com"
	longbridgeProbeSymbol     = "AAPL.US"
)

// LongbridgePriceProviderOptions configures the official Longbridge Go SDK.
// Credentials are intentionally supplied by the application configuration;
// this provider never reads a developer workstation's CLI credential files.
type LongbridgePriceProviderOptions struct {
	Provider    string
	AppKey      string
	AppSecret   string
	AccessToken string
	Calendar    MarketCalendar
	Now         func() time.Time
	NewClient   func(appKey, appSecret, accessToken string) (longbridgeQuoteClient, error)
}

type longbridgeQuoteClient interface {
	Quote(context.Context, []string) ([]longbridgeQuote, error)
	HistoryDaily(context.Context, string, time.Time, time.Time) ([]longbridgeCandle, error)
	Close() error
}

type longbridgeQuote struct {
	Symbol                    string
	LastDone, Open, High, Low string
	Timestamp                 int64
	Volume                    int64
}

type longbridgeCandle struct {
	Open, High, Low, Close string
	Timestamp              int64
	Volume                 int64
}

// LongbridgeQuoteProbeResult is a lightweight, auditable connectivity check
// for the same authenticated WebSocket quote client used by the market sync.
// It deliberately makes one quote request and does not write price snapshots.
type LongbridgeQuoteProbeResult struct {
	Provider       string `json:"provider"`
	Endpoint       string `json:"endpoint"`
	Symbol         string `json:"symbol"`
	Status         string `json:"status"`
	ErrorKind      string `json:"error_kind,omitempty"`
	Message        string `json:"message,omitempty"`
	ElapsedMillis  int64  `json:"elapsed_millis"`
	QuoteReceived  bool   `json:"quote_received"`
	QuoteTimestamp int64  `json:"quote_timestamp,omitempty"`
	LastDone       string `json:"last_done,omitempty"`
	Volume         int64  `json:"volume,omitempty"`
}

type LongbridgePriceProvider struct {
	options LongbridgePriceProviderOptions
}

func NewLongbridgePriceProvider(options LongbridgePriceProviderOptions) (*LongbridgePriceProvider, error) {
	options.Provider = strings.ToLower(strings.TrimSpace(options.Provider))
	if options.Provider == "" {
		options.Provider = defaultLongbridgeProvider
	}
	if strings.TrimSpace(options.AppKey) == "" || strings.TrimSpace(options.AppSecret) == "" || strings.TrimSpace(options.AccessToken) == "" {
		return nil, errors.New("longbridge app key, app secret, and access token are required")
	}
	if options.Calendar == nil {
		return nil, errors.New("longbridge market calendar is required")
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.NewClient == nil {
		options.NewClient = newLongbridgeSDKClient
	}
	return &LongbridgePriceProvider{options: options}, nil
}

// ProbeLongbridgeQuote verifies the configured credentials against the quote
// WebSocket used by the discovery market sync. It is intentionally separate
// from Load/LoadForDate so a user can diagnose connectivity without requesting
// an entire market universe or mutating the local research database.
func ProbeLongbridgeQuote(ctx context.Context, appKey, appSecret, accessToken string) LongbridgeQuoteProbeResult {
	return probeLongbridgeQuote(ctx, appKey, appSecret, accessToken, newLongbridgeSDKClient)
}

func probeLongbridgeQuote(ctx context.Context, appKey, appSecret, accessToken string, newClient func(string, string, string) (longbridgeQuoteClient, error)) LongbridgeQuoteProbeResult {
	result := LongbridgeQuoteProbeResult{
		Provider: defaultLongbridgeProvider,
		Endpoint: longbridgeQuoteEndpoint,
		Symbol:   longbridgeProbeSymbol,
		Status:   "failed",
	}
	started := time.Now()
	finish := func() LongbridgeQuoteProbeResult {
		result.ElapsedMillis = time.Since(started).Milliseconds()
		return result
	}
	if strings.TrimSpace(appKey) == "" || strings.TrimSpace(appSecret) == "" || strings.TrimSpace(accessToken) == "" {
		result.ErrorKind = "configuration"
		result.Message = "Longbridge app key, app secret, and access token are required"
		return finish()
	}
	if ctx == nil {
		ctx = context.Background()
	}
	probeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 20*time.Second)
	defer cancel()
	client, err := newClient(appKey, appSecret, accessToken)
	if err != nil {
		result.ErrorKind = probeLongbridgeErrorKind(err, "connect")
		result.Message = fmt.Sprintf("create Longbridge quote client: %v", err)
		return finish()
	}
	defer func() { _ = client.Close() }()
	quotes, err := client.Quote(probeCtx, []string{longbridgeProbeSymbol})
	if err != nil {
		result.ErrorKind = probeLongbridgeErrorKind(err, "quote")
		result.Message = fmt.Sprintf("request Longbridge quote %s: %v", longbridgeProbeSymbol, err)
		return finish()
	}
	if len(quotes) == 0 {
		result.ErrorKind = "empty_response"
		result.Message = fmt.Sprintf("Longbridge returned no quote for %s", longbridgeProbeSymbol)
		return finish()
	}
	quote := quotes[0]
	result.Status = "ok"
	result.Message = "Longbridge quote WebSocket is available"
	result.QuoteReceived = true
	result.QuoteTimestamp = quote.Timestamp
	result.LastDone = quote.LastDone
	result.Volume = quote.Volume
	return finish()
}

func probeLongbridgeErrorKind(err error, fallback string) string {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return "timeout"
	}
	if strings.Contains(strings.ToLower(err.Error()), "timeout") {
		return "timeout"
	}
	return fallback
}

func (p *LongbridgePriceProvider) ProviderName() string { return p.options.Provider }

func (p *LongbridgePriceProvider) Load(ctx context.Context, expected []Listing) ([]PriceRecord, ProviderResult, error) {
	dateText, err := nyCivilDate(p.options.Now())
	if err != nil {
		return nil, ProviderResult{}, err
	}
	date, err := parseNYCivilDate(dateText)
	if err != nil {
		return nil, ProviderResult{}, err
	}
	return p.load(ctx, expected, date)
}

func (p *LongbridgePriceProvider) LoadForDate(ctx context.Context, expected []Listing, effectiveDate string) ([]PriceRecord, ProviderResult, error) {
	date, err := parseNYCivilDate(effectiveDate)
	if err != nil {
		return nil, ProviderResult{}, err
	}
	return p.load(ctx, expected, date)
}

func (p *LongbridgePriceProvider) load(ctx context.Context, expected []Listing, target time.Time) ([]PriceRecord, ProviderResult, error) {
	if _, err := expectedSymbolMapping(expected); err != nil {
		return nil, ProviderResult{}, err
	}
	client, err := p.options.NewClient(p.options.AppKey, p.options.AppSecret, p.options.AccessToken)
	if err != nil {
		return nil, ProviderResult{}, fmt.Errorf("create longbridge quote client: %w", err)
	}
	defer func() { _ = client.Close() }()

	requests, canonicalByProvider := longbridgeRequests(expected)
	records := make([]PriceRecord, 0, len(requests))
	var requestErrs []string
	for start := 0; start < len(requests); start += longbridgeQuoteBatchSize {
		end := start + longbridgeQuoteBatchSize
		if end > len(requests) {
			end = len(requests)
		}
		quotes, quoteErr := client.Quote(ctx, requests[start:end])
		if quoteErr != nil {
			requestErrs = append(requestErrs, quoteErr.Error())
			continue
		}
		for _, item := range quotes {
			canonical, ok := canonicalByProvider[strings.ToUpper(strings.TrimSpace(item.Symbol))]
			if !ok {
				continue
			}
			record, ok := p.quoteRecord(item, canonical, target)
			if ok {
				records = append(records, record)
			}
		}
	}
	if len(records) == 0 && len(requestErrs) > 0 {
		return nil, ProviderResult{}, fmt.Errorf("longbridge quote request failed: %s", strings.Join(requestErrs, "; "))
	}
	if len(records) == 0 {
		return nil, ProviderResult{}, errors.New("longbridge returned no usable price records")
	}
	sortPriceRecordsByExpected(records, expected)
	priceDate := latestPriceRecordDate(records)
	contentSHA := hashLongbridgePriceRecords(records)
	result, err := validatePriceBatch(ctx, records, PriceValidationOptions{
		Provider:                      p.options.Provider,
		SourceVersion:                 longbridgePriceSourceVersion(p.options.Provider, priceDate, contentSHA),
		EffectiveDate:                 target,
		Now:                           p.options.Now(),
		Calendar:                      p.options.Calendar,
		Expected:                      expected,
		AllowPreviousTradingDatePrice: true,
	})
	if err != nil {
		return nil, ProviderResult{}, err
	}
	result.SHA256 = contentSHA
	return records, result, nil
}

func (p *LongbridgePriceProvider) quoteRecord(item longbridgeQuote, canonical string, target time.Time) (PriceRecord, bool) {
	if item.Timestamp <= 0 || item.Volume < 0 {
		return PriceRecord{}, false
	}
	newYork, err := time.LoadLocation("America/New_York")
	if err != nil {
		return PriceRecord{}, false
	}
	tradeDate := time.Unix(item.Timestamp, 0).In(newYork)
	tradeDate = time.Date(tradeDate.Year(), tradeDate.Month(), tradeDate.Day(), 0, 0, 0, 0, newYork)
	if tradeDate.After(target) {
		return PriceRecord{}, false
	}
	// Quote.LastDone is the regular-session quote. Do not write a same-day
	// intraday value as a daily close before the regular market has closed.
	now := p.options.Now().In(newYork)
	if tradeDate.Format(time.DateOnly) == now.Format(time.DateOnly) && now.Before(time.Date(now.Year(), now.Month(), now.Day(), 16, 15, 0, 0, newYork)) {
		return PriceRecord{}, false
	}
	openMicros, openErr := parsePriceMicros(item.Open)
	highMicros, highErr := parsePriceMicros(item.High)
	lowMicros, lowErr := parsePriceMicros(item.Low)
	closeMicros, closeErr := parsePriceMicros(item.LastDone)
	if openErr != nil || highErr != nil || lowErr != nil || closeErr != nil || closeMicros <= 0 {
		return PriceRecord{}, false
	}
	return PriceRecord{Symbol: canonical, TradeDate: tradeDate, OpenMicros: openMicros, HighMicros: highMicros, LowMicros: lowMicros, CloseMicros: closeMicros, Volume: item.Volume, Currency: "USD", Source: p.options.Provider}, true
}

// LoadHistory fetches regular-session daily OHLCV bars. The same volume field
// is persisted in PriceSnapshot and therefore powers the volume bars shown in
// the candidate detail chart.
func (p *LongbridgePriceProvider) LoadHistory(ctx context.Context, expected []Listing, effectiveDate string, lookbackDays int) ([]PriceRecord, error) {
	start, end, err := normalizeHistoryWindow(effectiveDate, lookbackDays)
	if err != nil {
		return nil, err
	}
	client, err := p.options.NewClient(p.options.AppKey, p.options.AppSecret, p.options.AccessToken)
	if err != nil {
		return nil, fmt.Errorf("create longbridge history client: %w", err)
	}
	defer func() { _ = client.Close() }()
	records := make([]PriceRecord, 0, len(expected)*min(lookbackDays, 220))
	for _, listing := range expected {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		symbol := longbridgeSymbol(listing)
		if symbol == "" {
			continue
		}
		candles, historyErr := client.HistoryDaily(ctx, symbol, start, end)
		if historyErr != nil {
			continue
		}
		for _, candle := range candles {
			record, ok := longbridgeCandleRecord(candle, listing.Ticker, p.options.Provider, start, end)
			if ok {
				records = append(records, record)
			}
		}
	}
	if len(records) == 0 {
		return nil, errors.New("longbridge returned no usable technical history")
	}
	return records, nil
}

func longbridgeRequests(expected []Listing) ([]string, map[string]string) {
	canonicalByProvider := make(map[string]string, len(expected))
	for _, listing := range expected {
		canonical := strings.ToUpper(strings.TrimSpace(listing.Ticker))
		symbol := longbridgeSymbol(listing)
		if canonical == "" || symbol == "" {
			continue
		}
		canonicalByProvider[symbol] = canonical
	}
	requests := make([]string, 0, len(canonicalByProvider))
	for symbol := range canonicalByProvider {
		requests = append(requests, symbol)
	}
	sort.Strings(requests)
	return requests, canonicalByProvider
}

func longbridgeSymbol(listing Listing) string {
	symbol := strings.ToUpper(strings.TrimSpace(listing.ProviderTicker))
	if symbol == "" {
		symbol = strings.ToUpper(strings.TrimSpace(listing.Ticker))
	}
	if symbol == "" {
		return ""
	}
	if strings.HasSuffix(symbol, ".US") {
		return symbol
	}
	return symbol + ".US"
}

func longbridgeCandleRecord(candle longbridgeCandle, ticker, provider string, start, end time.Time) (PriceRecord, bool) {
	if candle.Timestamp <= 0 || candle.Volume < 0 {
		return PriceRecord{}, false
	}
	newYork, err := time.LoadLocation("America/New_York")
	if err != nil {
		return PriceRecord{}, false
	}
	date := time.Unix(candle.Timestamp, 0).In(newYork)
	date = time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, newYork)
	if date.Before(start) || date.After(end) {
		return PriceRecord{}, false
	}
	openMicros, openErr := parsePriceMicros(candle.Open)
	highMicros, highErr := parsePriceMicros(candle.High)
	lowMicros, lowErr := parsePriceMicros(candle.Low)
	closeMicros, closeErr := parsePriceMicros(candle.Close)
	if openErr != nil || highErr != nil || lowErr != nil || closeErr != nil || closeMicros <= 0 {
		return PriceRecord{}, false
	}
	return PriceRecord{Symbol: strings.ToUpper(strings.TrimSpace(ticker)), TradeDate: date, OpenMicros: openMicros, HighMicros: highMicros, LowMicros: lowMicros, CloseMicros: closeMicros, Volume: candle.Volume, Currency: "USD", Source: provider}, true
}

func hashLongbridgePriceRecords(records []PriceRecord) string {
	rows := append([]PriceRecord(nil), records...)
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Symbol == rows[j].Symbol {
			return rows[i].TradeDate.Before(rows[j].TradeDate)
		}
		return rows[i].Symbol < rows[j].Symbol
	})
	hash := sha256.New()
	for _, row := range rows {
		_, _ = hash.Write([]byte(fmt.Sprintf("%s|%s|%d|%d|%d|%d|%d|%t\n", row.Symbol, row.TradeDate.Format(time.DateOnly), row.OpenMicros, row.HighMicros, row.LowMicros, row.CloseMicros, row.Volume, row.Adjusted)))
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func longbridgePriceSourceVersion(provider string, tradeDate time.Time, contentSHA string) string {
	return strings.ToLower(strings.TrimSpace(provider)) + ":" + tradeDate.Format(time.DateOnly) + ":" + contentSHA
}

type longbridgeSDKClient struct{ quote *lbquote.QuoteContext }

func newLongbridgeSDKClient(appKey, appSecret, accessToken string) (longbridgeQuoteClient, error) {
	cfg, err := lbconfig.New(lbconfig.WithConfigKey(appKey, appSecret, accessToken))
	if err != nil {
		return nil, err
	}
	context, err := lbquote.NewFromCfg(cfg)
	if err != nil {
		return nil, err
	}
	return &longbridgeSDKClient{quote: context}, nil
}

func (c *longbridgeSDKClient) Quote(ctx context.Context, symbols []string) ([]longbridgeQuote, error) {
	items, err := c.quote.Quote(ctx, symbols)
	if err != nil {
		return nil, err
	}
	result := make([]longbridgeQuote, 0, len(items))
	for _, item := range items {
		if item == nil {
			continue
		}
		result = append(result, longbridgeQuote{Symbol: item.Symbol, LastDone: decimalText(item.LastDone), Open: decimalText(item.Open), High: decimalText(item.High), Low: decimalText(item.Low), Timestamp: item.Timestamp, Volume: item.Volume})
	}
	return result, nil
}

func (c *longbridgeSDKClient) HistoryDaily(ctx context.Context, symbol string, start, end time.Time) ([]longbridgeCandle, error) {
	items, err := c.quote.HistoryCandlesticksByDate(ctx, symbol, lbquote.PeriodDay, lbquote.AdjustTypeNo, &start, &end)
	if err != nil {
		return nil, err
	}
	result := make([]longbridgeCandle, 0, len(items))
	for _, item := range items {
		if item == nil {
			continue
		}
		result = append(result, longbridgeCandle{Open: decimalText(item.Open), High: decimalText(item.High), Low: decimalText(item.Low), Close: decimalText(item.Close), Timestamp: item.Timestamp, Volume: item.Volume})
	}
	return result, nil
}

func (c *longbridgeSDKClient) Close() error { return c.quote.Close() }

type longbridgeDecimal interface{ String() string }

func decimalText(value longbridgeDecimal) string {
	if value == nil {
		return ""
	}
	// Longbridge uses pointer decimals for optional quote fields. A typed nil
	// pointer stored in this interface is not equal to nil, so detect it before
	// invoking String() and let the caller skip the incomplete quote instead of
	// terminating the entire market sync.
	reflected := reflect.ValueOf(value)
	if (reflected.Kind() == reflect.Ptr || reflected.Kind() == reflect.Interface || reflected.Kind() == reflect.Map || reflected.Kind() == reflect.Slice || reflected.Kind() == reflect.Func) && reflected.IsNil() {
		return ""
	}
	return value.String()
}

var _ DatedPriceProvider = (*LongbridgePriceProvider)(nil)
var _ HistoricalPriceProvider = (*LongbridgePriceProvider)(nil)
var _ NamedPriceProvider = (*LongbridgePriceProvider)(nil)
