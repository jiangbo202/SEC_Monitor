package discovery

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestListCurrentCandidateMarketPriceRecoveryQueue(t *testing.T) {
	db := openMigratedTestDatabase(t)
	now := time.Now().UTC().Truncate(24 * time.Hour)
	security := []Security{
		{CIK: "0000000001", CompanyName: "Missing", CatalogStatus: SecurityCatalogPublished},
		{CIK: "0000000002", CompanyName: "Fallback", CatalogStatus: SecurityCatalogPublished},
		{CIK: "0000000003", CompanyName: "Current", CatalogStatus: SecurityCatalogPublished},
	}
	if err := db.Create(&security).Error; err != nil {
		t.Fatal(err)
	}
	batch := UniverseBatch{BatchID: "market-recovery", Kind: BatchKindPrescreen, Status: BatchStatusPublished, EffectiveDate: now.Format(time.DateOnly), StartedAt: now}
	if err := db.Create(&batch).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&CurrentBatchPointer{Kind: BatchKindPrescreen, BatchID: batch.BatchID}).Error; err != nil {
		t.Fatal(err)
	}
	fallback := PriceSnapshot{Source: PriceSourceLocalCache, SourceVersion: "fallback", Symbol: "FALL", TradeDate: now, CloseMicros: 1_000_000, QualityStatus: QualityStatusValid}
	current := PriceSnapshot{Source: "longbridge", SourceVersion: "current", Symbol: "GOOD", TradeDate: now, CloseMicros: 1_000_000, QualityStatus: QualityStatusValid}
	if err := db.Create(&[]PriceSnapshot{fallback, current}).Error; err != nil {
		t.Fatal(err)
	}
	var prices []PriceSnapshot
	if err := db.Order("id ASC").Find(&prices).Error; err != nil {
		t.Fatal(err)
	}
	rows := []UniverseSnapshot{
		{BatchID: batch.BatchID, SecurityID: security[0].ID, Ticker: "MISS", Included: true},
		{BatchID: batch.BatchID, SecurityID: security[1].ID, Ticker: "FALL", Included: true, PriceSnapshotID: &prices[0].ID},
		{BatchID: batch.BatchID, SecurityID: security[2].ID, Ticker: "GOOD", Included: true, PriceSnapshotID: &prices[1].ID},
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	scores := []CandidateScoreSnapshot{
		{BatchID: batch.BatchID, SecurityID: security[0].ID, Ticker: "MISS", Grade: CandidateGradeB},
		{BatchID: batch.BatchID, SecurityID: security[1].ID, Ticker: "FALL", Grade: CandidateGradeB},
		{BatchID: batch.BatchID, SecurityID: security[2].ID, Ticker: "GOOD", Grade: CandidateGradeA},
	}
	if err := db.Create(&scores).Error; err != nil {
		t.Fatal(err)
	}
	queue, err := ListCurrentCandidateMarketPriceRecoveryQueue(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	if len(queue.Items) != 2 {
		t.Fatalf("queue items = %+v, want two recovery items", queue.Items)
	}
	if queue.Items[0].Ticker != "MISS" || queue.Items[0].Issue != "missing" {
		t.Fatalf("first item = %+v", queue.Items[0])
	}
	if queue.Items[1].Ticker != "FALL" || queue.Items[1].Issue != "local_fallback" {
		t.Fatalf("second item = %+v", queue.Items[1])
	}
}

func TestRepriceCurrentCandidateFromLocalHistoryPublishesCorrectionBatch(t *testing.T) {
	db := openMigratedTestDatabase(t)
	now := time.Date(2026, 7, 21, 22, 0, 0, 0, time.UTC)
	tradeDate := time.Date(2026, 7, 21, 0, 0, 0, 0, time.UTC)
	security := Security{CIK: "0000000101", CompanyName: "Reprice", CatalogStatus: SecurityCatalogPublished}
	if err := db.Create(&security).Error; err != nil {
		t.Fatal(err)
	}
	share := ShareSnapshot{SecurityID: security.ID, Instant: now.AddDate(0, 0, -1), Accession: "share", QualityStatus: QualityStatusValid, Shares: 10_000_000}
	if err := db.Create(&share).Error; err != nil {
		t.Fatal(err)
	}
	basePrice := PriceSnapshot{Source: "longbridge", SourceVersion: "base", Symbol: "REPX", TradeDate: now.AddDate(0, 0, -1), CloseMicros: 10_000_000, QualityStatus: QualityStatusValid}
	latestPrice := PriceSnapshot{Source: "longbridge", SourceVersion: "history", Symbol: "REPX", TradeDate: tradeDate, CloseMicros: 20_000_000, QualityStatus: QualityStatusValid}
	if err := db.Create(&[]PriceSnapshot{basePrice, latestPrice}).Error; err != nil {
		t.Fatal(err)
	}
	var prices []PriceSnapshot
	if err := db.Order("id ASC").Find(&prices).Error; err != nil {
		t.Fatal(err)
	}
	effectiveAt, err := parseNYCivilDate(now.Format(time.DateOnly))
	if err != nil {
		t.Fatal(err)
	}
	versionHash := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	versions, err := normalizeSourceVersions(now.Format(time.DateOnly), SourceVersion{Source: BatchKindSecurity, Version: "security", SHA256: versionHash, EffectiveAt: effectiveAt})
	if err != nil {
		t.Fatal(err)
	}
	batch := UniverseBatch{BatchID: "market-base", Kind: BatchKindPrescreen, Status: BatchStatusPublished, EffectiveDate: now.Format(time.DateOnly), UniverseSourceVersion: "security", SourceVersionsJSON: mustJSON(t, versions), ContentSHA256: versionHash, StartedAt: now}
	securityBatch := UniverseBatch{BatchID: "security", Kind: BatchKindSecurity, Status: BatchStatusPublished, EffectiveDate: now.Format(time.DateOnly), SourceVersionsJSON: mustJSON(t, versions), ContentSHA256: versionHash, StartedAt: now}
	if err := db.Create(&[]UniverseBatch{securityBatch, batch}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&CurrentBatchPointer{Kind: BatchKindPrescreen, BatchID: batch.BatchID}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&SecurityBatchIdentity{BatchID: "security", SecurityID: security.ID, Ticker: "REPX", SIC: 3841, MappingStatus: MappingStatusCurrent}).Error; err != nil {
		t.Fatal(err)
	}
	metric := FinancialMetricSnapshot{BatchID: "security", SecurityID: security.ID, RevenueGrowthAvailable: true, QuarterlyRevenueYoYPct: 50, RunwayAvailable: true, CashRunwayMonths: 15, GrossMarginPct: 55}
	if err := db.Create(&metric).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&UniverseSnapshot{BatchID: batch.BatchID, SecurityID: security.ID, Ticker: "REPX", MarketCapUSD: 100_000_000, Included: true, Status: EffectiveStatusPrescreen, QualityStatus: QualityStatusValid, PriceSnapshotID: &prices[0].ID, ShareSnapshotID: &share.ID}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&CandidateScoreSnapshot{BatchID: batch.BatchID, SecurityID: security.ID, Ticker: "REPX", MarketCapUSD: 100_000_000, Grade: CandidateGradeB, EligibleB: true, TotalScore: 65}).Error; err != nil {
		t.Fatal(err)
	}
	result, err := RepriceCurrentCandidateFromLocalHistory(context.Background(), db, "repx", now)
	if err != nil {
		t.Fatal(err)
	}
	if result.BatchID == "" || result.BatchID == batch.BatchID || result.MarketCapUSD != 200_000_000 {
		t.Fatalf("result = %+v", result)
	}
	var pointer CurrentBatchPointer
	if err := db.First(&pointer, "kind = ?", BatchKindPrescreen).Error; err != nil {
		t.Fatal(err)
	}
	if pointer.BatchID != result.BatchID {
		t.Fatalf("current batch = %s, want %s", pointer.BatchID, result.BatchID)
	}
	var original, corrected UniverseSnapshot
	if err := db.Where("batch_id = ?", batch.BatchID).First(&original).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Where("batch_id = ?", result.BatchID).First(&corrected).Error; err != nil {
		t.Fatal(err)
	}
	if original.MarketCapUSD != 100_000_000 || corrected.MarketCapUSD != 200_000_000 || corrected.PriceSnapshotID == nil || *corrected.PriceSnapshotID != prices[1].ID {
		t.Fatalf("original=%+v corrected=%+v", original, corrected)
	}
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}
