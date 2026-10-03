package discovery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	lbfundamental "github.com/longbridge/openapi-go/fundamental"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"sec_monitor/internal/config"
)

const ownershipDocs = "https://open.longbridge.com/docs/fundamental/fundamental/"

// No hidden rate-limit retries: the queue's cap counts actual provider calls.
func ownershipCall[T any](ctx context.Context, interval time.Duration, call func(context.Context) (T, error)) (T, error) {
	var zero T
	if err := waitLongbridgeFundamentalSlot(ctx, interval); err != nil {
		return zero, err
	}
	return call(ctx)
}

var ownershipRefreshInFlight sync.Map

type ownershipRefreshKey struct {
	DB     *gorm.DB
	Ticker string
}

func classifyInstitutionType(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "institution", "institutional", "fund", "investment advisor", "investment adviser", "investment manager", "bank", "insurance", "pension fund", "机构", "基金":
		return "Institution"
	case "person", "individual", "个人":
		return "Person"
	case "insider", "内部人士":
		return "Insider"
	case "company":
		return "Company"
	default:
		return "Unknown"
	}
}

func splitInstitutionalHolders(rows []InstitutionalHolderSnapshot) ([]InstitutionalHolderSnapshot, []InstitutionalHolderSnapshot) {
	institutions, other := []InstitutionalHolderSnapshot{}, []InstitutionalHolderSnapshot{}
	for _, row := range rows {
		if row.OwnerType == "" {
			row.OwnerType = classifyInstitutionType(row.InstitutionType)
		}
		if row.OwnerType == "Institution" {
			institutions = append(institutions, row)
		} else {
			other = append(other, row)
		}
	}
	return institutions, other
}

type ownershipHistoryClient interface {
	ShareholderTop(context.Context, string) (*lbfundamental.ShareholderTopResponse, error)
	ShareholderDetail(context.Context, string, int64) (*lbfundamental.ShareholderDetailResponse, error)
}

func (c *longbridgeCandidateResearchSDKClient) ShareholderTop(ctx context.Context, symbol string) (*lbfundamental.ShareholderTopResponse, error) {
	if err := CheckCurrentAPIEndpoint(ctx, "longbridge", "/v1/quote/shareholders"); err != nil {
		return nil, err
	}
	id, err := explicitUSStockCounterID(symbol)
	if err != nil {
		return nil, err
	}
	return c.fundamental.ShareholderTop(ctx, id)
}
func (c *longbridgeCandidateResearchSDKClient) ShareholderDetail(ctx context.Context, symbol string, holderID int64) (*lbfundamental.ShareholderDetailResponse, error) {
	if err := CheckCurrentAPIEndpoint(ctx, "longbridge", "/v1/quote/shareholders/holding"); err != nil {
		return nil, err
	}
	id, err := explicitUSStockCounterID(symbol)
	if err != nil {
		return nil, err
	}
	return c.fundamental.ShareholderDetail(ctx, id, holderID)
}

type OwnershipRefreshResult struct {
	FailedRequests int      `json:"failed_requests"`
	PointsSaved    int      `json:"points_saved"`
	HoldersFetched int      `json:"holders_fetched"`
	Cached         int      `json:"cached"`
	Warnings       []string `json:"warnings"`
}

// Only an explicit refresh calls the provider. Top and per-holder receipts use
// a one-day cache, including successful empty histories, to bound API cost.
func RefreshInstitutionalOwnership(ctx context.Context, db *gorm.DB, cfg config.DiscoveryConfig, ticker string) (OwnershipRefreshResult, error) {
	return RefreshInstitutionalOwnershipBudgeted(ctx, db, cfg, ticker, 20)
}

// Background queues use a smaller detail budget; receipts allow later runs to resume.
func RefreshInstitutionalOwnershipBudgeted(ctx context.Context, db *gorm.DB, cfg config.DiscoveryConfig, ticker string, budget int) (OwnershipRefreshResult, error) {
	options := NewLongbridgeCandidateResearchOptions(cfg)
	client, err := newLongbridgeCandidateResearchSDKClient(options.AppKey, options.AppSecret, options.AccessToken)
	if err != nil {
		return OwnershipRefreshResult{}, err
	}
	historyClient, ok := client.(ownershipHistoryClient)
	if !ok {
		return OwnershipRefreshResult{}, errors.New("provider does not support ownership history")
	}
	return refreshInstitutionalOwnershipLimited(ctx, db, ticker, historyClient, options.RequestInterval, time.Now().UTC(), budget)
}

func refreshInstitutionalOwnership(ctx context.Context, db *gorm.DB, ticker string, client ownershipHistoryClient, interval time.Duration, now time.Time) (OwnershipRefreshResult, error) {
	return refreshInstitutionalOwnershipLimited(ctx, db, ticker, client, interval, now, 20)
}

func refreshInstitutionalOwnershipLimited(ctx context.Context, db *gorm.DB, ticker string, client ownershipHistoryClient, interval time.Duration, now time.Time, budget int) (OwnershipRefreshResult, error) {
	if budget < 1 || budget > 20 {
		return OwnershipRefreshResult{}, errors.New("ownership detail budget must be 1..20")
	}
	result := OwnershipRefreshResult{Warnings: []string{}}
	ticker = normalizeAnalystRatingTicker(ticker)
	if db == nil || ticker == "" {
		return result, errors.New("database and ticker are required")
	}
	key := ownershipRefreshKey{DB: db, Ticker: ticker}
	if _, running := ownershipRefreshInFlight.LoadOrStore(key, struct{}{}); running {
		return result, errors.New("该标的机构历史正在刷新，请稍后查询本地记录")
	}
	defer ownershipRefreshInFlight.Delete(key)
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	symbol := ticker + ".US"
	var topRaw json.RawMessage
	var cached InstitutionalOwnershipReceipt
	err := db.WithContext(ctx).Where("ticker = ? AND holder_id = ?", ticker, "top").First(&cached).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return result, err
	}
	if err == nil && now.Sub(cached.FetchedAt) >= 0 && now.Sub(cached.FetchedAt) < 24*time.Hour {
		topRaw = json.RawMessage(cached.PayloadJSON)
		result.Cached++
	} else {
		top, err := ownershipCall(ctx, interval, func(c context.Context) (*lbfundamental.ShareholderTopResponse, error) {
			return client.ShareholderTop(c, symbol)
		})
		if err != nil {
			return result, err
		}
		if top == nil {
			return result, errors.New("empty top shareholder response")
		}
		topRaw = top.Data
	}
	topFetchedAt := now
	if result.Cached > 0 {
		topFetchedAt = cached.FetchedAt
	}
	points, err := parseOwnershipTop(ticker, topRaw, topFetchedAt)
	if err != nil {
		return result, err
	}
	if err := saveOwnershipPoints(ctx, db, points); err != nil {
		return result, err
	}
	result.PointsSaved += len(points)
	if err := saveOwnershipReceipt(ctx, db, ticker, "top", topRaw, "available", now, result.Cached == 0); err != nil {
		return result, err
	}
	ids := map[string]string{}
	ordered := []string{}
	for _, point := range points {
		if point.OwnerType == "Institution" || point.OwnerType == "Unknown" {
			if _, exists := ids[point.HolderID]; !exists {
				ordered = append(ordered, point.HolderID)
				ids[point.HolderID] = point.HolderName
			}
		}
	}
	if len(points) == 0 {
		result.Warnings = append(result.Warnings, "提供方未返回主要股东历史，不视为零机构持仓。")
	}
	// Rotate details too: always taking the top five after cache expiry would
	// permanently starve older holders. Load receipts once instead of N lookups.
	var receipts []InstitutionalOwnershipReceipt
	if err := db.WithContext(ctx).Where("ticker = ?", ticker).Find(&receipts).Error; err != nil {
		return result, err
	}
	receiptByID := map[string]InstitutionalOwnershipReceipt{}
	for _, receipt := range receipts {
		receiptByID[receipt.HolderID] = receipt
	}
	sort.SliceStable(ordered, func(i, j int) bool {
		return receiptByID[ordered[i]].FetchedAt.Before(receiptByID[ordered[j]].FetchedAt)
	})
	// Multiple periods can contain different top lists. Cap this explicit run
	// at 20 details; never claim to have synchronized the entire institution universe.
	attempted := 0
	for _, id := range ordered {
		if ctx.Err() != nil {
			result.Warnings = append(result.Warnings, "历史刷新达到时间上限，已保存的披露仍可查看。")
			break
		}
		receipt, exists := receiptByID[id]
		if exists && now.Sub(receipt.FetchedAt) >= 0 && now.Sub(receipt.FetchedAt) < 24*time.Hour {
			result.Cached++
			continue
		}
		if attempted >= budget {
			result.Warnings = append(result.Warnings, fmt.Sprintf("本次最多请求 %d 家股东明细，其余等待后续轮转或手动刷新；覆盖仍不完整。", budget))
			break
		}
		numericID, err := strconv.ParseInt(id, 10, 64)
		if err != nil || numericID <= 0 {
			result.Warnings = append(result.Warnings, ids[id]+"：股东 ID 无效，未请求历史。")
			continue
		}
		attempted++
		detail, err := ownershipCall(ctx, interval, func(c context.Context) (*lbfundamental.ShareholderDetailResponse, error) {
			return client.ShareholderDetail(c, symbol, numericID)
		})
		if err != nil {
			result.Warnings = append(result.Warnings, ids[id]+"："+SanitizeLongbridgeCandidateResearchError(err))
			result.FailedRequests++
			continue
		}
		if detail == nil {
			result.FailedRequests++
			result.Warnings = append(result.Warnings, ids[id]+"：未返回历史。")
			continue
		}
		rows, status, err := parseOwnershipDetail(ticker, id, ids[id], detail.Data, now)
		if err != nil {
			result.FailedRequests++
			if json.Valid(detail.Data) {
				if saveErr := saveOwnershipReceipt(ctx, db, ticker, id, detail.Data, "unsupported_schema", now, true); saveErr != nil {
					return result, saveErr
				}
			}
			result.Warnings = append(result.Warnings, ids[id]+"：历史格式无法识别，未生成趋势。")
			continue
		}
		if err := saveOwnershipPoints(ctx, db, rows); err != nil {
			return result, err
		}
		if err := saveOwnershipReceipt(ctx, db, ticker, id, detail.Data, status, now, true); err != nil {
			return result, err
		}
		result.PointsSaved += len(rows)
		result.HoldersFetched++
		if status != "available" {
			result.Warnings = append(result.Warnings, ids[id]+"："+ownershipStatusMessage(status))
		}
	}
	return result, nil
}

func ownershipNumber(value json.RawMessage, percentage bool) *float64 {
	if len(value) == 0 || string(value) == "null" {
		return nil
	}
	var s string
	if json.Unmarshal(value, &s) != nil {
		s = string(value)
	}
	s = strings.ReplaceAll(strings.TrimSpace(s), ",", "")
	if percentage {
		s = strings.TrimSuffix(s, "%")
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
		return nil
	}
	return &v
}

func ownershipDate(s string) string {
	s = strings.NewReplacer("/", "-", ".", "-").Replace(strings.TrimSpace(s))
	if _, err := time.Parse("2006-01-02", s); err != nil {
		return ""
	}
	return s
}

func parseOwnershipTop(ticker string, raw json.RawMessage, now time.Time) ([]InstitutionalOwnershipPoint, error) {
	var payload struct {
		Info []struct {
			Period  string `json:"period"`
			Holders []struct {
				ID         string          `json:"object_id"`
				Name       string          `json:"name"`
				Title      string          `json:"title"`
				Percent    json.RawMessage `json:"percent_shares_held"`
				Shares     json.RawMessage `json:"shares_held"`
				Period     string          `json:"period"`
				FilingDate string          `json:"filing_date"`
			} `json:"share_holders"`
		} `json:"info"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, err
	}
	var shape map[string]json.RawMessage
	_ = json.Unmarshal(raw, &shape)
	if _, ok := shape["info"]; !ok {
		return nil, errors.New("top shareholder response missing info")
	}
	rows := []InstitutionalOwnershipPoint{}
	for _, group := range payload.Info {
		for _, item := range group.Holders {
			if item.ID == "" || strings.TrimSpace(item.Name) == "" {
				continue
			}
			period := item.Period
			if period == "" {
				period = group.Period
			}
			date := ownershipDate(item.FilingDate)
			// "Latest" is a rolling label, not a report date. Preserve unknown
			// dates as an undated point, not a daily snapshot at fetch time.
			key := period + "|" + date
			rows = append(rows, InstitutionalOwnershipPoint{Provider: "longbridge", Ticker: ticker, HolderID: item.ID, PeriodKey: key, HolderName: strings.TrimSpace(item.Name), OwnerType: classifyInstitutionType(item.Title), Period: period, ProviderDate: date, PercentOfShares: ownershipNumber(item.Percent, true), SharesHeld: ownershipNumber(item.Shares, false), SourceKind: "top", SourceURL: ownershipDocs + "shareholder-top", FetchedAt: now})
		}
	}
	return rows, nil
}

func parseOwnershipDetail(ticker, id, name string, raw json.RawMessage, now time.Time) ([]InstitutionalOwnershipPoint, string, error) {
	var payload struct {
		OwnerType      string                       `json:"owner_source"`
		HoldingDetails []map[string]json.RawMessage `json:"holding_details"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, "", err
	}
	rows := []InstitutionalOwnershipPoint{}
	if payload.OwnerType != "Institution" {
		if classifyInstitutionType(payload.OwnerType) != "Unknown" {
			return rows, "excluded_type", nil
		}
		return rows, "unconfirmed_type", nil
	}
	for _, item := range payload.HoldingDetails {
		stringField := func(key string) string { var s string; _ = json.Unmarshal(item[key], &s); return s }
		holdingDate, providerDate := ownershipDate(stringField("holding_date")), ownershipDate(stringField("filing_date"))
		period := stringField("period")
		percent, shares := ownershipNumber(item["percent_shares_held"], true), ownershipNumber(item["shares_held"], false)
		if (holdingDate == "" && providerDate == "") || (percent == nil && shares == nil) {
			continue
		}
		keyDate := providerDate
		if keyDate == "" {
			keyDate = holdingDate
		}
		rows = append(rows, InstitutionalOwnershipPoint{Provider: "longbridge", Ticker: ticker, HolderID: id, PeriodKey: period + "|" + keyDate, HolderName: name, OwnerType: "Institution", Period: period, HoldingDate: holdingDate, ProviderDate: providerDate, PercentOfShares: percent, SharesHeld: shares, SourceKind: "detail", SourceURL: ownershipDocs + "shareholder-detail", FetchedAt: now})
	}
	status := "available"
	if len(rows) == 0 {
		status = "no_history"
		if len(payload.HoldingDetails) > 0 {
			status = "unsupported_schema"
		}
	}
	return rows, status, nil
}

func saveOwnershipPoints(ctx context.Context, db *gorm.DB, rows []InstitutionalOwnershipPoint) error {
	if len(rows) == 0 {
		return nil
	}
	return db.WithContext(ctx).Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "provider"}, {Name: "ticker"}, {Name: "holder_id"}, {Name: "period_key"}}, DoUpdates: clause.AssignmentColumns([]string{"holder_name", "owner_type", "period", "holding_date", "filing_date", "provider_date", "percent_of_shares", "shares_held", "source_kind", "source_url", "fetched_at"}), Where: clause.Where{Exprs: []clause.Expression{clause.Expr{SQL: "institutional_ownership_points.source_kind <> 'detail' OR excluded.source_kind = 'detail'"}}}}).Create(&rows).Error
}

func saveOwnershipReceipt(ctx context.Context, db *gorm.DB, ticker, id string, raw json.RawMessage, status string, now time.Time, write bool) error {
	if !write {
		return nil
	}
	return db.WithContext(ctx).Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "ticker"}, {Name: "holder_id"}}, DoUpdates: clause.AssignmentColumns([]string{"payload_json", "status", "fetched_at"})}).Create(&InstitutionalOwnershipReceipt{Ticker: ticker, HolderID: id, PayloadJSON: string(raw), Status: status, FetchedAt: now}).Error
}

func ownershipStatusMessage(status string) string {
	switch status {
	case "no_history":
		return "提供方暂无历史，不补零。"
	case "unconfirmed_type":
		return "机构类型未确认，暂不纳入机构趋势。"
	case "excluded_type":
		return "提供方分类为个人、内部人士或公司股东，分开展示，不计入机构趋势。"
	default:
		return "历史字段未能安全识别，原始披露已保留，不生成推测数据。"
	}
}

func loadInstitutionalOwnershipHistory(ctx context.Context, db *gorm.DB, result *TickerInstitutionalHoldingHistory) error {
	if err := db.WithContext(ctx).Where("provider = ? AND ticker = ?", "longbridge", result.Ticker).Order("holding_date DESC, provider_date DESC, holder_id ASC").Find(&result.OwnershipHistory).Error; err != nil {
		return err
	}
	// Detail classification overrides an empty/ambiguous title from top.
	var receipts []InstitutionalOwnershipReceipt
	if err := db.WithContext(ctx).Where("ticker = ? AND holder_id <> ?", result.Ticker, "top").Find(&receipts).Error; err != nil {
		return err
	}
	classified := map[string]string{}
	byName := map[string]string{}
	for _, receipt := range receipts {
		var p struct {
			OwnerType string `json:"owner_source"`
			Name      string `json:"name"`
		}
		_ = json.Unmarshal([]byte(receipt.PayloadJSON), &p)
		owner := classifyInstitutionType(p.OwnerType)
		if owner != "Unknown" {
			classified[receipt.HolderID] = owner
		}
		if p.Name != "" && owner != "Unknown" {
			byName[p.Name] = owner
		}
		if receipt.Status != "available" {
			status := receipt.Status
			if status == "unconfirmed_type" && owner != "Unknown" {
				status = "excluded_type"
			}
			result.HistoryWarnings = append(result.HistoryWarnings, fmt.Sprintf("股东 %s：%s", receipt.HolderID, ownershipStatusMessage(status)))
		}
	}
	filtered := []InstitutionalOwnershipPoint{}
	otherLatest := map[string]InstitutionalOwnershipPoint{}
	for _, point := range result.OwnershipHistory {
		if owner, ok := classified[point.HolderID]; ok {
			point.OwnerType = owner
		}
		if point.OwnerType == "Institution" {
			filtered = append(filtered, point)
		} else if _, exists := otherLatest[point.HolderID]; !exists {
			otherLatest[point.HolderID] = point
		}
	}
	result.OwnershipHistory = filtered
	all := append(append([]InstitutionalHolderSnapshot{}, result.InstitutionalHolders...), result.OtherHolders...)
	for i := range all {
		if owner, ok := classified[all[i].HolderID]; ok {
			all[i].OwnerType = owner
		} else if owner, ok := byName[all[i].HolderName]; ok {
			all[i].OwnerType = owner
		}
	}
	result.InstitutionalHolders, result.OtherHolders = splitInstitutionalHolders(all)
	seen := map[string]bool{}
	for _, row := range all {
		seen[row.HolderID] = true
		seen[row.HolderName] = true
	}
	for _, point := range otherLatest {
		if seen[point.HolderID] || seen[point.HolderName] {
			continue
		}
		result.OtherHolders = append(result.OtherHolders, InstitutionalHolderSnapshot{ID: point.ID, Provider: point.Provider, Ticker: point.Ticker, HolderID: point.HolderID, HolderName: point.HolderName, OwnerType: point.OwnerType, InstitutionType: point.OwnerType, PercentOfShares: point.PercentOfShares, ReportDate: point.ProviderDate, SourceURL: point.SourceURL, FetchedAt: point.FetchedAt})
	}
	sort.SliceStable(result.OtherHolders, func(i, j int) bool { return result.OtherHolders[i].HolderName < result.OtherHolders[j].HolderName })
	return nil
}
