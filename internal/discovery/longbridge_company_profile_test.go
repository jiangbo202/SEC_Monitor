package discovery

import (
	"context"
	"errors"
	"testing"
	"time"

	"sec_monitor/internal/config"
)

type fakeLongbridgeCompanyClient struct {
	overview LongbridgeCompanyOverview
	err      error
	symbol   string
}

type countingLongbridgeCompanyClient struct {
	err     error
	symbols []string
}

func (f *countingLongbridgeCompanyClient) Company(_ context.Context, symbol string) (LongbridgeCompanyOverview, error) {
	f.symbols = append(f.symbols, symbol)
	return LongbridgeCompanyOverview{}, f.err
}

func (f *fakeLongbridgeCompanyClient) Company(_ context.Context, symbol string) (LongbridgeCompanyOverview, error) {
	f.symbol = symbol
	return f.overview, f.err
}

func TestRefreshLongbridgeCompanyProfileCachesAndKeepsLastSuccess(t *testing.T) {
	db := openMigratedTestDatabase(t)
	security := Security{CIK: "0000123456", CompanyName: "Profile Co"}
	if err := db.Create(&security).Error; err != nil {
		t.Fatal(err)
	}
	listing := Listing{SecurityID: security.ID, Ticker: "PROF", ProviderTicker: "PROF", ValidFrom: time.Now().UTC(), MappingStatus: MappingStatusCurrent}
	if err := db.Create(&listing).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC)
	client := &fakeLongbridgeCompanyClient{overview: LongbridgeCompanyOverview{CompanyName: "Profile Co", Profile: "Makes useful test products.", Website: "https://example.test", Employees: "12"}}
	options := LongbridgeCompanyProfileOptions{
		AppKey: "key", AppSecret: "secret", AccessToken: "token", TTLDays: 30, Now: func() time.Time { return now },
		NewClient: func(_, _, _ string) (longbridgeCompanyClient, error) { return client, nil },
	}
	first, err := refreshLongbridgeCompanyProfile(context.Background(), db, security, listing, options, false)
	if err != nil || !first.Fetched || client.symbol != "PROF.US" {
		t.Fatalf("first refresh = %+v, err=%v, symbol=%q", first, err, client.symbol)
	}
	second, err := refreshLongbridgeCompanyProfile(context.Background(), db, security, listing, options, false)
	if err != nil || !second.Cached {
		t.Fatalf("second refresh = %+v, err=%v", second, err)
	}
	client.err = errors.New("provider unavailable")
	_, err = refreshLongbridgeCompanyProfile(context.Background(), db, security, listing, options, true)
	if err == nil {
		t.Fatal("force refresh should report provider failure")
	}
	var saved CompanyProfileSnapshot
	if err := db.Where("security_id = ?", security.ID).First(&saved).Error; err != nil {
		t.Fatal(err)
	}
	if saved.Profile != "Makes useful test products." || saved.LastError == "" || saved.FetchedAt == nil {
		t.Fatalf("saved profile = %#v", saved)
	}
	if saved.RetryCount != 1 || saved.NextRetryAt == nil || !saved.NextRetryAt.Equal(now.Add(5*time.Minute)) {
		t.Fatalf("saved retry state = %#v", saved)
	}
	client.err = nil
	if _, err := refreshLongbridgeCompanyProfile(context.Background(), db, security, listing, options, true); err != nil {
		t.Fatalf("recovery refresh: %v", err)
	}
	saved = CompanyProfileSnapshot{}
	if err := db.Where("security_id = ?", security.ID).First(&saved).Error; err != nil {
		t.Fatal(err)
	}
	if saved.LastError != "" || saved.RetryCount != 0 || saved.NextRetryAt != nil {
		t.Fatalf("successful recovery should clear retry state: %#v", saved)
	}
}

func TestCompanyProfileRecoveryQueueAndDeferredRetry(t *testing.T) {
	db := openMigratedTestDatabase(t)
	security := Security{CIK: "0000222444", CompanyName: "Retry Profile Co"}
	if err := db.Create(&security).Error; err != nil {
		t.Fatal(err)
	}
	listing := Listing{SecurityID: security.ID, Ticker: "RPRF", ValidFrom: time.Now().UTC(), MappingStatus: MappingStatusCurrent}
	if err := db.Create(&listing).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 28, 10, 0, 0, 0, time.UTC)
	batch := UniverseBatch{BatchID: "profile-recovery-batch", Kind: BatchKindPrescreen, Status: BatchStatusPublished, StartedAt: now}
	if err := db.Create(&batch).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&CurrentBatchPointer{Kind: BatchKindPrescreen, BatchID: batch.BatchID}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&CandidateScoreSnapshot{BatchID: batch.BatchID, SecurityID: security.ID, Ticker: "RPRF", Grade: CandidateGradeB, EligibleB: true, TotalScore: 70}).Error; err != nil {
		t.Fatal(err)
	}
	next := now.Add(5 * time.Minute)
	if err := db.Create(&CompanyProfileSnapshot{SecurityID: security.ID, Provider: longbridgeCompanyProfileProvider, Ticker: "RPRF", LastAttemptAt: &now, LastError: "provider unavailable", RetryCount: 1, NextRetryAt: &next}).Error; err != nil {
		t.Fatal(err)
	}

	queue, err := ListCurrentCandidateCompanyProfileRecoveryQueue(context.Background(), db, 30, now)
	if err != nil || len(queue.Items) != 1 {
		t.Fatalf("queue=%#v err=%v", queue, err)
	}
	item := queue.Items[0]
	if item.Ticker != "RPRF" || item.RetryDue || item.RetryCount != 1 || item.NextRetryAt == nil || !item.NextRetryAt.Equal(next) {
		t.Fatalf("queue item=%#v", item)
	}
	client := &fakeLongbridgeCompanyClient{}
	result, err := refreshLongbridgeCompanyProfile(context.Background(), db, security, listing, LongbridgeCompanyProfileOptions{
		AppKey: "key", AppSecret: "secret", AccessToken: "token", Now: func() time.Time { return now },
		NewClient: func(_, _, _ string) (longbridgeCompanyClient, error) { return client, nil },
	}, false)
	if err != nil || !result.Deferred || client.symbol != "" {
		t.Fatalf("deferred result=%#v err=%v symbol=%q", result, err, client.symbol)
	}

	queue, err = ListCurrentCandidateCompanyProfileRecoveryQueue(context.Background(), db, 30, next)
	if err != nil || len(queue.Items) != 1 || !queue.Items[0].RetryDue {
		t.Fatalf("due queue=%#v err=%v", queue, err)
	}
}

func TestRetryCurrentCandidateLongbridgeCompanyProfilesStopsAfterSameTransportFailures(t *testing.T) {
	db := openMigratedTestDatabase(t)
	now := time.Date(2026, 7, 29, 5, 0, 0, 0, time.UTC)
	batch := UniverseBatch{BatchID: "profile-bulk-retry-batch", Kind: BatchKindPrescreen, Status: BatchStatusPublished, StartedAt: now}
	if err := db.Create(&batch).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&CurrentBatchPointer{Kind: BatchKindPrescreen, BatchID: batch.BatchID}).Error; err != nil {
		t.Fatal(err)
	}
	for index, ticker := range []string{"FAIL1", "FAIL2", "FAIL3", "FAIL4"} {
		security := Security{CIK: "00009999" + string(rune('1'+index)), CompanyName: ticker + " Co"}
		if err := db.Create(&security).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&Listing{SecurityID: security.ID, Ticker: ticker, ValidFrom: now, MappingStatus: MappingStatusCurrent}).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&CandidateScoreSnapshot{BatchID: batch.BatchID, SecurityID: security.ID, Ticker: ticker, Grade: CandidateGradeB, EligibleB: true, TotalScore: 70 - index}).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&CompanyProfileSnapshot{SecurityID: security.ID, Provider: longbridgeCompanyProfileProvider, Ticker: ticker, LastAttemptAt: &now, LastError: "EOF", RetryCount: 1}).Error; err != nil {
			t.Fatal(err)
		}
	}
	client := &countingLongbridgeCompanyClient{err: errors.New("EOF")}
	result, err := retryCurrentCandidateLongbridgeCompanyProfiles(context.Background(), db, config.DiscoveryConfig{
		LongbridgeCompanyProfileEnabled: true, LongbridgeCompanyProfileRequestBudget: 10,
	}, LongbridgeCompanyProfileOptions{
		AppKey: "key", AppSecret: "secret", AccessToken: "token", Now: func() time.Time { return now },
		NewClient: func(_, _, _ string) (longbridgeCompanyClient, error) { return client, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.QueueCount != 4 || result.Attempted != 3 || result.Failed != 3 || !result.Stopped {
		t.Fatalf("bulk retry result = %#v", result)
	}
	if len(client.symbols) != 3 || result.StopReason == "" {
		t.Fatalf("client calls=%v stop=%q", client.symbols, result.StopReason)
	}
}

func TestGetCompanyProfileOverlaysCachedLongbridgeOverview(t *testing.T) {
	db := openMigratedTestDatabase(t)
	security := Security{CIK: "0000222222", CompanyName: "Profile Overlay", SIC: 2834}
	if err := db.Create(&security).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&Listing{SecurityID: security.ID, Ticker: "OVLY", ValidFrom: time.Now().UTC(), MappingStatus: MappingStatusCurrent}).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := db.Create(&CompanyProfileSnapshot{SecurityID: security.ID, Provider: longbridgeCompanyProfileProvider, Ticker: "OVLY", Profile: "Longbridge issuer profile.", Website: "https://issuer.test", Founded: "2001", FetchedAt: &now}).Error; err != nil {
		t.Fatal(err)
	}
	profile, err := GetCompanyProfile(context.Background(), db, "OVLY", security.CIK)
	if err != nil {
		t.Fatal(err)
	}
	if profile.BusinessSummary != "Longbridge issuer profile." || profile.Website != "https://issuer.test" || profile.ProfileProvider == "" {
		t.Fatalf("profile = %#v", profile)
	}
}
