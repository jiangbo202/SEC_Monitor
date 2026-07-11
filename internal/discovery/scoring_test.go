package discovery

import (
	"testing"
	"time"
)

func TestScoreDiscoveryCandidateGradesAWhenCoreSignalsPass(t *testing.T) {
	asOf := time.Date(2026, 6, 29, 12, 0, 0, 0, time.UTC)
	input := DiscoveryScoreInput{
		SecurityID:   7,
		Ticker:       "ACME",
		MarketCapUSD: 240_000_000,
		Financial: FinancialMetricSnapshot{
			RevenueGrowthAvailable: true,
			RunwayAvailable:        true,
			QuarterlyRevenueYoYPct: 55,
			AnnualRevenueYoYPct:    35,
			CashRunwayMonths:       14,
		},
		Insiders: []InsiderTransactionSnapshot{{
			Role: InsiderRoleCEO, TransactionDate: asOf.AddDate(0, 0, -30), Qualified: true, ValueMicros: 75_000_000_000,
		}},
		Risks:       []CapitalRiskSnapshot{{Kind: CapitalEventRegisteredFinancing, Active: true, BlocksA: false, Severity: CapitalRiskSeverityMedium}},
		SectorScore: 8,
		AsOf:        asOf,
	}

	score := ScoreDiscoveryCandidate(input)
	if score.Grade != CandidateGradeA || !score.EligibleA || !score.EligibleB {
		t.Fatalf("score = %#v", score)
	}
	if score.TotalScore != 88 || score.RevenueGrowthScore != 30 || score.CashRunwayScore != 20 || score.InsiderScore != 20 || score.DilutionRiskScore != 10 || score.SectorScore != 8 {
		t.Fatalf("score components = %#v", score)
	}
}

func TestScoreDiscoveryCandidateDowngradesForAOnlyRiskAndMissingInsider(t *testing.T) {
	asOf := time.Date(2026, 6, 29, 12, 0, 0, 0, time.UTC)
	input := DiscoveryScoreInput{
		SecurityID:   8,
		Ticker:       "BETA",
		MarketCapUSD: 650_000_000,
		Financial: FinancialMetricSnapshot{
			RevenueGrowthAvailable: true,
			RunwayAvailable:        true,
			QuarterlyRevenueYoYPct: 25,
			CashRunwayMonths:       8,
		},
		Risks:       []CapitalRiskSnapshot{{Kind: CapitalEventATMProgram, Active: true, BlocksA: true, BlocksB: false, Severity: CapitalRiskSeverityHigh}},
		SectorScore: 8,
		AsOf:        asOf,
	}

	score := ScoreDiscoveryCandidate(input)
	if score.Grade != CandidateGradeB || score.EligibleA || !score.EligibleB {
		t.Fatalf("score = %#v", score)
	}
	if score.TotalScore != 38 || score.DilutionRiskScore != 0 || score.SectorScore != 8 {
		t.Fatalf("score components = %#v", score)
	}
}

func TestScoreDiscoveryCandidateRequiresStrongSectorForBGrade(t *testing.T) {
	asOf := time.Date(2026, 6, 29, 12, 0, 0, 0, time.UTC)
	input := DiscoveryScoreInput{
		SecurityID:   10,
		Ticker:       "LOWSEC",
		MarketCapUSD: 650_000_000,
		Financial: FinancialMetricSnapshot{
			RevenueGrowthAvailable: true,
			QuarterlyRevenueYoYPct: 35,
		},
		SectorScore: 6,
		AsOf:        asOf,
	}

	score := ScoreDiscoveryCandidate(input)
	if score.Grade != CandidateGradeExcluded || score.EligibleB {
		t.Fatalf("low-sector score = %#v", score)
	}

	input.SectorScore = 7
	score = ScoreDiscoveryCandidate(input)
	if score.Grade != CandidateGradeB || !score.EligibleB || score.SectorScore != 7 {
		t.Fatalf("strong-sector score = %#v", score)
	}
}

func TestScoreDiscoveryCandidateExcludesWhenBBlocked(t *testing.T) {
	asOf := time.Date(2026, 6, 29, 12, 0, 0, 0, time.UTC)
	score := ScoreDiscoveryCandidate(DiscoveryScoreInput{
		SecurityID:   9,
		Ticker:       "RISK",
		MarketCapUSD: 300_000_000,
		Financial: FinancialMetricSnapshot{
			RevenueGrowthAvailable: true,
			RunwayAvailable:        true,
			QuarterlyRevenueYoYPct: 80,
			CashRunwayMonths:       24,
		},
		Insiders: []InsiderTransactionSnapshot{{Role: InsiderRoleCFO, TransactionDate: asOf.AddDate(0, 0, -10), Qualified: true, ValueMicros: 5_000_000_000}},
		Risks:    []CapitalRiskSnapshot{{Kind: CapitalEventGoingConcern, Active: true, BlocksA: true, BlocksB: true, Severity: CapitalRiskSeverityHigh}},
		AsOf:     asOf,
	})
	if score.Grade != CandidateGradeExcluded || score.EligibleA || score.EligibleB || score.TotalScore != 70 {
		t.Fatalf("score = %#v", score)
	}
}

func TestScoreDiscoveryCandidatePrefersQuarterlyRevenueGrowth(t *testing.T) {
	cases := []struct {
		name              string
		financial         FinancialMetricSnapshot
		wantGrowth        float64
		wantGrowthScore   int
		wantRevenueSignal bool
	}{
		{
			name: "uses quarterly result even when annual is higher",
			financial: FinancialMetricSnapshot{
				RevenueGrowthAvailable: true, QuarterlyRevenueYoYPct: -12, AnnualRevenueYoYPct: 80,
			},
			wantGrowth: -12,
		},
		{
			name: "falls back to annual when quarterly is unavailable",
			financial: FinancialMetricSnapshot{
				AnnualRevenueYoYPct: 45, LatestAnnualRevenueUSD: 145, PriorAnnualRevenueUSD: 100,
			},
			wantGrowth: 45, wantGrowthScore: 30, wantRevenueSignal: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			score := ScoreDiscoveryCandidate(DiscoveryScoreInput{Financial: tc.financial, AsOf: time.Date(2026, 7, 11, 0, 0, 0, 0, time.UTC)})
			if score.RevenueGrowthPct != tc.wantGrowth || score.RevenueGrowthScore != tc.wantGrowthScore {
				t.Fatalf("score = %#v, want growth=%v score=%d", score, tc.wantGrowth, tc.wantGrowthScore)
			}
			if score.RevenueGrowthScore > 0 != tc.wantRevenueSignal {
				t.Fatalf("revenue signal = %v, want %v", score.RevenueGrowthScore > 0, tc.wantRevenueSignal)
			}
		})
	}
}

func TestCandidateScoreToSnapshotPreservesScoreEvidence(t *testing.T) {
	now := time.Date(2026, 6, 29, 12, 0, 0, 0, time.UTC)
	score := DiscoveryScore{
		SecurityID: 7, Ticker: "ACME", MarketCapUSD: 240_000_000, Grade: CandidateGradeA,
		EligibleA: true, EligibleB: true, TotalScore: 80, RevenueGrowthScore: 30, CashRunwayScore: 20,
		InsiderScore: 20, DilutionRiskScore: 10, RevenueGrowthPct: 55, CashRunwayMonths: 14,
		RecentQualifiedInsider: true, ReasonCode: "a_criteria_met", ScoringVersion: DiscoveryScoringVersion,
	}

	row := CandidateScoreToSnapshot("batch-1", score, now)
	if row.BatchID != "batch-1" || row.SecurityID != 7 || row.Ticker != "ACME" || row.Grade != CandidateGradeA || !row.EligibleA || row.TotalScore != 80 || row.CreatedAt != now {
		t.Fatalf("snapshot = %#v", row)
	}
}
