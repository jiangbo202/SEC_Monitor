package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	"sec_monitor/internal/config"
	"sec_monitor/internal/model"

	lbconfig "github.com/longbridge/openapi-go/config"
	lbhttp "github.com/longbridge/openapi-go/http"
	lbquote "github.com/longbridge/openapi-go/quote"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const marketTrendSourceLongbridge = "longbridge"

type marketTrendDefinition struct {
	Symbol    string
	Label     string
	Group     string
	SortOrder int
}

var marketTrendDefinitions = []marketTrendDefinition{
	{Symbol: ".SPX.US", Label: "标普 500", Group: "market", SortOrder: 10},
	{Symbol: ".NDX.US", Label: "纳斯达克 100", Group: "market", SortOrder: 20},
	{Symbol: ".DJI.US", Label: "道琼斯工业", Group: "market", SortOrder: 30},
	{Symbol: "IWM.US", Label: "罗素 2000（IWM）", Group: "market", SortOrder: 40},
	{Symbol: ".VIX.US", Label: "VIX 波动率", Group: "market", SortOrder: 50},
	{Symbol: "XLK.US", Label: "信息技术", Group: "sector", SortOrder: 10},
	{Symbol: "XLC.US", Label: "通信服务", Group: "sector", SortOrder: 20},
	{Symbol: "XLY.US", Label: "可选消费", Group: "sector", SortOrder: 30},
	{Symbol: "XLP.US", Label: "必选消费", Group: "sector", SortOrder: 40},
	{Symbol: "XLE.US", Label: "能源", Group: "sector", SortOrder: 50},
	{Symbol: "XLF.US", Label: "金融", Group: "sector", SortOrder: 60},
	{Symbol: "XLV.US", Label: "医疗保健", Group: "sector", SortOrder: 70},
	{Symbol: "XLI.US", Label: "工业", Group: "sector", SortOrder: 80},
	{Symbol: "XLB.US", Label: "原材料", Group: "sector", SortOrder: 90},
	{Symbol: "XLRE.US", Label: "房地产", Group: "sector", SortOrder: 100},
	{Symbol: "XLU.US", Label: "公用事业", Group: "sector", SortOrder: 110},
}

type marketTrendCandle struct {
	Open, High, Low, Close string
	Timestamp              int64
	Volume                 int64
}

type marketTrendLongbridgeClient interface {
	HistoryDaily(context.Context, string, time.Time, time.Time) ([]marketTrendCandle, error)
	Close() error
}

type marketTrendLongbridgeSDKClient struct{ quote *lbquote.QuoteContext }

type marketTemperatureRecord struct {
	Timestamp   int64  `json:"timestamp"`
	Temperature int    `json:"temperature"`
	Valuation   int    `json:"valuation"`
	Sentiment   int    `json:"sentiment"`
	Description string `json:"description"`
}

// UnmarshalJSON accepts both variants returned by Longbridge: some endpoints
// encode timestamp as a JSON number, while the temperature-history endpoint
// currently encodes it as a quoted Unix timestamp.
func (r *marketTemperatureRecord) UnmarshalJSON(data []byte) error {
	type rawRecord struct {
		Timestamp   json.RawMessage `json:"timestamp"`
		Temperature int             `json:"temperature"`
		Valuation   int             `json:"valuation"`
		Sentiment   int             `json:"sentiment"`
		Description string          `json:"description"`
	}
	var raw rawRecord
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	var timestamp int64
	if len(raw.Timestamp) > 0 && string(raw.Timestamp) != "null" {
		value := strings.Trim(string(raw.Timestamp), "\"")
		parsed, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return fmt.Errorf("parse market temperature timestamp %q: %w", value, err)
		}
		timestamp = parsed
	}
	*r = marketTemperatureRecord{Timestamp: timestamp, Temperature: raw.Temperature, Valuation: raw.Valuation, Sentiment: raw.Sentiment, Description: raw.Description}
	return nil
}

type marketTemperatureHistoryResponse struct {
	Type string                    `json:"type"`
	List []marketTemperatureRecord `json:"list"`
}

type marketTemperatureLongbridgeClient interface {
	Current(context.Context, string) (marketTemperatureRecord, error)
	History(context.Context, string, time.Time, time.Time) ([]marketTemperatureRecord, error)
}

type marketTemperatureLongbridgeHTTPClient struct{ client *lbhttp.Client }

func newMarketTemperatureLongbridgeClient(appKey, appSecret, accessToken string) (marketTemperatureLongbridgeClient, error) {
	cfg, err := lbconfig.New(lbconfig.WithConfigKey(appKey, appSecret, accessToken))
	if err != nil {
		return nil, err
	}
	client, err := lbhttp.NewFromCfg(cfg)
	if err != nil {
		return nil, err
	}
	return &marketTemperatureLongbridgeHTTPClient{client: client}, nil
}

func (c *marketTemperatureLongbridgeHTTPClient) Current(ctx context.Context, market string) (marketTemperatureRecord, error) {
	var result marketTemperatureRecord
	err := c.client.Get(ctx, "/v1/quote/market_temperature", url.Values{"market": []string{market}}, &result)
	return result, err
}

func (c *marketTemperatureLongbridgeHTTPClient) History(ctx context.Context, market string, start, end time.Time) ([]marketTemperatureRecord, error) {
	var result marketTemperatureHistoryResponse
	params := url.Values{"market": []string{market}, "start_date": []string{start.Format("20060102")}, "end_date": []string{end.Format("20060102")}}
	if err := c.client.Get(ctx, "/v1/quote/history_market_temperature", params, &result); err != nil {
		return nil, err
	}
	return result.List, nil
}

func newMarketTrendLongbridgeClient(appKey, appSecret, accessToken string) (marketTrendLongbridgeClient, error) {
	cfg, err := lbconfig.New(lbconfig.WithConfigKey(appKey, appSecret, accessToken))
	if err != nil {
		return nil, err
	}
	client, err := lbquote.NewFromCfg(cfg)
	if err != nil {
		return nil, err
	}
	return &marketTrendLongbridgeSDKClient{quote: client}, nil
}

func (c *marketTrendLongbridgeSDKClient) HistoryDaily(ctx context.Context, symbol string, start, end time.Time) ([]marketTrendCandle, error) {
	items, err := c.quote.HistoryCandlesticksByDate(ctx, symbol, lbquote.PeriodDay, lbquote.AdjustTypeNo, &start, &end)
	if err != nil {
		return nil, err
	}
	result := make([]marketTrendCandle, 0, len(items))
	for _, item := range items {
		if item == nil {
			continue
		}
		result = append(result, marketTrendCandle{Open: decimalString(item.Open), High: decimalString(item.High), Low: decimalString(item.Low), Close: decimalString(item.Close), Timestamp: item.Timestamp, Volume: item.Volume})
	}
	return result, nil
}

func (c *marketTrendLongbridgeSDKClient) Close() error { return c.quote.Close() }

func decimalString(value interface{ String() string }) string {
	if value == nil {
		return ""
	}
	reflected := reflect.ValueOf(value)
	if (reflected.Kind() == reflect.Ptr || reflected.Kind() == reflect.Interface || reflected.Kind() == reflect.Map || reflected.Kind() == reflect.Slice || reflected.Kind() == reflect.Func) && reflected.IsNil() {
		return ""
	}
	return value.String()
}

type MarketTrendPoint struct {
	Date  string  `json:"date"`
	Close float64 `json:"close"`
}

type MarketTrendSeries struct {
	Symbol       string             `json:"symbol"`
	Label        string             `json:"label"`
	TradeDate    string             `json:"trade_date"`
	Open         float64            `json:"open"`
	High         float64            `json:"high"`
	Low          float64            `json:"low"`
	Close        float64            `json:"close"`
	Volume       int64              `json:"volume"`
	Change1DPct  *float64           `json:"change_1d_pct,omitempty"`
	Change5DPct  *float64           `json:"change_5d_pct,omitempty"`
	Change20DPct *float64           `json:"change_20d_pct,omitempty"`
	History      []MarketTrendPoint `json:"history"`
}

type MarketTrendResponse struct {
	Source      string              `json:"source"`
	LastFetched *time.Time          `json:"last_fetched_at,omitempty"`
	Market      []MarketTrendSeries `json:"market"`
	Sectors     []MarketTrendSeries `json:"sectors"`
	Temperature *MarketTemperature  `json:"temperature,omitempty"`
}

type MarketTemperaturePoint struct {
	Date        string `json:"date"`
	Temperature int    `json:"temperature"`
	Valuation   int    `json:"valuation"`
	Sentiment   int    `json:"sentiment"`
}

type MarketTemperature struct {
	Market      string                   `json:"market"`
	TradeDate   string                   `json:"trade_date"`
	Temperature int                      `json:"temperature"`
	Valuation   int                      `json:"valuation"`
	Sentiment   int                      `json:"sentiment"`
	Description string                   `json:"description,omitempty"`
	History     []MarketTemperaturePoint `json:"history"`
}

type MarketTrendRefreshResult struct {
	SymbolsRequested int      `json:"symbols_requested"`
	SymbolsUpdated   int      `json:"symbols_updated"`
	BarsSaved        int      `json:"bars_saved"`
	TemperatureSaved int      `json:"temperature_saved"`
	Warnings         []string `json:"warnings"`
}

// MarketTrendService caches Longbridge daily bars for broad US indices and
// the standard S&P sector ETFs. Reads never call the external provider.
type MarketTrendService struct {
	db                   *gorm.DB
	configs              *ConfigService
	runtime              config.DiscoveryConfig
	now                  func() time.Time
	newClient            func(string, string, string) (marketTrendLongbridgeClient, error)
	newTemperatureClient func(string, string, string) (marketTemperatureLongbridgeClient, error)
}

func NewMarketTrendService(db *gorm.DB, configs *ConfigService, runtime config.DiscoveryConfig) *MarketTrendService {
	return &MarketTrendService{db: db, configs: configs, runtime: runtime, now: time.Now, newClient: newMarketTrendLongbridgeClient, newTemperatureClient: newMarketTemperatureLongbridgeClient}
}

func (s *MarketTrendService) List(ctx context.Context, historyDays int) (MarketTrendResponse, error) {
	if s == nil || s.db == nil {
		return MarketTrendResponse{}, errors.New("market trend service is not configured")
	}
	if historyDays < 20 || historyDays > 365 {
		historyDays = 120
	}
	var rows []model.MarketTrendDaily
	if err := s.db.WithContext(ctx).Order("group_name ASC, sort_order ASC, trade_date ASC").Find(&rows).Error; err != nil {
		return MarketTrendResponse{}, err
	}
	result := MarketTrendResponse{Source: marketTrendSourceLongbridge, Market: []MarketTrendSeries{}, Sectors: []MarketTrendSeries{}}
	bySymbol := make(map[string][]model.MarketTrendDaily)
	for _, row := range rows {
		bySymbol[row.Symbol] = append(bySymbol[row.Symbol], row)
		if result.LastFetched == nil || row.FetchedAt.After(*result.LastFetched) {
			value := row.FetchedAt
			result.LastFetched = &value
		}
	}
	for _, definition := range marketTrendDefinitions {
		seriesRows := bySymbol[definition.Symbol]
		if len(seriesRows) == 0 {
			continue
		}
		series := buildMarketTrendSeries(seriesRows, historyDays)
		if definition.Group == "market" {
			result.Market = append(result.Market, series)
		} else {
			result.Sectors = append(result.Sectors, series)
		}
	}
	var temperatureRows []model.MarketTemperatureDaily
	if err := s.db.WithContext(ctx).Where("market = ?", "US").Order("trade_date ASC").Find(&temperatureRows).Error; err != nil {
		return MarketTrendResponse{}, err
	}
	if len(temperatureRows) > 0 {
		latest := temperatureRows[len(temperatureRows)-1]
		start := len(temperatureRows) - historyDays
		if start < 0 {
			start = 0
		}
		history := make([]MarketTemperaturePoint, 0, len(temperatureRows)-start)
		for _, row := range temperatureRows[start:] {
			history = append(history, MarketTemperaturePoint{Date: row.TradeDate, Temperature: row.Temperature, Valuation: row.Valuation, Sentiment: row.Sentiment})
		}
		result.Temperature = &MarketTemperature{Market: latest.Market, TradeDate: latest.TradeDate, Temperature: latest.Temperature, Valuation: latest.Valuation, Sentiment: latest.Sentiment, Description: latest.Description, History: history}
		if result.LastFetched == nil || latest.FetchedAt.After(*result.LastFetched) {
			value := latest.FetchedAt
			result.LastFetched = &value
		}
	}
	return result, nil
}

func buildMarketTrendSeries(rows []model.MarketTrendDaily, historyDays int) MarketTrendSeries {
	latest := rows[len(rows)-1]
	start := len(rows) - historyDays
	if start < 0 {
		start = 0
	}
	history := make([]MarketTrendPoint, 0, len(rows)-start)
	for _, row := range rows[start:] {
		history = append(history, MarketTrendPoint{Date: row.TradeDate, Close: row.Close})
	}
	return MarketTrendSeries{
		Symbol: latest.Symbol, Label: latest.Label, TradeDate: latest.TradeDate, Open: latest.Open, High: latest.High, Low: latest.Low, Close: latest.Close, Volume: latest.Volume,
		Change1DPct: marketTrendChange(rows, 1), Change5DPct: marketTrendChange(rows, 5), Change20DPct: marketTrendChange(rows, 20), History: history,
	}
}

func marketTrendChange(rows []model.MarketTrendDaily, days int) *float64 {
	index := len(rows) - 1 - days
	if index < 0 || rows[index].Close == 0 {
		return nil
	}
	value := (rows[len(rows)-1].Close/rows[index].Close - 1) * 100
	return &value
}

func (s *MarketTrendService) Refresh(ctx context.Context) (MarketTrendRefreshResult, error) {
	if s == nil || s.db == nil {
		return MarketTrendRefreshResult{}, errors.New("market trend service is not configured")
	}
	cfg := s.runtime
	var err error
	if s.configs != nil {
		cfg, err = s.configs.ApplyDiscoveryConfig(ctx, cfg)
		if err != nil {
			return MarketTrendRefreshResult{}, err
		}
	}
	if strings.TrimSpace(cfg.LongbridgeAppKey) == "" || strings.TrimSpace(cfg.LongbridgeAppSecret) == "" || strings.TrimSpace(cfg.LongbridgeAccessToken) == "" {
		return MarketTrendRefreshResult{}, errors.New("Longbridge 行情凭据未配置，请在系统配置中填写 App Key、App Secret 和 Access Token")
	}
	client, err := s.newClient(cfg.LongbridgeAppKey, cfg.LongbridgeAppSecret, cfg.LongbridgeAccessToken)
	if err != nil {
		return MarketTrendRefreshResult{}, fmt.Errorf("create Longbridge market client: %w", err)
	}
	defer func() { _ = client.Close() }()

	now := s.now().UTC()
	start := now.AddDate(0, -7, 0)
	result := MarketTrendRefreshResult{SymbolsRequested: len(marketTrendDefinitions), Warnings: []string{}}
	for _, definition := range marketTrendDefinitions {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		candles, fetchErr := client.HistoryDaily(ctx, definition.Symbol, start, now)
		if fetchErr != nil {
			result.Warnings = append(result.Warnings, definition.Label+"："+SanitizeSensitiveError(fetchErr.Error()))
			continue
		}
		saved, storeErr := s.storeDailyBars(ctx, definition, candles, now)
		if storeErr != nil {
			return result, storeErr
		}
		if saved > 0 {
			result.SymbolsUpdated++
			result.BarsSaved += saved
		}
	}
	temperatureClient, temperatureErr := s.newTemperatureClient(cfg.LongbridgeAppKey, cfg.LongbridgeAppSecret, cfg.LongbridgeAccessToken)
	if temperatureErr != nil {
		result.Warnings = append(result.Warnings, "市场温度客户端："+SanitizeSensitiveError(temperatureErr.Error()))
	} else if saved, syncErr := s.syncMarketTemperature(ctx, temperatureClient, now); syncErr != nil {
		result.Warnings = append(result.Warnings, "市场温度："+SanitizeSensitiveError(syncErr.Error()))
	} else {
		result.TemperatureSaved = saved
	}
	if result.SymbolsUpdated == 0 && len(result.Warnings) > 0 {
		return result, errors.New("Longbridge 未返回可用的大盘趋势日线")
	}
	return result, nil
}

func (s *MarketTrendService) syncMarketTemperature(ctx context.Context, client marketTemperatureLongbridgeClient, now time.Time) (int, error) {
	start := now.AddDate(0, -1, 0)
	var latest model.MarketTemperatureDaily
	if err := s.db.WithContext(ctx).Where("market = ?", "US").Order("trade_date DESC").First(&latest).Error; err == nil {
		if parsed, parseErr := time.Parse(time.DateOnly, latest.TradeDate); parseErr == nil {
			start = parsed.AddDate(0, 0, -14)
		}
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, err
	}
	history, err := client.History(ctx, "US", start, now)
	if err != nil {
		return 0, err
	}
	current, currentErr := client.Current(ctx, "US")
	if currentErr == nil && current.Timestamp > 0 {
		history = append(history, current)
	}
	rows := make([]model.MarketTemperatureDaily, 0, len(history))
	seen := map[string]bool{}
	location, err := time.LoadLocation("America/New_York")
	if err != nil {
		return 0, err
	}
	for _, item := range history {
		if item.Timestamp <= 0 {
			continue
		}
		date := time.Unix(item.Timestamp, 0).In(location).Format(time.DateOnly)
		if seen[date] {
			continue
		}
		seen[date] = true
		rows = append(rows, model.MarketTemperatureDaily{Market: "US", TradeDate: date, Temperature: item.Temperature, Valuation: item.Valuation, Sentiment: item.Sentiment, Description: item.Description, Source: marketTrendSourceLongbridge, FetchedAt: now})
	}
	if len(rows) == 0 {
		return 0, nil
	}
	err = s.db.WithContext(ctx).Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "market"}, {Name: "trade_date"}}, DoUpdates: clause.AssignmentColumns([]string{"temperature", "valuation", "sentiment", "description", "source", "fetched_at", "updated_at"})}).Create(&rows).Error
	return len(rows), err
}

func (s *MarketTrendService) storeDailyBars(ctx context.Context, definition marketTrendDefinition, candles []marketTrendCandle, fetchedAt time.Time) (int, error) {
	location, err := time.LoadLocation("America/New_York")
	if err != nil {
		return 0, err
	}
	rows := make([]model.MarketTrendDaily, 0, len(candles))
	seen := map[string]struct{}{}
	for _, candle := range candles {
		if candle.Timestamp <= 0 {
			continue
		}
		open, openErr := strconv.ParseFloat(candle.Open, 64)
		high, highErr := strconv.ParseFloat(candle.High, 64)
		low, lowErr := strconv.ParseFloat(candle.Low, 64)
		close, closeErr := strconv.ParseFloat(candle.Close, 64)
		if openErr != nil || highErr != nil || lowErr != nil || closeErr != nil || close <= 0 {
			continue
		}
		date := time.Unix(candle.Timestamp, 0).In(location).Format(time.DateOnly)
		if _, ok := seen[date]; ok {
			continue
		}
		seen[date] = struct{}{}
		rows = append(rows, model.MarketTrendDaily{Symbol: definition.Symbol, Label: definition.Label, Group: definition.Group, SortOrder: definition.SortOrder, TradeDate: date, Open: open, High: high, Low: low, Close: close, Volume: candle.Volume, Source: marketTrendSourceLongbridge, FetchedAt: fetchedAt})
	}
	if len(rows) == 0 {
		return 0, nil
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].TradeDate < rows[j].TradeDate })
	err = s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "symbol"}, {Name: "trade_date"}},
		DoUpdates: clause.AssignmentColumns([]string{"label", "group_name", "sort_order", "open", "high", "low", "close", "volume", "source", "fetched_at", "updated_at"}),
	}).Create(&rows).Error
	return len(rows), err
}
