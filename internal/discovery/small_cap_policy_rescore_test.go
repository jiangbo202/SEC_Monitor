package discovery

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"
)

func TestPreviewSmallCapPolicyChangeIsReadOnly(t *testing.T) {
	db, active, base := seedSmallCapPolicyProjectionTest(t)
	proposed := active.Policy
	proposed.MarketCapMinUSD = 50_000_000

	var versionsBefore, batchesBefore, scoresBefore int64
	db.Model(&SmallCapPolicyVersion{}).Count(&versionsBefore)
	db.Model(&UniverseBatch{}).Count(&batchesBefore)
	db.Model(&CandidateScoreSnapshot{}).Count(&scoresBefore)
	preview, err := PreviewSmallCapPolicyChange(context.Background(), db, proposed)
	if err != nil {
		t.Fatal(err)
	}
	if preview.BaseBatchID != base.BatchID || preview.Before.InMarketCapScope != 2 || preview.After.InMarketCapScope != 1 {
		t.Fatalf("preview = %#v", preview)
	}
	if preview.ChangedCount != 1 || preview.Changes[0].Ticker != "LOW" || preview.Changes[0].ChangeType != "exited_scope" {
		t.Fatalf("changes = %#v", preview.Changes)
	}
	var versionsAfter, batchesAfter, scoresAfter int64
	db.Model(&SmallCapPolicyVersion{}).Count(&versionsAfter)
	db.Model(&UniverseBatch{}).Count(&batchesAfter)
	db.Model(&CandidateScoreSnapshot{}).Count(&scoresAfter)
	if versionsAfter != versionsBefore || batchesAfter != batchesBefore || scoresAfter != scoresBefore {
		t.Fatalf("preview wrote data: versions %d->%d batches %d->%d scores %d->%d", versionsBefore, versionsAfter, batchesBefore, batchesAfter, scoresBefore, scoresAfter)
	}
}

func TestApplySmallCapPolicyPublishesDerivedBatchAtomically(t *testing.T) {
	db, active, base := seedSmallCapPolicyProjectionTest(t)
	proposed := active.Policy
	proposed.MarketCapMinUSD = 50_000_000
	result, err := ApplySmallCapPolicy(context.Background(), db, active.ID, SmallCapPolicyDraftInput{
		Name: "市值 50M–1B", Description: "test", CreatedBy: "tester", Policy: proposed,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "published" || result.Policy.ID == active.ID || result.Rescore.SourceBatchID != base.BatchID || result.Rescore.PublishedBatchID == "" {
		t.Fatalf("result = %#v", result)
	}
	var pointer CurrentBatchPointer
	if err := db.First(&pointer, "kind = ?", BatchKindPrescreen).Error; err != nil {
		t.Fatal(err)
	}
	if pointer.BatchID != result.Rescore.PublishedBatchID {
		t.Fatalf("pointer = %#v", pointer)
	}
	var derived UniverseBatch
	if err := db.First(&derived, "batch_id = ?", pointer.BatchID).Error; err != nil {
		t.Fatal(err)
	}
	if derived.PolicyVersionID != result.Policy.ID || derived.PolicyContentSHA256 != result.Policy.ContentSHA256 || derived.PolicySnapshotJSON == "" {
		t.Fatalf("derived lineage = %#v", derived)
	}
	var low UniverseSnapshot
	if err := db.Where("batch_id = ? AND ticker = ?", derived.BatchID, "LOW").First(&low).Error; err != nil {
		t.Fatal(err)
	}
	if low.Included || low.ReasonCode != ReasonOutsideMarketCap {
		t.Fatalf("LOW derived row = %#v", low)
	}
	var oldCount, providerCount int64
	db.Model(&UniverseSnapshot{}).Where("batch_id = ?", base.BatchID).Count(&oldCount)
	db.Model(&ProviderRun{}).Where("batch_id = ?", derived.BatchID).Count(&providerCount)
	if oldCount != 2 || providerCount != 1 {
		t.Fatalf("oldCount=%d providerCount=%d", oldCount, providerCount)
	}
	current, err := GetActiveSmallCapPolicy(context.Background(), db)
	if err != nil || current.ID != result.Policy.ID {
		t.Fatalf("active=%#v err=%v", current, err)
	}
}

func TestApplySmallCapPolicyConflictAndBootstrap(t *testing.T) {
	t.Run("conflict", func(t *testing.T) {
		db, active, _ := seedSmallCapPolicyProjectionTest(t)
		proposed := active.Policy
		proposed.MarketCapMinUSD++
		_, err := ApplySmallCapPolicy(context.Background(), db, active.ID+100, SmallCapPolicyDraftInput{Name: "conflict", Policy: proposed})
		if !errors.Is(err, ErrSmallCapPolicyConflict) {
			t.Fatalf("err=%v", err)
		}
	})
	t.Run("needs bootstrap", func(t *testing.T) {
		db := openMigratedTestDatabase(t)
		active, err := GetActiveSmallCapPolicy(context.Background(), db)
		if err != nil {
			t.Fatal(err)
		}
		proposed := active.Policy
		proposed.MarketCapMinUSD++
		result, err := ApplySmallCapPolicy(context.Background(), db, active.ID, SmallCapPolicyDraftInput{Name: "bootstrap", CreatedBy: "tester", Policy: proposed})
		if err != nil {
			t.Fatal(err)
		}
		if result.Status != "needs_bootstrap" || result.Policy.ID == active.ID || result.Rescore.PublishedBatchID != "" {
			t.Fatalf("result=%#v", result)
		}
	})
	t.Run("sync in progress", func(t *testing.T) {
		db, active, _ := seedSmallCapPolicyProjectionTest(t)
		mustCreate(t, db, &DiscoverySyncRun{Kind: "full", Status: "running", Phase: "security_universe", StartedAt: time.Now().UTC()})
		proposed := active.Policy
		proposed.MarketCapMinUSD++
		_, err := ApplySmallCapPolicy(context.Background(), db, active.ID, SmallCapPolicyDraftInput{Name: "busy", Policy: proposed})
		if !errors.Is(err, ErrSmallCapPolicyConflict) {
			t.Fatalf("err=%v", err)
		}
		current, readErr := GetActiveSmallCapPolicy(context.Background(), db)
		if readErr != nil || current.ID != active.ID {
			t.Fatalf("active=%#v err=%v", current, readErr)
		}
	})
}

func TestRollbackSmallCapPolicyCreatesNewVersion(t *testing.T) {
	db, original, _ := seedSmallCapPolicyProjectionTest(t)
	proposed := original.Policy
	proposed.MarketCapMinUSD = 50_000_000
	applied, err := ApplySmallCapPolicy(context.Background(), db, original.ID, SmallCapPolicyDraftInput{Name: "changed", CreatedBy: "tester", Policy: proposed})
	if err != nil {
		t.Fatal(err)
	}
	rolled, err := RollbackSmallCapPolicyWithRescore(context.Background(), db, applied.Policy.ID, original.ID, "tester", "undo")
	if err != nil {
		t.Fatal(err)
	}
	if rolled.Policy.ID == original.ID || rolled.Policy.ID == applied.Policy.ID || rolled.Policy.Version <= applied.Policy.Version {
		t.Fatalf("rollback reused history: original=%#v applied=%#v rolled=%#v", original, applied.Policy, rolled.Policy)
	}
	if rolled.Policy.ContentSHA256 != original.ContentSHA256 {
		t.Fatalf("rollback hash=%s want %s", rolled.Policy.ContentSHA256, original.ContentSHA256)
	}
	var activation SmallCapPolicyActivation
	if err := db.Order("id DESC").First(&activation).Error; err != nil || activation.Action != SmallCapPolicyActivationRollback {
		t.Fatalf("rollback activation=%#v err=%v", activation, err)
	}
}

func seedSmallCapPolicyProjectionTest(t *testing.T) (*gorm.DB, SmallCapPolicyVersion, UniverseBatch) {
	t.Helper()
	db := openMigratedTestDatabase(t)
	active, err := GetActiveSmallCapPolicy(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	securityBatchID := strings.Repeat("a", 64)
	versions, err := normalizeSourceVersions("2026-08-20",
		SourceVersion{Source: BatchKindSecurity, Version: securityBatchID, SHA256: securityBatchID, EffectiveAt: now},
		SourceVersion{Source: "price:test", Version: "v1", SHA256: strings.Repeat("b", 64), EffectiveAt: now},
	)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(versions)
	base := UniverseBatch{
		BatchID: strings.Repeat("c", 64), Kind: BatchKindPrescreen, Status: BatchStatusPublished,
		EffectiveDate: "2026-08-20", SourceVersionsJSON: string(encoded), ContentSHA256: strings.Repeat("d", 64),
		UniverseSourceVersion: securityBatchID, PriceSourceVersion: "v1", StartedAt: now,
	}
	securityBatch := UniverseBatch{
		BatchID: securityBatchID, Kind: BatchKindSecurity, Status: BatchStatusPublished,
		EffectiveDate: "2026-08-20", SourceVersionsJSON: string(encoded), ContentSHA256: strings.Repeat("e", 64), StartedAt: now,
	}
	mustCreate(t, db, &securityBatch)
	mustCreate(t, db, &base)
	mustCreate(t, db, &CurrentBatchPointer{Kind: BatchKindPrescreen, BatchID: base.BatchID, UpdatedAt: now})
	mustCreate(t, db, &ProviderRun{BatchID: base.BatchID, Provider: "test", Status: ProviderStatusActive, SourceVersion: "v1", CreatedAt: now})
	for index, input := range []struct {
		cik, ticker string
		marketCap   int64
	}{{"0000000101", "LOW", 40_000_000}, {"0000000102", "HIGH", 600_000_000}} {
		security := Security{CIK: input.cik, CompanyName: input.ticker, CatalogStatus: SecurityCatalogPublished}
		mustCreate(t, db, &security)
		mustCreate(t, db, &SecurityBatchIdentity{BatchID: securityBatchID, SecurityID: security.ID, CIK: input.cik, Ticker: input.ticker, Exchange: "Nasdaq", MappingStatus: MappingStatusCurrent, SIC: 2834, CreatedAt: now})
		mustCreate(t, db, &FinancialMetricSnapshot{BatchID: securityBatchID, SecurityID: security.ID, CreatedAt: now})
		mustCreate(t, db, &UniverseSnapshot{
			BatchID: base.BatchID, SecurityID: security.ID, Ticker: input.ticker, MarketCapUSD: input.marketCap,
			Included: true, Status: EffectiveStatusPrescreen, ReasonCode: ReasonQualifiedSmallCap, QualityStatus: QualityStatusValid, CreatedAt: now.Add(time.Duration(index) * time.Second),
		})
	}
	return db, active, base
}
