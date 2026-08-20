package discovery

import "strings"

const (
	InvestabilityTradable    = "tradable"
	InvestabilityConstrained = "constrained"
	InvestabilityBlocked     = "blocked"
	InvestabilityUnknown     = "unknown"

	minimumTradablePriceUSD        = 1.00
	minimumConstrainedADVUSD       = 100_000.0
	minimumTradableADVUSD          = 500_000.0
	minimumInvestabilitySampleDays = 15
	defaultMaxADVParticipationPct  = 5.0
)

// CandidateInvestability is an EOD research liquidity gate, not execution
// advice. Bid/ask spread, borrow and intraday halt information are not
// available from the free end-of-day providers and are explicitly left out.
type CandidateInvestability struct {
	Status                       string   `json:"status"`
	Reasons                      []string `json:"reasons"`
	SampleDays                   int      `json:"sample_days"`
	AverageDollarVolumeUSD       float64  `json:"average_dollar_volume_usd"`
	SuggestedMaxDailyNotionalUSD float64  `json:"suggested_max_daily_notional_usd"`
	MaxADVParticipationPct       float64  `json:"max_adv_participation_pct"`
	SpreadEvidenceStatus         string   `json:"spread_evidence_status"`
}

func buildCandidateInvestability(item CandidateScoreResult) CandidateInvestability {
	return BuildCandidateInvestabilityWithPolicy(item, DefaultSmallCapPolicy())
}

func BuildCandidateInvestabilityWithPolicy(item CandidateScoreResult, policy SmallCapPolicy) CandidateInvestability {
	if normalized, err := NormalizeSmallCapPolicy(policy); err == nil {
		policy = normalized
	} else {
		policy = DefaultSmallCapPolicy()
	}
	quality := item.MarketQuality
	result := CandidateInvestability{
		Status:                 InvestabilityTradable,
		Reasons:                []string{},
		SampleDays:             quality.SampleDays,
		AverageDollarVolumeUSD: quality.AverageDollarVolume,
		MaxADVParticipationPct: defaultMaxADVParticipationPct,
		SpreadEvidenceStatus:   "not_available_eod",
	}
	if quality.AverageDollarVolume > 0 {
		result.SuggestedMaxDailyNotionalUSD = quality.AverageDollarVolume * defaultMaxADVParticipationPct / 100
	}
	add := func(reason string) {
		for _, existing := range result.Reasons {
			if existing == reason {
				return
			}
		}
		result.Reasons = append(result.Reasons, reason)
	}
	block := func(reason string) {
		result.Status = InvestabilityBlocked
		add(reason)
	}
	constrain := func(reason string) {
		if result.Status != InvestabilityBlocked {
			result.Status = InvestabilityConstrained
		}
		add(reason)
	}
	if item.PriceCloseUSD <= 0 || item.PriceQualityStatus != QualityStatusValid {
		block("price_evidence_unavailable")
	}
	switch item.PriceFreshnessStatus {
	case PriceFreshnessCurrent, PriceFreshnessPreviousTradingDay:
	case "":
		block("price_freshness_unknown")
	default:
		block("price_not_recent_close")
	}
	if quality.SampleDays == 0 {
		block("liquidity_history_unavailable")
	} else {
		if quality.SampleDays < policy.MinimumHistoryDays {
			constrain("liquidity_history_short")
		}
		if quality.AverageDollarVolume < policy.BlockedADVUSD {
			block("average_dollar_volume_below_100k")
		} else if quality.AverageDollarVolume < policy.TradableADVUSD {
			constrain("average_dollar_volume_below_500k")
		}
	}
	if item.PriceCloseUSD > 0 && item.PriceCloseUSD < policy.MinimumPriceUSD {
		constrain("sub_dollar_price")
	}
	if quality.VolatilityPct >= 15 {
		constrain("extreme_daily_volatility")
	}
	for _, risk := range item.CapitalRiskSummaries {
		switch risk.Kind {
		case CapitalEventReverseSplit:
			block("reverse_split_risk")
		case CapitalEventGoingConcern:
			block("going_concern_risk")
		}
	}
	if result.Status == InvestabilityTradable && strings.TrimSpace(item.PriceSource) == "" {
		constrain("price_source_unknown")
	}
	return result
}
