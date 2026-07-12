package handler

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"sec_monitor/internal/config"
	"sec_monitor/internal/discovery"
	"sec_monitor/internal/model"
	"sec_monitor/internal/sec"
	"sec_monitor/internal/service"
	"sec_monitor/internal/telegram"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type fakeSECClient struct{}

type fakeDiscoveryRunner struct{}

func (fakeDiscoveryRunner) SyncSecurityUniverse(context.Context) (discovery.UniverseBatch, error) {
	return discovery.UniverseBatch{BatchID: "ops-security", Kind: discovery.BatchKindSecurity, Status: discovery.BatchStatusPublished}, nil
}

func (fakeDiscoveryRunner) SyncMarketPrices(context.Context) (discovery.UniverseBatch, error) {
	return discovery.UniverseBatch{BatchID: "ops-current", Kind: discovery.BatchKindPrescreen, Status: discovery.BatchStatusPublished}, nil
}

func TestSplitQueryValuesDeduplicatesCSVAndRepeatedParams(t *testing.T) {
	tests := []struct {
		name   string
		values []string
		csv    string
		want   []string
	}{
		{name: "csv only", csv: "low_liquidity, active_capital_risk", want: []string{"low_liquidity", "active_capital_risk"}},
		{name: "repeated and csv", values: []string{"low_liquidity", "low_liquidity,financials_missing"}, csv: "financials_missing", want: []string{"low_liquidity", "financials_missing"}},
		{name: "empty values ignored", values: []string{"", " , "}, csv: "", want: []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := splitQueryValues(tt.values, tt.csv)
			if len(got) != len(tt.want) {
				t.Fatalf("got=%#v want=%#v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("got=%#v want=%#v", got, tt.want)
				}
			}
		})
	}
}

func (f fakeSECClient) LookupCIK(ctx context.Context, ticker string) (string, string, error) {
	return "0000320193", "Apple Inc.", nil
}

func (f fakeSECClient) ListFilings(ctx context.Context, query sec.FilingQuery) ([]sec.FilingResult, error) {
	filingDate := time.Now().UTC().AddDate(0, 0, -1)
	return []sec.FilingResult{{
		FilingID:        "0000320193-26-000001",
		AccessionNumber: "0000320193-26-000001",
		Ticker:          "AAPL",
		CIK:             "0000320193",
		CompanyName:     "Apple Inc.",
		FilingType:      "8-K",
		FilingDate:      filingDate,
		FilingURL:       "https://sec.gov/aapl/8k",
		Title:           "Current report",
	}}, nil
}

func (f fakeSECClient) ListCurrentFilings(ctx context.Context, query sec.CurrentFilingQuery) ([]sec.CurrentFilingResult, error) {
	return []sec.CurrentFilingResult{{
		FilingID:        "0000000001-26-000001",
		AccessionNumber: "0000000001-26-000001",
		CIK:             "0000000001",
		CompanyName:     "Acme Space Inc.",
		FilingType:      "S-1",
		FilingDate:      time.Now().UTC(),
		FilingURL:       "https://sec.gov/acme/s1",
		Title:           "S-1 - Acme Space Inc.",
	}}, nil
}

type fakeNotifier struct{}

func (f fakeNotifier) Send(ctx context.Context, message telegram.Message) error {
	return nil
}

type captureNotifier struct {
	calls    int
	messages []telegram.Message
}

func (f *captureNotifier) Send(ctx context.Context, message telegram.Message) error {
	f.calls++
	f.messages = append(f.messages, message)
	return nil
}

type reliabilitySECClient struct {
	currentFilings  []sec.CurrentFilingResult
	listedCompanies []sec.ListedCompany
}

func (f reliabilitySECClient) LookupCIK(ctx context.Context, ticker string) (string, string, error) {
	return "", "", nil
}

func (f reliabilitySECClient) ListFilings(ctx context.Context, query sec.FilingQuery) ([]sec.FilingResult, error) {
	return nil, nil
}

func (f reliabilitySECClient) ListCurrentFilings(ctx context.Context, query sec.CurrentFilingQuery) ([]sec.CurrentFilingResult, error) {
	return f.currentFilings, nil
}

func (f reliabilitySECClient) ListListedCompanies(ctx context.Context) ([]sec.ListedCompany, error) {
	return f.listedCompanies, nil
}

func (f reliabilitySECClient) FetchFilingDocument(ctx context.Context, filingURL string) (string, error) {
	return "We are offering 1,000,000 shares of our common stock. The initial public offering price per share is $12.00.", nil
}

type failThenSucceedNotifier struct {
	failedAttempts int
}

func (f *failThenSucceedNotifier) Send(ctx context.Context, message telegram.Message) error {
	if f.failedAttempts > 0 {
		f.failedAttempts--
		return errors.New("temporary delivery failure")
	}
	return nil
}

type fakeScheduler struct {
	reloadCalls int
	runCalls    int
	runTasks    []string
	reloadErr   error
	runErr      error
}

func (f *fakeScheduler) Reload(ctx context.Context) error {
	f.reloadCalls++
	return f.reloadErr
}

func (f *fakeScheduler) RunOnce(ctx context.Context) error {
	f.runCalls++
	return f.runErr
}

func (f *fakeScheduler) RunTask(ctx context.Context, taskName string) error {
	f.runCalls++
	f.runTasks = append(f.runTasks, taskName)
	return f.runErr
}

func TestAppHandlerListsDiscoveryCandidates(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open main db: %v", err)
	}
	discoveryDB, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open discovery db: %v", err)
	}
	if err := discovery.Migrate(discoveryDB); err != nil {
		t.Fatalf("migrate discovery: %v", err)
	}
	security := discovery.Security{CIK: "0000001234", CompanyName: "Acme", CatalogStatus: discovery.SecurityCatalogPublished}
	if err := discoveryDB.Create(&security).Error; err != nil {
		t.Fatal(err)
	}
	batch := discovery.UniverseBatch{BatchID: "current", Kind: discovery.BatchKindPrescreen, Status: discovery.BatchStatusPublished, StartedAt: time.Now()}
	if err := discoveryDB.Create(&batch).Error; err != nil {
		t.Fatal(err)
	}
	if err := discoveryDB.Create(&discovery.CurrentBatchPointer{Kind: discovery.BatchKindPrescreen, BatchID: batch.BatchID}).Error; err != nil {
		t.Fatal(err)
	}
	if err := discoveryDB.Create(&discovery.CandidateScoreSnapshot{BatchID: batch.BatchID, SecurityID: security.ID, Ticker: "ACME", Grade: discovery.CandidateGradeA, EligibleA: true, EligibleB: true, TotalScore: 80, MarketCapUSD: 240_000_000}).Error; err != nil {
		t.Fatal(err)
	}
	h := &AppHandler{DB: db, DiscoveryDB: discoveryDB}
	r := gin.New()
	r.GET("/discovery/candidates", h.ListDiscoveryCandidates)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/discovery/candidates?grade=A", nil)
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"ticker":"ACME"`) || !strings.Contains(rec.Body.String(), `"total_score":80`) {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestAppHandlerGetsIPOHealth(t *testing.T) {
	r, db, _ := testApp(t)
	now := time.Now().UTC().Truncate(time.Second)
	if err := db.Create(&[]model.IPOFiling{
		{FilingID: "pending-s1", CIK: "0000000001", CompanyName: "Pending Listing", FilingType: "S-1", FilingDate: now.Add(-time.Hour)},
		{FilingID: "pending-424b4", CIK: "0000000001", CompanyName: "Pending Listing", FilingType: "424B4", FilingDate: now},
	}).Error; err != nil {
		t.Fatalf("seed IPO filing: %v", err)
	}
	if err := db.Create(&model.IPOCompanyMarketData{CIK: "0000000001", Ticker: "PEND", LifecycleCheckedAt: &now}).Error; err != nil {
		t.Fatalf("seed IPO market data: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ipo-health", nil)
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"pending_listing":1`) || !strings.Contains(rec.Body.String(), `"latest_sync":null`) {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestIPOReliabilityWorkflow(t *testing.T) {
	t.Setenv("CONFIG_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32)))
	const testBotToken = "test-only-bot-token"
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(
		&model.WatchTarget{}, &model.SystemConfig{}, &model.OperationLog{}, &model.NotificationBatch{}, &model.NotificationBatchItem{},
		&model.IPOFiling{}, &model.IPOCompanyOverride{}, &model.IPOCompanyMarketData{}, &model.IPOOfferingEvent{}, &model.SyncRun{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	audit := service.NewAuditService(db)
	configs := service.NewConfigService(db, audit, config.Load().System)
	if err := configs.EnsureDefaults(context.Background()); err != nil {
		t.Fatalf("default configs: %v", err)
	}
	if err := configs.UpsertMany(context.Background(), []service.ConfigInput{
		{Key: "telegram.enabled", Value: "true", ValueType: "bool", Category: "telegram"},
		{Key: "telegram.bot_token", Value: testBotToken, ValueType: "string", Category: "telegram", Encrypted: true},
		{Key: "telegram.chat_id", Value: "test-chat", ValueType: "string", Category: "telegram"},
	}, "test"); err != nil {
		t.Fatalf("configure telegram: %v", err)
	}
	var stored model.SystemConfig
	if err := db.Where("config_key = ?", "telegram.bot_token").First(&stored).Error; err != nil {
		t.Fatalf("load encrypted config: %v", err)
	}
	if !strings.HasPrefix(stored.ConfigValue, "enc:v1:") || stored.ConfigValue == testBotToken || strings.Contains(stored.ConfigValue, testBotToken) {
		t.Fatalf("telegram token was not encrypted at rest: %q", stored.ConfigValue)
	}

	now := time.Now().UTC().Truncate(time.Second)
	if err := db.Create(&model.IPOFiling{FilingID: "baseline-s1", CIK: "0000000001", CompanyName: "Acme IPO, Inc.", FilingType: "S-1", FilingDate: now.AddDate(0, 0, -1), FilingURL: "https://sec.test/baseline-s1"}).Error; err != nil {
		t.Fatalf("seed baseline: %v", err)
	}
	secClient := reliabilitySECClient{
		currentFilings:  []sec.CurrentFilingResult{{FilingID: "acme-424b4", CIK: "0000000001", CompanyName: "Acme IPO, Inc.", FilingType: "424B4", FilingDate: now, FilingURL: "https://sec.test/acme-424b4"}},
		listedCompanies: []sec.ListedCompany{{CIK: "0000000001", Name: "Acme IPO, Inc.", Ticker: "ACME"}},
	}
	notifier := &failThenSucceedNotifier{failedAttempts: 3}
	ipo := service.NewIPORadarService(db, secClient, notifier, configs)
	if _, err := ipo.Refresh(context.Background()); err != nil {
		t.Fatalf("refresh ipo radar: %v", err)
	}
	var persistedFiling model.IPOFiling
	if err := db.Where("filing_id = ?", "acme-424b4").First(&persistedFiling).Error; err != nil {
		t.Fatalf("load refreshed 424B4 filing: %v", err)
	}
	if persistedFiling.FilingID != "acme-424b4" || persistedFiling.FilingType != "424B4" {
		t.Fatalf("persisted 424B4 filing = %+v", persistedFiling)
	}
	var offering model.IPOOfferingEvent
	if err := db.Where("filing_id = ?", "acme-424b4").First(&offering).Error; err != nil {
		t.Fatalf("load refreshed 424B4 offering event: %v", err)
	}
	if offering.FilingID != "acme-424b4" || offering.ParseStatus != "parsed" || offering.OfferingType != "initial" {
		t.Fatalf("persisted 424B4 offering event = %+v", offering)
	}
	companies, err := ipo.ListCompanies(context.Background(), service.IPOCompanyFilter{Attention: "listing_pending", Page: 1, PageSize: 10}, now)
	if err != nil || companies.Total != 1 || len(companies.Items) != 1 || companies.Items[0].CIK != "0000000001" {
		t.Fatalf("listing-pending companies=%+v err=%v", companies, err)
	}
	var batch model.NotificationBatch
	if err := db.Where("source = ?", "ipo").First(&batch).Error; err != nil {
		t.Fatalf("load failed batch: %v", err)
	}
	if batch.Status != "failed" || batch.NextRetryAt == nil {
		t.Fatalf("expected failed batch scheduled for retry")
	}
	retries := service.NewNotificationBatchService(db, notifier, configs)
	result, err := retries.RetryDue(context.Background(), *batch.NextRetryAt)
	if err != nil || result.Sent != 1 || result.Attempted != 1 {
		t.Fatalf("retry result sent=%d attempted=%d err=%v", result.Sent, result.Attempted, err)
	}
	batchID := batch.ID
	batch = model.NotificationBatch{}
	if err := db.First(&batch, batchID).Error; err != nil {
		t.Fatalf("reload retried batch: %v", err)
	}
	if batch.Status != "sent" || batch.NextRetryAt != nil {
		t.Fatalf("retried batch = %+v", batch)
	}

	h := &AppHandler{IPO: ipo}
	r := gin.New()
	r.GET("/ipo-health", h.GetIPORadarHealth)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ipo-health", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"failed_notification_batches":0`) || !strings.Contains(rec.Body.String(), `"due_retry_batches":0`) || !strings.Contains(rec.Body.String(), `"dead_letter_batches":0`) {
		t.Fatalf("ipo health status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestAppHandlerGetsDiscoveryCandidateOverview(t *testing.T) {
	gin.SetMode(gin.TestMode)
	discoveryDB, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open discovery db: %v", err)
	}
	if err := discovery.Migrate(discoveryDB); err != nil {
		t.Fatalf("migrate discovery: %v", err)
	}
	security := discovery.Security{CIK: "0000001235", CompanyName: "Overview", CatalogStatus: discovery.SecurityCatalogPublished}
	if err := discoveryDB.Create(&security).Error; err != nil {
		t.Fatal(err)
	}
	securityBatch := discovery.UniverseBatch{BatchID: "overview-security", Kind: discovery.BatchKindSecurity, Status: discovery.BatchStatusPublished, StartedAt: time.Now().Add(-time.Minute)}
	batch := discovery.UniverseBatch{BatchID: "overview-current", Kind: discovery.BatchKindPrescreen, Status: discovery.BatchStatusPublished, UniverseSourceVersion: securityBatch.BatchID, StartedAt: time.Now()}
	if err := discoveryDB.Create(&[]discovery.UniverseBatch{securityBatch, batch}).Error; err != nil {
		t.Fatal(err)
	}
	if err := discoveryDB.Create(&discovery.CurrentBatchPointer{Kind: discovery.BatchKindPrescreen, BatchID: batch.BatchID}).Error; err != nil {
		t.Fatal(err)
	}
	if err := discoveryDB.Create(&discovery.CandidateScoreSnapshot{BatchID: batch.BatchID, SecurityID: security.ID, Ticker: "OVRV", Grade: discovery.CandidateGradeB, EligibleB: true, TotalScore: 72, MarketCapUSD: 200_000_000, RevenueGrowthPct: 55, CashRunwayMonths: 18}).Error; err != nil {
		t.Fatal(err)
	}
	if err := discoveryDB.Create(&discovery.SecurityBatchIdentity{BatchID: securityBatch.BatchID, SecurityID: security.ID, Ticker: "OVRV", SIC: 7372, CompanyName: "Overview"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := discoveryDB.Create(&discovery.FinancialMetricSnapshot{BatchID: securityBatch.BatchID, SecurityID: security.ID, RevenueGrowthAvailable: true, RunwayAvailable: true, QuarterlyRevenueYoYPct: 55, AnnualRevenueYoYPct: 40, LatestQuarterRevenueUSD: 10_000_000}).Error; err != nil {
		t.Fatal(err)
	}
	h := &AppHandler{DiscoveryDB: discoveryDB}
	r := gin.New()
	r.GET("/discovery/candidates/overview", h.GetDiscoveryCandidateOverview)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/discovery/candidates/overview", nil)
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"total":1`) || !strings.Contains(rec.Body.String(), `"strong_b":1`) {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestAppHandlerManagesCandidateWatches(t *testing.T) {
	gin.SetMode(gin.TestMode)
	discoveryDB, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open discovery db: %v", err)
	}
	if err := discovery.Migrate(discoveryDB); err != nil {
		t.Fatalf("migrate discovery: %v", err)
	}
	security := discovery.Security{CIK: "0000007777", CompanyName: "Watch API", CatalogStatus: discovery.SecurityCatalogPublished}
	if err := discoveryDB.Create(&security).Error; err != nil {
		t.Fatal(err)
	}
	batch := discovery.UniverseBatch{BatchID: "watch-api", Kind: discovery.BatchKindPrescreen, Status: discovery.BatchStatusPublished, StartedAt: time.Now()}
	if err := discoveryDB.Create(&batch).Error; err != nil {
		t.Fatal(err)
	}
	if err := discoveryDB.Create(&discovery.CurrentBatchPointer{Kind: discovery.BatchKindPrescreen, BatchID: batch.BatchID}).Error; err != nil {
		t.Fatal(err)
	}
	if err := discoveryDB.Create(&discovery.CandidateScoreSnapshot{BatchID: batch.BatchID, SecurityID: security.ID, Ticker: "WAPI", Grade: discovery.CandidateGradeB}).Error; err != nil {
		t.Fatal(err)
	}
	h := &AppHandler{DiscoveryDB: discoveryDB}
	r := gin.New()
	r.GET("/discovery/candidate-watches", h.ListCandidateWatches)
	r.POST("/discovery/candidate-watches", h.UpsertCandidateWatch)
	r.DELETE("/discovery/candidate-watches/:id", h.DeleteCandidateWatch)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/discovery/candidate-watches", strings.NewReader(`{"ticker":"wapi","note":"track"}`))
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"ticker":"WAPI"`) || !strings.Contains(rec.Body.String(), `"company_name":"Watch API"`) {
		t.Fatalf("create status=%d body=%s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/discovery/candidate-watches", nil)
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"total":1`) {
		t.Fatalf("list status=%d body=%s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/discovery/candidate-watches", strings.NewReader(`{"ticker":"wapi","note":"later","status":"archived"}`))
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"status":"archived"`) {
		t.Fatalf("archive status=%d body=%s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/discovery/candidate-watches", nil)
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"total":0`) {
		t.Fatalf("active list status=%d body=%s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/discovery/candidate-watches?status=archived", nil)
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"total":1`) {
		t.Fatalf("archived list status=%d body=%s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodDelete, "/discovery/candidate-watches/1", nil)
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestAppHandlerGetsDiscoveryCandidateDetail(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open main db: %v", err)
	}
	if err := db.AutoMigrate(&model.Filing{}); err != nil {
		t.Fatalf("migrate main: %v", err)
	}
	discoveryDB, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open discovery db: %v", err)
	}
	if err := discovery.Migrate(discoveryDB); err != nil {
		t.Fatalf("migrate discovery: %v", err)
	}
	security := discovery.Security{CIK: "0000002468", CompanyName: "Detail API Co", CatalogStatus: discovery.SecurityCatalogPublished}
	if err := discoveryDB.Create(&security).Error; err != nil {
		t.Fatal(err)
	}
	batch := discovery.UniverseBatch{BatchID: "current", Kind: discovery.BatchKindPrescreen, Status: discovery.BatchStatusPublished, StartedAt: time.Now()}
	if err := discoveryDB.Create(&batch).Error; err != nil {
		t.Fatal(err)
	}
	if err := discoveryDB.Create(&discovery.CurrentBatchPointer{Kind: discovery.BatchKindPrescreen, BatchID: batch.BatchID}).Error; err != nil {
		t.Fatal(err)
	}
	if err := discoveryDB.Create(&discovery.CandidateScoreSnapshot{BatchID: batch.BatchID, SecurityID: security.ID, Ticker: "DAPI", Grade: discovery.CandidateGradeA, EligibleA: true, TotalScore: 87}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.Filing{FilingID: "filing-1", AccessionNumber: "0000002468-26-000001", Ticker: "DAPI", CIK: security.CIK, CompanyName: "Detail API Co", FilingType: "8-K", FilingDate: time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC), FilingURL: "https://sec.test/filing", Title: "DAPI 8-K"}).Error; err != nil {
		t.Fatal(err)
	}
	h := &AppHandler{DB: db, DiscoveryDB: discoveryDB, DiscoverySync: service.NewDiscoverySyncService(discoveryDB, config.DiscoveryConfig{}).WithRunner(fakeDiscoveryRunner{})}
	r := gin.New()
	r.GET("/discovery/candidates/:ticker/detail", h.GetDiscoveryCandidateDetail)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/discovery/candidates/DAPI/detail", nil)
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"ticker":"DAPI"`) || !strings.Contains(rec.Body.String(), `"company_name":"Detail API Co"`) || !strings.Contains(rec.Body.String(), `"recent_filings"`) || !strings.Contains(rec.Body.String(), `"filing_type":"8-K"`) {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestAppHandlerPreviewsDiscoveryCandidateSummary(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open main db: %v", err)
	}
	discoveryDB, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open discovery db: %v", err)
	}
	if err := discovery.Migrate(discoveryDB); err != nil {
		t.Fatalf("migrate discovery: %v", err)
	}
	security := discovery.Security{CIK: "0000005678", CompanyName: "Preview Co", CatalogStatus: discovery.SecurityCatalogPublished}
	if err := discoveryDB.Create(&security).Error; err != nil {
		t.Fatal(err)
	}
	batch := discovery.UniverseBatch{BatchID: "current", Kind: discovery.BatchKindPrescreen, Status: discovery.BatchStatusPublished, StartedAt: time.Now()}
	if err := discoveryDB.Create(&batch).Error; err != nil {
		t.Fatal(err)
	}
	if err := discoveryDB.Create(&discovery.CurrentBatchPointer{Kind: discovery.BatchKindPrescreen, BatchID: batch.BatchID}).Error; err != nil {
		t.Fatal(err)
	}
	if err := discoveryDB.Create(&discovery.CandidateScoreSnapshot{BatchID: batch.BatchID, SecurityID: security.ID, Ticker: "PRVW", Grade: discovery.CandidateGradeA, EligibleA: true, TotalScore: 91, MarketCapUSD: 210_000_000, RevenueGrowthPct: 62, CashRunwayMonths: 16, RecentQualifiedInsider: true}).Error; err != nil {
		t.Fatal(err)
	}
	h := &AppHandler{DB: db, DiscoveryDB: discoveryDB}
	r := gin.New()
	r.GET("/discovery/candidates/summary", h.PreviewDiscoveryCandidateSummary)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/discovery/candidates/summary?limit=1", nil)
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"batch_id":"current"`) || !strings.Contains(rec.Body.String(), `"ticker":"PRVW"`) || !strings.Contains(rec.Body.String(), "仅研究与通知") {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestAppHandlerDiscoveryCandidateOperations(t *testing.T) {
	gin.SetMode(gin.TestMode)
	discoveryDB, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open discovery db: %v", err)
	}
	if err := discovery.Migrate(discoveryDB); err != nil {
		t.Fatalf("migrate discovery: %v", err)
	}
	security := discovery.Security{CIK: "0000005688", CompanyName: "Ops Co", CatalogStatus: discovery.SecurityCatalogPublished}
	if err := discoveryDB.Create(&security).Error; err != nil {
		t.Fatal(err)
	}
	batch := discovery.UniverseBatch{BatchID: "ops-current", Kind: discovery.BatchKindPrescreen, Status: discovery.BatchStatusPublished, EffectiveDate: "2026-06-30", StartedAt: time.Now()}
	if err := discoveryDB.Create(&batch).Error; err != nil {
		t.Fatal(err)
	}
	if err := discoveryDB.Create(&discovery.CurrentBatchPointer{Kind: discovery.BatchKindPrescreen, BatchID: batch.BatchID}).Error; err != nil {
		t.Fatal(err)
	}
	if err := discoveryDB.Create(&discovery.CandidateScoreSnapshot{BatchID: batch.BatchID, SecurityID: security.ID, Ticker: "OPS", Grade: discovery.CandidateGradeA, EligibleA: true, TotalScore: 92, MarketCapUSD: 220_000_000}).Error; err != nil {
		t.Fatal(err)
	}
	h := &AppHandler{DiscoveryDB: discoveryDB, DiscoverySync: service.NewDiscoverySyncService(discoveryDB, config.DiscoveryConfig{}).WithRunner(fakeDiscoveryRunner{})}
	r := gin.New()
	r.GET("/discovery/candidates/health", h.GetDiscoveryCandidateHealth)
	r.POST("/discovery/candidates/refresh", h.RefreshDiscoveryCandidates)
	r.GET("/discovery/candidates/report", h.GetDiscoveryCandidateReport)
	r.GET("/discovery/candidates/effectiveness", h.GetDiscoveryCandidateEffectiveness)
	r.GET("/exports/candidates.csv", h.ExportDiscoveryCandidatesCSV)

	for _, tc := range []struct {
		method string
		path   string
		want   string
	}{
		{method: http.MethodGet, path: "/discovery/candidates/health", want: `"total_candidates":1`},
		{method: http.MethodPost, path: "/discovery/candidates/refresh", want: `"status":"published"`},
		{method: http.MethodGet, path: "/discovery/candidates/report?date=2026-06-30", want: `"ticker":"OPS"`},
		{method: http.MethodGet, path: "/discovery/candidates/effectiveness", want: `"benchmark_ticker":"IWM"`},
		{method: http.MethodGet, path: "/exports/candidates.csv?grade=A", want: "ticker,grade,total_score"},
	} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(tc.method, tc.path, nil)
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), tc.want) {
			t.Fatalf("%s %s status=%d body=%s", tc.method, tc.path, rec.Code, rec.Body.String())
		}
	}
}

func TestAppHandlerPreviewsDiscoveryCandidateNotification(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open main db: %v", err)
	}
	if err := db.AutoMigrate(&model.SystemConfig{}, &model.OperationLog{}); err != nil {
		t.Fatalf("migrate main db: %v", err)
	}
	audit := service.NewAuditService(db)
	configs := service.NewConfigService(db, audit)
	if err := configs.EnsureDefaults(context.Background()); err != nil {
		t.Fatalf("EnsureDefaults: %v", err)
	}
	if err := configs.UpsertMany(context.Background(), []service.ConfigInput{
		{Key: "candidate_notification.enabled", Value: "true", ValueType: "bool", Category: "candidate_notification"},
		{Key: "candidate_notification.notify_a", Value: "true", ValueType: "bool", Category: "candidate_notification"},
		{Key: "candidate_notification.notify_b", Value: "false", ValueType: "bool", Category: "candidate_notification"},
	}, "test"); err != nil {
		t.Fatalf("UpsertMany: %v", err)
	}
	discoveryDB, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open discovery db: %v", err)
	}
	if err := discovery.Migrate(discoveryDB); err != nil {
		t.Fatalf("migrate discovery: %v", err)
	}
	security := discovery.Security{CIK: "0000006789", CompanyName: "Notify Co", CatalogStatus: discovery.SecurityCatalogPublished}
	if err := discoveryDB.Create(&security).Error; err != nil {
		t.Fatal(err)
	}
	batch := discovery.UniverseBatch{BatchID: "current", Kind: discovery.BatchKindPrescreen, Status: discovery.BatchStatusPublished, StartedAt: time.Now()}
	if err := discoveryDB.Create(&batch).Error; err != nil {
		t.Fatal(err)
	}
	if err := discoveryDB.Create(&discovery.CurrentBatchPointer{Kind: discovery.BatchKindPrescreen, BatchID: batch.BatchID}).Error; err != nil {
		t.Fatal(err)
	}
	if err := discoveryDB.Create(&discovery.CandidateScoreSnapshot{BatchID: batch.BatchID, SecurityID: security.ID, Ticker: "NTFY", Grade: discovery.CandidateGradeA, EligibleA: true, TotalScore: 91, MarketCapUSD: 210_000_000}).Error; err != nil {
		t.Fatal(err)
	}
	h := &AppHandler{DB: db, DiscoveryDB: discoveryDB, Configs: configs}
	r := gin.New()
	r.GET("/discovery/candidates/notification-preview", h.PreviewDiscoveryCandidateNotification)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/discovery/candidates/notification-preview", nil)
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"enabled":true`) || !strings.Contains(rec.Body.String(), `"ticker":"NTFY"`) || strings.Contains(rec.Body.String(), `"items_b":[{`) {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestAppHandlerSendsDiscoveryCandidateNotification(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open main db: %v", err)
	}
	if err := db.AutoMigrate(&model.SystemConfig{}, &model.OperationLog{}, &model.NotificationBatch{}, &model.NotificationBatchItem{}); err != nil {
		t.Fatalf("migrate main db: %v", err)
	}
	audit := service.NewAuditService(db)
	configs := service.NewConfigService(db, audit)
	if err := configs.EnsureDefaults(context.Background()); err != nil {
		t.Fatalf("EnsureDefaults: %v", err)
	}
	if err := configs.UpsertMany(context.Background(), []service.ConfigInput{
		{Key: "candidate_notification.enabled", Value: "true", ValueType: "bool", Category: "candidate_notification"},
		{Key: "candidate_notification.notify_a", Value: "true", ValueType: "bool", Category: "candidate_notification"},
		{Key: "candidate_notification.notify_b", Value: "false", ValueType: "bool", Category: "candidate_notification"},
		{Key: "telegram.enabled", Value: "true", ValueType: "bool", Category: "telegram"},
		{Key: "telegram.bot_token", Value: "token", ValueType: "string", Category: "telegram"},
		{Key: "telegram.chat_id", Value: "chat", ValueType: "string", Category: "telegram"},
	}, "test"); err != nil {
		t.Fatalf("UpsertMany: %v", err)
	}
	discoveryDB, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open discovery db: %v", err)
	}
	if err := discovery.Migrate(discoveryDB); err != nil {
		t.Fatalf("migrate discovery: %v", err)
	}
	security := discovery.Security{CIK: "0000007890", CompanyName: "Send Co", CatalogStatus: discovery.SecurityCatalogPublished}
	if err := discoveryDB.Create(&security).Error; err != nil {
		t.Fatal(err)
	}
	batch := discovery.UniverseBatch{BatchID: "current", Kind: discovery.BatchKindPrescreen, Status: discovery.BatchStatusPublished, StartedAt: time.Now()}
	if err := discoveryDB.Create(&batch).Error; err != nil {
		t.Fatal(err)
	}
	if err := discoveryDB.Create(&discovery.CurrentBatchPointer{Kind: discovery.BatchKindPrescreen, BatchID: batch.BatchID}).Error; err != nil {
		t.Fatal(err)
	}
	if err := discoveryDB.Create(&discovery.CandidateScoreSnapshot{BatchID: batch.BatchID, SecurityID: security.ID, Ticker: "SEND", Grade: discovery.CandidateGradeA, EligibleA: true, TotalScore: 93, MarketCapUSD: 200_000_000}).Error; err != nil {
		t.Fatal(err)
	}
	notifier := &captureNotifier{}
	h := &AppHandler{DB: db, DiscoveryDB: discoveryDB, Configs: configs, CandidateNotification: service.NewCandidateNotificationService(db, discoveryDB, notifier, configs)}
	r := gin.New()
	r.POST("/discovery/candidates/notification-send", h.SendDiscoveryCandidateNotification)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/discovery/candidates/notification-send", strings.NewReader(`{"confirm":true}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"source":"candidate"`) || !strings.Contains(rec.Body.String(), `"status":"sent"`) {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if notifier.calls != 1 || len(notifier.messages) != 1 || !strings.Contains(notifier.messages[0].Text, "SEND") {
		t.Fatalf("notifier calls=%d messages=%+v", notifier.calls, notifier.messages)
	}
}

func TestAppHandlerRequeuesNotificationBatch(t *testing.T) {
	r, db, _ := testApp(t)
	now := time.Date(2026, 7, 11, 10, 0, 0, 0, time.UTC)
	future := now.Add(time.Hour)
	batch := model.NotificationBatch{Source: "filing", Trigger: "manual", Channel: "telegram", Status: "failed", ItemCount: 1, FailedCount: 1, RetryCount: 2, NextRetryAt: &future}
	if err := db.Create(&batch).Error; err != nil {
		t.Fatalf("seed batch: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/notification-batches/%d/retry", batch.ID), nil)
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"status":"failed"`) || !strings.Contains(rec.Body.String(), `"next_retry_at"`) {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var persisted model.NotificationBatch
	if err := db.First(&persisted, batch.ID).Error; err != nil {
		t.Fatalf("load batch: %v", err)
	}
	if persisted.NextRetryAt == nil || !persisted.NextRetryAt.Before(time.Now().UTC().Add(time.Minute)) {
		t.Fatalf("persisted batch = %+v, want immediate requeue", persisted)
	}
}

func testApp(t *testing.T) (*gin.Engine, *gorm.DB, *fakeScheduler) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(
		&model.WatchTarget{}, &model.Filing{}, &model.SyncRun{}, &model.SyncRunDetail{}, &model.TaskConfig{},
		&model.SystemConfig{}, &model.OperationLog{}, &model.NotificationLog{},
		&model.NotificationBatch{}, &model.NotificationBatchItem{},
		&model.IPOFiling{}, &model.IPOCompanyOverride{}, &model.IPOCompanyMarketData{}, &model.IPOOfferingEvent{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	audit := service.NewAuditService(db)
	configs := service.NewConfigService(db, audit)
	if err := configs.EnsureDefaults(context.Background()); err != nil {
		t.Fatalf("default configs: %v", err)
	}
	targets := service.NewWatchTargetService(db, audit)
	filings := service.NewFilingService(db, fakeSECClient{}, fakeNotifier{}, configs)
	ipoRadar := service.NewIPORadarService(db, fakeSECClient{}, fakeNotifier{}, configs)
	tasks := service.NewTaskConfigService(db, audit)
	if err := tasks.EnsureDefault(context.Background()); err != nil {
		t.Fatalf("default task: %v", err)
	}
	sched := &fakeScheduler{}
	h := &AppHandler{
		Runtime: config.Config{
			Database: config.DatabaseConfig{Type: "sqlite", DSN: ":memory:"},
			SEC:      config.SECConfig{UserAgent: "sec-monitor-test test@example.com"},
		},
		DB:                db,
		DiscoveryDB:       nil,
		Targets:           targets,
		Configs:           configs,
		Tasks:             tasks,
		Filings:           filings,
		IPO:               ipoRadar,
		SEC:               fakeSECClient{},
		Audit:             audit,
		Notification:      service.NewNotificationService(db),
		NotificationBatch: service.NewNotificationBatchService(db, fakeNotifier{}, configs),
		Scheduler:         sched,
	}
	r := gin.New()
	r.GET("/healthz", Health)
	r.GET("/sec/tickers/:ticker", h.LookupTicker)
	r.GET("/discovery/candidates", h.ListDiscoveryCandidates)
	r.GET("/targets", h.ListWatchTargets)
	r.POST("/targets", h.CreateWatchTarget)
	r.GET("/targets/:id", h.GetWatchTarget)
	r.PUT("/targets/:id", h.UpdateWatchTarget)
	r.DELETE("/targets/:id", h.DeleteWatchTarget)
	r.PATCH("/targets/:id/status", h.SetWatchTargetStatus)
	r.POST("/targets/:id/sync", h.SyncWatchTarget)
	r.GET("/targets/:id/sync-details", h.ListWatchTargetSyncDetails)
	r.GET("/filings", h.ListFilings)
	r.POST("/filings/refresh", h.RefreshFilings)
	r.GET("/ipo-health", h.GetIPORadarHealth)
	r.GET("/ipo-companies", h.ListIPOCompanies)
	r.GET("/ipo-companies/:cik/offerings", h.ListIPOOfferingEvents)
	r.PUT("/ipo-companies/:cik/override", h.UpdateIPOCompanyOverride)
	r.GET("/ipo-filings", h.ListIPORadarFilings)
	r.POST("/ipo-filings/refresh", h.RefreshIPORadar)
	r.GET("/filings/cleanup-preview", h.PreviewFilingCleanup)
	r.POST("/filings/cleanup", h.CleanupFilings)
	r.GET("/filings/:id", h.GetFiling)
	r.GET("/sync-runs", h.ListSyncRuns)
	r.GET("/sync-runs/:id/details", h.ListSyncRunDetails)
	r.GET("/configs", h.ListSystemConfigs)
	r.PUT("/configs", h.UpdateSystemConfigs)
	r.POST("/configs/reload", h.ListSystemConfigs)
	r.GET("/telegram/config", h.GetTelegramConfig)
	r.PUT("/telegram/config", h.UpdateTelegramConfig)
	r.POST("/telegram/test", h.TestTelegram)
	r.GET("/operation-logs", h.ListOperationLogs)
	r.GET("/notification-logs", h.ListNotificationLogs)
	r.GET("/notification-batches", h.ListNotificationBatches)
	r.GET("/notification-batches/:id/items", h.ListNotificationBatchItems)
	r.POST("/notification-batches/:id/retry", h.RequeueNotificationBatch)
	r.GET("/tasks", h.ListTaskConfigs)
	r.PUT("/tasks/:id", h.UpdateTaskConfig)
	r.POST("/tasks/:id/run", h.RunTask)
	r.GET("/list-health", h.ListHealth)
	r.GET("/exports/filings.csv", h.ExportFilingsCSV)
	r.GET("/exports/ipo-companies.csv", h.ExportIPOCompaniesCSV)
	r.GET("/exports/ipo-filings.csv", h.ExportIPORadarFilingsCSV)
	r.GET("/exports/watch-targets.csv", h.ExportTargetsCSV)
	r.GET("/exports/configs.json", h.ExportConfigsJSON)
	r.GET("/exports/backup.json", h.ExportBackupJSON)
	r.GET("/not-implemented", NotImplemented("example"))
	return r, db, sched
}

func TestAppHandlerMasksHistoricalEncryptedOperationLogValues(t *testing.T) {
	r, db, _ := testApp(t)
	const token = "123456:historical-secret"
	if err := db.Create(&model.OperationLog{
		Action: "update", ObjectType: "system_config", ObjectID: "batch",
		AfterData: `[{"key":"telegram.bot_token","value":"` + token + `","encrypted":true}]`,
	}).Error; err != nil {
		t.Fatalf("seed operation log: %v", err)
	}

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/operation-logs", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), token) || !strings.Contains(rec.Body.String(), "******") {
		t.Fatalf("operation log response leaked secret: %s", rec.Body.String())
	}
}

func TestAppHandlerListsDiscoveryRunDiagnostics(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open main db: %v", err)
	}
	discoveryDB, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open discovery db: %v", err)
	}
	if err := discovery.Migrate(discoveryDB); err != nil {
		t.Fatalf("migrate discovery: %v", err)
	}
	started := time.Date(2026, 7, 1, 5, 30, 0, 0, time.UTC)
	completed := started.Add(10 * time.Minute)
	securityBatch := discovery.UniverseBatch{BatchID: "sec-batch", Kind: discovery.BatchKindSecurity, Status: discovery.BatchStatusPublished, EffectiveDate: "2026-06-30", SourceVersionsJSON: "[]", ContentSHA256: strings.Repeat("a", 64), RecordCount: 6012, StartedAt: started, CompletedAt: &completed}
	marketBatch := discovery.UniverseBatch{BatchID: "market-batch", Kind: discovery.BatchKindPrescreen, Status: discovery.BatchStatusFailed, EffectiveDate: "2026-06-30", SourceVersionsJSON: "[]", ContentSHA256: strings.Repeat("b", 64), ErrorMessage: "tiingo rate limited", StartedAt: started.Add(time.Minute), CompletedAt: &completed}
	if err := discoveryDB.Create(&[]discovery.UniverseBatch{securityBatch, marketBatch}).Error; err != nil {
		t.Fatal(err)
	}
	if err := discoveryDB.Create(&discovery.ProviderRun{BatchID: marketBatch.BatchID, Provider: "tiingo", Status: discovery.ProviderStatusValidation, SourceVersion: "tiingo:test", SHA256: strings.Repeat("c", 64), EffectiveDate: started, RecordCount: 100, ExpectedCount: 200, CoveragePct: 50, CreatedAt: completed}).Error; err != nil {
		t.Fatal(err)
	}
	if err := discoveryDB.Create(&discovery.ProviderHealth{Provider: "tiingo", Status: discovery.ProviderStatusValidation, LastTradeDate: "2026-06-30", QualifiedTradingDays: 1, UpdatedAt: completed}).Error; err != nil {
		t.Fatal(err)
	}
	h := &AppHandler{DB: db, DiscoveryDB: discoveryDB}
	r := gin.New()
	r.GET("/discovery/batches", h.ListDiscoveryBatches)
	r.GET("/discovery/provider-runs", h.ListDiscoveryProviderRuns)
	r.GET("/discovery/provider-health", h.ListDiscoveryProviderHealth)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/discovery/batches?kind=market-prescreen&status=failed", nil)
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"batch_id":"market-batch"`) || !strings.Contains(rec.Body.String(), `"error_message":"tiingo rate limited"`) {
		t.Fatalf("batches status=%d body=%s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/discovery/provider-runs?provider=tiingo&batch_id=market-batch", nil)
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"record_count":100`) || !strings.Contains(rec.Body.String(), `"coverage_pct":50`) {
		t.Fatalf("provider runs status=%d body=%s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/discovery/provider-health", nil)
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"provider":"tiingo"`) || !strings.Contains(rec.Body.String(), `"last_trade_date":"2026-06-30"`) {
		t.Fatalf("provider health status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestAppHandlerRoutesTableDriven(t *testing.T) {
	tests := []struct {
		name        string
		method      string
		path        string
		body        string
		seed        func(t *testing.T, db *gorm.DB)
		assert      func(t *testing.T, rec *httptest.ResponseRecorder, db *gorm.DB, sched *fakeScheduler)
		rawResponse bool
		wantStatus  int
	}{
		{name: "health", method: http.MethodGet, path: "/healthz", wantStatus: http.StatusOK},
		{name: "lookup ticker", method: http.MethodGet, path: "/sec/tickers/tsla", wantStatus: http.StatusOK, assert: func(t *testing.T, rec *httptest.ResponseRecorder, db *gorm.DB, sched *fakeScheduler) {
			if !strings.Contains(rec.Body.String(), `"ticker":"TSLA"`) || !strings.Contains(rec.Body.String(), `"cik":"0000320193"`) {
				t.Fatalf("lookup body = %s", rec.Body.String())
			}
		}},
		{name: "list health", method: http.MethodGet, path: "/list-health", wantStatus: http.StatusOK},
		{name: "export filings csv", method: http.MethodGet, path: "/exports/filings.csv", seed: seedFiling, rawResponse: true, wantStatus: http.StatusOK, assert: func(t *testing.T, rec *httptest.ResponseRecorder, db *gorm.DB, sched *fakeScheduler) {
			if !strings.Contains(rec.Body.String(), "ticker,company_name") || !strings.Contains(rec.Body.String(), "AAPL") {
				t.Fatalf("csv body = %s", rec.Body.String())
			}
		}},
		{name: "export ipo companies csv", method: http.MethodGet, path: "/exports/ipo-companies.csv", seed: seedIPOFiling, rawResponse: true, wantStatus: http.StatusOK, assert: func(t *testing.T, rec *httptest.ResponseRecorder, db *gorm.DB, sched *fakeScheduler) {
			if !strings.Contains(rec.Body.String(), "status_reason") || !strings.Contains(rec.Body.String(), "Acme Space Inc.") {
				t.Fatalf("ipo companies csv body = %s", rec.Body.String())
			}
		}},
		{name: "export ipo filings csv", method: http.MethodGet, path: "/exports/ipo-filings.csv", seed: seedIPOFiling, rawResponse: true, wantStatus: http.StatusOK, assert: func(t *testing.T, rec *httptest.ResponseRecorder, db *gorm.DB, sched *fakeScheduler) {
			if !strings.Contains(rec.Body.String(), "cik,company_name") || !strings.Contains(rec.Body.String(), "Acme Space Inc.") {
				t.Fatalf("ipo filings csv body = %s", rec.Body.String())
			}
		}},
		{name: "export targets csv", method: http.MethodGet, path: "/exports/watch-targets.csv", seed: seedTarget, rawResponse: true, wantStatus: http.StatusOK},
		{name: "export configs json", method: http.MethodGet, path: "/exports/configs.json", seed: seedTelegramConfig, rawResponse: true, wantStatus: http.StatusOK},
		{name: "export backup json", method: http.MethodGet, path: "/exports/backup.json", seed: seedFiling, rawResponse: true, wantStatus: http.StatusOK, assert: func(t *testing.T, rec *httptest.ResponseRecorder, db *gorm.DB, sched *fakeScheduler) {
			if !strings.Contains(rec.Body.String(), `"filings"`) || !strings.Contains(rec.Body.String(), `"configs"`) {
				t.Fatalf("backup body = %s", rec.Body.String())
			}
		}},
		{name: "create target", method: http.MethodPost, path: "/targets", body: `{"ticker":"aapl","company_name":"Apple Inc.","target_type":"stock","status":"enabled"}`, wantStatus: http.StatusCreated},
		{name: "reject invalid target", method: http.MethodPost, path: "/targets", body: `{"ticker":"","company_name":"Apple Inc.","target_type":"stock","status":"enabled"}`, wantStatus: http.StatusBadRequest},
		{name: "list targets", method: http.MethodGet, path: "/targets?page=bad&page_size=bad", seed: seedTarget, wantStatus: http.StatusOK},
		{name: "get target", method: http.MethodGet, path: "/targets/1", seed: seedTarget, wantStatus: http.StatusOK},
		{name: "missing target", method: http.MethodGet, path: "/targets/99", wantStatus: http.StatusNotFound},
		{name: "update target", method: http.MethodPut, path: "/targets/1", body: `{"ticker":"msft","company_name":"Microsoft Corp.","target_type":"stock","status":"enabled"}`, seed: seedTarget, wantStatus: http.StatusOK},
		{name: "set target status", method: http.MethodPatch, path: "/targets/1/status", body: `{"status":"disabled"}`, seed: seedTarget, wantStatus: http.StatusOK},
		{name: "delete target", method: http.MethodDelete, path: "/targets/1", seed: seedTarget, wantStatus: http.StatusNoContent},
		{name: "list filings", method: http.MethodGet, path: "/filings?ticker=AAPL&date_from=2026-06-01&date_to=bad", seed: seedFiling, wantStatus: http.StatusOK},
		{name: "get filing", method: http.MethodGet, path: "/filings/1", seed: seedFiling, wantStatus: http.StatusOK},
		{name: "refresh filings", method: http.MethodPost, path: "/filings/refresh", seed: seedTarget, wantStatus: http.StatusOK},
		{name: "preview filing cleanup", method: http.MethodGet, path: "/filings/cleanup-preview", seed: seedOldFiling, wantStatus: http.StatusOK, assert: func(t *testing.T, rec *httptest.ResponseRecorder, db *gorm.DB, sched *fakeScheduler) {
			if !strings.Contains(rec.Body.String(), `"delete_count":1`) {
				t.Fatalf("body = %s, want one delete candidate", rec.Body.String())
			}
		}},
		{name: "cleanup filings", method: http.MethodPost, path: "/filings/cleanup", seed: seedOldFiling, wantStatus: http.StatusOK, assert: func(t *testing.T, rec *httptest.ResponseRecorder, db *gorm.DB, sched *fakeScheduler) {
			if !strings.Contains(rec.Body.String(), `"deleted":1`) {
				t.Fatalf("body = %s, want one deleted", rec.Body.String())
			}
		}},
		{name: "refresh ipo radar", method: http.MethodPost, path: "/ipo-filings/refresh", wantStatus: http.StatusOK, assert: func(t *testing.T, rec *httptest.ResponseRecorder, db *gorm.DB, sched *fakeScheduler) {
			if !strings.Contains(rec.Body.String(), `"new_filings":1`) {
				t.Fatalf("body = %s, want new_filings", rec.Body.String())
			}
		}},
		{name: "list ipo radar filings", method: http.MethodGet, path: "/ipo-filings?filing_type=S-1&notified=no", seed: seedIPOFiling, wantStatus: http.StatusOK, assert: func(t *testing.T, rec *httptest.ResponseRecorder, db *gorm.DB, sched *fakeScheduler) {
			if !strings.Contains(rec.Body.String(), `"company_name":"Acme Space Inc."`) {
				t.Fatalf("body = %s, want ipo filing", rec.Body.String())
			}
		}},
		{name: "list ipo companies", method: http.MethodGet, path: "/ipo-companies?status=new", seed: seedIPOFiling, wantStatus: http.StatusOK, assert: func(t *testing.T, rec *httptest.ResponseRecorder, db *gorm.DB, sched *fakeScheduler) {
			if !strings.Contains(rec.Body.String(), `"status":"new"`) || !strings.Contains(rec.Body.String(), `"filing_count":1`) {
				t.Fatalf("body = %s, want ipo company status", rec.Body.String())
			}
		}},
		{name: "list ipo offering events", method: http.MethodGet, path: "/ipo-companies/0000000001/offerings?page=1&page_size=10", seed: seedIPOOfferingEvent, wantStatus: http.StatusOK, assert: func(t *testing.T, rec *httptest.ResponseRecorder, db *gorm.DB, sched *fakeScheduler) {
			if !strings.Contains(rec.Body.String(), `"offering_type":"initial"`) {
				t.Fatalf("body = %s, want offering event", rec.Body.String())
			}
		}},
		{name: "update ipo company override", method: http.MethodPut, path: "/ipo-companies/0000000001/override", body: `{"status_override":"withdrawn","final_ticker":"ACME","exchange":"NYSE","offer_price":"20.00","shares_offered":2500000,"listing_date":"2026-07-01","note":"manual"}`, seed: seedIPOFiling, wantStatus: http.StatusOK, assert: func(t *testing.T, rec *httptest.ResponseRecorder, db *gorm.DB, sched *fakeScheduler) {
			if !strings.Contains(rec.Body.String(), `"status_override":"withdrawn"`) || !strings.Contains(rec.Body.String(), `"final_ticker":"ACME"`) || !strings.Contains(rec.Body.String(), `"exchange":"NYSE"`) || !strings.Contains(rec.Body.String(), `"offer_price":"20.00"`) {
				t.Fatalf("body = %s, want override", rec.Body.String())
			}
		}},
		{name: "reject invalid ipo offer price", method: http.MethodPut, path: "/ipo-companies/0000000001/override", body: `{"offer_price":"invalid"}`, seed: seedIPOFiling, wantStatus: http.StatusBadRequest},
		{name: "reject invalid ipo shares", method: http.MethodPut, path: "/ipo-companies/0000000001/override", body: `{"shares_offered":-1}`, seed: seedIPOFiling, wantStatus: http.StatusBadRequest},
		{name: "reject invalid ipo listing date", method: http.MethodPut, path: "/ipo-companies/0000000001/override", body: `{"listing_date":"07/01/2026"}`, seed: seedIPOFiling, wantStatus: http.StatusBadRequest},
		{name: "sync target", method: http.MethodPost, path: "/targets/1/sync", seed: seedTarget, wantStatus: http.StatusOK, assert: func(t *testing.T, rec *httptest.ResponseRecorder, db *gorm.DB, sched *fakeScheduler) {
			if !strings.Contains(rec.Body.String(), `"new_filings":1`) {
				t.Fatalf("body = %s, want new_filings", rec.Body.String())
			}
		}},
		{name: "list target sync details", method: http.MethodGet, path: "/targets/1/sync-details", seed: seedSyncRunDetail, wantStatus: http.StatusOK, assert: func(t *testing.T, rec *httptest.ResponseRecorder, db *gorm.DB, sched *fakeScheduler) {
			if !strings.Contains(rec.Body.String(), `"ticker":"AAPL"`) || !strings.Contains(rec.Body.String(), `"duration_ms":2000`) {
				t.Fatalf("body = %s, want target sync details", rec.Body.String())
			}
		}},
		{name: "list sync runs", method: http.MethodGet, path: "/sync-runs?status=success&trigger=manual", seed: seedSyncRunDetail, wantStatus: http.StatusOK},
		{name: "list sync run details", method: http.MethodGet, path: "/sync-runs/1/details", seed: seedSyncRunDetail, wantStatus: http.StatusOK, assert: func(t *testing.T, rec *httptest.ResponseRecorder, db *gorm.DB, sched *fakeScheduler) {
			if !strings.Contains(rec.Body.String(), `"ticker":"AAPL"`) || !strings.Contains(rec.Body.String(), `"status":"success"`) {
				t.Fatalf("body = %s, want sync detail", rec.Body.String())
			}
		}},
		{name: "list configs", method: http.MethodGet, path: "/configs?category=telegram", seed: seedTelegramConfig, wantStatus: http.StatusOK},
		{name: "update configs", method: http.MethodPut, path: "/configs", body: `[{"key":"system.log_level","value":"debug","value_type":"string","category":"system"}]`, wantStatus: http.StatusOK},
		{name: "reload configs", method: http.MethodPost, path: "/configs/reload", wantStatus: http.StatusOK},
		{name: "get telegram config", method: http.MethodGet, path: "/telegram/config", seed: seedTelegramConfig, wantStatus: http.StatusOK},
		{name: "update telegram config", method: http.MethodPut, path: "/telegram/config", body: `{"bot_token":"token","chat_id":"10001","enabled":true}`, wantStatus: http.StatusOK},
		{name: "update telegram config preserves masked token", method: http.MethodPut, path: "/telegram/config", body: `{"bot_token":"tok******ken","chat_id":"20002","enabled":false}`, seed: seedTelegramConfig, wantStatus: http.StatusOK, assert: func(t *testing.T, rec *httptest.ResponseRecorder, db *gorm.DB, sched *fakeScheduler) {
			token, ok, err := service.NewConfigService(db, service.NewAuditService(db)).GetValue(context.Background(), "telegram.bot_token")
			if err != nil {
				t.Fatalf("get token: %v", err)
			}
			if !ok || token != "token" {
				t.Fatalf("stored token = %q, ok=%v, want original token", token, ok)
			}
		}},
		{name: "list operation logs", method: http.MethodGet, path: "/operation-logs?action=create", seed: seedTarget, wantStatus: http.StatusOK},
		{name: "list notification logs", method: http.MethodGet, path: "/notification-logs?status=success&channel=telegram", seed: seedNotification, wantStatus: http.StatusOK},
		{name: "list notification batches", method: http.MethodGet, path: "/notification-batches?source=filing&status=sent&trigger=scheduler", seed: seedNotificationBatch, wantStatus: http.StatusOK, assert: func(t *testing.T, rec *httptest.ResponseRecorder, db *gorm.DB, sched *fakeScheduler) {
			if !strings.Contains(rec.Body.String(), `"source":"filing"`) || !strings.Contains(rec.Body.String(), `"item_count":1`) {
				t.Fatalf("body = %s, want notification batch", rec.Body.String())
			}
		}},
		{name: "list notification batch items", method: http.MethodGet, path: "/notification-batches/1/items", seed: seedNotificationBatch, wantStatus: http.StatusOK, assert: func(t *testing.T, rec *httptest.ResponseRecorder, db *gorm.DB, sched *fakeScheduler) {
			if !strings.Contains(rec.Body.String(), `"filing_id":"batch-filing"`) {
				t.Fatalf("body = %s, want notification batch item", rec.Body.String())
			}
		}},
		{name: "list tasks", method: http.MethodGet, path: "/tasks", wantStatus: http.StatusOK},
		{name: "update task reloads scheduler", method: http.MethodPut, path: "/tasks/1", body: `{"cron_expr":"*/30 * * * *","enabled":false}`, wantStatus: http.StatusOK, assert: func(t *testing.T, rec *httptest.ResponseRecorder, db *gorm.DB, sched *fakeScheduler) {
			if sched.reloadCalls != 1 {
				t.Fatalf("reloadCalls = %d, want 1", sched.reloadCalls)
			}
		}},
		{name: "run task uses scheduler", method: http.MethodPost, path: "/tasks/1/run", wantStatus: http.StatusOK, assert: func(t *testing.T, rec *httptest.ResponseRecorder, db *gorm.DB, sched *fakeScheduler) {
			if sched.runCalls != 1 {
				t.Fatalf("runCalls = %d, want 1", sched.runCalls)
			}
			if len(sched.runTasks) != 1 || sched.runTasks[0] == "" {
				t.Fatalf("runTasks = %+v, want task name", sched.runTasks)
			}
		}},
		{name: "telegram test rejects masked token", method: http.MethodPost, path: "/telegram/test", seed: seedMaskedTelegramConfig, wantStatus: http.StatusBadRequest, assert: func(t *testing.T, rec *httptest.ResponseRecorder, db *gorm.DB, sched *fakeScheduler) {
			if !strings.Contains(rec.Body.String(), "重新输入真实 Token") {
				t.Fatalf("body = %s, want clear token error", rec.Body.String())
			}
		}},
		{name: "telegram test returns validation error without token", method: http.MethodPost, path: "/telegram/test", wantStatus: http.StatusInternalServerError},
		{name: "not implemented helper", method: http.MethodGet, path: "/not-implemented", wantStatus: http.StatusNotImplemented},
		{name: "invalid create json", method: http.MethodPost, path: "/targets", body: `{`, wantStatus: http.StatusInternalServerError},
		{name: "invalid update json", method: http.MethodPut, path: "/targets/1", body: `{`, seed: seedTarget, wantStatus: http.StatusInternalServerError},
		{name: "invalid status json", method: http.MethodPatch, path: "/targets/1/status", body: `{`, seed: seedTarget, wantStatus: http.StatusInternalServerError},
		{name: "invalid configs json", method: http.MethodPut, path: "/configs", body: `{`, wantStatus: http.StatusInternalServerError},
		{name: "invalid telegram json", method: http.MethodPut, path: "/telegram/config", body: `{`, wantStatus: http.StatusInternalServerError},
		{name: "invalid task json", method: http.MethodPut, path: "/tasks/1", body: `{`, wantStatus: http.StatusInternalServerError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, db, sched := testApp(t)
			if tt.seed != nil {
				tt.seed(t, db)
			}
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(tt.method, tt.path, bytes.NewBufferString(tt.body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Operator", "tester")
			r.ServeHTTP(rec, req)
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d, body=%s", rec.Code, tt.wantStatus, rec.Body.String())
			}
			if rec.Code != http.StatusNoContent && !tt.rawResponse {
				var payload map[string]any
				if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
					t.Fatalf("decode response: %v", err)
				}
			}
			if tt.assert != nil {
				tt.assert(t, rec, db, sched)
			}
		})
	}
}

func TestAppHandlerSchedulerErrorTableDriven(t *testing.T) {
	tests := []struct {
		name   string
		method string
		path   string
		body   string
		setup  func(*fakeScheduler)
	}{
		{name: "reload error", method: http.MethodPut, path: "/tasks/1", body: `{"cron_expr":"*/30 * * * *","enabled":true}`, setup: func(s *fakeScheduler) {
			s.reloadErr = errors.New("reload failed")
		}},
		{name: "run error", method: http.MethodPost, path: "/tasks/1/run", setup: func(s *fakeScheduler) {
			s.runErr = errors.New("run failed")
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, _, sched := testApp(t)
			tt.setup(sched)
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(tt.method, tt.path, bytes.NewBufferString(tt.body))
			req.Header.Set("Content-Type", "application/json")
			r.ServeHTTP(rec, req)
			if rec.Code != http.StatusInternalServerError {
				t.Fatalf("status = %d, want 500, body=%s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestAppHandlerRunTaskWithoutSchedulerTableDriven(t *testing.T) {
	tests := []struct {
		name string
		seed func(t *testing.T, db *gorm.DB)
	}{
		{name: "runs refresh fallback without scheduler", seed: seedTarget},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, db, _ := testApp(t)
			tt.seed(t, db)
			// Replace the route with a handler that has no scheduler to cover fallback behavior.
			audit := service.NewAuditService(db)
			configs := service.NewConfigService(db, audit)
			h := &AppHandler{
				DB:           db,
				Targets:      service.NewWatchTargetService(db, audit),
				Configs:      configs,
				Tasks:        service.NewTaskConfigService(db, audit),
				Filings:      service.NewFilingService(db, fakeSECClient{}, fakeNotifier{}, configs),
				IPO:          service.NewIPORadarService(db, fakeSECClient{}, fakeNotifier{}, configs),
				Audit:        audit,
				Notification: service.NewNotificationService(db),
			}
			r.POST("/tasks-no-scheduler/:id/run", h.RunTask)
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/tasks-no-scheduler/1/run", nil)
			r.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestAppHandlerDatabaseErrorTableDriven(t *testing.T) {
	tests := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{name: "list targets db error", method: http.MethodGet, path: "/targets"},
		{name: "delete target db error", method: http.MethodDelete, path: "/targets/1"},
		{name: "list filings db error", method: http.MethodGet, path: "/filings"},
		{name: "get filing db error", method: http.MethodGet, path: "/filings/1"},
		{name: "refresh filings db error", method: http.MethodPost, path: "/filings/refresh"},
		{name: "list configs db error", method: http.MethodGet, path: "/configs"},
		{name: "get telegram config db error", method: http.MethodGet, path: "/telegram/config"},
		{name: "list operation logs db error", method: http.MethodGet, path: "/operation-logs"},
		{name: "list notification logs db error", method: http.MethodGet, path: "/notification-logs"},
		{name: "list tasks db error", method: http.MethodGet, path: "/tasks"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, db, _ := testApp(t)
			sqlDB, err := db.DB()
			if err != nil {
				t.Fatalf("db handle: %v", err)
			}
			if err := sqlDB.Close(); err != nil {
				t.Fatalf("close db: %v", err)
			}
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(tt.method, tt.path, bytes.NewBufferString(tt.body))
			req.Header.Set("Content-Type", "application/json")
			r.ServeHTTP(rec, req)
			if rec.Code != http.StatusInternalServerError {
				t.Fatalf("status = %d, want 500, body=%s", rec.Code, rec.Body.String())
			}
		})
	}
}

func seedTarget(t *testing.T, db *gorm.DB) {
	t.Helper()
	if err := db.Create(&model.WatchTarget{
		Ticker: "AAPL", CompanyName: "Apple Inc.", CIK: "0000320193", TargetType: "stock", Status: "enabled",
	}).Error; err != nil {
		t.Fatalf("seed target: %v", err)
	}
}

func seedFiling(t *testing.T, db *gorm.DB) {
	t.Helper()
	if err := db.Create(&model.Filing{
		FilingID: "f1", Ticker: "AAPL", CIK: "0000320193", CompanyName: "Apple Inc.",
		FilingType: "8-K", FilingDate: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC), PulledAt: time.Now(),
	}).Error; err != nil {
		t.Fatalf("seed filing: %v", err)
	}
}

func seedOldFiling(t *testing.T, db *gorm.DB) {
	t.Helper()
	if err := db.Create(&model.Filing{
		FilingID: "old-f1", Ticker: "AAPL", CIK: "0000320193", CompanyName: "Apple Inc.",
		FilingType: "8-K", FilingDate: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC), PulledAt: time.Now().AddDate(0, 0, -60),
	}).Error; err != nil {
		t.Fatalf("seed old filing: %v", err)
	}
}

func seedIPOFiling(t *testing.T, db *gorm.DB) {
	t.Helper()
	if err := db.Create(&model.IPOFiling{
		FilingID:    "ipo-1",
		CIK:         "0000000001",
		CompanyName: "Acme Space Inc.",
		FilingType:  "S-1",
		FilingDate:  time.Date(2026, 6, 18, 0, 0, 0, 0, time.UTC),
		FilingURL:   "https://sec.gov/acme/s1",
		Title:       "S-1 - Acme Space Inc.",
	}).Error; err != nil {
		t.Fatalf("seed ipo filing: %v", err)
	}
}

func seedIPOOfferingEvent(t *testing.T, db *gorm.DB) {
	t.Helper()
	if err := db.Create(&model.IPOOfferingEvent{FilingID: "offering-1", CIK: "0000000001", CompanyName: "Acme Inc.", OfferingType: "initial", ParseStatus: "parsed", OfferPrice: "16.00", FilingDate: time.Now().UTC(), ParserVersion: 4}).Error; err != nil {
		t.Fatalf("seed ipo offering event: %v", err)
	}
}

func seedTelegramConfig(t *testing.T, db *gorm.DB) {
	t.Helper()
	if err := service.NewConfigService(db, service.NewAuditService(db)).UpsertMany(context.Background(), []service.ConfigInput{
		{Key: "telegram.bot_token", Value: "token", ValueType: "string", Category: "telegram", Encrypted: true},
		{Key: "telegram.chat_id", Value: "10001", ValueType: "string", Category: "telegram"},
		{Key: "telegram.enabled", Value: "true", ValueType: "bool", Category: "telegram"},
	}, "tester"); err != nil {
		t.Fatalf("seed configs: %v", err)
	}
}

func seedMaskedTelegramConfig(t *testing.T, db *gorm.DB) {
	t.Helper()
	if err := service.NewConfigService(db, service.NewAuditService(db)).UpsertMany(context.Background(), []service.ConfigInput{
		{Key: "telegram.bot_token", Value: "tok******ken", ValueType: "string", Category: "telegram", Encrypted: true},
		{Key: "telegram.chat_id", Value: "10001", ValueType: "string", Category: "telegram"},
		{Key: "telegram.enabled", Value: "true", ValueType: "bool", Category: "telegram"},
	}, "tester"); err != nil {
		t.Fatalf("seed masked configs: %v", err)
	}
}

func seedNotification(t *testing.T, db *gorm.DB) {
	t.Helper()
	if err := db.Create(&model.NotificationLog{FilingID: "f1", Channel: "telegram", Status: "success"}).Error; err != nil {
		t.Fatalf("seed notification: %v", err)
	}
}

func seedNotificationBatch(t *testing.T, db *gorm.DB) {
	t.Helper()
	batch := model.NotificationBatch{SyncRunID: 1, Source: "filing", Trigger: "scheduler", Channel: "telegram", Status: "sent", ItemCount: 1, SentCount: 1}
	if err := db.Create(&batch).Error; err != nil {
		t.Fatalf("seed notification batch: %v", err)
	}
	item := model.NotificationBatchItem{BatchID: batch.ID, EntityKind: "filing", FilingID: "batch-filing", CompanyName: "Acme", FilingType: "8-K", EventAt: time.Now(), Status: "sent", Reason: "eligible"}
	if err := db.Create(&item).Error; err != nil {
		t.Fatalf("seed notification batch item: %v", err)
	}
}

func seedSyncRunDetail(t *testing.T, db *gorm.DB) {
	t.Helper()
	startedAt := time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC)
	finishedAt := startedAt.Add(2 * time.Second)
	run := model.SyncRun{
		StartedAt:      startedAt,
		FinishedAt:     &finishedAt,
		Status:         "success",
		Trigger:        "manual",
		TargetsChecked: 1,
		NewFilings:     1,
	}
	if err := db.Create(&run).Error; err != nil {
		t.Fatalf("seed sync run: %v", err)
	}
	if err := db.Create(&model.SyncRunDetail{
		SyncRunID:  run.ID,
		TargetID:   1,
		Ticker:     "AAPL",
		Status:     "success",
		NewFilings: 1,
		StartedAt:  startedAt,
		FinishedAt: &finishedAt,
		DurationMS: 2000,
	}).Error; err != nil {
		t.Fatalf("seed sync run detail: %v", err)
	}
}
