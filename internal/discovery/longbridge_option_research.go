package discovery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"sec_monitor/internal/config"

	lbconfig "github.com/longbridge/openapi-go/config"
	lbquote "github.com/longbridge/openapi-go/quote"
	"gorm.io/gorm"
)

const longbridgeOptionResearchProvider = "longbridge"

type OptionResearchView struct {
	Latest  *OptionResearchSnapshot  `json:"latest,omitempty"`
	History []OptionResearchSnapshot `json:"history"`
	Message string                   `json:"message"`
}

type OptionResearchRefreshResult struct {
	Ticker   string                 `json:"ticker"`
	Fetched  bool                   `json:"fetched"`
	Snapshot OptionResearchSnapshot `json:"snapshot"`
	Warnings []string               `json:"warnings"`
	Message  string                 `json:"message"`
}

type OptionResearchSyncResult struct {
	CandidateCount int      `json:"candidate_count"`
	Attempted      int      `json:"attempted"`
	Fetched        int      `json:"fetched"`
	Failed         int      `json:"failed"`
	Skipped        bool     `json:"skipped"`
	Message        string   `json:"message"`
	Warnings       []string `json:"warnings"`
}

// OptionResearchTarget is a compact batch input. A single Longbridge quote
// connection is shared by the batch to avoid exhausting account connection
// limits while refreshing several candidates back-to-back.
type OptionResearchTarget struct {
	Ticker string
	CIK    string
}

type optionResearchBatchItem struct {
	Result OptionResearchRefreshResult
	Err    error
}

type longbridgeOptionResearchClient interface {
	OptionVolume(context.Context, string) (*lbquote.OptionVolumeStats, error)
	OptionVolumeDaily(context.Context, string, time.Time, time.Time) ([]*lbquote.DailyOptionVolume, error)
	ShortPositions(context.Context, string, uint32) (*lbquote.ShortPositionsResponse, error)
}

// longbridgeOptionResearchClientCloser is deliberately optional so tests and
// non-SDK clients remain lightweight. The official QuoteContext owns a live
// socket, however, and must be released after every refresh. Without this the
// sequential candidate budget can accumulate authenticated connections and
// eventually receive Longbridge's "connections limitation is hit" response.
type longbridgeOptionResearchClientCloser interface {
	Close() error
}

type LongbridgeOptionResearchOptions struct {
	AppKey      string
	AppSecret   string
	AccessToken string
	Now         func() time.Time
	NewClient   func(string, string, string) (longbridgeOptionResearchClient, error)
}

func NewLongbridgeOptionResearchOptions(cfg config.DiscoveryConfig) LongbridgeOptionResearchOptions {
	return LongbridgeOptionResearchOptions{AppKey: cfg.LongbridgeAppKey, AppSecret: cfg.LongbridgeAppSecret, AccessToken: cfg.LongbridgeAccessToken}
}

// GetOptionResearch is deliberately local/read-only. The page may be opened
// frequently without spending market-data quota or mutating observations.
func GetOptionResearch(ctx context.Context, db *gorm.DB, ticker string) (OptionResearchView, error) {
	result := OptionResearchView{History: []OptionResearchSnapshot{}}
	if db == nil {
		return result, errors.New("database is required")
	}
	symbol := normalizeAnalystRatingTicker(ticker)
	if symbol == "" {
		return result, errors.New("ticker is required")
	}
	if err := db.WithContext(ctx).Where("provider = ? AND ticker = ?", longbridgeOptionResearchProvider, symbol).Order("observed_date DESC, id DESC").Limit(30).Find(&result.History).Error; err != nil {
		return result, err
	}
	for index := range result.History {
		decodeOptionResearchAnomalies(&result.History[index])
	}
	if len(result.History) == 0 {
		result.Message = "尚未同步期权与空头研究快照；可手动刷新，或等待候选/监控标的定时任务。"
		return result, nil
	}
	result.Latest = &result.History[0]
	if result.Latest.Status == "unavailable" {
		result.Message = "Longbridge 已响应，但该标的当前暂无期权成交量或空头持仓覆盖；这不是同步失败。"
	} else if result.Latest.Status == "partial" {
		result.Message = "已保存部分期权与空头数据；某些美股、ETF 或账户权限可能没有期权或空头覆盖。"
	} else {
		result.Message = "Longbridge Call/Put 汇总成交量与空头持仓快照；仅作为多空研究指标，不构成真实全市场净仓位。"
	}
	return result, nil
}

func RefreshLongbridgeOptionResearch(ctx context.Context, db *gorm.DB, cfg config.DiscoveryConfig, ticker, cik string) (OptionResearchRefreshResult, error) {
	return refreshLongbridgeOptionResearch(ctx, db, ticker, cik, NewLongbridgeOptionResearchOptions(cfg))
}

// RefreshLongbridgeOptionResearchBatch shares exactly one authenticated quote
// context across the supplied tickers. It returns per-ticker outcomes so one
// unavailable security does not prevent the remaining candidates from being
// updated.
func RefreshLongbridgeOptionResearchBatch(ctx context.Context, db *gorm.DB, cfg config.DiscoveryConfig, targets []OptionResearchTarget) ([]optionResearchBatchItem, error) {
	return refreshLongbridgeOptionResearchBatch(ctx, db, targets, NewLongbridgeOptionResearchOptions(cfg))
}

func refreshLongbridgeOptionResearch(ctx context.Context, db *gorm.DB, ticker, cik string, options LongbridgeOptionResearchOptions) (OptionResearchRefreshResult, error) {
	result := OptionResearchRefreshResult{Ticker: normalizeAnalystRatingTicker(ticker), Warnings: []string{}}
	if db == nil {
		return result, errors.New("database is required")
	}
	if result.Ticker == "" {
		return result, errors.New("ticker is required")
	}
	if strings.TrimSpace(options.AppKey) == "" || strings.TrimSpace(options.AppSecret) == "" || strings.TrimSpace(options.AccessToken) == "" {
		return result, errors.New("Longbridge app key, app secret, and access token are required")
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.NewClient == nil {
		options.NewClient = newLongbridgeOptionResearchSDKClient
	}
	client, err := options.NewClient(options.AppKey, options.AppSecret, options.AccessToken)
	if err != nil {
		return result, fmt.Errorf("create Longbridge option research client: %w", err)
	}
	if closer, ok := client.(longbridgeOptionResearchClientCloser); ok {
		defer func() { _ = closer.Close() }()
	}
	return refreshLongbridgeOptionResearchWithClient(ctx, db, ticker, cik, options, client)
}

func refreshLongbridgeOptionResearchBatch(ctx context.Context, db *gorm.DB, targets []OptionResearchTarget, options LongbridgeOptionResearchOptions) ([]optionResearchBatchItem, error) {
	items := make([]optionResearchBatchItem, 0, len(targets))
	if len(targets) == 0 {
		return items, nil
	}
	if strings.TrimSpace(options.AppKey) == "" || strings.TrimSpace(options.AppSecret) == "" || strings.TrimSpace(options.AccessToken) == "" {
		return items, errors.New("Longbridge app key, app secret, and access token are required")
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.NewClient == nil {
		options.NewClient = newLongbridgeOptionResearchSDKClient
	}
	client, err := options.NewClient(options.AppKey, options.AppSecret, options.AccessToken)
	if err != nil {
		return items, fmt.Errorf("create Longbridge option research client: %w", err)
	}
	if closer, ok := client.(longbridgeOptionResearchClientCloser); ok {
		defer func() { _ = closer.Close() }()
	}
	for _, target := range targets {
		refreshed, refreshErr := refreshLongbridgeOptionResearchWithClient(ctx, db, target.Ticker, target.CIK, options, client)
		items = append(items, optionResearchBatchItem{Result: refreshed, Err: refreshErr})
	}
	return items, nil
}

func refreshLongbridgeOptionResearchWithClient(ctx context.Context, db *gorm.DB, ticker, cik string, options LongbridgeOptionResearchOptions, client longbridgeOptionResearchClient) (OptionResearchRefreshResult, error) {
	result := OptionResearchRefreshResult{Ticker: normalizeAnalystRatingTicker(ticker), Warnings: []string{}}
	if db == nil {
		return result, errors.New("database is required")
	}
	if result.Ticker == "" {
		return result, errors.New("ticker is required")
	}
	if client == nil {
		return result, errors.New("Longbridge option research client is required")
	}
	now := options.Now().UTC()
	requestCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	symbol := result.Ticker + ".US"
	snapshot := OptionResearchSnapshot{SecurityID: analystRatingSecurityID(ctx, db, result.Ticker, cik), Provider: longbridgeOptionResearchProvider, Ticker: result.Ticker, ObservedDate: now.Format(time.DateOnly), Status: "unavailable", FetchedAt: now}
	hadRequestError := false
	shortRequestSucceeded := false

	if volume, fetchErr := client.OptionVolume(requestCtx, symbol); fetchErr != nil {
		hadRequestError = true
		if daily, dailyErr := client.OptionVolumeDaily(requestCtx, symbol, now.AddDate(0, 0, -45), now); dailyErr != nil {
			result.Warnings = append(result.Warnings, "期权成交量："+SanitizeLongbridgeCandidateResearchError(fetchErr)+"；日序列回退失败："+SanitizeLongbridgeCandidateResearchError(dailyErr))
		} else if latest := latestDailyOptionVolume(daily); latest != nil {
			if call, ok := parseInt64Ptr(latest.TotalCallVolume); ok {
				snapshot.CallVolume = call
			}
			if put, ok := parseInt64Ptr(latest.TotalPutVolume); ok {
				snapshot.PutVolume = put
			}
			if ratio, ok := parseFloat64Ptr(latest.PutCallVolumeRatio); ok {
				snapshot.PutCallVolumeRatio = ratio
			}
			snapshot.OptionVolumeAsOf = normalizeOptionResearchTimestamp(latest.Timestamp)
			result.Warnings = append(result.Warnings, "实时期权成交量暂不可用，已使用最近交易日的期权日序列快照。")
		} else {
			result.Warnings = append(result.Warnings, "期权成交量："+SanitizeLongbridgeCandidateResearchError(fetchErr)+"；日序列暂无覆盖")
		}
	} else if volume != nil {
		if call, ok := parseInt64Ptr(volume.CallVolume); ok {
			snapshot.CallVolume = call
		}
		if put, ok := parseInt64Ptr(volume.PutVolume); ok {
			snapshot.PutVolume = put
		}
		if snapshot.CallVolume != nil && snapshot.PutVolume != nil && *snapshot.CallVolume > 0 {
			ratio := float64(*snapshot.PutVolume) / float64(*snapshot.CallVolume)
			snapshot.PutCallVolumeRatio = &ratio
		}
		snapshot.OptionVolumeAsOf = now.Format(time.RFC3339)
	}

	if positions, fetchErr := client.ShortPositions(requestCtx, symbol, 1); fetchErr != nil {
		hadRequestError = true
		result.Warnings = append(result.Warnings, "空头持仓："+SanitizeLongbridgeCandidateResearchError(fetchErr))
	} else {
		shortRequestSucceeded = true
		if positions != nil && len(positions.Data) > 0 && positions.Data[0] != nil {
			latest := positions.Data[0]
			if value, ok := parseFloat64Ptr(latest.Rate); ok {
				// Longbridge returns the US short ratio as a decimal (0.0120 for
				// 1.20%). Store a display-ready percentage consistently with the
				// rest of this project.
				percent := *value * 100
				snapshot.ShortRatioPct = &percent
			}
			if value, ok := parseInt64Ptr(latest.CurrentSharesShort); ok {
				snapshot.CurrentSharesShort = value
			}
			if value, ok := parseInt64Ptr(latest.AvgDailyShareVolume); ok {
				snapshot.AvgDailyShareVolume = value
			}
			if value, ok := parseFloat64Ptr(latest.DaysToCover); ok {
				snapshot.DaysToCover = value
			}
			snapshot.ShortReportedAt = normalizeOptionResearchTimestamp(latest.Timestamp)
		}
	}
	if snapshot.CallVolume != nil || snapshot.PutVolume != nil {
		snapshot.Status = "available"
	}
	if snapshot.ShortRatioPct != nil || snapshot.DaysToCover != nil {
		if snapshot.Status == "available" {
			snapshot.Status = "available"
		} else {
			snapshot.Status = "partial"
		}
	}
	if snapshot.Status == "unavailable" && hadRequestError && !shortRequestSucceeded {
		return result, errors.New("Longbridge did not return option-volume or short-position coverage")
	}
	snapshot.Anomalies = optionResearchAnomalies(ctx, db, snapshot)
	encoded, err := json.Marshal(snapshot.Anomalies)
	if err != nil {
		return result, err
	}
	snapshot.AnomaliesJSON = string(encoded)
	if err := saveOptionResearchSnapshot(ctx, db, &snapshot); err != nil {
		return result, err
	}
	result.Fetched, result.Snapshot = true, snapshot
	if snapshot.Status == "unavailable" {
		result.Message = "Longbridge 已响应，但该标的暂无期权成交量或空头持仓覆盖。"
	} else {
		result.Message = "已保存期权成交量与空头持仓快照。"
	}
	if len(result.Warnings) > 0 {
		result.Message = "已保存部分期权与空头研究快照。"
	}
	return result, nil
}

func SyncCurrentCandidateOptionResearch(ctx context.Context, db *gorm.DB, cfg config.DiscoveryConfig) (OptionResearchSyncResult, error) {
	result := OptionResearchSyncResult{Warnings: []string{}}
	if db == nil {
		return result, errors.New("database is required")
	}
	if !cfg.LongbridgeOptionResearchEnabled || cfg.LongbridgeCandidateOptionResearchBudget <= 0 {
		result.Skipped, result.Message = true, "Longbridge 期权与空头研究同步已关闭或预算为 0"
		return result, nil
	}
	batch, ok, err := currentPublishedPrescreenBatch(ctx, db)
	if err != nil || !ok {
		if !ok {
			result.Skipped, result.Message = true, "暂无已发布的小盘候选批次"
		}
		return result, err
	}
	var scores []CandidateScoreSnapshot
	if err := db.WithContext(ctx).Where("batch_id = ?", batch.BatchID).Order("total_score DESC, ticker ASC").Limit(cfg.LongbridgeCandidateOptionResearchBudget).Find(&scores).Error; err != nil {
		return result, err
	}
	result.CandidateCount = len(scores)
	targets := make([]OptionResearchTarget, 0, len(scores))
	for _, score := range scores {
		targets = append(targets, OptionResearchTarget{Ticker: score.Ticker})
	}
	items, err := RefreshLongbridgeOptionResearchBatch(ctx, db, cfg, targets)
	if err != nil {
		return result, err
	}
	for index, item := range items {
		result.Attempted++
		if item.Err != nil {
			result.Failed++
			result.Warnings = append(result.Warnings, targets[index].Ticker+"："+SanitizeLongbridgeCandidateResearchError(item.Err))
			continue
		}
		if item.Result.Fetched {
			result.Fetched++
		}
		result.Warnings = append(result.Warnings, item.Result.Warnings...)
	}
	result.Message = fmt.Sprintf("已更新 %d/%d 个候选的期权与空头研究快照", result.Fetched, result.Attempted)
	return result, nil
}

func optionResearchAnomalies(ctx context.Context, db *gorm.DB, snapshot OptionResearchSnapshot) []OptionResearchAnomaly {
	items := []OptionResearchAnomaly{}
	if snapshot.PutCallVolumeRatio != nil {
		switch {
		case *snapshot.PutCallVolumeRatio >= 1.5:
			items = append(items, OptionResearchAnomaly{Kind: "put_call_skew", Severity: "warning", Label: "Put 成交显著偏多", Detail: fmt.Sprintf("Put/Call 成交量比 %.2f", *snapshot.PutCallVolumeRatio)})
		case *snapshot.PutCallVolumeRatio <= 0.67:
			items = append(items, OptionResearchAnomaly{Kind: "put_call_skew", Severity: "info", Label: "Call 成交显著偏多", Detail: fmt.Sprintf("Put/Call 成交量比 %.2f", *snapshot.PutCallVolumeRatio)})
		}
	}
	if snapshot.DaysToCover != nil && *snapshot.DaysToCover >= 5 {
		items = append(items, OptionResearchAnomaly{Kind: "days_to_cover", Severity: "warning", Label: "days to cover 偏高", Detail: fmt.Sprintf("%.2f 天", *snapshot.DaysToCover)})
	}
	if snapshot.CallVolume == nil && snapshot.PutVolume == nil {
		return items
	}
	current := int64(0)
	if snapshot.CallVolume != nil {
		current += *snapshot.CallVolume
	}
	if snapshot.PutVolume != nil {
		current += *snapshot.PutVolume
	}
	var rows []OptionResearchSnapshot
	if err := db.WithContext(ctx).Where("provider = ? AND ticker = ? AND observed_date < ?", snapshot.Provider, snapshot.Ticker, snapshot.ObservedDate).Order("observed_date DESC").Limit(20).Find(&rows).Error; err != nil {
		return items
	}
	values := make([]int64, 0, len(rows))
	for _, row := range rows {
		total := int64(0)
		if row.CallVolume != nil {
			total += *row.CallVolume
		}
		if row.PutVolume != nil {
			total += *row.PutVolume
		}
		if total > 0 {
			values = append(values, total)
		}
	}
	if len(values) >= 5 {
		var total int64
		for _, value := range values {
			total += value
		}
		average := float64(total) / float64(len(values))
		if average > 0 && float64(current) >= average*2 {
			items = append(items, OptionResearchAnomaly{Kind: "volume_spike", Severity: "warning", Label: "期权成交量放大", Detail: fmt.Sprintf("%d，约为近 %d 次均值的 %.1f 倍", current, len(values), float64(current)/average)})
		}
	}
	return items
}

func saveOptionResearchSnapshot(ctx context.Context, db *gorm.DB, snapshot *OptionResearchSnapshot) error {
	var existing OptionResearchSnapshot
	err := db.WithContext(ctx).Where("provider = ? AND ticker = ? AND observed_date = ?", snapshot.Provider, snapshot.Ticker, snapshot.ObservedDate).First(&existing).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return db.WithContext(ctx).Create(snapshot).Error
	}
	if err != nil {
		return err
	}
	snapshot.ID = existing.ID
	return db.WithContext(ctx).Model(&existing).Select("security_id", "status", "call_volume", "put_volume", "put_call_volume_ratio", "option_volume_as_of", "short_ratio_pct", "current_shares_short", "avg_daily_share_volume", "days_to_cover", "short_reported_at", "anomalies_json", "fetched_at").Updates(snapshot).Error
}

func decodeOptionResearchAnomalies(snapshot *OptionResearchSnapshot) {
	if snapshot == nil {
		return
	}
	snapshot.Anomalies = []OptionResearchAnomaly{}
	_ = json.Unmarshal([]byte(snapshot.AnomaliesJSON), &snapshot.Anomalies)
}

func parseFloat64Ptr(value string) (*float64, bool) {
	parsed, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
	if err != nil {
		return nil, false
	}
	return &parsed, true
}
func parseInt64Ptr(value string) (*int64, bool) {
	parsed, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil {
		return nil, false
	}
	return &parsed, true
}

func latestDailyOptionVolume(items []*lbquote.DailyOptionVolume) *lbquote.DailyOptionVolume {
	var latest *lbquote.DailyOptionVolume
	for _, item := range items {
		if item != nil && (latest == nil || strings.TrimSpace(item.Timestamp) > strings.TrimSpace(latest.Timestamp)) {
			latest = item
		}
	}
	return latest
}

func normalizeOptionResearchTimestamp(value string) string {
	value = strings.TrimSpace(value)
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil && seconds > 946684800 {
		return time.Unix(seconds, 0).UTC().Format(time.RFC3339)
	}
	return value
}

type longbridgeOptionResearchSDKClient struct{ quote *lbquote.QuoteContext }

func (c *longbridgeOptionResearchSDKClient) Close() error {
	if c == nil || c.quote == nil {
		return nil
	}
	return c.quote.Close()
}

func newLongbridgeOptionResearchSDKClient(appKey, appSecret, accessToken string) (longbridgeOptionResearchClient, error) {
	cfg, err := lbconfig.New(lbconfig.WithConfigKey(appKey, appSecret, accessToken))
	if err != nil {
		return nil, err
	}
	quote, err := lbquote.NewFromCfg(cfg)
	if err != nil {
		return nil, err
	}
	return &longbridgeOptionResearchSDKClient{quote: quote}, nil
}
func (c *longbridgeOptionResearchSDKClient) OptionVolume(ctx context.Context, symbol string) (*lbquote.OptionVolumeStats, error) {
	return c.quote.OptionVolume(ctx, symbol)
}
func (c *longbridgeOptionResearchSDKClient) OptionVolumeDaily(ctx context.Context, symbol string, start, end time.Time) ([]*lbquote.DailyOptionVolume, error) {
	return c.quote.OptionVolumeDaily(ctx, symbol, start, end)
}
func (c *longbridgeOptionResearchSDKClient) ShortPositions(ctx context.Context, symbol string, count uint32) (*lbquote.ShortPositionsResponse, error) {
	return c.quote.ShortPositions(ctx, symbol, count)
}

func SortOptionResearchByTicker(items []OptionResearchSnapshot) {
	sort.Slice(items, func(left, right int) bool { return items[left].Ticker < items[right].Ticker })
}
