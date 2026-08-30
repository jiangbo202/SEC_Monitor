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
	if err := db.AutoMigrate(
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
		&model.AIAnalysis{},
		&model.ResearchThesis{},
		&model.ResearchThesisRevision{},
		&model.TradeSetupNotificationState{},
		&model.MacroRelease{},
		&model.MacroObservation{},
		&model.MarketTrendDaily{},
		&model.MarketTemperatureDaily{},
		&model.EarningsPreview{},
		&model.EarningsCalendarCheckpoint{},
		&model.EarningsPreviewNotice{},
		&model.CandidateEarningsPreview{},
		&model.EarningsExpectationSnapshot{},
		&model.FundFilingIdentity{},
		&model.InstitutionalFiling{},
		&model.InstitutionalPortfolioHolding{},
	); err != nil {
		return err
	}
	return backfillInAppNotificationDecisionContext(db)
}

// backfillInAppNotificationDecisionContext upgrades pre-P1 inbox rows once.
// New rows always carry DedupKey, so this deliberately leaves producer-owned
// decisions untouched on later starts.
func backfillInAppNotificationDecisionContext(db *gorm.DB) error {
	legacy := func() *gorm.DB {
		return db.Model(&model.InAppNotification{}).Where("dedup_key = '' OR dedup_key IS NULL")
	}
	if err := legacy().Update("priority", gorm.Expr("CASE severity WHEN 'danger' THEN 'urgent' WHEN 'warning' THEN 'high' WHEN 'success' THEN 'normal' ELSE 'low' END")).Error; err != nil {
		return err
	}
	if err := legacy().Update("thesis_impact", gorm.Expr(`CASE
		WHEN source IN ('technical_signal', 'technical_signal_watch_target', 'technical_signal_candidate', 'major_event', 'major_event_watch_target', 'earnings_release', 'earnings_release_watch_target', 'earnings_release_candidate') THEN 'review'
		WHEN source IN ('insider_trading', 'insider_trading_watch_target', 'ten_b5_one_plan_discovered', 'earnings_preview', 'earnings_preview_watch_target', 'earnings_preview_candidate') THEN 'context'
		ELSE 'none' END`)).Error; err != nil {
		return err
	}
	if err := legacy().Update("suggested_action", gorm.Expr("CASE severity WHEN 'danger' THEN 'review_now' WHEN 'warning' THEN 'review_today' ELSE 'record_only' END")).Error; err != nil {
		return err
	}
	if err := legacy().Update("why_now", gorm.Expr("CASE WHEN TRIM(COALESCE(body, '')) <> '' THEN body ELSE '历史事件已按统一决策语义补齐。' END")).Error; err != nil {
		return err
	}
	return legacy().Update("dedup_key", gorm.Expr("event_key")).Error
}
