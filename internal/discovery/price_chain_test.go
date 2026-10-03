package discovery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"reflect"
	"testing"
	"time"
)

type fakeChainPriceProvider struct {
	name          string
	records       []PriceRecord
	result        ProviderResult
	err           error
	expected      []Listing
	requestedDate string
}

func (f *fakeChainPriceProvider) ProviderName() string { return f.name }

func (f *fakeChainPriceProvider) Load(ctx context.Context, expected []Listing) ([]PriceRecord, ProviderResult, error) {
	return f.LoadForDate(ctx, expected, "")
}

func (f *fakeChainPriceProvider) LoadForDate(_ context.Context, expected []Listing, effectiveDate string) ([]PriceRecord, ProviderResult, error) {
	f.expected = append([]Listing(nil), expected...)
	f.requestedDate = effectiveDate
	return f.records, f.result, f.err
}

func TestPriceProviderChainFillsMissingSymbolsInOrder(t *testing.T) {
	ny, _ := time.LoadLocation("America/New_York")
	day := time.Date(2026, 6, 30, 0, 0, 0, 0, ny)
	expected := []Listing{
		{Ticker: "AAA"},
		{Ticker: "BBB"},
		{Ticker: "CCC"},
	}
	firstHash := sha256.Sum256([]byte("first"))
	secondHash := sha256.Sum256([]byte("second"))
	first := &fakeChainPriceProvider{
		name:    "longbridge",
		records: []PriceRecord{{Symbol: "AAA", Source: "longbridge", TradeDate: day, CloseMicros: 1_000_000, Volume: 10, Currency: "USD"}},
		result:  ProviderResult{Provider: "longbridge", SourceVersion: "longbridge:v1", SHA256: hex.EncodeToString(firstHash[:]), EffectiveDate: day, Records: 1, Expected: 3, CoveragePct: 33.3333, Timely: true},
	}
	second := &fakeChainPriceProvider{
		name: "backup",
		records: []PriceRecord{
			{Symbol: "BBB", Source: "backup", TradeDate: day, CloseMicros: 2_000_000, Volume: 20, Currency: "USD"},
			{Symbol: "CCC", Source: "backup", TradeDate: day, CloseMicros: 3_000_000, Volume: 30, Currency: "USD"},
		},
		result: ProviderResult{Provider: "backup", SourceVersion: "backup:v1", SHA256: hex.EncodeToString(secondHash[:]), EffectiveDate: day, Records: 2, Expected: 2, CoveragePct: 100, Timely: true},
	}
	var diagnostics []PriceProviderChainDiagnostic

	chain, err := NewPriceProviderChain(PriceProviderChainOptions{
		Providers: []PriceProvider{first, second},
		Calendar:  &stubMarketCalendar{},
		Diagnostics: func(event PriceProviderChainDiagnostic) {
			diagnostics = append(diagnostics, event)
		},
	})
	if err != nil {
		t.Fatalf("NewPriceProviderChain: %v", err)
	}
	records, result, err := chain.LoadForDate(context.Background(), expected, "2026-06-30")
	if err != nil {
		t.Fatalf("LoadForDate: %v", err)
	}

	if got := tickersFromListings(second.expected); !reflect.DeepEqual(got, []string{"BBB", "CCC"}) {
		t.Fatalf("second provider expected = %#v, want missing BBB/CCC", got)
	}
	if len(records) != 3 || result.Provider != "chain" || result.Expected != 3 || result.Records != 3 || result.CoveragePct != 100 {
		t.Fatalf("records=%#v result=%#v", records, result)
	}
	if !result.FallbackUsed || len(result.Attempts) != 2 || result.Attempts[0].Status != "partial" || result.Attempts[0].Remaining != 2 || result.Attempts[1].Status != "success" || result.Attempts[1].Remaining != 0 {
		t.Fatalf("provider attempts = %#v", result.Attempts)
	}
	if got := []string{records[0].Source, records[1].Source, records[2].Source}; !reflect.DeepEqual(got, []string{"longbridge", "backup", "backup"}) {
		t.Fatalf("record sources = %#v", got)
	}
	if got := chain.AllowedRecordSources(); !reflect.DeepEqual(got, []string{"longbridge", "backup"}) {
		t.Fatalf("allowed sources = %#v", got)
	}
	if got := diagnosticEvents(diagnostics); !reflect.DeepEqual(got, []string{"longbridge:start", "longbridge:success", "backup:start", "backup:success"}) {
		t.Fatalf("diagnostic events = %#v", got)
	}
	if diagnostics[1].Records != 1 || diagnostics[1].Remaining != 2 || diagnostics[3].Records != 2 || diagnostics[3].Remaining != 0 {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}
}

func TestPriceProviderChainReportsChildError(t *testing.T) {
	ny, _ := time.LoadLocation("America/New_York")
	day := time.Date(2026, 6, 30, 0, 0, 0, 0, ny)
	expected := []Listing{{Ticker: "AAA"}, {Ticker: "BBB"}}
	firstHash := sha256.Sum256([]byte("first"))
	first := &fakeChainPriceProvider{
		name:    "longbridge",
		records: []PriceRecord{{Symbol: "AAA", Source: "longbridge", TradeDate: day, CloseMicros: 1_000_000, Volume: 10, Currency: "USD"}},
		result:  ProviderResult{Provider: "longbridge", SourceVersion: "longbridge:v1", SHA256: hex.EncodeToString(firstHash[:]), EffectiveDate: day, Records: 1, Expected: 2, CoveragePct: 50, Timely: true},
	}
	second := &fakeChainPriceProvider{name: "futu", err: errors.New("futu rate limited")}
	var diagnostics []PriceProviderChainDiagnostic

	chain, err := NewPriceProviderChain(PriceProviderChainOptions{
		Providers: []PriceProvider{first, second},
		Calendar:  &stubMarketCalendar{},
		Diagnostics: func(event PriceProviderChainDiagnostic) {
			diagnostics = append(diagnostics, event)
		},
	})
	if err != nil {
		t.Fatalf("NewPriceProviderChain: %v", err)
	}
	records, result, err := chain.LoadForDate(context.Background(), expected, "2026-06-30")
	if err != nil {
		t.Fatalf("LoadForDate: %v", err)
	}

	if len(records) != 1 || result.Records != 1 || result.Expected != 2 {
		t.Fatalf("records=%#v result=%#v", records, result)
	}
	if got := diagnosticEvents(diagnostics); !reflect.DeepEqual(got, []string{"longbridge:start", "longbridge:success", "futu:start", "futu:error"}) {
		t.Fatalf("diagnostic events = %#v", got)
	}
	if diagnostics[3].Error != "futu rate limited" || diagnostics[3].Remaining != 1 {
		t.Fatalf("diagnostic error = %#v", diagnostics[3])
	}
	if !result.FallbackUsed || len(result.Attempts) != 2 || result.Attempts[1].Status != "failed" || result.Attempts[1].ErrorMessage != "futu rate limited" {
		t.Fatalf("provider attempts = %#v", result.Attempts)
	}
}

func TestPriceProviderChainUsesPreviousTradingDayOnlyAfterFreshProviders(t *testing.T) {
	ny, _ := time.LoadLocation("America/New_York")
	previous := time.Date(2026, 6, 29, 0, 0, 0, 0, ny)
	effective := time.Date(2026, 6, 30, 0, 0, 0, 0, ny)
	expected := []Listing{{Ticker: "FRESH"}, {Ticker: "FALLBACK"}}
	firstHash := sha256.Sum256([]byte("first-fallback"))
	secondHash := sha256.Sum256([]byte("second-fallback"))
	first := &fakeChainPriceProvider{
		name: "futu",
		records: []PriceRecord{
			{Symbol: "FRESH", Source: "futu", TradeDate: previous, CloseMicros: 1_000_000, Volume: 10, Currency: "USD"},
			{Symbol: "FALLBACK", Source: "futu", TradeDate: previous, CloseMicros: 2_000_000, Volume: 20, Currency: "USD"},
		},
		result: ProviderResult{Provider: "futu", SourceVersion: "futu:v1", SHA256: hex.EncodeToString(firstHash[:]), EffectiveDate: effective, Records: 2, Expected: 2, CoveragePct: 100, Timely: true},
	}
	second := &fakeChainPriceProvider{
		name:    "backup",
		records: []PriceRecord{{Symbol: "FRESH", Source: "backup", TradeDate: effective, CloseMicros: 1_100_000, Volume: 11, Currency: "USD"}},
		result:  ProviderResult{Provider: "backup", SourceVersion: "backup:v1", SHA256: hex.EncodeToString(secondHash[:]), EffectiveDate: effective, Records: 1, Expected: 2, CoveragePct: 50, Timely: true},
	}
	chain, err := NewPriceProviderChain(PriceProviderChainOptions{Providers: []PriceProvider{first, second}, Calendar: &stubMarketCalendar{}})
	if err != nil {
		t.Fatal(err)
	}

	records, result, err := chain.LoadForDate(context.Background(), expected, "2026-06-30")
	if err != nil {
		t.Fatalf("LoadForDate() error = %v", err)
	}
	if got := tickersFromListings(second.expected); !reflect.DeepEqual(got, []string{"FALLBACK", "FRESH"}) {
		t.Fatalf("second provider must retry both non-fresh symbols, got %#v", got)
	}
	if len(records) != 2 || result.CoveragePct != 100 {
		t.Fatalf("records=%#v result=%#v", records, result)
	}
	bySymbol := map[string]PriceRecord{}
	for _, record := range records {
		bySymbol[record.Symbol] = record
	}
	if fresh := bySymbol["FRESH"]; fresh.Source != "backup" || !fresh.TradeDate.Equal(effective) {
		t.Fatalf("fresh replacement = %#v", fresh)
	}
	if fallback := bySymbol["FALLBACK"]; fallback.Source != "futu" || !fallback.TradeDate.Equal(previous) {
		t.Fatalf("fallback preservation = %#v", fallback)
	}
}

func diagnosticEvents(events []PriceProviderChainDiagnostic) []string {
	out := make([]string, 0, len(events))
	for _, event := range events {
		out = append(out, event.Provider+":"+event.Event)
	}
	return out
}
