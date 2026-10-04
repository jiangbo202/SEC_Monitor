package handler

import (
	"context"
	"errors"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"sec_monitor/internal/discovery"
	"sec_monitor/internal/service"
	"strconv"
	"strings"
	"time"
)

type tickerEvaluationRequest struct {
	Ticker     string `json:"ticker"`
	TargetType string `json:"target_type"`
}

// EvaluateTicker performs one explicit, persisted assessment of a symbol. The
// endpoint intentionally refreshes price history; the history endpoint remains
// read-only so opening a prior result never consumes market-data quota.
func (h *AppHandler) EvaluateTicker(c *gin.Context) {
	var request tickerEvaluationRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		Error(c, service.ErrValidation)
		return
	}
	ticker := strings.ToUpper(strings.TrimSpace(request.Ticker))
	if ticker == "" {
		Error(c, service.ErrValidation)
		return
	}
	// Cache-first is important here: existing candidates and monitored symbols
	// already have an auditable local result. Do not spend SEC/Longbridge quota
	// merely because a user asks to view a symbol that the system knows.
	if cached, found, err := h.cachedTickerEvaluation(c.Request.Context(), ticker); err != nil {
		Error(c, err)
		return
	} else if found {
		OK(c, h.discoverySyncService().HydrateTickerEvaluationResearch(c.Request.Context(), cached))
		return
	}
	if recent, found, err := h.recentTickerEvaluation(c.Request.Context(), ticker); err != nil {
		Error(c, err)
		return
	} else if found {
		OK(c, h.discoverySyncService().HydrateTickerEvaluationResearch(c.Request.Context(), recent))
		return
	}
	if !h.beginTickerEvaluation(ticker) {
		Error(c, service.TaskAlreadyRunning("ticker_evaluation:"+ticker))
		return
	}
	defer h.finishTickerEvaluation(ticker)
	// Check once more after obtaining the per-symbol lock. Another request may
	// have completed between the first check and lock acquisition.
	if recent, found, err := h.recentTickerEvaluation(c.Request.Context(), ticker); err != nil {
		Error(c, err)
		return
	} else if found {
		OK(c, h.discoverySyncService().HydrateTickerEvaluationResearch(c.Request.Context(), recent))
		return
	}
	resolved := tickerLookupResponse{Ticker: ticker, TargetType: "stock"}
	if strings.EqualFold(strings.TrimSpace(request.TargetType), "etf") {
		fund, err := h.lookupFundTicker(context.WithoutCancel(c.Request.Context()), ticker)
		if err != nil {
			Error(c, err)
			return
		}
		resolved = fund
	} else {
		cik, companyName, err := h.SEC.LookupCIK(context.WithoutCancel(c.Request.Context()), ticker)
		if err != nil {
			fund, fundErr := h.lookupFundTicker(context.WithoutCancel(c.Request.Context()), ticker)
			if fundErr != nil || fund.FundIdentity == nil {
				Error(c, err)
				return
			}
			resolved = fund
		} else {
			resolved.CIK, resolved.CompanyName = cik, companyName
		}
	}
	if resolved.FundIdentity != nil {
		resolved.CIK = resolved.FundIdentity.CIK
		resolved.CompanyName = resolved.FundIdentity.FundName
	}
	// Fund-class resolution is intentionally strict because it is used by SEC
	// filing association. A valid ETF can still lack the series/class metadata,
	// though, so do not leave its human-readable name blank in an assessment.
	// The SEC ticker directory is a safe secondary identity source and does not
	// change the ETF's fundamental-data boundary.
	if resolved.TargetType == "etf" && strings.TrimSpace(resolved.CompanyName) == "" {
		if cik, name, lookupErr := h.SEC.LookupCIK(context.WithoutCancel(c.Request.Context()), ticker); lookupErr == nil {
			if resolved.CIK == "" {
				resolved.CIK = cik
			}
			resolved.CompanyName = strings.TrimSpace(name)
		}
	}
	result, err := h.discoverySyncService().EvaluateTicker(context.WithoutCancel(c.Request.Context()), service.TickerEvaluationRequest{Ticker: ticker, CIK: resolved.CIK, CompanyName: resolved.CompanyName, TargetType: resolved.TargetType})
	if err != nil {
		Error(c, err)
		return
	}
	OK(c, result)
}

func (h *AppHandler) beginTickerEvaluation(ticker string) bool {
	h.tickerEvaluationMu.Lock()
	defer h.tickerEvaluationMu.Unlock()
	if h.tickerEvaluations == nil {
		h.tickerEvaluations = make(map[string]struct{})
	}
	if _, exists := h.tickerEvaluations[ticker]; exists {
		return false
	}
	h.tickerEvaluations[ticker] = struct{}{}
	return true
}

func (h *AppHandler) finishTickerEvaluation(ticker string) {
	h.tickerEvaluationMu.Lock()
	defer h.tickerEvaluationMu.Unlock()
	delete(h.tickerEvaluations, ticker)
}

func (h *AppHandler) recentTickerEvaluation(ctx context.Context, ticker string) (discovery.TickerEvaluationResult, bool, error) {
	page, err := discovery.ListTickerEvaluations(ctx, h.DiscoveryDB, discovery.TickerEvaluationFilter{Ticker: ticker, Page: 1, PageSize: 1})
	if err != nil || len(page.Items) == 0 {
		return discovery.TickerEvaluationResult{}, false, err
	}
	result := page.Items[0]
	if result.EvaluatedAt.IsZero() || time.Since(result.EvaluatedAt) >= tickerEvaluationCooldown {
		return discovery.TickerEvaluationResult{}, false, nil
	}
	// Earlier ETF snapshots may predate display-name fallback. Re-evaluate them
	// immediately rather than preserving a blank historical label for the full
	// cooldown window.
	if result.TargetType == "etf" && strings.TrimSpace(result.CompanyName) == "" {
		return discovery.TickerEvaluationResult{}, false, nil
	}
	result.DataSource = "ad_hoc_evaluation_cooldown_cache"
	result.Warnings = append([]string{"同一标的已在 10 分钟内完成即时评估，直接返回该次结果以避免重复调用 SEC 与行情数据源。"}, result.Warnings...)
	return result, true, nil
}

func (h *AppHandler) cachedTickerEvaluation(ctx context.Context, ticker string) (discovery.TickerEvaluationResult, bool, error) {
	if h.DiscoveryDB != nil {
		page, err := discovery.ListCandidateScores(ctx, h.DiscoveryDB, discovery.CandidateScoreQuery{Ticker: ticker, Page: 1, PageSize: 1, SkipPerformance: true})
		if err != nil {
			return discovery.TickerEvaluationResult{}, false, err
		}
		if len(page.Items) > 0 {
			item := page.Items[0]
			companyName, cik := "", ""
			var security discovery.Security
			if err := h.DiscoveryDB.WithContext(ctx).First(&security, item.SecurityID).Error; err == nil {
				companyName, cik = security.CompanyName, security.CIK
			} else if !errors.Is(err, gorm.ErrRecordNotFound) {
				return discovery.TickerEvaluationResult{}, false, err
			}
			at := item.CreatedAt
			if at.IsZero() {
				at = time.Now().UTC()
			}
			return discovery.TickerEvaluationResult{Ticker: ticker, CIK: cik, CompanyName: companyName, TargetType: "stock", Status: discovery.TickerEvaluationStatusReady, DataSource: "candidate_cache", EvaluatedAt: at, Warnings: []string{"已直接返回当前小盘候选缓存；未发起 SEC 或行情补数请求。"}, CandidateScore: item, FundamentalStatus: "available"}, true, nil
		}
	}
	if h.Targets == nil {
		return discovery.TickerEvaluationResult{}, false, nil
	}
	targets, err := h.Targets.List(ctx, service.WatchTargetFilter{Ticker: ticker, Page: 1, PageSize: 20})
	if err != nil {
		return discovery.TickerEvaluationResult{}, false, err
	}
	for _, target := range targets.Items {
		if !strings.EqualFold(target.Ticker, ticker) {
			continue
		}
		prices, err := discovery.TickerEvaluationPriceHistory(ctx, h.DiscoveryDB, ticker)
		if err != nil {
			return discovery.TickerEvaluationResult{}, false, err
		}
		fundamentalStatus := "not_synced"
		if target.TargetType == "etf" {
			fundamentalStatus = "not_applicable"
		}
		result := discovery.BuildTickerEvaluationResult(ticker, target.CIK, target.CompanyName, target.TargetType, discovery.CandidateScoreSnapshot{Ticker: ticker}, discovery.FinancialMetricSnapshot{}, nil, prices, time.Now().UTC(), fundamentalStatus, []string{"已直接返回监控标的的本地行情缓存；未发起 SEC 或行情补数请求。", "该标的不在小盘候选评分缓存中，基本面和短线复核总分未生成。"})
		result.DataSource = "watch_target_cache"
		return result, true, nil
	}
	return discovery.TickerEvaluationResult{}, false, nil
}

func (h *AppHandler) ListTickerEvaluations(c *gin.Context) {
	page, pageSize := pageParams(c)
	result, err := discovery.ListTickerEvaluations(c.Request.Context(), h.DiscoveryDB, discovery.TickerEvaluationFilter{
		SummaryOnly: c.Query("view") == "summary", Ticker: c.Query("ticker"), EntryTrigger: c.Query("entry_trigger"), SortBy: c.Query("sort_by"), SortOrder: c.Query("sort_order"), Page: page, PageSize: pageSize,
	})
	if err != nil {
		Error(c, err)
		return
	}
	OK(c, result)
}

func (h *AppHandler) ListTickerEvaluationEntryTriggers(c *gin.Context) {
	items, err := discovery.ListTickerEvaluationEntryTriggers(c.Request.Context(), h.DiscoveryDB, c.Query("ticker"))
	if err != nil {
		Error(c, err)
		return
	}
	OK(c, items)
}

func (h *AppHandler) GetTickerEvaluation(c *gin.Context) {
	if h.DiscoveryDB == nil {
		Error(c, errors.New("database is required"))
		return
	}
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil || id == 0 {
		Error(c, service.ErrValidation)
		return
	}
	result, err := discovery.GetTickerEvaluation(c.Request.Context(), h.DiscoveryDB, uint(id))
	if errors.Is(err, gorm.ErrRecordNotFound) {
		err = service.ErrNotFound
	}
	if err != nil {
		Error(c, err)
		return
	}
	OK(c, result)
}
