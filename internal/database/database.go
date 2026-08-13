package database

import (
	"database/sql"
	"fmt"
	"log"
	"strings"
	"time"

	"sec_monitor/internal/config"
	"sec_monitor/internal/model"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func Open(cfg config.DatabaseConfig) (*gorm.DB, error) {
	switch cfg.Type {
	case "sqlite":
		db, err := gorm.Open(sqlite.Open(withSQLiteSettings(cfg.DSN)), &gorm.Config{Logger: gormLogger()})
		if err != nil {
			return nil, err
		}
		if sqlDB, err := db.DB(); err == nil {
			configureSQLitePool(sqlDB)
		}
		return db, nil
	default:
		return nil, fmt.Errorf("unsupported database type: %s", cfg.Type)
	}
}

// The primary operational database serves both scheduled writers and the
// interactive UI. Use the same conservative SQLite policy as the discovery
// database so normal read/write overlap waits briefly instead of surfacing
// avoidable "database is locked" errors.
func withSQLiteSettings(dsn string) string {
	separator := "?"
	if strings.Contains(dsn, "?") {
		separator = "&"
	}
	return dsn + separator + "_foreign_keys=on&_journal_mode=WAL&_synchronous=NORMAL&_busy_timeout=5000"
}

func configureSQLitePool(db *sql.DB) {
	// SQLite still has a single writer; a small pool prevents a burst of
	// concurrent handlers from competing with background synchronization.
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(4)
}

func gormLogger() logger.Interface {
	return logger.New(log.Default(), logger.Config{
		SlowThreshold:             500 * time.Millisecond,
		LogLevel:                  logger.Warn,
		IgnoreRecordNotFoundError: true,
		Colorful:                  false,
	})
}

func Migrate(db *gorm.DB) error {
	return db.AutoMigrate(
		&model.WatchTarget{},
		&model.Filing{},
		&model.WatchTargetFiling{},
		&model.IPOFiling{},
		&model.IPOCompanyFollow{},
		&model.IPOCompanyOverride{},
		&model.IPOCompanyMarketData{},
		&model.IPOOfferingEvent{},
		&model.IPOCalendarEvent{},
		&model.SyncRun{},
		&model.SyncRunDetail{},
		&model.TaskConfig{},
		&model.TaskExecution{},
		&model.OperationalAlertDelivery{},
		&model.RecoveryDrill{},
		&model.LifecycleCleanupRun{},
		&model.SQLiteCompactionRun{},
		&model.SystemConfig{},
		&model.OperationLog{},
		&model.NotificationLog{},
		&model.NotificationBatch{},
		&model.NotificationBatchItem{},
		&model.InAppNotification{},
		&model.TradeSetupNotificationState{},
		&model.MacroRelease{},
		&model.MacroObservation{},
		&model.MarketTrendDaily{},
		&model.MarketTemperatureDaily{},
		&model.EarningsPreview{},
		&model.EarningsPreviewNotice{},
		&model.CandidateEarningsPreview{},
		&model.FundFilingIdentity{},
		&model.InstitutionalFiling{},
		&model.InstitutionalPortfolioHolding{},
	)
}
