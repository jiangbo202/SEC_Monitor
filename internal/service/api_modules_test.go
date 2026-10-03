package service

import (
	"context"
	"errors"
	"net/url"
	"reflect"
	"testing"
	"time"

	"sec_monitor/internal/config"
	"sec_monitor/internal/discovery"
	"sec_monitor/internal/model"
)

func moduleInput(key string, enabled bool, revision uint) APIModuleInput {
	def, _ := discovery.APIModuleDefinitionFor(key)
	return APIModuleInput{Enabled: &enabled, Revision: revision, Interfaces: discovery.DefaultAPIInterfaces(def)}
}

func TestAPIModuleProviderChoiceConfirmationAndCapabilities(t *testing.T) {
	s := apiManagementTestService(t)
	ctx := context.Background()
	in := moduleInput("company", true, 1)
	in.Provider = "futu"
	if err := s.UpdateModule(ctx, "company", in, "test"); !errors.Is(err, ErrValidation) {
		t.Fatal("source change without confirmation", err)
	}
	in.ConfirmImpact = true
	if err := s.UpdateModule(ctx, "company", in, "test"); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateModule(ctx, "company", in, "test"); !errors.Is(err, ErrAPIConfigConflict) {
		t.Fatal("stale source change accepted", err)
	}
	if selected := discovery.APIModuleProvider(ctx, s.db, "company"); selected != "futu" {
		t.Fatal("choice not persisted", selected)
	}
	unsupported := moduleInput("temperature", true, 1)
	unsupported.Provider = "futu"
	unsupported.ConfirmImpact = true
	if err := s.UpdateModule(ctx, "temperature", unsupported, "test"); !errors.Is(err, ErrValidation) {
		t.Fatal("fake provider accepted", err)
	}
	if err := discovery.CheckAPIModuleEndpoint(ctx, s.db, "longbridge", "/v1/quote/comp-overview"); !errors.Is(err, discovery.ErrAPIDisabled) {
		t.Fatal("unselected vendor allowed", err)
	}
	if err := discovery.CheckAPIModuleEndpoint(ctx, s.db, "futu", "/api/v1.0/quote/{symbol}/company/profile"); err != nil {
		t.Fatal("selected vendor blocked", err)
	}
	ipoCtx := discovery.WithLongbridgeIPOCompanyProfile(ctx)
	if err := discovery.CheckAPIModuleEndpoint(ipoCtx, s.db, "longbridge", "/v1/quote/comp-overview"); err != nil {
		t.Fatal("independent IPO flow broken by issuer source selection", err)
	}
	s.db.Model(&discovery.APIModulePolicy{}).Where("key = ?", "company").Update("enabled", false)
	if err := discovery.CheckAPIModuleEndpoint(ipoCtx, s.db, "longbridge", "/v1/quote/comp-overview"); !errors.Is(err, discovery.ErrAPIDisabled) {
		t.Fatal("IPO bypassed shared permission switch", err)
	}
	var calls int64
	s.db.Model(&discovery.APICallRecord{}).Count(&calls)
	if calls != 0 {
		t.Fatal("saving dispatched external calls")
	}
}

func TestAPIModuleDisabledConsensusPreservesOnlySameReportCache(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	s := NewEarningsPreviewService(db, config.DiscoveryConfig{}, nil, nil)
	now := time.Now().UTC()
	report := now.AddDate(0, 0, 20)
	eps := 2.5
	previous := model.EarningsPreview{TargetID: 1, Ticker: "TEST", Provider: "longbridge", Status: "scheduled", ReportAt: &report, EPSEstimate: &eps, FiscalYear: 2026, FiscalPeriod: "Q3", EventKey: "same", FetchedAt: &now}
	if err := db.Create(&previous).Error; err != nil {
		t.Fatal(err)
	}
	next := previous
	next.EPSEstimate = nil
	next.FiscalYear = 0
	next.FiscalPeriod = ""
	next.LastError = earningsConsensusDisabledMessage
	saved, _, err := s.savePreview(ctx, next, false)
	if err != nil || saved.EPSEstimate == nil || *saved.EPSEstimate != eps || saved.FiscalPeriod != "Q3" {
		t.Fatalf("same report cleared %+v: %v", saved, err)
	}
	newReport := report.AddDate(0, 3, 0)
	next.ReportAt = &newReport
	next.EventKey = "new"
	saved, _, err = s.savePreview(ctx, next, false)
	if err != nil || saved.EPSEstimate != nil {
		t.Fatal("old report consensus copied into a different period", err)
	}
}
func TestAPIModuleValidationConfirmationVersionAndAudit(t *testing.T) {
	s := apiManagementTestService(t)
	ctx := context.Background()
	in := moduleInput("ownership", true, 1)
	in.Interfaces["holders"] = false
	if err := s.UpdateModule(ctx, "ownership", in, "tester"); !errors.Is(err, ErrValidation) {
		t.Fatal("required interface accepted", err)
	}
	off := false
	in.Enabled = &off
	if err := s.UpdateModule(ctx, "ownership", in, "tester"); !errors.Is(err, ErrValidation) {
		t.Fatal("broken dependency accepted", err)
	}
	in.Interfaces["history"] = false
	if err := s.UpdateModule(ctx, "ownership", in, "tester"); !errors.Is(err, ErrValidation) {
		t.Fatal("missing impact confirmation accepted", err)
	}
	in.ConfirmImpact = true
	if err := s.UpdateModule(ctx, "ownership", in, "tester"); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateModule(ctx, "ownership", in, "tester"); !errors.Is(err, ErrAPIConfigConflict) {
		t.Fatal("stale window overwrote policy", err)
	}
	in = moduleInput("ownership", true, 2)
	in.Interfaces["trade"] = true
	if err := s.UpdateModule(ctx, "ownership", in, "tester"); !errors.Is(err, ErrValidation) {
		t.Fatal("unknown interface accepted", err)
	}
	in = moduleInput("ownership", true, 2)
	if err := s.UpdateModule(ctx, "ownership", in, "tester"); err != nil {
		t.Fatal(err)
	}
	var row discovery.APIModulePolicy
	s.db.First(&row, "key = ?", "ownership")
	if !row.Enabled || row.Revision != 3 {
		t.Fatalf("policy=%+v", row)
	}
	var logs []model.OperationLog
	s.main.Find(&logs)
	if len(logs) != 2 {
		t.Fatalf("audit rows=%d", len(logs))
	}
	views, err := s.ModuleViews(ctx, []APIProviderSummary{{Policy: discovery.APIProviderPolicy{Provider: "longbridge", Paused: true}, CredentialConfigured: true}})
	if err != nil || len(views) != 12 {
		t.Fatalf("views=%d err=%v", len(views), err)
	}
	for _, v := range views {
		if v.Provider == "longbridge" && v.Status != "provider_paused" {
			t.Fatalf("pause not visible %+v", v)
		}
	}
}

func TestAPIModuleSharedTaskSkipAndFutuNoRenewal(t *testing.T) {
	s := apiManagementTestService(t)
	ctx := context.Background()
	s.db.Model(&discovery.APIModulePolicy{}).Where("key = ?", "eps").Update("enabled", false)
	if err := s.CheckModuleTask(ctx, "longbridge_candidate_research_sync"); err != nil {
		t.Fatal("holder task incorrectly skipped", err)
	}
	s.db.Model(&discovery.APIModulePolicy{}).Where("key = ?", "ownership").Update("enabled", false)
	if err := s.CheckModuleTask(ctx, "longbridge_candidate_research_sync"); !errors.Is(err, ErrTaskSkipped) {
		t.Fatal("disabled research not skipped", err)
	}
	// IPO and macro keep independent SEC / official sources even if Longbridge is disabled.
	s.db.Model(&discovery.APIModulePolicy{}).Where("key = ?", "calendar").Update("enabled", false)
	for _, task := range []string{"ipo_listing_reconcile_sync", "macro_calendar_sync"} {
		if err := s.CheckModuleTask(ctx, task); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.CheckModuleTask(ctx, "watch_target_earnings_sync"); !errors.Is(err, ErrTaskSkipped) {
		t.Fatal(err)
	}
	s.db.Model(&discovery.APIProviderPolicy{}).Where("provider = ?", "futu").Update("paused", false)
	s.db.Model(&discovery.APIModulePolicy{}).Where("key = ?", "futu_ownership").Update("enabled", false)
	// Without credentials this would fail in token(); the module guard must run first.
	if _, err := s.Futu.RefreshOwnership(ctx, "TEST"); !errors.Is(err, discovery.ErrAPIDisabled) {
		t.Fatal("token stage reached", err)
	}
	s.db.Create(&discovery.FutuInstitutionalReceipt{Ticker: "TEST", Status: "available", FetchedAt: time.Now()})
	if cached, err := s.Futu.RefreshOwnership(ctx, "TEST"); err != nil || !cached.Cached {
		t.Fatal("disabled module lost cache", err)
	}
}

func TestAPIPriceRoutePersistsActualOrderAndRejectsUnsafeChanges(t *testing.T) {
	s := apiManagementTestService(t)
	ctx := context.Background()
	s.cfg = config.DiscoveryConfig{PriceProvider: "longbridge,futu", LongbridgeAppKey: "fake-key", LongbridgeAppSecret: "fake-secret", LongbridgeAccessToken: "fake-token", CacheDir: t.TempDir()}
	seedFutuTestToken(t, s)
	bad := [][]string{nil, {"futu", "futu"}, {"tiingo"}, {"twelvedata"}, {"yahoo"}, {"trade"}}
	for _, order := range bad {
		if err := s.UpdatePriceRoute(ctx, APIPriceRouteInput{Order: order, Expected: s.cfg.PriceProvider}, "tester"); !errors.Is(err, ErrValidation) {
			t.Fatalf("invalid route %v: %v", order, err)
		}
	}
	if err := s.UpdatePriceRoute(ctx, APIPriceRouteInput{Order: []string{"futu"}, Expected: "stale"}, "tester"); !errors.Is(err, ErrAPIConfigConflict) {
		t.Fatal(err)
	}
	want := []string{"futu", "longbridge"}
	if err := s.UpdatePriceRoute(ctx, APIPriceRouteInput{Order: want, Expected: s.cfg.PriceProvider}, "tester"); err != nil {
		t.Fatal(err)
	}
	cfg, err := s.configs.ApplyDiscoveryConfig(ctx, s.cfg)
	if err != nil {
		t.Fatal(err)
	}
	if got := PriceRouteView(cfg).Order; !reflect.DeepEqual(got, want) {
		t.Fatalf("route=%v", got)
	}
	calendar, err := discovery.NewDatabaseMarketCalendar(s.db, discovery.DefaultNYSECalendarVersion)
	if err != nil {
		t.Fatal(err)
	}
	cfg.FutuReadJSON = func(context.Context, string, string, url.Values, []byte, any) error { return nil }
	sync := NewDiscoverySyncService(s.db, cfg)
	provider, marketErr, err := sync.buildPriceProvider(cfg, nil, calendar)
	if err != nil || marketErr != nil {
		t.Fatalf("runtime build: %v / %v", err, marketErr)
	}
	chain, ok := provider.(*discovery.PriceProviderChain)
	if !ok || !reflect.DeepEqual(chain.AllowedRecordSources(), want) {
		t.Fatal("runtime ignored saved order")
	}
	if err := s.UpdatePriceRoute(ctx, APIPriceRouteInput{Order: []string{"futu"}, Expected: s.cfg.PriceProvider}, "tester"); !errors.Is(err, ErrAPIConfigConflict) {
		t.Fatal("stale route overwrite", err)
	}
	legacy := apiManagementTestService(t)
	legacy.cfg.PriceProvider = "stooq"
	if PriceRouteView(legacy.cfg).Editable {
		t.Fatal("legacy silently editable")
	}
	if err := legacy.UpdatePriceRoute(ctx, APIPriceRouteInput{Order: []string{"futu"}, Expected: "stooq"}, "tester"); !errors.Is(err, ErrValidation) {
		t.Fatal(err)
	}
}

func TestAPIModuleMarketDisabledStillAllowsTemperature(t *testing.T) {
	s := apiManagementTestService(t)
	ctx := context.Background()
	if err := s.main.AutoMigrate(&model.MarketTrendDaily{}, &model.MarketTemperatureDaily{}); err != nil {
		t.Fatal(err)
	}
	discovery.ConfigureAPIMonitor(s.db)
	t.Cleanup(func() { discovery.ConfigureAPIMonitor(nil) })
	s.db.Model(&discovery.APIModulePolicy{}).Where("key = ?", "market").Update("enabled", false)
	service := NewMarketTrendService(s.main, nil, config.DiscoveryConfig{LongbridgeAppKey: "fake-key", LongbridgeAppSecret: "fake-secret", LongbridgeAccessToken: "fake-token"})
	service.newClient = func(_, _, _ string) (marketTrendLongbridgeClient, error) {
		t.Fatal("disabled quote initialized OTP")
		return nil, nil
	}
	service.newTemperatureClient = func(_, _, _ string) (marketTemperatureLongbridgeClient, error) {
		return &fakeMarketTemperatureClient{records: []marketTemperatureRecord{{Timestamp: time.Now().Unix(), Temperature: 60}}}, nil
	}
	result, err := service.Refresh(ctx)
	if err != nil || result.SymbolsRequested != 0 || result.TemperatureSaved != 1 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	s.db.Model(&discovery.APIModulePolicy{}).Where("key = ?", "temperature").Update("enabled", false)
	if _, err := service.Refresh(ctx); !errors.Is(err, ErrTaskSkipped) {
		t.Fatal(err)
	}
}
