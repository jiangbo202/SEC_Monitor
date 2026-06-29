package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"sec_monitor/internal/config"
	"sec_monitor/internal/discovery"
	"sec_monitor/internal/service"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type fakeSyncService struct {
	result service.DiscoverySyncResult
	err    error
}

func (s fakeSyncService) Run(context.Context) (service.DiscoverySyncResult, error) {
	return s.result, s.err
}

func TestRunPrintsDiscoverySyncResult(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	err = run(context.Background(), config.Config{Discovery: config.DiscoveryConfig{Database: config.DatabaseConfig{Type: "sqlite", DSN: ":memory:"}}}, &out, syncDependencies{
		openDiscoveryDatabase: func(config.DatabaseConfig) (*gorm.DB, error) { return db, nil },
		migrateDiscoveryDB:    discovery.Migrate,
		newSyncService: func(*gorm.DB, config.DiscoveryConfig) syncService {
			return fakeSyncService{result: service.DiscoverySyncResult{Status: service.DiscoverySyncStatusPublished, BatchID: "market"}}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"status":"published"`) || !strings.Contains(out.String(), `"batch_id":"market"`) {
		t.Fatalf("output = %s", out.String())
	}
}

func TestRunReturnsMarketFailureAfterPrintingResult(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	err = run(context.Background(), config.Config{Discovery: config.DiscoveryConfig{Database: config.DatabaseConfig{Type: "sqlite", DSN: ":memory:"}}}, &out, syncDependencies{
		openDiscoveryDatabase: func(config.DatabaseConfig) (*gorm.DB, error) { return db, nil },
		migrateDiscoveryDB:    discovery.Migrate,
		newSyncService: func(*gorm.DB, config.DiscoveryConfig) syncService {
			return fakeSyncService{
				result: service.DiscoverySyncResult{Status: service.DiscoverySyncStatusMarketFailed, SecurityBatchID: "security"},
				err:    service.ErrDiscoveryMarketSync,
			}
		},
	})
	if err == nil || !errors.Is(err, service.ErrDiscoveryMarketSync) {
		t.Fatalf("err = %v, want market failure", err)
	}
	if !strings.Contains(out.String(), `"status":"market_failed"`) || !strings.Contains(out.String(), `"security_batch_id":"security"`) {
		t.Fatalf("output = %s", out.String())
	}
}
