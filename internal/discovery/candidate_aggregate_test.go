package discovery

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"
)

func TestCandidateAggregateReusesLongHistoryWithoutChangingLiquidityGate(t *testing.T) {
	db := openMigratedTestDatabase(t)
	ctx := context.Background()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	latestDate := base.AddDate(0, 0, 219)
	batch := UniverseBatch{BatchID: "aggregate-long-history", Kind: BatchKindPrescreen, Status: BatchStatusPublished, EffectiveDate: latestDate.Format(time.DateOnly)}
	security := Security{CIK: "0000099922", CompanyName: "Long History", CatalogStatus: SecurityCatalogPublished}
	mustCreate(t, db, &security)
	mustCreate(t, db, &batch)
	mustCreate(t, db, &CurrentBatchPointer{Kind: BatchKindPrescreen, BatchID: batch.BatchID})
	var latest PriceSnapshot
	for index := 0; index < 220; index++ {
		latest = PriceSnapshot{Source: "longbridge", SourceVersion: "history", Symbol: "LONG", TradeDate: base.AddDate(0, 0, index), CloseMicros: int64(10_000_000 + index*10_000), Volume: int64(100_000 + index*1000), Adjusted: true, QualityStatus: QualityStatusValid}
		mustCreate(t, db, &latest)
	}
	mustCreate(t, db, &UniverseSnapshot{BatchID: batch.BatchID, SecurityID: security.ID, Ticker: "LONG", PriceSnapshotID: &latest.ID, MarketCapUSD: 100_000_000, QualityStatus: QualityStatusValid})
	mustCreate(t, db, &CandidateScoreSnapshot{BatchID: batch.BatchID, SecurityID: security.ID, Ticker: "LONG", Grade: CandidateGradeB, TotalScore: 60})
	filter := CandidateScoreQuery{SkipPerformance: true, SkipValuationDetails: true}
	page, err := ListCandidateScores(ctx, db, filter)
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("page=%+v err=%v", page, err)
	}
	all, err := ListAllCandidateScores(ctx, db, filter)
	if err != nil || len(all.Items) != 1 {
		t.Fatalf("all=%+v err=%v", all, err)
	}
	a, b := all.Items[0], page.Items[0]
	if a.MarketQuality.SampleDays != technicalMinimumSamples || !reflect.DeepEqual(a.MarketQuality, b.MarketQuality) || !reflect.DeepEqual(a.Investability, b.Investability) || !reflect.DeepEqual(a.ResearchReadiness, b.ResearchReadiness) {
		t.Fatal("long-history reuse changed the 21-session liquidity/research gate")
	}
	if !a.Technical.MA200Available || a.Technical.MA200USD != b.Technical.MA200USD || a.Technical.TradeSetup.Status != b.Technical.TradeSetup.Status {
		t.Fatal("aggregate technical evidence differs from the paginated detail")
	}
}

func TestCandidateAggregateIncludesBeyondHTTPPageLimit(t *testing.T) {
	db := openMigratedTestDatabase(t)
	batch := UniverseBatch{BatchID: "aggregate-over-200", Kind: BatchKindPrescreen, Status: BatchStatusPublished, EffectiveDate: time.Now().UTC().Format(time.DateOnly)}
	mustCreate(t, db, &batch)
	mustCreate(t, db, &CurrentBatchPointer{Kind: BatchKindPrescreen, BatchID: batch.BatchID})
	for index := 0; index < 205; index++ {
		security := Security{CIK: fmt.Sprintf("%010d", index+1), CompanyName: fmt.Sprintf("Issuer %d", index), CatalogStatus: SecurityCatalogPublished}
		mustCreate(t, db, &security)
		mustCreate(t, db, &CandidateScoreSnapshot{BatchID: batch.BatchID, SecurityID: security.ID, Ticker: fmt.Sprintf("T%03d", index), Grade: CandidateGradeB, TotalScore: 60})
	}
	page, err := ListCandidateScores(context.Background(), db, CandidateScoreQuery{Page: 1, PageSize: 200, SkipTechnicalDetails: true, SkipPerformance: true})
	if err != nil || len(page.Items) != 200 || page.Total != 205 {
		t.Fatalf("page=%d total=%d err=%v", len(page.Items), page.Total, err)
	}
	all, err := ListAllCandidateScores(context.Background(), db, CandidateScoreQuery{SkipTechnicalDetails: true, SkipPerformance: true, SkipValuationDetails: true})
	if err != nil || len(all.Items) != 205 {
		t.Fatalf("all=%d err=%v", len(all.Items), err)
	}
	overview, err := BuildCandidateOverviewFromItems(context.Background(), db, all.Items)
	if err != nil || overview.Total != 205 || overview.GradeCounts[CandidateGradeB] != 205 {
		t.Fatalf("overview=%+v err=%v", overview, err)
	}
	for index, item := range overview.TopCandidates {
		if item.Valuation.Status != page.Items[index].Valuation.Status {
			t.Fatalf("summary valuation changed for %s", item.Ticker)
		}
		if item.ResearchReadiness.Status != page.Items[index].ResearchReadiness.Status {
			t.Fatalf("summary readiness changed for %s", item.Ticker)
		}
	}
}
