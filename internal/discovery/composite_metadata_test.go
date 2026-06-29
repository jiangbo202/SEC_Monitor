package discovery

import (
	"context"
	"testing"
	"time"
)

func TestCompositeSecurityMetadataSourceStrictlyJoinsAndPreservesEvidence(t *testing.T) {
	now := time.Date(2026, 6, 23, 9, 0, 0, 0, time.UTC)
	nasdaq := fakeMetadataSource{records: []SecuritySourceRecord{{Ticker: "acme", SecurityName: "Acme Common Stock", Exchange: "Nasdaq", ETF: true}}, version: testSourceVersion("nasdaq", "n1", now)}
	sec := fakeMetadataSource{records: []SecuritySourceRecord{{CIK: "0000001234", Ticker: "ACME", CompanyName: "Acme Inc", SIC: 3571, RecentForms: []string{"10-Q"}}}, version: testSourceVersion("sec-bulk", "s1", now)}
	records, version, err := (CompositeSecurityMetadataSource{Nasdaq: nasdaq, SEC: sec}).Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].CIK != "0000001234" || records[0].SecurityName != "Acme Common Stock" || !records[0].ETF || records[0].SIC != 3571 {
		t.Fatalf("records=%#v", records)
	}
	if records[0].MappingVerifiedAt != nil || version.Source != "metadata:composite" || !validSHA256(version.SHA256) {
		t.Fatalf("record/version=%#v %#v", records[0], version)
	}
}

func TestCompositeSecurityMetadataSourceStagesUnmappedAndConflict(t *testing.T) {
	now := time.Date(2026, 6, 23, 9, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name        string
		nasdaq, sec []SecuritySourceRecord
	}{
		{"unmapped", []SecuritySourceRecord{{Ticker: "MISS"}}, nil},
		{"SEC duplicate", []SecuritySourceRecord{{Ticker: "DUP"}}, []SecuritySourceRecord{{Ticker: "DUP", CIK: "0000000001"}, {Ticker: "DUP", CIK: "0000000002"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := CompositeSecurityMetadataSource{Nasdaq: fakeMetadataSource{records: tc.nasdaq, version: testSourceVersion("nasdaq", "n", now)}, SEC: fakeMetadataSource{records: tc.sec, version: testSourceVersion("sec-bulk", "s", now)}}
			records, _, err := s.Load(context.Background())
			if err != nil || len(records) != 1 || records[0].CIK != "" || records[0].MappingStatus != MappingStatusConflict || records[0].EvidenceJSON == "" {
				t.Fatalf("records=%#v err=%v", records, err)
			}
		})
	}
}

func TestCompositeSecurityMetadataSourceUsesExactVersionedTickerMapping(t *testing.T) {
	now := time.Date(2026, 6, 23, 9, 0, 0, 0, time.UTC)
	nasdaq := fakeMetadataSource{records: []SecuritySourceRecord{{Ticker: "BRK.B"}}, version: testSourceVersion("nasdaq", "n", now)}
	sec := fakeMetadataSource{records: []SecuritySourceRecord{{Ticker: "BRK-B", CIK: "0000001067"}}, version: testSourceVersion("sec-bulk", "s", now)}
	records, _, err := (CompositeSecurityMetadataSource{Nasdaq: nasdaq, SEC: sec}).Load(context.Background())
	if err != nil || records[0].CIK != "" || records[0].MappingStatus != MappingStatusConflict {
		t.Fatalf("records=%#v err=%v", records, err)
	}
}

func TestCompositeSecurityMetadataSourceUsesOfficialProviderTicker(t *testing.T) {
	now := time.Date(2026, 6, 23, 9, 0, 0, 0, time.UTC)
	nasdaq := fakeMetadataSource{records: []SecuritySourceRecord{{Ticker: "BRK.B", ProviderTicker: "BRK-B"}}, version: testSourceVersion("nasdaq", "n", now)}
	sec := fakeMetadataSource{records: []SecuritySourceRecord{{Ticker: "BRK-B", CIK: "0000001067"}}, version: testSourceVersion("sec-bulk", "s", now)}
	records, _, err := (CompositeSecurityMetadataSource{Nasdaq: nasdaq, SEC: sec}).Load(context.Background())
	if err != nil || len(records) != 1 || records[0].CIK != "0000001067" || records[0].Ticker != "BRK.B" || records[0].ProviderTicker != "BRK-B" {
		t.Fatalf("records=%#v err=%v", records, err)
	}
}

func TestCompositeSecurityMetadataSourceRejectsConflictingExactKeys(t *testing.T) {
	now := time.Date(2026, 6, 23, 9, 0, 0, 0, time.UTC)
	nasdaq := fakeMetadataSource{records: []SecuritySourceRecord{{Ticker: "X.A", ProviderTicker: "X-A"}}, version: testSourceVersion("nasdaq", "n", now)}
	sec := fakeMetadataSource{records: []SecuritySourceRecord{{Ticker: "X.A", CIK: "0000000001"}, {Ticker: "X-A", CIK: "0000000002"}}, version: testSourceVersion("sec-bulk", "s", now)}
	records, _, err := (CompositeSecurityMetadataSource{Nasdaq: nasdaq, SEC: sec}).Load(context.Background())
	if err != nil || len(records) != 1 || records[0].MappingStatus != MappingStatusConflict || records[0].CIK != "" {
		t.Fatalf("records=%#v err=%v", records, err)
	}
}

func TestCompositeSecurityMetadataSourceVerifiesDeSPACOnlyWithPostCombinationEvidence(t *testing.T) {
	now := time.Date(2026, 6, 23, 9, 0, 0, 0, time.UTC)
	completed := now.Add(-48 * time.Hour)
	observed := now.Add(-24 * time.Hour)
	secRecord := SecuritySourceRecord{Ticker: "NEW", CIK: "0000004321", CompanyName: "New Company", HasBusinessCombinationItem201: true, BusinessCombinationCompletedAt: &completed}
	makeSource := func(at *time.Time) CompositeSecurityMetadataSource {
		return CompositeSecurityMetadataSource{
			Nasdaq:           fakeMetadataSource{records: []SecuritySourceRecord{{Ticker: "NEW", CompanyName: "New Company", ObservedAt: at}}, version: testSourceVersion("nasdaq", "n", now)},
			SEC:              fakeMetadataSource{records: []SecuritySourceRecord{secRecord}, version: testSourceVersion("sec-bulk", "s", now)},
			IdentityVerifier: ObservedIdentityVerificationSource{},
		}
	}
	records, _, err := makeSource(nil).Load(context.Background())
	if err != nil || records[0].MappingVerifiedAt != nil {
		t.Fatalf("ordinary observation records=%#v err=%v", records, err)
	}
	records, _, err = makeSource(&observed).Load(context.Background())
	if err != nil || records[0].MappingVerifiedAt == nil || !records[0].MappingVerifiedAt.Equal(observed) {
		t.Fatalf("verified records=%#v err=%v", records, err)
	}
}
