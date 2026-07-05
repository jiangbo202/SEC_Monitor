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
		hadSecurityTable := tx.Migrator().HasTable(&Security{})
		hadSecurityCatalogStatus := tx.Migrator().HasColumn(&Security{}, "CatalogStatus")
		hadCalendarYearTable := tx.Migrator().HasTable(&MarketCalendarYear{})
		hadHolidayCount := tx.Migrator().HasColumn(&MarketCalendarYear{}, "ExpectedHolidayCount")
		hadHolidayHash := tx.Migrator().HasColumn(&MarketCalendarYear{}, "HolidayDatesSHA256")
		legacyCalendarManifest := hadCalendarYearTable && !hadHolidayCount && !hadHolidayHash
		hadProviderHealthTable := tx.Migrator().HasTable(&ProviderHealth{})
		hadProviderWindow := tx.Migrator().HasColumn(&ProviderHealth{}, "WindowJSON")
		hadProviderGoldReady := tx.Migrator().HasColumn(&ProviderHealth{}, "GoldEvidenceReady")
		hadProviderGoldSHA := tx.Migrator().HasColumn(&ProviderHealth{}, "GoldSHA256")
		legacyProviderHealth := hadProviderHealthTable && (!hadProviderWindow || !hadProviderGoldReady || !hadProviderGoldSHA)

		if err := tx.AutoMigrate(
			&Security{},
			&UniverseBatch{},
			&CurrentBatchPointer{},
			&Listing{},
			&SecurityBatchIdentity{},
			&ListingIdentitySnapshot{},
			&ClassificationSnapshot{},
			&ProviderRun{},
			&ProviderHealth{},
			&MarketHoliday{},
			&MarketCalendarYear{},
			&PriceSnapshot{},
			&ShareSnapshot{},
			&FinancialFactSnapshot{},
			&FinancialMetricSnapshot{},
			&InsiderTransactionSnapshot{},
			&CapitalRiskSnapshot{},
			&SocialHeatSnapshot{},
			&CandidateScoreSnapshot{},
			&CandidateRecalcEvent{},
			&BatchShareSelection{},
			&UniverseSnapshot{},
			&ManualSecurityOverride{},
			&IdentityVerificationOverride{},
		); err != nil {
			return err
		}
		if legacyCalendarManifest {
			if err := backfillLegacyNYSECalendarManifest(tx); err != nil {
				return err
			}
		}
		if legacyProviderHealth {
			if err := tx.Model(&ProviderHealth{}).Where("1 = 1").Updates(map[string]any{
				"status": ProviderStatusValidation, "qualified_trading_days": 0, "failure_streak": 0,
				"last_trade_date": "", "window_json": "", "gold_evidence_ready": false, "gold_sha256": "",
			}).Error; err != nil {
				return fmt.Errorf("invalidate legacy provider health: %w", err)
			}
		}
		if hadSecurityTable && !hadSecurityCatalogStatus {
			if err := tx.Model(&Security{}).Where("1 = 1").Updates(map[string]any{"catalog_status": SecurityCatalogPublished, "published_at": gorm.Expr("COALESCE(updated_at, created_at, CURRENT_TIMESTAMP)")}).Error; err != nil {
				return fmt.Errorf("publish legacy security catalog: %w", err)
			}
		}
		return SeedDefaultNYSEMarketCalendar(tx.Statement.Context, tx)
	})
}
