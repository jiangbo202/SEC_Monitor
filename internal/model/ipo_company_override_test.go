package model

import "testing"

func TestIPOCompanyOverrideTableName(t *testing.T) {
	if got := (IPOCompanyOverride{}).TableName(); got != "ipo_company_overrides" {
		t.Fatalf("TableName = %q, want ipo_company_overrides", got)
	}
}
