package discovery

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	lbquote "github.com/longbridge/openapi-go/quote"
)

type moduleTestTransport func(*http.Request) (*http.Response, error)

func (f moduleTestTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestAPIModuleHTTPGuardBlocksDispatchIncludingFutuNormalizedSymbol(t *testing.T) {
	db := monitorTestDB(t)
	ctx := context.Background()
	if err := db.AutoMigrate(&APIModulePolicy{}); err != nil {
		t.Fatal(err)
	}
	if err := EnsureAPIModules(ctx, db); err != nil {
		t.Fatal(err)
	}
	db.Model(&APIModulePolicy{}).Where("key IN ?", []string{"company", "futu_ownership"}).Update("enabled", false)
	db.Model(&APIProviderPolicy{}).Where("provider = ?", "futu").Update("paused", false)
	dispatched := 0
	base := moduleTestTransport(func(r *http.Request) (*http.Response, error) {
		dispatched++
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"code":0,"ret_code":0}`)), Header: http.Header{}}, nil
	})
	m := NewAPIMonitor(db)
	for provider, path := range map[string]string{"longbridge": "/v1/quote/comp-overview?symbol=TEST.US", "futu": "/api/v1.0/quote/US.TEST/shareholders/institutional"} {
		client := &http.Client{Transport: &apiTransport{base: base, monitor: m, provider: provider}}
		if _, err := client.Get("https://example.test" + path); !errors.Is(err, ErrAPIDisabled) {
			t.Fatalf("%s err=%v", provider, err)
		}
	}
	if dispatched != 0 {
		t.Fatal("disabled HTTP reached transport")
	}
	var n int64
	db.Model(&APICallRecord{}).Count(&n)
	if n != 0 {
		t.Fatal("disabled dispatch recorded as real API request")
	}
}

func TestAPIModuleOptionalShortDisablePreservesCacheAndReportDate(t *testing.T) {
	db := openMigratedTestDatabase(t)
	ctx := context.Background()
	now := time.Date(2026, 10, 3, 1, 0, 0, 0, time.UTC)
	ratio := 12.5
	cached := OptionResearchSnapshot{Provider: longbridgeOptionResearchProvider, Ticker: "TEST", ObservedDate: now.Format(time.DateOnly), ShortRatioPct: &ratio, ShortReportedAt: "2026-09-15T00:00:00Z", FetchedAt: now.Add(-time.Hour), Status: "available"}
	if err := db.Create(&cached).Error; err != nil {
		t.Fatal(err)
	}
	client := &fakeLongbridgeOptionResearchClient{volume: &lbquote.OptionVolumeStats{CallVolume: "100", PutVolume: "200"}, shortErr: ErrAPIDisabled}
	result, err := refreshLongbridgeOptionResearchWithClient(ctx, db, "TEST", "", LongbridgeOptionResearchOptions{Now: func() time.Time { return now }}, client)
	if err != nil || result.Snapshot.ShortRatioPct == nil || *result.Snapshot.ShortRatioPct != ratio || result.Snapshot.ShortReportedAt != cached.ShortReportedAt {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	view, err := GetOptionResearch(ctx, db, "TEST")
	if err != nil || view.Latest.ShortRatioPct == nil || *view.Latest.ShortRatioPct != ratio {
		t.Fatal("cache erased", err)
	}
}

func TestAPIModuleRegistryAndDefaults(t *testing.T) {
	ctx := context.Background()
	db := monitorTestDB(t)
	if err := db.AutoMigrate(&APIModulePolicy{}); err != nil {
		t.Fatal(err)
	}
	if err := EnsureAPIModules(ctx, db); err != nil {
		t.Fatal(err)
	}
	modules, endpoints := map[string]bool{}, map[string]bool{}
	for _, def := range APIModuleDefinitions() {
		if modules[def.Key] || len(def.Pages) == 0 || len(def.Flow) == 0 {
			t.Fatalf("invalid module %s", def.Key)
		}
		modules[def.Key] = true
		keys := DefaultAPIInterfaces(def)
		for _, api := range def.Interfaces {
			for _, dep := range api.DependsOn {
				if !keys[dep] {
					t.Fatalf("unknown dependency %s", dep)
				}
			}
			for _, path := range api.Endpoints {
				key := def.Provider + path
				if def.Key == "futu_futures" {
					key += ":futures"
				}
				if endpoints[key] {
					t.Fatalf("ambiguous switch %s", key)
				}
				endpoints[key] = true
				if err := CheckAPIModuleEndpoint(ctx, db, def.Provider, path); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	if len(modules) != 12 {
		t.Fatalf("modules=%d", len(modules))
	}
	db.Model(&APIModulePolicy{}).Where("key = ?", "market").Updates(map[string]any{"enabled": false, "revision": 9})
	if err := EnsureAPIModules(ctx, db); err != nil {
		t.Fatal(err)
	}
	var row APIModulePolicy
	db.First(&row, "key = ?", "market")
	if row.Enabled || row.Revision != 9 {
		t.Fatal("migration reset saved policy")
	}
	// Shared auth infrastructure is deliberately not controlled by a data module.
	if err := CheckAPIModuleEndpoint(ctx, db, "longbridge", "/v1/socket/token"); err != nil {
		t.Fatal(err)
	}
}

func TestAPIModuleSDKPreflightStopsBeforeCounterResolution(t *testing.T) {
	db := monitorTestDB(t)
	ctx := context.Background()
	if err := db.AutoMigrate(&APIModulePolicy{}); err != nil {
		t.Fatal(err)
	}
	if err := EnsureAPIModules(ctx, db); err != nil {
		t.Fatal(err)
	}
	db.Model(&APIModulePolicy{}).Where("key <> ?", "").Update("enabled", false)
	previous := CurrentAPIMonitor()
	ConfigureAPIMonitor(db)
	t.Cleanup(func() { activeAPIMonitor.Lock(); activeAPIMonitor.monitor = previous; activeAPIMonitor.Unlock() })
	// Nil SDK clients would panic if identity lookup or a data request was reached.
	candidate := &longbridgeCandidateResearchSDKClient{}
	valuation := &longbridgeValuationResearchSDKClient{}
	cases := []func() error{
		func() error { _, e := (&longbridgeCompanySDKClient{}).Company(ctx, "TEST.US"); return e },
		func() error {
			_, e := (&longbridgeAnalystRatingSDKClient{}).InstitutionRating(ctx, "TEST.US")
			return e
		},
		func() error { _, e := candidate.ForecastEps(ctx, "TEST.US"); return e },
		func() error { _, e := candidate.Anomaly(ctx, "US"); return e },
		func() error { _, e := candidate.Shareholder(ctx, "TEST.US"); return e },
		func() error { _, e := candidate.ShareholderTop(ctx, "TEST.US"); return e },
		func() error { _, e := candidate.ShareholderDetail(ctx, "TEST.US", 1); return e },
		func() error { _, e := candidate.FundHolder(ctx, "TEST.US"); return e },
		func() error { _, e := valuation.Valuation(ctx, "TEST.US"); return e },
		func() error { _, e := valuation.IndustryValuation(ctx, "TEST.US"); return e },
		func() error { _, e := valuation.IndustryValuationDist(ctx, "TEST.US"); return e },
	}
	for index, call := range cases {
		if err := call(); !errors.Is(err, ErrAPIDisabled) {
			t.Fatalf("case %d: %v", index, err)
		}
	}
}

func TestAPIModuleDisabledBeforeBudgetAndClientInitialization(t *testing.T) {
	ctx := context.Background()
	db := monitorTestDB(t)
	if err := db.AutoMigrate(&APIModulePolicy{}); err != nil {
		t.Fatal(err)
	}
	if err := EnsureAPIModules(ctx, db); err != nil {
		t.Fatal(err)
	}
	previous := CurrentAPIMonitor()
	m := ConfigureAPIMonitor(db)
	t.Cleanup(func() { activeAPIMonitor.Lock(); activeAPIMonitor.monitor = previous; activeAPIMonitor.Unlock() })
	db.Model(&APIModulePolicy{}).Where("key IN ?", []string{"market", "options"}).Update("enabled", false)
	for _, path := range []string{"ws/quote", "ws/history_daily", "ws/option_volume"} {
		if _, err := m.Acquire(ctx, "longbridge", path, "TEST"); !errors.Is(err, ErrAPIDisabled) {
			t.Fatalf("admitted %s: %v", path, err)
		}
	}
	constructed := 0
	p := &LongbridgePriceProvider{options: LongbridgePriceProviderOptions{NewClient: func(_, _, _ string) (longbridgeQuoteClient, error) {
		constructed++
		return nil, errors.New("must not initialize")
	}}}
	if _, _, err := p.LoadForDate(ctx, nil, "2026-10-01"); !errors.Is(err, ErrAPIDisabled) {
		t.Fatal(err)
	}
	if _, err := p.LoadHistory(ctx, nil, "2026-10-01", 10); !errors.Is(err, ErrAPIDisabled) {
		t.Fatal(err)
	}
	probe := probeLongbridgeQuote(ctx, "fake-key", "fake-secret", "fake-token", p.options.NewClient)
	if probe.ErrorKind != "disabled" {
		t.Fatalf("probe=%+v", probe)
	}
	_, err := refreshLongbridgeOptionResearch(ctx, db, "TEST", "", LongbridgeOptionResearchOptions{NewClient: func(_, _, _ string) (longbridgeOptionResearchClient, error) {
		constructed++
		return nil, errors.New("must not initialize")
	}})
	if !errors.Is(err, ErrAPIDisabled) || constructed != 0 {
		t.Fatalf("err=%v initialized=%d", err, constructed)
	}
	var calls int64
	db.Model(&APICallRecord{}).Count(&calls)
	var policy APIProviderPolicy
	db.First(&policy, "provider = ?", "longbridge")
	if calls != 0 || policy.DailyUsed != 0 {
		t.Fatalf("disabled spent budget/calls: %d/%d", policy.DailyUsed, calls)
	}
	// Two monitor instances see a saved optional-interface switch immediately.
	def, _ := APIModuleDefinitionFor("ownership")
	flags := DefaultAPIInterfaces(def)
	flags["funds"] = false
	b, _ := json.Marshal(flags)
	db.Model(&APIModulePolicy{}).Where("key = ?", def.Key).Update("interfaces_json", string(b))
	for _, monitor := range []*APIMonitor{m, NewAPIMonitor(db)} {
		if _, err := monitor.Acquire(ctx, "longbridge", "/v1/quote/fund-holders", "TEST"); !errors.Is(err, ErrAPIDisabled) {
			t.Fatal(err)
		}
	}
	record, err := m.Acquire(ctx, "longbridge", "/v1/quote/shareholders", "TEST")
	if err != nil {
		t.Fatal(err)
	}
	m.Finish(record, nil)
	db.Model(&APICallRecord{}).Count(&calls)
	if calls != 1 {
		t.Fatalf("remaining interface requests=%d", calls)
	}
}

func TestAPIModuleCompanyDisableDoesNotScheduleRetry(t *testing.T) {
	db := openMigratedTestDatabase(t)
	if err := saveLongbridgeCompanyProfileAttempt(context.Background(), db, 123, "TEST", time.Now(), ErrAPIDisabled); err != nil {
		t.Fatal(err)
	}
	var n int64
	db.Model(&CompanyProfileSnapshot{}).Where("security_id = ?", 123).Count(&n)
	if n != 0 {
		t.Fatal("disabled module created failed retry")
	}
}
