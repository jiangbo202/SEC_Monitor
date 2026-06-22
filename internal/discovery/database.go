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
	return db.Transaction(func(tx *gorm.DB) error {
		hadCalendarYearTable := tx.Migrator().HasTable(&MarketCalendarYear{})
		hadHolidayCount := tx.Migrator().HasColumn(&MarketCalendarYear{}, "ExpectedHolidayCount")
		hadHolidayHash := tx.Migrator().HasColumn(&MarketCalendarYear{}, "HolidayDatesSHA256")
		legacyCalendarManifest := hadCalendarYearTable && !hadHolidayCount && !hadHolidayHash

		if err := tx.AutoMigrate(
			&Security{},
			&UniverseBatch{},
			&Listing{},
			&ClassificationSnapshot{},
			&ProviderRun{},
			&ProviderHealth{},
			&MarketHoliday{},
			&MarketCalendarYear{},
			&PriceSnapshot{},
			&ShareSnapshot{},
			&UniverseSnapshot{},
			&ManualSecurityOverride{},
		); err != nil {
			return err
		}
		if legacyCalendarManifest {
			if err := backfillLegacyNYSECalendarManifest(tx); err != nil {
				return err
			}
		}
		return SeedDefaultNYSEMarketCalendar(tx.Statement.Context, tx)
	})
}
