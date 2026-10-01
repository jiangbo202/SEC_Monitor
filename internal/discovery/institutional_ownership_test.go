package discovery

import (
	"context"
	"encoding/json"
	"errors"
	lbfundamental "github.com/longbridge/openapi-go/fundamental"
	"strconv"
	"testing"
	"time"
)

type fakeOwnershipClient struct {
	top      json.RawMessage
	details  map[int64]json.RawMessage
	calls    int
	topCalls int
	fail     bool
}

func TestOwnershipBudgetContinuesPastCachedHolders(t *testing.T) {
	db := openMigratedTestDatabase(t)
	now := time.Now().UTC()
	fake := &fakeOwnershipClient{details: map[int64]json.RawMessage{}}
	holders := []map[string]any{}
	for id := int64(1); id <= 21; id++ {
		holders = append(holders, map[string]any{"object_id": strconv.FormatInt(id, 10), "name": "Capital " + strconv.FormatInt(id, 10), "title": "Institution", "period": "Q2 2026", "filing_date": "2026/06/30", "percent_shares_held": "1%"})
		fake.details[id] = json.RawMessage(`{"owner_source":"Institution","holding_details":[]}`)
	}
	fake.top, _ = json.Marshal(map[string]any{"info": []any{map[string]any{"period": "Q2 2026", "share_holders": holders}}})
	first, err := refreshInstitutionalOwnership(context.Background(), db, "TEST", fake, 0, now)
	if err != nil || first.HoldersFetched != 20 || fake.calls != 20 {
		t.Fatalf("budget=%+v calls=%d err=%v", first, fake.calls, err)
	}
	second, err := refreshInstitutionalOwnership(context.Background(), db, "TEST", fake, 0, now.Add(time.Minute))
	if err != nil || second.HoldersFetched != 1 || fake.calls != 21 || fake.topCalls != 1 {
		t.Fatalf("continuation=%+v calls=%d err=%v", second, fake.calls, err)
	}
	key := ownershipRefreshKey{DB: db, Ticker: "TEST"}
	ownershipRefreshInFlight.Store(key, struct{}{})
	defer ownershipRefreshInFlight.Delete(key)
	if _, err := refreshInstitutionalOwnership(context.Background(), db, "TEST", fake, 0, now); err == nil {
		t.Fatal("concurrent refresh was not rejected")
	}
}

func (f *fakeOwnershipClient) ShareholderTop(context.Context, string) (*lbfundamental.ShareholderTopResponse, error) {
	f.topCalls++
	return &lbfundamental.ShareholderTopResponse{Data: f.top}, nil
}

func TestOwnershipBackgroundBudgetRotatesAfterCacheExpiry(t *testing.T) {
	db := openMigratedTestDatabase(t)
	now := time.Now().UTC()
	fake := &fakeOwnershipClient{details: map[int64]json.RawMessage{}}
	holders := []map[string]any{}
	for id := int64(1); id <= 6; id++ {
		holders = append(holders, map[string]any{"object_id": strconv.FormatInt(id, 10), "name": "Capital", "title": "Institution", "period": "Q2 2026", "filing_date": "2026/06/30", "percent_shares_held": "1%"})
		fake.details[id] = json.RawMessage(`{"owner_source":"Institution","holding_details":[]}`)
	}
	fake.top, _ = json.Marshal(map[string]any{"info": []any{map[string]any{"period": "Q2 2026", "share_holders": holders}}})
	if _, err := refreshInstitutionalOwnershipLimited(context.Background(), db, "TEST", fake, 0, now, 5); err != nil {
		t.Fatal(err)
	}
	if fake.calls != 5 {
		t.Fatalf("calls=%d", fake.calls)
	}
	if _, err := refreshInstitutionalOwnershipLimited(context.Background(), db, "TEST", fake, 0, now.Add(25*time.Hour), 1); err != nil {
		t.Fatal(err)
	}
	var receipt InstitutionalOwnershipReceipt
	if err := db.Where("ticker = ? AND holder_id = ?", "TEST", "6").First(&receipt).Error; err != nil {
		t.Fatal("unqueried sixth holder was starved", err)
	}
}
func (f *fakeOwnershipClient) ShareholderDetail(_ context.Context, _ string, id int64) (*lbfundamental.ShareholderDetailResponse, error) {
	f.calls++
	if f.fail {
		return nil, errors.New("timeout")
	}
	return &lbfundamental.ShareholderDetailResponse{Data: f.details[id]}, nil
}

func TestOwnershipClassificationAndMissingValues(t *testing.T) {
	rows := []InstitutionalHolderSnapshot{{HolderName: "Fund", InstitutionType: "Fund"}, {HolderName: "Individual", InstitutionType: "Person"}, {HolderName: "Unknown"}, {HolderName: "Insider", OwnerType: "Insider"}}
	inst, other := splitInstitutionalHolders(rows)
	if len(inst) != 1 || len(other) != 3 {
		t.Fatalf("classification=%+v / %+v", inst, other)
	}
	for _, value := range []string{`null`, `""`, `"-"`, `"<0.01%"`, `"NaN"`, `"-1"`} {
		if ownershipNumber(json.RawMessage(value), true) != nil {
			t.Fatalf("missing/invalid %s became numeric", value)
		}
	}
	if n := ownershipNumber(json.RawMessage(`"0.00%"`), true); n == nil || *n != 0 {
		t.Fatal("real zero missing")
	}
	if ownershipDate("2026/02/30") != "" {
		t.Fatal("invalid date accepted")
	}
}

func TestOwnershipHistoryCacheAndProviderDate(t *testing.T) {
	db := openMigratedTestDatabase(t)
	now := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	fake := &fakeOwnershipClient{top: json.RawMessage(`{"info":[{"period":"Q2 2026","share_holders":[{"object_id":"1","name":"Example Capital","title":"","percent_shares_held":"7.96%","shares_held":"1,000","filing_date":"2026/06/30"},{"object_id":"2","name":"Person","title":"Person"}]}]}`), details: map[int64]json.RawMessage{1: json.RawMessage(`{"name":"Example Capital","owner_source":"Institution","holding_details":[{"period":"Q2 2026","percent_shares_held":"7.96%","shares_held":"1000","filing_date":"2026/06/30"},{"period":"Q1 2026","percent_shares_held":null,"shares_held":"900","filing_date":"2026/03/31"}]}`)}}
	first, err := refreshInstitutionalOwnership(context.Background(), db, "TEST", fake, 0, now)
	if err != nil {
		t.Fatal(err)
	}
	if first.HoldersFetched != 1 || fake.calls != 1 {
		t.Fatalf("first=%+v", first)
	}
	second, err := refreshInstitutionalOwnership(context.Background(), db, "TEST", fake, 0, now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if second.Cached != 2 || fake.calls != 1 || fake.topCalls != 1 {
		t.Fatalf("cache=%+v", second)
	}
	view, err := GetTickerInstitutionalHoldingHistory(context.Background(), db, "TEST")
	if err != nil {
		t.Fatal(err)
	}
	if len(view.OwnershipHistory) != 2 {
		t.Fatalf("history=%+v", view)
	}
	for _, point := range view.OwnershipHistory {
		if point.FilingDate != "" || point.HoldingDate != "" || point.ProviderDate == "" {
			t.Fatalf("provider date misrepresented=%+v", point)
		}
		if !point.FetchedAt.Equal(now) {
			t.Fatal("cache fabricated freshness")
		}
		if point.SourceKind != "detail" {
			t.Fatal("top overwrote confirmed detail")
		}
	}
	if view.OwnershipHistory[1].PercentOfShares != nil {
		t.Fatal("missing ratio filled with zero")
	}
}

func TestOwnershipDetailNeverInfersPersonOrUnsupportedSchema(t *testing.T) {
	now := time.Now()
	rows, status, err := parseOwnershipDetail("TEST", "1", "Person", json.RawMessage(`{"owner_source":"Person","holding_details":[{"period":"Q2 2026","percent_shares_held":"10%","filing_date":"2026/06/30"}]}`), now)
	if err != nil || len(rows) != 0 || status != "excluded_type" {
		t.Fatalf("person rows=%+v status=%s err=%v", rows, status, err)
	}
	rows, status, err = parseOwnershipDetail("TEST", "1", "Capital", json.RawMessage(`{"owner_source":"Institution","holding_details":[{"unknown_date":"2026-06-30","weight":"10%"}]}`), now)
	if err != nil || len(rows) != 0 || status != "unsupported_schema" {
		t.Fatalf("unknown rows=%+v status=%s err=%v", rows, status, err)
	}
}

func TestOwnershipFailedDetailsAreRetryableAndTopFactsSurvive(t *testing.T) {
	db := openMigratedTestDatabase(t)
	now := time.Now().UTC()
	fake := &fakeOwnershipClient{top: json.RawMessage(`{"info":[{"period":"Latest","share_holders":[{"object_id":"1","name":"Capital","title":"Institution","percent_shares_held":"2%","filing_date":"2026/06/30"}]}]}`), fail: true}
	result, err := refreshInstitutionalOwnership(context.Background(), db, "TEST", fake, 0, now)
	if err != nil || len(result.Warnings) != 1 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	view, err := GetTickerInstitutionalHoldingHistory(context.Background(), db, "TEST")
	if err != nil || len(view.OwnershipHistory) != 1 {
		t.Fatalf("top lost=%+v err=%v", view, err)
	}
	fake.fail = false
	fake.details = map[int64]json.RawMessage{1: json.RawMessage(`{"owner_source":"Institution","holding_details":[]}`)}
	result, err = refreshInstitutionalOwnership(context.Background(), db, "TEST", fake, 0, now.Add(time.Minute))
	if err != nil || result.HoldersFetched != 1 {
		t.Fatalf("retry=%+v err=%v", result, err)
	}
	result, err = refreshInstitutionalOwnership(context.Background(), db, "TEST", fake, 0, now.Add(2*time.Minute))
	if err != nil || result.Cached != 2 {
		t.Fatalf("empty cache=%+v err=%v", result, err)
	}
}
