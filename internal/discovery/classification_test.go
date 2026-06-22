package discovery

import (
	"encoding/csv"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

func validClassificationRecord() SecuritySourceRecord {
	return SecuritySourceRecord{
		SecurityID:           42,
		CIK:                  "0000000042",
		Ticker:               "ACME",
		CompanyName:          "Acme Corporation",
		Exchange:             "Nasdaq",
		SecurityName:         "Acme Common Stock",
		StateOfIncorporation: "DE",
		LatestAnnualForm:     "10-K",
		RecentForms:          []string{"10-Q"},
		MappingStatus:        MappingStatusCurrent,
	}
}

func TestClassifySecurityRulePrecedenceAndEvidence(t *testing.T) {
	tests := []struct {
		name, reason, field string
		mutate              func(*SecuritySourceRecord)
	}{
		{"test before etf", ReasonTestIssue, "test_issue", func(r *SecuritySourceRecord) { r.TestIssue, r.ETF = true, true }},
		{"etf before warrant", ReasonFundOrETF, "etf", func(r *SecuritySourceRecord) { r.ETF = true; r.SecurityName = "Acme Warrants" }},
		{"non-common before investment", ReasonNonCommonSecurity, "security_name", func(r *SecuritySourceRecord) {
			r.SecurityName = "Acme Preferred Shares"
			r.RecentForms = []string{"N-1A"}
		}},
		{"investment before spac", ReasonInvestmentCompany, "recent_forms", func(r *SecuritySourceRecord) { r.RecentForms = []string{"485BPOS"}; r.SIC = 6770 }},
		{"spac before foreign", ReasonSPAC, "sic", func(r *SecuritySourceRecord) { r.SIC = 6770; r.LatestAnnualForm = "20-F" }},
		{"foreign before financial", ReasonForeignOrADR, "latest_annual_form", func(r *SecuritySourceRecord) { r.LatestAnnualForm = "20-F"; r.SIC = 6200 }},
		{"financial before inactive", ReasonFinancialCompany, "sic", func(r *SecuritySourceRecord) { r.SIC = 6000; r.Exchange = "OTC" }},
		{"inactive before mapping", ReasonNotActiveListed, "exchange", func(r *SecuritySourceRecord) { r.Exchange = "NYSE Arca"; r.MappingStatus = MappingStatusConflict }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := validClassificationRecord()
			tt.mutate(&r)
			got := ClassifySecurity(r, nil)
			if got.Included || got.Status != EffectiveStatusExcluded || got.Confidence != ConfidenceHigh || got.ReasonCode != tt.reason {
				t.Fatalf("classification = %+v", got)
			}
			if len(got.Evidence) == 0 || got.Evidence[0].Field != tt.field || got.Evidence[0].Source != ClassificationRuleVersion {
				t.Fatalf("evidence = %+v", got.Evidence)
			}
		})
	}
}

func TestClassifySecurityNonCommonWholePhrases(t *testing.T) {
	for _, name := range []string{"ACME WARRANT", "Acme warrants", "Acme RIGHT", "Acme rights", "Acme Preferred Stock", "Acme preferred shares", "Acme 8% Preferred Series A", "ACME PREFERRED", "Acme Depositary Share", "Acme depositary shares", "Acme Unit", "Acme units"} {
		r := validClassificationRecord()
		r.SecurityName = name
		if got := ClassifySecurity(r, nil); got.ReasonCode != ReasonNonCommonSecurity {
			t.Errorf("%q => %+v", name, got)
		}
	}
	for _, name := range []string{"United Common Stock", "Unitil Common Stock", "Preferredly Common Stock", "Unpreferred Common Stock"} {
		r := validClassificationRecord()
		r.SecurityName = name
		if got := ClassifySecurity(r, nil); !got.Included {
			t.Errorf("substring false positive %q => %+v", name, got)
		}
	}
}

func TestClassifySecurityFormsUseTrimmedExactUppercase(t *testing.T) {
	tests := []struct {
		form     string
		excluded bool
	}{{" N-2 ", true}, {"N-CSR", true}, {"N-CSRS", true}, {"485APOS", true}, {"n-1a", false}, {" 485bpos ", false}}
	for _, tt := range tests {
		r := validClassificationRecord()
		r.RecentForms = []string{tt.form}
		got := ClassifySecurity(r, nil)
		if (got.ReasonCode == ReasonInvestmentCompany) != tt.excluded {
			t.Errorf("form %q => %+v", tt.form, got)
		}
	}
}

func TestClassifySecurityForeignAllowlistAndUnknown(t *testing.T) {
	for _, state := range []string{"AL", "WY", "DC", "PR", "VI", "GU", "AS", "MP", " de "} {
		r := validClassificationRecord()
		r.StateOfIncorporation = state
		if got := ClassifySecurity(r, nil); !got.Included {
			t.Errorf("allowed state %q => %+v", state, got)
		}
	}
	r := validClassificationRecord()
	r.StateOfIncorporation = "CA"
	if got := ClassifySecurity(r, nil); !got.Included {
		t.Fatalf("CA => %+v", got)
	}
	r = validClassificationRecord()
	r.StateOfIncorporation = "ZZ"
	if got := ClassifySecurity(r, nil); got.ReasonCode != ReasonForeignOrADR {
		t.Fatalf("foreign state => %+v", got)
	}
	r = validClassificationRecord()
	r.StateOfIncorporation = ""
	if got := ClassifySecurity(r, nil); got.ReasonCode == ReasonForeignOrADR {
		t.Fatalf("empty state => %+v", got)
	}
	for _, form := range []string{"20-F", "40-F"} {
		r := validClassificationRecord()
		r.LatestAnnualForm = form
		if got := ClassifySecurity(r, nil); got.ReasonCode != ReasonForeignOrADR {
			t.Errorf("annual %s => %+v", form, got)
		}
	}
	for _, form := range []string{"F-1", "F-3"} {
		r := validClassificationRecord()
		r.RecentForms = []string{form}
		if got := ClassifySecurity(r, nil); got.ReasonCode != ReasonForeignOrADR {
			t.Errorf("recent %s => %+v", form, got)
		}
	}
}

func TestClassifySecuritySICBoundariesAndDeSPAC(t *testing.T) {
	for _, tc := range []struct {
		sic    int
		reason string
	}{{5999, ReasonDomesticOperatingCommon}, {6000, ReasonFinancialCompany}, {6799, ReasonFinancialCompany}, {6800, ReasonDomesticOperatingCommon}} {
		r := validClassificationRecord()
		r.SIC = tc.sic
		if got := ClassifySecurity(r, nil); got.ReasonCode != tc.reason {
			t.Errorf("SIC %d => %+v", tc.sic, got)
		}
	}
}

func TestClassifySecurityDeSPACTransitionTable(t *testing.T) {
	tests := []struct {
		name                  string
		sic                   int
		blankCheck            bool
		item201               bool
		mappingStatus         string
		wantReason            string
		wantIncluded          bool
		wantCompletedEvidence bool
	}{
		{"6770 not blank before combination current", 6770, false, false, MappingStatusCurrent, ReasonSPAC, false, false},
		{"6770 not blank after combination current", 6770, false, true, MappingStatusCurrent, ReasonSPAC, false, false},
		{"6770 blank before combination current", 6770, true, false, MappingStatusCurrent, ReasonSPAC, false, false},
		{"6770 blank after combination current", 6770, true, true, MappingStatusCurrent, ReasonSPAC, false, false},
		{"6770 not blank before combination conflict", 6770, false, false, MappingStatusConflict, ReasonSPAC, false, false},
		{"6770 not blank after combination conflict", 6770, false, true, MappingStatusConflict, ReasonSPAC, false, false},
		{"6770 blank before combination conflict", 6770, true, false, MappingStatusConflict, ReasonSPAC, false, false},
		{"6770 blank after combination conflict", 6770, true, true, MappingStatusConflict, ReasonSPAC, false, false},
		{"operating not blank before combination current", 3571, false, false, MappingStatusCurrent, ReasonDomesticOperatingCommon, true, false},
		{"operating not blank after combination current", 3571, false, true, MappingStatusCurrent, ReasonDomesticOperatingCommon, true, false},
		{"operating blank before combination current", 3571, true, false, MappingStatusCurrent, ReasonSPAC, false, false},
		{"operating blank after combination current", 3571, true, true, MappingStatusCurrent, ReasonDomesticOperatingCommon, true, true},
		{"operating not blank before combination conflict", 3571, false, false, MappingStatusConflict, ReasonMappingConflict, false, false},
		{"operating not blank after combination conflict", 3571, false, true, MappingStatusConflict, ReasonMappingConflict, false, false},
		{"operating blank before combination conflict", 3571, true, false, MappingStatusConflict, ReasonSPAC, false, false},
		{"operating blank after combination conflict", 3571, true, true, MappingStatusConflict, ReasonMappingConflict, false, true},
		{"operating blank after combination missing mapping", 3571, true, true, "", ReasonMappingConflict, false, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := validClassificationRecord()
			r.SIC = tt.sic
			r.BlankCheckIssuer = tt.blankCheck
			r.HasBusinessCombinationItem201 = tt.item201
			r.MappingStatus = tt.mappingStatus
			got := ClassifySecurity(r, nil)
			if got.ReasonCode != tt.wantReason || got.Included != tt.wantIncluded {
				t.Fatalf("classification = %+v", got)
			}
			hasCompletedEvidence := false
			for _, evidence := range got.Evidence {
				if evidence.Field == "business_combination_item_2_01" && evidence.Value == "true" {
					hasCompletedEvidence = true
				}
			}
			if hasCompletedEvidence != tt.wantCompletedEvidence {
				t.Fatalf("completed-combination evidence=%v, want %v: %+v", hasCompletedEvidence, tt.wantCompletedEvidence, got.Evidence)
			}
		})
	}
}

func TestClassifySecurityMappingIdentityAndInclusion(t *testing.T) {
	tests := []struct {
		name, reason string
		mutate       func(*SecuritySourceRecord)
	}{
		{"conflict", ReasonMappingConflict, func(r *SecuritySourceRecord) { r.MappingStatus = MappingStatusConflict }},
		{"expired", ReasonMappingConflict, func(r *SecuritySourceRecord) { r.MappingStatus = MappingStatusExpired }},
		{"missing cik", ReasonSecurityTypeUnresolved, func(r *SecuritySourceRecord) { r.CIK = "" }},
		{"short cik", ReasonSecurityTypeUnresolved, func(r *SecuritySourceRecord) { r.CIK = "123" }},
		{"nondigit cik", ReasonSecurityTypeUnresolved, func(r *SecuritySourceRecord) { r.CIK = "00000000A1" }},
		{"zero cik", ReasonSecurityTypeUnresolved, func(r *SecuritySourceRecord) { r.CIK = "0000000000" }},
		{"missing ticker", ReasonSecurityTypeUnresolved, func(r *SecuritySourceRecord) { r.Ticker = "" }},
		{"missing company", ReasonSecurityTypeUnresolved, func(r *SecuritySourceRecord) { r.CompanyName = "" }},
		{"missing security", ReasonSecurityTypeUnresolved, func(r *SecuritySourceRecord) { r.SecurityName = "" }},
		{"wrong annual", ReasonSecurityTypeUnresolved, func(r *SecuritySourceRecord) { r.LatestAnnualForm = "10-Q" }},
		{"missing quarter", ReasonSecurityTypeUnresolved, func(r *SecuritySourceRecord) { r.RecentForms = []string{"8-K"} }},
		{"ambiguous name", ReasonSecurityTypeUnresolved, func(r *SecuritySourceRecord) { r.SecurityName = "Acme Corporation" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := validClassificationRecord()
			tt.mutate(&r)
			got := ClassifySecurity(r, nil)
			if got.Included || got.Status != EffectiveStatusDataInsufficient || got.Confidence != ConfidenceLow || got.ReasonCode != tt.reason {
				t.Fatalf("got %+v", got)
			}
		})
	}
	for _, exchange := range []string{"Nasdaq", "NYSE", "NYSE American"} {
		for _, annual := range []string{"10-K", "10-K/A"} {
			for _, quarter := range []string{"10-Q", "10-Q/A"} {
				r := validClassificationRecord()
				r.Exchange, r.LatestAnnualForm, r.RecentForms = exchange, annual, []string{quarter}
				r.SecurityName = "Acme ordinary shares"
				got := ClassifySecurity(r, nil)
				if !got.Included || got.Status != EffectiveStatusIncluded || got.Confidence != ConfidenceHigh || got.ReasonCode != ReasonDomesticOperatingCommon {
					t.Fatalf("include => %+v", got)
				}
			}
		}
	}
}

func TestClassifySecurityOverrides(t *testing.T) {
	r := validClassificationRecord()
	r.TestIssue = true
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	t1 := t0.Add(time.Hour)
	overrides := []ManualSecurityOverride{
		{ID: 99, SecurityID: 9, Active: true, EffectiveStatus: EffectiveStatusIncluded, UpdatedAt: t1},
		{ID: 1, SecurityID: 42, Active: false, EffectiveStatus: EffectiveStatusIncluded, UpdatedAt: t1},
		{ID: 2, SecurityID: 42, Active: true, EffectiveStatus: EffectiveStatusExcluded, UpdatedAt: t0},
		{ID: 3, SecurityID: 42, Active: true, EffectiveStatus: EffectiveStatusDataInsufficient, UpdatedAt: t1},
		{ID: 4, SecurityID: 42, Active: true, EffectiveStatus: EffectiveStatusIncluded, UpdatedAt: t1, Operator: "reviewer", Reason: "verified", SourceURL: "https://example.test/evidence"},
	}
	got := ClassifySecurity(r, overrides)
	if !got.Included || got.Status != EffectiveStatusIncluded || got.Confidence != ConfidenceHigh || got.ReasonCode != ReasonManualOverride {
		t.Fatalf("override = %+v", got)
	}
	values := map[string]string{}
	for _, e := range got.Evidence {
		values[e.Field] = e.Value
	}
	for k, v := range map[string]string{"automatic_reason": ReasonTestIssue, "override_id": "4", "operator": "reviewer", "reason": "verified", "source": "https://example.test/evidence"} {
		if values[k] != v {
			t.Errorf("evidence %s=%q, want %q (%+v)", k, values[k], v, got.Evidence)
		}
	}
	invalid := overrides[len(overrides)-1]
	invalid.EffectiveStatus = "maybe"
	got = ClassifySecurity(validClassificationRecord(), []ManualSecurityOverride{invalid})
	if got.Status != EffectiveStatusDataInsufficient || got.Confidence != ConfidenceLow || got.ReasonCode != ReasonInvalidManualOverride {
		t.Fatalf("invalid = %+v", got)
	}
	got = ClassifySecurity(validClassificationRecord(), []ManualSecurityOverride{{ID: 1, SecurityID: 99, Active: true, EffectiveStatus: EffectiveStatusExcluded}})
	if !got.Included || got.ReasonCode != ReasonDomesticOperatingCommon {
		t.Fatalf("no match = %+v", got)
	}
}

func TestClassifySecurityDeterministicAndDoesNotMutateInputs(t *testing.T) {
	r := validClassificationRecord()
	r.RecentForms = []string{"10-Q", "8-K"}
	o := []ManualSecurityOverride{{ID: 8, SecurityID: 42, Active: true, EffectiveStatus: EffectiveStatusExcluded, UpdatedAt: time.Unix(5, 0), Reason: "review"}}
	rBefore := r
	rBefore.RecentForms = append([]string(nil), r.RecentForms...)
	oBefore := append([]ManualSecurityOverride(nil), o...)
	want := ClassifySecurity(r, o)
	for i := 0; i < 100; i++ {
		if got := ClassifySecurity(r, o); !reflect.DeepEqual(got, want) {
			t.Fatalf("run %d differs: %+v vs %+v", i, got, want)
		}
	}
	if !reflect.DeepEqual(r, rBefore) || !reflect.DeepEqual(o, oBefore) {
		t.Fatalf("inputs mutated: record=%+v overrides=%+v", r, o)
	}
}

func TestClassificationGoldCoverage(t *testing.T) {
	f, err := os.Open("testdata/gold/security_classification.csv")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	rows, err := csv.NewReader(f).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) < 2 {
		t.Fatal("gold fixture is empty")
	}
	head := map[string]int{}
	for i, v := range rows[0] {
		head[v] = i
	}
	required := []string{"case_id", "group", "security_name", "test_issue", "etf", "sic", "state", "annual_form", "recent_forms", "exchange", "mapping_status", "cik", "ticker", "company_name", "expected_status", "expected_reason", "expected_included", "expected_confidence", "review_status"}
	for _, col := range required {
		if _, ok := head[col]; !ok {
			t.Fatalf("missing column %q", col)
		}
	}
	counts := map[string]int{}
	for line, row := range rows[1:] {
		get := func(k string) string { return row[head[k]] }
		if get("review_status") != "approved" {
			t.Fatalf("line %d not approved", line+2)
		}
		sic, err := strconv.Atoi(get("sic"))
		if err != nil {
			t.Fatalf("line %d SIC: %v", line+2, err)
		}
		r := SecuritySourceRecord{SecurityID: uint(line + 1), CIK: get("cik"), Ticker: get("ticker"), CompanyName: get("company_name"), SecurityName: get("security_name"), TestIssue: get("test_issue") == "true", ETF: get("etf") == "true", SIC: sic, StateOfIncorporation: get("state"), LatestAnnualForm: get("annual_form"), RecentForms: strings.Split(get("recent_forms"), ";"), Exchange: get("exchange"), MappingStatus: get("mapping_status")}
		got := ClassifySecurity(r, nil)
		wantIncluded, _ := strconv.ParseBool(get("expected_included"))
		if got.Status != get("expected_status") || got.ReasonCode != get("expected_reason") || got.Included != wantIncluded || got.Confidence != get("expected_confidence") {
			t.Errorf("%s: got %+v", get("case_id"), got)
		}
		counts[get("group")]++
	}
	if len(rows)-1 < 120 {
		t.Errorf("gold cases=%d, want >=120", len(rows)-1)
	}
	for _, group := range []string{"test", "etf", "non-common", "investment", "spac", "foreign", "financial", "inactive", "mapping-conflict", "unresolved", "included"} {
		if counts[group] < 10 {
			t.Errorf("group %s cases=%d, want >=10", group, counts[group])
		}
	}
}
