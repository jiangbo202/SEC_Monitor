package handler

import (
	"context"
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

type insiderTransactionItem struct {
	ID                     uint       `json:"id"`
	Ticker                 string     `json:"ticker"`
	CompanyName            string     `json:"company_name"`
	OwnerName              string     `json:"owner_name"`
	OfficerTitle           string     `json:"officer_title"`
	Role                   string     `json:"role"`
	TransactionDate        time.Time  `json:"transaction_date"`
	TransactionCode        string     `json:"transaction_code"`
	AcquiredDisposedCode   string     `json:"acquired_disposed_code"`
	Direction              string     `json:"direction"`
	Derivative             bool       `json:"derivative"`
	Shares                 float64    `json:"shares"`
	PriceUSD               float64    `json:"price_usd"`
	ValueUSD               float64    `json:"value_usd"`
	SharesOwnedAfter       float64    `json:"shares_owned_after"`
	Qualified              bool       `json:"qualified"`
	ExclusionReason        string     `json:"exclusion_reason"`
	IsTenB5One             bool       `json:"is_10b5_1"`
	TenB5OneStatus         string     `json:"ten_b5_1_status"`
	TenB5OnePlanDate       *time.Time `json:"ten_b5_1_plan_adoption_date"`
	TenB5OneEvidence       string     `json:"ten_b5_1_evidence"`
	TenB5OneEvidenceSource string     `json:"ten_b5_1_evidence_source"`
	TenB5OnePlanID         *uint      `json:"ten_b5_1_plan_id"`
	TenB5OneLinkConfidence string     `json:"ten_b5_1_link_confidence"`
	ResearchInterpretation string     `json:"research_interpretation"`
	SourceURL              string     `json:"source_url"`
}

type insiderTransactionSummary struct {
	Transactions      int64   `json:"transactions"`
	Issuers           int64   `json:"issuers"`
	Purchases         int64   `json:"purchases"`
	Sales             int64   `json:"sales"`
	PricedPurchases   int64   `json:"priced_purchases"`
	PricedSales       int64   `json:"priced_sales"`
	OtherAcquisitions int64   `json:"other_acquisitions"`
	OtherDispositions int64   `json:"other_dispositions"`
	BuyValueUSD       float64 `json:"buy_value_usd"`
	SellValueUSD      float64 `json:"sell_value_usd"`
	NetValueUSD       float64 `json:"net_value_usd"`
	PlannedSales      int64   `json:"planned_sales"`
}

type insiderTransactionPage struct {
	Items    []insiderTransactionItem  `json:"items"`
	Total    int64                     `json:"total"`
	Page     int                       `json:"page"`
	PageSize int                       `json:"page_size"`
	Summary  insiderTransactionSummary `json:"summary"`
}

// ListInsiderTransactions exposes the normalized Form 4 facts already stored
// by small-cap discovery. It is deliberately local/read-only: opening the page
// never downloads SEC documents or consumes a provider quota.
func (h *AppHandler) ListInsiderTransactions(c *gin.Context) {
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
	query := h.insiderTransactionQuery(c, scopeTickers)
	var total int64
	if err := query.Count(&total).Error; err != nil {
		Error(c, err)
		return
	}

	var rows []discovery.InsiderTransactionSnapshot
	if err := query.Preload("Security").Order("transaction_date DESC, insider_transaction_snapshots.id DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&rows).Error; err != nil {
		Error(c, err)
		return
	}
	securityIDs := make([]uint, 0, len(rows))
	for _, row := range rows {
		securityIDs = append(securityIDs, row.SecurityID)
	}
	tickers := map[uint]string{}
	if len(securityIDs) > 0 {
		var listings []discovery.Listing
		if err := h.DiscoveryDB.WithContext(c.Request.Context()).Where("security_id IN ?", securityIDs).Order("valid_from DESC, id DESC").Find(&listings).Error; err != nil {
			Error(c, err)
			return
		}
		for _, listing := range listings {
			if _, ok := tickers[listing.SecurityID]; !ok {
				tickers[listing.SecurityID] = listing.Ticker
			}
		}
		// Published security batches are the canonical mapping source used by
		// discovery runs. Older Form 4 issuers may not have a row in the mutable
		// listings directory, so fall back to their latest batch identity.
		var identities []discovery.SecurityBatchIdentity
		if err := h.DiscoveryDB.WithContext(c.Request.Context()).Where("security_id IN ?", securityIDs).Order("created_at DESC, id DESC").Find(&identities).Error; err != nil {
			Error(c, err)
			return
		}
		for _, identity := range identities {
			if _, ok := tickers[identity.SecurityID]; !ok && strings.TrimSpace(identity.Ticker) != "" {
				tickers[identity.SecurityID] = identity.Ticker
			}
		}
	}
	items := make([]insiderTransactionItem, 0, len(rows))
	for _, row := range rows {
		direction := "other"
		if strings.EqualFold(row.AcquiredDisposedCode, "A") {
			direction = "buy"
		}
		if strings.EqualFold(row.AcquiredDisposedCode, "D") {
			direction = "sell"
		}
		items = append(items, insiderTransactionItem{
			ID: row.ID, Ticker: tickers[row.SecurityID], CompanyName: row.Security.CompanyName,
			OwnerName: row.OwnerName, OfficerTitle: row.OfficerTitle, Role: row.Role,
			TransactionDate: row.TransactionDate, TransactionCode: row.TransactionCode,
			AcquiredDisposedCode: row.AcquiredDisposedCode, Direction: direction, Derivative: row.Derivative,
			Shares: float64(row.SharesMicros) / 1_000_000, PriceUSD: float64(row.PriceMicros) / 1_000_000,
			ValueUSD: float64(row.ValueMicros) / 1_000_000, SharesOwnedAfter: float64(row.SharesOwnedAfterMicros) / 1_000_000,
			Qualified: row.Qualified, ExclusionReason: row.ExclusionReason, SourceURL: row.SourceURL,
			IsTenB5One: row.IsTenB5One, TenB5OneStatus: normalizedTenB5OneStatus(row),
			TenB5OnePlanDate: row.TenB5OnePlanAdoptionDate, TenB5OneEvidence: row.TenB5OneEvidence,
			TenB5OneEvidenceSource: row.TenB5OneEvidenceSource, ResearchInterpretation: insiderResearchInterpretation(row),
			TenB5OnePlanID: row.TenB5OnePlanID, TenB5OneLinkConfidence: row.TenB5OneLinkConfidence,
		})
	}
	summary, err := h.insiderTransactionSummary(c, h.insiderTransactionQuery(c, scopeTickers))
	if err != nil {
		Error(c, err)
		return
	}
	OK(c, insiderTransactionPage{Items: items, Total: total, Page: page, PageSize: pageSize, Summary: summary})
}

func (h *AppHandler) insiderTransactionQuery(c *gin.Context, scopeTickers []string) *gorm.DB {
	query := h.DiscoveryDB.WithContext(c.Request.Context()).Model(&discovery.InsiderTransactionSnapshot{})
	if len(scopeTickers) == 0 {
		query = query.Where("1 = 0")
	} else {
		query = query.Where(`security_id IN (
			SELECT security_id FROM listings WHERE ticker IN ?
			UNION
			SELECT security_id FROM security_batch_identities WHERE ticker IN ?
		)`, scopeTickers, scopeTickers)
	}
	if ticker := strings.ToUpper(strings.TrimSpace(c.Query("ticker"))); ticker != "" {
		query = query.Where(`security_id IN (
			SELECT security_id FROM listings WHERE ticker = ?
			UNION
			SELECT security_id FROM security_batch_identities WHERE ticker = ?
		)`, ticker, ticker)
	}
	if role := strings.TrimSpace(c.Query("role")); role != "" {
		query = query.Where("role = ?", role)
	}
	if direction := strings.TrimSpace(c.Query("direction")); direction == "buy" {
		query = query.Where("UPPER(acquired_disposed_code) = 'A'")
	} else if direction == "sell" {
		query = query.Where("UPPER(acquired_disposed_code) = 'D'")
	}
	if c.Query("qualified") == "true" {
		query = query.Where("qualified = ?", true)
	}
	if c.Query("qualified") == "false" {
		query = query.Where("qualified = ?", false)
	}
	if planStatus := strings.TrimSpace(c.Query("ten_b5_1_status")); planStatus != "" {
		switch planStatus {
		case discovery.TenB5OneStatusConfirmed:
			query = query.Where("is_ten_b5_one = ?", true)
		case discovery.TenB5OneStatusPossible:
			query = query.Where("ten_b5_one_status = ?", discovery.TenB5OneStatusPossible)
		case discovery.TenB5OneStatusNotDisclosed:
			query = query.Where("is_ten_b5_one = ? AND (ten_b5_one_status = ? OR ten_b5_one_status = '' OR ten_b5_one_status IS NULL)", false, discovery.TenB5OneStatusNotDisclosed)
		}
	}
	return query
}

func normalizedTenB5OneStatus(row discovery.InsiderTransactionSnapshot) string {
	if row.IsTenB5One {
		return discovery.TenB5OneStatusConfirmed
	}
	if strings.TrimSpace(row.TenB5OneStatus) == "" {
		return discovery.TenB5OneStatusNotDisclosed
	}
	return row.TenB5OneStatus
}

func insiderResearchInterpretation(row discovery.InsiderTransactionSnapshot) string {
	if !strings.EqualFold(row.AcquiredDisposedCode, "D") {
		return "acquisition_or_other"
	}
	switch normalizedTenB5OneStatus(row) {
	case discovery.TenB5OneStatusConfirmed:
		return "planned_sale_reduced_bearish"
	case discovery.TenB5OneStatusPossible:
		return "sale_plan_needs_verification"
	default:
		return "sale_plan_not_disclosed"
	}
}

// currentInsiderScopeTickers limits the research view to symbols the user is
// actively working with. Historical Form 4 facts remain stored for audit and
// become visible again if a symbol re-enters the current candidate pool or is
// re-enabled as a watch target.
func (h *AppHandler) currentInsiderScopeTickers(ctx context.Context) ([]string, error) {
	return h.currentInsiderScopeTickersBySource(ctx, "")
}

func (h *AppHandler) currentInsiderScopeTickersBySource(ctx context.Context, source string) ([]string, error) {
	source = strings.ToLower(strings.TrimSpace(source))
	switch source {
	case "", "all":
		source = "all"
	case "candidate", "watch":
	default:
		return nil, fmt.Errorf("%w: unsupported insider scope source %q", service.ErrValidation, source)
	}
	tickers := map[string]struct{}{}
	if source == "all" || source == "candidate" {
		var pointer discovery.CurrentBatchPointer
		err := h.DiscoveryDB.WithContext(ctx).First(&pointer, "kind = ?", discovery.BatchKindPrescreen).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
		if err == nil {
			var candidates []string
			if err := h.DiscoveryDB.WithContext(ctx).Model(&discovery.CandidateScoreSnapshot{}).
				Where("batch_id = ? AND grade IN ?", pointer.BatchID, []string{discovery.CandidateGradeA, discovery.CandidateGradeB}).
				Distinct("ticker").Pluck("ticker", &candidates).Error; err != nil {
				return nil, err
			}
			for _, ticker := range candidates {
				if ticker = strings.ToUpper(strings.TrimSpace(ticker)); ticker != "" {
					tickers[ticker] = struct{}{}
				}
			}
		}
	}
	if (source == "all" || source == "watch") && h.DB != nil {
		var targets []string
		if err := h.DB.WithContext(ctx).Model(&model.WatchTarget{}).
			Where("status = ?", "enabled").Distinct("ticker").Pluck("ticker", &targets).Error; err != nil {
			return nil, err
		}
		for _, ticker := range targets {
			if ticker = strings.ToUpper(strings.TrimSpace(ticker)); ticker != "" {
				tickers[ticker] = struct{}{}
			}
		}
	}
	result := make([]string, 0, len(tickers))
	for ticker := range tickers {
		result = append(result, ticker)
	}
	sort.Strings(result)
	return result, nil
}

func (h *AppHandler) insiderTransactionSummary(c *gin.Context, query *gorm.DB) (insiderTransactionSummary, error) {
	type aggregate struct {
		Transactions, Issuers, Purchases, Sales, PricedPurchases, PricedSales int64
		OtherAcquisitions, OtherDispositions, PlannedSales                    int64
		BuyValueUSD, SellValueUSD                                             float64
	}
	var value aggregate
	err := query.Select(`COUNT(*) AS transactions,
		COUNT(DISTINCT insider_transaction_snapshots.security_id) AS issuers,
		SUM(CASE WHEN UPPER(transaction_code) = 'P' AND UPPER(acquired_disposed_code) = 'A' AND derivative = 0 THEN 1 ELSE 0 END) AS purchases,
		SUM(CASE WHEN UPPER(transaction_code) = 'S' AND UPPER(acquired_disposed_code) = 'D' AND derivative = 0 THEN 1 ELSE 0 END) AS sales,
		SUM(CASE WHEN UPPER(transaction_code) = 'P' AND UPPER(acquired_disposed_code) = 'A' AND derivative = 0 AND price_micros > 0 AND value_micros > 0 THEN 1 ELSE 0 END) AS priced_purchases,
		SUM(CASE WHEN UPPER(transaction_code) = 'S' AND UPPER(acquired_disposed_code) = 'D' AND derivative = 0 AND price_micros > 0 AND value_micros > 0 THEN 1 ELSE 0 END) AS priced_sales,
		SUM(CASE WHEN UPPER(acquired_disposed_code) = 'A' AND NOT (UPPER(transaction_code) = 'P' AND derivative = 0) THEN 1 ELSE 0 END) AS other_acquisitions,
		SUM(CASE WHEN UPPER(acquired_disposed_code) = 'D' AND NOT (UPPER(transaction_code) = 'S' AND derivative = 0) THEN 1 ELSE 0 END) AS other_dispositions,
		SUM(CASE WHEN UPPER(transaction_code) = 'S' AND UPPER(acquired_disposed_code) = 'D' AND derivative = 0 AND is_ten_b5_one = 1 THEN 1 ELSE 0 END) AS planned_sales,
		COALESCE(SUM(CASE WHEN UPPER(transaction_code) = 'P' AND UPPER(acquired_disposed_code) = 'A' AND derivative = 0 AND price_micros > 0 AND value_micros > 0 THEN CAST(value_micros AS REAL) / 1000000.0 ELSE 0 END), 0) AS buy_value_usd,
		COALESCE(SUM(CASE WHEN UPPER(transaction_code) = 'S' AND UPPER(acquired_disposed_code) = 'D' AND derivative = 0 AND price_micros > 0 AND value_micros > 0 THEN CAST(value_micros AS REAL) / 1000000.0 ELSE 0 END), 0) AS sell_value_usd`).Scan(&value).Error
	if err != nil {
		return insiderTransactionSummary{}, err
	}
	buy, sell := value.BuyValueUSD, value.SellValueUSD
	return insiderTransactionSummary{
		Transactions: value.Transactions, Issuers: value.Issuers,
		Purchases: value.Purchases, Sales: value.Sales, PricedPurchases: value.PricedPurchases, PricedSales: value.PricedSales,
		OtherAcquisitions: value.OtherAcquisitions, OtherDispositions: value.OtherDispositions, PlannedSales: value.PlannedSales,
		BuyValueUSD: buy, SellValueUSD: sell, NetValueUSD: buy - sell,
	}, nil
}
