package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"sec_monitor/internal/config"
	"sec_monitor/internal/database"
	"sec_monitor/internal/discovery"
	"sec_monitor/internal/service"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type fakeSyncService struct {
	result       service.DiscoverySyncResult
	marketResult service.DiscoverySyncResult
	err          error
	marketErr    error
	runCalled    bool
	marketCalled bool
}

func (s *fakeSyncService) Run(context.Context) (service.DiscoverySyncResult, error) {
	s.runCalled = true
	return s.result, s.err
}

func (s *fakeSyncService) RunMarketOnly(context.Context) (service.DiscoverySyncResult, error) {
	s.marketCalled = true
	return s.marketResult, s.marketErr
}

func TestRunPrintsDiscoverySyncResult(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	err = run(context.Background(), config.Config{Discovery: config.DiscoveryConfig{Database: config.DatabaseConfig{Type: "sqlite", DSN: ":memory:"}}}, &out, syncDependencies{
		openMainDatabase:      func(config.DatabaseConfig) (*gorm.DB, error) { return db, nil },
		migrateMainDB:         database.Migrate,
		openDiscoveryDatabase: func(config.DatabaseConfig) (*gorm.DB, error) { return db, nil },
		migrateDiscoveryDB:    discovery.Migrate,
		newSyncService: func(*gorm.DB, config.DiscoveryConfig, *service.ConfigService) syncService {
			return &fakeSyncService{result: service.DiscoverySyncResult{Status: service.DiscoverySyncStatusPublished, BatchID: "market"}}
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
		openMainDatabase:      func(config.DatabaseConfig) (*gorm.DB, error) { return db, nil },
		migrateMainDB:         database.Migrate,
		openDiscoveryDatabase: func(config.DatabaseConfig) (*gorm.DB, error) { return db, nil },
		migrateDiscoveryDB:    discovery.Migrate,
		newSyncService: func(*gorm.DB, config.DiscoveryConfig, *service.ConfigService) syncService {
			return &fakeSyncService{
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

func TestRunSupportsMarketOnlyPhase(t *testing.T) {
	t.Setenv("DISCOVERY_SYNC_PHASE", "market")
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	fake := &fakeSyncService{marketResult: service.DiscoverySyncResult{Status: service.DiscoverySyncStatusPublished, BatchID: "market-only"}}
	var out bytes.Buffer
	err = run(context.Background(), config.Config{Discovery: config.DiscoveryConfig{Database: config.DatabaseConfig{Type: "sqlite", DSN: ":memory:"}}}, &out, syncDependencies{
		openMainDatabase:      func(config.DatabaseConfig) (*gorm.DB, error) { return db, nil },
		migrateMainDB:         database.Migrate,
		openDiscoveryDatabase: func(config.DatabaseConfig) (*gorm.DB, error) { return db, nil },
		migrateDiscoveryDB:    discovery.Migrate,
		newSyncService: func(*gorm.DB, config.DiscoveryConfig, *service.ConfigService) syncService {
			return fake
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if fake.runCalled || !fake.marketCalled {
		t.Fatalf("run called=%t market called=%t", fake.runCalled, fake.marketCalled)
	}
	if !strings.Contains(out.String(), `"batch_id":"market-only"`) {
		t.Fatalf("output = %s", out.String())
	}
}
