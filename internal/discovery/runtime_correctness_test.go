package discovery

import (
	"context"
	"testing"
	"time"
)

func TestExplicitUSIssuerIdentityRejectsTickerCollisions(t *testing.T) {
	for symbol, want := range map[string]string{"STI.US": "ST/US/STI", "SPCX.US": "ST/US/SPCX", "tsla.us": "ST/US/TSLA"} {
		got, err := explicitUSStockCounterID(symbol)
		if err != nil || got != want {
			t.Fatalf("%s: %s %v", symbol, got, err)
		}
	}
	for _, symbol := range []string{"STI.SG", ".SPX.US", "ST/US/STI", "A.B.US", ""} {
		if _, err := explicitUSStockCounterID(symbol); err == nil {
			t.Fatalf("accepted ambiguous identity %s", symbol)
		}
	}
}

func TestIncrementalInsiderLineageRetainsCoverageVersion(t *testing.T) {
	db := openMigratedTestDatabase(t)
	ctx := context.Background()
	full := UniverseBatch{BatchID: "full-lineage", Kind: BatchKindSecurity, SourceVersionsJSON: `[{"source":"insiders:sec-form4","version":"v1+` + InsiderCoverageVersion + `"}]`}
	inc := UniverseBatch{BatchID: "incremental-lineage", Kind: BatchKindSecurity, SourceVersionsJSON: `[{"source":"security-universe:incremental-base","version":"full-lineage"}]`}
	for _, batch := range []UniverseBatch{full, inc} {
		if err := db.Create(&batch).Error; err != nil {
			t.Fatal(err)
		}
	}
	market := UniverseBatch{BatchID: "market-lineage", Kind: BatchKindPrescreen, UniverseSourceVersion: inc.BatchID, SourceVersionsJSON: `[]`}
	if ok, err := candidateInsiderDataAvailable(ctx, db, market); err != nil || !ok {
		t.Fatalf("available=%v err=%v", ok, err)
	}
	if ok, err := candidateInsiderCoverageExpected(ctx, db, market); err != nil || !ok {
		t.Fatalf("coverage expected=%v err=%v", ok, err)
	}
}

func TestResearchRotationIncludesEmptyAndFailedAttempts(t *testing.T) {
	db := openMigratedTestDatabase(t)
	ctx := context.Background()
	now := time.Now().UTC()
	if err := MarkLongbridgeResearchAttempt(ctx, db, LongbridgeRefreshFamilyMarketResearch, "STI", now); err != nil {
		t.Fatal(err)
	}
	last := map[string]time.Time{}
	if err := MergeLongbridgeResearchAttempts(ctx, db, LongbridgeRefreshFamilyMarketResearch, last); err != nil {
		t.Fatal(err)
	}
	if !last["STI"].Equal(now) {
		t.Fatalf("attempt cursor lost: %v", last)
	}
	fresh, err := FreshLongbridgeResearchTickers(ctx, db, LongbridgeRefreshFamilyMarketResearch, now)
	if err != nil || fresh["STI"] {
		t.Fatalf("failed attempt treated as fresh: %v %v", fresh, err)
	}
	if err := MarkLongbridgeResearchSuccess(ctx, db, "eps_no_coverage", "STI", now); err != nil {
		t.Fatal(err)
	}
	if ok, err := longbridgeEPSNoCoverageCached(ctx, db, "STI", now.Add(6*24*time.Hour)); err != nil || !ok {
		t.Fatalf("cache=%v %v", ok, err)
	}
	if ok, err := longbridgeEPSNoCoverageCached(ctx, db, "STI", now.Add(8*24*time.Hour)); err != nil || ok {
		t.Fatalf("expired cache=%v %v", ok, err)
	}
}

func TestCollisionLegacySnapshotsRemainAuditableButNotResearchEvidence(t *testing.T) {
	db := openMigratedTestDatabase(t)
	now := time.Now().UTC()
	for _, row := range []EPSForecastSnapshot{
		{Ticker: "STI", Provider: longbridgeCandidateResearchProvider, SnapshotHash: "legacy", FetchedAt: now},
		{Ticker: "STI", Provider: longbridgeCandidateResearchProvider, SnapshotHash: "verified", IdentityCounterID: "ST/US/STI", FetchedAt: now.Add(-time.Hour)},
	} {
		if err := db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
	research, err := GetCandidateMarketResearch(t.Context(), db, "STI")
	if err != nil {
		t.Fatal(err)
	}
	if len(research.EPSForecast.History) != 1 || research.EPSForecast.Latest.SnapshotHash != "verified" {
		t.Fatalf("legacy collision leaked: %+v", research.EPSForecast)
	}
	var count int64
	if err := db.Model(&EPSForecastSnapshot{}).Where("ticker = ?", "STI").Count(&count).Error; err != nil || count != 2 {
		t.Fatalf("audit evidence lost: %d %v", count, err)
	}
}

func TestInsiderCoverageAgeGatesResearchAndEvidence(t *testing.T) {
	now := time.Now().UTC()
	item := CandidateScoreResult{CandidateScoreSnapshot: CandidateScoreSnapshot{MarketCapUSD: 100000000}, PriceCloseUSD: 12, PriceQualityStatus: QualityStatusValid, PriceFreshnessStatus: PriceFreshnessCurrent}
	metric := FinancialMetricSnapshot{RevenueGrowthAvailable: true, RunwayAvailable: true}
	coverage := candidateInsiderCoverage{coverageStatus: InsiderCoverageCoveredTransactions, checkedAt: now.Add(-11 * 24 * time.Hour)}
	readiness := buildCandidateResearchReadiness(item, metric, true, now.AddDate(0, 0, -60), true, true, true, coverage, now)
	evidence := buildCandidateEvidenceCompleteness(item, metric, true, now.AddDate(0, 0, -60), true, true, true, coverage, now)
	if readiness.Status != CandidateResearchReadinessResearchOnly || !containsString(readiness.Reasons, "insider_coverage_stale") || !containsString(evidence.Reasons, "insider_coverage_stale") {
		t.Fatalf("stale coverage escaped gates: %+v %+v", readiness, evidence)
	}
}

func TestEffectivenessExcludesBackfilledSignalsFromProspectiveWindow(t *testing.T) {
	db := openMigratedTestDatabase(t)
	base := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, -30)
	for i, ticker := range []string{"LIVE", "BACKFILLED"} {
		created := base.Add(24 * time.Hour)
		if i == 1 {
			created = base.Add(10 * 24 * time.Hour)
		}
		row := CandidateSignalEvent{BatchID: ticker, Ticker: ticker, EventType: CandidateSignalEnteredA, Grade: CandidateGradeA, SignalDate: base, BaselineTradeDate: base, BaselineCloseMicros: 1000000, CreatedAt: created}
		if err := db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
	report, err := BuildCandidateEffectiveness(t.Context(), db)
	if err != nil {
		t.Fatal(err)
	}
	if report.Cohorts[0].CandidateCount != 2 || len(report.ProspectiveWindows) != 4 || report.ProspectiveWindows[2].PendingCount != 1 {
		t.Fatalf("backfill counted as prospective: %+v", report)
	}
}
