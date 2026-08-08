package discovery

import (
	"context"
	"testing"
	"time"
)

func TestGetCandidateDetailReturnsCurrentEvidence(t *testing.T) {
	db := openMigratedTestDatabase(t)
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	security := Security{CIK: "0000012345", CompanyName: "", SIC: 0, CatalogStatus: SecurityCatalogPublished}
	if err := db.Create(&security).Error; err != nil {
		t.Fatal(err)
	}
	securityBatch := UniverseBatch{BatchID: "security-current", Kind: BatchKindSecurity, Status: BatchStatusPublished, EffectiveDate: "2026-07-21", StartedAt: now.Add(-2 * time.Hour)}
	if err := db.Create(&securityBatch).Error; err != nil {
		t.Fatal(err)
	}
	previous := UniverseBatch{BatchID: "previous", Kind: BatchKindPrescreen, Status: BatchStatusPublished, EffectiveDate: "2026-07-20", StartedAt: now.Add(-time.Hour)}
	if err := db.Create(&previous).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&CandidateScoreSnapshot{BatchID: previous.BatchID, SecurityID: security.ID, Ticker: "DTCO", Grade: CandidateGradeB, EligibleB: true, TotalScore: 80, RevenueGrowthPct: 41, CashRunwayMonths: 15}).Error; err != nil {
		t.Fatal(err)
	}
	batch := UniverseBatch{BatchID: "current", Kind: BatchKindPrescreen, Status: BatchStatusPublished, EffectiveDate: "2026-07-21", StartedAt: now}
	batch.UniverseSourceVersion = securityBatch.BatchID
	if err := db.Create(&batch).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&SecurityBatchIdentity{BatchID: securityBatch.BatchID, SecurityID: security.ID, CIK: security.CIK, Ticker: "DTCO", CompanyName: "Detail Co", SIC: 2834, SICDescription: "BIOLOGICAL PRODUCTS", StateOfIncorporation: "DE", LatestAnnualForm: "10-K", MappingStatus: MappingStatusCurrent, CreatedAt: time.Now()}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&CurrentBatchPointer{Kind: BatchKindPrescreen, BatchID: batch.BatchID}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&UniverseSnapshot{BatchID: batch.BatchID, SecurityID: security.ID, Ticker: "DTCO", MarketCapUSD: 220_000_000, Status: EffectiveStatusPrescreen, QualityStatus: QualityStatusValid, ReasonCode: ReasonQualifiedSmallCap}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&CandidateScoreSnapshot{BatchID: batch.BatchID, SecurityID: security.ID, Ticker: "DTCO", Grade: CandidateGradeA, EligibleA: true, TotalScore: 88, RevenueGrowthPct: 54, CashRunwayMonths: 18, RecentQualifiedInsider: true}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&CandidateSignalEvent{BatchID: batch.BatchID, SecurityID: security.ID, Ticker: "DTCO", Grade: CandidateGradeA, EventType: CandidateSignalEnteredA, TotalScore: 88, SignalDate: now, BaselineTradeDate: now, BaselineCloseMicros: 10_000_000, PriceSource: "test"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&FinancialMetricSnapshot{BatchID: securityBatch.BatchID, SecurityID: security.ID, RevenueGrowthAvailable: true, RunwayAvailable: true, QuarterlyRevenueYoYPct: 54, CashRunwayMonths: 18, QualityFlagsJSON: `["low_revenue_base"]`}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&InsiderTransactionSnapshot{SecurityID: security.ID, Accession: "0001", OwnerName: "CEO", OfficerTitle: "Chief Executive Officer", Role: InsiderRoleCEO, TransactionDate: time.Now().AddDate(0, 0, -5), TransactionCode: "P", AcquiredDisposedCode: "A", Qualified: true, ValueMicros: 1_000_000_000}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&CapitalRiskSnapshot{BatchID: securityBatch.BatchID, SecurityID: security.ID, Kind: CapitalEventATMProgram, Active: true, BlocksA: true, BlocksB: false, Severity: CapitalRiskSeverityHigh, Reason: "ATM program active"}).Error; err != nil {
		t.Fatal(err)
	}
	older := time.Now().AddDate(0, 0, -12)
	newer := time.Now().AddDate(0, 0, -2)
	if err := db.Create(&[]SECFilingSnapshot{
		{SecurityID: security.ID, AccessionNumber: "0000012345-26-000001", FilingType: "10-Q", FilingDate: older, FilingURL: "https://sec.test/older"},
		{SecurityID: security.ID, AccessionNumber: "0000012345-26-000002", FilingType: "8-K", FilingDate: newer, Items: "2.02", FilingURL: "https://sec.test/newer"},
	}).Error; err != nil {
		t.Fatal(err)
	}

	detail, err := GetCandidateDetail(context.Background(), db, "dtco")
	if err != nil {
		t.Fatal(err)
	}
	if detail.Score.Ticker != "DTCO" || detail.Security.CompanyName != "Detail Co" {
		t.Fatalf("detail identity = %#v", detail)
	}
	if detail.Security.SIC != 2834 || detail.Sector.Category != "生物医药" {
		t.Fatalf("detail sector = %#v security=%#v", detail.Sector, detail.Security)
	}
	if detail.CompanyProfile.SICDescription != "BIOLOGICAL PRODUCTS" || detail.CompanyProfile.Exchange != "" || detail.CompanyProfile.Status != "available" {
		t.Fatalf("company profile = %#v", detail.CompanyProfile)
	}
	if detail.Financial == nil || detail.Financial.QuarterlyRevenueYoYPct != 54 {
		t.Fatalf("financial = %#v", detail.Financial)
	}
	if len(detail.Insiders) != 1 || detail.Insiders[0].OwnerName != "CEO" {
		t.Fatalf("insiders = %#v", detail.Insiders)
	}
	if len(detail.CapitalRisks) != 1 || !detail.CapitalRisks[0].BlocksA {
		t.Fatalf("risks = %#v", detail.CapitalRisks)
	}
	if detail.CapitalRiskSummary.TotalEvents != 1 || detail.CapitalRiskSummary.ActiveEvents != 1 || detail.CapitalRiskSummary.HistoricalInactiveCount != 0 {
		t.Fatalf("risk summary = %#v", detail.CapitalRiskSummary)
	}
	if len(detail.RecentFilings) != 2 || detail.RecentFilings[0].AccessionNumber != "0000012345-26-000002" || detail.RecentFilings[0].Ticker != "DTCO" || detail.RecentFilings[0].Title != "8-K — Items 2.02" {
		t.Fatalf("recent filings = %#v", detail.RecentFilings)
	}
	if detail.DataQuality["financial"] != QualityStatusValid || detail.DataQuality["universe"] != QualityStatusValid {
		t.Fatalf("quality = %#v", detail.DataQuality)
	}
	if detail.DataLineage.ScoreBatchID != batch.BatchID || detail.DataLineage.EvidenceBatchID != securityBatch.BatchID {
		t.Fatalf("lineage batches = %#v", detail.DataLineage)
	}
	if item, ok := candidateLineageItem(detail.DataLineage, "financial"); !ok || item.Status != QualityStatusValid || item.Source != "SEC Company Facts → 本地财务指标快照" {
		t.Fatalf("financial lineage = %#v, exists=%v", item, ok)
	}
	if item, ok := candidateLineageItem(detail.DataLineage, "filings"); !ok || item.Status != QualityStatusValid || item.AsOf != newer.UTC().Format(time.DateOnly) {
		t.Fatalf("filing lineage = %#v, exists=%v", item, ok)
	}
	if len(detail.ScoreHistory) != 2 || detail.ScoreHistory[0].BatchID != batch.BatchID || detail.ScoreHistory[0].ScoreDelta == nil || *detail.ScoreHistory[0].ScoreDelta != 8 || detail.ScoreHistory[0].ChangeStatus != "improved" {
		t.Fatalf("score history = %#v", detail.ScoreHistory)
	}
	if len(detail.SignalEvents) != 1 || detail.SignalEvents[0].EventType != CandidateSignalEnteredA {
		t.Fatalf("signal events = %#v", detail.SignalEvents)
	}
}

func candidateLineageItem(lineage CandidateDataLineage, key string) (CandidateLineageItem, bool) {
	for _, item := range lineage.Items {
		if item.Key == key {
			return item, true
		}
	}
	return CandidateLineageItem{}, false
}

func TestSummarizeCandidateCapitalRisksSeparatesInactiveHistory(t *testing.T) {
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	rows := []CapitalRiskSnapshot{
		{Kind: CapitalEventATMProgram, EffectiveAt: now.AddDate(0, 0, -3), Active: true},
		{Kind: CapitalEventWarrants, EffectiveAt: now.AddDate(0, 0, -20), Active: false},
		{Kind: CapitalEventConfirmedFinancing, EffectiveAt: now.AddDate(0, 0, -181), Active: false},
	}
	summary, current := summarizeCandidateCapitalRisks(rows, now)
	if summary.TotalEvents != 3 || summary.ActiveEvents != 1 || summary.RecentInactiveEvents != 1 || summary.HistoricalInactiveCount != 1 {
		t.Fatalf("summary = %#v", summary)
	}
	if len(current) != 2 || !current[0].Active || current[1].Active {
		t.Fatalf("current = %#v", current)
	}
}

func TestGetCandidateDetailMissingTicker(t *testing.T) {
	db := openMigratedTestDatabase(t)
	if _, err := GetCandidateDetail(context.Background(), db, "missing"); err == nil {
		t.Fatalf("expected missing ticker error")
	}
}
