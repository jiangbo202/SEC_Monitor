package discovery

import "testing"

func TestComparableCashRunwayDoesNotTreatPositiveCashFlowSentinelAsLiteral999Months(t *testing.T) {
	if got := comparableCashRunwayMonths(MaxCashRunwayMonths); got != 60 {
		t.Fatalf("sentinel comparable value=%v, want 60", got)
	}
	if !candidateSortLess(CandidateScoreResult{CandidateScoreSnapshot: CandidateScoreSnapshot{CashRunwayMonths: 24}}, CandidateScoreResult{CandidateScoreSnapshot: CandidateScoreSnapshot{CashRunwayMonths: MaxCashRunwayMonths}}, "cash_runway_months") {
		t.Fatal("positive cash flow sentinel should sort above a 24-month runway")
	}
	if !candidateSortLess(CandidateScoreResult{CandidateScoreSnapshot: CandidateScoreSnapshot{CashRunwayMonths: MaxCashRunwayMonths}}, CandidateScoreResult{CandidateScoreSnapshot: CandidateScoreSnapshot{CashRunwayMonths: 72}}, "cash_runway_months") {
		t.Fatal("positive cash flow sentinel must not sort above a measured 72-month runway")
	}
}
