package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"sec_monitor/internal/discovery"
	"sec_monitor/internal/model"
	"sec_monitor/internal/service"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// DashboardSummary is a deliberately compact, local-snapshot-only response
// for the landing page. Keeping this aggregation server-side avoids the old
// dashboard fan-out (including a 500-row IPO-company request) and makes a
// partial local-data failure visible without blanking the complete page.
type DashboardSummary struct {
	GeneratedAt     time.Time                  `json:"generated_at"`
	Warnings        []string                   `json:"warnings"`
	Preferences     DashboardPreferences       `json:"preferences"`
	Decision        DashboardDecisionSummary   `json:"decision"`
	Monitoring      DashboardMonitoringSummary `json:"monitoring"`
	Operations      DashboardOperationsSummary `json:"operations"`
	candidateHealth *discovery.CandidateHealth
}

// DashboardPreferences remains in the existing local system-config store.
// It is intentionally small and user-controlled: hidden widgets never alter
// collection or retention of the underlying research data.
type DashboardPreferences struct {
	HiddenModules []string `json:"hidden_modules"`
}

type DashboardDecisionSummary struct {
	Market       DashboardMarketSummary         `json:"market"`
	Readiness    DashboardDecisionReadiness     `json:"readiness"`
	Availability DashboardCandidateAvailability `json:"availability"`
	Actions      []DashboardCandidateAction     `json:"actions"`
	Calendar     []DashboardCalendarItem        `json:"calendar"`
	ReviewDue    DashboardReviewDueSummary      `json:"review_due"`
}

type DashboardDecisionReadiness struct {
	Status               string                           `json:"status"`
	Label                string                           `json:"label"`
	ResearchUsable       bool                             `json:"research_usable"`
	NewTradePlanAllowed  bool                             `json:"new_trade_plan_allowed"`
	AsOf                 string                           `json:"as_of,omitempty"`
	ExpectedTradeDate    string                           `json:"expected_trade_date,omitempty"`
	EffectivenessStatus  string                           `json:"effectiveness_status"`
	EffectivenessVersion string                           `json:"effectiveness_version,omitempty"`
	Reasons              []DashboardDecisionReadinessItem `json:"reasons"`
}

type DashboardDecisionReadinessItem struct {
	Key      string `json:"key"`
	Severity string `json:"severity"`
	Title    string `json:"title"`
	Detail   string `json:"detail"`
	Action   string `json:"action,omitempty"`
}

type DashboardMarketSummary struct {
	Market      []DashboardMarketSeries    `json:"market"`
	Sectors     []DashboardMarketSeries    `json:"sectors"`
	Futures     []DashboardMarketSeries    `json:"futures"`
	Temperature *service.MarketTemperature `json:"temperature,omitempty"`
	LastFetched *time.Time                 `json:"last_fetched_at,omitempty"`
	Freshness   DashboardDataFreshness     `json:"freshness"`
}

// DashboardDataFreshness makes a local market snapshot's age explicit. This
// is derived from persisted data only: opening the dashboard never probes a
// provider that may be degraded or consuming a limited quota.
type DashboardDataFreshness struct {
	Status            string `json:"status"`
	Detail            string `json:"detail"`
	AsOf              string `json:"as_of,omitempty"`
	ExpectedTradeDate string `json:"expected_trade_date,omitempty"`
	Source            string `json:"source,omitempty"`
	QualityStatus     string `json:"quality_status"`
	FallbackUsed      bool   `json:"fallback_used"`
}

// DashboardMarketSeries purposefully excludes OHLC/volume history. Charts
// belong to their dedicated pages; sending those arrays with every landing
// page refresh used to dominate the dashboard payload.
type DashboardMarketSeries struct {
	Symbol      string   `json:"symbol"`
	Label       string   `json:"label"`
	TradeDate   string   `json:"trade_date"`
	Close       float64  `json:"close"`
	Change1DPct *float64 `json:"change_1d_pct,omitempty"`
}

type DashboardCandidateAction struct {
	Ticker         string    `json:"ticker"`
	CompanyName    string    `json:"company_name,omitempty"`
	Status         string    `json:"status"`
	Priority       string    `json:"priority"`
	Tradability    string    `json:"tradability"`
	EntryTrigger   string    `json:"entry_trigger,omitempty"`
	Reason         string    `json:"reason,omitempty"`
	NextAction     string    `json:"next_action"`
	DueLabel       string    `json:"due_label"`
	EvidenceAsOf   string    `json:"evidence_as_of,omitempty"`
	CloseUSD       float64   `json:"close_usd,omitempty"`
	StopLossUSD    float64   `json:"stop_loss_usd,omitempty"`
	RiskPct        float64   `json:"risk_pct,omitempty"`
	TakeProfitLow  float64   `json:"take_profit_low_usd,omitempty"`
	TakeProfitHigh float64   `json:"take_profit_high_usd,omitempty"`
	Score          int       `json:"score,omitempty"`
	Grade          string    `json:"grade,omitempty"`
	Since          time.Time `json:"since"`
}

type DashboardCandidateAvailability struct {
	Total        int                                  `json:"total"`
	Eligible     int                                  `json:"eligible"`
	ResearchOnly int                                  `json:"research_only"`
	Blocked      int                                  `json:"blocked"`
	Usable       []DashboardCandidateAvailabilityItem `json:"usable"`
	Excluded     []DashboardCandidateAvailabilityItem `json:"excluded"`
}

type DashboardCandidateAvailabilityItem struct {
	Ticker         string  `json:"ticker"`
	CompanyName    string  `json:"company_name,omitempty"`
	Grade          string  `json:"grade,omitempty"`
	Score          int     `json:"score,omitempty"`
	Readiness      string  `json:"readiness"`
	PrimaryReason  string  `json:"primary_reason"`
	NextAction     string  `json:"next_action"`
	PriceFreshness string  `json:"price_freshness"`
	PriceTradeDate string  `json:"price_trade_date,omitempty"`
	CloseUSD       float64 `json:"close_usd,omitempty"`
	ReviewPriority int     `json:"review_priority,omitempty"`
}

type DashboardCalendarItem struct {
	Kind    string     `json:"kind"`
	Scope   string     `json:"scope"`
	Ticker  string     `json:"ticker,omitempty"`
	Title   string     `json:"title"`
	At      *time.Time `json:"at,omitempty"`
	Session string     `json:"session,omitempty"`
	Link    string     `json:"link,omitempty"`
}

type DashboardReviewDueSummary struct {
	Overdue  int `json:"overdue"`
	DueToday int `json:"due_today"`
	Upcoming int `json:"upcoming"`
}

type DashboardMonitoringSummary struct {
	WatchTargets           int64               `json:"watch_targets"`
	EnabledTargets         int64               `json:"enabled_targets"`
	RecentFilings          []DashboardFiling   `json:"recent_filings"`
	IPO                    DashboardIPOSummary `json:"ipo"`
	UpcomingEarnings       int                 `json:"upcoming_earnings"`
	EarningsCoverageStatus string              `json:"earnings_coverage_status"`
	EarningsCoveredTargets int                 `json:"earnings_covered_targets"`
	EarningsUnavailable    int                 `json:"earnings_unavailable"`
	EarningsLastFetchedAt  *time.Time          `json:"earnings_last_fetched_at,omitempty"`
}

type DashboardFiling struct {
	ID          uint      `json:"id"`
	Ticker      string    `json:"ticker"`
	CompanyName string    `json:"company_name"`
	FilingType  string    `json:"filing_type"`
	Title       string    `json:"title"`
	FiledAt     time.Time `json:"filed_at"`
}

type DashboardIPOSummary struct {
	InProgress    int                      `json:"in_progress"`
	Followed      []service.IPOCompanyItem `json:"followed"`
	FollowedTotal int                      `json:"followed_total"`
}

type DashboardOperationsSummary struct {
	Status                    string                          `json:"status"`
	CriticalIssues            []service.OperationalIssue      `json:"critical_issues"`
	Issues                    []service.OperationalIssue      `json:"issues"`
	Tasks                     []service.OperationalTaskStatus `json:"tasks"`
	FailedNotificationBatches int64                           `json:"failed_notification_batches"`
	DeadLetterBatches         int64                           `json:"dead_letter_batches"`
}

// Dashboard aggregation includes candidate research gates derived from a
// sizeable local price-history snapshot. Keep a short process-local cache so
// navigation and tab changes do not repeat that work. Explicit refreshes can
// bypass it, and the small TTL bounds staleness from background syncs.
const dashboardSummaryCacheTTL = 30 * time.Second

// GetDashboardSummary is read-only. It never refreshes SEC, Longbridge,
// Yahoo, or Telegram data; background tasks remain solely responsible for
// provider I/O and this endpoint reads their local snapshots.
func (h *AppHandler) GetDashboardSummary(c *gin.Context) {
	if h.DB == nil {
		Error(c, errors.New("dashboard database is not configured"))
		return
	}
	forceRefresh := c.Query("refresh") == "1" || strings.EqualFold(c.Query("refresh"), "true")
	h.dashboardCacheMu.Lock()
	defer h.dashboardCacheMu.Unlock()
	if !forceRefresh && !h.dashboardCacheAt.IsZero() && time.Since(h.dashboardCacheAt) < dashboardSummaryCacheTTL {
		c.Header("X-Dashboard-Cache", "hit")
		OK(c, h.dashboardCache)
		return
	}
	c.Header("X-Dashboard-Cache", "miss")
	ctx := c.Request.Context()
	now := time.Now().UTC()
	result := DashboardSummary{
		GeneratedAt: now,
		Warnings:    []string{},
		Decision: DashboardDecisionSummary{
			Actions:      []DashboardCandidateAction{},
			Calendar:     []DashboardCalendarItem{},
			Availability: DashboardCandidateAvailability{Usable: []DashboardCandidateAvailabilityItem{}, Excluded: []DashboardCandidateAvailabilityItem{}},
		},
		Monitoring: DashboardMonitoringSummary{RecentFilings: []DashboardFiling{}, IPO: DashboardIPOSummary{Followed: []service.IPOCompanyItem{}}},
		Operations: DashboardOperationsSummary{CriticalIssues: []service.OperationalIssue{}, Issues: []service.OperationalIssue{}, Tasks: []service.OperationalTaskStatus{}},
	}
	if h.Configs != nil {
		if value, found, err := h.Configs.GetValue(ctx, "ui.dashboard_hidden_modules"); err != nil {
			result.Warnings = append(result.Warnings, "总览偏好："+service.SanitizeSensitiveError(err.Error()))
		} else if found && strings.TrimSpace(value) != "" {
			if err := json.Unmarshal([]byte(value), &result.Preferences.HiddenModules); err != nil {
				result.Warnings = append(result.Warnings, "总览偏好格式无效，已使用默认布局")
				result.Preferences.HiddenModules = []string{}
			}
		}
	}
	addWarning := func(section string, err error) {
		if err != nil {
			result.Warnings = append(result.Warnings, section+"："+service.SanitizeSensitiveError(err.Error()))
		}
	}

	marketSource := ""
	if h.MarketTrend != nil {
		if market, err := h.MarketTrend.List(ctx, 20); err != nil {
			addWarning("大盘快照", err)
		} else {
			result.Decision.Market.Market = compactDashboardSeries(market.Market)
			result.Decision.Market.Sectors = compactDashboardSeries(strongestAndWeakestSectors(market.Sectors))
			if market.Temperature != nil {
				temperature := *market.Temperature
				temperature.History = nil
				result.Decision.Market.Temperature = &temperature
			}
			result.Decision.Market.LastFetched = market.LastFetched
			marketSource = market.Source
		}
	}
	if h.USFutures != nil {
		if futures, err := h.USFutures.List(ctx, 20); err != nil {
			addWarning("美股期货快照", err)
		} else {
			result.Decision.Market.Futures = compactDashboardSeries(futures.Futures)
			if result.Decision.Market.LastFetched == nil || (futures.LastFetched != nil && futures.LastFetched.After(*result.Decision.Market.LastFetched)) {
				result.Decision.Market.LastFetched = futures.LastFetched
			}
		}
	}
	result.Decision.Market.Freshness = dashboardDataFreshness(ctx, h.DiscoveryDB, dashboardLatestTradeDate(result.Decision.Market), marketSource, result.Decision.Market.LastFetched, now)

	if err := h.loadDashboardCandidateActions(ctx, &result); err != nil {
		addWarning("候选交易计划", err)
	}
	if err := h.loadDashboardCandidateAvailability(ctx, &result); err != nil {
		addWarning("候选可用性", err)
	}
	if err := h.loadDashboardCalendar(ctx, now, &result); err != nil {
		addWarning("事件日历", err)
	}
	if err := h.loadDashboardMonitoring(ctx, now, &result); err != nil {
		addWarning("监控概览", err)
	}
	var operationalReport *service.OperationalReport
	if h.OperationalHealth != nil {
		if report, err := h.OperationalHealth.Report(ctx); err != nil {
			addWarning("运行健康", err)
		} else {
			operationalReport = &report
			result.Operations.Status = report.Status
			result.Operations.Issues = report.Issues
			result.Operations.Tasks = report.Tasks
			result.Operations.FailedNotificationBatches = report.FailedNotificationBatches
			result.Operations.DeadLetterBatches = report.DeadLetterBatches
			for _, issue := range report.Issues {
				// Delivery failures are a user-facing blind spot even when the
				// health service otherwise classifies the queue as warning. Keep
				// them in the intentionally small landing-page banner set.
				if issue.Severity == "critical" || issue.Severity == "danger" || (issue.Category == "notification" && report.FailedNotificationBatches > 0) {
					result.Operations.CriticalIssues = append(result.Operations.CriticalIssues, issue)
				}
			}
		}
	}
	result.Decision.Readiness = buildDashboardDecisionReadiness(ctx, h.DiscoveryDB, result.Decision.Market.Freshness, operationalReport, result.candidateHealth)
	applyDashboardCandidateAvailability(&result.Decision.Readiness, result.Decision.Availability)
	h.dashboardCache = result
	h.dashboardCacheAt = time.Now().UTC()
	OK(c, result)
}

// applyDashboardCandidateAvailability keeps pipeline readiness separate from
// opportunity readiness. A healthy local snapshot can remain useful for
// research even when every candidate is gated from a new trade plan.
func applyDashboardCandidateAvailability(readiness *DashboardDecisionReadiness, availability DashboardCandidateAvailability) {
	if readiness == nil || !readiness.ResearchUsable || availability.Total == 0 || availability.Eligible > 0 {
		return
	}
	readiness.NewTradePlanAllowed = false
	if readiness.Status == "ready" {
		readiness.Status = "research_only"
		readiness.Label = "数据可研究，暂无可行动候选"
	}
	readiness.Reasons = append(readiness.Reasons, DashboardDecisionReadinessItem{
		Key:      "candidate_universe_gated",
		Severity: "warning",
		Title:    "候选均未通过交易门槛",
		Detail:   fmt.Sprintf("当前 %d 只仅供研究、%d 只被阻断；数据管线可用，但不应据此形成新的交易计划", availability.ResearchOnly, availability.Blocked),
		Action:   "discovery-candidates",
	})
}

func (h *AppHandler) invalidateDashboardCache() {
	h.dashboardCacheMu.Lock()
	h.dashboardCacheAt = time.Time{}
	h.dashboardCache = DashboardSummary{}
	h.dashboardCacheMu.Unlock()
}

func buildDashboardDecisionReadiness(ctx context.Context, db *gorm.DB, freshness DashboardDataFreshness, operations *service.OperationalReport, preloadedHealth *discovery.CandidateHealth) DashboardDecisionReadiness {
	result := DashboardDecisionReadiness{
		Status: "ready", Label: "今日数据可用", ResearchUsable: true, NewTradePlanAllowed: true,
		AsOf: freshness.AsOf, ExpectedTradeDate: freshness.ExpectedTradeDate, Reasons: []DashboardDecisionReadinessItem{},
	}
	add := func(key, severity, title, detail, action string) {
		result.Reasons = append(result.Reasons, DashboardDecisionReadinessItem{Key: key, Severity: severity, Title: title, Detail: detail, Action: action})
	}
	researchOnly := func() {
		if result.Status == "ready" {
			result.Status, result.Label = "research_only", "研究可用，暂不形成新交易计划"
		}
		result.NewTradePlanAllowed = false
	}
	block := func() {
		result.Status, result.Label = "blocked", "当日数据不可用于交易判断"
		result.ResearchUsable, result.NewTradePlanAllowed = false, false
	}
	switch freshness.Status {
	case "fresh":
	case "stale":
		researchOnly()
		add("market_stale", "warning", "市场快照落后", freshness.Detail, "market-trend")
	default:
		block()
		add("market_unavailable", "critical", "市场快照不可用", freshness.Detail, "market-trend")
	}
	if db == nil {
		block()
		add("discovery_db_unavailable", "critical", "研究数据库不可用", "无法核对当前候选批次、价格覆盖与策略验证状态", "system-health")
		return result
	}
	health := discovery.CandidateHealth{}
	var err error
	if preloadedHealth != nil {
		health = *preloadedHealth
	} else {
		health, err = discovery.BuildCandidateHealth(ctx, db)
	}
	if err != nil {
		block()
		add("candidate_health_unavailable", "critical", "候选健康不可读", service.SanitizeSensitiveError(err.Error()), "system-health")
	} else if health.Status == discovery.CandidateHealthMissing || health.TotalCandidates == 0 {
		block()
		add("candidate_batch_missing", "critical", "没有可用候选批次", "请先完成小盘候选同步并发布当前批次", "discovery-logs")
	} else {
		if health.MissingPriceCandidates > 0 || health.StalePriceCandidates > 0 || health.FallbackPriceCandidates > 0 || health.MissingMarketCap > 0 {
			researchOnly()
			add("candidate_market_gaps", "warning", "部分候选行情不可用于新计划", fmt.Sprintf("缺价 %d、过期 %d、回退 %d、缺市值 %d；受影响标的必须单独排除", health.MissingPriceCandidates, health.StalePriceCandidates, health.FallbackPriceCandidates, health.MissingMarketCap), "discovery-logs")
		}
		if health.OpenDataQualityIncidents > 0 {
			researchOnly()
			add("candidate_quality_incidents", "warning", "候选事实存在隔离事件", fmt.Sprintf("%d 条数据质量事件尚未关闭", health.OpenDataQualityIncidents), "discovery-logs")
		}
		if health.TechnicalHistoryRetryPending > 0 {
			add("technical_history_pending", "info", "部分标的技术历史待补齐", fmt.Sprintf("%d 只标的仍在独立重试；不影响历史完整的其他标的", health.TechnicalHistoryRetryPending), "discovery-logs")
		}
	}
	effectiveness, err := discovery.BuildCandidateEffectiveness(ctx, db)
	if err != nil {
		researchOnly()
		result.EffectivenessStatus = "unavailable"
		add("effectiveness_unavailable", "warning", "策略效果验证不可读", service.SanitizeSensitiveError(err.Error()), "discovery-candidates")
	} else {
		result.EffectivenessStatus = effectiveness.Status
		result.EffectivenessVersion = effectiveness.ScoringVersion
		if effectiveness.Status != "validated" {
			researchOnly()
			add("effectiveness_"+effectiveness.Status, "warning", "策略效果尚未达到验证门槛", effectiveness.StatusDetail, "discovery-candidates")
		}
		if effectiveness.OutcomeTrackingStatus != "current" {
			researchOnly()
			add("effectiveness_tracking_"+effectiveness.OutcomeTrackingStatus, "warning", "信号结果闭环尚未完整运行", fmt.Sprintf("已跟踪 %d 个结果，成熟 %d，待成熟 %d，缺基准 %d", effectiveness.TrackedOutcomeCount, effectiveness.MatureOutcomeCount, effectiveness.PendingOutcomeCount, effectiveness.BenchmarkMissingOutcomeCount), "discovery-candidates")
		}
	}
	if operations != nil {
		for _, issue := range operations.Issues {
			if issue.Severity != "critical" && issue.Severity != "danger" {
				continue
			}
			block()
			add("operational:"+issue.Key, "critical", issue.Title, issue.Detail, issue.Action)
		}
	}
	return result
}

func dashboardDataFreshness(ctx context.Context, discoveryDB *gorm.DB, tradeDate, source string, lastFetched *time.Time, now time.Time) DashboardDataFreshness {
	if lastFetched == nil || lastFetched.IsZero() {
		return DashboardDataFreshness{Status: "unavailable", Detail: "尚未同步到可用的本地市场快照", QualityStatus: "missing", Source: source}
	}
	result := DashboardDataFreshness{AsOf: tradeDate, Source: source, QualityStatus: discovery.QualityStatusValid}
	if discoveryDB != nil && tradeDate != "" {
		calendar, err := discovery.NewDatabaseMarketCalendar(discoveryDB, discovery.DefaultNYSECalendarVersion)
		if err == nil {
			expected, expectedErr := discovery.LatestCompletedTradingDate(ctx, calendar, now)
			if expectedErr == nil {
				result.ExpectedTradeDate = expected.Format(time.DateOnly)
				newYork, locationErr := time.LoadLocation("America/New_York")
				if locationErr == nil {
					observed, parseErr := time.ParseInLocation(time.DateOnly, tradeDate, newYork)
					if parseErr == nil {
						switch {
						case tradeDate >= result.ExpectedTradeDate:
							result.Status = "fresh"
							result.Detail = "本地市场快照已覆盖最近完成的交易日"
							return result
						case tradingSessionDistance(ctx, calendar, observed, expected) <= 1:
							result.Status = "stale"
							result.QualityStatus = "stale"
							result.Detail = "本地市场快照落后 1 个交易日；请核对同步状态"
							return result
						default:
							result.Status = "expired"
							result.QualityStatus = "expired"
							result.Detail = "本地市场快照落后超过 1 个交易日，不应据此作出交易判断"
							return result
						}
					}
				}
			}
		}
	}
	age := now.Sub(lastFetched.UTC())
	if age < 0 {
		age = 0
	}
	if age <= 36*time.Hour {
		result.Status, result.Detail = "fresh", "本地市场快照在最近 36 小时内更新"
		return result
	}
	if age <= 72*time.Hour {
		result.Status, result.QualityStatus, result.Detail = "stale", "stale", "无法核验交易日历；本地市场快照已超过 36 小时"
		return result
	}
	result.Status, result.QualityStatus, result.Detail = "expired", "expired", "无法核验交易日历；本地市场快照已超过 72 小时，不应据此作出交易判断"
	return result
}

func dashboardLatestTradeDate(summary DashboardMarketSummary) string {
	latest := ""
	for _, rows := range [][]DashboardMarketSeries{summary.Market, summary.Sectors, summary.Futures} {
		for _, row := range rows {
			if row.TradeDate > latest {
				latest = row.TradeDate
			}
		}
	}
	return latest
}

func tradingSessionDistance(ctx context.Context, calendar discovery.MarketCalendar, observed, expected time.Time) int {
	if !observed.Before(expected) {
		return 0
	}
	distance := 0
	for day := observed.AddDate(0, 0, 1); !day.After(expected) && distance <= 15; day = day.AddDate(0, 0, 1) {
		trading, err := calendar.IsTradingDate(ctx, day.Format(time.DateOnly))
		if err != nil {
			return 16
		}
		if trading {
			distance++
		}
	}
	return distance
}

type dashboardPreferencesInput struct {
	HiddenModules []string `json:"hidden_modules"`
}

// UpdateDashboardPreferences persists display-only options without reloading
// the scheduler. A visual preference must never interrupt or re-plan jobs.
func (h *AppHandler) UpdateDashboardPreferences(c *gin.Context) {
	if h.Configs == nil {
		Error(c, errors.New("configuration service is not configured"))
		return
	}
	var input dashboardPreferencesInput
	if err := c.ShouldBindJSON(&input); err != nil {
		Error(c, service.ErrValidation)
		return
	}
	allowed := map[string]bool{"market": true, "actions": true, "calendar": true, "monitoring": true, "operations": true}
	unique := make(map[string]bool, len(input.HiddenModules))
	modules := make([]string, 0, len(input.HiddenModules))
	for _, item := range input.HiddenModules {
		item = strings.TrimSpace(item)
		if !allowed[item] {
			Error(c, service.ErrValidation)
			return
		}
		if !unique[item] {
			unique[item] = true
			modules = append(modules, item)
		}
	}
	sort.Strings(modules)
	encoded, err := json.Marshal(modules)
	if err != nil {
		Error(c, err)
		return
	}
	if err := h.Configs.UpsertMany(c.Request.Context(), []service.ConfigInput{{Key: "ui.dashboard_hidden_modules", Value: string(encoded), ValueType: "json", Category: "ui"}}, operator(c)); err != nil {
		Error(c, err)
		return
	}
	h.invalidateDashboardCache()
	OK(c, DashboardPreferences{HiddenModules: modules})
}

func compactDashboardSeries(rows []service.MarketTrendSeries) []DashboardMarketSeries {
	result := make([]DashboardMarketSeries, 0, len(rows))
	for _, row := range rows {
		result = append(result, DashboardMarketSeries{Symbol: row.Symbol, Label: row.Label, TradeDate: row.TradeDate, Close: row.Close, Change1DPct: row.Change1DPct})
	}
	return result
}

func strongestAndWeakestSectors(sectors []service.MarketTrendSeries) []service.MarketTrendSeries {
	if len(sectors) <= 6 {
		return sectors
	}
	copyRows := append([]service.MarketTrendSeries(nil), sectors...)
	sort.SliceStable(copyRows, func(i, j int) bool {
		left, right := copyRows[i].Change1DPct, copyRows[j].Change1DPct
		if left == nil {
			return false
		}
		if right == nil {
			return true
		}
		return *left > *right
	})
	return append(append([]service.MarketTrendSeries{}, copyRows[:3]...), copyRows[len(copyRows)-3:]...)
}

func (h *AppHandler) loadDashboardCandidateActions(ctx context.Context, result *DashboardSummary) error {
	if h.DiscoveryDB == nil {
		return nil
	}
	var events []discovery.TradeSetupStatusEvent
	if err := h.DiscoveryDB.WithContext(ctx).Order("ticker ASC, started_at DESC, id DESC").Limit(500).Find(&events).Error; err != nil {
		return err
	}
	latest := map[string]discovery.TradeSetupStatusEvent{}
	for _, event := range events {
		if _, found := latest[event.Ticker]; !found {
			latest[event.Ticker] = event
		}
	}
	var pointer discovery.CurrentBatchPointer
	scores := map[string]discovery.CandidateScoreSnapshot{}
	if err := h.DiscoveryDB.WithContext(ctx).First(&pointer, "kind = ?", discovery.BatchKindPrescreen).Error; err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	} else if err == nil {
		var rows []discovery.CandidateScoreSnapshot
		if err := h.DiscoveryDB.WithContext(ctx).Where("batch_id = ?", pointer.BatchID).Find(&rows).Error; err != nil {
			return err
		}
		for _, row := range rows {
			scores[row.Ticker] = row
		}
	}
	var watches []discovery.CandidateWatch
	if err := h.DiscoveryDB.WithContext(ctx).Where("status = ?", discovery.CandidateWatchStatusActive).Find(&watches).Error; err != nil {
		return err
	}
	companyByTicker := map[string]string{}
	for _, watch := range watches {
		companyByTicker[watch.Ticker] = watch.CompanyName
	}
	items := make([]DashboardCandidateAction, 0, len(latest))
	for ticker, event := range latest {
		if event.Status != discovery.TradeSetupEntryCandidate && event.Status != discovery.TradeSetupExitWarning && event.Status != discovery.TradeSetupInvalidated {
			continue
		}
		score := scores[ticker]
		if score.Grade != discovery.CandidateGradeA && score.Grade != discovery.CandidateGradeB {
			continue
		}
		reason := event.EntryTrigger
		if reason == "" {
			reason = event.ExitReason
		}
		priority, nextAction, dueLabel := dashboardActionWorkflow(event.Status)
		items = append(items, DashboardCandidateAction{
			Ticker: ticker, CompanyName: companyByTicker[ticker], Status: event.Status, Priority: priority,
			Tradability: discovery.CandidateResearchReadinessReady, EntryTrigger: event.EntryTrigger, Reason: reason,
			NextAction: nextAction, DueLabel: dueLabel, EvidenceAsOf: event.TradeDate,
			CloseUSD: event.CloseUSD, StopLossUSD: event.StopLossUSD, RiskPct: event.RiskPct,
			TakeProfitLow: event.TakeProfitZoneLowUSD, TakeProfitHigh: event.TakeProfitZoneHighUSD,
			Score: score.TotalScore, Grade: score.Grade, Since: event.StartedAt,
		})
	}
	sort.SliceStable(items, func(i, j int) bool {
		priority := func(status string) int {
			switch status {
			case discovery.TradeSetupInvalidated:
				return 3
			case discovery.TradeSetupExitWarning:
				return 2
			default:
				return 1
			}
		}
		if priority(items[i].Status) != priority(items[j].Status) {
			return priority(items[i].Status) > priority(items[j].Status)
		}
		if items[i].Score != items[j].Score {
			return items[i].Score > items[j].Score
		}
		return items[i].Ticker < items[j].Ticker
	})
	if len(items) > 8 {
		items = items[:8]
	}
	result.Decision.Actions = items

	now := time.Now().UTC()
	if h.Configs != nil {
		if location, _, err := h.Configs.SchedulerTimezone(ctx); err == nil {
			now = time.Now().In(location)
		}
	}
	queue, err := discovery.ListCandidateReviewQueue(ctx, h.DiscoveryDB, now)
	if err != nil {
		return err
	}
	result.Decision.ReviewDue = DashboardReviewDueSummary{Overdue: queue.OverdueCount, DueToday: queue.DueTodayCount, Upcoming: queue.UpcomingCount}
	return nil
}

func dashboardActionWorkflow(status string) (priority, nextAction, dueLabel string) {
	switch status {
	case discovery.TradeSetupInvalidated:
		return "critical", "停止新增风险，复核失效条件与退出计划", "开盘前"
	case discovery.TradeSetupExitWarning:
		return "high", "核对持仓与止损，决定减仓或退出", "开盘前"
	default:
		return "medium", "复核催化剂、入场条件与单笔风险预算", "下次交易前"
	}
}

func (h *AppHandler) loadDashboardCandidateAvailability(ctx context.Context, result *DashboardSummary) error {
	if h.DiscoveryDB == nil {
		return nil
	}
	page, err := discovery.ListCandidateScores(ctx, h.DiscoveryDB, discovery.CandidateScoreQuery{Page: 1, PageSize: 200, SkipPerformance: true, SkipTechnicalDetails: true})
	if err != nil {
		return err
	}
	health, err := discovery.BuildCandidateHealthWithReadiness(ctx, h.DiscoveryDB, page.Items)
	if err != nil {
		return err
	}
	result.candidateHealth = &health
	securityIDs := make([]uint, 0, len(page.Items))
	for _, item := range page.Items {
		securityIDs = append(securityIDs, item.SecurityID)
	}
	companyBySecurity := map[uint]string{}
	if len(securityIDs) > 0 {
		var securities []discovery.Security
		if err := h.DiscoveryDB.WithContext(ctx).Where("id IN ?", securityIDs).Find(&securities).Error; err != nil {
			return err
		}
		for _, security := range securities {
			companyBySecurity[security.ID] = security.CompanyName
		}
	}
	availability := DashboardCandidateAvailability{
		Total: len(page.Items), Usable: []DashboardCandidateAvailabilityItem{}, Excluded: []DashboardCandidateAvailabilityItem{},
	}
	readinessByTicker := make(map[string]string, len(page.Items))
	for _, item := range page.Items {
		readiness := item.ResearchReadiness.Status
		usable := readiness == discovery.CandidateResearchReadinessReady &&
			item.PriceFreshnessStatus == discovery.PriceFreshnessCurrent &&
			item.PriceQualityStatus == discovery.QualityStatusValid && item.MarketCapUSD > 0
		if !usable && readiness == discovery.CandidateResearchReadinessReady {
			readiness = discovery.CandidateResearchReadinessResearchOnly
		}
		readinessByTicker[item.Ticker] = readiness
		reason, nextAction := dashboardCandidateGate(item, usable)
		row := DashboardCandidateAvailabilityItem{
			Ticker: item.Ticker, CompanyName: companyBySecurity[item.SecurityID], Grade: item.Grade,
			Score: item.TotalScore, Readiness: readiness, PrimaryReason: reason, NextAction: nextAction,
			PriceFreshness: item.PriceFreshnessStatus, CloseUSD: item.PriceCloseUSD, ReviewPriority: item.ReviewPriorityScore,
		}
		if item.PriceTradeDate != nil {
			row.PriceTradeDate = item.PriceTradeDate.Format(time.DateOnly)
		}
		switch readiness {
		case discovery.CandidateResearchReadinessReady:
			availability.Eligible++
			availability.Usable = append(availability.Usable, row)
		case discovery.CandidateResearchReadinessBlocked:
			availability.Blocked++
			availability.Excluded = append(availability.Excluded, row)
		default:
			availability.ResearchOnly++
			availability.Excluded = append(availability.Excluded, row)
		}
	}
	sort.SliceStable(availability.Usable, func(i, j int) bool {
		if availability.Usable[i].ReviewPriority != availability.Usable[j].ReviewPriority {
			return availability.Usable[i].ReviewPriority > availability.Usable[j].ReviewPriority
		}
		return availability.Usable[i].Score > availability.Usable[j].Score
	})
	sort.SliceStable(availability.Excluded, func(i, j int) bool {
		if availability.Excluded[i].Readiness != availability.Excluded[j].Readiness {
			return availability.Excluded[i].Readiness == discovery.CandidateResearchReadinessBlocked
		}
		if availability.Excluded[i].ReviewPriority != availability.Excluded[j].ReviewPriority {
			return availability.Excluded[i].ReviewPriority > availability.Excluded[j].ReviewPriority
		}
		return availability.Excluded[i].Score > availability.Excluded[j].Score
	})
	if len(availability.Usable) > 5 {
		availability.Usable = availability.Usable[:5]
	}
	if len(availability.Excluded) > 8 {
		availability.Excluded = availability.Excluded[:8]
	}
	result.Decision.Availability = availability
	for index := range result.Decision.Actions {
		if readiness, found := readinessByTicker[result.Decision.Actions[index].Ticker]; found {
			result.Decision.Actions[index].Tradability = readiness
			if readiness != discovery.CandidateResearchReadinessReady {
				result.Decision.Actions[index].NextAction = "先解除数据或研究门控，再评估交易动作"
			}
		}
	}
	return nil
}

func dashboardCandidateGate(item discovery.CandidateScoreResult, usable bool) (string, string) {
	if usable {
		return "关键行情与研究证据可用", "形成可证伪论点，并核对技术信号与近期催化剂"
	}
	hasReason := func(want string) bool {
		for _, reason := range item.ResearchReadiness.Reasons {
			if reason == want {
				return true
			}
		}
		return false
	}
	switch {
	case item.PriceFreshnessStatus == discovery.PriceFreshnessPreviousTradingDay:
		return "行情仅到前一交易日", "补齐最近完成交易日的有效收盘价"
	case item.PriceFreshnessStatus == discovery.PriceFreshnessStale:
		return "行情已过期", "刷新并核验最近有效行情"
	case item.PriceCloseUSD <= 0 || item.PriceFreshnessStatus == discovery.PriceFreshnessMissing:
		return "缺少有效行情", "补齐最近有效收盘价"
	case item.MarketCapUSD <= 0:
		return "市值证据缺失", "核对股本与市值数据"
	case hasReason("capital_risk_blocks_research"):
		return "重大资本风险阻断", "复核反向拆股、持续经营等 SEC 原始证据"
	case hasReason("investability_blocked"):
		return "流动性条件阻断", "复核成交额与可参与上限"
	case hasReason("financial_metrics_unavailable"), hasReason("financial_period_stale"):
		return "财务证据缺失或过期", "核对最新 10-Q / 10-K 财务指标"
	case hasReason("insider_source_unavailable"), hasReason("insider_coverage_missing"), hasReason("insider_coverage_partial"), hasReason("insider_coverage_unavailable"):
		return "内幕交易证据未完整覆盖", "复核 Form 4 覆盖情况"
	case hasReason("share_dilution_high"):
		return "稀释风险较高", "复核股本变化与融资文件"
	case hasReason("biotech_business_model_unconfirmed"), hasReason("biotech_business_model_review_due"):
		return "业务模型待确认", "确认收入来源与可重复性"
	case hasReason("investability_constrained"):
		return "流动性受限", "复核流动性约束与风险预算"
	default:
		return "关键证据尚未满足门槛", "补齐待核验证据并重新评估"
	}
}

func (h *AppHandler) loadDashboardCalendar(ctx context.Context, now time.Time, result *DashboardSummary) error {
	until := now.AddDate(0, 0, 14)
	items := []DashboardCalendarItem{}
	var previews []model.EarningsPreview
	if err := h.DB.WithContext(ctx).Where("status = ? AND report_at >= ? AND report_at <= ?", "scheduled", now, until).Order("report_at ASC").Limit(8).Find(&previews).Error; err != nil {
		return err
	}
	for _, item := range previews {
		items = append(items, DashboardCalendarItem{Kind: "earnings", Scope: "watch_target", Ticker: item.Ticker, Title: strings.TrimSpace(item.CompanyName) + " 财报预告", At: item.ReportAt, Session: item.Session, Link: "/targets"})
	}
	var candidatePreviews []model.CandidateEarningsPreview
	if err := h.DB.WithContext(ctx).Where("status = ? AND report_at >= ? AND report_at <= ?", "scheduled", now, until).Order("report_at ASC").Limit(8).Find(&candidatePreviews).Error; err != nil {
		return err
	}
	for _, item := range candidatePreviews {
		items = append(items, DashboardCalendarItem{Kind: "earnings", Scope: "candidate", Ticker: item.Ticker, Title: item.Ticker + " 小盘候选财报预告", At: item.ReportAt, Session: item.Session, Link: "/discovery-candidates"})
	}
	var macro []model.MacroRelease
	if err := h.DB.WithContext(ctx).Where("status = ? AND scheduled_at >= ? AND scheduled_at <= ?", service.MacroReleaseScheduled, now, until).Order("market_importance DESC, scheduled_at ASC").Limit(8).Find(&macro).Error; err != nil {
		return err
	}
	for _, item := range macro {
		items = append(items, DashboardCalendarItem{Kind: "macro", Scope: "macro", Title: item.Title, At: item.ScheduledAt, Link: "/macro-calendar"})
	}
	if h.IPO != nil {
		followed := true
		companies, err := h.IPO.ListCompanies(ctx, service.IPOCompanyFilter{Followed: &followed, IncludeEnded: true, Page: 1, PageSize: 6}, now)
		if err != nil {
			return err
		}
		for _, item := range companies.Items {
			at := item.LatestAcceptedAt
			items = append(items, DashboardCalendarItem{Kind: "ipo", Scope: "ipo_followed", Ticker: item.FinalTicker, Title: item.CompanyName + " · " + item.Status, At: at, Link: "/ipo-radar"})
		}
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].At == nil {
			return false
		}
		if items[j].At == nil {
			return true
		}
		return items[i].At.Before(*items[j].At)
	})
	if len(items) > 12 {
		items = items[:12]
	}
	result.Decision.Calendar = items
	return nil
}

func (h *AppHandler) loadDashboardMonitoring(ctx context.Context, now time.Time, result *DashboardSummary) error {
	if err := h.DB.WithContext(ctx).Model(&model.WatchTarget{}).Count(&result.Monitoring.WatchTargets).Error; err != nil {
		return err
	}
	if err := h.DB.WithContext(ctx).Model(&model.WatchTarget{}).Where("status = ?", "enabled").Count(&result.Monitoring.EnabledTargets).Error; err != nil {
		return err
	}
	var upcomingEarnings int64
	if err := h.DB.WithContext(ctx).Model(&model.EarningsPreview{}).Where("status = ? AND report_at >= ?", "scheduled", now).Count(&upcomingEarnings).Error; err != nil {
		return err
	}
	result.Monitoring.UpcomingEarnings = int(upcomingEarnings)
	var enabledTargets []model.WatchTarget
	if err := h.DB.WithContext(ctx).Where("status = ?", "enabled").Find(&enabledTargets).Error; err != nil {
		return err
	}
	result.Monitoring.EarningsCoverageStatus = "not_started"
	if len(enabledTargets) > 0 {
		targetIDs := make([]uint, 0, len(enabledTargets))
		for _, target := range enabledTargets {
			targetIDs = append(targetIDs, target.ID)
		}
		var previews []model.EarningsPreview
		if err := h.DB.WithContext(ctx).Where("target_id IN ?", targetIDs).Find(&previews).Error; err != nil {
			return err
		}
		for _, preview := range previews {
			if preview.Status == "unavailable" {
				result.Monitoring.EarningsUnavailable++
			} else if preview.FetchedAt != nil {
				result.Monitoring.EarningsCoveredTargets++
			}
			if preview.FetchedAt != nil && (result.Monitoring.EarningsLastFetchedAt == nil || preview.FetchedAt.After(*result.Monitoring.EarningsLastFetchedAt)) {
				fetchedAt := preview.FetchedAt.UTC()
				result.Monitoring.EarningsLastFetchedAt = &fetchedAt
			}
		}
		switch {
		case result.Monitoring.EarningsCoveredTargets == len(enabledTargets):
			result.Monitoring.EarningsCoverageStatus = "complete"
		case result.Monitoring.EarningsCoveredTargets > 0:
			result.Monitoring.EarningsCoverageStatus = "partial"
		case result.Monitoring.EarningsUnavailable > 0:
			result.Monitoring.EarningsCoverageStatus = "unavailable"
		}
	}
	importantForms := []string{"8-K", "6-K", "13D", "13G", "3", "4", "5"}
	var filings []model.Filing
	if err := h.DB.WithContext(ctx).Where("filing_type IN ?", importantForms).Order("pulled_at DESC, id DESC").Limit(8).Find(&filings).Error; err != nil {
		return err
	}
	for _, item := range filings {
		filedAt := item.FilingDate
		if item.PublishedAt != nil {
			filedAt = *item.PublishedAt
		}
		result.Monitoring.RecentFilings = append(result.Monitoring.RecentFilings, DashboardFiling{ID: item.ID, Ticker: item.Ticker, CompanyName: item.CompanyName, FilingType: item.FilingType, Title: item.Title, FiledAt: filedAt})
	}
	if h.IPO == nil {
		return nil
	}
	all, err := h.IPO.ListCompanies(ctx, service.IPOCompanyFilter{IncludeEnded: false, Page: 1, PageSize: 100}, now)
	if err != nil {
		return err
	}
	result.Monitoring.IPO.InProgress = int(all.Total)
	followed := true
	followedPage, err := h.IPO.ListCompanies(ctx, service.IPOCompanyFilter{Followed: &followed, IncludeEnded: true, Page: 1, PageSize: 6}, now)
	if err != nil {
		return err
	}
	result.Monitoring.IPO.Followed, result.Monitoring.IPO.FollowedTotal = followedPage.Items, int(followedPage.Total)
	return nil
}
