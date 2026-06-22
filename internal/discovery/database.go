package discovery

import (
	"fmt"
	"strings"

	"sec_monitor/internal/config"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func OpenDatabase(cfg config.DatabaseConfig) (*gorm.DB, error) {
	if cfg.Type != "sqlite" {
		return nil, fmt.Errorf("unsupported discovery database type: %s", cfg.Type)
	}
	return gorm.Open(sqlite.Open(withSQLiteForeignKeys(cfg.DSN)), &gorm.Config{})
}

func withSQLiteForeignKeys(dsn string) string {
	separator := "?"
	if strings.Contains(dsn, "?") {
		separator = "&"
	}
	return dsn + separator + "_foreign_keys=on"
}

func Migrate(db *gorm.DB) error {
	if err := db.AutoMigrate(
		&Security{},
		&UniverseBatch{},
		&Listing{},
		&ClassificationSnapshot{},
		&ProviderRun{},
		&MarketHoliday{},
		&MarketCalendarYear{},
		&PriceSnapshot{},
		&ShareSnapshot{},
		&UniverseSnapshot{},
		&ManualSecurityOverride{},
	); err != nil {
		return err
	}
	return SeedDefaultNYSEMarketCalendar(db.Statement.Context, db)
}
