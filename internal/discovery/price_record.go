package discovery

import "time"

func latestPriceRecordDate(records []PriceRecord) time.Time {
	var latest time.Time
	for _, record := range records {
		if latest.IsZero() || record.TradeDate.After(latest) {
			latest = record.TradeDate
		}
	}
	return latest
}
