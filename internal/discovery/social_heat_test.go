package discovery

import (
	"context"
	"testing"
	"time"
)

func TestUpsertSocialHeatSnapshotStoresOfflineSignal(t *testing.T) {
	db := openMigratedTestDatabase(t)
	security := Security{CIK: "0000012001", CompanyName: "Social Co", CatalogStatus: SecurityCatalogPublished}
	if err := db.Create(&security).Error; err != nil {
		t.Fatal(err)
	}
	batch := UniverseBatch{BatchID: "social-current", Kind: BatchKindPrescreen, Status: BatchStatusPublished, StartedAt: time.Now()}
	if err := db.Create(&batch).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&CurrentBatchPointer{Kind: BatchKindPrescreen, BatchID: batch.BatchID}).Error; err != nil {
		t.Fatal(err)
	}

	input := SocialHeatSnapshot{BatchID: batch.BatchID, SecurityID: security.ID, Ticker: "SOCL", Provider: "manual", MentionCount: 12, SentimentScore: 0.35, SourceStatus: "manual"}
	if err := UpsertSocialHeatSnapshot(context.Background(), db, input); err != nil {
		t.Fatal(err)
	}
	input.MentionCount = 20
	if err := UpsertSocialHeatSnapshot(context.Background(), db, input); err != nil {
		t.Fatal(err)
	}
	items, err := ListSocialHeatForBatch(context.Background(), db, batch.BatchID)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].MentionCount != 20 || items[0].Provider != "manual" {
		t.Fatalf("items = %#v", items)
	}
}
