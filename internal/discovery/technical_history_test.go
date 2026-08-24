package discovery

import (
	"context"
	"testing"
	"time"
)

type fakeTechnicalHistoryProvider struct {
	records    []PriceRecord
	called     []Listing
	failTicker string
}

func (p *fakeTechnicalHistoryProvider) Load(context.Context, []Listing) ([]PriceRecord, ProviderResult, error) {
	return nil, ProviderResult{}, nil
}

func (p *fakeTechnicalHistoryProvider) LoadHistory(_ context.Context, listings []Listing, _ string, _ int) ([]PriceRecord, error) {
	p.called = append(p.called, listings...)
	expected := map[string]struct{}{}
	for _, listing := range listings {
		expected[listing.Ticker] = struct{}{}
	}
	rows := make([]PriceRecord, 0)
	for _, record := range p.records {
		if _, ok := expected[record.Symbol]; ok && record.Symbol != p.failTicker {
			rows = append(rows, record)
		}
	}
	return rows, nil
}

func TestNormalizeTechnicalHistoryLookbackUsesMA200Default(t *testing.T) {
	if got := normalizeTechnicalHistoryLookbackDays(0); got != defaultTechnicalHistoryLookbackDays {
		t.Fatalf("default lookback = %d, want %d", got, defaultTechnicalHistoryLookbackDays)
	}
	if got := normalizeTechnicalHistoryLookbackDays(technicalMA200LookbackDays - 1); got != technicalMA200LookbackDays {
		t.Fatalf("minimum lookback = %d, want %d", got, technicalMA200LookbackDays)
	}
}

func TestBackfillCandidateTechnicalHistoryPersistsOnlyIncompleteCandidates(t *testing.T) {
	db := openMigratedTestDatabase(t)
	security := Security{CIK: "0000011111", CompanyName: "History Co", CatalogStatus: SecurityCatalogPublished}
	if err := db.Create(&security).Error; err != nil {
		t.Fatal(err)
	}
	securityBatch := UniverseBatch{BatchID: "security", Kind: BatchKindSecurity, Status: BatchStatusPublished, StartedAt: time.Now()}
	marketBatch := UniverseBatch{BatchID: "market", Kind: BatchKindPrescreen, Status: BatchStatusPublished, UniverseSourceVersion: securityBatch.BatchID, StartedAt: time.Now()}
	if err := db.Create(&[]UniverseBatch{securityBatch, marketBatch}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&CurrentBatchPointer{Kind: BatchKindPrescreen, BatchID: marketBatch.BatchID}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&SecurityBatchIdentity{BatchID: securityBatch.BatchID, SecurityID: security.ID, Ticker: "HIST", ProviderTicker: "HIST.P", MappingStatus: MappingStatusCurrent}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&CandidateScoreSnapshot{BatchID: marketBatch.BatchID, SecurityID: security.ID, Ticker: "HIST", Grade: CandidateGradeB, EligibleB: true}).Error; err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	records := make([]PriceRecord, 0, technicalHistorySamplesRequired)
	for index := 0; index < technicalHistorySamplesRequired; index++ {
		closeMicros := 1_000_000 + int64(index)
		records = append(records, PriceRecord{Symbol: "HIST", Source: "fake", TradeDate: base.AddDate(0, 0, index), OpenMicros: closeMicros - 10, HighMicros: closeMicros + 20, LowMicros: closeMicros - 20, CloseMicros: closeMicros, Volume: 100, Currency: "USD"})
		records = append(records, PriceRecord{Symbol: "IWM", Source: "fake", TradeDate: base.AddDate(0, 0, index), OpenMicros: closeMicros * 2, HighMicros: closeMicros*2 + 20, LowMicros: closeMicros*2 - 20, CloseMicros: closeMicros * 2, Volume: 1000, Currency: "USD"})
	}
	provider := &fakeTechnicalHistoryProvider{records: records}
	result, err := BackfillCandidateTechnicalHistory(context.Background(), db, provider, time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC), 35)
	if err != nil {
		t.Fatal(err)
	}
	if result.CandidateCount != 1 || result.RequestedCount != 2 || result.PersistedCount != technicalHistorySamplesRequired*2 || !result.BenchmarkReady {
		t.Fatalf("result = %+v", result)
	}
	if len(provider.called) != 2 || provider.called[0].Ticker != "IWM" || provider.called[1].ProviderTicker != "HIST.P" {
		t.Fatalf("listings = %+v", provider.called)
	}
	var count int64
	if err := db.Model(&PriceSnapshot{}).Where("symbol = ?", "HIST").Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != technicalHistorySamplesRequired {
		t.Fatalf("history rows = %d, want %d", count, technicalHistorySamplesRequired)
	}
	second, err := BackfillCandidateTechnicalHistory(context.Background(), db, provider, time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC), 35)
	if err != nil {
		t.Fatal(err)
	}
	if second.AlreadyReadyCount != 1 || second.RequestedCount != 0 {
		t.Fatalf("second result = %+v", second)
	}
}

func TestBackfillCandidateTechnicalHistoryDegradesWhenIWMIsMissing(t *testing.T) {
	db := openMigratedTestDatabase(t)
	security := Security{CIK: "0000011112", CompanyName: "Benchmark Gap Co", CatalogStatus: SecurityCatalogPublished}
	if err := db.Create(&security).Error; err != nil {
		t.Fatal(err)
	}
	securityBatch := UniverseBatch{BatchID: "security-gap", Kind: BatchKindSecurity, Status: BatchStatusPublished, StartedAt: time.Now()}
	marketBatch := UniverseBatch{BatchID: "market-gap", Kind: BatchKindPrescreen, Status: BatchStatusPublished, UniverseSourceVersion: securityBatch.BatchID, EffectiveDate: "2026-07-14", StartedAt: time.Now()}
	if err := db.Create(&[]UniverseBatch{securityBatch, marketBatch}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&CurrentBatchPointer{Kind: BatchKindPrescreen, BatchID: marketBatch.BatchID}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&SecurityBatchIdentity{BatchID: securityBatch.BatchID, SecurityID: security.ID, Ticker: "GAP", ProviderTicker: "GAP", MappingStatus: MappingStatusCurrent}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&CandidateScoreSnapshot{BatchID: marketBatch.BatchID, SecurityID: security.ID, Ticker: "GAP", Grade: CandidateGradeA, EligibleA: true}).Error; err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	records := make([]PriceRecord, 0, technicalMA200LookbackDays)
	for index := 0; index < technicalMA200LookbackDays; index++ {
		closeMicros := int64(1_000_000 + index)
		records = append(records, PriceRecord{Symbol: "GAP", Source: "fake", TradeDate: base.AddDate(0, 0, index), OpenMicros: closeMicros, HighMicros: closeMicros + 10, LowMicros: closeMicros - 10, CloseMicros: closeMicros, Volume: 100})
	}
	provider := &fakeTechnicalHistoryProvider{records: records, failTicker: "IWM"}
	result, err := BackfillCandidateTechnicalHistory(context.Background(), db, provider, time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC), 320)
	if err != nil {
		t.Fatal(err)
	}
	if result.BenchmarkReady || result.BenchmarkStatus != "missing" || result.BenchmarkSampleDays != 0 || len(result.Failures) != 1 || result.Failures[0].Ticker != "IWM" || len(result.Warnings) == 0 {
		t.Fatalf("degraded result = %+v", result)
	}
	if len(provider.called) != 2 || provider.called[0].Ticker != "IWM" {
		t.Fatalf("benchmark was not requested first: %+v", provider.called)
	}
}

func TestScheduledTechnicalHistoryBackfillHonorsPerTickerBackoffAndManualRetryResolves(t *testing.T) {
	db := openMigratedTestDatabase(t)
	security := Security{CIK: "0000011113", CompanyName: "Retry Co", CatalogStatus: SecurityCatalogPublished}
	if err := db.Create(&security).Error; err != nil {
		t.Fatal(err)
	}
	securityBatch := UniverseBatch{BatchID: "security-retry", Kind: BatchKindSecurity, Status: BatchStatusPublished, StartedAt: time.Now()}
	marketBatch := UniverseBatch{BatchID: "market-retry", Kind: BatchKindPrescreen, Status: BatchStatusPublished, UniverseSourceVersion: securityBatch.BatchID, StartedAt: time.Now()}
	if err := db.Create(&[]UniverseBatch{securityBatch, marketBatch}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&CurrentBatchPointer{Kind: BatchKindPrescreen, BatchID: marketBatch.BatchID}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&SecurityBatchIdentity{BatchID: securityBatch.BatchID, SecurityID: security.ID, Ticker: "RETRY", ProviderTicker: "RETRY", MappingStatus: MappingStatusCurrent}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&CandidateScoreSnapshot{BatchID: marketBatch.BatchID, SecurityID: security.ID, Ticker: "RETRY", Grade: CandidateGradeA, EligibleA: true}).Error; err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	records := make([]PriceRecord, 0, technicalHistorySamplesRequired*2)
	for index := 0; index < technicalHistorySamplesRequired; index++ {
		closeMicros := int64(1_000_000 + index)
		for _, ticker := range []string{"IWM", "RETRY"} {
			records = append(records, PriceRecord{Symbol: ticker, Source: "fake", TradeDate: base.AddDate(0, 0, index), OpenMicros: closeMicros, HighMicros: closeMicros + 10, LowMicros: closeMicros - 10, CloseMicros: closeMicros, Volume: 100})
		}
	}
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	provider := &fakeTechnicalHistoryProvider{records: records, failTicker: "RETRY"}
	first, err := BackfillDueCandidateTechnicalHistory(context.Background(), db, provider, now, technicalHistorySamplesRequired)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Failures) != 1 || first.Failures[0].Ticker != "RETRY" || first.Failures[0].AttemptCount != 1 || first.PendingRetryCount != 1 {
		t.Fatalf("first = %+v", first)
	}
	calledAfterFirst := len(provider.called)
	second, err := BackfillDueCandidateTechnicalHistory(context.Background(), db, provider, now.Add(time.Hour), technicalHistorySamplesRequired)
	if err != nil {
		t.Fatal(err)
	}
	if second.RequestedCount != 0 || second.DeferredRetryCount != 1 || len(provider.called) != calledAfterFirst {
		t.Fatalf("second = %+v, calls=%+v", second, provider.called)
	}
	manual, err := BackfillCandidateTechnicalHistory(context.Background(), db, provider, now.Add(2*time.Hour), technicalHistorySamplesRequired)
	if err != nil {
		t.Fatal(err)
	}
	if manual.RequestedCount != 1 || len(manual.Failures) != 1 || manual.Failures[0].AttemptCount != 2 {
		t.Fatalf("manual = %+v", manual)
	}
	localRows := make([]PriceRecord, 0, technicalHistorySamplesRequired)
	for _, record := range records {
		if record.Symbol == "RETRY" {
			localRows = append(localRows, record)
		}
	}
	if _, err := PersistTechnicalPriceHistory(context.Background(), db, localRows, "fake:daily-recovery"); err != nil {
		t.Fatal(err)
	}
	calledBeforeReconcile := len(provider.called)
	reconciled, err := BackfillDueCandidateTechnicalHistory(context.Background(), db, provider, now.Add(3*time.Hour), technicalHistorySamplesRequired)
	if err != nil {
		t.Fatal(err)
	}
	if reconciled.RequestedCount != 0 || reconciled.PendingRetryCount != 0 || len(provider.called) != calledBeforeReconcile {
		t.Fatalf("reconciled = %+v, calls=%+v", reconciled, provider.called)
	}
	var state TechnicalHistoryRetryState
	if err := db.Where("ticker = ?", "RETRY").First(&state).Error; err != nil {
		t.Fatal(err)
	}
	if state.Status != TechnicalHistoryRetryResolved || state.FailureCount != 0 || state.LastSuccessAt == nil {
		t.Fatalf("state = %+v", state)
	}
	var openIncidents int64
	if err := db.Model(&DataQualityIncident{}).Where("domain = ? AND entity_key = ? AND status = ?", "technical_history", "RETRY", DataQualityIncidentOpen).Count(&openIncidents).Error; err != nil {
		t.Fatal(err)
	}
	if openIncidents != 0 {
		t.Fatalf("open technical-history incidents = %d", openIncidents)
	}
}

func TestBackfillTickerTechnicalHistoryPersistsAndReadsWatchTargetSeries(t *testing.T) {
	db := openMigratedTestDatabase(t)
	base := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	provider := &fakeTechnicalHistoryProvider{records: []PriceRecord{
		{Symbol: "WATCH", Source: "fake", TradeDate: base, OpenMicros: 9_800_000, HighMicros: 10_200_000, LowMicros: 9_700_000, CloseMicros: 10_000_000, Volume: 100, Currency: "USD"},
		{Symbol: "WATCH", Source: "fake", TradeDate: base.AddDate(0, 0, 1), OpenMicros: 10_500_000, HighMicros: 11_200_000, LowMicros: 10_300_000, CloseMicros: 11_000_000, Volume: 200, Currency: "USD"},
	}}
	result, err := BackfillTickerTechnicalHistory(context.Background(), db, provider, "watch", base.AddDate(0, 0, 2), 0)
	if err != nil {
		t.Fatal(err)
	}
	if result.CandidateCount != 1 || result.RequestedCount != 1 || result.PersistedCount != 2 {
		t.Fatalf("result = %+v", result)
	}
	if len(provider.called) != 1 || provider.called[0].Ticker != "WATCH" || provider.called[0].ProviderTicker != "WATCH" {
		t.Fatalf("listings = %+v", provider.called)
	}
	history, err := GetTickerTechnicalHistory(context.Background(), db, "watch")
	if err != nil {
		t.Fatal(err)
	}
	if history.Ticker != "WATCH" || len(history.History) != 2 || history.History[0].TradeDate != "2026-01-03" {
		t.Fatalf("history = %+v", history)
	}
	if !history.History[0].OHLCAvailable || history.History[0].HighUSD != 11.2 || history.History[0].LowUSD != 10.3 {
		t.Fatalf("OHLC history = %+v", history.History[0])
	}
}
