package discovery

import (
	"fmt"

	"sec_monitor/internal/config"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func OpenDatabase(cfg config.DatabaseConfig) (*gorm.DB, error) {
	if cfg.Type != "sqlite" {
		return nil, fmt.Errorf("unsupported discovery database type: %s", cfg.Type)
	}
	return gorm.Open(sqlite.Open(cfg.DSN), &gorm.Config{})
}

func Migrate(_ *gorm.DB) error {
	return nil
}
