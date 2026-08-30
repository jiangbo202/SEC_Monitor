package handler

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"sec_monitor/internal/discovery"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type insiderPlanItem struct {
	ID                   uint       `json:"id"`
	Ticker               string     `json:"ticker"`
	CompanyName          string     `json:"company_name"`
	OwnerName            string     `json:"owner_name"`
	OfficerTitle         string     `json:"officer_title"`
	AdoptionDate         time.Time  `json:"adoption_date"`
	AmendmentDate        *time.Time `json:"amendment_date"`
	TerminationDate      *time.Time `json:"termination_date"`
	ExpirationDate       *time.Time `json:"expiration_date"`
	Status               string     `json:"status"`
	EvidenceConfidence   string     `json:"evidence_confidence"`
	MaximumSharesKnown   bool       `json:"maximum_shares_known"`
	MaximumShares        float64    `json:"maximum_shares"`
	ExecutedShares       float64    `json:"executed_shares"`
	ExecutedValueUSD     float64    `json:"executed_value_usd"`
	RemainingSharesKnown bool       `json:"remaining_shares_known"`
	RemainingShares      float64    `json:"remaining_shares"`
	ExecutionCount       int        `json:"execution_count"`
	EvidenceCount        int        `json:"evidence_count"`
	FirstExecutionDate   *time.Time `json:"first_execution_date"`
	LastExecutionDate    *time.Time `json:"last_execution_date"`
	PrimarySourceForm    string     `json:"primary_source_form"`
	PrimarySourceURL     string     `json:"primary_source_url"`
	EvidenceSummary      string     `json:"evidence_summary"`
}

type insiderPlanPage struct {
	Items    []insiderPlanItem   `json:"items"`
	Total    int64               `json:"total"`
	Page     int                 `json:"page"`
	PageSize int                 `json:"page_size"`
	Coverage insiderPlanCoverage `json:"coverage"`
}

type insiderPlanCoverage struct {
	Status                    string     `json:"status"`
	RequiredParserVersion     string     `json:"required_parser_version"`
	ScopedTransactions        int64      `json:"scoped_transactions"`
	ParsedTransactions        int64      `json:"parsed_transactions"`
	ConfirmedPlanTransactions int64      `json:"confirmed_plan_transactions"`
	RegisteredPlans           int64      `json:"registered_plans"`
	CoveragePct               float64    `json:"coverage_pct"`
	LastSyncCompletedAt       *time.Time `json:"last_sync_completed_at"`
}

type insiderPlanTickerSummary struct {
	Source  string   `json:"source"`
	Tickers []string `json:"tickers"`
	Count   int      `json:"count"`
}

// GetInsiderTradingPlanTickers returns the distinct current-list symbols with
// an active/executing 10b5-1 plan. It is intentionally compact so list badges
// do not have to run the much heavier candidate hydration query.
func (h *AppHandler) GetInsiderTradingPlanTickers(c *gin.Context) {
	source := strings.TrimSpace(c.Query("source"))
	scopeTickers, err := h.currentInsiderScopeTickersBySource(c.Request.Context(), source)
	if err != nil {
		Error(c, err)
		return
	}
	activeTickers, err := discovery.ActiveInsiderPlanTickers(c.Request.Context(), h.DiscoveryDB)
	if err != nil {
		Error(c, err)
		return
	}
	active := make(map[string]bool, len(activeTickers))
	for _, ticker := range activeTickers {
		active[ticker] = true
	}
	result := make([]string, 0)
	for _, ticker := range scopeTickers {
		ticker = strings.ToUpper(strings.TrimSpace(ticker))
		if active[ticker] {
			result = append(result, ticker)
		}
	}
	sort.Strings(result)
	OK(c, insiderPlanTickerSummary{Source: source, Tickers: result, Count: len(result)})
}

// ListInsiderTradingPlans returns the local evidence-backed 10b5-1 registry.
// It never turns an unknown plan maximum into a derived remaining balance.
func (h *AppHandler) ListInsiderTradingPlans(c *gin.Context) {
	if h.DiscoveryDB == nil {
		Error(c, errors.New("research database is unavailable"))
		return
	}
	page, pageSize := pageParams(c)
	scopeTickers, err := h.currentInsiderScopeTickersBySource(c.Request.Context(), c.Query("source"))
	if err != nil {
		Error(c, err)
		return
	}
	query := h.DiscoveryDB.WithContext(c.Request.Context()).Model(&discovery.InsiderTradingPlan{})
	if len(scopeTickers) == 0 {
		query = query.Where("1 = 0")
	} else {
		query = query.Where(`security_id IN (
			SELECT security_id FROM listings WHERE ticker IN ?
			UNION SELECT security_id FROM security_batch_identities WHERE ticker IN ?
		)`, scopeTickers, scopeTickers)
	}
	if ticker := strings.ToUpper(strings.TrimSpace(c.Query("ticker"))); ticker != "" {
		query = query.Where(`security_id IN (
			SELECT security_id FROM listings WHERE ticker = ?
			UNION SELECT security_id FROM security_batch_identities WHERE ticker = ?
		)`, ticker, ticker)
	}
	if status := strings.TrimSpace(c.Query("status")); status != "" {
		query = query.Where("status = ?", status)
	}
	coverage, err := h.insiderPlanCoverage(c.Request.Context(), scopeTickers, strings.ToUpper(strings.TrimSpace(c.Query("ticker"))))
	if err != nil {
		Error(c, err)
		return
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		Error(c, err)
		return
	}
	var plans []discovery.InsiderTradingPlan
	if err := query.Preload("Security").Order("last_execution_date DESC, adoption_date DESC, id DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&plans).Error; err != nil {
		Error(c, err)
		return
	}
	securityIDs := make([]uint, 0, len(plans))
	for _, plan := range plans {
		securityIDs = append(securityIDs, plan.SecurityID)
	}
	tickers, err := latestTickerBySecurity(c.Request.Context(), h.DiscoveryDB, securityIDs)
	if err != nil {
		Error(c, err)
		return
	}
	items := make([]insiderPlanItem, 0, len(plans))
	for _, plan := range plans {
		items = append(items, insiderPlanItem{
			ID: plan.ID, Ticker: tickers[plan.SecurityID], CompanyName: plan.Security.CompanyName,
			OwnerName: plan.OwnerName, OfficerTitle: plan.OfficerTitle, AdoptionDate: plan.AdoptionDate,
			AmendmentDate: plan.AmendmentDate, TerminationDate: plan.TerminationDate, ExpirationDate: plan.ExpirationDate,
			Status: plan.Status, EvidenceConfidence: plan.EvidenceConfidence,
			MaximumSharesKnown: plan.MaximumSharesKnown, MaximumShares: float64(plan.MaximumSharesMicros) / 1_000_000,
			ExecutedShares: float64(plan.ExecutedSharesMicros) / 1_000_000, ExecutedValueUSD: float64(plan.ExecutedValueMicros) / 1_000_000,
			RemainingSharesKnown: plan.RemainingSharesKnown, RemainingShares: float64(plan.RemainingSharesMicros) / 1_000_000,
			ExecutionCount: plan.ExecutionCount, EvidenceCount: plan.EvidenceCount,
			FirstExecutionDate: plan.FirstExecutionDate, LastExecutionDate: plan.LastExecutionDate,
			PrimarySourceForm: plan.PrimarySourceForm, PrimarySourceURL: plan.PrimarySourceURL, EvidenceSummary: plan.EvidenceSummary,
		})
	}
	OK(c, insiderPlanPage{Items: items, Total: total, Page: page, PageSize: pageSize, Coverage: coverage})
}

func (h *AppHandler) insiderPlanCoverage(ctx context.Context, scopeTickers []string, ticker string) (insiderPlanCoverage, error) {
	coverage := insiderPlanCoverage{Status: "pending", RequiredParserVersion: discovery.InsiderParserVersion}
	if len(scopeTickers) == 0 {
		coverage.Status = "empty_scope"
		return coverage, nil
	}
	transactionQuery := func() *gorm.DB {
		query := h.DiscoveryDB.WithContext(ctx).Model(&discovery.InsiderTransactionSnapshot{}).Where(`security_id IN (
			SELECT security_id FROM listings WHERE ticker IN ?
			UNION SELECT security_id FROM security_batch_identities WHERE ticker IN ?
		)`, scopeTickers, scopeTickers)
		if ticker != "" {
			query = query.Where(`security_id IN (
				SELECT security_id FROM listings WHERE ticker = ?
				UNION SELECT security_id FROM security_batch_identities WHERE ticker = ?
			)`, ticker, ticker)
		}
		return query
	}
	if err := transactionQuery().Count(&coverage.ScopedTransactions).Error; err != nil {
		return coverage, err
	}
	if err := transactionQuery().Where("parser_version = ?", discovery.InsiderParserVersion).Count(&coverage.ParsedTransactions).Error; err != nil {
		return coverage, err
	}
	if err := transactionQuery().Where("parser_version = ? AND is_ten_b5_one = ? AND ten_b5_one_plan_adoption_date IS NOT NULL", discovery.InsiderParserVersion, true).Count(&coverage.ConfirmedPlanTransactions).Error; err != nil {
		return coverage, err
	}
	planQuery := h.DiscoveryDB.WithContext(ctx).Model(&discovery.InsiderTradingPlan{}).Where(`security_id IN (
		SELECT security_id FROM listings WHERE ticker IN ?
		UNION SELECT security_id FROM security_batch_identities WHERE ticker IN ?
	)`, scopeTickers, scopeTickers)
	if ticker != "" {
		planQuery = planQuery.Where(`security_id IN (
			SELECT security_id FROM listings WHERE ticker = ?
			UNION SELECT security_id FROM security_batch_identities WHERE ticker = ?
		)`, ticker, ticker)
	}
	if err := planQuery.Count(&coverage.RegisteredPlans).Error; err != nil {
		return coverage, err
	}
	var checkpoint discovery.SecuritySourceCheckpoint
	checkpointResult := h.DiscoveryDB.WithContext(ctx).Where("phase = ? AND status = ?", "security-insiders", "completed").Order("completed_at DESC").First(&checkpoint)
	if checkpointResult.Error == nil {
		coverage.LastSyncCompletedAt = checkpoint.CompletedAt
	} else if !errors.Is(checkpointResult.Error, gorm.ErrRecordNotFound) {
		return coverage, checkpointResult.Error
	}
	if coverage.ScopedTransactions == 0 {
		coverage.Status = "no_transactions"
		return coverage, nil
	}
	coverage.CoveragePct = float64(coverage.ParsedTransactions) * 100 / float64(coverage.ScopedTransactions)
	switch {
	case coverage.ParsedTransactions == 0:
		coverage.Status = "pending"
	case coverage.ParsedTransactions < coverage.ScopedTransactions:
		coverage.Status = "partial"
	case coverage.ConfirmedPlanTransactions == 0:
		coverage.Status = "complete_no_confirmed_plans"
	default:
		coverage.Status = "complete"
	}
	return coverage, nil
}

func latestTickerBySecurity(ctx context.Context, db *gorm.DB, securityIDs []uint) (map[uint]string, error) {
	result := map[uint]string{}
	if len(securityIDs) == 0 {
		return result, nil
	}
	var listings []discovery.Listing
	if err := db.WithContext(ctx).Where("security_id IN ?", securityIDs).Order("valid_from DESC, id DESC").Find(&listings).Error; err != nil {
		return nil, err
	}
	for _, row := range listings {
		if result[row.SecurityID] == "" {
			result[row.SecurityID] = row.Ticker
		}
	}
	var identities []discovery.SecurityBatchIdentity
	if err := db.WithContext(ctx).Where("security_id IN ?", securityIDs).Order("created_at DESC, id DESC").Find(&identities).Error; err != nil {
		return nil, err
	}
	for _, row := range identities {
		if result[row.SecurityID] == "" {
			result[row.SecurityID] = row.Ticker
		}
	}
	return result, nil
}
