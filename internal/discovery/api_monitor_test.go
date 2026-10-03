package discovery

import (
	"context"
	"encoding/json"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func monitorTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	pool, _ := db.DB()
	pool.SetMaxOpenConns(1)
	t.Cleanup(func() { pool.Close() })
	if err = db.AutoMigrate(&APIProviderPolicy{}, &APICallRecord{}); err != nil {
		t.Fatal(err)
	}
	if err = EnsureAPIPolicies(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	db.Model(&APIProviderPolicy{}).Where("provider = ?", "longbridge").Update("min_interval_ms", 0)
	return db
}
func TestAPIMonitorBudgetPauseResetAndConcurrency(t *testing.T) {
	db := monitorTestDB(t)
	m := NewAPIMonitor(db)
	ctx := context.Background()
	db.Model(&APIProviderPolicy{}).Where("provider = ?", "longbridge").Update("daily_budget", 3)
	m2 := NewAPIMonitor(db)
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			active := m
			if i%2 == 0 {
				active = m2
			}
			r, e := active.Acquire(ctx, "longbridge", "ws/quote", "TEST")
			if e == nil {
				m.Finish(r, nil)
			}
		}(i)
	}
	wg.Wait()
	var p APIProviderPolicy
	db.First(&p, "provider = ?", "longbridge")
	if p.DailyUsed != 3 {
		t.Fatalf("used=%d", p.DailyUsed)
	}
	if _, e := m.Acquire(ctx, "futu", "test", ""); e == nil {
		t.Fatal("paused allowed")
	}
	db.Model(&p).Updates(map[string]any{"budget_date": "2000-01-01", "daily_used": 99})
	if _, e := m.Acquire(ctx, "longbridge", "test", ""); e != nil {
		t.Fatal(e)
	}
	db.First(&p, "provider = ?", "longbridge")
	if p.DailyUsed != 1 {
		t.Fatalf("reset used=%d", p.DailyUsed)
	}
	if e := EnsureAPIPolicies(ctx, db); e != nil {
		t.Fatal(e)
	}
	db.First(&p, "provider = ?", "longbridge")
	if p.DailyBudget != 3 {
		t.Fatal("policy reset")
	}
}
func TestAPIMonitorHTTPCountsRetriesAndRedacts(t *testing.T) {
	db := monitorTestDB(t)
	m := NewAPIMonitor(db)
	var attempts int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts == 1 {
			w.WriteHeader(429)
			io.WriteString(w, `{"message":"secret-test-token"}`)
		} else {
			io.WriteString(w, `{"code":0,"data":{}}`)
		}
	}))
	defer server.Close()
	for i := 0; i < 2; i++ {
		req, _ := http.NewRequestWithContext(WithAPITrigger(context.Background(), "manual"), "GET", server.URL+"/v1/quote?symbol=TEST&access_token=secret-test-token", nil)
		req.Header.Set("Authorization", "Bearer secret-test-token")
		resp, e := m.HTTPClient("longbridge").Do(req)
		if e != nil {
			t.Fatal(e)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}
	var rows []APICallRecord
	db.Order("id").Find(&rows)
	if len(rows) != 2 || rows[0].ErrorKind != "rate_limited" || rows[1].Status != "success" || rows[1].Trigger != "manual" {
		t.Fatalf("rows=%+v", rows)
	}
	b, _ := json.Marshal(rows)
	if strings.Contains(string(b), "secret-test-token") || strings.Contains(string(b), "access_token") {
		t.Fatal("secret in audit")
	}
	db.Model(&APIProviderPolicy{}).Where("provider = ?", "longbridge").Update("paused", true)
	if _, e := m.HTTPClient("longbridge").Get(server.URL); e == nil {
		t.Fatal("paused transport")
	}
	if attempts != 2 {
		t.Fatal("paused dispatched")
	}
}
func TestAPIMonitorCancellationDoesNotSpendBudget(t *testing.T) {
	db := monitorTestDB(t)
	m := NewAPIMonitor(db)
	m.next["longbridge"] = time.Now().Add(time.Minute)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e := m.Acquire(ctx, "longbridge", "x", ""); e == nil {
		t.Fatal("canceled allowed")
	}
	var n int64
	db.Model(&APICallRecord{}).Count(&n)
	if n != 0 {
		t.Fatal("spent canceled budget")
	}
}
