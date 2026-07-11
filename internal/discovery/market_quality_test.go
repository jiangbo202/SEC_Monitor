package discovery

import (
	"testing"
	"time"
)

func TestBuildCandidateMarketQualityClassifiesLiquidityVolatilityAndMomentum(t *testing.T) {
	base := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		rows []PriceSnapshot
		want string
	}{
		{name: "liquid upward", rows: []PriceSnapshot{{TradeDate: base, CloseMicros: 1_000_000, Volume: 2_000_000}, {TradeDate: base.AddDate(0, 0, 1), CloseMicros: 1_100_000, Volume: 2_000_000}}, want: "healthy"},
		{name: "thin volatile decline", rows: []PriceSnapshot{{TradeDate: base, CloseMicros: 1_000_000, Volume: 20_000}, {TradeDate: base.AddDate(0, 0, 1), CloseMicros: 700_000, Volume: 20_000}}, want: "risk"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			quality := buildCandidateMarketQuality(tc.rows)
			if quality.Status != tc.want {
				t.Fatalf("quality=%#v, want %s", quality, tc.want)
			}
		})
	}
}
