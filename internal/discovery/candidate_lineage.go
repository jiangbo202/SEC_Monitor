package discovery

import (
	"fmt"
	"strings"
	"time"
)

// CandidateDataLineage makes the current candidate's inputs auditable without
// issuing live SEC or market-data requests from the detail page. The values
// point to the immutable local batch and snapshot records used by the score.
type CandidateDataLineage struct {
	ScoreBatchID       string                 `json:"score_batch_id"`
	EvidenceBatchID    string                 `json:"evidence_batch_id"`
	BatchEffectiveDate string                 `json:"batch_effective_date"`
	Items              []CandidateLineageItem `json:"items"`
}

type CandidateLineageItem struct {
	Key    string `json:"key"`
	Label  string `json:"label"`
	Source string `json:"source"`
	AsOf   string `json:"as_of"`
	Status string `json:"status"`
	Detail string `json:"detail"`
}

func buildCandidateDataLineage(detail CandidateDetail, batch UniverseBatch, evidenceBatchID string, market CandidateScoreResult, share *ShareSnapshot) CandidateDataLineage {
	lineage := CandidateDataLineage{
		ScoreBatchID:       detail.BatchID,
		EvidenceBatchID:    evidenceBatchID,
		BatchEffectiveDate: strings.TrimSpace(batch.EffectiveDate),
		Items:              make([]CandidateLineageItem, 0, 8),
	}
	lineage.Items = append(lineage.Items, CandidateLineageItem{
		Key: "score", Label: "候选评分", Source: "本地候选评分快照", AsOf: bestLineageDate(batch.EffectiveDate, detail.Score.CreatedAt), Status: QualityStatusValid,
		Detail: fmt.Sprintf("评分版本 %s；总分 %d / 100", stringOrDefault(detail.Score.ScoringVersion, "未标记"), detail.Score.TotalScore),
	})

	priceStatus := stringOrDefault(market.PriceQualityStatus, detail.DataQuality["universe"])
	lineage.Items = append(lineage.Items, CandidateLineageItem{
		Key: "market", Label: "价格与市值", Source: "本地日线价格快照 + SEC 股本快照", AsOf: lineageDatePtr(market.PriceTradeDate), Status: lineageStatus(priceStatus),
		Detail: marketLineageDetail(market, share),
	})

	financialStatus := detail.DataQuality["financial"]
	financialAsOf := ""
	financialDetail := "未形成可用财务指标"
	if detail.Financial != nil {
		financialAsOf = dateOnly(detail.Financial.CreatedAt)
		financialDetail = fmt.Sprintf("解析版本 %s；收入增长与现金 runway 由 SEC Company Facts 计算", stringOrDefault(detail.Financial.ParserVersion, "未标记"))
	}
	lineage.Items = append(lineage.Items, CandidateLineageItem{
		Key: "financial", Label: "财务指标", Source: "SEC Company Facts → 本地财务指标快照", AsOf: financialAsOf, Status: lineageStatus(financialStatus), Detail: financialDetail,
	})

	insiderStatus := detail.DataQuality["insider_coverage"]
	insiderAsOf := ""
	insiderDetail := "尚未保留 Form 4 覆盖结论"
	if detail.InsiderCoverage != nil {
		insiderAsOf = dateOnly(detail.InsiderCoverage.CheckedAt)
		insiderDetail = fmt.Sprintf("覆盖状态 %s；可解析文件 %d / 合格文件 %d；交易记录 %d", detail.InsiderCoverage.Status, detail.InsiderCoverage.ParsedDocuments, detail.InsiderCoverage.EligibleFilings, detail.InsiderCoverage.TransactionCount)
	} else if len(detail.Insiders) > 0 {
		insiderAsOf = dateOnly(detail.Insiders[0].TransactionDate)
		insiderDetail = fmt.Sprintf("已保存 %d 条内幕交易；未保留本批覆盖结论", len(detail.Insiders))
	}
	lineage.Items = append(lineage.Items, CandidateLineageItem{
		Key: "insider", Label: "内幕交易", Source: "SEC Form 4 → 本地内幕交易快照", AsOf: insiderAsOf, Status: lineageStatus(insiderStatus), Detail: insiderDetail,
	})

	filingAsOf := ""
	filingDetail := "暂无本地 SEC 公告索引"
	if len(detail.RecentFilings) > 0 {
		filingAsOf = dateOnly(detail.RecentFilings[0].FilingDate)
		filingDetail = fmt.Sprintf("已索引最近 %d 条公告；最新 %s", len(detail.RecentFilings), detail.RecentFilings[0].FilingType)
	}
	lineage.Items = append(lineage.Items, CandidateLineageItem{
		Key: "filings", Label: "近期公告", Source: "SEC submissions → 本地公告索引", AsOf: filingAsOf, Status: lineageStatus(detail.DataQuality["recent_filings"]), Detail: filingDetail,
	})

	riskAsOf := ""
	if detail.CapitalRiskSummary.LatestEffectiveAt != nil {
		riskAsOf = dateOnly(*detail.CapitalRiskSummary.LatestEffectiveAt)
	}
	riskDetail := fmt.Sprintf("活跃风险 %d；近 180 日已失效风险 %d", detail.CapitalRiskSummary.ActiveEvents, detail.CapitalRiskSummary.RecentInactiveEvents)
	lineage.Items = append(lineage.Items, CandidateLineageItem{
		Key: "capital_risk", Label: "融资/稀释风险", Source: "SEC submissions → 本地融资风险快照", AsOf: riskAsOf, Status: lineageStatus(detail.DataQuality["capital_risk"]), Detail: riskDetail,
	})

	profileAsOf := ""
	if detail.CompanyProfile.ProfileFetchedAt != nil {
		profileAsOf = dateOnly(*detail.CompanyProfile.ProfileFetchedAt)
	} else if detail.CompanyProfile.MetadataAsOf != nil {
		profileAsOf = dateOnly(*detail.CompanyProfile.MetadataAsOf)
	}
	lineage.Items = append(lineage.Items, CandidateLineageItem{
		Key: "company_profile", Label: "公司资料", Source: stringOrDefault(detail.CompanyProfile.SummarySource, "SEC 发行人元数据"), AsOf: profileAsOf, Status: lineageStatus(detail.CompanyProfile.Status),
		Detail: "详情页只读取本地缓存；手动刷新公司资料才会请求 Longbridge",
	})
	return lineage
}

func marketLineageDetail(market CandidateScoreResult, share *ShareSnapshot) string {
	parts := []string{}
	if source := strings.TrimSpace(market.PriceSource); source != "" {
		parts = append(parts, "价格来源 "+source)
	}
	if freshness := strings.TrimSpace(market.PriceFreshnessStatus); freshness != "" {
		parts = append(parts, "新鲜度 "+freshness)
	}
	if share != nil {
		parts = append(parts, fmt.Sprintf("股本来自 %s（%s）", stringOrDefault(share.Form, "SEC"), dateOnly(share.FiledAt)))
	}
	if len(parts) == 0 {
		return "尚未关联可用价格或股本快照"
	}
	return strings.Join(parts, "；")
}

func lineageStatus(status string) string {
	status = strings.TrimSpace(status)
	switch status {
	case "", QualityStatusMissing, "legacy_not_evaluated":
		return QualityStatusMissing
	case QualityStatusValid, "available", "complete":
		return QualityStatusValid
	case "partial", "unavailable", "no_filings":
		return "partial"
	default:
		return status
	}
}

func bestLineageDate(batchDate string, fallback time.Time) string {
	if strings.TrimSpace(batchDate) != "" {
		return strings.TrimSpace(batchDate)
	}
	return dateOnly(fallback)
}

func lineageDatePtr(value *time.Time) string {
	if value == nil {
		return ""
	}
	return dateOnly(*value)
}

func dateOnly(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.DateOnly)
}
