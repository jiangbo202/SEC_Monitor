package main

import (
	"fmt"
	"log"

	"sec_monitor/internal/api/router"
	"sec_monitor/internal/config"
	"sec_monitor/internal/database"
	"sec_monitor/internal/discovery"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type startupDependencies struct {
	openMainDatabase      func(config.DatabaseConfig) (*gorm.DB, error)
	migrateMainDatabase   func(*gorm.DB) error
	openDiscoveryDatabase func(config.DatabaseConfig) (*gorm.DB, error)
	migrateDiscoveryDB    func(*gorm.DB) error
	newRouter             func(router.Dependencies) *gin.Engine
}

func main() {
	if err := run(config.Load(), func(app *gin.Engine, address string) error {
		return app.Run(address)
	}); err != nil {
		log.Fatal(err)
	}
}

func run(cfg config.Config, serve func(app *gin.Engine, address string) error) error {
	return runWithDependencies(cfg, serve, startupDependencies{
		openMainDatabase:      database.Open,
		migrateMainDatabase:   database.Migrate,
		openDiscoveryDatabase: discovery.OpenDatabase,
		migrateDiscoveryDB:    discovery.Migrate,
		newRouter:             router.New,
	})
}

func runWithDependencies(cfg config.Config, serve func(app *gin.Engine, address string) error, deps startupDependencies) error {
	db, err := deps.openMainDatabase(cfg.Database)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer closeDatabase(db)
	if err := deps.migrateMainDatabase(db); err != nil {
		return fmt.Errorf("migrate database: %w", err)
	}
	discoveryDB, err := deps.openDiscoveryDatabase(cfg.Discovery.Database)
	if err != nil {
		return fmt.Errorf("open discovery database: %w", err)
	}
	defer closeDatabase(discoveryDB)
	if err := deps.migrateDiscoveryDB(discoveryDB); err != nil {
		return fmt.Errorf("migrate discovery database: %w", err)
	}

	app := deps.newRouter(router.Dependencies{
		Config:      cfg,
		DB:          db,
		DiscoveryDB: discoveryDB,
	})

	if err := serve(app, cfg.Server.Address); err != nil {
		return fmt.Errorf("run server: %w", err)
	}
	return nil
}

func closeDatabase(db *gorm.DB) {
	sqlDB, err := db.DB()
	if err == nil {
		_ = sqlDB.Close()
	}
}
