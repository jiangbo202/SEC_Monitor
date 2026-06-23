package discovery

import (
	"context"
	"testing"
	"time"
)

func TestUniverseQueryReadsOnlyCurrentPublishedBatch(t *testing.T) {
	db := openMigratedTestDatabase(t)
	security := Security{CIK: "0000004321", CompanyName: "Query Co"}
	if err := db.Create(&security).Error; err != nil {
		t.Fatal(err)
	}
	security2 := Security{CIK: "0000004322", CompanyName: "Query Co 2"}
	if err := db.Create(&security2).Error; err != nil {
		t.Fatal(err)
	}
	old := UniverseBatch{BatchID: "old", Kind: BatchKindPrescreen, Status: BatchStatusPublished, StartedAt: time.Now().Add(-time.Hour)}
	current := UniverseBatch{BatchID: "current", Kind: BatchKindPrescreen, Status: BatchStatusPublished, StartedAt: time.Now()}
	draft := UniverseBatch{BatchID: "draft", Kind: BatchKindPrescreen, Status: BatchStatusDraft, StartedAt: time.Now().Add(time.Hour)}
	for _, batch := range []UniverseBatch{old, current, draft} {
		if err := db.Create(&batch).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Create(&CurrentBatchPointer{Kind: BatchKindPrescreen, BatchID: current.BatchID}).Error; err != nil {
		t.Fatal(err)
	}
	rows := []UniverseSnapshot{
		{BatchID: old.BatchID, SecurityID: security.ID, Ticker: "OLD", MarketCapUSD: 900, Status: EffectiveStatusPrescreen, ReasonCode: ReasonQualifiedSmallCap},
		{BatchID: current.BatchID, SecurityID: security.ID, Ticker: "AAA", MarketCapUSD: 100, Status: EffectiveStatusDataInsufficient, ReasonCode: ReasonPriceMissing},
		{BatchID: current.BatchID, SecurityID: security2.ID, Ticker: "BBB", MarketCapUSD: 200, Status: EffectiveStatusPrescreen, ReasonCode: ReasonQualifiedSmallCap},
		{BatchID: draft.BatchID, SecurityID: security.ID, Ticker: "DRAFT", MarketCapUSD: 999, Status: EffectiveStatusPrescreen},
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	page, err := ListUniverse(context.Background(), db, UniverseQuery{Page: 1, PageSize: 1, Status: EffectiveStatusPrescreen})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 || len(page.Items) != 1 || page.Items[0].Ticker != "BBB" {
		t.Fatalf("page = %#v", page)
	}
	reason, err := ListUniverse(context.Background(), db, UniverseQuery{ReasonCode: ReasonPriceMissing})
	if err != nil || reason.Total != 1 || reason.Items[0].Ticker != "AAA" {
		t.Fatalf("reason = %#v err=%v", reason, err)
	}
}

func TestUniverseQueryWithoutPointerIsEmpty(t *testing.T) {
	db := openMigratedTestDatabase(t)
	page, err := ListUniverse(context.Background(), db, UniverseQuery{})
	if err != nil || page.Total != 0 || len(page.Items) != 0 {
		t.Fatalf("page=%#v err=%v", page, err)
	}
}

func TestBatchAndProviderQueriesPaginateFilterAndOrder(t *testing.T) {
	db := openMigratedTestDatabase(t)
	now := time.Date(2026, 6, 23, 9, 0, 0, 0, time.UTC)
	batches := []UniverseBatch{
		{BatchID: "older", Kind: BatchKindSecurity, Status: BatchStatusPublished, StartedAt: now.Add(-time.Hour)},
		{BatchID: "newer", Kind: BatchKindSecurity, Status: BatchStatusPublished, StartedAt: now},
		{BatchID: "failed", Kind: BatchKindPrescreen, Status: BatchStatusFailed, StartedAt: now.Add(time.Hour)},
	}
	if err := db.Create(&batches).Error; err != nil {
		t.Fatal(err)
	}
	page, err := ListBatches(context.Background(), db, BatchQuery{Page: 1, PageSize: 1, Kind: BatchKindSecurity, Status: BatchStatusPublished})
	if err != nil || page.Total != 2 || len(page.Items) != 1 || page.Items[0].BatchID != "newer" {
		t.Fatalf("page=%#v err=%v", page, err)
	}
	runs := []ProviderRun{
		{BatchID: "older", Provider: "p", Status: ProviderStatusActive, CreatedAt: now.Add(-time.Hour)},
		{BatchID: "newer", Provider: "p", Status: ProviderStatusDegraded, CreatedAt: now},
		{BatchID: "failed", Provider: "other", Status: ProviderStatusDegraded, CreatedAt: now.Add(time.Hour)},
	}
	if err := db.Create(&runs).Error; err != nil {
		t.Fatal(err)
	}
	diagnostics, err := ListProviderDiagnostics(context.Background(), db, ProviderRunQuery{Page: 1, PageSize: 1, Provider: "p"})
	if err != nil || diagnostics.Total != 2 || len(diagnostics.Items) != 1 || diagnostics.Items[0].BatchID != "newer" {
		t.Fatalf("diagnostics=%#v err=%v", diagnostics, err)
	}
}
