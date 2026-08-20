package discovery

import (
	"fmt"
	"time"
)

const (
	CandidateGradeA        = "A"
	CandidateGradeB        = "B"
	CandidateGradeExcluded = "excluded"
)

const DiscoveryScoringVersion = "small-cap-discovery-score-v2-biotech-calibrated"

const (
	CandidateAMarketCapMaxExclusiveUSD = int64(500_000_000)
	CandidateBMarketCapMaxExclusiveUSD = MaximumSmallCapUSD
	CandidateARevenueGrowthMinPct      = 40.0
	CandidateBRevenueGrowthMinPct      = 20.0
	CandidateARunwayMinMonths          = 12.0
	CandidateInsiderLookbackDays       = 180
	CandidateBMinSectorScore           = 7
)

// CandidateSelectionCriteria exposes the active, code-defined research rules
// so the UI can describe the same criteria that the scoring engine applies.
type CandidateSelectionCriteria struct {
	ScoringVersion                string                 `json:"scoring_version"`
	ScoringRubric                 CandidateScoringRubric `json:"scoring_rubric"`
	MarketCapMinUSD               int64                  `json:"market_cap_min_usd"`
	AMarketCapMaxExclusiveUSD     int64                  `json:"a_market_cap_max_exclusive_usd"`
	BMarketCapMaxExclusiveUSD     int64                  `json:"b_market_cap_max_exclusive_usd"`
	ARevenueGrowthMinExclusivePct float64                `json:"a_revenue_growth_min_exclusive_pct"`
	BRevenueGrowthMinExclusivePct float64                `json:"b_revenue_growth_min_exclusive_pct"`
	ARunwayMinMonths              float64                `json:"a_runway_min_months"`
	InsiderLookbackDays           int                    `json:"insider_lookback_days"`
	BMinSectorScore               int                    `json:"b_min_sector_score"`
	AllowedExchanges              []string               `json:"allowed_exchanges"`
	MaxPriceAgeTradingDays        int                    `json:"max_price_age_trading_days"`
	MinimumPriceUSD               float64                `json:"minimum_price_usd"`
	BlockedADVUSD                 float64                `json:"blocked_adv_usd"`
	TradableADVUSD                float64                `json:"tradable_adv_usd"`
	MinimumHistoryDays            int                    `json:"minimum_history_days"`
	RevenueGrowthSelection        string                 `json:"revenue_growth_selection"`
	QualifiedInsiderRequirement   string                 `json:"qualified_insider_requirement"`
	ActiveCapitalRiskRequirement  string                 `json:"active_capital_risk_requirement"`
}

func CurrentCandidateSelectionCriteria() CandidateSelectionCriteria {
	return CandidateSelectionCriteriaForPolicy(DefaultSmallCapPolicy())
}

func CandidateSelectionCriteriaForPolicy(policy SmallCapPolicy) CandidateSelectionCriteria {
	return CandidateSelectionCriteria{
		ScoringVersion:                DiscoveryScoringVersion,
		ScoringRubric:                 CandidateScoringRubricForPolicy(policy),
		MarketCapMinUSD:               policy.MarketCapMinUSD,
		AMarketCapMaxExclusiveUSD:     policy.AMarketCapMaxExclusiveUSD,
		BMarketCapMaxExclusiveUSD:     policy.MarketCapMaxUSD,
		ARevenueGrowthMinExclusivePct: policy.ARevenueGrowthMinPct,
		BRevenueGrowthMinExclusivePct: policy.BRevenueGrowthMinPct,
		ARunwayMinMonths:              policy.ARunwayMinMonths,
		InsiderLookbackDays:           policy.InsiderLookbackDays,
		BMinSectorScore:               policy.BMinSectorScore,
		AllowedExchanges:              append([]string(nil), policy.AllowedExchanges...),
		MaxPriceAgeTradingDays:        policy.MaxPriceAgeTradingDays,
		MinimumPriceUSD:               policy.MinimumPriceUSD,
		BlockedADVUSD:                 policy.BlockedADVUSD,
		TradableADVUSD:                policy.TradableADVUSD,
		MinimumHistoryDays:            policy.MinimumHistoryDays,
		RevenueGrowthSelection:        "优先最新可比季度收入同比；季度不可用时回退年度同比",
		QualifiedInsiderRequirement:   fmt.Sprintf("近 %d 日 CEO、CFO 或创始人的合格 Form 4 公开市场买入", policy.InsiderLookbackDays),
		ActiveCapitalRiskRequirement:  "A级不允许 A/B 阻断；B级不允许 B 阻断",
	}
}

type DiscoveryScoreInput struct {
	SecurityID     uint
	Ticker         string
	MarketCapUSD   int64
	Financial      FinancialMetricSnapshot
	Insiders       []InsiderTransactionSnapshot
	Risks          []CapitalRiskSnapshot
	GrossMarginPct float64
	SectorScore    int
	BusinessModel  CandidateBusinessModelEvidence
	AsOf           time.Time
}

type DiscoveryScore struct {
	SecurityID   uint
	Ticker       string
	MarketCapUSD int64
	Grade        string
	EligibleA    bool
	EligibleB    bool
	TotalScore   int

	RevenueGrowthScore int
	CashRunwayScore    int
	InsiderScore       int
	GrossMarginScore   int
	DilutionRiskScore  int
	SectorScore        int

	RevenueGrowthPct       float64
	CashRunwayMonths       float64
	RecentQualifiedInsider bool
	ActiveBlocksA          bool
	ActiveBlocksB          bool
	ReasonCode             string
	ScoringVersion         string
	ScoringRubricSHA256    string
	ScoringRubricJSON      string
	BusinessModelAtScore   string
	RevenueScoreCapReason  string
}

func ScoreDiscoveryCandidate(input DiscoveryScoreInput) DiscoveryScore {
	return ScoreDiscoveryCandidateWithPolicy(input, DefaultSmallCapPolicy())
}

func ScoreDiscoveryCandidateWithPolicy(input DiscoveryScoreInput, policy SmallCapPolicy) DiscoveryScore {
	if normalized, err := NormalizeSmallCapPolicy(policy); err == nil {
		policy = normalized
	} else {
		policy = DefaultSmallCapPolicy()
	}
	asOf := input.AsOf
	if asOf.IsZero() {
		asOf = time.Now().UTC()
	}
	growth, growthAvailable, _ := selectRevenueGrowth(input.Financial)
	recentInsider := hasRecentQualifiedInsiderWithLookback(input.Insiders, asOf, policy.InsiderLookbackDays)
	blocksA, blocksB := activeRiskBlocks(input.Risks)

	score := DiscoveryScore{
		SecurityID: input.SecurityID, Ticker: input.Ticker, MarketCapUSD: input.MarketCapUSD,
		RevenueGrowthPct: growth, CashRunwayMonths: input.Financial.CashRunwayMonths,
		RecentQualifiedInsider: recentInsider, ActiveBlocksA: blocksA, ActiveBlocksB: blocksB,
		ScoringVersion: DiscoveryScoringVersion, BusinessModelAtScore: input.BusinessModel.Model,
		RevenueScoreCapReason: input.BusinessModel.RevenueScoreCapReason,
	}
	score.ScoringRubricJSON, score.ScoringRubricSHA256 = candidateScoringRubricJSON(policy)
	if growthAvailable {
		switch {
		case growth >= 40:
			score.RevenueGrowthScore = 30
		case growth >= 20:
			score.RevenueGrowthScore = 20
		case growth > 0:
			score.RevenueGrowthScore = 10
		}
	}
	if input.BusinessModel.RevenueScoreCap > 0 && score.RevenueGrowthScore > input.BusinessModel.RevenueScoreCap {
		score.RevenueGrowthScore = input.BusinessModel.RevenueScoreCap
	}
	if input.Financial.RunwayAvailable {
		switch {
		case input.Financial.CashRunwayMonths >= 12:
			score.CashRunwayScore = 20
		case input.Financial.CashRunwayMonths >= 6:
			score.CashRunwayScore = 10
		}
	}
	if recentInsider {
		score.InsiderScore = 20
	}
	if input.GrossMarginPct >= 50 {
		score.GrossMarginScore = 10
	} else if input.GrossMarginPct >= 30 {
		score.GrossMarginScore = 5
	}
	if !blocksA && !blocksB {
		score.DilutionRiskScore = 10
	}
	if input.SectorScore > 10 {
		score.SectorScore = 10
	} else if input.SectorScore > 0 {
		score.SectorScore = input.SectorScore
	}
	score.TotalScore = score.RevenueGrowthScore + score.CashRunwayScore + score.InsiderScore + score.GrossMarginScore + score.DilutionRiskScore + score.SectorScore

	modelAllowsA := input.BusinessModel.Model != CandidateBusinessModelClinicalPreRevenue &&
		!(input.BusinessModel.Model == CandidateBusinessModelMixedOrLicensing && !input.BusinessModel.RevenueRepeatableConfirmed) &&
		input.BusinessModel.Model != CandidateBusinessModelUnknown
	score.EligibleA = modelAllowsA && input.MarketCapUSD >= policy.MarketCapMinUSD && input.MarketCapUSD < policy.AMarketCapMaxExclusiveUSD &&
		growthAvailable && growth > policy.ARevenueGrowthMinPct &&
		input.Financial.RunwayAvailable && input.Financial.CashRunwayMonths >= policy.ARunwayMinMonths &&
		recentInsider && !blocksA && !blocksB
	score.EligibleB = input.MarketCapUSD >= policy.MarketCapMinUSD && input.MarketCapUSD < policy.MarketCapMaxUSD &&
		growthAvailable && growth > policy.BRevenueGrowthMinPct && !blocksB &&
		score.SectorScore >= policy.BMinSectorScore
	switch {
	case score.EligibleA:
		score.Grade = CandidateGradeA
		score.ReasonCode = "a_criteria_met"
	case score.EligibleB:
		score.Grade = CandidateGradeB
		score.ReasonCode = "b_criteria_met"
	default:
		score.Grade = CandidateGradeExcluded
		score.ReasonCode = "criteria_not_met"
	}
	return score
}

// selectRevenueGrowth deliberately favors the most recent comparable quarter.
// Annual revenue is a fallback only when a quarterly year-over-year comparison is unavailable.
func selectRevenueGrowth(financial FinancialMetricSnapshot) (float64, bool, string) {
	if financial.RevenueGrowthAvailable {
		return financial.QuarterlyRevenueYoYPct, true, "quarterly_revenue_yoy_pct"
	}
	if financial.LatestAnnualRevenueUSD > 0 && financial.PriorAnnualRevenueUSD > 0 {
		return financial.AnnualRevenueYoYPct, true, "annual_revenue_yoy_pct"
	}
	return 0, false, "missing"
}

func CandidateScoreToSnapshot(batchID string, score DiscoveryScore, now time.Time) CandidateScoreSnapshot {
	return CandidateScoreSnapshot{
		BatchID: batchID, SecurityID: score.SecurityID, Ticker: score.Ticker, MarketCapUSD: score.MarketCapUSD,
		Grade: score.Grade, EligibleA: score.EligibleA, EligibleB: score.EligibleB, TotalScore: score.TotalScore,
		RevenueGrowthScore: score.RevenueGrowthScore, CashRunwayScore: score.CashRunwayScore,
		InsiderScore: score.InsiderScore, GrossMarginScore: score.GrossMarginScore,
		DilutionRiskScore: score.DilutionRiskScore, SectorScore: score.SectorScore,
		RevenueGrowthPct: score.RevenueGrowthPct, CashRunwayMonths: score.CashRunwayMonths,
		RecentQualifiedInsider: score.RecentQualifiedInsider, ActiveBlocksA: score.ActiveBlocksA,
		ActiveBlocksB: score.ActiveBlocksB, ReasonCode: score.ReasonCode,
		ScoringVersion: score.ScoringVersion, BusinessModelAtScore: score.BusinessModelAtScore,
		ScoringRubricSHA256: score.ScoringRubricSHA256, ScoringRubricJSON: score.ScoringRubricJSON,
		RevenueScoreCapReason: score.RevenueScoreCapReason, CreatedAt: now,
	}
}

func hasRecentQualifiedInsider(rows []InsiderTransactionSnapshot, asOf time.Time) bool {
	return hasRecentQualifiedInsiderWithLookback(rows, asOf, CandidateInsiderLookbackDays)
}

func hasRecentQualifiedInsiderWithLookback(rows []InsiderTransactionSnapshot, asOf time.Time, lookbackDays int) bool {
	cutoff := asOf.AddDate(0, 0, -lookbackDays)
	for _, row := range rows {
		if !row.Qualified || row.TransactionDate.IsZero() || row.TransactionDate.Before(cutoff) || row.TransactionDate.After(asOf) {
			continue
		}
		switch row.Role {
		case InsiderRoleCEO, InsiderRoleCFO, InsiderRoleFounder:
			return true
		}
	}
	return false
}

func activeRiskBlocks(rows []CapitalRiskSnapshot) (bool, bool) {
	blocksA, blocksB := false, false
	for _, row := range rows {
		if !row.Active {
			continue
		}
		blocksA = blocksA || row.BlocksA
		blocksB = blocksB || row.BlocksB
	}
	return blocksA, blocksB
}
