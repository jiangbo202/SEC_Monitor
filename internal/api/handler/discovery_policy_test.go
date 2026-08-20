package handler

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"sec_monitor/internal/discovery"
	"sec_monitor/internal/model"
	"sec_monitor/internal/service"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestDiscoverySmallCapPolicyHTTPFlow(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mainDB, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s-main?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "-"))), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := mainDB.AutoMigrate(&model.OperationLog{}); err != nil {
		t.Fatal(err)
	}
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "-"))
	discoveryDB, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := discovery.Migrate(discoveryDB); err != nil {
		t.Fatal(err)
	}
	h := &AppHandler{DiscoveryDB: discoveryDB, Audit: service.NewAuditService(mainDB)}
	r := gin.New()
	r.GET("/discovery/candidates/policy", h.GetDiscoverySmallCapPolicy)
	r.POST("/discovery/candidates/policy/preview", h.PreviewDiscoverySmallCapPolicy)
	r.POST("/discovery/candidates/policy/activate", h.ActivateDiscoverySmallCapPolicy)
	r.GET("/discovery/candidates/policy/versions", h.ListDiscoverySmallCapPolicyVersions)
	r.POST("/discovery/candidates/policy/versions/:id/rollback", h.RollbackDiscoverySmallCapPolicy)
	r.GET("/discovery/candidates/criteria", h.GetDiscoveryCandidateCriteria)

	active, err := discovery.GetActiveSmallCapPolicy(t.Context(), discoveryDB)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("gets seeded active policy", func(t *testing.T) {
		rec := servePolicyRequest(t, r, http.MethodGet, "/discovery/candidates/policy", nil, "")
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"active":{`) || !strings.Contains(rec.Body.String(), `"status":"active"`) || !strings.Contains(rec.Body.String(), `"b_market_cap_max_exclusive_usd":1000000000`) {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
	})

	proposed := smallCapPolicyEditableCriteria{MarketCapMinUSD: 30_000_000, AMarketCapMaxExclusiveUSD: 450_000_000, BMarketCapMaxExclusiveUSD: 900_000_000}

	t.Run("previews bootstrap without failing HTTP", func(t *testing.T) {
		rec := servePolicyRequest(t, r, http.MethodPost, "/discovery/candidates/policy/preview", smallCapPolicyPreviewRequest{Criteria: proposed}, "")
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"status":"needs_bootstrap"`) {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
	})

	t.Run("rejects invalid policy", func(t *testing.T) {
		invalid := proposed
		invalid.BMarketCapMaxExclusiveUSD = invalid.MarketCapMinUSD
		rec := servePolicyRequest(t, r, http.MethodPost, "/discovery/candidates/policy/preview", smallCapPolicyPreviewRequest{Criteria: invalid}, "")
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
	})

	activate := smallCapPolicyActivateRequest{ExpectedActiveVersionID: active.ID, Name: "Narrow cap range", Note: "HTTP activation", Criteria: proposed}
	rec := servePolicyRequest(t, r, http.MethodPost, "/discovery/candidates/policy/activate", activate, "alice")
	if rec.Code != http.StatusOK {
		t.Fatalf("activate status=%d body=%s", rec.Code, rec.Body.String())
	}
	activated, err := discovery.GetActiveSmallCapPolicy(t.Context(), discoveryDB)
	if err != nil {
		t.Fatal(err)
	}
	if activated.ID == active.ID || activated.CreatedBy != "alice" || activated.Policy.MarketCapMaxUSD != proposed.BMarketCapMaxExclusiveUSD {
		t.Fatalf("activated=%#v", activated)
	}

	t.Run("legacy criteria endpoint follows active policy", func(t *testing.T) {
		rec := servePolicyRequest(t, r, http.MethodGet, "/discovery/candidates/criteria", nil, "")
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"a_market_cap_max_exclusive_usd":450000000`) || !strings.Contains(rec.Body.String(), `"b_market_cap_max_exclusive_usd":900000000`) {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
	})

	t.Run("stale expected version conflicts", func(t *testing.T) {
		rec := servePolicyRequest(t, r, http.MethodPost, "/discovery/candidates/policy/activate", activate, "bob")
		if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), `"code":"policy_conflict"`) {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
	})

	t.Run("lists immutable versions", func(t *testing.T) {
		rec := servePolicyRequest(t, r, http.MethodGet, "/discovery/candidates/policy/versions", nil, "")
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"name":"Narrow cap range"`) || !strings.Contains(rec.Body.String(), `"name":"默认小盘股策略 v1"`) {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
	})

	t.Run("rolls back with optimistic concurrency", func(t *testing.T) {
		path := fmt.Sprintf("/discovery/candidates/policy/versions/%d/rollback", active.ID)
		rec := servePolicyRequest(t, r, http.MethodPost, path, smallCapPolicyRollbackRequest{ExpectedActiveVersionID: activated.ID, Note: "restore baseline"}, "carol")
		if rec.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
		current, err := discovery.GetActiveSmallCapPolicy(t.Context(), discoveryDB)
		if err != nil || current.ID == active.ID || current.ID == activated.ID || current.Policy.MarketCapMaxUSD != active.Policy.MarketCapMaxUSD {
			t.Fatalf("current=%#v err=%v", current, err)
		}
	})

	var logs []model.OperationLog
	if err := mainDB.Where("object_type = ?", "small_cap_policy").Order("id ASC").Find(&logs).Error; err != nil {
		t.Fatal(err)
	}
	if len(logs) != 2 || logs[0].Operator != "alice" || logs[0].Action != "activate" || logs[1].Operator != "carol" || logs[1].Action != "rollback" {
		t.Fatalf("policy audit logs=%#v", logs)
	}
}

func servePolicyRequest(t *testing.T, router http.Handler, method, path string, body any, actor string) *httptest.ResponseRecorder {
	t.Helper()
	var payload *bytes.Reader
	if body == nil {
		payload = bytes.NewReader(nil)
	} else {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		payload = bytes.NewReader(encoded)
	}
	req := httptest.NewRequest(method, path, payload)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if actor != "" {
		req.Header.Set("X-Operator", actor)
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}
