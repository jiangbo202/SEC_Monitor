package discovery

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestTickerEvaluationSummaryLoadsImmutableDetail(t *testing.T) {
	db := openMigratedTestDatabase(t)
	input := TickerEvaluationResult{Ticker: "TEST", CompanyName: "Original issuer", EvaluatedAt: time.Now(), CandidateScore: CandidateScoreResult{CandidateScoreSnapshot: CandidateScoreSnapshot{TotalScore: 72}}, Research: TickerEvaluationResearchSnapshot{Profile: CompanyProfile{BusinessSummary: strings.Repeat("original profile", 1000)}}}
	saved, err := SaveTickerEvaluation(context.Background(), db, input)
	if err != nil {
		t.Fatal(err)
	}
	page, err := ListTickerEvaluations(context.Background(), db, TickerEvaluationFilter{Ticker: "TEST", SummaryOnly: true})
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("page=%+v err=%v", page, err)
	}
	encoded, err := json.Marshal(page.Items[0])
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "\"research\"") || !page.Items[0].SummaryOnly || page.Items[0].CandidateScore.TotalScore != 72 {
		t.Fatal("summary is not compact or lost score")
	}
	detail, err := GetTickerEvaluation(context.Background(), db, saved.ID)
	if err != nil || detail.Research.Profile.BusinessSummary != input.Research.Profile.BusinessSummary || detail.CandidateScore.TotalScore != 72 {
		t.Fatal("history changed", err)
	}
}
