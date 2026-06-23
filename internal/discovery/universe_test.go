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

func TestCoordinatorPublishesPrescreenWithExactEvidence(t *testing.T) {
	db := openMigratedTestDatabase(t)
	ny, _ := time.LoadLocation("America/New_York")
	now := time.Date(2026, 6, 23, 9, 0, 0, 0, ny)
	record := SecuritySourceRecord{CIK: "0000005678", Ticker: "CAP", CompanyName: "Cap Co", SecurityName: "Cap Co Common Stock", Exchange: "Nasdaq", SIC: 3571, StateOfIncorporation: "DE", LatestAnnualForm: "10-K", RecentForms: []string{"10-Q"}, MappingStatus: MappingStatusCurrent}
	fact := ShareFact{CIK: record.CIK, Concept: "dei:EntityCommonStockSharesOutstanding", Unit: "shares", Form: "10-Q", Accession: "0000005678-26-000001", Instant: now.AddDate(0, 0, -1), FiledAt: now.Add(-time.Hour), AcceptedAt: now.Add(-time.Hour), Shares: 10_000_000, SourceURL: "https://www.sec.gov/fact"}
	calendar := &stubMarketCalendar{}
	c := Coordinator{DB: db, Metadata: fakeMetadataSource{records: []SecuritySourceRecord{record}, version: testSourceVersion("metadata", "v1", now)}, Shares: fakeShareSource{facts: []ShareFact{fact}, version: testSourceVersion("shares", "v1", now)}, Clock: func() time.Time { return now }}
	securityBatch, err := c.SyncSecurityUniverse(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	gold := sha256.Sum256(frozenMarketGoldCSV)
	window := make([]providerWindowDay, 0, ProviderActivationTradingDays)
	day := time.Date(2026, 5, 20, 0, 0, 0, 0, ny)
	for len(window) < ProviderActivationTradingDays {
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
	c := Coordinator{DB: db, Metadata: fakeMetadataSource{records: []SecuritySourceRecord{record}, version: testSourceVersion("metadata", "v1", now)}, Shares: fakeShareSource{facts: []ShareFact{fact}, version: testSourceVersion("shares", "v1", now)}, Clock: func() time.Time { return now }}

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

func TestCoordinatorRejectsInvalidSourceVersionAndPreservesPointer(t *testing.T) {
	db := openMigratedTestDatabase(t)
	old := UniverseBatch{BatchID: strings.Repeat("a", 64), Kind: BatchKindSecurity, Status: BatchStatusPublished}
	if err := db.Create(&old).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&CurrentBatchPointer{Kind: BatchKindSecurity, BatchID: old.BatchID}).Error; err != nil {
		t.Fatal(err)
	}
	c := Coordinator{DB: db, Metadata: fakeMetadataSource{version: SourceVersion{Source: "metadata", Version: "v1"}}, Shares: fakeShareSource{}, Clock: time.Now}
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
	if err == nil || batch.Status != BatchStatusPartial {
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
	c := Coordinator{DB: db, Metadata: fakeMetadataSource{records: records, version: testSourceVersion("metadata", "chunks", now)}, Shares: fakeShareSource{version: testSourceVersion("shares", "chunks", now)}, Clock: func() time.Time { return now }, AfterStageChunk: func(kind string, chunk int) error {
		if kind == BatchKindSecurity && chunk == 0 {
			return errors.New("injected chunk failure")
		}
		return nil
	}}
	batch, err := c.SyncSecurityUniverse(context.Background())
	if err == nil || batch.Status != BatchStatusPartial {
		t.Fatalf("batch=%#v err=%v", batch, err)
	}
	var count int64
	if err := db.Model(&ClassificationSnapshot{}).Where("batch_id = ?", batch.BatchID).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 1000 {
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
