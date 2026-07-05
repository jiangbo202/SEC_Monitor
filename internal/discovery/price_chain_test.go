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
		name:    "tiingo",
		records: []PriceRecord{{Symbol: "AAA", Source: "tiingo", TradeDate: day, CloseMicros: 1_000_000, Volume: 10, Currency: "USD"}},
		result:  ProviderResult{Provider: "tiingo", SourceVersion: "tiingo:v1", SHA256: hex.EncodeToString(firstHash[:]), EffectiveDate: day, Records: 1, Expected: 3, CoveragePct: 33.3333, Timely: true},
	}
	second := &fakeChainPriceProvider{
		name: "yahoo",
		records: []PriceRecord{
			{Symbol: "BBB", Source: "yahoo", TradeDate: day, CloseMicros: 2_000_000, Volume: 20, Currency: "USD"},
			{Symbol: "CCC", Source: "yahoo", TradeDate: day, CloseMicros: 3_000_000, Volume: 30, Currency: "USD"},
		},
		result: ProviderResult{Provider: "yahoo", SourceVersion: "yahoo:v1", SHA256: hex.EncodeToString(secondHash[:]), EffectiveDate: day, Records: 2, Expected: 2, CoveragePct: 100, Timely: true},
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
	if got := []string{records[0].Source, records[1].Source, records[2].Source}; !reflect.DeepEqual(got, []string{"tiingo", "yahoo", "yahoo"}) {
		t.Fatalf("record sources = %#v", got)
	}
	if got := chain.AllowedRecordSources(); !reflect.DeepEqual(got, []string{"tiingo", "yahoo"}) {
		t.Fatalf("allowed sources = %#v", got)
	}
	if got := diagnosticEvents(diagnostics); !reflect.DeepEqual(got, []string{"tiingo:start", "tiingo:success", "yahoo:start", "yahoo:success"}) {
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
		name:    "tiingo",
		records: []PriceRecord{{Symbol: "AAA", Source: "tiingo", TradeDate: day, CloseMicros: 1_000_000, Volume: 10, Currency: "USD"}},
		result:  ProviderResult{Provider: "tiingo", SourceVersion: "tiingo:v1", SHA256: hex.EncodeToString(firstHash[:]), EffectiveDate: day, Records: 1, Expected: 2, CoveragePct: 50, Timely: true},
	}
	second := &fakeChainPriceProvider{name: "twelvedata", err: errors.New("twelve data rate limited")}
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
	if got := diagnosticEvents(diagnostics); !reflect.DeepEqual(got, []string{"tiingo:start", "tiingo:success", "twelvedata:start", "twelvedata:error"}) {
		t.Fatalf("diagnostic events = %#v", got)
	}
	if diagnostics[3].Error != "twelve data rate limited" || diagnostics[3].Remaining != 1 {
		t.Fatalf("diagnostic error = %#v", diagnostics[3])
	}
}

func diagnosticEvents(events []PriceProviderChainDiagnostic) []string {
	out := make([]string, 0, len(events))
	for _, event := range events {
		out = append(out, event.Provider+":"+event.Event)
	}
	return out
}
