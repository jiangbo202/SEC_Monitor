package discovery

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestSECSubmissionsCapitalEventSourceConservativelyMapsFilings(t *testing.T) {
	now := time.Date(2026, 6, 23, 12, 0, 0, 0, time.UTC)
	filed := now.Add(-24 * time.Hour)
	accepted := filed.Add(time.Hour)
	record := SecuritySourceRecord{CIK: "0000001234", FilingMetadata: []FilingMetadata{
		{CIK: "0000001234", Accession: "0000001234-26-000001", Form: "8-K", Items: "3.02", FiledAt: filed, AcceptedAt: accepted},
		{CIK: "0000001234", Accession: "0000001234-26-000002", Form: "8-K", Items: "5.03", FiledAt: filed, AcceptedAt: accepted},
		{CIK: "0000001234", Accession: "0000001234-26-000003", Form: "S-3ASR", FiledAt: filed, AcceptedAt: accepted},
		{CIK: "0000001234", Accession: "0000001234-26-000004", Form: "10-Q", FiledAt: filed, AcceptedAt: accepted},
		{CIK: "0000001234", Accession: "0000001234-26-000005", Form: "F-1/A", FiledAt: filed, AcceptedAt: accepted},
		{CIK: "0000001234", Accession: "0000001234-26-000006", Form: "424B3", FiledAt: filed, AcceptedAt: accepted},
		{CIK: "0000001234", Accession: "0000001234-26-000007", Form: "EFFECT", FiledAt: filed, AcceptedAt: accepted},
		{CIK: "0000001234", Accession: "0000001234-26-000008", Form: "POS AM", FiledAt: filed, AcceptedAt: accepted},
	}}
	source := SECSubmissionsCapitalEventSource{Metadata: fakeMetadataSource{records: []SecuritySourceRecord{record}, version: testSourceVersion("sec-submissions", "v1", now)}}
	events, version, err := source.Load(context.Background(), map[string]struct{}{"0000001234": {}}, now)
	if err != nil || len(events) != 7 || version.Source != "capital-events:sec-submissions" || !strings.Contains(version.Version, CapitalRiskPolicyVersion) || !validSHA256(version.SHA256) {
		t.Fatalf("events=%#v version=%#v err=%v", events, version, err)
	}
	for _, event := range events {
		if !event.ChangesShares || event.AcceptedAt.IsZero() || event.Accession == "" || !strings.Contains(event.Reason, "potential") {
			t.Fatalf("unsafe event=%#v", event)
		}
	}
}

func TestCoordinatorRejectsDisabledCapitalEventsSourceByDefault(t *testing.T) {
	db := openMigratedTestDatabase(t)
	now := time.Date(2026, 6, 23, 12, 0, 0, 0, time.UTC)
	c := Coordinator{DB: db, Metadata: fakeMetadataSource{}, Shares: fakeShareSource{}, Events: NoCapitalEventsSource{Version: testSourceVersion("capital-events", "disabled", now)}, Clock: func() time.Time { return now }}
	batch, err := c.SyncSecurityUniverse(context.Background())
	if err == nil || batch.Status != BatchStatusFailed {
		t.Fatalf("batch=%#v err=%v", batch, err)
	}
}
