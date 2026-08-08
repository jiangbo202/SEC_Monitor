package discovery

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

type fakeLongbridgeClient struct {
	quotes       map[string]longbridgeQuote
	history      map[string][]longbridgeCandle
	quoteBatches [][]string
	quoteErr     error
	closed       bool
}

type nilLongbridgeDecimal struct{}

func (*nilLongbridgeDecimal) String() string { return "unexpected" }

func TestDecimalTextAcceptsTypedNilDecimal(t *testing.T) {
	var value *nilLongbridgeDecimal
	if got := decimalText(value); got != "" {
		t.Fatalf("decimalText(typed nil) = %q, want empty", got)
	}
}

func (f *fakeLongbridgeClient) Quote(_ context.Context, symbols []string) ([]longbridgeQuote, error) {
	f.quoteBatches = append(f.quoteBatches, append([]string(nil), symbols...))
	if f.quoteErr != nil {
		return nil, f.quoteErr
	}
	result := make([]longbridgeQuote, 0, len(symbols))
	for _, symbol := range symbols {
		if quote, ok := f.quotes[symbol]; ok {
			result = append(result, quote)
		}
	}
	return result, nil
}

func TestProbeLongbridgeQuoteReportsQuoteStatus(t *testing.T) {
	tests := []struct {
		name          string
		appKey        string
		client        *fakeLongbridgeClient
		newClientErr  error
		wantStatus    string
		wantErrorKind string
	}{
		{
			name:       "success",
			appKey:     "key",
			client:     &fakeLongbridgeClient{quotes: map[string]longbridgeQuote{longbridgeProbeSymbol: {Symbol: longbridgeProbeSymbol, LastDone: "210.42", Timestamp: 1_784_000_000, Volume: 123}}},
			wantStatus: "ok",
		},
		{name: "missing credentials", wantStatus: "failed", wantErrorKind: "configuration"},
		{
			name:          "connection error",
			appKey:        "key",
			newClientErr:  errors.New("EOF"),
			wantStatus:    "failed",
			wantErrorKind: "connect",
		},
		{
			name:          "quote timeout",
			appKey:        "key",
			client:        &fakeLongbridgeClient{quoteErr: context.DeadlineExceeded},
			wantStatus:    "failed",
			wantErrorKind: "timeout",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := probeLongbridgeQuote(context.Background(), tt.appKey, "secret", "token", func(_, _, _ string) (longbridgeQuoteClient, error) {
				if tt.newClientErr != nil {
					return nil, tt.newClientErr
				}
				return tt.client, nil
			})
			if result.Status != tt.wantStatus || result.ErrorKind != tt.wantErrorKind {
				t.Fatalf("result=%#v", result)
			}
			if tt.wantStatus == "ok" && (!result.QuoteReceived || result.LastDone != "210.42" || !tt.client.closed) {
				t.Fatalf("successful probe=%#v closed=%t", result, tt.client.closed)
			}
		})
	}
}

func (f *fakeLongbridgeClient) HistoryDaily(_ context.Context, symbol string, _, _ time.Time) ([]longbridgeCandle, error) {
	return append([]longbridgeCandle(nil), f.history[symbol]...), nil
}

func (f *fakeLongbridgeClient) Close() error {
	f.closed = true
	return nil
}

func TestLongbridgePriceProviderBatchesQuotesAndPersistsDailyVolume(t *testing.T) {
	ny := mustNY(t)
	target := time.Date(2026, 7, 17, 0, 0, 0, 0, ny)
	quoteAt := time.Date(2026, 7, 17, 16, 1, 0, 0, ny).Unix()
	expected := make([]Listing, 0, longbridgeQuoteBatchSize+1)
	quotes := make(map[string]longbridgeQuote, longbridgeQuoteBatchSize+1)
	for index := 0; index <= longbridgeQuoteBatchSize; index++ {
		ticker := fmt.Sprintf("T%03d", index)
		expected = append(expected, Listing{Ticker: ticker, ProviderTicker: ticker})
		quotes[ticker+".US"] = longbridgeQuote{Symbol: ticker + ".US", Open: "10.00", High: "11.00", Low: "9.00", LastDone: "10.50", Timestamp: quoteAt, Volume: int64(1000 + index)}
	}
	client := &fakeLongbridgeClient{quotes: quotes}
	provider, err := NewLongbridgePriceProvider(LongbridgePriceProviderOptions{
		AppKey: "key", AppSecret: "secret", AccessToken: "token", Calendar: &stubMarketCalendar{},
		Now:       func() time.Time { return time.Date(2026, 7, 18, 9, 0, 0, 0, ny) },
		NewClient: func(_, _, _ string) (longbridgeQuoteClient, error) { return client, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	records, result, err := provider.LoadForDate(context.Background(), expected, target.Format(time.DateOnly))
	if err != nil {
		t.Fatal(err)
	}
	if len(client.quoteBatches) != 2 || len(client.quoteBatches[0]) != longbridgeQuoteBatchSize || len(client.quoteBatches[1]) != 1 {
		t.Fatalf("quote batches = %#v, want 500 + 1", client.quoteBatches)
	}
	if len(records) != longbridgeQuoteBatchSize+1 || records[0].Volume <= 0 || records[0].TradeDate.Format(time.DateOnly) != "2026-07-17" {
		t.Fatalf("records = %#v", records[:min(1, len(records))])
	}
	if result.CoveragePct != 100 || result.Records != len(expected) || !client.closed {
		t.Fatalf("result=%#v closed=%t", result, client.closed)
	}
}

func TestLongbridgePriceProviderHistoryRetainsVolume(t *testing.T) {
	ny := mustNY(t)
	date := time.Date(2026, 7, 17, 16, 1, 0, 0, ny).Unix()
	client := &fakeLongbridgeClient{history: map[string][]longbridgeCandle{
		"ACME.US": {{Open: "10", High: "11", Low: "9", Close: "10.5", Timestamp: date, Volume: 456789}},
	}}
	provider, err := NewLongbridgePriceProvider(LongbridgePriceProviderOptions{
		AppKey: "key", AppSecret: "secret", AccessToken: "token", Calendar: &stubMarketCalendar{},
		NewClient: func(_, _, _ string) (longbridgeQuoteClient, error) { return client, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	records, err := provider.LoadHistory(context.Background(), []Listing{{Ticker: "ACME", ProviderTicker: "ACME"}}, "2026-07-18", 21)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Volume != 456789 || records[0].CloseMicros != 10_500_000 || records[0].Source != "longbridge" {
		t.Fatalf("records=%#v", records)
	}
}
