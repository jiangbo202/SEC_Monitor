package service

import (
	"context"
	"testing"
	"time"

	"sec_monitor/internal/model"
)

func TestInAppNotificationCreateDeduplicatesAndTracksReadState(t *testing.T) {
	db := testDB(t)
	if err := db.AutoMigrate(&model.InAppNotification{}); err != nil {
		t.Fatalf("migrate in-app notifications: %v", err)
	}
	now := time.Date(2026, 8, 12, 0, 0, 0, 0, time.UTC)
	service := NewInAppNotificationService(db)
	service.now = func() time.Time { return now }
	input := InAppNotificationInput{EventKey: "technical:watch:1:entry:2026-08-12", Source: "technical_signal", Scope: "watch_target", EntityKind: "trade_setup", TargetID: 1, Ticker: "acme", Title: "技术信号变化", OccurredAt: now}

	created, inserted, err := service.Create(context.Background(), input)
	if err != nil || !inserted || created.Ticker != "ACME" {
		t.Fatalf("first Create = (%+v, %v, %v)", created, inserted, err)
	}
	duplicated, inserted, err := service.Create(context.Background(), input)
	if err != nil || inserted || duplicated.ID != created.ID {
		t.Fatalf("duplicate Create = (%+v, %v, %v)", duplicated, inserted, err)
	}
	if count, err := service.UnreadCount(context.Background()); err != nil || count != 1 {
		t.Fatalf("UnreadCount = (%d, %v), want (1, nil)", count, err)
	}
	changed, err := service.MarkRead(context.Background(), created.ID)
	if err != nil || !changed {
		t.Fatalf("MarkRead = (%v, %v)", changed, err)
	}
	if count, err := service.UnreadCount(context.Background()); err != nil || count != 0 {
		t.Fatalf("UnreadCount after read = (%d, %v), want (0, nil)", count, err)
	}
	second := input
	second.EventKey = "technical:watch:1:exit:2026-08-12"
	if _, inserted, err := service.Create(context.Background(), second); err != nil || !inserted {
		t.Fatalf("second Create = (%v, %v)", inserted, err)
	}
	third := input
	third.EventKey = "technical:watch:1:watching:2026-08-12"
	if _, inserted, err := service.Create(context.Background(), third); err != nil || !inserted {
		t.Fatalf("third Create = (%v, %v)", inserted, err)
	}
	changedAll, err := service.MarkAllRead(context.Background())
	if err != nil || changedAll != 2 {
		t.Fatalf("MarkAllRead = (%d, %v), want (2, nil)", changedAll, err)
	}
	if changedAll, err := service.MarkAllRead(context.Background()); err != nil || changedAll != 0 {
		t.Fatalf("second MarkAllRead = (%d, %v), want (0, nil)", changedAll, err)
	}
	page, err := service.List(context.Background(), InAppNotificationFilter{UnreadOnly: true, Page: 1, PageSize: 20})
	if err != nil || page.Total != 0 {
		t.Fatalf("unread List = (%+v, %v)", page, err)
	}
}

func TestInAppNotificationListOrdersByCreationTimeNotFutureEventTime(t *testing.T) {
	db := testDB(t)
	if err := db.AutoMigrate(&model.InAppNotification{}); err != nil {
		t.Fatalf("migrate in-app notifications: %v", err)
	}
	now := time.Date(2026, 8, 13, 15, 0, 0, 0, time.UTC)
	service := NewInAppNotificationService(db)
	service.now = func() time.Time { return now }
	if _, inserted, err := service.Create(context.Background(), InAppNotificationInput{EventKey: "earnings:future", Source: "earnings_preview", Title: "未来财报预告", OccurredAt: now.Add(24 * time.Hour)}); err != nil || !inserted {
		t.Fatalf("create future earnings notification = (%v, %v), want (true, nil)", inserted, err)
	}
	now = now.Add(time.Minute)
	if _, inserted, err := service.Create(context.Background(), InAppNotificationInput{EventKey: "technical:latest", Source: "technical_signal", Title: "最新技术信号", OccurredAt: now.Add(-24 * time.Hour)}); err != nil || !inserted {
		t.Fatalf("create latest technical notification = (%v, %v), want (true, nil)", inserted, err)
	}
	page, err := service.List(context.Background(), InAppNotificationFilter{Page: 1, PageSize: 20})
	if err != nil || len(page.Items) != 2 || page.Items[0].EventKey != "technical:latest" {
		t.Fatalf("List = (%+v, %v), want latest created notification first", page.Items, err)
	}
}

func TestInAppFilingClassifiers(t *testing.T) {
	tests := []struct {
		form           string
		major, insider bool
	}{
		{form: "8-K", major: true},
		{form: "S-3/A", major: true},
		{form: "4", insider: true},
		{form: "4/A", insider: true},
		{form: "10-Q"},
	}
	for _, test := range tests {
		if actual := isMajorEventFiling(test.form); actual != test.major {
			t.Errorf("isMajorEventFiling(%q) = %v, want %v", test.form, actual, test.major)
		}
		if actual := isInsiderFiling(test.form); actual != test.insider {
			t.Errorf("isInsiderFiling(%q) = %v, want %v", test.form, actual, test.insider)
		}
	}
}

func TestInAppNotificationRespectsSourceConfiguration(t *testing.T) {
	db := testDB(t)
	if err := db.AutoMigrate(&model.InAppNotification{}); err != nil {
		t.Fatal(err)
	}
	configs := NewConfigService(db, NewAuditService(db))
	if err := configs.EnsureDefaults(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := configs.UpsertMany(context.Background(), []ConfigInput{{Key: "in_app_notification.major_event_enabled", Value: "false", ValueType: "bool", Category: "in_app_notification"}}, "test"); err != nil {
		t.Fatal(err)
	}
	service := NewInAppNotificationService(db, configs)
	_, inserted, err := service.Create(context.Background(), InAppNotificationInput{EventKey: "major:disabled", Source: "major_event", Title: "不应入站"})
	if err != nil || inserted {
		t.Fatalf("disabled major-event Create = (_, %v, %v), want (_, false, nil)", inserted, err)
	}
	_, inserted, err = service.Create(context.Background(), InAppNotificationInput{EventKey: "insider:enabled", Source: "insider_trading", Title: "应入站"})
	if err != nil || !inserted {
		t.Fatalf("enabled insider Create = (_, %v, %v), want (_, true, nil)", inserted, err)
	}
}
