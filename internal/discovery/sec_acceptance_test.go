package discovery

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestParseSECSubmissionsFilingMetadata(t *testing.T) {
	body := `{"name":"Acme","cik":1234,"filings":{"recent":{"form":["10-Q","10-Q/A"],"accessionNumber":["0000001234-26-000001","0000001234-26-000002"],"filingDate":["2026-05-01","2026-05-02"],"reportDate":["2026-03-31","2026-03-31"],"acceptanceDateTime":["2026-05-01T12:34:56Z","2026-05-02T12:34:56.123456789Z"]}}}`
	records, err := parseSubmissionEntries(t, []task4ZIPEntry{{"CIK0000001234.json", body}})
	if err != nil {
		t.Fatal(err)
	}
	want := []FilingMetadata{
		{CIK: "0000001234", Accession: "0000001234-26-000001", Form: "10-Q", FiledAt: mustAcceptanceTime(t, "2026-05-01T00:00:00Z"), ReportAt: mustAcceptanceTime(t, "2026-03-31T00:00:00Z"), AcceptedAt: mustAcceptanceTime(t, "2026-05-01T12:34:56Z")},
		{CIK: "0000001234", Accession: "0000001234-26-000002", Form: "10-Q/A", FiledAt: mustAcceptanceTime(t, "2026-05-02T00:00:00Z"), ReportAt: mustAcceptanceTime(t, "2026-03-31T00:00:00Z"), AcceptedAt: mustAcceptanceTime(t, "2026-05-02T12:34:56.123456789Z")},
	}
	if got := records["0000001234"].FilingMetadata; !reflect.DeepEqual(got, want) {
		t.Fatalf("filing metadata = %#v, want %#v", got, want)
	}
}

func TestParseSECSubmissionsAcceptanceContracts(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "acceptance length", body: `{"name":"Acme","cik":1234,"filings":{"recent":{"form":["10-Q","10-Q/A"],"accessionNumber":["a","b"],"acceptanceDateTime":["2026-05-01T12:34:56Z"]}}}`},
		{name: "acceptance without accession", body: `{"name":"Acme","cik":1234,"filings":{"recent":{"form":["10-Q"],"acceptanceDateTime":["2026-05-01T12:34:56Z"]}}}`},
		{name: "empty acceptance", body: `{"name":"Acme","cik":1234,"filings":{"recent":{"form":["10-Q"],"accessionNumber":["a"],"acceptanceDateTime":[""]}}}`},
		{name: "invalid acceptance", body: `{"name":"Acme","cik":1234,"filings":{"recent":{"form":["10-Q"],"accessionNumber":["a"],"acceptanceDateTime":["2026-05-01 12:34:56"]}}}`},
		{name: "empty accession", body: `{"name":"Acme","cik":1234,"filings":{"recent":{"form":["10-Q"],"accessionNumber":[""],"acceptanceDateTime":["2026-05-01T12:34:56Z"]}}}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := parseSubmissionEntries(t, []task4ZIPEntry{{"CIK0000001234.json", test.body}})
			if err == nil {
				t.Fatal("invalid submissions acceptance metadata parsed")
			}
		})
	}
}

func TestParseSECSubmissionsAllowsLegacyMissingAcceptance(t *testing.T) {
	body := `{"name":"Acme","cik":1234,"filings":{"recent":{"form":["10-Q"],"accessionNumber":["0000001234-26-000001"],"filingDate":["2026-05-01"],"reportDate":["2026-03-31"]}}}`
	records, err := parseSubmissionEntries(t, []task4ZIPEntry{{"CIK0000001234.json", body}})
	if err != nil {
		t.Fatal(err)
	}
	metadata := records["0000001234"].FilingMetadata
	if len(metadata) != 1 || !metadata[0].AcceptedAt.IsZero() {
		t.Fatalf("legacy metadata = %#v", metadata)
	}
}

func TestEnrichShareFactsWithAcceptance(t *testing.T) {
	accepted := mustAcceptanceTime(t, "2026-05-01T12:34:56.123Z")
	facts := []ShareFact{
		{CIK: "0000001234", Accession: "a", Shares: 10},
		{CIK: "0000001234", Accession: "unmatched", Shares: 20},
	}
	before := append([]ShareFact(nil), facts...)
	metadata := []FilingMetadata{
		{CIK: "0000001234", Accession: "a", AcceptedAt: accepted},
		{CIK: "0000001234", Accession: "a", AcceptedAt: accepted},
	}
	got, err := EnrichShareFactsWithAcceptance(facts, metadata)
	if err != nil {
		t.Fatal(err)
	}
	if !got[0].AcceptedAt.Equal(accepted) || !got[1].AcceptedAt.IsZero() {
		t.Fatalf("enriched facts = %#v", got)
	}
	if !reflect.DeepEqual(facts, before) {
		t.Fatalf("input facts mutated: got %#v want %#v", facts, before)
	}
	got[0].Shares = 99
	if facts[0].Shares == 99 {
		t.Fatal("output aliases input")
	}
}

func TestEnrichShareFactsRejectsMetadataConflicts(t *testing.T) {
	t1 := mustAcceptanceTime(t, "2026-05-01T12:34:56Z")
	t2 := t1.Add(time.Second)
	tests := []struct {
		name     string
		facts    []ShareFact
		metadata []FilingMetadata
	}{
		{name: "duplicate time", metadata: []FilingMetadata{{CIK: "0000001234", Accession: "a", AcceptedAt: t1}, {CIK: "0000001234", Accession: "a", AcceptedAt: t2}}},
		{name: "duplicate CIK", metadata: []FilingMetadata{{CIK: "0000001234", Accession: "a", AcceptedAt: t1}, {CIK: "0000001235", Accession: "a", AcceptedAt: t1}}},
		{name: "missing accession", metadata: []FilingMetadata{{CIK: "0000001234", AcceptedAt: t1}}},
		{name: "missing CIK", metadata: []FilingMetadata{{Accession: "a", AcceptedAt: t1}}},
		{name: "fact CIK mismatch", facts: []ShareFact{{CIK: "0000001235", Accession: "a"}}, metadata: []FilingMetadata{{CIK: "0000001234", Accession: "a", AcceptedAt: t1}}},
		{name: "existing accepted time", facts: []ShareFact{{CIK: "0000001234", Accession: "a", AcceptedAt: t2}}, metadata: []FilingMetadata{{CIK: "0000001234", Accession: "a", AcceptedAt: t1}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := EnrichShareFactsWithAcceptance(test.facts, test.metadata)
			if err == nil || !strings.Contains(err.Error(), "acceptance metadata") {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func mustAcceptanceTime(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}
