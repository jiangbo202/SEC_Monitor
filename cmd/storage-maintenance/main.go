// storage-maintenance performs offline maintenance of this deployment's two
// SQLite databases. Stop the application before invoking this command.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"gorm.io/gorm"
	"sec_monitor/internal/config"
	"sec_monitor/internal/database"
	"sec_monitor/internal/discovery"
	"sec_monitor/internal/model"
	"sec_monitor/internal/service"
)

func main() {
	action := flag.String("action", "", "prune-backups, backup, normalize-risks, or compact (application must be stopped)")
	flag.Parse()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := run(ctx, *action); err != nil {
		log.Fatal(service.SanitizeSensitiveError(err.Error()))
	}
}

func run(ctx context.Context, action string) error {
	switch action {
	case "prune-backups", "backup", "normalize-risks", "compact":
	default:
		return fmt.Errorf("unknown maintenance action %q", action)
	}
	cfg := config.Load()
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
