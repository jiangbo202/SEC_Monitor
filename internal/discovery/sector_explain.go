package discovery

import "fmt"

type SectorExplanation struct {
	SIC       int    `json:"sic"`
	Category  string `json:"category"`
	Score     int    `json:"score"`
	Label     string `json:"label"`
	Rationale string `json:"rationale"`
}

func ExplainSectorScore(score CandidateScoreSnapshot, security Security) SectorExplanation {
	category := sectorCategory(security.SIC)
	label := "普通赛道"
	switch {
	case score.SectorScore >= 8:
		label = "优秀赛道"
	case score.SectorScore >= 5:
		label = "可接受赛道"
	case score.SectorScore > 0:
		label = "弱赛道"
	}
	return SectorExplanation{
		SIC:       security.SIC,
		Category:  category,
		Score:     score.SectorScore,
		Label:     label,
		Rationale: fmt.Sprintf("基于 SIC %d 归类为%s，当前赛道分为 %d/10。", security.SIC, category, score.SectorScore),
	}
}

func sectorCategory(sic int) string {
	switch {
	case sic >= 2830 && sic <= 2836:
		return "生物医药"
	case sic >= 2800 && sic <= 2899:
		return "化工/生命科学材料"
	case sic >= 3570 && sic <= 3579:
		return "计算机硬件"
	case sic >= 3600 && sic <= 3679:
		return "电子/半导体"
	case sic >= 4800 && sic <= 4899:
		return "通信服务"
	case sic >= 7370 && sic <= 7379:
		return "软件与数据服务"
	case sic >= 8000 && sic <= 8099:
		return "医疗服务"
	default:
		return "未分类赛道"
	}
}
