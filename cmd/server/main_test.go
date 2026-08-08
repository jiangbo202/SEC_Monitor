package main

import (
	"database/sql"
	"errors"
	"strings"
	"testing"

	"sec_monitor/internal/api/router"
	"sec_monitor/internal/config"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func openLifecycleTestDatabase(t *testing.T) (*gorm.DB, *sql.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open lifecycle test database: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get lifecycle sql database: %v", err)
	}
	return db, sqlDB
}

func assertDatabaseClosed(t *testing.T, db *sql.DB) {
	t.Helper()
	if err := db.Ping(); err == nil {
		t.Fatal("database Ping succeeded after close")
	}
}

func TestServeHTTPGracefullyReturnsListenError(t *testing.T) {
	if err := serveHTTPGracefully(gin.New(), "invalid-address"); err == nil {
		t.Fatal("serveHTTPGracefully succeeded with invalid address")
	}
}

func TestRunWithDependenciesClosesMainDatabaseWhenDiscoveryOpenFails(t *testing.T) {
	mainDB, mainSQL := openLifecycleTestDatabase(t)

	err := runWithDependencies(config.Config{}, func(*gin.Engine, string) error { return nil }, startupDependencies{
		openMainDatabase:      func(config.DatabaseConfig) (*gorm.DB, error) { return mainDB, nil },
		migrateMainDatabase:   func(*gorm.DB) error { return nil },
		openDiscoveryDatabase: func(config.DatabaseConfig) (*gorm.DB, error) { return nil, errors.New("open failed") },
		migrateDiscoveryDB:    func(*gorm.DB) error { return nil },
		newRouter:             router.New,
	})
	if err == nil {
		t.Fatal("runWithDependencies expected error")
	}
	assertDatabaseClosed(t, mainSQL)
}

func TestRunWithDependenciesClosesMainDatabaseWhenMainMigrationFails(t *testing.T) {
	mainDB, mainSQL := openLifecycleTestDatabase(t)

	err := runWithDependencies(config.Config{}, func(*gin.Engine, string) error { return nil }, startupDependencies{
		openMainDatabase:      func(config.DatabaseConfig) (*gorm.DB, error) { return mainDB, nil },
		migrateMainDatabase:   func(*gorm.DB) error { return errors.New("migrate failed") },
		openDiscoveryDatabase: func(config.DatabaseConfig) (*gorm.DB, error) { return nil, nil },
		migrateDiscoveryDB:    func(*gorm.DB) error { return nil },
		newRouter:             router.New,
	})
	if err == nil {
		t.Fatal("runWithDependencies expected error")
	}
	assertDatabaseClosed(t, mainSQL)
}

func TestRunWithDependenciesClosesDatabasesWhenDiscoveryMigrationFails(t *testing.T) {
	mainDB, mainSQL := openLifecycleTestDatabase(t)
	discoveryDB, discoverySQL := openLifecycleTestDatabase(t)

	err := runWithDependencies(config.Config{}, func(*gin.Engine, string) error { return nil }, startupDependencies{
		openMainDatabase:      func(config.DatabaseConfig) (*gorm.DB, error) { return mainDB, nil },
		migrateMainDatabase:   func(*gorm.DB) error { return nil },
		openDiscoveryDatabase: func(config.DatabaseConfig) (*gorm.DB, error) { return discoveryDB, nil },
		migrateDiscoveryDB:    func(*gorm.DB) error { return errors.New("migrate failed") },
		newRouter:             router.New,
	})
	if err == nil {
		t.Fatal("runWithDependencies expected error")
	}
	assertDatabaseClosed(t, mainSQL)
	assertDatabaseClosed(t, discoverySQL)
}

func TestRunWithDependenciesKeepsDatabasesOpenUntilServeReturnsThenClosesThem(t *testing.T) {
	mainDB, mainSQL := openLifecycleTestDatabase(t)
	discoveryDB, discoverySQL := openLifecycleTestDatabase(t)

	err := runWithDependencies(config.Config{}, func(*gin.Engine, string) error {
		if err := mainSQL.Ping(); err != nil {
			t.Fatalf("main database closed during serve: %v", err)
		}
		if err := discoverySQL.Ping(); err != nil {
			t.Fatalf("discovery database closed during serve: %v", err)
		}
		return nil
	}, startupDependencies{
		openMainDatabase:      func(config.DatabaseConfig) (*gorm.DB, error) { return mainDB, nil },
		migrateMainDatabase:   func(*gorm.DB) error { return nil },
		openDiscoveryDatabase: func(config.DatabaseConfig) (*gorm.DB, error) { return discoveryDB, nil },
		migrateDiscoveryDB:    func(*gorm.DB) error { return nil },
		newRouter:             func(router.Dependencies) (*gin.Engine, error) { return gin.New(), nil },
	})
	if err != nil {
		t.Fatalf("runWithDependencies: %v", err)
	}
	assertDatabaseClosed(t, mainSQL)
	assertDatabaseClosed(t, discoverySQL)
}

func TestRunWithDependenciesWrapsDiscoveryStartupErrors(t *testing.T) {
	mainDB, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open main test database: %v", err)
	}
	discoveryDB, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open discovery test database: %v", err)
	}

	tests := []struct {
		name    string
		open    func(config.DatabaseConfig) (*gorm.DB, error)
		migrate func(*gorm.DB) error
		want    string
	}{
		{
			name: "open discovery database",
			open: func(config.DatabaseConfig) (*gorm.DB, error) {
				return nil, errors.New("open failed")
			},
			migrate: func(*gorm.DB) error { return nil },
			want:    "open discovery database: open failed",
		},
		{
			name: "migrate discovery database",
			open: func(config.DatabaseConfig) (*gorm.DB, error) {
				return discoveryDB, nil
			},
			migrate: func(*gorm.DB) error { return errors.New("migrate failed") },
			want:    "migrate discovery database: migrate failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := runWithDependencies(config.Config{}, func(*gin.Engine, string) error { return nil }, startupDependencies{
				openMainDatabase:      func(config.DatabaseConfig) (*gorm.DB, error) { return mainDB, nil },
				migrateMainDatabase:   func(*gorm.DB) error { return nil },
				openDiscoveryDatabase: tt.open,
				migrateDiscoveryDB:    tt.migrate,
				newRouter:             router.New,
			})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("runWithDependencies error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestRunWithDependenciesPassesBothDatabaseHandles(t *testing.T) {
	mainDB, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open main test database: %v", err)
	}
	discoveryDB, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open discovery test database: %v", err)
	}
	var got router.Dependencies
	served := false
	var calls []string
	err = runWithDependencies(config.Config{}, func(*gin.Engine, string) error {
		calls = append(calls, "serve")
		served = true
		return nil
	}, startupDependencies{
		openMainDatabase: func(config.DatabaseConfig) (*gorm.DB, error) {
			calls = append(calls, "open main")
			return mainDB, nil
		},
		migrateMainDatabase: func(*gorm.DB) error {
			calls = append(calls, "migrate main")
			return nil
		},
		openDiscoveryDatabase: func(config.DatabaseConfig) (*gorm.DB, error) {
			calls = append(calls, "open discovery")
			return discoveryDB, nil
		},
		migrateDiscoveryDB: func(*gorm.DB) error {
			calls = append(calls, "migrate discovery")
			return nil
		},
		newRouter: func(deps router.Dependencies) (*gin.Engine, error) {
			calls = append(calls, "router")
			got = deps
			return gin.New(), nil
		},
	})
	if err != nil {
		t.Fatalf("runWithDependencies: %v", err)
	}
	if !served || got.DB != mainDB || got.DiscoveryDB != discoveryDB {
		t.Fatalf("served=%v main=%p discovery=%p", served, got.DB, got.DiscoveryDB)
	}
	wantCalls := []string{"open main", "migrate main", "open discovery", "migrate discovery", "router", "serve"}
	if strings.Join(calls, ",") != strings.Join(wantCalls, ",") {
		t.Fatalf("calls = %v, want %v", calls, wantCalls)
	}
}

func TestRunWithDependenciesPropagatesRouterInitializationFailure(t *testing.T) {
	mainDB, mainSQL := openLifecycleTestDatabase(t)
	discoveryDB, discoverySQL := openLifecycleTestDatabase(t)
	served := false

	err := runWithDependencies(config.Config{}, func(*gin.Engine, string) error {
		served = true
		return nil
	}, startupDependencies{
		openMainDatabase:      func(config.DatabaseConfig) (*gorm.DB, error) { return mainDB, nil },
		migrateMainDatabase:   func(*gorm.DB) error { return nil },
		openDiscoveryDatabase: func(config.DatabaseConfig) (*gorm.DB, error) { return discoveryDB, nil },
		migrateDiscoveryDB:    func(*gorm.DB) error { return nil },
		newRouter: func(router.Dependencies) (*gin.Engine, error) {
			return nil, errors.New("configuration initialization failed")
		},
	})
	if err == nil || !strings.Contains(err.Error(), "initialize router: configuration initialization failed") {
		t.Fatalf("runWithDependencies error = %v", err)
	}
	if served {
		t.Fatal("server was called after router initialization failed")
	}
	assertDatabaseClosed(t, mainSQL)
	assertDatabaseClosed(t, discoverySQL)
}

func TestRunTableDriven(t *testing.T) {
	tests := []struct {
		name       string
		cfg        config.Config
		serve      func(app *gin.Engine, address string) error
		wantErr    bool
		wantCalled bool
	}{
		{
			name: "opens migrates and serves",
			cfg: config.Config{
				Server:    config.ServerConfig{Address: "127.0.0.1:0"},
				Database:  config.DatabaseConfig{Type: "sqlite", DSN: ":memory:"},
				Discovery: config.DiscoveryConfig{Database: config.DatabaseConfig{Type: "sqlite", DSN: ":memory:"}},
			},
			serve: func(app *gin.Engine, address string) error {
				if address != "127.0.0.1:0" {
					t.Fatalf("address = %q", address)
				}
				return nil
			},
			wantCalled: true,
		},
		{
			name:    "database open error",
			cfg:     config.Config{Database: config.DatabaseConfig{Type: "bad"}},
			serve:   func(app *gin.Engine, address string) error { return nil },
			wantErr: true,
		},
		{
			name: "serve error",
			cfg: config.Config{
				Database:  config.DatabaseConfig{Type: "sqlite", DSN: ":memory:"},
				Discovery: config.DiscoveryConfig{Database: config.DatabaseConfig{Type: "sqlite", DSN: ":memory:"}},
			},
			serve: func(app *gin.Engine, address string) error {
				return errors.New("listen failed")
			},
			wantErr:    true,
			wantCalled: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			called := false
			err := run(tt.cfg, func(app *gin.Engine, address string) error {
				called = true
				return tt.serve(app, address)
			})
			if tt.wantErr && err == nil {
				t.Fatalf("run expected error")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("run: %v", err)
			}
			if called != tt.wantCalled {
				t.Fatalf("called = %v, want %v", called, tt.wantCalled)
			}
		})
	}
}
