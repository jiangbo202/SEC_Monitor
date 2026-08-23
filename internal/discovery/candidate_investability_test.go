package discovery

import "testing"

func TestBuildCandidateInvestabilitySeparatesLiquidityFromCompanyScore(t *testing.T) {
	base := CandidateScoreResult{
		CandidateScoreSnapshot: CandidateScoreSnapshot{MarketCapUSD: 120_000_000},
		PriceCloseUSD:          12,
		PriceQualityStatus:     QualityStatusValid,
		PriceFreshnessStatus:   PriceFreshnessCurrent,
		PriceSource:            "tiingo",
		MarketQuality:          CandidateMarketQuality{SampleDays: 21, AverageDollarVolume: 1_000_000},
	}
	tests := []struct {
		name       string
		item       CandidateScoreResult
		wantStatus string
		wantReason string
	}{
		{name: "liquid daily evidence is tradable", item: base, wantStatus: InvestabilityTradable},
		{name: "modest liquidity is constrained", item: func() CandidateScoreResult { row := base; row.MarketQuality.AverageDollarVolume = 300_000; return row }(), wantStatus: InvestabilityConstrained, wantReason: "average_dollar_volume_below_500k"},
		{name: "thin liquidity is blocked", item: func() CandidateScoreResult { row := base; row.MarketQuality.AverageDollarVolume = 99_000; return row }(), wantStatus: InvestabilityBlocked, wantReason: "average_dollar_volume_below_100k"},
		{name: "capital risk is evaluated separately from liquidity", item: func() CandidateScoreResult {
			row := base
			row.CapitalRiskSummaries = []CapitalRiskSummary{{Kind: CapitalEventReverseSplit}, {Kind: CapitalEventGoingConcern}}
			return row
		}(), wantStatus: InvestabilityTradable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildCandidateInvestability(tt.item)
			if got.Status != tt.wantStatus || (tt.wantReason != "" && !containsString(got.Reasons, tt.wantReason)) {
				t.Fatalf("investability=%#v", got)
			}
			if got.SpreadEvidenceStatus != "not_available_eod" || got.MaxADVParticipationPct != 5 {
				t.Fatalf("evidence disclosure=%#v", got)
			}
		})
	}
}
