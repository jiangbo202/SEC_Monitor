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
		Risks: []CapitalRiskSnapshot{{Kind: CapitalEventRegisteredFinancing, Active: true, BlocksA: false, Severity: CapitalRiskSeverityMedium}},
		AsOf:  asOf,
	}

	score := ScoreDiscoveryCandidate(input)
	if score.Grade != CandidateGradeA || !score.EligibleA || !score.EligibleB {
		t.Fatalf("score = %#v", score)
	}
	if score.TotalScore != 80 || score.RevenueGrowthScore != 30 || score.CashRunwayScore != 20 || score.InsiderScore != 20 || score.DilutionRiskScore != 10 {
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
		Risks: []CapitalRiskSnapshot{{Kind: CapitalEventATMProgram, Active: true, BlocksA: true, BlocksB: false, Severity: CapitalRiskSeverityHigh}},
		AsOf:  asOf,
	}

	score := ScoreDiscoveryCandidate(input)
	if score.Grade != CandidateGradeB || score.EligibleA || !score.EligibleB {
		t.Fatalf("score = %#v", score)
	}
	if score.TotalScore != 30 || score.DilutionRiskScore != 0 {
		t.Fatalf("score components = %#v", score)
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
