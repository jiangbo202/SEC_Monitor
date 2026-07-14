package discovery

import (
	"context"
	"testing"
	"time"
)

type fakeTechnicalHistoryProvider struct {
	records []PriceRecord
	called  []Listing
}

func (p *fakeTechnicalHistoryProvider) Load(context.Context, []Listing) ([]PriceRecord, ProviderResult, error) {
	return nil, ProviderResult{}, nil
}

func (p *fakeTechnicalHistoryProvider) LoadHistory(_ context.Context, listings []Listing, _ string, _ int) ([]PriceRecord, error) {
	p.called = append([]Listing(nil), listings...)
	return append([]PriceRecord(nil), p.records...), nil
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
	records := make([]PriceRecord, 0, technicalMinimumSamples)
	for index := 0; index < technicalMinimumSamples; index++ {
		records = append(records, PriceRecord{Symbol: "HIST", Source: "fake", TradeDate: base.AddDate(0, 0, index), CloseMicros: 1_000_000 + int64(index), Volume: 100, Currency: "USD"})
	}
	provider := &fakeTechnicalHistoryProvider{records: records}
	result, err := BackfillCandidateTechnicalHistory(context.Background(), db, provider, time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC), 35)
	if err != nil {
		t.Fatal(err)
	}
	if result.CandidateCount != 1 || result.RequestedCount != 1 || result.PersistedCount != technicalMinimumSamples {
		t.Fatalf("result = %+v", result)
	}
	if len(provider.called) != 1 || provider.called[0].ProviderTicker != "HIST.P" {
		t.Fatalf("listings = %+v", provider.called)
	}
	var count int64
	if err := db.Model(&PriceSnapshot{}).Where("symbol = ?", "HIST").Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != technicalMinimumSamples {
		t.Fatalf("history rows = %d, want %d", count, technicalMinimumSamples)
	}
	second, err := BackfillCandidateTechnicalHistory(context.Background(), db, provider, time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC), 35)
	if err != nil {
		t.Fatal(err)
	}
	if second.AlreadyReadyCount != 1 || second.RequestedCount != 0 {
		t.Fatalf("second result = %+v", second)
	}
}
