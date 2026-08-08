package discovery

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestSyncIncrementalListingsCarriesForwardUniverseAndAddsNewIssuer(t *testing.T) {
	db := openMigratedTestDatabase(t)
	now := time.Date(2026, 7, 30, 15, 0, 0, 0, time.UTC)
	previous := UniverseBatch{
		BatchID:            strings.Repeat("a", 64),
		Kind:               BatchKindSecurity,
		Status:             BatchStatusPublished,
		EffectiveDate:      "2026-07-30",
		SourceVersionsJSON: "[]",
		ContentSHA256:      strings.Repeat("b", 64),
		StartedAt:          now.Add(-time.Hour),
		CompletedAt:        &now,
	}
	if err := db.Create(&previous).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&CurrentBatchPointer{Kind: BatchKindSecurity, BatchID: previous.BatchID, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	const existingCount = 120 // exceeds SQLite's default 999 variable limit when inserted as one snapshot statement.
	for i := 0; i < existingCount; i++ {
		cik := fmt.Sprintf("%010d", i+1)
		ticker := fmt.Sprintf("OLD%03d", i)
		existing := Security{CIK: cik}
		if err := db.Create(&existing).Error; err != nil {
			t.Fatal(err)
		}
		for _, row := range []any{
			&SecurityBatchIdentity{BatchID: previous.BatchID, SecurityID: existing.ID, CIK: existing.CIK, Ticker: ticker, ProviderTicker: ticker, Exchange: "Nasdaq", MappingStatus: MappingStatusCurrent, CompanyName: "Old Corp", SIC: 3571, LatestAnnualForm: "10-K", CreatedAt: now},
			&ListingIdentitySnapshot{BatchID: previous.BatchID, SourceKey: ticker, CIK: existing.CIK, Ticker: ticker, ProviderTicker: ticker, Exchange: "Nasdaq", MappingStatus: MappingStatusCurrent, CompanyName: "Old Corp", Included: true, Status: EffectiveStatusIncluded, CreatedAt: now},
			&ClassificationSnapshot{BatchID: previous.BatchID, SecurityID: existing.ID, Included: true, Status: EffectiveStatusIncluded, Confidence: ConfidenceHigh, ReasonCode: ReasonDomesticOperatingCommon, RuleVersion: ClassificationRuleVersion, CreatedAt: now},
			&BatchShareSelection{BatchID: previous.BatchID, SecurityID: existing.ID, QualityStatus: QualityStatusMissing, ReasonCode: ReasonShareFactMissing, CreatedAt: now},
		} {
			if err := db.Create(row).Error; err != nil {
				t.Fatal(err)
			}
		}
	}
	newRecord := SecuritySourceRecord{
		CIK: "0000009999", SourceKey: "NEW", Ticker: "NEW", ProviderTicker: "NEW", Exchange: "Nasdaq",
		CompanyName: "New Corp", SecurityName: "New Corp Common Stock", SIC: 3571, LatestAnnualForm: "10-K",
		RecentForms: []string{"10-K", "10-Q"}, MappingStatus: MappingStatusCurrent,
	}
	shareDate := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	c := &Coordinator{DB: db, Clock: func() time.Time { return now }}
	result, err := c.SyncIncrementalListings(context.Background(), IncrementalListingInput{
		Records:        newRecordAsSlice(newRecord),
		Shares:         []ShareFact{{CIK: newRecord.CIK, Concept: "dei:EntityCommonStockSharesOutstanding", Unit: "shares", Form: "10-Q", Accession: "0000009999-26-000001", Instant: shareDate, FiledAt: shareDate, AcceptedAt: shareDate, Shares: 2_000_000}},
		SourceVersions: []SourceVersion{testSourceVersion("metadata:incremental", "daily", now)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Added != 1 || result.Batch.Status != BatchStatusPublished || result.Batch.BatchID == previous.BatchID {
		t.Fatalf("result = %#v", result)
	}
	var identities []SecurityBatchIdentity
	if err := db.Where("batch_id = ?", result.Batch.BatchID).Order("ticker").Find(&identities).Error; err != nil {
		t.Fatal(err)
	}
	if len(identities) != existingCount+1 || identities[0].Ticker != "NEW" {
		t.Fatalf("identities = %#v", identities)
	}
	var pointer CurrentBatchPointer
	if err := db.First(&pointer, "kind = ?", BatchKindSecurity).Error; err != nil || pointer.BatchID != result.Batch.BatchID {
		t.Fatalf("pointer=%#v err=%v", pointer, err)
	}
}

func TestSyncIncrementalListingsReplacesUnresolvedListingSourceKey(t *testing.T) {
	db := openMigratedTestDatabase(t)
	now := time.Date(2026, 7, 30, 15, 0, 0, 0, time.UTC)
	previous := UniverseBatch{
		BatchID:            strings.Repeat("c", 64),
		Kind:               BatchKindSecurity,
		Status:             BatchStatusPublished,
		EffectiveDate:      "2026-07-30",
		SourceVersionsJSON: "[]",
		ContentSHA256:      strings.Repeat("d", 64),
		StartedAt:          now.Add(-time.Hour),
		CompletedAt:        &now,
	}
	if err := db.Create(&previous).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&CurrentBatchPointer{Kind: BatchKindSecurity, BatchID: previous.BatchID, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&ListingIdentitySnapshot{
		BatchID: previous.BatchID, SourceKey: "NEW", Ticker: "NEW", ProviderTicker: "NEW", Exchange: "Nasdaq",
		MappingStatus: "", CompanyName: "New Corp", Included: false, Status: EffectiveStatusExcluded, CreatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	newRecord := SecuritySourceRecord{
		CIK: "0000009999", SourceKey: "NEW", Ticker: "NEW", ProviderTicker: "NEW", Exchange: "Nasdaq",
		CompanyName: "New Corp", SecurityName: "New Corp Common Stock", SIC: 3571, LatestAnnualForm: "10-K",
		RecentForms: []string{"10-K", "10-Q"}, MappingStatus: MappingStatusCurrent,
	}
	c := &Coordinator{DB: db, Clock: func() time.Time { return now }}
	result, err := c.SyncIncrementalListings(context.Background(), IncrementalListingInput{
		Records:        newRecordAsSlice(newRecord),
		SourceVersions: []SourceVersion{testSourceVersion("metadata:incremental", "daily", now)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Added != 1 || result.Batch.Status != BatchStatusPublished {
		t.Fatalf("result = %#v", result)
	}
	var listings []ListingIdentitySnapshot
	if err := db.Where("batch_id = ?", result.Batch.BatchID).Find(&listings).Error; err != nil {
		t.Fatal(err)
	}
	if len(listings) != 1 || listings[0].SourceKey != "NEW" || listings[0].CIK != newRecord.CIK || listings[0].MappingStatus != MappingStatusCurrent {
		t.Fatalf("listings = %#v", listings)
	}
}

func TestSyncIncrementalListingsRepairsCommonShareWithAttachedWarrant(t *testing.T) {
	db := openMigratedTestDatabase(t)
	now := time.Date(2026, 8, 3, 15, 0, 0, 0, time.UTC)
	previous := UniverseBatch{
		BatchID:            strings.Repeat("e", 64),
		Kind:               BatchKindSecurity,
		Status:             BatchStatusPublished,
		EffectiveDate:      "2026-08-03",
		SourceVersionsJSON: "[]",
		ContentSHA256:      strings.Repeat("f", 64),
		StartedAt:          now.Add(-time.Hour),
		CompletedAt:        &now,
	}
	mustCreate(t, db, &previous)
	mustCreate(t, db, &CurrentBatchPointer{Kind: BatchKindSecurity, BatchID: previous.BatchID, UpdatedAt: now})
	security := Security{CIK: "0001844862"}
	mustCreate(t, db, &security)
	mustCreate(t, db, &SecurityBatchIdentity{BatchID: previous.BatchID, SecurityID: security.ID, CIK: security.CIK, Ticker: "SLDP", ProviderTicker: "SLDP", Exchange: "Nasdaq", MappingStatus: MappingStatusConflict, CompanyName: "Solid Power, Inc.", SIC: 3690, LatestAnnualForm: "10-K", CreatedAt: now})
	mustCreate(t, db, &ClassificationSnapshot{BatchID: previous.BatchID, SecurityID: security.ID, Status: EffectiveStatusDataInsufficient, Confidence: ConfidenceLow, ReasonCode: ReasonMappingConflict, RuleVersion: ClassificationRuleVersion, CreatedAt: now})
	mustCreate(t, db, &BatchShareSelection{BatchID: previous.BatchID, SecurityID: security.ID, QualityStatus: QualityStatusMissing, ReasonCode: ReasonShareFactMissing, CreatedAt: now})
	for _, row := range []ListingIdentitySnapshot{
		{BatchID: previous.BatchID, SourceKey: "SLDP", CIK: security.CIK, Ticker: "SLDP", ProviderTicker: "SLDP", Exchange: "Nasdaq", MappingStatus: MappingStatusCurrent, CompanyName: "Solid Power, Inc.", Status: EffectiveStatusDataInsufficient, ReasonCode: ReasonMappingConflict, CreatedAt: now},
		{BatchID: previous.BatchID, SourceKey: "SLDPW", CIK: security.CIK, Ticker: "SLDPW", ProviderTicker: "SLDPW", Exchange: "Nasdaq", MappingStatus: MappingStatusCurrent, CompanyName: "Solid Power, Inc.", Status: EffectiveStatusDataInsufficient, ReasonCode: ReasonMappingConflict, CreatedAt: now},
	} {
		mustCreate(t, db, &row)
	}
	common := SecuritySourceRecord{CIK: security.CIK, SourceKey: "SLDP", Ticker: "SLDP", ProviderTicker: "SLDP", CompanyName: "Solid Power, Inc.", SecurityName: "Solid Power, Inc. - Class A Common Stock", Exchange: "Nasdaq", SIC: 3690, StateOfIncorporation: "DE", LatestAnnualForm: "10-K", RecentForms: []string{"10-Q"}, MappingStatus: MappingStatusCurrent}
	warrant := common
	warrant.SourceKey, warrant.Ticker, warrant.ProviderTicker = "SLDPW", "SLDPW", "SLDPW"
	warrant.SecurityName = "Solid Power, Inc. - Warrant"
	c := &Coordinator{DB: db, Clock: func() time.Time { return now }}
	plan, err := c.FindNewListings(context.Background(), []SecuritySourceRecord{common, warrant})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Records) != 2 || plan.Records[0].Ticker != "SLDP" || plan.Records[1].Ticker != "SLDPW" || len(plan.ReplaceSecurityIDs) != 1 || plan.ReplaceSecurityIDs[0] != security.ID {
		t.Fatalf("plan=%#v", plan)
	}
	result, err := c.SyncIncrementalListings(context.Background(), IncrementalListingInput{Records: []SecuritySourceRecord{common, warrant}, SourceVersions: []SourceVersion{testSourceVersion("metadata:incremental", "common-warrant", now)}})
	if err != nil || result.Batch.Status != BatchStatusPublished {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	var repaired SecurityBatchIdentity
	if err := db.First(&repaired, "batch_id = ? AND security_id = ?", result.Batch.BatchID, security.ID).Error; err != nil {
		t.Fatal(err)
	}
	if repaired.Ticker != "SLDP" || repaired.MappingStatus != MappingStatusCurrent {
		t.Fatalf("repaired identity=%#v", repaired)
	}
	var warrantListing ListingIdentitySnapshot
	if err := db.First(&warrantListing, "batch_id = ? AND source_key = ?", result.Batch.BatchID, "SLDPW").Error; err != nil {
		t.Fatal(err)
	}
	if warrantListing.Included || warrantListing.ReasonCode != ReasonNonCommonSecurity {
		t.Fatalf("warrant listing=%#v", warrantListing)
	}
}

func TestRepairEvidenceFiltersBatchScopedRowsForReplacedSecurity(t *testing.T) {
	excluded := map[uint]struct{}{7: {}}
	risks := filterBatchCapitalRisks([]CapitalRiskSnapshot{{SecurityID: 7}, {SecurityID: 8}}, excluded)
	if len(risks) != 1 || risks[0].SecurityID != 8 {
		t.Fatalf("risks = %#v", risks)
	}
}

func newRecordAsSlice(record SecuritySourceRecord) []SecuritySourceRecord {
	return []SecuritySourceRecord{record}
}
