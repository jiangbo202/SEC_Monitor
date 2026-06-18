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
		})
	}
}
