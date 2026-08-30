package database

import (
	"strings"
	"testing"
	"time"

	"sec_monitor/internal/config"
	"sec_monitor/internal/model"
)

func TestOpenTableDriven(t *testing.T) {
	tests := []struct {
		name    string
		cfg     config.DatabaseConfig
		wantErr bool
	}{
		{name: "opens sqlite memory database", cfg: config.DatabaseConfig{Type: "sqlite", DSN: ":memory:"}},
		{name: "rejects unsupported database", cfg: config.DatabaseConfig{Type: "mysql", DSN: "ignored"}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, err := Open(tt.cfg)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("Open expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			if err := Migrate(db); err != nil {
				t.Fatalf("Migrate: %v", err)
			}
			if !db.Migrator().HasTable(&model.WatchTarget{}) {
				t.Fatalf("watch_targets table was not migrated")
			}
			for _, target := range []any{&model.TaskExecution{}, &model.NotificationBatch{}, &model.NotificationBatchItem{}, &model.OperationalAlertDelivery{}, &model.RecoveryDrill{}, &model.LifecycleCleanupRun{}, &model.IPOCompanyMarketData{}, &model.IPOOfferingEvent{}, &model.IPOCalendarEvent{}, &model.FundFilingIdentity{}, &model.MacroRelease{}, &model.MacroObservation{}, &model.MarketTrendDaily{}} {
				if !db.Migrator().HasTable(target) {
					t.Fatalf("table for %T was not migrated", target)
				}
			}
			for _, column := range []struct {
				model any
				name  string
			}{
				{model: &model.Filing{}, name: "notified_at"},
				{model: &model.SyncRun{}, name: "warning_message"},
				{model: &model.IPOCompanyOverride{}, name: "exchange"},
				{model: &model.IPOCompanyOverride{}, name: "offer_price"},
				{model: &model.IPOCompanyOverride{}, name: "shares_offered"},
				{model: &model.IPOCompanyOverride{}, name: "listing_date"},
				{model: &model.IPOCompanyMarketData{}, name: "gross_proceeds"},
				{model: &model.IPOCompanyMarketData{}, name: "offering_checked_at"},
				{model: &model.IPOCompanyMarketData{}, name: "offering_parser_version"},
				{model: &model.IPOCompanyMarketData{}, name: "listing_date"},
				{model: &model.IPOCompanyMarketData{}, name: "listing_checked_at"},
				{model: &model.IPOCompanyMarketData{}, name: "longbridge_listing_check_count"},
				{model: &model.IPOCompanyMarketData{}, name: "longbridge_listing_last_result"},
				{model: &model.FundFilingIdentity{}, name: "accession_number"},
				{model: &model.FundFilingIdentity{}, name: "parse_status"},
			} {
				if !db.Migrator().HasColumn(column.model, column.name) {
					t.Fatalf("column %s for %T was not migrated", column.name, column.model)
				}
			}
		})
	}
}

func TestBackfillInAppNotificationDecisionContext(t *testing.T) {
	db, err := Open(config.DatabaseConfig{Type: "sqlite", DSN: ":memory:"})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := Migrate(db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	row := model.InAppNotification{EventKey: "legacy:technical:1", Source: "technical_signal", Scope: "watch_target", EntityKind: "trade_setup", Severity: "warning", Priority: "low", Title: "技术信号变化", Body: "收盘跌破均线", ThesisImpact: "none", SuggestedAction: "record_only", OccurredAt: time.Now().UTC()}
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("create legacy row: %v", err)
	}
	if err := backfillInAppNotificationDecisionContext(db); err != nil {
		t.Fatalf("backfill: %v", err)
	}
	if err := db.First(&row, row.ID).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if row.Priority != "high" || row.ThesisImpact != "review" || row.SuggestedAction != "review_today" || row.DedupKey != row.EventKey || row.WhyNow != row.Body {
		t.Fatalf("backfilled row = %+v", row)
	}
}

func TestSQLiteSettingsEnableWALAndBoundedBusyWait(t *testing.T) {
	settings := withSQLiteSettings("file:test.db?mode=rwc")
	for _, want := range []string{"_foreign_keys=on", "_journal_mode=WAL", "_synchronous=NORMAL", "_busy_timeout=5000"} {
		if !strings.Contains(settings, want) {
			t.Fatalf("settings %q missing %q", settings, want)
		}
	}
}
