package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"

	"sec_monitor/internal/config"
	"sec_monitor/internal/discovery"
	"sec_monitor/internal/service"

	"gorm.io/gorm"
)

type syncService interface {
	Run(context.Context) (service.DiscoverySyncResult, error)
}

type syncDependencies struct {
	openDiscoveryDatabase func(config.DatabaseConfig) (*gorm.DB, error)
	migrateDiscoveryDB    func(*gorm.DB) error
	newSyncService        func(*gorm.DB, config.DiscoveryConfig) syncService
}

func main() {
	if err := run(context.Background(), config.Load(), log.Writer(), syncDependencies{
		openDiscoveryDatabase: discovery.OpenDatabase,
		migrateDiscoveryDB:    discovery.Migrate,
		newSyncService: func(db *gorm.DB, cfg config.DiscoveryConfig) syncService {
			return service.NewDiscoverySyncService(db, cfg)
		},
	}); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context, cfg config.Config, output io.Writer, deps syncDependencies) error {
	db, err := deps.openDiscoveryDatabase(cfg.Discovery.Database)
	if err != nil {
		return fmt.Errorf("open discovery database: %w", err)
	}
	defer closeDatabase(db)
	if err := deps.migrateDiscoveryDB(db); err != nil {
		return fmt.Errorf("migrate discovery database: %w", err)
	}
	result, err := deps.newSyncService(db, cfg.Discovery).Run(ctx)
	encodeErr := json.NewEncoder(output).Encode(result)
	if encodeErr != nil {
		return fmt.Errorf("write discovery sync result: %w", encodeErr)
	}
	if err != nil {
		if errors.Is(err, service.ErrDiscoveryMarketSync) {
			return fmt.Errorf("discovery sync completed security phase but market phase failed: %w", err)
		}
		return err
	}
	return nil
}

func closeDatabase(db *gorm.DB) {
	sqlDB, err := db.DB()
	if err == nil {
		_ = sqlDB.Close()
	}
}
