package discovery

import (
	"context"
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

func normalizeHistoryWindow(effectiveDate string, lookbackDays int) (time.Time, time.Time, error) {
	end, err := parseNYCivilDate(effectiveDate)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	if lookbackDays < minimumTechnicalHistoryLookbackDays {
		lookbackDays = minimumTechnicalHistoryLookbackDays
	}
	return end.AddDate(0, 0, -lookbackDays), end, nil
}

func (provider *TiingoPriceProvider) LoadHistory(ctx context.Context, expected []Listing, effectiveDate string, lookbackDays int) ([]PriceRecord, error) {
	start, end, err := normalizeHistoryWindow(effectiveDate, lookbackDays)
	if err != nil {
		return nil, err
	}
	state := &tiingoLoadState{budget: provider.totalRequestBudget()}
	records := []PriceRecord{}
	for _, request := range uniqueTiingoRequests(expected) {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if state.isRateLimited() {
			break
		}
		rows, ok, reason, err := provider.loadHistorySymbol(ctx, request, start, end, state)
		if err != nil {
			return nil, err
		}
		if ok {
			records = append(records, rows...)
			continue
		}
		if reason == "rate_limited" {
			state.markRateLimited()
		}
	}
	if len(records) == 0 {
		return nil, errors.New("tiingo returned no usable technical history")
	}
	return records, nil
}

func (provider *TiingoPriceProvider) loadHistorySymbol(ctx context.Context, request tiingoRequest, start, end time.Time, state *tiingoLoadState) ([]PriceRecord, bool, string, error) {
	if state.isRateLimited() {
		return nil, false, "rate_limited", nil
	}
	endpoint := *provider.baseURL
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + "/tiingo/daily/" + url.PathEscape(request.provider) + "/prices"
	query := endpoint.Query()
	query.Set("startDate", start.Format(time.DateOnly))
	query.Set("endDate", end.Format(time.DateOnly))
	query.Set("format", "json")
	endpoint.RawQuery = query.Encode()
	rateLimited := 0
	for _, token := range provider.nextTokenSequence() {
		if !state.acquireRequestBudget() {
			return nil, false, "request_budget_exhausted", nil
		}
		if err := provider.waitForRequestSlot(ctx); err != nil {
			return nil, false, "", err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
		if err != nil {
			return nil, false, "", err
		}
		req.Header.Set("Authorization", "Token "+token)
		req.Header.Set("Accept", "application/json")
		resp, err := provider.options.Client.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return nil, false, "", ctx.Err()
			}
			return nil, false, "network_error", nil
		}
		payload, readErr := readTiingoResponseBody(resp)
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			return nil, false, "", fmt.Errorf("tiingo authentication failed with HTTP %d", resp.StatusCode)
		}
		if resp.StatusCode == http.StatusTooManyRequests {
			rateLimited++
			continue
		}
		if resp.StatusCode >= 500 {
			return nil, false, "", fmt.Errorf("tiingo server error HTTP %d", resp.StatusCode)
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, false, fmt.Sprintf("http_%d", resp.StatusCode), nil
		}
		if readErr != nil {
			return nil, false, "read_error", nil
		}
		rows := parseTiingoHistoryRecords(payload, request.canonical, provider.options.Provider, start, end)
		if len(rows) > 0 {
			return rows, true, "", nil
		}
		return nil, false, "no_matching_trade_date", nil
	}
	if rateLimited > 0 {
		return nil, false, "rate_limited", nil
	}
	return nil, false, "no_token_available", nil
}

func parseTiingoHistoryRecords(payload []byte, canonical, source string, start, end time.Time) []PriceRecord {
	rows, ok := decodeTiingoDailyPrices(payload)
	if !ok {
		return nil
	}
	result := make([]PriceRecord, 0, len(rows))
	for _, row := range rows {
		record, err := tiingoDailyPriceToRecord(row, canonical, source)
		if err != nil || record.TradeDate.Before(start) || record.TradeDate.After(end) {
			continue
		}
		result = append(result, record)
	}
	return result
}

func (p *TwelveDataPriceProvider) LoadHistory(ctx context.Context, expected []Listing, effectiveDate string, lookbackDays int) ([]PriceRecord, error) {
	start, end, err := normalizeHistoryWindow(effectiveDate, lookbackDays)
	if err != nil {
		return nil, err
	}
	limit := len(expected)
	if p.options.RequestBudget > 0 && p.options.RequestBudget < limit {
		limit = p.options.RequestBudget
	}
	records := []PriceRecord{}
	for index, listing := range expected {
		if index >= limit {
			break
		}
		if index > 0 && p.options.RequestInterval > 0 {
			timer := time.NewTimer(p.options.RequestInterval)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil, ctx.Err()
			case <-timer.C:
			}
		}
		rows, ok, reason, err := p.loadHistoryListing(ctx, listing, start, end)
		if err != nil {
			return nil, err
		}
		if ok {
			records = append(records, rows...)
		} else if reason == "rate_limited" {
			break
		}
	}
	if len(records) == 0 {
		return nil, errors.New("twelve data returned no usable technical history")
	}
	return records, nil
}

func (p *TwelveDataPriceProvider) loadHistoryListing(ctx context.Context, listing Listing, start, end time.Time) ([]PriceRecord, bool, string, error) {
	symbol := strings.TrimSpace(listing.ProviderTicker)
	if symbol == "" {
		symbol = strings.TrimSpace(listing.Ticker)
	}
	if symbol == "" {
		return nil, false, "blank_symbol", nil
	}
	endpoint := *p.baseURL
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + "/time_series"
	query := endpoint.Query()
	query.Set("symbol", symbol)
	query.Set("interval", "1day")
	query.Set("start_date", start.Format(time.DateOnly))
	query.Set("end_date", end.Format(time.DateOnly))
	query.Set("outputsize", strconv.Itoa(int(end.Sub(start).Hours()/24)+10))
	query.Set("apikey", p.options.APIKey)
	endpoint.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, false, "", err
	}
	resp, err := p.options.Client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, false, "", ctx.Err()
		}
		return nil, false, "network_error", nil
	}
	defer resp.Body.Close()
	payload, readErr := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode == http.StatusTooManyRequests {
		return nil, false, "rate_limited", nil
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, false, "", fmt.Errorf("twelve data authentication failed with HTTP %d", resp.StatusCode)
	}
	if resp.StatusCode >= 500 {
		return nil, false, "", fmt.Errorf("twelve data server error HTTP %d", resp.StatusCode)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, false, fmt.Sprintf("http_%d", resp.StatusCode), nil
	}
	if readErr != nil {
		return nil, false, "read_error", nil
	}
	rows, reason, err := parseTwelveDataHistoryRecords(payload, listing.Ticker, p.options.Provider, start, end)
	if err != nil {
		return nil, false, reasonOrDefault(reason, "invalid_record"), nil
	}
	if len(rows) == 0 {
		return nil, false, reason, nil
	}
	return rows, true, "", nil
}

func parseTwelveDataHistoryRecords(payload []byte, ticker, provider string, start, end time.Time) ([]PriceRecord, string, error) {
	var response twelveDataResponse
	if err := json.Unmarshal(payload, &response); err != nil {
		return nil, "invalid_json", err
	}
	if strings.EqualFold(response.Status, "error") {
		if response.Code == http.StatusTooManyRequests || strings.Contains(strings.ToLower(response.Message), "credit") || strings.Contains(strings.ToLower(response.Message), "limit") {
			return nil, "rate_limited", nil
		}
		return nil, "api_error", nil
	}
	rows := []PriceRecord{}
	for _, value := range response.Values {
		tradeDate, err := parseNYCivilDate(strings.TrimSpace(value.Datetime))
		if err != nil || tradeDate.Before(start) || tradeDate.After(end) {
			continue
		}
		openMicros, openErr := parsePriceMicros(rawJSONScalar(value.Open))
		highMicros, highErr := parsePriceMicros(rawJSONScalar(value.High))
		lowMicros, lowErr := parsePriceMicros(rawJSONScalar(value.Low))
		closeMicros, closeErr := parsePriceMicros(rawJSONScalar(value.Close))
		volume, volumeErr := parseTwelveDataVolume(value.Volume)
		if openErr != nil || highErr != nil || lowErr != nil || closeErr != nil || volumeErr != nil {
			continue
		}
		record := PriceRecord{Symbol: strings.ToUpper(strings.TrimSpace(ticker)), TradeDate: tradeDate, OpenMicros: openMicros, HighMicros: highMicros, LowMicros: lowMicros, CloseMicros: closeMicros, Volume: volume, Currency: "USD", Source: provider}
		if validateOHLC(record) == nil {
			rows = append(rows, record)
		}
	}
	if len(rows) == 0 {
		return nil, "no_matching_trade_date", nil
	}
	return rows, "", nil
}

func (p *YahooPriceProvider) LoadHistory(ctx context.Context, expected []Listing, effectiveDate string, lookbackDays int) ([]PriceRecord, error) {
	start, end, err := normalizeHistoryWindow(effectiveDate, lookbackDays)
	if err != nil {
		return nil, err
	}
	limit := len(expected)
	if p.options.RequestBudget > 0 && p.options.RequestBudget < limit {
		limit = p.options.RequestBudget
	}
	records := []PriceRecord{}
	for index, listing := range expected {
		if index >= limit {
			break
		}
		if index > 0 && p.options.RequestInterval > 0 {
			timer := time.NewTimer(p.options.RequestInterval)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil, ctx.Err()
			case <-timer.C:
			}
		}
		rows, ok, err := p.loadHistoryListing(ctx, listing, start, end)
		if err != nil {
			return nil, err
		}
		if ok {
			records = append(records, rows...)
		}
	}
	if len(records) == 0 {
		return nil, errors.New("yahoo returned no usable technical history")
	}
	return records, nil
}

func (p *YahooPriceProvider) loadHistoryListing(ctx context.Context, listing Listing, start, end time.Time) ([]PriceRecord, bool, error) {
	symbol := strings.TrimSpace(listing.ProviderTicker)
	if symbol == "" {
		symbol = strings.TrimSpace(listing.Ticker)
	}
	if symbol == "" {
		return nil, false, nil
	}
	endpoint := *p.baseURL
	endpoint.Path = path.Join(endpoint.Path, "/v8/finance/chart", symbol)
	query := endpoint.Query()
	query.Set("period1", strconv.FormatInt(start.Unix(), 10))
	query.Set("period2", strconv.FormatInt(end.AddDate(0, 0, 1).Unix(), 10))
	query.Set("interval", "1d")
	query.Set("events", "history")
	query.Set("includeAdjustedClose", "false")
	endpoint.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, false, err
	}
	resp, err := p.options.Client.Do(req)
	if err != nil {
		return nil, false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusTooManyRequests {
		return nil, false, fmt.Errorf("yahoo rate limited with HTTP %d", resp.StatusCode)
	}
	if resp.StatusCode == http.StatusNotFound {
		return nil, false, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, false, fmt.Errorf("yahoo returned HTTP %d", resp.StatusCode)
	}
	payload, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, false, err
	}
	rows, err := parseYahooChartHistoryRecords(payload, listing.Ticker, p.options.Provider, start, end)
	return rows, len(rows) > 0, err
}

func parseYahooChartHistoryRecords(payload []byte, ticker, provider string, start, end time.Time) ([]PriceRecord, error) {
	var response yahooChartResponse
	if err := json.Unmarshal(payload, &response); err != nil {
		return nil, err
	}
	if len(response.Chart.Result) == 0 || len(response.Chart.Result[0].Indicators.Quote) == 0 {
		return nil, nil
	}
	result, quote := response.Chart.Result[0], response.Chart.Result[0].Indicators.Quote[0]
	rows := []PriceRecord{}
	for index, timestamp := range result.Timestamp {
		if index >= len(quote.Open) || index >= len(quote.High) || index >= len(quote.Low) || index >= len(quote.Close) || index >= len(quote.Volume) || quote.Open[index] == nil || quote.High[index] == nil || quote.Low[index] == nil || quote.Close[index] == nil || quote.Volume[index] == nil {
			continue
		}
		tradeDate, err := parseNYCivilDate(time.Unix(timestamp, 0).UTC().Format(time.DateOnly))
		if err != nil || tradeDate.Before(start) || tradeDate.After(end) {
			continue
		}
		openMicros, openErr := parsePriceMicros(strconv.FormatFloat(*quote.Open[index], 'f', -1, 64))
		highMicros, highErr := parsePriceMicros(strconv.FormatFloat(*quote.High[index], 'f', -1, 64))
		lowMicros, lowErr := parsePriceMicros(strconv.FormatFloat(*quote.Low[index], 'f', -1, 64))
		closeMicros, closeErr := parsePriceMicros(strconv.FormatFloat(*quote.Close[index], 'f', -1, 64))
		if openErr != nil || highErr != nil || lowErr != nil || closeErr != nil {
			continue
		}
		record := PriceRecord{Symbol: strings.ToUpper(strings.TrimSpace(ticker)), TradeDate: tradeDate, OpenMicros: openMicros, HighMicros: highMicros, LowMicros: lowMicros, CloseMicros: closeMicros, Volume: *quote.Volume[index], Currency: "USD", Source: provider}
		if validateOHLC(record) == nil {
			rows = append(rows, record)
		}
	}
	return rows, nil
}

func (p *PriceProviderChain) LoadHistory(ctx context.Context, expected []Listing, effectiveDate string, lookbackDays int) ([]PriceRecord, error) {
	remaining := append([]Listing(nil), expected...)
	covered := map[string]struct{}{}
	dayCounts := map[string]map[string]struct{}{}
	records := []PriceRecord{}
	var lastErr error
	for _, child := range p.providers {
		if len(remaining) == 0 {
			break
		}
		history, ok := child.(HistoricalPriceProvider)
		if !ok {
			lastErr = fmt.Errorf("price provider %s does not support technical history", providerName(child))
			continue
		}
		rows, err := history.LoadHistory(ctx, remaining, effectiveDate, lookbackDays)
		if err != nil {
			lastErr = err
			continue
		}
		for _, row := range rows {
			symbol := strings.ToUpper(strings.TrimSpace(row.Symbol))
			if symbol == "" {
				continue
			}
			records = append(records, row)
			if dayCounts[symbol] == nil {
				dayCounts[symbol] = map[string]struct{}{}
			}
			dayCounts[symbol][row.TradeDate.Format(time.DateOnly)] = struct{}{}
			if len(dayCounts[symbol]) >= technicalHistorySamplesRequired {
				covered[symbol] = struct{}{}
			}
		}
		remaining = missingListings(expected, covered)
	}
	if len(records) == 0 && lastErr != nil {
		return nil, lastErr
	}
	if len(records) == 0 {
		return nil, errors.New("price provider chain returned no usable technical history")
	}
	return records, nil
}

var _ HistoricalPriceProvider = (*TiingoPriceProvider)(nil)
var _ HistoricalPriceProvider = (*TwelveDataPriceProvider)(nil)
var _ HistoricalPriceProvider = (*YahooPriceProvider)(nil)
var _ HistoricalPriceProvider = (*PriceProviderChain)(nil)
