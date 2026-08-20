package discovery

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

const CandidateScoringDisclaimer = "评分仅用于研究排序，反映已保存证据在当前规则下的匹配程度；不是涨跌概率、目标收益或投资建议。"

type CandidateScoringRule struct {
	Condition string `json:"condition"`
	Points    int    `json:"points"`
}

type CandidateScoringDimension struct {
	Key        string                 `json:"key"`
	Label      string                 `json:"label"`
	MaxPoints  int                    `json:"max_points"`
	WeightPct  int                    `json:"weight_pct"`
	Evidence   string                 `json:"evidence"`
	Rules      []CandidateScoringRule `json:"rules"`
	Adjustment string                 `json:"adjustment,omitempty"`
}

// CandidateScoringRubric is a serializable explanation of the deterministic
// score. It is stored with every score snapshot so historical results remain
// explainable after code or policy changes.
type CandidateScoringRubric struct {
	Version       string                      `json:"version"`
	Name          string                      `json:"name"`
	Formula       string                      `json:"formula"`
	MaxScore      int                         `json:"max_score"`
	Dimensions    []CandidateScoringDimension `json:"dimensions"`
	GradeRuleNote string                      `json:"grade_rule_note"`
	Disclaimer    string                      `json:"disclaimer"`
	ContentSHA256 string                      `json:"content_sha256"`
}

func CurrentCandidateScoringRubric() CandidateScoringRubric {
	return CandidateScoringRubricForPolicy(DefaultSmallCapPolicy())
}

func CandidateScoringRubricForPolicy(policy SmallCapPolicy) CandidateScoringRubric {
	if normalized, err := NormalizeSmallCapPolicy(policy); err == nil {
		policy = normalized
	} else {
		policy = DefaultSmallCapPolicy()
	}
	rubric := CandidateScoringRubric{
		Version:  DiscoveryScoringVersion,
		Name:     "小盘候选确定性基本面评分卡",
		Formula:  "总分 = 收入增长 + 现金储备 + 内幕增持 + 毛利率 + 稀释风险 + 赛道空间",
		MaxScore: 100,
		Dimensions: []CandidateScoringDimension{
			{Key: "revenue_growth", Label: "收入增长", MaxPoints: 30, WeightPct: 30, Evidence: "SEC 财务事实；优先季度同比，缺失时回退年度同比", Rules: []CandidateScoringRule{{Condition: "增长率 ≥ 40%", Points: 30}, {Condition: "20% ≤ 增长率 < 40%", Points: 20}, {Condition: "0% < 增长率 < 20%", Points: 10}, {Condition: "增长率 ≤ 0% 或证据缺失", Points: 0}}, Adjustment: "临床前收入、一次性授权收入或未确认业务模型可触发收入分上限"},
			{Key: "cash_runway", Label: "现金储备", MaxPoints: 20, WeightPct: 20, Evidence: "SEC 现金与经营现金流快照", Rules: []CandidateScoringRule{{Condition: "现金 runway ≥ 12 个月", Points: 20}, {Condition: "6 ≤ 现金 runway < 12 个月", Points: 10}, {Condition: "现金 runway < 6 个月或证据缺失", Points: 0}}},
			{Key: "qualified_insider", Label: "内幕增持", MaxPoints: 20, WeightPct: 20, Evidence: "SEC Form 4 公开市场买入", Rules: []CandidateScoringRule{{Condition: fmt.Sprintf("近 %d 日 CEO、CFO 或创始人存在合格买入", policy.InsiderLookbackDays), Points: 20}, {Condition: "不满足或覆盖证据不足", Points: 0}}},
			{Key: "gross_margin", Label: "毛利率", MaxPoints: 10, WeightPct: 10, Evidence: "SEC 财务事实计算的毛利率", Rules: []CandidateScoringRule{{Condition: "毛利率 ≥ 50%", Points: 10}, {Condition: "30% ≤ 毛利率 < 50%", Points: 5}, {Condition: "毛利率 < 30% 或证据缺失", Points: 0}}},
			{Key: "dilution_risk", Label: "稀释风险", MaxPoints: 10, WeightPct: 10, Evidence: "SEC 融资、稀释与持续经营风险事件", Rules: []CandidateScoringRule{{Condition: "不存在生效中的 A/B 级阻断", Points: 10}, {Condition: "存在任一生效阻断", Points: 0}}},
			{Key: "sector", Label: "赛道空间", MaxPoints: 10, WeightPct: 10, Evidence: "SIC 分类与人工确认的赛道规则", Rules: []CandidateScoringRule{{Condition: "使用 0–10 的赛道原始分，超过 10 按 10 计", Points: 10}, {Condition: "无正向赛道分", Points: 0}}},
		},
		GradeRuleNote: "A/B 等级由市值、增长、现金、内幕、赛道及风险闸门单独决定；总分相同不保证等级相同。",
		Disclaimer:    CandidateScoringDisclaimer,
	}
	payload, _ := json.Marshal(rubric)
	digest := sha256.Sum256(payload)
	rubric.ContentSHA256 = hex.EncodeToString(digest[:])
	return rubric
}

func candidateScoringRubricJSON(policy SmallCapPolicy) (string, string) {
	rubric := CandidateScoringRubricForPolicy(policy)
	payload, _ := json.Marshal(rubric)
	return string(payload), rubric.ContentSHA256
}

func CandidateScoringRubricForSnapshot(score CandidateScoreSnapshot) CandidateScoringRubric {
	if raw := strings.TrimSpace(score.ScoringRubricJSON); raw != "" {
		var rubric CandidateScoringRubric
		if json.Unmarshal([]byte(raw), &rubric) == nil && rubric.Version != "" {
			return rubric
		}
	}
	if score.ScoringVersion == "" || score.ScoringVersion == DiscoveryScoringVersion {
		return CurrentCandidateScoringRubric()
	}
	return CandidateScoringRubric{
		Version: score.ScoringVersion, Name: "历史评分卡", MaxScore: 100,
		GradeRuleNote: "该旧快照未保存完整评分公式，不能使用当前公式反推。",
		Disclaimer:    CandidateScoringDisclaimer,
	}
}
