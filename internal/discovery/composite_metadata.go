package discovery

import (
	"context"
	"encoding/json"
	"fmt"
	"gorm.io/gorm"
	"reflect"
	"sort"
	"strings"
	"time"
)

// CompositeSecurityMetadataSource joins the current exchange directory to
// SEC's authoritative ticker/CIK mapping. Neither input is sufficient alone.
type CompositeSecurityMetadataSource struct {
	Nasdaq           SecurityMetadataSource
	SEC              SecurityMetadataSource
	IdentityVerifier IdentityVerificationSource
}

type IdentityVerificationSource interface {
	Verify(context.Context, SecuritySourceRecord, SecuritySourceRecord) (*time.Time, string, error)
}

// ObservedIdentityVerificationSource verifies only timestamped exchange
// observations made after SEC's conservative Item 2.01 filed-date bound.
type ObservedIdentityVerificationSource struct{}

func (ObservedIdentityVerificationSource) Verify(_ context.Context, market, sec SecuritySourceRecord) (*time.Time, string, error) {
	if market.ObservedAt == nil || sec.BusinessCombinationCompletedAt == nil || market.ObservedAt.Before(*sec.BusinessCombinationCompletedAt) {
		return nil, "", nil
	}
	if !strings.EqualFold(strings.TrimSpace(market.Ticker), strings.TrimSpace(sec.Ticker)) {
		return nil, "", nil
	}
	marketName, secName := strings.TrimSpace(market.CompanyName), strings.TrimSpace(sec.CompanyName)
	if marketName == "" || secName == "" || !strings.EqualFold(marketName, secName) {
		return nil, "", nil
	}
	verified := *market.ObservedAt
	return &verified, "timestamped exchange identity observation", nil
}

// ManualIdentityVerificationSource is the persistent production escape hatch
// for de-SPAC identities whose exchange history lacks a trustworthy timestamp.
type ManualIdentityVerificationSource struct{ DB *gorm.DB }

func (s ManualIdentityVerificationSource) Verify(ctx context.Context, _ SecuritySourceRecord, sec SecuritySourceRecord) (*time.Time, string, error) {
	if s.DB == nil {
		return nil, "", fmt.Errorf("identity override database is required")
	}
	var row IdentityVerificationOverride
	err := s.DB.WithContext(ctx).Where("cik = ? AND ticker = ? AND active = ?", sec.CIK, strings.ToUpper(sec.Ticker), true).Order("verified_at DESC").First(&row).Error
	if err == gorm.ErrRecordNotFound {
		return nil, "", nil
	}
	if err != nil {
		return nil, "", err
	}
	if row.VerifiedAt.IsZero() || row.SourceURL == "" || row.Operator == "" {
		return nil, "", fmt.Errorf("identity override is incomplete")
	}
	return &row.VerifiedAt, row.SourceURL + " reviewer=" + row.Operator, nil
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
	secByTicker := make(map[string][]SecuritySourceRecord, len(sec))
	for _, row := range sec {
		ticker := strings.ToUpper(strings.TrimSpace(row.Ticker))
		if ticker == "" || !validCIK(row.CIK) {
			return nil, SourceVersion{}, fmt.Errorf("SEC metadata contains invalid ticker/CIK identity")
		}
		row.Ticker = ticker
		duplicate := false
		for _, prior := range secByTicker[ticker] {
			if reflect.DeepEqual(prior, row) {
				duplicate = true
				break
			}
		}
		if !duplicate {
			secByTicker[ticker] = append(secByTicker[ticker], row)
		}
	}
	result := make([]SecuritySourceRecord, 0, len(directory))
	seen := make(map[string]struct{}, len(directory))
	for _, market := range directory {
		ticker := strings.ToUpper(strings.TrimSpace(market.Ticker))
		if _, duplicate := seen[ticker]; duplicate {
			return nil, SourceVersion{}, fmt.Errorf("ticker %s is duplicated in Nasdaq directory", ticker)
		}
		seen[ticker] = struct{}{}
		candidates := secByTicker[ticker]
		if len(candidates) != 1 {
			candidateCIKs := make([]string, 0, len(candidates))
			for _, candidate := range candidates {
				candidateCIKs = append(candidateCIKs, candidate.CIK)
			}
			sort.Strings(candidateCIKs)
			evidence, _ := json.Marshal(map[string]any{"ticker": ticker, "candidate_ciks": candidateCIKs, "nasdaq_version": nv.Version, "sec_version": sv.Version})
			market.SourceKey = ticker
			market.Ticker = ticker
			market.CIK = ""
			market.MappingStatus = MappingStatusConflict
			market.EvidenceJSON = string(evidence)
			result = append(result, market)
			continue
		}
		identity := candidates[0]
		identity.SourceKey = ticker
		identity.Ticker = ticker
		identity.ProviderTicker = candidates[0].Ticker
		identity.Exchange = market.Exchange
		identity.SecurityName = market.SecurityName
		identity.TestIssue = market.TestIssue
		identity.ETF = market.ETF
		identity.MappingStatus = MappingStatusCurrent
		// A successful field join is not an identity-aware historical
		// verification event. Callers may set this only after such a check.
		identity.MappingVerifiedAt = nil
		if identity.HasBusinessCombinationItem201 && identity.BusinessCombinationCompletedAt != nil && s.IdentityVerifier != nil {
			verified, evidence, verifyErr := s.IdentityVerifier.Verify(ctx, market, identity)
			if verifyErr != nil {
				return nil, SourceVersion{}, fmt.Errorf("verify identity %s: %w", ticker, verifyErr)
			}
			if verified != nil && !verified.Before(*identity.BusinessCombinationCompletedAt) {
				identity.MappingVerifiedAt = verified
				if evidence != "" {
					identity.EvidenceJSON = evidence
				}
			}
		}
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
