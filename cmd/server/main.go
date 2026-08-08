package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

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
	newRouter             func(router.Dependencies) (*gin.Engine, error)
}

func main() {
	if err := run(config.Load(), serveHTTPGracefully); err != nil {
		log.Fatal(err)
	}
}

// serveHTTPGracefully lets Docker stop a container without cutting off active
// API responses. Background jobs use their own configured deadlines; this
// only controls the HTTP listener and prevents new requests during shutdown.
func serveHTTPGracefully(app *gin.Engine, address string) error {
	server := &http.Server{Addr: address, Handler: app, ReadHeaderTimeout: 10 * time.Second}
	serverErr := make(chan error, 1)
	go func() { serverErr <- server.ListenAndServe() }()

	stopSignals := make(chan os.Signal, 1)
	signal.Notify(stopSignals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(stopSignals)

	select {
	case err := <-serverErr:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case received := <-stopSignals:
		log.Printf("received %s, shutting down HTTP server", received)
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			return fmt.Errorf("graceful HTTP shutdown: %w", err)
		}
		if err := <-serverErr; err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
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

	app, err := deps.newRouter(router.Dependencies{
		Config:      cfg,
		DB:          db,
		DiscoveryDB: discoveryDB,
	})
	if err != nil {
		return fmt.Errorf("initialize router: %w", err)
	}

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
