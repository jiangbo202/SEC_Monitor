package discovery

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"sec_monitor/internal/config"
	"strings"
	"testing"
	"time"
)

func TestFutuSelectedResearchSeparateCacheAndRatios(t *testing.T) {
	db := openMigratedTestDatabase(t)
	ctx := context.Background()
	if err := EnsureAPIModules(ctx, db); err != nil {
		t.Fatal(err)
	}
	security := Security{CIK: "0000123456", CompanyName: "Test SEC"}
	if err := db.Create(&security).Error; err != nil {
		t.Fatal(err)
	}
	listing := Listing{SecurityID: security.ID, Ticker: "TEST", ProviderTicker: "TEST", ValidFrom: time.Now().AddDate(-1, 0, 0)}
	db.Create(&listing)
	now := time.Now()
	db.Create(&CompanyProfileSnapshot{Provider: "longbridge", SecurityID: security.ID, Ticker: "TEST", Profile: "Old LB profile", FetchedAt: &now})
	db.Model(&APIModulePolicy{}).Where("key IN ?", []string{"company", "analyst"}).Update("provider", "futu")
	calls := 0
	cfg := config.DiscoveryConfig{LongbridgeCompanyProfileTTLDays: 30, FutuConfigured: true, FutuReadJSON: func(ctx context.Context, method, path string, q url.Values, body []byte, out any) error {
		calls++
		payload := `{"ret_code":0,"data":{"items":[{"attribute_type":9,"value":"Test Futu"},{"attribute_type":29,"value":"Futu profile"},{"attribute_type":23,"value":"https://issuer.test"}]}}`
		if strings.Contains(path, "analyst-consensus") {
			payload = `{"ret_code":0,"data":{"rating":4,"total":7,"num_of_target_analysts":5,"average":25.5,"highest":30,"lowest":20,"strong_buy":57.14,"hold":42.86,"update_time_str":"2026-10-02"}}`
		}
		return json.Unmarshal([]byte(payload), out)
	}}
	if r, err := RefreshLongbridgeCompanyProfile(ctx, db, cfg, "TEST", security.CIK, false); err != nil || !r.Fetched {
		t.Fatalf("profile=%+v err=%v", r, err)
	}
	profile, err := GetCompanyProfile(ctx, db, "TEST", security.CIK)
	if err != nil || profile.BusinessSummary != "Futu profile" || !strings.Contains(profile.ProfileProvider, "Futu") {
		t.Fatalf("wrong cached source %+v %v", profile, err)
	}
	if r, err := RefreshLongbridgeCompanyProfile(ctx, db, cfg, "TEST", security.CIK, false); err != nil || !r.Cached || calls != 1 {
		t.Fatal("company TTL", r, err, calls)
	}
	r, err := RefreshLongbridgeAnalystRating(ctx, db, cfg, "TEST", security.CIK)
	if err != nil || !r.Fetched {
		t.Fatal(r, err)
	}
	if r.Snapshot.StrongBuyCount != 0 || r.Snapshot.StrongBuyPct == nil || *r.Snapshot.StrongBuyPct != 57.14 || r.Snapshot.TargetAnalystCount != 5 || r.Snapshot.Currency != "" || r.Snapshot.TargetAverageMicros != 25500000 {
		t.Fatalf("falsified counts/currency %+v", r.Snapshot)
	}
	view, err := GetAnalystRating(ctx, db, "TEST")
	if err != nil || view.Latest == nil || view.Latest.Provider != "futu" {
		t.Fatal(view, err)
	}
	db.Model(&APIModulePolicy{}).Where("key = ?", "analyst").Update("interfaces_json", `{"latest":true,"summary":false}`)
	if _, err := RefreshLongbridgeAnalystRating(ctx, db, cfg, "TEST", security.CIK); !errors.Is(err, ErrAPIDisabled) {
		t.Fatal("required sibling not gated", err)
	}
	if calls != 2 {
		t.Fatal("disabled interface called", calls)
	}
	db.Model(&APIModulePolicy{}).Where("key = ?", "company").Update("provider", "longbridge")
	profile, err = GetCompanyProfile(ctx, db, "TEST", security.CIK)
	if err != nil || profile.BusinessSummary != "Old LB profile" {
		t.Fatal("source switch lost old cache", profile, err)
	}
}

func TestFutuPriceUnadjustedSchemaAndInvalidOHLC(t *testing.T) {
	ctx := context.Background()
	payload := `{"ret_code":0,"data":{"volume_precision":0,"kline_list":[{"date":20261002,"open":10,"high":12,"low":9,"close":11,"volume":100}]}}`
	p := &FutuPriceProvider{ReadJSON: func(ctx context.Context, method, path string, q url.Values, body []byte, out any) error {
		if q.Get("autype") != "0" || q.Get("ktype") != "2" || q.Get("num") != "370" || path != "/api/v1.0/quote/US.TEST/history-kline" {
			t.Error("wrong request", path, q)
		}
		return json.Unmarshal([]byte(payload), out)
	}}
	listings := []Listing{{Ticker: "TEST"}}
	rows, result, err := p.LoadForDate(ctx, listings, "2026-10-02")
	if err != nil || len(rows) != 1 || rows[0].Source != "futu" || rows[0].Adjusted || rows[0].Volume != 100 || rows[0].CloseMicros != 11000000 || result.CoveragePct != 100 {
		t.Fatal(rows, result, err)
	}
	payload = strings.ReplaceAll(payload, `"high":12`, `"high":8`)
	if _, _, err := p.LoadForDate(ctx, listings, "2026-10-02"); err == nil {
		t.Fatal("invalid OHLC accepted")
	}
	payload = `{"ret_code":0,"data":{"volume_precision":2,"kline_list":[{"date":20261002,"open":10,"high":12,"low":9,"close":11,"volume":101}]}}`
	if _, _, err := p.LoadForDate(ctx, listings, "2026-10-02"); err == nil {
		t.Fatal("fractional shares truncated")
	}
}

func TestFutuUnverifiedTargetCurrencyCannotEnterUSDFairValue(t *testing.T) {
	analyst := &AnalystRatingSnapshot{Provider: "futu", Status: AnalystRatingStatusAvailable, TargetAverageMicros: 25000000, AnalystCount: 7}
	value := buildCandidateFairValueEstimate(CandidateTechnicalAnalysis{CloseUSD: 10}, analyst, nil)
	if value.MarketConsensusTarget != nil || value.MarketConsensusUpsidePct != nil {
		t.Fatalf("unverified currency used for USD valuation: %+v", value)
	}
}
