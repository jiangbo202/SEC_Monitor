package discovery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"sec_monitor/internal/config"

	lbconfig "github.com/longbridge/openapi-go/config"
	lbfundamental "github.com/longbridge/openapi-go/fundamental"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type CandidateValuationResearch struct {
	Latest  *ValuationResearchSnapshot  `json:"latest,omitempty"`
	History []ValuationResearchSnapshot `json:"history"`
	Message string                      `json:"message"`
}

type ValuationResearchSnapshot struct {
	ID            uint                     `json:"id"`
	Ticker        string                   `json:"ticker"`
	Metrics       ValuationResearchMetrics `json:"metrics"`
	Percentiles   ValuationPercentiles     `json:"percentiles"`
	Peers         []ValuationPeer          `json:"peers"`
	ChangeSummary string                   `json:"change_summary,omitempty"`
	FetchedAt     time.Time                `json:"fetched_at"`
}

type ValuationResearchMetrics struct {
	PE ValuationMetric `json:"pe"`
	PB ValuationMetric `json:"pb"`
	PS ValuationMetric `json:"ps"`
}
type ValuationMetric struct {
	Current *float64                `json:"current,omitempty"`
	Low     *float64                `json:"low,omitempty"`
	High    *float64                `json:"high,omitempty"`
	Median  *float64                `json:"median,omitempty"`
	History []ValuationHistoryPoint `json:"history"`
}
type ValuationHistoryPoint struct {
	Date  string   `json:"date"`
	Value *float64 `json:"value,omitempty"`
}
type ValuationPercentiles struct {
	PE ValuationPercentile `json:"pe"`
	PB ValuationPercentile `json:"pb"`
	PS ValuationPercentile `json:"ps"`
}
type ValuationPercentile struct {
	Value     *float64 `json:"value,omitempty"`
	Low       *float64 `json:"low,omitempty"`
	High      *float64 `json:"high,omitempty"`
	Median    *float64 `json:"median,omitempty"`
	Ranking   *float64 `json:"ranking,omitempty"`
	RankIndex string   `json:"rank_index"`
	RankTotal string   `json:"rank_total"`
}
type ValuationPeer struct {
	Symbol   string   `json:"symbol"`
	Name     string   `json:"name"`
	Currency string   `json:"currency"`
	PE       *float64 `json:"pe,omitempty"`
	PB       *float64 `json:"pb,omitempty"`
	PS       *float64 `json:"ps,omitempty"`
}

type ValuationResearchRefreshResult struct {
	Ticker        string   `json:"ticker"`
	Fetched       bool     `json:"fetched"`
	Cached        bool     `json:"cached"`
	ChangeSummary string   `json:"change_summary,omitempty"`
	Warnings      []string `json:"warnings"`
	Message       string   `json:"message"`
}

type CandidateValuationResearchSyncResult struct {
	CandidateCount int      `json:"candidate_count"`
	Attempted      int      `json:"attempted"`
	Fetched        int      `json:"fetched"`
	Failed         int      `json:"failed"`
	Skipped        bool     `json:"skipped"`
	Message        string   `json:"message"`
	Warnings       []string `json:"warnings"`
}

type longbridgeValuationResearchClient interface {
	Valuation(context.Context, string) (*lbfundamental.ValuationData, error)
	IndustryValuation(context.Context, string) (*lbfundamental.IndustryValuationList, error)
	IndustryValuationDist(context.Context, string) (*lbfundamental.IndustryValuationDist, error)
}
type LongbridgeValuationResearchOptions struct {
	AppKey      string
	AppSecret   string
	AccessToken string
	Now         func() time.Time
	NewClient   func(string, string, string) (longbridgeValuationResearchClient, error)
}

func NewLongbridgeValuationResearchOptions(cfg config.DiscoveryConfig) LongbridgeValuationResearchOptions {
	return LongbridgeValuationResearchOptions{AppKey: cfg.LongbridgeAppKey, AppSecret: cfg.LongbridgeAppSecret, AccessToken: cfg.LongbridgeAccessToken}
}

func GetCandidateValuationResearch(ctx context.Context, db *gorm.DB, ticker string) (CandidateValuationResearch, error) {
	result := CandidateValuationResearch{History: []ValuationResearchSnapshot{}}
	if db == nil {
		return result, errors.New("database is required")
	}
	symbol := normalizeAnalystRatingTicker(ticker)
	if symbol == "" {
		return result, errors.New("ticker is required")
	}
	var rows []LongbridgeValuationSnapshot
	if err := db.WithContext(ctx).Where("provider = ? AND ticker = ?", longbridgeCandidateResearchProvider, symbol).Order("fetched_at DESC, id DESC").Limit(12).Find(&rows).Error; err != nil {
		return result, err
	}
	for _, row := range rows {
		var decoded ValuationResearchSnapshot
		if err := json.Unmarshal([]byte(row.PayloadJSON), &decoded); err != nil {
			continue
		}
		decoded.ID, decoded.Ticker, decoded.ChangeSummary, decoded.FetchedAt = row.ID, row.Ticker, row.ChangeSummary, row.FetchedAt
		result.History = append(result.History, decoded)
	}
	if len(result.History) == 0 {
		result.Message = "尚未同步 Longbridge 估值历史与同业比较；小盘股覆盖可能不完整。"
		return result, nil
	}
	result.Latest = &result.History[0]
	result.Message = "Longbridge 估值历史、行业分位与同业比较；仅用于研究展示，不能作为硬筛选。"
	return result, nil
}

func RefreshLongbridgeCandidateValuationResearch(ctx context.Context, db *gorm.DB, cfg config.DiscoveryConfig, ticker, cik string) (ValuationResearchRefreshResult, error) {
	return refreshLongbridgeCandidateValuationResearch(ctx, db, ticker, cik, NewLongbridgeValuationResearchOptions(cfg))
}

// SyncCurrentCandidateLongbridgeValuationResearch is deliberately separate
// from P1 market research: valuation history uses a distinct endpoint family
// and receives an independent request budget and retry cycle.
func SyncCurrentCandidateLongbridgeValuationResearch(ctx context.Context, db *gorm.DB, cfg config.DiscoveryConfig) (CandidateValuationResearchSyncResult, error) {
	result := CandidateValuationResearchSyncResult{Warnings: []string{}}
	if db == nil {
		return result, errors.New("database is required")
	}
	if !cfg.LongbridgeCandidateValuationEnabled || cfg.LongbridgeCandidateValuationRequestBudget <= 0 {
		result.Skipped, result.Message = true, "Longbridge P2 估值研究同步已关闭或预算为 0"
		return result, nil
	}
	lastFetched := map[string]time.Time{}
	var prior []LongbridgeValuationSnapshot
	if err := db.WithContext(ctx).Where("provider = ?", longbridgeCandidateResearchProvider).Order("fetched_at DESC, id DESC").Find(&prior).Error; err != nil {
		return result, err
	}
	for _, row := range prior {
		if _, ok := lastFetched[row.Ticker]; !ok {
			lastFetched[row.Ticker] = row.FetchedAt
		}
	}
	now := time.Now().UTC()
	fresh, freshErr := FreshLongbridgeResearchTickers(ctx, db, LongbridgeRefreshFamilyValuation, now)
	if freshErr != nil {
		return result, freshErr
	}
	tickers, candidateCount, err := leastRecentlyRefreshedCandidateTickers(ctx, db, cfg.LongbridgeCandidateValuationRequestBudget, lastFetched, fresh)
	if err != nil {
		return result, err
	}
	result.CandidateCount = candidateCount
	if candidateCount == 0 {
		result.Skipped, result.Message = true, "暂无已发布的小盘候选批次"
		return result, nil
	}
	for _, ticker := range tickers {
		result.Attempted++
		refreshed, refreshErr := RefreshLongbridgeCandidateValuationResearch(ctx, db, cfg, ticker, "")
		if refreshErr != nil {
			result.Failed++
			result.Warnings = append(result.Warnings, ticker+"："+SanitizeLongbridgeCandidateResearchError(refreshErr))
			continue
		}
		if markErr := MarkLongbridgeResearchSuccess(ctx, db, LongbridgeRefreshFamilyValuation, ticker, now); markErr != nil {
			return result, markErr
		}
		if refreshed.Fetched || refreshed.Cached {
			result.Fetched++
		}
		result.Warnings = append(result.Warnings, refreshed.Warnings...)
	}
	result.Message = fmt.Sprintf("已同步 %d 个候选的 Longbridge 估值研究，失败 %d 个", result.Fetched, result.Failed)
	return result, nil
}
func refreshLongbridgeCandidateValuationResearch(ctx context.Context, db *gorm.DB, ticker, cik string, options LongbridgeValuationResearchOptions) (ValuationResearchRefreshResult, error) {
	result := ValuationResearchRefreshResult{Ticker: normalizeAnalystRatingTicker(ticker), Warnings: []string{}}
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
		options.NewClient = newLongbridgeValuationResearchSDKClient
	}
	client, err := options.NewClient(options.AppKey, options.AppSecret, options.AccessToken)
	if err != nil {
		return result, err
	}
	requestCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	symbol := result.Ticker + ".US"
	valuation, valuationErr := client.Valuation(requestCtx, symbol)
	if valuationErr != nil {
		return result, fmt.Errorf("load Longbridge valuation: %w", valuationErr)
	}
	peers, peerErr := client.IndustryValuation(requestCtx, symbol)
	if peerErr != nil {
		result.Warnings = append(result.Warnings, "同业比较："+SanitizeLongbridgeCandidateResearchError(peerErr))
	}
	dist, distErr := client.IndustryValuationDist(requestCtx, symbol)
	if distErr != nil {
		result.Warnings = append(result.Warnings, "行业分位："+SanitizeLongbridgeCandidateResearchError(distErr))
	}
	payload := valuationResearchPayload(valuation, peers, dist)
	encoded, err := json.Marshal(payload)
	if err != nil {
		return result, err
	}
	securityID := analystRatingSecurityID(ctx, db, result.Ticker, cik)
	now := options.Now().UTC()
	row := LongbridgeValuationSnapshot{SecurityID: securityID, Provider: longbridgeCandidateResearchProvider, Ticker: result.Ticker, PayloadJSON: string(encoded), FetchedAt: now}
	row.SnapshotHash = valuationResearchHash(row.PayloadJSON)
	var previous LongbridgeValuationSnapshot
	lookupErr := db.WithContext(ctx).Where("provider = ? AND ticker = ?", row.Provider, row.Ticker).Order("fetched_at DESC, id DESC").First(&previous).Error
	if lookupErr == nil && previous.SnapshotHash == row.SnapshotHash {
		result.Cached, result.Message = true, "估值研究数据与本地最新快照一致。"
		return result, nil
	}
	if lookupErr != nil && !errors.Is(lookupErr, gorm.ErrRecordNotFound) {
		return result, lookupErr
	}
	if lookupErr == nil {
		row.ChangeSummary = valuationResearchChangeSummary(previous.PayloadJSON, row.PayloadJSON)
	}
	if err := db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error; err != nil {
		return result, err
	}
	result.Fetched, result.ChangeSummary, result.Message = true, row.ChangeSummary, "已保存 Longbridge 估值历史、行业分位和同业比较快照。"
	return result, nil
}

func valuationResearchPayload(value *lbfundamental.ValuationData, peers *lbfundamental.IndustryValuationList, dist *lbfundamental.IndustryValuationDist) ValuationResearchSnapshot {
	payload := ValuationResearchSnapshot{Metrics: ValuationResearchMetrics{PE: valuationMetric(valueMetric(value, "pe")), PB: valuationMetric(valueMetric(value, "pb")), PS: valuationMetric(valueMetric(value, "ps"))}, Percentiles: valuationPercentiles(dist), Peers: []ValuationPeer{}}
	if peers != nil {
		for _, item := range peers.List {
			pb, ps := peerHistoricalValuation(item)
			payload.Peers = append(payload.Peers, ValuationPeer{Symbol: item.Symbol, Name: item.Name, Currency: item.Currency, PE: decimalFloat(item.PE), PB: pb, PS: ps})
		}
		sort.SliceStable(payload.Peers, func(i, j int) bool { return payload.Peers[i].Symbol < payload.Peers[j].Symbol })
	}
	return payload
}

func peerHistoricalValuation(item lbfundamental.IndustryValuationItem) (*float64, *float64) {
	if len(item.History) == 0 {
		return nil, nil
	}
	history := append([]lbfundamental.IndustryValuationHistory(nil), item.History...)
	sort.SliceStable(history, func(left, right int) bool { return history[left].Date > history[right].Date })
	return decimalFloat(history[0].PB), decimalFloat(history[0].PS)
}
func valueMetric(value *lbfundamental.ValuationData, kind string) *lbfundamental.ValuationMetricData {
	if value == nil {
		return nil
	}
	if kind == "pe" {
		return value.Metrics.PE
	}
	if kind == "pb" {
		return value.Metrics.PB
	}
	return value.Metrics.PS
}
func valuationMetric(value *lbfundamental.ValuationMetricData) ValuationMetric {
	result := ValuationMetric{History: []ValuationHistoryPoint{}}
	if value == nil {
		return result
	}
	result.Low, result.High, result.Median = decimalFloat(value.Low), decimalFloat(value.High), decimalFloat(value.Median)
	for _, point := range value.List {
		result.History = append(result.History, ValuationHistoryPoint{Date: point.Timestamp.UTC().Format(time.DateOnly), Value: decimalFloat(point.Value)})
	}
	if len(result.History) > 0 {
		result.Current = result.History[len(result.History)-1].Value
	}
	return result
}
func valuationPercentiles(value *lbfundamental.IndustryValuationDist) ValuationPercentiles {
	if value == nil {
		return ValuationPercentiles{}
	}
	return ValuationPercentiles{PE: valuationPercentile(value.PE), PB: valuationPercentile(value.PB), PS: valuationPercentile(value.PS)}
}
func valuationPercentile(value *lbfundamental.ValuationDist) ValuationPercentile {
	if value == nil {
		return ValuationPercentile{}
	}
	return ValuationPercentile{Value: decimalFloat(value.Value), Low: decimalFloat(value.Low), High: decimalFloat(value.High), Median: decimalFloat(value.Median), Ranking: decimalFloat(value.Ranking), RankIndex: value.RankIndex, RankTotal: value.RankTotal}
}
func valuationResearchHash(payload string) string {
	sum := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(sum[:])
}
func valuationResearchChangeSummary(previousPayload, currentPayload string) string {
	var before, after ValuationResearchSnapshot
	if json.Unmarshal([]byte(previousPayload), &before) != nil || json.Unmarshal([]byte(currentPayload), &after) != nil {
		return ""
	}
	changes := []string{}
	for _, item := range []struct {
		label         string
		before, after *float64
	}{{"PE", before.Metrics.PE.Current, after.Metrics.PE.Current}, {"PB", before.Metrics.PB.Current, after.Metrics.PB.Current}, {"PS", before.Metrics.PS.Current, after.Metrics.PS.Current}} {
		if item.before != nil && item.after != nil && *item.before != *item.after {
			changes = append(changes, fmt.Sprintf("%s %.2f → %.2f", item.label, *item.before, *item.after))
		}
	}
	return strings.Join(changes, "；")
}

type longbridgeValuationResearchSDKClient struct {
	fundamental *lbfundamental.FundamentalContext
}

func newLongbridgeValuationResearchSDKClient(appKey, appSecret, accessToken string) (longbridgeValuationResearchClient, error) {
	cfg, err := lbconfig.New(lbconfig.WithConfigKey(appKey, appSecret, accessToken))
	if err != nil {
		return nil, err
	}
	client, err := lbfundamental.NewFromCfg(cfg)
	if err != nil {
		return nil, err
	}
	return &longbridgeValuationResearchSDKClient{fundamental: client}, nil
}
func (c *longbridgeValuationResearchSDKClient) Valuation(ctx context.Context, symbol string) (*lbfundamental.ValuationData, error) {
	return c.fundamental.Valuation(ctx, symbol)
}
func (c *longbridgeValuationResearchSDKClient) IndustryValuation(ctx context.Context, symbol string) (*lbfundamental.IndustryValuationList, error) {
	return c.fundamental.IndustryValuation(ctx, symbol)
}
func (c *longbridgeValuationResearchSDKClient) IndustryValuationDist(ctx context.Context, symbol string) (*lbfundamental.IndustryValuationDist, error) {
	return c.fundamental.IndustryValuationDist(ctx, symbol)
}
