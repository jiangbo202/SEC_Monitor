package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sec_monitor/internal/config"
	"sec_monitor/internal/discovery"
	"sec_monitor/internal/model"
	"strings"
	"testing"
	"time"
)

func apiManagementTestService(t *testing.T) *APIManagementService {
	t.Helper()
	t.Setenv("CONFIG_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{3}, 32)))
	main, db := testDB(t), testDiscoveryDB(t)
	pool, _ := db.DB()
	pool.SetMaxOpenConns(1)
	t.Cleanup(func() { pool.Close() })
	if e := discovery.EnsureAPIPolicies(context.Background(), db); e != nil {
		t.Fatal(e)
	}
	cfg := NewConfigService(main, NewAuditService(main), config.Load().System)
	return NewAPIManagementService(db, main, cfg, NewTaskConfigService(main, NewAuditService(main)), config.Load().Discovery, discovery.NewAPIMonitor(db))
}
func seedFutuTestToken(t *testing.T, s *APIManagementService) {
	t.Helper()
	if e := s.Futu.saveToken(context.Background(), futuTokenResponse{AccessToken: "fake-access-only", RefreshToken: "fake-refresh-only", Scope: "quote:read", ExpiresIn: 3600}, true); e != nil {
		t.Fatal(e)
	}
	s.db.Model(&discovery.APIProviderPolicy{}).Where("provider = ?", "futu").Updates(map[string]any{"paused": false, "min_interval_ms": 0})
}
func TestFutuReadOnlyOAuthStateEncryptionAndDisconnect(t *testing.T) {
	s := apiManagementTestService(t)
	ctx := context.Background()
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path == "/oauth2/register" {
			io.WriteString(w, `{"client_id":"test-client"}`)
			return
		}
		r.ParseForm()
		if r.Form.Get("code_verifier") == "" {
			t.Error("no PKCE verifier")
		}
		io.WriteString(w, `{"access_token":"fake-access-only","refresh_token":"fake-refresh-only","expires_in":3600,"scope":"quote:read"}`)
	}))
	defer server.Close()
	s.Futu.host = server.URL
	raw, e := s.Futu.BeginAuthorization(ctx)
	if e != nil {
		t.Fatal(e)
	}
	u, _ := url.Parse(raw)
	q := u.Query()
	if q.Get("scope") != "quote:read" || q.Get("code_challenge_method") != "S256" {
		t.Fatal("unsafe auth")
	}
	if e = s.Futu.CompleteAuthorization(ctx, "bad-state", "fake-code"); e == nil || requests != 1 {
		t.Fatal("invalid state made external call")
	}
	if e = s.Futu.CompleteAuthorization(ctx, q.Get("state"), "fake-code"); e != nil {
		t.Fatal(e)
	}
	if e = s.Futu.CompleteAuthorization(ctx, q.Get("state"), "fake-code"); e == nil {
		t.Fatal("state replay")
	}
	for _, scope := range []string{"trade:read", "quote:read trade:write", "quote:write", ""} {
		if validateFutuScope(scope) {
			t.Fatal("scope accepted", scope)
		}
	}
	var stored model.SystemConfig
	s.main.First(&stored, "config_key = ?", "futu.access_token")
	if !strings.HasPrefix(stored.ConfigValue, "enc:v1:") {
		t.Fatal("plaintext token")
	}
	list, e := s.configs.List(ctx, "futu", true)
	if e != nil {
		t.Fatal(e)
	}
	b, _ := json.Marshal(list)
	if strings.Contains(string(b), "fake-access-only") || strings.Contains(string(b), "fake-refresh-only") {
		t.Fatal("token leaked")
	}
	var logs []model.OperationLog
	s.main.Find(&logs)
	b, _ = json.Marshal(logs)
	if strings.Contains(string(b), "fake-access-only") {
		t.Fatal("audit leaked")
	}
	if e = s.Futu.Disconnect(ctx); e != nil {
		t.Fatal(e)
	}
	ok, _ := s.Futu.Configured(ctx)
	if ok {
		t.Fatal("not disconnected")
	}
}
func TestFutuOwnershipPaginationNullCacheAndPreserve(t *testing.T) {
	s := apiManagementTestService(t)
	seedFutuTestToken(t, s)
	ctx := context.Background()
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != "Bearer fake-access-only" {
			t.Error("no auth")
		}
		if r.URL.Query().Get("limit") != "50" {
			t.Error("no limit")
		}
		if calls == 1 {
			io.WriteString(w, `{"ret_code":0,"data":{"holders":[{"period_text":"2026/Q2","holder_pct":12.5,"institution_quantity":10}]},"pagination":{"has_more":true,"next_key":"next"}}`)
		} else {
			io.WriteString(w, `{"ret_code":0,"data":{"holders":[{"period_text":"2026/Q1","holder_pct":null}]},"pagination":{"has_more":false}}`)
		}
	}))
	defer server.Close()
	s.Futu.host = server.URL
	r, e := s.Futu.RefreshOwnership(ctx, "test")
	if e != nil || r.Pages != 2 || r.Points != 2 {
		t.Fatalf("result=%+v err=%v", r, e)
	}
	r, e = s.Futu.RefreshOwnership(ctx, "TEST")
	if e != nil || !r.Cached || calls != 2 {
		t.Fatal("cache miss", r, e)
	}
	var rows []discovery.FutuInstitutionalPoint
	s.db.Order("period").Find(&rows)
	if len(rows) != 2 || rows[0].HolderPct != nil || rows[1].HolderQuantity != nil {
		t.Fatal("missing filled")
	}
	s.db.Model(&discovery.FutuInstitutionalReceipt{}).Where("ticker = ?", "TEST").Update("fetched_at", time.Now().Add(-25*time.Hour))
	s.Futu.host = "http://127.0.0.1:1"
	if _, e = s.Futu.RefreshOwnership(ctx, "TEST"); e == nil {
		t.Fatal("failure hidden")
	}
	var n int64
	s.db.Model(&discovery.FutuInstitutionalPoint{}).Count(&n)
	if n != 2 {
		t.Fatal("history lost")
	}
}
func TestFutuMalformedEmptyAndPaused(t *testing.T) {
	s := apiManagementTestService(t)
	seedFutuTestToken(t, s)
	calls := 0
	payload := `{"data":{"holders":[]}}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; io.WriteString(w, payload) }))
	defer server.Close()
	s.Futu.host = server.URL
	ctx := context.Background()
	if _, e := s.Futu.RefreshOwnership(ctx, "TEST"); e == nil {
		t.Fatal("missing code accepted")
	}
	payload = `{"ret_code":-10}`
	r, e := s.Futu.RefreshOwnership(ctx, "TEST")
	if e != nil || r.Status != "no_coverage" {
		t.Fatal(r, e)
	}
	s.Futu.RefreshOwnership(ctx, "TEST")
	if calls != 2 {
		t.Fatal("empty cache miss")
	}
	s.db.Model(&discovery.APIProviderPolicy{}).Where("provider = ?", "futu").Update("paused", true)
	if _, e = s.Futu.RefreshOwnership(ctx, "OTHER"); e == nil || calls != 2 {
		t.Fatal("paused requested")
	}
}
func TestAPIOverviewLocalMetricsCoverageAndPolicy(t *testing.T) {
	s := apiManagementTestService(t)
	ctx := context.Background()
	now := time.Now().UTC()
	s.db.Create(&[]discovery.APICallRecord{{Provider: "longbridge", Ticker: "TEST", Status: "success", StartedAt: now, ElapsedMS: 10}, {Provider: "longbridge", Ticker: "OTHER", Status: "failed", ErrorKind: "rate_limited", StartedAt: now, ElapsedMS: 30}, {Provider: "futu", Status: "failed", StartedAt: now.Add(-9 * 24 * time.Hour)}})
	s.db.Create(&discovery.AnalystRatingSnapshot{Ticker: "TEST", Provider: "longbridge", Status: "no_coverage", FetchedAt: now, SnapshotHash: "x"})
	r, e := s.Overview(ctx, "longbridge", "TEST", false)
	if e != nil {
		t.Fatal(e)
	}
	if len(r.Calls) != 1 || len(r.Providers) != 2 || len(r.Coverage) != 6 {
		t.Fatalf("overview=%+v", r)
	}
	for _, p := range r.Providers {
		if p.Policy.Provider == "longbridge" && (p.Requests != 2 || p.Failures != 1 || p.RateLimited != 1 || p.VendorQuota != nil) {
			t.Fatal("bad metrics", p)
		}
	}
	for _, c := range r.Coverage {
		if c.Capability == "analyst" && c.Status != "no_coverage" {
			t.Fatal("false coverage", c)
		}
	}
	if e = s.UpdatePolicy(ctx, "futu", discovery.APIProviderPolicy{DailyBudget: 50, MinIntervalMS: 1100}, "test"); e == nil {
		t.Fatal("unauthed enabled")
	}
	if e = s.UpdatePolicy(ctx, "longbridge", discovery.APIProviderPolicy{DailyBudget: -1, MinIntervalMS: 1100}, "test"); e == nil {
		t.Fatal("negative budget")
	}
	if e = s.SetCapability(ctx, "trading", true, "test"); e == nil {
		t.Fatal("trading enabled")
	}
	var n int64
	s.db.Model(&discovery.APICallRecord{}).Count(&n)
	if n != 3 {
		t.Fatal("GET made external call")
	}
}

func TestFutuPaginationCapResumesSavedCursor(t *testing.T) {
	s := apiManagementTestService(t)
	seedFutuTestToken(t, s)
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		switch r.URL.Query().Get("next_key") {
		case "":
			io.WriteString(w, `{"ret_code":0,"data":{"holders":[{"period_text":"2026/Q4","holder_pct":15}]},"pagination":{"has_more":true,"next_key":"one"}}`)
		case "one":
			io.WriteString(w, `{"ret_code":0,"data":{"holders":[{"period_text":"2026/Q3","holder_pct":14}]},"pagination":{"has_more":true,"next_key":"two"}}`)
		case "two":
			io.WriteString(w, `{"ret_code":0,"data":{"holders":[]},"pagination":{"has_more":false}}`)
		default:
			t.Error("unexpected cursor")
		}
	}))
	defer server.Close()
	s.Futu.host = server.URL
	r, e := s.Futu.RefreshOwnership(context.Background(), "TEST")
	if e != nil || r.Pages != 2 || r.Status != "partial" || calls != 2 {
		t.Fatal(r, e)
	}
	r, e = s.Futu.RefreshOwnership(context.Background(), "TEST")
	if e != nil || r.Cached || r.Pages != 1 || r.Status != "available" || calls != 3 {
		t.Fatal(r, e)
	}
	r, e = s.Futu.RefreshOwnership(context.Background(), "TEST")
	if e != nil || !r.Cached || calls != 3 {
		t.Fatal(r, e)
	}
}

func TestFutuRefreshTokenKeepsValidatedScopeAndSecret(t *testing.T) {
	s := apiManagementTestService(t)
	seedFutuTestToken(t, s)
	ctx := context.Background()
	if e := s.configs.UpsertMany(ctx, []ConfigInput{{Key: "futu.client_id", Value: "test-client", ValueType: "string", Category: "futu"}, {Key: "futu.token_expires_at", Value: time.Now().Add(-time.Hour).UTC().Format(time.RFC3339), ValueType: "string", Category: "futu"}}, "test"); e != nil {
		t.Fatal(e)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		if r.Form.Get("grant_type") != "refresh_token" || r.Form.Get("refresh_token") != "fake-refresh-only" {
			t.Error("invalid renewal")
		}
		io.WriteString(w, `{"access_token":"fake-renewed-only","expires_in":3600}`)
	}))
	defer server.Close()
	s.Futu.host = server.URL
	value, e := s.Futu.token(ctx)
	if e != nil || value != "fake-renewed-only" {
		t.Fatal(e)
	}
	refresh, _, e := s.configs.GetValue(ctx, "futu.refresh_token")
	if e != nil || refresh != "fake-refresh-only" {
		t.Fatal("lost refresh token")
	}
	var n int64
	s.db.Model(&discovery.APICallRecord{}).Count(&n)
	if n != 0 {
		t.Fatal("OAuth counted as market data")
	}
}

func TestAPICoverageUsesSuccessfulChecksWithoutChangingSnapshots(t *testing.T) {
	s := apiManagementTestService(t)
	ctx := context.Background()
	now := time.Now().UTC()
	old := now.Add(-30 * 24 * time.Hour)
	for _, ticker := range []string{"SAME", "FAILED", "OTHER"} {
		if err := s.db.Create(&discovery.AnalystRatingSnapshot{Ticker: ticker, Provider: "longbridge", Status: "available", FetchedAt: old, SnapshotHash: ticker}).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := discovery.RecordAPIDataSync(ctx, s.db, "longbridge", "analyst", "SAME", "available", now); err != nil {
		t.Fatal(err)
	}
	if err := discovery.RecordAPIDataSync(ctx, s.db, "futu", "analyst", "OTHER", "available", now); err != nil {
		t.Fatal(err)
	}
	if err := s.db.Create(&discovery.APICallRecord{Provider: "longbridge", Ticker: "FAILED", Status: "failed", StartedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	for ticker, want := range map[string]string{"SAME": "available", "FAILED": "stale", "OTHER": "stale"} {
		overview, err := s.Overview(ctx, "", ticker, false)
		if err != nil {
			t.Fatal(err)
		}
		for _, cell := range overview.Coverage {
			if cell.Capability == "analyst" {
				if cell.Status != want || cell.SnapshotAt == nil || !cell.SnapshotAt.Equal(old) {
					t.Fatalf("%s: %+v", ticker, cell)
				}
				if ticker == "SAME" && (cell.CheckedAt == nil || !cell.CheckedAt.Equal(now) || cell.TTLHours != 168) {
					t.Fatal(cell)
				}
			}
		}
	}
	if err := discovery.RecordAPIDataSync(ctx, s.db, "longbridge", "eps", "EMPTY", "no_coverage", now); err != nil {
		t.Fatal(err)
	}
	overview, err := s.Overview(ctx, "", "EMPTY", false)
	if err != nil {
		t.Fatal(err)
	}
	for _, cell := range overview.Coverage {
		if cell.Capability == "eps" && (cell.Status != "no_coverage" || cell.SnapshotAt != nil || cell.CheckedAt == nil) {
			t.Fatal(cell)
		}
	}
}

func TestAPICoverageUsesExistingValuationChecksButNotP1Success(t *testing.T) {
	s := apiManagementTestService(t)
	ctx := context.Background()
	now := time.Now().UTC()
	old := now.Add(-30 * 24 * time.Hour)
	for _, row := range []any{&discovery.LongbridgeValuationSnapshot{Ticker: "TEST", Provider: "longbridge", FetchedAt: old, SnapshotHash: "x"}, &discovery.EPSForecastSnapshot{Ticker: "TEST", Provider: "longbridge", FetchedAt: old, SnapshotHash: "x"}} {
		if err := s.db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, family := range []string{discovery.LongbridgeRefreshFamilyValuation, discovery.LongbridgeRefreshFamilyMarketResearch} {
		if err := discovery.MarkLongbridgeResearchSuccess(ctx, s.db, family, "TEST", now); err != nil {
			t.Fatal(err)
		}
	}
	overview, err := s.Overview(ctx, "", "TEST", false)
	if err != nil {
		t.Fatal(err)
	}
	for _, cell := range overview.Coverage {
		if cell.Capability == "valuation" && (cell.Status != "available" || cell.CheckedAt == nil || !cell.CheckedAt.Equal(now)) {
			t.Fatal(cell)
		}
		if cell.Capability == "eps" && (cell.Status != "stale" || cell.CheckedAt != nil) {
			t.Fatal("P1 completion must not prove EPS success", cell)
		}
	}
}
