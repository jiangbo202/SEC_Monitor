package discovery

import (
	"strings"
	"testing"

	"sec_monitor/internal/config"
)

func TestOpenDatabaseRejectsUnsupportedType(t *testing.T) {
	_, err := OpenDatabase(config.DatabaseConfig{Type: "postgres"})
	if err == nil || !strings.Contains(err.Error(), "unsupported discovery database type: postgres") {
		t.Fatalf("OpenDatabase error = %v", err)
	}
}

func TestMigrateContract(t *testing.T) {
	db, err := OpenDatabase(config.DatabaseConfig{Type: "sqlite", DSN: ":memory:"})
	if err != nil {
		t.Fatalf("OpenDatabase: %v", err)
	}
	if err := Migrate(db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
}
