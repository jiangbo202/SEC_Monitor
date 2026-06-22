package database

import (
	"testing"

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
			for _, target := range []any{&model.NotificationBatch{}, &model.NotificationBatchItem{}, &model.IPOCompanyMarketData{}, &model.IPOOfferingEvent{}} {
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
			} {
				if !db.Migrator().HasColumn(column.model, column.name) {
					t.Fatalf("column %s for %T was not migrated", column.name, column.model)
				}
			}
		})
	}
}
