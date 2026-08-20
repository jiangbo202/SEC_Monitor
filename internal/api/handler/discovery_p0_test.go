package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"sec_monitor/internal/config"
	"sec_monitor/internal/discovery"
	"sec_monitor/internal/service"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestDiscoveryP0HTTPStates(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open("file:discovery-p0-handler?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := discovery.Migrate(db); err != nil {
		t.Fatal(err)
	}
	h := &AppHandler{DiscoveryDB: db, DiscoverySync: service.NewDiscoverySyncService(db, config.DiscoveryConfig{})}
	r := gin.New()
	r.POST("/discovery/candidates/refresh", h.RefreshDiscoveryCandidates)
	r.GET("/discovery/candidates/report", h.GetDiscoveryCandidateReport)

	t.Run("manual full reports persisted running lease", func(t *testing.T) {
		run := discovery.DiscoverySyncRun{Kind: "incremental", Status: "running", Phase: "market_prescreen", StartedAt: time.Now(), UpdatedAt: time.Now()}
		if err := db.Create(&run).Error; err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = db.Delete(&run).Error })
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/discovery/candidates/refresh", nil))
		if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), `"code":"task_busy"`) {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
	})

	t.Run("candidate report has normal bootstrap state", func(t *testing.T) {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/discovery/candidates/report", nil))
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"status":"empty"`) || !strings.Contains(rec.Body.String(), `"available":false`) {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
	})
}
