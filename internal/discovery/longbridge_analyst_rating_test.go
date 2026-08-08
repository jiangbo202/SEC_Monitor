package discovery

import (
	"context"
	"testing"
	"time"

	lbfundamental "github.com/longbridge/openapi-go/fundamental"
	"github.com/shopspring/decimal"
)

type fakeLongbridgeAnalystRatingClient struct {
	rating *lbfundamental.InstitutionRating
	err    error
	symbol string
}

func (f *fakeLongbridgeAnalystRatingClient) InstitutionRating(_ context.Context, symbol string) (*lbfundamental.InstitutionRating, error) {
	f.symbol = symbol
	return f.rating, f.err
}

func TestRefreshLongbridgeAnalystRatingStoresSnapshotsAndSemanticChanges(t *testing.T) {
	db := openMigratedTestDatabase(t)
	security := Security{CIK: "0000123456", CompanyName: "Analyst Co"}
	if err := db.Create(&security).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&Listing{SecurityID: security.ID, Ticker: "RATE", ValidFrom: time.Now().UTC(), MappingStatus: MappingStatusCurrent}).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 31, 0, 0, 0, 0, time.UTC)
	client := &fakeLongbridgeAnalystRatingClient{rating: testInstitutionRating(lbfundamental.InstitutionRecommendBuy, 10, 25)}
	options := LongbridgeAnalystRatingOptions{
		AppKey: "key", AppSecret: "secret", AccessToken: "token", TargetChangePct: 5,
		Now:       func() time.Time { return now },
		NewClient: func(_, _, _ string) (longbridgeAnalystRatingClient, error) { return client, nil },
	}
	first, err := refreshLongbridgeAnalystRating(context.Background(), db, "rate", security.CIK, options)
	if err != nil || !first.Fetched || first.Changed || client.symbol != "RATE.US" {
		t.Fatalf("first refresh = %+v, err=%v symbol=%q", first, err, client.symbol)
	}
	if first.Snapshot.Status != AnalystRatingStatusAvailable || first.Snapshot.NotificationStatus != "not_applicable" {
		t.Fatalf("first snapshot = %#v", first.Snapshot)
	}
	cached, err := refreshLongbridgeAnalystRating(context.Background(), db, "RATE", security.CIK, options)
	if err != nil || !cached.Cached || cached.Fetched {
		t.Fatalf("cached refresh = %+v, err=%v", cached, err)
	}
	now = now.Add(24 * time.Hour)
	client.rating = testInstitutionRating(lbfundamental.InstitutionRecommendHold, 12, 30)
	changed, err := refreshLongbridgeAnalystRating(context.Background(), db, "RATE", security.CIK, options)
	if err != nil || !changed.Fetched || !changed.Changed || changed.Snapshot.NotificationStatus != "pending" {
		t.Fatalf("changed refresh = %+v, err=%v", changed, err)
	}
	if changed.ChangeSummary == "" {
		t.Fatal("changed refresh should explain semantic change")
	}
	pending, err := PendingAnalystRatingNotifications(context.Background(), db, 10)
	if err != nil || len(pending) != 1 || pending[0].ID != changed.Snapshot.ID {
		t.Fatalf("pending = %#v, err=%v", pending, err)
	}
	view, err := GetAnalystRating(context.Background(), db, "rate")
	if err != nil || view.Latest == nil || view.Latest.Recommendation != "hold" || len(view.History) != 2 {
		t.Fatalf("view = %#v, err=%v", view, err)
	}
}

func TestRefreshLongbridgeAnalystRatingTreatsNoCoverageAsNormal(t *testing.T) {
	db := openMigratedTestDatabase(t)
	client := &fakeLongbridgeAnalystRatingClient{rating: &lbfundamental.InstitutionRating{}}
	result, err := refreshLongbridgeAnalystRating(context.Background(), db, "NONE", "", LongbridgeAnalystRatingOptions{
		AppKey: "key", AppSecret: "secret", AccessToken: "token", Now: time.Now,
		NewClient: func(_, _, _ string) (longbridgeAnalystRatingClient, error) { return client, nil },
	})
	if err != nil || !result.Fetched || result.Snapshot.Status != AnalystRatingStatusNoCoverage {
		t.Fatalf("no coverage result = %+v, err=%v", result, err)
	}
}

func testInstitutionRating(recommend lbfundamental.InstitutionRecommend, analystCount int32, target int64) *lbfundamental.InstitutionRating {
	targetDecimal := decimal.NewFromInt(target)
	low := decimal.NewFromInt(target - 5)
	high := decimal.NewFromInt(target + 5)
	close := decimal.NewFromInt(target - 10)
	return &lbfundamental.InstitutionRating{
		Latest: lbfundamental.InstitutionRatingLatest{
			Evaluate: lbfundamental.RatingEvaluate{Buy: analystCount - 2, Hold: 1, Under: 1, Total: analystCount},
			Target:   lbfundamental.RatingTarget{HighestPrice: &high, LowestPrice: &low, PrevClose: &close},
		},
		Summary: lbfundamental.InstitutionRatingSummary{
			CcySymbol: "$", Recommend: recommend, Target: &targetDecimal, UpdatedAt: "2026-07-31",
			Evaluate: lbfundamental.RatingSummaryEvaluate{Buy: analystCount - 2, Hold: 1, Under: 1},
		},
	}
}
