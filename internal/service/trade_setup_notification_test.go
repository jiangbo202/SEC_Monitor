package service

import (
	"context"
	"testing"
	"time"

	"sec_monitor/internal/discovery"
	"sec_monitor/internal/model"

	"gorm.io/gorm"
)

func TestTradeSetupNotificationSendsInitialEntryOnlyOnce(t *testing.T) {
	db := testDB(t)
	discoveryDB := testDiscoveryDB(t)
	configs := NewConfigService(db, NewAuditService(db))
	if err := configs.EnsureDefaults(context.Background()); err != nil {
		t.Fatal(err)
	}
	configureTradeSetupNotification(t, configs, false)
	seedTradeSetupNotificationTarget(t, db, discoveryDB, "PLAN")
	notifier := &fakeNotifier{}
	service := NewTradeSetupNotificationService(db, discoveryDB, notifier, configs)

	preview, _, err := service.Preview(context.Background())
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	if !preview.Enabled || preview.ObservedCount != 1 || preview.EligibleCount != 1 || len(preview.Events) != 1 {
		t.Fatalf("preview = %+v", preview)
	}
	if preview.Events[0].Status != discovery.TradeSetupEntryCandidate || preview.Events[0].Reason != "eligible" {
		t.Fatalf("event = %+v", preview.Events[0])
	}

	result, err := service.Send(context.Background(), TradeSetupNotificationSendInput{Confirm: true})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if result.Batch.Status != "sent" || result.Batch.Source != tradeSetupNotificationSource || notifier.calls != 1 {
		t.Fatalf("send result=%+v calls=%d", result, notifier.calls)
	}
	var state model.TradeSetupNotificationState
	if err := db.Where("ticker = ?", "PLAN").First(&state).Error; err != nil {
		t.Fatalf("load state: %v", err)
	}
	if state.Status != discovery.TradeSetupEntryCandidate {
		t.Fatalf("state = %+v", state)
	}

	second, _, err := service.Preview(context.Background())
	if err != nil {
		t.Fatalf("second Preview: %v", err)
	}
	if second.EligibleCount != 0 || len(second.Events) != 1 || second.Events[0].Reason != "unchanged" {
		t.Fatalf("second preview = %+v", second)
	}
}

func TestTradeSetupNotificationShadowModeDoesNotSendOrPersist(t *testing.T) {
	db := testDB(t)
	discoveryDB := testDiscoveryDB(t)
	configs := NewConfigService(db, NewAuditService(db))
	if err := configs.EnsureDefaults(context.Background()); err != nil {
		t.Fatal(err)
	}
	configureTradeSetupNotification(t, configs, true)
	seedTradeSetupNotificationTarget(t, db, discoveryDB, "SHDW")
	notifier := &fakeNotifier{}

	result, err := NewTradeSetupNotificationService(db, discoveryDB, notifier, configs).Send(context.Background(), TradeSetupNotificationSendInput{Confirm: true})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if result.Preview.SuppressedReason != "trade_setup_notification_shadow_mode" || result.Preview.EligibleCount != 1 || notifier.calls != 0 || result.Batch.ID != 0 {
		t.Fatalf("result=%+v calls=%d", result, notifier.calls)
	}
	var count int64
	if err := db.Model(&model.TradeSetupNotificationState{}).Count(&count).Error; err != nil {
		t.Fatalf("count state: %v", err)
	}
	if count != 0 {
		t.Fatalf("state count = %d, want 0", count)
	}
}

func configureTradeSetupNotification(t *testing.T, configs *ConfigService, shadowMode bool) {
	t.Helper()
	if err := configs.UpsertMany(context.Background(), []ConfigInput{
		{Key: "trade_setup_notification.enabled", Value: "true", ValueType: "bool", Category: "trade_setup_notification"},
		{Key: "trade_setup_notification.shadow_mode", Value: map[bool]string{true: "true", false: "false"}[shadowMode], ValueType: "bool", Category: "trade_setup_notification"},
		{Key: "trade_setup_notification.notify_entry", Value: "true", ValueType: "bool", Category: "trade_setup_notification"},
		{Key: "telegram.enabled", Value: "true", ValueType: "bool", Category: "telegram"},
		{Key: "telegram.bot_token", Value: "token", ValueType: "string", Category: "telegram"},
		{Key: "telegram.chat_id", Value: "chat", ValueType: "string", Category: "telegram"},
	}, "test"); err != nil {
		t.Fatalf("configure trade setup notification: %v", err)
	}
}

func seedTradeSetupNotificationTarget(t *testing.T, db, discoveryDB *gorm.DB, ticker string) {
	t.Helper()
	target := model.WatchTarget{Ticker: ticker, CompanyName: ticker + " Co", TargetType: "stock", Status: "enabled"}
	if err := db.Create(&target).Error; err != nil {
		t.Fatalf("create watch target: %v", err)
	}
	base := time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC)
	prices := make([]discovery.PriceSnapshot, 0, 200)
	for day := 0; day < 200; day++ {
		closeMicros := int64(10_000_000 + day*450_000)
		volume := int64(100_000)
		if day == 199 {
			volume = 200_000
		}
		prices = append(prices, discovery.PriceSnapshot{
			Source: "test", Symbol: ticker, TradeDate: base.AddDate(0, 0, day), CloseMicros: closeMicros,
			Volume: volume, Currency: "USD", QualityStatus: discovery.QualityStatusValid,
		})
	}
	if err := discoveryDB.Create(&prices).Error; err != nil {
		t.Fatalf("create price history: %v", err)
	}
}
