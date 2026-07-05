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

func TestSectorRatingMapsCommonSICGroups(t *testing.T) {
	tests := []struct {
		name     string
		sic      int
		category string
		score    int
	}{
		{name: "biotech", sic: 2834, category: "生物医药", score: 9},
		{name: "medical devices", sic: 3841, category: "医疗器械", score: 8},
		{name: "software", sic: 7372, category: "软件与数据服务", score: 9},
		{name: "semiconductor", sic: 3674, category: "电子/半导体", score: 8},
		{name: "energy", sic: 1311, category: "能源", score: 6},
		{name: "mining", sic: 1040, category: "矿业/资源", score: 5},
		{name: "retail", sic: 5961, category: "消费/零售", score: 5},
		{name: "other valid sic", sic: 999, category: "其他已分类赛道", score: 5},
		{name: "missing sic", sic: 0, category: "赛道数据缺失", score: 0},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			rating := SectorRatingForSIC(test.sic)
			if rating.Category != test.category || rating.Score != test.score {
				t.Fatalf("rating = %#v", rating)
			}
		})
	}
}
