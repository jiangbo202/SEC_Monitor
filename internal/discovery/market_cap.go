package discovery

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
)

const (
	marketCapMicrosPerUSD = int64(1_000_000)
	MinimumSmallCapUSD    = int64(30_000_000)
	MaximumSmallCapUSD    = int64(1_000_000_000)
	MaximumPriceAgeDays   = 3
)

var (
	ErrPriceStale         = errors.New("price is older than three trading days")
	ErrPriceNotTradingDay = errors.New("price date is not a trading day")
	ErrPriceFuture        = errors.New("price date is in the future")
	ErrPriceAdjusted      = errors.New("adjusted price is not accepted")
	ErrPriceCurrency      = errors.New("price currency is not USD")
	ErrPriceInvalid       = errors.New("price is invalid")
	ErrPriceZero          = errors.New("price is zero")
	ErrPriceNegative      = errors.New("price is negative")
)

// ComputeMarketCapUSD uses checked integer arithmetic. Any fractional dollar
// is truncated toward zero (equivalent to flooring because inputs are positive).
func ComputeMarketCapUSD(closeMicros, shares int64) (int64, error) {
	if closeMicros <= 0 || shares <= 0 {
		return 0, errors.New("price and shares must be positive")
	}
	if closeMicros > math.MaxInt64/shares {
		return 0, errors.New("market cap multiplication overflows int64")
	}
	return closeMicros * shares / marketCapMicrosPerUSD, nil
}

// ComputeSmallCapQualification is the only recommended Task 9 entry point for
// computing and qualifying market cap. It compares the exact micro-dollar
// product before flooring the returned display value to whole USD.
func ComputeSmallCapQualification(closeMicros, shares int64) (marketCapUSD int64, qualified bool, err error) {
	if closeMicros <= 0 || shares <= 0 {
		return 0, false, errors.New("price and shares must be positive")
	}
	if closeMicros > math.MaxInt64/shares {
		return 0, false, errors.New("market cap multiplication overflows int64")
	}
	product := closeMicros * shares
	marketCapUSD = product / marketCapMicrosPerUSD
	qualified = product >= MinimumSmallCapUSD*marketCapMicrosPerUSD && product <= MaximumSmallCapUSD*marketCapMicrosPerUSD
	return marketCapUSD, qualified, nil
}

// ValidateMarketCapPrice returns the number of NYSE trading days strictly
// after the price date through the New York civil date containing asOf.
func ValidateMarketCapPrice(ctx context.Context, calendar MarketCalendar, price PriceRecord, asOf time.Time) (int, error) {
	if calendar == nil {
		return 0, errors.New("market calendar is required")
	}
	if asOf.IsZero() || price.TradeDate.IsZero() {
		return 0, errors.New("price date and as-of time are required")
	}
	if price.CloseMicros == 0 {
		return 0, ErrPriceZero
	}
	if price.CloseMicros < 0 {
		return 0, ErrPriceNegative
	}
	if price.Adjusted {
		return 0, ErrPriceAdjusted
	}
	if strings.TrimSpace(price.Currency) != "USD" {
		return 0, fmt.Errorf("%w: %q", ErrPriceCurrency, price.Currency)
	}
	newYork, err := time.LoadLocation("America/New_York")
	if err != nil {
		return 0, fmt.Errorf("load America/New_York: %w", err)
	}
	priceDate := price.TradeDate.In(newYork).Format(time.DateOnly)
	asOfDate := asOf.In(newYork).Format(time.DateOnly)
	if priceDate > asOfDate {
		return 0, ErrPriceFuture
	}
	// Validate the as-of calendar year even when the date itself is a weekend.
	// A partial calendar must never be mistaken for a stale-price decision.
	if _, err := calendar.IsTradingDate(ctx, asOfDate); err != nil {
		return 0, err
	}
	trading, err := calendar.IsTradingDate(ctx, priceDate)
	if err != nil {
		return 0, err
	}
	if !trading {
		return 0, ErrPriceNotTradingDay
	}
	cursor, err := time.ParseInLocation(time.DateOnly, priceDate, newYork)
	if err != nil {
		return 0, err
	}
	end, err := time.ParseInLocation(time.DateOnly, asOfDate, newYork)
	if err != nil {
		return 0, err
	}
	age := 0
	for cursor.Before(end) {
		cursor = cursor.AddDate(0, 0, 1)
		open, calendarErr := calendar.IsTradingDate(ctx, cursor.Format(time.DateOnly))
		if calendarErr != nil {
			return age, calendarErr
		}
		if open {
			age++
			if age > MaximumPriceAgeDays {
				return age, ErrPriceStale
			}
		}
	}
	return age, nil
}
