package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strings"
	"time"

	"sec_monitor/internal/config"
	"sec_monitor/internal/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const usFuturesSourceYahoo = "yahoo_finance"

var ErrUSFuturesRateLimited = errors.New("Yahoo Finance rate limited")

var usFuturesDefinitions = []marketTrendDefinition{
	{Symbol: "ES=F", Label: "标普 500 E-mini", Group: "futures", SortOrder: 10},
	{Symbol: "NQ=F", Label: "纳指 100 E-mini", Group: "futures", SortOrder: 20},
	{Symbol: "YM=F", Label: "道指 E-mini", Group: "futures", SortOrder: 30},
	{Symbol: "RTY=F", Label: "罗素 2000 E-mini", Group: "futures", SortOrder: 40},
	{Symbol: "CL=F", Label: "WTI 原油", Group: "futures", SortOrder: 50},
	{Symbol: "GC=F", Label: "COMEX 黄金", Group: "futures", SortOrder: 60},
	{Symbol: "SI=F", Label: "COMEX 白银", Group: "futures", SortOrder: 70},
	{Symbol: "NG=F", Label: "天然气", Group: "futures", SortOrder: 80},
	{Symbol: "ZN=F", Label: "10 年期美债", Group: "futures", SortOrder: 90},
	{Symbol: "ZB=F", Label: "30 年期美债", Group: "futures", SortOrder: 100},
}

type usFuturesChartResponse struct {
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
	} `json:"chart"`
}

type USFuturesResponse struct {
	Source      string              `json:"source"`
	LastFetched *time.Time          `json:"last_fetched_at,omitempty"`
	Futures     []MarketTrendSeries `json:"futures"`
}

type USFuturesRefreshResult struct {
	SymbolsRequested int      `json:"symbols_requested"`
	SymbolsUpdated   int      `json:"symbols_updated"`
	BarsSaved        int      `json:"bars_saved"`
	Warnings         []string `json:"warnings"`
}

// USFuturesService uses Yahoo Finance's continuous futures symbols. Longbridge
// currently has no futures market code, so this is deliberately a separate
// source from the Longbridge-backed market-trend view.
type USFuturesService struct {
	db      *gorm.DB
	configs *ConfigService
	runtime config.DiscoveryConfig
	client  *http.Client
	now     func() time.Time
}

func NewUSFuturesService(db *gorm.DB, configs *ConfigService, runtime config.DiscoveryConfig) *USFuturesService {
	return &USFuturesService{db: db, configs: configs, runtime: runtime, client: &http.Client{Timeout: 30 * time.Second}, now: time.Now}
}

func (s *USFuturesService) List(ctx context.Context, historyDays int) (USFuturesResponse, error) {
	if s == nil || s.db == nil {
		return USFuturesResponse{}, errors.New("US futures service is not configured")
	}
	if historyDays < 20 || historyDays > 365 {
		historyDays = 120
	}
	var rows []model.MarketTrendDaily
	if err := s.db.WithContext(ctx).Where("group_name = ?", "futures").Order("sort_order ASC, trade_date ASC").Find(&rows).Error; err != nil {
		return USFuturesResponse{}, err
	}
	result := USFuturesResponse{Source: usFuturesSourceYahoo, Futures: []MarketTrendSeries{}}
	bySymbol := make(map[string][]model.MarketTrendDaily)
	for _, row := range rows {
		bySymbol[row.Symbol] = append(bySymbol[row.Symbol], row)
		if result.LastFetched == nil || row.FetchedAt.After(*result.LastFetched) {
			value := row.FetchedAt
			result.LastFetched = &value
		}
	}
	for _, definition := range usFuturesDefinitions {
		if seriesRows := bySymbol[definition.Symbol]; len(seriesRows) > 0 {
			result.Futures = append(result.Futures, buildMarketTrendSeries(seriesRows, historyDays))
		}
	}
	return result, nil
}

func (s *USFuturesService) Refresh(ctx context.Context) (USFuturesRefreshResult, error) {
	if s == nil || s.db == nil {
		return USFuturesRefreshResult{}, errors.New("US futures service is not configured")
	}
	cfg := s.runtime
	var err error
	if s.configs != nil {
		cfg, err = s.configs.ApplyDiscoveryConfig(ctx, cfg)
		if err != nil {
			return USFuturesRefreshResult{}, err
		}
	}
	baseURL, err := parseUSFuturesBaseURL(cfg.YahooBaseURL)
	if err != nil {
		return USFuturesRefreshResult{}, err
	}
	now := s.now().UTC()
	result := USFuturesRefreshResult{SymbolsRequested: len(usFuturesDefinitions), Warnings: []string{}}
	rateLimited := false
	for _, definition := range usFuturesDefinitions {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		rows, fetchErr := s.fetchHistory(ctx, baseURL, definition, now.AddDate(0, -7, 0), now.AddDate(0, 0, 1), now)
		if fetchErr != nil {
			result.Warnings = append(result.Warnings, definition.Label+"："+SanitizeSensitiveError(fetchErr.Error()))
			// A public provider's 429 is account/IP scoped. Do not turn one
			// rejected request into ten further requests in the same run.
			if errors.Is(fetchErr, ErrUSFuturesRateLimited) {
				rateLimited = true
				result.Warnings = append(result.Warnings, "已在首次限流后停止本轮剩余期货请求；请等待下次计划运行或配置已授权的数据源。")
				break
			}
			continue
		}
		if len(rows) == 0 {
			result.Warnings = append(result.Warnings, definition.Label+"：未返回可用日线")
			continue
		}
		if err := s.store(ctx, rows); err != nil {
			return result, err
		}
		result.SymbolsUpdated++
		result.BarsSaved += len(rows)
	}
	if result.SymbolsUpdated == 0 && len(result.Warnings) > 0 {
		if rateLimited {
			return result, fmt.Errorf("%w: %s", ErrUSFuturesRateLimited, strings.Join(result.Warnings, "；"))
		}
		return result, errors.New("期货行情源未返回可用日线：" + strings.Join(result.Warnings, "；"))
	}
	return result, nil
}

func parseUSFuturesBaseURL(raw string) (*url.URL, error) {
	if strings.TrimSpace(raw) == "" {
		raw = "https://query1.finance.yahoo.com"
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
		return nil, errors.New("futures data source URL must be HTTPS without user info")
	}
	return parsed, nil
}

func (s *USFuturesService) fetchHistory(ctx context.Context, baseURL *url.URL, definition marketTrendDefinition, start, end, fetchedAt time.Time) ([]model.MarketTrendDaily, error) {
	requestURL := *baseURL
	requestURL.Path = path.Join(requestURL.Path, "/v8/finance/chart", definition.Symbol)
	query := requestURL.Query()
	query.Set("period1", fmt.Sprintf("%d", start.Unix()))
	query.Set("period2", fmt.Sprintf("%d", end.Unix()))
	query.Set("interval", "1d")
	query.Set("events", "history")
	query.Set("includeAdjustedClose", "false")
	requestURL.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL.String(), nil)
	if err != nil {
		return nil, err
	}
	response, err := s.client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusTooManyRequests {
		return nil, fmt.Errorf("%w with HTTP %d", ErrUSFuturesRateLimited, response.StatusCode)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		_, _ = io.Copy(io.Discard, response.Body)
		return nil, fmt.Errorf("Yahoo Finance returned HTTP %d", response.StatusCode)
	}
	payload, err := io.ReadAll(io.LimitReader(response.Body, 2<<20))
	if err != nil {
		return nil, err
	}
	return parseUSFuturesHistory(payload, definition, fetchedAt)
}

func parseUSFuturesHistory(payload []byte, definition marketTrendDefinition, fetchedAt time.Time) ([]model.MarketTrendDaily, error) {
	var response usFuturesChartResponse
	if err := json.Unmarshal(payload, &response); err != nil {
		return nil, err
	}
	if len(response.Chart.Result) == 0 || len(response.Chart.Result[0].Indicators.Quote) == 0 {
		return nil, nil
	}
	result, quote := response.Chart.Result[0], response.Chart.Result[0].Indicators.Quote[0]
	rows := make([]model.MarketTrendDaily, 0, len(result.Timestamp))
	seen := map[string]struct{}{}
	for index, timestamp := range result.Timestamp {
		if index >= len(quote.Open) || index >= len(quote.High) || index >= len(quote.Low) || index >= len(quote.Close) || index >= len(quote.Volume) || quote.Open[index] == nil || quote.High[index] == nil || quote.Low[index] == nil || quote.Close[index] == nil || quote.Volume[index] == nil || *quote.Close[index] <= 0 {
			continue
		}
		tradeDate := time.Unix(timestamp, 0).UTC().Format(time.DateOnly)
		if _, ok := seen[tradeDate]; ok {
			continue
		}
		seen[tradeDate] = struct{}{}
		rows = append(rows, model.MarketTrendDaily{Symbol: definition.Symbol, Label: definition.Label, Group: definition.Group, SortOrder: definition.SortOrder, TradeDate: tradeDate, Open: *quote.Open[index], High: *quote.High[index], Low: *quote.Low[index], Close: *quote.Close[index], Volume: *quote.Volume[index], Source: usFuturesSourceYahoo, FetchedAt: fetchedAt})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].TradeDate < rows[j].TradeDate })
	return rows, nil
}

func (s *USFuturesService) store(ctx context.Context, rows []model.MarketTrendDaily) error {
	return s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "symbol"}, {Name: "trade_date"}},
		DoUpdates: clause.AssignmentColumns([]string{"label", "group_name", "sort_order", "open", "high", "low", "close", "volume", "source", "fetched_at", "updated_at"}),
	}).Create(&rows).Error
}
