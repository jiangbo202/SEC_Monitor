package discovery

import (
	"testing"
	"time"
)

func TestSelectSharesDeterministicSelection(t *testing.T) {
	asOf := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	instant := time.Date(2026, 3, 31, 0, 0, 0, 0, time.UTC)
	accepted := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	base := ShareFact{CIK: "0000000001", Concept: "dei:EntityCommonStockSharesOutstanding", Unit: "shares", Form: "10-Q", Accession: "0001", Instant: instant, FiledAt: accepted, AcceptedAt: accepted, Shares: 40_000_000, SourceURL: "https://www.sec.gov/Archives/edgar/data/1/0001/xbrl.zip"}

	tests := []struct {
		name       string
		facts      []ShareFact
		wantShares int64
		wantStatus string
		wantReason string
	}{
		{name: "DEI priority", facts: []ShareFact{withShare(base, "us-gaap:CommonStockSharesOutstanding", "0002", 41_000_000), base}, wantShares: 40_000_000, wantStatus: QualityStatusValid, wantReason: ReasonShareSelected},
		{name: "us gaap fallback", facts: []ShareFact{withShare(base, "us-gaap:CommonStockSharesOutstanding", "0002", 41_000_000)}, wantShares: 41_000_000, wantStatus: QualityStatusValid, wantReason: ReasonShareSelected},
		{name: "newer instant wins", facts: []ShareFact{withInstant(base, instant.AddDate(0, -3, 0), "old", 35_000_000), base}, wantShares: 40_000_000, wantStatus: QualityStatusValid, wantReason: ReasonShareSelected},
		{name: "amendment accepted later replaces original", facts: []ShareFact{base, func() ShareFact {
			f := base
			f.Form = "10-Q/A"
			f.Accession = "0003"
			f.Shares = 42_000_000
			f.AcceptedAt = accepted.Add(time.Hour)
			f.FiledAt = accepted
			return f
		}()}, wantShares: 42_000_000, wantStatus: QualityStatusValid, wantReason: ReasonShareSelected},
		{name: "accepted tie with conflicting values", facts: []ShareFact{base, withShare(base, base.Concept, "0002", 41_000_000)}, wantStatus: QualityStatusConflict, wantReason: ReasonShareFactConflict},
		{name: "missing accepted cannot order conflicting accessions", facts: []ShareFact{func() ShareFact { f := base; f.AcceptedAt = time.Time{}; return f }(), func() ShareFact {
			f := withShare(base, base.Concept, "0002", 41_000_000)
			f.AcceptedAt = time.Time{}
			return f
		}()}, wantStatus: QualityStatusConflict, wantReason: ReasonShareAcceptedAtMissing},
		{name: "exact duplicates are harmless", facts: []ShareFact{base, base}, wantShares: 40_000_000, wantStatus: QualityStatusValid, wantReason: ReasonShareSelected},
		{name: "future facts ignored", facts: []ShareFact{base, withInstant(base, asOf.AddDate(0, 0, 1), "future", 99_000_000)}, wantShares: 40_000_000, wantStatus: QualityStatusValid, wantReason: ReasonShareSelected},
		{name: "invalid facts ignored", facts: []ShareFact{{}, withShare(base, "us-gaap:WeightedAverageNumberOfSharesOutstandingBasic", "bad", 99), func() ShareFact { f := base; f.Unit = "USD"; f.Accession = "bad2"; return f }(), base}, wantShares: 40_000_000, wantStatus: QualityStatusValid, wantReason: ReasonShareSelected},
		{name: "empty", wantStatus: QualityStatusMissing, wantReason: ReasonShareFactMissing},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			before := append([]ShareFact(nil), test.facts...)
			got := SelectShareSnapshot(test.facts, nil, asOf)
			if got.QualityStatus != test.wantStatus || got.ReasonCode != test.wantReason {
				t.Fatalf("SelectShareSnapshot = %#v, want status=%q reason=%q", got, test.wantStatus, test.wantReason)
			}
			if test.wantShares > 0 && (got.Fact == nil || got.Fact.Shares != test.wantShares) {
				t.Fatalf("selected fact = %#v, want shares=%d", got.Fact, test.wantShares)
			}
			for i := range before {
				if before[i] != test.facts[i] {
					t.Fatalf("input mutated at %d", i)
				}
			}
		})
	}
}

func TestSelectSharesQualityBoundariesAndEvents(t *testing.T) {
	asOf := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	fact := ShareFact{CIK: "0000000001", Concept: "dei:EntityCommonStockSharesOutstanding", Unit: "shares", Form: "10-Q", Accession: "0001", Instant: asOf.AddDate(0, 0, -150), FiledAt: asOf.AddDate(0, 0, -10), AcceptedAt: asOf.AddDate(0, 0, -10).Add(time.Hour), Shares: 40_000_000, SourceURL: "https://www.sec.gov/Archives/edgar/data/1/0001/a.htm"}
	tests := []struct {
		name           string
		fact           ShareFact
		events         []CapitalEvent
		status, reason string
	}{
		{name: "150 days valid", fact: fact, status: QualityStatusValid, reason: ReasonShareSelected},
		{name: "over 150 days stale", fact: func() ShareFact { f := fact; f.Instant = f.Instant.Add(-time.Second); return f }(), status: QualityStatusStale, reason: ReasonShareFactStale},
		{name: "post instant financing", fact: fact, events: []CapitalEvent{{CIK: fact.CIK, Kind: "financing", Accession: "8k1", EffectiveAt: fact.Instant.Add(time.Hour), ChangesShares: true}}, status: QualityStatusConflict, reason: ReasonShareCapitalEvent},
		{name: "event covered by newer fact", fact: fact, events: []CapitalEvent{{CIK: fact.CIK, Kind: "issuance", Accession: "8k1", EffectiveAt: fact.Instant.Add(-time.Hour), ChangesShares: true}}, status: QualityStatusValid, reason: ReasonShareSelected},
		{name: "other issuer event ignored", fact: fact, events: []CapitalEvent{{CIK: "0000000002", Kind: "ATM", Accession: "8k1", EffectiveAt: fact.Instant.Add(time.Hour), ChangesShares: true}}, status: QualityStatusValid, reason: ReasonShareSelected},
		{name: "multiple classes", fact: fact, events: []CapitalEvent{{CIK: fact.CIK, Kind: "multiple_class", EffectiveAt: asOf}}, status: QualityStatusConflict, reason: ReasonShareMultipleClasses},
		{name: "split mismatch", fact: fact, events: []CapitalEvent{{CIK: fact.CIK, Kind: "reverse_split", EffectiveAt: fact.Instant.Add(time.Hour), ChangesShares: true}}, status: QualityStatusConflict, reason: ReasonShareSplitMismatch},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := SelectShareSnapshot([]ShareFact{test.fact}, test.events, asOf)
			if got.QualityStatus != test.status || got.ReasonCode != test.reason {
				t.Fatalf("got %#v, want status=%q reason=%q", got, test.status, test.reason)
			}
		})
	}
}

func TestSelectSharesRejectsIncompleteAndUnsupportedFacts(t *testing.T) {
	asOf := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	valid := ShareFact{CIK: "0000000001", Concept: "dei:EntityCommonStockSharesOutstanding", Unit: "shares", Form: "10-K", Accession: "a", Instant: asOf.AddDate(0, -1, 0), FiledAt: asOf.AddDate(0, 0, -2), AcceptedAt: asOf.AddDate(0, 0, -2), Shares: 1, SourceURL: "https://www.sec.gov/a"}
	mutations := []func(*ShareFact){
		func(f *ShareFact) { f.CIK = "" }, func(f *ShareFact) { f.Accession = "" }, func(f *ShareFact) { f.SourceURL = "" },
		func(f *ShareFact) { f.Shares = 0 }, func(f *ShareFact) { f.Unit = "pure" }, func(f *ShareFact) { f.Form = "8-K" },
		func(f *ShareFact) { f.Concept = "us-gaap:WeightedAverageNumberOfDilutedSharesOutstanding" },
		func(f *ShareFact) { f.FiledAt = asOf.Add(time.Second) }, func(f *ShareFact) { f.AcceptedAt = asOf.Add(time.Second) },
	}
	for i, mutate := range mutations {
		fact := valid
		mutate(&fact)
		got := SelectShareSnapshot([]ShareFact{fact}, nil, asOf)
		if got.QualityStatus != QualityStatusMissing {
			t.Fatalf("mutation %d got %#v", i, got)
		}
	}
}

func withShare(base ShareFact, concept, accession string, shares int64) ShareFact {
	base.Concept, base.Accession, base.Shares = concept, accession, shares
	return base
}
func withInstant(base ShareFact, instant time.Time, accession string, shares int64) ShareFact {
	base.Instant, base.Accession, base.Shares = instant, accession, shares
	return base
}
