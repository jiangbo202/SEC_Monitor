package service

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"sec_monitor/internal/model"
	"sec_monitor/internal/sec"
	"sec_monitor/internal/telegram"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type fakeSECClient struct {
	filings         []sec.FilingResult
	filingsByTicker map[string][]sec.FilingResult
	listErrs        []error
	listErrByTicker map[string]error
	listCalls       int
	queries         []sec.FilingQuery
}

func (f fakeSECClient) LookupCIK(ctx context.Context, ticker string) (string, string, error) {
	return "0000320193", "Apple Inc.", nil
}

func (f *fakeSECClient) ListFilings(ctx context.Context, query sec.FilingQuery) ([]sec.FilingResult, error) {
	f.queries = append(f.queries, query)
	if f.listErrByTicker != nil {
		if err := f.listErrByTicker[query.Ticker]; err != nil {
			f.listCalls++
			return nil, err
		}
	}
	if f.filingsByTicker != nil {
		f.listCalls++
		return f.filingsByTicker[query.Ticker], nil
	}
	if f.listCalls < len(f.listErrs) && f.listErrs[f.listCalls] != nil {
		err := f.listErrs[f.listCalls]
		f.listCalls++
		return nil, err
	}
	f.listCalls++
	return f.filings, nil
}

type fakeNotifier struct {
	messages []telegram.Message
	errs     []error
	calls    int
}

func (f *fakeNotifier) Send(ctx context.Context, message telegram.Message) error {
	if f.calls < len(f.errs) && f.errs[f.calls] != nil {
		err := f.errs[f.calls]
		f.calls++
		return err
	}
	f.calls++
	f.messages = append(f.messages, message)
	return nil
}

func testDB(t *testing.T) *gorm.DB {
	t.Helper()

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	if err := db.AutoMigrate(
		&model.WatchTarget{},
		&model.Filing{},
		&model.SyncRun{},
		&model.SyncRunDetail{},
		&model.TaskConfig{},
		&model.SystemConfig{},
		&model.OperationLog{},
		&model.NotificationLog{},
	); err != nil {
		t.Fatalf("migrate test db: %v", err)
	}
	return db
}

func ptrTime(value time.Time) *time.Time {
	return &value
}

func TestWatchTargetServiceCreatesListsUpdatesAndAuditsTargets(t *testing.T) {
	db := testDB(t)
	audit := NewAuditService(db)
	svc := NewWatchTargetService(db, audit)

	created, err := svc.Create(context.Background(), WatchTargetInput{
		Ticker:      "aapl",
		CompanyName: "Apple Inc.",
		CIK:         "0000320193",
		TargetType:  "stock",
		Group:       "EV",
		Status:      "enabled",
	}, "tester")
	if err != nil {
		t.Fatalf("create target: %v", err)
	}
	if created.Ticker != "AAPL" {
		t.Fatalf("ticker normalized = %q, want AAPL", created.Ticker)
	}
	if created.Group != "EV" {
		t.Fatalf("group = %q, want EV", created.Group)
	}

	updated, err := svc.SetStatus(context.Background(), created.ID, "disabled", "tester")
	if err != nil {
		t.Fatalf("set status: %v", err)
	}
	if updated.Status != "disabled" {
		t.Fatalf("status = %q, want disabled", updated.Status)
	}

	page, err := svc.List(context.Background(), WatchTargetFilter{Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("list targets: %v", err)
	}
	if page.Total != 1 || len(page.Items) != 1 {
		t.Fatalf("target page total=%d len=%d, want one target", page.Total, len(page.Items))
	}
	groupPage, err := svc.List(context.Background(), WatchTargetFilter{Group: "EV", Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("list target group: %v", err)
	}
	if groupPage.Total != 1 {
		t.Fatalf("group page total = %d, want 1", groupPage.Total)
	}

	logs, err := audit.List(context.Background(), AuditLogFilter{Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("list audit logs: %v", err)
	}
	if logs.Total != 2 {
		t.Fatalf("audit total = %d, want create and status update logs", logs.Total)
	}
}

func TestConfigServicePersistsAndMasksTelegramToken(t *testing.T) {
	db := testDB(t)
	audit := NewAuditService(db)
	svc := NewConfigService(db, audit)

	if err := svc.UpsertMany(context.Background(), []ConfigInput{
		{Key: "telegram.bot_token", Value: "123456:secret-token", ValueType: "string", Category: "telegram", Encrypted: true},
		{Key: "telegram.chat_id", Value: "10001", ValueType: "string", Category: "telegram"},
		{Key: "telegram.enabled", Value: "true", ValueType: "bool", Category: "telegram"},
	}, "tester"); err != nil {
		t.Fatalf("upsert configs: %v", err)
	}

	configs, err := svc.List(context.Background(), "telegram", true)
	if err != nil {
		t.Fatalf("list configs: %v", err)
	}
	if len(configs) != 3 {
		t.Fatalf("config len = %d, want 3", len(configs))
	}
	for _, cfg := range configs {
		if cfg.ConfigKey == "telegram.bot_token" && cfg.ConfigValue == "123456:secret-token" {
			t.Fatalf("bot token was not masked")
		}
	}
}

func TestConfigServiceDefaultsTableDriven(t *testing.T) {
	tests := []struct {
		name string
		run  func(t *testing.T, db *gorm.DB, svc *ConfigService)
	}{
		{name: "ensure sec fetch defaults is idempotent", run: func(t *testing.T, db *gorm.DB, svc *ConfigService) {
			if err := svc.EnsureDefaults(context.Background()); err != nil {
				t.Fatalf("EnsureDefaults: %v", err)
			}
			if err := svc.EnsureDefaults(context.Background()); err != nil {
				t.Fatalf("EnsureDefaults second: %v", err)
			}
			configs, err := svc.List(context.Background(), "sec", false)
			if err != nil {
				t.Fatalf("List: %v", err)
			}
			if len(configs) != 4 {
				t.Fatalf("sec defaults = %d, want 4", len(configs))
			}
			settings, err := svc.SECFetchSettings(context.Background())
			if err != nil {
				t.Fatalf("SECFetchSettings: %v", err)
			}
			if settings.InitialFetchDays != 30 || settings.SyncWindowDays != 30 || settings.MaxFetchCount != 300 || settings.FetchFullHistory {
				t.Fatalf("settings = %+v", settings)
			}
		}},
		{name: "ensure ui defaults include locale and onboarding state", run: func(t *testing.T, db *gorm.DB, svc *ConfigService) {
			if err := svc.EnsureDefaults(context.Background()); err != nil {
				t.Fatalf("EnsureDefaults: %v", err)
			}
			if err := svc.EnsureDefaults(context.Background()); err != nil {
				t.Fatalf("EnsureDefaults second: %v", err)
			}
			configs, err := svc.List(context.Background(), "ui", false)
			if err != nil {
				t.Fatalf("List: %v", err)
			}
			values := map[string]string{}
			for _, cfg := range configs {
				values[cfg.ConfigKey] = cfg.ConfigValue
			}
			if values["ui.default_locale"] != "zh-CN" || values["ui.onboarding_completed"] != "false" {
				t.Fatalf("ui defaults = %+v", values)
			}
		}},
		{name: "ensure notification defaults are usable", run: func(t *testing.T, db *gorm.DB, svc *ConfigService) {
			if err := svc.EnsureDefaults(context.Background()); err != nil {
				t.Fatalf("EnsureDefaults: %v", err)
			}
			settings, err := svc.NotificationSettings(context.Background())
			if err != nil {
				t.Fatalf("NotificationSettings: %v", err)
			}
			if settings.ImportantOnly || settings.QuietHoursEnabled || settings.QuietHoursStart != "22:00" || settings.QuietHoursEnd != "08:00" {
				t.Fatalf("settings = %+v", settings)
			}
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := testDB(t)
			tt.run(t, db, NewConfigService(db, NewAuditService(db)))
		})
	}
}

func TestShouldNotifyFilingTableDriven(t *testing.T) {
	now := time.Date(2026, 6, 18, 10, 30, 0, 0, time.UTC)
	filing := model.Filing{FilingType: "8-K", Title: "Merger agreement", CompanyName: "Acme Inc."}
	tests := []struct {
		name     string
		settings NotificationSettings
		want     bool
	}{
		{name: "default allows notification", settings: NotificationSettings{}, want: true},
		{name: "important only allows 8-K", settings: NotificationSettings{ImportantOnly: true}, want: true},
		{name: "filing type mismatch blocks", settings: NotificationSettings{FilingTypes: []string{"10-K"}}, want: false},
		{name: "keyword match allows", settings: NotificationSettings{Keywords: []string{"merger"}}, want: true},
		{name: "keyword mismatch blocks", settings: NotificationSettings{Keywords: []string{"bankruptcy"}}, want: false},
		{name: "quiet hours blocks", settings: NotificationSettings{QuietHoursEnabled: true, QuietHoursStart: "09:00", QuietHoursEnd: "11:00"}, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldNotifyFiling(filing, tt.settings, now); got != tt.want {
				t.Fatalf("shouldNotifyFiling = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestFilingServiceRefreshesEnabledTargetsDeduplicatesAndNotifies(t *testing.T) {
	db := testDB(t)
	audit := NewAuditService(db)
	targets := NewWatchTargetService(db, audit)
	configs := NewConfigService(db, audit)
	notifier := &fakeNotifier{}
	filingDate := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)

	if _, err := targets.Create(context.Background(), WatchTargetInput{
		Ticker: "AAPL", CompanyName: "Apple Inc.", CIK: "0000320193", TargetType: "stock", Status: "enabled",
	}, "tester"); err != nil {
		t.Fatalf("create target: %v", err)
	}
	if err := configs.UpsertMany(context.Background(), []ConfigInput{
		{Key: "telegram.enabled", Value: "true", ValueType: "bool", Category: "telegram"},
		{Key: "telegram.chat_id", Value: "10001", ValueType: "string", Category: "telegram"},
		{Key: "telegram.bot_token", Value: "token", ValueType: "string", Category: "telegram", Encrypted: true},
	}, "tester"); err != nil {
		t.Fatalf("upsert telegram config: %v", err)
	}

	svc := NewFilingService(db, &fakeSECClient{filings: []sec.FilingResult{{
		FilingID: "0000320193-26-000001", AccessionNumber: "0000320193-26-000001",
		Ticker: "AAPL", CIK: "0000320193", CompanyName: "Apple Inc.",
		FilingType: "8-K", FilingDate: filingDate, FilingURL: "https://sec.gov/aapl/8k", Title: "Current report",
	}}}, notifier, configs)

	first, err := svc.Refresh(context.Background())
	if err != nil {
		t.Fatalf("refresh filings: %v", err)
	}
	second, err := svc.Refresh(context.Background())
	if err != nil {
		t.Fatalf("refresh filings second time: %v", err)
	}
	if first.NewFilings != 1 || second.NewFilings != 0 {
		t.Fatalf("new filings first=%d second=%d, want 1 then 0", first.NewFilings, second.NewFilings)
	}
	if len(notifier.messages) != 1 {
		t.Fatalf("notifications = %d, want only one", len(notifier.messages))
	}

	page, err := svc.List(context.Background(), FilingFilter{Ticker: "AAPL", Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("list filings: %v", err)
	}
	if page.Total != 1 || page.Items[0].FilingType != "8-K" {
		t.Fatalf("filing page total=%d type=%q, want one 8-K", page.Total, page.Items[0].FilingType)
	}

	var target model.WatchTarget
	if err := db.Where("ticker = ?", "AAPL").First(&target).Error; err != nil {
		t.Fatalf("load target: %v", err)
	}
	if target.LastSyncStatus != "success" || target.LastNewFilings != 0 || target.LastSyncAt == nil {
		t.Fatalf("target sync status = %+v", target)
	}

	runs, err := svc.ListSyncRuns(context.Background(), SyncRunFilter{Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("list sync runs: %v", err)
	}
	if runs.Total != 2 || runs.Items[0].Status != "success" {
		t.Fatalf("sync runs = %+v", runs)
	}
}

func TestFilingServiceRefreshAppliesPullSettingsTableDriven(t *testing.T) {
	now := time.Now().UTC()
	tests := []struct {
		name          string
		target        model.WatchTarget
		configs       []ConfigInput
		filings       []sec.FilingResult
		wantInserted  int64
		wantFullFetch bool
	}{
		{
			name:   "first sync filters by days and max count",
			target: model.WatchTarget{Ticker: "TSLA", CompanyName: "Tesla Inc.", CIK: "0001318605", TargetType: "stock", Status: "enabled"},
			configs: []ConfigInput{
				{Key: "sec.initial_fetch_days", Value: "30", ValueType: "int", Category: "sec"},
				{Key: "sec.max_fetch_count", Value: "2", ValueType: "int", Category: "sec"},
				{Key: "sec.fetch_full_history", Value: "true", ValueType: "bool", Category: "sec"},
			},
			filings: []sec.FilingResult{
				{FilingID: "new-1", AccessionNumber: "new-1", Ticker: "TSLA", CIK: "0001318605", CompanyName: "Tesla Inc.", FilingType: "8-K", FilingDate: now.AddDate(0, 0, -1)},
				{FilingID: "new-2", AccessionNumber: "new-2", Ticker: "TSLA", CIK: "0001318605", CompanyName: "Tesla Inc.", FilingType: "10-Q", FilingDate: now.AddDate(0, 0, -2)},
				{FilingID: "new-3", AccessionNumber: "new-3", Ticker: "TSLA", CIK: "0001318605", CompanyName: "Tesla Inc.", FilingType: "4", FilingDate: now.AddDate(0, 0, -3)},
				{FilingID: "old-1", AccessionNumber: "old-1", Ticker: "TSLA", CIK: "0001318605", CompanyName: "Tesla Inc.", FilingType: "8-K", FilingDate: now.AddDate(0, 0, -45)},
			},
			wantInserted:  2,
			wantFullFetch: true,
		},
		{
			name:   "existing sync ignores initial days",
			target: model.WatchTarget{Ticker: "TSLA", CompanyName: "Tesla Inc.", CIK: "0001318605", TargetType: "stock", Status: "enabled", LastSyncAt: ptrTime(now.AddDate(0, 0, -1))},
			configs: []ConfigInput{
				{Key: "sec.initial_fetch_days", Value: "1", ValueType: "int", Category: "sec"},
				{Key: "sec.sync_window_days", Value: "0", ValueType: "int", Category: "sec"},
				{Key: "sec.max_fetch_count", Value: "0", ValueType: "int", Category: "sec"},
			},
			filings: []sec.FilingResult{
				{FilingID: "old-1", AccessionNumber: "old-1", Ticker: "TSLA", CIK: "0001318605", CompanyName: "Tesla Inc.", FilingType: "8-K", FilingDate: now.AddDate(0, 0, -45)},
			},
			wantInserted: 1,
		},
		{
			name:   "sync window filters every sync",
			target: model.WatchTarget{Ticker: "TSLA", CompanyName: "Tesla Inc.", CIK: "0001318605", TargetType: "stock", Status: "enabled", LastSyncAt: ptrTime(now.AddDate(0, 0, -1))},
			configs: []ConfigInput{
				{Key: "sec.initial_fetch_days", Value: "3650", ValueType: "int", Category: "sec"},
				{Key: "sec.sync_window_days", Value: "30", ValueType: "int", Category: "sec"},
				{Key: "sec.max_fetch_count", Value: "0", ValueType: "int", Category: "sec"},
			},
			filings: []sec.FilingResult{
				{FilingID: "recent-1", AccessionNumber: "recent-1", Ticker: "TSLA", CIK: "0001318605", CompanyName: "Tesla Inc.", FilingType: "8-K", FilingDate: now.AddDate(0, 0, -2)},
				{FilingID: "old-1", AccessionNumber: "old-1", Ticker: "TSLA", CIK: "0001318605", CompanyName: "Tesla Inc.", FilingType: "8-K", FilingDate: now.AddDate(0, 0, -45)},
			},
			wantInserted: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := testDB(t)
			if err := db.Create(&tt.target).Error; err != nil {
				t.Fatalf("seed target: %v", err)
			}
			configs := NewConfigService(db, NewAuditService(db))
			if len(tt.configs) > 0 {
				if err := configs.UpsertMany(context.Background(), tt.configs, "tester"); err != nil {
					t.Fatalf("seed configs: %v", err)
				}
			}
			secClient := &fakeSECClient{filings: tt.filings}
			svc := NewFilingService(db, secClient, &fakeNotifier{}, configs)

			if _, err := svc.Refresh(context.Background()); err != nil {
				t.Fatalf("Refresh: %v", err)
			}

			var count int64
			if err := db.Model(&model.Filing{}).Where("ticker = ?", "TSLA").Count(&count).Error; err != nil {
				t.Fatalf("count filings: %v", err)
			}
			if count != tt.wantInserted {
				t.Fatalf("inserted = %d, want %d", count, tt.wantInserted)
			}
			if len(secClient.queries) != 1 {
				t.Fatalf("queries = %d, want 1", len(secClient.queries))
			}
			if secClient.queries[0].FetchFullHistory != tt.wantFullFetch {
				t.Fatalf("FetchFullHistory = %v, want %v", secClient.queries[0].FetchFullHistory, tt.wantFullFetch)
			}
		})
	}
}

func TestFilingServiceRefreshTargetAndDetailsTableDriven(t *testing.T) {
	now := time.Now().UTC()
	tests := []struct {
		name       string
		seed       []model.WatchTarget
		secClient  *fakeSECClient
		run        func(context.Context, *FilingService) (RefreshResult, error)
		wantCalls  int
		wantFailed int
		assert     func(t *testing.T, db *gorm.DB, secClient *fakeSECClient, result RefreshResult)
	}{
		{
			name: "refresh target only syncs selected ticker",
			seed: []model.WatchTarget{
				{Ticker: "AAPL", CompanyName: "Apple Inc.", CIK: "0000320193", TargetType: "stock", Status: "enabled"},
				{Ticker: "MSFT", CompanyName: "Microsoft Corp.", CIK: "0000789019", TargetType: "stock", Status: "enabled"},
			},
			secClient: &fakeSECClient{filingsByTicker: map[string][]sec.FilingResult{
				"AAPL": {{FilingID: "aapl-1", AccessionNumber: "aapl-1", Ticker: "AAPL", CIK: "0000320193", CompanyName: "Apple Inc.", FilingType: "8-K", FilingDate: now}},
				"MSFT": {{FilingID: "msft-1", AccessionNumber: "msft-1", Ticker: "MSFT", CIK: "0000789019", CompanyName: "Microsoft Corp.", FilingType: "8-K", FilingDate: now}},
			}},
			run: func(ctx context.Context, svc *FilingService) (RefreshResult, error) {
				return svc.RefreshTarget(ctx, 1)
			},
			wantCalls: 1,
			assert: func(t *testing.T, db *gorm.DB, secClient *fakeSECClient, result RefreshResult) {
				if result.TargetsChecked != 1 || result.NewFilings != 1 {
					t.Fatalf("result = %+v", result)
				}
				if secClient.queries[0].Ticker != "AAPL" {
					t.Fatalf("queried ticker = %q, want AAPL", secClient.queries[0].Ticker)
				}
				var count int64
				if err := db.Model(&model.Filing{}).Where("ticker = ?", "MSFT").Count(&count).Error; err != nil {
					t.Fatalf("count msft: %v", err)
				}
				if count != 0 {
					t.Fatalf("MSFT filings = %d, want 0", count)
				}
			},
		},
		{
			name: "refresh records per target failure detail",
			seed: []model.WatchTarget{
				{Ticker: "AAPL", CompanyName: "Apple Inc.", CIK: "0000320193", TargetType: "stock", Status: "enabled"},
			},
			secClient: &fakeSECClient{listErrByTicker: map[string]error{"AAPL": fmt.Errorf("sec timeout")}},
			run: func(ctx context.Context, svc *FilingService) (RefreshResult, error) {
				return svc.Refresh(ctx)
			},
			wantCalls:  3,
			wantFailed: 1,
			assert: func(t *testing.T, db *gorm.DB, secClient *fakeSECClient, result RefreshResult) {
				details, err := NewFilingService(db, secClient, &fakeNotifier{}, NewConfigService(db, NewAuditService(db))).ListSyncRunDetails(context.Background(), result.SyncRunID)
				if err != nil {
					t.Fatalf("ListSyncRunDetails: %v", err)
				}
				if len(details) != 1 || details[0].Ticker != "AAPL" || details[0].Status != "failed" || details[0].ErrorMessage == "" {
					t.Fatalf("details = %+v", details)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := testDB(t)
			if err := db.Create(&tt.seed).Error; err != nil {
				t.Fatalf("seed targets: %v", err)
			}
			configs := NewConfigService(db, NewAuditService(db))
			svc := NewFilingService(db, tt.secClient, &fakeNotifier{}, configs)
			result, err := tt.run(context.Background(), svc)
			if err != nil {
				t.Fatalf("run: %v", err)
			}
			if tt.secClient.listCalls != tt.wantCalls {
				t.Fatalf("listCalls = %d, want %d", tt.secClient.listCalls, tt.wantCalls)
			}
			if result.FailedTargets != tt.wantFailed {
				t.Fatalf("FailedTargets = %d, want %d", result.FailedTargets, tt.wantFailed)
			}
			tt.assert(t, db, tt.secClient, result)
		})
	}
}

func TestWatchTargetServiceValidationTableDriven(t *testing.T) {
	tests := []struct {
		name  string
		input WatchTargetInput
	}{
		{name: "missing ticker", input: WatchTargetInput{CompanyName: "Apple Inc.", TargetType: "stock", Status: "enabled"}},
		{name: "missing company", input: WatchTargetInput{Ticker: "AAPL", TargetType: "stock", Status: "enabled"}},
		{name: "invalid type", input: WatchTargetInput{Ticker: "AAPL", CompanyName: "Apple Inc.", TargetType: "fund", Status: "enabled"}},
		{name: "invalid status", input: WatchTargetInput{Ticker: "AAPL", CompanyName: "Apple Inc.", TargetType: "stock", Status: "paused"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := testDB(t)
			svc := NewWatchTargetService(db, NewAuditService(db))
			if _, err := svc.Create(context.Background(), tt.input, "tester"); !errors.Is(err, ErrValidation) {
				t.Fatalf("Create err = %v, want validation", err)
			}
		})
	}
}

func TestWatchTargetServiceMutationsTableDriven(t *testing.T) {
	tests := []struct {
		name   string
		action func(t *testing.T, svc *WatchTargetService, id uint)
	}{
		{name: "get existing target", action: func(t *testing.T, svc *WatchTargetService, id uint) {
			got, err := svc.Get(context.Background(), id)
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			if got.Ticker != "AAPL" {
				t.Fatalf("ticker = %q", got.Ticker)
			}
		}},
		{name: "update existing target", action: func(t *testing.T, svc *WatchTargetService, id uint) {
			got, err := svc.Update(context.Background(), id, WatchTargetInput{Ticker: "MSFT", CompanyName: "Microsoft Corp.", TargetType: "stock", Status: "enabled"}, "tester")
			if err != nil {
				t.Fatalf("Update: %v", err)
			}
			if got.Ticker != "MSFT" {
				t.Fatalf("ticker = %q", got.Ticker)
			}
		}},
		{name: "delete existing target", action: func(t *testing.T, svc *WatchTargetService, id uint) {
			if err := svc.Delete(context.Background(), id, "tester"); err != nil {
				t.Fatalf("Delete: %v", err)
			}
			if _, err := svc.Get(context.Background(), id); !errors.Is(err, ErrNotFound) {
				t.Fatalf("Get after delete err = %v, want not found", err)
			}
		}},
		{name: "invalid status returns validation", action: func(t *testing.T, svc *WatchTargetService, id uint) {
			if _, err := svc.SetStatus(context.Background(), id, "paused", "tester"); !errors.Is(err, ErrValidation) {
				t.Fatalf("SetStatus err = %v, want validation", err)
			}
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := testDB(t)
			svc := NewWatchTargetService(db, NewAuditService(db))
			target, err := svc.Create(context.Background(), WatchTargetInput{Ticker: "AAPL", CompanyName: "Apple Inc.", TargetType: "stock", Status: "enabled"}, "tester")
			if err != nil {
				t.Fatalf("Create: %v", err)
			}
			tt.action(t, svc, target.ID)
		})
	}
}

func TestConfigHelpersTableDriven(t *testing.T) {
	maskTests := []struct {
		name string
		in   string
		want string
	}{
		{name: "empty", in: "", want: ""},
		{name: "short", in: "abc", want: "******"},
		{name: "long", in: "123456:secret-token", want: "123******ken"},
	}

	for _, tt := range maskTests {
		t.Run(tt.name, func(t *testing.T) {
			if got := maskSecret(tt.in); got != tt.want {
				t.Fatalf("maskSecret = %q, want %q", got, tt.want)
			}
		})
	}

	maskedTests := []struct {
		name string
		in   string
		want bool
	}{
		{name: "masked", in: "tok******ken", want: true},
		{name: "not masked", in: "token", want: false},
		{name: "empty", in: "", want: false},
	}

	for _, tt := range maskedTests {
		t.Run("is masked "+tt.name, func(t *testing.T) {
			if got := IsMaskedSecret(tt.in); got != tt.want {
				t.Fatalf("IsMaskedSecret = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestTaskConfigServiceTableDriven(t *testing.T) {
	tests := []struct {
		name string
		run  func(t *testing.T, svc *TaskConfigService)
	}{
		{name: "ensure default is idempotent", run: func(t *testing.T, svc *TaskConfigService) {
			if err := svc.EnsureDefault(context.Background()); err != nil {
				t.Fatalf("EnsureDefault: %v", err)
			}
			if err := svc.EnsureDefault(context.Background()); err != nil {
				t.Fatalf("EnsureDefault second: %v", err)
			}
			tasks, err := svc.List(context.Background())
			if err != nil {
				t.Fatalf("List: %v", err)
			}
			if len(tasks) != 1 {
				t.Fatalf("tasks = %d, want 1", len(tasks))
			}
		}},
		{name: "update task", run: func(t *testing.T, svc *TaskConfigService) {
			if err := svc.EnsureDefault(context.Background()); err != nil {
				t.Fatalf("EnsureDefault: %v", err)
			}
			tasks, _ := svc.List(context.Background())
			updated, err := svc.Update(context.Background(), tasks[0].ID, TaskConfigInput{CronExpr: "*/30 * * * *", Enabled: false}, "tester")
			if err != nil {
				t.Fatalf("Update: %v", err)
			}
			if updated.CronExpr != "*/30 * * * *" || updated.Enabled {
				t.Fatalf("updated = %+v", updated)
			}
		}},
		{name: "missing task returns not found", run: func(t *testing.T, svc *TaskConfigService) {
			_, err := svc.Update(context.Background(), 404, TaskConfigInput{CronExpr: "* * * * *", Enabled: true}, "tester")
			if !errors.Is(err, ErrNotFound) {
				t.Fatalf("Update err = %v, want not found", err)
			}
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := testDB(t)
			tt.run(t, NewTaskConfigService(db, NewAuditService(db)))
		})
	}
}

func TestNotificationServiceListTableDriven(t *testing.T) {
	tests := []struct {
		name   string
		filter NotificationLogFilter
		want   int64
	}{
		{name: "all", filter: NotificationLogFilter{Page: 1, PageSize: 20}, want: 2},
		{name: "by status", filter: NotificationLogFilter{Status: "failed", Page: 1, PageSize: 20}, want: 1},
		{name: "by channel", filter: NotificationLogFilter{Channel: "telegram", Page: 1, PageSize: 20}, want: 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := testDB(t)
			if err := db.Create(&[]model.NotificationLog{
				{FilingID: "1", Channel: "telegram", Status: "success"},
				{FilingID: "2", Channel: "telegram", Status: "failed"},
			}).Error; err != nil {
				t.Fatalf("seed logs: %v", err)
			}
			got, err := NewNotificationService(db).List(context.Background(), tt.filter)
			if err != nil {
				t.Fatalf("List: %v", err)
			}
			if got.Total != tt.want {
				t.Fatalf("total = %d, want %d", got.Total, tt.want)
			}
		})
	}
}

func TestFilingServiceTableDriven(t *testing.T) {
	filingDate := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		run  func(t *testing.T, db *gorm.DB, svc *FilingService)
	}{
		{name: "get existing filing", run: func(t *testing.T, db *gorm.DB, svc *FilingService) {
			filing := model.Filing{FilingID: "f1", Ticker: "AAPL", CompanyName: "Apple Inc.", FilingType: "10-K", FilingDate: filingDate, PulledAt: time.Now()}
			if err := db.Create(&filing).Error; err != nil {
				t.Fatalf("seed filing: %v", err)
			}
			got, err := svc.Get(context.Background(), filing.ID)
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			if got.FilingID != "f1" {
				t.Fatalf("filing id = %q", got.FilingID)
			}
		}},
		{name: "get missing filing", run: func(t *testing.T, db *gorm.DB, svc *FilingService) {
			if _, err := svc.Get(context.Background(), 99); !errors.Is(err, ErrNotFound) {
				t.Fatalf("Get err = %v, want not found", err)
			}
		}},
		{name: "list filters by company type and date range", run: func(t *testing.T, db *gorm.DB, svc *FilingService) {
			if err := db.Create(&[]model.Filing{
				{FilingID: "a", Ticker: "AAPL", CompanyName: "Apple Inc.", FilingType: "8-K", FilingDate: filingDate, PulledAt: time.Now()},
				{FilingID: "m", Ticker: "MSFT", CompanyName: "Microsoft Corp.", FilingType: "10-Q", FilingDate: filingDate.AddDate(0, 0, 2), PulledAt: time.Now()},
			}).Error; err != nil {
				t.Fatalf("seed filings: %v", err)
			}
			from := filingDate.AddDate(0, 0, -1)
			to := filingDate.AddDate(0, 0, 1)
			got, err := svc.List(context.Background(), FilingFilter{CompanyName: "Apple", FilingType: "8-K", DateFrom: &from, DateTo: &to, Page: -1, PageSize: 500})
			if err != nil {
				t.Fatalf("List: %v", err)
			}
			if got.Total != 1 || got.Page != 1 || got.PageSize != 200 {
				t.Fatalf("page = %+v", got)
			}
		}},
		{name: "list sorts by sync time ascending", run: func(t *testing.T, db *gorm.DB, svc *FilingService) {
			if err := db.Create(&[]model.Filing{
				{FilingID: "late", Ticker: "AAPL", CompanyName: "Apple Inc.", FilingType: "8-K", FilingDate: filingDate, PulledAt: filingDate.Add(2 * time.Hour)},
				{FilingID: "early", Ticker: "AAPL", CompanyName: "Apple Inc.", FilingType: "10-Q", FilingDate: filingDate, PulledAt: filingDate.Add(time.Hour)},
			}).Error; err != nil {
				t.Fatalf("seed filings: %v", err)
			}
			got, err := svc.List(context.Background(), FilingFilter{SortBy: "pulled_at", SortOrder: "asc", Page: 1, PageSize: 10})
			if err != nil {
				t.Fatalf("List: %v", err)
			}
			if len(got.Items) != 2 || got.Items[0].FilingID != "early" {
				t.Fatalf("sorted items = %+v", got.Items)
			}
		}},
		{name: "list includes latest notification status", run: func(t *testing.T, db *gorm.DB, svc *FilingService) {
			if err := db.Create(&[]model.Filing{
				{FilingID: "notified", Ticker: "AAPL", CompanyName: "Apple Inc.", FilingType: "8-K", FilingDate: filingDate, PulledAt: filingDate.Add(2 * time.Hour)},
				{FilingID: "silent", Ticker: "AAPL", CompanyName: "Apple Inc.", FilingType: "10-Q", FilingDate: filingDate, PulledAt: filingDate.Add(time.Hour)},
			}).Error; err != nil {
				t.Fatalf("seed filings: %v", err)
			}
			if err := db.Create(&model.NotificationLog{FilingID: "notified", Channel: "telegram", Status: "success", RetryCount: 0}).Error; err != nil {
				t.Fatalf("seed notification: %v", err)
			}
			got, err := svc.List(context.Background(), FilingFilter{SortBy: "pulled_at", SortOrder: "desc", Page: 1, PageSize: 10})
			if err != nil {
				t.Fatalf("List: %v", err)
			}
			if got.Items[0].FilingID != "notified" || got.Items[0].NotificationStatus != "success" || got.Items[0].NotificationLogID == 0 {
				t.Fatalf("notified item = %+v", got.Items[0])
			}
			if got.Items[1].FilingID != "silent" || got.Items[1].NotificationStatus != "" || got.Items[1].NotificationLogID != 0 {
				t.Fatalf("silent item = %+v", got.Items[1])
			}
		}},
		{name: "list filters by latest notification status", run: func(t *testing.T, db *gorm.DB, svc *FilingService) {
			if err := db.Create(&[]model.Filing{
				{FilingID: "ok", Ticker: "AAPL", CompanyName: "Apple Inc.", FilingType: "8-K", FilingDate: filingDate, PulledAt: filingDate.Add(3 * time.Hour)},
				{FilingID: "failed", Ticker: "MSFT", CompanyName: "Microsoft Corp.", FilingType: "10-Q", FilingDate: filingDate, PulledAt: filingDate.Add(2 * time.Hour)},
				{FilingID: "none", Ticker: "TSLA", CompanyName: "Tesla Inc.", FilingType: "10-K", FilingDate: filingDate, PulledAt: filingDate.Add(time.Hour)},
			}).Error; err != nil {
				t.Fatalf("seed filings: %v", err)
			}
			if err := db.Create(&[]model.NotificationLog{
				{FilingID: "ok", Channel: "telegram", Status: "success", RetryCount: 0, CreatedAt: filingDate.Add(time.Hour)},
				{FilingID: "failed", Channel: "telegram", Status: "success", RetryCount: 0, CreatedAt: filingDate.Add(time.Hour)},
				{FilingID: "failed", Channel: "telegram", Status: "failed", RetryCount: 3, CreatedAt: filingDate.Add(2 * time.Hour)},
			}).Error; err != nil {
				t.Fatalf("seed notifications: %v", err)
			}
			tests := []struct {
				name   string
				status string
				wantID string
			}{
				{name: "success", status: "success", wantID: "ok"},
				{name: "failed", status: "failed", wantID: "failed"},
				{name: "unnotified", status: "unnotified", wantID: "none"},
			}
			for _, tt := range tests {
				t.Run(tt.name, func(t *testing.T) {
					got, err := svc.List(context.Background(), FilingFilter{NotificationStatus: tt.status, Page: 1, PageSize: 10})
					if err != nil {
						t.Fatalf("List: %v", err)
					}
					if got.Total != 1 || len(got.Items) != 1 || got.Items[0].FilingID != tt.wantID {
						t.Fatalf("status %q got total=%d items=%+v, want %s", tt.status, got.Total, got.Items, tt.wantID)
					}
				})
			}
		}},
		{name: "cleanup preview and execute uses retention days", run: func(t *testing.T, db *gorm.DB, svc *FilingService) {
			now := time.Now().UTC()
			if err := db.Create(&[]model.Filing{
				{FilingID: "old", Ticker: "AAPL", CompanyName: "Apple Inc.", FilingType: "8-K", FilingDate: now.AddDate(0, 0, -40), PulledAt: now.AddDate(0, 0, -40)},
				{FilingID: "new", Ticker: "AAPL", CompanyName: "Apple Inc.", FilingType: "8-K", FilingDate: now, PulledAt: now},
			}).Error; err != nil {
				t.Fatalf("seed filings: %v", err)
			}
			preview, err := svc.CleanupPreview(context.Background(), 30, now)
			if err != nil {
				t.Fatalf("CleanupPreview: %v", err)
			}
			if preview.DeleteCount != 1 || preview.RetentionDays != 30 {
				t.Fatalf("preview = %+v", preview)
			}
			deleted, err := svc.Cleanup(context.Background(), 30, now)
			if err != nil {
				t.Fatalf("Cleanup: %v", err)
			}
			if deleted != 1 {
				t.Fatalf("deleted = %d, want 1", deleted)
			}
		}},
		{name: "create filing validates filing id", run: func(t *testing.T, db *gorm.DB, svc *FilingService) {
			if _, err := svc.createFilingIfNew(context.Background(), model.Filing{}); !errors.Is(err, ErrValidation) {
				t.Fatalf("createFilingIfNew err = %v, want validation", err)
			}
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := testDB(t)
			configs := NewConfigService(db, NewAuditService(db))
			svc := NewFilingService(db, &fakeSECClient{}, &fakeNotifier{}, configs)
			tt.run(t, db, svc)
		})
	}
}

func TestSendWithRetryTableDriven(t *testing.T) {
	tests := []struct {
		name      string
		errs      []error
		wantErr   bool
		wantCalls int
	}{
		{name: "succeeds first try", wantCalls: 1},
		{name: "retries then succeeds", errs: []error{errors.New("temporary")}, wantCalls: 2},
		{name: "returns final error", errs: []error{errors.New("one"), errors.New("two"), errors.New("three")}, wantErr: true, wantCalls: 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			notifier := &fakeNotifier{errs: tt.errs}
			err := sendWithRetry(context.Background(), notifier, telegram.Message{Text: "hello"}, 3)
			if tt.wantErr && err == nil {
				t.Fatalf("sendWithRetry expected error")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("sendWithRetry: %v", err)
			}
			if notifier.calls != tt.wantCalls {
				t.Fatalf("calls = %d, want %d", notifier.calls, tt.wantCalls)
			}
		})
	}
}
