package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sec_monitor/internal/discovery"
	"sec_monitor/internal/model"
	"strings"
	"testing"
	"time"
)

func futureDecode(out any, data any) error {
	b, _ := json.Marshal(map[string]any{"data": data})
	return json.Unmarshal(b, out)
}
func futureBar(date int) map[string]any {
	return map[string]any{"date": date, "open": 100, "high": 103, "low": 99, "close": 102, "volume": 1100}
}
func TestUSFuturesRefreshCachesContinuousContracts(t *testing.T) {
	db := testDB(t)
	if err := db.AutoMigrate(&model.MarketTrendDaily{}); err != nil {
		t.Fatal(err)
	}
	s := NewUSFuturesService(db, nil)
	s.now = func() time.Time { return time.Date(2026, 10, 3, 10, 0, 0, 0, time.UTC) }
	calls := 0
	s.readJSON = func(ctx context.Context, method, path string, q url.Values, body []byte, out any) error {
		calls++
		if method != http.MethodGet || len(body) > 0 {
			t.Fatal("non-read-only request")
		}
		if strings.HasSuffix(path, "reference-future") {
			symbol := strings.Split(path, "/")[4]
			return futureDecode(out, map[string]any{"reference_list": []any{map[string]any{"code": symbol, "stock_type": "FUTURE", "future_main_contract": true, "future_valid": true}}})
		}
		if q.Get("ktype") != "2" || q.Get("autype") != "0" || q.Get("num") != "370" {
			t.Fatal(q)
		}
		day, _ := time.Parse(time.DateOnly, q.Get("start"))
		date, _ := strconvDate(day)
		date2, _ := strconvDate(day.AddDate(0, 0, 1))
		return futureDecode(out, map[string]any{"kline_list": []any{futureBar(date), futureBar(date2)}})
	}
	result, err := s.Refresh(context.Background())
	if err != nil || result.SymbolsUpdated != 10 || result.BarsSaved != 20 || calls != 20 {
		t.Fatalf("result=%+v err=%v calls=%d", result, err, calls)
	}
	// Legacy provider rows must not contaminate a Futu series or its freshness.
	db.Create(&model.MarketTrendDaily{Symbol: "legacy", Group: "futures", Source: "legacy", TradeDate: "2026-10-02", Close: 999, FetchedAt: s.now().Add(time.Hour)})
	response, err := s.List(context.Background(), 250)
	if err != nil || response.Source != "futu" || len(response.Futures) != 10 || response.Futures[0].Symbol != "US.ESmain" || !response.LastFetched.Equal(s.now()) {
		t.Fatalf("response=%+v err=%v", response, err)
	}
}
func strconvDate(day time.Time) (int, error) {
	var n int
	_, err := fmt.Sscanf(day.Format("20060102"), "%d", &n)
	return n, err
}
func TestUSFuturesRefreshStopsAfterFirstRateLimit(t *testing.T) {
	s := NewUSFuturesService(testDB(t), nil)
	calls := 0
	s.readJSON = func(context.Context, string, string, url.Values, []byte, any) error {
		calls++
		return ErrFutuRateLimited
	}
	result, err := s.Refresh(context.Background())
	if !errors.Is(err, ErrUSFuturesRateLimited) || calls != 1 || len(result.Warnings) != 2 {
		t.Fatalf("result=%+v err=%v calls=%d", result, err, calls)
	}
}
func TestUSFuturesPagingAndRejectIncompleteHistory(t *testing.T) {
	s := NewUSFuturesService(testDB(t), nil)
	start, _ := time.Parse(time.DateOnly, "2026-09-01")
	end := start.AddDate(0, 0, 10)
	cursor := start.AddDate(0, 0, 4).UnixMilli()
	reference := true
	pages := 0
	s.readJSON = func(_ context.Context, _, path string, q url.Values, _ []byte, out any) error {
		if strings.HasSuffix(path, "reference-future") {
			return futureDecode(out, map[string]any{"reference_list": []any{map[string]any{"code": "US.ESmain", "stock_type": "FUTURE", "future_main_contract": reference, "future_valid": true}}})
		}
		pages++
		if pages == 1 {
			return futureDecode(out, map[string]any{"next_time": cursor, "kline_list": []any{futureBar(20260910)}})
		}
		if q.Get("end") != fmt.Sprint(cursor) {
			t.Fatal("cursor not passed through", q)
		}
		return futureDecode(out, map[string]any{"kline_list": []any{futureBar(20260903)}})
	}
	rows, err := s.fetchHistory(context.Background(), usFuturesDefinitions[0], start, end, end)
	if err != nil || len(rows) != 2 || rows[0].TradeDate != "2026-09-03" {
		t.Fatalf("%+v %v", rows, err)
	}
	reference = false
	rows, err = s.fetchHistory(context.Background(), usFuturesDefinitions[0], start, end, end)
	if err == nil || len(rows) != 0 || pages != 2 {
		t.Fatal("non-main contract admitted")
	}
	reference = true
	pages = 0
	s.readJSON = func(_ context.Context, _, path string, _ url.Values, _ []byte, out any) error {
		if strings.HasSuffix(path, "reference-future") {
			return futureDecode(out, map[string]any{"reference_list": []any{map[string]any{"code": "US.ESmain", "stock_type": "FUTURE", "future_main_contract": true, "future_valid": true}}})
		}
		bar := futureBar(20260903)
		delete(bar, "close")
		return futureDecode(out, map[string]any{"kline_list": []any{bar}})
	}
	if rows, err = s.fetchHistory(context.Background(), usFuturesDefinitions[0], start, end, end); err == nil || len(rows) != 0 {
		t.Fatal("incomplete response accepted")
	}
}
func TestFutuFuturesModuleAndSharedBudget(t *testing.T) {
	management := apiManagementTestService(t)
	seedFutuTestToken(t, management)
	ctx := context.Background()
	calls := 0
	serverTransport := aiRoundTripper(func(r *http.Request) (*http.Response, error) {
		calls++
		return nil, errors.New("unexpected external dispatch")
	})
	management.Futu.dataClient = &http.Client{Transport: serverTransport}
	management.db.Model(&discovery.APIModulePolicy{}).Where("key = ?", "futu_market").Update("enabled", false)
	endpoint := "/api/v1.0/quote/{symbol}/history-kline"
	if err := discovery.CheckAPIModuleEndpoint(discovery.WithFutuFutures(ctx), management.db, "futu", endpoint); err != nil {
		t.Fatal("stock switch blocked futures", err)
	}
	management.db.Model(&discovery.APIModulePolicy{}).Where("key = ?", "futu_futures").Update("enabled", false)
	var out any
	if err := management.Futu.ReadJSON(ctx, http.MethodGet, "/api/v1.0/quote/US.ESmain/history-kline", nil, nil, &out); !errors.Is(err, discovery.ErrAPIDisabled) || calls != 0 {
		t.Fatal("disabled futures dispatched", err, calls)
	}
	management.db.Model(&discovery.APIModulePolicy{}).Where("key = ?", "futu_market").Update("enabled", true)
	if err := discovery.CheckAPIModuleEndpoint(ctx, management.db, "futu", endpoint); err != nil {
		t.Fatal("futures switch blocked stock", err)
	}
	for _, path := range []string{"/api/v1.0/quote/US.ESmain/reference-future", "/api/v1.0/quote/US.ESmain/history-kline"} {
		if !futuReadPathAllowed(path) {
			t.Fatal("valid main rejected", path)
		}
	}
	if futuReadPathAllowed("/api/v1.0/quote/US.UNKNOWNmain/history-kline") {
		t.Fatal("arbitrary main admitted")
	}
}

func TestUSFuturesOfficialTransportRespectsBudgetAndPause(t *testing.T) {
	management := apiManagementTestService(t)
	seedFutuTestToken(t, management)
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != "Bearer fake-access-only" {
			t.Error("missing shared authorization")
		}
		json.NewEncoder(w).Encode(map[string]any{"ret_code": 0, "data": map[string]any{"reference_list": []any{map[string]any{"code": "US.ESmain", "stock_type": "FUTURE", "future_main_contract": true, "future_valid": true}}}})
	}))
	defer server.Close()
	management.Futu.host = server.URL
	management.db.Model(&discovery.APIProviderPolicy{}).Where("provider = ?", "futu").Updates(map[string]any{"daily_budget": 1, "daily_used": 0})
	s := NewUSFuturesService(management.main, management.Futu)
	result, err := s.Refresh(context.Background())
	if err == nil || calls != 1 || result.SymbolsUpdated != 0 || !strings.Contains(err.Error(), "budget") {
		t.Fatalf("budget result=%+v err=%v calls=%d", result, err, calls)
	}
	var records []discovery.APICallRecord
	management.db.Find(&records)
	if len(records) != 1 || records[0].Provider != "futu" || records[0].Endpoint != "/api/v1.0/quote/{symbol}/reference-future" {
		t.Fatal("missing official-call audit", records)
	}
	management.db.Model(&discovery.APIProviderPolicy{}).Where("provider = ?", "futu").Update("paused", true)
	_, err = s.Refresh(context.Background())
	if err == nil || calls != 1 || !strings.Contains(err.Error(), "暂停") {
		t.Fatal("pause dispatched", err, calls)
	}
}
