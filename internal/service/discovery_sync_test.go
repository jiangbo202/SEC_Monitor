package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"sec_monitor/internal/config"
	"sec_monitor/internal/discovery"
	"sec_monitor/internal/model"
)

type fakeDiscoveryRunner struct {
	securityBatch             discovery.UniverseBatch
	marketBatch               discovery.UniverseBatch
	securityErr               error
	marketErr                 error
	securityCalls             int
	marketCalls               int
	securityDeadlineRemaining time.Duration
}

type stubServiceCalendar struct{}

func (s *stubServiceCalendar) IsTradingDate(context.Context, string) (bool, error) { return true, nil }
func (s *stubServiceCalendar) IsTradingDay(context.Context, time.Time) (bool, error) {
	return true, nil
}

type fakeWatchTargetPriceProvider struct {
	records  []discovery.PriceRecord
	date     string
	expected []discovery.Listing
	calls    int
}

func TestFixedTickerEvaluationMetadataSupportsForm4CheckpointIdentity(t *testing.T) {
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	record := discovery.SecuritySourceRecord{CIK: "0001646188", Ticker: "ONDS", CompanyName: "Ondas Inc."}
	version := discovery.SourceVersion{Source: "sec-individual-new-listings", Version: "issuer-v1", SHA256: strings.Repeat("a", 64), EffectiveAt: now}
	metadata := fixedSecurityMetadataSource{records: []discovery.SecuritySourceRecord{record}, version: version}
	loaded, loadedVersion, err := metadata.Load(context.Background())
	if err != nil || len(loaded) != 1 || loadedVersion.SHA256 != version.SHA256 {
		t.Fatalf("loaded=%+v version=%+v err=%v", loaded, loadedVersion, err)
	}
	transactions, coverages, _, err := (discovery.SECForm4InsiderSource{
		Metadata: metadata, Downloader: &discovery.Downloader{CacheDir: t.TempDir()}, LookbackDays: discovery.CandidateInsiderLookbackDays,
	}).LoadInsiderTransactionsWithCoverage(context.Background(), map[string]struct{}{record.CIK: {}}, now)
	if err != nil || len(transactions) != 0 || len(coverages) != 1 || coverages[0].Status != discovery.InsiderCoverageCoveredNoFilings {
		t.Fatalf("transactions=%+v coverages=%+v err=%v", transactions, coverages, err)
	}
}

func (f *fakeWatchTargetPriceProvider) Load(context.Context, []discovery.Listing) ([]discovery.PriceRecord, discovery.ProviderResult, error) {
	return f.records, discovery.ProviderResult{}, nil
}

func (f *fakeWatchTargetPriceProvider) LoadForDate(_ context.Context, expected []discovery.Listing, effectiveDate string) ([]discovery.PriceRecord, discovery.ProviderResult, error) {
	f.calls++
	f.date = effectiveDate
	f.expected = append([]discovery.Listing(nil), expected...)
	return f.records, discovery.ProviderResult{Provider: "test", EffectiveDate: f.records[0].TradeDate, Records: len(f.records), Expected: len(expected)}, nil
}

func (f *fakeDiscoveryRunner) SyncSecurityUniverse(ctx context.Context) (discovery.UniverseBatch, error) {
	f.securityCalls++
	if deadline, ok := ctx.Deadline(); ok {
		f.securityDeadlineRemaining = time.Until(deadline)
	}
	return f.securityBatch, f.securityErr
}

func (f *fakeDiscoveryRunner) SyncMarketPrices(ctx context.Context) (discovery.UniverseBatch, error) {
	f.marketCalls++
	return f.marketBatch, f.marketErr
}

func TestDiscoverySyncServiceRunsSecurityAndMarket(t *testing.T) {
	discoveryDB := testDiscoveryDB(t)
	runner := &fakeDiscoveryRunner{
		securityBatch: discovery.UniverseBatch{BatchID: "security", Kind: discovery.BatchKindSecurity, Status: discovery.BatchStatusPublished, StartedAt: time.Now()},
		marketBatch:   discovery.UniverseBatch{BatchID: "market", Kind: discovery.BatchKindPrescreen, Status: discovery.BatchStatusPublished, StartedAt: time.Now()},
	}
	result, err := NewDiscoverySyncService(discoveryDB, config.DiscoveryConfig{}).withRunner(runner).Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != DiscoverySyncStatusPublished || result.SecurityBatchID != "security" || result.MarketBatchID != "market" {
		t.Fatalf("result = %#v", result)
	}
	if runner.securityCalls != 1 || runner.marketCalls != 1 {
		t.Fatalf("calls security=%d market=%d", runner.securityCalls, runner.marketCalls)
	}
	var run discovery.DiscoverySyncRun
	if err := discoveryDB.Order("id DESC").First(&run).Error; err != nil {
		t.Fatal(err)
	}
	if run.Status != DiscoverySyncStatusPublished || run.Phase != "completed" || run.SecurityBatchID != "security" || run.MarketBatchID != "market" || run.CompletedAt == nil {
		t.Fatalf("sync lifecycle = %#v", run)
	}
}

func TestDiscoverySyncServiceUsesIndependentSecurityWorkflowBudget(t *testing.T) {
	discoveryDB := testDiscoveryDB(t)
	runner := &fakeDiscoveryRunner{
		securityBatch: discovery.UniverseBatch{BatchID: "security-budget", Kind: discovery.BatchKindSecurity, Status: discovery.BatchStatusPublished},
		marketBatch:   discovery.UniverseBatch{BatchID: "market-budget", Kind: discovery.BatchKindPrescreen, Status: discovery.BatchStatusPublished},
	}
	_, err := NewDiscoverySyncService(discoveryDB, config.DiscoveryConfig{TaskTimeoutMin: 1}).withRunner(runner).Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if runner.securityDeadlineRemaining < 3*time.Minute+50*time.Second || runner.securityDeadlineRemaining > 4*time.Minute+time.Second {
		t.Fatalf("security workflow budget=%s want about 4m", runner.securityDeadlineRemaining)
	}
}

func TestSecuritySourceStageObserverPersistsVisibleSubsteps(t *testing.T) {
	discoveryDB := testDiscoveryDB(t)
	run := discovery.DiscoverySyncRun{Kind: "full", Status: "running", Phase: "security_universe", StartedAt: time.Now().UTC()}
	if err := discoveryDB.Create(&run).Error; err != nil {
		t.Fatal(err)
	}
	service := NewDiscoverySyncService(discoveryDB, config.DiscoveryConfig{})
	observer := service.securitySourceStageObserver(run.ID)
	observer(discovery.SecuritySourceStageProgress{Phase: "security-fundamentals", Status: "running", Message: "开始采集"})
	observer(discovery.SecuritySourceStageProgress{Phase: "security-fundamentals", Status: "completed", RecordCount: 42, Message: "采集完成"})
	observer(discovery.SecuritySourceStageProgress{Phase: "security-insiders", Status: "resumed", RecordCount: 7, Message: "复用已完成的数据采集检查点"})
	observer(discovery.SecuritySourceStageProgress{Phase: "security-capital-events", Status: "running", RecordCount: 25, TotalCount: 100, Message: "已处理 25 个发行人"})
	observer(discovery.SecuritySourceStageProgress{Phase: "security-capital-events", Status: "failed", Message: "context deadline exceeded"})
	var steps []discovery.DiscoverySyncStep
	if err := discoveryDB.Where("run_id = ?", run.ID).Order("sequence ASC").Find(&steps).Error; err != nil {
		t.Fatal(err)
	}
	if len(steps) != 3 || steps[0].Status != "completed" || steps[0].RecordCount != 42 || steps[1].Status != DiscoverySyncRunStatusSkipped || steps[1].RecordCount != 7 || steps[2].Status != "failed" || steps[2].RecordCount != 25 || steps[2].TotalCount != 100 {
		t.Fatalf("source steps=%#v", steps)
	}
}

func TestDiscoverySyncServiceReportsMarketFailureAfterSecuritySync(t *testing.T) {
	discoveryDB := testDiscoveryDB(t)
	runner := &fakeDiscoveryRunner{
		securityBatch: discovery.UniverseBatch{BatchID: "security", Kind: discovery.BatchKindSecurity, Status: discovery.BatchStatusPublished},
		marketErr:     errors.New("provider inactive"),
	}
	result, err := NewDiscoverySyncService(discoveryDB, config.DiscoveryConfig{}).withRunner(runner).Run(context.Background())
	if err == nil || !errors.Is(err, ErrDiscoveryMarketSync) {
		t.Fatalf("err = %v, want market sync wrapper", err)
	}
	if result.Status != DiscoverySyncStatusMarketFailed || result.SecurityBatchID != "security" {
		t.Fatalf("result = %#v", result)
	}
	var run discovery.DiscoverySyncRun
	if err := discoveryDB.Order("id DESC").First(&run).Error; err != nil {
		t.Fatal(err)
	}
	if run.Status != DiscoverySyncRunStatusFailed || run.Phase != "failed" || run.SecurityBatchID != "security" || run.CompletedAt == nil || !strings.Contains(run.ErrorMessage, "provider inactive") {
		t.Fatalf("sync lifecycle = %#v", run)
	}
}

func TestDiscoverySyncServiceResumesPublishedSecurityPhaseAfterMarketFailure(t *testing.T) {
	discoveryDB := testDiscoveryDB(t)
	location, _ := time.LoadLocation("America/New_York")
	date := time.Now().In(location).Format(time.DateOnly)
	binding, err := discovery.ActiveSmallCapPolicyBinding(context.Background(), discoveryDB)
	if err != nil {
		t.Fatal(err)
	}
	security := discovery.UniverseBatch{BatchID: strings.Repeat("a", 64), Kind: discovery.BatchKindSecurity, Status: discovery.BatchStatusPublished, EffectiveDate: date, RecordCount: 12, PolicyVersionID: binding.PolicyVersionID, PolicyVersion: binding.PolicyVersion, PolicyContentSHA256: binding.PolicyContentSHA256, PolicySnapshotJSON: binding.PolicySnapshotJSON}
	if err := discoveryDB.Create(&security).Error; err != nil {
		t.Fatal(err)
	}
	previous := discovery.DiscoverySyncRun{Kind: "full", Status: DiscoverySyncRunStatusFailed, Phase: "failed", StartedAt: time.Now().Add(-time.Minute), SecurityBatchID: security.BatchID, PolicyVersionID: binding.PolicyVersionID, PolicyVersion: binding.PolicyVersion, PolicyContentSHA256: binding.PolicyContentSHA256, PolicySnapshotJSON: binding.PolicySnapshotJSON}
	if err := discoveryDB.Create(&previous).Error; err != nil {
		t.Fatal(err)
	}
	runner := &fakeDiscoveryRunner{marketBatch: discovery.UniverseBatch{BatchID: "resumed-market", Kind: discovery.BatchKindPrescreen, Status: discovery.BatchStatusPublished}}
	result, err := NewDiscoverySyncService(discoveryDB, config.DiscoveryConfig{}).withRunner(runner).Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if runner.securityCalls != 0 || runner.marketCalls != 1 || result.SecurityBatchID != security.BatchID {
		t.Fatalf("calls security=%d market=%d result=%#v", runner.securityCalls, runner.marketCalls, result)
	}
	var run discovery.DiscoverySyncRun
	if err := discoveryDB.Order("id DESC").First(&run).Error; err != nil {
		t.Fatal(err)
	}
	var securityStep discovery.DiscoverySyncStep
	if err := discoveryDB.First(&securityStep, "run_id = ? AND phase = ?", run.ID, "security_universe").Error; err != nil {
		t.Fatal(err)
	}
	if securityStep.Status != DiscoverySyncRunStatusSkipped || securityStep.RecordCount != security.RecordCount {
		t.Fatalf("resumed security step=%#v", securityStep)
	}
}

func TestDiscoverySyncServiceRecoversOnlyStaleRuns(t *testing.T) {
	discoveryDB := testDiscoveryDB(t)
	now := time.Now().UTC()
	stale := discovery.DiscoverySyncRun{Kind: "full", Status: "running", Phase: "market_prescreen", StartedAt: now.Add(-2 * time.Hour), UpdatedAt: now.Add(-2 * time.Hour)}
	if err := discoveryDB.Create(&stale).Error; err != nil {
		t.Fatal(err)
	}
	if err := discoveryDB.Create(&discovery.DiscoverySyncStep{RunID: stale.ID, Sequence: 1, Phase: "market_prescreen", Status: "running", StartedAt: now.Add(-2 * time.Hour)}).Error; err != nil {
		t.Fatal(err)
	}
	recovered, err := NewDiscoverySyncService(discoveryDB, config.DiscoveryConfig{}).RecoverInterruptedRuns(context.Background())
	if err != nil || recovered != 1 {
		t.Fatalf("RecoverInterruptedRuns = %d, %v; want 1, nil", recovered, err)
	}
	if err := discoveryDB.First(&stale, stale.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stale.Status != DiscoverySyncRunStatusFailed || stale.CompletedAt == nil || !strings.Contains(stale.ErrorMessage, "中断") {
		t.Fatalf("stale run = %#v, want recovered failed run", stale)
	}
	fresh := discovery.DiscoverySyncRun{Kind: "full", Status: "running", Phase: "market_prescreen", StartedAt: now, UpdatedAt: now}
	if err := discoveryDB.Create(&fresh).Error; err != nil {
		t.Fatal(err)
	}
	if recovered, err = NewDiscoverySyncService(discoveryDB, config.DiscoveryConfig{}).RecoverInterruptedRuns(context.Background()); err != nil || recovered != 0 {
		t.Fatalf("fresh RecoverInterruptedRuns = %d, %v; want 0, nil", recovered, err)
	}
	var steps []discovery.DiscoverySyncStep
	if err := discoveryDB.Order("run_id ASC").Find(&steps).Error; err != nil {
		t.Fatal(err)
	}
	if len(steps) != 1 || steps[0].Status != "failed" || steps[0].CompletedAt == nil {
		t.Fatalf("steps = %#v, want stale failed", steps)
	}
}

func TestDiscoverySyncServiceRecoversFreshOrphanAtStartup(t *testing.T) {
	discoveryDB := testDiscoveryDB(t)
	now := time.Now().UTC()
	run := discovery.DiscoverySyncRun{Kind: "full", Status: "running", Phase: "security_universe", StartedAt: now, UpdatedAt: now}
	if err := discoveryDB.Create(&run).Error; err != nil {
		t.Fatal(err)
	}
	step := discovery.DiscoverySyncStep{RunID: run.ID, Sequence: 1, Phase: "security-insiders", Status: "running", RecordCount: 12, TotalCount: 50, StartedAt: now}
	if err := discoveryDB.Create(&step).Error; err != nil {
		t.Fatal(err)
	}
	recovered, err := NewDiscoverySyncService(discoveryDB, config.DiscoveryConfig{}).RecoverOrphanedRunsAtStartup(context.Background())
	if err != nil || recovered != 1 {
		t.Fatalf("RecoverOrphanedRunsAtStartup = %d, %v; want 1, nil", recovered, err)
	}
	if err := discoveryDB.First(&run, run.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := discoveryDB.First(&step, step.ID).Error; err != nil {
		t.Fatal(err)
	}
	if run.Status != DiscoverySyncRunStatusFailed || step.Status != "failed" || step.RecordCount != 12 || step.TotalCount != 50 {
		t.Fatalf("run=%#v step=%#v", run, step)
	}
}

type blockingLeaseDiscoveryRunner struct {
	started chan struct{}
	unblock chan struct{}
}

func (r *blockingLeaseDiscoveryRunner) SyncSecurityUniverse(context.Context) (discovery.UniverseBatch, error) {
	close(r.started)
	<-r.unblock
	return discovery.UniverseBatch{BatchID: "security-lease", Kind: discovery.BatchKindSecurity, Status: discovery.BatchStatusPublished}, nil
}

func (r *blockingLeaseDiscoveryRunner) SyncMarketPrices(context.Context) (discovery.UniverseBatch, error) {
	return discovery.UniverseBatch{BatchID: "market-lease", Kind: discovery.BatchKindPrescreen, Status: discovery.BatchStatusPublished}, nil
}

func TestDiscoverySyncServicePersistsOneLeaseAcrossServiceInstances(t *testing.T) {
	discoveryDB := testDiscoveryDB(t)
	runner := &blockingLeaseDiscoveryRunner{started: make(chan struct{}), unblock: make(chan struct{})}
	first := NewDiscoverySyncService(discoveryDB, config.DiscoveryConfig{}).WithRunner(runner)
	second := NewDiscoverySyncService(discoveryDB, config.DiscoveryConfig{}).WithRunner(&fakeDiscoveryRunner{})
	firstDone := make(chan error, 1)
	go func() {
		_, err := first.Run(context.Background())
		firstDone <- err
	}()
	select {
	case <-runner.started:
	case <-time.After(time.Second):
		t.Fatal("first full sync did not start")
	}

	if _, err := second.RunMarketOnly(context.Background()); !errors.Is(err, ErrTaskAlreadyRunning) {
		t.Fatalf("overlapping market sync err=%v, want ErrTaskAlreadyRunning", err)
	}
	var running int64
	if err := discoveryDB.Model(&discovery.DiscoverySyncRun{}).Where("status = ?", "running").Count(&running).Error; err != nil || running != 1 {
		t.Fatalf("running leases=%d err=%v, want 1", running, err)
	}

	close(runner.unblock)
	if err := <-firstDone; err != nil {
		t.Fatalf("first full sync: %v", err)
	}
}

func TestDiscoverySyncServiceIncrementalNeedsBootstrap(t *testing.T) {
	discoveryDB := testDiscoveryDB(t)
	result, err := NewDiscoverySyncService(discoveryDB, config.DiscoveryConfig{}).RunIncremental(context.Background())
	if err != nil {
		t.Fatalf("RunIncremental: %v", err)
	}
	if result.Status != DiscoverySyncStatusNeedsBootstrap || result.Message == "" {
		t.Fatalf("result=%#v, want needs_bootstrap", result)
	}
	var run discovery.DiscoverySyncRun
	if err := discoveryDB.Order("id DESC").First(&run).Error; err != nil {
		t.Fatal(err)
	}
	if run.Kind != "incremental" || run.Status != DiscoverySyncRunStatusSkipped || run.Phase != DiscoverySyncStatusNeedsBootstrap || run.CompletedAt == nil {
		t.Fatalf("run=%#v, want skipped needs_bootstrap", run)
	}
	var steps []discovery.DiscoverySyncStep
	if err := discoveryDB.Where("run_id = ?", run.ID).Order("sequence ASC").Find(&steps).Error; err != nil {
		t.Fatal(err)
	}
	if len(steps) != 2 || steps[0].Sequence != 1 || steps[1].Sequence != 2 || steps[1].Status != DiscoverySyncRunStatusSkipped {
		t.Fatalf("steps=%#v, want ordered prepare/bootstrap steps", steps)
	}
}

func TestDiscoverySyncServiceRunMarketOnlyTableDriven(t *testing.T) {
	tests := []struct {
		name       string
		marketErr  error
		wantStatus string
		wantErr    bool
	}{
		{name: "publishes market batch", wantStatus: DiscoverySyncStatusPublished},
		{name: "reports market failure", marketErr: errors.New("market unavailable"), wantStatus: DiscoverySyncStatusMarketFailed, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			discoveryDB := testDiscoveryDB(t)
			runner := &fakeDiscoveryRunner{
				marketBatch: discovery.UniverseBatch{BatchID: "market", Kind: discovery.BatchKindPrescreen, Status: discovery.BatchStatusPublished, StartedAt: time.Now()},
				marketErr:   tt.marketErr,
			}
			result, err := NewDiscoverySyncService(discoveryDB, config.DiscoveryConfig{}).withRunner(runner).RunMarketOnly(context.Background())
			if (err != nil) != tt.wantErr {
				t.Fatalf("RunMarketOnly err=%v wantErr=%v", err, tt.wantErr)
			}
			if tt.wantErr && !errors.Is(err, ErrDiscoveryMarketSync) {
				t.Fatalf("RunMarketOnly err=%v, want ErrDiscoveryMarketSync", err)
			}
			if result.Status != tt.wantStatus || result.MarketBatchID != "market" || result.SecurityBatchID != "" {
				t.Fatalf("result = %#v, want status=%s market only", result, tt.wantStatus)
			}
			if runner.securityCalls != 0 || runner.marketCalls != 1 {
				t.Fatalf("calls security=%d market=%d", runner.securityCalls, runner.marketCalls)
			}
		})
	}
}

func TestDiscoverySyncServiceForceMarketOnlyRecordsRecoveryKind(t *testing.T) {
	discoveryDB := testDiscoveryDB(t)
	runner := &fakeDiscoveryRunner{marketBatch: discovery.UniverseBatch{BatchID: "market-force", Kind: discovery.BatchKindPrescreen, Status: discovery.BatchStatusPublished, StartedAt: time.Now()}}
	result, err := NewDiscoverySyncService(discoveryDB, config.DiscoveryConfig{}).withRunner(runner).RunMarketOnlyForceLive(context.Background())
	if err != nil || result.Status != DiscoverySyncStatusPublished || runner.securityCalls != 0 || runner.marketCalls != 1 {
		t.Fatalf("result=%#v err=%v calls=%d/%d", result, err, runner.securityCalls, runner.marketCalls)
	}
	var run discovery.DiscoverySyncRun
	if err := discoveryDB.Order("id DESC").First(&run).Error; err != nil {
		t.Fatal(err)
	}
	if run.Kind != "market-force" || run.Status != DiscoverySyncStatusPublished {
		t.Fatalf("run=%#v", run)
	}
}

func TestDiscoverySyncServiceSyncsEnabledWatchTargetDailyPrices(t *testing.T) {
	mainDB := testDB(t)
	discoveryDB := testDiscoveryDB(t)
	if err := mainDB.Create(&[]model.WatchTarget{
		{Ticker: "CBRS", CompanyName: "Cerebras", TargetType: "stock", Status: "enabled"},
		{Ticker: "SKIP", CompanyName: "Disabled", TargetType: "stock", Status: "disabled"},
	}).Error; err != nil {
		t.Fatal(err)
	}
	newYork, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	tradeDate := time.Date(2026, 7, 21, 0, 0, 0, 0, newYork)
	provider := &fakeWatchTargetPriceProvider{records: []discovery.PriceRecord{{Symbol: "CBRS", TradeDate: tradeDate, CloseMicros: 200_000_000, Volume: 123_000, Currency: "USD", Adjusted: true, Source: "longbridge"}}}
	svc := NewDiscoverySyncService(discoveryDB, config.DiscoveryConfig{}).WithWatchTargetDB(mainDB)
	result, err := svc.syncEnabledWatchTargetMarketPricesWithProvider(context.Background(), &stubServiceCalendar{}, provider, time.Date(2026, 7, 21, 21, 30, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if result.TargetCount != 1 || result.RequestedCount != 1 || result.RecordCount != 1 || result.PersistedCount != 1 || result.EffectiveDate != "2026-07-21" {
		t.Fatalf("result = %#v", result)
	}
	if provider.date != "2026-07-21" || len(provider.expected) != 1 || provider.expected[0].Ticker != "CBRS" {
		t.Fatalf("provider date=%s expected=%#v", provider.date, provider.expected)
	}
	history, err := discovery.GetTickerTechnicalHistory(context.Background(), discoveryDB, "CBRS")
	if err != nil || len(history.History) != 1 || history.History[0].CloseUSD != 200 {
		t.Fatalf("history=%#v err=%v", history, err)
	}
	repeated, err := svc.syncEnabledWatchTargetMarketPricesWithProvider(context.Background(), &stubServiceCalendar{}, provider, time.Date(2026, 7, 21, 22, 0, 0, 0, time.UTC))
	if err != nil || !repeated.Skipped || repeated.AlreadyCurrentCount != 1 || provider.calls != 1 {
		t.Fatalf("repeated=%#v err=%v provider calls=%d", repeated, err, provider.calls)
	}
}

func TestDiscoverySyncServiceOptionResearchUsesWatchTargetStatus(t *testing.T) {
	mainDB := testDB(t)
	discoveryDB := testDiscoveryDB(t)
	if err := mainDB.Create(&model.WatchTarget{Ticker: "DISABLED", CompanyName: "Disabled", TargetType: "stock", Status: "disabled"}).Error; err != nil {
		t.Fatal(err)
	}

	// No provider call is expected: the disabled target must be filtered by the
	// persisted status column. Before the regression fix this query referenced
	// a non-existent boolean enabled column and failed before it could skip.
	svc := NewDiscoverySyncService(discoveryDB, config.DiscoveryConfig{
		LongbridgeOptionResearchEnabled:           true,
		LongbridgeWatchTargetOptionResearchBudget: 1,
	}).WithWatchTargetDB(mainDB)
	result, err := svc.SyncEnabledWatchTargetOptionResearch(context.Background())
	if err != nil {
		t.Fatalf("SyncEnabledWatchTargetOptionResearch: %v", err)
	}
	if !result.Skipped || result.Message != "暂无已启用监控标的" {
		t.Fatalf("result=%+v, want disabled target to be skipped", result)
	}
}

func TestWatchTargetMarketSyncFetchesOnlyLocalPriceGaps(t *testing.T) {
	mainDB := testDB(t)
	discoveryDB := testDiscoveryDB(t)
	if err := mainDB.Create(&[]model.WatchTarget{{Ticker: "CACHED", CompanyName: "Cached", TargetType: "stock", Status: "enabled"}, {Ticker: "MISSING", CompanyName: "Missing", TargetType: "stock", Status: "enabled"}}).Error; err != nil {
		t.Fatal(err)
	}
	newYork, _ := time.LoadLocation("America/New_York")
	date := time.Date(2026, 7, 21, 0, 0, 0, 0, newYork)
	if err := discoveryDB.Create(&discovery.PriceSnapshot{Source: "longbridge", SourceVersion: "candidate", Symbol: "CACHED", TradeDate: date, CloseMicros: 10_000_000, Volume: 100_000, Currency: "USD", QualityStatus: discovery.QualityStatusValid}).Error; err != nil {
		t.Fatal(err)
	}
	provider := &fakeWatchTargetPriceProvider{records: []discovery.PriceRecord{{Symbol: "MISSING", TradeDate: date, CloseMicros: 20_000_000, Volume: 100_000, Currency: "USD", Adjusted: true, Source: "longbridge"}}}
	svc := NewDiscoverySyncService(discoveryDB, config.DiscoveryConfig{}).WithWatchTargetDB(mainDB)
	result, err := svc.syncEnabledWatchTargetMarketPricesWithProvider(context.Background(), &stubServiceCalendar{}, provider, time.Date(2026, 7, 21, 21, 30, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if result.AlreadyCurrentCount != 1 || len(provider.expected) != 1 || provider.expected[0].Ticker != "MISSING" {
		t.Fatalf("result=%#v expected=%#v", result, provider.expected)
	}
}

func TestLatestCompletedWatchTargetTradingDateWaitsForClose(t *testing.T) {
	date, trading, err := latestCompletedWatchTargetTradingDate(context.Background(), &stubServiceCalendar{}, time.Date(2026, 7, 21, 19, 0, 0, 0, time.UTC)) // 15:00 New York
	if err != nil || !trading || date.Format(time.DateOnly) != "2026-07-20" {
		t.Fatalf("date=%s trading=%t err=%v", date.Format(time.DateOnly), trading, err)
	}
}

func TestDiscoverySyncServiceBuildsRunnerWithoutMarketURL(t *testing.T) {
	discoveryDB := testDiscoveryDB(t)
	runner, err := NewDiscoverySyncService(discoveryDB, config.DiscoveryConfig{}).buildRunner()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runner.SyncMarketPrices(context.Background()); err == nil || !strings.Contains(err.Error(), "SMALL_CAP_STOOQ_URLS") {
		t.Fatalf("market err = %v, want missing SMALL_CAP_STOOQ_URLS", err)
	}
}

func TestDiscoverySyncServiceBuildsTiingoRunnerFromToken(t *testing.T) {
	discoveryDB := testDiscoveryDB(t)
	runner, err := NewDiscoverySyncService(discoveryDB, config.DiscoveryConfig{
		PriceProvider:  "tiingo",
		TiingoAPIToken: "test-token",
		TiingoBaseURL:  "https://api.tiingo.com",
	}).buildRunner()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runner.SyncMarketPrices(context.Background()); err == nil || strings.Contains(err.Error(), "SMALL_CAP_STOOQ_URLS") {
		t.Fatalf("market err = %v, want tiingo runner without stooq config error", err)
	}
}

func TestDiscoverySyncServiceUsesStoredDiscoveryConfig(t *testing.T) {
	mainDB := testDB(t)
	configs := NewConfigService(mainDB, NewAuditService(mainDB))
	if err := configs.UpsertMany(context.Background(), []ConfigInput{
		{Key: "discovery.price_provider", Value: "tiingo", ValueType: "string", Category: "discovery"},
		{Key: "discovery.tiingo_api_token", Value: "stored-token", ValueType: "string", Category: "discovery", Encrypted: true},
		{Key: "discovery.tiingo_base_url", Value: "https://api.tiingo.com", ValueType: "string", Category: "discovery"},
	}, "tester"); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	discoveryDB := testDiscoveryDB(t)
	runner, err := NewDiscoverySyncService(discoveryDB, config.DiscoveryConfig{}).WithConfigService(configs).buildRunner()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runner.SyncMarketPrices(context.Background()); err == nil || strings.Contains(err.Error(), "SMALL_CAP_STOOQ_URLS") {
		t.Fatalf("market err = %v, want stored tiingo config without stooq config error", err)
	}
}

func TestDiscoverySyncServiceRejectsTiingoWithoutToken(t *testing.T) {
	discoveryDB := testDiscoveryDB(t)
	_, err := NewDiscoverySyncService(discoveryDB, config.DiscoveryConfig{
		PriceProvider:  "tiingo",
		TiingoBaseURL:  "https://api.tiingo.com",
		TaskTimeoutMin: 1,
	}).buildRunner()
	if err == nil || !strings.Contains(err.Error(), "TIINGO_API_TOKEN") {
		t.Fatalf("err = %v, want TIINGO_API_TOKEN error", err)
	}
}

func TestDiscoverySyncServiceBuildSinglePriceProviderTableDriven(t *testing.T) {
	tests := []struct {
		name        string
		cfg         config.DiscoveryConfig
		wantName    string
		wantMarket  string
		wantSetup   string
		wantNilProv bool
	}{
		{
			name:     "builds stooq zip provider",
			cfg:      config.DiscoveryConfig{PriceProvider: "stooq", StooqURLs: []string{"https://prices.test/stooq.zip"}},
			wantName: "stooq",
		},
		{
			name:     "builds yahoo provider",
			cfg:      config.DiscoveryConfig{PriceProvider: "yahoo", YahooBaseURL: "https://query1.finance.yahoo.com", YahooRequestBudget: 10},
			wantName: "yahoo",
		},
		{
			name:        "missing twelvedata key returns setup error",
			cfg:         config.DiscoveryConfig{PriceProvider: "twelvedata", TwelveDataBaseURL: "https://api.twelvedata.com"},
			wantSetup:   "TWELVE_DATA_API_KEY",
			wantNilProv: true,
		},
		{
			name:        "unsupported provider returns setup error",
			cfg:         config.DiscoveryConfig{PriceProvider: "unknown"},
			wantSetup:   "unsupported SMALL_CAP_PRICE_PROVIDER",
			wantNilProv: true,
		},
		{
			name:        "stooq without url returns market error",
			cfg:         config.DiscoveryConfig{PriceProvider: "stooq"},
			wantMarket:  "SMALL_CAP_STOOQ_URLS",
			wantNilProv: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			discoveryDB := testDiscoveryDB(t)
			provider, marketErr, setupErr := NewDiscoverySyncService(discoveryDB, config.DiscoveryConfig{}).buildSinglePriceProvider(tt.cfg, &discovery.Downloader{}, &stubServiceCalendar{})
			if tt.wantMarket != "" {
				if marketErr == nil || !strings.Contains(marketErr.Error(), tt.wantMarket) {
					t.Fatalf("marketErr = %v, want %q", marketErr, tt.wantMarket)
				}
			} else if marketErr != nil {
				t.Fatalf("marketErr = %v", marketErr)
			}
			if tt.wantSetup != "" {
				if setupErr == nil || !strings.Contains(setupErr.Error(), tt.wantSetup) {
					t.Fatalf("setupErr = %v, want %q", setupErr, tt.wantSetup)
				}
			} else if setupErr != nil {
				t.Fatalf("setupErr = %v", setupErr)
			}
			if (provider == nil) != tt.wantNilProv {
				t.Fatalf("provider = %T, want nil=%v", provider, tt.wantNilProv)
			}
			if tt.wantName != "" {
				named, ok := provider.(discovery.NamedPriceProvider)
				if !ok || named.ProviderName() != tt.wantName {
					t.Fatalf("provider name = %T %#v, want %s", provider, provider, tt.wantName)
				}
			}
		})
	}
}

func TestDiscoverySyncServiceBuildPriceProviderChainErrorsWhenNoUsableProvider(t *testing.T) {
	discoveryDB := testDiscoveryDB(t)
	_, marketErr, setupErr := NewDiscoverySyncService(discoveryDB, config.DiscoveryConfig{}).buildPriceProvider(config.DiscoveryConfig{
		PriceProvider: "twelvedata,unknown",
	}, nil, &stubServiceCalendar{})
	if marketErr != nil {
		t.Fatalf("marketErr = %v", marketErr)
	}
	if setupErr == nil || !strings.Contains(setupErr.Error(), "price provider chain has no usable provider") {
		t.Fatalf("setupErr = %v, want no usable provider", setupErr)
	}
}

func TestDiscoverySyncServiceBuildsProviderChain(t *testing.T) {
	discoveryDB := testDiscoveryDB(t)
	provider, marketErr, err := NewDiscoverySyncService(discoveryDB, config.DiscoveryConfig{
		PriceProvider:           "tiingo,twelvedata,yahoo",
		TiingoAPIToken:          "test-token",
		TiingoBaseURL:           "https://api.tiingo.com",
		TwelveDataAPIKey:        "td-key",
		TwelveDataBaseURL:       "https://api.twelvedata.com",
		TwelveDataRequestBudget: 10,
		YahooBaseURL:            "https://query1.finance.yahoo.com",
		TiingoRequestBudget:     10,
		YahooRequestBudget:      10,
	}).buildPriceProvider(config.DiscoveryConfig{
		PriceProvider:           "tiingo,twelvedata,yahoo",
		TiingoAPIToken:          "test-token",
		TiingoBaseURL:           "https://api.tiingo.com",
		TwelveDataAPIKey:        "td-key",
		TwelveDataBaseURL:       "https://api.twelvedata.com",
		TwelveDataRequestBudget: 10,
		YahooBaseURL:            "https://query1.finance.yahoo.com",
		TiingoRequestBudget:     10,
		YahooRequestBudget:      10,
	}, nil, &stubServiceCalendar{})
	if err != nil || marketErr != nil {
		t.Fatalf("buildPriceProvider err=%v marketErr=%v", err, marketErr)
	}
	named, ok := provider.(discovery.NamedPriceProvider)
	if !ok || named.ProviderName() != "chain" {
		t.Fatalf("provider=%T", provider)
	}
	allowlist, ok := provider.(discovery.RecordSourceAllowlistProvider)
	if !ok || strings.Join(allowlist.AllowedRecordSources(), ",") != "tiingo,twelvedata,yahoo" {
		t.Fatalf("allowed sources = %#v", allowlist.AllowedRecordSources())
	}
}
