package discovery

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	CatalystEvidenceFact         = "fact"
	CatalystEvidenceUserJudgment = "user_judgment"
	CatalystTimingObserved       = "observed"
	CatalystTimingScheduled      = "scheduled"
	CatalystTimingNeedsSource    = "needs_source"
)

// CandidateCatalystEvent keeps sourced facts separate from user-authored
// expectations. SEC rows describe events that already happened; a manually
// entered future catalyst is a research decision and is never relabeled as a
// confirmed company event.
type CandidateCatalystEvent struct {
	ID           string              `json:"id"`
	Ticker       string              `json:"ticker"`
	EventType    string              `json:"event_type"`
	Title        string              `json:"title"`
	EventDate    time.Time           `json:"event_date"`
	TimingStatus string              `json:"timing_status"`
	EvidenceType string              `json:"evidence_type"`
	Source       string              `json:"source"`
	SourceURL    string              `json:"source_url,omitempty"`
	Quality      DataQualityMetadata `json:"quality"`
}

func BuildCandidateCatalystTimeline(detail CandidateDetail) []CandidateCatalystEvent {
	result := make([]CandidateCatalystEvent, 0, len(detail.RecentFilings)+len(detail.CapitalRisks)+1)
	for _, filing := range detail.RecentFilings {
		title, eventType := secResearchEventLabel(filing)
		asOf := filing.FilingDate
		if filing.PublishedAt != nil {
			asOf = *filing.PublishedAt
		}
		result = append(result, CandidateCatalystEvent{
			ID: fmt.Sprintf("sec:%s", filing.AccessionNumber), Ticker: detail.Score.Ticker,
			EventType: eventType, Title: title, EventDate: filing.FilingDate,
			TimingStatus: CatalystTimingObserved, EvidenceType: CatalystEvidenceFact,
			Source: "SEC EDGAR", SourceURL: filing.FilingURL,
			Quality: researchQualityMetadata(DataLayerFact, "sec_edgar", filing.AccessionNumber, asOf, 0, 0),
		})
	}
	for _, risk := range detail.CapitalRisks {
		result = append(result, CandidateCatalystEvent{
			ID: fmt.Sprintf("capital-risk:%d", risk.ID), Ticker: detail.Score.Ticker,
			EventType: "capital_event", Title: capitalRiskCatalystTitle(risk.Reason), EventDate: risk.EffectiveAt,
			TimingStatus: CatalystTimingObserved, EvidenceType: CatalystEvidenceFact,
			Source:  "SEC capital-risk parser",
			Quality: researchQualityMetadata(DataLayerFeature, "sec_capital_risk", risk.Accession, risk.AcceptedAt, 0, 0),
		})
	}
	if detail.Research != nil && strings.TrimSpace(detail.Research.Catalyst) != "" {
		date := detail.Research.UpdatedAt
		if detail.Research.CatalystDate != nil {
			date = *detail.Research.CatalystDate
		}
		sourceURL := strings.TrimSpace(detail.Research.CatalystSource)
		status, quality := CatalystTimingNeedsSource, QualityStatusMissing
		if sourceURL != "" && detail.Research.CatalystDate != nil {
			status, quality = CatalystTimingScheduled, QualityStatusValid
		}
		result = append(result, CandidateCatalystEvent{
			ID: fmt.Sprintf("user:%d", detail.Research.ID), Ticker: detail.Score.Ticker,
			EventType: "user_catalyst", Title: detail.Research.Catalyst, EventDate: date,
			TimingStatus: status, EvidenceType: CatalystEvidenceUserJudgment,
			Source: "local_user", SourceURL: sourceURL,
			Quality: DataQualityMetadata{Layer: DataLayerDecision, Source: "local_user", AsOf: detail.Research.UpdatedAt.UTC().Format(time.RFC3339), QualityStatus: quality},
		})
	}
	sort.SliceStable(result, func(i, j int) bool {
		leftScheduled := result[i].TimingStatus == CatalystTimingScheduled || result[i].TimingStatus == CatalystTimingNeedsSource
		rightScheduled := result[j].TimingStatus == CatalystTimingScheduled || result[j].TimingStatus == CatalystTimingNeedsSource
		if leftScheduled != rightScheduled {
			return leftScheduled
		}
		return result[i].EventDate.After(result[j].EventDate)
	})
	return result
}

func capitalRiskCatalystTitle(reason string) string {
	if strings.Contains(strings.ToLower(reason), "potential share-count change") {
		return "潜在股本变化（保守风险提示，不代表已确认发行）"
	}
	return reason
}

func secResearchEventLabel(filing RecentSECFiling) (string, string) {
	form := strings.ToUpper(strings.TrimSpace(filing.FilingType))
	switch {
	case strings.HasPrefix(form, "10-Q"), strings.HasPrefix(form, "10-K"):
		return filing.Title, "financial_report"
	case strings.HasPrefix(form, "S-1"), strings.HasPrefix(form, "S-3"), strings.HasPrefix(form, "424B"):
		return filing.Title, "financing"
	case strings.HasPrefix(form, "8-K"):
		return filing.Title, "material_event"
	case strings.HasPrefix(form, "DEF 14A"):
		return filing.Title, "shareholder_meeting"
	case strings.HasPrefix(form, "SC 13"):
		return filing.Title, "ownership_change"
	default:
		return filing.Title, "sec_filing"
	}
}
