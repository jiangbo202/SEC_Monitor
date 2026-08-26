package discovery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
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

type fakeCoveredInsiderSource struct {
	fakeInsiderSource
	coverage []InsiderCoverage
}

func (f fakeCoveredInsiderSource) LoadInsiderTransactionsWithCoverage(ctx context.Context, allowed map[string]struct{}, asOf time.Time) ([]InsiderTransaction, []InsiderCoverage, SourceVersion, error) {
	transactions, version, err := f.fakeInsiderSource.LoadInsiderTransactions(ctx, allowed, asOf)
	return transactions, f.coverage, version, err
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
	name          string
	records       []PriceRecord
	result        ProviderResult
	err           error
	called        int
	datedCalled   int
	requestedDate string
	expected      []Listing
}

func (f *fakePriceProvider) ProviderName() string { return f.name }
func (f *fakePriceProvider) Load(context.Context, []Listing) ([]PriceRecord, ProviderResult, error) {
	f.called++
	return f.records, f.result, f.err
}
func (f *fakePriceProvider) LoadForDate(_ context.Context, expected []Listing, effectiveDate string) ([]PriceRecord, ProviderResult, error) {
	f.datedCalled++
	f.requestedDate = effectiveDate
	f.expected = append([]Listing(nil), expected...)
	return f.records, f.result, f.err
}

func TestCandidateInsiderAllowlistUsesPriorABCandidates(t *testing.T) {
	db := openMigratedTestDatabase(t)
	now := time.Date(2026, 7, 19, 0, 0, 0, 0, time.UTC)
	batch := UniverseBatch{BatchID: strings.Repeat("f", 64), Kind: BatchKindPrescreen, Status: BatchStatusPublished, EffectiveDate: now.Format(time.DateOnly), SourceVersionsJSON: "[]", ContentSHA256: strings.Repeat("e", 64), StartedAt: now, CompletedAt: &now}
	if err := db.Create(&batch).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&CurrentBatchPointer{Kind: BatchKindPrescreen, BatchID: batch.BatchID}).Error; err != nil {
		t.Fatal(err)
	}
	securities := []Security{{CIK: "0000000001", CompanyName: "A"}, {CIK: "0000000002", CompanyName: "B"}, {CIK: "0000000003", CompanyName: "C"}}
	if err := db.Create(&securities).Error; err != nil {
		t.Fatal(err)
	}
	scores := []CandidateScoreSnapshot{{BatchID: batch.BatchID, SecurityID: securities[0].ID, Ticker: "AAA", Grade: CandidateGradeA}, {BatchID: batch.BatchID, SecurityID: securities[1].ID, Ticker: "BBB", Grade: CandidateGradeB}, {BatchID: batch.BatchID, SecurityID: securities[2].ID, Ticker: "CCC", Grade: CandidateGradeExcluded}}
	if err := db.Create(&scores).Error; err != nil {
		t.Fatal(err)
	}
	allowed := map[string]struct{}{"0000000001": {}, "0000000002": {}, "0000000003": {}, "0000000004": {}}
	got, err := (&Coordinator{DB: db}).candidateInsiderAllowlist(context.Background(), allowed, nil, DefaultSmallCapPolicy(), now)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("allowlist = %#v", got)
	}
	for _, cik := range []string{"0000000001", "0000000002"} {
		if _, ok := got[cik]; !ok {
			t.Fatalf("missing selected CIK %s in %#v", cik, got)
		}
	}
}

func TestCandidateInsiderAllowlistUsesFinancialGrowthOnBootstrap(t *testing.T) {
	db := openMigratedTestDatabase(t)
	now := time.Date(2026, 7, 19, 0, 0, 0, 0, time.UTC)
	allowed := map[string]struct{}{"0000000001": {}, "0000000002": {}, "0000000003": {}}
	facts := []FinancialFact{
		{CIK: "0000000001", Metric: FinancialMetricRevenue, PeriodStart: time.Date(2025, 4, 1, 0, 0, 0, 0, time.UTC), PeriodEnd: time.Date(2025, 6, 30, 0, 0, 0, 0, time.UTC), FiledAt: time.Date(2025, 8, 1, 0, 0, 0, 0, time.UTC), AmountMicros: 100_000_000_000_000},
		{CIK: "0000000001", Metric: FinancialMetricRevenue, PeriodStart: time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC), PeriodEnd: time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC), FiledAt: now.AddDate(0, 0, -1), AmountMicros: 130_000_000_000_000},
		{CIK: "0000000002", Metric: FinancialMetricRevenue, PeriodStart: time.Date(2025, 4, 1, 0, 0, 0, 0, time.UTC), PeriodEnd: time.Date(2025, 6, 30, 0, 0, 0, 0, time.UTC), FiledAt: time.Date(2025, 8, 1, 0, 0, 0, 0, time.UTC), AmountMicros: 100_000_000_000_000},
		{CIK: "0000000002", Metric: FinancialMetricRevenue, PeriodStart: time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC), PeriodEnd: time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC), FiledAt: now.AddDate(0, 0, -1), AmountMicros: 110_000_000_000_000},
	}
	got, err := (&Coordinator{DB: db}).candidateInsiderAllowlist(context.Background(), allowed, facts, DefaultSmallCapPolicy(), now)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("allowlist = %#v", got)
	}
	if _, ok := got["0000000001"]; !ok {
		t.Fatalf("growth-qualified issuer missing from %#v", got)
	}
}

func TestCoordinatorResearchLocalPriceFallbackUsesOnlyPreviousTradingDay(t *testing.T) {
	db := openMigratedTestDatabase(t)
	ny := mustNY(t)
	previous := time.Date(2026, 7, 13, 0, 0, 0, 0, ny)
	effective := time.Date(2026, 7, 14, 0, 0, 0, 0, ny)
	if err := db.Create(&[]PriceSnapshot{
		{Source: "tiingo", SourceVersion: "prior", Symbol: "AAA", TradeDate: previous, CloseMicros: 1_000_000, Volume: 100, Currency: "USD", QualityStatus: QualityStatusValid},
		{Source: "tiingo", SourceVersion: "old", Symbol: "STALE", TradeDate: previous.AddDate(0, 0, -3), CloseMicros: 1_000_000, Volume: 100, Currency: "USD", QualityStatus: QualityStatusValid},
	}).Error; err != nil {
		t.Fatal(err)
	}
	c := Coordinator{DB: db, Calendar: &stubMarketCalendar{}, ResearchMode: true, Clock: func() time.Time { return time.Date(2026, 7, 15, 11, 0, 0, 0, ny) }}
	expected := []Listing{{Ticker: "AAA"}, {Ticker: "LIVE"}, {Ticker: "STALE"}}
	live := []PriceRecord{{Symbol: "LIVE", TradeDate: effective, CloseMicros: 2_000_000, Volume: 200, Currency: "USD", Source: "tiingo"}}
	records, result, err := c.mergeResearchLocalPriceFallback(context.Background(), "chain", "2026-07-14", expected, live, ProviderResult{Provider: "chain", SourceVersion: "chain:live"}, nil, c.Clock())
	if err != nil {
		t.Fatalf("mergeResearchLocalPriceFallback() error = %v", err)
	}
	if len(records) != 2 || result.Provider != "chain" || result.Expected != 3 || result.Records != 2 || result.CoveragePct < 66 || result.CoveragePct > 67 {
		t.Fatalf("records=%#v result=%#v", records, result)
	}
	bySymbol := map[string]PriceRecord{}
	for _, record := range records {
		bySymbol[record.Symbol] = record
	}
	if got := bySymbol["AAA"]; got.Source != PriceSourceLocalCache || !got.TradeDate.Equal(previous) {
		t.Fatalf("AAA fallback = %#v", got)
	}
	if _, found := bySymbol["STALE"]; found {
		t.Fatalf("stale local quote must not be reused: %#v", bySymbol["STALE"])
	}
}

func TestCoordinatorResearchLocalPriceFallbackKeepsLiveRecordsWhenFallbackContextExpires(t *testing.T) {
	db := openMigratedTestDatabase(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	live := []PriceRecord{{Symbol: "LIVE", CloseMicros: 2_000_000, Volume: 200, Currency: "USD", Source: "tiingo"}}
	c := Coordinator{DB: db, Calendar: contextAwareFailingCalendar{}, ResearchMode: true}
	records, result, err := c.mergeResearchLocalPriceFallback(ctx, "chain", "2026-07-14", []Listing{{Ticker: "LIVE"}}, live, ProviderResult{Provider: "chain", SourceVersion: "chain:live"}, nil, time.Now())
	if err != nil {
		t.Fatalf("mergeResearchLocalPriceFallback() error = %v", err)
	}
	if !reflect.DeepEqual(records, live) || result.Provider != "chain" {
		t.Fatalf("records=%#v result=%#v", records, result)
	}
}

type contextAwareFailingCalendar struct{}

func (contextAwareFailingCalendar) IsTradingDate(ctx context.Context, _ string) (bool, error) {
	return false, ctx.Err()
}

func (contextAwareFailingCalendar) IsTradingDay(ctx context.Context, _ time.Time) (bool, error) {
	return false, ctx.Err()
}

func TestMarketPriceRunPlanUsesLatestCompletedNewYorkTradingSession(t *testing.T) {
	ny := mustNY(t)
	calendar := &stubMarketCalendar{holidays: map[string]bool{"2026-07-03": true}}
	tests := []struct {
		name          string
		now           time.Time
		wantDate      string
		wantLocalOnly bool
	}{
		{
			name:          "before regular close reuses previous completed close",
			now:           time.Date(2026, 7, 20, 15, 30, 0, 0, ny),
			wantDate:      "2026-07-17",
			wantLocalOnly: true,
		},
		{
			name:          "after provider finalization targets current close",
			now:           time.Date(2026, 7, 20, 16, 15, 0, 0, ny),
			wantDate:      "2026-07-20",
			wantLocalOnly: false,
		},
		{
			name:          "weekend reuses Friday close",
			now:           time.Date(2026, 7, 18, 11, 0, 0, 0, ny),
			wantDate:      "2026-07-17",
			wantLocalOnly: true,
		},
		{
			name:          "exchange holiday reuses last trading close",
			now:           time.Date(2026, 7, 3, 17, 0, 0, 0, ny),
			wantDate:      "2026-07-02",
			wantLocalOnly: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			date, localOnly, err := marketPriceRunPlan(context.Background(), calendar, test.now)
			if err != nil {
				t.Fatalf("marketPriceRunPlan() error = %v", err)
			}
			if date != test.wantDate || localOnly != test.wantLocalOnly {
				t.Fatalf("marketPriceRunPlan() = (%q, %t), want (%q, %t)", date, localOnly, test.wantDate, test.wantLocalOnly)
			}
		})
	}
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
	financials := []FinancialFact{
		financialDuration(FinancialMetricRevenue, "2025-01-01", "2025-03-31", 10_000_000),
		financialDuration(FinancialMetricRevenue, "2026-01-01", "2026-03-31", 15_000_000),
		financialInstant(FinancialMetricCash, "2026-03-31", 30_000_000),
		financialDuration(FinancialMetricOperatingCashFlow, "2025-04-01", "2025-06-30", -3_000_000),
		financialDuration(FinancialMetricOperatingCashFlow, "2025-07-01", "2025-09-30", -3_000_000),
		financialDuration(FinancialMetricOperatingCashFlow, "2025-10-01", "2025-12-31", -3_000_000),
		financialDuration(FinancialMetricOperatingCashFlow, "2026-01-01", "2026-03-31", -3_000_000),
	}
	for i := range financials {
		financials[i].CIK = record.CIK
		financials[i].FiledAt = now.Add(-time.Hour)
		financials[i].AcceptedAt = now.Add(-time.Hour)
		financials[i].Concept = "us-gaap:" + financials[i].Metric
		financials[i].SourceURL = "https://www.sec.gov/financial"
	}
	insider := InsiderTransaction{CIK: record.CIK, Ticker: record.Ticker, Accession: "0000005678-26-000004", SourceURL: "https://www.sec.gov/form4.xml", OwnerName: "Jane Buyer", OfficerTitle: "Chief Executive Officer", Role: InsiderRoleCEO, TransactionDate: now.AddDate(0, 0, -2), TransactionCode: "P", AcquiredDisposedCode: "A", Shares: 10_000, PricePerShareUSD: 2.5, ValueUSD: 25_000, Qualified: true}
	calendar := &stubMarketCalendar{}
	c := Coordinator{
		DB:         db,
		Metadata:   fakeMetadataSource{records: []SecuritySourceRecord{record}, version: testSourceVersion("metadata", "v1", now)},
		Shares:     fakeShareSource{facts: []ShareFact{fact}, version: testSourceVersion("shares", "v1", now)},
		Financials: fakeFinancialSource{facts: financials, version: testSourceVersion("financials", "v1", now)},
		Insiders:   fakeInsiderSource{transactions: []InsiderTransaction{insider}, version: testSourceVersion("insiders", "v1", now)},
		Events:     noEvents(now),
		Clock:      func() time.Time { return now },
	}
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
	if provider.datedCalled != 1 || provider.requestedDate != securityBatch.EffectiveDate || provider.called != 0 {
		t.Fatalf("provider dated call = %d date %q fallback calls %d", provider.datedCalled, provider.requestedDate, provider.called)
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
	var score CandidateScoreSnapshot
	if err := db.First(&score, "batch_id = ? AND security_id = ?", batch.BatchID, snapshot.SecurityID).Error; err != nil {
		t.Fatal(err)
	}
	if score.Grade != CandidateGradeA || !score.EligibleA || score.TotalScore != 86 || score.RevenueGrowthScore != 30 || score.CashRunwayScore != 20 || score.InsiderScore != 20 || score.DilutionRiskScore != 10 || score.SectorScore != 6 {
		t.Fatalf("score=%#v", score)
	}
	if securityBatch.BatchID == batch.BatchID {
		t.Fatal("security and prescreen batches must differ")
	}
}

func testSourceVersion(source, version string, day time.Time) SourceVersion {
	sum := sha256.Sum256([]byte(source + ":" + version))
	return SourceVersion{Source: source, Version: version, SHA256: hex.EncodeToString(sum[:]), EffectiveAt: day}
}

func TestAlignSourceVersionsToBatchDateAllowsFutureSourceTimestamp(t *testing.T) {
	shanghai, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	sourceAt := time.Date(2026, 7, 4, 0, 53, 0, 0, shanghai)
	sourceVersion := testSourceVersion("metadata:composite", "v1", sourceAt)
	aligned, err := alignSourceVersionsToBatchDate("2026-07-03", []SourceVersion{sourceVersion})
	if err != nil {
		t.Fatalf("alignSourceVersionsToBatchDate: %v", err)
	}
	if len(aligned) != 1 {
		t.Fatalf("aligned length = %d, want 1", len(aligned))
	}
	if aligned[0].Source != sourceVersion.Source || aligned[0].Version != sourceVersion.Version || aligned[0].SHA256 != sourceVersion.SHA256 {
		t.Fatalf("aligned changed source identity: %+v", aligned[0])
	}
	if got := aligned[0].EffectiveAt.Format(time.DateOnly); got != "2026-07-03" {
		t.Fatalf("aligned effective date = %s, want 2026-07-03", got)
	}
	if _, err := normalizeSourceVersions("2026-07-03", aligned...); err != nil {
		t.Fatalf("normalizeSourceVersions after alignment: %v", err)
	}
}

func TestPersistCandidateScoreSnapshotsUsesSecuritySICForSectorScore(t *testing.T) {
	db := openMigratedTestDatabase(t)
	now := time.Date(2026, 6, 30, 12, 0, 0, 0, time.UTC)
	securityBatch := UniverseBatch{BatchID: "security-sector", Kind: BatchKindSecurity, Status: BatchStatusPublished, EffectiveDate: "2026-06-30", StartedAt: now}
	marketBatch := UniverseBatch{BatchID: "market-sector", Kind: BatchKindPrescreen, Status: BatchStatusPublished, EffectiveDate: "2026-06-30", StartedAt: now}
	if err := db.Create(&securityBatch).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&marketBatch).Error; err != nil {
		t.Fatal(err)
	}
	security := Security{CIK: "0000099999", CompanyName: "Software Co", SIC: 0, CatalogStatus: SecurityCatalogPublished}
	if err := db.Create(&security).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&SecurityBatchIdentity{BatchID: securityBatch.BatchID, SecurityID: security.ID, CIK: security.CIK, Ticker: "SOFT", CompanyName: "Software Co", SIC: 7372, MappingStatus: MappingStatusCurrent, CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	metric := FinancialMetricSnapshot{
		BatchID: securityBatch.BatchID, SecurityID: security.ID,
		RevenueGrowthAvailable: true, QuarterlyRevenueYoYPct: 35,
		RunwayAvailable: true, CashRunwayMonths: 18,
		GrossMarginAvailable: true, GrossMarginPct: 50, CreatedAt: now,
	}
	if err := db.Create(&metric).Error; err != nil {
		t.Fatal(err)
	}
	universeRows := []UniverseSnapshot{{
		BatchID: marketBatch.BatchID, SecurityID: security.ID, Ticker: "SOFT",
		MarketCapUSD: 650_000_000, QualityStatus: QualityStatusValid, CreatedAt: now,
	}}

	c := Coordinator{DB: db}
	if err := c.persistCandidateScoreSnapshots(context.Background(), securityBatch.BatchID, marketBatch.BatchID, universeRows, now); err != nil {
		t.Fatal(err)
	}

	var score CandidateScoreSnapshot
	if err := db.First(&score, "batch_id = ? AND security_id = ?", marketBatch.BatchID, security.ID).Error; err != nil {
		t.Fatal(err)
	}
	if score.SectorScore != 9 || score.GrossMarginScore != 10 || score.TotalScore != 69 || score.Grade != CandidateGradeB || !score.EligibleB {
		t.Fatalf("score = %#v", score)
	}
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
	third, err := c.SyncSecurityUniverse(context.Background())
	if err != nil {
		t.Fatalf("same source versions with changed content should create new batch: %v", err)
	}
	if third.BatchID == first.BatchID || third.Status != BatchStatusPublished {
		t.Fatalf("third batch = %#v, first = %#v", third, first)
	}
	if err := db.First(&pointer, "kind = ?", BatchKindSecurity).Error; err != nil {
		t.Fatal(err)
	}
	if pointer.BatchID != third.BatchID {
		t.Fatalf("pointer = %s, want %s", pointer.BatchID, third.BatchID)
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
		financialDuration(FinancialMetricGrossProfit, "2026-01-01", "2026-03-31", 9_000_000),
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
	if !metric.RevenueGrowthAvailable || !metric.RunwayAvailable || !metric.GrossMarginAvailable || metric.LatestQuarterRevenueUSD != 15_000_000 || metric.CashRunwayMonths != 15 || metric.GrossMarginPct != 60 {
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
	c := Coordinator{DB: db, Metadata: fakeMetadataSource{records: []SecuritySourceRecord{record}, version: testSourceVersion("metadata", "insider", now)}, Shares: fakeShareSource{facts: []ShareFact{share}, version: testSourceVersion("shares", "insider", now)}, Insiders: fakeCoveredInsiderSource{fakeInsiderSource: fakeInsiderSource{transactions: []InsiderTransaction{buy}, version: testSourceVersion("insiders", "v1+"+InsiderCoverageVersion, now)}, coverage: []InsiderCoverage{{CIK: record.CIK, Status: InsiderCoverageCoveredTransactions, EligibleFilings: 1, DownloadedDocuments: 1, ParsedDocuments: 1, TransactionCount: 1, CheckedAt: now}}}, Events: noEvents(now), Clock: func() time.Time { return now }}

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
	var coverage InsiderCoverageSnapshot
	if err := db.First(&coverage, "batch_id = ? AND security_id = ?", batch.BatchID, row.SecurityID).Error; err != nil {
		t.Fatal(err)
	}
	if coverage.Status != InsiderCoverageCoveredTransactions || coverage.TransactionCount != 1 || coverage.ParsedDocuments != 1 {
		t.Fatalf("coverage snapshot = %#v", coverage)
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

func TestCoordinatorMissingProviderHealthRunsValidationWithoutPublishing(t *testing.T) {
	db := openMigratedTestDatabase(t)
	ny, _ := time.LoadLocation("America/New_York")
	now := time.Date(2026, 6, 23, 17, 0, 0, 0, ny)
	securityRow := Security{CIK: "0000009999", CatalogStatus: SecurityCatalogPublished}
	if err := db.Create(&securityRow).Error; err != nil {
		t.Fatal(err)
	}
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
	if err := db.Create(&SecurityBatchIdentity{BatchID: security.BatchID, SecurityID: securityRow.ID, CIK: securityRow.CIK, Ticker: "VAL", ProviderTicker: "VAL", Exchange: "Nasdaq", MappingStatus: MappingStatusCurrent}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&ClassificationSnapshot{BatchID: security.BatchID, SecurityID: securityRow.ID, Included: true, Status: EffectiveStatusIncluded}).Error; err != nil {
		t.Fatal(err)
	}
	provider := &fakePriceProvider{
		name:    "new-provider",
		records: []PriceRecord{{Symbol: "VAL", Source: "new-provider", TradeDate: now, CloseMicros: 1_000_000, Currency: "USD"}},
		result:  ProviderResult{Provider: "new-provider", SourceVersion: "pv1", SHA256: strings.Repeat("a", 64), EffectiveDate: now, Records: 1, Expected: 1, CoveragePct: 100, Timely: true},
	}
	c := Coordinator{DB: db, Prices: provider, Calendar: &stubMarketCalendar{}, Clock: func() time.Time { return now }}
	c.providerDayEvaluator = func(result ProviderResult, _ []PriceRecord, _ time.Time) (ProviderDayResult, error) {
		return ProviderDayResult{TradeDate: result.EffectiveDate, coveragePct: 100, timely: true, validationOK: true, goldSHA256: strings.Repeat("b", 64)}, nil
	}
	batch, err := c.SyncMarketPrices(context.Background())
	if err == nil || batch.Status != BatchStatusFailed {
		t.Fatalf("batch=%#v err=%v", batch, err)
	}
	if provider.datedCalled != 1 || provider.requestedDate != security.EffectiveDate || provider.called != 0 {
		t.Fatalf("provider dated call = %d date %q fallback calls %d", provider.datedCalled, provider.requestedDate, provider.called)
	}
	var health ProviderHealth
	if err := db.First(&health, "provider = ?", "new-provider").Error; err != nil {
		t.Fatal(err)
	}
	if health.Status != ProviderStatusValidation || health.QualifiedTradingDays != 1 || health.LastTradeDate != "2026-06-23" {
		t.Fatalf("health=%#v", health)
	}
	var pointer CurrentBatchPointer
	if err := db.First(&pointer, "kind = ?", BatchKindPrescreen).Error; err != nil {
		t.Fatal(err)
	}
	if pointer.BatchID != old.BatchID {
		t.Fatalf("pointer changed to %s", pointer.BatchID)
	}
}

func TestCoordinatorPrefiltersTiingoPriceRequestsWithFinancialSECSignals(t *testing.T) {
	db := openMigratedTestDatabase(t)
	ny, _ := time.LoadLocation("America/New_York")
	now := time.Date(2026, 6, 23, 17, 0, 0, 0, ny)
	securityBatch := seedSecurityBatchForMarketTest(t, db, now, []marketSeedSecurity{
		{CIK: "0000000001", Ticker: "PASS", Growth: 35, Runway: 9, Shares: 10_000_000},
		{CIK: "0000000002", Ticker: "LOW", Growth: 5, Runway: 24, Shares: 10_000_000},
		{CIK: "0000000003", Ticker: "BLOCK", Growth: 55, Runway: 24, Shares: 10_000_000, BlocksB: true},
	})
	priceSHA := sha256.Sum256([]byte("research-prefilter-prices"))
	provider := &fakePriceProvider{
		name:    "tiingo",
		records: []PriceRecord{{Symbol: "PASS", Source: "tiingo", TradeDate: now, CloseMicros: 5_000_000, Currency: "USD"}},
		result:  ProviderResult{Provider: "tiingo", SourceVersion: "tiingo:test", SHA256: hex.EncodeToString(priceSHA[:]), EffectiveDate: now, Records: 1, Expected: 1, CoveragePct: 100, Timely: true},
	}
	c := Coordinator{DB: db, Prices: provider, Calendar: &stubMarketCalendar{}, Clock: func() time.Time { return now }, ResearchMode: true}
	c.providerDayEvaluator = func(result ProviderResult, _ []PriceRecord, _ time.Time) (ProviderDayResult, error) {
		return ProviderDayResult{TradeDate: result.EffectiveDate, coveragePct: 100, timely: true, validationOK: true, goldSHA256: strings.Repeat("b", 64)}, nil
	}
	batch, err := c.SyncMarketPrices(context.Background())
	if err != nil {
		t.Fatalf("SyncMarketPrices() error = %v", err)
	}
	if batch.Status != BatchStatusPublished || securityBatch.BatchID == batch.BatchID {
		t.Fatalf("batch=%#v", batch)
	}
	if got := tickersFromListings(provider.expected); !reflect.DeepEqual(got, []string{"PASS"}) {
		t.Fatalf("price requests = %#v, want PASS only", got)
	}
}

func TestCoordinatorResearchModePublishesValidationProviderBatch(t *testing.T) {
	db := openMigratedTestDatabase(t)
	ny, _ := time.LoadLocation("America/New_York")
	now := time.Date(2026, 6, 23, 17, 0, 0, 0, ny)
	seedSecurityBatchForMarketTest(t, db, now, []marketSeedSecurity{{CIK: "0000000011", Ticker: "RSCH", Growth: 45, Runway: 18, Shares: 10_000_000}})
	priceSHA := sha256.Sum256([]byte("research-mode-prices"))
	provider := &fakePriceProvider{
		name:    "tiingo",
		records: []PriceRecord{{Symbol: "RSCH", Source: "tiingo", TradeDate: now, CloseMicros: 5_000_000, Currency: "USD"}},
		result:  ProviderResult{Provider: "tiingo", SourceVersion: "tiingo:test", SHA256: hex.EncodeToString(priceSHA[:]), EffectiveDate: now, Records: 1, Expected: 1, CoveragePct: 100, Timely: true},
	}
	c := Coordinator{DB: db, Prices: provider, Calendar: &stubMarketCalendar{}, Clock: func() time.Time { return now }, ResearchMode: true}
	c.providerDayEvaluator = func(result ProviderResult, _ []PriceRecord, _ time.Time) (ProviderDayResult, error) {
		return ProviderDayResult{TradeDate: result.EffectiveDate, coveragePct: 100, timely: true, validationOK: true, goldReady: false, goldSHA256: strings.Repeat("c", 64)}, nil
	}
	batch, err := c.SyncMarketPrices(context.Background())
	if err != nil {
		t.Fatalf("SyncMarketPrices() error = %v", err)
	}
	if batch.Status != BatchStatusPublished {
		t.Fatalf("batch=%#v", batch)
	}
	var run ProviderRun
	if err := db.First(&run, "batch_id = ?", batch.BatchID).Error; err != nil {
		t.Fatal(err)
	}
	if run.Status != ProviderStatusValidation {
		t.Fatalf("provider run status = %q", run.Status)
	}
	if err := hydrateProviderRunAttempts(&run); err != nil {
		t.Fatal(err)
	}
	if run.FallbackUsed || len(run.Attempts) != 1 || run.Attempts[0].Provider != "tiingo" || run.Attempts[0].Status != "success" {
		t.Fatalf("provider attempts = %#v fallback=%v", run.Attempts, run.FallbackUsed)
	}
}

func TestCoordinatorResearchModeRejectsLowCoverageWithoutPublishing(t *testing.T) {
	db := openMigratedTestDatabase(t)
	ny, _ := time.LoadLocation("America/New_York")
	now := time.Date(2026, 6, 23, 17, 0, 0, 0, ny)
	seedSecurityBatchForMarketTest(t, db, now, []marketSeedSecurity{
		{CIK: "0000000011", Ticker: "LOW1", Growth: 45, Runway: 18, Shares: 10_000_000},
		{CIK: "0000000012", Ticker: "LOW2", Growth: 45, Runway: 18, Shares: 10_000_000},
	})
	old := UniverseBatch{BatchID: strings.Repeat("f", 64), Kind: BatchKindPrescreen, Status: BatchStatusPublished, EffectiveDate: "2026-06-20", StartedAt: now.Add(-time.Hour)}
	if err := db.Create(&old).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&CurrentBatchPointer{Kind: BatchKindPrescreen, BatchID: old.BatchID}).Error; err != nil {
		t.Fatal(err)
	}
	priceSHA := sha256.Sum256([]byte("low-coverage-prices"))
	provider := &fakePriceProvider{
		name:    "tiingo",
		records: []PriceRecord{{Symbol: "LOW1", Source: "tiingo", TradeDate: now, CloseMicros: 5_000_000, Currency: "USD"}},
		result:  ProviderResult{Provider: "tiingo", SourceVersion: "tiingo:partial", SHA256: hex.EncodeToString(priceSHA[:]), EffectiveDate: now, Records: 1, Expected: 2, CoveragePct: 50, Timely: true},
	}
	c := Coordinator{DB: db, Prices: provider, Calendar: &stubMarketCalendar{}, Clock: func() time.Time { return now }, ResearchMode: true, MinPublishCoveragePct: 75}
	c.providerDayEvaluator = func(result ProviderResult, _ []PriceRecord, _ time.Time) (ProviderDayResult, error) {
		return ProviderDayResult{TradeDate: result.EffectiveDate, coveragePct: result.CoveragePct, timely: true, validationOK: true, goldReady: false, goldSHA256: strings.Repeat("c", 64)}, nil
	}

	batch, err := c.SyncMarketPrices(context.Background())
	if err == nil || batch.Status != BatchStatusFailed {
		t.Fatalf("batch=%#v err=%v", batch, err)
	}
	if !strings.Contains(batch.ErrorMessage, "coverage") {
		t.Fatalf("error message = %q, want coverage reason", batch.ErrorMessage)
	}
	var pointer CurrentBatchPointer
	if err := db.First(&pointer, "kind = ?", BatchKindPrescreen).Error; err != nil {
		t.Fatal(err)
	}
	if pointer.BatchID != old.BatchID {
		t.Fatalf("pointer changed to %s", pointer.BatchID)
	}
}

func TestCoordinatorResearchModeRejectsCoverageDropFromCurrentBatch(t *testing.T) {
	db := openMigratedTestDatabase(t)
	ny, _ := time.LoadLocation("America/New_York")
	now := time.Date(2026, 6, 23, 17, 0, 0, 0, ny)
	seedSecurityBatchForMarketTest(t, db, now, []marketSeedSecurity{
		{CIK: "0000000021", Ticker: "DROP1", Growth: 45, Runway: 18, Shares: 10_000_000},
		{CIK: "0000000022", Ticker: "DROP2", Growth: 45, Runway: 18, Shares: 10_000_000},
	})
	old := UniverseBatch{BatchID: strings.Repeat("e", 64), Kind: BatchKindPrescreen, Status: BatchStatusPublished, EffectiveDate: "2026-06-20", StartedAt: now.Add(-time.Hour)}
	if err := db.Create(&old).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&CurrentBatchPointer{Kind: BatchKindPrescreen, BatchID: old.BatchID}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&ProviderRun{BatchID: old.BatchID, Provider: "tiingo", Status: ProviderStatusValidation, SourceVersion: "tiingo:healthy", EffectiveDate: now.AddDate(0, 0, -1), ExpectedCount: 2, RecordCount: 2, CoveragePct: 95, Timely: true, CreatedAt: now.Add(-time.Hour)}).Error; err != nil {
		t.Fatal(err)
	}
	priceSHA := sha256.Sum256([]byte("coverage-drop-prices"))
	provider := &fakePriceProvider{
		name:    "tiingo",
		records: []PriceRecord{{Symbol: "DROP1", Source: "tiingo", TradeDate: now, CloseMicros: 5_000_000, Currency: "USD"}},
		result:  ProviderResult{Provider: "tiingo", SourceVersion: "tiingo:dropped", SHA256: hex.EncodeToString(priceSHA[:]), EffectiveDate: now, Records: 1, Expected: 2, CoveragePct: 70, Timely: true},
	}
	c := Coordinator{DB: db, Prices: provider, Calendar: &stubMarketCalendar{}, Clock: func() time.Time { return now }, ResearchMode: true, MinPublishCoveragePct: 20}
	c.providerDayEvaluator = func(result ProviderResult, _ []PriceRecord, _ time.Time) (ProviderDayResult, error) {
		return ProviderDayResult{TradeDate: result.EffectiveDate, coveragePct: result.CoveragePct, timely: true, validationOK: true, goldReady: false, goldSHA256: strings.Repeat("c", 64)}, nil
	}

	batch, err := c.SyncMarketPrices(context.Background())
	if err == nil || batch.Status != BatchStatusFailed || !strings.Contains(batch.ErrorMessage, "dropped") {
		t.Fatalf("batch=%#v err=%v", batch, err)
	}
	var pointer CurrentBatchPointer
	if err := db.First(&pointer, "kind = ?", BatchKindPrescreen).Error; err != nil {
		t.Fatal(err)
	}
	if pointer.BatchID != old.BatchID {
		t.Fatalf("pointer changed to %s", pointer.BatchID)
	}
}

func TestCoordinatorResearchModeUsesLatestCompletedMarketDateForMarketOnlyCatchup(t *testing.T) {
	db := openMigratedTestDatabase(t)
	ny, _ := time.LoadLocation("America/New_York")
	securityDate := time.Date(2026, 6, 30, 10, 0, 0, 0, ny)
	now := time.Date(2026, 7, 1, 17, 0, 0, 0, ny)
	seedSecurityBatchForMarketTest(t, db, securityDate, []marketSeedSecurity{{CIK: "0000000021", Ticker: "CUP", Growth: 45, Runway: 18, Shares: 10_000_000}})
	priceSHA := sha256.Sum256([]byte("catchup-prices"))
	provider := &fakePriceProvider{
		name:    "tiingo",
		records: []PriceRecord{{Symbol: "CUP", Source: "tiingo", TradeDate: now, CloseMicros: 5_000_000, Currency: "USD"}},
		result:  ProviderResult{Provider: "tiingo", SourceVersion: "tiingo:2026-07-01", SHA256: hex.EncodeToString(priceSHA[:]), EffectiveDate: now, Records: 1, Expected: 1, CoveragePct: 100, Timely: true},
	}
	c := Coordinator{DB: db, Prices: provider, Calendar: &stubMarketCalendar{}, Clock: func() time.Time { return now }, ResearchMode: true}
	c.providerDayEvaluator = func(result ProviderResult, _ []PriceRecord, _ time.Time) (ProviderDayResult, error) {
		return ProviderDayResult{TradeDate: result.EffectiveDate, coveragePct: 100, timely: true, validationOK: true, goldSHA256: strings.Repeat("d", 64)}, nil
	}

	batch, err := c.SyncMarketPrices(context.Background())
	if err != nil {
		t.Fatalf("SyncMarketPrices() error = %v", err)
	}
	if batch.Status != BatchStatusPublished || batch.EffectiveDate != "2026-07-01" {
		t.Fatalf("batch=%#v", batch)
	}
	if provider.requestedDate != "2026-07-01" {
		t.Fatalf("requested date = %q", provider.requestedDate)
	}
}

func TestCoordinatorResearchModeAllowsNonTradingSecurityDateWithPreviousPrice(t *testing.T) {
	db := openMigratedTestDatabase(t)
	ny, _ := time.LoadLocation("America/New_York")
	securityDate := time.Date(2026, 7, 3, 10, 0, 0, 0, ny)
	priceDate := time.Date(2026, 7, 2, 0, 0, 0, 0, ny)
	now := time.Date(2026, 7, 4, 1, 30, 0, 0, time.UTC)
	seedSecurityBatchForMarketTest(t, db, securityDate, []marketSeedSecurity{{CIK: "0000000023", Ticker: "HOLI", Growth: 45, Runway: 18, Shares: 10_000_000}})
	priceSHA := sha256.Sum256([]byte("holiday-previous-price"))
	provider := &fakePriceProvider{
		name:    "tiingo",
		records: []PriceRecord{{Symbol: "HOLI", Source: "tiingo", TradeDate: priceDate, CloseMicros: 5_000_000, Currency: "USD"}},
		result:  ProviderResult{Provider: "tiingo", SourceVersion: "tiingo:2026-07-02", SHA256: hex.EncodeToString(priceSHA[:]), EffectiveDate: securityDate, Records: 1, Expected: 1, CoveragePct: 100, Timely: true},
	}
	if err := db.Create(&PriceSnapshot{Source: "tiingo", SourceVersion: "friday", Symbol: "HOLI", TradeDate: priceDate, CloseMicros: 5_000_000, Currency: "USD", QualityStatus: QualityStatusValid}).Error; err != nil {
		t.Fatal(err)
	}
	c := Coordinator{DB: db, Prices: provider, Calendar: &stubMarketCalendar{holidays: map[string]bool{"2026-07-03": true}}, Clock: func() time.Time { return now }, ResearchMode: true}

	batch, err := c.SyncMarketPrices(context.Background())
	if err != nil {
		t.Fatalf("SyncMarketPrices() error = %v", err)
	}
	if batch.Status != BatchStatusPublished || batch.EffectiveDate != "2026-07-02" {
		t.Fatalf("batch=%#v", batch)
	}
	if provider.requestedDate != "" {
		t.Fatalf("non-trading day must not call provider, requested date = %q", provider.requestedDate)
	}
	var health ProviderHealth
	if err := db.First(&health, "provider = ?", "tiingo").Error; err != nil {
		t.Fatal(err)
	}
	if health.LastTradeDate != "2026-07-02" {
		t.Fatalf("health last trade date = %q, want 2026-07-02", health.LastTradeDate)
	}
}

func TestCoordinatorResearchModeAllowsSameDateProviderHealthCatchup(t *testing.T) {
	db := openMigratedTestDatabase(t)
	ny, _ := time.LoadLocation("America/New_York")
	securityDate := time.Date(2026, 6, 30, 10, 0, 0, 0, ny)
	now := time.Date(2026, 6, 30, 17, 0, 0, 0, ny)
	seedSecurityBatchForMarketTest(t, db, securityDate, []marketSeedSecurity{{CIK: "0000000022", Ticker: "CUP2", Growth: 45, Runway: 18, Shares: 10_000_000}})
	window, err := json.Marshal([]providerWindowDay{{Date: "2026-06-30", CoveragePct: 100, Timely: true, ValidationOK: true, GoldReady: false}})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&ProviderHealth{Provider: "tiingo", Status: ProviderStatusValidation, LastTradeDate: "2026-06-30", QualifiedTradingDays: 1, WindowJSON: string(window), GoldSHA256: strings.Repeat("e", 64), UpdatedAt: securityDate}).Error; err != nil {
		t.Fatal(err)
	}
	priceSHA := sha256.Sum256([]byte("catchup-same-date-prices"))
	provider := &fakePriceProvider{
		name:    "tiingo",
		records: []PriceRecord{{Symbol: "CUP2", Source: "tiingo", TradeDate: securityDate, CloseMicros: 5_000_000, Currency: "USD"}},
		result:  ProviderResult{Provider: "tiingo", SourceVersion: "tiingo:2026-06-30:partial", SHA256: hex.EncodeToString(priceSHA[:]), EffectiveDate: securityDate, Records: 1, Expected: 1, CoveragePct: 100, Timely: true},
	}
	c := Coordinator{DB: db, Prices: provider, Calendar: &stubMarketCalendar{}, Clock: func() time.Time { return now }, ResearchMode: true}
	c.providerDayEvaluator = func(result ProviderResult, _ []PriceRecord, _ time.Time) (ProviderDayResult, error) {
		return ProviderDayResult{TradeDate: result.EffectiveDate, coveragePct: 100, timely: true, validationOK: true, goldReady: false, goldSHA256: strings.Repeat("e", 64)}, nil
	}

	batch, err := c.SyncMarketPrices(context.Background())
	if err != nil {
		t.Fatalf("SyncMarketPrices() error = %v", err)
	}
	if batch.Status != BatchStatusPublished || batch.EffectiveDate != "2026-06-30" {
		t.Fatalf("batch=%#v", batch)
	}
	var health ProviderHealth
	if err := db.First(&health, "provider = ?", "tiingo").Error; err != nil {
		t.Fatal(err)
	}
	if health.LastTradeDate != "2026-06-30" || health.QualifiedTradingDays != 1 {
		t.Fatalf("health advanced unexpectedly: %#v", health)
	}
	var run ProviderRun
	if err := db.First(&run, "batch_id = ?", batch.BatchID).Error; err != nil {
		t.Fatal(err)
	}
	if run.Status != ProviderStatusValidation {
		t.Fatalf("provider run status = %q", run.Status)
	}
}

func TestCoordinatorRetriesExistingFailedMarketBatch(t *testing.T) {
	db := openMigratedTestDatabase(t)
	ny, _ := time.LoadLocation("America/New_York")
	securityDate := time.Date(2026, 6, 30, 10, 0, 0, 0, ny)
	now := time.Date(2026, 6, 30, 17, 0, 0, 0, ny)
	securityBatch := seedSecurityBatchForMarketTest(t, db, securityDate, []marketSeedSecurity{{CIK: "0000000023", Ticker: "RETRY", Growth: 45, Runway: 18, Shares: 10_000_000}})
	priceSHA := sha256.Sum256([]byte("retry-prices"))
	records := []PriceRecord{{Symbol: "RETRY", Source: "tiingo", TradeDate: securityDate, CloseMicros: 5_000_000, Currency: "USD"}}
	result := ProviderResult{Provider: "tiingo", SourceVersion: "tiingo:2026-06-30:retry", SHA256: hex.EncodeToString(priceSHA[:]), EffectiveDate: securityDate, Records: 1, Expected: 1, CoveragePct: 100, Timely: true}
	effectiveAt, err := parseNYCivilDate("2026-06-30")
	if err != nil {
		t.Fatal(err)
	}
	versions, err := normalizeSourceVersions("2026-06-30",
		SourceVersion{Source: BatchKindSecurity, Version: securityBatch.BatchID, SHA256: securityBatch.BatchID, EffectiveAt: effectiveAt},
		SourceVersion{Source: "price:" + result.Provider, Version: result.SourceVersion, SHA256: result.SHA256, EffectiveAt: result.EffectiveDate},
	)
	if err != nil {
		t.Fatal(err)
	}
	contentSHA, err := hashPriceInputs(securityBatch.BatchID, records)
	if err != nil {
		t.Fatal(err)
	}
	batchID, encoded, err := batchIdentity(BatchKindPrescreen, "2026-06-30", versions, contentSHA)
	if err != nil {
		t.Fatal(err)
	}
	failedCompletedAt := now.Add(-time.Hour)
	if err := db.Create(&UniverseBatch{BatchID: batchID, Kind: BatchKindPrescreen, Status: BatchStatusFailed, EffectiveDate: "2026-06-30", SourceVersionsJSON: encoded, ContentSHA256: contentSHA, StartedAt: now.Add(-2 * time.Hour), CompletedAt: &failedCompletedAt, ErrorMessage: "old failure"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&ProviderRun{BatchID: batchID, Provider: "tiingo", Status: ProviderStatusFailed, SourceVersion: "old", SHA256: strings.Repeat("f", 64), EffectiveDate: securityDate, CreatedAt: now.Add(-time.Hour)}).Error; err != nil {
		t.Fatal(err)
	}
	provider := &fakePriceProvider{name: "tiingo", records: records, result: result}
	c := Coordinator{DB: db, Prices: provider, Calendar: &stubMarketCalendar{}, Clock: func() time.Time { return now }, ResearchMode: true}
	c.providerDayEvaluator = func(result ProviderResult, _ []PriceRecord, _ time.Time) (ProviderDayResult, error) {
		return ProviderDayResult{TradeDate: result.EffectiveDate, coveragePct: 100, timely: true, validationOK: true, goldReady: false, goldSHA256: strings.Repeat("e", 64)}, nil
	}

	batch, err := c.SyncMarketPrices(context.Background())
	if err != nil {
		t.Fatalf("SyncMarketPrices() error = %v", err)
	}
	if batch.BatchID != batchID || batch.Status != BatchStatusPublished || batch.ErrorMessage != "" {
		t.Fatalf("batch=%#v", batch)
	}
	var runs []ProviderRun
	if err := db.Where("batch_id = ?", batchID).Order("id").Find(&runs).Error; err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || runs[0].SourceVersion != result.SourceVersion {
		t.Fatalf("provider runs after retry = %#v", runs)
	}
}

type marketSeedSecurity struct {
	CIK     string
	Ticker  string
	Growth  float64
	Runway  float64
	Shares  int64
	BlocksB bool
}

func seedSecurityBatchForMarketTest(t *testing.T, db *gorm.DB, now time.Time, seeds []marketSeedSecurity) UniverseBatch {
	t.Helper()
	batch := UniverseBatch{BatchID: strings.Repeat("a", 64), Kind: BatchKindSecurity, Status: BatchStatusPublished, EffectiveDate: now.Format(time.DateOnly), SourceVersionsJSON: "[]", ContentSHA256: strings.Repeat("b", 64), StartedAt: now, CompletedAt: &now}
	if err := db.Create(&batch).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&CurrentBatchPointer{Kind: BatchKindSecurity, BatchID: batch.BatchID}).Error; err != nil {
		t.Fatal(err)
	}
	for i, seed := range seeds {
		security := Security{CIK: seed.CIK, CompanyName: seed.Ticker + " Co", CatalogStatus: SecurityCatalogPublished}
		if err := db.Create(&security).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&SecurityBatchIdentity{BatchID: batch.BatchID, SecurityID: security.ID, CIK: seed.CIK, Ticker: seed.Ticker, ProviderTicker: seed.Ticker, Exchange: "Nasdaq", MappingStatus: MappingStatusCurrent}).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&ClassificationSnapshot{BatchID: batch.BatchID, SecurityID: security.ID, Included: true, Status: EffectiveStatusIncluded}).Error; err != nil {
			t.Fatal(err)
		}
		share := ShareSnapshot{SecurityID: security.ID, Instant: now.AddDate(0, 0, -1), Accession: fmt.Sprintf("share-%d", i), Concept: "dei:EntityCommonStockSharesOutstanding", Shares: seed.Shares, QualityStatus: QualityStatusValid, CreatedAt: now}
		if err := db.Create(&share).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&BatchShareSelection{BatchID: batch.BatchID, SecurityID: security.ID, ShareSnapshotID: &share.ID, QualityStatus: QualityStatusValid, CreatedAt: now}).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&FinancialMetricSnapshot{BatchID: batch.BatchID, SecurityID: security.ID, RevenueGrowthAvailable: true, RunwayAvailable: true, QuarterlyRevenueYoYPct: seed.Growth, CashRunwayMonths: seed.Runway, CreatedAt: now}).Error; err != nil {
			t.Fatal(err)
		}
		if seed.BlocksB {
			risk := CapitalRiskSnapshot{BatchID: batch.BatchID, SecurityID: security.ID, Kind: CapitalEventATMProgram, Accession: fmt.Sprintf("risk-%d", i), EffectiveAt: now.AddDate(0, 0, -1), Active: true, BlocksB: true, CreatedAt: now}
			if err := db.Create(&risk).Error; err != nil {
				t.Fatal(err)
			}
		}
	}
	return batch
}

func tickersFromListings(listings []Listing) []string {
	out := make([]string, 0, len(listings))
	for _, listing := range listings {
		out = append(out, listing.Ticker)
	}
	sort.Strings(out)
	return out
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

func TestCoordinatorUsesCommonShareWhenIssuerAlsoListsWarrants(t *testing.T) {
	db := openMigratedTestDatabase(t)
	now := time.Date(2026, 8, 3, 10, 0, 0, 0, time.UTC)
	common := SecuritySourceRecord{
		SourceKey: "SLDP", CIK: "0001844862", Ticker: "SLDP", ProviderTicker: "SLDP",
		CompanyName: "Solid Power, Inc.", SecurityName: "Solid Power, Inc. - Class A Common Stock",
		Exchange: "Nasdaq", SIC: 3690, StateOfIncorporation: "DE", LatestAnnualForm: "10-K",
		RecentForms: []string{"10-Q"}, MappingStatus: MappingStatusCurrent,
	}
	warrant := common
	warrant.SourceKey, warrant.Ticker, warrant.ProviderTicker = "SLDPW", "SLDPW", "SLDPW"
	warrant.SecurityName = "Solid Power, Inc. - Warrant"
	c := Coordinator{
		DB:       db,
		Metadata: fakeMetadataSource{records: []SecuritySourceRecord{warrant, common}, version: testSourceVersion("metadata", "common-warrant", now)},
		Shares:   fakeShareSource{version: testSourceVersion("shares", "common-warrant", now)},
		Events:   noEvents(now),
		Clock:    func() time.Time { return now },
	}
	batch, err := c.SyncSecurityUniverse(context.Background())
	if err != nil || batch.Status != BatchStatusPublished {
		t.Fatalf("batch=%#v err=%v", batch, err)
	}
	var identity SecurityBatchIdentity
	if err := db.First(&identity, "batch_id = ?", batch.BatchID).Error; err != nil {
		t.Fatal(err)
	}
	if identity.Ticker != "SLDP" || identity.MappingStatus != MappingStatusCurrent {
		t.Fatalf("primary identity=%#v", identity)
	}
	var classification ClassificationSnapshot
	if err := db.First(&classification, "batch_id = ?", batch.BatchID).Error; err != nil {
		t.Fatal(err)
	}
	if !classification.Included || classification.ReasonCode != ReasonDomesticOperatingCommon {
		t.Fatalf("classification=%#v", classification)
	}
	var warrantSnapshot ListingIdentitySnapshot
	if err := db.First(&warrantSnapshot, "batch_id = ? AND source_key = ?", batch.BatchID, "SLDPW").Error; err != nil {
		t.Fatal(err)
	}
	if warrantSnapshot.Included || warrantSnapshot.Status != EffectiveStatusExcluded || warrantSnapshot.ReasonCode != ReasonNonCommonSecurity {
		t.Fatalf("warrant snapshot=%#v", warrantSnapshot)
	}
}

func TestGroupMetadataKeepsUnrelatedCommonListingsAsConflict(t *testing.T) {
	rows := []SecuritySourceRecord{
		{CIK: "0001844862", Ticker: "SLDP", SecurityName: "Solid Power, Inc. - Class A Common Stock", MappingStatus: MappingStatusCurrent},
		{CIK: "0001844862", Ticker: "OTHER", SecurityName: "Solid Power, Inc. - Class B Common Stock", MappingStatus: MappingStatusCurrent},
	}
	groups, err := groupMetadata(rows)
	if err != nil || len(groups) != 1 {
		t.Fatalf("groups=%#v err=%v", groups, err)
	}
	if groups[0].Primary.MappingStatus != MappingStatusConflict {
		t.Fatalf("unrelated common listings must conflict: %#v", groups[0].Primary)
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
	h1, err := hashSecurityInputs(one, []ShareFact{{CIK: a.CIK, Accession: "a", Instant: now}}, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	h2, err := hashSecurityInputs(two, []ShareFact{{CIK: a.CIK, Accession: "a", Instant: now}}, nil, nil, nil, nil, nil)
	if err != nil || h1 != h2 {
		t.Fatalf("hashes=%s/%s err=%v", h1, h2, err)
	}
	conflict := a
	conflict.CompanyName = "changed"
	if _, err := normalizeMetadataRecords([]SecuritySourceRecord{a, conflict}); err == nil {
		t.Fatal("expected duplicate conflict")
	}
}

func TestNormalizeInsiderTransactionsKeepsSequentialSameDayLots(t *testing.T) {
	day := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	base := InsiderTransaction{
		CIK: "0000884144", Accession: "0001628280-26-023298", OwnerName: "Officer",
		TransactionDate: day, TransactionCode: "D", AcquiredDisposedCode: "D", Shares: 812, PricePerShareUSD: 8.34,
	}
	first := base
	first.SharesOwnedAfter, first.SharesOwnedBefore = 298_544, 299_356
	second := base
	second.SharesOwnedAfter, second.SharesOwnedBefore = 297_732, 298_544
	rows, err := normalizeInsiderTransactions([]InsiderTransaction{first, second, first})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows=%#v, want two distinct sequential lots", rows)
	}
}

func TestNormalizeInsiderTransactionsMergesDuplicateSECArchiveAliases(t *testing.T) {
	row := InsiderTransaction{
		CIK: "0001070423", Accession: "0001581990-26-000045", OwnerName: "PLAINS GP HOLDINGS LP",
		TransactionDate: time.Date(2026, 8, 14, 0, 0, 0, 0, time.UTC), TransactionCode: "A",
		AcquiredDisposedCode: "A", Shares: 59_200, SharesOwnedAfter: 1_234_567, SharesOwnedBefore: 1_175_367,
		SourceURL: "https://www.sec.gov/Archives/edgar/data/1070423/000158199026000045/form4.xml",
	}
	alias := row
	alias.SourceURL = "https://www.sec.gov/Archives/edgar/data/1581990/000158199026000045/form4.xml"

	rows, err := normalizeInsiderTransactions([]InsiderTransaction{row, alias})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows=%#v, want one merged transaction", rows)
	}
	if rows[0].SourceURL != row.SourceURL {
		t.Fatalf("source URL = %q, want canonical issuer path %q", rows[0].SourceURL, row.SourceURL)
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
