package discovery

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

type FutuReadJSON func(context.Context, string, string, url.Values, []byte, any) error

var futuUSSymbolPattern = regexp.MustCompile(`^[A-Z][A-Z0-9.-]{0,19}$`)

type FutuPriceProvider struct {
	ReadJSON FutuReadJSON
	Calendar MarketCalendar
}

func (p *FutuPriceProvider) ProviderName() string { return "futu" }
func (p *FutuPriceProvider) Load(ctx context.Context, listings []Listing) ([]PriceRecord, ProviderResult, error) {
	day, err := LatestCompletedTradingDate(ctx, p.Calendar, time.Now())
	if err != nil {
		return nil, ProviderResult{}, err
	}
	return p.LoadForDate(ctx, listings, day.Format(time.DateOnly))
}
func (p *FutuPriceProvider) LoadForDate(ctx context.Context, listings []Listing, date string) ([]PriceRecord, ProviderResult, error) {
	day, err := parseNYCivilDate(date)
	if err != nil {
		return nil, ProviderResult{}, err
	}
	rows, err := p.loadWindow(ctx, listings, day, day)
	result := ProviderResult{Provider: "futu", EffectiveDate: day, Expected: len(listings), Records: len(rows), Status: "success", Timely: true, SourceVersion: "futu:daily:unadjusted:" + date}
	result.SHA256 = hashLongbridgePriceRecords(rows)
	result.SourceVersion += ":" + result.SHA256
	if len(listings) > 0 {
		result.CoveragePct = float64(len(rows)) / float64(len(listings)) * 100
	}
	if err != nil {
		result.Status = "failed"
	}
	return rows, result, err
}
func (p *FutuPriceProvider) LoadHistory(ctx context.Context, listings []Listing, date string, lookback int) ([]PriceRecord, error) {
	start, end, err := normalizeHistoryWindow(date, lookback)
	if err != nil {
		return nil, err
	}
	if end.Sub(start) > 5*366*24*time.Hour {
		return nil, errors.New("富途单次历史补齐最多五年")
	}
	return p.loadWindow(ctx, listings, start, end)
}
func (p *FutuPriceProvider) loadWindow(ctx context.Context, listings []Listing, start, end time.Time) ([]PriceRecord, error) {
	if p.ReadJSON == nil {
		return nil, errors.New("Futu 只读客户端未配置")
	}
	rows := []PriceRecord{}
	seen := map[string]bool{}
	for _, listing := range listings {
		ticker := strings.ToUpper(strings.TrimSpace(listing.Ticker))
		if !futuUSSymbolPattern.MatchString(ticker) {
			continue
		}
		if seen[ticker] {
			continue
		}
		seen[ticker] = true
		for from := start; !from.After(end); {
			until := from.AddDate(0, 0, 179)
			if until.After(end) {
				until = end
			}
			q := url.Values{"start": {from.Format(time.DateOnly)}, "end": {until.Format(time.DateOnly)}, "ktype": {"2"}, "autype": {"0"}, "num": {"370"}, "extended_time": {"0"}}
			var data struct {
				Data struct {
					VolumePrecision int `json:"volume_precision"`
					KlineList       []struct {
						Date    int     `json:"date"`
						TimeKey int64   `json:"time_key"`
						Open    float64 `json:"open"`
						High    float64 `json:"high"`
						Low     float64 `json:"low"`
						Close   float64 `json:"close"`
						Volume  int64   `json:"volume"`
					} `json:"kline_list"`
				} `json:"data"`
			}
			err := p.ReadJSON(ctx, http.MethodGet, "/api/v1.0/quote/US."+ticker+"/history-kline", q, nil, &data)
			if err != nil {
				return nil, err
			}
			if data.Data.VolumePrecision < 0 || data.Data.VolumePrecision > 8 {
				return nil, errors.New("Futu 成交量精度无法识别")
			}
			divisor := int64(math.Pow10(data.Data.VolumePrecision))
			dates := map[string]bool{}
			for _, bar := range data.Data.KlineList {
				civil := ""
				if bar.Date > 0 {
					raw := strconv.Itoa(bar.Date)
					if len(raw) != 8 {
						return nil, errors.New("Futu 日线日期无效")
					}
					civil = raw[:4] + "-" + raw[4:6] + "-" + raw[6:]
				} else if bar.TimeKey > 0 {
					civil = time.UnixMilli(bar.TimeKey).In(start.Location()).Format(time.DateOnly)
				}
				day, err := parseNYCivilDate(civil)
				if err != nil {
					return nil, errors.New("Futu 日线日期无效")
				}
				if day.Before(from) || day.After(until) {
					return nil, errors.New("Futu 日线超出请求窗口")
				}
				if p.Calendar != nil {
					isTrading, err := p.Calendar.IsTradingDay(ctx, day)
					if err != nil {
						return nil, err
					}
					if !isTrading {
						return nil, errors.New("Futu 返回非交易日日线")
					}
				}
				if dates[civil] {
					return nil, errors.New("Futu 日线包含重复交易日")
				}
				dates[civil] = true
				if bar.Volume < 0 || bar.Volume%divisor != 0 {
					return nil, errors.New("Futu 成交量无法转换为完整股数")
				}
				values := []float64{bar.Open, bar.High, bar.Low, bar.Close}
				micros := make([]int64, 4)
				for i, v := range values {
					if v <= 0 || math.IsInf(v, 0) || math.IsNaN(v) || v > float64(math.MaxInt64)/1e6 {
						return nil, errors.New("Futu 日线价格无效")
					}
					micros[i] = int64(math.Round(v * 1e6))
				}
				if bar.High < math.Max(bar.Open, bar.Close) || bar.Low > math.Min(bar.Open, bar.Close) || bar.High < bar.Low {
					return nil, errors.New("Futu 日线 OHLC 范围无效")
				}
				rows = append(rows, PriceRecord{Symbol: ticker, TradeDate: day, OpenMicros: micros[0], HighMicros: micros[1], LowMicros: micros[2], CloseMicros: micros[3], Volume: bar.Volume / divisor, Currency: "USD", Source: "futu"})
			}
			from = until.AddDate(0, 0, 1)
		}
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("Futu 在请求交易日内无可用日线")
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Symbol != rows[j].Symbol {
			return rows[i].Symbol < rows[j].Symbol
		}
		return rows[i].TradeDate.Before(rows[j].TradeDate)
	})
	return rows, nil
}
