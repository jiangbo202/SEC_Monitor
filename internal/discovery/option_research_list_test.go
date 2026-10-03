package discovery

import (
	"context"
	"testing"
	"time"
)

func TestOptionResearchListLatestPerTickerSortedBySyncTime(t *testing.T) {
	db := openMigratedTestDatabase(t)
	now := time.Date(2026, 10, 3, 10, 0, 0, 0, time.UTC)
	call, put, short := int64(0), int64(12), float64(3)
	rows := []OptionResearchSnapshot{
		{Provider: "longbridge", Ticker: "OLD", ObservedDate: "2026-10-01", FetchedAt: now.Add(-3 * time.Hour), Status: "partial", ShortRatioPct: &short},
		{Provider: "longbridge", Ticker: "NEW", ObservedDate: "2026-10-03", FetchedAt: now, Status: "available", CallVolume: &call, PutVolume: &put, ShortRatioPct: &short, AnomaliesJSON: `[{"kind":"test","label":"提示"}]`},
		{Provider: "longbridge", Ticker: "MID", ObservedDate: "2026-10-03", FetchedAt: now.Add(-time.Hour), Status: "unavailable"},
		// A more recently fetched backfill must not replace NEW's current day.
		{Provider: "longbridge", Ticker: "NEW", ObservedDate: "2026-09-30", FetchedAt: now.Add(time.Hour), Status: "unavailable"},
		{Provider: "futu", Ticker: "OTHER", ObservedDate: "2026-10-03", FetchedAt: now.Add(2 * time.Hour), Status: "available", CallVolume: &call},
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	result, err := ListOptionResearch(context.Background(), db, OptionResearchListQuery{Page: 1, PageSize: 2})
	if err != nil || result.Total != 3 || len(result.Items) != 2 || result.Items[0].Ticker != "NEW" || result.Items[1].Ticker != "MID" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if result.Summary.OptionCovered != 1 || result.Summary.ShortCovered != 2 || result.Summary.LastFetchedAt == nil || !result.Summary.LastFetchedAt.Equal(now) || len(result.Items[0].Anomalies) != 1 {
		t.Fatalf("summary=%+v items=%+v", result.Summary, result.Items)
	}
	result, err = ListOptionResearch(context.Background(), db, OptionResearchListQuery{Page: 2, PageSize: 2})
	if err != nil || len(result.Items) != 1 || result.Items[0].Ticker != "OLD" || result.Total != 3 {
		t.Fatalf("page2=%+v err=%v", result, err)
	}
	result, err = ListOptionResearch(context.Background(), db, OptionResearchListQuery{Page: 3, PageSize: 2})
	if err != nil || len(result.Items) != 0 || result.Total != 3 {
		t.Fatalf("empty page=%+v err=%v", result, err)
	}
}
func TestOptionResearchListEmptyAndPageBounds(t *testing.T) {
	db := openMigratedTestDatabase(t)
	result, err := ListOptionResearch(context.Background(), db, OptionResearchListQuery{Page: -1, PageSize: 99999})
	if err != nil || result.Page != 1 || result.PageSize != 20 || result.Items == nil || result.Total != 0 || result.Summary.LastFetchedAt != nil {
		t.Fatalf("empty=%+v err=%v", result, err)
	}
	if _, err = ListOptionResearch(context.Background(), nil, OptionResearchListQuery{}); err == nil {
		t.Fatal("missing db accepted")
	}
}
