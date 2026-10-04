// storage-maintenance inspects or maintains this deployment's SQLite databases.
// Inspection is read-only; mutation actions require a stopped application.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"sec_monitor/internal/config"
	"sec_monitor/internal/database"
	"sec_monitor/internal/discovery"
	"sec_monitor/internal/model"
	"sec_monitor/internal/service"
)

func main() {
	action := flag.String("action", "", "inspect (read-only, server may run); prune-backups, backup, normalize-risks, compact require stopped server")
	flag.Parse()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := run(ctx, *action); err != nil {
		log.Fatal(service.SanitizeSensitiveError(err.Error()))
	}
}

func run(ctx context.Context, action string) error {
	switch action {
	case "inspect", "prune-backups", "backup", "normalize-risks", "compact":
	default:
		return fmt.Errorf("unknown maintenance action %q", action)
	}
	cfg := config.Load()
	if action == "inspect" {
		if cfg.Discovery.Database.Type != "sqlite" {
			return fmt.Errorf("storage inspection requires SQLite")
		}
		filePath, err := filepath.Abs(cfg.Discovery.Database.DSN)
		if err != nil {
			return err
		}
		uri := url.URL{Scheme: "file", Path: filePath, RawQuery: "mode=ro&_query_only=1&_busy_timeout=3000"}
		db, err := gorm.Open(sqlite.Open(uri.String()), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
		if err != nil {
			return err
		}
		defer closeDB(db)
		result, err := service.InspectFinancialStorage(ctx, db)
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(result)
	}
	mainDB, err := database.Open(cfg.Database)
	if err != nil {
		return err
	}
	defer closeDB(mainDB)
	researchDB, err := discovery.OpenDatabase(cfg.Discovery.Database)
	if err != nil {
		return err
	}
	defer closeDB(researchDB)
	// Do not run research schema migration before the original-schema backup.
	if err := mainDB.AutoMigrate(&model.SQLiteCompactionRun{}); err != nil {
		return err
	}
	configs := service.NewConfigService(mainDB, service.NewAuditService(mainDB), cfg.System)
	backups := service.NewSQLiteBackupService(mainDB, researchDB, cfg.Database.DSN, cfg.Discovery.Database.DSN, configs)
	var result any
	switch action {
	case "prune-backups":
		if err := configs.UpsertMany(ctx, []service.ConfigInput{{Key: "system.backup_keep_pairs", Value: "3", ValueType: "int", Category: "system"}}, "storage-maintenance"); err != nil {
			return err
		}
		result, err = backups.PruneExisting(ctx)
	case "backup":
		result, err = backups.Backup(ctx)
	case "normalize-risks":
		lastReported := int64(-1)
		result, err = discovery.ConsolidateCapitalRiskStorage(ctx, researchDB, func(done, total int64) {
			if done/100000 != lastReported || done == total {
				log.Printf("capital risks: %d / %d", done, total)
				lastReported = done / 100000
			}
		})
		if err == nil {
			err = researchDB.WithContext(ctx).Exec("ANALYZE").Error
		}
	case "compact":
		result, err = backups.Compact(ctx)
	}
	if encodeErr := json.NewEncoder(os.Stdout).Encode(result); encodeErr != nil {
		return encodeErr
	}
	return err
}

func closeDB(db *gorm.DB) {
	if connection, err := db.DB(); err == nil {
		_ = connection.Close()
	}
}
