package discovery

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"strings"
)

// CompositeSecurityMetadataSource joins the current exchange directory to
// SEC's authoritative ticker/CIK mapping. Neither input is sufficient alone.
type CompositeSecurityMetadataSource struct {
	Nasdaq SecurityMetadataSource
	SEC    SecurityMetadataSource
}

func (s CompositeSecurityMetadataSource) Load(ctx context.Context) ([]SecuritySourceRecord, SourceVersion, error) {
	if s.Nasdaq == nil || s.SEC == nil {
		return nil, SourceVersion{}, fmt.Errorf("Nasdaq and SEC metadata sources are required")
	}
	directory, nv, err := s.Nasdaq.Load(ctx)
	if err != nil {
		return nil, SourceVersion{}, fmt.Errorf("load Nasdaq directory: %w", err)
	}
	sec, sv, err := s.SEC.Load(ctx)
	if err != nil {
		return nil, SourceVersion{}, fmt.Errorf("load SEC metadata: %w", err)
	}
	secByTicker := make(map[string]SecuritySourceRecord, len(sec))
	for _, row := range sec {
		ticker := strings.ToUpper(strings.TrimSpace(row.Ticker))
		if ticker == "" || !validCIK(row.CIK) {
			return nil, SourceVersion{}, fmt.Errorf("SEC metadata contains invalid ticker/CIK identity")
		}
		row.Ticker = ticker
		if prior, ok := secByTicker[ticker]; ok {
			if !reflect.DeepEqual(prior, row) {
				return nil, SourceVersion{}, fmt.Errorf("ticker %s has conflicting SEC metadata", ticker)
			}
			continue
		}
		secByTicker[ticker] = row
	}
	result := make([]SecuritySourceRecord, 0, len(directory))
	seen := make(map[string]struct{}, len(directory))
	for _, market := range directory {
		ticker := strings.ToUpper(strings.TrimSpace(market.Ticker))
		if _, duplicate := seen[ticker]; duplicate {
			return nil, SourceVersion{}, fmt.Errorf("ticker %s is duplicated in Nasdaq directory", ticker)
		}
		seen[ticker] = struct{}{}
		identity, ok := secByTicker[ticker]
		if !ok {
			return nil, SourceVersion{}, fmt.Errorf("ticker %s has no SEC mapping", ticker)
		}
		identity.Ticker = ticker
		identity.Exchange = market.Exchange
		identity.SecurityName = market.SecurityName
		identity.TestIssue = market.TestIssue
		identity.ETF = market.ETF
		identity.MappingStatus = MappingStatusCurrent
		// A successful field join is not an identity-aware historical
		// verification event. Callers may set this only after such a check.
		identity.MappingVerifiedAt = nil
		result = append(result, identity)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Ticker < result[j].Ticker })
	digest, err := hashCanonicalContent(struct{ Nasdaq, SEC SourceVersion }{nv, sv})
	if err != nil {
		return nil, SourceVersion{}, err
	}
	effective := nv.EffectiveAt
	if effective.IsZero() || (!sv.EffectiveAt.IsZero() && sv.EffectiveAt.After(effective)) {
		effective = sv.EffectiveAt
	}
	versionText := nv.Version + "+" + sv.Version
	if versionText == "+" {
		versionText = digest
	}
	return result, SourceVersion{Source: "metadata:composite", Version: versionText, SHA256: digest, EffectiveAt: effective}, nil
}
