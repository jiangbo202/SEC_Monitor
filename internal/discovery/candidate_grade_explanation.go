package discovery

import (
	"fmt"
	"strings"
)

const (
	CandidateCashRunwayMeasured         = "measured"
	CandidateCashRunwayPositiveCashFlow = "positive_cash_flow"
	CandidateCashRunwayUnavailable      = "unavailable"
)

type CandidateGradeExplanation struct {
	Profile          string   `json:"profile"`
	Summary          string   `json:"summary"`
	UnmetAConditions []string `json:"unmet_a_conditions"`
	NearA            bool     `json:"near_a"`
}

func annotateCandidateGradeExplanations(items []CandidateScoreResult, policy SmallCapPolicy) {
	if normalized, err := NormalizeSmallCapPolicy(policy); err == nil {
		policy = normalized
	} else {
		policy = DefaultSmallCapPolicy()
	}
	for i := range items {
		item := &items[i]
		switch {
		case item.CashRunwayMonths >= MaxCashRunwayMonths:
			item.CashRunwayStatus = CandidateCashRunwayPositiveCashFlow
		case item.CashRunwayMonths > 0:
			item.CashRunwayStatus = CandidateCashRunwayMeasured
		default:
			item.CashRunwayStatus = CandidateCashRunwayUnavailable
		}
		explanation := CandidateGradeExplanation{UnmetAConditions: []string{}}
		switch item.Grade {
		case CandidateGradeA:
			explanation.Profile = "a_strong_signal"
			explanation.Summary = "已满足强信号画像的全部硬门槛；内幕买入和赛道分仅影响总分。"
		case CandidateGradeB:
			explanation.Profile = "b_growth_watch"
			explanation.UnmetAConditions = candidateUnmetAConditions(*item, policy)
			explanation.NearA = len(explanation.UnmetAConditions) == 1
			if len(explanation.UnmetAConditions) == 0 {
				explanation.Summary = "当前快照保存为成长观察画像；按现行规则重新评分后可能进入强信号画像。"
			} else if explanation.NearA {
				explanation.Summary = "距强信号画像还差 1 项：" + explanation.UnmetAConditions[0]
			} else {
				explanation.Summary = fmt.Sprintf("距强信号画像还差 %d 项：%s", len(explanation.UnmetAConditions), strings.Join(explanation.UnmetAConditions, "；"))
			}
		default:
			explanation.Profile = "excluded"
			explanation.Summary = "未满足当前候选画像的硬门槛。"
		}
		item.GradeExplanation = explanation
	}
}

func candidateUnmetAConditions(item CandidateScoreResult, policy SmallCapPolicy) []string {
	reasons := []string{}
	if item.MarketCapUSD < policy.MarketCapMinUSD {
		reasons = append(reasons, fmt.Sprintf("市值低于强信号画像下限 %s", formatCandidateUSD(policy.MarketCapMinUSD)))
	} else if item.MarketCapUSD >= policy.AMarketCapMaxExclusiveUSD {
		over := item.MarketCapUSD - policy.AMarketCapMaxExclusiveUSD
		reasons = append(reasons, fmt.Sprintf("市值 %s，超过强信号画像上限 %s（超出 %s）", formatCandidateUSD(item.MarketCapUSD), formatCandidateUSD(policy.AMarketCapMaxExclusiveUSD), formatCandidateUSD(over)))
	}
	if item.RevenueGrowthPct < policy.ARevenueGrowthMinPct {
		reasons = append(reasons, fmt.Sprintf("收入增长 %.1f%%，低于强信号画像门槛 %.1f%%", item.RevenueGrowthPct, policy.ARevenueGrowthMinPct))
	}
	if item.CashRunwayMonths < policy.ARunwayMinMonths {
		reasons = append(reasons, fmt.Sprintf("现金 runway %.1f 个月，低于强信号画像门槛 %.1f 个月", item.CashRunwayMonths, policy.ARunwayMinMonths))
	}
	if item.ActiveBlocksA || item.ActiveBlocksB {
		reasons = append(reasons, "存在资本风险阻断")
	}
	if item.BusinessModel.Model == CandidateBusinessModelClinicalPreRevenue || item.BusinessModel.Model == CandidateBusinessModelUnknown ||
		(item.BusinessModel.Model == CandidateBusinessModelMixedOrLicensing && !item.BusinessModel.RevenueRepeatableConfirmed) {
		reasons = append(reasons, "业务模型尚不允许进入强信号画像")
	}
	return reasons
}

func formatCandidateUSD(value int64) string {
	if value >= 1_000_000_000 {
		return fmt.Sprintf("$%.2fB", float64(value)/1_000_000_000)
	}
	if value >= 1_000_000 {
		return fmt.Sprintf("$%.1fM", float64(value)/1_000_000)
	}
	if value >= 1_000 {
		return fmt.Sprintf("$%.1fK", float64(value)/1_000)
	}
	return fmt.Sprintf("$%d", value)
}
