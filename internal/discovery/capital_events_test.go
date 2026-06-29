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
	byAccession := map[string]CapitalEvent{}
	for _, event := range events {
		if !event.ChangesShares || event.AcceptedAt.IsZero() || event.Accession == "" || !strings.Contains(event.Reason, "potential") {
			t.Fatalf("unsafe event=%#v", event)
		}
		byAccession[event.Accession] = event
	}
	if byAccession["0000001234-26-000001"].Kind != CapitalEventConfirmedFinancing {
		t.Fatalf("8-K item 3.02 kind = %s", byAccession["0000001234-26-000001"].Kind)
	}
	if byAccession["0000001234-26-000003"].Kind != CapitalEventRegisteredFinancing {
		t.Fatalf("S-3 kind = %s", byAccession["0000001234-26-000003"].Kind)
	}
	if byAccession["0000001234-26-000006"].Kind != CapitalEventRegisteredFinancing {
		t.Fatalf("424B kind = %s", byAccession["0000001234-26-000006"].Kind)
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

func TestAssessCapitalRisksAppliesValidityWindowsAndGradeBlocks(t *testing.T) {
	now := time.Date(2026, 6, 29, 12, 0, 0, 0, time.UTC)
	events := []CapitalEvent{
		{CIK: "0000001234", Kind: CapitalEventConfirmedFinancing, Accession: "financing", EffectiveAt: now.AddDate(0, 0, -30), AcceptedAt: now.AddDate(0, 0, -29), ChangesShares: true, Reason: "8-K item 3.02"},
		{CIK: "0000001234", Kind: CapitalEventRegisteredFinancing, Accession: "shelf", EffectiveAt: now.AddDate(0, 0, -30), AcceptedAt: now.AddDate(0, 0, -29), ChangesShares: true, Reason: "S-3 shelf"},
		{CIK: "0000001234", Kind: CapitalEventReverseSplit, Accession: "split", EffectiveAt: now.AddDate(0, 0, -120), AcceptedAt: now.AddDate(0, 0, -119), ChangesShares: true, Reason: "8-K item 5.03"},
		{CIK: "0000001234", Kind: CapitalEventGoingConcern, Accession: "going", EffectiveAt: now.AddDate(-1, 0, 0), AcceptedAt: now.AddDate(-1, 0, 1), Reason: "10-K warning"},
		{CIK: "0000001234", Kind: CapitalEventATMProgram, Accession: "old-atm", EffectiveAt: now.AddDate(0, 0, -100), AcceptedAt: now.AddDate(0, 0, -99), ChangesShares: true, Reason: "expired ATM"},
	}

	risks := AssessCapitalRisks(events, now)
	if len(risks) != 5 {
		t.Fatalf("risk count = %d, want 5: %#v", len(risks), risks)
	}
	byAccession := map[string]CapitalRiskAssessment{}
	for _, risk := range risks {
		byAccession[risk.Accession] = risk
	}
	if risk := byAccession["financing"]; !risk.Active || !risk.BlocksA || risk.BlocksB || risk.Severity != CapitalRiskSeverityHigh {
		t.Fatalf("confirmed financing risk = %#v", risk)
	}
	if risk := byAccession["shelf"]; !risk.Active || risk.BlocksA || risk.BlocksB || risk.Severity != CapitalRiskSeverityMedium {
		t.Fatalf("registered financing risk = %#v", risk)
	}
	if risk := byAccession["split"]; !risk.Active || !risk.BlocksA || risk.BlocksB || risk.ActiveUntil.Sub(risk.EffectiveAt) != 365*24*time.Hour {
		t.Fatalf("reverse split risk = %#v", risk)
	}
	if risk := byAccession["going"]; !risk.Active || !risk.BlocksA || !risk.BlocksB || !risk.ActiveUntil.IsZero() {
		t.Fatalf("going concern risk = %#v", risk)
	}
	if risk := byAccession["old-atm"]; risk.Active || !risk.BlocksA {
		t.Fatalf("expired ATM risk = %#v", risk)
	}
}

func TestCoordinatorPersistsCapitalRiskSnapshots(t *testing.T) {
	db := openMigratedTestDatabase(t)
	now := time.Date(2026, 6, 29, 12, 0, 0, 0, time.UTC)
	record := SecuritySourceRecord{CIK: "0000001234", Ticker: "ACME", SecurityName: "Acme Inc", Exchange: "NASDAQ", MappingStatus: MappingStatusCurrent}
	fact := ShareFact{CIK: record.CIK, Concept: "dei:EntityCommonStockSharesOutstanding", Unit: "shares", Shares: 20_000_000, Accession: "shares", Form: "10-Q", Instant: now.AddDate(0, 0, -10), FiledAt: now.AddDate(0, 0, -9), AcceptedAt: now.AddDate(0, 0, -9), SourceURL: "https://www.sec.gov/shares"}
	event := CapitalEvent{CIK: record.CIK, Kind: CapitalEventATMProgram, Accession: "atm", EffectiveAt: now.AddDate(0, 0, -5), AcceptedAt: now.AddDate(0, 0, -4), ChangesShares: true, Reason: "ATM sales agreement"}
	c := Coordinator{
		DB:       db,
		Metadata: fakeMetadataSource{records: []SecuritySourceRecord{record}, version: testSourceVersion("metadata", "risk", now)},
		Shares:   fakeShareSource{facts: []ShareFact{fact}, version: testSourceVersion("shares", "risk", now)},
		Events:   fakeCapitalEventSource{events: []CapitalEvent{event}, version: testSourceVersion("events", "risk", now)},
		Clock:    func() time.Time { return now },
	}

	batch, err := c.SyncSecurityUniverse(context.Background())
	if err != nil || batch.Status != BatchStatusPublished {
		t.Fatalf("batch=%#v err=%v", batch, err)
	}
	var rows []CapitalRiskSnapshot
	if err := db.Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].BatchID != batch.BatchID || rows[0].Kind != CapitalEventATMProgram || !rows[0].Active || !rows[0].BlocksA || rows[0].BlocksB || rows[0].Severity != CapitalRiskSeverityHigh {
		t.Fatalf("risk rows = %#v", rows)
	}
}
