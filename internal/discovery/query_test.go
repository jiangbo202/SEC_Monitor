package discovery

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestPagedResponsesExposeDistinctPageAndPageSizeFields(t *testing.T) {
	tests := []struct {
		name  string
		value any
	}{
		{name: "universe", value: UniversePage{Page: 2, PageSize: 25}},
		{name: "candidate_scores", value: CandidateScorePage{Page: 2, PageSize: 25}},
		{name: "batches", value: BatchPage{Page: 2, PageSize: 25}},
		{name: "provider_runs", value: ProviderRunPage{Page: 2, PageSize: 25}},
		{name: "candidate_watches", value: CandidateWatchPage{Page: 2, PageSize: 25}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			encoded, err := json.Marshal(tt.value)
			if err != nil {
				t.Fatal(err)
			}
			var payload map[string]any
			if err := json.Unmarshal(encoded, &payload); err != nil {
				t.Fatal(err)
			}
			if payload["page"] != float64(2) || payload["page_size"] != float64(25) {
				t.Fatalf("payload=%s, want page=2 and page_size=25", encoded)
			}
		})
	}
}

func TestUniverseQueryReadsOnlyCurrentPublishedBatch(t *testing.T) {
	db := openMigratedTestDatabase(t)
	security := Security{CIK: "0000004321", CompanyName: "Query Co", CatalogStatus: SecurityCatalogPublished}
	if err := db.Create(&security).Error; err != nil {
		t.Fatal(err)
	}
	security2 := Security{CIK: "0000004322", CompanyName: "Query Co 2", CatalogStatus: SecurityCatalogPublished}
	if err := db.Create(&security2).Error; err != nil {
		t.Fatal(err)
	}
	staged := Security{CIK: "0000004323", CompanyName: "Staged", CatalogStatus: SecurityCatalogStaged}
	if err := db.Create(&staged).Error; err != nil {
		t.Fatal(err)
	}
	old := UniverseBatch{BatchID: "old", Kind: BatchKindPrescreen, Status: BatchStatusPublished, StartedAt: time.Now().Add(-time.Hour)}
	current := UniverseBatch{BatchID: "current", Kind: BatchKindPrescreen, Status: BatchStatusPublished, StartedAt: time.Now()}
	draft := UniverseBatch{BatchID: "draft", Kind: BatchKindPrescreen, Status: BatchStatusDraft, StartedAt: time.Now().Add(time.Hour)}
	for _, batch := range []UniverseBatch{old, current, draft} {
		if err := db.Create(&batch).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Create(&CurrentBatchPointer{Kind: BatchKindPrescreen, BatchID: current.BatchID}).Error; err != nil {
		t.Fatal(err)
	}
	rows := []UniverseSnapshot{
		{BatchID: old.BatchID, SecurityID: security.ID, Ticker: "OLD", MarketCapUSD: 900, Status: EffectiveStatusPrescreen, ReasonCode: ReasonQualifiedSmallCap},
		{BatchID: current.BatchID, SecurityID: security.ID, Ticker: "AAA", MarketCapUSD: 100, Status: EffectiveStatusDataInsufficient, ReasonCode: ReasonPriceMissing},
		{BatchID: current.BatchID, SecurityID: security2.ID, Ticker: "BBB", MarketCapUSD: 200, Status: EffectiveStatusPrescreen, ReasonCode: ReasonQualifiedSmallCap},
		{BatchID: current.BatchID, SecurityID: staged.ID, Ticker: "STAGED", MarketCapUSD: 999, Status: EffectiveStatusPrescreen, ReasonCode: ReasonQualifiedSmallCap},
		{BatchID: draft.BatchID, SecurityID: security.ID, Ticker: "DRAFT", MarketCapUSD: 999, Status: EffectiveStatusPrescreen},
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	page, err := ListUniverse(context.Background(), db, UniverseQuery{Page: 1, PageSize: 1, Status: EffectiveStatusPrescreen})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 2 || len(page.Items) != 1 || page.Items[0].Ticker != "STAGED" {
		t.Fatalf("page = %#v", page)
	}
	reason, err := ListUniverse(context.Background(), db, UniverseQuery{ReasonCode: ReasonPriceMissing})
	if err != nil || reason.Total != 1 || reason.Items[0].Ticker != "AAA" {
		t.Fatalf("reason = %#v err=%v", reason, err)
	}
}

func TestUniverseQueryWithoutPointerIsEmpty(t *testing.T) {
	db := openMigratedTestDatabase(t)
	page, err := ListUniverse(context.Background(), db, UniverseQuery{})
	if err != nil || page.Total != 0 || len(page.Items) != 0 {
		t.Fatalf("page=%#v err=%v", page, err)
	}
}

func TestCandidateScoreQueryReadsCurrentPublishedBatchWithGradeFilter(t *testing.T) {
	db := openMigratedTestDatabase(t)
	security := Security{CIK: "0000004321", CompanyName: "Candidate Co", CatalogStatus: SecurityCatalogPublished}
	if err := db.Create(&security).Error; err != nil {
		t.Fatal(err)
	}
	security2 := Security{CIK: "0000004322", CompanyName: "Candidate Co 2", CatalogStatus: SecurityCatalogPublished}
	if err := db.Create(&security2).Error; err != nil {
		t.Fatal(err)
	}
	old := UniverseBatch{BatchID: "old", Kind: BatchKindPrescreen, Status: BatchStatusPublished, StartedAt: time.Now().Add(-time.Hour)}
	securityBatch := UniverseBatch{BatchID: "security-current", Kind: BatchKindSecurity, Status: BatchStatusPublished, StartedAt: time.Now()}
	current := UniverseBatch{BatchID: "current", Kind: BatchKindPrescreen, Status: BatchStatusPublished, EffectiveDate: "2026-07-01", UniverseSourceVersion: securityBatch.BatchID, StartedAt: time.Now()}
	if err := db.Create(&[]UniverseBatch{old, securityBatch, current}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&CurrentBatchPointer{Kind: BatchKindPrescreen, BatchID: current.BatchID}).Error; err != nil {
		t.Fatal(err)
	}
	identities := []SecurityBatchIdentity{
		{BatchID: securityBatch.BatchID, SecurityID: security.ID, CIK: security.CIK, Ticker: "AAA", SIC: 7372, CompanyName: "Software Candidate", MappingStatus: MappingStatusCurrent},
		{BatchID: securityBatch.BatchID, SecurityID: security2.ID, CIK: security2.CIK, Ticker: "BBB", SIC: 1311, CompanyName: "Energy Candidate", MappingStatus: MappingStatusCurrent},
	}
	if err := db.Create(&identities).Error; err != nil {
		t.Fatal(err)
	}
	rows := []CandidateScoreSnapshot{
		{BatchID: old.BatchID, SecurityID: security.ID, Ticker: "OLD", Grade: CandidateGradeA, TotalScore: 100, MarketCapUSD: 100},
		{BatchID: current.BatchID, SecurityID: security.ID, Ticker: "AAA", Grade: CandidateGradeA, EligibleA: true, EligibleB: true, TotalScore: 80, MarketCapUSD: 300_000_000, RevenueGrowthPct: 42},
		{BatchID: current.BatchID, SecurityID: security2.ID, Ticker: "BBB", Grade: CandidateGradeB, EligibleB: true, TotalScore: 60, MarketCapUSD: 800_000_000, RevenueGrowthPct: 21},
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	metrics := []FinancialMetricSnapshot{
		{BatchID: securityBatch.BatchID, SecurityID: security.ID, RevenueGrowthAvailable: true, QuarterlyRevenueYoYPct: 42, QuarterlyRevenueQoQPct: 10, AnnualRevenueYoYPct: 30, AnnualRevenueQoQPct: 30, LatestQuarterRevenueUSD: 142, PriorYearQuarterRevenueUSD: 100, PreviousQuarterRevenueUSD: 129, LatestAnnualRevenueUSD: 260, PriorAnnualRevenueUSD: 200, QualityFlagsJSON: `["test"]`},
		{BatchID: securityBatch.BatchID, SecurityID: security2.ID, RevenueGrowthAvailable: true, QuarterlyRevenueYoYPct: 21, QuarterlyRevenueQoQPct: 90, AnnualRevenueYoYPct: 80, AnnualRevenueQoQPct: 80, LatestQuarterRevenueUSD: 121, PriorYearQuarterRevenueUSD: 100, PreviousQuarterRevenueUSD: 64},
	}
	if err := db.Create(&metrics).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&CapitalRiskSnapshot{
		BatchID: securityBatch.BatchID, SecurityID: security.ID, Kind: CapitalEventATMProgram, Active: true, BlocksA: true, BlocksB: false, Severity: CapitalRiskSeverityHigh,
		Reason: "ATM program active", EffectiveAt: time.Date(2026, 6, 29, 0, 0, 0, 0, time.UTC),
	}).Error; err != nil {
		t.Fatal(err)
	}
	tradeDate := time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC)
	price := PriceSnapshot{Source: "tiingo", SourceVersion: "tiingo:2026-06-30", Symbol: "AAA", TradeDate: tradeDate, CloseMicros: 1_250_000, Volume: 1234567, Currency: "USD", QualityStatus: QualityStatusValid, CreatedAt: current.StartedAt}
	if err := db.Create(&price).Error; err != nil {
		t.Fatal(err)
	}
	latestTradeDate := tradeDate.AddDate(0, 0, 1)
	latestPrice := PriceSnapshot{Source: "twelvedata", SourceVersion: "twelvedata:technical-history", Symbol: "AAA", TradeDate: latestTradeDate, CloseMicros: 1_500_000, Volume: 7654321, Currency: "USD", QualityStatus: QualityStatusValid, CreatedAt: current.StartedAt.Add(time.Minute)}
	if err := db.Create(&latestPrice).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&UniverseSnapshot{BatchID: current.BatchID, SecurityID: security.ID, Ticker: "AAA", MarketCapUSD: 300_000_000, PriceSnapshotID: &price.ID, QualityStatus: QualityStatusValid}).Error; err != nil {
		t.Fatal(err)
	}

	page, err := ListCandidateScores(context.Background(), db, CandidateScoreQuery{Grade: CandidateGradeA})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 || len(page.Items) != 1 || page.Items[0].Ticker != "AAA" {
		t.Fatalf("page=%#v", page)
	}
	if page.Items[0].PriceCloseUSD != 1.5 || page.Items[0].PriceVolume != 7654321 || page.Items[0].PriceCurrency != "USD" || page.Items[0].PriceSource != "twelvedata" || page.Items[0].PriceTradeDate == nil || !page.Items[0].PriceTradeDate.Equal(latestTradeDate) {
		t.Fatalf("price evidence = %#v", page.Items[0])
	}
	if page.Items[0].PriceFreshnessStatus != PriceFreshnessCurrent || page.Items[0].PriceAgeCalendarDays != 0 {
		t.Fatalf("price freshness = %#v", page.Items[0])
	}
	if page.Items[0].SectorCategory != "软件与数据服务" || page.Items[0].SectorRatingScore != 9 || page.Items[0].SectorSIC != 7372 || page.Items[0].SectorLabel != "优秀赛道" {
		t.Fatalf("sector evidence = %#v", page.Items[0])
	}
	if page.Items[0].RevenueGrowthInfo.SelectedRevenueGrowthBasis != "quarterly_revenue_yoy_pct" || page.Items[0].RevenueGrowthInfo.LatestQuarterRevenueUSD != 142 || !strings.Contains(page.Items[0].RevenueGrowthInfo.Method, "preferred") {
		t.Fatalf("revenue explanation = %#v", page.Items[0].RevenueGrowthInfo)
	}
	if page.Items[0].RevenueGrowthInfo.QuarterlyRevenueQoQPct != 10 || page.Items[0].RevenueGrowthInfo.PreviousQuarterRevenueUSD != 129 {
		t.Fatalf("qoq explanation = %#v", page.Items[0].RevenueGrowthInfo)
	}
	if len(page.Items[0].CapitalRiskSummaries) != 1 || page.Items[0].CapitalRiskSummaries[0].Reason != "ATM program active" || !page.Items[0].CapitalRiskSummaries[0].BlocksA {
		t.Fatalf("capital risk summaries = %#v", page.Items[0].CapitalRiskSummaries)
	}
	software, err := ListCandidateScores(context.Background(), db, CandidateScoreQuery{SectorCategory: "软件与数据服务"})
	if err != nil || software.Total != 1 || len(software.Items) != 1 || software.Items[0].Ticker != "AAA" {
		t.Fatalf("software=%#v err=%v", software, err)
	}
	marketDesc, err := ListCandidateScores(context.Background(), db, CandidateScoreQuery{SortBy: "market_cap_usd", SortOrder: "desc"})
	if err != nil || marketDesc.Total != 2 || marketDesc.Items[0].Ticker != "BBB" {
		t.Fatalf("marketDesc=%#v err=%v", marketDesc, err)
	}
	quarterlyDesc, err := ListCandidateScores(context.Background(), db, CandidateScoreQuery{SortBy: "quarterly_revenue_yoy_pct", SortOrder: "desc"})
	if err != nil || quarterlyDesc.Total != 2 || quarterlyDesc.Items[0].Ticker != "AAA" {
		t.Fatalf("quarterlyDesc=%#v err=%v", quarterlyDesc, err)
	}
	annualDesc, err := ListCandidateScores(context.Background(), db, CandidateScoreQuery{SortBy: "annual_revenue_yoy_pct", SortOrder: "desc"})
	if err != nil || annualDesc.Total != 2 || annualDesc.Items[0].Ticker != "BBB" {
		t.Fatalf("annualDesc=%#v err=%v", annualDesc, err)
	}
	qoqDesc, err := ListCandidateScores(context.Background(), db, CandidateScoreQuery{SortBy: "quarterly_revenue_qoq_pct", SortOrder: "desc"})
	if err != nil || qoqDesc.Total != 2 || qoqDesc.Items[0].Ticker != "BBB" {
		t.Fatalf("qoqDesc=%#v err=%v", qoqDesc, err)
	}
	all, err := ListCandidateScores(context.Background(), db, CandidateScoreQuery{Page: 1, PageSize: 1})
	if err != nil || all.Total != 2 || len(all.Items) != 1 || all.Items[0].Ticker != "AAA" {
		t.Fatalf("all=%#v err=%v", all, err)
	}
}

func TestCandidateScoreQueryAnnotatesQualityTierTagsPriorityAndChanges(t *testing.T) {
	db := openMigratedTestDatabase(t)
	securities := []Security{
		{CIK: "0000006101", CompanyName: "Strong B", CatalogStatus: SecurityCatalogPublished},
		{CIK: "0000006102", CompanyName: "Watch B", CatalogStatus: SecurityCatalogPublished},
		{CIK: "0000006103", CompanyName: "Old B", CatalogStatus: SecurityCatalogPublished},
	}
	if err := db.Create(&securities).Error; err != nil {
		t.Fatal(err)
	}
	securityBatch := UniverseBatch{BatchID: "security-current", Kind: BatchKindSecurity, Status: BatchStatusPublished, StartedAt: time.Now()}
	oldBatch := UniverseBatch{BatchID: "old-market", Kind: BatchKindPrescreen, Status: BatchStatusPublished, UniverseSourceVersion: securityBatch.BatchID, StartedAt: time.Now().Add(-24 * time.Hour)}
	current := UniverseBatch{BatchID: "current-market", Kind: BatchKindPrescreen, Status: BatchStatusPublished, UniverseSourceVersion: securityBatch.BatchID, StartedAt: time.Now()}
	if err := db.Create(&[]UniverseBatch{securityBatch, oldBatch, current}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&CurrentBatchPointer{Kind: BatchKindPrescreen, BatchID: current.BatchID}).Error; err != nil {
		t.Fatal(err)
	}
	identities := []SecurityBatchIdentity{
		{BatchID: securityBatch.BatchID, SecurityID: securities[0].ID, CIK: securities[0].CIK, Ticker: "STRB", SIC: 7372, CompanyName: "Strong B", MappingStatus: MappingStatusCurrent},
		{BatchID: securityBatch.BatchID, SecurityID: securities[1].ID, CIK: securities[1].CIK, Ticker: "WATB", SIC: 2834, CompanyName: "Watch B", MappingStatus: MappingStatusCurrent},
		{BatchID: securityBatch.BatchID, SecurityID: securities[2].ID, CIK: securities[2].CIK, Ticker: "OLDB", SIC: 1311, CompanyName: "Old B", MappingStatus: MappingStatusCurrent},
	}
	if err := db.Create(&identities).Error; err != nil {
		t.Fatal(err)
	}
	oldScores := []CandidateScoreSnapshot{
		{BatchID: oldBatch.BatchID, SecurityID: securities[0].ID, Ticker: "STRB", Grade: CandidateGradeB, EligibleB: true, TotalScore: 64, MarketCapUSD: 420_000_000},
		{BatchID: oldBatch.BatchID, SecurityID: securities[2].ID, Ticker: "OLDB", Grade: CandidateGradeB, EligibleB: true, TotalScore: 71, MarketCapUSD: 500_000_000},
	}
	currentScores := []CandidateScoreSnapshot{
		{BatchID: current.BatchID, SecurityID: securities[0].ID, Ticker: "STRB", Grade: CandidateGradeB, EligibleB: true, TotalScore: 72, MarketCapUSD: 260_000_000, RevenueGrowthPct: 58, CashRunwayMonths: 18, DilutionRiskScore: 10, SectorScore: 9},
		{BatchID: current.BatchID, SecurityID: securities[1].ID, Ticker: "WATB", Grade: CandidateGradeB, EligibleB: true, TotalScore: 69, MarketCapUSD: 80_000_000, RevenueGrowthPct: 2500, CashRunwayMonths: 11, DilutionRiskScore: 10, SectorScore: 9},
		{BatchID: current.BatchID, SecurityID: securities[2].ID, Ticker: "OLDB", Grade: CandidateGradeB, EligibleB: true, TotalScore: 67, MarketCapUSD: 520_000_000, RevenueGrowthPct: 35, CashRunwayMonths: 9, ActiveBlocksA: true, DilutionRiskScore: 6, SectorScore: 4},
	}
	if err := db.Create(&oldScores).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&currentScores).Error; err != nil {
		t.Fatal(err)
	}
	metrics := []FinancialMetricSnapshot{
		{BatchID: securityBatch.BatchID, SecurityID: securities[0].ID, RevenueGrowthAvailable: true, RunwayAvailable: true, QuarterlyRevenueYoYPct: 58, AnnualRevenueYoYPct: 44, LatestQuarterRevenueUSD: 15_000_000, PriorYearQuarterRevenueUSD: 9_500_000},
		{BatchID: securityBatch.BatchID, SecurityID: securities[1].ID, RevenueGrowthAvailable: true, RunwayAvailable: true, QuarterlyRevenueYoYPct: 2500, AnnualRevenueYoYPct: 1000, LatestQuarterRevenueUSD: 400_000, PriorYearQuarterRevenueUSD: 15_000, QualityFlagsJSON: `["low_revenue_base","extreme_revenue_growth"]`},
		{BatchID: securityBatch.BatchID, SecurityID: securities[2].ID, RevenueGrowthAvailable: true, RunwayAvailable: true, QuarterlyRevenueYoYPct: 35, AnnualRevenueYoYPct: 30, LatestQuarterRevenueUSD: 8_000_000, PriorYearQuarterRevenueUSD: 5_900_000},
	}
	if err := db.Create(&metrics).Error; err != nil {
		t.Fatal(err)
	}
	priceDate := time.Date(2026, 7, 8, 0, 0, 0, 0, time.UTC)
	prices := []PriceSnapshot{
		{Source: "tiingo", SourceVersion: "p1", Symbol: "STRB", TradeDate: priceDate, CloseMicros: 2_000_000, Volume: 900_000, Currency: "USD", QualityStatus: QualityStatusValid},
		{Source: "twelvedata", SourceVersion: "p1", Symbol: "WATB", TradeDate: priceDate, CloseMicros: 900_000, Volume: 30_000, Currency: "USD", QualityStatus: QualityStatusValid},
		{Source: "twelvedata", SourceVersion: "p1", Symbol: "OLDB", TradeDate: priceDate, CloseMicros: 4_000_000, Volume: 120_000, Currency: "USD", QualityStatus: QualityStatusValid},
	}
	if err := db.Create(&prices).Error; err != nil {
		t.Fatal(err)
	}
	universe := []UniverseSnapshot{
		{BatchID: current.BatchID, SecurityID: securities[0].ID, Ticker: "STRB", MarketCapUSD: 260_000_000, PriceSnapshotID: &prices[0].ID, QualityStatus: QualityStatusValid},
		{BatchID: current.BatchID, SecurityID: securities[1].ID, Ticker: "WATB", MarketCapUSD: 80_000_000, PriceSnapshotID: &prices[1].ID, QualityStatus: QualityStatusValid},
		{BatchID: current.BatchID, SecurityID: securities[2].ID, Ticker: "OLDB", MarketCapUSD: 520_000_000, PriceSnapshotID: &prices[2].ID, QualityStatus: QualityStatusValid},
	}
	if err := db.Create(&universe).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&CapitalRiskSnapshot{
		BatchID: securityBatch.BatchID, SecurityID: securities[2].ID, Kind: CapitalEventATMProgram, Active: true, BlocksA: true, BlocksB: false, Severity: CapitalRiskSeverityHigh, Reason: "ATM active", EffectiveAt: priceDate,
	}).Error; err != nil {
		t.Fatal(err)
	}

	page, err := ListCandidateScores(context.Background(), db, CandidateScoreQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 3 || len(page.Items) != 3 {
		t.Fatalf("page=%#v", page)
	}
	if page.Items[0].Ticker != "STRB" || page.Items[0].QualityTier != "strong_b" || page.Items[0].ChangeStatus != "improved" {
		t.Fatalf("strong candidate annotations = %#v", page.Items[0])
	}
	if page.Items[0].ReviewPriorityScore <= page.Items[1].ReviewPriorityScore || page.Items[0].ReviewPriorityScore > 100 {
		t.Fatalf("default priority order not applied: %#v", page.Items)
	}
	if !containsPriorityReason(page.Items[0].ReviewPriorityReasons, "质量：强B", 12) || !containsPriorityReason(page.Items[0].ReviewPriorityReasons, "变化：改善", 8) {
		t.Fatalf("strong candidate priority reasons = %#v", page.Items[0].ReviewPriorityReasons)
	}
	if !containsString(page.Items[1].QualityTags, "low_revenue_base") || !containsString(page.Items[1].QualityTags, "low_liquidity") || page.Items[1].QualityTier != "watch_b" || page.Items[1].ChangeStatus != "new" {
		t.Fatalf("watch candidate annotations = %#v", page.Items[1])
	}
	if page.Items[1].QualityAdjustedScore >= page.Items[1].TotalScore || page.Items[1].QualityAdjustedScore > 60 {
		t.Fatalf("watch candidate adjusted score = %#v", page.Items[1])
	}
	if !containsString(page.Items[2].QualityTags, "active_capital_risk") || page.Items[2].ChangeStatus != "weakened" {
		t.Fatalf("old candidate annotations = %#v", page.Items[2])
	}
	filtered, err := ListCandidateScores(context.Background(), db, CandidateScoreQuery{
		QualityTier:            "strong_b",
		ChangeStatus:           "improved",
		MinReviewPriorityScore: 70,
		ExcludeQualityTags:     []string{"low_liquidity"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if filtered.Total != 1 || filtered.Items[0].Ticker != "STRB" {
		t.Fatalf("filtered candidates = %#v", filtered)
	}
	recommended, err := ListCandidateScores(context.Background(), db, CandidateScoreQuery{RecommendedOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if recommended.Total != 1 || recommended.Items[0].Ticker != "STRB" {
		t.Fatalf("recommended candidates = %#v", recommended)
	}
}

func TestSortCandidateScoreResultsDefaultsToVisibleTotalScore(t *testing.T) {
	items := []CandidateScoreResult{
		{CandidateScoreSnapshot: CandidateScoreSnapshot{Ticker: "LOW", Grade: CandidateGradeB, TotalScore: 70, MarketCapUSD: 100_000_000}, ReviewPriorityScore: 99},
		{CandidateScoreSnapshot: CandidateScoreSnapshot{Ticker: "BA", Grade: CandidateGradeB, TotalScore: 90, MarketCapUSD: 200_000_000}, ReviewPriorityScore: 10},
		{CandidateScoreSnapshot: CandidateScoreSnapshot{Ticker: "AA", Grade: CandidateGradeA, TotalScore: 90, MarketCapUSD: 300_000_000}, ReviewPriorityScore: 1},
		{CandidateScoreSnapshot: CandidateScoreSnapshot{Ticker: "AB", Grade: CandidateGradeA, TotalScore: 90, MarketCapUSD: 400_000_000}, ReviewPriorityScore: 100},
	}

	sortCandidateScoreResults(items, "", "")
	got := []string{items[0].Ticker, items[1].Ticker, items[2].Ticker, items[3].Ticker}
	want := []string{"AA", "AB", "BA", "LOW"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("default candidate order = %#v, want %#v", got, want)
	}
}

func TestFilterCandidateScoreResultsExcludesResearchReadiness(t *testing.T) {
	items := []CandidateScoreResult{
		{CandidateScoreSnapshot: CandidateScoreSnapshot{Ticker: "READY"}, ResearchReadiness: CandidateResearchReadiness{Status: CandidateResearchReadinessReady}},
		{CandidateScoreSnapshot: CandidateScoreSnapshot{Ticker: "REVIEW"}, ResearchReadiness: CandidateResearchReadiness{Status: CandidateResearchReadinessResearchOnly}},
		{CandidateScoreSnapshot: CandidateScoreSnapshot{Ticker: "BLOCK"}, ResearchReadiness: CandidateResearchReadiness{Status: CandidateResearchReadinessBlocked}},
	}
	filtered := filterCandidateScoreResults(items, CandidateScoreQuery{ExcludeResearchReadiness: []string{CandidateResearchReadinessBlocked}})
	if len(filtered) != 2 || filtered[0].Ticker != "READY" || filtered[1].Ticker != "REVIEW" {
		t.Fatalf("filtered candidates = %#v", filtered)
	}
}

func TestFilterCandidateScoreResultsByPriceFreshness(t *testing.T) {
	items := []CandidateScoreResult{
		{CandidateScoreSnapshot: CandidateScoreSnapshot{Ticker: "CURRENT"}, PriceFreshnessStatus: PriceFreshnessCurrent},
		{CandidateScoreSnapshot: CandidateScoreSnapshot{Ticker: "STALE"}, PriceFreshnessStatus: PriceFreshnessStale},
		{CandidateScoreSnapshot: CandidateScoreSnapshot{Ticker: "MISSING"}, PriceFreshnessStatus: PriceFreshnessMissing},
	}
	filtered := filterCandidateScoreResults(items, CandidateScoreQuery{PriceFreshnessStatuses: []string{PriceFreshnessStale, PriceFreshnessMissing}})
	if len(filtered) != 2 || filtered[0].Ticker != "STALE" || filtered[1].Ticker != "MISSING" {
		t.Fatalf("filtered candidates = %#v", filtered)
	}
}

func TestCandidateQualityTagsFlagQuarterlyAnnualGrowthConflict(t *testing.T) {
	cases := []struct {
		name string
		item CandidateScoreResult
		want bool
	}{
		{
			name: "quarterly decline conflicts with annual growth",
			item: CandidateScoreResult{RevenueGrowthInfo: RevenueGrowthExplanation{RevenueGrowthAvailable: true, QuarterlyRevenueYoYPct: -5, AnnualRevenueYoYPct: 35}},
			want: true,
		},
		{
			name: "positive quarterly growth has no conflict",
			item: CandidateScoreResult{RevenueGrowthInfo: RevenueGrowthExplanation{RevenueGrowthAvailable: true, QuarterlyRevenueYoYPct: 5, AnnualRevenueYoYPct: 35}},
			want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := containsString(candidateQualityTags(tc.item), "quarterly_growth_conflicts_with_annual"); got != tc.want {
				t.Fatalf("conflict tag = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestCandidateScoreQueryDefaultsToActiveCandidatesAndAllowsExcludedPool(t *testing.T) {
	db := openMigratedTestDatabase(t)
	now := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	securities := []Security{
		{CIK: "0000007001", CompanyName: "Alpha", CatalogStatus: SecurityCatalogPublished},
		{CIK: "0000007002", CompanyName: "Beta", CatalogStatus: SecurityCatalogPublished},
		{CIK: "0000007003", CompanyName: "Excluded", CatalogStatus: SecurityCatalogPublished},
	}
	if err := db.Create(&securities).Error; err != nil {
		t.Fatal(err)
	}
	securityBatch := UniverseBatch{BatchID: "security-candidate-scope", Kind: BatchKindSecurity, Status: BatchStatusPublished, StartedAt: now.Add(-time.Minute)}
	marketBatch := UniverseBatch{BatchID: "market-candidate-scope", Kind: BatchKindPrescreen, Status: BatchStatusPublished, UniverseSourceVersion: securityBatch.BatchID, StartedAt: now}
	if err := db.Create(&[]UniverseBatch{securityBatch, marketBatch}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&CurrentBatchPointer{Kind: BatchKindPrescreen, BatchID: marketBatch.BatchID}).Error; err != nil {
		t.Fatal(err)
	}
	identities := make([]SecurityBatchIdentity, 0, len(securities))
	for index, security := range securities {
		identities = append(identities, SecurityBatchIdentity{
			BatchID: securityBatch.BatchID, SecurityID: security.ID, CIK: security.CIK,
			Ticker: []string{"AAA", "BBB", "XXX"}[index], SIC: 7372, MappingStatus: MappingStatusCurrent,
		})
	}
	if err := db.Create(&identities).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&[]CandidateScoreSnapshot{
		{BatchID: marketBatch.BatchID, SecurityID: securities[0].ID, Ticker: "AAA", Grade: CandidateGradeA, EligibleA: true, TotalScore: 85, MarketCapUSD: 100_000_000},
		{BatchID: marketBatch.BatchID, SecurityID: securities[1].ID, Ticker: "BBB", Grade: CandidateGradeB, EligibleB: true, TotalScore: 70, MarketCapUSD: 200_000_000},
		{BatchID: marketBatch.BatchID, SecurityID: securities[2].ID, Ticker: "XXX", Grade: CandidateGradeExcluded, TotalScore: 75, MarketCapUSD: 2_000_000_000},
	}).Error; err != nil {
		t.Fatal(err)
	}

	active, err := ListCandidateScores(context.Background(), db, CandidateScoreQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if active.Total != 2 || len(active.Items) != 2 {
		t.Fatalf("active candidates = %#v", active)
	}
	for _, item := range active.Items {
		if item.Grade == CandidateGradeExcluded {
			t.Fatalf("excluded item leaked into active candidates: %#v", item)
		}
	}

	excluded, err := ListCandidateScores(context.Background(), db, CandidateScoreQuery{Grade: CandidateGradeExcluded})
	if err != nil {
		t.Fatal(err)
	}
	if excluded.Total != 1 || len(excluded.Items) != 1 || excluded.Items[0].Ticker != "XXX" || excluded.Items[0].QualityTier != CandidateGradeExcluded {
		t.Fatalf("excluded pool = %#v", excluded)
	}

	overview, err := BuildCandidateOverview(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	if overview.Total != 2 || overview.GradeCounts[CandidateGradeExcluded] != 0 || overview.QualityTierCounts[CandidateGradeExcluded] != 0 {
		t.Fatalf("overview = %#v", overview)
	}
}

func TestCandidateScoreQueryIncludesForwardPerformance(t *testing.T) {
	db := openMigratedTestDatabase(t)
	security := Security{CIK: "0000006151", CompanyName: "Perf Co", CatalogStatus: SecurityCatalogPublished}
	if err := db.Create(&security).Error; err != nil {
		t.Fatal(err)
	}
	baseDate := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	first := UniverseBatch{BatchID: "first-perf", Kind: BatchKindPrescreen, Status: BatchStatusPublished, StartedAt: baseDate}
	current := UniverseBatch{BatchID: "current-perf", Kind: BatchKindPrescreen, Status: BatchStatusPublished, StartedAt: baseDate.AddDate(0, 0, 5)}
	if err := db.Create(&[]UniverseBatch{first, current}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&CurrentBatchPointer{Kind: BatchKindPrescreen, BatchID: current.BatchID}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&[]CandidateScoreSnapshot{
		{BatchID: first.BatchID, SecurityID: security.ID, Ticker: "PERF", Grade: CandidateGradeB, EligibleB: true, TotalScore: 70, MarketCapUSD: 100_000_000, RevenueGrowthPct: 50, CashRunwayMonths: 18},
		{BatchID: current.BatchID, SecurityID: security.ID, Ticker: "PERF", Grade: CandidateGradeB, EligibleB: true, TotalScore: 72, MarketCapUSD: 100_000_000, RevenueGrowthPct: 50, CashRunwayMonths: 18},
	}).Error; err != nil {
		t.Fatal(err)
	}
	prices := []PriceSnapshot{
		{Source: "tiingo", SourceVersion: "v1", Symbol: "PERF", TradeDate: baseDate, CloseMicros: 1_000_000, Volume: 200_000, Currency: "USD", QualityStatus: QualityStatusValid},
		{Source: "tiingo", SourceVersion: "v2", Symbol: "PERF", TradeDate: baseDate.AddDate(0, 0, 1), CloseMicros: 1_100_000, Volume: 210_000, Currency: "USD", QualityStatus: QualityStatusValid},
		{Source: "tiingo", SourceVersion: "v3", Symbol: "PERF", TradeDate: baseDate.AddDate(0, 0, 2), CloseMicros: 1_120_000, Volume: 220_000, Currency: "USD", QualityStatus: QualityStatusValid},
		{Source: "tiingo", SourceVersion: "v4", Symbol: "PERF", TradeDate: baseDate.AddDate(0, 0, 3), CloseMicros: 1_180_000, Volume: 220_000, Currency: "USD", QualityStatus: QualityStatusValid},
		{Source: "tiingo", SourceVersion: "v5", Symbol: "PERF", TradeDate: baseDate.AddDate(0, 0, 4), CloseMicros: 1_200_000, Volume: 220_000, Currency: "USD", QualityStatus: QualityStatusValid},
		{Source: "tiingo", SourceVersion: "v6", Symbol: "PERF", TradeDate: baseDate.AddDate(0, 0, 5), CloseMicros: 1_250_000, Volume: 220_000, Currency: "USD", QualityStatus: QualityStatusValid},
	}
	if err := db.Create(&prices).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&[]UniverseSnapshot{
		{BatchID: first.BatchID, SecurityID: security.ID, Ticker: "PERF", MarketCapUSD: 100_000_000, PriceSnapshotID: &prices[0].ID, QualityStatus: QualityStatusValid},
		{BatchID: current.BatchID, SecurityID: security.ID, Ticker: "PERF", MarketCapUSD: 100_000_000, PriceSnapshotID: &prices[5].ID, QualityStatus: QualityStatusValid},
	}).Error; err != nil {
		t.Fatal(err)
	}

	page, err := ListCandidateScores(context.Background(), db, CandidateScoreQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].Performance.BaseDate != baseDate.Format(time.DateOnly) || page.Items[0].Performance.BaseClose != 1 || page.Items[0].Performance.Return1D == nil || page.Items[0].Performance.Return5D == nil {
		t.Fatalf("performance = %#v", page.Items)
	}
	if got := *page.Items[0].Performance.Return1D; got < 9.9 || got > 10.1 {
		t.Fatalf("1d return = %v", got)
	}
	if got := *page.Items[0].Performance.Return5D; got < 24.9 || got > 25.1 {
		t.Fatalf("5d return = %v", got)
	}
}

func TestBuildCandidateOverviewSummarizesCandidateDimensions(t *testing.T) {
	db := openMigratedTestDatabase(t)
	securities := []Security{
		{CIK: "0000006201", CompanyName: "Strong B", CatalogStatus: SecurityCatalogPublished},
		{CIK: "0000006202", CompanyName: "Watch B", CatalogStatus: SecurityCatalogPublished},
	}
	if err := db.Create(&securities).Error; err != nil {
		t.Fatal(err)
	}
	securityBatch := UniverseBatch{BatchID: "security-overview", Kind: BatchKindSecurity, Status: BatchStatusPublished, StartedAt: time.Now().Add(-time.Minute)}
	current := UniverseBatch{BatchID: "market-overview", Kind: BatchKindPrescreen, Status: BatchStatusPublished, UniverseSourceVersion: securityBatch.BatchID, StartedAt: time.Now()}
	if err := db.Create(&[]UniverseBatch{securityBatch, current}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&CurrentBatchPointer{Kind: BatchKindPrescreen, BatchID: current.BatchID}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&[]SecurityBatchIdentity{
		{BatchID: securityBatch.BatchID, SecurityID: securities[0].ID, Ticker: "GOOD", SIC: 7372, CompanyName: "Strong B"},
		{BatchID: securityBatch.BatchID, SecurityID: securities[1].ID, Ticker: "RISK", SIC: 2834, CompanyName: "Watch B"},
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&[]CandidateScoreSnapshot{
		{BatchID: current.BatchID, SecurityID: securities[0].ID, Ticker: "GOOD", Grade: CandidateGradeB, EligibleB: true, TotalScore: 72, MarketCapUSD: 250_000_000, RevenueGrowthPct: 55, CashRunwayMonths: 16, DilutionRiskScore: 10, SectorScore: 9},
		{BatchID: current.BatchID, SecurityID: securities[1].ID, Ticker: "RISK", Grade: CandidateGradeB, EligibleB: true, TotalScore: 68, MarketCapUSD: 90_000_000, RevenueGrowthPct: 2000, CashRunwayMonths: 10, DilutionRiskScore: 10, SectorScore: 9},
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&[]FinancialMetricSnapshot{
		{BatchID: securityBatch.BatchID, SecurityID: securities[0].ID, RevenueGrowthAvailable: true, RunwayAvailable: true, LatestQuarterRevenueUSD: 10_000_000, QuarterlyRevenueYoYPct: 55, AnnualRevenueYoYPct: 40},
		{BatchID: securityBatch.BatchID, SecurityID: securities[1].ID, RevenueGrowthAvailable: true, RunwayAvailable: true, LatestQuarterRevenueUSD: 200_000, QuarterlyRevenueYoYPct: 2000, AnnualRevenueYoYPct: 1500, QualityFlagsJSON: `["low_revenue_base","extreme_revenue_growth"]`},
	}).Error; err != nil {
		t.Fatal(err)
	}

	overview, err := BuildCandidateOverview(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	if overview.Total != 2 || overview.GradeCounts[CandidateGradeB] != 2 || overview.QualityTierCounts["strong_b"] != 1 || overview.QualityTierCounts["watch_b"] != 1 {
		t.Fatalf("overview counts = %#v", overview)
	}
	if overview.ChangeCounts["new"] != 2 || overview.SectorCounts["软件与数据服务"] != 1 || overview.QualityTagCounts["low_revenue_base"] != 1 {
		t.Fatalf("overview dimensions = %#v", overview)
	}
	if len(overview.TopCandidates) != 2 || overview.TopCandidates[0].Ticker != "GOOD" {
		t.Fatalf("top candidates = %#v", overview.TopCandidates)
	}
}

func TestBuildCandidateOverviewCountsExitedCandidates(t *testing.T) {
	db := openMigratedTestDatabase(t)
	securities := []Security{
		{CIK: "0000006251", CompanyName: "Keep", CatalogStatus: SecurityCatalogPublished},
		{CIK: "0000006252", CompanyName: "Gone", CatalogStatus: SecurityCatalogPublished},
	}
	if err := db.Create(&securities).Error; err != nil {
		t.Fatal(err)
	}
	old := UniverseBatch{BatchID: "old-exit", Kind: BatchKindPrescreen, Status: BatchStatusPublished, StartedAt: time.Now().Add(-time.Hour)}
	current := UniverseBatch{BatchID: "current-exit", Kind: BatchKindPrescreen, Status: BatchStatusPublished, StartedAt: time.Now()}
	if err := db.Create(&[]UniverseBatch{old, current}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&CurrentBatchPointer{Kind: BatchKindPrescreen, BatchID: current.BatchID}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&[]CandidateScoreSnapshot{
		{BatchID: old.BatchID, SecurityID: securities[0].ID, Ticker: "KEEP", Grade: CandidateGradeB, EligibleB: true, TotalScore: 70},
		{BatchID: old.BatchID, SecurityID: securities[1].ID, Ticker: "GONE", Grade: CandidateGradeB, EligibleB: true, TotalScore: 69},
		{BatchID: current.BatchID, SecurityID: securities[0].ID, Ticker: "KEEP", Grade: CandidateGradeB, EligibleB: true, TotalScore: 72},
	}).Error; err != nil {
		t.Fatal(err)
	}
	overview, err := BuildCandidateOverview(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	if overview.ChangeCounts["exited"] != 1 {
		t.Fatalf("change counts = %#v", overview.ChangeCounts)
	}
}

func TestBuildCandidateSummaryActionableOnlyFiltersNoisyBCandidates(t *testing.T) {
	db := openMigratedTestDatabase(t)
	securities := []Security{
		{CIK: "0000006301", CompanyName: "Strong Notify", CatalogStatus: SecurityCatalogPublished},
		{CIK: "0000006302", CompanyName: "Watch Notify", CatalogStatus: SecurityCatalogPublished},
	}
	if err := db.Create(&securities).Error; err != nil {
		t.Fatal(err)
	}
	securityBatch := UniverseBatch{BatchID: "security-notify", Kind: BatchKindSecurity, Status: BatchStatusPublished, StartedAt: time.Now().Add(-time.Minute)}
	current := UniverseBatch{BatchID: "market-notify", Kind: BatchKindPrescreen, Status: BatchStatusPublished, UniverseSourceVersion: securityBatch.BatchID, StartedAt: time.Now()}
	if err := db.Create(&[]UniverseBatch{securityBatch, current}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&CurrentBatchPointer{Kind: BatchKindPrescreen, BatchID: current.BatchID}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&[]SecurityBatchIdentity{
		{BatchID: securityBatch.BatchID, SecurityID: securities[0].ID, Ticker: "SNTF", SIC: 7372, CompanyName: "Strong Notify"},
		{BatchID: securityBatch.BatchID, SecurityID: securities[1].ID, Ticker: "WNTF", SIC: 2834, CompanyName: "Watch Notify"},
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&[]CandidateScoreSnapshot{
		{BatchID: current.BatchID, SecurityID: securities[0].ID, Ticker: "SNTF", Grade: CandidateGradeB, EligibleB: true, TotalScore: 72, MarketCapUSD: 220_000_000, RevenueGrowthPct: 55, CashRunwayMonths: 18},
		{BatchID: current.BatchID, SecurityID: securities[1].ID, Ticker: "WNTF", Grade: CandidateGradeB, EligibleB: true, TotalScore: 71, MarketCapUSD: 80_000_000, RevenueGrowthPct: 2000, CashRunwayMonths: 18},
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&[]FinancialMetricSnapshot{
		{BatchID: securityBatch.BatchID, SecurityID: securities[0].ID, RevenueGrowthAvailable: true, RunwayAvailable: true, QuarterlyRevenueYoYPct: 55, AnnualRevenueYoYPct: 44, LatestQuarterRevenueUSD: 20_000_000},
		{BatchID: securityBatch.BatchID, SecurityID: securities[1].ID, RevenueGrowthAvailable: true, RunwayAvailable: true, QuarterlyRevenueYoYPct: 2000, AnnualRevenueYoYPct: 1500, LatestQuarterRevenueUSD: 100_000, QualityFlagsJSON: `["low_revenue_base","extreme_revenue_growth"]`},
	}).Error; err != nil {
		t.Fatal(err)
	}

	summary, err := BuildCandidateSummaryWithOptions(context.Background(), db, CandidateSummaryOptions{LimitPerGrade: 5, IncludeB: true, ActionableOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if summary.TotalB != 2 || len(summary.ItemsB) != 1 || summary.ItemsB[0].Ticker != "SNTF" || strings.Contains(summary.Message, "WNTF") {
		t.Fatalf("summary = %#v", summary)
	}
}

func TestBuildCandidateSummaryFiltersByMinimumPriority(t *testing.T) {
	db := openMigratedTestDatabase(t)
	securities := []Security{
		{CIK: "0000006351", CompanyName: "High Priority", CatalogStatus: SecurityCatalogPublished},
		{CIK: "0000006352", CompanyName: "Low Priority", CatalogStatus: SecurityCatalogPublished},
	}
	if err := db.Create(&securities).Error; err != nil {
		t.Fatal(err)
	}
	securityBatch := UniverseBatch{BatchID: "security-priority-summary", Kind: BatchKindSecurity, Status: BatchStatusPublished, StartedAt: time.Now().Add(-time.Minute)}
	current := UniverseBatch{BatchID: "market-priority-summary", Kind: BatchKindPrescreen, Status: BatchStatusPublished, UniverseSourceVersion: securityBatch.BatchID, StartedAt: time.Now()}
	if err := db.Create(&[]UniverseBatch{securityBatch, current}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&CurrentBatchPointer{Kind: BatchKindPrescreen, BatchID: current.BatchID}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&[]CandidateScoreSnapshot{
		{BatchID: current.BatchID, SecurityID: securities[0].ID, Ticker: "HIGH", Grade: CandidateGradeA, EligibleA: true, TotalScore: 88, MarketCapUSD: 200_000_000, RevenueGrowthPct: 55, CashRunwayMonths: 18, RecentQualifiedInsider: true},
		{BatchID: current.BatchID, SecurityID: securities[1].ID, Ticker: "LOW", Grade: CandidateGradeA, EligibleA: true, TotalScore: 70, MarketCapUSD: 900_000_000, RevenueGrowthPct: 15, CashRunwayMonths: 7},
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&[]FinancialMetricSnapshot{
		{BatchID: securityBatch.BatchID, SecurityID: securities[0].ID, RevenueGrowthAvailable: true, RunwayAvailable: true, LatestQuarterRevenueUSD: 10_000_000, QuarterlyRevenueYoYPct: 55},
		{BatchID: securityBatch.BatchID, SecurityID: securities[1].ID, RevenueGrowthAvailable: true, RunwayAvailable: true, LatestQuarterRevenueUSD: 8_000_000, QuarterlyRevenueYoYPct: 15},
	}).Error; err != nil {
		t.Fatal(err)
	}
	summary, err := BuildCandidateSummaryWithOptions(context.Background(), db, CandidateSummaryOptions{
		LimitPerGrade:          5,
		IncludeA:               true,
		MinReviewPriorityScore: 70,
	})
	if err != nil {
		t.Fatal(err)
	}
	if summary.TotalA != 2 || len(summary.ItemsA) != 1 || summary.ItemsA[0].Ticker != "HIGH" {
		t.Fatalf("summary = %#v", summary)
	}
}

func TestCandidateWatchLifecycle(t *testing.T) {
	db := openMigratedTestDatabase(t)
	security := Security{CIK: "0000006401", CompanyName: "Watch Co", CatalogStatus: SecurityCatalogPublished}
	if err := db.Create(&security).Error; err != nil {
		t.Fatal(err)
	}
	batch := UniverseBatch{BatchID: "watch-market", Kind: BatchKindPrescreen, Status: BatchStatusPublished, StartedAt: time.Now()}
	if err := db.Create(&batch).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&CurrentBatchPointer{Kind: BatchKindPrescreen, BatchID: batch.BatchID}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&CandidateScoreSnapshot{BatchID: batch.BatchID, SecurityID: security.ID, Ticker: "WCH", Grade: CandidateGradeB, EligibleB: true, TotalScore: 72, MarketCapUSD: 180_000_000, RevenueGrowthPct: 45, CashRunwayMonths: 15}).Error; err != nil {
		t.Fatal(err)
	}

	watch, err := UpsertCandidateWatch(context.Background(), db, CandidateWatchInput{Ticker: "wch", Note: "track"})
	if err != nil {
		t.Fatal(err)
	}
	if watch.Ticker != "WCH" || watch.SecurityID != security.ID || watch.CIK != security.CIK || watch.CompanyName != security.CompanyName || watch.Status != "active" || watch.SourceBatchID != batch.BatchID {
		t.Fatalf("watch = %#v", watch)
	}
	if watch.BaselineCapturedAt == nil || watch.BaselineBatchID != batch.BatchID || watch.BaselineJSON == "" {
		t.Fatalf("watch baseline was not captured = %#v", watch)
	}
	originalBaseline := watch.BaselineJSON
	watch, err = UpsertCandidateWatch(context.Background(), db, CandidateWatchInput{Ticker: "WCH", Note: "updated"})
	if err != nil {
		t.Fatal(err)
	}
	if watch.Note != "updated" {
		t.Fatalf("updated watch = %#v", watch)
	}
	if watch.BaselineJSON != originalBaseline {
		t.Fatalf("ordinary watch update must retain original baseline: %#v", watch)
	}
	page, err := ListCandidateWatches(context.Background(), db, CandidateWatchQuery{Page: 1, PageSize: 10})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 || len(page.Items) != 1 || page.Items[0].Ticker != "WCH" {
		t.Fatalf("page = %#v", page)
	}
	if page.Items[0].LatestScore == nil || page.Items[0].LatestScore.TotalScore != 72 || page.Items[0].LatestScore.QualityTier == "" {
		t.Fatalf("latest score not attached = %#v", page.Items[0])
	}
	if page.Items[0].Baseline == nil || page.Items[0].Current == nil || page.Items[0].Baseline.TotalScore != 72 || page.Items[0].Current.TotalScore != 72 {
		t.Fatalf("watch comparison = %#v", page.Items[0])
	}
	candidates, err := ListCandidateScores(context.Background(), db, CandidateScoreQuery{Page: 1, PageSize: 10, FollowedOnly: true})
	if err != nil || candidates.Total != 1 || len(candidates.Items) != 1 || !candidates.Items[0].Followed || candidates.Items[0].Ticker != "WCH" {
		t.Fatalf("followed candidates=%#v err=%v", candidates, err)
	}
	watch, err = UpsertCandidateWatch(context.Background(), db, CandidateWatchInput{Ticker: "WCH", Note: "paused", Status: CandidateWatchStatusArchived})
	if err != nil {
		t.Fatal(err)
	}
	if watch.Status != CandidateWatchStatusArchived || watch.Note != "paused" {
		t.Fatalf("archived watch = %#v", watch)
	}
	page, err = ListCandidateWatches(context.Background(), db, CandidateWatchQuery{Page: 1, PageSize: 10})
	if err != nil || page.Total != 0 {
		t.Fatalf("default active page=%#v err=%v", page, err)
	}
	page, err = ListCandidateWatches(context.Background(), db, CandidateWatchQuery{Page: 1, PageSize: 10, Status: CandidateWatchStatusArchived})
	if err != nil || page.Total != 1 || page.Items[0].Status != CandidateWatchStatusArchived {
		t.Fatalf("archived page=%#v err=%v", page, err)
	}
	watch, err = UpsertCandidateWatch(context.Background(), db, CandidateWatchInput{Ticker: "WCH", Status: CandidateWatchStatusActive})
	if err != nil || watch.Status != CandidateWatchStatusActive {
		t.Fatalf("restore watch=%#v err=%v", watch, err)
	}
	if err := DeleteCandidateWatch(context.Background(), db, watch.ID); err != nil {
		t.Fatal(err)
	}
	page, err = ListCandidateWatches(context.Background(), db, CandidateWatchQuery{Page: 1, PageSize: 10})
	if err != nil || page.Total != 0 {
		t.Fatalf("after delete page=%#v err=%v", page, err)
	}
}

func TestCandidateWatchMetricComparisonsRetainBaselineAndCalculateChanges(t *testing.T) {
	capturedAt := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	baseline := CandidateWatchMetricSnapshot{
		BatchID: "baseline", CapturedAt: capturedAt, PriceCloseUSD: 10, PriceVolume: 1_000,
		MarketCapUSD: 100_000_000, TotalScore: 60, RevenueGrowthPct: 30, CashRunwayMonths: 12,
	}
	encoded, err := json.Marshal(baseline)
	if err != nil {
		t.Fatal(err)
	}
	items := []CandidateWatchResult{{
		CandidateWatch: CandidateWatch{Ticker: "CMP", BaselineJSON: string(encoded)},
		LatestScore: &CandidateScoreResult{CandidateScoreSnapshot: CandidateScoreSnapshot{
			BatchID: "current", Ticker: "CMP", MarketCapUSD: 120_000_000,
			TotalScore: 68, RevenueGrowthPct: 42, CashRunwayMonths: 10,
		}, PriceCloseUSD: 12, PriceVolume: 1_500},
	}}
	attachCandidateWatchMetricComparisons(items)
	item := items[0]
	if item.Baseline == nil || item.Current == nil || item.MetricChanges.PriceChangePct == nil || *item.MetricChanges.PriceChangePct != 20 {
		t.Fatalf("price comparison = %#v", item)
	}
	if item.MetricChanges.MarketCapChangePct == nil || *item.MetricChanges.MarketCapChangePct != 20 || item.MetricChanges.ScoreChange == nil || *item.MetricChanges.ScoreChange != 8 {
		t.Fatalf("metric changes = %#v", item.MetricChanges)
	}
	if item.MetricChanges.RevenueGrowthChangePct == nil || *item.MetricChanges.RevenueGrowthChangePct != 12 || item.MetricChanges.CashRunwayChangeMonths == nil || *item.MetricChanges.CashRunwayChangeMonths != -2 {
		t.Fatalf("fundamental changes = %#v", item.MetricChanges)
	}
}

func TestListCandidateReviewQueueUsesConfiguredCalendarAndKeepsExitedWatches(t *testing.T) {
	db := openMigratedTestDatabase(t)
	security := Security{CIK: "0000006411", CompanyName: "Review Co", CatalogStatus: SecurityCatalogPublished}
	if err := db.Create(&security).Error; err != nil {
		t.Fatal(err)
	}
	batch := UniverseBatch{BatchID: "review-queue-market", Kind: BatchKindPrescreen, Status: BatchStatusPublished, StartedAt: time.Now()}
	if err := db.Create(&batch).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&CurrentBatchPointer{Kind: BatchKindPrescreen, BatchID: batch.BatchID}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&CandidateScoreSnapshot{BatchID: batch.BatchID, SecurityID: security.ID, Ticker: "TODAY", Grade: CandidateGradeB, EligibleB: true, TotalScore: 74, MarketCapUSD: 160_000_000}).Error; err != nil {
		t.Fatal(err)
	}
	shanghai, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 28, 10, 0, 0, 0, shanghai)
	due := func(days int) *time.Time {
		value := time.Date(2026, 7, 28+days, 0, 0, 0, 0, time.UTC)
		return &value
	}
	watches := []CandidateWatch{
		{Ticker: "OVERDUE", Status: CandidateWatchStatusActive, ResearchStatus: CandidateResearchStatusResearching, NextReviewAt: due(-2)},
		{Ticker: "TODAY", SecurityID: security.ID, Status: CandidateWatchStatusActive, ResearchStatus: CandidateResearchStatusConviction, NextReviewAt: due(0)},
		{Ticker: "UPCOMING", Status: CandidateWatchStatusActive, ResearchStatus: CandidateResearchStatusInbox, NextReviewAt: due(7)},
		{Ticker: "TOO-FAR", Status: CandidateWatchStatusActive, ResearchStatus: CandidateResearchStatusInbox, NextReviewAt: due(8)},
		{Ticker: "ARCHIVED", Status: CandidateWatchStatusArchived, ResearchStatus: CandidateResearchStatusInbox, NextReviewAt: due(0)},
	}
	if err := db.Create(&watches).Error; err != nil {
		t.Fatal(err)
	}

	queue, err := ListCandidateReviewQueue(context.Background(), db, now)
	if err != nil {
		t.Fatal(err)
	}
	if queue.AsOf != "2026-07-28" || queue.OverdueCount != 1 || queue.DueTodayCount != 1 || queue.UpcomingCount != 1 || len(queue.Items) != 3 {
		t.Fatalf("queue = %#v", queue)
	}
	if queue.Items[0].Ticker != "OVERDUE" || queue.Items[0].ReviewState != "overdue" || queue.Items[0].DaysUntilReview != -2 {
		t.Fatalf("overdue item = %#v", queue.Items[0])
	}
	if queue.Items[1].Ticker != "TODAY" || queue.Items[1].ReviewState != "due_today" || !queue.Items[1].CurrentCandidate || queue.Items[1].LatestScore == nil {
		t.Fatalf("today item = %#v", queue.Items[1])
	}
	if queue.Items[2].Ticker != "UPCOMING" || queue.Items[2].ReviewState != "upcoming" || queue.Items[2].DaysUntilReview != 7 || queue.Items[2].CurrentCandidate {
		t.Fatalf("upcoming item = %#v", queue.Items[2])
	}
}

func TestCandidateWatchResearchFieldsSupportPartialUpdates(t *testing.T) {
	db := openMigratedTestDatabase(t)
	thesis := "收入增长可持续"
	risk := "现金消耗加快"
	marketConcern := "市场担忧下一轮融资"
	falsifiableJudgment := "若下一份 10-Q 显示现金 runway 仍超过 12 个月，则该担忧不成立"
	catalyst := "下一次季度财报"
	catalystSource := "https://www.sec.gov/edgar/browse/?CIK=RSCH"
	catalystDate := time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC)
	nextReview := time.Date(2026, 7, 31, 0, 0, 0, 0, time.UTC)
	researching := CandidateResearchStatusResearching
	watch, err := UpsertCandidateWatch(context.Background(), db, CandidateWatchInput{
		Ticker: "RSCH", ResearchStatus: &researching, Thesis: &thesis, RiskNotes: &risk,
		MarketConcern: &marketConcern, FalsifiableJudgment: &falsifiableJudgment,
		Catalyst: &catalyst, CatalystSource: &catalystSource, CatalystDate: &catalystDate,
		NextReviewAt: &nextReview,
	})
	if err != nil {
		t.Fatal(err)
	}
	if watch.ResearchStatus != researching || watch.Thesis != thesis || watch.RiskNotes != risk ||
		watch.MarketConcern != marketConcern || watch.FalsifiableJudgment != falsifiableJudgment ||
		watch.Catalyst != catalyst || watch.CatalystSource != catalystSource || watch.CatalystDate == nil || !watch.CatalystDate.Equal(catalystDate) ||
		watch.NextReviewAt == nil || !watch.NextReviewAt.Equal(nextReview) {
		t.Fatalf("created research watch = %#v", watch)
	}
	var versionCount int64
	if err := db.Model(&CandidateResearchMemoVersion{}).Where("ticker = ?", "RSCH").Count(&versionCount).Error; err != nil || versionCount != 1 {
		t.Fatalf("initial memo versions=%d err=%v, want 1", versionCount, err)
	}

	conviction := CandidateResearchStatusConviction
	watch, err = UpsertCandidateWatch(context.Background(), db, CandidateWatchInput{Ticker: "RSCH", ResearchStatus: &conviction, Note: "reviewed"})
	if err != nil {
		t.Fatal(err)
	}
	if watch.ResearchStatus != conviction || watch.Thesis != thesis || watch.RiskNotes != risk ||
		watch.MarketConcern != marketConcern || watch.FalsifiableJudgment != falsifiableJudgment ||
		watch.Catalyst != catalyst || watch.CatalystSource != catalystSource || watch.CatalystDate == nil || !watch.CatalystDate.Equal(catalystDate) ||
		watch.NextReviewAt == nil || !watch.NextReviewAt.Equal(nextReview) || watch.Note != "reviewed" {
		t.Fatalf("partial update research watch = %#v", watch)
	}
	if err := db.Model(&CandidateResearchMemoVersion{}).Where("ticker = ?", "RSCH").Count(&versionCount).Error; err != nil || versionCount != 1 {
		t.Fatalf("status-only change should not create memo version=%d err=%v", versionCount, err)
	}

	invalid := "unknown"
	if _, err := UpsertCandidateWatch(context.Background(), db, CandidateWatchInput{Ticker: "RSCH", ResearchStatus: &invalid}); err == nil {
		t.Fatal("expected invalid research status error")
	}
	watch, err = UpsertCandidateWatch(context.Background(), db, CandidateWatchInput{Ticker: "RSCH", ClearNextReviewAt: true})
	if err != nil || watch.NextReviewAt != nil {
		t.Fatalf("clear review date watch=%#v err=%v", watch, err)
	}
	if err := db.Model(&CandidateResearchMemoVersion{}).Where("ticker = ?", "RSCH").Count(&versionCount).Error; err != nil || versionCount != 2 {
		t.Fatalf("review clear should create memo version=%d err=%v", versionCount, err)
	}
	watch, err = UpsertCandidateWatch(context.Background(), db, CandidateWatchInput{Ticker: "RSCH", ClearCatalystDate: true})
	if err != nil || watch.CatalystDate != nil || watch.Catalyst != catalyst || watch.MarketConcern != marketConcern {
		t.Fatalf("clear catalyst date watch=%#v err=%v", watch, err)
	}
	var latest CandidateResearchMemoVersion
	if err := db.Where("ticker = ?", "RSCH").Order("version DESC").First(&latest).Error; err != nil || latest.Version != 3 || latest.CatalystDate != nil || latest.Author != "local_user" {
		t.Fatalf("latest memo version=%#v err=%v", latest, err)
	}
}

func TestBuildCandidateSummaryUsesCurrentPublishedBatchAndLimitsByGrade(t *testing.T) {
	db := openMigratedTestDatabase(t)
	securities := []Security{
		{CIK: "0000005001", CompanyName: "Alpha", CatalogStatus: SecurityCatalogPublished},
		{CIK: "0000005002", CompanyName: "Beta", CatalogStatus: SecurityCatalogPublished},
		{CIK: "0000005003", CompanyName: "Gamma", CatalogStatus: SecurityCatalogPublished},
		{CIK: "0000005004", CompanyName: "Old", CatalogStatus: SecurityCatalogPublished},
	}
	if err := db.Create(&securities).Error; err != nil {
		t.Fatal(err)
	}
	old := UniverseBatch{BatchID: "old", Kind: BatchKindPrescreen, Status: BatchStatusPublished, StartedAt: time.Now().Add(-time.Hour)}
	current := UniverseBatch{BatchID: "current", Kind: BatchKindPrescreen, Status: BatchStatusPublished, StartedAt: time.Now()}
	if err := db.Create(&[]UniverseBatch{old, current}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&CurrentBatchPointer{Kind: BatchKindPrescreen, BatchID: current.BatchID}).Error; err != nil {
		t.Fatal(err)
	}
	rows := []CandidateScoreSnapshot{
		{BatchID: old.BatchID, SecurityID: securities[3].ID, Ticker: "OLD", Grade: CandidateGradeA, EligibleA: true, TotalScore: 99},
		{BatchID: current.BatchID, SecurityID: securities[0].ID, Ticker: "ALPH", Grade: CandidateGradeA, EligibleA: true, EligibleB: true, TotalScore: 88, MarketCapUSD: 240_000_000, RevenueGrowthPct: 55.4, CashRunwayMonths: 18.2, RecentQualifiedInsider: true},
		{BatchID: current.BatchID, SecurityID: securities[1].ID, Ticker: "BETA", Grade: CandidateGradeA, EligibleA: true, EligibleB: true, TotalScore: 82, MarketCapUSD: 320_000_000, RevenueGrowthPct: 41.1, CashRunwayMonths: 13.0},
		{BatchID: current.BatchID, SecurityID: securities[2].ID, Ticker: "GAMM", Grade: CandidateGradeB, EligibleB: true, TotalScore: 71, MarketCapUSD: 780_000_000, RevenueGrowthPct: 25.0, CashRunwayMonths: 8.5, ActiveBlocksA: true},
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}

	summary, err := BuildCandidateSummary(context.Background(), db, 1)
	if err != nil {
		t.Fatal(err)
	}
	if summary.BatchID != "current" || summary.TotalA != 2 || summary.TotalB != 1 {
		t.Fatalf("summary counts = %#v", summary)
	}
	if len(summary.ItemsA) != 1 || summary.ItemsA[0].Ticker != "ALPH" {
		t.Fatalf("items A = %#v", summary.ItemsA)
	}
	if len(summary.ItemsB) != 1 || summary.ItemsB[0].Ticker != "GAMM" {
		t.Fatalf("items B = %#v", summary.ItemsB)
	}
	if !strings.Contains(summary.Message, "研究候选") || !strings.Contains(summary.Message, "A级候选 2 只") || !strings.Contains(summary.Message, "ALPH") || !strings.Contains(summary.Message, "GAMM") || strings.Contains(summary.Message, "OLD") {
		t.Fatalf("message = %s", summary.Message)
	}
}

func TestBuildCandidateSummaryWithoutCurrentBatchIsEmpty(t *testing.T) {
	db := openMigratedTestDatabase(t)

	summary, err := BuildCandidateSummary(context.Background(), db, 5)
	if err != nil {
		t.Fatal(err)
	}
	if summary.TotalA != 0 || summary.TotalB != 0 || len(summary.ItemsA) != 0 || len(summary.ItemsB) != 0 || !strings.Contains(summary.Message, "暂无小盘候选批次") {
		t.Fatalf("summary = %#v", summary)
	}
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func containsPriorityReason(values []ReviewPriorityReason, label string, points int) bool {
	for _, value := range values {
		if value.Label == label && value.Points == points {
			return true
		}
	}
	return false
}

func TestCandidatePriceFreshnessUsesLatestCompletedNYSESession(t *testing.T) {
	date := func(value string) *time.Time {
		parsed, err := time.Parse(time.DateOnly, value)
		if err != nil {
			t.Fatal(err)
		}
		return &parsed
	}
	ny := mustNY(t)
	tests := []struct {
		name     string
		expected string
		actual   string
		now      time.Time
		want     string
		wantAge  int
	}{
		{
			name:     "pre-close prior session is current",
			expected: "2026-07-17",
			actual:   "2026-07-16",
			now:      time.Date(2026, 7, 17, 9, 11, 0, 0, ny),
			want:     PriceFreshnessCurrent,
			wantAge:  0,
		},
		{
			name:     "after close prior session is stale fallback",
			expected: "2026-07-17",
			actual:   "2026-07-16",
			now:      time.Date(2026, 7, 17, 16, 1, 0, 0, ny),
			want:     PriceFreshnessPreviousTradingDay,
			wantAge:  1,
		},
		{
			name:     "pre-close skips weekend and NYSE holiday",
			expected: "2026-07-06",
			actual:   "2026-07-02",
			now:      time.Date(2026, 7, 6, 10, 0, 0, 0, ny),
			want:     PriceFreshnessCurrent,
			wantAge:  0,
		},
		{
			name:     "weekend batch keeps Friday close current",
			expected: "2026-07-18",
			actual:   "2026-07-17",
			now:      time.Date(2026, 7, 19, 9, 43, 0, 0, ny),
			want:     PriceFreshnessCurrent,
			wantAge:  0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, age := candidatePriceFreshnessAt(tt.expected, date(tt.actual), tt.now)
			if status != tt.want || age != tt.wantAge {
				t.Fatalf("freshness = (%q, %d), want (%q, %d)", status, age, tt.want, tt.wantAge)
			}
		})
	}
}

func TestBatchAndProviderQueriesPaginateFilterAndOrder(t *testing.T) {
	db := openMigratedTestDatabase(t)
	now := time.Date(2026, 6, 23, 9, 0, 0, 0, time.UTC)
	security := Security{CIK: "0000000001", CompanyName: "Batch Summary Co", CatalogStatus: SecurityCatalogPublished}
	if err := db.Create(&security).Error; err != nil {
		t.Fatal(err)
	}
	batches := []UniverseBatch{
		{BatchID: "older", Kind: BatchKindSecurity, Status: BatchStatusPublished, StartedAt: now.Add(-time.Hour)},
		{BatchID: "newer", Kind: BatchKindSecurity, Status: BatchStatusPublished, StartedAt: now},
		{BatchID: "failed", Kind: BatchKindPrescreen, Status: BatchStatusFailed, StartedAt: now.Add(time.Hour)},
	}
	if err := db.Create(&batches).Error; err != nil {
		t.Fatal(err)
	}
	page, err := ListBatches(context.Background(), db, BatchQuery{Page: 1, PageSize: 1, Kind: BatchKindSecurity, Status: BatchStatusPublished})
	if err != nil || page.Total != 2 || len(page.Items) != 1 || page.Items[0].BatchID != "newer" {
		t.Fatalf("page=%#v err=%v", page, err)
	}
	attemptsJSON, err := encodeProviderAttempts([]ProviderAttempt{{Provider: "tiingo", Status: "partial", Expected: 4, Records: 2, Remaining: 2, CoveragePct: 50}, {Provider: "twelvedata", Status: "success", Expected: 2, Records: 1, Remaining: 1, CoveragePct: 50}})
	if err != nil {
		t.Fatal(err)
	}
	runs := []ProviderRun{
		{BatchID: "older", Provider: "p", Status: ProviderStatusActive, CreatedAt: now.Add(-time.Hour)},
		{BatchID: "newer", Provider: "p", Status: ProviderStatusDegraded, SourceVersion: "chain:test", RecordCount: 3, ExpectedCount: 4, CoveragePct: 75, AttemptsJSON: attemptsJSON, FallbackUsed: true, CreatedAt: now},
		{BatchID: "failed", Provider: "other", Status: ProviderStatusDegraded, CreatedAt: now.Add(time.Hour)},
	}
	if err := db.Create(&runs).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&[]PriceSnapshot{
		{Source: "tiingo", SourceVersion: "chain:test", Symbol: "AAA", TradeDate: now, CloseMicros: 1_000_000, Currency: "USD"},
		{Source: "tiingo", SourceVersion: "chain:test", Symbol: "BBB", TradeDate: now, CloseMicros: 1_000_000, Currency: "USD"},
		{Source: "twelvedata", SourceVersion: "chain:test", Symbol: "CCC", TradeDate: now, CloseMicros: 1_000_000, Currency: "USD"},
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&CandidateScoreSnapshot{BatchID: "newer", SecurityID: security.ID, Ticker: "AAA"}).Error; err != nil {
		t.Fatal(err)
	}
	page, err = ListBatches(context.Background(), db, BatchQuery{Page: 1, PageSize: 1, Kind: BatchKindSecurity, Status: BatchStatusPublished})
	if err != nil {
		t.Fatal(err)
	}
	summary := page.Items[0].ProviderSummary
	if summary == nil || summary.ExpectedCount != 4 || summary.RecordCount != 3 || summary.PriceSourceCounts["tiingo"] != 2 || summary.PriceSourceCounts["twelvedata"] != 1 || !summary.FallbackUsed || len(summary.ProviderAttempts) != 2 || page.Items[0].CandidateCount != 1 {
		t.Fatalf("batch summary=%#v candidate_count=%d", summary, page.Items[0].CandidateCount)
	}
	diagnostics, err := ListProviderDiagnostics(context.Background(), db, ProviderRunQuery{Page: 1, PageSize: 1, Provider: "p"})
	if err != nil || diagnostics.Total != 2 || len(diagnostics.Items) != 1 || diagnostics.Items[0].BatchID != "newer" || !diagnostics.Items[0].FallbackUsed || len(diagnostics.Items[0].Attempts) != 2 {
		t.Fatalf("diagnostics=%#v err=%v", diagnostics, err)
	}
}
