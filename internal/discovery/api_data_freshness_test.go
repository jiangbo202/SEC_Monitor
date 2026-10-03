package discovery

import (
	"context"
	"testing"
	"time"
)

func TestAPIDataSyncReceiptsKeepNewestAndSeparateProvidersAndCapabilities(t *testing.T) {
	db := openMigratedTestDatabase(t)
	ctx := context.Background()
	now := time.Now().UTC()
	for _, row := range []struct {
		provider, capability, status string
		at                           time.Time
	}{
		{"longbridge", "analyst", "available", now},
		{"longbridge", "analyst", "no_coverage", now.Add(-time.Hour)},
		{"futu", "analyst", "no_coverage", now},
		{"longbridge", "eps", "no_coverage", now},
	} {
		if err := RecordAPIDataSync(ctx, db, row.provider, row.capability, "TEST", row.status, row.at); err != nil {
			t.Fatal(err)
		}
	}
	receipts, err := APIDataSyncReceipts(ctx, db, "longbridge", "analyst")
	if err != nil {
		t.Fatal(err)
	}
	if len(receipts) != 1 || receipts["TEST"].Status != "available" || !receipts["TEST"].LastSuccessAt.Equal(now) {
		t.Fatal(receipts)
	}
	latest := map[string]time.Time{"TEST": now.Add(-30 * 24 * time.Hour)}
	if err := MergeAPIDataSyncTimes(ctx, db, "longbridge", "analyst", latest); err != nil {
		t.Fatal(err)
	}
	if !latest["TEST"].Equal(now) {
		t.Fatal("unchanged consensus did not advance rotation", latest)
	}
}
