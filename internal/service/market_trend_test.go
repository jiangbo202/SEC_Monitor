package service

import (
	"context"
	"fmt"
	"testing"
	"time"

	"sec_monitor/internal/config"
	"sec_monitor/internal/model"
)

type fakeMarketTrendClient struct {
	candles map[string][]marketTrendCandle
	calls   []string
	closed  bool
}

type fakeMarketTemperatureClient struct{ records []marketTemperatureRecord }

func (f *fakeMarketTemperatureClient) Current(_ context.Context, _ string) (marketTemperatureRecord, error) {
	return f.records[len(f.records)-1], nil
}
func (f *fakeMarketTemperatureClient) History(_ context.Context, _ string, _, _ time.Time) ([]marketTemperatureRecord, error) {
	return f.records, nil
}

func (f *fakeMarketTrendClient) HistoryDaily(_ context.Context, symbol string, _, _ time.Time) ([]marketTrendCandle, error) {
	f.calls = append(f.calls, symbol)
	return f.candles[symbol], nil
}

func (f *fakeMarketTrendClient) Close() error {
	f.closed = true
	return nil
}

func TestMarketTrendRefreshCachesLongbridgeDailyBars(t *testing.T) {
	db := testDB(t)
	if err := db.AutoMigrate(&model.MarketTrendDaily{}, &model.MarketTemperatureDaily{}); err != nil {
		t.Fatalf("migrate market trend: %v", err)
	}
	now := time.Date(2026, time.August, 9, 12, 0, 0, 0, time.UTC)
	candles := make(map[string][]marketTrendCandle, len(marketTrendDefinitions))
	for _, definition := range marketTrendDefinitions {
		bars := make([]marketTrendCandle, 0, 22)
		for index := 0; index < 22; index++ {
			value := 100 + index
			bars = append(bars, marketTrendCandle{
				Open: fmt.Sprintf("%d", value-1), High: fmt.Sprintf("%d", value+1), Low: fmt.Sprintf("%d", value-2), Close: fmt.Sprintf("%d", value),
				Timestamp: now.AddDate(0, 0, -(21 - index)).Unix(), Volume: int64(1_000 + index),
			})
		}
		candles[definition.Symbol] = bars
	}
	client := &fakeMarketTrendClient{candles: candles}
	service := NewMarketTrendService(db, nil, config.DiscoveryConfig{LongbridgeAppKey: "key", LongbridgeAppSecret: "secret", LongbridgeAccessToken: "token"})
	service.now = func() time.Time { return now }
	service.newClient = func(_, _, _ string) (marketTrendLongbridgeClient, error) { return client, nil }
	service.newTemperatureClient = func(_, _, _ string) (marketTemperatureLongbridgeClient, error) {
		return &fakeMarketTemperatureClient{records: []marketTemperatureRecord{{Timestamp: now.AddDate(0, 0, -1).Unix(), Temperature: 62, Valuation: 70, Sentiment: 55}}}, nil
	}

	result, err := service.Refresh(context.Background())
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if result.SymbolsUpdated != len(marketTrendDefinitions) || result.BarsSaved != len(marketTrendDefinitions)*22 || result.TemperatureSaved != 1 || len(result.Warnings) != 0 {
		t.Fatalf("refresh result = %+v", result)
	}
	if len(client.calls) != len(marketTrendDefinitions) || !client.closed {
		t.Fatalf("client calls=%d closed=%v", len(client.calls), client.closed)
	}

	response, err := service.List(context.Background(), 120)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(response.Market) != 5 || len(response.Sectors) != 11 || response.Temperature == nil || response.LastFetched == nil {
		t.Fatalf("trend response = %+v", response)
	}
	spx := response.Market[0]
	if spx.Symbol != ".SPX.US" || spx.Change1DPct == nil || *spx.Change1DPct < 0.82 || *spx.Change1DPct > 0.84 || spx.Change20DPct == nil || len(spx.History) != 22 {
		t.Fatalf("SPX series = %+v", spx)
	}
}
