package discovery

import (
	"strings"
	"testing"
	"time"
)

func TestAnnotateCandidateGradeExplanationsShowsOnlyUnmetAGates(t *testing.T) {
	items := []CandidateScoreResult{{
		CandidateScoreSnapshot: CandidateScoreSnapshot{
			Grade: CandidateGradeB, MarketCapUSD: 513_400_000, RevenueGrowthPct: 84, CashRunwayMonths: 18,
		},
		BusinessModel: CandidateBusinessModelEvidence{Model: CandidateBusinessModelCommercial},
	}}
	annotateCandidateGradeExplanations(items, DefaultSmallCapPolicy())
	got := items[0].GradeExplanation
	if !got.NearA || len(got.UnmetAConditions) != 1 || !strings.Contains(got.Summary, "$13.4M") {
		t.Fatalf("grade explanation = %#v", got)
	}
}

func TestEvidenceCompletenessDoesNotMixInTradabilityOrDilutionRisk(t *testing.T) {
	asOf := time.Date(2026, 8, 22, 0, 0, 0, 0, time.UTC)
	item := CandidateScoreResult{
		CandidateScoreSnapshot: CandidateScoreSnapshot{MarketCapUSD: 100_000_000},
		PriceCloseUSD:          3,
		PriceQualityStatus:     QualityStatusValid,
		PriceFreshnessStatus:   PriceFreshnessCurrent,
		Investability:          CandidateInvestability{Status: InvestabilityBlocked},
		DilutionTrend:          CandidateDilutionTrend{Status: "high_dilution"},
	}
	metric := FinancialMetricSnapshot{RevenueGrowthAvailable: true, RunwayAvailable: true}
	evidence := buildCandidateEvidenceCompleteness(item, metric, true, asOf.AddDate(0, 0, -60), true, false, false, candidateInsiderCoverage{}, asOf)
	if evidence.Status != CandidateEvidenceComplete || len(evidence.Reasons) != 0 {
		t.Fatalf("evidence = %#v", evidence)
	}
	readiness := buildCandidateResearchReadiness(item, metric, true, asOf.AddDate(0, 0, -60), true, false, false, candidateInsiderCoverage{}, asOf)
	if readiness.Status != CandidateResearchReadinessBlocked {
		t.Fatalf("research readiness = %#v", readiness)
	}
}

func TestBiotechEvidenceReasonIsNotDuplicated(t *testing.T) {
	asOf := time.Date(2026, 8, 22, 0, 0, 0, 0, time.UTC)
	item := CandidateScoreResult{
		CandidateScoreSnapshot: CandidateScoreSnapshot{MarketCapUSD: 100_000_000},
		PriceCloseUSD:          3,
		PriceQualityStatus:     QualityStatusValid,
		PriceFreshnessStatus:   PriceFreshnessCurrent,
		SectorCategory:         "生物医药",
		BusinessModel:          CandidateBusinessModelEvidence{Model: CandidateBusinessModelUnknown, RequiresReview: true},
	}
	metric := FinancialMetricSnapshot{RevenueGrowthAvailable: true, RunwayAvailable: true}
	readiness := buildCandidateResearchReadiness(item, metric, true, asOf.AddDate(0, 0, -60), true, false, false, candidateInsiderCoverage{}, asOf)
	if !containsString(readiness.Reasons, "biotech_business_model_unconfirmed") || containsString(readiness.Reasons, "biotech_business_model_review_due") {
		t.Fatalf("readiness reasons = %#v", readiness.Reasons)
	}
}

func TestCandidateQualityUsesAverageDollarVolumeAndConfiguredSourceRole(t *testing.T) {
	policy := DefaultSmallCapPolicy()
	item := CandidateScoreResult{PriceVolume: 1, PriceSourceRole: "primary", MarketQuality: CandidateMarketQuality{AverageDollarVolume: policy.TradableADVUSD * 2}}
	tags := candidateQualityTagsWithPolicy(item, policy)
	if containsString(tags, "low_liquidity") || containsString(tags, "secondary_price_source") {
		t.Fatalf("healthy ADV primary source tags = %#v", tags)
	}
	item.MarketQuality.AverageDollarVolume = policy.TradableADVUSD / 2
	item.PriceSourceRole = "fallback"
	tags = candidateQualityTagsWithPolicy(item, policy)
	if !containsString(tags, "low_liquidity") || !containsString(tags, "secondary_price_source") {
		t.Fatalf("low ADV fallback source tags = %#v", tags)
	}
}
