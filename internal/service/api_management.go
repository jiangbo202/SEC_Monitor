package service

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"
	"sec_monitor/internal/config"
	"sec_monitor/internal/discovery"
	"sec_monitor/internal/model"
)

type APIManagementService struct {
	db, main *gorm.DB
	configs  *ConfigService
	tasks    *TaskConfigService
	cfg      config.DiscoveryConfig
	Futu     *FutuAPIService
	Monitor  *discovery.APIMonitor
}

func NewAPIManagementService(db, main *gorm.DB, configs *ConfigService, tasks *TaskConfigService, cfg config.DiscoveryConfig, m *discovery.APIMonitor) *APIManagementService {
	return &APIManagementService{db: db, main: main, configs: configs, tasks: tasks, cfg: cfg, Futu: NewFutuAPIService(db, configs, m), Monitor: m}
}

type APIProviderSummary struct {
	Policy               discovery.APIProviderPolicy `json:"policy"`
	CredentialConfigured bool                        `json:"credential_configured"`
	Authorization        string                      `json:"authorization"`
	Requests             int64                       `json:"requests"`
	Failures             int64                       `json:"failures"`
	RateLimited          int64                       `json:"rate_limited"`
	Pending              int64                       `json:"pending"`
	AverageMS            float64                     `json:"average_ms"`
	LastSuccessAt        *time.Time                  `json:"last_success_at,omitempty"`
	VendorQuota          *int                        `json:"vendor_quota"`
}
type APICapability struct {
	Key          string `json:"key"`
	Label        string `json:"label"`
	Provider     string `json:"provider"`
	AutoEnabled  bool   `json:"auto_enabled"`
	IssuerBudget int    `json:"issuer_budget"`
	TTLHours     int    `json:"ttl_hours"`
	MetricScope  string `json:"metric_scope"`
	ConfigKey    string `json:"config_key,omitempty"`
	Implemented  bool   `json:"implemented"`
}
type APITrend struct {
	Provider    string `json:"provider"`
	Day         string `json:"day"`
	Requests    int64  `json:"requests"`
	Failures    int64  `json:"failures"`
	RateLimited int64  `json:"rate_limited"`
}
type APICoverageCell struct {
	Ticker     string     `json:"ticker"`
	Capability string     `json:"capability"`
	Status     string     `json:"status"`
	SyncedAt   *time.Time `json:"synced_at,omitempty"`
	CheckedAt  *time.Time `json:"checked_at,omitempty"`
	SnapshotAt *time.Time `json:"snapshot_at,omitempty"`
	TTLHours   int        `json:"ttl_hours"`
}
type APIManagementOverview struct {
	GeneratedAt  time.Time                 `json:"generated_at"`
	WindowStart  time.Time                 `json:"window_start"`
	TimeZone     string                    `json:"time_zone"`
	Providers    []APIProviderSummary      `json:"providers"`
	Capabilities []APICapability           `json:"capabilities"`
	Trends       []APITrend                `json:"trends"`
	Calls        []discovery.APICallRecord `json:"calls"`
	Coverage     []APICoverageCell         `json:"coverage"`
	Tasks        []model.TaskConfig        `json:"tasks"`
	Notice       string                    `json:"notice"`
	Modules      []APIModuleView           `json:"modules"`
	PriceRoute   APIPriceRoute             `json:"price_route"`
}

func (s *APIManagementService) Overview(ctx context.Context, provider, ticker string, failuresOnly bool) (APIManagementOverview, error) {
	now := time.Now().UTC()
	loc := time.FixedZone("HK", 8*3600)
	local := now.In(loc)
	start := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc).AddDate(0, 0, -6).UTC()
	result := APIManagementOverview{GeneratedAt: now, WindowStart: start, TimeZone: "Asia/Hong_Kong", Providers: []APIProviderSummary{}, Trends: []APITrend{}, Calls: []discovery.APICallRecord{}, Coverage: []APICoverageCell{}, Tasks: []model.TaskConfig{}, Notice: "调用记录自本功能启用起采集，历史不回填；统计 Longbridge HTTP 请求尝试与行情/期权 WS 数据命令、Futu 数据 HTTP。HTTP 分页与重试分别计数；WS SDK 内部重连/握手不计。OAuth 授权/续期不计行情调用。预算在派发前预留，未完成不算成功；本地预算不是供应商配额，剩余额度未知。"}
	if provider != "" && provider != "longbridge" && provider != "futu" {
		return result, ErrValidation
	}
	if ticker != "" && !futuTickerPattern.MatchString(strings.ToUpper(ticker)) {
		return result, ErrValidation
	}
	cfg, err := s.configs.ApplyDiscoveryConfig(ctx, s.cfg)
	if err != nil {
		return result, err
	}
	var policies []discovery.APIProviderPolicy
	if err := s.db.WithContext(ctx).Order("provider ASC").Find(&policies).Error; err != nil {
		return result, err
	}
	for _, policy := range policies {
		summary := APIProviderSummary{Policy: policy, Authorization: "not_configured"}
		if policy.BudgetDate != now.In(loc).Format(time.DateOnly) {
			summary.Policy.DailyUsed = 0
		}
		if policy.Provider == "longbridge" {
			summary.CredentialConfigured = cfg.LongbridgeAppKey != "" && cfg.LongbridgeAppSecret != "" && cfg.LongbridgeAccessToken != ""
			if summary.CredentialConfigured {
				summary.Authorization = "configured_unverified"
			}
		}
		if policy.Provider == "futu" {
			ok, err := s.Futu.Configured(ctx)
			if err != nil {
				return result, err
			}
			summary.CredentialConfigured = ok
			if ok {
				summary.Authorization = "quote_read_authorized"
				credentials, err := s.Futu.Credentials(ctx)
				if err != nil {
					return result, err
				}
				if credentials.Mode == "api_key" {
					summary.Authorization = "api_key_configured_unverified"
				}
			}
		}
		q := s.db.WithContext(ctx).Model(&discovery.APICallRecord{}).Where("provider = ? AND started_at >= ?", policy.Provider, start)
		var counts struct {
			Requests    int64
			Failures    int64
			RateLimited int64
			Pending     int64
			AverageMS   float64
		}
		if err := q.Select("COUNT(*) as requests, COALESCE(SUM(CASE WHEN status = 'started' THEN 1 ELSE 0 END),0) as pending, COALESCE(SUM(CASE WHEN status = 'failed' THEN 1 ELSE 0 END),0) as failures, COALESCE(SUM(CASE WHEN error_kind = 'rate_limited' THEN 1 ELSE 0 END),0) as rate_limited, COALESCE(AVG(CASE WHEN status != 'started' THEN elapsed_ms END),0) as average_ms").Scan(&counts).Error; err != nil {
			return result, err
		}
		summary.Requests, summary.Failures, summary.RateLimited, summary.AverageMS = counts.Requests, counts.Failures, counts.RateLimited, counts.AverageMS
		summary.Pending = counts.Pending
		var latest discovery.APICallRecord
		err := s.db.WithContext(ctx).Where("provider = ? AND status IN ?", policy.Provider, []string{"success", "no_coverage"}).Order("started_at DESC").First(&latest).Error
		if err == nil {
			summary.LastSuccessAt = &latest.StartedAt
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return result, err
		}
		result.Providers = append(result.Providers, summary)
	}
	trend := s.db.WithContext(ctx).Model(&discovery.APICallRecord{}).Where("started_at >= ?", start)
	if provider != "" {
		trend = trend.Where("provider = ?", provider)
	}
	if err := trend.Select("provider, DATE(started_at, '+8 hours') as day, COUNT(*) as requests, SUM(CASE WHEN status = 'failed' THEN 1 ELSE 0 END) as failures, SUM(CASE WHEN error_kind = 'rate_limited' THEN 1 ELSE 0 END) as rate_limited").Group("provider, DATE(started_at, '+8 hours')").Order("day ASC, provider ASC").Scan(&result.Trends).Error; err != nil {
		return result, err
	}
	calls := s.db.WithContext(ctx).Where("started_at >= ?", start)
	if provider != "" {
		calls = calls.Where("provider = ?", provider)
	}
	if ticker != "" {
		calls = calls.Where("ticker = ?", strings.ToUpper(ticker))
	}
	if failuresOnly {
		calls = calls.Where("status = ?", "failed")
	}
	if err := calls.Order("id DESC").Limit(100).Find(&result.Calls).Error; err != nil {
		return result, err
	}
	result.Capabilities = []APICapability{
		{Key: "company", Label: "公司资料", Provider: "longbridge", AutoEnabled: cfg.LongbridgeCompanyProfileEnabled, IssuerBudget: cfg.LongbridgeCompanyProfileRequestBudget, TTLHours: cfg.LongbridgeCompanyProfileTTLDays * 24, ConfigKey: "discovery.longbridge_company_profile_enabled", Implemented: true},
		{Key: "analyst", Label: "分析师共识", Provider: "longbridge", AutoEnabled: cfg.LongbridgeAnalystRatingEnabled, IssuerBudget: cfg.LongbridgeAnalystRatingRequestBudget, TTLHours: 7 * 24, ConfigKey: "discovery.longbridge_analyst_rating_enabled", Implemented: true},
		{Key: "eps", Label: "候选 EPS / 异动 / 股东快照", Provider: "longbridge", AutoEnabled: cfg.LongbridgeCandidateResearchEnabled, IssuerBudget: cfg.LongbridgeCandidateResearchRequestBudget, TTLHours: 7 * 24, ConfigKey: "discovery.longbridge_candidate_research_enabled", Implemented: true},
		{Key: "watch_research", Label: "监控 EPS / 异动 / 股东快照", Provider: "longbridge", AutoEnabled: cfg.LongbridgeWatchTargetResearchEnabled, IssuerBudget: cfg.LongbridgeWatchTargetResearchRequestBudget, TTLHours: 7 * 24, ConfigKey: "discovery.longbridge_watch_target_research_enabled", Implemented: true},
		{Key: "valuation", Label: "候选估值", Provider: "longbridge", AutoEnabled: cfg.LongbridgeCandidateValuationEnabled, IssuerBudget: cfg.LongbridgeCandidateValuationRequestBudget, TTLHours: 7 * 24, ConfigKey: "discovery.longbridge_candidate_valuation_enabled", Implemented: true},
		{Key: "watch_valuation", Label: "监控估值", Provider: "longbridge", AutoEnabled: cfg.LongbridgeWatchTargetValuationEnabled, IssuerBudget: cfg.LongbridgeWatchTargetValuationRequestBudget, TTLHours: 7 * 24, ConfigKey: "discovery.longbridge_watch_target_valuation_enabled", Implemented: true},
		{Key: "ownership", Label: "单家主要机构历史", Provider: "longbridge", AutoEnabled: cfg.LongbridgeCandidateResearchEnabled || cfg.LongbridgeWatchTargetResearchEnabled, IssuerBudget: 4, TTLHours: 24, MetricScope: "部分主要机构；不可相加推算机构合计", Implemented: true},
		{Key: "aggregate", Label: "机构合计历史", Provider: "futu", TTLHours: 24, IssuerBudget: 4, MetricScope: "富途报告期合计口径；不替换单家机构记录", Implemented: true},
		{Key: "price", Label: "行情 / 日线", Provider: "longbridge", AutoEnabled: strings.Contains(cfg.PriceProvider, "longbridge") || cfg.PriceProvider == "", MetricScope: "现有供应商链；Futu 行情备源尚未接入", Implemented: true},
		{Key: "price_futu", Label: "行情备源", Provider: "futu", MetricScope: "未实现，不能启用或回退", Implemented: false},
		{Key: "trading", Label: "实盘交易", Provider: "futu", MetricScope: "不申请交易权限，不提供下单入口", Implemented: false},
	}
	result.Capabilities = append(result.Capabilities,
		APICapability{Key: "options", Label: "候选 / 监控期权研究", Provider: "longbridge", AutoEnabled: cfg.LongbridgeOptionResearchEnabled, ConfigKey: "discovery.longbridge_option_research_enabled", Implemented: true})
	for i := range result.Capabilities {
		cap := &result.Capabilities[i]
		if cap.Key == "company" || cap.Key == "analyst" {
			cap.Provider = discovery.APIModuleProvider(ctx, s.db, cap.Key)
		}
		if cap.Key == "price_futu" {
			cap.Implemented = true
			cap.Label = "美股行情日线"
			cap.MetricScope = "不复权日线；通过价格链选择主源 / 备源，不接管大盘趋势"
			cap.AutoEnabled = strings.Contains(cfg.PriceProvider, "futu")
		}
		if cap.Key == "price" {
			cap.MetricScope = "候选 / 监控日线价格链，支持 Longbridge / Futu 顺序回退；大盘趋势仍为 Longbridge"
		}
	}
	result.Modules, err = s.ModuleViews(ctx, result.Providers)
	if err != nil {
		return result, err
	}
	result.PriceRoute = PriceRouteView(cfg)
	tasks, err := s.tasks.List(ctx)
	if err != nil {
		return result, err
	}
	linkedTasks := map[string]bool{}
	for _, module := range discovery.APIModuleDefinitions() {
		for _, name := range module.TaskNames {
			linkedTasks[name] = true
		}
	}
	for _, task := range tasks {
		if linkedTasks[task.TaskName] || strings.HasPrefix(task.TaskName, "longbridge_") || strings.HasPrefix(task.TaskName, "futu_") {
			task.LastErrorMessage = SanitizeSensitiveError(task.LastErrorMessage)
			result.Tasks = append(result.Tasks, task)
		}
	}
	tickers := []string{}
	if ticker != "" {
		tickers = append(tickers, strings.ToUpper(ticker))
	} else {
		var targets []model.WatchTarget
		if err := s.main.WithContext(ctx).Where("status = ? AND target_type = ?", "enabled", "stock").Order("ticker ASC").Limit(20).Find(&targets).Error; err != nil {
			return result, err
		}
		seen := map[string]bool{}
		for _, target := range targets {
			t := strings.ToUpper(strings.TrimSpace(target.Ticker))
			if !seen[t] {
				tickers = append(tickers, t)
				seen[t] = true
			}
		}
		candidates, err := discovery.OwnershipCandidateTickers(ctx, s.db, 20, map[string]time.Time{})
		if err != nil {
			return result, err
		}
		for _, t := range candidates {
			if len(tickers) >= 25 {
				break
			}
			if !seen[t] {
				tickers = append(tickers, t)
				seen[t] = true
			}
		}
	}
	type spec struct {
		key, table, column, condition string
		ttl                           time.Duration
	}
	specs := []spec{{"company", "company_profile_snapshots", "fetched_at", "provider = 'longbridge'", time.Duration(cfg.LongbridgeCompanyProfileTTLDays) * 24 * time.Hour}, {"analyst", "analyst_rating_snapshots", "fetched_at", "provider = 'longbridge'", 7 * 24 * time.Hour}, {"eps", "eps_forecast_snapshots", "fetched_at", "provider = 'longbridge'", 7 * 24 * time.Hour}, {"valuation", "longbridge_valuation_snapshots", "fetched_at", "provider = 'longbridge'", 7 * 24 * time.Hour}, {"ownership", "institutional_ownership_points", "fetched_at", "owner_type = 'Institution' AND provider = 'longbridge'", 7 * 24 * time.Hour}, {"aggregate", "futu_institutional_receipts", "fetched_at", "", 7 * 24 * time.Hour}}
	for _, spec := range specs {
		if spec.key == "company" || spec.key == "analyst" {
			spec.condition = "provider = '" + discovery.APIModuleProvider(ctx, s.db, spec.key) + "'"
		}
		if spec.key == "analyst" || spec.key == "eps" || spec.key == "valuation" {
			spec.condition += " AND (ticker NOT IN ('STI','SPCX') OR identity_counter_id = 'ST/US/' || ticker)"
		}
		latest := map[string]time.Time{}
		coverageStatus := map[string]string{}
		if len(tickers) > 0 && s.db.Migrator().HasTable(spec.table) {
			var rows []struct {
				Ticker   string
				SyncedAt time.Time
			}
			q := s.db.WithContext(ctx).Table(spec.table).Where("ticker IN ? AND fetched_at IS NOT NULL", tickers)
			if spec.condition != "" {
				q = q.Where(spec.condition)
			}
			// Scan original timestamps, but return only the latest local row per ticker.
			sub := s.db.Table(spec.table).Select("ticker, MAX(fetched_at) AS last_at").Where("ticker IN ?", tickers)
			if spec.condition != "" {
				sub = sub.Where(spec.condition)
			}
			sub = sub.Group("ticker")
			q = q.Where("(ticker, fetched_at) IN (?)", sub).Select("ticker, " + spec.column + " as synced_at").Distinct()
			if err := q.Scan(&rows).Error; err != nil {
				return result, err
			}
			for _, row := range rows {
				latest[row.Ticker] = row.SyncedAt
			}
			if spec.key == "analyst" {
				var ratings []discovery.AnalystRatingSnapshot
				if err := s.db.WithContext(ctx).Where("(ticker, fetched_at) IN (?)", sub).Find(&ratings).Error; err != nil {
					return result, err
				}
				for _, r := range ratings {
					coverageStatus[r.Ticker] = r.Status
				}
			}
			if spec.key == "aggregate" {
				var receipts []discovery.FutuInstitutionalReceipt
				if err := s.db.WithContext(ctx).Where("ticker IN ?", tickers).Find(&receipts).Error; err != nil {
					return result, err
				}
				for _, r := range receipts {
					coverageStatus[r.Ticker] = r.Status
				}
			}
		}
		provider := "longbridge"
		if spec.key == "company" || spec.key == "analyst" {
			provider = discovery.APIModuleProvider(ctx, s.db, spec.key)
		} else if spec.key == "aggregate" {
			provider = "futu"
		}
		receipts, err := discovery.APIDataSyncReceipts(ctx, s.db, provider, spec.key)
		if err != nil {
			return result, err
		}
		// Existing P2 cursors prove all valuation endpoints completed and were
		// saved/validated even when the immutable content hash did not change.
		// P1 success is not sufficient evidence of EPS success, so never use it.
		legacy := map[string]discovery.LongbridgeResearchRefreshState{}
		if (spec.key == "valuation" || spec.key == "eps") && s.db.Migrator().HasTable(&discovery.LongbridgeResearchRefreshState{}) {
			family := discovery.LongbridgeRefreshFamilyValuation
			if spec.key == "eps" {
				family = "eps_no_coverage"
			}
			var rows []discovery.LongbridgeResearchRefreshState
			if err := s.db.WithContext(ctx).Where("family = ? AND ticker IN ? AND last_success_at IS NOT NULL", family, tickers).Find(&rows).Error; err != nil {
				return result, err
			}
			for _, row := range rows {
				// Legacy cursors did not record verified issuer identity. Never let
				// one make the reused STI/SPCX ticker's historical cache look fresh.
				if row.Ticker == "STI" || row.Ticker == "SPCX" {
					continue
				}
				if spec.key == "eps" {
					row.Status = "no_coverage"
				} else {
					row.Status = "available"
				}
				legacy[row.Ticker] = row
			}
		}
		for _, t := range tickers {
			cell := APICoverageCell{Ticker: t, Capability: spec.key, Status: "not_synced", TTLHours: int(spec.ttl / time.Hour)}
			if timestamp, ok := latest[t]; ok {
				cell.SyncedAt = &timestamp
				cell.SnapshotAt = &timestamp
				cell.Status = "available"
				if now.Sub(timestamp) > spec.ttl {
					cell.Status = "stale"
				}
				if status := coverageStatus[t]; status == "no_coverage" {
					cell.Status = status
				} else if status == "partial" {
					cell.Status = status
				}
			}
			receipt, ok := receipts[t]
			if old, exists := legacy[t]; exists && (!ok || old.LastSuccessAt.After(*receipt.LastSuccessAt)) {
				if spec.key == "eps" || cell.SnapshotAt != nil {
					receipt, ok = old, true
				}
			}
			if ok && (cell.SnapshotAt == nil || !receipt.LastSuccessAt.Before(*cell.SnapshotAt)) {
				cell.CheckedAt, cell.SyncedAt = receipt.LastSuccessAt, receipt.LastSuccessAt
				cell.Status = receipt.Status
				if cell.Status == "available" && now.Sub(*receipt.LastSuccessAt) > spec.ttl {
					cell.Status = "stale"
				}
			}
			result.Coverage = append(result.Coverage, cell)
		}
	}
	return result, nil
}

func (s *APIManagementService) UpdatePolicy(ctx context.Context, provider string, input discovery.APIProviderPolicy, operator string) error {
	if provider != "longbridge" && provider != "futu" {
		return ErrValidation
	}
	if input.DailyBudget < 0 || input.DailyBudget > 100000 || input.MinIntervalMS < 100 || input.MinIntervalMS > 60000 {
		return ErrValidation
	}
	if provider == "futu" && !input.Paused {
		ok, err := s.Futu.Configured(ctx)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("%w: 请先完成 Futu 只读授权", ErrValidation)
		}
	}
	if err := s.db.WithContext(ctx).Model(&discovery.APIProviderPolicy{}).Where("provider = ?", provider).Updates(map[string]any{"paused": input.Paused, "daily_budget": input.DailyBudget, "min_interval_ms": input.MinIntervalMS}).Error; err != nil {
		return err
	}
	return NewAuditService(s.main).Record(ctx, operator, "update", "api_provider_policy", provider, nil, map[string]any{"paused": input.Paused, "daily_budget": input.DailyBudget, "min_interval_ms": input.MinIntervalMS})
}
func (s *APIManagementService) SetCapability(ctx context.Context, key string, enabled bool, operator string) error {
	keys := map[string]string{"company": "discovery.longbridge_company_profile_enabled", "analyst": "discovery.longbridge_analyst_rating_enabled", "eps": "discovery.longbridge_candidate_research_enabled", "watch_research": "discovery.longbridge_watch_target_research_enabled", "valuation": "discovery.longbridge_candidate_valuation_enabled", "watch_valuation": "discovery.longbridge_watch_target_valuation_enabled", "options": "discovery.longbridge_option_research_enabled"}
	configKey, ok := keys[key]
	if !ok {
		return ErrValidation
	}
	return s.configs.UpsertMany(ctx, []ConfigInput{{Key: configKey, Value: fmt.Sprint(enabled), ValueType: "bool", Category: "discovery"}}, operator)
}

func (s *APIManagementService) SyncFutuOwnership(ctx context.Context) error {
	var policy discovery.APIProviderPolicy
	if err := s.db.WithContext(ctx).First(&policy, "provider = ?", "futu").Error; err != nil {
		return err
	}
	if policy.Paused {
		return SkipTask("Futu 已暂停，不查询外部数据")
	}
	ok, err := s.Futu.Configured(ctx)
	if err != nil {
		return err
	}
	if !ok {
		return SkipTask("Futu 尚未完成只读授权")
	}
	last := map[string]time.Time{}
	if err := discovery.MergeLongbridgeResearchAttempts(ctx, s.db, "futu_ownership", last); err != nil {
		return err
	}
	var targets []model.WatchTarget
	if err := s.main.WithContext(ctx).Where("status = ? AND target_type = ?", "enabled", "stock").Find(&targets).Error; err != nil {
		return err
	}
	sort.Slice(targets, func(i, j int) bool {
		if last[targets[i].Ticker].Equal(last[targets[j].Ticker]) {
			return targets[i].Ticker < targets[j].Ticker
		}
		return last[targets[i].Ticker].Before(last[targets[j].Ticker])
	})
	tickers := []string{}
	for _, target := range targets {
		if time.Since(last[target.Ticker]) >= 24*time.Hour {
			tickers = append(tickers, target.Ticker)
			if len(tickers) >= 2 {
				break
			}
		}
	}
	candidates, err := discovery.OwnershipCandidateTickers(ctx, s.db, 2, last)
	if err != nil {
		return err
	}
	tickers = append(tickers, candidates...)
	seen := map[string]bool{}
	failed := 0
	ctx, cancel := context.WithTimeout(ctx, 4*time.Minute)
	defer cancel()
	for _, ticker := range tickers {
		if seen[ticker] || time.Since(last[ticker]) < 24*time.Hour {
			continue
		}
		seen[ticker] = true
		if err := discovery.MarkLongbridgeResearchAttempt(ctx, s.db, "futu_ownership", ticker, time.Now().UTC()); err != nil {
			return err
		}
		if _, err := s.Futu.RefreshOwnership(ctx, ticker); err != nil {
			failed++
			continue
		}
		if err := discovery.MarkLongbridgeResearchSuccess(ctx, s.db, "futu_ownership", ticker, time.Now().UTC()); err != nil {
			return err
		}
	}
	if failed > 0 {
		return PartialTask(fmt.Sprintf("%d 个 Futu 标的查询失败，已保存其余记录，等待下次轮转", failed))
	}
	if len(seen) == 0 {
		return SkipTask("Futu 轮转队列在 24 小时内已尝试")
	}
	return nil
}
