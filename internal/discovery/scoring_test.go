package discovery

import (
	"strings"
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

func TestScoreDiscoveryCandidateCalibratesBiotechRevenueSignals(t *testing.T) {
	base := DiscoveryScoreInput{
		SecurityID: 44, Ticker: "BIOX", MarketCapUSD: 240_000_000, SectorScore: 9,
		Financial: FinancialMetricSnapshot{RevenueGrowthAvailable: true, RunwayAvailable: true, QuarterlyRevenueYoYPct: 120, CashRunwayMonths: 18},
		Insiders:  []InsiderTransactionSnapshot{{Role: InsiderRoleCEO, TransactionDate: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC), Qualified: true}},
		AsOf:      time.Date(2026, 7, 11, 0, 0, 0, 0, time.UTC),
	}
	tests := []struct {
		name      string
		model     CandidateBusinessModelEvidence
		wantScore int
		wantA     bool
	}{
		{name: "commercial can retain full revenue score", model: CandidateBusinessModelEvidence{Model: CandidateBusinessModelCommercial, RevenueScoreCap: 30}, wantScore: 30, wantA: true},
		{name: "clinical revenue is capped and cannot be A", model: CandidateBusinessModelEvidence{Model: CandidateBusinessModelClinicalPreRevenue, RevenueScoreCap: 10}, wantScore: 10, wantA: false},
		{name: "unconfirmed licensing revenue is capped and cannot be A", model: CandidateBusinessModelEvidence{Model: CandidateBusinessModelMixedOrLicensing, RevenueScoreCap: 10}, wantScore: 10, wantA: false},
		{name: "confirmed licensing revenue can be A", model: CandidateBusinessModelEvidence{Model: CandidateBusinessModelMixedOrLicensing, RevenueScoreCap: 30, RevenueRepeatableConfirmed: true}, wantScore: 30, wantA: true},
		{name: "unknown is retained for research but cannot be A", model: CandidateBusinessModelEvidence{Model: CandidateBusinessModelUnknown, RevenueScoreCap: 10, RequiresReview: true}, wantScore: 10, wantA: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			input := base
			input.BusinessModel = tc.model
			score := ScoreDiscoveryCandidate(input)
			if score.RevenueGrowthScore != tc.wantScore || score.EligibleA != tc.wantA || !score.EligibleB {
				t.Fatalf("score = %#v", score)
			}
		})
	}
}

func TestCandidateScoringRubricIsCompleteDeterministicAndPolicyBound(t *testing.T) {
	rubric := CurrentCandidateScoringRubric()
	if rubric.Version != DiscoveryScoringVersion || rubric.MaxScore != 100 || len(rubric.Dimensions) != 6 || len(rubric.ContentSHA256) != 64 {
		t.Fatalf("rubric metadata = %#v", rubric)
	}
	total := 0
	for _, dimension := range rubric.Dimensions {
		total += dimension.MaxPoints
		if dimension.Key == "" || dimension.Label == "" || dimension.Evidence == "" || len(dimension.Rules) == 0 {
			t.Fatalf("incomplete dimension = %#v", dimension)
		}
	}
	if total != rubric.MaxScore {
		t.Fatalf("dimension max total = %d, want %d", total, rubric.MaxScore)
	}
	if repeated := CurrentCandidateScoringRubric(); repeated.ContentSHA256 != rubric.ContentSHA256 {
		t.Fatalf("rubric hash is not deterministic: %q != %q", repeated.ContentSHA256, rubric.ContentSHA256)
	}
	policy := DefaultSmallCapPolicy()
	policy.InsiderLookbackDays++
	if changed := CandidateScoringRubricForPolicy(policy); changed.ContentSHA256 == rubric.ContentSHA256 {
		t.Fatal("policy-dependent insider rule did not change rubric hash")
	}
}

func TestCandidateScoreSnapshotFreezesScoringRubric(t *testing.T) {
	policy := DefaultSmallCapPolicy()
	policy.InsiderLookbackDays = 365
	score := ScoreDiscoveryCandidateWithPolicy(DiscoveryScoreInput{AsOf: time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC)}, policy)
	snapshot := CandidateScoreToSnapshot("rubric-batch", score, time.Now().UTC())
	if snapshot.ScoringRubricJSON == "" || len(snapshot.ScoringRubricSHA256) != 64 {
		t.Fatalf("snapshot rubric lineage missing: %#v", snapshot)
	}
	rubric := CandidateScoringRubricForSnapshot(snapshot)
	if rubric.ContentSHA256 != snapshot.ScoringRubricSHA256 || !strings.Contains(rubric.Dimensions[2].Rules[0].Condition, "365") {
		t.Fatalf("frozen rubric = %#v", rubric)
	}
}

func TestCandidateScoringRubricDoesNotRewriteUnknownLegacyVersion(t *testing.T) {
	rubric := CandidateScoringRubricForSnapshot(CandidateScoreSnapshot{ScoringVersion: "legacy-score-v0"})
	if rubric.Version != "legacy-score-v0" || len(rubric.Dimensions) != 0 || !strings.Contains(rubric.GradeRuleNote, "不能使用当前公式反推") {
		t.Fatalf("legacy rubric fallback = %#v", rubric)
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
