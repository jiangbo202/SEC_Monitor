package discovery

import (
	"context"
	"strings"
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

func TestCompositeSecurityMetadataSourceFailsClosedOnUnmappedOrConflict(t *testing.T) {
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
			if _, _, err := s.Load(context.Background()); err == nil || !strings.Contains(err.Error(), "ticker") {
				t.Fatalf("err=%v", err)
			}
		})
	}
}
