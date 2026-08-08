package database

import (
	"fmt"
	"log"
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
		return gorm.Open(sqlite.Open(cfg.DSN), &gorm.Config{Logger: gormLogger()})
	default:
		return nil, fmt.Errorf("unsupported database type: %s", cfg.Type)
	}
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
		&model.IPOCompanyOverride{},
		&model.IPOCompanyMarketData{},
		&model.IPOOfferingEvent{},
		&model.SyncRun{},
		&model.SyncRunDetail{},
		&model.TaskConfig{},
		&model.OperationalAlertDelivery{},
		&model.RecoveryDrill{},
		&model.LifecycleCleanupRun{},
		&model.SQLiteCompactionRun{},
		&model.SystemConfig{},
		&model.OperationLog{},
		&model.NotificationLog{},
		&model.NotificationBatch{},
		&model.NotificationBatchItem{},
		&model.TradeSetupNotificationState{},
		&model.MacroRelease{},
		&model.MacroObservation{},
		&model.EarningsPreview{},
		&model.EarningsPreviewNotice{},
		&model.CandidateEarningsPreview{},
		&model.FundFilingIdentity{},
	)
}
