package service

import (
	"context"
	"testing"
	"time"

	"sec_monitor/internal/discovery"
	"sec_monitor/internal/model"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestCreateTenB5OnePlanDiscoveryNotificationsCreatesOneCurrentScopeEvent(t *testing.T) {
	ctx := context.Background()
	mainDB := testDB(t)
	if err := mainDB.AutoMigrate(&model.InAppNotification{}); err != nil {
		t.Fatal(err)
	}
	discoveryDB, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	if err := discovery.Migrate(discoveryDB); err != nil {
		t.Fatal(err)
	}
	if err := mainDB.Create(&model.WatchTarget{Ticker: "ACME", CompanyName: "Acme", TargetType: "stock", Status: "enabled"}).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 30, 4, 0, 0, 0, time.UTC)
	security := discovery.Security{CIK: "0000000001", CompanyName: "Acme", CatalogStatus: discovery.SecurityCatalogPublished}
	if err := discoveryDB.Create(&security).Error; err != nil {
		t.Fatal(err)
	}
	if err := discoveryDB.Create(&discovery.Listing{SecurityID: security.ID, Ticker: "ACME", ValidFrom: now.AddDate(-1, 0, 0)}).Error; err != nil {
		t.Fatal(err)
	}
	plan := discovery.InsiderTradingPlan{SecurityID: security.ID, IdentitySHA256: "plan-acme", OwnerKey: "owner", OwnerName: "Owner", AdoptionDate: now.AddDate(0, -2, 0), Status: discovery.InsiderPlanStatusActive, CreatedAt: now, UpdatedAt: now}
	if err := discoveryDB.Create(&plan).Error; err != nil {
		t.Fatal(err)
	}
	inApp := NewInAppNotificationService(mainDB)
	created, err := CreateTenB5OnePlanDiscoveryNotifications(ctx, mainDB, discoveryDB, inApp, now.Add(-time.Second))
	if err != nil || created != 1 {
		t.Fatalf("create notifications = (%d, %v), want (1, nil)", created, err)
	}
	created, err = CreateTenB5OnePlanDiscoveryNotifications(ctx, mainDB, discoveryDB, inApp, now.Add(-time.Second))
	if err != nil || created != 0 {
		t.Fatalf("repeat notifications = (%d, %v), want (0, nil)", created, err)
	}
	var item model.InAppNotification
	if err := mainDB.Where("event_key = ?", "ten_b5_one:first:ACME").First(&item).Error; err != nil {
		t.Fatal(err)
	}
	if item.Scope != "watch_target" || item.Ticker != "ACME" || item.Link != "/insider-trading?tab=plans&ticker=ACME" {
		t.Fatalf("notification = %+v", item)
	}
}
