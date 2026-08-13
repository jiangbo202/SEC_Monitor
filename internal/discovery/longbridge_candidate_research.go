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
	lbmarket "github.com/longbridge/openapi-go/market"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const longbridgeCandidateResearchProvider = "longbridge"

// CandidateMarketResearch is local, read-only research context for a selected
// candidate. It deliberately does not change a candidate's fundamental score.
type CandidateMarketResearch struct {
	EPSForecast          EPSForecastView               `json:"eps_forecast"`
	Anomalies            []MarketAnomalySnapshot       `json:"anomalies"`
	InstitutionalHolders []InstitutionalHolderSnapshot `json:"institutional_holders"`
	FundHolders          []FundHolderSnapshot          `json:"fund_holders"`
}

// TickerInstitutionalHoldingHistory exposes the complete locally retained
// report-date history for a ticker. Unlike the compact candidate card, this
// view is intended for a watch-target detail where an operator needs to trace
// every institution/fund disclosure and its reported ratio.
type TickerInstitutionalHoldingHistory struct {
	Ticker               string                        `json:"ticker"`
	InstitutionalHolders []InstitutionalHolderSnapshot `json:"institutional_holders"`
	FundHolders          []FundHolderSnapshot          `json:"fund_holders"`
	Message              string                        `json:"message"`
}

type EPSForecastView struct {
	Latest  *EPSForecastSnapshot  `json:"latest,omitempty"`
	History []EPSForecastSnapshot `json:"history"`
	Message string                `json:"message"`
}

type CandidateMarketResearchRefreshResult struct {
	Ticker            string   `json:"ticker"`
	EPSFetched        bool     `json:"eps_fetched"`
	EPSChanged        bool     `json:"eps_changed"`
	EPSChangeSummary  string   `json:"eps_change_summary,omitempty"`
	AnomaliesSaved    int      `json:"anomalies_saved"`
	ShareholdersSaved int      `json:"shareholders_saved"`
	FundHoldersSaved  int      `json:"fund_holders_saved"`
	Warnings          []string `json:"warnings"`
	Message           string   `json:"message"`
}

type CandidateMarketResearchSyncResult struct {
	CandidateCount int      `json:"candidate_count"`
	Attempted      int      `json:"attempted"`
	Fetched        int      `json:"fetched"`
	EPSChanged     int      `json:"eps_changed"`
	Failed         int      `json:"failed"`
	Skipped        bool     `json:"skipped"`
	Message        string   `json:"message"`
	Warnings       []string `json:"warnings"`
}

type longbridgeCandidateResearchClient interface {
	ForecastEps(context.Context, string) (*lbfundamental.ForecastEps, error)
	Anomaly(context.Context, string) (*lbmarket.AnomalyResponse, error)
	Shareholder(context.Context, string) (*lbfundamental.ShareholderList, error)
	FundHolder(context.Context, string) (*lbfundamental.FundHolders, error)
}

type LongbridgeCandidateResearchOptions struct {
	AppKey      string
	AppSecret   string
	AccessToken string
	Now         func() time.Time
	NewClient   func(string, string, string) (longbridgeCandidateResearchClient, error)
}

func NewLongbridgeCandidateResearchOptions(cfg config.DiscoveryConfig) LongbridgeCandidateResearchOptions {
	return LongbridgeCandidateResearchOptions{AppKey: cfg.LongbridgeAppKey, AppSecret: cfg.LongbridgeAppSecret, AccessToken: cfg.LongbridgeAccessToken}
}

// GetCandidateMarketResearch never calls Longbridge. Detail views stay fast
// and auditable, while refreshes are explicit or performed by the bounded job.
func GetCandidateMarketResearch(ctx context.Context, db *gorm.DB, ticker string) (CandidateMarketResearch, error) {
	result := CandidateMarketResearch{EPSForecast: EPSForecastView{History: []EPSForecastSnapshot{}}, Anomalies: []MarketAnomalySnapshot{}, InstitutionalHolders: []InstitutionalHolderSnapshot{}, FundHolders: []FundHolderSnapshot{}}
	if db == nil {
		return result, errors.New("database is required")
	}
	symbol := normalizeAnalystRatingTicker(ticker)
	if symbol == "" {
		return result, errors.New("ticker is required")
	}
	if err := db.WithContext(ctx).Where("provider = ? AND ticker = ?", longbridgeCandidateResearchProvider, symbol).Order("fetched_at DESC, id DESC").Limit(24).Find(&result.EPSForecast.History).Error; err != nil {
		return result, err
	}
	if len(result.EPSForecast.History) == 0 {
		result.EPSForecast.Message = "尚未同步 EPS 市场预期；可在候选详情手动刷新。"
	} else {
		result.EPSForecast.Latest = &result.EPSForecast.History[0]
		result.EPSForecast.Message = "Longbridge EPS 预期快照；预期变化仅作为研究提醒。"
	}
	if err := db.WithContext(ctx).Where("provider = ? AND ticker = ?", longbridgeCandidateResearchProvider, symbol).Order("alert_time DESC, id DESC").Limit(20).Find(&result.Anomalies).Error; err != nil {
		return result, err
	}
	if err := db.WithContext(ctx).Where("provider = ? AND ticker = ?", longbridgeCandidateResearchProvider, symbol).Order("report_date DESC, percent_of_shares DESC, id DESC").Limit(50).Find(&result.InstitutionalHolders).Error; err != nil {
		return result, err
	}
	if err := db.WithContext(ctx).Where("provider = ? AND ticker = ?", longbridgeCandidateResearchProvider, symbol).Order("report_date DESC, position_ratio DESC, id DESC").Limit(50).Find(&result.FundHolders).Error; err != nil {
		return result, err
	}
	return result, nil
}

// GetTickerInstitutionalHoldingHistory reads every locally retained disclosure
// row for one ticker and never requests Longbridge on page load. A row's
// identity includes provider, ticker, holder and report date, so later reports
// remain available beside earlier disclosures.
func GetTickerInstitutionalHoldingHistory(ctx context.Context, db *gorm.DB, ticker string) (TickerInstitutionalHoldingHistory, error) {
	result := TickerInstitutionalHoldingHistory{InstitutionalHolders: []InstitutionalHolderSnapshot{}, FundHolders: []FundHolderSnapshot{}}
	if db == nil {
		return result, errors.New("database is required")
	}
	symbol := normalizeAnalystRatingTicker(ticker)
	if symbol == "" {
		return result, errors.New("ticker is required")
	}
	result.Ticker = symbol
	if err := db.WithContext(ctx).Where("provider = ? AND ticker = ?", longbridgeCandidateResearchProvider, symbol).Order("report_date DESC, holder_name ASC, id DESC").Find(&result.InstitutionalHolders).Error; err != nil {
		return result, err
	}
	if err := db.WithContext(ctx).Where("provider = ? AND ticker = ?", longbridgeCandidateResearchProvider, symbol).Order("report_date DESC, fund_name ASC, id DESC").Find(&result.FundHolders).Error; err != nil {
		return result, err
	}
	if len(result.InstitutionalHolders) == 0 && len(result.FundHolders) == 0 {
		result.Message = "尚未同步 Longbridge 机构或基金持仓披露；可手动刷新，或等待每日监控标的市场研究任务。"
	} else {
		result.Message = "Longbridge 返回的机构股东与基金/ETF 持仓披露历史。持股比例、组合权重和报告日均为提供方口径；不同机构的披露频率与覆盖范围可能不同。"
	}
	return result, nil
}

// RefreshLongbridgeCandidateMarketResearch fetches only one selected issuer.
// A partial provider response is persisted with warnings so missing small-cap
// coverage is distinguishable from an application failure.
func RefreshLongbridgeCandidateMarketResearch(ctx context.Context, db *gorm.DB, cfg config.DiscoveryConfig, ticker, cik string) (CandidateMarketResearchRefreshResult, error) {
	return refreshLongbridgeCandidateMarketResearch(ctx, db, ticker, cik, NewLongbridgeCandidateResearchOptions(cfg))
}

func refreshLongbridgeCandidateMarketResearch(ctx context.Context, db *gorm.DB, ticker, cik string, options LongbridgeCandidateResearchOptions) (CandidateMarketResearchRefreshResult, error) {
	result := CandidateMarketResearchRefreshResult{Ticker: normalizeAnalystRatingTicker(ticker), Warnings: []string{}}
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
		options.NewClient = newLongbridgeCandidateResearchSDKClient
	}
	client, err := options.NewClient(options.AppKey, options.AppSecret, options.AccessToken)
	if err != nil {
		return result, fmt.Errorf("create Longbridge candidate research client: %w", err)
	}
	securityID := analystRatingSecurityID(ctx, db, result.Ticker, cik)
	now := options.Now().UTC()
	requestCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	symbol := result.Ticker + ".US"

	if forecast, fetchErr := client.ForecastEps(requestCtx, symbol); fetchErr != nil {
		result.Warnings = append(result.Warnings, "EPS 预期："+SanitizeLongbridgeCandidateResearchError(fetchErr))
	} else if latest, ok := latestForecastEpsItem(forecast); ok {
		snapshot := epsForecastSnapshotFromLongbridge(result.Ticker, securityID, latest, now)
		previous, lookupErr := latestEPSForecastSnapshot(ctx, db, snapshot.Ticker)
		if lookupErr != nil {
			return result, lookupErr
		}
		if previous != nil && previous.SnapshotHash == snapshot.SnapshotHash {
			result.EPSFetched = true
		} else {
			if previous != nil {
				snapshot.ChangeSummary = epsForecastChangeSummary(*previous, snapshot)
				if snapshot.ChangeSummary != "" {
					snapshot.NotificationStatus = "pending"
				}
			}
			if snapshot.NotificationStatus == "" {
				snapshot.NotificationStatus = "not_applicable"
			}
			if err := db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&snapshot).Error; err != nil {
				return result, fmt.Errorf("save EPS forecast snapshot: %w", err)
			}
			result.EPSFetched = true
			result.EPSChanged = snapshot.ChangeSummary != ""
			result.EPSChangeSummary = snapshot.ChangeSummary
		}
	} else {
		result.Warnings = append(result.Warnings, "EPS 预期：Longbridge 暂无覆盖")
	}

	if anomalies, fetchErr := client.Anomaly(requestCtx, "US"); fetchErr != nil {
		result.Warnings = append(result.Warnings, "市场异动："+SanitizeLongbridgeCandidateResearchError(fetchErr))
	} else {
		result.AnomaliesSaved, err = saveLongbridgeAnomalies(ctx, db, securityID, result.Ticker, anomalies, now)
		if err != nil {
			return result, err
		}
	}
	if shareholders, fetchErr := client.Shareholder(requestCtx, symbol); fetchErr != nil {
		result.Warnings = append(result.Warnings, "机构股东："+SanitizeLongbridgeCandidateResearchError(fetchErr))
	} else {
		result.ShareholdersSaved, err = saveLongbridgeInstitutionalHolders(ctx, db, securityID, result.Ticker, shareholders, now)
		if err != nil {
			return result, err
		}
	}
	if holders, fetchErr := client.FundHolder(requestCtx, symbol); fetchErr != nil {
		result.Warnings = append(result.Warnings, "基金持仓："+SanitizeLongbridgeCandidateResearchError(fetchErr))
	} else {
		result.FundHoldersSaved, err = saveLongbridgeFundHolders(ctx, db, securityID, result.Ticker, holders, now)
		if err != nil {
			return result, err
		}
	}
	if result.EPSFetched || result.AnomaliesSaved > 0 || result.ShareholdersSaved > 0 || result.FundHoldersSaved > 0 {
		result.Message = "已更新 Longbridge 市场预期、异动和机构持仓研究快照。"
	} else {
		result.Message = "Longbridge 未返回该标的可保存的 P1 研究数据。"
	}
	return result, nil
}

func latestForecastEpsItem(value *lbfundamental.ForecastEps) (lbfundamental.ForecastEpsItem, bool) {
	if value == nil || len(value.Items) == 0 {
		return lbfundamental.ForecastEpsItem{}, false
	}
	items := append([]lbfundamental.ForecastEpsItem(nil), value.Items...)
	sort.SliceStable(items, func(left, right int) bool {
		if !items[left].ForecastStartDate.Equal(items[right].ForecastStartDate) {
			return items[left].ForecastStartDate.After(items[right].ForecastStartDate)
		}
		return items[left].ForecastEndDate.After(items[right].ForecastEndDate)
	})
	return items[0], true
}

func latestEPSForecastSnapshot(ctx context.Context, db *gorm.DB, ticker string) (*EPSForecastSnapshot, error) {
	var row EPSForecastSnapshot
	err := db.WithContext(ctx).Where("provider = ? AND ticker = ?", longbridgeCandidateResearchProvider, ticker).Order("fetched_at DESC, id DESC").First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &row, nil
}

func epsForecastSnapshotFromLongbridge(ticker string, securityID uint, item lbfundamental.ForecastEpsItem, now time.Time) EPSForecastSnapshot {
	row := EPSForecastSnapshot{SecurityID: securityID, Provider: longbridgeCandidateResearchProvider, Ticker: ticker, ForecastStartDate: item.ForecastStartDate, ForecastEndDate: item.ForecastEndDate, Mean: decimalFloat(item.ForecastEpsMean), Median: decimalFloat(item.ForecastEpsMedian), Low: decimalFloat(item.ForecastEpsLowest), High: decimalFloat(item.ForecastEpsHighest), InstitutionTotal: int(item.InstitutionTotal), InstitutionUp: int(item.InstitutionUp), InstitutionDown: int(item.InstitutionDown), FetchedAt: now}
	row.SnapshotHash = epsForecastHash(row)
	return row
}

func epsForecastHash(row EPSForecastSnapshot) string {
	values := []string{row.Provider, row.Ticker, row.ForecastStartDate.UTC().Format(time.RFC3339), row.ForecastEndDate.UTC().Format(time.RFC3339), floatText(row.Mean), floatText(row.Median), floatText(row.Low), floatText(row.High), fmt.Sprintf("%d", row.InstitutionTotal), fmt.Sprintf("%d", row.InstitutionUp), fmt.Sprintf("%d", row.InstitutionDown)}
	sum := sha256.Sum256([]byte(strings.Join(values, "|")))
	return hex.EncodeToString(sum[:])
}

func epsForecastChangeSummary(previous, current EPSForecastSnapshot) string {
	changes := []string{}
	if previous.Median != nil && current.Median != nil && *previous.Median != *current.Median {
		direction := "上修"
		if *current.Median < *previous.Median {
			direction = "下修"
		}
		changes = append(changes, fmt.Sprintf("EPS 预期中位数%s %.3f → %.3f", direction, *previous.Median, *current.Median))
	}
	if previous.Low != nil && previous.High != nil && current.Low != nil && current.High != nil && (*previous.Low != *current.Low || *previous.High != *current.High) {
		changes = append(changes, fmt.Sprintf("EPS 区间 %.3f–%.3f → %.3f–%.3f", *previous.Low, *previous.High, *current.Low, *current.High))
	}
	if previous.InstitutionUp != current.InstitutionUp || previous.InstitutionDown != current.InstitutionDown || previous.InstitutionTotal != current.InstitutionTotal {
		changes = append(changes, fmt.Sprintf("机构上修/下修 %d/%d → %d/%d", previous.InstitutionUp, previous.InstitutionDown, current.InstitutionUp, current.InstitutionDown))
	}
	return strings.Join(changes, "；")
}

func saveLongbridgeAnomalies(ctx context.Context, db *gorm.DB, securityID uint, ticker string, value *lbmarket.AnomalyResponse, now time.Time) (int, error) {
	if value == nil {
		return 0, nil
	}
	rows := make([]MarketAnomalySnapshot, 0)
	for _, item := range value.Changes {
		if normalizeAnalystRatingTicker(item.Symbol) != ticker || item.AlertTime <= 0 {
			continue
		}
		values, _ := json.Marshal(item.ChangeValues)
		alertTime := unixMilliseconds(item.AlertTime)
		key := fmt.Sprintf("%s:%d:%s", ticker, item.AlertTime, item.AlertName)
		rows = append(rows, MarketAnomalySnapshot{SecurityID: securityID, Provider: longbridgeCandidateResearchProvider, Ticker: ticker, EventKey: key, Name: item.Name, AlertName: item.AlertName, AlertTime: alertTime, ValuesJSON: string(values), Emotion: int(item.Emotion), FetchedAt: now})
	}
	if len(rows) == 0 {
		return 0, nil
	}
	err := db.WithContext(ctx).Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "provider"}, {Name: "event_key"}}, DoUpdates: clause.AssignmentColumns([]string{"security_id", "name", "alert_name", "alert_time", "values_json", "emotion", "fetched_at", "updated_at"})}).Create(&rows).Error
	return len(rows), err
}

func saveLongbridgeInstitutionalHolders(ctx context.Context, db *gorm.DB, securityID uint, ticker string, value *lbfundamental.ShareholderList, now time.Time) (int, error) {
	if value == nil {
		return 0, nil
	}
	rows := make([]InstitutionalHolderSnapshot, 0, len(value.ShareholderList))
	for _, item := range value.ShareholderList {
		name := strings.TrimSpace(item.ShareholderName)
		if name == "" {
			continue
		}
		rows = append(rows, InstitutionalHolderSnapshot{SecurityID: securityID, Provider: longbridgeCandidateResearchProvider, Ticker: ticker, HolderName: name, InstitutionType: strings.TrimSpace(item.InstitutionType), PercentOfShares: decimalFloat(item.PercentOfShares), SharesChanged: decimalFloat(item.SharesChanged), ReportDate: strings.TrimSpace(item.ReportDate), SourceURL: longbridgeCandidateResearchSourceURL("shareholder"), FetchedAt: now})
	}
	if len(rows) == 0 {
		return 0, nil
	}
	err := db.WithContext(ctx).Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "provider"}, {Name: "ticker"}, {Name: "holder_name"}, {Name: "report_date"}}, DoUpdates: clause.AssignmentColumns([]string{"security_id", "institution_type", "percent_of_shares", "shares_changed", "source_url", "fetched_at", "updated_at"})}).Create(&rows).Error
	return len(rows), err
}

func saveLongbridgeFundHolders(ctx context.Context, db *gorm.DB, securityID uint, ticker string, value *lbfundamental.FundHolders, now time.Time) (int, error) {
	if value == nil {
		return 0, nil
	}
	rows := make([]FundHolderSnapshot, 0, len(value.Lists))
	for _, item := range value.Lists {
		code := strings.TrimSpace(item.Code)
		if code == "" {
			continue
		}
		rows = append(rows, FundHolderSnapshot{SecurityID: securityID, Provider: longbridgeCandidateResearchProvider, Ticker: ticker, FundCode: code, FundSymbol: strings.TrimSpace(item.Symbol), FundName: strings.TrimSpace(item.Name), Currency: strings.TrimSpace(item.Currency), PositionRatio: decimalToFloat(item.PositionRatio), ReportDate: strings.TrimSpace(item.ReportDate), SourceURL: longbridgeCandidateResearchSourceURL("fund-holder"), FetchedAt: now})
	}
	if len(rows) == 0 {
		return 0, nil
	}
	err := db.WithContext(ctx).Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "provider"}, {Name: "ticker"}, {Name: "fund_code"}, {Name: "report_date"}}, DoUpdates: clause.AssignmentColumns([]string{"security_id", "fund_symbol", "fund_name", "currency", "position_ratio", "source_url", "fetched_at", "updated_at"})}).Create(&rows).Error
	return len(rows), err
}

func SyncCurrentCandidateLongbridgeMarketResearch(ctx context.Context, db *gorm.DB, cfg config.DiscoveryConfig) (CandidateMarketResearchSyncResult, error) {
	result := CandidateMarketResearchSyncResult{Warnings: []string{}}
	if db == nil {
		return result, errors.New("database is required")
	}
	if !cfg.LongbridgeCandidateResearchEnabled || cfg.LongbridgeCandidateResearchRequestBudget <= 0 {
		result.Skipped, result.Message = true, "Longbridge P1 候选研究同步已关闭或预算为 0"
		return result, nil
	}
	lastFetched := map[string]time.Time{}
	var prior []EPSForecastSnapshot
	if err := db.WithContext(ctx).Where("provider = ?", longbridgeCandidateResearchProvider).Order("fetched_at DESC, id DESC").Find(&prior).Error; err != nil {
		return result, err
	}
	for _, row := range prior {
		if _, ok := lastFetched[row.Ticker]; !ok {
			lastFetched[row.Ticker] = row.FetchedAt
		}
	}
	now := time.Now().UTC()
	fresh, freshErr := FreshLongbridgeResearchTickers(ctx, db, LongbridgeRefreshFamilyMarketResearch, now)
	if freshErr != nil {
		return result, freshErr
	}
	tickers, candidateCount, selectErr := leastRecentlyRefreshedCandidateTickers(ctx, db, cfg.LongbridgeCandidateResearchRequestBudget, lastFetched, fresh)
	if selectErr != nil {
		return result, selectErr
	}
	result.CandidateCount = candidateCount
	if candidateCount == 0 {
		result.Skipped, result.Message = true, "暂无已发布的小盘候选批次"
		return result, nil
	}
	for _, ticker := range tickers {
		result.Attempted++
		refreshed, refreshErr := RefreshLongbridgeCandidateMarketResearch(ctx, db, cfg, ticker, "")
		if refreshErr != nil {
			result.Failed++
			result.Warnings = append(result.Warnings, ticker+"："+SanitizeLongbridgeCandidateResearchError(refreshErr))
			continue
		}
		if markErr := MarkLongbridgeResearchSuccess(ctx, db, LongbridgeRefreshFamilyMarketResearch, ticker, now); markErr != nil {
			return result, markErr
		}
		if refreshed.EPSFetched || refreshed.AnomaliesSaved > 0 || refreshed.ShareholdersSaved > 0 || refreshed.FundHoldersSaved > 0 {
			result.Fetched++
		}
		if refreshed.EPSChanged {
			result.EPSChanged++
		}
		result.Warnings = append(result.Warnings, refreshed.Warnings...)
	}
	result.Message = fmt.Sprintf("已同步 %d 个候选的 Longbridge 市场研究，EPS 变化 %d 个，失败 %d 个", result.Fetched, result.EPSChanged, result.Failed)
	return result, nil
}

// leastRecentlyRefreshedCandidateTickers rotates through the current published
// candidate universe. Each independently scheduled research family supplies
// its own last-fetched map, so a slower valuation endpoint never blocks P1.
func leastRecentlyRefreshedCandidateTickers(ctx context.Context, db *gorm.DB, budget int, lastFetched map[string]time.Time, fresh map[string]bool) ([]string, int, error) {
	batch, ok, err := currentPublishedPrescreenBatch(ctx, db)
	if err != nil || !ok {
		return []string{}, 0, err
	}
	var scores []CandidateScoreSnapshot
	if err := db.WithContext(ctx).Where("batch_id = ?", batch.BatchID).Order("total_score DESC, ticker ASC").Find(&scores).Error; err != nil {
		return nil, 0, err
	}
	sort.SliceStable(scores, func(left, right int) bool {
		leftAt, leftOK := lastFetched[normalizeAnalystRatingTicker(scores[left].Ticker)]
		rightAt, rightOK := lastFetched[normalizeAnalystRatingTicker(scores[right].Ticker)]
		if leftOK != rightOK {
			return !leftOK
		}
		if !leftAt.Equal(rightAt) {
			return leftAt.Before(rightAt)
		}
		return scores[left].TotalScore > scores[right].TotalScore
	})
	tickers := make([]string, 0, budget)
	seen := map[string]bool{}
	for _, score := range scores {
		if len(tickers) >= budget {
			break
		}
		ticker := normalizeAnalystRatingTicker(score.Ticker)
		if ticker == "" || seen[ticker] || fresh[ticker] {
			continue
		}
		seen[ticker] = true
		tickers = append(tickers, ticker)
	}
	return tickers, len(scores), nil
}

func decimalFloat(value *decimal.Decimal) *float64 {
	if value == nil {
		return nil
	}
	parsed := value.InexactFloat64()
	return &parsed
}
func decimalToFloat(value decimal.Decimal) float64 { return value.InexactFloat64() }
func floatText(value *float64) string {
	if value == nil {
		return ""
	}
	return fmt.Sprintf("%.8f", *value)
}
func unixMilliseconds(value int64) time.Time {
	if value > 1_000_000_000_000 {
		return time.UnixMilli(value).UTC()
	}
	return time.Unix(value, 0).UTC()
}
func longbridgeCandidateResearchSourceURL(kind string) string {
	return "https://open.longbridge.com/docs/quote/pull/" + kind
}
func SanitizeLongbridgeCandidateResearchError(err error) string {
	if err == nil {
		return ""
	}
	return strings.ReplaceAll(strings.TrimSpace(err.Error()), "\n", " ")
}

type longbridgeCandidateResearchSDKClient struct {
	fundamental *lbfundamental.FundamentalContext
	market      *lbmarket.MarketContext
}

func newLongbridgeCandidateResearchSDKClient(appKey, appSecret, accessToken string) (longbridgeCandidateResearchClient, error) {
	cfg, err := lbconfig.New(lbconfig.WithConfigKey(appKey, appSecret, accessToken))
	if err != nil {
		return nil, err
	}
	fundamental, err := lbfundamental.NewFromCfg(cfg)
	if err != nil {
		return nil, err
	}
	market, err := lbmarket.NewFromCfg(cfg)
	if err != nil {
		return nil, err
	}
	return &longbridgeCandidateResearchSDKClient{fundamental: fundamental, market: market}, nil
}
func (c *longbridgeCandidateResearchSDKClient) ForecastEps(ctx context.Context, symbol string) (*lbfundamental.ForecastEps, error) {
	return c.fundamental.ForecastEps(ctx, symbol)
}
func (c *longbridgeCandidateResearchSDKClient) Anomaly(ctx context.Context, market string) (*lbmarket.AnomalyResponse, error) {
	return c.market.Anomaly(ctx, market)
}
func (c *longbridgeCandidateResearchSDKClient) Shareholder(ctx context.Context, symbol string) (*lbfundamental.ShareholderList, error) {
	return c.fundamental.Shareholder(ctx, symbol)
}
func (c *longbridgeCandidateResearchSDKClient) FundHolder(ctx context.Context, symbol string) (*lbfundamental.FundHolders, error) {
	return c.fundamental.FundHolder(ctx, symbol)
}
