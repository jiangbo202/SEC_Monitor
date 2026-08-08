package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"sec_monitor/internal/config"
	"sec_monitor/internal/model"

	lbcalendar "github.com/longbridge/openapi-go/calendar"
	lbfundamental "github.com/longbridge/openapi-go/fundamental"
	"github.com/shopspring/decimal"
)

type fakeEarningsClient struct {
	events    []lbcalendar.CalendarEventInfo
	consensus *lbfundamental.FinancialConsensus
	err       error
	calls     int
}

func (f *fakeEarningsClient) FinanceCalendar(_ context.Context, _ lbcalendar.CalendarCategory, start, _ string, _ *string) (*lbcalendar.CalendarEventsResponse, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return &lbcalendar.CalendarEventsResponse{Date: start, List: []lbcalendar.CalendarDateGroup{{Date: "2026-08-13", Infos: f.events}}}, nil
}

func (f *fakeEarningsClient) Consensus(context.Context, string) (*lbfundamental.FinancialConsensus, error) {
	return f.consensus, nil
}

func TestEarningsPreviewSyncCachesCalendarAndConsensus(t *testing.T) {
	db := testDB(t)
	target := model.WatchTarget{Ticker: "TEST", CompanyName: "Test Inc.", TargetType: "stock", Status: "enabled"}
	if err := db.Create(&target).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.August, 1, 2, 0, 0, 0, time.UTC)
	eps := decimal.NewFromFloat(1.25)
	revenue := decimal.NewFromInt(2_000_000)
	client := &fakeEarningsClient{
		events:    []lbcalendar.CalendarEventInfo{{ID: "event-1", Symbol: "TEST.US", Date: "2026-08-13", DateType: "盘后", Currency: "USD", Content: "Q2 earnings"}},
		consensus: &lbfundamental.FinancialConsensus{Currency: "USD", List: []lbfundamental.ConsensusReport{{FiscalYear: 2026, FiscalPeriod: "Q2", Details: []lbfundamental.ConsensusDetail{{Key: "eps", Estimate: &eps}, {Key: "revenue", Estimate: &revenue}}}}},
	}
	svc := NewEarningsPreviewService(db, config.DiscoveryConfig{LongbridgeAppKey: "key", LongbridgeAppSecret: "secret", LongbridgeAccessToken: "token"}, nil, nil)
	svc.now = func() time.Time { return now }
	svc.newClient = func(_, _, _ string) (longbridgeEarningsClient, error) { return client, nil }

	result, err := svc.SyncEnabled(context.Background())
	if err != nil {
		t.Fatalf("SyncEnabled: %v", err)
	}
	if result.Fetched != 1 || result.Matched != 1 || result.Failed != 0 {
		t.Fatalf("unexpected sync result: %+v", result)
	}
	view, err := svc.Get(context.Background(), target.ID)
	if err != nil || view.Preview == nil {
		t.Fatalf("Get preview=%+v err=%v", view, err)
	}
	if view.Preview.Status != earningsPreviewStatusScheduled || view.Preview.ReportAt.Format(time.DateOnly) != "2026-08-13" || view.Preview.EPSEstimate == nil || *view.Preview.EPSEstimate != 1.25 || view.Preview.RevenueEstimate == nil || *view.Preview.RevenueEstimate != 2_000_000 {
		t.Fatalf("stored preview = %+v", view.Preview)
	}
}

func TestEarningsPreviewChangeAndReminderAreDeduplicated(t *testing.T) {
	db := testDB(t)
	configs := NewConfigService(db, NewAuditService(db))
	if err := configs.EnsureDefaults(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := configs.UpsertMany(context.Background(), []ConfigInput{{Key: "earnings_preview.notify_enabled", Value: "true", ValueType: "bool", Category: "earnings_preview"}, {Key: "telegram.enabled", Value: "true", ValueType: "bool", Category: "telegram"}, {Key: "telegram.bot_token", Value: "token", ValueType: "string", Category: "telegram", Encrypted: true}, {Key: "telegram.chat_id", Value: "chat", ValueType: "string", Category: "telegram"}}, "tester"); err != nil {
		t.Fatal(err)
	}
	target := model.WatchTarget{Ticker: "NOTICE", CompanyName: "Notice Inc.", TargetType: "stock", Status: "enabled"}
	if err := db.Create(&target).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.August, 6, 8, 0, 0, 0, time.UTC)
	client := &fakeEarningsClient{events: []lbcalendar.CalendarEventInfo{{ID: "event-notice", Symbol: "NOTICE.US", Date: "2026-08-13", DateType: "盘前"}}}
	notifier := &fakeNotifier{}
	svc := NewEarningsPreviewService(db, config.DiscoveryConfig{LongbridgeAppKey: "key", LongbridgeAppSecret: "secret", LongbridgeAccessToken: "token"}, configs, notifier)
	svc.now = func() time.Time { return now }
	svc.newClient = func(_, _, _ string) (longbridgeEarningsClient, error) { return client, nil }
	if _, err := svc.SyncEnabled(context.Background()); err != nil {
		t.Fatal(err)
	}
	// The initial calendar baseline is saved silently, but the 7-day reminder
	// is delivered exactly once.
	if len(notifier.messages) != 1 {
		t.Fatalf("messages=%d, want one 7-day reminder", len(notifier.messages))
	}
	if _, err := svc.SyncEnabled(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(notifier.messages) != 1 {
		t.Fatalf("duplicate scheduler sync sent %d reminders", len(notifier.messages))
	}
}

func TestEarningsPreviewSyncRecordsMaterialCalendarChange(t *testing.T) {
	db := testDB(t)
	target := model.WatchTarget{Ticker: "CHANGE", CompanyName: "Change Inc.", TargetType: "stock", Status: "enabled"}
	if err := db.Create(&target).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.August, 1, 8, 0, 0, 0, time.UTC)
	client := &fakeEarningsClient{events: []lbcalendar.CalendarEventInfo{{ID: "change-event", Symbol: "CHANGE.US", Date: "2026-08-13", DateType: "盘后"}}}
	svc := NewEarningsPreviewService(db, config.DiscoveryConfig{LongbridgeAppKey: "key", LongbridgeAppSecret: "secret", LongbridgeAccessToken: "token"}, nil, nil)
	svc.now = func() time.Time { return now }
	svc.newClient = func(_, _, _ string) (longbridgeEarningsClient, error) { return client, nil }
	if result, err := svc.SyncEnabled(context.Background()); err != nil || result.Changed != 0 {
		t.Fatalf("first sync result=%+v err=%v", result, err)
	}
	client.events[0].Date = "2026-08-14"
	if result, err := svc.SyncEnabled(context.Background()); err != nil || result.Changed != 1 {
		t.Fatalf("changed sync result=%+v err=%v", result, err)
	}
	view, err := svc.Get(context.Background(), target.ID)
	if err != nil || view.Preview == nil {
		t.Fatalf("preview=%+v err=%v", view, err)
	}
	if got := view.Preview.ReportAt.Format(time.DateOnly); got != "2026-08-14" {
		t.Fatalf("report date=%s", got)
	}
	if !strings.Contains(view.Preview.ChangeSummary, "2026-08-13") || !strings.Contains(view.Preview.ChangeSummary, "2026-08-14") {
		t.Fatalf("change summary=%q", view.Preview.ChangeSummary)
	}
}

func TestParseReminderDays(t *testing.T) {
	got := parseReminderDays("7, 3, 1, 0, 3, bad, 100", nil)
	want := []int{7, 3, 1, 0}
	if len(got) != len(want) {
		t.Fatalf("got=%v want=%v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("got=%v want=%v", got, want)
		}
	}
}
