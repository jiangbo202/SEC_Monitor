package discovery

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
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

var (
	nonCommonSecurityPattern = regexp.MustCompile(`(?i)(^|[^a-z0-9])(warrants?|rights?|preferred (stock|shares?)|depositary shares?|units?)($|[^a-z0-9])`)
	commonSecurityPattern    = regexp.MustCompile(`(?i)(^|[^a-z0-9])(common stock|common shares|ordinary shares?)($|[^a-z0-9])`)
)

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
		Evidence{Field: "source", Value: selected.SourceURL, Source: ReasonManualOverride},
	)

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
	if nonCommonSecurityPattern.MatchString(record.SecurityName) {
		return excluded(ReasonNonCommonSecurity, "security_name", record.SecurityName)
	}
	if form, ok := matchingForm(record.RecentForms, func(form string) bool {
		return form == "N-1A" || form == "N-2" || form == "N-CSR" || form == "N-CSRS" || (form == strings.ToUpper(form) && strings.HasPrefix(form, "485"))
	}); ok {
		return excluded(ReasonInvestmentCompany, "recent_forms", form)
	}
	if record.SIC == 6770 {
		return excluded(ReasonSPAC, "sic", strconv.Itoa(record.SIC))
	}
	if record.BlankCheckIssuer {
		return excluded(ReasonSPAC, "blank_check_issuer", "true")
	}
	annualForm := strings.TrimSpace(record.LatestAnnualForm)
	if annualForm == "20-F" || annualForm == "40-F" {
		return excluded(ReasonForeignOrADR, "latest_annual_form", annualForm)
	}
	if form, ok := matchingForm(record.RecentForms, func(form string) bool { return form == "F-1" || form == "F-3" }); ok {
		return excluded(ReasonForeignOrADR, "recent_forms", form)
	}
	state := strings.ToUpper(strings.TrimSpace(record.StateOfIncorporation))
	if state != "" {
		if _, domestic := usJurisdictions[state]; !domestic {
			return excluded(ReasonForeignOrADR, "state_of_incorporation", state)
		}
	}
	if record.SIC >= 6000 && record.SIC <= 6799 {
		return excluded(ReasonFinancialCompany, "sic", strconv.Itoa(record.SIC))
	}
	if record.Exchange != "Nasdaq" && record.Exchange != "NYSE" && record.Exchange != "NYSE American" {
		return excluded(ReasonNotActiveListed, "exchange", record.Exchange)
	}
	if record.MappingStatus != MappingStatusCurrent {
		return unresolved(ReasonMappingConflict, "mapping_status", record.MappingStatus)
	}
	if field, value, invalid := invalidIdentity(record); invalid {
		return unresolved(ReasonSecurityTypeUnresolved, field, value)
	}
	if (annualForm == "10-K" || annualForm == "10-K/A") && hasExactForm(record.RecentForms, "10-Q", "10-Q/A") && commonSecurityPattern.MatchString(record.SecurityName) {
		return Classification{
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
		}
	}
	return unresolved(ReasonSecurityTypeUnresolved, "security_name", record.SecurityName)
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
	matches := make([]ManualSecurityOverride, 0)
	for _, override := range overrides {
		if override.Active && override.SecurityID == securityID {
			matches = append(matches, override)
		}
	}
	if len(matches) == 0 {
		return ManualSecurityOverride{}, false
	}
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].UpdatedAt.Equal(matches[j].UpdatedAt) {
			return matches[i].ID > matches[j].ID
		}
		return matches[i].UpdatedAt.After(matches[j].UpdatedAt)
	})
	return matches[0], true
}
