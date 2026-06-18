package router

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"sec_monitor/internal/config"
	"sec_monitor/internal/model"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestRouterCreatesAndListsWatchTargets(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(
		&model.WatchTarget{}, &model.Filing{}, &model.SyncRun{}, &model.SyncRunDetail{}, &model.TaskConfig{},
		&model.SystemConfig{}, &model.OperationLog{}, &model.NotificationLog{},
	); err != nil {
		t.Fatalf("migrate db: %v", err)
	}

	r := New(Dependencies{Config: config.Config{}, DB: db})
	body := bytes.NewBufferString(`{"ticker":"msft","company_name":"Microsoft Corp.","target_type":"stock","status":"enabled"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/watch-targets", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body=%s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/watch-targets?page=1&page_size=10", nil)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d, body=%s", rec.Code, rec.Body.String())
	}

	var payload struct {
		Data struct {
			Total int64 `json:"total"`
			Items []struct {
				Ticker string `json:"ticker"`
			} `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload.Data.Total != 1 || payload.Data.Items[0].Ticker != "MSFT" {
		t.Fatalf("list payload = %+v, want one MSFT target", payload.Data)
	}
}

func TestRouterServesWebAppFallback(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(
		&model.WatchTarget{}, &model.Filing{}, &model.SyncRun{}, &model.SyncRunDetail{}, &model.TaskConfig{},
		&model.SystemConfig{}, &model.OperationLog{}, &model.NotificationLog{},
	); err != nil {
		t.Fatalf("migrate db: %v", err)
	}
	webDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(webDir, "index.html"), []byte("<!doctype html><title>SEC Monitor</title>"), 0o644); err != nil {
		t.Fatalf("write index: %v", err)
	}
	assetsDir := filepath.Join(webDir, "assets")
	if err := os.Mkdir(assetsDir, 0o755); err != nil {
		t.Fatalf("mkdir assets: %v", err)
	}
	if err := os.WriteFile(filepath.Join(assetsDir, "app.js"), []byte("console.log('ok')"), 0o644); err != nil {
		t.Fatalf("write asset: %v", err)
	}

	r := New(Dependencies{Config: config.Config{}, DB: db, WebDistDir: webDir})
	tests := []struct {
		name     string
		path     string
		wantBody string
	}{
		{name: "root", path: "/", wantBody: "SEC Monitor"},
		{name: "spa route", path: "/targets", wantBody: "SEC Monitor"},
		{name: "asset", path: "/assets/app.js", wantBody: "console.log('ok')"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
			}
			if !bytes.Contains(rec.Body.Bytes(), []byte(tt.wantBody)) {
				t.Fatalf("body = %s, want %q", rec.Body.String(), tt.wantBody)
			}
		})
	}
}
