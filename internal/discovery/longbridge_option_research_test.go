package discovery

import (
	"context"
	"errors"
	"testing"
	"time"

	lbquote "github.com/longbridge/openapi-go/quote"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type fakeLongbridgeOptionResearchClient struct {
	volume    *lbquote.OptionVolumeStats
	volumeErr error
	short     *lbquote.ShortPositionsResponse
	shortErr  error
	closed    bool
}

func (f *fakeLongbridgeOptionResearchClient) OptionVolume(context.Context, string) (*lbquote.OptionVolumeStats, error) {
	return f.volume, f.volumeErr
}
func (f *fakeLongbridgeOptionResearchClient) OptionVolumeDaily(context.Context, string, time.Time, time.Time) ([]*lbquote.DailyOptionVolume, error) {
	return nil, nil
}
func (f *fakeLongbridgeOptionResearchClient) ShortPositions(context.Context, string, uint32) (*lbquote.ShortPositionsResponse, error) {
	return f.short, f.shortErr
}
func (f *fakeLongbridgeOptionResearchClient) Close() error {
	f.closed = true
	return nil
}

func TestRefreshLongbridgeOptionResearchPersistsCompactDailySnapshot(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&OptionResearchSnapshot{}); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 12, 0, 0, 0, 0, time.UTC)
	client := &fakeLongbridgeOptionResearchClient{volume: &lbquote.OptionVolumeStats{CallVolume: "2000", PutVolume: "4000"}, short: &lbquote.ShortPositionsResponse{Data: []*lbquote.ShortPositionsItem{{Timestamp: "1785470400", Rate: "0.125", CurrentSharesShort: "1000000", AvgDailyShareVolume: "200000", DaysToCover: "5.5"}}}}
	result, err := refreshLongbridgeOptionResearch(context.Background(), db, "opt", "", LongbridgeOptionResearchOptions{AppKey: "key", AppSecret: "secret", AccessToken: "token", Now: func() time.Time { return now }, NewClient: func(_, _, _ string) (longbridgeOptionResearchClient, error) { return client, nil }})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Fetched || result.Snapshot.PutCallVolumeRatio == nil || *result.Snapshot.PutCallVolumeRatio != 2 || result.Snapshot.ShortRatioPct == nil || *result.Snapshot.ShortRatioPct != 12.5 || result.Snapshot.ShortReportedAt != "2026-07-31T04:00:00Z" {
		t.Fatalf("refresh=%+v", result)
	}
	if !client.closed {
		t.Fatal("expected Longbridge option client to be closed after refresh")
	}
	if len(result.Snapshot.Anomalies) < 2 {
		t.Fatalf("anomalies=%+v", result.Snapshot.Anomalies)
	}
	view, err := GetOptionResearch(context.Background(), db, "OPT")
	if err != nil || view.Latest == nil || len(view.Latest.Anomalies) < 2 {
		t.Fatalf("view=%+v err=%v", view, err)
	}
	if _, err := refreshLongbridgeOptionResearch(context.Background(), db, "OPT", "", LongbridgeOptionResearchOptions{AppKey: "key", AppSecret: "secret", AccessToken: "token", Now: func() time.Time { return now.Add(time.Hour) }, NewClient: func(_, _, _ string) (longbridgeOptionResearchClient, error) { return client, nil }}); err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := db.Model(&OptionResearchSnapshot{}).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("count=%d err=%v", count, err)
	}
}

func TestRefreshLongbridgeOptionResearchBatchReusesOneConnection(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&OptionResearchSnapshot{}); err != nil {
		t.Fatal(err)
	}
	client := &fakeLongbridgeOptionResearchClient{volume: &lbquote.OptionVolumeStats{CallVolume: "10", PutVolume: "20"}}
	created := 0
	items, err := refreshLongbridgeOptionResearchBatch(context.Background(), db, []OptionResearchTarget{{Ticker: "ONE"}, {Ticker: "TWO"}}, LongbridgeOptionResearchOptions{
		AppKey: "key", AppSecret: "secret", AccessToken: "token", Now: func() time.Time { return time.Date(2026, 8, 13, 0, 0, 0, 0, time.UTC) },
		NewClient: func(_, _, _ string) (longbridgeOptionResearchClient, error) {
			created++
			return client, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if created != 1 || len(items) != 2 || items[0].Err != nil || items[1].Err != nil || !items[0].Result.Fetched || !items[1].Result.Fetched {
		t.Fatalf("batch created=%d items=%+v", created, items)
	}
	if !client.closed {
		t.Fatal("expected shared client to be closed after batch")
	}
}

func TestRefreshLongbridgeOptionResearchPersistsUnavailableCoverage(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&OptionResearchSnapshot{}); err != nil {
		t.Fatal(err)
	}
	client := &fakeLongbridgeOptionResearchClient{}
	result, err := refreshLongbridgeOptionResearch(context.Background(), db, "none", "", LongbridgeOptionResearchOptions{
		AppKey: "key", AppSecret: "secret", AccessToken: "token", Now: func() time.Time { return time.Date(2026, 8, 13, 0, 0, 0, 0, time.UTC) },
		NewClient: func(_, _, _ string) (longbridgeOptionResearchClient, error) { return client, nil },
	})
	if err != nil || !result.Fetched || result.Snapshot.Status != "unavailable" {
		t.Fatalf("refresh=%+v err=%v", result, err)
	}
	view, err := GetOptionResearch(context.Background(), db, "NONE")
	if err != nil || view.Latest == nil || view.Latest.Status != "unavailable" {
		t.Fatalf("view=%+v err=%v", view, err)
	}
}

func TestRefreshLongbridgeOptionResearchTreatsEmptyShortCoverageAsNormal(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&OptionResearchSnapshot{}); err != nil {
		t.Fatal(err)
	}
	client := &fakeLongbridgeOptionResearchClient{volumeErr: errors.New("option endpoint unavailable")}
	result, err := refreshLongbridgeOptionResearch(context.Background(), db, "empty", "", LongbridgeOptionResearchOptions{
		AppKey: "key", AppSecret: "secret", AccessToken: "token", Now: func() time.Time { return time.Date(2026, 8, 13, 0, 0, 0, 0, time.UTC) },
		NewClient: func(_, _, _ string) (longbridgeOptionResearchClient, error) { return client, nil },
	})
	if err != nil || !result.Fetched || result.Snapshot.Status != "unavailable" {
		t.Fatalf("refresh=%+v err=%v", result, err)
	}
}

func TestOptionResearchAnomalyDetectsHistoricalVolumeSpike(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&OptionResearchSnapshot{}); err != nil {
		t.Fatal(err)
	}
	for day := 1; day <= 5; day++ {
		call, put := int64(100), int64(100)
		if err := db.Create(&OptionResearchSnapshot{Provider: longbridgeOptionResearchProvider, Ticker: "SPIKE", ObservedDate: time.Date(2026, 8, day, 0, 0, 0, 0, time.UTC).Format(time.DateOnly), CallVolume: &call, PutVolume: &put}).Error; err != nil {
			t.Fatal(err)
		}
	}
	call, put := int64(500), int64(500)
	anomalies := optionResearchAnomalies(context.Background(), db, OptionResearchSnapshot{Provider: longbridgeOptionResearchProvider, Ticker: "SPIKE", ObservedDate: "2026-08-10", CallVolume: &call, PutVolume: &put})
	found := false
	for _, item := range anomalies {
		if item.Kind == "volume_spike" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected volume spike, got %+v", anomalies)
	}
}
