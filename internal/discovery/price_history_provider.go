package discovery

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

func normalizeHistoryWindow(effectiveDate string, lookbackDays int) (time.Time, time.Time, error) {
	end, err := parseNYCivilDate(effectiveDate)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	if lookbackDays < minimumTechnicalHistoryLookbackDays {
		lookbackDays = minimumTechnicalHistoryLookbackDays
	}
	return end.AddDate(0, 0, -lookbackDays), end, nil
}

func (p *PriceProviderChain) LoadHistory(ctx context.Context, expected []Listing, effectiveDate string, lookbackDays int) ([]PriceRecord, error) {
	remaining := append([]Listing(nil), expected...)
	covered := map[string]struct{}{}
	dayCounts := map[string]map[string]struct{}{}
	records := []PriceRecord{}
	var lastErr error
	for _, child := range p.providers {
		if len(remaining) == 0 {
			break
		}
		history, ok := child.(HistoricalPriceProvider)
		if !ok {
			lastErr = fmt.Errorf("price provider %s does not support technical history", providerName(child))
			continue
		}
		rows, err := history.LoadHistory(ctx, remaining, effectiveDate, lookbackDays)
		if err != nil {
			lastErr = err
			continue
		}
		for _, row := range rows {
			symbol := strings.ToUpper(strings.TrimSpace(row.Symbol))
			if symbol == "" {
				continue
			}
			records = append(records, row)
			if dayCounts[symbol] == nil {
				dayCounts[symbol] = map[string]struct{}{}
			}
			dayCounts[symbol][row.TradeDate.Format(time.DateOnly)] = struct{}{}
			if len(dayCounts[symbol]) >= technicalHistorySamplesRequired {
				covered[symbol] = struct{}{}
			}
		}
		remaining = missingListings(expected, covered)
	}
	if len(records) == 0 && lastErr != nil {
		return nil, lastErr
	}
	if len(records) == 0 {
		return nil, errors.New("price provider chain returned no usable technical history")
	}
	return records, nil
}

var _ HistoricalPriceProvider = (*PriceProviderChain)(nil)
