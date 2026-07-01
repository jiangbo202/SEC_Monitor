package discovery

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
)

func TestCandidateScoreSnapshotMarshalJSONSanitizesNonFiniteFloats(t *testing.T) {
	payload, err := json.Marshal(CandidateScoreSnapshot{Ticker: "INF", RevenueGrowthPct: math.Inf(1), CashRunwayMonths: math.Inf(1)})
	if err != nil {
		t.Fatalf("MarshalJSON() error = %v", err)
	}
	text := string(payload)
	if strings.Contains(text, "Inf") || !strings.Contains(text, `"cash_runway_months":999`) || !strings.Contains(text, `"revenue_growth_pct":0`) {
		t.Fatalf("payload = %s", text)
	}
}

func TestFinancialMetricSnapshotMarshalJSONSanitizesNonFiniteFloats(t *testing.T) {
	payload, err := json.Marshal(FinancialMetricSnapshot{QuarterlyRevenueYoYPct: math.NaN(), CashRunwayMonths: math.Inf(1)})
	if err != nil {
		t.Fatalf("MarshalJSON() error = %v", err)
	}
	text := string(payload)
	if strings.Contains(text, "Inf") || strings.Contains(text, "NaN") || !strings.Contains(text, `"cash_runway_months":999`) || !strings.Contains(text, `"quarterly_revenue_yoy_pct":0`) {
		t.Fatalf("payload = %s", text)
	}
}
