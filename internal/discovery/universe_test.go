package discovery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"
)

type fakeMetadataSource struct {
	records []SecuritySourceRecord
	version SourceVersion
	err     error
}

func (f fakeMetadataSource) Load(context.Context) ([]SecuritySourceRecord, SourceVersion, error) {
	return f.records, f.version, f.err
}

type fakeShareSource struct {
	facts   []ShareFact
	version SourceVersion
	err     error
}

type fakeFinancialSource struct {
	facts   []FinancialFact
	version SourceVersion
	err     error
}

type fakeInsiderSource struct {
	transactions []InsiderTransaction
	version      SourceVersion
	err          error
}

type fakeCapitalEventSource struct {
	events  []CapitalEvent
	version SourceVersion
	err     error
}

func (f fakeCapitalEventSource) Load(context.Context, map[string]struct{}, time.Time) ([]CapitalEvent, SourceVersion, error) {
	return f.events, f.version, f.err
}

type fakePriceProvider struct {
	name    string
	records []PriceRecord
	result  ProviderResult
	err     error
	called  int
}

func (f *fakePriceProvider) ProviderName() string { return f.name }
func (f *fakePriceProvider) Load(context.Context, []Listing) ([]PriceRecord, ProviderResult, error) {
	f.called++
	return f.records, f.result, f.err
}

func (f fakeShareSource) LoadLatestShares(context.Context, map[string]struct{}) ([]ShareFact, SourceVersion, error) {
	return f.facts, f.version, f.err
}

func (f fakeFinancialSource) LoadFinancialFacts(context.Context, map[string]struct{}) ([]FinancialFact, SourceVersion, error) {
	return f.facts, f.version, f.err
}

func (f fakeInsiderSource) LoadInsiderTransactions(context.Context, map[string]struct{}, time.Time) ([]InsiderTransaction, SourceVersion, error) {
	return f.transactions, f.version, f.err
}

func noEvents(now time.Time) NoCapitalEventsSource {
	return NoCapitalEventsSource{Version: testSourceVersion("test:capital-events", "none", now), TestOnly: true}
}

func TestCoordinatorPublishesPrescreenWithExactEvidence(t *testing.T) {
	db := openMigratedTestDatabase(t)
	ny, _ := time.LoadLocation("America/New_York")
	now := time.Date(2026, 6, 23, 9, 0, 0, 0, ny)
	record := SecuritySourceRecord{CIK: "0000005678", Ticker: "CAP", CompanyName: "Cap Co", SecurityName: "Cap Co Common Stock", Exchange: "Nasdaq", SIC: 3571, StateOfIncorporation: "DE", LatestAnnualForm: "10-K", RecentForms: []string{"10-Q"}, MappingStatus: MappingStatusCurrent}
	fact := ShareFact{CIK: record.CIK, Concept: "dei:EntityCommonStockSharesOutstanding", Unit: "shares", Form: "10-Q", Accession: "0000005678-26-000001", Instant: now.AddDate(0, 0, -1), FiledAt: now.Add(-time.Hour), AcceptedAt: now.Add(-time.Hour), Shares: 10_000_000, SourceURL: "https://www.sec.gov/fact"}
	calendar := &stubMarketCalendar{}
	c := Coordinator{DB: db, Metadata: fakeMetadataSource{records: []SecuritySourceRecord{record}, version: testSourceVersion("metadata", "v1", now)}, Shares: fakeShareSource{facts: []ShareFact{fact}, version: testSourceVersion("shares", "v1", now)}, Events: noEvents(now), Clock: func() time.Time { return now }}
	securityBatch, err := c.SyncSecurityUniverse(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	gold := sha256.Sum256(frozenMarketGoldCSV)
	window := make([]providerWindowDay, 0, ProviderActivationTradingDays)
	day := time.Date(2026, 5, 26, 0, 0, 0, 0, ny)
	for !day.After(time.Date(2026, 6, 22, 0, 0, 0, 0, ny)) {
		if day.Weekday() != time.Saturday && day.Weekday() != time.Sunday {
			window = append(window, providerWindowDay{Date: day.Format(time.DateOnly), CoveragePct: 100, Timely: true, ValidationOK: true, GoldReady: true})
		}
		day = day.AddDate(0, 0, 1)
	}
	windowJSON, _ := json.Marshal(window)
	health := ProviderHealth{Provider: "fake", Status: ProviderStatusActive, QualifiedTradingDays: len(window), LastTradeDate: window[len(window)-1].Date, WindowJSON: string(windowJSON), GoldEvidenceReady: true, GoldSHA256: hex.EncodeToString(gold[:]), UpdatedAt: now}
	if err := db.Create(&health).Error; err != nil {
		t.Fatal(err)
	}
	price := PriceRecord{Symbol: "CAP", TradeDate: now, CloseMicros: 5_000_000, Volume: 100, Currency: "USD", Source: "fake"}
	goldDate := time.Date(2026, 6, 18, 0, 0, 0, 0, ny)
	goldPrices := []PriceRecord{
		{Symbol: "BRK.B", TradeDate: goldDate, CloseMicros: 500_123_456, Currency: "USD", Source: "fake"},
		{Symbol: "PER", TradeDate: goldDate, CloseMicros: 10_250_000, Currency: "USD", Source: "fake"},
	}
	priceSHA := sha256.Sum256([]byte("prices"))
	provider := &fakePriceProvider{name: "fake", records: append([]PriceRecord{price}, goldPrices...), result: ProviderResult{Provider: "fake", Status: ProviderStatusActive, SourceVersion: "pv1", SHA256: hex.EncodeToString(priceSHA[:]), EffectiveDate: now, Records: 3, Expected: 1, CoveragePct: 100, Timely: true}}
	c.Prices = provider
	c.Calendar = calendar
	c.providerDayEvaluator = func(result ProviderResult, _ []PriceRecord, _ time.Time) (ProviderDayResult, error) {
		return ProviderDayResult{TradeDate: result.EffectiveDate, coveragePct: 100, timely: true, validationOK: true, goldReady: true, goldSHA256: health.GoldSHA256}, nil
	}
	batch, err := c.SyncMarketPrices(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if batch.Status != BatchStatusPublished || batch.Kind != BatchKindPrescreen {
		t.Fatalf("batch=%#v", batch)
	}
	var snapshot UniverseSnapshot
	if err := db.First(&snapshot, "batch_id = ?", batch.BatchID).Error; err != nil {
		t.Fatal(err)
	}
	if !snapshot.Included || snapshot.MarketCapUSD != 50_000_000 || snapshot.PriceSnapshotID == nil || snapshot.ShareSnapshotID == nil {
		t.Fatalf("snapshot=%#v", snapshot)
	}
	if securityBatch.BatchID == batch.BatchID {
		t.Fatal("security and prescreen batches must differ")
	}
}

func testSourceVersion(source, version string, day time.Time) SourceVersion {
	sum := sha256.Sum256([]byte(source + ":" + version))
	return SourceVersion{Source: source, Version: version, SHA256: hex.EncodeToString(sum[:]), EffectiveAt: day}
}

func TestCoordinatorPublishesSecurityUniverseIdempotently(t *testing.T) {
	db := openMigratedTestDatabase(t)
	ny, _ := time.LoadLocation("America/New_York")
	now := time.Date(2026, 6, 23, 9, 0, 0, 0, ny)
	record := SecuritySourceRecord{CIK: "0000001234", Ticker: "ACME", CompanyName: "Acme", SecurityName: "Acme Common Stock", Exchange: "Nasdaq", SIC: 3571, StateOfIncorporation: "DE", LatestAnnualForm: "10-K", RecentForms: []string{"10-K", "10-Q"}, MappingStatus: MappingStatusCurrent}
	fact := ShareFact{CIK: record.CIK, Concept: "dei:EntityCommonStockSharesOutstanding", Unit: "shares", Form: "10-Q", Accession: "0000001234-26-000001", Instant: now.AddDate(0, 0, -1), FiledAt: now.Add(-time.Hour), AcceptedAt: now.Add(-time.Hour), Shares: 10_000_000, SourceURL: "https://www.sec.gov/fact"}
	c := Coordinator{DB: db, Metadata: fakeMetadataSource{records: []SecuritySourceRecord{record}, version: testSourceVersion("metadata", "v1", now)}, Shares: fakeShareSource{facts: []ShareFact{fact}, version: testSourceVersion("shares", "v1", now)}, Events: noEvents(now), Clock: func() time.Time { return now }}

	first, err := c.SyncSecurityUniverse(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	second, err := c.SyncSecurityUniverse(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if first.BatchID != second.BatchID || first.Status != BatchStatusPublished {
		t.Fatalf("batches = %#v %#v", first, second)
	}
	var pointer CurrentBatchPointer
	if err := db.First(&pointer, "kind = ?", BatchKindSecurity).Error; err != nil {
		t.Fatal(err)
	}
	if pointer.BatchID != first.BatchID {
		t.Fatalf("pointer = %s, want %s", pointer.BatchID, first.BatchID)
	}
	var classifications int64
	if err := db.Model(&ClassificationSnapshot{}).Where("batch_id = ?", first.BatchID).Count(&classifications).Error; err != nil {
		t.Fatal(err)
	}
	if classifications != 1 {
		t.Fatalf("classifications = %d", classifications)
	}
	changed := record
	changed.CompanyName = "Conflicting content"
	c.Metadata = fakeMetadataSource{records: []SecuritySourceRecord{changed}, version: testSourceVersion("metadata", "v1", now)}
	if _, err := c.SyncSecurityUniverse(context.Background()); err == nil || !strings.Contains(err.Error(), "content conflict") {
		t.Fatalf("same source versions with changed content error = %v", err)
	}
}

func TestCoordinatorStagesFinancialMetricSnapshots(t *testing.T) {
	db := openMigratedTestDatabase(t)
	now := time.Date(2026, 6, 23, 9, 0, 0, 0, time.UTC)
	record := SecuritySourceRecord{CIK: "0000004321", Ticker: "FIN", CompanyName: "Financial Metrics Co", SecurityName: "Financial Metrics Co Common Stock", Exchange: "Nasdaq", SIC: 3571, StateOfIncorporation: "DE", LatestAnnualForm: "10-K", RecentForms: []string{"10-K", "10-Q"}, MappingStatus: MappingStatusCurrent}
	share := ShareFact{CIK: record.CIK, Concept: "dei:EntityCommonStockSharesOutstanding", Unit: "shares", Form: "10-Q", Accession: "0000004321-26-000001", Instant: now.AddDate(0, 0, -1), FiledAt: now.Add(-time.Hour), AcceptedAt: now.Add(-time.Hour), Shares: 10_000_000, SourceURL: "https://www.sec.gov/share"}
	financials := []FinancialFact{
		financialDuration(FinancialMetricRevenue, "2025-01-01", "2025-03-31", 10_000_000),
		financialDuration(FinancialMetricRevenue, "2026-01-01", "2026-03-31", 15_000_000),
		financialDuration(FinancialMetricRevenue, "2024-01-01", "2024-12-31", 40_000_000),
		financialDuration(FinancialMetricRevenue, "2025-01-01", "2025-12-31", 55_000_000),
		financialInstant(FinancialMetricCash, "2026-03-31", 24_000_000),
		financialInstant(FinancialMetricShortTermInvestments, "2026-03-31", 6_000_000),
		financialDuration(FinancialMetricOperatingCashFlow, "2025-04-01", "2025-06-30", -3_000_000),
		financialDuration(FinancialMetricOperatingCashFlow, "2025-07-01", "2025-09-30", -4_000_000),
		financialDuration(FinancialMetricOperatingCashFlow, "2025-10-01", "2025-12-31", -5_000_000),
		financialDuration(FinancialMetricOperatingCashFlow, "2026-01-01", "2026-03-31", -6_000_000),
		financialDuration(FinancialMetricCapitalExpenditure, "2025-04-01", "2025-06-30", 1_000_000),
		financialDuration(FinancialMetricCapitalExpenditure, "2025-07-01", "2025-09-30", 1_500_000),
		financialDuration(FinancialMetricCapitalExpenditure, "2025-10-01", "2025-12-31", 1_500_000),
		financialDuration(FinancialMetricCapitalExpenditure, "2026-01-01", "2026-03-31", 2_000_000),
	}
	for i := range financials {
		financials[i].CIK = record.CIK
		financials[i].FiledAt = now.Add(-time.Hour)
		financials[i].AcceptedAt = now.Add(-time.Hour)
		financials[i].Concept = "us-gaap:" + financials[i].Metric
		financials[i].SourceURL = "https://www.sec.gov/financial"
	}
	c := Coordinator{DB: db, Metadata: fakeMetadataSource{records: []SecuritySourceRecord{record}, version: testSourceVersion("metadata", "financial", now)}, Shares: fakeShareSource{facts: []ShareFact{share}, version: testSourceVersion("shares", "financial", now)}, Financials: fakeFinancialSource{facts: financials, version: testSourceVersion("financials", "v1", now)}, Events: noEvents(now), Clock: func() time.Time { return now }}

	batch, err := c.SyncSecurityUniverse(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var metric FinancialMetricSnapshot
	if err := db.First(&metric, "batch_id = ?", batch.BatchID).Error; err != nil {
		t.Fatal(err)
	}
	if !metric.RevenueGrowthAvailable || !metric.RunwayAvailable || metric.LatestQuarterRevenueUSD != 15_000_000 || metric.CashRunwayMonths != 15 {
		t.Fatalf("metric snapshot = %#v", metric)
	}
	var factCount int64
	if err := db.Model(&FinancialFactSnapshot{}).Where("security_id = ?", metric.SecurityID).Count(&factCount).Error; err != nil {
		t.Fatal(err)
	}
	if factCount != int64(len(financials)) {
		t.Fatalf("financial fact snapshots = %d, want %d", factCount, len(financials))
	}
	if !strings.Contains(batch.SourceVersionsJSON, `"source":"financials"`) {
		t.Fatalf("batch source versions missing financials: %s", batch.SourceVersionsJSON)
	}
}

func TestCoordinatorStagesInsiderTransactionSnapshots(t *testing.T) {
	db := openMigratedTestDatabase(t)
	now := time.Date(2026, 6, 23, 9, 0, 0, 0, time.UTC)
	record := SecuritySourceRecord{CIK: "0000004322", Ticker: "BUY", CompanyName: "Buyer Co", SecurityName: "Buyer Co Common Stock", Exchange: "Nasdaq", SIC: 3571, StateOfIncorporation: "DE", LatestAnnualForm: "10-K", RecentForms: []string{"10-K", "10-Q", "4"}, MappingStatus: MappingStatusCurrent}
	share := ShareFact{CIK: record.CIK, Concept: "dei:EntityCommonStockSharesOutstanding", Unit: "shares", Form: "10-Q", Accession: "0000004322-26-000001", Instant: now.AddDate(0, 0, -1), FiledAt: now.Add(-time.Hour), AcceptedAt: now.Add(-time.Hour), Shares: 10_000_000, SourceURL: "https://www.sec.gov/share"}
	buy := InsiderTransaction{CIK: record.CIK, Ticker: record.Ticker, Accession: "0000004322-26-000004", SourceURL: "https://www.sec.gov/form4.xml", OwnerName: "Jane Buyer", OfficerTitle: "Chief Executive Officer", Role: InsiderRoleCEO, TransactionDate: now.AddDate(0, 0, -2), TransactionCode: "P", AcquiredDisposedCode: "A", Shares: 10_000, PricePerShareUSD: 2.5, ValueUSD: 25_000, SharesOwnedAfter: 50_000, SharesOwnedBefore: 40_000, Qualified: true}
	c := Coordinator{DB: db, Metadata: fakeMetadataSource{records: []SecuritySourceRecord{record}, version: testSourceVersion("metadata", "insider", now)}, Shares: fakeShareSource{facts: []ShareFact{share}, version: testSourceVersion("shares", "insider", now)}, Insiders: fakeInsiderSource{transactions: []InsiderTransaction{buy}, version: testSourceVersion("insiders", "v1", now)}, Events: noEvents(now), Clock: func() time.Time { return now }}

	batch, err := c.SyncSecurityUniverse(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var row InsiderTransactionSnapshot
	if err := db.First(&row, "accession = ?", buy.Accession).Error; err != nil {
		t.Fatal(err)
	}
	if !row.Qualified || row.Role != InsiderRoleCEO || row.ValueMicros != 25_000_000_000 || row.SharesOwnedBeforeMicros != 40_000_000_000 {
		t.Fatalf("insider snapshot = %#v", row)
	}
	if !strings.Contains(batch.SourceVersionsJSON, `"source":"insiders"`) {
		t.Fatalf("batch source versions missing insiders: %s", batch.SourceVersionsJSON)
	}
}

func TestCoordinatorManualOverrideChangesBatchIdentity(t *testing.T) {
	db := openMigratedTestDatabase(t)
	now := time.Date(2026, 6, 23, 10, 0, 0, 0, time.UTC)
	record := SecuritySourceRecord{CIK: "0000006543", Ticker: "OVR", CompanyName: "Override Co", SecurityName: "Override Co Common Stock", Exchange: "Nasdaq", SIC: 3571, StateOfIncorporation: "DE", LatestAnnualForm: "10-K", RecentForms: []string{"10-Q"}, MappingStatus: MappingStatusCurrent}
	c := Coordinator{DB: db, Metadata: fakeMetadataSource{records: []SecuritySourceRecord{record}, version: testSourceVersion("metadata", "same", now)}, Shares: fakeShareSource{version: testSourceVersion("shares", "same", now)}, Events: noEvents(now), Clock: func() time.Time { return now }}
	first, err := c.SyncSecurityUniverse(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var shell Security
	if err := db.First(&shell, "cik = ?", record.CIK).Error; err != nil {
		t.Fatal(err)
	}
	mustCreate(t, db, &ManualSecurityOverride{SecurityID: shell.ID, EffectiveStatus: EffectiveStatusExcluded, Reason: "review", SourceURL: "https://example.test/review", Operator: "tester", Active: true})
	second, err := c.SyncSecurityUniverse(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if first.BatchID == second.BatchID {
		t.Fatalf("override did not version batch: %s", first.BatchID)
	}
}

func TestCoordinatorSecurityPublishTransactionHasConstantWritesAtEightThousandRows(t *testing.T) {
	db := openMigratedTestDatabase(t)
	now := time.Date(2026, 6, 23, 10, 0, 0, 0, time.UTC)
	batch := UniverseBatch{BatchID: strings.Repeat("8", 64), Kind: BatchKindSecurity, Status: BatchStatusDraft, EffectiveDate: "2026-06-23", StartedAt: now}
	mustCreate(t, db, &batch)
	rows := make([]ListingIdentitySnapshot, 8000)
	for i := range rows {
		rows[i] = ListingIdentitySnapshot{BatchID: batch.BatchID, SourceKey: fmt.Sprintf("K%04d", i), Ticker: fmt.Sprintf("T%04d", i)}
	}
	if err := db.CreateInBatches(rows, 500).Error; err != nil {
		t.Fatal(err)
	}
	writes := 0
	name := "test:count-publish-writes"
	if err := db.Callback().Create().Before("gorm:create").Register(name, func(*gorm.DB) { writes++ }); err != nil {
		t.Fatal(err)
	}
	if err := db.Callback().Update().Before("gorm:update").Register(name, func(*gorm.DB) { writes++ }); err != nil {
		t.Fatal(err)
	}
	defer db.Callback().Create().Remove(name)
	defer db.Callback().Update().Remove(name)
	c := Coordinator{DB: db, Clock: func() time.Time { return now }}
	published, err := c.publish(context.Background(), batch, len(rows))
	if err != nil || published.Status != BatchStatusPublished {
		t.Fatalf("published=%+v err=%v", published, err)
	}
	if writes != 2 {
		t.Fatalf("publish writes=%d want=2", writes)
	}
}

func TestCoordinatorRejectsInvalidSourceVersionAndPreservesPointer(t *testing.T) {
	db := openMigratedTestDatabase(t)
	old := UniverseBatch{BatchID: strings.Repeat("a", 64), Kind: BatchKindSecurity, Status: BatchStatusPublished}
	if err := db.Create(&old).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&CurrentBatchPointer{Kind: BatchKindSecurity, BatchID: old.BatchID}).Error; err != nil {
		t.Fatal(err)
	}
	c := Coordinator{DB: db, Metadata: fakeMetadataSource{version: SourceVersion{Source: "metadata", Version: "v1"}}, Shares: fakeShareSource{}, Events: noEvents(time.Now()), Clock: time.Now}
	if _, err := c.SyncSecurityUniverse(context.Background()); err == nil {
		t.Fatal("expected invalid version error")
	}
	var pointer CurrentBatchPointer
	if err := db.First(&pointer, "kind = ?", BatchKindSecurity).Error; err != nil {
		t.Fatal(err)
	}
	if pointer.BatchID != old.BatchID {
		t.Fatalf("pointer changed to %s", pointer.BatchID)
	}
}

func TestCoordinatorInactiveProviderDoesNotLoadOrReplacePrescreen(t *testing.T) {
	db := openMigratedTestDatabase(t)
	ny, _ := time.LoadLocation("America/New_York")
	now := time.Date(2026, 6, 23, 10, 0, 0, 0, ny)
	security := UniverseBatch{BatchID: strings.Repeat("c", 64), Kind: BatchKindSecurity, Status: BatchStatusPublished, EffectiveDate: "2026-06-23", SourceVersionsJSON: "[]", ContentSHA256: strings.Repeat("d", 64)}
	if err := db.Create(&security).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&CurrentBatchPointer{Kind: BatchKindSecurity, BatchID: security.BatchID}).Error; err != nil {
		t.Fatal(err)
	}
	old := UniverseBatch{BatchID: strings.Repeat("e", 64), Kind: BatchKindPrescreen, Status: BatchStatusPublished}
	if err := db.Create(&old).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&CurrentBatchPointer{Kind: BatchKindPrescreen, BatchID: old.BatchID}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&ProviderHealth{Provider: "inactive", Status: ProviderStatusValidation}).Error; err != nil {
		t.Fatal(err)
	}
	provider := &fakePriceProvider{name: "inactive"}
	c := Coordinator{DB: db, Prices: provider, Calendar: &stubMarketCalendar{}, Clock: func() time.Time { return now }}
	batch, err := c.SyncMarketPrices(context.Background())
	if err == nil || batch.Status != BatchStatusFailed {
		t.Fatalf("batch=%#v err=%v", batch, err)
	}
	if provider.called != 0 {
		t.Fatalf("provider called %d times", provider.called)
	}
	var pointer CurrentBatchPointer
	if err := db.First(&pointer, "kind = ?", BatchKindPrescreen).Error; err != nil {
		t.Fatal(err)
	}
	if pointer.BatchID != old.BatchID {
		t.Fatalf("pointer changed to %s", pointer.BatchID)
	}
}

func TestCoordinatorChunkFailureRetainsDiagnosticsAndOldPointer(t *testing.T) {
	db := openMigratedTestDatabase(t)
	ny, _ := time.LoadLocation("America/New_York")
	now := time.Date(2026, 6, 23, 10, 0, 0, 0, ny)
	old := UniverseBatch{BatchID: strings.Repeat("b", 64), Kind: BatchKindSecurity, Status: BatchStatusPublished}
	if err := db.Create(&old).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&CurrentBatchPointer{Kind: BatchKindSecurity, BatchID: old.BatchID}).Error; err != nil {
		t.Fatal(err)
	}
	records := make([]SecuritySourceRecord, 1001)
	for i := range records {
		records[i] = SecuritySourceRecord{CIK: fmt.Sprintf("%010d", i+1), Ticker: fmt.Sprintf("T%04d", i), CompanyName: "Chunk Co", SecurityName: "Chunk Co Common Stock", Exchange: "Nasdaq", SIC: 3571, StateOfIncorporation: "DE", LatestAnnualForm: "10-K", RecentForms: []string{"10-Q"}, MappingStatus: MappingStatusCurrent}
	}
	c := Coordinator{DB: db, Metadata: fakeMetadataSource{records: records, version: testSourceVersion("metadata", "chunks", now)}, Shares: fakeShareSource{version: testSourceVersion("shares", "chunks", now)}, Events: noEvents(now), Clock: func() time.Time { return now }, AfterStageChunk: func(kind string, chunk int) error {
		if kind == BatchKindSecurity && chunk == 0 {
			return errors.New("injected chunk failure")
		}
		return nil
	}}
	batch, err := c.SyncSecurityUniverse(context.Background())
	if err == nil || batch.Status != BatchStatusFailed {
		t.Fatalf("batch=%#v err=%v", batch, err)
	}
	var count int64
	if err := db.Model(&ClassificationSnapshot{}).Where("batch_id = ?", batch.BatchID).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count == 0 {
		t.Fatalf("diagnostic rows=%d", count)
	}
	var pointer CurrentBatchPointer
	if err := db.First(&pointer, "kind = ?", BatchKindSecurity).Error; err != nil {
		t.Fatal(err)
	}
	if pointer.BatchID != old.BatchID {
		t.Fatalf("pointer changed to %s", pointer.BatchID)
	}
}

func TestCoordinatorListingSecondChunkFailureRetainsFirstChunk(t *testing.T) {
	db := openMigratedTestDatabase(t)
	now := time.Date(2026, 6, 23, 10, 0, 0, 0, time.UTC)
	old := UniverseBatch{BatchID: strings.Repeat("9", 64), Kind: BatchKindSecurity, Status: BatchStatusPublished}
	mustCreate(t, db, &old)
	mustCreate(t, db, &CurrentBatchPointer{Kind: BatchKindSecurity, BatchID: old.BatchID})
	records := make([]SecuritySourceRecord, 1001)
	for i := range records {
		records[i] = SecuritySourceRecord{SourceKey: fmt.Sprintf("K%04d", i), Ticker: fmt.Sprintf("T%04d", i), MappingStatus: MappingStatusConflict}
	}
	c := Coordinator{DB: db, Metadata: fakeMetadataSource{records: records, version: testSourceVersion("metadata", "listing-chunks", now)}, Shares: fakeShareSource{version: testSourceVersion("shares", "listing-chunks", now)}, Events: noEvents(now), Clock: func() time.Time { return now }, AfterStageChunk: func(kind string, chunk int) error {
		if kind == "security-listings" && chunk == 1 {
			return errors.New("stop second listing chunk")
		}
		return nil
	}}
	batch, err := c.SyncSecurityUniverse(context.Background())
	if err == nil || batch.Status != BatchStatusFailed {
		t.Fatalf("batch=%+v err=%v", batch, err)
	}
	var rows int64
	if err := db.Model(&ListingIdentitySnapshot{}).Where("batch_id = ?", batch.BatchID).Count(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if rows != 1001 {
		t.Fatalf("listing diagnostics=%d want=1001", rows)
	}
	var pointer CurrentBatchPointer
	if err := db.First(&pointer, "kind = ?", BatchKindSecurity).Error; err != nil || pointer.BatchID != old.BatchID {
		t.Fatalf("pointer=%+v err=%v", pointer, err)
	}
}

func TestCoordinatorPublishesUnmappedListingAsConflictWithoutFakeCIK(t *testing.T) {
	db := openMigratedTestDatabase(t)
	now := time.Date(2026, 6, 23, 10, 0, 0, 0, time.UTC)
	record := SecuritySourceRecord{SourceKey: "MISS", Ticker: "MISS", CompanyName: "Missing Mapping", Exchange: "Nasdaq", MappingStatus: MappingStatusConflict, EvidenceJSON: `{"candidate_ciks":[]}`}
	c := Coordinator{DB: db, Metadata: fakeMetadataSource{records: []SecuritySourceRecord{record}, version: testSourceVersion("metadata", "unmapped", now)}, Shares: fakeShareSource{version: testSourceVersion("shares", "unmapped", now)}, Events: noEvents(now), Clock: func() time.Time { return now }}
	batch, err := c.SyncSecurityUniverse(context.Background())
	if err != nil || batch.Status != BatchStatusPublished {
		t.Fatalf("batch=%#v err=%v", batch, err)
	}
	var row ListingIdentitySnapshot
	if err := db.First(&row, "batch_id = ?", batch.BatchID).Error; err != nil {
		t.Fatal(err)
	}
	if row.CIK != "" || row.Included || row.ReasonCode != ReasonMappingConflict {
		t.Fatalf("row=%#v", row)
	}
	var securities int64
	if err := db.Model(&Security{}).Count(&securities).Error; err != nil || securities != 0 {
		t.Fatalf("securities=%d err=%v", securities, err)
	}
}

func TestCoordinatorSecurityCatalogActivationIsAtomicAndRetryable(t *testing.T) {
	db := openMigratedTestDatabase(t)
	now := time.Date(2026, 6, 23, 10, 0, 0, 0, time.UTC)
	old := UniverseBatch{BatchID: strings.Repeat("e", 64), Kind: BatchKindSecurity, Status: BatchStatusPublished}
	if err := db.Create(&old).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&CurrentBatchPointer{Kind: BatchKindSecurity, BatchID: old.BatchID}).Error; err != nil {
		t.Fatal(err)
	}
	record := SecuritySourceRecord{CIK: "0000002468", Ticker: "NEW", CompanyName: "New Co", SecurityName: "New Co Common Stock", Exchange: "Nasdaq", SIC: 3571, StateOfIncorporation: "DE", LatestAnnualForm: "10-K", RecentForms: []string{"10-Q"}, MappingStatus: MappingStatusCurrent}
	c := Coordinator{DB: db, Metadata: fakeMetadataSource{records: []SecuritySourceRecord{record}, version: testSourceVersion("metadata", "draft", now)}, Shares: fakeShareSource{version: testSourceVersion("shares", "draft", now)}, Events: noEvents(now), Clock: func() time.Time { return now }, AfterStageChunk: func(kind string, _ int) error {
		if kind == BatchKindSecurity {
			return errors.New("stop draft")
		}
		return nil
	}}
	failed, err := c.SyncSecurityUniverse(context.Background())
	if err == nil || failed.Status != BatchStatusFailed {
		t.Fatalf("failed=%#v err=%v", failed, err)
	}
	var staged int64
	if err := db.Model(&Security{}).Where("catalog_status = ?", SecurityCatalogStaged).Count(&staged).Error; err != nil {
		t.Fatal(err)
	}
	if staged != 1 {
		t.Fatalf("staged=%d", staged)
	}
	var shell Security
	if err := db.First(&shell, "cik = ?", record.CIK).Error; err != nil {
		t.Fatal(err)
	}
	if shell.CompanyName != "" || shell.SIC != 0 || shell.PublishedAt != nil {
		t.Fatalf("draft shell leaked catalog fields: %#v", shell)
	}
	var listings int64
	if err := db.Model(&Listing{}).Count(&listings).Error; err != nil || listings != 0 {
		t.Fatalf("listings=%d err=%v", listings, err)
	}
	var pointer CurrentBatchPointer
	if err := db.First(&pointer, "kind = ?", BatchKindSecurity).Error; err != nil || pointer.BatchID != old.BatchID {
		t.Fatalf("pointer=%#v err=%v", pointer, err)
	}

	c.Metadata = fakeMetadataSource{records: []SecuritySourceRecord{record}, version: testSourceVersion("metadata", "retry", now)}
	c.Shares = fakeShareSource{version: testSourceVersion("shares", "retry", now)}
	c.AfterStageChunk = nil
	succeeded, err := c.SyncSecurityUniverse(context.Background())
	if err != nil || succeeded.Status != BatchStatusPublished {
		t.Fatalf("succeeded=%#v err=%v", succeeded, err)
	}
	var security Security
	if err := db.First(&security, "cik = ?", record.CIK).Error; err != nil {
		t.Fatal(err)
	}
	if security.CreatedBatchID != failed.BatchID || security.CompanyName != "" || security.PublishedAt != nil {
		t.Fatalf("security=%#v", security)
	}
	var identity SecurityBatchIdentity
	if err := db.First(&identity, "batch_id = ? AND security_id = ?", succeeded.BatchID, security.ID).Error; err != nil || identity.CompanyName != record.CompanyName {
		t.Fatalf("identity=%#v err=%v", identity, err)
	}
	if err := db.Model(&Listing{}).Count(&listings).Error; err != nil || listings != 0 {
		t.Fatalf("listings=%d err=%v", listings, err)
	}
}

func TestCoordinatorFailedDraftDoesNotMutatePublishedSecurity(t *testing.T) {
	db := openMigratedTestDatabase(t)
	now := time.Date(2026, 6, 23, 10, 0, 0, 0, time.UTC)
	record := SecuritySourceRecord{CIK: "0000001357", Ticker: "OLD", CompanyName: "Original Co", SecurityName: "Original Co Common Stock", Exchange: "Nasdaq", SIC: 3571, StateOfIncorporation: "DE", LatestAnnualForm: "10-K", RecentForms: []string{"10-Q"}, MappingStatus: MappingStatusCurrent}
	c := Coordinator{DB: db, Metadata: fakeMetadataSource{records: []SecuritySourceRecord{record}, version: testSourceVersion("metadata", "published", now)}, Shares: fakeShareSource{version: testSourceVersion("shares", "published", now)}, Events: noEvents(now), Clock: func() time.Time { return now }}
	if _, err := c.SyncSecurityUniverse(context.Background()); err != nil {
		t.Fatal(err)
	}
	record.CompanyName, record.SIC, record.Ticker = "Unpublished Change", 9999, "NEW"
	c.Metadata = fakeMetadataSource{records: []SecuritySourceRecord{record}, version: testSourceVersion("metadata", "failed-change", now)}
	c.AfterStageChunk = func(string, int) error { return errors.New("stop draft") }
	if _, err := c.SyncSecurityUniverse(context.Background()); err == nil {
		t.Fatal("failed draft published")
	}
	var security Security
	if err := db.First(&security, "cik = ?", record.CIK).Error; err != nil {
		t.Fatal(err)
	}
	var active SecurityBatchIdentity
	var pointer CurrentBatchPointer
	if err := db.First(&pointer, "kind = ?", BatchKindSecurity).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.First(&active, "batch_id = ? AND security_id = ?", pointer.BatchID, security.ID).Error; err != nil || active.Ticker != "OLD" || active.CompanyName != "Original Co" {
		t.Fatalf("active=%#v err=%v", active, err)
	}
}

func TestCoordinatorFailedTickerChangeCannotAlterPublishedIdentity(t *testing.T) {
	db := openMigratedTestDatabase(t)
	ny, _ := time.LoadLocation("America/New_York")
	now := time.Date(2026, 6, 23, 9, 0, 0, 0, ny)
	record := SecuritySourceRecord{CIK: "0000007777", Ticker: "A", CompanyName: "Atomic", SecurityName: "Atomic Common Stock", Exchange: "Nasdaq", SIC: 3571, StateOfIncorporation: "DE", LatestAnnualForm: "10-K", RecentForms: []string{"10-Q"}, MappingStatus: MappingStatusCurrent}
	c := Coordinator{DB: db, Metadata: fakeMetadataSource{records: []SecuritySourceRecord{record}, version: testSourceVersion("metadata", "a", now)}, Shares: fakeShareSource{version: testSourceVersion("shares", "a", now)}, Events: noEvents(now), Clock: func() time.Time { return now }}
	published, err := c.SyncSecurityUniverse(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	record.Ticker = "B"
	c.Metadata = fakeMetadataSource{records: []SecuritySourceRecord{record}, version: testSourceVersion("metadata", "b", now)}
	c.AfterStageChunk = func(kind string, _ int) error {
		if kind == BatchKindSecurity {
			return errors.New("stop draft")
		}
		return nil
	}
	failed, err := c.SyncSecurityUniverse(context.Background())
	if err == nil || failed.Status != BatchStatusFailed {
		t.Fatalf("failed=%#v err=%v", failed, err)
	}
	listings, err := c.currentIncludedListings(context.Background(), published.BatchID)
	if err != nil {
		t.Fatal(err)
	}
	if len(listings) != 1 || listings[0].Ticker != "A" {
		t.Fatalf("published listings=%#v", listings)
	}
	var identities int64
	if err := db.Model(&SecurityBatchIdentity{}).Where("batch_id = ?", failed.BatchID).Count(&identities).Error; err != nil {
		t.Fatal(err)
	}
	if identities == 0 {
		t.Fatalf("failed identities=%d", identities)
	}
}

func TestCoordinatorCapitalEventsInvalidateSelectedShareAndVersionBatch(t *testing.T) {
	db := openMigratedTestDatabase(t)
	ny, _ := time.LoadLocation("America/New_York")
	now := time.Date(2026, 6, 23, 9, 0, 0, 0, ny)
	record := SecuritySourceRecord{CIK: "0000008888", Ticker: "EVT", CompanyName: "Events", SecurityName: "Events Common Stock", Exchange: "Nasdaq", SIC: 3571, StateOfIncorporation: "DE", LatestAnnualForm: "10-K", RecentForms: []string{"10-Q"}, MappingStatus: MappingStatusCurrent}
	fact := ShareFact{CIK: record.CIK, Concept: "dei:EntityCommonStockSharesOutstanding", Unit: "shares", Form: "10-Q", Accession: "fact", Instant: now.AddDate(0, 0, -10), FiledAt: now.AddDate(0, 0, -9), AcceptedAt: now.AddDate(0, 0, -9), Shares: 1_000_000, SourceURL: "https://www.sec.gov/fact"}
	event := CapitalEvent{CIK: record.CIK, Kind: "financing", Accession: "event", EffectiveAt: now.AddDate(0, 0, -1), AcceptedAt: now.Add(-time.Hour), ChangesShares: true}
	c := Coordinator{DB: db, Metadata: fakeMetadataSource{records: []SecuritySourceRecord{record}, version: testSourceVersion("metadata", "e", now)}, Shares: fakeShareSource{facts: []ShareFact{fact}, version: testSourceVersion("shares", "e", now)}, Events: fakeCapitalEventSource{events: []CapitalEvent{event}, version: testSourceVersion("capital-events", "e", now)}, Clock: func() time.Time { return now }}
	batch, err := c.SyncSecurityUniverse(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var selection BatchShareSelection
	if err := db.First(&selection, "batch_id = ?", batch.BatchID).Error; err != nil {
		t.Fatal(err)
	}
	if selection.ReasonCode != ReasonShareCapitalEvent || selection.ShareSnapshotID == nil {
		t.Fatalf("selection=%#v", selection)
	}
	if !strings.Contains(batch.SourceVersionsJSON, "capital-events") {
		t.Fatalf("versions=%s", batch.SourceVersionsJSON)
	}
}

func TestCoordinatorRecordsEarlyDependencyFailure(t *testing.T) {
	db := openMigratedTestDatabase(t)
	now := time.Date(2026, 6, 23, 9, 0, 0, 0, time.UTC)
	c := Coordinator{DB: db, Clock: func() time.Time { return now }}
	batch, err := c.SyncMarketPrices(context.Background())
	if err == nil || batch.Status != BatchStatusFailed || batch.ErrorMessage == "" {
		t.Fatalf("batch=%#v err=%v", batch, err)
	}
}

func TestCoordinatorBlocksBadCurrentProviderDayAndPersistsHealth(t *testing.T) {
	db := openMigratedTestDatabase(t)
	ny, _ := time.LoadLocation("America/New_York")
	now := time.Date(2026, 6, 23, 9, 0, 0, 0, ny)
	security := Security{CIK: "0000009999", CatalogStatus: SecurityCatalogPublished}
	if err := db.Create(&security).Error; err != nil {
		t.Fatal(err)
	}
	batch := UniverseBatch{BatchID: strings.Repeat("a", 64), Kind: BatchKindSecurity, Status: BatchStatusPublished, EffectiveDate: "2026-06-23", SourceVersionsJSON: "[]", ContentSHA256: strings.Repeat("b", 64)}
	if err := db.Create(&batch).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&CurrentBatchPointer{Kind: BatchKindSecurity, BatchID: batch.BatchID}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&SecurityBatchIdentity{BatchID: batch.BatchID, SecurityID: security.ID, CIK: security.CIK, Ticker: "BAD", ProviderTicker: "BAD", Exchange: "Nasdaq", MappingStatus: MappingStatusCurrent}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&ClassificationSnapshot{BatchID: batch.BatchID, SecurityID: security.ID, Included: true, Status: EffectiveStatusIncluded}).Error; err != nil {
		t.Fatal(err)
	}
	window := make([]providerWindowDay, 0, ProviderActivationTradingDays)
	for day := time.Date(2026, 5, 26, 0, 0, 0, 0, ny); !day.After(time.Date(2026, 6, 22, 0, 0, 0, 0, ny)); day = day.AddDate(0, 0, 1) {
		if day.Weekday() != time.Saturday && day.Weekday() != time.Sunday {
			window = append(window, providerWindowDay{Date: day.Format(time.DateOnly), CoveragePct: 100, Timely: true, ValidationOK: true, GoldReady: true})
		}
	}
	if len(window) != ProviderActivationTradingDays {
		t.Fatalf("window=%d", len(window))
	}
	encoded, _ := json.Marshal(window)
	gold := strings.Repeat("c", 64)
	if err := db.Create(&ProviderHealth{Provider: "bad", Status: ProviderStatusActive, QualifiedTradingDays: len(window), LastTradeDate: "2026-06-22", WindowJSON: string(encoded), GoldEvidenceReady: true, GoldSHA256: gold}).Error; err != nil {
		t.Fatal(err)
	}
	provider := &fakePriceProvider{name: "bad", records: []PriceRecord{{Symbol: "BAD", Source: "bad", TradeDate: now, CloseMicros: 1_000_000, Currency: "USD"}}, result: ProviderResult{Provider: "bad", SourceVersion: "p1", SHA256: strings.Repeat("d", 64), EffectiveDate: now, Records: 1, Expected: 1, CoveragePct: 50, Timely: false}}
	c := Coordinator{DB: db, Prices: provider, Calendar: &stubMarketCalendar{}, Clock: func() time.Time { return now }}
	c.providerDayEvaluator = func(result ProviderResult, _ []PriceRecord, _ time.Time) (ProviderDayResult, error) {
		return ProviderDayResult{TradeDate: result.EffectiveDate, coveragePct: 50, validationOK: false, goldSHA256: gold}, nil
	}
	failed, err := c.SyncMarketPrices(context.Background())
	if err == nil || failed.Status != BatchStatusFailed {
		t.Fatalf("batch=%#v err=%v", failed, err)
	}
	var health ProviderHealth
	if err := db.First(&health, "provider = ?", "bad").Error; err != nil {
		t.Fatal(err)
	}
	if health.FailureStreak != 1 || health.LastTradeDate != "2026-06-23" {
		t.Fatalf("health=%#v", health)
	}
	var run ProviderRun
	if err := db.First(&run, "batch_id = ?", failed.BatchID).Error; err != nil {
		t.Fatal(err)
	}
	if run.GoldSHA256 != gold || run.SourceVersion != "p1" {
		t.Fatalf("run=%#v", run)
	}
}

func TestSecurityInputNormalizationIsPermutationInvariantAndRejectsConflicts(t *testing.T) {
	now := time.Date(2026, 6, 23, 0, 0, 0, 0, time.UTC)
	a := SecuritySourceRecord{CIK: "0000000001", Ticker: "A", CompanyName: "A"}
	b := SecuritySourceRecord{CIK: "0000000002", Ticker: "B", CompanyName: "B"}
	one, err := normalizeMetadataRecords([]SecuritySourceRecord{a, b, a})
	if err != nil {
		t.Fatal(err)
	}
	two, err := normalizeMetadataRecords([]SecuritySourceRecord{b, a})
	if err != nil {
		t.Fatal(err)
	}
	h1, err := hashSecurityInputs(one, []ShareFact{{CIK: a.CIK, Accession: "a", Instant: now}}, nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	h2, err := hashSecurityInputs(two, []ShareFact{{CIK: a.CIK, Accession: "a", Instant: now}}, nil, nil, nil, nil)
	if err != nil || h1 != h2 {
		t.Fatalf("hashes=%s/%s err=%v", h1, h2, err)
	}
	conflict := a
	conflict.CompanyName = "changed"
	if _, err := normalizeMetadataRecords([]SecuritySourceRecord{a, conflict}); err == nil {
		t.Fatal("expected duplicate conflict")
	}
}

func TestPersistPricesChecksEntireDuplicateGroupDeterministically(t *testing.T) {
	db := openMigratedTestDatabase(t)
	ny, _ := time.LoadLocation("America/New_York")
	now := time.Date(2026, 6, 23, 9, 0, 0, 0, ny)
	records := []PriceRecord{
		{Symbol: "DUP", Source: "p", TradeDate: now, CloseMicros: 2_000_000, Currency: "USD"},
		{Symbol: "DUP", Source: "p", TradeDate: now, CloseMicros: 1_000_000, Currency: "USD"},
		{Symbol: "DUP", Source: "p", TradeDate: now, CloseMicros: 2_000_000, Currency: "USD"},
	}
	c := Coordinator{DB: db, Calendar: &stubMarketCalendar{}, Clock: func() time.Time { return now }}
	if err := c.persistPrices(context.Background(), records, "v1"); err != nil {
		t.Fatal(err)
	}
	var rows []PriceSnapshot
	if err := db.Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].QualityStatus != QualityStatusConflict || rows[0].CloseMicros != 1_000_000 {
		t.Fatalf("rows=%#v", rows)
	}
}
