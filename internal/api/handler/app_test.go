package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"sec_monitor/internal/model"
	"sec_monitor/internal/sec"
	"sec_monitor/internal/service"
	"sec_monitor/internal/telegram"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type fakeSECClient struct{}

func (f fakeSECClient) LookupCIK(ctx context.Context, ticker string) (string, string, error) {
	return "0000320193", "Apple Inc.", nil
}

func (f fakeSECClient) ListFilings(ctx context.Context, query sec.FilingQuery) ([]sec.FilingResult, error) {
	return []sec.FilingResult{{
		FilingID:        "0000320193-26-000001",
		AccessionNumber: "0000320193-26-000001",
		Ticker:          "AAPL",
		CIK:             "0000320193",
		CompanyName:     "Apple Inc.",
		FilingType:      "8-K",
		FilingDate:      time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		FilingURL:       "https://sec.gov/aapl/8k",
		Title:           "Current report",
	}}, nil
}

type fakeNotifier struct{}

func (f fakeNotifier) Send(ctx context.Context, message telegram.Message) error {
	return nil
}

type fakeScheduler struct {
	reloadCalls int
	runCalls    int
	reloadErr   error
	runErr      error
}

func (f *fakeScheduler) Reload(ctx context.Context) error {
	f.reloadCalls++
	return f.reloadErr
}

func (f *fakeScheduler) RunOnce(ctx context.Context) error {
	f.runCalls++
	return f.runErr
}

func testApp(t *testing.T) (*gin.Engine, *gorm.DB, *fakeScheduler) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(
		&model.WatchTarget{}, &model.Filing{}, &model.SyncRun{}, &model.SyncRunDetail{}, &model.TaskConfig{},
		&model.SystemConfig{}, &model.OperationLog{}, &model.NotificationLog{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	audit := service.NewAuditService(db)
	configs := service.NewConfigService(db, audit)
	targets := service.NewWatchTargetService(db, audit)
	filings := service.NewFilingService(db, fakeSECClient{}, fakeNotifier{}, configs)
	tasks := service.NewTaskConfigService(db, audit)
	if err := tasks.EnsureDefault(context.Background()); err != nil {
		t.Fatalf("default task: %v", err)
	}
	sched := &fakeScheduler{}
	h := &AppHandler{
		DB:           db,
		Targets:      targets,
		Configs:      configs,
		Tasks:        tasks,
		Filings:      filings,
		SEC:          fakeSECClient{},
		Audit:        audit,
		Notification: service.NewNotificationService(db),
		Scheduler:    sched,
	}
	r := gin.New()
	r.GET("/healthz", Health)
	r.GET("/sec/tickers/:ticker", h.LookupTicker)
	r.GET("/targets", h.ListWatchTargets)
	r.POST("/targets", h.CreateWatchTarget)
	r.GET("/targets/:id", h.GetWatchTarget)
	r.PUT("/targets/:id", h.UpdateWatchTarget)
	r.DELETE("/targets/:id", h.DeleteWatchTarget)
	r.PATCH("/targets/:id/status", h.SetWatchTargetStatus)
	r.POST("/targets/:id/sync", h.SyncWatchTarget)
	r.GET("/targets/:id/sync-details", h.ListWatchTargetSyncDetails)
	r.GET("/filings", h.ListFilings)
	r.POST("/filings/refresh", h.RefreshFilings)
	r.GET("/filings/:id", h.GetFiling)
	r.GET("/sync-runs/:id/details", h.ListSyncRunDetails)
	r.GET("/configs", h.ListSystemConfigs)
	r.PUT("/configs", h.UpdateSystemConfigs)
	r.POST("/configs/reload", h.ListSystemConfigs)
	r.GET("/telegram/config", h.GetTelegramConfig)
	r.PUT("/telegram/config", h.UpdateTelegramConfig)
	r.POST("/telegram/test", h.TestTelegram)
	r.GET("/operation-logs", h.ListOperationLogs)
	r.GET("/notification-logs", h.ListNotificationLogs)
	r.GET("/tasks", h.ListTaskConfigs)
	r.PUT("/tasks/:id", h.UpdateTaskConfig)
	r.POST("/tasks/:id/run", h.RunTask)
	r.GET("/list-health", h.ListHealth)
	r.GET("/not-implemented", NotImplemented("example"))
	return r, db, sched
}

func TestAppHandlerRoutesTableDriven(t *testing.T) {
	tests := []struct {
		name       string
		method     string
		path       string
		body       string
		seed       func(t *testing.T, db *gorm.DB)
		assert     func(t *testing.T, rec *httptest.ResponseRecorder, db *gorm.DB, sched *fakeScheduler)
		wantStatus int
	}{
		{name: "health", method: http.MethodGet, path: "/healthz", wantStatus: http.StatusOK},
		{name: "lookup ticker", method: http.MethodGet, path: "/sec/tickers/tsla", wantStatus: http.StatusOK, assert: func(t *testing.T, rec *httptest.ResponseRecorder, db *gorm.DB, sched *fakeScheduler) {
			if !strings.Contains(rec.Body.String(), `"ticker":"TSLA"`) || !strings.Contains(rec.Body.String(), `"cik":"0000320193"`) {
				t.Fatalf("lookup body = %s", rec.Body.String())
			}
		}},
		{name: "list health", method: http.MethodGet, path: "/list-health", wantStatus: http.StatusOK},
		{name: "create target", method: http.MethodPost, path: "/targets", body: `{"ticker":"aapl","company_name":"Apple Inc.","target_type":"stock","status":"enabled"}`, wantStatus: http.StatusCreated},
		{name: "reject invalid target", method: http.MethodPost, path: "/targets", body: `{"ticker":"","company_name":"Apple Inc.","target_type":"stock","status":"enabled"}`, wantStatus: http.StatusBadRequest},
		{name: "list targets", method: http.MethodGet, path: "/targets?page=bad&page_size=bad", seed: seedTarget, wantStatus: http.StatusOK},
		{name: "get target", method: http.MethodGet, path: "/targets/1", seed: seedTarget, wantStatus: http.StatusOK},
		{name: "missing target", method: http.MethodGet, path: "/targets/99", wantStatus: http.StatusNotFound},
		{name: "update target", method: http.MethodPut, path: "/targets/1", body: `{"ticker":"msft","company_name":"Microsoft Corp.","target_type":"stock","status":"enabled"}`, seed: seedTarget, wantStatus: http.StatusOK},
		{name: "set target status", method: http.MethodPatch, path: "/targets/1/status", body: `{"status":"disabled"}`, seed: seedTarget, wantStatus: http.StatusOK},
		{name: "delete target", method: http.MethodDelete, path: "/targets/1", seed: seedTarget, wantStatus: http.StatusNoContent},
		{name: "list filings", method: http.MethodGet, path: "/filings?ticker=AAPL&date_from=2026-06-01&date_to=bad", seed: seedFiling, wantStatus: http.StatusOK},
		{name: "get filing", method: http.MethodGet, path: "/filings/1", seed: seedFiling, wantStatus: http.StatusOK},
		{name: "refresh filings", method: http.MethodPost, path: "/filings/refresh", seed: seedTarget, wantStatus: http.StatusOK},
		{name: "sync target", method: http.MethodPost, path: "/targets/1/sync", seed: seedTarget, wantStatus: http.StatusOK, assert: func(t *testing.T, rec *httptest.ResponseRecorder, db *gorm.DB, sched *fakeScheduler) {
			if !strings.Contains(rec.Body.String(), `"new_filings":1`) {
				t.Fatalf("body = %s, want new_filings", rec.Body.String())
			}
		}},
		{name: "list target sync details", method: http.MethodGet, path: "/targets/1/sync-details", seed: seedSyncRunDetail, wantStatus: http.StatusOK, assert: func(t *testing.T, rec *httptest.ResponseRecorder, db *gorm.DB, sched *fakeScheduler) {
			if !strings.Contains(rec.Body.String(), `"ticker":"AAPL"`) || !strings.Contains(rec.Body.String(), `"duration_ms":2000`) {
				t.Fatalf("body = %s, want target sync details", rec.Body.String())
			}
		}},
		{name: "list sync run details", method: http.MethodGet, path: "/sync-runs/1/details", seed: seedSyncRunDetail, wantStatus: http.StatusOK, assert: func(t *testing.T, rec *httptest.ResponseRecorder, db *gorm.DB, sched *fakeScheduler) {
			if !strings.Contains(rec.Body.String(), `"ticker":"AAPL"`) || !strings.Contains(rec.Body.String(), `"status":"success"`) {
				t.Fatalf("body = %s, want sync detail", rec.Body.String())
			}
		}},
		{name: "list configs", method: http.MethodGet, path: "/configs?category=telegram", seed: seedTelegramConfig, wantStatus: http.StatusOK},
		{name: "update configs", method: http.MethodPut, path: "/configs", body: `[{"key":"system.log_level","value":"debug","value_type":"string","category":"system"}]`, wantStatus: http.StatusOK},
		{name: "reload configs", method: http.MethodPost, path: "/configs/reload", wantStatus: http.StatusOK},
		{name: "get telegram config", method: http.MethodGet, path: "/telegram/config", seed: seedTelegramConfig, wantStatus: http.StatusOK},
		{name: "update telegram config", method: http.MethodPut, path: "/telegram/config", body: `{"bot_token":"token","chat_id":"10001","enabled":true}`, wantStatus: http.StatusOK},
		{name: "update telegram config preserves masked token", method: http.MethodPut, path: "/telegram/config", body: `{"bot_token":"tok******ken","chat_id":"20002","enabled":false}`, seed: seedTelegramConfig, wantStatus: http.StatusOK, assert: func(t *testing.T, rec *httptest.ResponseRecorder, db *gorm.DB, sched *fakeScheduler) {
			token, ok, err := service.NewConfigService(db, service.NewAuditService(db)).GetValue(context.Background(), "telegram.bot_token")
			if err != nil {
				t.Fatalf("get token: %v", err)
			}
			if !ok || token != "token" {
				t.Fatalf("stored token = %q, ok=%v, want original token", token, ok)
			}
		}},
		{name: "list operation logs", method: http.MethodGet, path: "/operation-logs?action=create", seed: seedTarget, wantStatus: http.StatusOK},
		{name: "list notification logs", method: http.MethodGet, path: "/notification-logs?status=success&channel=telegram", seed: seedNotification, wantStatus: http.StatusOK},
		{name: "list tasks", method: http.MethodGet, path: "/tasks", wantStatus: http.StatusOK},
		{name: "update task reloads scheduler", method: http.MethodPut, path: "/tasks/1", body: `{"cron_expr":"*/30 * * * *","enabled":false}`, wantStatus: http.StatusOK, assert: func(t *testing.T, rec *httptest.ResponseRecorder, db *gorm.DB, sched *fakeScheduler) {
			if sched.reloadCalls != 1 {
				t.Fatalf("reloadCalls = %d, want 1", sched.reloadCalls)
			}
		}},
		{name: "run task uses scheduler", method: http.MethodPost, path: "/tasks/1/run", wantStatus: http.StatusOK, assert: func(t *testing.T, rec *httptest.ResponseRecorder, db *gorm.DB, sched *fakeScheduler) {
			if sched.runCalls != 1 {
				t.Fatalf("runCalls = %d, want 1", sched.runCalls)
			}
		}},
		{name: "telegram test rejects masked token", method: http.MethodPost, path: "/telegram/test", seed: seedMaskedTelegramConfig, wantStatus: http.StatusBadRequest, assert: func(t *testing.T, rec *httptest.ResponseRecorder, db *gorm.DB, sched *fakeScheduler) {
			if !strings.Contains(rec.Body.String(), "重新输入真实 Token") {
				t.Fatalf("body = %s, want clear token error", rec.Body.String())
			}
		}},
		{name: "telegram test returns validation error without token", method: http.MethodPost, path: "/telegram/test", wantStatus: http.StatusInternalServerError},
		{name: "not implemented helper", method: http.MethodGet, path: "/not-implemented", wantStatus: http.StatusNotImplemented},
		{name: "invalid create json", method: http.MethodPost, path: "/targets", body: `{`, wantStatus: http.StatusInternalServerError},
		{name: "invalid update json", method: http.MethodPut, path: "/targets/1", body: `{`, seed: seedTarget, wantStatus: http.StatusInternalServerError},
		{name: "invalid status json", method: http.MethodPatch, path: "/targets/1/status", body: `{`, seed: seedTarget, wantStatus: http.StatusInternalServerError},
		{name: "invalid configs json", method: http.MethodPut, path: "/configs", body: `{`, wantStatus: http.StatusInternalServerError},
		{name: "invalid telegram json", method: http.MethodPut, path: "/telegram/config", body: `{`, wantStatus: http.StatusInternalServerError},
		{name: "invalid task json", method: http.MethodPut, path: "/tasks/1", body: `{`, wantStatus: http.StatusInternalServerError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, db, sched := testApp(t)
			if tt.seed != nil {
				tt.seed(t, db)
			}
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(tt.method, tt.path, bytes.NewBufferString(tt.body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Operator", "tester")
			r.ServeHTTP(rec, req)
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d, body=%s", rec.Code, tt.wantStatus, rec.Body.String())
			}
			if rec.Code != http.StatusNoContent {
				var payload map[string]any
				if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
					t.Fatalf("decode response: %v", err)
				}
			}
			if tt.assert != nil {
				tt.assert(t, rec, db, sched)
			}
		})
	}
}

func TestAppHandlerSchedulerErrorTableDriven(t *testing.T) {
	tests := []struct {
		name   string
		method string
		path   string
		body   string
		setup  func(*fakeScheduler)
	}{
		{name: "reload error", method: http.MethodPut, path: "/tasks/1", body: `{"cron_expr":"*/30 * * * *","enabled":true}`, setup: func(s *fakeScheduler) {
			s.reloadErr = errors.New("reload failed")
		}},
		{name: "run error", method: http.MethodPost, path: "/tasks/1/run", setup: func(s *fakeScheduler) {
			s.runErr = errors.New("run failed")
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, _, sched := testApp(t)
			tt.setup(sched)
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(tt.method, tt.path, bytes.NewBufferString(tt.body))
			req.Header.Set("Content-Type", "application/json")
			r.ServeHTTP(rec, req)
			if rec.Code != http.StatusInternalServerError {
				t.Fatalf("status = %d, want 500, body=%s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestAppHandlerRunTaskWithoutSchedulerTableDriven(t *testing.T) {
	tests := []struct {
		name string
		seed func(t *testing.T, db *gorm.DB)
	}{
		{name: "runs refresh fallback without scheduler", seed: seedTarget},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, db, _ := testApp(t)
			tt.seed(t, db)
			// Replace the route with a handler that has no scheduler to cover fallback behavior.
			audit := service.NewAuditService(db)
			configs := service.NewConfigService(db, audit)
			h := &AppHandler{
				DB:           db,
				Targets:      service.NewWatchTargetService(db, audit),
				Configs:      configs,
				Tasks:        service.NewTaskConfigService(db, audit),
				Filings:      service.NewFilingService(db, fakeSECClient{}, fakeNotifier{}, configs),
				Audit:        audit,
				Notification: service.NewNotificationService(db),
			}
			r.POST("/tasks-no-scheduler/:id/run", h.RunTask)
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/tasks-no-scheduler/1/run", nil)
			r.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestAppHandlerDatabaseErrorTableDriven(t *testing.T) {
	tests := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{name: "list targets db error", method: http.MethodGet, path: "/targets"},
		{name: "delete target db error", method: http.MethodDelete, path: "/targets/1"},
		{name: "list filings db error", method: http.MethodGet, path: "/filings"},
		{name: "get filing db error", method: http.MethodGet, path: "/filings/1"},
		{name: "refresh filings db error", method: http.MethodPost, path: "/filings/refresh"},
		{name: "list configs db error", method: http.MethodGet, path: "/configs"},
		{name: "get telegram config db error", method: http.MethodGet, path: "/telegram/config"},
		{name: "list operation logs db error", method: http.MethodGet, path: "/operation-logs"},
		{name: "list notification logs db error", method: http.MethodGet, path: "/notification-logs"},
		{name: "list tasks db error", method: http.MethodGet, path: "/tasks"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, db, _ := testApp(t)
			sqlDB, err := db.DB()
			if err != nil {
				t.Fatalf("db handle: %v", err)
			}
			if err := sqlDB.Close(); err != nil {
				t.Fatalf("close db: %v", err)
			}
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(tt.method, tt.path, bytes.NewBufferString(tt.body))
			req.Header.Set("Content-Type", "application/json")
			r.ServeHTTP(rec, req)
			if rec.Code != http.StatusInternalServerError {
				t.Fatalf("status = %d, want 500, body=%s", rec.Code, rec.Body.String())
			}
		})
	}
}

func seedTarget(t *testing.T, db *gorm.DB) {
	t.Helper()
	if err := db.Create(&model.WatchTarget{
		Ticker: "AAPL", CompanyName: "Apple Inc.", CIK: "0000320193", TargetType: "stock", Status: "enabled",
	}).Error; err != nil {
		t.Fatalf("seed target: %v", err)
	}
}

func seedFiling(t *testing.T, db *gorm.DB) {
	t.Helper()
	if err := db.Create(&model.Filing{
		FilingID: "f1", Ticker: "AAPL", CIK: "0000320193", CompanyName: "Apple Inc.",
		FilingType: "8-K", FilingDate: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC), PulledAt: time.Now(),
	}).Error; err != nil {
		t.Fatalf("seed filing: %v", err)
	}
}

func seedTelegramConfig(t *testing.T, db *gorm.DB) {
	t.Helper()
	if err := service.NewConfigService(db, service.NewAuditService(db)).UpsertMany(context.Background(), []service.ConfigInput{
		{Key: "telegram.bot_token", Value: "token", ValueType: "string", Category: "telegram", Encrypted: true},
		{Key: "telegram.chat_id", Value: "10001", ValueType: "string", Category: "telegram"},
		{Key: "telegram.enabled", Value: "true", ValueType: "bool", Category: "telegram"},
	}, "tester"); err != nil {
		t.Fatalf("seed configs: %v", err)
	}
}

func seedMaskedTelegramConfig(t *testing.T, db *gorm.DB) {
	t.Helper()
	if err := service.NewConfigService(db, service.NewAuditService(db)).UpsertMany(context.Background(), []service.ConfigInput{
		{Key: "telegram.bot_token", Value: "tok******ken", ValueType: "string", Category: "telegram", Encrypted: true},
		{Key: "telegram.chat_id", Value: "10001", ValueType: "string", Category: "telegram"},
		{Key: "telegram.enabled", Value: "true", ValueType: "bool", Category: "telegram"},
	}, "tester"); err != nil {
		t.Fatalf("seed masked configs: %v", err)
	}
}

func seedNotification(t *testing.T, db *gorm.DB) {
	t.Helper()
	if err := db.Create(&model.NotificationLog{FilingID: "f1", Channel: "telegram", Status: "success"}).Error; err != nil {
		t.Fatalf("seed notification: %v", err)
	}
}

func seedSyncRunDetail(t *testing.T, db *gorm.DB) {
	t.Helper()
	startedAt := time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC)
	finishedAt := startedAt.Add(2 * time.Second)
	run := model.SyncRun{
		StartedAt:      startedAt,
		FinishedAt:     &finishedAt,
		Status:         "success",
		Trigger:        "manual",
		TargetsChecked: 1,
		NewFilings:     1,
	}
	if err := db.Create(&run).Error; err != nil {
		t.Fatalf("seed sync run: %v", err)
	}
	if err := db.Create(&model.SyncRunDetail{
		SyncRunID:  run.ID,
		TargetID:   1,
		Ticker:     "AAPL",
		Status:     "success",
		NewFilings: 1,
		StartedAt:  startedAt,
		FinishedAt: &finishedAt,
		DurationMS: 2000,
	}).Error; err != nil {
		t.Fatalf("seed sync run detail: %v", err)
	}
}
