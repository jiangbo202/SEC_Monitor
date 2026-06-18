package main

import (
	"errors"
	"testing"

	"sec_monitor/internal/config"

	"github.com/gin-gonic/gin"
)

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
			cfg:  config.Config{Server: config.ServerConfig{Address: "127.0.0.1:0"}, Database: config.DatabaseConfig{Type: "sqlite", DSN: ":memory:"}},
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
			cfg:  config.Config{Database: config.DatabaseConfig{Type: "sqlite", DSN: ":memory:"}},
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
