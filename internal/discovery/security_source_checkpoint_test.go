package discovery

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type countingMetadataSource struct {
	records []SecuritySourceRecord
	version SourceVersion
	calls   int
}

func (s *countingMetadataSource) Load(context.Context) ([]SecuritySourceRecord, SourceVersion, error) {
	s.calls++
	return s.records, s.version, nil
}

type checkpointFundamentalsSource struct {
	result SecurityFundamentals
	calls  int
}

func (s *checkpointFundamentalsSource) LoadLatestShares(context.Context, map[string]struct{}) ([]ShareFact, SourceVersion, error) {
	return nil, SourceVersion{}, errors.New("standalone share loader should not be called")
}

func (s *checkpointFundamentalsSource) LoadFinancialFacts(context.Context, map[string]struct{}) ([]FinancialFact, SourceVersion, error) {
	return nil, SourceVersion{}, errors.New("standalone financial loader should not be called")
}

func (s *checkpointFundamentalsSource) LoadSecurityFundamentals(context.Context, map[string]struct{}, []SecuritySourceRecord, SourceVersion) (SecurityFundamentals, error) {
	s.calls++
	return s.result, nil
}

type checkpointInsiderSource struct {
	version SourceVersion
	calls   int
}

func (s *checkpointInsiderSource) LoadInsiderTransactions(context.Context, map[string]struct{}, time.Time) ([]InsiderTransaction, SourceVersion, error) {
	return nil, SourceVersion{}, errors.New("standalone insider loader should not be called")
}

func (s *checkpointInsiderSource) LoadInsiderTransactionsWithMetadata(context.Context, []SecuritySourceRecord, SourceVersion, map[string]struct{}, time.Time) ([]InsiderTransaction, []InsiderCoverage, SourceVersion, error) {
	s.calls++
	return nil, nil, s.version, nil
}

type retryCapitalEventSource struct {
	version SourceVersion
	calls   int
	fail    bool
}

func (s *retryCapitalEventSource) Load(context.Context, map[string]struct{}, time.Time) ([]CapitalEvent, SourceVersion, error) {
	return nil, SourceVersion{}, errors.New("standalone capital-event loader should not be called")
}

func (s *retryCapitalEventSource) LoadWithMetadata(context.Context, []SecuritySourceRecord, SourceVersion, map[string]struct{}, time.Time) ([]CapitalEvent, SourceVersion, error) {
	s.calls++
	if s.fail {
		return nil, SourceVersion{}, errors.New("simulated capital-event interruption")
	}
	return nil, s.version, nil
}

func TestSecuritySourceRetryReusesCompletedAcquisitionArtifacts(t *testing.T) {
	db := openMigratedTestDatabase(t)
	now := time.Date(2026, 8, 21, 10, 0, 0, 0, time.UTC)
	record := SecuritySourceRecord{
		CIK: "0000004321", SourceKey: "RESUME", Ticker: "RESUME", ProviderTicker: "RESUME",
		CompanyName: "Resume Co", SecurityName: "Resume Co Common Stock", Exchange: "Nasdaq",
		SIC: 3571, StateOfIncorporation: "DE", LatestAnnualForm: "10-K", MappingStatus: MappingStatusCurrent,
	}
	metadata := &countingMetadataSource{records: []SecuritySourceRecord{record}, version: testSourceVersion("metadata", "source-resume", now)}
	fundamentals := &checkpointFundamentalsSource{result: SecurityFundamentals{
		ShareVersion:     testSourceVersion("shares", "source-resume", now),
		FinancialVersion: testSourceVersion("financials", "source-resume", now),
	}}
	insiders := &checkpointInsiderSource{version: testSourceVersion("insiders", "source-resume", now)}
	events := &retryCapitalEventSource{version: testSourceVersion("capital-events", "source-resume", now), fail: true}
	coordinator := Coordinator{
		DB: db, Metadata: metadata, Shares: fundamentals, Financials: fundamentals, Insiders: insiders, Events: events,
		Clock: func() time.Time { return now }, SecurityArtifactDir: t.TempDir(), SecurityStageTimeout: time.Minute,
	}

	if _, err := coordinator.SyncSecurityUniverse(context.Background()); err == nil || !strings.Contains(err.Error(), "simulated capital-event interruption") {
		t.Fatalf("first run error=%v", err)
	}
	events.fail = false
	published, err := coordinator.SyncSecurityUniverse(context.Background())
	if err != nil || published.Status != BatchStatusPublished {
		t.Fatalf("retry batch=%#v err=%v", published, err)
	}
	if metadata.calls != 2 {
		t.Fatalf("metadata calls=%d want=2 (metadata is freshness checked each run)", metadata.calls)
	}
	if fundamentals.calls != 1 || insiders.calls != 1 {
		t.Fatalf("completed source stages reran: fundamentals=%d insiders=%d", fundamentals.calls, insiders.calls)
	}
	if events.calls != 2 {
		t.Fatalf("failed source stage calls=%d want=2", events.calls)
	}
	var completed int64
	if err := db.Model(&SecuritySourceCheckpoint{}).Where("status = ?", securityCheckpointCompleted).Count(&completed).Error; err != nil {
		t.Fatal(err)
	}
	if completed < 3 {
		t.Fatalf("completed source checkpoints=%d want at least 3", completed)
	}
}

func TestSecurityUniverseCapitalEventsReuseLoadedMetadata(t *testing.T) {
	db := openMigratedTestDatabase(t)
	now := time.Date(2026, 8, 21, 10, 0, 0, 0, time.UTC)
	record := SecuritySourceRecord{
		CIK: "0000009876", SourceKey: "ONCE", Ticker: "ONCE", ProviderTicker: "ONCE",
		CompanyName: "Once Co", SecurityName: "Once Co Common Stock", Exchange: "Nasdaq",
		SIC: 3571, StateOfIncorporation: "DE", LatestAnnualForm: "10-K", MappingStatus: MappingStatusCurrent,
	}
	metadata := &countingMetadataSource{records: []SecuritySourceRecord{record}, version: testSourceVersion("metadata", "single-load", now)}
	coordinator := Coordinator{
		DB: db, Metadata: metadata,
		Shares: fakeShareSource{version: testSourceVersion("shares", "single-load", now)},
		Events: SECSubmissionsCapitalEventSource{Metadata: metadata}, Clock: func() time.Time { return now },
	}
	batch, err := coordinator.SyncSecurityUniverse(context.Background())
	if err != nil || batch.Status != BatchStatusPublished {
		t.Fatalf("batch=%#v err=%v", batch, err)
	}
	if metadata.calls != 1 {
		t.Fatalf("metadata loads=%d want=1", metadata.calls)
	}
}

func TestSecurityInsiderStageUsesIndependentLongerTimeout(t *testing.T) {
	coordinator := Coordinator{SecurityStageTimeout: time.Minute, SecurityInsiderStageTimeout: 3 * time.Minute}
	regularCtx, regularCancel := coordinator.securitySourceStageContext(context.Background(), "security-fundamentals")
	defer regularCancel()
	insiderCtx, insiderCancel := coordinator.securitySourceStageContext(context.Background(), "security-insiders")
	defer insiderCancel()
	regularDeadline, regularOK := regularCtx.Deadline()
	insiderDeadline, insiderOK := insiderCtx.Deadline()
	if !regularOK || !insiderOK {
		t.Fatal("stage deadlines are missing")
	}
	delta := insiderDeadline.Sub(regularDeadline)
	if delta < 119*time.Second || delta > 121*time.Second {
		t.Fatalf("insider deadline delta=%s want about 2m", delta)
	}
}
