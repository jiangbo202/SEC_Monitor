package database

import (
	"fmt"

	"sec_monitor/internal/config"
	"sec_monitor/internal/model"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func Open(cfg config.DatabaseConfig) (*gorm.DB, error) {
	switch cfg.Type {
	case "sqlite":
		return gorm.Open(sqlite.Open(cfg.DSN), &gorm.Config{})
	default:
		return nil, fmt.Errorf("unsupported database type: %s", cfg.Type)
	}
}

func Migrate(db *gorm.DB) error {
	return db.AutoMigrate(
		&model.WatchTarget{},
		&model.Filing{},
		&model.IPOFiling{},
		&model.SyncRun{},
		&model.SyncRunDetail{},
		&model.TaskConfig{},
		&model.SystemConfig{},
		&model.OperationLog{},
		&model.NotificationLog{},
	)
}
