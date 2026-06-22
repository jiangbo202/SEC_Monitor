package discovery

import (
	"reflect"
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
		wantAccn   string
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
		{name: "single fact missing accepted", facts: []ShareFact{func() ShareFact { f := base; f.AcceptedAt = time.Time{}; return f }()}, wantStatus: QualityStatusMissing, wantReason: ReasonShareAcceptedAtMissing},
		{name: "same shares missing accepted", facts: []ShareFact{func() ShareFact { f := base; f.AcceptedAt = time.Time{}; return f }(), func() ShareFact {
			f := withShare(base, base.Concept, "0002", base.Shares)
			f.AcceptedAt = time.Time{}
			return f
		}()}, wantStatus: QualityStatusMissing, wantReason: ReasonShareAcceptedAtMissing},
		{name: "exact duplicates missing accepted", facts: []ShareFact{func() ShareFact { f := base; f.AcceptedAt = time.Time{}; return f }(), func() ShareFact { f := base; f.AcceptedAt = time.Time{}; return f }()}, wantStatus: QualityStatusMissing, wantReason: ReasonShareAcceptedAtMissing},
		{name: "all highest accepted ties must agree", facts: []ShareFact{func() ShareFact { f := withShare(base, base.Concept, "0001", 10); return f }(), func() ShareFact {
			f := withShare(base, base.Concept, "0002", 10)
			return f
		}(), func() ShareFact {
			f := withShare(base, base.Concept, "0003", 20)
			return f
		}()}, wantStatus: QualityStatusConflict, wantReason: ReasonShareFactConflict},
		{name: "same accepted value chooses lowest accession", facts: []ShareFact{withShare(base, base.Concept, "0002", base.Shares), withShare(base, base.Concept, "0001", base.Shares)}, wantShares: base.Shares, wantAccn: "0001", wantStatus: QualityStatusValid, wantReason: ReasonShareSelected},
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
			if test.wantAccn != "" && (got.Fact == nil || got.Fact.Accession != test.wantAccn) {
				t.Fatalf("selected fact = %#v, want accession=%q", got.Fact, test.wantAccn)
			}
			for i := range before {
				if before[i] != test.facts[i] {
					t.Fatalf("input mutated at %d", i)
				}
			}
		})
	}
}

func TestSelectSharesExcludesFactsAcceptedAfterAsOf(t *testing.T) {
	asOf := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	fact := ShareFact{
		CIK: "0000000001", Concept: "dei:EntityCommonStockSharesOutstanding", Unit: "shares", Form: "10-Q", Accession: "0001",
		Instant: asOf.AddDate(0, -1, 0), FiledAt: asOf.Add(-time.Hour), AcceptedAt: asOf.Add(time.Nanosecond), Shares: 40_000_000, SourceURL: "https://www.sec.gov/a",
	}
	got := SelectShareSnapshot([]ShareFact{fact}, nil, asOf)
	if got.QualityStatus != QualityStatusMissing || got.ReasonCode != ReasonShareFactMissing {
		t.Fatalf("SelectShareSnapshot = %#v, want missing before SEC acceptance", got)
	}
}

func TestSelectSharesQualityBoundariesAndEvents(t *testing.T) {
	asOf := time.Date(2026, 6, 1, 23, 59, 59, 0, time.UTC)
	fact := ShareFact{CIK: "0000000001", Concept: "dei:EntityCommonStockSharesOutstanding", Unit: "shares", Form: "10-Q", Accession: "0001", Instant: asOf.AddDate(0, 0, -150), FiledAt: asOf.AddDate(0, 0, -10), AcceptedAt: asOf.AddDate(0, 0, -10).Add(time.Hour), Shares: 40_000_000, SourceURL: "https://www.sec.gov/Archives/edgar/data/1/0001/a.htm"}
	accepted := asOf.AddDate(0, 0, -1)
	tests := []struct {
		name           string
		fact           ShareFact
		events         []CapitalEvent
		status, reason string
	}{
		{name: "150 days valid", fact: fact, status: QualityStatusValid, reason: ReasonShareSelected},
		{name: "151st civil day stale", fact: func() ShareFact { f := fact; f.Instant = f.Instant.AddDate(0, 0, -1); return f }(), status: QualityStatusStale, reason: ReasonShareFactStale},
		{name: "future civil date fails closed", fact: func() ShareFact { f := fact; f.Instant = asOf.Add(time.Hour); return f }(), status: QualityStatusMissing, reason: ReasonShareFactMissing},
		{name: "post instant financing", fact: fact, events: []CapitalEvent{{CIK: fact.CIK, Kind: "financing", Accession: "8k1", EffectiveAt: fact.Instant.Add(time.Hour), AcceptedAt: accepted, ChangesShares: true}}, status: QualityStatusConflict, reason: ReasonShareCapitalEvent},
		{name: "event covered by newer fact", fact: fact, events: []CapitalEvent{{CIK: fact.CIK, Kind: "issuance", Accession: "8k1", EffectiveAt: fact.Instant.Add(-time.Hour), AcceptedAt: accepted, ChangesShares: true}}, status: QualityStatusValid, reason: ReasonShareSelected},
		{name: "other issuer event ignored", fact: fact, events: []CapitalEvent{{CIK: "0000000002", Kind: "ATM", Accession: "8k1", EffectiveAt: fact.Instant.Add(time.Hour), AcceptedAt: accepted, ChangesShares: true}}, status: QualityStatusValid, reason: ReasonShareSelected},
		{name: "multiple classes", fact: fact, events: []CapitalEvent{{CIK: fact.CIK, Kind: "multiple_class", EffectiveAt: asOf, AcceptedAt: asOf}}, status: QualityStatusConflict, reason: ReasonShareMultipleClasses},
		{name: "split mismatch", fact: fact, events: []CapitalEvent{{CIK: fact.CIK, Kind: "reverse_split", EffectiveAt: fact.Instant.Add(time.Hour), AcceptedAt: accepted, ChangesShares: true}}, status: QualityStatusConflict, reason: ReasonShareSplitMismatch},
		{name: "missing event acceptance fails closed", fact: fact, events: []CapitalEvent{{CIK: fact.CIK, Kind: "financing", EffectiveAt: fact.Instant.Add(time.Hour), ChangesShares: true}}, status: QualityStatusMissing, reason: ReasonShareEventAcceptedAtMissing},
		{name: "future disclosure ignored in replay", fact: fact, events: []CapitalEvent{{CIK: fact.CIK, Kind: "financing", EffectiveAt: fact.Instant.Add(time.Hour), AcceptedAt: asOf.Add(time.Nanosecond), ChangesShares: true}}, status: QualityStatusValid, reason: ReasonShareSelected},
		{name: "acceptance at as-of included", fact: fact, events: []CapitalEvent{{CIK: fact.CIK, Kind: "financing", EffectiveAt: fact.Instant.Add(time.Hour), AcceptedAt: asOf, ChangesShares: true}}, status: QualityStatusConflict, reason: ReasonShareCapitalEvent},
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

func TestSelectSharesCapitalEventQualificationIsDeterministic(t *testing.T) {
	asOf := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	fact := ShareFact{CIK: "0000000001", Concept: "dei:EntityCommonStockSharesOutstanding", Unit: "shares", Form: "10-Q", Accession: "0001", Instant: asOf.AddDate(0, -1, 0), FiledAt: asOf.AddDate(0, 0, -10), AcceptedAt: asOf.AddDate(0, 0, -10), Shares: 40_000_000, SourceURL: "https://www.sec.gov/a"}
	accepted := asOf.Add(-time.Hour)
	afterFact := fact.Instant.Add(time.Hour)

	tests := []struct {
		name   string
		events []CapitalEvent
		reason string
	}{
		{
			name: "confirmed conflict beats missing acceptance",
			events: []CapitalEvent{
				{CIK: fact.CIK, Kind: "financing", EffectiveAt: afterFact, ChangesShares: true},
				{CIK: fact.CIK, Kind: "issuance", EffectiveAt: afterFact, AcceptedAt: accepted, ChangesShares: true},
			},
			reason: ReasonShareCapitalEvent,
		},
		{
			name: "split beats capital event",
			events: []CapitalEvent{
				{CIK: fact.CIK, Kind: "issuance", EffectiveAt: afterFact, AcceptedAt: accepted, ChangesShares: true},
				{CIK: fact.CIK, Kind: "reverse_split", EffectiveAt: afterFact, AcceptedAt: accepted, ChangesShares: true},
			},
			reason: ReasonShareSplitMismatch,
		},
		{
			name: "multiple classes beats all other outcomes",
			events: []CapitalEvent{
				{CIK: fact.CIK, Kind: "financing", EffectiveAt: afterFact, ChangesShares: true},
				{CIK: fact.CIK, Kind: "issuance", EffectiveAt: afterFact, AcceptedAt: accepted, ChangesShares: true},
				{CIK: fact.CIK, Kind: "stock_split", EffectiveAt: afterFact, AcceptedAt: accepted, ChangesShares: true},
				{CIK: fact.CIK, Kind: "multiple_class", EffectiveAt: fact.Instant, AcceptedAt: accepted},
			},
			reason: ReasonShareMultipleClasses,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			for permutationIndex, events := range capitalEventPermutations(test.events) {
				before := append([]CapitalEvent(nil), events...)
				got := SelectShareSnapshot([]ShareFact{fact}, events, asOf)
				if got.QualityStatus != QualityStatusConflict || got.ReasonCode != test.reason {
					t.Fatalf("permutation %d got %#v, want conflict reason=%q", permutationIndex, got, test.reason)
				}
				if !reflect.DeepEqual(events, before) {
					t.Fatalf("permutation %d mutated input: got %#v want %#v", permutationIndex, events, before)
				}
			}
		})
	}
}

func capitalEventPermutations(events []CapitalEvent) [][]CapitalEvent {
	working := append([]CapitalEvent(nil), events...)
	permutations := make([][]CapitalEvent, 0)
	var visit func(int)
	visit = func(index int) {
		if index == len(working) {
			permutations = append(permutations, append([]CapitalEvent(nil), working...))
			return
		}
		for i := index; i < len(working); i++ {
			working[index], working[i] = working[i], working[index]
			visit(index + 1)
			working[index], working[i] = working[i], working[index]
		}
	}
	visit(0)
	return permutations
}

func TestSelectSharesAgeUsesUTCCivilDates(t *testing.T) {
	asOf := time.Date(2026, 11, 1, 23, 30, 0, 0, time.FixedZone("local", -5*60*60))
	instant := time.Date(2026, 6, 5, 23, 59, 59, 0, time.UTC) // 150 UTC civil days before asOf (Nov 2 UTC).
	fact := ShareFact{CIK: "1", Concept: "dei:EntityCommonStockSharesOutstanding", Unit: "shares", Form: "10-Q", Accession: "a", Instant: instant, FiledAt: asOf.AddDate(0, 0, -1), AcceptedAt: asOf.AddDate(0, 0, -1), Shares: 1, SourceURL: "https://sec.test/a"}
	if got := SelectShareSnapshot([]ShareFact{fact}, nil, asOf); got.QualityStatus != QualityStatusValid {
		t.Fatalf("150th UTC civil day at arbitrary times = %#v, want valid", got)
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
