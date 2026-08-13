package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"sec_monitor/internal/config"
	"sec_monitor/internal/model"
)

type usFuturesRoundTripper struct{ body string }

func (r usFuturesRoundTripper) RoundTrip(_ *http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(r.body))}, nil
}

type usFuturesRateLimitRoundTripper struct{ calls int }

func (r *usFuturesRateLimitRoundTripper) RoundTrip(_ *http.Request) (*http.Response, error) {
	r.calls++
	return &http.Response{StatusCode: http.StatusTooManyRequests, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("rate limited"))}, nil
}

func TestUSFuturesRefreshCachesContinuousContracts(t *testing.T) {
	db := testDB(t)
	if err := db.AutoMigrate(&model.MarketTrendDaily{}); err != nil {
		t.Fatalf("migrate market trend: %v", err)
	}
	now := time.Date(2026, time.August, 9, 12, 0, 0, 0, time.UTC)
	payload := `{"chart":{"result":[{"timestamp":[1786147200,1786233600],"indicators":{"quote":[{"open":[100,101],"high":[102,103],"low":[99,100],"close":[101,102],"volume":[1000,1100]}]}}]}}`
	service := NewUSFuturesService(db, nil, config.DiscoveryConfig{YahooBaseURL: "https://futures.test"})
	service.now = func() time.Time { return now }
	service.client = &http.Client{Transport: usFuturesRoundTripper{body: payload}}

	result, err := service.Refresh(context.Background())
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if result.SymbolsUpdated != len(usFuturesDefinitions) || result.BarsSaved != len(usFuturesDefinitions)*2 || len(result.Warnings) != 0 {
		t.Fatalf("refresh result = %+v", result)
	}
	response, err := service.List(context.Background(), 120)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(response.Futures) != len(usFuturesDefinitions) || response.LastFetched == nil || response.Futures[0].Symbol != "ES=F" || response.Futures[0].Change1DPct == nil {
		t.Fatalf("futures response = %+v", response)
	}
}

func TestUSFuturesRefreshStopsAfterFirstRateLimit(t *testing.T) {
	db := testDB(t)
	service := NewUSFuturesService(db, nil, config.DiscoveryConfig{YahooBaseURL: "https://futures.test"})
	roundTripper := &usFuturesRateLimitRoundTripper{}
	service.client = &http.Client{Transport: roundTripper}
	result, err := service.Refresh(context.Background())
	if !errors.Is(err, ErrUSFuturesRateLimited) || roundTripper.calls != 1 {
		t.Fatalf("result=%+v err=%v calls=%d", result, err, roundTripper.calls)
	}
	if len(result.Warnings) != 2 || !strings.Contains(result.Warnings[1], "停止本轮") {
		t.Fatalf("warnings=%+v", result.Warnings)
	}
}
