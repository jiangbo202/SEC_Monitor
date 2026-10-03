package discovery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"math"
	"net/http"
	"net/url"
	"sec_monitor/internal/config"
	"strings"
	"time"
)

func fetchFutuCompany(ctx context.Context, cfg config.DiscoveryConfig, ticker string) (LongbridgeCompanyOverview, error) {
	var result LongbridgeCompanyOverview
	ticker = strings.ToUpper(strings.TrimSpace(ticker))
	if !futuUSSymbolPattern.MatchString(ticker) || cfg.FutuReadJSON == nil {
		return result, errors.New("Futu 未配置或美股代码无效")
	}
	var payload struct {
		Data struct {
			Items []struct {
				Value         string `json:"value"`
				AttributeType int    `json:"attribute_type"`
			} `json:"items"`
		} `json:"data"`
	}
	if err := cfg.FutuReadJSON(ctx, http.MethodGet, "/api/v1.0/quote/US."+ticker+"/company/profile", url.Values{}, nil, &payload); err != nil {
		return result, err
	}
	fields := map[int]string{}
	for _, item := range payload.Data.Items {
		fields[item.AttributeType] = strings.TrimSpace(item.Value)
	}
	if symbol := fields[7]; symbol != "" && symbol != ticker && symbol != "US."+ticker {
		return result, errors.New("Futu 公司资料代码与请求不一致")
	}
	result = LongbridgeCompanyOverview{CompanyName: fields[9], Profile: fields[29], Website: fields[23], Founded: fields[13], ListingDate: fields[10], Market: fields[14], Address: fields[19], Employees: fields[18], Manager: fields[30], YearEnd: fields[17]}
	if result.Profile == "" {
		result.Profile = fields[35]
	}
	if result.CompanyName == "" || result.Profile == "" {
		return result, errors.New("Futu 暂无完整公司资料；保留旧缓存与 SEC 资料")
	}
	return result, nil
}
func refreshFutuCompany(ctx context.Context, db *gorm.DB, cfg config.DiscoveryConfig, ticker, cik string, force bool) (CompanyProfileRefreshResult, error) {
	result := CompanyProfileRefreshResult{Ticker: strings.ToUpper(strings.TrimSpace(ticker))}
	security, listing, err := resolveCompanyProfileSecurity(ctx, db, result.Ticker, cik)
	if err != nil {
		return result, err
	}
	result.Ticker = strings.ToUpper(listing.Ticker)
	var cached CompanyProfileSnapshot
	err = db.WithContext(ctx).Where("provider = ? AND security_id = ?", "futu", security.ID).First(&cached).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return result, err
	}
	now := time.Now().UTC()
	if !force && companyProfileSnapshotFresh(cached, cfg.LongbridgeCompanyProfileTTLDays, now) {
		result.Cached = true
		result.FetchedAt = cached.FetchedAt
		result.Message = "使用 Futu 本地公司资料缓存"
		return result, nil
	}
	if err := CheckSelectedAPIModule(ctx, db, "company"); err != nil {
		return result, err
	}
	overview, err := fetchFutuCompany(ctx, cfg, result.Ticker)
	if err != nil {
		return result, err
	}
	snapshot := CompanyProfileSnapshot{SecurityID: security.ID, Provider: "futu", Ticker: result.Ticker, CompanyName: overview.CompanyName, Profile: overview.Profile, Website: overview.Website, Founded: overview.Founded, ListingDate: overview.ListingDate, Market: overview.Market, Address: overview.Address, Employees: overview.Employees, Manager: overview.Manager, YearEnd: overview.YearEnd, FetchedAt: &now, LastAttemptAt: &now}
	err = db.WithContext(ctx).Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "provider"}, {Name: "security_id"}}, DoUpdates: clause.AssignmentColumns([]string{"ticker", "company_name", "profile", "website", "founded", "listing_date", "market", "address", "employees", "manager", "year_end", "fetched_at", "last_attempt_at", "updated_at"})}).Create(&snapshot).Error
	result.Fetched = err == nil
	result.FetchedAt = &now
	result.Message = "已保存 Futu 公司资料；SEC 身份保持不变"
	return result, err
}
func refreshFutuAnalyst(ctx context.Context, db *gorm.DB, cfg config.DiscoveryConfig, ticker, cik string) (AnalystRatingRefreshResult, error) {
	result := AnalystRatingRefreshResult{Ticker: normalizeAnalystRatingTicker(ticker)}
	if !futuUSSymbolPattern.MatchString(result.Ticker) || cfg.FutuReadJSON == nil {
		return result, errors.New("Futu 未配置或美股代码无效")
	}
	if err := CheckSelectedAPIModule(ctx, db, "analyst"); err != nil {
		return result, err
	}
	var payload struct {
		Data struct {
			Rating        int      `json:"rating"`
			Total         int      `json:"total"`
			TargetCount   int      `json:"num_of_target_analysts"`
			Average       *float64 `json:"average"`
			Highest       *float64 `json:"highest"`
			Lowest        *float64 `json:"lowest"`
			StrongBuy     *float64 `json:"strong_buy"`
			Buy           *float64 `json:"buy"`
			Hold          *float64 `json:"hold"`
			Underperform  *float64 `json:"underperform"`
			Sell          *float64 `json:"sell"`
			UpdateTimeStr string   `json:"update_time_str"`
		} `json:"data"`
	}
	if err := cfg.FutuReadJSON(ctx, http.MethodGet, "/api/v1.0/quote/US."+result.Ticker+"/research/analyst-consensus", url.Values{}, nil, &payload); err != nil {
		return result, err
	}
	d := payload.Data
	if d.Total < 0 || d.Total > 10000 || d.TargetCount < 0 || d.TargetCount > 10000 {
		return result, errors.New("Futu 分析师数量无效")
	}
	for _, v := range []*float64{d.StrongBuy, d.Buy, d.Hold, d.Underperform, d.Sell} {
		if v != nil && (math.IsNaN(*v) || math.IsInf(*v, 0) || *v < 0 || *v > 100) {
			return result, errors.New("Futu 评级比例无效")
		}
	}
	target := func(v *float64) (int64, error) {
		if v == nil {
			return 0, nil
		}
		if math.IsNaN(*v) || math.IsInf(*v, 0) || *v < 0 || *v > 1e9 {
			return 0, errors.New("Futu 目标价无效")
		}
		return int64(math.Round(*v * 1e6)), nil
	}
	avg, err := target(d.Average)
	if err != nil {
		return result, err
	}
	high, err := target(d.Highest)
	if err != nil {
		return result, err
	}
	low, err := target(d.Lowest)
	if err != nil {
		return result, err
	}
	if high > 0 && low > high {
		return result, errors.New("Futu 目标价区间无效")
	}
	recommendations := []string{"", "sell", "underperform", "hold", "buy", "strong_buy"}
	status := AnalystRatingStatusAvailable
	if d.Total == 0 {
		status = AnalystRatingStatusNoCoverage
	} else if d.Rating < 1 || d.Rating > 5 {
		return result, errors.New("Futu 共识评级无效")
	}
	raw, _ := json.Marshal(d)
	hash := sha256.Sum256(raw)
	snapshot := AnalystRatingSnapshot{Provider: "futu", Ticker: result.Ticker, SecurityID: analystRatingSecurityID(ctx, db, result.Ticker, cik), IdentityCounterID: issuerCounterID(result.Ticker), Status: status, AnalystCount: d.Total, TargetAnalystCount: d.TargetCount, TargetAverageMicros: avg, TargetHighMicros: high, TargetLowMicros: low, StrongBuyPct: d.StrongBuy, BuyPct: d.Buy, HoldPct: d.Hold, UnderperformPct: d.Underperform, SellPct: d.Sell, ProviderUpdatedAtText: d.UpdateTimeStr, SnapshotHash: hex.EncodeToString(hash[:]), FetchedAt: time.Now().UTC(), NotificationStatus: "not_applicable"}
	if d.Rating >= 1 && d.Rating <= 5 {
		snapshot.Recommendation = recommendations[d.Rating]
	}
	previous, err := latestAnalystRatingSnapshot(ctx, db, "futu", result.Ticker)
	if err != nil {
		return result, err
	}
	if previous != nil && previous.SnapshotHash == snapshot.SnapshotHash {
		result.Cached = true
		result.Snapshot = *previous
		return result, RecordAPIDataSync(ctx, db, "futu", "analyst", snapshot.Ticker, snapshot.Status, snapshot.FetchedAt)
	}
	err = db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&snapshot).Error
	if err == nil {
		err = RecordAPIDataSync(ctx, db, "futu", "analyst", snapshot.Ticker, snapshot.Status, snapshot.FetchedAt)
	}
	result.Fetched = err == nil
	result.Snapshot = snapshot
	result.Message = "已保存 Futu 近三个月共识；评级为比例，不推算人数；目标价为提供方报告币种，不与美元现价推算空间"
	return result, err
}
