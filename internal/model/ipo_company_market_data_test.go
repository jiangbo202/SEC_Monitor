package model

import "testing"

func TestIPOCompanyMarketDataTableName(t *testing.T) {
	if got := (IPOCompanyMarketData{}).TableName(); got != "ipo_company_market_data" {
		t.Fatalf("TableName = %q, want ipo_company_market_data", got)
	}
}
