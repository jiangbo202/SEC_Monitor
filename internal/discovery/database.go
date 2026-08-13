package discovery

import (
	"database/sql"
	"fmt"
	"log"
	"strings"
	"time"

	"sec_monitor/internal/config"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func OpenDatabase(cfg config.DatabaseConfig) (*gorm.DB, error) {
	if cfg.Type != "sqlite" {
		return nil, fmt.Errorf("unsupported discovery database type: %s", cfg.Type)
	}
	db, err := gorm.Open(sqlite.Open(withSQLiteSettings(cfg.DSN)), &gorm.Config{Logger: gormLogger()})
	if err != nil {
		return nil, err
	}
	if sqlDB, err := db.DB(); err == nil {
		configureSQLitePool(sqlDB)
	}
	return db, nil
}

func gormLogger() logger.Interface {
	return logger.New(log.Default(), logger.Config{
		SlowThreshold:             500 * time.Millisecond,
		LogLevel:                  logger.Warn,
		IgnoreRecordNotFoundError: true,
		Colorful:                  false,
	})
}

// withSQLiteSettings makes the local research database safe for the expected
// shape of this application: long background ingestion alongside interactive
// reads. WAL allows readers to proceed while a writer is active; a bounded
// busy timeout avoids surfacing short write-contention windows as failures.
func withSQLiteSettings(dsn string) string {
	separator := "?"
	if strings.Contains(dsn, "?") {
		separator = "&"
	}
	return dsn + separator + "_foreign_keys=on&_journal_mode=WAL&_synchronous=NORMAL&_busy_timeout=5000"
}

func configureSQLitePool(db *sql.DB) {
	// SQLite still has one writer. Keeping this deliberately small avoids a
	// local API process opening a large set of competing write connections.
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(4)
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
			&DiscoverySyncRun{},
			&DiscoverySyncStep{},
			&CurrentBatchPointer{},
			&Listing{},
			&CompanyProfileSnapshot{},
			&AnalystRatingSnapshot{},
			&EPSForecastSnapshot{},
			&MarketAnomalySnapshot{},
			&InstitutionalHolderSnapshot{},
			&FundHolderSnapshot{},
			&LongbridgeValuationSnapshot{},
			&OptionResearchSnapshot{},
			&LongbridgeResearchRefreshState{},
			&SecurityBatchIdentity{},
			&ListingIdentitySnapshot{},
			&ClassificationSnapshot{},
			&ProviderRun{},
			&ProviderHealth{},
			&MarketHoliday{},
			&MarketCalendarYear{},
			&PriceSnapshot{},
			&TickerEvaluationSnapshot{},
			&ShareSnapshot{},
			&FinancialFactSnapshot{},
			&FinancialMetricSnapshot{},
			&InsiderTransactionSnapshot{},
			&InsiderCoverageSnapshot{},
			&SECFilingSnapshot{},
			&CapitalRiskSnapshot{},
			&SocialHeatSnapshot{},
			&CandidateScoreSnapshot{},
			&SmallCapEligibilityCheckHistory{},
			&CandidateBusinessModelOverride{},
			&CandidateSignalEvent{},
			&CandidateRecalcEvent{},
			&CandidateWatch{},
			&CandidateResearchMemoVersion{},
			&CandidateResearchPosition{},
			&TradeSetupStatusEvent{},
			&TradePlanSimulation{},
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
