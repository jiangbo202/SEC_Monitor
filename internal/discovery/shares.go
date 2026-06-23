package discovery

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	ReasonShareSelected               = "share_selected"
	ReasonShareFactMissing            = "share_fact_missing"
	ReasonShareFactConflict           = "share_fact_conflict"
	ReasonShareAcceptedAtMissing      = "share_accepted_at_missing"
	ReasonShareFactStale              = "share_fact_stale"
	ReasonShareCapitalEvent           = "share_capital_event"
	ReasonShareEventAcceptedAtMissing = "share_event_accepted_at_missing"
	ReasonShareMultipleClasses        = "share_multiple_classes"
	ReasonShareSplitMismatch          = "share_split_mismatch"
)

const maxShareFactAgeDays = 150

type CapitalEvent struct {
	CIK, Kind, Accession    string
	EffectiveAt, AcceptedAt time.Time
	ChangesShares           bool
}

type CapitalEventSource interface {
	Load(context.Context, map[string]struct{}, time.Time) ([]CapitalEvent, SourceVersion, error)
}

// SECSubmissionsCapitalEventSource derives conservative point-in-time capital
// events from SEC submissions metadata. Metadata should be backed by the SEC
// submissions archive in production (SECBulkSource satisfies this contract).
type SECSubmissionsCapitalEventSource struct{ Metadata SecurityMetadataSource }

func (s SECSubmissionsCapitalEventSource) ProductionSafe() bool { return s.Metadata != nil }

func (s SECSubmissionsCapitalEventSource) Load(ctx context.Context, allowed map[string]struct{}, asOf time.Time) ([]CapitalEvent, SourceVersion, error) {
	if s.Metadata == nil {
		return nil, SourceVersion{}, fmt.Errorf("SEC submissions metadata source is required")
	}
	records, upstream, err := s.Metadata.Load(ctx)
	if err != nil {
		return nil, SourceVersion{}, fmt.Errorf("load SEC submissions capital events: %w", err)
	}
	events := make([]CapitalEvent, 0)
	for _, record := range records {
		if _, ok := allowed[record.CIK]; !ok {
			continue
		}
		for _, filing := range record.FilingMetadata {
			if err := ctx.Err(); err != nil {
				return nil, SourceVersion{}, err
			}
			if filing.CIK != record.CIK || (!filing.AcceptedAt.IsZero() && filing.AcceptedAt.After(asOf)) {
				continue
			}
			kind, changes, relevant := capitalEventForFiling(filing)
			if !relevant {
				continue
			}
			effective := filing.FiledAt
			if effective.IsZero() {
				effective = filing.ReportAt
			}
			if effective.IsZero() {
				effective = filing.AcceptedAt
			}
			if effective.IsZero() {
				effective = asOf
			}
			events = append(events, CapitalEvent{CIK: record.CIK, Kind: kind, Accession: filing.Accession, EffectiveAt: effective, AcceptedAt: filing.AcceptedAt, ChangesShares: changes})
		}
	}
	sort.Slice(events, func(i, j int) bool { return canonicalLess(events[i], events[j]) })
	digest, err := hashCanonicalContent(struct {
		Upstream SourceVersion
		Events   []CapitalEvent
	}{upstream, events})
	if err != nil {
		return nil, SourceVersion{}, err
	}
	version := upstream.Version
	if version == "" {
		version = digest
	}
	return events, SourceVersion{Source: "capital-events:sec-submissions", Version: version, SHA256: digest, EffectiveAt: upstream.EffectiveAt}, nil
}

func capitalEventForFiling(filing FilingMetadata) (string, bool, bool) {
	form := strings.ToUpper(strings.TrimSpace(filing.Form))
	items := strings.FieldsFunc(filing.Items, func(r rune) bool { return r == ',' || r == ';' || r == ' ' })
	if form == "8-K" || form == "8-K/A" {
		for _, item := range items {
			switch item {
			case "3.02":
				return "financing", true, true
			case "5.03":
				return "capital_structure", true, true
			}
		}
		if strings.TrimSpace(filing.Items) == "" {
			return "capital_structure_unknown", true, true
		}
		return "", false, false
	}
	for _, prefix := range []string{"S-1", "S-3", "424B5", "PIPE"} {
		if strings.HasPrefix(form, prefix) {
			return "financing", true, true
		}
	}
	return "", false, false
}

type NoCapitalEventsSource struct {
	Version  SourceVersion
	TestOnly bool
}

func (NoCapitalEventsSource) ProductionSafe() bool { return false }

func (s NoCapitalEventsSource) Load(_ context.Context, _ map[string]struct{}, _ time.Time) ([]CapitalEvent, SourceVersion, error) {
	return nil, s.Version, nil
}

type ShareSelection struct {
	Fact          *ShareFact
	QualityStatus string
	ReasonCode    string
}

// SelectShareSnapshot selects a point-in-time SEC cover fact without mutating
// the inputs. Age is measured by UTC civil dates because SEC instant facts are
// civil dates: every time on the 150th day is valid and day 151 is stale.
func SelectShareSnapshot(facts []ShareFact, events []CapitalEvent, asOf time.Time) ShareSelection {
	if asOf.IsZero() {
		return shareResult(nil, QualityStatusMissing, ReasonShareFactMissing)
	}
	eligible := make([]ShareFact, 0, len(facts))
	seen := make(map[string]struct{}, len(facts))
	for _, fact := range facts {
		if !eligibleShareFact(fact, asOf) {
			continue
		}
		key := shareFactIdentity(fact)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		eligible = append(eligible, fact)
	}
	if len(eligible) == 0 {
		return shareResult(nil, QualityStatusMissing, ReasonShareFactMissing)
	}

	sort.Slice(eligible, func(i, j int) bool {
		if !eligible[i].Instant.Equal(eligible[j].Instant) {
			return eligible[i].Instant.After(eligible[j].Instant)
		}
		pi, pj := shareConceptPriority(eligible[i].Concept), shareConceptPriority(eligible[j].Concept)
		if pi != pj {
			return pi < pj
		}
		if !eligible[i].AcceptedAt.Equal(eligible[j].AcceptedAt) {
			return eligible[i].AcceptedAt.After(eligible[j].AcceptedAt)
		}
		return eligible[i].Accession < eligible[j].Accession
	})

	latestInstant := eligible[0].Instant
	priority := 2
	for _, fact := range eligible {
		if fact.Instant.Equal(latestInstant) && shareConceptPriority(fact.Concept) < priority {
			priority = shareConceptPriority(fact.Concept)
		}
	}
	candidates := make([]ShareFact, 0, len(eligible))
	for _, fact := range eligible {
		if fact.Instant.Equal(latestInstant) && shareConceptPriority(fact.Concept) == priority {
			candidates = append(candidates, fact)
		}
	}
	selected, status, reason := selectAcceptedShareFact(candidates)
	if selected == nil {
		return shareResult(nil, status, reason)
	}

	multipleClassesConflict := false
	splitConflict := false
	capitalEventConflict := false
	missingEventAcceptance := false
	for _, event := range events {
		if event.CIK != selected.CIK || event.EffectiveAt.IsZero() || event.EffectiveAt.After(asOf) {
			continue
		}
		kind := normalizeCapitalEventKind(event.Kind)
		multipleClasses := kind == "multiple_class" || kind == "multiple_classes" || kind == "multi_class"
		splitAfterFact := event.EffectiveAt.After(selected.Instant) && (kind == "split" || kind == "stock_split" || kind == "reverse_split")
		sharesChangedAfterFact := event.ChangesShares && event.EffectiveAt.After(selected.Instant)
		if !multipleClasses && !splitAfterFact && !sharesChangedAfterFact {
			continue
		}
		if event.AcceptedAt.IsZero() {
			missingEventAcceptance = true
			continue
		}
		if event.AcceptedAt.After(asOf) {
			continue
		}
		if multipleClasses {
			multipleClassesConflict = true
		} else if splitAfterFact {
			splitConflict = true
		} else if sharesChangedAfterFact {
			capitalEventConflict = true
		}
	}
	if multipleClassesConflict {
		return shareResult(selected, QualityStatusConflict, ReasonShareMultipleClasses)
	}
	if splitConflict {
		return shareResult(selected, QualityStatusConflict, ReasonShareSplitMismatch)
	}
	if capitalEventConflict {
		return shareResult(selected, QualityStatusConflict, ReasonShareCapitalEvent)
	}
	if missingEventAcceptance {
		return shareResult(selected, QualityStatusMissing, ReasonShareEventAcceptedAtMissing)
	}
	asOfDate := utcCivilDate(asOf)
	instantDate := utcCivilDate(selected.Instant)
	if instantDate.After(asOfDate) {
		return shareResult(selected, QualityStatusMissing, ReasonShareFactMissing)
	}
	if instantDate.Before(asOfDate.AddDate(0, 0, -maxShareFactAgeDays)) {
		return shareResult(selected, QualityStatusStale, ReasonShareFactStale)
	}
	return shareResult(selected, QualityStatusValid, ReasonShareSelected)
}

func eligibleShareFact(fact ShareFact, asOf time.Time) bool {
	if strings.TrimSpace(fact.CIK) == "" || strings.TrimSpace(fact.Accession) == "" || strings.TrimSpace(fact.SourceURL) == "" || fact.Shares <= 0 {
		return false
	}
	if fact.Unit != "shares" || shareConceptPriority(fact.Concept) >= 2 {
		return false
	}
	switch fact.Form {
	case "10-Q", "10-K", "10-Q/A", "10-K/A":
	default:
		return false
	}
	if fact.Instant.IsZero() || fact.FiledAt.IsZero() || fact.Instant.After(asOf) || fact.FiledAt.After(asOf) || (!fact.AcceptedAt.IsZero() && fact.AcceptedAt.After(asOf)) {
		return false
	}
	return true
}

func selectAcceptedShareFact(candidates []ShareFact) (*ShareFact, string, string) {
	if len(candidates) == 0 {
		return nil, QualityStatusMissing, ReasonShareFactMissing
	}
	missingAcceptance := false
	values := make(map[int64]struct{}, len(candidates))
	for i := range candidates {
		values[candidates[i].Shares] = struct{}{}
		missingAcceptance = missingAcceptance || candidates[i].AcceptedAt.IsZero()
	}
	if missingAcceptance {
		if len(values) > 1 {
			return nil, QualityStatusConflict, ReasonShareAcceptedAtMissing
		}
		return nil, QualityStatusMissing, ReasonShareAcceptedAtMissing
	}
	sort.Slice(candidates, func(i, j int) bool {
		if !candidates[i].AcceptedAt.Equal(candidates[j].AcceptedAt) {
			return candidates[i].AcceptedAt.After(candidates[j].AcceptedAt)
		}
		return candidates[i].Accession < candidates[j].Accession
	})
	for i := 1; i < len(candidates) && candidates[i].AcceptedAt.Equal(candidates[0].AcceptedAt); i++ {
		if candidates[i].Shares != candidates[0].Shares {
			return nil, QualityStatusConflict, ReasonShareFactConflict
		}
	}
	selected := candidates[0]
	return &selected, QualityStatusValid, ReasonShareSelected
}

func utcCivilDate(value time.Time) time.Time {
	year, month, day := value.UTC().Date()
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
}

func shareConceptPriority(concept string) int {
	switch concept {
	case "dei:EntityCommonStockSharesOutstanding":
		return 0
	case "us-gaap:CommonStockSharesOutstanding":
		return 1
	default:
		return 2
	}
}

func shareFactIdentity(fact ShareFact) string {
	return strings.Join([]string{fact.CIK, fact.Concept, fact.Unit, fact.Form, fact.Accession, fact.Instant.UTC().Format(time.RFC3339Nano), fact.FiledAt.UTC().Format(time.RFC3339Nano), fact.AcceptedAt.UTC().Format(time.RFC3339Nano), strings.TrimSpace(fact.SourceURL), strconv.FormatInt(fact.Shares, 10)}, "\x00")
}

func normalizeCapitalEventKind(kind string) string {
	return strings.ReplaceAll(strings.ToLower(strings.TrimSpace(kind)), "-", "_")
}

func shareResult(fact *ShareFact, quality, reason string) ShareSelection {
	return ShareSelection{Fact: fact, QualityStatus: quality, ReasonCode: reason}
}
