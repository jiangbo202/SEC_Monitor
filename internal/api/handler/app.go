package handler

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"sec_monitor/internal/config"
	"sec_monitor/internal/discovery"
	"sec_monitor/internal/model"
	"sec_monitor/internal/sec"
	"sec_monitor/internal/service"
	"sec_monitor/internal/telegram"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type AppHandler struct {
	Runtime                config.Config
	DB                     *gorm.DB
	DiscoveryDB            *gorm.DB
	Targets                *service.WatchTargetService
	Configs                *service.ConfigService
	Tasks                  *service.TaskConfigService
	Filings                *service.FilingService
	IPO                    *service.IPORadarService
	SEC                    sec.Client
	Audit                  *service.AuditService
	Notification           *service.NotificationService
	NotificationBatch      *service.NotificationBatchService
	CandidateNotification  *service.CandidateNotificationService
	TradeSetupNotification *service.TradeSetupNotificationService
	TradePlanSimulation    *service.TradePlanSimulationService
	DiscoverySync          *service.DiscoverySyncService
	Backup                 *service.SQLiteBackupService
	Lifecycle              *service.LifecycleService
	OperationalHealth      *service.OperationalHealthService
	Macro                  *service.MacroCalendarService
	EarningsPreview        *service.EarningsPreviewService
	Scheduler              SchedulerController
}

// dataSourceHealthItem is a compact operational summary for a source the
// system relies on. It intentionally describes the most recent *recorded*
// result rather than issuing a fresh network request from the health endpoint.
// Health checks must not consume provider quotas or worsen an outage.
type dataSourceHealthItem struct {
	Source            string     `json:"source"`
	Kind              string     `json:"kind"`
	Status            string     `json:"status"`
	LastCheckedAt     *time.Time `json:"last_checked_at,omitempty"`
	FailureStreak     int        `json:"failure_streak"`
	CoveragePct       *float64   `json:"coverage_pct,omitempty"`
	Detail            string     `json:"detail,omitempty"`
	ErrorMessage      string     `json:"error_message,omitempty"`
	RecommendedAction string     `json:"recommended_action,omitempty"`
}

type SchedulerController interface {
	Reload(ctx context.Context) error
	RunOnce(ctx context.Context) error
	RunTask(ctx context.Context, taskName string) error
}

type tickerLookupResponse struct {
	Ticker           string                 `json:"ticker"`
	CIK              string                 `json:"cik"`
	CompanyName      string                 `json:"company_name"`
	TargetType       string                 `json:"target_type"`
	FundIdentity     *fundIdentityResponse  `json:"fund_identity,omitempty"`
	FundCandidates   []fundIdentityResponse `json:"fund_candidates,omitempty"`
	ResolutionReason string                 `json:"resolution_reason,omitempty"`
}

func (h *AppHandler) ListMacroReleases(c *gin.Context) {
	if h.Macro == nil {
		Error(c, errors.New("macro calendar service is not configured"))
		return
	}
	page, pageSize := pageParams(c)
	filter := service.MacroReleaseFilter{
		Status: strings.TrimSpace(c.Query("status")), Category: strings.TrimSpace(c.Query("category")),
		View: strings.TrimSpace(c.Query("view")), Frequency: strings.TrimSpace(c.Query("frequency")), SortOrder: strings.TrimSpace(c.Query("sort")), Page: page, PageSize: pageSize,
	}
	if filter.Category != "" && !map[string]bool{"personal_income_outlays": true, "gdp": true, "employment": true, "initial_claims": true, "petroleum_inventories": true, "cpi": true, "ppi": true, "jolts": true, "retail_sales": true, "durable_goods": true, "housing_starts": true, "new_home_sales": true, "international_trade": true, "advance_trade": true, "treasury_yields": true, "treasury_real_yields": true, "fomc": true}[filter.Category] {
		Error(c, service.ErrValidation)
		return
	}
	if filter.View != "" && filter.View != "economic" && filter.View != "rates" {
		Error(c, service.ErrValidation)
		return
	}
	if filter.Frequency != "" && filter.Frequency != "daily" && filter.Frequency != "weekly" && filter.Frequency != "monthly" && filter.Frequency != "quarterly" && filter.Frequency != "meeting" {
		Error(c, service.ErrValidation)
		return
	}
	if filter.SortOrder != "" && !strings.EqualFold(filter.SortOrder, "asc") && !strings.EqualFold(filter.SortOrder, "desc") {
		Error(c, service.ErrValidation)
		return
	}
	for key, target := range map[string]**time.Time{"from": &filter.From, "to": &filter.To} {
		if value := strings.TrimSpace(c.Query(key)); value != "" {
			parsed, err := time.Parse("2006-01-02", value)
			if err != nil {
				Error(c, service.ErrValidation)
				return
			}
			if key == "to" {
				// Date-picker ranges are inclusive for humans. Convert the end
				// date to its final instant so releases later that day remain
				// visible regardless of their US publication timezone.
				parsed = parsed.AddDate(0, 0, 1).Add(-time.Nanosecond)
			}
			*target = &parsed
		}
	}
	result, err := h.Macro.List(c.Request.Context(), filter)
	if err != nil {
		Error(c, err)
		return
	}
	OK(c, result)
}

// SyncMacroReleases fetches supported official agency calendars and release
// pages. It never requests a commercial consensus forecast.
func (h *AppHandler) SyncMacroReleases(c *gin.Context) {
	if h.Macro == nil {
		Error(c, errors.New("macro calendar service is not configured"))
		return
	}
	result, err := h.Macro.SyncOfficialBEA(context.WithoutCancel(c.Request.Context()))
	if err != nil {
		Error(c, fmt.Errorf("sync official macro calendar: %s", service.SanitizeSensitiveError(err.Error())))
		return
	}
	OK(c, result)
}

type fundIdentityResponse struct {
	Ticker       string `json:"ticker"`
	CIK          string `json:"cik"`
	FundSeriesID string `json:"fund_series_id"`
	FundClassID  string `json:"fund_class_id"`
	SeriesID     string `json:"series_id"`
	ClassID      string `json:"class_id"`
	FundName     string `json:"fund_name,omitempty"`
	Source       string `json:"source"`
	EvidenceURL  string `json:"evidence_url,omitempty"`
}

// watchTargetWithTechnical adds local EOD technical context to the watch-list
// response without changing the persisted watch-target schema.
type watchTargetWithTechnical struct {
	model.WatchTarget
	Technical discovery.CandidateTechnicalAnalysis `json:"technical"`
}

type watchTargetTechnicalPage struct {
	Items    []watchTargetWithTechnical `json:"items"`
	Total    int64                      `json:"total"`
	Page     int                        `json:"page"`
	PageSize int                        `json:"page_size"`
	Pages    int                        `json:"pages"`
}

func (h *AppHandler) ListDiscoveryCandidates(c *gin.Context) {
	page, pageSize := pageParams(c)
	var eligibleA *bool
	if value := strings.TrimSpace(c.Query("eligible_a")); value != "" {
		parsed, err := strconv.ParseBool(value)
		if err != nil {
			Error(c, service.ErrValidation)
			return
		}
		eligibleA = &parsed
	}
	var eligibleB *bool
	if value := strings.TrimSpace(c.Query("eligible_b")); value != "" {
		parsed, err := strconv.ParseBool(value)
		if err != nil {
			Error(c, service.ErrValidation)
			return
		}
		eligibleB = &parsed
	}
	minPriority := 0
	if value := strings.TrimSpace(c.Query("min_review_priority_score")); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 0 {
			Error(c, service.ErrValidation)
			return
		}
		minPriority = parsed
	}
	recommendedOnly := false
	if value := strings.TrimSpace(c.Query("recommended_only")); value != "" {
		parsed, err := strconv.ParseBool(value)
		if err != nil {
			Error(c, service.ErrValidation)
			return
		}
		recommendedOnly = parsed
	}
	followedOnly := false
	if value := strings.TrimSpace(c.Query("followed")); value != "" {
		parsed, err := strconv.ParseBool(value)
		if err != nil {
			Error(c, service.ErrValidation)
			return
		}
		followedOnly = parsed
	}
	includePerformance := false
	if value := strings.TrimSpace(c.Query("include_performance")); value != "" {
		parsed, err := strconv.ParseBool(value)
		if err != nil {
			Error(c, service.ErrValidation)
			return
		}
		includePerformance = parsed
	}
	parseOptionalFloat := func(key string) (*float64, bool) {
		value := strings.TrimSpace(c.Query(key))
		if value == "" {
			return nil, true
		}
		parsed, err := strconv.ParseFloat(value, 64)
		if err != nil || parsed < 0 {
			return nil, false
		}
		return &parsed, true
	}
	maxEVSales, ok := parseOptionalFloat("max_ev_sales")
	if !ok {
		Error(c, service.ErrValidation)
		return
	}
	minNetCashToMarketCapPct, ok := parseOptionalFloat("min_net_cash_to_market_cap_pct")
	if !ok {
		Error(c, service.ErrValidation)
		return
	}
	priceFreshnessStatuses := splitQueryValues(c.QueryArray("price_freshness"), c.Query("price_freshness"))
	validPriceFreshness := map[string]bool{
		discovery.PriceFreshnessCurrent: true, discovery.PriceFreshnessPreviousTradingDay: true,
		discovery.PriceFreshnessStale: true, discovery.PriceFreshnessFuture: true,
		discovery.PriceFreshnessMissing: true, discovery.PriceFreshnessUnknown: true,
	}
	for _, status := range priceFreshnessStatuses {
		if !validPriceFreshness[status] {
			Error(c, service.ErrValidation)
			return
		}
	}
	upcomingEarningsTickers := []string(nil)
	upcomingEarningsOnly := false
	if value := strings.TrimSpace(c.Query("upcoming_earnings")); value != "" {
		parsed, parseErr := strconv.ParseBool(value)
		if parseErr != nil {
			Error(c, service.ErrValidation)
			return
		}
		if parsed {
			upcomingEarningsOnly = true
			var earningsErr error
			upcomingEarningsTickers, earningsErr = h.earningsPreviewService().UpcomingCandidateTickers(c.Request.Context())
			if earningsErr != nil {
				Error(c, earningsErr)
				return
			}
		}
	}
	result, err := discovery.ListCandidateScores(c.Request.Context(), h.DiscoveryDB, discovery.CandidateScoreQuery{
		Page: page, PageSize: pageSize, Ticker: c.Query("ticker"), Grade: c.Query("grade"),
		SectorCategory: c.Query("sector_category"), QualityTier: c.Query("quality_tier"), ChangeStatus: c.Query("change_status"), TechnicalSignal: c.Query("technical_signal"), ResearchReadiness: c.Query("research_readiness"),
		SortBy: c.Query("sort_by"), SortOrder: c.Query("sort_order"), MinReviewPriorityScore: minPriority,
		RecommendedOnly:          recommendedOnly,
		ExcludeResearchReadiness: splitQueryValues(c.QueryArray("exclude_research_readiness"), c.Query("exclude_research_readiness")),
		PriceFreshnessStatuses:   priceFreshnessStatuses,
		ExcludeQualityTags:       splitQueryValues(c.QueryArray("exclude_quality_tag"), c.Query("exclude_quality_tag")),
		EligibleA:                eligibleA, EligibleB: eligibleB,
		MaxEVSales: maxEVSales, MinNetCashToMarketCapPct: minNetCashToMarketCapPct,
		SkipPerformance:         !includePerformance,
		UpcomingEarningsTickers: upcomingEarningsTickers,
		UpcomingEarningsOnly:    upcomingEarningsOnly,
		FollowedOnly:            followedOnly,
	})
	if err != nil {
		Error(c, err)
		return
	}
	OK(c, result)
}

func (h *AppHandler) GetDiscoveryCandidateCriteria(c *gin.Context) {
	OK(c, discovery.CurrentCandidateSelectionCriteria())
}

// CheckDiscoverySmallCapEligibility explains the current, persisted selection
// rules for any ticker in the discovered universe. It intentionally performs
// no provider request; a manual check must not silently spend SEC or market
// data quota while a researcher is comparing conditions.
func (h *AppHandler) CheckDiscoverySmallCapEligibility(c *gin.Context) {
	var input discovery.SmallCapEligibilityCheckInput
	if err := c.ShouldBindJSON(&input); err != nil {
		Error(c, service.ErrValidation)
		return
	}
	result, err := discovery.CheckSmallCapEligibility(c.Request.Context(), h.DiscoveryDB, input, time.Now().UTC())
	if err != nil {
		Error(c, err)
		return
	}
	OK(c, result)
}

func (h *AppHandler) ListDiscoverySmallCapEligibilityChecks(c *gin.Context) {
	page, pageSize := pageParams(c)
	result, err := discovery.ListSmallCapEligibilityCheckHistory(c.Request.Context(), h.DiscoveryDB, page, pageSize, c.Query("ticker"))
	if err != nil {
		Error(c, err)
		return
	}
	OK(c, result)
}

func (h *AppHandler) GetDiscoveryCandidateOverview(c *gin.Context) {
	result, err := discovery.BuildCandidateOverview(c.Request.Context(), h.DiscoveryDB)
	if err != nil {
		Error(c, err)
		return
	}
	OK(c, result)
}

func (h *AppHandler) ListCandidateWatches(c *gin.Context) {
	page, pageSize := pageParams(c)
	result, err := discovery.ListCandidateWatches(c.Request.Context(), h.DiscoveryDB, discovery.CandidateWatchQuery{
		Page: page, PageSize: pageSize, Ticker: c.Query("ticker"), Status: c.Query("status"),
	})
	if err != nil {
		Error(c, err)
		return
	}
	OK(c, result)
}

func (h *AppHandler) GetCandidateReviewQueue(c *gin.Context) {
	now := time.Now().UTC()
	if h.Configs != nil {
		location, _, err := h.Configs.SchedulerTimezone(c.Request.Context())
		if err != nil {
			Error(c, err)
			return
		}
		now = time.Now().In(location)
	}
	result, err := discovery.ListCandidateReviewQueue(c.Request.Context(), h.DiscoveryDB, now)
	if err != nil {
		Error(c, err)
		return
	}
	OK(c, result)
}

func (h *AppHandler) UpsertCandidateWatch(c *gin.Context) {
	var input discovery.CandidateWatchInput
	if err := c.ShouldBindJSON(&input); err != nil {
		Error(c, err)
		return
	}
	result, err := discovery.UpsertCandidateWatch(c.Request.Context(), h.DiscoveryDB, input)
	if err != nil {
		Error(c, err)
		return
	}
	OK(c, result)
}

func (h *AppHandler) DeleteCandidateWatch(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		Error(c, service.ErrValidation)
		return
	}
	if err := discovery.DeleteCandidateWatch(c.Request.Context(), h.DiscoveryDB, uint(id)); err != nil {
		Error(c, err)
		return
	}
	c.Status(204)
}

func (h *AppHandler) ListCandidateResearchPositions(c *gin.Context) {
	result, err := discovery.ListCandidateResearchPositions(c.Request.Context(), h.DiscoveryDB)
	if err != nil {
		Error(c, err)
		return
	}
	OK(c, result)
}

func (h *AppHandler) UpsertCandidateResearchPosition(c *gin.Context) {
	var input discovery.CandidateResearchPositionInput
	if err := c.ShouldBindJSON(&input); err != nil {
		Error(c, err)
		return
	}
	result, err := discovery.UpsertCandidateResearchPosition(c.Request.Context(), h.DiscoveryDB, input)
	if err != nil {
		Error(c, err)
		return
	}
	OK(c, result)
}

func (h *AppHandler) DeleteCandidateResearchPosition(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		Error(c, service.ErrValidation)
		return
	}
	if err := discovery.DeleteCandidateResearchPosition(c.Request.Context(), h.DiscoveryDB, uint(id)); err != nil {
		Error(c, err)
		return
	}
	c.Status(204)
}

func (h *AppHandler) GetDiscoveryCandidateDetail(c *gin.Context) {
	result, err := discovery.GetCandidateDetail(c.Request.Context(), h.DiscoveryDB, c.Param("ticker"))
	if err != nil {
		Error(c, err)
		return
	}
	if h.DB != nil {
		filings, err := h.listRecentCandidateFilings(c.Request.Context(), result.Score.Ticker, result.Security.CIK, 20)
		if err != nil {
			Error(c, err)
			return
		}
		result.RecentFilings = filings
	}
	OK(c, result)
}

// GetDiscoveryCompanyProfile serves the locally persisted SEC issuer metadata
// used by both candidate and watch-target detail panels. It intentionally does
// not make an external SEC request while rendering a page.
func (h *AppHandler) GetDiscoveryCompanyProfile(c *gin.Context) {
	result, err := discovery.GetCompanyProfile(c.Request.Context(), h.DiscoveryDB, c.Param("ticker"), c.Query("cik"))
	if err != nil {
		Error(c, err)
		return
	}
	OK(c, result)
}

// ListDiscoveryCompanyProfileRecoveryQueue exposes persisted, failed
// Longbridge profile enrichments for current candidates. Rendering this queue
// is local-only and never consumes a provider request.
func (h *AppHandler) ListDiscoveryCompanyProfileRecoveryQueue(c *gin.Context) {
	cfg := h.Runtime.Discovery
	if h.Configs != nil {
		applied, err := h.Configs.ApplyDiscoveryConfig(c.Request.Context(), cfg)
		if err != nil {
			Error(c, err)
			return
		}
		cfg = applied
	}
	result, err := discovery.ListCurrentCandidateCompanyProfileRecoveryQueue(c.Request.Context(), h.DiscoveryDB, cfg.LongbridgeCompanyProfileTTLDays, time.Now().UTC())
	if err != nil {
		Error(c, err)
		return
	}
	for index := range result.Items {
		result.Items[index].LastError = service.SanitizeSensitiveError(result.Items[index].LastError)
	}
	OK(c, result)
}

// ListDiscoveryMarketPriceRecoveryQueue exposes current candidates whose
// locally displayed quote needs attention. This endpoint is intentionally
// read-only: it never invokes a market provider while the log page loads.
func (h *AppHandler) ListDiscoveryMarketPriceRecoveryQueue(c *gin.Context) {
	result, err := discovery.ListCurrentCandidateMarketPriceRecoveryQueue(c.Request.Context(), h.DiscoveryDB)
	if err != nil {
		Error(c, err)
		return
	}
	OK(c, result)
}

// RefreshDiscoveryCandidateMarketHistory requests price history for exactly
// one current candidate. It only enriches local daily close/volume snapshots;
// eligibility and score remain the immutable output of the published market
// batch and are refreshed by the normal market workflow.
func (h *AppHandler) RefreshDiscoveryCandidateMarketHistory(c *gin.Context) {
	result, err := h.discoverySyncService().RefreshCandidateMarketHistoryAndScore(context.WithoutCancel(c.Request.Context()), c.Param("ticker"))
	if err != nil {
		Error(c, err)
		return
	}
	OK(c, result)
}

// RefreshDiscoveryCompanyProfile explicitly refreshes a single cached
// Longbridge overview. It is intentionally POST-only so normal detail-page
// rendering remains fully local and cannot consume provider quota.
func (h *AppHandler) RefreshDiscoveryCompanyProfile(c *gin.Context) {
	result, err := h.discoverySyncService().RefreshLongbridgeCompanyProfile(c.Request.Context(), c.Param("ticker"), c.Query("cik"), true)
	if err != nil {
		Error(c, err)
		return
	}
	profile, err := discovery.GetCompanyProfile(c.Request.Context(), h.DiscoveryDB, c.Param("ticker"), c.Query("cik"))
	if err != nil {
		Error(c, err)
		return
	}
	OK(c, gin.H{"refresh": result, "profile": profile})
}

// RetryDiscoveryCompanyProfile is an explicit operator action for one failed
// enrichment. It bypasses the automatic backoff but still updates the same
// durable retry record if Longbridge remains unavailable.
func (h *AppHandler) RetryDiscoveryCompanyProfile(c *gin.Context) {
	result, err := h.discoverySyncService().RefreshLongbridgeCompanyProfile(c.Request.Context(), c.Param("ticker"), c.Query("cik"), true)
	if err != nil {
		Error(c, fmt.Errorf("retry Longbridge company profile: %s", service.SanitizeSensitiveError(err.Error())))
		return
	}
	OK(c, result)
}

// RetryDiscoveryCompanyProfileQueue retries the local failed profile queue in
// a bounded sequence. It never starts a SEC or market-price workflow.
func (h *AppHandler) RetryDiscoveryCompanyProfileQueue(c *gin.Context) {
	result, err := h.discoverySyncService().RetryCurrentLongbridgeCompanyProfiles(c.Request.Context())
	if err != nil {
		Error(c, fmt.Errorf("retry Longbridge company profile queue: %s", service.SanitizeSensitiveError(err.Error())))
		return
	}
	result.StopReason = service.SanitizeSensitiveError(result.StopReason)
	result.Message = service.SanitizeSensitiveError(result.Message)
	OK(c, result)
}

// ProbeDiscoveryLongbridgeQuote performs one authenticated AAPL.US quote
// request without starting a candidate sync. Probe failures are returned as
// structured diagnostic data so the UI can show the exact safe error text.
func (h *AppHandler) ProbeDiscoveryLongbridgeQuote(c *gin.Context) {
	result := h.discoverySyncService().ProbeLongbridgeQuote(c.Request.Context())
	OK(c, result)
}

// GetDiscoveryAnalystRating serves locally persisted analyst consensus data.
// It intentionally does not make a Longbridge call on page load.
func (h *AppHandler) GetDiscoveryAnalystRating(c *gin.Context) {
	result, err := discovery.GetAnalystRating(c.Request.Context(), h.DiscoveryDB, c.Param("ticker"))
	if err != nil {
		Error(c, err)
		return
	}
	OK(c, result)
}

// RefreshDiscoveryAnalystRating explicitly refreshes one issuer. It does not
// run SEC sync, change a score, or submit a candidate notification by itself.
func (h *AppHandler) RefreshDiscoveryAnalystRating(c *gin.Context) {
	result, err := h.discoverySyncService().RefreshLongbridgeAnalystRating(context.WithoutCancel(c.Request.Context()), c.Param("ticker"), c.Query("cik"))
	if err != nil {
		Error(c, fmt.Errorf("refresh Longbridge analyst rating: %s", service.SanitizeSensitiveError(err.Error())))
		return
	}
	view, err := discovery.GetAnalystRating(c.Request.Context(), h.DiscoveryDB, c.Param("ticker"))
	if err != nil {
		Error(c, err)
		return
	}
	OK(c, gin.H{"refresh": result, "rating": view})
}

func (h *AppHandler) UpsertDiscoveryCandidateBusinessModel(c *gin.Context) {
	var input discovery.CandidateBusinessModelInput
	if err := c.ShouldBindJSON(&input); err != nil {
		Error(c, err)
		return
	}
	input.Ticker = c.Param("ticker")
	result, err := discovery.UpsertCandidateBusinessModel(c.Request.Context(), h.DiscoveryDB, input)
	if err != nil {
		Error(c, err)
		return
	}
	OK(c, result)
}

func (h *AppHandler) GetDiscoveryProfitHistory(c *gin.Context) {
	result, err := discovery.GetProfitHistory(c.Request.Context(), h.DiscoveryDB, c.Param("ticker"))
	if err != nil {
		Error(c, err)
		return
	}
	OK(c, result)
}

func (h *AppHandler) listRecentCandidateFilings(ctx context.Context, ticker, cik string, limit int) ([]discovery.RecentSECFiling, error) {
	if limit <= 0 {
		limit = 20
	}
	query := h.DB.WithContext(ctx).Model(&model.Filing{})
	symbol := strings.ToUpper(strings.TrimSpace(ticker))
	issuerCIK := strings.TrimSpace(cik)
	switch {
	case symbol != "" && issuerCIK != "":
		query = query.Where("ticker = ? OR cik = ?", symbol, issuerCIK)
	case symbol != "":
		query = query.Where("ticker = ?", symbol)
	case issuerCIK != "":
		query = query.Where("cik = ?", issuerCIK)
	default:
		return []discovery.RecentSECFiling{}, nil
	}
	var rows []model.Filing
	if err := query.Order("filing_date DESC").Order("published_at DESC").Order("id DESC").Limit(limit).Find(&rows).Error; err != nil {
		return nil, err
	}
	items := make([]discovery.RecentSECFiling, 0, len(rows))
	for _, row := range rows {
		items = append(items, discovery.RecentSECFiling{
			FilingID: row.FilingID, AccessionNumber: row.AccessionNumber, Ticker: row.Ticker, CIK: row.CIK,
			CompanyName: row.CompanyName, FilingType: row.FilingType, FilingDate: row.FilingDate,
			PublishedAt: row.PublishedAt, FilingURL: row.FilingURL, Title: row.Title,
		})
	}
	return items, nil
}

func (h *AppHandler) GetDiscoveryCandidateHealth(c *gin.Context) {
	result, err := discovery.BuildCandidateHealth(c.Request.Context(), h.DiscoveryDB)
	if err != nil {
		Error(c, err)
		return
	}
	OK(c, result)
}

func (h *AppHandler) RefreshDiscoveryCandidates(c *gin.Context) {
	// A full SEC discovery run can take much longer than an HTTP request.  The
	// browser may cancel its request while the user keeps the page open (or
	// navigates away), but that must not abort the persisted workflow midway.
	// Run still applies its configured end-to-end task timeout internally.
	result, err := h.discoverySyncService().Run(context.WithoutCancel(c.Request.Context()))
	if err != nil {
		Error(c, err)
		return
	}
	OK(c, result)
}

func (h *AppHandler) ForceRefreshDiscoveryMarketPrices(c *gin.Context) {
	// This is an intentional operator recovery action. It does not re-download
	// SEC bulk data; it only requests the most recently completed market close
	// when the regular pre-close cache reuse cannot supply sufficient coverage.
	result, err := h.discoverySyncService().RunMarketOnlyForceLive(context.WithoutCancel(c.Request.Context()))
	if err != nil {
		Error(c, err)
		return
	}
	OK(c, result)
}

func (h *AppHandler) BackfillDiscoveryCandidateTechnicalHistory(c *gin.Context) {
	var input struct {
		LookbackDays int `json:"lookback_days"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		Error(c, service.ErrValidation)
		return
	}
	// Historical price backfills are similarly long-running and rate-limited by
	// providers, so do not couple their execution to the browser connection.
	result, err := h.discoverySyncService().BackfillTechnicalHistory(context.WithoutCancel(c.Request.Context()), input.LookbackDays)
	if err != nil {
		Error(c, err)
		return
	}
	OK(c, result)
}

func (h *AppHandler) GetDiscoveryCandidateReport(c *gin.Context) {
	result, err := discovery.BuildCandidateReport(c.Request.Context(), h.DiscoveryDB, c.Query("date"))
	if err != nil {
		Error(c, err)
		return
	}
	OK(c, result)
}

func (h *AppHandler) GetDiscoveryCandidateEffectiveness(c *gin.Context) {
	result, err := discovery.BuildCandidateEffectiveness(c.Request.Context(), h.DiscoveryDB)
	if err != nil {
		Error(c, err)
		return
	}
	OK(c, result)
}

func (h *AppHandler) GetTradePlanSimulations(c *gin.Context) {
	result, err := h.tradePlanSimulationService().Report(c.Request.Context())
	if err != nil {
		Error(c, err)
		return
	}
	OK(c, result)
}

func (h *AppHandler) RebuildTradePlanSimulations(c *gin.Context) {
	result, err := h.tradePlanSimulationService().Rebuild(context.WithoutCancel(c.Request.Context()))
	if err != nil {
		Error(c, err)
		return
	}
	OK(c, result)
}

func (h *AppHandler) ExportDiscoveryCandidatesCSV(c *gin.Context) {
	items, err := discovery.ListCandidateScores(c.Request.Context(), h.DiscoveryDB, discovery.CandidateScoreQuery{Page: 1, PageSize: 200, Ticker: c.Query("ticker"), Grade: c.Query("grade"), QualityTier: c.Query("quality_tier"), ChangeStatus: c.Query("change_status")})
	if err != nil {
		Error(c, err)
		return
	}
	c.Header("Content-Type", "text/csv; charset=utf-8")
	c.Header("Content-Disposition", `attachment; filename="sec-monitor-candidates.csv"`)
	w := csv.NewWriter(c.Writer)
	defer w.Flush()
	_ = w.Write([]string{"ticker", "grade", "total_score", "priority", "change", "market_cap_usd", "revenue_growth_pct", "cash_runway_months", "average_dollar_volume_usd", "volatility_pct", "momentum_pct", "max_drawdown_pct", "quality_tags"})
	for _, item := range items.Items {
		_ = w.Write([]string{item.Ticker, item.Grade, strconv.Itoa(item.TotalScore), strconv.Itoa(item.ReviewPriorityScore), item.ChangeStatus, strconv.FormatInt(item.MarketCapUSD, 10), fmt.Sprintf("%.2f", item.RevenueGrowthPct), fmt.Sprintf("%.2f", item.CashRunwayMonths), fmt.Sprintf("%.2f", item.MarketQuality.AverageDollarVolume), fmt.Sprintf("%.2f", item.MarketQuality.VolatilityPct), fmt.Sprintf("%.2f", item.MarketQuality.MomentumPct), fmt.Sprintf("%.2f", item.MarketQuality.MaxDrawdownPct), strings.Join(item.QualityTags, "|")})
	}
}

func (h *AppHandler) PreviewDiscoveryCandidateSummary(c *gin.Context) {
	limit := 0
	if value := strings.TrimSpace(c.Query("limit")); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 0 {
			Error(c, service.ErrValidation)
			return
		}
		limit = parsed
	}
	result, err := discovery.BuildCandidateSummary(c.Request.Context(), h.DiscoveryDB, limit)
	if err != nil {
		Error(c, err)
		return
	}
	OK(c, result)
}

func (h *AppHandler) PreviewDiscoveryCandidateNotification(c *gin.Context) {
	result, err := h.candidateNotificationService().Preview(c.Request.Context())
	if err != nil {
		Error(c, err)
		return
	}
	OK(c, result)
}

func (h *AppHandler) SendDiscoveryCandidateNotification(c *gin.Context) {
	var input service.CandidateNotificationSendInput
	if err := c.ShouldBindJSON(&input); err != nil {
		Error(c, service.ErrValidation)
		return
	}
	result, err := h.candidateNotificationService().Send(c.Request.Context(), input)
	if err != nil {
		Error(c, err)
		return
	}
	OK(c, result)
}

func (h *AppHandler) PreviewTradeSetupNotification(c *gin.Context) {
	result, _, err := h.tradeSetupNotificationService().Preview(c.Request.Context())
	if err != nil {
		Error(c, err)
		return
	}
	OK(c, result)
}

func (h *AppHandler) SendTradeSetupNotification(c *gin.Context) {
	var input service.TradeSetupNotificationSendInput
	if err := c.ShouldBindJSON(&input); err != nil {
		Error(c, service.ErrValidation)
		return
	}
	result, err := h.tradeSetupNotificationService().Send(c.Request.Context(), input)
	if err != nil {
		Error(c, err)
		return
	}
	OK(c, result)
}

func (h *AppHandler) ListDiscoveryBatches(c *gin.Context) {
	page, pageSize := pageParams(c)
	result, err := discovery.ListBatches(c.Request.Context(), h.DiscoveryDB, discovery.BatchQuery{
		Page: page, PageSize: pageSize, Kind: c.Query("kind"), Status: c.Query("status"),
	})
	if err != nil {
		Error(c, err)
		return
	}
	OK(c, result)
}

func (h *AppHandler) ListDiscoveryProviderRuns(c *gin.Context) {
	page, pageSize := pageParams(c)
	result, err := discovery.ListProviderDiagnostics(c.Request.Context(), h.DiscoveryDB, discovery.ProviderRunQuery{
		Page: page, PageSize: pageSize, Provider: c.Query("provider"), Status: c.Query("status"), BatchID: c.Query("batch_id"),
	})
	if err != nil {
		Error(c, err)
		return
	}
	OK(c, result)
}

func (h *AppHandler) GetDiscoverySyncStatus(c *gin.Context) {
	run, err := discovery.LatestDiscoverySyncRun(c.Request.Context(), h.DiscoveryDB)
	if err != nil {
		Error(c, err)
		return
	}
	OK(c, run)
}

func (h *AppHandler) ListDiscoverySyncRuns(c *gin.Context) {
	page, pageSize := pageParams(c)
	result, err := discovery.ListDiscoverySyncRuns(c.Request.Context(), h.DiscoveryDB, discovery.DiscoverySyncRunQuery{
		Page: page, PageSize: pageSize, Status: c.Query("status"), Kind: c.Query("kind"),
	})
	if err != nil {
		Error(c, err)
		return
	}
	OK(c, result)
}

func (h *AppHandler) ListDiscoverySyncSteps(c *gin.Context) {
	result, err := discovery.ListDiscoverySyncSteps(c.Request.Context(), h.DiscoveryDB, uintParam(c, "id"))
	if err != nil {
		Error(c, err)
		return
	}
	OK(c, result)
}

func (h *AppHandler) GetDiscoveryStorageHealth(c *gin.Context) {
	cfg, err := h.Configs.ApplyDiscoveryConfig(c.Request.Context(), h.Runtime.Discovery)
	if err != nil {
		Error(c, err)
		return
	}
	result, err := discovery.InspectStorage(cfg.Database.DSN, cfg.CacheDir)
	if err != nil {
		Error(c, err)
		return
	}
	OK(c, result)
}

func (h *AppHandler) PreviewDiscoveryCacheCleanup(c *gin.Context) {
	cfg, err := h.Configs.ApplyDiscoveryConfig(c.Request.Context(), h.Runtime.Discovery)
	if err != nil {
		Error(c, err)
		return
	}
	result, err := discovery.PreviewCacheCleanup(cfg.CacheDir, cfg.CacheRetentionDays, time.Now())
	if err != nil {
		Error(c, err)
		return
	}
	OK(c, result)
}

func (h *AppHandler) CleanupDiscoveryCache(c *gin.Context) {
	cfg, err := h.Configs.ApplyDiscoveryConfig(c.Request.Context(), h.Runtime.Discovery)
	if err != nil {
		Error(c, err)
		return
	}
	result, err := discovery.CleanupExpiredCache(cfg.CacheDir, cfg.CacheRetentionDays, time.Now())
	if err != nil {
		Error(c, err)
		return
	}
	if h.Audit != nil {
		_ = h.Audit.Record(c.Request.Context(), "local_user", "cleanup", "discovery_cache", cfg.CacheDir, nil, result)
	}
	OK(c, result)
}

func (h *AppHandler) ListDiscoveryProviderHealth(c *gin.Context) {
	result, err := discovery.ListProviderHealth(c.Request.Context(), h.DiscoveryDB)
	if err != nil {
		Error(c, err)
		return
	}
	OK(c, result)
}

// GetDiscoveryProviderObservability returns locally recorded provider state.
// It intentionally does not probe third-party APIs, so opening the discovery
// log page cannot consume a price-provider quota.
func (h *AppHandler) GetDiscoveryProviderObservability(c *gin.Context) {
	cfg, err := h.Configs.ApplyDiscoveryConfig(c.Request.Context(), h.Runtime.Discovery)
	if err != nil {
		Error(c, err)
		return
	}
	result, err := discovery.GetProviderObservability(c.Request.Context(), h.DiscoveryDB, cfg)
	if err != nil {
		Error(c, err)
		return
	}
	OK(c, result)
}

func (h *AppHandler) candidateNotificationService() *service.CandidateNotificationService {
	if h.CandidateNotification != nil {
		return h.CandidateNotification
	}
	return service.NewCandidateNotificationService(h.DB, h.DiscoveryDB, nil, h.Configs)
}

func (h *AppHandler) tradeSetupNotificationService() *service.TradeSetupNotificationService {
	if h.TradeSetupNotification != nil {
		return h.TradeSetupNotification
	}
	return service.NewTradeSetupNotificationService(h.DB, h.DiscoveryDB, nil, h.Configs)
}

func (h *AppHandler) tradePlanSimulationService() *service.TradePlanSimulationService {
	if h.TradePlanSimulation == nil {
		h.TradePlanSimulation = service.NewTradePlanSimulationService(h.DB, h.DiscoveryDB)
	}
	return h.TradePlanSimulation
}

func (h *AppHandler) earningsPreviewService() *service.EarningsPreviewService {
	if h.EarningsPreview != nil {
		return h.EarningsPreview
	}
	return service.NewEarningsPreviewService(h.DB, h.Runtime.Discovery, h.Configs, nil)
}

func (h *AppHandler) discoverySyncService() *service.DiscoverySyncService {
	if h.DiscoverySync != nil {
		return h.DiscoverySync
	}
	return service.NewDiscoverySyncService(h.DiscoveryDB, h.Runtime.Discovery).WithConfigService(h.Configs).WithWatchTargetDB(h.DB)
}

func (h *AppHandler) LookupTicker(c *gin.Context) {
	ticker := strings.ToUpper(strings.TrimSpace(c.Param("ticker")))
	if ticker == "" {
		Error(c, service.ErrValidation)
		return
	}
	if strings.EqualFold(strings.TrimSpace(c.Query("target_type")), "etf") {
		result, err := h.lookupFundTicker(c.Request.Context(), ticker)
		if err != nil {
			Error(c, err)
			return
		}
		OK(c, result)
		return
	}
	cik, companyName, err := h.SEC.LookupCIK(c.Request.Context(), ticker)
	if err == nil {
		OK(c, tickerLookupResponse{
			Ticker: ticker, CIK: cik, CompanyName: companyName, TargetType: "stock",
		})
		return
	}

	if _, ok := h.SEC.(sec.FundIdentityClient); ok {
		result, fundErr := h.lookupFundTicker(c.Request.Context(), ticker)
		if fundErr == nil && (result.FundIdentity != nil || len(result.FundCandidates) > 0 || result.ResolutionReason != "") {
			OK(c, result)
			return
		}
	}
	Error(c, err)
}

func (h *AppHandler) lookupFundTicker(ctx context.Context, ticker string) (tickerLookupResponse, error) {
	result := tickerLookupResponse{Ticker: ticker, TargetType: "etf"}
	fundClient, ok := h.SEC.(sec.FundIdentityClient)
	if !ok {
		result.ResolutionReason = "SEC client does not support fund identity resolution"
		return result, nil
	}
	resolution, err := fundClient.ResolveFundTicker(ctx, ticker)
	if err != nil {
		return tickerLookupResponse{}, err
	}
	if resolution.Identity != nil && completeFundIdentity(*resolution.Identity, ticker) {
		identity := fundIdentityTransport(*resolution.Identity)
		result.FundIdentity = &identity
		return result, nil
	}
	for _, candidate := range resolution.Candidates {
		if completeFundIdentity(candidate, ticker) {
			result.FundCandidates = append(result.FundCandidates, fundIdentityTransport(candidate))
		}
	}
	result.ResolutionReason = strings.TrimSpace(resolution.Reason)
	if result.ResolutionReason == "" {
		result.ResolutionReason = "no complete exact SEC fund identity found"
	}
	return result, nil
}

func completeFundIdentity(identity sec.FundIdentity, ticker string) bool {
	return strings.EqualFold(strings.TrimSpace(identity.Ticker), ticker) &&
		strings.TrimSpace(identity.CIK) != "" &&
		strings.TrimSpace(identity.SeriesID) != "" &&
		strings.TrimSpace(identity.ClassID) != ""
}

func fundIdentityTransport(identity sec.FundIdentity) fundIdentityResponse {
	return fundIdentityResponse{
		Ticker:       strings.ToUpper(strings.TrimSpace(identity.Ticker)),
		CIK:          strings.TrimSpace(identity.CIK),
		FundSeriesID: strings.TrimSpace(identity.SeriesID),
		FundClassID:  strings.TrimSpace(identity.ClassID),
		SeriesID:     strings.TrimSpace(identity.SeriesID),
		ClassID:      strings.TrimSpace(identity.ClassID),
		FundName:     strings.TrimSpace(identity.FundName),
		Source:       strings.TrimSpace(identity.Source),
		EvidenceURL:  strings.TrimSpace(identity.EvidenceURL),
	}
}

func (h *AppHandler) ListWatchTargets(c *gin.Context) {
	page, pageSize := pageParams(c)
	upcomingEarnings := false
	if raw := strings.TrimSpace(c.Query("upcoming_earnings")); raw != "" {
		parsed, err := strconv.ParseBool(raw)
		if err != nil {
			Error(c, service.ErrValidation)
			return
		}
		upcomingEarnings = parsed
	}
	result, err := h.Targets.List(c.Request.Context(), service.WatchTargetFilter{
		Ticker:           c.Query("ticker"),
		Status:           c.Query("status"),
		TargetType:       c.Query("target_type"),
		Group:            c.Query("group"),
		Page:             page,
		PageSize:         pageSize,
		UpcomingEarnings: upcomingEarnings,
	})
	if err != nil {
		Error(c, err)
		return
	}
	items := make([]watchTargetWithTechnical, 0, len(result.Items))
	for _, target := range result.Items {
		technical := discovery.MissingCandidateTechnicalAnalysis()
		// The list must remain usable if the optional discovery database is
		// temporarily unavailable. In that case the UI clearly reports missing
		// price history instead of failing the core SEC monitoring page.
		if h.DiscoveryDB != nil {
			history, historyErr := discovery.GetTickerTechnicalHistory(c.Request.Context(), h.DiscoveryDB, target.Ticker)
			if historyErr == nil {
				technical = history.Technical
			}
		}
		items = append(items, watchTargetWithTechnical{WatchTarget: target, Technical: technical})
	}
	OK(c, watchTargetTechnicalPage{Items: items, Total: result.Total, Page: result.Page, PageSize: result.PageSize, Pages: result.Pages})
}

// ListWatchTargetEarningsPreviews returns cached data only. Keeping this
// separate from the target list allows the core SEC-monitoring screen to stay
// usable even while a market-data provider is degraded.
func (h *AppHandler) ListWatchTargetEarningsPreviews(c *gin.Context) {
	items, err := h.earningsPreviewService().List(c.Request.Context())
	if err != nil {
		Error(c, err)
		return
	}
	OK(c, items)
}

func (h *AppHandler) CreateWatchTarget(c *gin.Context) {
	var input service.WatchTargetInput
	if err := c.ShouldBindJSON(&input); err != nil {
		Error(c, err)
		return
	}
	target, err := h.Targets.Create(c.Request.Context(), input, operator(c))
	if err != nil {
		Error(c, err)
		return
	}
	Created(c, target)
}

func (h *AppHandler) GetWatchTarget(c *gin.Context) {
	target, err := h.Targets.Get(c.Request.Context(), uintParam(c, "id"))
	if err != nil {
		Error(c, err)
		return
	}
	OK(c, target)
}

func (h *AppHandler) GetWatchTargetEarningsPreview(c *gin.Context) {
	target, err := h.Targets.Get(c.Request.Context(), uintParam(c, "id"))
	if err != nil {
		Error(c, err)
		return
	}
	result, err := h.earningsPreviewService().Get(c.Request.Context(), target.ID)
	if err != nil {
		Error(c, err)
		return
	}
	OK(c, result)
}

func (h *AppHandler) RefreshWatchTargetEarningsPreview(c *gin.Context) {
	targetID := uintParam(c, "id")
	// This explicit operator action must outlive a browser navigation. It only
	// calls the Longbridge calendar/consensus endpoints for this target.
	result, err := h.earningsPreviewService().RefreshTarget(context.WithoutCancel(c.Request.Context()), targetID)
	if err != nil {
		Error(c, err)
		return
	}
	if h.Audit != nil {
		_ = h.Audit.Record(c.Request.Context(), operator(c), "refresh_earnings_preview", "watch_target", strconv.FormatUint(uint64(targetID), 10), nil, result)
	}
	OK(c, result)
}

func (h *AppHandler) GetWatchTargetTechnicalHistory(c *gin.Context) {
	target, err := h.Targets.Get(c.Request.Context(), uintParam(c, "id"))
	if err != nil {
		Error(c, err)
		return
	}
	result, err := discovery.GetTickerTechnicalHistory(c.Request.Context(), h.DiscoveryDB, target.Ticker)
	if err != nil {
		Error(c, err)
		return
	}
	OK(c, result)
}

func (h *AppHandler) BackfillWatchTargetTechnicalHistory(c *gin.Context) {
	target, err := h.Targets.Get(c.Request.Context(), uintParam(c, "id"))
	if err != nil {
		Error(c, err)
		return
	}
	var input struct {
		LookbackDays int `json:"lookback_days"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		Error(c, service.ErrValidation)
		return
	}
	result, err := h.discoverySyncService().BackfillTickerTechnicalHistory(c.Request.Context(), target.Ticker, input.LookbackDays)
	if err != nil {
		Error(c, err)
		return
	}
	OK(c, result)
}

func (h *AppHandler) UpdateWatchTarget(c *gin.Context) {
	var input service.WatchTargetInput
	if err := c.ShouldBindJSON(&input); err != nil {
		Error(c, err)
		return
	}
	target, err := h.Targets.Update(c.Request.Context(), uintParam(c, "id"), input, operator(c))
	if err != nil {
		Error(c, err)
		return
	}
	OK(c, target)
}

func (h *AppHandler) DeleteWatchTarget(c *gin.Context) {
	if err := h.Targets.Delete(c.Request.Context(), uintParam(c, "id"), operator(c)); err != nil {
		Error(c, err)
		return
	}
	NoContent(c)
}

func (h *AppHandler) SetWatchTargetStatus(c *gin.Context) {
	var input struct {
		Status string `json:"status"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		Error(c, err)
		return
	}
	target, err := h.Targets.SetStatus(c.Request.Context(), uintParam(c, "id"), input.Status, operator(c))
	if err != nil {
		Error(c, err)
		return
	}
	OK(c, target)
}

func (h *AppHandler) SyncWatchTarget(c *gin.Context) {
	result, err := h.Filings.RefreshTarget(c.Request.Context(), uintParam(c, "id"))
	if err != nil {
		Error(c, err)
		return
	}
	OK(c, result)
}

func (h *AppHandler) ListWatchTargetSyncDetails(c *gin.Context) {
	details, err := h.Filings.ListTargetSyncDetails(c.Request.Context(), uintParam(c, "id"), 3)
	if err != nil {
		Error(c, err)
		return
	}
	OK(c, details)
}

func (h *AppHandler) ListFilings(c *gin.Context) {
	page, pageSize := pageParams(c)
	filter := service.FilingFilter{
		Ticker:             c.Query("ticker"),
		CompanyName:        c.Query("company_name"),
		FilingType:         c.Query("filing_type"),
		NotificationStatus: c.Query("notification_status"),
		SortBy:             c.Query("sort_by"),
		SortOrder:          c.Query("sort_order"),
		Page:               page,
		PageSize:           pageSize,
	}
	if value := c.Query("date_from"); value != "" {
		if t, err := time.Parse("2006-01-02", value); err == nil {
			filter.DateFrom = &t
		}
	}
	if value := c.Query("date_to"); value != "" {
		if t, err := time.Parse("2006-01-02", value); err == nil {
			filter.DateTo = &t
		}
	}
	result, err := h.Filings.List(c.Request.Context(), filter)
	if err != nil {
		Error(c, err)
		return
	}
	OK(c, result)
}

func (h *AppHandler) GetFiling(c *gin.Context) {
	filing, err := h.Filings.Get(c.Request.Context(), uintParam(c, "id"))
	if err != nil {
		Error(c, err)
		return
	}
	OK(c, filing)
}

func (h *AppHandler) RefreshFilings(c *gin.Context) {
	result, err := h.Filings.Refresh(c.Request.Context())
	if err != nil {
		Error(c, err)
		return
	}
	OK(c, result)
}

func (h *AppHandler) GetIPORadarHealth(c *gin.Context) {
	if h.IPO == nil {
		Error(c, service.ErrValidation)
		return
	}
	result, err := h.IPO.Health(c.Request.Context(), time.Now().UTC())
	if err != nil {
		Error(c, err)
		return
	}
	OK(c, result)
}

func (h *AppHandler) ListIPORadarFilings(c *gin.Context) {
	if h.IPO == nil {
		Error(c, service.ErrValidation)
		return
	}
	page, pageSize := pageParams(c)
	result, err := h.IPO.List(c.Request.Context(), service.IPOFilingFilter{
		CompanyName: c.Query("company_name"),
		CIK:         c.Query("cik"),
		FilingType:  c.Query("filing_type"),
		Notified:    c.Query("notified"),
		Sort:        c.Query("sort"),
		Page:        page,
		PageSize:    pageSize,
	})
	if err != nil {
		Error(c, err)
		return
	}
	OK(c, result)
}

func (h *AppHandler) ListIPOCompanies(c *gin.Context) {
	if h.IPO == nil {
		Error(c, service.ErrValidation)
		return
	}
	page, pageSize := pageParams(c)
	includeEnded := false
	if value := strings.TrimSpace(c.Query("include_ended")); value != "" {
		parsed, err := strconv.ParseBool(value)
		if err != nil {
			Error(c, service.ErrValidation)
			return
		}
		includeEnded = parsed
	}
	result, err := h.IPO.ListCompanies(c.Request.Context(), service.IPOCompanyFilter{
		CompanyName:  c.Query("company_name"),
		CIK:          c.Query("cik"),
		Status:       c.Query("status"),
		Attention:    c.Query("attention"),
		IncludeEnded: includeEnded,
		SortBy:       c.Query("sort_by"),
		SortOrder:    c.Query("sort_order"),
		Page:         page,
		PageSize:     pageSize,
	}, time.Now().UTC())
	if err != nil {
		Error(c, err)
		return
	}
	OK(c, result)
}

func (h *AppHandler) ListIPOOfferingEvents(c *gin.Context) {
	if h.IPO == nil {
		Error(c, service.ErrValidation)
		return
	}
	page, pageSize := pageParams(c)
	result, err := h.IPO.ListOfferingEvents(c.Request.Context(), c.Param("cik"), page, pageSize)
	if err != nil {
		Error(c, err)
		return
	}
	OK(c, result)
}

func (h *AppHandler) UpdateIPOCompanyOverride(c *gin.Context) {
	if h.IPO == nil {
		Error(c, service.ErrValidation)
		return
	}
	var input service.IPOCompanyOverrideInput
	if err := c.ShouldBindJSON(&input); err != nil {
		Error(c, service.ErrValidation)
		return
	}
	result, err := h.IPO.UpsertCompanyOverride(c.Request.Context(), c.Param("cik"), input)
	if err != nil {
		Error(c, err)
		return
	}
	OK(c, result)
}

func (h *AppHandler) RefreshIPORadar(c *gin.Context) {
	if h.IPO == nil {
		Error(c, service.ErrValidation)
		return
	}
	result, err := h.IPO.Refresh(c.Request.Context())
	if err != nil {
		Error(c, err)
		return
	}
	OK(c, result)
}

func (h *AppHandler) ExportIPOCompaniesCSV(c *gin.Context) {
	if h.IPO == nil {
		Error(c, service.ErrValidation)
		return
	}
	result, err := h.IPO.ListCompanies(c.Request.Context(), service.IPOCompanyFilter{IncludeEnded: true, Page: 1, PageSize: 10000}, time.Now().UTC())
	if err != nil {
		Error(c, err)
		return
	}
	c.Header("Content-Type", "text/csv; charset=utf-8")
	c.Header("Content-Disposition", `attachment; filename="sec-monitor-ipo-companies.csv"`)
	writer := csv.NewWriter(c.Writer)
	_ = writer.Write([]string{"cik", "company_name", "status", "status_reason", "status_confidence", "status_source", "matched_ticker", "final_ticker", "exchange", "offer_price", "shares_offered", "gross_proceeds", "lifecycle_checked_at", "listed_verified_at", "listing_date", "market_data_source", "market_data_confidence", "market_data_updated_at", "filing_count", "first_filing_date", "latest_filing_date", "latest_filing_type", "latest_title", "latest_filing_url"})
	for _, item := range result.Items {
		listedVerifiedAt := ""
		if item.ListedVerifiedAt != nil {
			listedVerifiedAt = item.ListedVerifiedAt.Format(time.RFC3339)
		}
		lifecycleCheckedAt := ""
		if item.LifecycleCheckedAt != nil {
			lifecycleCheckedAt = item.LifecycleCheckedAt.Format(time.RFC3339)
		}
		listingDate := ""
		if item.ListingDate != nil {
			listingDate = item.ListingDate.Format("2006-01-02")
		}
		marketUpdatedAt := ""
		if item.MarketDataUpdatedAt != nil {
			marketUpdatedAt = item.MarketDataUpdatedAt.Format(time.RFC3339)
		}
		_ = writer.Write([]string{
			item.CIK,
			item.CompanyName,
			item.Status,
			item.StatusReason,
			item.StatusConfidence,
			item.StatusSource,
			item.MatchedTicker,
			item.FinalTicker,
			item.Exchange,
			item.OfferPrice,
			strconv.FormatInt(item.SharesOffered, 10),
			item.GrossProceeds,
			lifecycleCheckedAt,
			listedVerifiedAt,
			listingDate,
			item.MarketDataSource,
			item.MarketDataConfidence,
			marketUpdatedAt,
			strconv.Itoa(item.FilingCount),
			item.FirstFilingDate.Format("2006-01-02"),
			item.LatestFilingDate.Format("2006-01-02"),
			item.LatestFilingType,
			item.LatestTitle,
			item.LatestFilingURL,
		})
	}
	writer.Flush()
}

func (h *AppHandler) ExportIPORadarFilingsCSV(c *gin.Context) {
	var filings []model.IPOFiling
	if err := h.DB.WithContext(c.Request.Context()).Order("cik ASC, accepted_at ASC, filing_date ASC, id ASC").Find(&filings).Error; err != nil {
		Error(c, err)
		return
	}
	c.Header("Content-Type", "text/csv; charset=utf-8")
	c.Header("Content-Disposition", `attachment; filename="sec-monitor-ipo-filings.csv"`)
	writer := csv.NewWriter(c.Writer)
	_ = writer.Write([]string{"cik", "company_name", "filing_type", "filing_date", "accepted_at", "synced_at", "title", "filing_url", "filing_id"})
	for _, filing := range filings {
		acceptedAt := ""
		if filing.AcceptedAt != nil {
			acceptedAt = filing.AcceptedAt.Format(time.RFC3339)
		}
		_ = writer.Write([]string{
			filing.CIK,
			filing.CompanyName,
			filing.FilingType,
			filing.FilingDate.Format("2006-01-02"),
			acceptedAt,
			filing.CreatedAt.Format(time.RFC3339),
			filing.Title,
			filing.FilingURL,
			filing.FilingID,
		})
	}
	writer.Flush()
}

func (h *AppHandler) ListSyncRuns(c *gin.Context) {
	page, pageSize := pageParams(c)
	result, err := h.Filings.ListSyncRuns(c.Request.Context(), service.SyncRunFilter{
		Status:   c.Query("status"),
		Trigger:  c.Query("trigger"),
		Page:     page,
		PageSize: pageSize,
	})
	if err != nil {
		Error(c, err)
		return
	}
	OK(c, result)
}

func (h *AppHandler) ListSyncRunDetails(c *gin.Context) {
	details, err := h.Filings.ListSyncRunDetails(c.Request.Context(), uintParam(c, "id"))
	if err != nil {
		Error(c, err)
		return
	}
	OK(c, details)
}

func (h *AppHandler) PreviewFilingCleanup(c *gin.Context) {
	days, err := h.retentionDays(c.Request.Context())
	if err != nil {
		Error(c, err)
		return
	}
	preview, err := h.Filings.CleanupPreview(c.Request.Context(), days, time.Now().UTC())
	if err != nil {
		Error(c, err)
		return
	}
	OK(c, preview)
}

func (h *AppHandler) CleanupFilings(c *gin.Context) {
	days, err := h.retentionDays(c.Request.Context())
	if err != nil {
		Error(c, err)
		return
	}
	deleted, err := h.Filings.Cleanup(c.Request.Context(), days, time.Now().UTC())
	if err != nil {
		Error(c, err)
		return
	}
	OK(c, gin.H{"deleted": deleted})
}

// PreviewLifecycleCleanup presents expired diagnostics and superseded targeted
// market-repair snapshots that can be pruned without affecting filings, the
// current discovery candidates, price history, or research conclusions.
func (h *AppHandler) PreviewLifecycleCleanup(c *gin.Context) {
	if h.Lifecycle == nil {
		Error(c, fmt.Errorf("lifecycle service is not configured"))
		return
	}
	preview, err := h.Lifecycle.Preview(c.Request.Context(), time.Now().UTC())
	if err != nil {
		Error(c, err)
		return
	}
	OK(c, preview)
}

func (h *AppHandler) CleanupLifecycle(c *gin.Context) {
	if h.Lifecycle == nil {
		Error(c, fmt.Errorf("lifecycle service is not configured"))
		return
	}
	result, err := h.Lifecycle.Cleanup(c.Request.Context(), time.Now().UTC())
	if err != nil {
		Error(c, err)
		return
	}
	if h.Audit != nil {
		_ = h.Audit.Record(c.Request.Context(), "local_user", "cleanup", "operational_history", "retention", nil, result)
	}
	OK(c, result)
}

func (h *AppHandler) ListSystemConfigs(c *gin.Context) {
	configs, err := h.Configs.List(c.Request.Context(), c.Query("category"), true)
	if err != nil {
		Error(c, err)
		return
	}
	OK(c, configs)
}

func (h *AppHandler) UpdateSystemConfigs(c *gin.Context) {
	var input []service.ConfigInput
	if err := c.ShouldBindJSON(&input); err != nil {
		Error(c, err)
		return
	}
	if err := h.Configs.UpsertMany(c.Request.Context(), input, operator(c)); err != nil {
		Error(c, err)
		return
	}
	if h.Scheduler != nil {
		if err := h.Scheduler.Reload(c.Request.Context()); err != nil {
			Error(c, err)
			return
		}
	}
	configs, err := h.Configs.List(c.Request.Context(), "", true)
	if err != nil {
		Error(c, err)
		return
	}
	OK(c, configs)
}

func (h *AppHandler) GetTelegramConfig(c *gin.Context) {
	configs, err := h.Configs.List(c.Request.Context(), "telegram", true)
	if err != nil {
		Error(c, err)
		return
	}
	OK(c, configs)
}

func (h *AppHandler) UpdateTelegramConfig(c *gin.Context) {
	var input struct {
		BotToken   string `json:"bot_token"`
		ChatID     string `json:"chat_id"`
		Enabled    bool   `json:"enabled"`
		APIBaseURL string `json:"api_base_url"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		Error(c, err)
		return
	}
	configs := []service.ConfigInput{
		{Key: "telegram.chat_id", Value: input.ChatID, ValueType: "string", Category: "telegram"},
		{Key: "telegram.enabled", Value: strconv.FormatBool(input.Enabled), ValueType: "bool", Category: "telegram"},
		{Key: "telegram.api_base_url", Value: strings.TrimRight(strings.TrimSpace(input.APIBaseURL), "/"), ValueType: "string", Category: "telegram"},
	}
	if !service.IsMaskedSecret(input.BotToken) {
		configs = append(configs, service.ConfigInput{Key: "telegram.bot_token", Value: input.BotToken, ValueType: "string", Category: "telegram", Encrypted: true})
	}
	err := h.Configs.UpsertMany(c.Request.Context(), configs, operator(c))
	if err != nil {
		Error(c, err)
		return
	}
	h.GetTelegramConfig(c)
}

func (h *AppHandler) TestTelegram(c *gin.Context) {
	cfg, err := h.Configs.Telegram(c.Request.Context())
	if err != nil {
		Error(c, err)
		return
	}
	if service.IsMaskedSecret(cfg.BotToken) {
		Error(c, fmt.Errorf("%w: Bot Token 已被脱敏值覆盖，请重新输入真实 Token 并保存", service.ErrValidation))
		return
	}
	notifier := telegram.NewHTTPNotifier(cfg.BotToken, cfg.ChatID, 10*time.Second)
	notifier.BaseURL = cfg.APIBaseURL
	err = notifier.Send(c.Request.Context(), telegram.Message{Text: "SEC Monitor test message"})
	if err != nil {
		Error(c, fmt.Errorf("%s", service.SanitizeSensitiveError(err.Error())))
		return
	}
	OK(c, gin.H{"sent": true})
}

func (h *AppHandler) ListOperationLogs(c *gin.Context) {
	page, pageSize := pageParams(c)
	result, err := h.Audit.List(c.Request.Context(), service.AuditLogFilter{
		Action:     c.Query("action"),
		ObjectType: c.Query("object_type"),
		Page:       page,
		PageSize:   pageSize,
	})
	if err != nil {
		Error(c, err)
		return
	}
	OK(c, result)
}

func (h *AppHandler) ListNotificationLogs(c *gin.Context) {
	page, pageSize := pageParams(c)
	result, err := h.Notification.List(c.Request.Context(), service.NotificationLogFilter{
		Status:   c.Query("status"),
		Channel:  c.Query("channel"),
		Page:     page,
		PageSize: pageSize,
	})
	if err != nil {
		Error(c, err)
		return
	}
	OK(c, result)
}

func (h *AppHandler) ListNotificationBatches(c *gin.Context) {
	page, pageSize := pageParams(c)
	filter := service.NotificationBatchFilter{
		Source: c.Query("source"), Status: c.Query("status"), Trigger: c.Query("trigger"),
		Page: page, PageSize: pageSize,
	}
	if value := c.Query("date_from"); value != "" {
		if parsed, err := time.Parse("2006-01-02", value); err == nil {
			filter.DateFrom = &parsed
		}
	}
	if value := c.Query("date_to"); value != "" {
		if parsed, err := time.Parse("2006-01-02", value); err == nil {
			filter.DateTo = &parsed
		}
	}
	result, err := h.NotificationBatch.List(c.Request.Context(), filter)
	if err != nil {
		Error(c, err)
		return
	}
	OK(c, result)
}

func (h *AppHandler) ListNotificationBatchItems(c *gin.Context) {
	page, pageSize := pageParams(c)
	result, err := h.NotificationBatch.ListItems(c.Request.Context(), uintParam(c, "id"), page, pageSize)
	if err != nil {
		Error(c, err)
		return
	}
	OK(c, result)
}

func (h *AppHandler) RequeueNotificationBatch(c *gin.Context) {
	batch, err := h.NotificationBatch.Requeue(c.Request.Context(), uintParam(c, "id"), time.Now().UTC())
	if err != nil {
		Error(c, err)
		return
	}
	OK(c, batch)
}

func (h *AppHandler) ListTaskConfigs(c *gin.Context) {
	tasks, err := h.Tasks.List(c.Request.Context())
	if err != nil {
		Error(c, err)
		return
	}
	OK(c, tasks)
}

func (h *AppHandler) UpdateTaskConfig(c *gin.Context) {
	id := uintParam(c, "id")
	var input service.TaskConfigInput
	if err := c.ShouldBindJSON(&input); err != nil {
		Error(c, err)
		return
	}
	task, err := h.Tasks.Update(c.Request.Context(), id, input, operator(c))
	if err != nil {
		Error(c, err)
		return
	}
	if h.Scheduler != nil {
		if err := h.Scheduler.Reload(c.Request.Context()); err != nil {
			Error(c, err)
			return
		}
		// Reload computes and persists the schedule preview. Return the fresh
		// row so callers do not briefly render the old next_run_at value.
		if refreshed, getErr := h.Tasks.Get(c.Request.Context(), id); getErr == nil {
			task = refreshed
		}
	}
	OK(c, task)
}

func (h *AppHandler) RunTask(c *gin.Context) {
	if h.Scheduler != nil {
		task, err := h.Tasks.Get(c.Request.Context(), uintParam(c, "id"))
		if err != nil {
			Error(c, err)
			return
		}
		if err := h.Scheduler.RunTask(context.Background(), task.TaskName); err != nil {
			var partial *service.TaskPartialError
			if errors.As(err, &partial) {
				OK(c, gin.H{"started": true, "status": "partial", "message": partial.Reason})
				return
			}
			Error(c, err)
			return
		}
		OK(c, gin.H{"started": true})
		return
	}
	if h.Tasks != nil {
		task, err := h.Tasks.Get(c.Request.Context(), uintParam(c, "id"))
		if err == nil && task.TaskName == "ipo_radar_sync" && h.IPO != nil {
			result, err := h.IPO.RefreshWithTrigger(context.Background(), "ipo_manual")
			if err != nil {
				Error(c, err)
				return
			}
			OK(c, result)
			return
		}
	}
	result, err := h.Filings.Refresh(context.Background())
	if err != nil {
		Error(c, err)
		return
	}
	OK(c, result)
}

func (h *AppHandler) ListHealth(c *gin.Context) {
	var targetTotal int64
	var enabledTargets int64
	var filingTotal int64
	var notificationFailures int64
	var unstableTasks []model.TaskConfig
	_ = h.DB.WithContext(c.Request.Context()).Model(&model.WatchTarget{}).Count(&targetTotal).Error
	_ = h.DB.WithContext(c.Request.Context()).Model(&model.WatchTarget{}).Where("status = ?", "enabled").Count(&enabledTargets).Error
	_ = h.DB.WithContext(c.Request.Context()).Model(&model.Filing{}).Count(&filingTotal).Error
	_ = h.DB.WithContext(c.Request.Context()).Model(&model.NotificationLog{}).Where("status = ?", "failed").Count(&notificationFailures).Error
	_ = h.DB.WithContext(c.Request.Context()).Where("consecutive_failures >= ?", 3).Order("consecutive_failures DESC, task_name ASC").Find(&unstableTasks).Error

	var latestSync model.SyncRun
	_ = h.DB.WithContext(c.Request.Context()).Order("started_at DESC, id DESC").First(&latestSync).Error

	telegramCfg, _ := h.Configs.Telegram(c.Request.Context())
	dbSize := int64(0)
	dbPath := h.Runtime.Database.DSN
	storage := gin.H{}
	if strings.EqualFold(h.Runtime.Database.Type, "sqlite") {
		if info, err := os.Stat(dbPath); err == nil {
			dbSize = info.Size()
			if abs, err := filepath.Abs(dbPath); err == nil {
				dbPath = abs
			}
		}
	}
	if usage, err := filesystemUsage(dbPath); err == nil {
		storage = gin.H{"path": dbPath, "used_bytes": usage.usedBytes, "total_bytes": usage.totalBytes, "used_pct": usage.usedPct}
	}

	secUserAgent := strings.TrimSpace(h.Runtime.SEC.UserAgent)
	issues := []gin.H{}
	if secUserAgent == "" || strings.Contains(secUserAgent, "contact@example.com") {
		issues = append(issues, gin.H{"level": "warning", "message": "SEC User-Agent 仍是默认值，建议设置成包含联系方式的描述性值"})
	}
	if targetTotal == 0 {
		issues = append(issues, gin.H{"level": "info", "message": "还没有监控标的"})
	}
	if latestSync.ID == 0 {
		issues = append(issues, gin.H{"level": "warning", "message": "还没有同步记录"})
	}
	if notificationFailures > 0 {
		issues = append(issues, gin.H{"level": "warning", "message": "存在失败的通知记录"})
	}
	if usedPct, ok := storage["used_pct"].(int); ok {
		warningPct := 80
		if value, exists, err := h.Configs.GetValue(c.Request.Context(), "system.storage_warning_pct"); err == nil && exists {
			if parsed, parseErr := strconv.Atoi(strings.TrimSpace(value)); parseErr == nil && parsed > 0 && parsed <= 100 {
				warningPct = parsed
			}
		}
		if usedPct >= warningPct {
			issues = append(issues, gin.H{"level": "warning", "message": fmt.Sprintf("数据库所在磁盘已使用 %d%%，达到 %d%% 告警阈值", usedPct, warningPct)})
		}
	}
	for _, task := range unstableTasks {
		issues = append(issues, gin.H{"level": "warning", "message": fmt.Sprintf("调度任务 %s 已连续失败 %d 次：%s", task.TaskName, task.ConsecutiveFailures, task.LastErrorMessage)})
	}
	encryptionHealth := h.Configs.EncryptionHealth()
	backupHealth := service.SQLiteBackupHealth{}
	var recoveryDrill model.RecoveryDrill
	if h.Backup != nil {
		if value, err := h.Backup.Health(c.Request.Context()); err != nil {
			issues = append(issues, gin.H{"level": "warning", "message": "无法读取 SQLite 备份状态：" + err.Error()})
		} else {
			backupHealth = value
			if value.LatestCompleted == nil {
				issues = append(issues, gin.H{"level": "warning", "message": "尚无完整 SQLite 备份；可在调度任务中立即执行 sqlite_backup"})
			} else if time.Since(*value.LatestCompleted) > 30*time.Hour {
				issues = append(issues, gin.H{"level": "warning", "message": "SQLite 备份已超过 30 小时未更新"})
			}
			if value.IncompletePairs > 0 {
				issues = append(issues, gin.H{"level": "warning", "message": fmt.Sprintf("发现 %d 组不完整 SQLite 备份；系统不会将其用作恢复点", value.IncompletePairs)})
			}
		}
		if drill, err := h.Backup.LatestRecoveryDrill(c.Request.Context()); err != nil {
			issues = append(issues, gin.H{"level": "warning", "message": "无法读取 SQLite 恢复演练状态：" + service.SanitizeSensitiveError(err.Error())})
		} else {
			recoveryDrill = drill
			if drill.ID == 0 {
				issues = append(issues, gin.H{"level": "warning", "message": "尚未执行 SQLite 恢复演练；建议在系统健康页完成一次只读校验"})
			} else if drill.Status != "ready" {
				issues = append(issues, gin.H{"level": "critical", "message": "最近 SQLite 恢复演练失败：" + service.SanitizeSensitiveError(drill.ErrorMessage)})
			} else if time.Since(drill.StartedAt) > 8*24*time.Hour {
				issues = append(issues, gin.H{"level": "warning", "message": "SQLite 恢复演练已超过 8 天未执行"})
			}
		}
	}
	if encryptionHealth.Status == "critical" {
		issues = append(issues, gin.H{"level": "critical", "message": encryptionHealth.Message})
	}
	dataSources, sourceIssues := h.dataSourceHealth(c.Request.Context())
	issues = append(issues, sourceIssues...)

	status := "ok"
	if encryptionHealth.Status == "critical" {
		status = "critical"
	} else if len(issues) > 0 {
		status = "warning"
	}
	OK(c, gin.H{
		"status":                status,
		"issues":                issues,
		"target_total":          targetTotal,
		"enabled_targets":       enabledTargets,
		"filing_total":          filingTotal,
		"notification_failures": notificationFailures,
		"telegram_enabled":      telegramCfg.Enabled,
		"sec_user_agent":        secUserAgent,
		"database_type":         h.Runtime.Database.Type,
		"database_path":         dbPath,
		"database_size_bytes":   dbSize,
		"storage":               storage,
		"latest_sync":           latestSync,
		"encryption":            encryptionHealth,
		"backup":                backupHealth,
		"recovery_drill":        recoveryDrill,
		"data_sources":          dataSources,
	})
}

// GetOperationalHealth returns a read-only operational report assembled from
// persisted task, retry-queue, and provider state. It never calls SEC or a
// market-data provider.
func (h *AppHandler) GetOperationalHealth(c *gin.Context) {
	if h.OperationalHealth == nil {
		Error(c, fmt.Errorf("operational health service is not configured"))
		return
	}
	report, err := h.OperationalHealth.Report(c.Request.Context())
	if err != nil {
		Error(c, err)
		return
	}
	OK(c, report)
}

func (h *AppHandler) NotifyOperationalHealth(c *gin.Context) {
	if h.OperationalHealth == nil {
		Error(c, fmt.Errorf("operational health service is not configured"))
		return
	}
	result, err := h.OperationalHealth.Notify(c.Request.Context())
	if err != nil {
		if errors.Is(err, service.ErrTaskSkipped) {
			OK(c, result)
			return
		}
		Error(c, err)
		return
	}
	OK(c, result)
}

func (h *AppHandler) dataSourceHealth(ctx context.Context) ([]dataSourceHealthItem, []gin.H) {
	items := make([]dataSourceHealthItem, 0, 4)
	issues := make([]gin.H, 0, 4)
	appendIssue := func(item dataSourceHealthItem) {
		if item.Status != "warning" && item.Status != "critical" {
			return
		}
		level := item.Status
		message := fmt.Sprintf("数据源 %s 需要关注：%s", item.Source, item.Detail)
		if item.ErrorMessage != "" {
			message += "；" + item.ErrorMessage
		}
		issues = append(issues, gin.H{"level": level, "message": message})
	}

	secItem := dataSourceHealthItem{Source: "SEC EDGAR", Kind: "sec", Status: "unknown", Detail: "尚无 SEC 同步任务记录", RecommendedAction: "scheduler"}
	if h.DB != nil {
		var task model.TaskConfig
		if err := h.DB.WithContext(ctx).Where("task_name = ?", "sec_filing_sync").First(&task).Error; err == nil {
			secItem.LastCheckedAt = task.LastRunAt
			secItem.FailureStreak = task.ConsecutiveFailures
			secItem.ErrorMessage = service.SanitizeSensitiveError(task.LastErrorMessage)
			switch task.LastStatus {
			case "success", "skipped":
				secItem.Status = "ok"
				secItem.Detail = "最近 SEC 增量同步正常"
			case "running":
				secItem.Status = "unknown"
				secItem.Detail = "SEC 增量同步正在运行"
			case "failed", "interrupted", "partial":
				secItem.Status = "warning"
				if task.LastStatus == "partial" {
					secItem.Detail = "最近 SEC 增量同步部分完成，个别标的待重试或处理"
				} else {
					secItem.Detail = "最近 SEC 增量同步未完成"
				}
				if task.ConsecutiveFailures >= 3 {
					secItem.Status = "critical"
				}
				secItem.RecommendedAction = "scheduler"
			default:
				secItem.Detail = "SEC 增量同步尚未执行"
			}
		}
	}
	if userAgent := strings.TrimSpace(h.Runtime.SEC.UserAgent); (userAgent == "" || strings.Contains(userAgent, "contact@example.com")) && secItem.Status != "critical" && secItem.Status != "warning" {
		secItem.Status = "warning"
		secItem.Detail = "SEC User-Agent 仍是默认值，建议设置包含联系方式的描述性值"
		secItem.RecommendedAction = "configs"
	}
	items = append(items, secItem)
	appendIssue(secItem)

	if h.DiscoveryDB == nil {
		return items, issues
	}
	var providerHealth []discovery.ProviderHealth
	if err := h.DiscoveryDB.WithContext(ctx).Order("provider ASC").Find(&providerHealth).Error; err != nil {
		issues = append(issues, gin.H{"level": "warning", "message": "无法读取行情数据源健康状态：" + service.SanitizeSensitiveError(err.Error())})
		return items, issues
	}
	if len(providerHealth) == 0 {
		items = append(items, dataSourceHealthItem{Source: "行情数据源", Kind: "market", Status: "unknown", Detail: "尚无已验证的行情 Provider 运行记录", RecommendedAction: "discovery_logs"})
		return items, issues
	}
	for _, health := range providerHealth {
		item := dataSourceHealthItem{
			Source:            health.Provider,
			Kind:              "market",
			Status:            marketSourceStatus(health),
			LastCheckedAt:     &health.UpdatedAt,
			FailureStreak:     health.FailureStreak,
			Detail:            marketSourceDetail(health),
			RecommendedAction: "discovery_logs",
		}
		var latest discovery.ProviderRun
		if err := h.DiscoveryDB.WithContext(ctx).Where("provider = ?", health.Provider).Order("created_at DESC, id DESC").First(&latest).Error; err == nil {
			item.LastCheckedAt = &latest.CreatedAt
			coverage := latest.CoveragePct
			item.CoveragePct = &coverage
			item.ErrorMessage = service.SanitizeSensitiveError(latest.ErrorMessage)
			if item.ErrorMessage != "" && item.Status == "ok" {
				item.Status = "warning"
				item.Detail = "最近行情 Provider 运行存在错误"
			}
		}
		items = append(items, item)
		appendIssue(item)
	}
	return items, issues
}

func marketSourceStatus(health discovery.ProviderHealth) string {
	if health.FailureStreak >= 3 || health.Status == discovery.ProviderStatusFailed {
		return "critical"
	}
	if health.FailureStreak > 0 || health.Status == discovery.ProviderStatusDegraded || health.Status == discovery.ProviderStatusValidation {
		return "warning"
	}
	if health.Status == discovery.ProviderStatusActive {
		return "ok"
	}
	return "unknown"
}

func marketSourceDetail(health discovery.ProviderHealth) string {
	switch health.Status {
	case discovery.ProviderStatusActive:
		return "行情 Provider 已验证并处于正常状态"
	case discovery.ProviderStatusDegraded:
		return "行情 Provider 已降级，后续数据源或本地回退可能正在补齐"
	case discovery.ProviderStatusFailed:
		return "行情 Provider 最近运行失败"
	case discovery.ProviderStatusValidation:
		return "行情 Provider 尚在验证窗口，尚未进入稳定状态"
	default:
		return "行情 Provider 尚无可用健康状态"
	}
}

func (h *AppHandler) VerifyLatestSQLiteBackup(c *gin.Context) {
	if h.Backup == nil {
		Error(c, fmt.Errorf("SQLite backup service is not configured"))
		return
	}
	result, err := h.Backup.VerifyLatest(c.Request.Context())
	if err != nil {
		Error(c, err)
		return
	}
	OK(c, result)
}

func (h *AppHandler) CheckSQLiteRecoveryReadiness(c *gin.Context) {
	if h.Backup == nil {
		Error(c, fmt.Errorf("SQLite backup service is not configured"))
		return
	}
	result, err := h.Backup.CheckRecoveryReadiness(c.Request.Context())
	if err != nil {
		Error(c, err)
		return
	}
	OK(c, result)
}

// CompactSQLiteDatabases is an explicitly manual, low-traffic maintenance
// operation. The service creates a new verified backup pair before VACUUM can
// rewrite either live SQLite file.
func (h *AppHandler) CompactSQLiteDatabases(c *gin.Context) {
	if h.Backup == nil {
		Error(c, fmt.Errorf("SQLite backup service is not configured"))
		return
	}
	result, err := h.Backup.Compact(c.Request.Context())
	if h.Audit != nil {
		_ = h.Audit.Record(c.Request.Context(), "local_user", "compact", "sqlite_databases", "manual", nil, result)
	}
	if err != nil {
		Error(c, err)
		return
	}
	OK(c, result)
}

func (h *AppHandler) GetLatestSQLiteCompaction(c *gin.Context) {
	if h.Backup == nil {
		Error(c, fmt.Errorf("SQLite backup service is not configured"))
		return
	}
	result, err := h.Backup.LatestCompaction(c.Request.Context())
	if err != nil {
		Error(c, err)
		return
	}
	OK(c, result)
}

type filesystemUsageSnapshot struct {
	usedBytes  int64
	totalBytes int64
	usedPct    int
}

func filesystemUsage(path string) (filesystemUsageSnapshot, error) {
	if path == "" {
		return filesystemUsageSnapshot{}, fmt.Errorf("storage path is empty")
	}
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return filesystemUsageSnapshot{}, err
	}
	total := uint64(stat.Blocks) * uint64(stat.Bsize)
	available := uint64(stat.Bavail) * uint64(stat.Bsize)
	if total == 0 || available > total {
		return filesystemUsageSnapshot{}, fmt.Errorf("invalid filesystem capacity")
	}
	used := total - available
	return filesystemUsageSnapshot{usedBytes: int64(used), totalBytes: int64(total), usedPct: int((used * 100) / total)}, nil
}

func (h *AppHandler) ExportFilingsCSV(c *gin.Context) {
	var filings []model.Filing
	if err := h.DB.WithContext(c.Request.Context()).Order("filing_date DESC, id DESC").Find(&filings).Error; err != nil {
		Error(c, err)
		return
	}
	c.Header("Content-Type", "text/csv; charset=utf-8")
	c.Header("Content-Disposition", `attachment; filename="sec-monitor-filings.csv"`)
	writer := csv.NewWriter(c.Writer)
	_ = writer.Write([]string{"ticker", "company_name", "filing_type", "filing_date", "published_at", "pulled_at", "title", "filing_url", "filing_id"})
	for _, filing := range filings {
		publishedAt := ""
		if filing.PublishedAt != nil {
			publishedAt = filing.PublishedAt.Format(time.RFC3339)
		}
		_ = writer.Write([]string{
			filing.Ticker,
			filing.CompanyName,
			filing.FilingType,
			filing.FilingDate.Format("2006-01-02"),
			publishedAt,
			filing.PulledAt.Format(time.RFC3339),
			filing.Title,
			filing.FilingURL,
			filing.FilingID,
		})
	}
	writer.Flush()
}

func (h *AppHandler) ExportTargetsCSV(c *gin.Context) {
	var targets []model.WatchTarget
	if err := h.DB.WithContext(c.Request.Context()).Order("ticker ASC").Find(&targets).Error; err != nil {
		Error(c, err)
		return
	}
	c.Header("Content-Type", "text/csv; charset=utf-8")
	c.Header("Content-Disposition", `attachment; filename="sec-monitor-targets.csv"`)
	writer := csv.NewWriter(c.Writer)
	_ = writer.Write([]string{"ticker", "company_name", "cik", "target_type", "group", "status", "last_sync_at", "last_sync_status", "last_new_filings"})
	for _, target := range targets {
		lastSyncAt := ""
		if target.LastSyncAt != nil {
			lastSyncAt = target.LastSyncAt.Format(time.RFC3339)
		}
		_ = writer.Write([]string{
			target.Ticker,
			target.CompanyName,
			target.CIK,
			target.TargetType,
			target.Group,
			target.Status,
			lastSyncAt,
			target.LastSyncStatus,
			strconv.Itoa(target.LastNewFilings),
		})
	}
	writer.Flush()
}

func (h *AppHandler) ExportConfigsJSON(c *gin.Context) {
	configs, err := h.Configs.List(c.Request.Context(), "", true)
	if err != nil {
		Error(c, err)
		return
	}
	c.Header("Content-Type", "application/json; charset=utf-8")
	c.Header("Content-Disposition", `attachment; filename="sec-monitor-configs.json"`)
	_ = json.NewEncoder(c.Writer).Encode(configs)
}

func (h *AppHandler) ExportBackupJSON(c *gin.Context) {
	var targets []model.WatchTarget
	var filings []model.Filing
	var tasks []model.TaskConfig
	configs, err := h.Configs.List(c.Request.Context(), "", true)
	if err != nil {
		Error(c, err)
		return
	}
	if err := h.DB.WithContext(c.Request.Context()).Order("ticker ASC").Find(&targets).Error; err != nil {
		Error(c, err)
		return
	}
	if err := h.DB.WithContext(c.Request.Context()).Order("filing_date DESC, id DESC").Find(&filings).Error; err != nil {
		Error(c, err)
		return
	}
	if err := h.DB.WithContext(c.Request.Context()).Order("task_name ASC").Find(&tasks).Error; err != nil {
		Error(c, err)
		return
	}
	c.Header("Content-Type", "application/json; charset=utf-8")
	c.Header("Content-Disposition", `attachment; filename="sec-monitor-backup.json"`)
	_ = json.NewEncoder(c.Writer).Encode(gin.H{
		"exported_at": time.Now().UTC(),
		"targets":     targets,
		"filings":     filings,
		"tasks":       tasks,
		"configs":     configs,
	})
}

func pageParams(c *gin.Context) (int, int) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	return page, pageSize
}

func uintParam(c *gin.Context, name string) uint {
	value, _ := strconv.ParseUint(c.Param(name), 10, 64)
	return uint(value)
}

func splitQueryValues(values []string, csv string) []string {
	out := []string{}
	seen := map[string]struct{}{}
	add := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		if _, ok := seen[value]; ok {
			return
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			add(part)
		}
	}
	for _, part := range strings.Split(csv, ",") {
		add(part)
	}
	return out
}

func operator(c *gin.Context) string {
	value := c.GetHeader("X-Operator")
	if value == "" {
		return "anonymous"
	}
	return value
}

func (h *AppHandler) retentionDays(ctx context.Context) (int, error) {
	raw, ok, err := h.Configs.GetValue(ctx, "system.data_retention_days")
	if err != nil {
		return 0, err
	}
	if !ok || strings.TrimSpace(raw) == "" {
		return 30, nil
	}
	days, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%w: system.data_retention_days must be a number", service.ErrValidation)
	}
	return days, nil
}
