package service

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"sec_monitor/internal/discovery"
	"sec_monitor/internal/model"
)

const usFuturesSourceFutu = "futu"

var ErrUSFuturesRateLimited = errors.New("Futu futures rate limited")

var usFuturesDefinitions = []marketTrendDefinition{
	{Symbol: "US.ESmain", Label: "标普 500 E-mini", Group: "futures", SortOrder: 10},
	{Symbol: "US.NQmain", Label: "纳指 100 E-mini", Group: "futures", SortOrder: 20},
	{Symbol: "US.YMmain", Label: "道指 E-mini", Group: "futures", SortOrder: 30},
	{Symbol: "US.RTYmain", Label: "罗素 2000 E-mini", Group: "futures", SortOrder: 40},
	{Symbol: "US.CLmain", Label: "WTI 原油", Group: "futures", SortOrder: 50},
	{Symbol: "US.GCmain", Label: "COMEX 黄金", Group: "futures", SortOrder: 60},
	{Symbol: "US.SImain", Label: "COMEX 白银", Group: "futures", SortOrder: 70},
	{Symbol: "US.NGmain", Label: "天然气", Group: "futures", SortOrder: 80},
	{Symbol: "US.ZNmain", Label: "10 年期美债", Group: "futures", SortOrder: 90},
	{Symbol: "US.ZBmain", Label: "30 年期美债", Group: "futures", SortOrder: 100},
}

type USFuturesResponse struct {
	Source        string                `json:"source"`
	LastFetched   *time.Time            `json:"last_fetched_at,omitempty"`
	Futures       []MarketTrendSeries   `json:"futures"`
	AutomaticSync *FuturesAutomaticSync `json:"automatic_sync,omitempty"`
}
type FuturesAutomaticSync struct {
	Enabled    bool       `json:"enabled"`
	Running    bool       `json:"running"`
	NextRunAt  *time.Time `json:"next_run_at,omitempty"`
	LastStatus string     `json:"last_status"`
}
type USFuturesRefreshResult struct {
	SymbolsRequested int      `json:"symbols_requested"`
	SymbolsUpdated   int      `json:"symbols_updated"`
	BarsSaved        int      `json:"bars_saved"`
	Warnings         []string `json:"warnings"`
}

// Futures use their own module and exchange dates, while sharing the official
// Futu read-only authentication, request budget and audit transport.
type USFuturesService struct {
	db       *gorm.DB
	readJSON discovery.FutuReadJSON
	now      func() time.Time
	mu       sync.Mutex
}

func NewUSFuturesService(db *gorm.DB, futu *FutuAPIService) *USFuturesService {
	s := &USFuturesService{db: db, now: time.Now}
	if futu != nil {
		s.readJSON = futu.ReadJSON
	}
	return s
}
func (s *USFuturesService) List(ctx context.Context, historyDays int) (USFuturesResponse, error) {
	if s == nil || s.db == nil {
		return USFuturesResponse{}, errors.New("US futures service is not configured")
	}
	if historyDays < 20 || historyDays > 365 {
		historyDays = 120
	}
	var rows []model.MarketTrendDaily
	if err := s.db.WithContext(ctx).Where("group_name = ? AND source = ?", "futures", usFuturesSourceFutu).Order("sort_order ASC, trade_date ASC").Find(&rows).Error; err != nil {
		return USFuturesResponse{}, err
	}
	result := USFuturesResponse{Source: usFuturesSourceFutu, Futures: []MarketTrendSeries{}}
	if s.db.Migrator().HasTable(&model.TaskConfig{}) {
		var task model.TaskConfig
		if err := s.db.WithContext(ctx).Where("task_name = ?", "us_futures_sync").First(&task).Error; err == nil {
			result.AutomaticSync = &FuturesAutomaticSync{Enabled: task.Enabled, Running: task.Running, NextRunAt: task.NextRunAt, LastStatus: task.LastStatus}
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return result, err
		}
	}
	bySymbol := map[string][]model.MarketTrendDaily{}
	for _, row := range rows {
		bySymbol[row.Symbol] = append(bySymbol[row.Symbol], row)
		if result.LastFetched == nil || row.FetchedAt.After(*result.LastFetched) {
			v := row.FetchedAt
			result.LastFetched = &v
		}
	}
	for _, def := range usFuturesDefinitions {
		if values := bySymbol[def.Symbol]; len(values) > 0 {
			result.Futures = append(result.Futures, buildMarketTrendSeries(values, historyDays))
		}
	}
	return result, nil
}
func (s *USFuturesService) Refresh(ctx context.Context) (USFuturesRefreshResult, error) {
	if s == nil || s.db == nil || s.readJSON == nil {
		return USFuturesRefreshResult{}, errors.New("富途期货只读客户端未配置")
	}
	if !s.mu.TryLock() {
		return USFuturesRefreshResult{}, errors.New("期货同步正在进行，请稍后读取本地状态")
	}
	defer s.mu.Unlock()
	ctx, cancel := context.WithTimeout(discovery.WithFutuFutures(ctx), 5*time.Minute)
	defer cancel()
	now := s.now().UTC()
	result := USFuturesRefreshResult{SymbolsRequested: len(usFuturesDefinitions), Warnings: []string{}}
	for _, def := range usFuturesDefinitions {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		rows, err := s.fetchHistory(ctx, def, now.AddDate(0, 0, -400), now.AddDate(0, 0, -1), now)
		if err != nil {
			result.Warnings = append(result.Warnings, def.Label+"："+SanitizeSensitiveError(err.Error()))
			if errors.Is(err, ErrFutuRateLimited) || errors.Is(err, discovery.ErrAPIDisabled) || strings.Contains(err.Error(), "budget") || strings.Contains(err.Error(), "暂停") || strings.Contains(err.Error(), "授权") || strings.Contains(err.Error(), "paused") {
				result.Warnings = append(result.Warnings, "已停止本轮剩余期货请求；请检查富途授权、模块开关、暂停和请求预算。")
				if errors.Is(err, ErrFutuRateLimited) && result.SymbolsUpdated == 0 {
					return result, fmt.Errorf("%w: %s", ErrUSFuturesRateLimited, strings.Join(result.Warnings, "；"))
				}
				break
			}
			continue
		}
		if len(rows) == 0 {
			result.Warnings = append(result.Warnings, def.Label+"：富途暂无可用日线")
			continue
		}
		if err := s.store(ctx, rows); err != nil {
			return result, err
		}
		result.SymbolsUpdated++
		result.BarsSaved += len(rows)
	}
	if result.SymbolsUpdated == 0 && len(result.Warnings) > 0 {
		return result, errors.New("富途期货未返回可用日线：" + strings.Join(result.Warnings, "；"))
	}
	return result, nil
}
func (s *USFuturesService) fetchHistory(ctx context.Context, def marketTrendDefinition, start, end, fetchedAt time.Time) ([]model.MarketTrendDaily, error) {
	var reference struct {
		Data struct {
			List []struct {
				Code  string `json:"code"`
				Type  string `json:"stock_type"`
				Main  bool   `json:"future_main_contract"`
				Valid bool   `json:"future_valid"`
			} `json:"reference_list"`
		} `json:"data"`
	}
	if err := s.readJSON(ctx, http.MethodGet, "/api/v1.0/quote/"+def.Symbol+"/reference-future", nil, nil, &reference); err != nil {
		return nil, err
	}
	valid := false
	for _, item := range reference.Data.List {
		if item.Code == def.Symbol && item.Type == "FUTURE" && item.Main && item.Valid {
			valid = true
		}
	}
	if !valid {
		return nil, errors.New("富途未确认该代码为有效主连期货")
	}
	rows := []model.MarketTrendDaily{}
	seen := map[string]bool{}
	from, until := start, end
	q := url.Values{"start": {from.Format(time.DateOnly)}, "end": {until.Format(time.DateOnly)}, "ktype": {"2"}, "autype": {"0"}, "num": {"370"}, "extended_time": {"0"}}
	// Bounded pages, atomic per-contract persistence and cursor progress checks
	// prevent truncated responses from silently replacing a complete history.
	cursor := until.AddDate(0, 0, 1).UnixMilli()
	for page := 0; ; page++ {
		if page >= 10 {
			return nil, errors.New("富途期货分页超出限制")
		}
		var response struct {
			Data struct {
				Next      int64 `json:"next_time"`
				Precision int   `json:"volume_precision"`
				Bars      []struct {
					Date                   int   `json:"date"`
					Time                   int64 `json:"time_key"`
					Zone                   int   `json:"time_zone"`
					Open, High, Low, Close *float64
					Volume                 *int64
				} `json:"kline_list"`
			} `json:"data"`
		}
		if err := s.readJSON(ctx, http.MethodGet, "/api/v1.0/quote/"+def.Symbol+"/history-kline", q, nil, &response); err != nil {
			return nil, err
		}
		if response.Data.Precision < 0 || response.Data.Precision > 8 {
			return nil, errors.New("富途期货成交量精度无效")
		}
		divisor := int64(math.Pow10(response.Data.Precision))
		for _, bar := range response.Data.Bars {
			civil := strconv.Itoa(bar.Date)
			if bar.Date == 0 && bar.Time > 0 {
				civil = time.UnixMilli(bar.Time).In(time.FixedZone("provider", bar.Zone*60)).Format("20060102")
			}
			day, err := time.Parse("20060102", civil)
			if err != nil {
				return nil, errors.New("富途期货日线日期无效")
			}
			date := day.Format(time.DateOnly)
			if date < from.Format(time.DateOnly) || date > until.Format(time.DateOnly) {
				return nil, errors.New("富途期货日线超出请求窗口")
			}
			if bar.Open == nil || bar.High == nil || bar.Low == nil || bar.Close == nil || bar.Volume == nil {
				return nil, errors.New("富途期货日线缺少 OHLC 或成交量")
			}
			values := []float64{*bar.Open, *bar.High, *bar.Low, *bar.Close}
			for _, v := range values {
				if math.IsNaN(v) || math.IsInf(v, 0) {
					return nil, errors.New("富途期货价格无效")
				}
			}
			if *bar.High < *bar.Low || *bar.High < math.Max(*bar.Open, *bar.Close) || *bar.Low > math.Min(*bar.Open, *bar.Close) || *bar.Volume < 0 || *bar.Volume%divisor != 0 {
				return nil, errors.New("富途期货 OHLC 或成交量无效")
			}
			if seen[date] {
				continue
			}
			seen[date] = true
			rows = append(rows, model.MarketTrendDaily{Symbol: def.Symbol, Label: def.Label, Group: def.Group, SortOrder: def.SortOrder, TradeDate: date, Open: *bar.Open, High: *bar.High, Low: *bar.Low, Close: *bar.Close, Volume: *bar.Volume / divisor, Source: usFuturesSourceFutu, FetchedAt: fetchedAt})
		}
		if response.Data.Next <= 0 {
			break
		}
		if response.Data.Next >= cursor || response.Data.Next < from.UnixMilli() {
			return nil, errors.New("富途期货分页游标未向前推进")
		}
		cursor = response.Data.Next
		q.Set("end", strconv.FormatInt(cursor, 10))
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].TradeDate < rows[j].TradeDate })
	return rows, nil
}
func (s *USFuturesService) store(ctx context.Context, rows []model.MarketTrendDaily) error {
	return s.db.WithContext(ctx).Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "symbol"}, {Name: "trade_date"}}, DoUpdates: clause.AssignmentColumns([]string{"label", "group_name", "sort_order", "open", "high", "low", "close", "volume", "source", "fetched_at", "updated_at"})}).CreateInBatches(&rows, 100).Error
}
