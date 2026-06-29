package discovery

import "testing"

func TestExplainSectorScoreUsesSICAndScore(t *testing.T) {
	explanation := ExplainSectorScore(CandidateScoreSnapshot{SectorScore: 9, Grade: CandidateGradeA}, Security{SIC: 2834})
	if explanation.Label != "优秀赛道" || explanation.SIC != 2834 || explanation.Score != 9 {
		t.Fatalf("explanation = %#v", explanation)
	}
	if explanation.Rationale == "" || explanation.Category == "" {
		t.Fatalf("missing rationale/category: %#v", explanation)
	}
}
