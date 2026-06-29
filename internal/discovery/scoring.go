package discovery

import (
	"math"
	"time"
)

const (
	CandidateGradeA        = "A"
	CandidateGradeB        = "B"
	CandidateGradeExcluded = "excluded"
)

const DiscoveryScoringVersion = "small-cap-discovery-score-v1"

type DiscoveryScoreInput struct {
	SecurityID     uint
	Ticker         string
	MarketCapUSD   int64
	Financial      FinancialMetricSnapshot
	Insiders       []InsiderTransactionSnapshot
	Risks          []CapitalRiskSnapshot
	GrossMarginPct float64
	SectorScore    int
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
}

func ScoreDiscoveryCandidate(input DiscoveryScoreInput) DiscoveryScore {
	asOf := input.AsOf
	if asOf.IsZero() {
		asOf = time.Now().UTC()
	}
	growth := math.Max(input.Financial.QuarterlyRevenueYoYPct, input.Financial.AnnualRevenueYoYPct)
	recentInsider := hasRecentQualifiedInsider(input.Insiders, asOf)
	blocksA, blocksB := activeRiskBlocks(input.Risks)

	score := DiscoveryScore{
		SecurityID: input.SecurityID, Ticker: input.Ticker, MarketCapUSD: input.MarketCapUSD,
		RevenueGrowthPct: growth, CashRunwayMonths: input.Financial.CashRunwayMonths,
		RecentQualifiedInsider: recentInsider, ActiveBlocksA: blocksA, ActiveBlocksB: blocksB,
		ScoringVersion: DiscoveryScoringVersion,
	}
	if input.Financial.RevenueGrowthAvailable {
		switch {
		case growth >= 40:
			score.RevenueGrowthScore = 30
		case growth >= 20:
			score.RevenueGrowthScore = 20
		case growth > 0:
			score.RevenueGrowthScore = 10
		}
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

	score.EligibleA = input.MarketCapUSD >= 30_000_000 && input.MarketCapUSD < 500_000_000 &&
		input.Financial.RevenueGrowthAvailable && growth > 40 &&
		input.Financial.RunwayAvailable && input.Financial.CashRunwayMonths >= 12 &&
		recentInsider && !blocksA && !blocksB
	score.EligibleB = input.MarketCapUSD >= 30_000_000 && input.MarketCapUSD < 1_000_000_000 &&
		input.Financial.RevenueGrowthAvailable && growth > 20 && !blocksB
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
		ScoringVersion: score.ScoringVersion, CreatedAt: now,
	}
}

func hasRecentQualifiedInsider(rows []InsiderTransactionSnapshot, asOf time.Time) bool {
	cutoff := asOf.AddDate(0, 0, -180)
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
