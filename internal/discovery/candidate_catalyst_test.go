package discovery

import (
	"testing"
	"time"
)

func TestBuildCandidateCatalystTimelineSeparatesFactsAndUserJudgment(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	future := now.AddDate(0, 0, 10)
	published := now.AddDate(0, 0, -2)
	detail := CandidateDetail{
		Score:         CandidateScoreSnapshot{Ticker: "TEST"},
		RecentFilings: []RecentSECFiling{{AccessionNumber: "0001", FilingType: "10-Q", FilingDate: published, PublishedAt: &published, FilingURL: "https://www.sec.gov/test", Title: "10-Q"}},
		Research:      &CandidateWatch{ID: 7, Ticker: "TEST", Catalyst: "下一次产品数据", CatalystSource: "https://example.com/source", CatalystDate: &future, UpdatedAt: now},
	}
	items := BuildCandidateCatalystTimeline(detail)
	if len(items) != 2 {
		t.Fatalf("items=%#v", items)
	}
	if items[0].EvidenceType != CatalystEvidenceUserJudgment || items[0].TimingStatus != CatalystTimingScheduled || items[0].Quality.Layer != DataLayerDecision {
		t.Fatalf("user catalyst=%#v", items[0])
	}
	if items[1].EvidenceType != CatalystEvidenceFact || items[1].EventType != "financial_report" || items[1].Quality.Layer != DataLayerFact {
		t.Fatalf("SEC fact=%#v", items[1])
	}
}

func TestBuildCandidateCatalystTimelineMarksIncompleteUserSource(t *testing.T) {
	now := time.Now().UTC()
	detail := CandidateDetail{Score: CandidateScoreSnapshot{Ticker: "TEST"}, Research: &CandidateWatch{ID: 8, Ticker: "TEST", Catalyst: "待确认事项", UpdatedAt: now}}
	items := BuildCandidateCatalystTimeline(detail)
	if len(items) != 1 || items[0].TimingStatus != CatalystTimingNeedsSource || items[0].Quality.QualityStatus != QualityStatusMissing {
		t.Fatalf("items=%#v", items)
	}
}
