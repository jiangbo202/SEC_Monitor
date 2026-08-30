package discovery

import (
	"context"
	"errors"
	"testing"
	"time"

	lbfundamental "github.com/longbridge/openapi-go/fundamental"
	lbmarket "github.com/longbridge/openapi-go/market"
	"github.com/shopspring/decimal"
)

type fakeLongbridgeCandidateResearchClient struct {
	forecast      *lbfundamental.ForecastEps
	anomalies     *lbmarket.AnomalyResponse
	shareholders  *lbfundamental.ShareholderList
	fundHolders   *lbfundamental.FundHolders
	forecastError error
}

func (f *fakeLongbridgeCandidateResearchClient) ForecastEps(context.Context, string) (*lbfundamental.ForecastEps, error) {
	return f.forecast, f.forecastError
}
func (f *fakeLongbridgeCandidateResearchClient) Anomaly(context.Context, string) (*lbmarket.AnomalyResponse, error) {
	return f.anomalies, nil
}
func (f *fakeLongbridgeCandidateResearchClient) Shareholder(context.Context, string) (*lbfundamental.ShareholderList, error) {
	return f.shareholders, nil
}
func (f *fakeLongbridgeCandidateResearchClient) FundHolder(context.Context, string) (*lbfundamental.FundHolders, error) {
	return f.fundHolders, nil
}

func TestResearchRequestFailureIsNotSuccessfulNoCoverage(t *testing.T) {
	db := openMigratedTestDatabase(t)
	client := &fakeLongbridgeCandidateResearchClient{forecastError: errors.New("provider timeout")}
	options := LongbridgeCandidateResearchOptions{AppKey: "key", AppSecret: "secret", AccessToken: "token", NewClient: func(_, _, _ string) (longbridgeCandidateResearchClient, error) { return client, nil }}
	if _, err := refreshLongbridgeCandidateMarketResearch(context.Background(), db, "TEST", "", options); err == nil {
		t.Fatal("request failure was swallowed as a coverage warning")
	}
	client.forecastError = nil
	if _, err := refreshLongbridgeCandidateMarketResearch(context.Background(), db, "TEST", "", options); err != nil {
		t.Fatalf("empty successful response should remain no-coverage: %v", err)
	}
}

func TestRefreshLongbridgeCandidateMarketResearchPersistsEvidenceAndEPSRevision(t *testing.T) {
	db := openMigratedTestDatabase(t)
	security := Security{CIK: "0000001234", CompanyName: "Research Co"}
	if err := db.Create(&security).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&Listing{SecurityID: security.ID, Ticker: "RCH", ValidFrom: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)}).Error; err != nil {
		t.Fatal(err)
	}
	mean, median, low, high := decimal.NewFromFloat(1.1), decimal.NewFromFloat(1.0), decimal.NewFromFloat(0.8), decimal.NewFromFloat(1.2)
	client := &fakeLongbridgeCandidateResearchClient{
		forecast:     &lbfundamental.ForecastEps{Items: []lbfundamental.ForecastEpsItem{{ForecastEpsMean: &mean, ForecastEpsMedian: &median, ForecastEpsLowest: &low, ForecastEpsHighest: &high, InstitutionTotal: 8, InstitutionUp: 3, InstitutionDown: 2, ForecastStartDate: time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC)}}},
		anomalies:    &lbmarket.AnomalyResponse{Changes: []lbmarket.AnomalyItem{{Symbol: "RCH.US", AlertName: "大笔买入", AlertTime: 1_786_132_172_000, ChangeValues: []string{"10,000 股"}, Emotion: 1}}},
		shareholders: &lbfundamental.ShareholderList{ShareholderList: []lbfundamental.Shareholder{{ShareholderName: "Example Capital", InstitutionType: "Fund", PercentOfShares: &high, SharesChanged: &mean, ReportDate: "2026-06-30"}}},
		fundHolders:  &lbfundamental.FundHolders{Lists: []lbfundamental.FundHolder{{Code: "ETF1", Symbol: "ETF1.US", Name: "Example ETF", Currency: "USD", PositionRatio: high, ReportDate: "2026.06.30"}}},
	}
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	options := LongbridgeCandidateResearchOptions{AppKey: "key", AppSecret: "secret", AccessToken: "token", Now: func() time.Time { return now }, NewClient: func(_, _, _ string) (longbridgeCandidateResearchClient, error) { return client, nil }}
	first, err := refreshLongbridgeCandidateMarketResearch(context.Background(), db, "rch", security.CIK, options)
	if err != nil {
		t.Fatal(err)
	}
	if !first.EPSFetched || first.EPSChanged || first.AnomaliesSaved != 1 || first.ShareholdersSaved != 1 || first.FundHoldersSaved != 1 {
		t.Fatalf("first refresh = %+v", first)
	}

	updatedMedian := decimal.NewFromFloat(0.9)
	client.forecast.Items[0].ForecastEpsMedian = &updatedMedian
	now = now.Add(24 * time.Hour)
	second, err := refreshLongbridgeCandidateMarketResearch(context.Background(), db, "RCH", security.CIK, options)
	if err != nil {
		t.Fatal(err)
	}
	if !second.EPSChanged || second.EPSChangeSummary == "" {
		t.Fatalf("expected EPS revision, got %+v", second)
	}
	view, err := GetCandidateMarketResearch(context.Background(), db, "rch")
	if err != nil {
		t.Fatal(err)
	}
	if view.EPSForecast.Latest == nil || len(view.EPSForecast.History) != 2 || len(view.Anomalies) != 1 || len(view.InstitutionalHolders) != 1 || len(view.FundHolders) != 1 {
		t.Fatalf("research view = %+v", view)
	}
	if view.EPSForecast.Latest.ChangeSummary == "" {
		t.Fatalf("latest EPS snapshot should retain change summary: %+v", view.EPSForecast.Latest)
	}
	if view.EPSRevision.Status != "available" || view.EPSRevision.Direction != "down" || view.EPSRevision.PreviousMedian == nil || view.EPSRevision.CurrentMedian == nil {
		t.Fatalf("EPS revision summary = %+v", view.EPSRevision)
	}
	if view.EarningsSurprise.Status != "unavailable" {
		t.Fatalf("earnings surprise must remain gated without point-in-time actual EPS: %+v", view.EarningsSurprise)
	}
}

func TestCandidateReviewPriorityIncludesRecentAnomaly(t *testing.T) {
	item := CandidateScoreResult{CandidateScoreSnapshot: CandidateScoreSnapshot{Grade: CandidateGradeB, TotalScore: 70}, QualityAdjustedScore: 50, RecentAnomalyLabels: []string{"大笔买入"}}
	reasons := candidateReviewPriorityReasons(item)
	if !containsPriorityReason(reasons, "异动：大笔买入", 6) {
		t.Fatalf("reasons = %#v", reasons)
	}
}
