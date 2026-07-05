package discovery

import "fmt"

type SectorExplanation struct {
	SIC       int    `json:"sic"`
	Category  string `json:"category"`
	Score     int    `json:"score"`
	Label     string `json:"label"`
	Rationale string `json:"rationale"`
}

type SectorRating struct {
	SIC      int
	Category string
	Score    int
}

func ExplainSectorScore(score CandidateScoreSnapshot, security Security) SectorExplanation {
	rating := SectorRatingForSIC(security.SIC)
	category := rating.Category
	return SectorExplanation{
		SIC:       security.SIC,
		Category:  category,
		Score:     score.SectorScore,
		Label:     sectorLabel(score.SectorScore),
		Rationale: fmt.Sprintf("基于 SIC %d 归类为%s，当前赛道分为 %d/10。", security.SIC, category, score.SectorScore),
	}
}

func sectorLabel(score int) string {
	switch {
	case score >= 7:
		return "优秀赛道"
	case score >= 5:
		return "可接受赛道"
	case score > 0:
		return "弱赛道"
	default:
		return "赛道数据不足"
	}
}

func SectorRatingForSIC(sic int) SectorRating {
	return SectorRating{SIC: sic, Category: sectorCategory(sic), Score: sectorScore(sic)}
}

func sectorCategory(sic int) string {
	switch {
	case sic < 100 || sic > 9999:
		return "赛道数据缺失"
	case sic >= 1300 && sic <= 1399:
		return "能源"
	case sic >= 1000 && sic <= 1499:
		return "矿业/资源"
	case sic >= 2800 && sic <= 2829:
		return "化工/生命科学材料"
	case sic >= 2830 && sic <= 2836:
		return "生物医药"
	case sic >= 2837 && sic <= 2899:
		return "化工/生命科学材料"
	case sic >= 3500 && sic <= 3569:
		return "工业制造"
	case sic >= 3570 && sic <= 3579:
		return "计算机硬件"
	case sic >= 3580 && sic <= 3599:
		return "工业制造"
	case sic >= 3600 && sic <= 3679:
		return "电子/半导体"
	case sic >= 3800 && sic <= 3899:
		return "医疗器械"
	case sic >= 4800 && sic <= 4899:
		return "通信服务"
	case sic >= 5000 && sic <= 5999:
		return "消费/零售"
	case sic >= 7000 && sic <= 7369:
		return "商业服务"
	case sic >= 7370 && sic <= 7379:
		return "软件与数据服务"
	case sic >= 7380 && sic <= 7399:
		return "商业服务"
	case sic >= 7800 && sic <= 7999:
		return "消费服务"
	case sic >= 8000 && sic <= 8099:
		return "医疗服务"
	case sic >= 8200 && sic <= 8299:
		return "教育服务"
	case sic >= 8700 && sic <= 8999:
		return "专业服务"
	default:
		return "其他已分类赛道"
	}
}

func sectorScore(sic int) int {
	switch {
	case sic < 100 || sic > 9999:
		return 0
	case sic >= 2830 && sic <= 2836:
		return 9
	case sic >= 7370 && sic <= 7379:
		return 9
	case sic >= 3600 && sic <= 3679:
		return 8
	case sic >= 3800 && sic <= 3899:
		return 8
	case sic >= 4800 && sic <= 4899:
		return 7
	case sic >= 8000 && sic <= 8099:
		return 7
	case sic >= 1300 && sic <= 1399:
		return 6
	case sic >= 2800 && sic <= 2899:
		return 6
	case sic >= 3500 && sic <= 3599:
		return 6
	case sic >= 8700 && sic <= 8999:
		return 6
	case sic >= 1000 && sic <= 1499:
		return 5
	case sic >= 5000 && sic <= 5999:
		return 5
	case sic >= 7000 && sic <= 7399:
		return 5
	case sic >= 7800 && sic <= 7999:
		return 5
	case sic >= 8200 && sic <= 8299:
		return 5
	default:
		return 5
	}
}
