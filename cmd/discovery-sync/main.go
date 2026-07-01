package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"strings"

	"sec_monitor/internal/config"
	"sec_monitor/internal/database"
	"sec_monitor/internal/discovery"
	"sec_monitor/internal/service"

	"gorm.io/gorm"
)

type syncService interface {
	Run(context.Context) (service.DiscoverySyncResult, error)
	RunMarketOnly(context.Context) (service.DiscoverySyncResult, error)
}

type syncDependencies struct {
	openMainDatabase      func(config.DatabaseConfig) (*gorm.DB, error)
	migrateMainDB         func(*gorm.DB) error
	openDiscoveryDatabase func(config.DatabaseConfig) (*gorm.DB, error)
	migrateDiscoveryDB    func(*gorm.DB) error
	newSyncService        func(*gorm.DB, config.DiscoveryConfig, *service.ConfigService) syncService
}

func main() {
	if err := run(context.Background(), config.Load(), log.Writer(), syncDependencies{
		openMainDatabase:      database.Open,
		migrateMainDB:         database.Migrate,
		openDiscoveryDatabase: discovery.OpenDatabase,
		migrateDiscoveryDB:    discovery.Migrate,
		newSyncService: func(db *gorm.DB, cfg config.DiscoveryConfig, configs *service.ConfigService) syncService {
			return service.NewDiscoverySyncService(db, cfg).WithConfigService(configs)
		},
	}); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context, cfg config.Config, output io.Writer, deps syncDependencies) error {
	mainDB, err := deps.openMainDatabase(cfg.Database)
	if err != nil {
		return fmt.Errorf("open main database: %w", err)
	}
	defer closeDatabase(mainDB)
	if err := deps.migrateMainDB(mainDB); err != nil {
		return fmt.Errorf("migrate main database: %w", err)
	}
	configs := service.NewConfigService(mainDB, service.NewAuditService(mainDB))
	if err := configs.EnsureDefaults(ctx); err != nil {
		return fmt.Errorf("ensure system config defaults: %w", err)
	}
	db, err := deps.openDiscoveryDatabase(cfg.Discovery.Database)
	if err != nil {
		return fmt.Errorf("open discovery database: %w", err)
	}
	defer closeDatabase(db)
	if err := deps.migrateDiscoveryDB(db); err != nil {
		return fmt.Errorf("migrate discovery database: %w", err)
	}
	syncer := deps.newSyncService(db, cfg.Discovery, configs)
	var result service.DiscoverySyncResult
	phase := strings.ToLower(strings.TrimSpace(os.Getenv("DISCOVERY_SYNC_PHASE")))
	if phase == "market" || phase == "market-only" {
		result, err = syncer.RunMarketOnly(ctx)
	} else {
		result, err = syncer.Run(ctx)
	}
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
