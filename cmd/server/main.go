package main

import (
	"fmt"
	"log"

	"sec_monitor/internal/api/router"
	"sec_monitor/internal/config"
	"sec_monitor/internal/database"

	"github.com/gin-gonic/gin"
)

func main() {
	if err := run(config.Load(), func(app *gin.Engine, address string) error {
		return app.Run(address)
	}); err != nil {
		log.Fatal(err)
	}
}

func run(cfg config.Config, serve func(app *gin.Engine, address string) error) error {
	db, err := database.Open(cfg.Database)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	if err := database.Migrate(db); err != nil {
		return fmt.Errorf("migrate database: %w", err)
	}

	app := router.New(router.Dependencies{
		Config: cfg,
		DB:     db,
	})

	if err := serve(app, cfg.Server.Address); err != nil {
		return fmt.Errorf("run server: %w", err)
	}
	return nil
}
