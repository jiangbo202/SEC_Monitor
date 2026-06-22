package discovery

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"
)

func TestComputeMarketCap(t *testing.T) {
	tests := []struct {
		name                string
		price, shares, want int64
		wantErr             bool
	}{
		{name: "whole dollars", price: 2_500_000, shares: 12_000_000, want: 30_000_000},
		{name: "fractional dollar floors", price: 1_500_001, shares: 1, want: 1},
		{name: "zero price", shares: 1, wantErr: true}, {name: "zero shares", price: 1, wantErr: true},
		{name: "negative price", price: -1, shares: 1, wantErr: true}, {name: "negative shares", price: 1, shares: -1, wantErr: true},
		{name: "overflow", price: math.MaxInt64, shares: 2, wantErr: true},
		{name: "maximum safe", price: math.MaxInt64, shares: 1, want: math.MaxInt64 / 1_000_000},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := ComputeMarketCapUSD(test.price, test.shares)
			if (err != nil) != test.wantErr || got != test.want {
				t.Fatalf("got (%d,%v), want (%d,err=%t)", got, err, test.want, test.wantErr)
			}
		})
	}
}

func TestComputeSmallCapQualificationUsesUnroundedProduct(t *testing.T) {
	tests := []struct {
		name                   string
		closeMicros, shares    int64
		wantCap                int64
		wantQualified, wantErr bool
	}{
		{name: "exact lower bound", closeMicros: 30_000_000_000_000, shares: 1, wantCap: 30_000_000, wantQualified: true},
		{name: "one micro-dollar below lower", closeMicros: 29_999_999_999_999, shares: 1, wantCap: 29_999_999},
		{name: "exact upper bound", closeMicros: 1_000_000_000_000_000, shares: 1, wantCap: 1_000_000_000, wantQualified: true},
		{name: "one micro-dollar above upper", closeMicros: 1_000_000_000_000_001, shares: 1, wantCap: 1_000_000_000},
		{name: "overflow", closeMicros: math.MaxInt64, shares: 2, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			capUSD, qualified, err := ComputeSmallCapQualification(test.closeMicros, test.shares)
			if capUSD != test.wantCap || qualified != test.wantQualified || (err != nil) != test.wantErr {
				t.Fatalf("got (%d,%t,%v), want (%d,%t,err=%t)", capUSD, qualified, err, test.wantCap, test.wantQualified, test.wantErr)
			}
		})
	}
}

func TestValidateMarketCapPriceUsesTradingDays(t *testing.T) {
	db := openMigratedTestDatabase(t)
	calendar, err := NewDatabaseMarketCalendar(db, DefaultNYSECalendarVersion)
	if err != nil {
		t.Fatal(err)
	}
	ny, _ := time.LoadLocation("America/New_York")
	asOf := time.Date(2026, 7, 8, 12, 0, 0, 0, ny)
	for _, test := range []struct {
		name    string
		date    time.Time
		wantAge int
		wantErr error
	}{
		{name: "same trading day", date: time.Date(2026, 7, 8, 0, 0, 0, 0, ny), wantAge: 0},
		{name: "three trading days across holiday weekend", date: time.Date(2026, 7, 2, 0, 0, 0, 0, ny), wantAge: 3},
		{name: "four trading days stale", date: time.Date(2026, 7, 1, 0, 0, 0, 0, ny), wantAge: 4, wantErr: ErrPriceStale},
		{name: "price date holiday", date: time.Date(2026, 7, 3, 0, 0, 0, 0, ny), wantErr: ErrPriceNotTradingDay},
		{name: "future", date: time.Date(2026, 7, 9, 0, 0, 0, 0, ny), wantErr: ErrPriceFuture},
	} {
		t.Run(test.name, func(t *testing.T) {
			record := PriceRecord{TradeDate: test.date, CloseMicros: 1, Currency: "USD"}
			age, err := ValidateMarketCapPrice(context.Background(), calendar, record, asOf)
			if age != test.wantAge || !errors.Is(err, test.wantErr) {
				t.Fatalf("got (%d,%v), want (%d,%v)", age, err, test.wantAge, test.wantErr)
			}
		})
	}
}

func TestValidateMarketCapPriceFailsClosed(t *testing.T) {
	calendar := &stubMarketCalendar{}
	asOf := time.Date(2026, 7, 8, 16, 0, 0, 0, time.FixedZone("EDT", -4*60*60))
	tests := []struct {
		name     string
		calendar MarketCalendar
		record   PriceRecord
		asOf     time.Time
	}{
		{name: "nil calendar", record: PriceRecord{TradeDate: asOf, CloseMicros: 1, Currency: "USD"}, asOf: asOf},
		{name: "zero as of", calendar: calendar, record: PriceRecord{TradeDate: asOf, CloseMicros: 1, Currency: "USD"}},
		{name: "zero price", calendar: calendar, record: PriceRecord{TradeDate: asOf, Currency: "USD"}, asOf: asOf},
		{name: "adjusted", calendar: calendar, record: PriceRecord{TradeDate: asOf, CloseMicros: 1, Currency: "USD", Adjusted: true}, asOf: asOf},
		{name: "non USD", calendar: calendar, record: PriceRecord{TradeDate: asOf, CloseMicros: 1, Currency: "CAD"}, asOf: asOf},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := ValidateMarketCapPrice(context.Background(), test.calendar, test.record, test.asOf); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestValidateMarketCapPricePreservesInstantCivilDateSemantics(t *testing.T) {
	calendar := &stubMarketCalendar{holidays: map[string]bool{"2026-07-03": true}}
	// UTC midnight July 7 is still July 6 in New York.
	asOf := time.Date(2026, 7, 7, 0, 0, 0, 0, time.UTC)
	price := PriceRecord{TradeDate: time.Date(2026, 7, 2, 12, 0, 0, 0, time.FixedZone("EDT", -4*60*60)), CloseMicros: 1, Currency: "USD"}
	age, err := ValidateMarketCapPrice(context.Background(), calendar, price, asOf)
	if err != nil || age != 1 {
		t.Fatalf("got (%d,%v), want (1,nil)", age, err)
	}
}

func TestValidateMarketCapPricePropagatesMissingCalendarYear(t *testing.T) {
	db := openMigratedTestDatabase(t)
	calendar, err := NewDatabaseMarketCalendar(db, DefaultNYSECalendarVersion)
	if err != nil {
		t.Fatal(err)
	}
	ny, _ := time.LoadLocation("America/New_York")
	price := PriceRecord{TradeDate: time.Date(2028, 12, 29, 0, 0, 0, 0, ny), CloseMicros: 1, Currency: "USD"}
	_, err = ValidateMarketCapPrice(context.Background(), calendar, price, time.Date(2029, 1, 2, 12, 0, 0, 0, ny))
	if !errors.Is(err, ErrCalendarYearMissing) {
		t.Fatalf("error = %v, want ErrCalendarYearMissing", err)
	}
}
