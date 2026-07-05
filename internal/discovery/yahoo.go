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
	"path"
	"strconv"
	"strings"
	"time"
)

const defaultYahooProvider = "yahoo"

type YahooPriceProviderOptions struct {
	Provider        string
	BaseURL         string
	Client          *http.Client
	Now             func() time.Time
	Calendar        MarketCalendar
	RequestBudget   int
	RequestInterval time.Duration
}

type YahooPriceProvider struct {
	options YahooPriceProviderOptions
	baseURL *url.URL
}

func NewYahooPriceProvider(options YahooPriceProviderOptions) (*YahooPriceProvider, error) {
	options.Provider = strings.ToLower(strings.TrimSpace(options.Provider))
	if options.Provider == "" {
		options.Provider = defaultYahooProvider
	}
	if strings.TrimSpace(options.BaseURL) == "" {
		options.BaseURL = "https://query1.finance.yahoo.com"
	}
	parsed, err := url.Parse(options.BaseURL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
		return nil, errors.New("yahoo base URL must be HTTPS without user info")
	}
	if options.Client == nil {
		options.Client = &http.Client{Timeout: 30 * time.Second}
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.Calendar == nil {
		return nil, errors.New("yahoo market calendar is required")
	}
	return &YahooPriceProvider{options: options, baseURL: parsed}, nil
}

func (p *YahooPriceProvider) ProviderName() string { return p.options.Provider }

func (p *YahooPriceProvider) Load(ctx context.Context, expected []Listing) ([]PriceRecord, ProviderResult, error) {
	target, err := nyCivilDate(p.options.Now())
	if err != nil {
		return nil, ProviderResult{}, err
	}
	return p.load(ctx, expected, target)
}

func (p *YahooPriceProvider) LoadForDate(ctx context.Context, expected []Listing, effectiveDate string) ([]PriceRecord, ProviderResult, error) {
	return p.load(ctx, expected, effectiveDate)
}

func (p *YahooPriceProvider) load(ctx context.Context, expected []Listing, effectiveDate string) ([]PriceRecord, ProviderResult, error) {
	target, err := parseNYCivilDate(effectiveDate)
	if err != nil {
		return nil, ProviderResult{}, err
	}
	limit := len(expected)
	if p.options.RequestBudget > 0 && p.options.RequestBudget < limit {
		limit = p.options.RequestBudget
	}
	records := make([]PriceRecord, 0, limit)
	for index, listing := range expected {
		if index >= limit {
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
		record, ok, err := p.loadListing(ctx, listing, target)
		if err != nil {
			return nil, ProviderResult{}, err
		}
		if ok {
			records = append(records, record)
		}
	}
	priceDate := latestPriceRecordDate(records)
	if priceDate.IsZero() {
		priceDate = target
	}
	sha := hashYahooPriceRecords(records)
	result, err := validatePriceBatch(ctx, records, PriceValidationOptions{
		Provider:                      p.options.Provider,
		SourceVersion:                 p.options.Provider + ":" + priceDate.Format(time.DateOnly),
		EffectiveDate:                 target,
		Now:                           p.options.Now(),
		Calendar:                      p.options.Calendar,
		Expected:                      expected,
		AllowPreviousTradingDatePrice: true,
	})
	if err != nil {
		return nil, ProviderResult{}, err
	}
	result.SHA256 = sha
	return records, result, nil
}

func (p *YahooPriceProvider) loadListing(ctx context.Context, listing Listing, target time.Time) (PriceRecord, bool, error) {
	requestURL := *p.baseURL
	symbol := strings.TrimSpace(listing.ProviderTicker)
	if symbol == "" {
		symbol = strings.TrimSpace(listing.Ticker)
	}
	if symbol == "" {
		return PriceRecord{}, false, nil
	}
	requestURL.Path = path.Join(requestURL.Path, "/v8/finance/chart", symbol)
	query := requestURL.Query()
	start := target.AddDate(0, 0, -10)
	end := target.AddDate(0, 0, 1)
	query.Set("period1", strconv.FormatInt(start.Unix(), 10))
	query.Set("period2", strconv.FormatInt(end.Unix(), 10))
	query.Set("interval", "1d")
	query.Set("events", "history")
	query.Set("includeAdjustedClose", "false")
	requestURL.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL.String(), nil)
	if err != nil {
		return PriceRecord{}, false, err
	}
	resp, err := p.options.Client.Do(req)
	if err != nil {
		return PriceRecord{}, false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusTooManyRequests {
		return PriceRecord{}, false, fmt.Errorf("yahoo rate limited with HTTP %d", resp.StatusCode)
	}
	if resp.StatusCode == http.StatusNotFound {
		_, _ = io.Copy(io.Discard, resp.Body)
		return PriceRecord{}, false, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, resp.Body)
		return PriceRecord{}, false, fmt.Errorf("yahoo returned HTTP %d", resp.StatusCode)
	}
	payload, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return PriceRecord{}, false, err
	}
	record, ok, err := parseYahooChartRecord(payload, listing.Ticker, p.options.Provider, target)
	if err != nil || !ok {
		return PriceRecord{}, ok, err
	}
	return record, true, nil
}

type yahooChartResponse struct {
	Chart struct {
		Result []struct {
			Timestamp  []int64 `json:"timestamp"`
			Indicators struct {
				Quote []struct {
					Open   []*float64 `json:"open"`
					High   []*float64 `json:"high"`
					Low    []*float64 `json:"low"`
					Close  []*float64 `json:"close"`
					Volume []*int64   `json:"volume"`
				} `json:"quote"`
			} `json:"indicators"`
		} `json:"result"`
		Error any `json:"error"`
	} `json:"chart"`
}

func parseYahooChartRecord(payload []byte, ticker, provider string, target time.Time) (PriceRecord, bool, error) {
	var response yahooChartResponse
	if err := json.Unmarshal(payload, &response); err != nil {
		return PriceRecord{}, false, err
	}
	if len(response.Chart.Result) == 0 || len(response.Chart.Result[0].Indicators.Quote) == 0 {
		return PriceRecord{}, false, nil
	}
	result := response.Chart.Result[0]
	quote := result.Indicators.Quote[0]
	targetText := target.Format(time.DateOnly)
	var selected PriceRecord
	var selectedDate string
	for i, ts := range result.Timestamp {
		if i >= len(quote.Open) || i >= len(quote.High) || i >= len(quote.Low) || i >= len(quote.Close) || i >= len(quote.Volume) {
			continue
		}
		if quote.Open[i] == nil || quote.High[i] == nil || quote.Low[i] == nil || quote.Close[i] == nil || quote.Volume[i] == nil {
			continue
		}
		date := time.Unix(ts, 0).UTC().Format(time.DateOnly)
		if date > targetText || date < selectedDate {
			continue
		}
		openMicros, err := parsePriceMicros(strconv.FormatFloat(*quote.Open[i], 'f', -1, 64))
		if err != nil {
			return PriceRecord{}, false, err
		}
		highMicros, err := parsePriceMicros(strconv.FormatFloat(*quote.High[i], 'f', -1, 64))
		if err != nil {
			return PriceRecord{}, false, err
		}
		lowMicros, err := parsePriceMicros(strconv.FormatFloat(*quote.Low[i], 'f', -1, 64))
		if err != nil {
			return PriceRecord{}, false, err
		}
		closeMicros, err := parsePriceMicros(strconv.FormatFloat(*quote.Close[i], 'f', -1, 64))
		if err != nil {
			return PriceRecord{}, false, err
		}
		tradeDate, err := parseNYCivilDate(date)
		if err != nil {
			return PriceRecord{}, false, err
		}
		selectedDate = date
		selected = PriceRecord{
			Symbol:      strings.ToUpper(strings.TrimSpace(ticker)),
			TradeDate:   tradeDate,
			OpenMicros:  openMicros,
			HighMicros:  highMicros,
			LowMicros:   lowMicros,
			CloseMicros: closeMicros,
			Volume:      *quote.Volume[i],
			Currency:    "USD",
			Adjusted:    false,
			Source:      provider,
		}
	}
	if selected.Symbol == "" {
		return PriceRecord{}, false, nil
	}
	return selected, true, nil
}

func hashYahooPriceRecords(records []PriceRecord) string {
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
