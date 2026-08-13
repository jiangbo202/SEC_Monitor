package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"sec_monitor/internal/config"
	"sec_monitor/internal/model"
	"sec_monitor/internal/sec"
	"sec_monitor/internal/telegram"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type fakeSECClient struct {
	filings         []sec.FilingResult
	currentFilings  []sec.CurrentFilingResult
	filingsByTicker map[string][]sec.FilingResult
	listErrs        []error
	listErrByTicker map[string]error
	currentErr      error
	listCalls       int
	currentCalls    int
	queries         []sec.FilingQuery
	currentQueries  []sec.CurrentFilingQuery
	listedCompanies []sec.ListedCompany
	listedErr       error
	documents       map[string]string
	documentErr     error
	documentCalls   int
}

type fakeLongbridgeIPOListingClient struct {
	overviews map[string]longbridgeIPOListingOverview
	errors    map[string]error
	calls     []string
}

type fakeLongbridgeIPOCalendarClient struct {
	pages []longbridgeIPOCalendarPage
	errs  []error
	calls []string
}

func (f *fakeLongbridgeIPOCalendarClient) FinanceCalendar(_ context.Context, start, end, market string) (longbridgeIPOCalendarPage, error) {
	f.calls = append(f.calls, start+"|"+end+"|"+market)
	index := len(f.calls) - 1
	if index < len(f.errs) && f.errs[index] != nil {
		return longbridgeIPOCalendarPage{}, f.errs[index]
	}
	if index < len(f.pages) {
		return f.pages[index], nil
	}
	return longbridgeIPOCalendarPage{}, nil
}

func (f *fakeLongbridgeIPOListingClient) Company(_ context.Context, symbol string) (longbridgeIPOListingOverview, error) {
	f.calls = append(f.calls, symbol)
	if err := f.errors[symbol]; err != nil {
		return longbridgeIPOListingOverview{}, err
	}
	return f.overviews[symbol], nil
}

type fakeFundSECClient struct {
	*fakeSECClient
	matches      map[string]bool
	matchReasons map[string]string
	matchResults map[string]fakeFundMatchResult
	matchErrs    map[string]error
	matchCalls   map[string]int
}

type fakeFundMatchResult struct {
	matched bool
	reason  string
}

type fakeFundMetadataSECClient struct {
	*fakeFundSECClient
	metadata      map[string]sec.FundFilingMetadata
	metadataErrs  map[string]error
	metadataCalls map[string]int
}

func (f *fakeFundMetadataSECClient) ParseFundFiling(_ context.Context, filing sec.FilingResult) (sec.FundFilingMetadata, error) {
	if f.metadataCalls == nil {
		f.metadataCalls = map[string]int{}
	}
	f.metadataCalls[filing.AccessionNumber]++
	if err := f.metadataErrs[filing.AccessionNumber]; err != nil {
		return sec.FundFilingMetadata{}, err
	}
	return f.metadata[filing.AccessionNumber], nil
}

func (f *fakeFundSECClient) ResolveFundTicker(context.Context, string) (sec.FundResolution, error) {
	return sec.FundResolution{}, nil
}

func (f *fakeFundSECClient) MatchFundFiling(_ context.Context, _ sec.FundIdentity, filing sec.FilingResult) (bool, string, error) {
	if f.matchCalls == nil {
		f.matchCalls = map[string]int{}
	}
	f.matchCalls[filing.AccessionNumber]++
	if err := f.matchErrs[filing.AccessionNumber]; err != nil {
		return false, "", err
	}
	if result, ok := f.matchResults[filing.AccessionNumber]; ok {
		return result.matched, result.reason, nil
	}
	matched := f.matches[filing.AccessionNumber]
	reason := f.matchReasons[filing.AccessionNumber]
	if reason == "" {
		reason = "class_not_found"
	}
	if matched {
		return true, "matched_class", nil
	}
	return false, reason, nil
}

func (f fakeSECClient) LookupCIK(ctx context.Context, ticker string) (string, string, error) {
	return "0000320193", "Apple Inc.", nil
}

func (f *fakeSECClient) ListFilings(ctx context.Context, query sec.FilingQuery) ([]sec.FilingResult, error) {
	f.queries = append(f.queries, query)
	if f.listErrByTicker != nil {
		if err := f.listErrByTicker[query.Ticker]; err != nil {
			f.listCalls++
			return nil, err
		}
	}
	if f.filingsByTicker != nil {
		f.listCalls++
		return f.filingsByTicker[query.Ticker], nil
	}
	if f.listCalls < len(f.listErrs) && f.listErrs[f.listCalls] != nil {
		err := f.listErrs[f.listCalls]
		f.listCalls++
		return nil, err
	}
	f.listCalls++
	return f.filings, nil
}

func (f *fakeSECClient) ListCurrentFilings(ctx context.Context, query sec.CurrentFilingQuery) ([]sec.CurrentFilingResult, error) {
	f.currentCalls++
	f.currentQueries = append(f.currentQueries, query)
	if f.currentErr != nil {
		return nil, f.currentErr
	}
	return f.currentFilings, nil
}

func (f *fakeSECClient) ListListedCompanies(ctx context.Context) ([]sec.ListedCompany, error) {
	return f.listedCompanies, f.listedErr
}

func (f *fakeSECClient) FetchFilingDocument(ctx context.Context, filingURL string) (string, error) {
	f.documentCalls++
	if f.documentErr != nil {
		return "", f.documentErr
	}
	return f.documents[filingURL], nil
}

func TestIPORadarBackfillsHistorical424B4Offering(t *testing.T) {
	now := time.Date(2026, 6, 21, 9, 0, 0, 0, time.UTC)
	db := testDB(t)
	url := "https://sec.test/kardigan-424b4"
	if err := db.Create(&[]model.IPOFiling{
		{FilingID: "kard-s1", CIK: "0002123613", CompanyName: "Kardigan, Inc.", FilingType: "S-1", FilingDate: now.AddDate(0, 0, -20), FilingURL: "https://sec.test/kardigan-s1"},
		{FilingID: "kard-424b4", CIK: "0002123613", CompanyName: "Kardigan, Inc.", FilingType: "424B4", FilingDate: now.AddDate(0, 0, -1), FilingURL: url},
	}).Error; err != nil {
		t.Fatalf("seed filings: %v", err)
	}
	configs := NewConfigService(db, NewAuditService(db))
	if err := configs.EnsureDefaults(context.Background()); err != nil {
		t.Fatalf("defaults: %v", err)
	}
	if err := configs.UpsertMany(context.Background(), []ConfigInput{
		{Key: "ipo.lookback_days", Value: "30", ValueType: "int", Category: "ipo"},
		{Key: "telegram.enabled", Value: "true", ValueType: "bool", Category: "telegram"},
		{Key: "telegram.bot_token", Value: "token", ValueType: "string", Category: "telegram"},
		{Key: "telegram.chat_id", Value: "chat", ValueType: "string", Category: "telegram"},
	}, "tester"); err != nil {
		t.Fatalf("telegram config: %v", err)
	}
	secClient := &fakeSECClient{documents: map[string]string{url: `We are offering 25,000,000 shares of our common stock. The initial public offering price per share is $16.00.`}}
	notifier := &fakeNotifier{}
	svc := NewIPORadarService(db, secClient, notifier, configs)
	for run := 1; run <= 2; run++ {
		if _, err := svc.Refresh(context.Background()); err != nil {
			t.Fatalf("Refresh run %d: %v", run, err)
		}
	}
	page, err := svc.ListCompanies(context.Background(), IPOCompanyFilter{Page: 1, PageSize: 10}, now)
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("ListCompanies page=%+v err=%v", page, err)
	}
	item := page.Items[0]
	if item.OfferPrice != "16.00" || item.SharesOffered != 25000000 || item.GrossProceeds != "400000000.00" {
		t.Fatalf("offering = %+v", item)
	}
	if secClient.documentCalls != 1 {
		t.Fatalf("document calls = %d, want 1", secClient.documentCalls)
	}
	if notifier.calls != 0 {
		t.Fatalf("historical backfill notifier calls = %d, want 0", notifier.calls)
	}
}

func TestIPORadarRecordsFetchFailureOfferingDiagnostic(t *testing.T) {
	db := testDB(t)
	now := time.Date(2026, 6, 21, 9, 0, 0, 0, time.UTC)
	if err := db.Create(&[]model.IPOFiling{
		{FilingID: "acme-s1", CIK: "0000000001", CompanyName: "Acme Inc.", FilingType: "S-1", FilingDate: now.AddDate(0, 0, -1), FilingURL: "https://sec.test/acme-s1"},
		{FilingID: "acme-424b4", CIK: "0000000001", CompanyName: "Acme Inc.", FilingType: "424B4", FilingDate: now, FilingURL: "https://sec.test/acme-424b4"},
	}).Error; err != nil {
		t.Fatalf("seed filings: %v", err)
	}
	svc := NewIPORadarService(db, &fakeSECClient{documentErr: errors.New("unavailable")}, &fakeNotifier{}, NewConfigService(db, NewAuditService(db)))

	_, _ = svc.enrichIPOMarketData(context.Background(), nil, false)

	var event model.IPOOfferingEvent
	if err := db.Where("filing_id = ?", "acme-424b4").First(&event).Error; err != nil {
		t.Fatalf("load offering event: %v", err)
	}
	if event.ParseStatus != "unsupported" || event.ParseMessage != "fetch_failed" {
		t.Fatalf("offering event = %+v", event)
	}
}

func TestIPORadarReprocessesOlderOfferingParserVersion(t *testing.T) {
	db := testDB(t)
	now := time.Date(2026, 6, 21, 9, 0, 0, 0, time.UTC)
	const filingID = "acme-424b4"
	const filingURL = "https://sec.test/acme-424b4"
	if err := db.Create(&[]model.IPOFiling{
		{FilingID: "acme-s1", CIK: "0000000001", CompanyName: "Acme Inc.", FilingType: "S-1", FilingDate: now.AddDate(0, 0, -1), FilingURL: "https://sec.test/acme-s1"},
		{FilingID: filingID, CIK: "0000000001", CompanyName: "Acme Inc.", FilingType: "424B4", FilingDate: now, FilingURL: filingURL},
	}).Error; err != nil {
		t.Fatalf("seed filings: %v", err)
	}
	if err := db.Create(&model.IPOOfferingEvent{FilingID: filingID, CIK: "0000000001", CompanyName: "Acme Inc.", OfferingType: "unknown", ParseStatus: "unsupported", ParseMessage: "shares_offered_not_found", FilingDate: now, ParserVersion: ipoOfferingParserVersion - 1}).Error; err != nil {
		t.Fatalf("seed offering event: %v", err)
	}
	svc := NewIPORadarService(db, &fakeSECClient{documents: map[string]string{filingURL: `We are offering 10,000,000 shares. The public offering price is $15.00 per share.`}}, &fakeNotifier{}, NewConfigService(db, NewAuditService(db)))

	_, _ = svc.enrichIPOMarketData(context.Background(), nil, false)

	var event model.IPOOfferingEvent
	if err := db.Where("filing_id = ?", filingID).First(&event).Error; err != nil {
		t.Fatalf("load offering event: %v", err)
	}
	if event.ParserVersion != ipoOfferingParserVersion || event.ParseStatus != "parsed" || event.OfferPrice != "15.00" || event.SharesOffered != 10000000 || event.GrossProceeds != "150000000.00" || event.ParseMessage != "" {
		t.Fatalf("reprocessed offering event = %+v", event)
	}
}

func TestIPORadarNew424B4SendsSeparateOfferingNotification(t *testing.T) {
	// The refresh intentionally applies the configured rolling lookback window.
	// Keep this scenario current so it continues to exercise a genuinely new
	// 424B4 instead of silently falling outside that window as calendar time
	// advances.
	now := time.Now().UTC().Truncate(time.Second)
	db := testDB(t)
	if err := db.Create(&model.IPOFiling{FilingID: "kard-s1", CIK: "0002123613", CompanyName: "Kardigan, Inc.", FilingType: "S-1", FilingDate: now.AddDate(0, 0, -20), FilingURL: "https://sec.test/kardigan-s1"}).Error; err != nil {
		t.Fatalf("seed registration filing: %v", err)
	}
	configs := NewConfigService(db, NewAuditService(db))
	if err := configs.EnsureDefaults(context.Background()); err != nil {
		t.Fatalf("defaults: %v", err)
	}
	if err := configs.UpsertMany(context.Background(), []ConfigInput{
		{Key: "ipo.lookback_days", Value: "30", ValueType: "int", Category: "ipo"},
		{Key: "telegram.enabled", Value: "true", ValueType: "bool", Category: "telegram"},
		{Key: "telegram.bot_token", Value: "token", ValueType: "string", Category: "telegram"},
		{Key: "telegram.chat_id", Value: "chat", ValueType: "string", Category: "telegram"},
	}, "tester"); err != nil {
		t.Fatalf("telegram config: %v", err)
	}
	url := "https://sec.test/kardigan-424b4"
	secClient := &fakeSECClient{
		currentFilings:  []sec.CurrentFilingResult{{FilingID: "kard-424b4", CIK: "0002123613", CompanyName: "Kardigan, Inc.", FilingType: "424B4", FilingDate: now, FilingURL: url, Title: "Final prospectus"}},
		listedCompanies: []sec.ListedCompany{{CIK: "0002123613", Name: "Kardigan, Inc.", Ticker: "KARD", Exchange: "Nasdaq"}},
		documents:       map[string]string{url: `We are offering 25,000,000 shares of our common stock. The initial public offering price per share is $16.00.`},
	}
	notifier := &fakeNotifier{}
	svc := NewIPORadarService(db, secClient, notifier, configs)
	firstResult, err := svc.Refresh(context.Background())
	if err != nil {
		t.Fatalf("initial refresh: %v", err)
	}
	if firstResult.NewFilings != 1 {
		t.Fatalf("initial refresh result=%+v, want one new 424B4 filing", firstResult)
	}
	secClient.currentFilings = []sec.CurrentFilingResult{{FilingID: "kard-duplicate", CIK: "0002123613", CompanyName: "Kardigan, Inc.", FilingType: "424B4", FilingDate: now.Add(time.Hour), FilingURL: "https://sec.test/kardigan-duplicate"}}
	secClient.documents["https://sec.test/kardigan-duplicate"] = secClient.documents[url]
	if _, err := svc.Refresh(context.Background()); err != nil {
		t.Fatalf("duplicate refresh: %v", err)
	}
	secClient.currentFilings = []sec.CurrentFilingResult{{FilingID: "kard-correction", CIK: "0002123613", CompanyName: "Kardigan, Inc.", FilingType: "424B4", FilingDate: now.Add(2 * time.Hour), FilingURL: "https://sec.test/kardigan-correction"}}
	secClient.documents["https://sec.test/kardigan-correction"] = `We are offering 25,000,000 shares of our common stock. The initial public offering price per share is $17.00.`
	if _, err := svc.Refresh(context.Background()); err != nil {
		t.Fatalf("correction refresh: %v", err)
	}
	secClient.currentFilings = []sec.CurrentFilingResult{{FilingID: "kard-follow-on", CIK: "0002123613", CompanyName: "Kardigan, Inc.", FilingType: "424B4", FilingDate: now.Add(3 * time.Hour), FilingURL: "https://sec.test/kardigan-follow-on"}}
	secClient.documents["https://sec.test/kardigan-follow-on"] = `We are offering 5,000,000 shares of our common stock. The public offering price is $20.00 per share.`
	if _, err := svc.Refresh(context.Background()); err != nil {
		t.Fatalf("follow-on refresh: %v", err)
	}
	if notifier.calls != 2 || len(notifier.messages) != 2 {
		t.Fatalf("notifier calls=%d messages=%d, want two pricing messages without listed-company IPO filing alerts", notifier.calls, len(notifier.messages))
	}
	offeringMessage := notifier.messages[0].Text
	for _, want := range []string{"IPO 定价", "KARD", "$16.00", "25,000,000", "$400,000,000.00", url} {
		if !strings.Contains(offeringMessage, want) {
			t.Fatalf("offering message %q missing %q", offeringMessage, want)
		}
	}
	var batch model.NotificationBatch
	if err := db.Where("source = ?", "ipo_offering").First(&batch).Error; err != nil {
		t.Fatalf("load offering batch: %v", err)
	}
	if batch.Status != "sent" || batch.SentCount != 1 {
		t.Fatalf("offering batch = %+v", batch)
	}
	var events []model.IPOOfferingEvent
	if err := db.Order("id ASC").Find(&events).Error; err != nil {
		t.Fatalf("load events: %v", err)
	}
	wantTypes := []string{"initial", "duplicate", "correction", "follow_on"}
	if len(events) != len(wantTypes) {
		t.Fatalf("events = %+v", events)
	}
	for i, want := range wantTypes {
		if events[i].OfferingType != want {
			t.Fatalf("event %d type=%q want=%q", i, events[i].OfferingType, want)
		}
		wantNotified := want == "initial" || want == "correction"
		if (events[i].NotifiedAt != nil) != wantNotified {
			t.Fatalf("event %d notified=%v want=%v", i, events[i].NotifiedAt != nil, wantNotified)
		}
	}
	var market model.IPOCompanyMarketData
	if err := db.Where("cik = ?", "0002123613").First(&market).Error; err != nil {
		t.Fatalf("market: %v", err)
	}
	if market.OfferPrice != "17.00" || market.GrossProceeds != "425000000.00" {
		t.Fatalf("market overwritten by follow-on: %+v", market)
	}
}

func TestIPORadarListOfferingEvents(t *testing.T) {
	db := testDB(t)
	now := time.Date(2026, 6, 22, 9, 0, 0, 0, time.UTC)
	rows := []model.IPOOfferingEvent{
		{FilingID: "old", CIK: "1", CompanyName: "Acme", OfferingType: "initial", ParseStatus: "parsed", FilingDate: now.Add(-time.Hour), ParserVersion: 4},
		{FilingID: "new", CIK: "1", CompanyName: "Acme", OfferingType: "correction", ParseStatus: "parsed", FilingDate: now, ParserVersion: 4},
		{FilingID: "other", CIK: "2", CompanyName: "Other", OfferingType: "initial", ParseStatus: "parsed", FilingDate: now.Add(time.Hour), ParserVersion: 4},
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatalf("seed events: %v", err)
	}
	svc := NewIPORadarService(db, &fakeSECClient{}, &fakeNotifier{}, NewConfigService(db, NewAuditService(db)))

	got, err := svc.ListOfferingEvents(context.Background(), "1", 1, 10)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if got.Total != 2 || len(got.Items) != 2 || got.Items[0].FilingID != "new" || got.Items[1].FilingID != "old" {
		t.Fatalf("events = %+v", got)
	}
}

type fakeNotifier struct {
	messages []telegram.Message
	errs     []error
	calls    int
}

func (f *fakeNotifier) Send(ctx context.Context, message telegram.Message) error {
	if f.calls < len(f.errs) && f.errs[f.calls] != nil {
		err := f.errs[f.calls]
		f.calls++
		return err
	}
	f.calls++
	f.messages = append(f.messages, message)
	return nil
}

func testDB(t *testing.T) *gorm.DB {
	t.Helper()

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	if err := db.AutoMigrate(
		&model.WatchTarget{},
		&model.Filing{},
		&model.WatchTargetFiling{},
		&model.SyncRun{},
		&model.SyncRunDetail{},
		&model.TaskConfig{},
		&model.TaskExecution{},
		&model.OperationalAlertDelivery{},
		&model.RecoveryDrill{},
		&model.LifecycleCleanupRun{},
		&model.SystemConfig{},
		&model.OperationLog{},
		&model.NotificationLog{},
		&model.NotificationBatch{},
		&model.NotificationBatchItem{},
		&model.TradeSetupNotificationState{},
		&model.MacroRelease{},
		&model.MacroObservation{},
		&model.EarningsPreview{},
		&model.EarningsPreviewNotice{},
		&model.FundFilingIdentity{},
		&model.IPOFiling{},
		&model.IPOCompanyFollow{},
		&model.IPOCompanyOverride{},
		&model.IPOCompanyMarketData{},
		&model.IPOOfferingEvent{},
		&model.IPOCalendarEvent{},
	); err != nil {
		t.Fatalf("migrate test db: %v", err)
	}
	return db
}

func TestIPORadarFollowUsesBaselineThenNotifiesProgress(t *testing.T) {
	db := testDB(t)
	if err := db.AutoMigrate(&model.InAppNotification{}); err != nil {
		t.Fatalf("migrate in-app notifications: %v", err)
	}
	now := time.Date(2026, 8, 12, 2, 0, 0, 0, time.UTC)
	if err := db.Create(&model.IPOFiling{FilingID: "acme-s1", CIK: "0000000001", CompanyName: "Acme Inc.", FilingType: "S-1", FilingDate: now.AddDate(0, 0, -1), FilingURL: "https://sec.test/s1"}).Error; err != nil {
		t.Fatalf("seed ipo filing: %v", err)
	}
	configs := NewConfigService(db, NewAuditService(db))
	if err := configs.EnsureDefaults(context.Background()); err != nil {
		t.Fatalf("ensure defaults: %v", err)
	}
	inApp := NewInAppNotificationService(db, configs)
	svc := NewIPORadarService(db, &fakeSECClient{}, &fakeNotifier{}, configs).WithInAppNotifications(inApp)
	follow, err := svc.SetCompanyFollow(context.Background(), "0000000001", true, now)
	if err != nil || follow.LastProgressKey == "" {
		t.Fatalf("SetCompanyFollow follow=%+v err=%v", follow, err)
	}
	followed := true
	followedPage, err := svc.ListCompanies(context.Background(), IPOCompanyFilter{Followed: &followed, Page: 1, PageSize: 10}, now)
	if err != nil || followedPage.Total != 1 || !followedPage.Items[0].Followed {
		t.Fatalf("followed company page=%+v err=%v", followedPage, err)
	}
	if err := svc.notifyFollowedCompanyProgress(context.Background(), 0, "test"); err != nil {
		t.Fatalf("notify unchanged follow: %v", err)
	}
	var count int64
	if err := db.Model(&model.InAppNotification{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("baseline created %d in-app notifications, err=%v", count, err)
	}
	acceptedAt := now.Add(time.Hour)
	if err := db.Create(&model.IPOFiling{FilingID: "acme-amendment", CIK: "0000000001", CompanyName: "Acme Inc.", FilingType: "S-1/A", FilingDate: now, AcceptedAt: &acceptedAt, FilingURL: "https://sec.test/s1a"}).Error; err != nil {
		t.Fatalf("seed progress filing: %v", err)
	}
	if err := svc.notifyFollowedCompanyProgress(context.Background(), 0, "test"); err != nil {
		t.Fatalf("notify progress: %v", err)
	}
	if err := db.Model(&model.InAppNotification{}).Where("source = ?", "ipo_progress").Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("ipo progress notifications = %d, err=%v", count, err)
	}
	if err := svc.notifyFollowedCompanyProgress(context.Background(), 0, "test"); err != nil {
		t.Fatalf("repeat progress notification: %v", err)
	}
	if err := db.Model(&model.InAppNotification{}).Where("source = ?", "ipo_progress").Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("repeat created %d IPO progress notifications, err=%v", count, err)
	}
}

func TestIPORadarHealthCountsOperatorAttention(t *testing.T) {
	now := time.Date(2026, 7, 12, 9, 0, 0, 0, time.UTC)
	db := testDB(t)
	configs := NewConfigService(db, NewAuditService(db))
	if err := configs.EnsureDefaults(context.Background()); err != nil {
		t.Fatalf("ensure defaults: %v", err)
	}
	if err := configs.UpsertMany(context.Background(), []ConfigInput{{Key: "ipo.lifecycle_recheck_hours", Value: "24", ValueType: "int", Category: "ipo"}}, "tester"); err != nil {
		t.Fatalf("configure lifecycle threshold: %v", err)
	}
	if err := db.Create(&[]model.IPOFiling{
		{FilingID: "pending-s1", CIK: "0000000001", CompanyName: "Pending Listing", FilingType: "S-1", FilingDate: now.AddDate(0, 0, -2)},
		{FilingID: "pending-424b4", CIK: "0000000001", CompanyName: "Pending Listing", FilingType: "424B4", FilingDate: now.AddDate(0, 0, -1)},
		{FilingID: "stale-s1", CIK: "0000000002", CompanyName: "Stale Lifecycle", FilingType: "S-1", FilingDate: now.AddDate(0, 0, -1)},
		{FilingID: "stale-effect", CIK: "0000000002", CompanyName: "Stale Lifecycle", FilingType: "EFFECT", FilingDate: now},
	}).Error; err != nil {
		t.Fatalf("seed IPO filings: %v", err)
	}
	if err := db.Create(&model.IPOCompanyMarketData{CIK: "0000000001", Ticker: "PEND", LifecycleCheckedAt: ptrTime(now)}).Error; err != nil {
		t.Fatalf("seed pending listing mapping: %v", err)
	}
	if err := db.Create(&model.IPOCompanyMarketData{CIK: "0000000002", LifecycleCheckedAt: ptrTime(now.Add(-25 * time.Hour))}).Error; err != nil {
		t.Fatalf("seed stale lifecycle mapping: %v", err)
	}
	if err := db.Create(&model.IPOOfferingEvent{FilingID: "unsupported-424b4", CIK: "0000000002", CompanyName: "Stale Lifecycle", OfferingType: "unknown", ParseStatus: "unsupported", FilingDate: now}).Error; err != nil {
		t.Fatalf("seed unsupported offering: %v", err)
	}
	if err := db.Create(&[]model.NotificationBatch{
		{Source: "ipo", Trigger: "ipo_scheduler", Channel: "telegram", Status: "failed", ItemCount: 1, FailedCount: 1, NextRetryAt: ptrTime(now.Add(-time.Minute))},
		{Source: "ipo", Trigger: "ipo_scheduler", Channel: "telegram", Status: "dead_letter", ItemCount: 1, FailedCount: 1},
	}).Error; err != nil {
		t.Fatalf("seed notification batches: %v", err)
	}
	if err := db.Create(&model.SyncRun{StartedAt: now, Status: "success", Trigger: "ipo_scheduler", NewFilings: 2}).Error; err != nil {
		t.Fatalf("seed IPO sync run: %v", err)
	}

	health, err := NewIPORadarService(db, &fakeSECClient{}, &fakeNotifier{}, configs).Health(context.Background(), now)
	if err != nil {
		t.Fatalf("IPO health: %v", err)
	}
	if health.PendingListing != 1 || health.MissingMarketMapping != 1 || health.StaleLifecycleChecks != 1 || health.UnsupportedOfferingEvents != 1 || health.DueRetryBatches != 1 || health.DeadLetterBatches != 1 {
		t.Fatalf("health = %+v", health)
	}
	if health.LatestSync == nil || health.LatestSync.Status != "success" || !health.LatestSync.StartedAt.Equal(now) || health.LatestSync.NewFilings != 2 {
		t.Fatalf("latest IPO sync = %+v", health.LatestSync)
	}
	actionKeys := make([]string, 0, len(health.Actions))
	for _, action := range health.Actions {
		actionKeys = append(actionKeys, action.Key)
	}
	wantActions := []string{"review_dead_letters", "retry_notifications", "verify_listing", "complete_market_mapping", "recheck_lifecycle", "review_offering_parse"}
	if !reflect.DeepEqual(actionKeys, wantActions) {
		t.Fatalf("IPO health actions = %v, want %v", actionKeys, wantActions)
	}
}

func TestIPOCompanyAttentionFilterReturnsPendingListing(t *testing.T) {
	now := time.Date(2026, 7, 12, 9, 0, 0, 0, time.UTC)
	db := testDB(t)
	if err := db.Create(&[]model.IPOFiling{
		{FilingID: "pending-s1", CIK: "0000000001", CompanyName: "Pending Listing", FilingType: "S-1", FilingDate: now.AddDate(0, 0, -1)},
		{FilingID: "pending-424b4", CIK: "0000000001", CompanyName: "Pending Listing", FilingType: "424B4", FilingDate: now},
		{FilingID: "new-s1", CIK: "0000000002", CompanyName: "New Registration", FilingType: "S-1", FilingDate: now},
	}).Error; err != nil {
		t.Fatalf("seed IPO filings: %v", err)
	}
	if err := db.Create(&model.IPOCompanyMarketData{CIK: "0000000001", Ticker: "PEND"}).Error; err != nil {
		t.Fatalf("seed pending listing mapping: %v", err)
	}

	page, err := NewIPORadarService(db, &fakeSECClient{}, &fakeNotifier{}, NewConfigService(db, NewAuditService(db))).ListCompanies(context.Background(), IPOCompanyFilter{Attention: "listing_pending", Page: 1, PageSize: 10}, now)
	if err != nil {
		t.Fatalf("list attention queue: %v", err)
	}
	if page.Total != 1 || len(page.Items) != 1 || page.Items[0].CIK != "0000000001" || page.Items[0].Status != "listing_pending" {
		t.Fatalf("attention results = %+v", page)
	}
}

func TestIPOCompanyTickerFilterMatchesAllTickerSources(t *testing.T) {
	now := time.Date(2026, 8, 8, 9, 0, 0, 0, time.UTC)
	db := testDB(t)
	if err := db.Create(&[]model.IPOFiling{
		{FilingID: "auto-s1", CIK: "0000000001", CompanyName: "Automatic Ticker", FilingType: "S-1", FilingDate: now},
		{FilingID: "manual-s1", CIK: "0000000002", CompanyName: "Manual Ticker", FilingType: "S-1", FilingDate: now},
		{FilingID: "watch-s1", CIK: "0000000003", CompanyName: "Watch Ticker", FilingType: "S-1", FilingDate: now},
	}).Error; err != nil {
		t.Fatalf("seed IPO filings: %v", err)
	}
	if err := db.Create(&[]model.IPOCompanyMarketData{
		{CIK: "0000000001", Ticker: "AUTO"},
		{CIK: "0000000002", Ticker: "SECT"},
	}).Error; err != nil {
		t.Fatalf("seed market data: %v", err)
	}
	if err := db.Create(&model.IPOCompanyOverride{CIK: "0000000002", FinalTicker: "MAN"}).Error; err != nil {
		t.Fatalf("seed manual ticker: %v", err)
	}
	if err := db.Create(&model.WatchTarget{CIK: "0000000003", Ticker: "WATCH", CompanyName: "Watch Ticker", TargetType: "stock", Status: "enabled"}).Error; err != nil {
		t.Fatalf("seed watch ticker: %v", err)
	}
	svc := NewIPORadarService(db, &fakeSECClient{}, &fakeNotifier{}, NewConfigService(db, NewAuditService(db)))
	for _, tt := range []struct {
		filter string
		want   string
	}{
		{filter: "auto.us", want: "0000000001"},
		{filter: "$man", want: "0000000002"},
		{filter: "WATCH", want: "0000000003"},
		{filter: "MISSING", want: ""},
	} {
		page, err := svc.ListCompanies(context.Background(), IPOCompanyFilter{Ticker: tt.filter, Page: 1, PageSize: 10}, now)
		if err != nil {
			t.Fatalf("ListCompanies(%q): %v", tt.filter, err)
		}
		if tt.want == "" {
			if page.Total != 0 || len(page.Items) != 0 {
				t.Fatalf("ticker %q results = %+v, want none", tt.filter, page)
			}
			continue
		}
		if page.Total != 1 || len(page.Items) != 1 || page.Items[0].CIK != tt.want {
			t.Fatalf("ticker %q results = %+v, want CIK %s", tt.filter, page, tt.want)
		}
	}
}

func TestIPOCompanyAttentionFilterReturnsMissingMarketMapping(t *testing.T) {
	now := time.Date(2026, 7, 12, 9, 0, 0, 0, time.UTC)
	db := testDB(t)
	if err := db.Create(&[]model.IPOFiling{
		{FilingID: "unmapped-s1", CIK: "0000000011", CompanyName: "Unmapped IPO", FilingType: "S-1", FilingDate: now},
		{FilingID: "unmapped-effect", CIK: "0000000011", CompanyName: "Unmapped IPO", FilingType: "EFFECT", FilingDate: now},
		{FilingID: "mapped-s1", CIK: "0000000012", CompanyName: "Mapped IPO", FilingType: "S-1", FilingDate: now},
		{FilingID: "mapped-effect", CIK: "0000000012", CompanyName: "Mapped IPO", FilingType: "EFFECT", FilingDate: now},
	}).Error; err != nil {
		t.Fatalf("seed IPO filings: %v", err)
	}
	if err := db.Create(&model.IPOCompanyMarketData{CIK: "0000000012", Ticker: "MAPD"}).Error; err != nil {
		t.Fatalf("seed IPO market mapping: %v", err)
	}

	page, err := NewIPORadarService(db, &fakeSECClient{}, &fakeNotifier{}, NewConfigService(db, NewAuditService(db))).ListCompanies(context.Background(), IPOCompanyFilter{Attention: "missing_market_mapping", Page: 1, PageSize: 10}, now)
	if err != nil {
		t.Fatalf("list missing market mappings: %v", err)
	}
	if page.Total != 1 || len(page.Items) != 1 || page.Items[0].CIK != "0000000011" {
		t.Fatalf("missing market mapping results = %+v", page)
	}
}

func TestIPOCompanyAttentionFiltersUseExactSignals(t *testing.T) {
	now := time.Date(2026, 7, 12, 9, 0, 0, 0, time.UTC)
	db := testDB(t)
	configs := NewConfigService(db, NewAuditService(db))
	if err := configs.EnsureDefaults(context.Background()); err != nil {
		t.Fatalf("ensure defaults: %v", err)
	}
	filings := []model.IPOFiling{
		{FilingID: "pending-s1", CIK: "0000000001", CompanyName: "Pending", FilingType: "S-1", FilingDate: now.Add(-time.Hour)},
		{FilingID: "pending-424b4", CIK: "0000000001", CompanyName: "Pending", FilingType: "424B4", FilingDate: now},
		{FilingID: "parse-s1", CIK: "0000000002", CompanyName: "Parse", FilingType: "S-1", FilingDate: now},
		{FilingID: "stale-s1", CIK: "0000000003", CompanyName: "Stale", FilingType: "S-1", FilingDate: now},
		{FilingID: "notify-s1", CIK: "0000000004", CompanyName: "Notify", FilingType: "S-1", FilingDate: now},
	}
	if err := db.Create(&filings).Error; err != nil {
		t.Fatalf("seed IPO filings: %v", err)
	}
	if err := db.Create(&[]model.IPOCompanyMarketData{
		{CIK: "0000000001", Ticker: "PEND", LifecycleCheckedAt: ptrTime(now)},
		{CIK: "0000000002", Ticker: "PARSE", Exchange: "Nasdaq", ListedVerifiedAt: ptrTime(now), LifecycleCheckedAt: ptrTime(now)},
		{CIK: "0000000004", Ticker: "NOTIFY", Exchange: "Nasdaq", ListedVerifiedAt: ptrTime(now), LifecycleCheckedAt: ptrTime(now)},
	}).Error; err != nil {
		t.Fatalf("seed market data: %v", err)
	}
	if err := db.Create(&model.IPOOfferingEvent{FilingID: "parse-424b4", CIK: "0000000002", CompanyName: "Parse", OfferingType: "unknown", ParseStatus: "unsupported", FilingDate: now}).Error; err != nil {
		t.Fatalf("seed unsupported offering: %v", err)
	}
	batch := model.NotificationBatch{Source: "ipo", Trigger: "ipo_scheduler", Channel: "telegram", Status: "dead_letter", ItemCount: 1, FailedCount: 1}
	if err := db.Create(&batch).Error; err != nil {
		t.Fatalf("seed dead-letter batch: %v", err)
	}
	if err := db.Create(&model.NotificationBatchItem{BatchID: batch.ID, EntityKind: "ipo_filing", FilingID: "notify-s1", CIK: "0000000004", CompanyName: "Notify", FilingType: "S-1", EventAt: now, Status: "failed", Reason: "delivery_failed"}).Error; err != nil {
		t.Fatalf("seed dead-letter item: %v", err)
	}
	svc := NewIPORadarService(db, &fakeSECClient{}, &fakeNotifier{}, configs)
	for attention, wantCIK := range map[string]string{
		"listing_pending":     "0000000001",
		"parse_failed":        "0000000002",
		"lifecycle_stale":     "0000000003",
		"notification_failed": "0000000004",
	} {
		page, err := svc.ListCompanies(context.Background(), IPOCompanyFilter{Attention: attention, Page: 1, PageSize: 10}, now)
		if err != nil || page.Total != 1 || len(page.Items) != 1 || page.Items[0].CIK != wantCIK {
			t.Fatalf("attention=%s page=%+v err=%v", attention, page, err)
		}
	}
}

func ptrTime(value time.Time) *time.Time {
	return &value
}

func TestWatchTargetServiceCreatesListsUpdatesAndAuditsTargets(t *testing.T) {
	db := testDB(t)
	audit := NewAuditService(db)
	svc := NewWatchTargetService(db, audit)

	created, err := svc.Create(context.Background(), WatchTargetInput{
		Ticker:      "aapl",
		CompanyName: "Apple Inc.",
		CIK:         "0000320193",
		TargetType:  "stock",
		Group:       "EV",
		Status:      "enabled",
	}, "tester")
	if err != nil {
		t.Fatalf("create target: %v", err)
	}
	if created.Ticker != "AAPL" {
		t.Fatalf("ticker normalized = %q, want AAPL", created.Ticker)
	}
	if created.Group != "EV" {
		t.Fatalf("group = %q, want EV", created.Group)
	}

	updated, err := svc.SetStatus(context.Background(), created.ID, "disabled", "tester")
	if err != nil {
		t.Fatalf("set status: %v", err)
	}
	if updated.Status != "disabled" {
		t.Fatalf("status = %q, want disabled", updated.Status)
	}

	page, err := svc.List(context.Background(), WatchTargetFilter{Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("list targets: %v", err)
	}
	if page.Total != 1 || len(page.Items) != 1 {
		t.Fatalf("target page total=%d len=%d, want one target", page.Total, len(page.Items))
	}
	groupPage, err := svc.List(context.Background(), WatchTargetFilter{Group: "EV", Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("list target group: %v", err)
	}
	if groupPage.Total != 1 {
		t.Fatalf("group page total = %d, want 1", groupPage.Total)
	}

	logs, err := audit.List(context.Background(), AuditLogFilter{Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("list audit logs: %v", err)
	}
	if logs.Total != 2 {
		t.Fatalf("audit total = %d, want create and status update logs", logs.Total)
	}
}

func TestWatchTargetServiceListsOnlyUpcomingEarnings(t *testing.T) {
	db := testDB(t)
	svc := NewWatchTargetService(db, NewAuditService(db))
	now := time.Now().UTC().Truncate(24 * time.Hour)

	upcoming := model.WatchTarget{Ticker: "UPCOMING", CompanyName: "Upcoming Inc.", TargetType: "stock", Status: "enabled"}
	past := model.WatchTarget{Ticker: "PAST", CompanyName: "Past Inc.", TargetType: "stock", Status: "enabled"}
	if err := db.Create(&upcoming).Error; err != nil {
		t.Fatalf("create upcoming target: %v", err)
	}
	if err := db.Create(&past).Error; err != nil {
		t.Fatalf("create past target: %v", err)
	}
	if err := db.Create(&model.EarningsPreview{TargetID: upcoming.ID, Ticker: upcoming.Ticker, Provider: "test", Status: "scheduled", ReportAt: ptrTime(now.Add(24 * time.Hour))}).Error; err != nil {
		t.Fatalf("create upcoming earnings preview: %v", err)
	}
	if err := db.Create(&model.EarningsPreview{TargetID: past.ID, Ticker: past.Ticker, Provider: "test", Status: "scheduled", ReportAt: ptrTime(now.Add(-24 * time.Hour))}).Error; err != nil {
		t.Fatalf("create past earnings preview: %v", err)
	}

	page, err := svc.List(context.Background(), WatchTargetFilter{UpcomingEarnings: true, Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("list upcoming earnings: %v", err)
	}
	if page.Total != 1 || len(page.Items) != 1 || page.Items[0].Ticker != "UPCOMING" {
		t.Fatalf("upcoming earnings page = %+v, want only UPCOMING", page)
	}
}

func TestFilingNotificationReasonTableDriven(t *testing.T) {
	now := time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)
	previous := now.Add(-2 * time.Hour)
	tests := []struct {
		name         string
		filing       model.Filing
		previousSync *time.Time
		settings     NotificationSettings
		at           time.Time
		want         string
	}{
		{name: "first sync", filing: model.Filing{FilingType: "8-K", FilingDate: now}, want: "initial_sync"},
		{name: "published before previous sync", filing: model.Filing{FilingType: "8-K", FilingDate: now, PublishedAt: ptrTime(previous.Add(-time.Minute))}, previousSync: &previous, want: "history_backfill"},
		{name: "same day without publication remains eligible", filing: model.Filing{FilingType: "8-K", FilingDate: time.Date(2026, 6, 20, 0, 0, 0, 0, time.UTC)}, previousSync: &previous, want: "eligible"},
		{name: "older date without publication is history", filing: model.Filing{FilingType: "8-K", FilingDate: time.Date(2026, 6, 19, 0, 0, 0, 0, time.UTC)}, previousSync: &previous, want: "history_backfill"},
		{name: "form rule filters", filing: model.Filing{FilingType: "10-Q", FilingDate: now, PublishedAt: ptrTime(now)}, previousSync: &previous, settings: NotificationSettings{FilingTypes: []string{"8-K"}}, want: "rule_filtered"},
		{name: "quiet hours filter", filing: model.Filing{FilingType: "8-K", FilingDate: now, PublishedAt: ptrTime(now)}, previousSync: &previous, settings: NotificationSettings{QuietHoursEnabled: true, QuietHoursStart: "11:00", QuietHoursEnd: "13:00"}, at: now, want: "quiet_hours"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			at := tt.at
			if at.IsZero() {
				at = now
			}
			if got := filingNotificationReason(tt.filing, tt.previousSync, tt.settings, at); got != tt.want {
				t.Fatalf("reason = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestConfigServicePersistsAndMasksTelegramToken(t *testing.T) {
	db := testDB(t)
	audit := NewAuditService(db)
	svc := NewConfigService(db, audit)

	if err := svc.UpsertMany(context.Background(), []ConfigInput{
		{Key: "telegram.bot_token", Value: "123456:secret-token", ValueType: "string", Category: "telegram", Encrypted: true},
		{Key: "telegram.chat_id", Value: "10001", ValueType: "string", Category: "telegram"},
		{Key: "telegram.enabled", Value: "true", ValueType: "bool", Category: "telegram"},
	}, "tester"); err != nil {
		t.Fatalf("upsert configs: %v", err)
	}

	configs, err := svc.List(context.Background(), "telegram", true)
	if err != nil {
		t.Fatalf("list configs: %v", err)
	}
	if len(configs) != 3 {
		t.Fatalf("config len = %d, want 3", len(configs))
	}
	for _, cfg := range configs {
		if cfg.ConfigKey == "telegram.bot_token" && cfg.ConfigValue == "123456:secret-token" {
			t.Fatalf("bot token was not masked")
		}
	}
}

func TestConfigServiceRedactsEncryptedValuesInAuditPersistenceAndReads(t *testing.T) {
	db := testDB(t)
	svc := NewConfigService(db, NewAuditService(db))
	const token = "123456:audit-secret"

	if err := svc.UpsertMany(context.Background(), []ConfigInput{{
		Key: "telegram.bot_token", Value: token, ValueType: "string", Category: "telegram", Encrypted: true,
	}}, "tester"); err != nil {
		t.Fatalf("upsert config: %v", err)
	}

	var persisted model.OperationLog
	if err := db.Where("object_type = ?", "system_config").First(&persisted).Error; err != nil {
		t.Fatalf("load operation log: %v", err)
	}
	if strings.Contains(persisted.AfterData, token) || !strings.Contains(persisted.AfterData, maskedSecretMarker) {
		t.Fatalf("persisted audit after_data = %q", persisted.AfterData)
	}

	page, err := NewAuditService(db).List(context.Background(), AuditLogFilter{Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("list operation logs: %v", err)
	}
	if len(page.Items) != 1 || strings.Contains(page.Items[0].AfterData, token) {
		t.Fatalf("listed operation logs = %#v", page.Items)
	}
}

func TestConfigServiceEncryptsMigratesAndMasksSecrets(t *testing.T) {
	db := testDB(t)
	t.Setenv("CONFIG_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32)))
	if err := db.Create(&model.SystemConfig{
		ConfigKey: "telegram.bot_token", ConfigValue: "legacy-telegram-token", ValueType: "string", Category: "telegram", Encrypted: true,
	}).Error; err != nil {
		t.Fatalf("seed plaintext encrypted config: %v", err)
	}

	svc := NewConfigService(db, NewAuditService(db), config.Load().System)
	if err := svc.MigrateEncryptedValues(context.Background()); err != nil {
		t.Fatalf("migrate encrypted values: %v", err)
	}

	var stored model.SystemConfig
	if err := db.Where("config_key = ?", "telegram.bot_token").First(&stored).Error; err != nil {
		t.Fatalf("load encrypted config: %v", err)
	}
	if !strings.HasPrefix(stored.ConfigValue, "enc:v1:") {
		t.Fatalf("stored value is not encrypted")
	}
	value, ok, err := svc.GetValue(context.Background(), "telegram.bot_token")
	if err != nil || !ok || value != "legacy-telegram-token" {
		t.Fatalf("GetValue ok=%v err=%v", ok, err)
	}
	configs, err := svc.List(context.Background(), "telegram", true)
	if err != nil {
		t.Fatalf("list encrypted configs: %v", err)
	}
	if len(configs) != 1 || !strings.Contains(configs[0].ConfigValue, maskedSecretMarker) {
		t.Fatalf("masked configs = %#v", configs)
	}
}

func TestConfigServiceRejectsNewEncryptedValueWithoutKey(t *testing.T) {
	db := testDB(t)
	t.Setenv("CONFIG_ENCRYPTION_KEY", "")
	svc := NewConfigService(db, NewAuditService(db), config.Load().System)

	err := svc.UpsertMany(context.Background(), []ConfigInput{
		{Key: "telegram.bot_token", Value: "new-telegram-token", ValueType: "string", Category: "telegram", Encrypted: true},
	}, "tester")
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("UpsertMany error = %v", err)
	}
	if svc.EncryptionHealth().Status != "critical" {
		t.Fatal("expected critical encryption health")
	}
}

func TestConfigServiceClassifiesKnownSecretsAndExistingEncryptedRowsServerSide(t *testing.T) {
	db := testDB(t)
	t.Setenv("CONFIG_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{2}, 32)))
	svc := NewConfigService(db, NewAuditService(db), config.Load().System)

	if err := db.Create(&model.SystemConfig{
		ConfigKey: "custom.legacy_secret", ConfigValue: "old-value", ValueType: "string", Category: "custom", Encrypted: true,
	}).Error; err != nil {
		t.Fatalf("seed encrypted config: %v", err)
	}
	inputs := []ConfigInput{
		{Key: "telegram.bot_token", Value: "telegram-token", ValueType: "string", Category: "telegram", Encrypted: false},
		{Key: "discovery.tiingo_api_token", Value: "tiingo-token", ValueType: "string", Category: "discovery", Encrypted: false},
		{Key: "custom.legacy_secret", Value: "updated-value", ValueType: "string", Category: "custom", Encrypted: false},
	}
	if err := svc.UpsertMany(context.Background(), inputs, "tester"); err != nil {
		t.Fatalf("UpsertMany: %v", err)
	}

	for _, key := range []string{"telegram.bot_token", "discovery.tiingo_api_token", "custom.legacy_secret"} {
		var stored model.SystemConfig
		if err := db.Where("config_key = ?", key).First(&stored).Error; err != nil {
			t.Fatalf("load %s: %v", key, err)
		}
		if !stored.Encrypted || !strings.HasPrefix(stored.ConfigValue, encryptedSecretPrefix) {
			t.Fatalf("stored %s = %+v, want encrypted ciphertext", key, stored)
		}
	}

	var audit model.OperationLog
	if err := db.Where("object_type = ?", "system_config").Order("id DESC").First(&audit).Error; err != nil {
		t.Fatalf("load audit record: %v", err)
	}
	for _, secret := range []string{"telegram-token", "tiingo-token", "updated-value"} {
		if strings.Contains(audit.AfterData, secret) {
			t.Fatalf("audit leaked %q: %s", secret, audit.AfterData)
		}
	}
}

func TestMigrateEncryptedValuesSanitizesNotificationErrors(t *testing.T) {
	db := testDB(t)
	message := `Post "https://api.telegram.org/bot123:secret/sendMessage": timeout`
	if err := db.Create(&model.NotificationBatch{SyncRunID: 1, Source: "filing", Trigger: "manual", Channel: "telegram", Status: "failed", ErrorMessage: message}).Error; err != nil {
		t.Fatalf("seed notification batch: %v", err)
	}
	if err := db.Create(&model.NotificationLog{FilingID: "f1", Channel: "telegram", Status: "failed", ErrorMessage: message}).Error; err != nil {
		t.Fatalf("seed notification log: %v", err)
	}
	if err := db.Create(&model.OperationLog{
		Action: "update", ObjectType: "system_config", ObjectID: "batch",
		AfterData: `[{"key":"telegram.bot_token","value":"123:secret","value_type":"string","category":"telegram","encrypted":true}]`,
	}).Error; err != nil {
		t.Fatalf("seed operation log: %v", err)
	}

	if err := NewConfigService(db, NewAuditService(db)).MigrateEncryptedValues(context.Background()); err != nil {
		t.Fatalf("migrate encrypted values: %v", err)
	}
	var batch model.NotificationBatch
	var log model.NotificationLog
	var operation model.OperationLog
	if err := db.First(&batch).Error; err != nil {
		t.Fatalf("load batch: %v", err)
	}
	if err := db.First(&log).Error; err != nil {
		t.Fatalf("load log: %v", err)
	}
	if err := db.Where("object_type = ?", "system_config").First(&operation).Error; err != nil {
		t.Fatalf("load operation log: %v", err)
	}
	if strings.Contains(batch.ErrorMessage, "123:secret") || strings.Contains(log.ErrorMessage, "123:secret") || strings.Contains(operation.AfterData, "123:secret") {
		t.Fatal("stored error or operation log leaked telegram token")
	}
}

func TestSanitizeSensitiveErrorMasksTelegramURL(t *testing.T) {
	got := SanitizeSensitiveError(`Post "https://api.telegram.org/bot123:secret/sendMessage": timeout`)
	if strings.Contains(got, "123:secret") {
		t.Fatal("telegram token leaked")
	}
}

func TestConfigServicePreservesMaskedEncryptedValues(t *testing.T) {
	db := testDB(t)
	svc := NewConfigService(db, NewAuditService(db))
	if err := svc.UpsertMany(context.Background(), []ConfigInput{
		{Key: "discovery.tiingo_api_token", Value: "real-token", ValueType: "string", Category: "discovery", Encrypted: true},
	}, "tester"); err != nil {
		t.Fatalf("seed token: %v", err)
	}
	if err := svc.UpsertMany(context.Background(), []ConfigInput{
		{Key: "discovery.tiingo_api_token", Value: "rea******ken", ValueType: "string", Category: "discovery", Encrypted: true},
	}, "tester"); err != nil {
		t.Fatalf("update masked token: %v", err)
	}
	got, ok, err := svc.GetValue(context.Background(), "discovery.tiingo_api_token")
	if err != nil || !ok || got != "real-token" {
		t.Fatalf("token = %q ok=%v err=%v, want original real-token", got, ok, err)
	}
}

func TestConfigServiceApplyDiscoveryConfigTableDriven(t *testing.T) {
	tests := []struct {
		name    string
		inputs  []ConfigInput
		want    config.DiscoveryConfig
		wantErr bool
	}{
		{
			name: "applies stored discovery datasource values",
			inputs: []ConfigInput{
				{Key: "discovery.price_provider", Value: "Tiingo", ValueType: "string", Category: "discovery"},
				{Key: "discovery.stooq_urls", Value: "https://a.test/listed.zip, https://b.test/other.zip", ValueType: "string", Category: "discovery"},
				{Key: "discovery.tiingo_api_token", Value: "primary-token", ValueType: "string", Category: "discovery", Encrypted: true},
				{Key: "discovery.tiingo_api_tokens", Value: "token-a, token-b", ValueType: "string", Category: "discovery", Encrypted: true},
				{Key: "discovery.tiingo_base_url", Value: "https://tiingo.test", ValueType: "string", Category: "discovery"},
				{Key: "discovery.tiingo_request_budget", Value: "12", ValueType: "int", Category: "discovery"},
				{Key: "discovery.twelve_data_api_key", Value: "twelve-key", ValueType: "string", Category: "discovery", Encrypted: true},
				{Key: "discovery.twelve_data_base_url", Value: "https://twelve.test", ValueType: "string", Category: "discovery"},
				{Key: "discovery.twelve_data_request_budget", Value: "34", ValueType: "int", Category: "discovery"},
				{Key: "discovery.twelve_data_request_interval_ms", Value: "1500", ValueType: "int", Category: "discovery"},
				{Key: "discovery.yahoo_base_url", Value: "https://yahoo.test", ValueType: "string", Category: "discovery"},
				{Key: "discovery.yahoo_request_budget", Value: "56", ValueType: "int", Category: "discovery"},
				{Key: "discovery.min_publish_coverage_pct", Value: "85.5", ValueType: "float", Category: "discovery"},
				{Key: "discovery.research_mode", Value: "false", ValueType: "bool", Category: "discovery"},
				{Key: "discovery.auto_technical_history_warmup", Value: "true", ValueType: "bool", Category: "discovery"},
				{Key: "discovery.task_timeout_minutes", Value: "75", ValueType: "int", Category: "discovery"},
				{Key: "discovery.download_idle_timeout_seconds", Value: "120", ValueType: "int", Category: "discovery"},
				{Key: "discovery.sec_bulk_cache_ttl_hours", Value: "10", ValueType: "int", Category: "discovery"},
			},
			want: config.DiscoveryConfig{
				PriceProvider:               "tiingo",
				StooqURLs:                   []string{"https://a.test/listed.zip", "https://b.test/other.zip"},
				TiingoAPIToken:              "primary-token",
				TiingoAPITokens:             []string{"token-a", "token-b"},
				TiingoBaseURL:               "https://tiingo.test",
				TiingoRequestBudget:         12,
				TwelveDataAPIKey:            "twelve-key",
				TwelveDataBaseURL:           "https://twelve.test",
				TwelveDataRequestBudget:     34,
				TwelveDataRequestIntervalMS: 1500,
				YahooBaseURL:                "https://yahoo.test",
				YahooRequestBudget:          56,
				MinPublishCoveragePct:       85.5,
				ResearchMode:                false,
				AutoTechnicalHistoryWarmup:  true,
				TaskTimeoutMin:              75,
				DownloadIdleTimeoutSec:      120,
				SECBulkCacheTTLHours:        10,
			},
		},
		{
			name: "ignores masked and invalid numeric discovery values",
			inputs: []ConfigInput{
				{Key: "discovery.tiingo_api_token", Value: "old-token", ValueType: "string", Category: "discovery", Encrypted: true},
				{Key: "discovery.tiingo_api_tokens", Value: "old-a,old-b", ValueType: "string", Category: "discovery", Encrypted: true},
				{Key: "discovery.twelve_data_api_key", Value: "old-twelve", ValueType: "string", Category: "discovery", Encrypted: true},
				{Key: "discovery.tiingo_request_budget", Value: "bad", ValueType: "int", Category: "discovery"},
				{Key: "discovery.twelve_data_request_budget", Value: "-1", ValueType: "int", Category: "discovery"},
				{Key: "discovery.twelve_data_request_interval_ms", Value: "0", ValueType: "int", Category: "discovery"},
				{Key: "discovery.yahoo_request_budget", Value: "-2", ValueType: "int", Category: "discovery"},
				{Key: "discovery.min_publish_coverage_pct", Value: "-1", ValueType: "float", Category: "discovery"},
				{Key: "discovery.research_mode", Value: "not-bool", ValueType: "bool", Category: "discovery"},
				{Key: "discovery.task_timeout_minutes", Value: "bad", ValueType: "int", Category: "discovery"},
				{Key: "discovery.download_idle_timeout_seconds", Value: "0", ValueType: "int", Category: "discovery"},
				{Key: "discovery.sec_bulk_cache_ttl_hours", Value: "-1", ValueType: "int", Category: "discovery"},
			},
			want: config.DiscoveryConfig{
				TiingoAPIToken:              "old-token",
				TiingoAPITokens:             []string{"old-a", "old-b"},
				TwelveDataAPIKey:            "old-twelve",
				TiingoRequestBudget:         7,
				TwelveDataRequestBudget:     8,
				TwelveDataRequestIntervalMS: 9,
				YahooRequestBudget:          10,
				MinPublishCoveragePct:       11,
				ResearchMode:                true,
				AutoTechnicalHistoryWarmup:  true,
				TaskTimeoutMin:              12,
				DownloadIdleTimeoutSec:      13,
				SECBulkCacheTTLHours:        14,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := testDB(t)
			svc := NewConfigService(db, NewAuditService(db))
			if err := svc.UpsertMany(context.Background(), tt.inputs, "tester"); err != nil {
				t.Fatalf("UpsertMany: %v", err)
			}
			got, err := svc.ApplyDiscoveryConfig(context.Background(), config.DiscoveryConfig{
				TiingoRequestBudget:         7,
				TwelveDataRequestBudget:     8,
				TwelveDataRequestIntervalMS: 9,
				YahooRequestBudget:          10,
				MinPublishCoveragePct:       11,
				ResearchMode:                true,
				AutoTechnicalHistoryWarmup:  true,
				TaskTimeoutMin:              12,
				DownloadIdleTimeoutSec:      13,
				SECBulkCacheTTLHours:        14,
			})
			if (err != nil) != tt.wantErr {
				t.Fatalf("ApplyDiscoveryConfig err=%v wantErr=%v", err, tt.wantErr)
			}
			if err != nil {
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("config = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestConfigServiceDefaultsTableDriven(t *testing.T) {
	tests := []struct {
		name string
		run  func(t *testing.T, db *gorm.DB, svc *ConfigService)
	}{
		{name: "ensure sec fetch defaults is idempotent", run: func(t *testing.T, db *gorm.DB, svc *ConfigService) {
			if err := svc.EnsureDefaults(context.Background()); err != nil {
				t.Fatalf("EnsureDefaults: %v", err)
			}
			if err := svc.EnsureDefaults(context.Background()); err != nil {
				t.Fatalf("EnsureDefaults second: %v", err)
			}
			configs, err := svc.List(context.Background(), "sec", false)
			if err != nil {
				t.Fatalf("List: %v", err)
			}
			if len(configs) != 5 {
				t.Fatalf("sec defaults = %d, want 5", len(configs))
			}
			settings, err := svc.SECFetchSettings(context.Background())
			if err != nil {
				t.Fatalf("SECFetchSettings: %v", err)
			}
			if settings.InitialFetchDays != 30 || settings.SyncWindowDays != 30 || settings.MaxFetchCount != 300 || settings.FetchFullHistory {
				t.Fatalf("settings = %+v", settings)
			}
		}},
		{name: "ensure ui defaults include locale and onboarding state", run: func(t *testing.T, db *gorm.DB, svc *ConfigService) {
			if err := svc.EnsureDefaults(context.Background()); err != nil {
				t.Fatalf("EnsureDefaults: %v", err)
			}
			if err := svc.EnsureDefaults(context.Background()); err != nil {
				t.Fatalf("EnsureDefaults second: %v", err)
			}
			configs, err := svc.List(context.Background(), "ui", false)
			if err != nil {
				t.Fatalf("List: %v", err)
			}
			values := map[string]string{}
			for _, cfg := range configs {
				values[cfg.ConfigKey] = cfg.ConfigValue
			}
			if values["ui.default_locale"] != "zh-CN" || values["ui.onboarding_completed"] != "false" {
				t.Fatalf("ui defaults = %+v", values)
			}
		}},
		{name: "ensure scheduler timezone default is valid", run: func(t *testing.T, db *gorm.DB, svc *ConfigService) {
			if err := svc.EnsureDefaults(context.Background()); err != nil {
				t.Fatalf("EnsureDefaults: %v", err)
			}
			location, name, err := svc.SchedulerTimezone(context.Background())
			if err != nil {
				t.Fatalf("SchedulerTimezone: %v", err)
			}
			if location == nil || name != "UTC" {
				t.Fatalf("scheduler timezone = %v %q, want UTC", location, name)
			}
		}},
		{name: "reject invalid scheduler timezone", run: func(t *testing.T, db *gorm.DB, svc *ConfigService) {
			if err := svc.UpsertMany(context.Background(), []ConfigInput{{Key: "scheduler.timezone", Value: "Not/AZone", ValueType: "string", Category: "scheduler"}}, "tester"); err == nil {
				t.Fatalf("expected invalid timezone error")
			}
		}},
		{name: "ensure notification defaults are usable", run: func(t *testing.T, db *gorm.DB, svc *ConfigService) {
			if err := svc.EnsureDefaults(context.Background()); err != nil {
				t.Fatalf("EnsureDefaults: %v", err)
			}
			settings, err := svc.NotificationSettings(context.Background())
			if err != nil {
				t.Fatalf("NotificationSettings: %v", err)
			}
			if settings.ImportantOnly || settings.QuietHoursEnabled || settings.QuietHoursStart != "22:00" || settings.QuietHoursEnd != "08:00" {
				t.Fatalf("settings = %+v", settings)
			}
		}},
		{name: "ensure ipo radar defaults are usable", run: func(t *testing.T, db *gorm.DB, svc *ConfigService) {
			if err := svc.EnsureDefaults(context.Background()); err != nil {
				t.Fatalf("EnsureDefaults: %v", err)
			}
			settings, err := svc.IPORadarSettings(context.Background())
			if err != nil {
				t.Fatalf("IPORadarSettings: %v", err)
			}
			if !settings.Enabled || !settings.NotifyEnabled || settings.LookbackDays != 7 || settings.MaxResults != 100 || settings.LifecycleMaxCIKs != 50 || settings.LifecycleRecheckHours != 12 {
				t.Fatalf("settings = %+v", settings)
			}
			if len(settings.FormTypes) == 0 || settings.FormTypes[0] != "S-1" {
				t.Fatalf("form types = %+v", settings.FormTypes)
			}
		}},
		{name: "ensure candidate notification task default exists disabled", run: func(t *testing.T, db *gorm.DB, svc *ConfigService) {
			tasks := NewTaskConfigService(db, NewAuditService(db))
			if err := tasks.EnsureDefault(context.Background()); err != nil {
				t.Fatalf("EnsureDefault: %v", err)
			}
			var task model.TaskConfig
			if err := db.Where("task_name = ?", "candidate_notification_sync").First(&task).Error; err != nil {
				t.Fatalf("load candidate task: %v", err)
			}
			if task.Enabled || task.CronExpr != "15 8 * * 2-6" {
				t.Fatalf("task = %+v", task)
			}
		}},
		{name: "ensure candidate notification defaults are usable", run: func(t *testing.T, db *gorm.DB, svc *ConfigService) {
			if err := svc.EnsureDefaults(context.Background()); err != nil {
				t.Fatalf("EnsureDefaults: %v", err)
			}
			settings, err := svc.CandidateNotificationSettings(context.Background())
			if err != nil {
				t.Fatalf("CandidateNotificationSettings: %v", err)
			}
			if settings.Enabled || settings.ShadowMode || settings.NotifyA || settings.NotifyB || settings.SendTime != "09:30" || settings.MaxPerGrade != 5 || !settings.ActionableOnly || settings.MinReviewPriorityScore != 0 {
				t.Fatalf("settings = %+v", settings)
			}
			configs, err := svc.List(context.Background(), "candidate_notification", false)
			if err != nil {
				t.Fatalf("List: %v", err)
			}
			if len(configs) != 8 {
				t.Fatalf("candidate notification defaults = %d, want 8", len(configs))
			}
		}},
		{name: "ensure social heat defaults are usable", run: func(t *testing.T, db *gorm.DB, svc *ConfigService) {
			if err := svc.EnsureDefaults(context.Background()); err != nil {
				t.Fatalf("EnsureDefaults: %v", err)
			}
			settings, err := svc.SocialHeatSettings(context.Background())
			if err != nil {
				t.Fatalf("SocialHeatSettings: %v", err)
			}
			if settings.Enabled || settings.Provider != "manual" || settings.LookbackHours != 24 || settings.BaselineDays != 30 {
				t.Fatalf("settings = %+v", settings)
			}
			configs, err := svc.List(context.Background(), "social_heat", false)
			if err != nil {
				t.Fatalf("List: %v", err)
			}
			if len(configs) != 4 {
				t.Fatalf("social heat defaults = %d, want 4", len(configs))
			}
		}},
		{name: "social heat settings normalize invalid numbers", run: func(t *testing.T, db *gorm.DB, svc *ConfigService) {
			if err := svc.UpsertMany(context.Background(), []ConfigInput{
				{Key: "social_heat.enabled", Value: "true", ValueType: "bool", Category: "social_heat"},
				{Key: "social_heat.provider", Value: "", ValueType: "string", Category: "social_heat"},
				{Key: "social_heat.lookback_hours", Value: "0", ValueType: "int", Category: "social_heat"},
				{Key: "social_heat.baseline_days", Value: "-1", ValueType: "int", Category: "social_heat"},
			}, "tester"); err != nil {
				t.Fatalf("UpsertMany: %v", err)
			}
			settings, err := svc.SocialHeatSettings(context.Background())
			if err != nil {
				t.Fatalf("SocialHeatSettings: %v", err)
			}
			if !settings.Enabled || settings.Provider != "manual" || settings.LookbackHours != 24 || settings.BaselineDays != 30 {
				t.Fatalf("settings = %+v", settings)
			}
		}},
		{name: "ensure discovery datasource defaults are usable", run: func(t *testing.T, db *gorm.DB, svc *ConfigService) {
			if err := svc.EnsureDefaults(context.Background()); err != nil {
				t.Fatalf("EnsureDefaults: %v", err)
			}
			cfg := config.DiscoveryConfig{PriceProvider: "stooq", StooqURLs: []string{"https://env.example.test/stooq.csv"}, TiingoAPIToken: "env-token", TiingoBaseURL: "https://env.example.test", TiingoRequestBudget: 10, TwelveDataAPIKey: "env-td", TwelveDataBaseURL: "https://env-td.example.test", TwelveDataRequestBudget: 10, YahooBaseURL: "https://env-yahoo.example.test", YahooRequestBudget: 10, LongbridgeAppKey: "env-lb-key", LongbridgeAppSecret: "env-lb-secret", LongbridgeAccessToken: "env-lb-token", ResearchMode: false, MinPublishCoveragePct: 0}
			applied, err := svc.ApplyDiscoveryConfig(context.Background(), cfg)
			if err != nil {
				t.Fatalf("ApplyDiscoveryConfig: %v", err)
			}
			if applied.PriceProvider != "stooq" || len(applied.StooqURLs) != 1 || applied.StooqURLs[0] != "https://env.example.test/stooq.csv" || applied.TiingoAPIToken != "env-token" || applied.TiingoBaseURL != "https://api.tiingo.com" || applied.TiingoRequestBudget != 45 || applied.TwelveDataAPIKey != "env-td" || applied.TwelveDataBaseURL != "https://api.twelvedata.com" || applied.TwelveDataRequestBudget != 700 || applied.TwelveDataRequestIntervalMS != 8000 || applied.YahooBaseURL != "https://query1.finance.yahoo.com" || applied.YahooRequestBudget != 45 || applied.LongbridgeAppKey != "env-lb-key" || applied.LongbridgeAppSecret != "env-lb-secret" || applied.LongbridgeAccessToken != "env-lb-token" || !applied.LongbridgeCompanyProfileEnabled || applied.LongbridgeCompanyProfileRequestBudget != 20 || applied.LongbridgeCompanyProfileTTLDays != 30 || !applied.LongbridgeAnalystRatingEnabled || applied.LongbridgeAnalystRatingRequestBudget != 20 || applied.LongbridgeAnalystRatingTargetChangePct != 5 || !applied.LongbridgeCandidateResearchEnabled || applied.LongbridgeCandidateResearchRequestBudget != 5 || !applied.LongbridgeWatchTargetResearchEnabled || applied.LongbridgeWatchTargetResearchRequestBudget != 5 || !applied.LongbridgeCandidateValuationEnabled || applied.LongbridgeCandidateValuationRequestBudget != 3 || !applied.LongbridgeWatchTargetValuationEnabled || applied.LongbridgeWatchTargetValuationRequestBudget != 3 || !applied.LongbridgeOptionResearchEnabled || applied.LongbridgeCandidateOptionResearchBudget != 5 || applied.LongbridgeWatchTargetOptionResearchBudget != 5 || applied.MinPublishCoveragePct != 85 || !applied.ResearchMode || !applied.AutoTechnicalHistoryWarmup || applied.TaskTimeoutMin != 60 || applied.DownloadIdleTimeoutSec != 90 || applied.SECBulkCacheTTLHours != 12 || applied.CacheRetentionDays != 14 {
				t.Fatalf("applied defaults = %+v", applied)
			}
			configs, err := svc.List(context.Background(), "discovery", true)
			if err != nil {
				t.Fatalf("List: %v", err)
			}
			if len(configs) != 39 {
				t.Fatalf("discovery defaults = %d, want 39", len(configs))
			}
		}},
		{name: "stored twelve data config overrides env config", run: func(t *testing.T, db *gorm.DB, svc *ConfigService) {
			if err := svc.UpsertMany(context.Background(), []ConfigInput{
				{Key: "discovery.twelve_data_api_key", Value: "stored-td", ValueType: "string", Category: "discovery", Encrypted: true},
				{Key: "discovery.twelve_data_base_url", Value: "https://stored-td.example.test", ValueType: "string", Category: "discovery"},
				{Key: "discovery.twelve_data_request_budget", Value: "650", ValueType: "int", Category: "discovery"},
				{Key: "discovery.twelve_data_request_interval_ms", Value: "8500", ValueType: "int", Category: "discovery"},
			}, "tester"); err != nil {
				t.Fatalf("seed config: %v", err)
			}
			applied, err := svc.ApplyDiscoveryConfig(context.Background(), config.DiscoveryConfig{TwelveDataAPIKey: "env-td", TwelveDataBaseURL: "https://env.example.test", TwelveDataRequestBudget: 10})
			if err != nil {
				t.Fatalf("ApplyDiscoveryConfig: %v", err)
			}
			if applied.TwelveDataAPIKey != "stored-td" || applied.TwelveDataBaseURL != "https://stored-td.example.test" || applied.TwelveDataRequestBudget != 650 || applied.TwelveDataRequestIntervalMS != 8500 {
				t.Fatalf("twelve data config = %+v", applied)
			}
		}},
		{name: "stored stooq urls override env config", run: func(t *testing.T, db *gorm.DB, svc *ConfigService) {
			if err := svc.UpsertMany(context.Background(), []ConfigInput{
				{Key: "discovery.stooq_urls", Value: " https://stored.example.test/a.csv, https://stored.example.test/b.zip ", ValueType: "string", Category: "discovery"},
			}, "tester"); err != nil {
				t.Fatalf("seed config: %v", err)
			}
			applied, err := svc.ApplyDiscoveryConfig(context.Background(), config.DiscoveryConfig{StooqURLs: []string{"https://env.example.test/stooq.csv"}})
			if err != nil {
				t.Fatalf("ApplyDiscoveryConfig: %v", err)
			}
			want := []string{"https://stored.example.test/a.csv", "https://stored.example.test/b.zip"}
			if !reflect.DeepEqual(applied.StooqURLs, want) {
				t.Fatalf("stooq urls = %#v, want %#v", applied.StooqURLs, want)
			}
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := testDB(t)
			tt.run(t, db, NewConfigService(db, NewAuditService(db)))
		})
	}
}

func TestIPORadarServiceRefreshTableDriven(t *testing.T) {
	now := time.Now().UTC()
	tests := []struct {
		name            string
		configs         []ConfigInput
		secFilings      []sec.CurrentFilingResult
		notifierErr     []error
		seedBaseline    bool
		runTwice        bool
		wantNew         int
		wantNotify      int
		wantStored      int64
		wantBatchStatus string
		wantSuppressed  int
		wantFailed      int
	}{
		{
			name: "creates and sends one batch after baseline",
			configs: []ConfigInput{
				{Key: "telegram.enabled", Value: "true", ValueType: "bool", Category: "telegram"},
				{Key: "telegram.bot_token", Value: "token", ValueType: "string", Category: "telegram", Encrypted: true},
				{Key: "telegram.chat_id", Value: "10001", ValueType: "string", Category: "telegram"},
			},
			secFilings: []sec.CurrentFilingResult{{
				FilingID:        "0000000001-26-000001",
				AccessionNumber: "0000000001-26-000001",
				CIK:             "0000000001",
				CompanyName:     "Acme Space Inc.",
				FilingType:      "S-1",
				FilingDate:      now,
				FilingURL:       "https://www.sec.gov/acme/s1",
				Title:           "S-1 - Acme Space Inc.",
			}},
			seedBaseline:    true,
			wantNew:         1,
			wantNotify:      1,
			wantStored:      2,
			wantBatchStatus: "sent",
		},
		{
			name: "deduplicates existing filing on second refresh",
			configs: []ConfigInput{
				{Key: "ipo.notify_enabled", Value: "false", ValueType: "bool", Category: "ipo"},
			},
			secFilings: []sec.CurrentFilingResult{{
				FilingID:    "dup",
				CompanyName: "Duplicate Corp.",
				FilingType:  "F-1",
				FilingDate:  now,
				FilingURL:   "https://www.sec.gov/dup/f1",
				Title:       "F-1 - Duplicate Corp.",
			}},
			runTwice:        true,
			wantNew:         0,
			wantStored:      1,
			wantBatchStatus: "suppressed",
			wantSuppressed:  1,
		},
		{
			name: "filters old filing and keyword mismatch",
			configs: []ConfigInput{
				{Key: "ipo.lookback_days", Value: "1", ValueType: "int", Category: "ipo"},
				{Key: "ipo.keywords", Value: "biotech", ValueType: "string", Category: "ipo"},
			},
			secFilings: []sec.CurrentFilingResult{
				{FilingID: "old", CompanyName: "Old Biotech", FilingType: "S-1", FilingDate: now.AddDate(0, 0, -3), FilingURL: "https://www.sec.gov/old"},
				{FilingID: "keyword-miss", CompanyName: "Cloud Tools", FilingType: "S-1", FilingDate: now, FilingURL: "https://www.sec.gov/cloud"},
			},
			wantStored: 0,
		},
		{
			name: "records failed notification without failing refresh",
			configs: []ConfigInput{
				{Key: "telegram.enabled", Value: "true", ValueType: "bool", Category: "telegram"},
				{Key: "telegram.bot_token", Value: "token", ValueType: "string", Category: "telegram", Encrypted: true},
				{Key: "telegram.chat_id", Value: "10001", ValueType: "string", Category: "telegram"},
			},
			secFilings: []sec.CurrentFilingResult{{
				FilingID:    "notify-fail",
				CompanyName: "Fail Notify Inc.",
				FilingType:  "S-1",
				FilingDate:  now,
				FilingURL:   "https://www.sec.gov/fail",
			}},
			notifierErr:     []error{errors.New("telegram down"), errors.New("telegram down"), errors.New("telegram down")},
			seedBaseline:    true,
			wantNew:         1,
			wantStored:      2,
			wantBatchStatus: "failed",
			wantFailed:      1,
		},
		{
			name: "skips notification when ipo notify form type does not match",
			configs: []ConfigInput{
				{Key: "telegram.enabled", Value: "true", ValueType: "bool", Category: "telegram"},
				{Key: "telegram.bot_token", Value: "token", ValueType: "string", Category: "telegram", Encrypted: true},
				{Key: "telegram.chat_id", Value: "10001", ValueType: "string", Category: "telegram"},
				{Key: "ipo.notify_form_types", Value: "EFFECT,424B4", ValueType: "string", Category: "ipo"},
			},
			secFilings: []sec.CurrentFilingResult{{
				FilingID:    "notify-filtered",
				CompanyName: "Filtered Notify Inc.",
				FilingType:  "S-1",
				FilingDate:  now,
				FilingURL:   "https://www.sec.gov/filter",
			}},
			seedBaseline:    true,
			wantNew:         1,
			wantStored:      2,
			wantBatchStatus: "suppressed",
			wantSuppressed:  1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := testDB(t)
			configs := NewConfigService(db, NewAuditService(db))
			if err := configs.EnsureDefaults(context.Background()); err != nil {
				t.Fatalf("EnsureDefaults: %v", err)
			}
			if len(tt.configs) > 0 {
				if err := configs.UpsertMany(context.Background(), tt.configs, "tester"); err != nil {
					t.Fatalf("UpsertMany: %v", err)
				}
			}
			if tt.seedBaseline {
				if err := db.Create(&model.IPOFiling{FilingID: "baseline", CIK: "baseline", CompanyName: "Baseline Inc.", FilingType: "S-1", FilingDate: now.AddDate(0, 0, -1), FilingURL: "https://sec.test/baseline"}).Error; err != nil {
					t.Fatalf("seed baseline: %v", err)
				}
			}
			secClient := &fakeSECClient{currentFilings: tt.secFilings}
			notifier := &fakeNotifier{errs: tt.notifierErr}
			svc := NewIPORadarService(db, secClient, notifier, configs)
			got, err := svc.Refresh(context.Background())
			if err != nil {
				t.Fatalf("Refresh: %v", err)
			}
			if tt.runTwice {
				got, err = svc.Refresh(context.Background())
				if err != nil {
					t.Fatalf("Refresh second: %v", err)
				}
			}
			if got.NewFilings != tt.wantNew || got.Notified != tt.wantNotify {
				t.Fatalf("result = %+v, want new=%d notified=%d", got, tt.wantNew, tt.wantNotify)
			}
			var syncRun model.SyncRun
			if err := db.Order("id DESC").First(&syncRun).Error; err != nil {
				t.Fatalf("load sync run: %v", err)
			}
			if syncRun.Trigger != "ipo_manual" {
				t.Fatalf("sync run trigger = %q, want ipo_manual", syncRun.Trigger)
			}
			if syncRun.NewFilings != got.NewFilings || syncRun.Status != "success" {
				t.Fatalf("sync run = %+v, want result counts and success", syncRun)
			}
			var stored int64
			if err := db.Model(&model.IPOFiling{}).Count(&stored).Error; err != nil {
				t.Fatalf("count ipo filings: %v", err)
			}
			if stored != tt.wantStored {
				t.Fatalf("stored = %d, want %d", stored, tt.wantStored)
			}
			if tt.wantBatchStatus != "" {
				var batch model.NotificationBatch
				if err := db.Order("id DESC").First(&batch).Error; err != nil {
					t.Fatalf("load notification batch: %v", err)
				}
				if batch.Status != tt.wantBatchStatus || batch.SuppressedCount != tt.wantSuppressed || batch.FailedCount != tt.wantFailed {
					t.Fatalf("batch = %+v, want status=%s suppressed=%d failed=%d", batch, tt.wantBatchStatus, tt.wantSuppressed, tt.wantFailed)
				}
			}
		})
	}
}

func TestIPONotificationReasonTableDriven(t *testing.T) {
	filing := model.IPOFiling{FilingType: "S-1"}
	tests := []struct {
		name     string
		baseline bool
		backfill bool
		settings IPORadarSettings
		want     string
	}{
		{name: "initial baseline", baseline: true, settings: IPORadarSettings{NotifyEnabled: true}, want: "initial_sync"},
		{name: "lifecycle backfill", backfill: true, settings: IPORadarSettings{NotifyEnabled: true}, want: "lifecycle_backfill"},
		{name: "lifecycle reason wins during initial baseline", baseline: true, backfill: true, settings: IPORadarSettings{NotifyEnabled: true}, want: "lifecycle_backfill"},
		{name: "notifications disabled", settings: IPORadarSettings{}, want: "rule_filtered"},
		{name: "form type filtered", settings: IPORadarSettings{NotifyEnabled: true, NotifyFormTypes: []string{"424B4"}}, want: "rule_filtered"},
		{name: "eligible current filing", settings: IPORadarSettings{NotifyEnabled: true}, want: "eligible"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ipoNotificationReason(filing, tt.settings, tt.baseline, tt.backfill); got != tt.want {
				t.Fatalf("reason = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestIPORadarMarketDataMergeTableDriven(t *testing.T) {
	now := time.Now().UTC()
	tests := []struct {
		name         string
		override     *IPOCompanyOverrideInput
		wantStatus   string
		wantTicker   string
		wantExchange string
		wantPrice    string
		wantShares   int64
		wantSource   string
	}{
		{name: "uses SEC listed mapping and 424B4 offering", wantStatus: "listed", wantTicker: "ACME", wantExchange: "Nasdaq", wantPrice: "15.00", wantShares: 10000000, wantSource: "sec"},
		{name: "manual fields override automatic values", override: &IPOCompanyOverrideInput{StatusOverride: "priced", FinalTicker: "MAN", Exchange: "NYSE", OfferPrice: "20.00", SharesOffered: 2500000, ListingDate: "2026-07-01"}, wantStatus: "priced", wantTicker: "MAN", wantExchange: "NYSE", wantPrice: "20.00", wantShares: 2500000, wantSource: "manual"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := testDB(t)
			if err := db.Create(&model.IPOFiling{FilingID: "acme-s1", CIK: "0000000001", CompanyName: "Acme Inc.", FilingType: "S-1", FilingDate: now.AddDate(0, 0, -7), FilingURL: "https://sec.test/acme-s1"}).Error; err != nil {
				t.Fatalf("seed registration filing: %v", err)
			}
			configs := NewConfigService(db, NewAuditService(db))
			if err := configs.EnsureDefaults(context.Background()); err != nil {
				t.Fatalf("defaults: %v", err)
			}
			if err := configs.UpsertMany(context.Background(), []ConfigInput{{Key: "ipo.notify_enabled", Value: "false", ValueType: "bool", Category: "ipo"}}, "tester"); err != nil {
				t.Fatalf("disable notifications: %v", err)
			}
			url := "https://sec.test/acme-424b4"
			secClient := &fakeSECClient{
				currentFilings:  []sec.CurrentFilingResult{{FilingID: "acme-424b4", CIK: "0000000001", CompanyName: "Acme Inc.", FilingType: "424B4", FilingDate: now, FilingURL: url}},
				listedCompanies: []sec.ListedCompany{{CIK: "0000000001", Name: "Acme Inc.", Ticker: "ACME", Exchange: "Nasdaq"}},
				documents:       map[string]string{url: "We are offering 10,000,000 ordinary shares. The public offering price is $15.00 per share."},
			}
			svc := NewIPORadarService(db, secClient, &fakeNotifier{}, configs)
			if _, err := svc.Refresh(context.Background()); err != nil {
				t.Fatalf("Refresh: %v", err)
			}
			if tt.override != nil {
				if _, err := svc.UpsertCompanyOverride(context.Background(), "0000000001", *tt.override); err != nil {
					t.Fatalf("UpsertCompanyOverride: %v", err)
				}
			}
			page, err := svc.ListCompanies(context.Background(), IPOCompanyFilter{IncludeEnded: true, Page: 1, PageSize: 10}, now)
			if err != nil || len(page.Items) != 1 {
				t.Fatalf("ListCompanies page=%+v err=%v", page, err)
			}
			item := page.Items[0]
			if item.Status != tt.wantStatus || item.FinalTicker != tt.wantTicker || item.Exchange != tt.wantExchange || item.OfferPrice != tt.wantPrice || item.SharesOffered != tt.wantShares || item.MarketDataSource != tt.wantSource {
				t.Fatalf("item = %+v", item)
			}
			if item.AutomaticTicker != "ACME" || item.AutomaticExchange != "Nasdaq" || item.AutomaticOfferPrice != "15.00" || item.AutomaticShares != 10000000 {
				t.Fatalf("automatic market data = %+v", item)
			}
			if tt.override != nil && (item.OverrideFinalTicker != "MAN" || item.OverrideExchange != "NYSE" || item.OverrideOfferPrice != "20.00" || item.OverrideShares != 2500000 || item.OverrideListingDate == nil) {
				t.Fatalf("override market data = %+v", item)
			}
		})
	}
}

func TestIPORadarListingConfirmationTableDriven(t *testing.T) {
	now := time.Date(2026, 6, 21, 8, 30, 0, 0, time.UTC)
	tests := []struct {
		name            string
		listedCompanies []sec.ListedCompany
		seedMarket      *model.IPOCompanyMarketData
		wantStatus      string
		wantTicker      string
		wantVerified    bool
	}{
		{
			name:            "ticker without exchange is listing pending",
			listedCompanies: []sec.ListedCompany{{CIK: "0002112466", Name: "Silentium Ltd.", Ticker: "SIAI"}},
			wantStatus:      "listing_pending", wantTicker: "SIAI",
		},
		{
			name:       "missing SEC mapping clears stale listing confirmation",
			seedMarket: &model.IPOCompanyMarketData{CIK: "0002112466", Ticker: "SIAI", Exchange: "Nasdaq", ListedVerifiedAt: &now, TickerSource: "sec", TickerConfidence: "high"},
			wantStatus: "updating",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := testDB(t)
			filing := model.IPOFiling{FilingID: "silentium-f1a", CIK: "0002112466", CompanyName: "Silentium Ltd.", FilingType: "F-1/A", FilingDate: now, FilingURL: "https://sec.test/silentium"}
			if err := db.Create(&filing).Error; err != nil {
				t.Fatalf("seed filing: %v", err)
			}
			if tt.seedMarket != nil {
				if err := db.Create(tt.seedMarket).Error; err != nil {
					t.Fatalf("seed market data: %v", err)
				}
			}
			svc := NewIPORadarService(db, &fakeSECClient{}, &fakeNotifier{}, NewConfigService(db, NewAuditService(db)))
			if err := svc.upsertListedCompanies(context.Background(), tt.listedCompanies); err != nil {
				t.Fatalf("upsertListedCompanies: %v", err)
			}
			page, err := svc.ListCompanies(context.Background(), IPOCompanyFilter{Page: 1, PageSize: 10}, now.Add(time.Hour))
			if err != nil || len(page.Items) != 1 {
				t.Fatalf("ListCompanies page=%+v err=%v", page, err)
			}
			item := page.Items[0]
			if item.Status != tt.wantStatus || item.AutomaticTicker != tt.wantTicker || (item.ListedVerifiedAt != nil) != tt.wantVerified {
				t.Fatalf("item = %+v, want status=%s ticker=%q verified=%v", item, tt.wantStatus, tt.wantTicker, tt.wantVerified)
			}
		})
	}
}

func TestIPORadarListCompaniesHidesEndedProjectsByDefault(t *testing.T) {
	now := time.Date(2026, 7, 12, 8, 0, 0, 0, time.UTC)
	db := testDB(t)
	if err := db.Create(&[]model.IPOFiling{
		{FilingID: "active-s1", CIK: "0000000101", CompanyName: "Active IPO Inc.", FilingType: "S-1", FilingDate: now, FilingURL: "https://sec.test/active-s1"},
		{FilingID: "listed-s1", CIK: "0000000102", CompanyName: "Historic IPO Inc.", FilingType: "S-1", FilingDate: now.AddDate(0, 0, -30), FilingURL: "https://sec.test/listed-s1"},
	}).Error; err != nil {
		t.Fatalf("seed filings: %v", err)
	}
	if err := db.Create(&model.IPOCompanyMarketData{CIK: "0000000102", Ticker: "HIST", Exchange: "Nasdaq", ListedVerifiedAt: &now, TickerSource: "sec", TickerConfidence: "high"}).Error; err != nil {
		t.Fatalf("seed confirmed listing: %v", err)
	}
	svc := NewIPORadarService(db, &fakeSECClient{}, &fakeNotifier{}, NewConfigService(db, NewAuditService(db)))

	active, err := svc.ListCompanies(context.Background(), IPOCompanyFilter{Page: 1, PageSize: 10}, now)
	if err != nil || active.Total != 1 || len(active.Items) != 1 || active.Items[0].CIK != "0000000101" {
		t.Fatalf("default active companies = %+v, err=%v", active, err)
	}
	history, err := svc.ListCompanies(context.Background(), IPOCompanyFilter{IncludeEnded: true, Page: 1, PageSize: 10}, now)
	if err != nil || history.Total != 2 {
		t.Fatalf("history companies = %+v, err=%v", history, err)
	}
	listed, err := svc.ListCompanies(context.Background(), IPOCompanyFilter{Status: "listed", Page: 1, PageSize: 10}, now)
	if err != nil || listed.Total != 1 || listed.Items[0].CIK != "0000000102" {
		t.Fatalf("listed companies = %+v, err=%v", listed, err)
	}
}

func TestIPORadarRefreshSkipsConfirmedListedCompanyBeforeHistoricalBackfill(t *testing.T) {
	now := time.Now().UTC()
	db := testDB(t)
	configs := NewConfigService(db, NewAuditService(db))
	if err := configs.EnsureDefaults(context.Background()); err != nil {
		t.Fatalf("EnsureDefaults: %v", err)
	}
	secClient := &fakeSECClient{
		currentFilings: []sec.CurrentFilingResult{{
			FilingID: "historic-s1a", CIK: "0001848247", CompanyName: "Historic Listed IPO Inc.", FilingType: "S-1/A", FilingDate: now, FilingURL: "https://sec.test/historic-s1a",
		}},
		filings: []sec.FilingResult{
			{FilingID: "historic-s1", CIK: "0001848247", CompanyName: "Historic Listed IPO Inc.", FilingType: "S-1", FilingDate: now.AddDate(-5, 0, 0), FilingURL: "https://sec.test/historic-s1"},
			{FilingID: "historic-s1a", CIK: "0001848247", CompanyName: "Historic Listed IPO Inc.", FilingType: "S-1/A", FilingDate: now, FilingURL: "https://sec.test/historic-s1a"},
		},
		listedCompanies: []sec.ListedCompany{{CIK: "0001848247", Name: "Historic Listed IPO Inc.", Ticker: "HIST", Exchange: "Nasdaq"}},
	}
	notifier := &fakeNotifier{}
	svc := NewIPORadarService(db, secClient, notifier, configs)

	result, err := svc.Refresh(context.Background())
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if result.NewFilings != 0 || result.Notified != 0 {
		t.Fatalf("refresh result = %+v, want no listed-company ingestion or notification", result)
	}
	if secClient.listCalls != 0 {
		t.Fatalf("historical ListFilings calls = %d, want 0", secClient.listCalls)
	}
	var count int64
	if err := db.Model(&model.IPOFiling{}).Count(&count).Error; err != nil {
		t.Fatalf("count stored filings: %v", err)
	}
	if count != 0 || notifier.calls != 0 {
		t.Fatalf("stored filings=%d notifier calls=%d, want 0", count, notifier.calls)
	}
}

func TestIPORadarTickerWithoutExchangeIsListingPending(t *testing.T) {
	now := time.Date(2026, 7, 11, 9, 0, 0, 0, time.UTC)
	db := testDB(t)
	if err := db.Create(&[]model.IPOFiling{{
		FilingID: "pending-s1a", CIK: "0002112466", CompanyName: "Silentium Ltd.", FilingType: "F-1/A", FilingDate: now, FilingURL: "https://sec.test/pending-s1a",
	}}).Error; err != nil {
		t.Fatalf("seed filing: %v", err)
	}
	if err := db.Create(&model.IPOCompanyMarketData{CIK: "0002112466", Ticker: "SIAI", TickerSource: "sec", TickerConfidence: "high"}).Error; err != nil {
		t.Fatalf("seed market data: %v", err)
	}
	svc := NewIPORadarService(db, &fakeSECClient{}, &fakeNotifier{}, NewConfigService(db, NewAuditService(db)))
	page, err := svc.ListCompanies(context.Background(), IPOCompanyFilter{Page: 1, PageSize: 10}, now)
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("ListCompanies page=%+v err=%v", page, err)
	}
	if item := page.Items[0]; item.Status != "listing_pending" || item.StatusConfidence != "medium" {
		t.Fatalf("company = %+v, want listing_pending with medium confidence", item)
	}
}

func TestIPORadarAutomaticallyConfirmsTickerOnlyListingWithLongbridge(t *testing.T) {
	now := time.Date(2026, 8, 8, 9, 0, 0, 0, time.UTC)
	db := testDB(t)
	if err := db.Create(&model.IPOFiling{FilingID: "auto-s1", CIK: "0002112466", CompanyName: "Automated IPO Inc.", FilingType: "S-1", FilingDate: now, FilingURL: "https://sec.test/auto-s1"}).Error; err != nil {
		t.Fatalf("seed filing: %v", err)
	}
	if err := db.Create(&model.IPOCompanyMarketData{CIK: "0002112466", Ticker: "AUTO", TickerSource: "sec", TickerConfidence: "high"}).Error; err != nil {
		t.Fatalf("seed ticker-only mapping: %v", err)
	}
	configs := NewConfigService(db, NewAuditService(db))
	if err := configs.EnsureDefaults(context.Background()); err != nil {
		t.Fatalf("EnsureDefaults: %v", err)
	}
	client := &fakeLongbridgeIPOListingClient{overviews: map[string]longbridgeIPOListingOverview{
		"AUTO.US": {Market: "Nasdaq", ListingDate: "2026-08-07"},
	}}
	svc := NewIPORadarService(db, &fakeSECClient{}, &fakeNotifier{}, configs).
		WithLongbridgeListingRuntime(config.DiscoveryConfig{LongbridgeAppKey: "key", LongbridgeAppSecret: "secret", LongbridgeAccessToken: "token"})
	svc.newLongbridgeListingClient = func(string, string, string) (longbridgeIPOListingClient, error) { return client, nil }

	confirmed, warning := svc.confirmListedCompaniesWithLongbridge(context.Background(), IPORadarSettings{
		LongbridgeListingVerificationEnabled: true, LongbridgeListingRequestBudget: 20, LongbridgeListingRecheckHours: 24,
	})
	if warning != "" || !confirmed["2112466"] || !reflect.DeepEqual(client.calls, []string{"AUTO.US"}) {
		t.Fatalf("confirmation=%v warning=%q calls=%v", confirmed, warning, client.calls)
	}
	page, err := svc.ListCompanies(context.Background(), IPOCompanyFilter{IncludeEnded: true, Page: 1, PageSize: 10}, now.Add(time.Hour))
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("ListCompanies page=%+v err=%v", page, err)
	}
	item := page.Items[0]
	if item.Status != "listed" || item.Exchange != "Nasdaq" || item.MarketDataSource != "longbridge" || item.ListingDate == nil || item.ListingDate.Format(time.DateOnly) != "2026-08-07" || item.StatusSource != "longbridge" || item.LongbridgeListingCheckCount != 1 || item.LongbridgeListingLastResult != "confirmed" {
		t.Fatalf("automatic listing item = %+v", item)
	}
}

func TestIPORadarShowsLongbridgeNoDataCountInPendingStatusReason(t *testing.T) {
	now := time.Date(2026, 8, 8, 9, 0, 0, 0, time.UTC)
	db := testDB(t)
	if err := db.Create(&model.IPOFiling{FilingID: "no-data-s1", CIK: "0002112466", CompanyName: "No Data IPO Inc.", FilingType: "S-1", FilingDate: now, FilingURL: "https://sec.test/no-data-s1"}).Error; err != nil {
		t.Fatalf("seed filing: %v", err)
	}
	if err := db.Create(&model.IPOCompanyMarketData{CIK: "0002112466", Ticker: "EMPTY", TickerSource: "sec", TickerConfidence: "high"}).Error; err != nil {
		t.Fatalf("seed ticker-only mapping: %v", err)
	}
	configs := NewConfigService(db, NewAuditService(db))
	if err := configs.EnsureDefaults(context.Background()); err != nil {
		t.Fatalf("EnsureDefaults: %v", err)
	}
	client := &fakeLongbridgeIPOListingClient{overviews: map[string]longbridgeIPOListingOverview{"EMPTY.US": {}}}
	svc := NewIPORadarService(db, &fakeSECClient{}, &fakeNotifier{}, configs).
		WithLongbridgeListingRuntime(config.DiscoveryConfig{LongbridgeAppKey: "key", LongbridgeAppSecret: "secret", LongbridgeAccessToken: "token"})
	svc.newLongbridgeListingClient = func(string, string, string) (longbridgeIPOListingClient, error) { return client, nil }
	if _, warning := svc.confirmListedCompaniesWithLongbridge(context.Background(), IPORadarSettings{LongbridgeListingVerificationEnabled: true, LongbridgeListingRequestBudget: 20, LongbridgeListingRecheckHours: 24}); warning == "" {
		t.Fatal("expected no-data Longbridge warning")
	}
	page, err := svc.ListCompanies(context.Background(), IPOCompanyFilter{Page: 1, PageSize: 10}, now.Add(time.Hour))
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("ListCompanies page=%+v err=%v", page, err)
	}
	item := page.Items[0]
	if item.Status != "listing_pending" || item.LongbridgeListingCheckCount != 1 || item.LongbridgeListingLastResult != "no_data" || !strings.Contains(item.StatusReason, "Longbridge queried 1 time(s); no listing market information returned") {
		t.Fatalf("pending item = %+v", item)
	}
}

func TestIPORadarCachesLongbridgeIPOCalendarAndEnrichesStrictSECNameMatch(t *testing.T) {
	now := time.Date(2026, 8, 8, 9, 0, 0, 0, time.UTC)
	db := testDB(t)
	if err := db.Create(&model.IPOFiling{FilingID: "calendar-s1", CIK: "0002112468", CompanyName: "Calendar IPO, Inc.", FilingType: "S-1", FilingDate: now, FilingURL: "https://sec.test/calendar-s1"}).Error; err != nil {
		t.Fatalf("seed filing: %v", err)
	}
	configs := NewConfigService(db, NewAuditService(db))
	if err := configs.EnsureDefaults(context.Background()); err != nil {
		t.Fatalf("EnsureDefaults: %v", err)
	}
	client := &fakeLongbridgeIPOCalendarClient{pages: []longbridgeIPOCalendarPage{{Events: []longbridgeIPOCalendarEvent{{
		ID: "ipo-calendar-1", Symbol: "CAL.US", Market: "US", CompanyName: "Calendar IPO Inc", Date: "2026.08.12", Session: "盘前", Content: "IPO", Currency: "USD",
	}}}}}
	svc := NewIPORadarService(db, &fakeSECClient{}, &fakeNotifier{}, configs).
		WithLongbridgeListingRuntime(config.DiscoveryConfig{LongbridgeAppKey: "key", LongbridgeAppSecret: "secret", LongbridgeAccessToken: "token"})
	svc.newLongbridgeIPOCalendarClient = func(string, string, string) (longbridgeIPOCalendarClient, error) { return client, nil }
	settings := IPORadarSettings{LongbridgeIPOCalendarEnabled: true, LongbridgeIPOCalendarLookbackDays: 14, LongbridgeIPOCalendarLookaheadDays: 30, LongbridgeIPOCalendarMaxPages: 5}
	if warning := svc.syncLongbridgeIPOCalendar(context.Background(), settings); warning != "" {
		t.Fatalf("sync warning: %s", warning)
	}
	if len(client.calls) != 1 || !strings.HasSuffix(client.calls[0], "|US") {
		t.Fatalf("calendar calls = %v", client.calls)
	}
	calendarPage, err := svc.ListCalendarEvents(context.Background(), IPOCalendarEventFilter{Ticker: "CAL", Page: 1, PageSize: 10})
	if err != nil || len(calendarPage.Items) != 1 || calendarPage.Items[0].Symbol != "CAL.US" {
		t.Fatalf("calendar page = %+v err=%v", calendarPage, err)
	}
	companies, err := svc.ListCompanies(context.Background(), IPOCompanyFilter{Page: 1, PageSize: 10}, now)
	if err != nil || len(companies.Items) != 1 {
		t.Fatalf("company page=%+v err=%v", companies, err)
	}
	company := companies.Items[0]
	if company.FinalTicker != "CAL" || company.ListingDate == nil || company.ListingDate.Format(time.DateOnly) != "2026-08-12" || company.MarketDataSource != "longbridge_calendar" || company.StatusSource != "longbridge_calendar" {
		t.Fatalf("calendar-enriched company = %+v", company)
	}
}

func TestIPORadarCalendarMatchesUniqueCompanyNameWithoutLegalSuffix(t *testing.T) {
	db := testDB(t)
	filing := model.IPOFiling{FilingID: "calendar-core-s1", CIK: "0002112499", CompanyName: "Core Match Holdings, Inc.", FilingType: "S-1", FilingDate: time.Now().UTC(), FilingURL: "https://sec.test/core-s1"}
	if err := db.Create(&filing).Error; err != nil {
		t.Fatalf("seed filing: %v", err)
	}
	svc := NewIPORadarService(db, &fakeSECClient{}, &fakeNotifier{}, nil)
	if err := svc.attachLongbridgeCalendarToSECCandidates(context.Background(), []longbridgeIPOCalendarEvent{{Symbol: "CORE.US", Market: "US", CompanyName: "Core Match Holdings", Date: "2026-08-12"}}); err != nil {
		t.Fatalf("attach calendar: %v", err)
	}
	var market model.IPOCompanyMarketData
	if err := db.Where("cik = ?", filing.CIK).First(&market).Error; err != nil {
		t.Fatalf("load market data: %v", err)
	}
	if market.Ticker != "CORE" || market.TickerSource != longbridgeIPOCalendarSource {
		t.Fatalf("market data = %+v", market)
	}
}

func TestIPORadarRefreshSkipsRegistrationAfterLongbridgeListingConfirmation(t *testing.T) {
	now := time.Now().UTC()
	db := testDB(t)
	if err := db.Create(&model.IPOFiling{FilingID: "prior-s1", CIK: "0002112466", CompanyName: "Automated IPO Inc.", FilingType: "S-1", FilingDate: now.AddDate(0, 0, -7), FilingURL: "https://sec.test/prior-s1"}).Error; err != nil {
		t.Fatalf("seed prior IPO filing: %v", err)
	}
	configs := NewConfigService(db, NewAuditService(db))
	if err := configs.EnsureDefaults(context.Background()); err != nil {
		t.Fatalf("EnsureDefaults: %v", err)
	}
	secClient := &fakeSECClient{
		currentFilings:  []sec.CurrentFilingResult{{FilingID: "new-s1a", CIK: "0002112466", CompanyName: "Automated IPO Inc.", FilingType: "S-1/A", FilingDate: now, FilingURL: "https://sec.test/new-s1a"}},
		listedCompanies: []sec.ListedCompany{{CIK: "0002112466", Name: "Automated IPO Inc.", Ticker: "AUTO"}},
	}
	client := &fakeLongbridgeIPOListingClient{overviews: map[string]longbridgeIPOListingOverview{"AUTO.US": {Market: "Nasdaq"}}}
	svc := NewIPORadarService(db, secClient, &fakeNotifier{}, configs).
		WithLongbridgeListingRuntime(config.DiscoveryConfig{LongbridgeAppKey: "key", LongbridgeAppSecret: "secret", LongbridgeAccessToken: "token"})
	svc.newLongbridgeListingClient = func(string, string, string) (longbridgeIPOListingClient, error) { return client, nil }

	result, err := svc.Refresh(context.Background())
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if result.NewFilings != 0 || secClient.listCalls != 0 || !reflect.DeepEqual(client.calls, []string{"AUTO.US"}) {
		t.Fatalf("result=%+v historicalCalls=%d longbridgeCalls=%v", result, secClient.listCalls, client.calls)
	}
	var count int64
	if err := db.Model(&model.IPOFiling{}).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("IPO filings=%d err=%v, want only prior filing", count, err)
	}
}

func TestIPORadarLongbridgeListingFailureUsesRecheckWindow(t *testing.T) {
	db := testDB(t)
	if err := db.Create(&model.IPOCompanyMarketData{CIK: "0002112466", Ticker: "WAIT", TickerSource: "sec", TickerConfidence: "high"}).Error; err != nil {
		t.Fatalf("seed ticker-only mapping: %v", err)
	}
	configs := NewConfigService(db, NewAuditService(db))
	if err := configs.EnsureDefaults(context.Background()); err != nil {
		t.Fatalf("EnsureDefaults: %v", err)
	}
	client := &fakeLongbridgeIPOListingClient{errors: map[string]error{"WAIT.US": errors.New("upstream unavailable")}}
	svc := NewIPORadarService(db, &fakeSECClient{}, &fakeNotifier{}, configs).
		WithLongbridgeListingRuntime(config.DiscoveryConfig{LongbridgeAppKey: "key", LongbridgeAppSecret: "secret", LongbridgeAccessToken: "token"})
	svc.newLongbridgeListingClient = func(string, string, string) (longbridgeIPOListingClient, error) { return client, nil }
	settings := IPORadarSettings{LongbridgeListingVerificationEnabled: true, LongbridgeListingRequestBudget: 20, LongbridgeListingRecheckHours: 24}
	if _, warning := svc.confirmListedCompaniesWithLongbridge(context.Background(), settings); warning == "" {
		t.Fatal("expected warning for unavailable Longbridge")
	}
	if _, warning := svc.confirmListedCompaniesWithLongbridge(context.Background(), settings); warning != "" {
		t.Fatalf("cached recheck should not warn, got %q", warning)
	}
	if !reflect.DeepEqual(client.calls, []string{"WAIT.US"}) {
		t.Fatalf("Longbridge calls=%v want one throttled call", client.calls)
	}
	var row model.IPOCompanyMarketData
	if err := db.Where("cik = ?", "0002112466").First(&row).Error; err != nil || row.ListingCheckedAt == nil || row.ListedVerifiedAt != nil || row.Exchange != "" || row.LongbridgeListingCheckCount != 1 || row.LongbridgeListingLastResult != "unavailable" {
		t.Fatalf("failure row = %+v err=%v", row, err)
	}
}

func TestIPORadarWithdrawalOverridesListingMapping(t *testing.T) {
	now := time.Date(2026, 7, 11, 9, 0, 0, 0, time.UTC)
	db := testDB(t)
	if err := db.Create(&[]model.IPOFiling{
		{FilingID: "withdrawn-s1", CIK: "0002112467", CompanyName: "Withdrawn Co.", FilingType: "S-1", FilingDate: now.Add(-time.Hour), FilingURL: "https://sec.test/withdrawn-s1"},
		{FilingID: "withdrawn-rw", CIK: "0002112467", CompanyName: "Withdrawn Co.", FilingType: "RW", FilingDate: now, FilingURL: "https://sec.test/withdrawn-rw"},
	}).Error; err != nil {
		t.Fatalf("seed filings: %v", err)
	}
	if err := db.Create(&model.IPOCompanyMarketData{CIK: "0002112467", Ticker: "WDN", Exchange: "Nasdaq", ListedVerifiedAt: &now, TickerSource: "sec", TickerConfidence: "high"}).Error; err != nil {
		t.Fatalf("seed market data: %v", err)
	}
	svc := NewIPORadarService(db, &fakeSECClient{}, &fakeNotifier{}, NewConfigService(db, NewAuditService(db)))
	page, err := svc.ListCompanies(context.Background(), IPOCompanyFilter{IncludeEnded: true, Page: 1, PageSize: 10}, now)
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("ListCompanies page=%+v err=%v", page, err)
	}
	if item := page.Items[0]; item.Status != "withdrawn" {
		t.Fatalf("company = %+v, want withdrawn", item)
	}
}

func TestIPORadarLifecycleSweepRotatesOldestActiveCIKs(t *testing.T) {
	now := time.Now().UTC()
	db := testDB(t)
	if err := db.Create(&[]model.IPOFiling{
		{FilingID: "sweep-s1-1", CIK: "0000000001", CompanyName: "One Inc.", FilingType: "S-1", FilingDate: now, FilingURL: "https://sec.test/sweep-1"},
		{FilingID: "sweep-s1-2", CIK: "0000000002", CompanyName: "Two Inc.", FilingType: "S-1", FilingDate: now, FilingURL: "https://sec.test/sweep-2"},
		{FilingID: "sweep-s1-3", CIK: "0000000003", CompanyName: "Three Inc.", FilingType: "S-1", FilingDate: now, FilingURL: "https://sec.test/sweep-3"},
	}).Error; err != nil {
		t.Fatalf("seed filings: %v", err)
	}
	oldest := now.Add(-3 * time.Hour)
	newer := now.Add(-2 * time.Hour)
	if err := db.Create(&[]model.IPOCompanyMarketData{
		{CIK: "0000000001", LifecycleCheckedAt: &oldest},
		{CIK: "0000000002"},
		{CIK: "0000000003", LifecycleCheckedAt: &newer},
	}).Error; err != nil {
		t.Fatalf("seed lifecycle data: %v", err)
	}
	configs := NewConfigService(db, NewAuditService(db))
	if err := configs.EnsureDefaults(context.Background()); err != nil {
		t.Fatalf("EnsureDefaults: %v", err)
	}
	if err := configs.UpsertMany(context.Background(), []ConfigInput{
		{Key: "ipo.lifecycle_sweep_enabled", Value: "true", ValueType: "bool", Category: "ipo"},
		{Key: "ipo.lifecycle_max_ciks", Value: "2", ValueType: "int", Category: "ipo"},
		{Key: "ipo.lifecycle_recheck_hours", Value: "1", ValueType: "int", Category: "ipo"},
		{Key: "ipo.notify_enabled", Value: "false", ValueType: "bool", Category: "ipo"},
	}, "tester"); err != nil {
		t.Fatalf("configure lifecycle sweep: %v", err)
	}
	secClient := &fakeSECClient{}
	svc := NewIPORadarService(db, secClient, &fakeNotifier{}, configs)
	if _, err := svc.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	checked := map[string]bool{}
	for _, query := range secClient.queries {
		checked[query.CIK] = true
	}
	if !checked["0000000001"] || !checked["0000000002"] || checked["0000000003"] {
		t.Fatalf("lifecycle checks = %+v, want oldest CIKs 0000000001 and 0000000002", checked)
	}
	var rows []model.IPOCompanyMarketData
	if err := db.Order("cik ASC").Find(&rows).Error; err != nil {
		t.Fatalf("load lifecycle data: %v", err)
	}
	for _, row := range rows[:2] {
		if row.LifecycleCheckedAt == nil || !row.LifecycleCheckedAt.After(oldest) {
			t.Fatalf("lifecycle timestamp for %s = %v, want advanced", row.CIK, row.LifecycleCheckedAt)
		}
	}
	if rows[2].LifecycleCheckedAt == nil || !rows[2].LifecycleCheckedAt.Equal(newer) {
		t.Fatalf("lifecycle timestamp for unselected CIK = %v, want %v", rows[2].LifecycleCheckedAt, newer)
	}
}

func TestIPORadarLifecycleSweepRequiresRecentLifecycleFiling(t *testing.T) {
	now := time.Now().UTC()
	db := testDB(t)
	if err := db.Create(&[]model.IPOFiling{
		{FilingID: "expired-s1", CIK: "0000000001", CompanyName: "Expired IPO", FilingType: "S-1", FilingDate: now.AddDate(0, 0, -181), FilingURL: "https://sec.test/expired-s1"},
		{FilingID: "expired-424b4", CIK: "0000000001", CompanyName: "Expired IPO", FilingType: "424B4", FilingDate: now.AddDate(0, 0, -181), FilingURL: "https://sec.test/expired-424b4"},
		{FilingID: "recent-s1", CIK: "0000000002", CompanyName: "Recent IPO", FilingType: "S-1", FilingDate: now.AddDate(0, 0, -1), FilingURL: "https://sec.test/recent-s1"},
	}).Error; err != nil {
		t.Fatalf("seed IPO filings: %v", err)
	}
	svc := NewIPORadarService(db, &fakeSECClient{}, &fakeNotifier{}, NewConfigService(db, NewAuditService(db)))
	ciks, err := svc.lifecycleSweepCandidateCIKs(context.Background(), now.Add(-time.Hour), nil, 10)
	if err != nil {
		t.Fatalf("lifecycle sweep candidates: %v", err)
	}
	if len(ciks) != 1 || ciks[0] != "0000000002" {
		t.Fatalf("lifecycle sweep candidates = %v, want only recent CIK", ciks)
	}
}

func TestIPORadarLifecycleSweepExcludesTerminalManualOverrides(t *testing.T) {
	now := time.Now().UTC()
	db := testDB(t)
	if err := db.Create(&[]model.IPOFiling{
		{FilingID: "terminal-s1-withdrawn", CIK: "0000000001", CompanyName: "Withdrawn Override Inc.", FilingType: "S-1", FilingDate: now, FilingURL: "https://sec.test/terminal-withdrawn"},
		{FilingID: "terminal-s1-listed", CIK: "0000000002", CompanyName: "Listed Override Inc.", FilingType: "S-1", FilingDate: now, FilingURL: "https://sec.test/terminal-listed"},
		{FilingID: "terminal-s1-active", CIK: "0000000003", CompanyName: "Active Inc.", FilingType: "S-1", FilingDate: now, FilingURL: "https://sec.test/terminal-active"},
	}).Error; err != nil {
		t.Fatalf("seed filings: %v", err)
	}
	if err := db.Create(&[]model.IPOCompanyOverride{
		{CIK: "0000000001", StatusOverride: "withdrawn"},
		{CIK: "0000000002", StatusOverride: "listed"},
	}).Error; err != nil {
		t.Fatalf("seed overrides: %v", err)
	}
	configs := NewConfigService(db, NewAuditService(db))
	if err := configs.EnsureDefaults(context.Background()); err != nil {
		t.Fatalf("EnsureDefaults: %v", err)
	}
	if err := configs.UpsertMany(context.Background(), []ConfigInput{
		{Key: "ipo.lifecycle_sweep_enabled", Value: "true", ValueType: "bool", Category: "ipo"},
		{Key: "ipo.lifecycle_max_ciks", Value: "1", ValueType: "int", Category: "ipo"},
		{Key: "ipo.lifecycle_recheck_hours", Value: "1", ValueType: "int", Category: "ipo"},
		{Key: "ipo.notify_enabled", Value: "false", ValueType: "bool", Category: "ipo"},
	}, "tester"); err != nil {
		t.Fatalf("configure lifecycle sweep: %v", err)
	}
	secClient := &fakeSECClient{}
	svc := NewIPORadarService(db, secClient, &fakeNotifier{}, configs)
	if _, err := svc.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if len(secClient.queries) != 1 || secClient.queries[0].CIK != "0000000003" {
		t.Fatalf("lifecycle sweep queries = %+v, want only active CIK 0000000003", secClient.queries)
	}
}

func TestIPORadarLifecycleSweepSkipsCurrentFeedBackfillsBeforeLimit(t *testing.T) {
	now := time.Now().UTC()
	db := testDB(t)
	if err := db.Create(&[]model.IPOFiling{
		{FilingID: "current-sweep-s1-2", CIK: "0000000002", CompanyName: "Second Inc.", FilingType: "S-1", FilingDate: now, FilingURL: "https://sec.test/current-sweep-2"},
		{FilingID: "current-sweep-s1-3", CIK: "0000000003", CompanyName: "Third Inc.", FilingType: "S-1", FilingDate: now, FilingURL: "https://sec.test/current-sweep-3"},
	}).Error; err != nil {
		t.Fatalf("seed filings: %v", err)
	}
	configs := NewConfigService(db, NewAuditService(db))
	if err := configs.EnsureDefaults(context.Background()); err != nil {
		t.Fatalf("EnsureDefaults: %v", err)
	}
	if err := configs.UpsertMany(context.Background(), []ConfigInput{
		{Key: "ipo.lifecycle_sweep_enabled", Value: "true", ValueType: "bool", Category: "ipo"},
		{Key: "ipo.lifecycle_max_ciks", Value: "2", ValueType: "int", Category: "ipo"},
		{Key: "ipo.lifecycle_recheck_hours", Value: "1", ValueType: "int", Category: "ipo"},
		{Key: "ipo.notify_enabled", Value: "false", ValueType: "bool", Category: "ipo"},
	}, "tester"); err != nil {
		t.Fatalf("configure lifecycle sweep: %v", err)
	}
	secClient := &fakeSECClient{currentFilings: []sec.CurrentFilingResult{{
		FilingID: "current-sweep-s1-1", CIK: "0000000001", CompanyName: "First Inc.", FilingType: "S-1", FilingDate: now, FilingURL: "https://sec.test/current-sweep-1",
	}}}
	svc := NewIPORadarService(db, secClient, &fakeNotifier{}, configs)
	if _, err := svc.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	checked := map[string]bool{}
	for _, query := range secClient.queries {
		checked[query.CIK] = true
	}
	for _, cik := range []string{"0000000001", "0000000002", "0000000003"} {
		if !checked[cik] {
			t.Fatalf("lifecycle queries = %+v, missing %s", secClient.queries, cik)
		}
	}
	if len(checked) != 3 {
		t.Fatalf("lifecycle queries = %+v, want one current-feed backfill plus two sweep CIKs", secClient.queries)
	}
}

func TestIPORadarCurrentLifecycleFormsAreIngested(t *testing.T) {
	now := time.Now().UTC()
	db := testDB(t)
	if err := db.Create(&model.IPOFiling{FilingID: "lifecycle-s1", CIK: "0002112468", CompanyName: "Lifecycle Inc.", FilingType: "S-1", FilingDate: now.AddDate(0, 0, -10), FilingURL: "https://sec.test/lifecycle-s1"}).Error; err != nil {
		t.Fatalf("seed registration filing: %v", err)
	}
	configs := NewConfigService(db, NewAuditService(db))
	if err := configs.EnsureDefaults(context.Background()); err != nil {
		t.Fatalf("EnsureDefaults: %v", err)
	}
	if err := configs.UpsertMany(context.Background(), []ConfigInput{{Key: "ipo.notify_enabled", Value: "false", ValueType: "bool", Category: "ipo"}}, "tester"); err != nil {
		t.Fatalf("disable notifications: %v", err)
	}
	secClient := &fakeSECClient{currentFilings: []sec.CurrentFilingResult{
		{FilingID: "lifecycle-effect", CIK: "0002112468", CompanyName: "Lifecycle Inc.", FilingType: "EFFECT", FilingDate: now, FilingURL: "https://sec.test/lifecycle-effect"},
		{FilingID: "lifecycle-424b4", CIK: "0002112468", CompanyName: "Lifecycle Inc.", FilingType: "424B4", FilingDate: now, FilingURL: "https://sec.test/lifecycle-424b4"},
		{FilingID: "lifecycle-rw", CIK: "0002112468", CompanyName: "Lifecycle Inc.", FilingType: "RW", FilingDate: now, FilingURL: "https://sec.test/lifecycle-rw"},
	}}
	svc := NewIPORadarService(db, secClient, &fakeNotifier{}, configs)
	if _, err := svc.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if len(secClient.currentQueries) != 1 {
		t.Fatalf("current queries = %d, want 1", len(secClient.currentQueries))
	}
	forms := map[string]bool{}
	for _, form := range secClient.currentQueries[0].FormTypes {
		forms[form] = true
	}
	for _, required := range []string{"EFFECT", "424B4", "RW"} {
		if !forms[required] {
			t.Fatalf("current forms = %+v, missing %s", forms, required)
		}
	}
	var filings []model.IPOFiling
	if err := db.Where("cik = ?", "0002112468").Order("filing_type ASC").Find(&filings).Error; err != nil {
		t.Fatalf("load lifecycle filings: %v", err)
	}
	if len(filings) != 4 {
		t.Fatalf("lifecycle filings = %+v, want S-1 plus EFFECT/424B4/RW", filings)
	}
}

func TestIPOLifecycleFilingTypeTableDriven(t *testing.T) {
	tests := []struct {
		name       string
		filingType string
		want       bool
	}{
		{name: "registration filing", filingType: "F-1/A", want: true},
		{name: "IPO final prospectus", filingType: "424B4", want: true},
		{name: "structured note prospectus", filingType: "424B2", want: false},
		{name: "effectiveness notice", filingType: "EFFECT", want: true},
		{name: "withdrawal", filingType: "RW", want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isIPOLifecycleFilingType(tt.filingType, []string{"S-1", "S-1/A", "F-1", "F-1/A", "S-1MEF"}); got != tt.want {
				t.Fatalf("isIPOLifecycleFilingType(%q) = %v, want %v", tt.filingType, got, tt.want)
			}
		})
	}
}

func TestIPORadarFiltersNonIPOLegacyFilings(t *testing.T) {
	now := time.Date(2026, 6, 21, 9, 0, 0, 0, time.UTC)
	db := testDB(t)
	filings := []model.IPOFiling{
		{FilingID: "jpm-note", CIK: "0000019617", CompanyName: "JPMORGAN CHASE & CO", FilingType: "424B2", FilingDate: now, FilingURL: "https://sec.test/jpm-note"},
		{FilingID: "acme-s1", CIK: "0000000001", CompanyName: "Acme Inc.", FilingType: "S-1", FilingDate: now.Add(-time.Hour), FilingURL: "https://sec.test/acme-s1"},
		{FilingID: "acme-note", CIK: "0000000001", CompanyName: "Acme Inc.", FilingType: "424B2", FilingDate: now, FilingURL: "https://sec.test/acme-note"},
		{FilingID: "acme-final", CIK: "0000000001", CompanyName: "Acme Inc.", FilingType: "424B4", FilingDate: now.Add(time.Hour), FilingURL: "https://sec.test/acme-final"},
	}
	if err := db.Create(&filings).Error; err != nil {
		t.Fatalf("seed filings: %v", err)
	}
	svc := NewIPORadarService(db, &fakeSECClient{}, &fakeNotifier{}, NewConfigService(db, NewAuditService(db)))
	companies, err := svc.ListCompanies(context.Background(), IPOCompanyFilter{IncludeEnded: true, Page: 1, PageSize: 20}, now)
	if err != nil {
		t.Fatalf("ListCompanies: %v", err)
	}
	if companies.Total != 1 || companies.Items[0].CIK != "0000000001" || companies.Items[0].FilingCount != 2 {
		t.Fatalf("companies = %+v", companies)
	}
	listed, err := svc.List(context.Background(), IPOFilingFilter{Page: 1, PageSize: 20})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if listed.Total != 2 {
		t.Fatalf("filings = %+v, want only S-1 and 424B4", listed)
	}
}

func TestIPORadarSelectsDeterministicPrimaryTicker(t *testing.T) {
	now := time.Date(2026, 6, 21, 9, 0, 0, 0, time.UTC)
	db := testDB(t)
	if err := db.Create(&model.IPOFiling{FilingID: "jpm-s1", CIK: "0000019617", CompanyName: "JPMORGAN CHASE & CO", FilingType: "S-1", FilingDate: now, FilingURL: "https://sec.test/jpm-s1"}).Error; err != nil {
		t.Fatalf("seed filing: %v", err)
	}
	svc := NewIPORadarService(db, &fakeSECClient{}, &fakeNotifier{}, NewConfigService(db, NewAuditService(db)))
	listed := []sec.ListedCompany{
		{CIK: "0000019617", Name: "JPMORGAN CHASE & CO", Ticker: "JPM", Exchange: "NYSE"},
		{CIK: "0000019617", Name: "JPMORGAN CHASE & CO", Ticker: "JPM-PC", Exchange: "NYSE"},
		{CIK: "0000019617", Name: "JPMORGAN CHASE & CO", Ticker: "VYLD", Exchange: "NYSE"},
	}
	if err := svc.upsertListedCompanies(context.Background(), listed); err != nil {
		t.Fatalf("upsertListedCompanies: %v", err)
	}
	companies, err := svc.ListCompanies(context.Background(), IPOCompanyFilter{IncludeEnded: true, Page: 1, PageSize: 20}, now)
	if err != nil || len(companies.Items) != 1 {
		t.Fatalf("ListCompanies companies=%+v err=%v", companies, err)
	}
	if companies.Items[0].AutomaticTicker != "JPM" {
		t.Fatalf("automatic ticker = %q, want JPM", companies.Items[0].AutomaticTicker)
	}
}

func TestIPORadarServiceRefreshBackfillsCompanyLifecycleFilings(t *testing.T) {
	now := time.Now().UTC()
	db := testDB(t)
	configs := NewConfigService(db, NewAuditService(db))
	if err := configs.EnsureDefaults(context.Background()); err != nil {
		t.Fatalf("EnsureDefaults: %v", err)
	}
	if err := configs.UpsertMany(context.Background(), []ConfigInput{
		{Key: "ipo.notify_enabled", Value: "false", ValueType: "bool", Category: "ipo"},
	}, "tester"); err != nil {
		t.Fatalf("UpsertMany: %v", err)
	}

	secClient := &fakeSECClient{
		currentFilings: []sec.CurrentFilingResult{{
			FilingID:        "acme-s1a",
			AccessionNumber: "0000000001-26-000002",
			CIK:             "0000000001",
			CompanyName:     "Acme Space Inc.",
			FilingType:      "S-1/A",
			FilingDate:      now,
			FilingURL:       "https://www.sec.gov/acme/s1a",
			Title:           "S-1/A - Acme Space Inc.",
		}},
		filings: []sec.FilingResult{
			{FilingID: "acme-s1", AccessionNumber: "0000000001-26-000001", CIK: "0000000001", CompanyName: "Acme Space Inc.", FilingType: "S-1", FilingDate: now.AddDate(0, 0, -10), FilingURL: "https://www.sec.gov/acme/s1", Title: "S-1 - Acme Space Inc."},
			{FilingID: "acme-s1a", AccessionNumber: "0000000001-26-000002", CIK: "0000000001", CompanyName: "Acme Space Inc.", FilingType: "S-1/A", FilingDate: now, FilingURL: "https://www.sec.gov/acme/s1a", Title: "S-1/A - Acme Space Inc."},
			{FilingID: "acme-effect", AccessionNumber: "9999999995-26-000001", CIK: "0000000001", CompanyName: "Acme Space Inc.", FilingType: "EFFECT", FilingDate: now.AddDate(0, 0, 1), FilingURL: "https://www.sec.gov/acme/effect", Title: "EFFECT - Acme Space Inc."},
			{FilingID: "acme-424b4", AccessionNumber: "0000000001-26-000003", CIK: "0000000001", CompanyName: "Acme Space Inc.", FilingType: "424B4", FilingDate: now.AddDate(0, 0, 2), FilingURL: "https://www.sec.gov/acme/424b4", Title: "424B4 - Acme Space Inc."},
			{FilingID: "acme-10k", AccessionNumber: "0000000001-26-000004", CIK: "0000000001", CompanyName: "Acme Space Inc.", FilingType: "10-K", FilingDate: now.AddDate(0, 0, 3), FilingURL: "https://www.sec.gov/acme/10k", Title: "10-K - Acme Space Inc."},
		},
	}
	svc := NewIPORadarService(db, secClient, &fakeNotifier{}, configs)

	got, err := svc.Refresh(context.Background())
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if secClient.listCalls != 1 {
		t.Fatalf("ListFilings calls = %d, want 1", secClient.listCalls)
	}
	if len(secClient.queries) != 1 || secClient.queries[0].CIK != "0000000001" || !secClient.queries[0].FetchFullHistory {
		t.Fatalf("ListFilings query = %+v, want CIK full-history lookup", secClient.queries)
	}
	if got.NewFilings != 4 {
		t.Fatalf("NewFilings = %d, want 4", got.NewFilings)
	}
	var filings []model.IPOFiling
	if err := db.Order("filing_type ASC").Find(&filings).Error; err != nil {
		t.Fatalf("load ipo filings: %v", err)
	}
	gotTypes := map[string]bool{}
	for _, filing := range filings {
		gotTypes[filing.FilingType] = true
	}
	for _, want := range []string{"S-1", "S-1/A", "EFFECT", "424B4"} {
		if !gotTypes[want] {
			t.Fatalf("stored filing types = %+v, missing %s", gotTypes, want)
		}
	}
	if gotTypes["10-K"] {
		t.Fatalf("stored filing types = %+v, did not expect 10-K", gotTypes)
	}
}

func TestIPORadarServiceListTableDriven(t *testing.T) {
	now := time.Date(2026, 6, 18, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		name   string
		filter IPOFilingFilter
		want   int64
	}{
		{name: "filters company", filter: IPOFilingFilter{CompanyName: "Acme", Page: 1, PageSize: 10}, want: 1},
		{name: "filters cik", filter: IPOFilingFilter{CIK: "0000000002", Page: 1, PageSize: 10}, want: 1},
		{name: "filters type", filter: IPOFilingFilter{FilingType: "F-1", Page: 1, PageSize: 10}, want: 1},
		{name: "filters notified yes", filter: IPOFilingFilter{Notified: "yes", Page: 1, PageSize: 10}, want: 1},
		{name: "filters notified no", filter: IPOFilingFilter{Notified: "no", Page: 1, PageSize: 10}, want: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := testDB(t)
			notifiedAt := now
			if err := db.Create(&[]model.IPOFiling{
				{FilingID: "ipo-1", CompanyName: "Acme Space Inc.", CIK: "0000000001", FilingType: "S-1", FilingDate: now, NotifiedAt: &notifiedAt},
				{FilingID: "ipo-2", CompanyName: "Beta Bio Ltd.", CIK: "0000000002", FilingType: "F-1", FilingDate: now},
			}).Error; err != nil {
				t.Fatalf("seed ipo filings: %v", err)
			}
			svc := NewIPORadarService(db, &fakeSECClient{}, &fakeNotifier{}, NewConfigService(db, NewAuditService(db)))
			got, err := svc.List(context.Background(), tt.filter)
			if err != nil {
				t.Fatalf("List: %v", err)
			}
			if got.Total != tt.want {
				t.Fatalf("total = %d, want %d", got.Total, tt.want)
			}
		})
	}
}

func TestIPORadarServiceListOrdersBySyncAndPublishTime(t *testing.T) {
	now := time.Date(2026, 6, 18, 10, 0, 0, 0, time.UTC)
	acceptedOld := now.Add(-2 * time.Hour)
	acceptedNew := now.Add(-1 * time.Hour)
	db := testDB(t)
	if err := db.Create([]model.IPOFiling{
		{FilingID: "older-sync-newer-filing", CompanyName: "Beta Bio Ltd.", CIK: "0000000002", FilingType: "F-1", FilingDate: now, AcceptedAt: &acceptedNew, FilingURL: "https://sec.gov/beta", CreatedAt: now.Add(-1 * time.Hour)},
		{FilingID: "newer-sync-older-filing", CompanyName: "Acme Space Inc.", CIK: "0000000001", FilingType: "S-1", FilingDate: now.AddDate(0, 0, -3), AcceptedAt: &acceptedOld, FilingURL: "https://sec.gov/acme", CreatedAt: now},
	}).Error; err != nil {
		t.Fatalf("seed ipo filings: %v", err)
	}
	svc := NewIPORadarService(db, &fakeSECClient{}, &fakeNotifier{}, NewConfigService(db, NewAuditService(db)))

	got, err := svc.List(context.Background(), IPOFilingFilter{Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got.Items) != 2 {
		t.Fatalf("items = %d, want 2", len(got.Items))
	}
	if got.Items[0].FilingID != "newer-sync-older-filing" {
		t.Fatalf("first filing = %s, want newer sync time first", got.Items[0].FilingID)
	}
}

func TestIPORadarServiceListTimelineOrdersByAcceptedTimeAscending(t *testing.T) {
	now := time.Date(2026, 6, 18, 10, 0, 0, 0, time.UTC)
	acceptedOld := now.Add(-3 * time.Hour)
	acceptedNew := now.Add(-1 * time.Hour)
	db := testDB(t)
	if err := db.Create([]model.IPOFiling{
		{FilingID: "s1a", CompanyName: "Acme Space Inc.", CIK: "0000000001", FilingType: "S-1/A", FilingDate: now, AcceptedAt: &acceptedNew, FilingURL: "https://sec.gov/acme/s1a", CreatedAt: now.Add(-2 * time.Hour)},
		{FilingID: "s1", CompanyName: "Acme Space Inc.", CIK: "0000000001", FilingType: "S-1", FilingDate: now.AddDate(0, 0, -2), AcceptedAt: &acceptedOld, FilingURL: "https://sec.gov/acme/s1", CreatedAt: now},
	}).Error; err != nil {
		t.Fatalf("seed ipo filings: %v", err)
	}
	svc := NewIPORadarService(db, &fakeSECClient{}, &fakeNotifier{}, NewConfigService(db, NewAuditService(db)))

	got, err := svc.List(context.Background(), IPOFilingFilter{CIK: "0000000001", Sort: "timeline", Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got.Items) != 2 {
		t.Fatalf("items = %d, want 2", len(got.Items))
	}
	if got.Items[0].FilingID != "s1" || got.Items[1].FilingID != "s1a" {
		t.Fatalf("filing order = %s,%s; want s1,s1a", got.Items[0].FilingID, got.Items[1].FilingID)
	}
}

func TestIPORadarServiceListCompaniesTableDriven(t *testing.T) {
	now := time.Date(2026, 6, 18, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		name       string
		filings    []model.IPOFiling
		targets    []model.WatchTarget
		wantStatus string
		wantLatest string
	}{
		{
			name: "new filing from initial registration",
			filings: []model.IPOFiling{{
				FilingID: "new-s1", CIK: "0000000001", CompanyName: "Acme Space Inc.", FilingType: "S-1", FilingDate: now, FilingURL: "https://sec.gov/acme",
			}},
			wantStatus: "new",
			wantLatest: "S-1",
		},
		{
			name: "amendment marks updating",
			filings: []model.IPOFiling{
				{FilingID: "s1", CIK: "0000000002", CompanyName: "Beta Bio Inc.", FilingType: "S-1", FilingDate: now.AddDate(0, 0, -3), FilingURL: "https://sec.gov/beta/s1"},
				{FilingID: "s1a", CIK: "0000000002", CompanyName: "Beta Bio Inc.", FilingType: "S-1/A", FilingDate: now, FilingURL: "https://sec.gov/beta/s1a"},
			},
			wantStatus: "updating",
			wantLatest: "S-1/A",
		},
		{
			name: "effect marks effective before pricing",
			filings: []model.IPOFiling{
				{FilingID: "s1", CIK: "0000000007", CompanyName: "Gamma Cloud Inc.", FilingType: "S-1/A", FilingDate: now.AddDate(0, 0, -2), FilingURL: "https://sec.gov/gamma/s1a"},
				{FilingID: "effect", CIK: "0000000007", CompanyName: "Gamma Cloud Inc.", FilingType: "EFFECT", FilingDate: now, FilingURL: "https://sec.gov/gamma/effect"},
			},
			wantStatus: "effective",
			wantLatest: "EFFECT",
		},
		{
			name: "424B marks priced",
			filings: []model.IPOFiling{
				{FilingID: "f1", CIK: "0000000003", CompanyName: "Cedar AI Ltd.", FilingType: "F-1", FilingDate: now.AddDate(0, 0, -4), FilingURL: "https://sec.gov/cedar/f1"},
				{FilingID: "424b4", CIK: "0000000003", CompanyName: "Cedar AI Ltd.", FilingType: "424B4", FilingDate: now, FilingURL: "https://sec.gov/cedar/424b4"},
			},
			wantStatus: "priced",
			wantLatest: "424B4",
		},
		{
			name: "withdrawn beats amendment",
			filings: []model.IPOFiling{
				{FilingID: "s1", CIK: "0000000004", CompanyName: "Delta Energy Inc.", FilingType: "S-1/A", FilingDate: now.AddDate(0, 0, -2), FilingURL: "https://sec.gov/delta/s1a"},
				{FilingID: "rw", CIK: "0000000004", CompanyName: "Delta Energy Inc.", FilingType: "RW", FilingDate: now, FilingURL: "https://sec.gov/delta/rw"},
			},
			wantStatus: "withdrawn",
			wantLatest: "RW",
		},
		{
			name: "watch target ticker alone does not confirm listing",
			filings: []model.IPOFiling{{
				FilingID: "s1", CIK: "0000000005", CompanyName: "Echo Robotics Inc.", FilingType: "S-1", FilingDate: now, FilingURL: "https://sec.gov/echo/s1",
			}},
			targets:    []model.WatchTarget{{Ticker: "ECHO", CompanyName: "Echo Robotics Inc.", CIK: "0000000005", TargetType: "stock", Status: "enabled"}},
			wantStatus: "new",
			wantLatest: "S-1",
		},
		{
			name: "stale after long inactivity",
			filings: []model.IPOFiling{{
				FilingID: "old-s1", CIK: "0000000006", CompanyName: "Foxtrot Cloud Inc.", FilingType: "S-1", FilingDate: now.AddDate(0, 0, -80), FilingURL: "https://sec.gov/foxtrot/s1",
			}},
			wantStatus: "stale",
			wantLatest: "S-1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := testDB(t)
			if err := db.Create(&tt.filings).Error; err != nil {
				t.Fatalf("seed ipo filings: %v", err)
			}
			if len(tt.targets) > 0 {
				if err := db.Create(&tt.targets).Error; err != nil {
					t.Fatalf("seed targets: %v", err)
				}
			}
			svc := NewIPORadarService(db, &fakeSECClient{}, &fakeNotifier{}, NewConfigService(db, NewAuditService(db)))
			got, err := svc.ListCompanies(context.Background(), IPOCompanyFilter{IncludeEnded: true, Page: 1, PageSize: 10}, now)
			if err != nil {
				t.Fatalf("ListCompanies: %v", err)
			}
			if got.Total != 1 || len(got.Items) != 1 {
				t.Fatalf("page = %+v, want one company", got)
			}
			item := got.Items[0]
			if item.Status != tt.wantStatus || item.LatestFilingType != tt.wantLatest {
				t.Fatalf("company = %+v, want status=%s latest=%s", item, tt.wantStatus, tt.wantLatest)
			}
		})
	}
}

func TestSortIPOCompaniesTableDriven(t *testing.T) {
	now := time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)
	items := []IPOCompanyItem{
		{CIK: "3", CompanyName: "Gamma", Status: "new", LatestFilingDate: now.AddDate(0, 0, -1), LatestAcceptedAt: ptrTime(now.Add(-time.Hour))},
		{CIK: "2", CompanyName: "Beta", Status: "stale", LatestFilingDate: now, LatestAcceptedAt: ptrTime(now.Add(-2 * time.Hour))},
		{CIK: "1", CompanyName: "Alpha", Status: "updating", LatestFilingDate: now.Add(30 * time.Minute)},
		{CIK: "4", CompanyName: "Alpha", Status: "new", LatestFilingDate: now.AddDate(0, 0, -1), LatestAcceptedAt: ptrTime(now.Add(-time.Hour))},
		{CIK: "5", CompanyName: "ListedCo", Status: "listed", LatestFilingDate: now.Add(time.Hour), LatestAcceptedAt: ptrTime(now.Add(time.Hour))},
	}
	tests := []struct {
		name      string
		sortBy    string
		sortOrder string
		wantCIKs  []string
	}{
		{name: "defaults to active IPOs before completed companies", wantCIKs: []string{"1", "4", "3", "5", "2"}},
		{name: "sorts latest SEC activity ascending", sortBy: "latest_update", sortOrder: "asc", wantCIKs: []string{"2", "4", "3", "1", "5"}},
		{name: "sorts status ascending with latest activity tie break", sortBy: "status", sortOrder: "asc", wantCIKs: []string{"4", "3", "1", "5", "2"}},
		{name: "sorts status descending with latest activity tie break", sortBy: "status", sortOrder: "desc", wantCIKs: []string{"2", "5", "1", "4", "3"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := append([]IPOCompanyItem(nil), items...)
			sortIPOCompanies(got, tt.sortBy, tt.sortOrder)
			for index, wantCIK := range tt.wantCIKs {
				if got[index].CIK != wantCIK {
					t.Fatalf("item %d cik = %s, want %s; items=%+v", index, got[index].CIK, wantCIK, got)
				}
			}
		})
	}
}

func TestIPORadarCompanyOverrideTableDriven(t *testing.T) {
	now := time.Date(2026, 6, 18, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		name       string
		input      IPOCompanyOverrideInput
		wantStatus string
		wantSource string
	}{
		{
			name:       "manual status override wins",
			input:      IPOCompanyOverrideInput{StatusOverride: "withdrawn", FinalTicker: "ACME", Note: "confirmed withdrawn"},
			wantStatus: "withdrawn",
			wantSource: "manual",
		},
		{
			name:       "final ticker without status keeps system status",
			input:      IPOCompanyOverrideInput{FinalTicker: "ACME"},
			wantStatus: "new",
			wantSource: "system",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := testDB(t)
			if err := db.Create(&model.IPOFiling{
				FilingID: "s1-" + tt.name, CIK: "0000000001", CompanyName: "Acme Space Inc.", FilingType: "S-1", FilingDate: now, FilingURL: "https://sec.gov/acme/s1",
			}).Error; err != nil {
				t.Fatalf("seed ipo filing: %v", err)
			}
			svc := NewIPORadarService(db, &fakeSECClient{}, &fakeNotifier{}, NewConfigService(db, NewAuditService(db)))
			if _, err := svc.UpsertCompanyOverride(context.Background(), "0000000001", tt.input); err != nil {
				t.Fatalf("UpsertCompanyOverride: %v", err)
			}
			got, err := svc.ListCompanies(context.Background(), IPOCompanyFilter{IncludeEnded: true, Page: 1, PageSize: 10}, now)
			if err != nil {
				t.Fatalf("ListCompanies: %v", err)
			}
			if got.Total != 1 {
				t.Fatalf("total = %d, want 1", got.Total)
			}
			if got.Items[0].Status != tt.wantStatus || got.Items[0].StatusSource != tt.wantSource {
				t.Fatalf("company = %+v, want status=%s source=%s", got.Items[0], tt.wantStatus, tt.wantSource)
			}
		})
	}
}

func TestShouldNotifyFilingTableDriven(t *testing.T) {
	now := time.Date(2026, 6, 18, 10, 30, 0, 0, time.UTC)
	filing := model.Filing{FilingType: "8-K", Title: "Merger agreement", CompanyName: "Acme Inc."}
	tests := []struct {
		name     string
		settings NotificationSettings
		want     bool
	}{
		{name: "default allows notification", settings: NotificationSettings{}, want: true},
		{name: "important only allows 8-K", settings: NotificationSettings{ImportantOnly: true}, want: true},
		{name: "filing type mismatch blocks", settings: NotificationSettings{FilingTypes: []string{"10-K"}}, want: false},
		{name: "keyword match allows", settings: NotificationSettings{Keywords: []string{"merger"}}, want: true},
		{name: "keyword mismatch blocks", settings: NotificationSettings{Keywords: []string{"bankruptcy"}}, want: false},
		{name: "quiet hours blocks", settings: NotificationSettings{QuietHoursEnabled: true, QuietHoursStart: "09:00", QuietHoursEnd: "11:00"}, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldNotifyFiling(filing, tt.settings, now); got != tt.want {
				t.Fatalf("shouldNotifyFiling = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestFilingServiceRefreshesEnabledTargetsDeduplicatesAndSuppressesInitialNotification(t *testing.T) {
	db := testDB(t)
	audit := NewAuditService(db)
	targets := NewWatchTargetService(db, audit)
	configs := NewConfigService(db, audit)
	notifier := &fakeNotifier{}
	filingDate := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)

	if _, err := targets.Create(context.Background(), WatchTargetInput{
		Ticker: "AAPL", CompanyName: "Apple Inc.", CIK: "0000320193", TargetType: "stock", Status: "enabled",
	}, "tester"); err != nil {
		t.Fatalf("create target: %v", err)
	}
	if err := configs.UpsertMany(context.Background(), []ConfigInput{
		{Key: "telegram.enabled", Value: "true", ValueType: "bool", Category: "telegram"},
		{Key: "telegram.chat_id", Value: "10001", ValueType: "string", Category: "telegram"},
		{Key: "telegram.bot_token", Value: "token", ValueType: "string", Category: "telegram", Encrypted: true},
	}, "tester"); err != nil {
		t.Fatalf("upsert telegram config: %v", err)
	}

	svc := NewFilingService(db, &fakeSECClient{filings: []sec.FilingResult{{
		FilingID: "0000320193-26-000001", AccessionNumber: "0000320193-26-000001",
		Ticker: "AAPL", CIK: "0000320193", CompanyName: "Apple Inc.",
		FilingType: "8-K", FilingDate: filingDate, FilingURL: "https://sec.gov/aapl/8k", Title: "Current report",
	}}}, notifier, configs)

	first, err := svc.Refresh(context.Background())
	if err != nil {
		t.Fatalf("refresh filings: %v", err)
	}
	second, err := svc.Refresh(context.Background())
	if err != nil {
		t.Fatalf("refresh filings second time: %v", err)
	}
	if first.NewFilings != 1 || second.NewFilings != 0 {
		t.Fatalf("new filings first=%d second=%d, want 1 then 0", first.NewFilings, second.NewFilings)
	}
	if len(notifier.messages) != 0 {
		t.Fatalf("notifications = %d, want initial sync suppressed", len(notifier.messages))
	}
	var batch model.NotificationBatch
	if err := db.Where("sync_run_id = ?", first.SyncRunID).First(&batch).Error; err != nil {
		t.Fatalf("load notification batch: %v", err)
	}
	if batch.Status != "suppressed" || batch.SuppressedCount != 1 {
		t.Fatalf("batch = %+v, want one suppressed item", batch)
	}

	page, err := svc.List(context.Background(), FilingFilter{Ticker: "AAPL", Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("list filings: %v", err)
	}
	if page.Total != 1 || page.Items[0].FilingType != "8-K" {
		t.Fatalf("filing page total=%d type=%q, want one 8-K", page.Total, page.Items[0].FilingType)
	}

	var target model.WatchTarget
	if err := db.Where("ticker = ?", "AAPL").First(&target).Error; err != nil {
		t.Fatalf("load target: %v", err)
	}
	if target.LastSyncStatus != "success" || target.LastNewFilings != 0 || target.LastSyncAt == nil {
		t.Fatalf("target sync status = %+v", target)
	}

	runs, err := svc.ListSyncRuns(context.Background(), SyncRunFilter{Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("list sync runs: %v", err)
	}
	if runs.Total != 2 || runs.Items[0].Status != "success" {
		t.Fatalf("sync runs = %+v", runs)
	}
}

func TestFilingServiceSyncFiltersFundClass(t *testing.T) {
	db := testDB(t)
	configs := NewConfigService(db, NewAuditService(db))
	target := model.WatchTarget{Ticker: "DRAM", CompanyName: "DRAM ETF", CIK: "0001976517", TargetType: "etf", FundSeriesID: "S000102337", FundClassID: "C000272806", Status: "enabled"}
	secClient := &fakeFundSECClient{
		fakeSECClient: &fakeSECClient{filings: []sec.FilingResult{
			{FilingID: "keep", AccessionNumber: "keep", CIK: target.CIK, FilingType: "N-CSR", FilingDate: time.Now().UTC()},
			{FilingID: "drop", AccessionNumber: "drop", CIK: target.CIK, FilingType: "N-CSR", FilingDate: time.Now().UTC()},
		}},
		matches: map[string]bool{"keep": true, "drop": false},
	}
	result, err := NewFilingService(db, secClient, &fakeNotifier{}, configs).RefreshTargets(context.Background(), []model.WatchTarget{target})
	if err != nil || result.NewFilings != 1 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	var filings []model.Filing
	if err := db.Order("accession_number ASC").Find(&filings).Error; err != nil {
		t.Fatalf("load filings: %v", err)
	}
	if len(filings) != 1 || filings[0].AccessionNumber != "keep" {
		t.Fatalf("stored filings=%+v, want only keep", filings)
	}
	var detail model.SyncRunDetail
	if err := db.Where("sync_run_id = ?", result.SyncRunID).First(&detail).Error; err != nil {
		t.Fatalf("load sync detail: %v", err)
	}
	if detail.WarningMessage != "fund identity filtered 1 trust filings (class_not_found: 1)" {
		t.Fatalf("warning=%q", detail.WarningMessage)
	}
}

func TestFilingServiceAssociatesOneFundFilingWithEveryMatchedTarget(t *testing.T) {
	db := testDB(t)
	configs := NewConfigService(db, NewAuditService(db))
	first := model.WatchTarget{Ticker: "DRAM", CompanyName: "Roundhill Memory ETF", CIK: "0001976517", TargetType: "etf", FundSeriesID: "S000102337", FundClassID: "C000272806", Status: "enabled"}
	second := model.WatchTarget{Ticker: "DRAMX", CompanyName: "Roundhill Memory ETF Institutional", CIK: "0001976517", TargetType: "etf", FundSeriesID: "S000102337", FundClassID: "C000272807", Status: "enabled"}
	if err := db.Create(&first).Error; err != nil {
		t.Fatalf("create first target: %v", err)
	}
	if err := db.Create(&second).Error; err != nil {
		t.Fatalf("create second target: %v", err)
	}
	filing := sec.FilingResult{FilingID: "0001976517-26-000001", AccessionNumber: "0001976517-26-000001", CIK: first.CIK, FilingType: "N-CSR", FilingDate: time.Now().UTC()}
	secClient := &fakeFundMetadataSECClient{
		fakeFundSECClient: &fakeFundSECClient{fakeSECClient: &fakeSECClient{filings: []sec.FilingResult{filing}}},
		metadata:          map[string]sec.FundFilingMetadata{filing.AccessionNumber: {Relationships: []sec.FundFilingRelationship{{SeriesID: first.FundSeriesID, ClassID: first.FundClassID}, {SeriesID: second.FundSeriesID, ClassID: second.FundClassID}}}},
	}
	result, err := NewFilingService(db, secClient, &fakeNotifier{}, configs).RefreshTargets(context.Background(), []model.WatchTarget{first, second})
	if err != nil || result.NewFilings != 1 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	var documents int64
	if err := db.Model(&model.Filing{}).Count(&documents).Error; err != nil || documents != 1 {
		t.Fatalf("documents=%d err=%v", documents, err)
	}
	var associations int64
	if err := db.Model(&model.WatchTargetFiling{}).Count(&associations).Error; err != nil || associations != 2 {
		t.Fatalf("associations=%d err=%v", associations, err)
	}
	for _, target := range []model.WatchTarget{first, second} {
		var detail model.SyncRunDetail
		if err := db.Where("sync_run_id = ? AND target_id = ?", result.SyncRunID, target.ID).First(&detail).Error; err != nil || detail.NewFilings != 1 {
			t.Fatalf("target=%s detail=%+v err=%v", target.Ticker, detail, err)
		}
	}
	var notificationItems int64
	if err := db.Model(&model.NotificationBatchItem{}).Count(&notificationItems).Error; err != nil || notificationItems != 2 {
		t.Fatalf("notification items=%d err=%v", notificationItems, err)
	}
	for _, ticker := range []string{"DRAM", "DRAMX"} {
		page, err := NewFilingService(db, secClient, &fakeNotifier{}, configs).List(context.Background(), FilingFilter{Ticker: ticker, Page: 1, PageSize: 10})
		if err != nil || page.Total != 1 || page.Items[0].AccessionNumber != filing.AccessionNumber {
			t.Fatalf("ticker=%s page=%+v err=%v", ticker, page, err)
		}
	}
}

func TestFilingServiceCachesFundIndexMetadataAcrossTargetClasses(t *testing.T) {
	db := testDB(t)
	configs := NewConfigService(db, NewAuditService(db))
	first := model.WatchTarget{Ticker: "DRAM", CompanyName: "Roundhill Memory ETF", CIK: "0001976517", TargetType: "etf", FundSeriesID: "S000102337", FundClassID: "C000272806", Status: "enabled"}
	second := model.WatchTarget{Ticker: "DRAMX", CompanyName: "Roundhill Memory ETF Institutional", CIK: "0001976517", TargetType: "etf", FundSeriesID: "S000102337", FundClassID: "C000272807", Status: "enabled"}
	filing := sec.FilingResult{FilingID: "0001976517-26-000002", AccessionNumber: "0001976517-26-000002", CIK: first.CIK, FilingType: "N-CSR", FilingDate: time.Now().UTC()}
	secClient := &fakeFundMetadataSECClient{
		fakeFundSECClient: &fakeFundSECClient{fakeSECClient: &fakeSECClient{filings: []sec.FilingResult{filing}}},
		metadata:          map[string]sec.FundFilingMetadata{filing.AccessionNumber: {Relationships: []sec.FundFilingRelationship{{SeriesID: first.FundSeriesID, ClassID: first.FundClassID}, {SeriesID: second.FundSeriesID, ClassID: second.FundClassID}}}},
	}
	if _, err := NewFilingService(db, secClient, &fakeNotifier{}, configs).RefreshTargets(context.Background(), []model.WatchTarget{first, second}); err != nil {
		t.Fatalf("refresh targets: %v", err)
	}
	if secClient.metadataCalls[filing.AccessionNumber] != 1 {
		t.Fatalf("index parses=%d, want one accession-level parse", secClient.metadataCalls[filing.AccessionNumber])
	}
	var cached model.FundFilingIdentity
	if err := db.Where("cik = ? AND accession_number = ?", first.CIK, filing.AccessionNumber).First(&cached).Error; err != nil {
		t.Fatalf("load cached metadata: %v", err)
	}
	if cached.ParseStatus != "parsed" || !strings.Contains(cached.RelationshipsJSON, first.FundClassID) || !strings.Contains(cached.RelationshipsJSON, second.FundClassID) {
		t.Fatalf("cached metadata=%+v", cached)
	}
}

func TestFilingServiceKeepsNotificationStatusPerTargetAssociation(t *testing.T) {
	db := testDB(t)
	configs := NewConfigService(db, NewAuditService(db))
	if err := configs.EnsureDefaults(context.Background()); err != nil {
		t.Fatalf("ensure defaults: %v", err)
	}
	if err := configs.UpsertMany(context.Background(), []ConfigInput{
		{Key: "telegram.enabled", Value: "true", ValueType: "bool", Category: "telegram"},
		{Key: "telegram.bot_token", Value: "token", ValueType: "string", Category: "telegram"},
		{Key: "telegram.chat_id", Value: "chat", ValueType: "string", Category: "telegram"},
	}, "tester"); err != nil {
		t.Fatalf("configure telegram: %v", err)
	}
	previous := time.Now().UTC().Add(-time.Hour)
	first := model.WatchTarget{Ticker: "DRAM", CompanyName: "Roundhill Memory ETF", CIK: "0001976517", TargetType: "etf", FundSeriesID: "S000102337", FundClassID: "C000272806", Status: "enabled", LastSyncAt: &previous}
	second := model.WatchTarget{Ticker: "DRAMX", CompanyName: "Roundhill Memory ETF Institutional", CIK: "0001976517", TargetType: "etf", FundSeriesID: "S000102337", FundClassID: "C000272807", Status: "enabled"}
	if err := db.Create(&[]model.WatchTarget{first, second}).Error; err != nil {
		t.Fatalf("create targets: %v", err)
	}
	if err := db.Where("ticker = ?", first.Ticker).First(&first).Error; err != nil {
		t.Fatalf("reload first: %v", err)
	}
	if err := db.Where("ticker = ?", second.Ticker).First(&second).Error; err != nil {
		t.Fatalf("reload second: %v", err)
	}
	filing := sec.FilingResult{FilingID: "0001976517-26-000003", AccessionNumber: "0001976517-26-000003", CIK: first.CIK, FilingType: "N-CSR", FilingDate: time.Now().UTC()}
	secClient := &fakeFundMetadataSECClient{
		fakeFundSECClient: &fakeFundSECClient{fakeSECClient: &fakeSECClient{filings: []sec.FilingResult{filing}}},
		metadata:          map[string]sec.FundFilingMetadata{filing.AccessionNumber: {Relationships: []sec.FundFilingRelationship{{SeriesID: first.FundSeriesID, ClassID: first.FundClassID}, {SeriesID: second.FundSeriesID, ClassID: second.FundClassID}}}},
	}
	notifier := &fakeNotifier{}
	if _, err := NewFilingService(db, secClient, notifier, configs).RefreshTargets(context.Background(), []model.WatchTarget{first, second}); err != nil {
		t.Fatalf("refresh targets: %v", err)
	}
	if notifier.calls != 1 {
		t.Fatalf("notification calls=%d, want one eligible target delivery", notifier.calls)
	}
	firstPage, err := NewFilingService(db, secClient, notifier, configs).List(context.Background(), FilingFilter{Ticker: first.Ticker, Page: 1, PageSize: 10})
	if err != nil || firstPage.Total != 1 || firstPage.Items[0].NotificationStatus != "success" {
		t.Fatalf("first notification page=%+v err=%v", firstPage, err)
	}
	secondPage, err := NewFilingService(db, secClient, notifier, configs).List(context.Background(), FilingFilter{Ticker: second.Ticker, Page: 1, PageSize: 10})
	if err != nil || secondPage.Total != 1 || secondPage.Items[0].NotificationStatus != "" {
		t.Fatalf("second notification page=%+v err=%v", secondPage, err)
	}
	var items []model.NotificationBatchItem
	if err := db.Order("target_id ASC").Find(&items).Error; err != nil || len(items) != 2 {
		t.Fatalf("notification items=%+v err=%v", items, err)
	}
	if items[0].TargetID != first.ID || items[0].Status != "sent" || items[1].TargetID != second.ID || items[1].Status != "suppressed" || items[1].Reason != "initial_sync" {
		t.Fatalf("notification items=%+v", items)
	}
}

func TestFilingServiceTargetScopedListFallsBackToLegacyNotificationHistory(t *testing.T) {
	db := testDB(t)
	now := time.Now().UTC()
	targets := []model.WatchTarget{
		{Ticker: "LEGST", CompanyName: "Legacy Stock", CIK: "0000001001", TargetType: "stock", Status: "enabled"},
		{Ticker: "LEGLOG", CompanyName: "Legacy ETF Log", CIK: "0000001002", TargetType: "etf", Status: "enabled"},
		{Ticker: "LEGBAT", CompanyName: "Legacy ETF Batch", CIK: "0000001003", TargetType: "etf", Status: "enabled"},
	}
	if err := db.Create(&targets).Error; err != nil {
		t.Fatalf("create targets: %v", err)
	}
	if err := db.Create(&[]model.Filing{
		{FilingID: "legacy-success", Ticker: "LEGST", CompanyName: "Legacy Stock", FilingType: "8-K", FilingDate: now, PulledAt: now, NotifiedAt: &now},
		{FilingID: "legacy-log-failed", Ticker: "LEGLOG", CompanyName: "Legacy ETF Log", FilingType: "N-CSR", FilingDate: now, PulledAt: now},
		{FilingID: "legacy-batch-failed", Ticker: "LEGBAT", CompanyName: "Legacy ETF Batch", FilingType: "N-CSR", FilingDate: now, PulledAt: now},
	}).Error; err != nil {
		t.Fatalf("create filings: %v", err)
	}
	if err := db.Create(&model.NotificationLog{FilingID: "legacy-log-failed", Channel: "telegram", Status: "failed"}).Error; err != nil {
		t.Fatalf("create notification log: %v", err)
	}
	if err := db.Create(&model.NotificationBatchItem{BatchID: 1, EntityKind: "filing", FilingID: "legacy-batch-failed", Status: "failed", Reason: "delivery_failed"}).Error; err != nil {
		t.Fatalf("create global batch item: %v", err)
	}
	svc := NewFilingService(db, &fakeSECClient{}, &fakeNotifier{}, NewConfigService(db, NewAuditService(db)))
	for _, tt := range []struct {
		ticker string
		status string
	}{
		{ticker: "LEGST", status: "success"},
		{ticker: "LEGLOG", status: "failed"},
		{ticker: "LEGBAT", status: "failed"},
	} {
		page, err := svc.List(context.Background(), FilingFilter{Ticker: tt.ticker, Page: 1, PageSize: 10})
		if err != nil || page.Total != 1 || page.Items[0].NotificationStatus != tt.status {
			t.Fatalf("ticker=%s page=%+v err=%v", tt.ticker, page, err)
		}
		filtered, err := svc.List(context.Background(), FilingFilter{Ticker: tt.ticker, NotificationStatus: tt.status, Page: 1, PageSize: 10})
		if err != nil || filtered.Total != 1 {
			t.Fatalf("ticker=%s filtered=%+v err=%v", tt.ticker, filtered, err)
		}
	}
}

func TestWatchTargetDeleteRemovesAssociationsAndRestoresLegacyFilingVisibility(t *testing.T) {
	db := testDB(t)
	target := model.WatchTarget{Ticker: "CLEAN", CompanyName: "Cleanup Fund", CIK: "0000002001", TargetType: "stock", Status: "enabled"}
	if err := db.Create(&target).Error; err != nil {
		t.Fatalf("create target: %v", err)
	}
	filing := model.Filing{FilingID: "cleanup-target-filing", Ticker: target.Ticker, CompanyName: target.CompanyName, FilingType: "8-K", FilingDate: time.Now().UTC(), PulledAt: time.Now().UTC()}
	if err := db.Create(&filing).Error; err != nil {
		t.Fatalf("create filing: %v", err)
	}
	if err := db.Create(&model.WatchTargetFiling{TargetID: target.ID, FilingID: filing.ID}).Error; err != nil {
		t.Fatalf("create association: %v", err)
	}
	targetService := NewWatchTargetService(db, NewAuditService(db))
	if err := targetService.Delete(context.Background(), target.ID, "tester"); err != nil {
		t.Fatalf("delete target: %v", err)
	}
	if _, err := targetService.Create(context.Background(), WatchTargetInput{Ticker: target.Ticker, CompanyName: target.CompanyName, CIK: target.CIK, TargetType: "stock", Status: "enabled"}, "tester"); err != nil {
		t.Fatalf("recreate target: %v", err)
	}
	var associationCount int64
	if err := db.Model(&model.WatchTargetFiling{}).Count(&associationCount).Error; err != nil || associationCount != 0 {
		t.Fatalf("associations=%d err=%v", associationCount, err)
	}
	page, err := NewFilingService(db, &fakeSECClient{}, &fakeNotifier{}, NewConfigService(db, NewAuditService(db))).List(context.Background(), FilingFilter{Ticker: target.Ticker, Page: 1, PageSize: 10})
	if err != nil || page.Total != 1 || page.Items[0].FilingID != filing.FilingID {
		t.Fatalf("page=%+v err=%v", page, err)
	}
}

func TestFilingServiceCleanupRemovesTargetAssociations(t *testing.T) {
	db := testDB(t)
	now := time.Now().UTC()
	target := model.WatchTarget{Ticker: "RETENTION", CompanyName: "Retention Fund", CIK: "0000003001", TargetType: "stock", Status: "enabled"}
	filing := model.Filing{FilingID: "retention-filing", Ticker: target.Ticker, CompanyName: target.CompanyName, FilingType: "8-K", FilingDate: now.AddDate(0, 0, -31), PulledAt: now.AddDate(0, 0, -31)}
	if err := db.Create(&target).Error; err != nil {
		t.Fatalf("create target: %v", err)
	}
	if err := db.Create(&filing).Error; err != nil {
		t.Fatalf("create filing: %v", err)
	}
	if err := db.Create(&model.WatchTargetFiling{TargetID: target.ID, FilingID: filing.ID}).Error; err != nil {
		t.Fatalf("create association: %v", err)
	}
	deleted, err := NewFilingService(db, &fakeSECClient{}, &fakeNotifier{}, NewConfigService(db, NewAuditService(db))).Cleanup(context.Background(), 30, now)
	if err != nil || deleted != 1 {
		t.Fatalf("deleted=%d err=%v", deleted, err)
	}
	var associationCount int64
	if err := db.Model(&model.WatchTargetFiling{}).Count(&associationCount).Error; err != nil || associationCount != 0 {
		t.Fatalf("associations=%d err=%v", associationCount, err)
	}
}

func TestFilingServiceSyncWarningExplainsFundFilterReasons(t *testing.T) {
	db := testDB(t)
	configs := NewConfigService(db, NewAuditService(db))
	target := model.WatchTarget{Ticker: "DRAM", CompanyName: "DRAM ETF", CIK: "0001976517", TargetType: "etf", FundSeriesID: "S000102337", FundClassID: "C000272806", Status: "enabled"}
	secClient := &fakeFundSECClient{
		fakeSECClient: &fakeSECClient{filings: []sec.FilingResult{
			{FilingID: "series", AccessionNumber: "series", CIK: target.CIK, FilingType: "N-CSR", FilingDate: time.Now().UTC()},
			{FilingID: "class", AccessionNumber: "class", CIK: target.CIK, FilingType: "N-CSR", FilingDate: time.Now().UTC()},
		}},
		matches:      map[string]bool{"series": false, "class": false},
		matchReasons: map[string]string{"series": "series_not_found", "class": "class_not_found"},
	}
	result, err := NewFilingService(db, secClient, &fakeNotifier{}, configs).RefreshTargets(context.Background(), []model.WatchTarget{target})
	if err != nil || result.NewFilings != 0 || result.FailedTargets != 0 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	var detail model.SyncRunDetail
	if err := db.Where("sync_run_id = ?", result.SyncRunID).First(&detail).Error; err != nil {
		t.Fatalf("load sync detail: %v", err)
	}
	want := "fund identity filtered 2 trust filings (class_not_found: 1, series_not_found: 1)"
	if detail.WarningMessage != want {
		t.Fatalf("warning=%q, want %q", detail.WarningMessage, want)
	}
}

func TestFilingServiceRefreshTargetsFailsClosedOnInconsistentFundMatch(t *testing.T) {
	target := model.WatchTarget{Ticker: "DRAM", CompanyName: "DRAM ETF", CIK: "0001976517", TargetType: "etf", FundSeriesID: "S000102337", FundClassID: "C000272806", Status: "enabled"}
	tests := []struct {
		name   string
		seed   func(*gorm.DB)
		client *fakeFundSECClient
	}{
		{
			name: "live matched with non-match reason",
			client: &fakeFundSECClient{
				fakeSECClient: &fakeSECClient{filings: []sec.FilingResult{{FilingID: "live", AccessionNumber: "live", CIK: target.CIK, FilingType: "N-CSR", FilingDate: time.Now().UTC()}}},
				matchResults:  map[string]fakeFundMatchResult{"live": {matched: true, reason: "class_not_found"}},
			},
		},
		{
			name: "cached matched with non-match reason",
			seed: func(db *gorm.DB) {
				if err := db.Create(&model.FundFilingIdentity{
					CIK: target.CIK, AccessionNumber: "cached", SeriesIDsJSON: `["S000102337"]`, ClassIDsJSON: `["C000272806"]`,
					ParseStatus: "matched", ParseMessage: "class_not_found", CheckedAt: time.Now().UTC(),
				}).Error; err != nil {
					t.Fatalf("seed fund identity cache: %v", err)
				}
			},
			client: &fakeFundSECClient{fakeSECClient: &fakeSECClient{filings: []sec.FilingResult{{FilingID: "cached", AccessionNumber: "cached", CIK: target.CIK, FilingType: "N-CSR", FilingDate: time.Now().UTC()}}}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := testDB(t)
			if tt.seed != nil {
				tt.seed(db)
			}
			result, err := NewFilingService(db, tt.client, &fakeNotifier{}, NewConfigService(db, NewAuditService(db))).RefreshTargets(context.Background(), []model.WatchTarget{target})
			if err != nil || result.FailedTargets != 1 || result.NewFilings != 0 {
				t.Fatalf("result=%+v err=%v", result, err)
			}
			var detail model.SyncRunDetail
			if err := db.Where("sync_run_id = ?", result.SyncRunID).First(&detail).Error; err != nil {
				t.Fatalf("load sync detail: %v", err)
			}
			if detail.Status != "failed" || !strings.Contains(detail.ErrorMessage, "fund identity unavailable") {
				t.Fatalf("detail=%+v", detail)
			}
			var count int64
			if err := db.Model(&model.Filing{}).Count(&count).Error; err != nil {
				t.Fatalf("count filings: %v", err)
			}
			if count != 0 {
				t.Fatalf("filings=%d, want 0", count)
			}
		})
	}
}

func TestFilingServiceRefreshTargetsLegacyETFKeepsAllFilings(t *testing.T) {
	db := testDB(t)
	// RefreshTargets receives persisted rows directly, so legacy ETFs are not
	// revalidated through WatchTargetInput.toModel.
	target := model.WatchTarget{Ticker: "OLD", CompanyName: "Legacy ETF", CIK: "0001976517", TargetType: "etf", Status: "enabled"}
	secClient := &fakeSECClient{filings: []sec.FilingResult{
		{FilingID: "legacy-1", AccessionNumber: "legacy-1", CIK: target.CIK, FilingType: "N-CSR", FilingDate: time.Now().UTC()},
		{FilingID: "legacy-2", AccessionNumber: "legacy-2", CIK: target.CIK, FilingType: "N-CSR", FilingDate: time.Now().UTC()},
	}}
	result, err := NewFilingService(db, secClient, &fakeNotifier{}, NewConfigService(db, NewAuditService(db))).RefreshTargets(context.Background(), []model.WatchTarget{target})
	if err != nil || result.NewFilings != 2 || result.FailedTargets != 0 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestFilingServiceRefreshTargetsFailsExactETFWhenIdentityUnavailable(t *testing.T) {
	db := testDB(t)
	configs := NewConfigService(db, NewAuditService(db))
	etf := model.WatchTarget{Ticker: "DRAM", CompanyName: "DRAM ETF", CIK: "0001976517", TargetType: "etf", FundSeriesID: "S000102337", FundClassID: "C000272806", Status: "enabled"}
	stock := model.WatchTarget{Ticker: "AAPL", CompanyName: "Apple Inc.", CIK: "0000320193", TargetType: "stock", Status: "enabled"}
	secClient := &fakeSECClient{filingsByTicker: map[string][]sec.FilingResult{
		"DRAM": {{FilingID: "dram-1", AccessionNumber: "dram-1", CIK: etf.CIK, FilingType: "N-CSR", FilingDate: time.Now().UTC()}},
		"AAPL": {{FilingID: "aapl-1", AccessionNumber: "aapl-1", CIK: stock.CIK, FilingType: "8-K", FilingDate: time.Now().UTC()}},
	}}
	result, err := NewFilingService(db, secClient, &fakeNotifier{}, configs).RefreshTargets(context.Background(), []model.WatchTarget{etf, stock})
	if err != nil || result.FailedTargets != 1 || result.NewFilings != 1 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	var detail model.SyncRunDetail
	if err := db.Where("ticker = ?", "DRAM").First(&detail).Error; err != nil {
		t.Fatalf("load ETF detail: %v", err)
	}
	if detail.Status != "failed" || !strings.Contains(detail.ErrorMessage, "fund identity unavailable") {
		t.Fatalf("detail=%+v", detail)
	}
}

func TestFilingServiceRefreshTargetsSkipsAndRetriesIncompleteFundIdentity(t *testing.T) {
	db := testDB(t)
	configs := NewConfigService(db, NewAuditService(db))
	target := model.WatchTarget{Ticker: "KMEM", CompanyName: "KMEM ETF", CIK: "0001976517", TargetType: "etf", FundSeriesID: "S000102337", FundClassID: "C000272806", Status: "enabled"}
	filing := sec.FilingResult{FilingID: "kmem-1", AccessionNumber: "kmem-1", CIK: target.CIK, FilingType: "N-CSR", FilingDate: time.Now().UTC()}
	secClient := &fakeFundMetadataSECClient{
		fakeFundSECClient: &fakeFundSECClient{fakeSECClient: &fakeSECClient{filings: []sec.FilingResult{filing}}},
		metadata:          map[string]sec.FundFilingMetadata{filing.AccessionNumber: {Incomplete: true}},
	}
	svc := NewFilingService(db, secClient, &fakeNotifier{}, configs)

	first, err := svc.RefreshTargets(context.Background(), []model.WatchTarget{target})
	if err != nil || first.FailedTargets != 0 || first.NewFilings != 0 {
		t.Fatalf("first refresh=%+v err=%v", first, err)
	}
	var detail model.SyncRunDetail
	if err := db.Where("sync_run_id = ?", first.SyncRunID).First(&detail).Error; err != nil {
		t.Fatalf("load first detail: %v", err)
	}
	if detail.Status != "success" || !strings.Contains(detail.WarningMessage, "filing_identity_incomplete") {
		t.Fatalf("first detail=%+v", detail)
	}
	if got := secClient.metadataCalls[filing.AccessionNumber]; got != 1 {
		t.Fatalf("first metadata calls=%d, want 1", got)
	}

	second, err := svc.RefreshTargets(context.Background(), []model.WatchTarget{target})
	if err != nil || second.FailedTargets != 0 || secClient.metadataCalls[filing.AccessionNumber] != 1 {
		t.Fatalf("cooldown refresh=%+v calls=%d err=%v", second, secClient.metadataCalls[filing.AccessionNumber], err)
	}

	if err := db.Model(&model.FundFilingIdentity{}).
		Where("cik = ? AND accession_number = ?", target.CIK, filing.AccessionNumber).
		Update("checked_at", time.Now().UTC().Add(-fundFilingIdentityIncompleteRetryAfter-time.Minute)).Error; err != nil {
		t.Fatalf("expire cached parse: %v", err)
	}
	secClient.metadata[filing.AccessionNumber] = sec.FundFilingMetadata{Relationships: []sec.FundFilingRelationship{{SeriesID: target.FundSeriesID, ClassID: target.FundClassID}}}
	third, err := svc.RefreshTargets(context.Background(), []model.WatchTarget{target})
	if err != nil || third.FailedTargets != 0 || third.NewFilings != 1 {
		t.Fatalf("retry refresh=%+v err=%v", third, err)
	}
	if got := secClient.metadataCalls[filing.AccessionNumber]; got != 2 {
		t.Fatalf("retry metadata calls=%d, want 2", got)
	}
}

func TestFilingServiceRefreshTargetRetriesTransientFundMetadataFailure(t *testing.T) {
	db := testDB(t)
	configs := NewConfigService(db, NewAuditService(db))
	target := model.WatchTarget{Ticker: "KMEM", CompanyName: "KMEM ETF", CIK: "0001976517", TargetType: "etf", FundSeriesID: "S000102337", FundClassID: "C000272806", Status: "enabled"}
	if err := db.Create(&target).Error; err != nil {
		t.Fatalf("create target: %v", err)
	}
	filing := sec.FilingResult{FilingID: "kmem-timeout", AccessionNumber: "kmem-timeout", CIK: target.CIK, FilingType: "N-CSR", FilingDate: time.Now().UTC()}
	secClient := &fakeFundMetadataSECClient{
		fakeFundSECClient: &fakeFundSECClient{fakeSECClient: &fakeSECClient{filings: []sec.FilingResult{filing}}},
		metadata:          map[string]sec.FundFilingMetadata{},
		metadataErrs:      map[string]error{filing.AccessionNumber: errors.New("context deadline exceeded")},
	}
	svc := NewFilingService(db, secClient, &fakeNotifier{}, configs)

	first, err := svc.RefreshTarget(context.Background(), target.ID)
	if err != nil || first.FailedTargets != 1 {
		t.Fatalf("first refresh=%+v err=%v", first, err)
	}
	var detail model.SyncRunDetail
	if err := db.Where("sync_run_id = ?", first.SyncRunID).First(&detail).Error; err != nil {
		t.Fatalf("load failed detail: %v", err)
	}
	if !detail.Retryable || detail.FailureKind != "timeout" {
		t.Fatalf("detail=%+v, want retryable timeout", detail)
	}

	delete(secClient.metadataErrs, filing.AccessionNumber)
	secClient.metadata[filing.AccessionNumber] = sec.FundFilingMetadata{Relationships: []sec.FundFilingRelationship{{SeriesID: target.FundSeriesID, ClassID: target.FundClassID}}}
	second, err := svc.RefreshTarget(context.Background(), target.ID)
	if err != nil || second.FailedTargets != 0 || second.NewFilings != 1 {
		t.Fatalf("manual retry=%+v err=%v", second, err)
	}
	if got := secClient.metadataCalls[filing.AccessionNumber]; got != 2 {
		t.Fatalf("metadata calls=%d, want 2", got)
	}
}

func TestFilingServiceRefreshTargetsMarksIndexFailurePartial(t *testing.T) {
	db := testDB(t)
	configs := NewConfigService(db, NewAuditService(db))
	etf := model.WatchTarget{Ticker: "DRAM", CompanyName: "DRAM ETF", CIK: "0001976517", TargetType: "etf", FundSeriesID: "S000102337", FundClassID: "C000272806", Status: "enabled"}
	stock := model.WatchTarget{Ticker: "AAPL", CompanyName: "Apple Inc.", CIK: "0000320193", TargetType: "stock", Status: "enabled"}
	secClient := &fakeFundSECClient{
		fakeSECClient: &fakeSECClient{filingsByTicker: map[string][]sec.FilingResult{
			"DRAM": {{FilingID: "dram-1", AccessionNumber: "dram-1", CIK: etf.CIK, FilingType: "N-CSR", FilingDate: time.Now().UTC()}},
			"AAPL": {{FilingID: "aapl-1", AccessionNumber: "aapl-1", CIK: stock.CIK, FilingType: "8-K", FilingDate: time.Now().UTC()}},
		}},
		matchErrs: map[string]error{"dram-1": errors.New("index timeout")},
	}
	result, err := NewFilingService(db, secClient, &fakeNotifier{}, configs).RefreshTargets(context.Background(), []model.WatchTarget{etf, stock})
	if err != nil || result.FailedTargets != 1 || result.NewFilings != 1 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	var run model.SyncRun
	if err := db.First(&run, result.SyncRunID).Error; err != nil {
		t.Fatalf("load sync run: %v", err)
	}
	if run.Status != "partial" {
		t.Fatalf("run=%+v", run)
	}
	var count int64
	if err := db.Model(&model.Filing{}).Where("ticker = ?", "DRAM").Count(&count).Error; err != nil {
		t.Fatalf("count ETF filings: %v", err)
	}
	if count != 0 {
		t.Fatalf("ETF filings=%d, want 0", count)
	}
}

func TestFilingServiceRefreshTargetsCachesExactFundMatches(t *testing.T) {
	db := testDB(t)
	target := model.WatchTarget{Ticker: "DRAM", CompanyName: "DRAM ETF", CIK: "0001976517", TargetType: "etf", FundSeriesID: "S000102337", FundClassID: "C000272806", Status: "enabled"}
	secClient := &fakeFundSECClient{
		fakeSECClient: &fakeSECClient{filings: []sec.FilingResult{{FilingID: "keep", AccessionNumber: "keep", CIK: target.CIK, FilingType: "N-CSR", FilingDate: time.Now().UTC()}}},
		matches:       map[string]bool{"keep": true},
	}
	svc := NewFilingService(db, secClient, &fakeNotifier{}, NewConfigService(db, NewAuditService(db)))
	if _, err := svc.RefreshTargets(context.Background(), []model.WatchTarget{target}); err != nil {
		t.Fatalf("first refresh: %v", err)
	}
	if _, err := svc.RefreshTargets(context.Background(), []model.WatchTarget{target}); err != nil {
		t.Fatalf("second refresh: %v", err)
	}
	if secClient.matchCalls["keep"] != 1 {
		t.Fatalf("match calls=%d, want 1", secClient.matchCalls["keep"])
	}
	var cached model.FundFilingIdentity
	if err := db.Where("cik = ? AND accession_number = ?", target.CIK, "keep").First(&cached).Error; err != nil {
		t.Fatalf("load cached identity: %v", err)
	}
}

func TestWatchTargetServiceValidatesFundIdentityPairs(t *testing.T) {
	db := testDB(t)
	svc := NewWatchTargetService(db, NewAuditService(db))
	tests := []struct {
		name  string
		input WatchTargetInput
		valid bool
	}{
		{name: "valid ETF identity", input: WatchTargetInput{Ticker: "DRAM", CompanyName: "DRAM ETF", CIK: "0001976517", TargetType: "etf", FundSeriesID: "S000102337", FundClassID: "C000272806", Status: "enabled"}, valid: true},
		{name: "missing ETF identity", input: WatchTargetInput{Ticker: "DRAM", CompanyName: "DRAM ETF", CIK: "0001976517", TargetType: "etf", Status: "enabled"}},
		{name: "ETF identity missing CIK", input: WatchTargetInput{Ticker: "DRAM", CompanyName: "DRAM ETF", TargetType: "etf", FundSeriesID: "S000102337", FundClassID: "C000272806", Status: "enabled"}},
		{name: "partial identity", input: WatchTargetInput{Ticker: "DRAM", CompanyName: "DRAM ETF", TargetType: "etf", FundSeriesID: "S000102337", Status: "enabled"}},
		{name: "malformed identity", input: WatchTargetInput{Ticker: "DRAM", CompanyName: "DRAM ETF", TargetType: "etf", FundSeriesID: "bad", FundClassID: "C000272806", Status: "enabled"}},
		{name: "stock identity forbidden", input: WatchTargetInput{Ticker: "AAPL", CompanyName: "Apple Inc.", TargetType: "stock", FundSeriesID: "S000102337", FundClassID: "C000272806", Status: "enabled"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := svc.Create(context.Background(), tt.input, "tester")
			if (err == nil) != tt.valid {
				t.Fatalf("Create err=%v, valid=%v", err, tt.valid)
			}
		})
	}
}

func TestWatchTargetServiceRejectsImpreciseETFIdentityOnCreateAndUpdate(t *testing.T) {
	tests := []struct {
		name  string
		input WatchTargetInput
	}{
		{name: "no identity", input: WatchTargetInput{Ticker: "DRAM", CompanyName: "DRAM ETF", CIK: "0001976517", TargetType: "etf", Status: "enabled"}},
		{name: "series only", input: WatchTargetInput{Ticker: "DRAM", CompanyName: "DRAM ETF", CIK: "0001976517", TargetType: "etf", FundSeriesID: "S000102337", Status: "enabled"}},
		{name: "class only", input: WatchTargetInput{Ticker: "DRAM", CompanyName: "DRAM ETF", CIK: "0001976517", TargetType: "etf", FundClassID: "C000272806", Status: "enabled"}},
		{name: "missing CIK", input: WatchTargetInput{Ticker: "DRAM", CompanyName: "DRAM ETF", TargetType: "etf", FundSeriesID: "S000102337", FundClassID: "C000272806", Status: "enabled"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := testDB(t)
			svc := NewWatchTargetService(db, NewAuditService(db))
			if _, err := svc.Create(context.Background(), tt.input, "tester"); !errors.Is(err, ErrValidation) {
				t.Fatalf("Create error = %v, want validation", err)
			}
			legacy := model.WatchTarget{Ticker: "OLD", CompanyName: "Legacy ETF", CIK: "0001976517", TargetType: "etf", Status: "enabled"}
			if err := db.Create(&legacy).Error; err != nil {
				t.Fatalf("seed legacy ETF: %v", err)
			}
			if _, err := svc.Update(context.Background(), legacy.ID, tt.input, "tester"); !errors.Is(err, ErrValidation) {
				t.Fatalf("Update error = %v, want validation", err)
			}
		})
	}
}

func TestFilingServiceListTargetSyncDetailsTableDriven(t *testing.T) {
	now := time.Date(2026, 6, 20, 10, 0, 0, 0, time.UTC)
	tests := []struct {
		name      string
		targetID  uint
		limit     int
		wantIDs   []uint
		wantTotal int
	}{
		{name: "default limit newest first", targetID: 1, limit: 0, wantIDs: []uint{4, 2, 1}, wantTotal: 3},
		{name: "explicit limit", targetID: 1, limit: 1, wantIDs: []uint{4}, wantTotal: 1},
		{name: "filters target", targetID: 2, limit: 10, wantIDs: []uint{3}, wantTotal: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := testDB(t)
			details := []model.SyncRunDetail{
				{ID: 1, SyncRunID: 1, TargetID: 1, Ticker: "AAPL", Status: "success", StartedAt: now.Add(-3 * time.Hour)},
				{ID: 2, SyncRunID: 2, TargetID: 1, Ticker: "AAPL", Status: "success", StartedAt: now.Add(-2 * time.Hour)},
				{ID: 3, SyncRunID: 3, TargetID: 2, Ticker: "MSFT", Status: "success", StartedAt: now.Add(-1 * time.Hour)},
				{ID: 4, SyncRunID: 4, TargetID: 1, Ticker: "AAPL", Status: "failed", StartedAt: now.Add(-1 * time.Hour)},
				{ID: 5, SyncRunID: 5, TargetID: 1, Ticker: "AAPL", Status: "success", StartedAt: now.Add(-4 * time.Hour)},
			}
			if err := db.Create(&details).Error; err != nil {
				t.Fatalf("seed details: %v", err)
			}
			got, err := NewFilingService(db, &fakeSECClient{}, &fakeNotifier{}, NewConfigService(db, NewAuditService(db))).ListTargetSyncDetails(context.Background(), tt.targetID, tt.limit)
			if err != nil {
				t.Fatalf("ListTargetSyncDetails: %v", err)
			}
			if len(got) != tt.wantTotal {
				t.Fatalf("details = %+v, want total %d", got, tt.wantTotal)
			}
			for index, wantID := range tt.wantIDs {
				if got[index].ID != wantID {
					t.Fatalf("detail %d id = %d, want %d; details=%+v", index, got[index].ID, wantID, got)
				}
			}
		})
	}
}

func TestFilingServiceRefreshAppliesPullSettingsTableDriven(t *testing.T) {
	now := time.Now().UTC()
	tests := []struct {
		name          string
		target        model.WatchTarget
		configs       []ConfigInput
		filings       []sec.FilingResult
		wantInserted  int64
		wantFullFetch bool
	}{
		{
			name:   "first sync filters by days and max count",
			target: model.WatchTarget{Ticker: "TSLA", CompanyName: "Tesla Inc.", CIK: "0001318605", TargetType: "stock", Status: "enabled"},
			configs: []ConfigInput{
				{Key: "sec.initial_fetch_days", Value: "30", ValueType: "int", Category: "sec"},
				{Key: "sec.max_fetch_count", Value: "2", ValueType: "int", Category: "sec"},
				{Key: "sec.fetch_full_history", Value: "true", ValueType: "bool", Category: "sec"},
			},
			filings: []sec.FilingResult{
				{FilingID: "new-1", AccessionNumber: "new-1", Ticker: "TSLA", CIK: "0001318605", CompanyName: "Tesla Inc.", FilingType: "8-K", FilingDate: now.AddDate(0, 0, -1)},
				{FilingID: "new-2", AccessionNumber: "new-2", Ticker: "TSLA", CIK: "0001318605", CompanyName: "Tesla Inc.", FilingType: "10-Q", FilingDate: now.AddDate(0, 0, -2)},
				{FilingID: "new-3", AccessionNumber: "new-3", Ticker: "TSLA", CIK: "0001318605", CompanyName: "Tesla Inc.", FilingType: "4", FilingDate: now.AddDate(0, 0, -3)},
				{FilingID: "old-1", AccessionNumber: "old-1", Ticker: "TSLA", CIK: "0001318605", CompanyName: "Tesla Inc.", FilingType: "8-K", FilingDate: now.AddDate(0, 0, -45)},
			},
			wantInserted:  2,
			wantFullFetch: true,
		},
		{
			name:   "existing sync ignores initial days",
			target: model.WatchTarget{Ticker: "TSLA", CompanyName: "Tesla Inc.", CIK: "0001318605", TargetType: "stock", Status: "enabled", LastSyncAt: ptrTime(now.AddDate(0, 0, -1))},
			configs: []ConfigInput{
				{Key: "sec.initial_fetch_days", Value: "1", ValueType: "int", Category: "sec"},
				{Key: "sec.sync_window_days", Value: "0", ValueType: "int", Category: "sec"},
				{Key: "sec.max_fetch_count", Value: "0", ValueType: "int", Category: "sec"},
			},
			filings: []sec.FilingResult{
				{FilingID: "old-1", AccessionNumber: "old-1", Ticker: "TSLA", CIK: "0001318605", CompanyName: "Tesla Inc.", FilingType: "8-K", FilingDate: now.AddDate(0, 0, -45)},
			},
			wantInserted: 1,
		},
		{
			name:   "sync window filters every sync",
			target: model.WatchTarget{Ticker: "TSLA", CompanyName: "Tesla Inc.", CIK: "0001318605", TargetType: "stock", Status: "enabled", LastSyncAt: ptrTime(now.AddDate(0, 0, -1))},
			configs: []ConfigInput{
				{Key: "sec.initial_fetch_days", Value: "3650", ValueType: "int", Category: "sec"},
				{Key: "sec.sync_window_days", Value: "30", ValueType: "int", Category: "sec"},
				{Key: "sec.max_fetch_count", Value: "0", ValueType: "int", Category: "sec"},
			},
			filings: []sec.FilingResult{
				{FilingID: "recent-1", AccessionNumber: "recent-1", Ticker: "TSLA", CIK: "0001318605", CompanyName: "Tesla Inc.", FilingType: "8-K", FilingDate: now.AddDate(0, 0, -2)},
				{FilingID: "old-1", AccessionNumber: "old-1", Ticker: "TSLA", CIK: "0001318605", CompanyName: "Tesla Inc.", FilingType: "8-K", FilingDate: now.AddDate(0, 0, -45)},
			},
			wantInserted: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := testDB(t)
			if err := db.Create(&tt.target).Error; err != nil {
				t.Fatalf("seed target: %v", err)
			}
			configs := NewConfigService(db, NewAuditService(db))
			if len(tt.configs) > 0 {
				if err := configs.UpsertMany(context.Background(), tt.configs, "tester"); err != nil {
					t.Fatalf("seed configs: %v", err)
				}
			}
			secClient := &fakeSECClient{filings: tt.filings}
			svc := NewFilingService(db, secClient, &fakeNotifier{}, configs)

			if _, err := svc.Refresh(context.Background()); err != nil {
				t.Fatalf("Refresh: %v", err)
			}

			var count int64
			if err := db.Model(&model.Filing{}).Where("ticker = ?", "TSLA").Count(&count).Error; err != nil {
				t.Fatalf("count filings: %v", err)
			}
			if count != tt.wantInserted {
				t.Fatalf("inserted = %d, want %d", count, tt.wantInserted)
			}
			if len(secClient.queries) != 1 {
				t.Fatalf("queries = %d, want 1", len(secClient.queries))
			}
			if secClient.queries[0].FetchFullHistory != tt.wantFullFetch {
				t.Fatalf("FetchFullHistory = %v, want %v", secClient.queries[0].FetchFullHistory, tt.wantFullFetch)
			}
		})
	}
}

func TestFilingServiceRefreshTargetAndDetailsTableDriven(t *testing.T) {
	now := time.Now().UTC()
	tests := []struct {
		name       string
		seed       []model.WatchTarget
		secClient  *fakeSECClient
		run        func(context.Context, *FilingService) (RefreshResult, error)
		wantCalls  int
		wantFailed int
		assert     func(t *testing.T, db *gorm.DB, secClient *fakeSECClient, result RefreshResult)
	}{
		{
			name: "refresh target only syncs selected ticker",
			seed: []model.WatchTarget{
				{Ticker: "AAPL", CompanyName: "Apple Inc.", CIK: "0000320193", TargetType: "stock", Status: "enabled"},
				{Ticker: "MSFT", CompanyName: "Microsoft Corp.", CIK: "0000789019", TargetType: "stock", Status: "enabled"},
			},
			secClient: &fakeSECClient{filingsByTicker: map[string][]sec.FilingResult{
				"AAPL": {{FilingID: "aapl-1", AccessionNumber: "aapl-1", Ticker: "AAPL", CIK: "0000320193", CompanyName: "Apple Inc.", FilingType: "8-K", FilingDate: now}},
				"MSFT": {{FilingID: "msft-1", AccessionNumber: "msft-1", Ticker: "MSFT", CIK: "0000789019", CompanyName: "Microsoft Corp.", FilingType: "8-K", FilingDate: now}},
			}},
			run: func(ctx context.Context, svc *FilingService) (RefreshResult, error) {
				return svc.RefreshTarget(ctx, 1)
			},
			wantCalls: 1,
			assert: func(t *testing.T, db *gorm.DB, secClient *fakeSECClient, result RefreshResult) {
				if result.TargetsChecked != 1 || result.NewFilings != 1 {
					t.Fatalf("result = %+v", result)
				}
				if secClient.queries[0].Ticker != "AAPL" {
					t.Fatalf("queried ticker = %q, want AAPL", secClient.queries[0].Ticker)
				}
				var count int64
				if err := db.Model(&model.Filing{}).Where("ticker = ?", "MSFT").Count(&count).Error; err != nil {
					t.Fatalf("count msft: %v", err)
				}
				if count != 0 {
					t.Fatalf("MSFT filings = %d, want 0", count)
				}
			},
		},
		{
			name: "refresh records per target failure detail",
			seed: []model.WatchTarget{
				{Ticker: "AAPL", CompanyName: "Apple Inc.", CIK: "0000320193", TargetType: "stock", Status: "enabled"},
			},
			secClient: &fakeSECClient{listErrByTicker: map[string]error{"AAPL": fmt.Errorf("sec timeout")}},
			run: func(ctx context.Context, svc *FilingService) (RefreshResult, error) {
				return svc.Refresh(ctx)
			},
			wantCalls:  3,
			wantFailed: 1,
			assert: func(t *testing.T, db *gorm.DB, secClient *fakeSECClient, result RefreshResult) {
				details, err := NewFilingService(db, secClient, &fakeNotifier{}, NewConfigService(db, NewAuditService(db))).ListSyncRunDetails(context.Background(), result.SyncRunID)
				if err != nil {
					t.Fatalf("ListSyncRunDetails: %v", err)
				}
				if len(details) != 1 || details[0].Ticker != "AAPL" || details[0].Status != "failed" || details[0].ErrorMessage == "" {
					t.Fatalf("details = %+v", details)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := testDB(t)
			if err := db.Create(&tt.seed).Error; err != nil {
				t.Fatalf("seed targets: %v", err)
			}
			configs := NewConfigService(db, NewAuditService(db))
			svc := NewFilingService(db, tt.secClient, &fakeNotifier{}, configs)
			result, err := tt.run(context.Background(), svc)
			if err != nil {
				t.Fatalf("run: %v", err)
			}
			if tt.secClient.listCalls != tt.wantCalls {
				t.Fatalf("listCalls = %d, want %d", tt.secClient.listCalls, tt.wantCalls)
			}
			if result.FailedTargets != tt.wantFailed {
				t.Fatalf("FailedTargets = %d, want %d", result.FailedTargets, tt.wantFailed)
			}
			tt.assert(t, db, tt.secClient, result)
		})
	}
}

func TestFilingServiceRecordsActionableTargetFailureMetadata(t *testing.T) {
	db := testDB(t)
	target := model.WatchTarget{Ticker: "MISS", CompanyName: "Missing Inc.", CIK: "0000000001", TargetType: "stock", Status: "enabled"}
	if err := db.Create(&target).Error; err != nil {
		t.Fatalf("seed target: %v", err)
	}
	requestErr := &sec.RequestError{Operation: "submissions", StatusCode: http.StatusNotFound, Attempts: 1}
	client := &fakeSECClient{listErrByTicker: map[string]error{"MISS": requestErr}}
	svc := NewFilingService(db, client, &fakeNotifier{}, NewConfigService(db, NewAuditService(db)))
	result, err := svc.RefreshWithTrigger(context.Background(), "scheduler")
	if err != nil {
		t.Fatalf("RefreshWithTrigger: %v", err)
	}
	if client.listCalls != 1 {
		t.Fatalf("ListFilings calls = %d, want 1 for terminal 404", client.listCalls)
	}
	if result.FailedTargets != 1 {
		t.Fatalf("failed targets = %d, want 1", result.FailedTargets)
	}
	details, err := svc.ListSyncRunDetails(context.Background(), result.SyncRunID)
	if err != nil || len(details) != 1 {
		t.Fatalf("details = %+v, %v", details, err)
	}
	if details[0].FailureKind != "not_found" || details[0].Retryable || details[0].AttemptCount != 1 || details[0].NextRetryAt != nil {
		t.Fatalf("detail metadata = %+v", details[0])
	}
}

func TestFilingServiceSchedulerDefersRecentTerminalTargetFailure(t *testing.T) {
	db := testDB(t)
	now := time.Now().UTC()
	target := model.WatchTarget{Ticker: "HOLD", CompanyName: "Hold Inc.", CIK: "0000000002", TargetType: "stock", Status: "enabled"}
	if err := db.Create(&target).Error; err != nil {
		t.Fatalf("seed target: %v", err)
	}
	previousRun := model.SyncRun{StartedAt: now.Add(-time.Minute), Status: "partial", Trigger: "scheduler"}
	if err := db.Create(&previousRun).Error; err != nil {
		t.Fatalf("seed run: %v", err)
	}
	if err := db.Create(&model.SyncRunDetail{SyncRunID: previousRun.ID, TargetID: target.ID, Ticker: target.Ticker, Status: "failed", StartedAt: now.Add(-time.Minute), FinishedAt: &now, FailureKind: "not_found"}).Error; err != nil {
		t.Fatalf("seed detail: %v", err)
	}
	client := &fakeSECClient{filingsByTicker: map[string][]sec.FilingResult{"HOLD": {}}}
	svc := NewFilingService(db, client, &fakeNotifier{}, NewConfigService(db, NewAuditService(db)))
	result, err := svc.RefreshWithTrigger(context.Background(), "scheduler")
	if err != nil {
		t.Fatalf("RefreshWithTrigger: %v", err)
	}
	if result.DeferredTargets != 1 || result.FailedTargets != 0 || client.listCalls != 0 {
		t.Fatalf("result=%+v listCalls=%d", result, client.listCalls)
	}
	details, err := svc.ListSyncRunDetails(context.Background(), result.SyncRunID)
	if err != nil || len(details) != 1 || details[0].Status != "deferred" || details[0].WarningMessage == "" {
		t.Fatalf("details=%+v err=%v", details, err)
	}
}

func TestFilingServiceRecoveryRetriesDueFailureAcrossEarlierRun(t *testing.T) {
	db := testDB(t)
	now := time.Now().UTC()
	target := model.WatchTarget{Ticker: "RETRY", CompanyName: "Retry Inc.", CIK: "0000000003", TargetType: "stock", Status: "enabled"}
	if err := db.Create(&target).Error; err != nil {
		t.Fatalf("seed target: %v", err)
	}
	previous := model.SyncRun{StartedAt: now.Add(-time.Hour), FinishedAt: ptrTime(now.Add(-59 * time.Minute)), Status: "partial", Trigger: "scheduler"}
	if err := db.Create(&previous).Error; err != nil {
		t.Fatalf("seed run: %v", err)
	}
	if err := db.Create(&model.SyncRunDetail{SyncRunID: previous.ID, TargetID: target.ID, Ticker: target.Ticker, Status: "failed", Retryable: true, NextRetryAt: ptrTime(now.Add(-time.Minute)), StartedAt: now.Add(-time.Hour), FinishedAt: ptrTime(now.Add(-59 * time.Minute))}).Error; err != nil {
		t.Fatalf("seed retryable detail: %v", err)
	}
	client := &fakeSECClient{filingsByTicker: map[string][]sec.FilingResult{"RETRY": {}}}
	svc := NewFilingService(db, client, &fakeNotifier{}, NewConfigService(db, NewAuditService(db)))
	result, err := svc.RetryRecoverableFailures(context.Background(), 0)
	if err != nil || result.TargetsChecked != 1 || result.FailedTargets != 0 || client.listCalls != 1 {
		t.Fatalf("recovery result=%+v err=%v calls=%d", result, err, client.listCalls)
	}
	var prior model.SyncRunDetail
	if err := db.Where("sync_run_id = ?", previous.ID).First(&prior).Error; err != nil || prior.Retryable || prior.NextRetryAt != nil {
		t.Fatalf("prior detail=%+v err=%v", prior, err)
	}
}

func TestTaskConfigServiceMarksPartialOutcome(t *testing.T) {
	db := testDB(t)
	svc := NewTaskConfigService(db, NewAuditService(db))
	if err := svc.EnsureDefault(context.Background()); err != nil {
		t.Fatalf("EnsureDefault: %v", err)
	}
	if err := svc.MarkRunOutcome(context.Background(), "sec_filing_sync", time.Now().UTC(), PartialTask("1 个标的等待自动重试")); err != nil {
		t.Fatalf("MarkRunOutcome: %v", err)
	}
	var task model.TaskConfig
	if err := db.Where("task_name = ?", "sec_filing_sync").First(&task).Error; err != nil {
		t.Fatalf("load task: %v", err)
	}
	if task.LastStatus != "partial" || task.ConsecutiveFailures != 1 || task.LastErrorMessage == "" {
		t.Fatalf("task outcome = %+v", task)
	}
}

func TestTaskConfigServiceUpgradesOnlyHistoricalScheduleDefaults(t *testing.T) {
	db := testDB(t)
	if err := db.Create(&[]model.TaskConfig{
		{TaskName: "sqlite_backup", CronExpr: "15 9 * * *", Enabled: true},
		{TaskName: "institutional_holdings_sync", CronExpr: "15 9 * * 1-5", Enabled: true},
		{TaskName: "operational_health_notification_sync", CronExpr: "CRON_TZ=Asia/Shanghai 15 10 * * *", Enabled: false},
		{TaskName: "sec_filing_sync", CronExpr: "7 * * * *", Enabled: true},
	}).Error; err != nil {
		t.Fatal(err)
	}
	svc := NewTaskConfigService(db, NewAuditService(db))
	if err := svc.EnsureDefault(context.Background()); err != nil {
		t.Fatalf("EnsureDefault: %v", err)
	}
	got := map[string]string{}
	var tasks []model.TaskConfig
	if err := db.Where("task_name IN ?", []string{"sqlite_backup", "institutional_holdings_sync", "operational_health_notification_sync", "sec_filing_sync"}).Find(&tasks).Error; err != nil {
		t.Fatal(err)
	}
	for _, task := range tasks {
		got[task.TaskName] = task.CronExpr
	}
	if got["sqlite_backup"] != "15 3 * * *" || got["institutional_holdings_sync"] != "15 10 * * 1-5" || got["operational_health_notification_sync"] != "0 11 * * *" {
		t.Fatalf("upgraded cron expressions = %#v", got)
	}
	if got["sec_filing_sync"] != "7 * * * *" {
		t.Fatalf("custom cron was overwritten: %#v", got)
	}
}

func TestTaskConfigServiceTaskExecutionHistory(t *testing.T) {
	db := testDB(t)
	svc := NewTaskConfigService(db, NewAuditService(db))
	startedAt := time.Date(2026, 8, 12, 1, 0, 0, 0, time.UTC)
	first, err := svc.StartExecution(context.Background(), "market_trend_sync", "scheduled", startedAt)
	if err != nil {
		t.Fatalf("StartExecution: %v", err)
	}
	if err := svc.FinishExecution(context.Background(), first.ID, startedAt.Add(1500*time.Millisecond), PartialTask("1 个 ETF 待下次重试")); err != nil {
		t.Fatalf("FinishExecution partial: %v", err)
	}
	second, err := svc.StartExecution(context.Background(), "market_trend_sync", "manual", startedAt.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("StartExecution second: %v", err)
	}
	if err := svc.FinishExecution(context.Background(), second.ID, startedAt.Add(2*time.Minute+2*time.Second), nil); err != nil {
		t.Fatalf("FinishExecution success: %v", err)
	}
	page, err := svc.ListExecutions(context.Background(), TaskExecutionFilter{TaskName: "market_trend_sync", Status: "partial", Page: 1, PageSize: 20})
	if err != nil {
		t.Fatalf("ListExecutions: %v", err)
	}
	if page.Total != 1 || len(page.Items) != 1 || page.Items[0].Status != "partial" || page.Items[0].Trigger != "scheduled" || page.Items[0].DurationMS < 1400 || page.Items[0].ErrorMessage == "" {
		t.Fatalf("execution page = %#v", page)
	}
	if recovered, err := svc.RecoverInterruptedExecutions(context.Background(), startedAt.Add(5*time.Minute)); err != nil || recovered != 0 {
		t.Fatalf("RecoverInterruptedExecutions = %d, %v; want 0, nil", recovered, err)
	}
}

func TestTaskConfigServiceHasRecentManualSuccess(t *testing.T) {
	db := testDB(t)
	svc := NewTaskConfigService(db, NewAuditService(db))
	now := time.Date(2026, 8, 13, 1, 35, 0, 0, time.UTC)
	finishedAt := now.Add(-5 * time.Minute)
	if err := db.Create(&model.TaskExecution{TaskName: "ipo_radar_sync", Trigger: "manual", Status: "success", StartedAt: finishedAt.Add(-time.Minute), FinishedAt: &finishedAt}).Error; err != nil {
		t.Fatal(err)
	}
	if ok, err := svc.HasRecentManualSuccess(context.Background(), "ipo_radar_sync", now.Add(-10*time.Minute)); err != nil || !ok {
		t.Fatalf("HasRecentManualSuccess = %v, %v; want true, nil", ok, err)
	}
	if ok, err := svc.HasRecentManualSuccess(context.Background(), "ipo_radar_sync", now.Add(-2*time.Minute)); err != nil || ok {
		t.Fatalf("HasRecentManualSuccess outside window = %v, %v; want false, nil", ok, err)
	}
}

func TestWatchTargetServiceValidationTableDriven(t *testing.T) {
	tests := []struct {
		name  string
		input WatchTargetInput
	}{
		{name: "missing ticker", input: WatchTargetInput{CompanyName: "Apple Inc.", TargetType: "stock", Status: "enabled"}},
		{name: "missing company", input: WatchTargetInput{Ticker: "AAPL", TargetType: "stock", Status: "enabled"}},
		{name: "invalid type", input: WatchTargetInput{Ticker: "AAPL", CompanyName: "Apple Inc.", TargetType: "fund", Status: "enabled"}},
		{name: "invalid status", input: WatchTargetInput{Ticker: "AAPL", CompanyName: "Apple Inc.", TargetType: "stock", Status: "paused"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := testDB(t)
			svc := NewWatchTargetService(db, NewAuditService(db))
			if _, err := svc.Create(context.Background(), tt.input, "tester"); !errors.Is(err, ErrValidation) {
				t.Fatalf("Create err = %v, want validation", err)
			}
		})
	}
}

func TestWatchTargetServiceMutationsTableDriven(t *testing.T) {
	tests := []struct {
		name   string
		action func(t *testing.T, svc *WatchTargetService, id uint)
	}{
		{name: "get existing target", action: func(t *testing.T, svc *WatchTargetService, id uint) {
			got, err := svc.Get(context.Background(), id)
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			if got.Ticker != "AAPL" {
				t.Fatalf("ticker = %q", got.Ticker)
			}
		}},
		{name: "update existing target", action: func(t *testing.T, svc *WatchTargetService, id uint) {
			got, err := svc.Update(context.Background(), id, WatchTargetInput{Ticker: "MSFT", CompanyName: "Microsoft Corp.", TargetType: "stock", Status: "enabled"}, "tester")
			if err != nil {
				t.Fatalf("Update: %v", err)
			}
			if got.Ticker != "MSFT" {
				t.Fatalf("ticker = %q", got.Ticker)
			}
		}},
		{name: "delete existing target", action: func(t *testing.T, svc *WatchTargetService, id uint) {
			if err := svc.Delete(context.Background(), id, "tester"); err != nil {
				t.Fatalf("Delete: %v", err)
			}
			if _, err := svc.Get(context.Background(), id); !errors.Is(err, ErrNotFound) {
				t.Fatalf("Get after delete err = %v, want not found", err)
			}
		}},
		{name: "invalid status returns validation", action: func(t *testing.T, svc *WatchTargetService, id uint) {
			if _, err := svc.SetStatus(context.Background(), id, "paused", "tester"); !errors.Is(err, ErrValidation) {
				t.Fatalf("SetStatus err = %v, want validation", err)
			}
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := testDB(t)
			svc := NewWatchTargetService(db, NewAuditService(db))
			target, err := svc.Create(context.Background(), WatchTargetInput{Ticker: "AAPL", CompanyName: "Apple Inc.", TargetType: "stock", Status: "enabled"}, "tester")
			if err != nil {
				t.Fatalf("Create: %v", err)
			}
			tt.action(t, svc, target.ID)
		})
	}
}

func TestConfigHelpersTableDriven(t *testing.T) {
	maskTests := []struct {
		name string
		in   string
		want string
	}{
		{name: "empty", in: "", want: ""},
		{name: "short", in: "abc", want: "******"},
		{name: "long", in: "123456:secret-token", want: "123******ken"},
	}

	for _, tt := range maskTests {
		t.Run(tt.name, func(t *testing.T) {
			if got := maskSecret(tt.in); got != tt.want {
				t.Fatalf("maskSecret = %q, want %q", got, tt.want)
			}
		})
	}

	maskedTests := []struct {
		name string
		in   string
		want bool
	}{
		{name: "masked", in: "tok******ken", want: true},
		{name: "not masked", in: "token", want: false},
		{name: "empty", in: "", want: false},
	}

	for _, tt := range maskedTests {
		t.Run("is masked "+tt.name, func(t *testing.T) {
			if got := IsMaskedSecret(tt.in); got != tt.want {
				t.Fatalf("IsMaskedSecret = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestTaskConfigServiceTableDriven(t *testing.T) {
	tests := []struct {
		name string
		run  func(t *testing.T, svc *TaskConfigService)
	}{
		{name: "ensure default is idempotent", run: func(t *testing.T, svc *TaskConfigService) {
			if err := svc.EnsureDefault(context.Background()); err != nil {
				t.Fatalf("EnsureDefault: %v", err)
			}
			if err := svc.EnsureDefault(context.Background()); err != nil {
				t.Fatalf("EnsureDefault second: %v", err)
			}
			tasks, err := svc.List(context.Background())
			if err != nil {
				t.Fatalf("List: %v", err)
			}
			if len(tasks) != 25 {
				t.Fatalf("tasks = %d, want 25", len(tasks))
			}
			names := map[string]bool{}
			for _, task := range tasks {
				names[task.TaskName] = true
			}
			if !names["sec_filing_sync"] || !names["ipo_radar_sync"] || !names["ipo_lifecycle_reconcile_sync"] || !names["ipo_offering_reconcile_sync"] || !names["ipo_listing_reconcile_sync"] || !names["candidate_notification_sync"] || !names["trade_setup_notification_sync"] || !names["small_cap_discovery_sync"] || !names["small_cap_discovery_full_sync"] || !names["watch_target_market_sync"] || !names["watch_target_earnings_sync"] || !names["notification_retry_sync"] || !names["sqlite_backup"] || !names["operation_history_cleanup"] || !names["operational_health_notification_sync"] || !names["macro_calendar_sync"] || !names["market_trend_sync"] || !names["us_futures_sync"] || !names["institutional_holdings_sync"] || !names["longbridge_candidate_research_sync"] || !names["longbridge_candidate_valuation_sync"] || !names["longbridge_watch_target_valuation_sync"] || !names["longbridge_watch_target_research_sync"] || !names["longbridge_candidate_option_research_sync"] || !names["longbridge_watch_target_option_research_sync"] {
				t.Fatalf("task names = %+v, want SEC, IPO, candidate/trade-plan notification, small-cap discovery, watch-target market/earnings, macro calendar, market trend, US futures, retry, backup, history cleanup, and operational alert tasks", names)
			}
		}},
		{name: "update task", run: func(t *testing.T, svc *TaskConfigService) {
			if err := svc.EnsureDefault(context.Background()); err != nil {
				t.Fatalf("EnsureDefault: %v", err)
			}
			tasks, _ := svc.List(context.Background())
			got, err := svc.Get(context.Background(), tasks[0].ID)
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			if got.TaskName != tasks[0].TaskName {
				t.Fatalf("Get task = %+v, want %s", got, tasks[0].TaskName)
			}
			updated, err := svc.Update(context.Background(), tasks[0].ID, TaskConfigInput{CronExpr: "*/30 * * * *", Enabled: false}, "tester")
			if err != nil {
				t.Fatalf("Update: %v", err)
			}
			if updated.CronExpr != "*/30 * * * *" || updated.Enabled {
				t.Fatalf("updated = %+v", updated)
			}
		}},
		{name: "mark run lifecycle", run: func(t *testing.T, svc *TaskConfigService) {
			if err := svc.EnsureDefault(context.Background()); err != nil {
				t.Fatalf("EnsureDefault: %v", err)
			}
			if err := svc.MarkRunStarted(context.Background(), "ipo_radar_sync"); err != nil {
				t.Fatalf("MarkRunStarted: %v", err)
			}
			tasks, err := svc.List(context.Background())
			if err != nil {
				t.Fatalf("List: %v", err)
			}
			var task model.TaskConfig
			for _, item := range tasks {
				if item.TaskName == "ipo_radar_sync" {
					task = item
					break
				}
			}
			if !task.Running || task.RunningSince == nil {
				t.Fatalf("running task = %+v, want running with start time", task)
			}
			ranAt := time.Date(2026, 6, 20, 9, 30, 0, 0, time.UTC)
			if err := svc.MarkRunFinished(context.Background(), "ipo_radar_sync", ranAt); err != nil {
				t.Fatalf("MarkRunFinished: %v", err)
			}
			tasks, err = svc.List(context.Background())
			if err != nil {
				t.Fatalf("List after finish: %v", err)
			}
			for _, item := range tasks {
				if item.TaskName == "ipo_radar_sync" {
					task = item
					break
				}
			}
			if task.Running || task.RunningSince != nil || task.LastRunAt == nil || !task.LastRunAt.Equal(ranAt) {
				t.Fatalf("finished task = %+v, want not running at %s", task, ranAt)
			}
		}},
		{name: "recover interrupted task", run: func(t *testing.T, svc *TaskConfigService) {
			if err := svc.EnsureDefault(context.Background()); err != nil {
				t.Fatalf("EnsureDefault: %v", err)
			}
			if err := svc.MarkRunStarted(context.Background(), "sqlite_backup"); err != nil {
				t.Fatalf("MarkRunStarted: %v", err)
			}
			recovered, err := svc.RecoverInterrupted(context.Background())
			if err != nil || recovered != 1 {
				t.Fatalf("RecoverInterrupted = %d, %v; want 1, nil", recovered, err)
			}
			var task model.TaskConfig
			if err := svc.db.Where("task_name = ?", "sqlite_backup").First(&task).Error; err != nil {
				t.Fatal(err)
			}
			if task.Running || task.LastStatus != "interrupted" || task.LastErrorMessage == "" {
				t.Fatalf("recovered task = %+v, want interrupted task state", task)
			}
		}},
		{name: "persist failed outcome then reset after success", run: func(t *testing.T, svc *TaskConfigService) {
			if err := svc.EnsureDefault(context.Background()); err != nil {
				t.Fatalf("EnsureDefault: %v", err)
			}
			failedAt := time.Date(2026, 7, 20, 1, 0, 0, 0, time.UTC)
			if err := svc.MarkRunOutcome(context.Background(), "sqlite_backup", failedAt, errors.New("provider token=secret failed")); err != nil {
				t.Fatalf("MarkRunOutcome failed: %v", err)
			}
			var task model.TaskConfig
			if err := svc.db.Where("task_name = ?", "sqlite_backup").First(&task).Error; err != nil {
				t.Fatal(err)
			}
			if task.LastStatus != "failed" || task.ConsecutiveFailures != 1 || task.LastErrorMessage == "" {
				t.Fatalf("failed task = %+v", task)
			}
			if err := svc.MarkRunOutcome(context.Background(), "sqlite_backup", failedAt.Add(time.Minute), nil); err != nil {
				t.Fatalf("MarkRunOutcome success: %v", err)
			}
			if err := svc.db.Where("task_name = ?", "sqlite_backup").First(&task).Error; err != nil {
				t.Fatal(err)
			}
			if task.LastStatus != "success" || task.ConsecutiveFailures != 0 || task.LastErrorMessage != "" {
				t.Fatalf("successful task = %+v", task)
			}
		}},
		{name: "missing task returns not found", run: func(t *testing.T, svc *TaskConfigService) {
			_, getErr := svc.Get(context.Background(), 404)
			if !errors.Is(getErr, ErrNotFound) {
				t.Fatalf("Get err = %v, want not found", getErr)
			}
			_, err := svc.Update(context.Background(), 404, TaskConfigInput{CronExpr: "* * * * *", Enabled: true}, "tester")
			if !errors.Is(err, ErrNotFound) {
				t.Fatalf("Update err = %v, want not found", err)
			}
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := testDB(t)
			tt.run(t, NewTaskConfigService(db, NewAuditService(db)))
		})
	}
}

func TestValidateTaskCronExprUsesGlobalTimezone(t *testing.T) {
	tests := []struct {
		name string
		expr string
		want bool
	}{
		{name: "standard expression", expr: "15 8 * * 2-6", want: true},
		{name: "empty expression", expr: "", want: false},
		{name: "invalid expression", expr: "every morning", want: false},
		{name: "per task cron timezone", expr: "CRON_TZ=Asia/Shanghai 15 8 * * 2-6", want: false},
		{name: "per task tz timezone", expr: "TZ=UTC 15 8 * * 2-6", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := validateTaskCronExpr(tt.expr); (err == nil) != tt.want {
				t.Fatalf("validateTaskCronExpr(%q) error = %v, want valid=%v", tt.expr, err, tt.want)
			}
		})
	}
}

func TestTaskConfigAddsNotificationRetryDefault(t *testing.T) {
	db := testDB(t)
	svc := NewTaskConfigService(db, NewAuditService(db))
	if err := svc.EnsureDefault(context.Background()); err != nil {
		t.Fatalf("EnsureDefault: %v", err)
	}

	var task model.TaskConfig
	if err := db.Where("task_name = ?", "notification_retry_sync").First(&task).Error; err != nil {
		t.Fatalf("load notification retry task: %v", err)
	}
	if !task.Enabled || task.CronExpr != "*/10 * * * *" {
		t.Fatalf("notification retry task = %+v, want enabled every 10 minutes", task)
	}
}

func TestNotificationServiceListTableDriven(t *testing.T) {
	tests := []struct {
		name   string
		filter NotificationLogFilter
		want   int64
	}{
		{name: "all", filter: NotificationLogFilter{Page: 1, PageSize: 20}, want: 2},
		{name: "by status", filter: NotificationLogFilter{Status: "failed", Page: 1, PageSize: 20}, want: 1},
		{name: "by channel", filter: NotificationLogFilter{Channel: "telegram", Page: 1, PageSize: 20}, want: 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := testDB(t)
			if err := db.Create(&[]model.NotificationLog{
				{FilingID: "1", Channel: "telegram", Status: "success"},
				{FilingID: "2", Channel: "telegram", Status: "failed"},
			}).Error; err != nil {
				t.Fatalf("seed logs: %v", err)
			}
			got, err := NewNotificationService(db).List(context.Background(), tt.filter)
			if err != nil {
				t.Fatalf("List: %v", err)
			}
			if got.Total != tt.want {
				t.Fatalf("total = %d, want %d", got.Total, tt.want)
			}
		})
	}
}

func TestFilingServiceTableDriven(t *testing.T) {
	filingDate := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		run  func(t *testing.T, db *gorm.DB, svc *FilingService)
	}{
		{name: "get existing filing", run: func(t *testing.T, db *gorm.DB, svc *FilingService) {
			filing := model.Filing{FilingID: "f1", Ticker: "AAPL", CompanyName: "Apple Inc.", FilingType: "10-K", FilingDate: filingDate, PulledAt: time.Now()}
			if err := db.Create(&filing).Error; err != nil {
				t.Fatalf("seed filing: %v", err)
			}
			got, err := svc.Get(context.Background(), filing.ID)
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			if got.FilingID != "f1" {
				t.Fatalf("filing id = %q", got.FilingID)
			}
		}},
		{name: "get missing filing", run: func(t *testing.T, db *gorm.DB, svc *FilingService) {
			if _, err := svc.Get(context.Background(), 99); !errors.Is(err, ErrNotFound) {
				t.Fatalf("Get err = %v, want not found", err)
			}
		}},
		{name: "list filters by company type and date range", run: func(t *testing.T, db *gorm.DB, svc *FilingService) {
			if err := db.Create(&[]model.Filing{
				{FilingID: "a", Ticker: "AAPL", CompanyName: "Apple Inc.", FilingType: "8-K", FilingDate: filingDate, PulledAt: time.Now()},
				{FilingID: "m", Ticker: "MSFT", CompanyName: "Microsoft Corp.", FilingType: "10-Q", FilingDate: filingDate.AddDate(0, 0, 2), PulledAt: time.Now()},
			}).Error; err != nil {
				t.Fatalf("seed filings: %v", err)
			}
			from := filingDate.AddDate(0, 0, -1)
			to := filingDate.AddDate(0, 0, 1)
			got, err := svc.List(context.Background(), FilingFilter{CompanyName: "Apple", FilingType: "8-K", DateFrom: &from, DateTo: &to, Page: -1, PageSize: 500})
			if err != nil {
				t.Fatalf("List: %v", err)
			}
			if got.Total != 1 || got.Page != 1 || got.PageSize != 200 {
				t.Fatalf("page = %+v", got)
			}
		}},
		{name: "list sorts by sync time ascending", run: func(t *testing.T, db *gorm.DB, svc *FilingService) {
			if err := db.Create(&[]model.Filing{
				{FilingID: "late", Ticker: "AAPL", CompanyName: "Apple Inc.", FilingType: "8-K", FilingDate: filingDate, PulledAt: filingDate.Add(2 * time.Hour)},
				{FilingID: "early", Ticker: "AAPL", CompanyName: "Apple Inc.", FilingType: "10-Q", FilingDate: filingDate, PulledAt: filingDate.Add(time.Hour)},
			}).Error; err != nil {
				t.Fatalf("seed filings: %v", err)
			}
			got, err := svc.List(context.Background(), FilingFilter{SortBy: "pulled_at", SortOrder: "asc", Page: 1, PageSize: 10})
			if err != nil {
				t.Fatalf("List: %v", err)
			}
			if len(got.Items) != 2 || got.Items[0].FilingID != "early" {
				t.Fatalf("sorted items = %+v", got.Items)
			}
		}},
		{name: "list includes latest notification status", run: func(t *testing.T, db *gorm.DB, svc *FilingService) {
			if err := db.Create(&[]model.Filing{
				{FilingID: "notified", Ticker: "AAPL", CompanyName: "Apple Inc.", FilingType: "8-K", FilingDate: filingDate, PulledAt: filingDate.Add(2 * time.Hour)},
				{FilingID: "silent", Ticker: "AAPL", CompanyName: "Apple Inc.", FilingType: "10-Q", FilingDate: filingDate, PulledAt: filingDate.Add(time.Hour)},
			}).Error; err != nil {
				t.Fatalf("seed filings: %v", err)
			}
			if err := db.Create(&model.NotificationLog{FilingID: "notified", Channel: "telegram", Status: "success", RetryCount: 0}).Error; err != nil {
				t.Fatalf("seed notification: %v", err)
			}
			got, err := svc.List(context.Background(), FilingFilter{SortBy: "pulled_at", SortOrder: "desc", Page: 1, PageSize: 10})
			if err != nil {
				t.Fatalf("List: %v", err)
			}
			if got.Items[0].FilingID != "notified" || got.Items[0].NotificationStatus != "success" || got.Items[0].NotificationLogID == 0 {
				t.Fatalf("notified item = %+v", got.Items[0])
			}
			if got.Items[1].FilingID != "silent" || got.Items[1].NotificationStatus != "" || got.Items[1].NotificationLogID != 0 {
				t.Fatalf("silent item = %+v", got.Items[1])
			}
		}},
		{name: "list filters by latest notification status", run: func(t *testing.T, db *gorm.DB, svc *FilingService) {
			if err := db.Create(&[]model.Filing{
				{FilingID: "ok", Ticker: "AAPL", CompanyName: "Apple Inc.", FilingType: "8-K", FilingDate: filingDate, PulledAt: filingDate.Add(3 * time.Hour)},
				{FilingID: "failed", Ticker: "MSFT", CompanyName: "Microsoft Corp.", FilingType: "10-Q", FilingDate: filingDate, PulledAt: filingDate.Add(2 * time.Hour)},
				{FilingID: "none", Ticker: "TSLA", CompanyName: "Tesla Inc.", FilingType: "10-K", FilingDate: filingDate, PulledAt: filingDate.Add(time.Hour)},
			}).Error; err != nil {
				t.Fatalf("seed filings: %v", err)
			}
			if err := db.Create(&[]model.NotificationLog{
				{FilingID: "ok", Channel: "telegram", Status: "success", RetryCount: 0, CreatedAt: filingDate.Add(time.Hour)},
				{FilingID: "failed", Channel: "telegram", Status: "success", RetryCount: 0, CreatedAt: filingDate.Add(time.Hour)},
				{FilingID: "failed", Channel: "telegram", Status: "failed", RetryCount: 3, CreatedAt: filingDate.Add(2 * time.Hour)},
			}).Error; err != nil {
				t.Fatalf("seed notifications: %v", err)
			}
			tests := []struct {
				name   string
				status string
				wantID string
			}{
				{name: "success", status: "success", wantID: "ok"},
				{name: "failed", status: "failed", wantID: "failed"},
				{name: "unnotified", status: "unnotified", wantID: "none"},
			}
			for _, tt := range tests {
				t.Run(tt.name, func(t *testing.T) {
					got, err := svc.List(context.Background(), FilingFilter{NotificationStatus: tt.status, Page: 1, PageSize: 10})
					if err != nil {
						t.Fatalf("List: %v", err)
					}
					if got.Total != 1 || len(got.Items) != 1 || got.Items[0].FilingID != tt.wantID {
						t.Fatalf("status %q got total=%d items=%+v, want %s", tt.status, got.Total, got.Items, tt.wantID)
					}
				})
			}
		}},
		{name: "cleanup preview and execute uses retention days", run: func(t *testing.T, db *gorm.DB, svc *FilingService) {
			now := time.Now().UTC()
			if err := db.Create(&[]model.Filing{
				{FilingID: "old", Ticker: "AAPL", CompanyName: "Apple Inc.", FilingType: "8-K", FilingDate: now.AddDate(0, 0, -40), PulledAt: now.AddDate(0, 0, -40)},
				{FilingID: "new", Ticker: "AAPL", CompanyName: "Apple Inc.", FilingType: "8-K", FilingDate: now, PulledAt: now},
			}).Error; err != nil {
				t.Fatalf("seed filings: %v", err)
			}
			preview, err := svc.CleanupPreview(context.Background(), 30, now)
			if err != nil {
				t.Fatalf("CleanupPreview: %v", err)
			}
			if preview.DeleteCount != 1 || preview.RetentionDays != 30 {
				t.Fatalf("preview = %+v", preview)
			}
			deleted, err := svc.Cleanup(context.Background(), 30, now)
			if err != nil {
				t.Fatalf("Cleanup: %v", err)
			}
			if deleted != 1 {
				t.Fatalf("deleted = %d, want 1", deleted)
			}
		}},
		{name: "create filing validates filing id", run: func(t *testing.T, db *gorm.DB, svc *FilingService) {
			if _, err := svc.createFilingIfNew(context.Background(), model.Filing{}); !errors.Is(err, ErrValidation) {
				t.Fatalf("createFilingIfNew err = %v, want validation", err)
			}
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := testDB(t)
			configs := NewConfigService(db, NewAuditService(db))
			svc := NewFilingService(db, &fakeSECClient{}, &fakeNotifier{}, configs)
			tt.run(t, db, svc)
		})
	}
}

func TestSendWithRetryTableDriven(t *testing.T) {
	tests := []struct {
		name      string
		errs      []error
		wantErr   bool
		wantCalls int
	}{
		{name: "succeeds first try", wantCalls: 1},
		{name: "retries then succeeds", errs: []error{errors.New("temporary")}, wantCalls: 2},
		{name: "returns final error", errs: []error{errors.New("one"), errors.New("two"), errors.New("three")}, wantErr: true, wantCalls: 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			notifier := &fakeNotifier{errs: tt.errs}
			err := sendWithRetry(context.Background(), notifier, telegram.Message{Text: "hello"}, 3)
			if tt.wantErr && err == nil {
				t.Fatalf("sendWithRetry expected error")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("sendWithRetry: %v", err)
			}
			if notifier.calls != tt.wantCalls {
				t.Fatalf("calls = %d, want %d", notifier.calls, tt.wantCalls)
			}
		})
	}
}
