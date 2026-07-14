package discovery

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"
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

func TestCandidateScoreResultMarshalJSONIncludesEnrichedEvidence(t *testing.T) {
	tradeDate := mustParseDate(t, "2026-06-30")
	previousScore := 72
	return1D := 15.07
	payload, err := json.Marshal(CandidateScoreResult{
		CandidateScoreSnapshot: CandidateScoreSnapshot{Ticker: "PX", RevenueGrowthPct: math.Inf(1), CashRunwayMonths: math.Inf(1)},
		PriceCloseUSD:          12.34,
		PriceVolume:            987654,
		PriceTradeDate:         &tradeDate,
		PriceFreshnessStatus:   PriceFreshnessCurrent,
		PriceAgeCalendarDays:   0,
		PriceCurrency:          "USD",
		PriceQualityStatus:     QualityStatusValid,
		PriceSource:            "tiingo",
		QualityTier:            "premium_a",
		QualityTags:            []string{"high_growth", "valid_price"},
		QualityAdjustedScore:   88,
		ReviewPriorityScore:    91,
		ReviewPriorityReasons:  []ReviewPriorityReason{{Label: "质量：强B", Points: 80, Kind: "positive"}},
		ChangeStatus:           "improved",
		ChangeReasons:          []CandidateChangeReason{{Field: "candidate", Label: "首次入选", Current: "B", Kind: "new"}},
		PreviousTotalScore:     &previousScore,
		PreviousGrade:          "B",
		Performance: CandidatePerformance{
			BaseDate:  "2026-06-30",
			BaseClose: 12.34,
			Date1D:    "2026-07-01",
			Close1D:   14.20,
			Return1D:  &return1D,
		},
		SectorCategory:    "软件与数据服务",
		SectorLabel:       "优秀赛道",
		SectorSIC:         7372,
		SectorRatingScore: 9,
	})
	if err != nil {
		t.Fatalf("MarshalJSON() error = %v", err)
	}
	text := string(payload)
	for _, want := range []string{
		`"ticker":"PX"`,
		`"revenue_growth_pct":0`,
		`"cash_runway_months":999`,
		`"price_close_usd":12.34`,
		`"price_volume":987654`,
		`"price_trade_date":"2026-06-30T00:00:00Z"`,
		`"price_freshness_status":"current"`,
		`"price_age_calendar_days":0`,
		`"price_currency":"USD"`,
		`"price_quality_status":"valid"`,
		`"price_source":"tiingo"`,
		`"quality_tier":"premium_a"`,
		`"quality_tags":["high_growth","valid_price"]`,
		`"quality_adjusted_score":88`,
		`"review_priority_score":91`,
		`"review_priority_reasons":[{"label":"质量：强B","points":80,"kind":"positive"}]`,
		`"change_status":"improved"`,
		`"change_reasons":[{"field":"candidate","label":"首次入选","previous":"","current":"B","kind":"new"}]`,
		`"previous_total_score":72`,
		`"previous_grade":"B"`,
		`"performance":{"base_date":"2026-06-30","base_close":12.34,"date_1d":"2026-07-01","close_1d":14.2,"return_1d":15.07`,
		`"sector_category":"软件与数据服务"`,
		`"sector_label":"优秀赛道"`,
		`"sector_sic":7372`,
		`"sector_rating_score":9`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("payload missing %s: %s", want, text)
		}
	}
	if strings.Contains(text, "Inf") || strings.Contains(text, "NaN") {
		t.Fatalf("payload contains non-finite value: %s", text)
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

func mustParseDate(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.Parse("2006-01-02", value)
	if err != nil {
		t.Fatalf("parse date: %v", err)
	}
	return parsed
}
