package discovery

import (
	"context"
	"encoding/json"
	"fmt"
	"gorm.io/gorm"
	"sort"
	"strings"
	"time"
)

const (
	TickerMappingRuleVersion        = "nasdaq-provider-ticker-exact-v1"
	ObservedIdentityVerifierVersion = "observed-identity-v2"
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

type versionedIdentityVerificationSource interface {
	SourceVersion(context.Context) (SourceVersion, error)
}

// ObservedIdentityVerificationSource verifies only timestamped exchange
// observations made after SEC's conservative Item 2.01 filed-date bound.
type ObservedIdentityVerificationSource struct{}

func (ObservedIdentityVerificationSource) SourceVersion(context.Context) (SourceVersion, error) {
	digest, _ := hashCanonicalContent(ObservedIdentityVerifierVersion)
	return SourceVersion{Source: "identity-verifier:observed", Version: ObservedIdentityVerifierVersion, SHA256: digest}, nil
}

func (ObservedIdentityVerificationSource) Verify(_ context.Context, market, sec SecuritySourceRecord) (*time.Time, string, error) {
	if market.ObservedAt == nil || sec.BusinessCombinationCompletedAt == nil || market.ObservedAt.Before(*sec.BusinessCombinationCompletedAt) {
		return nil, "", nil
	}
	if !exactMarketSECTicker(market, sec.Ticker) {
		return nil, "", nil
	}
	marketName, secName := conservativeCompanyName(market.CompanyName), conservativeCompanyName(sec.CompanyName)
	if marketName == "" || secName == "" || marketName != secName {
		return nil, "", nil
	}
	verified := *market.ObservedAt
	return &verified, "timestamped exchange identity observation", nil
}

// ManualIdentityVerificationSource is the persistent production escape hatch
// for de-SPAC identities whose exchange history lacks a trustworthy timestamp.
type ManualIdentityVerificationSource struct{ DB *gorm.DB }

func (s ManualIdentityVerificationSource) SourceVersion(ctx context.Context) (SourceVersion, error) {
	if s.DB == nil {
		return SourceVersion{}, fmt.Errorf("identity override database is required")
	}
	var rows []IdentityVerificationOverride
	if err := s.DB.WithContext(ctx).Where("active = ?", true).Order("cik, ticker, verified_at, id").Find(&rows).Error; err != nil {
		return SourceVersion{}, err
	}
	digest, err := hashCanonicalContent(rows)
	if err != nil {
		return SourceVersion{}, err
	}
	return SourceVersion{Source: "identity-verifier:manual", Version: digest, SHA256: digest}, nil
}

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
		secByTicker[ticker] = append(secByTicker[ticker], row)
	}
	result := make([]SecuritySourceRecord, 0, len(directory))
	seen := make(map[string]struct{}, len(directory))
	for _, market := range directory {
		ticker := strings.ToUpper(strings.TrimSpace(market.Ticker))
		if _, duplicate := seen[ticker]; duplicate {
			return nil, SourceVersion{}, fmt.Errorf("ticker %s is duplicated in Nasdaq directory", ticker)
		}
		seen[ticker] = struct{}{}
		providerTicker := strings.ToUpper(strings.TrimSpace(market.ProviderTicker))
		candidateByCIK := map[string]SecuritySourceRecord{}
		for _, key := range []string{ticker, providerTicker} {
			if key == "" {
				continue
			}
			for _, candidate := range secByTicker[key] {
				candidateByCIK[candidate.CIK] = candidate
			}
		}
		candidates := make([]SecuritySourceRecord, 0, len(candidateByCIK))
		for _, candidate := range candidateByCIK {
			candidates = append(candidates, candidate)
		}
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
		identity.ProviderTicker = providerTicker
		if identity.ProviderTicker == "" {
			identity.ProviderTicker = ticker
		}
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
	verifierVersion := SourceVersion{Source: "identity-verifier:none", Version: "none"}
	if versioned, ok := s.IdentityVerifier.(versionedIdentityVerificationSource); ok {
		verifierVersion, err = versioned.SourceVersion(ctx)
		if err != nil {
			return nil, SourceVersion{}, err
		}
	}
	digest, err := hashCanonicalContent(struct {
		Nasdaq, SEC, Verifier SourceVersion
		MappingRule           string
	}{nv, sv, verifierVersion, TickerMappingRuleVersion})
	if err != nil {
		return nil, SourceVersion{}, err
	}
	effective := nv.EffectiveAt
	if effective.IsZero() || (!sv.EffectiveAt.IsZero() && sv.EffectiveAt.After(effective)) {
		effective = sv.EffectiveAt
	}
	versionText := nv.Version + "+" + sv.Version + "+" + TickerMappingRuleVersion + "+" + verifierVersion.Version
	if versionText == "+" {
		versionText = digest
	}
	return result, SourceVersion{Source: "metadata:composite", Version: versionText, SHA256: digest, EffectiveAt: effective}, nil
}

func exactMarketSECTicker(market SecuritySourceRecord, secTicker string) bool {
	secTicker = strings.ToUpper(strings.TrimSpace(secTicker))
	return secTicker != "" && (strings.ToUpper(strings.TrimSpace(market.Ticker)) == secTicker || strings.ToUpper(strings.TrimSpace(market.ProviderTicker)) == secTicker)
}

func conservativeCompanyName(value string) string {
	parts := strings.Fields(strings.NewReplacer(".", " ", ",", " ").Replace(strings.ToUpper(strings.TrimSpace(value))))
	for len(parts) > 0 {
		switch parts[len(parts)-1] {
		case "INC", "INCORPORATED", "CORP", "CORPORATION", "LTD", "LIMITED", "LLC":
			parts = parts[:len(parts)-1]
		default:
			return strings.Join(parts, " ")
		}
	}
	return ""
}
