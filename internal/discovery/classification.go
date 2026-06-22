package discovery

import (
	"strconv"
	"strings"
	"time"
	"unicode"
)

const ClassificationRuleVersion = "v1"

const (
	ReasonTestIssue               = "test_issue"
	ReasonFundOrETF               = "fund_or_etf"
	ReasonNonCommonSecurity       = "non_common_security"
	ReasonInvestmentCompany       = "investment_company"
	ReasonSPAC                    = "spac"
	ReasonForeignOrADR            = "foreign_or_adr"
	ReasonFinancialCompany        = "financial_company"
	ReasonNotActiveListed         = "not_active_listed"
	ReasonMappingConflict         = "mapping_conflict"
	ReasonSecurityTypeUnresolved  = "security_type_unresolved"
	ReasonDomesticOperatingCommon = "domestic_operating_common"
	ReasonManualOverride          = "manual_override"
	ReasonInvalidManualOverride   = "invalid_manual_override"
)

type Classification struct {
	Included   bool
	Status     string
	Confidence string
	ReasonCode string
	Evidence   []Evidence
}

var nonCommonSecurityTerms = []string{"warrant", "warrants", "right", "rights", "preferred", "preferred stock", "preferred share", "preferred shares", "depositary share", "depositary shares", "unit", "units"}
var commonSecurityTerms = []string{"common stock", "common shares", "ordinary share", "ordinary shares"}

var usJurisdictions = func() map[string]struct{} {
	codes := strings.Fields("AL AK AZ AR CA CO CT DE FL GA HI ID IL IN IA KS KY LA ME MD MA MI MN MS MO MT NE NV NH NJ NM NY NC ND OH OK OR PA RI SC SD TN TX UT VT VA WA WV WI WY DC PR VI GU AS MP")
	result := make(map[string]struct{}, len(codes))
	for _, code := range codes {
		result[code] = struct{}{}
	}
	return result
}()

func ClassifySecurity(record SecuritySourceRecord, overrides []ManualSecurityOverride) Classification {
	automatic := classifySecurityAutomatic(record)
	selected, ok := selectManualOverride(record.SecurityID, overrides)
	if !ok {
		return automatic
	}

	evidence := append([]Evidence(nil), automatic.Evidence...)
	evidence = append(evidence,
		Evidence{Field: "automatic_reason", Value: automatic.ReasonCode, Source: ReasonManualOverride},
		Evidence{Field: "override_id", Value: strconv.FormatUint(uint64(selected.ID), 10), Source: ReasonManualOverride},
		Evidence{Field: "operator", Value: selected.Operator, Source: ReasonManualOverride},
		Evidence{Field: "reason", Value: selected.Reason, Source: ReasonManualOverride},
	)
	if selected.SourceURL != "" {
		evidence = append(evidence, Evidence{Field: "source", Value: selected.SourceURL, Source: ReasonManualOverride})
	}
	evidence = append(evidence, Evidence{Field: "updated_at", Value: selected.UpdatedAt.Format(time.RFC3339), Source: ReasonManualOverride})

	if !validManualOverride(selected) {
		return Classification{
			Status:     EffectiveStatusDataInsufficient,
			Confidence: ConfidenceLow,
			ReasonCode: ReasonInvalidManualOverride,
			Evidence:   evidence,
		}
	}

	switch selected.EffectiveStatus {
	case EffectiveStatusIncluded, EffectiveStatusExcluded, EffectiveStatusDataInsufficient:
		return Classification{
			Included:   selected.EffectiveStatus == EffectiveStatusIncluded,
			Status:     selected.EffectiveStatus,
			Confidence: ConfidenceHigh,
			ReasonCode: ReasonManualOverride,
			Evidence:   evidence,
		}
	default:
		return Classification{
			Status:     EffectiveStatusDataInsufficient,
			Confidence: ConfidenceLow,
			ReasonCode: ReasonInvalidManualOverride,
			Evidence:   evidence,
		}
	}
}

func classifySecurityAutomatic(record SecuritySourceRecord) Classification {
	if record.TestIssue {
		return excluded(ReasonTestIssue, "test_issue", "true")
	}
	if record.ETF {
		return excluded(ReasonFundOrETF, "etf", "true")
	}
	if containsWholeTerm(record.SecurityName, nonCommonSecurityTerms) {
		return excluded(ReasonNonCommonSecurity, "security_name", record.SecurityName)
	}
	if form, ok := matchingForm(record.RecentForms, func(form string) bool {
		return form == "N-1A" || form == "N-2" || form == "N-CSR" || form == "N-CSRS" || (form == strings.ToUpper(form) && strings.HasPrefix(form, "485"))
	}); ok {
		return excluded(ReasonInvestmentCompany, "recent_forms", form)
	}
	if record.SIC == 6770 {
		if record.HasBusinessCombinationItem201 {
			return Classification{
				Status:     EffectiveStatusDataInsufficient,
				Confidence: ConfidenceLow,
				ReasonCode: ReasonSecurityTypeUnresolved,
				Evidence: []Evidence{
					{Field: "business_combination_item_2_01", Value: "true", Source: ClassificationRuleVersion},
					{Field: "stale_spac_sic", Value: strconv.Itoa(record.SIC), Source: ClassificationRuleVersion},
				},
			}
		}
		return excluded(ReasonSPAC, "sic", strconv.Itoa(record.SIC))
	}
	if record.BlankCheckIssuer && !record.HasBusinessCombinationItem201 {
		return excluded(ReasonSPAC, "blank_check_issuer", "true")
	}
	var transitionEvidence []Evidence
	if record.BlankCheckIssuer && record.HasBusinessCombinationItem201 {
		transitionEvidence = []Evidence{
			{Field: "blank_check_issuer", Value: "true", Source: ClassificationRuleVersion},
			{Field: "business_combination_item_2_01", Value: "true", Source: ClassificationRuleVersion},
		}
	}
	annualForm := strings.TrimSpace(record.LatestAnnualForm)
	if annualForm == "20-F" || annualForm == "40-F" {
		return withEvidence(excluded(ReasonForeignOrADR, "latest_annual_form", annualForm), transitionEvidence)
	}
	if form, ok := matchingForm(record.RecentForms, func(form string) bool { return form == "F-1" || form == "F-3" }); ok {
		return withEvidence(excluded(ReasonForeignOrADR, "recent_forms", form), transitionEvidence)
	}
	state := strings.ToUpper(strings.TrimSpace(record.StateOfIncorporation))
	if state != "" {
		if _, domestic := usJurisdictions[state]; !domestic {
			return withEvidence(excluded(ReasonForeignOrADR, "state_of_incorporation", state), transitionEvidence)
		}
	}
	if record.SIC >= 6000 && record.SIC <= 6799 {
		return withEvidence(excluded(ReasonFinancialCompany, "sic", strconv.Itoa(record.SIC)), transitionEvidence)
	}
	if record.Exchange != "Nasdaq" && record.Exchange != "NYSE" && record.Exchange != "NYSE American" {
		return withEvidence(excluded(ReasonNotActiveListed, "exchange", record.Exchange), transitionEvidence)
	}
	if record.MappingStatus != MappingStatusCurrent {
		return withEvidence(unresolved(ReasonMappingConflict, "mapping_status", record.MappingStatus), transitionEvidence)
	}
	if record.BlankCheckIssuer && record.HasBusinessCombinationItem201 {
		completed, verified := "", ""
		if record.BusinessCombinationCompletedAt != nil {
			completed = record.BusinessCombinationCompletedAt.Format(time.RFC3339)
		}
		if record.MappingVerifiedAt != nil {
			verified = record.MappingVerifiedAt.Format(time.RFC3339)
		}
		transitionEvidence = append(transitionEvidence,
			Evidence{Field: "business_combination_completed_at", Value: completed, Source: ClassificationRuleVersion},
			Evidence{Field: "mapping_verified_at", Value: verified, Source: ClassificationRuleVersion},
		)
		if record.BusinessCombinationCompletedAt == nil || record.MappingVerifiedAt == nil || record.MappingVerifiedAt.Before(*record.BusinessCombinationCompletedAt) {
			return Classification{Status: EffectiveStatusDataInsufficient, Confidence: ConfidenceLow, ReasonCode: ReasonSecurityTypeUnresolved, Evidence: transitionEvidence}
		}
	}
	if field, value, invalid := invalidIdentity(record); invalid {
		return withEvidence(unresolved(ReasonSecurityTypeUnresolved, field, value), transitionEvidence)
	}
	if record.SIC < 100 || record.SIC > 9999 {
		return withEvidence(unresolved(ReasonSecurityTypeUnresolved, "sic", strconv.Itoa(record.SIC)), transitionEvidence)
	}
	if (annualForm == "10-K" || annualForm == "10-K/A") && hasExactForm(record.RecentForms, "10-Q", "10-Q/A") && containsWholeTerm(record.SecurityName, commonSecurityTerms) {
		return withEvidence(Classification{
			Included:   true,
			Status:     EffectiveStatusIncluded,
			Confidence: ConfidenceHigh,
			ReasonCode: ReasonDomesticOperatingCommon,
			Evidence: []Evidence{
				{Field: "mapping_status", Value: record.MappingStatus, Source: ClassificationRuleVersion},
				{Field: "latest_annual_form", Value: annualForm, Source: ClassificationRuleVersion},
				{Field: "recent_forms", Value: firstExactForm(record.RecentForms, "10-Q", "10-Q/A"), Source: ClassificationRuleVersion},
				{Field: "security_name", Value: record.SecurityName, Source: ClassificationRuleVersion},
			},
		}, transitionEvidence)
	}
	return withEvidence(unresolved(ReasonSecurityTypeUnresolved, "security_name", record.SecurityName), transitionEvidence)
}

func withEvidence(classification Classification, evidence []Evidence) Classification {
	if len(evidence) == 0 {
		return classification
	}
	classification.Evidence = append(append([]Evidence(nil), evidence...), classification.Evidence...)
	return classification
}

func excluded(reason, field, value string) Classification {
	return Classification{Status: EffectiveStatusExcluded, Confidence: ConfidenceHigh, ReasonCode: reason, Evidence: []Evidence{{Field: field, Value: value, Source: ClassificationRuleVersion}}}
}

func unresolved(reason, field, value string) Classification {
	return Classification{Status: EffectiveStatusDataInsufficient, Confidence: ConfidenceLow, ReasonCode: reason, Evidence: []Evidence{{Field: field, Value: value, Source: ClassificationRuleVersion}}}
}

func invalidIdentity(record SecuritySourceRecord) (string, string, bool) {
	if record.SecurityID == 0 {
		return "security_id", "0", true
	}
	if !validCIK(record.CIK) {
		return "cik", record.CIK, true
	}
	if strings.TrimSpace(record.Ticker) == "" {
		return "ticker", record.Ticker, true
	}
	if strings.TrimSpace(record.CompanyName) == "" {
		return "company_name", record.CompanyName, true
	}
	if strings.TrimSpace(record.SecurityName) == "" {
		return "security_name", record.SecurityName, true
	}
	return "", "", false
}

func validCIK(cik string) bool {
	if len(cik) != 10 || cik == "0000000000" {
		return false
	}
	for _, ch := range cik {
		if ch < '0' || ch > '9' {
			return false
		}
	}
	return true
}

func matchingForm(forms []string, match func(string) bool) (string, bool) {
	for _, raw := range forms {
		form := strings.TrimSpace(raw)
		if match(form) {
			return form, true
		}
	}
	return "", false
}

func hasExactForm(forms []string, candidates ...string) bool {
	return firstExactForm(forms, candidates...) != ""
}

func firstExactForm(forms []string, candidates ...string) string {
	form, _ := matchingForm(forms, func(form string) bool {
		for _, candidate := range candidates {
			if form == candidate {
				return true
			}
		}
		return false
	})
	return form
}

func selectManualOverride(securityID uint, overrides []ManualSecurityOverride) (ManualSecurityOverride, bool) {
	var selected ManualSecurityOverride
	found := false
	for _, override := range overrides {
		if !override.Active || override.SecurityID != securityID {
			continue
		}
		if !found || override.UpdatedAt.After(selected.UpdatedAt) || (override.UpdatedAt.Equal(selected.UpdatedAt) && override.ID > selected.ID) {
			selected = override
			found = true
		}
	}
	return selected, found
}

func validManualOverride(override ManualSecurityOverride) bool {
	if override.ID == 0 || override.SecurityID == 0 || strings.TrimSpace(override.Operator) == "" || strings.TrimSpace(override.Reason) == "" || override.UpdatedAt.IsZero() {
		return false
	}
	return override.EffectiveStatus == EffectiveStatusIncluded || override.EffectiveStatus == EffectiveStatusExcluded || override.EffectiveStatus == EffectiveStatusDataInsufficient
}

// Exact override duplicates retain their original input order because selection
// only replaces the current maximum when UpdatedAt or ID is strictly greater.
func containsWholeTerm(value string, terms []string) bool {
	runes := []rune(strings.ToLower(value))
	for _, term := range terms {
		needle := []rune(term)
		for start := 0; start+len(needle) <= len(runes); start++ {
			if string(runes[start:start+len(needle)]) != term {
				continue
			}
			leftBoundary := start == 0 || (!unicode.IsLetter(runes[start-1]) && !unicode.IsNumber(runes[start-1]))
			end := start + len(needle)
			rightBoundary := end == len(runes) || (!unicode.IsLetter(runes[end]) && !unicode.IsNumber(runes[end]))
			if leftBoundary && rightBoundary {
				return true
			}
		}
	}
	return false
}

func ClassificationGoldReady(totalCases, minimumCasesPerGroup int, independentlySourced bool) bool {
	return independentlySourced && totalCases >= 120 && minimumCasesPerGroup >= 10
}
