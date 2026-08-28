package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"sec_monitor/internal/discovery"
	"sec_monitor/internal/model"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestListInsiderTransactionsReturnsNormalizedFactsAndSummary(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:insider-handler?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&discovery.Security{}, &discovery.Listing{}, &discovery.SecurityBatchIdentity{}, &discovery.InsiderTransactionSnapshot{}, &discovery.UniverseBatch{}, &discovery.CurrentBatchPointer{}, &discovery.CandidateScoreSnapshot{}); err != nil {
		t.Fatal(err)
	}
	mainDB, err := gorm.Open(sqlite.Open("file:insider-handler-main?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := mainDB.AutoMigrate(&model.WatchTarget{}); err != nil {
		t.Fatal(err)
	}
	security := discovery.Security{CIK: "0000000001", CompanyName: "Example Corp"}
	if err := db.Create(&security).Error; err != nil {
		t.Fatal(err)
	}
	validTo := time.Now().Add(-time.Hour)
	if err := db.Create(&discovery.Listing{SecurityID: security.ID, Ticker: "EXM", ValidFrom: time.Now().AddDate(-1, 0, 0), ValidTo: &validTo}).Error; err != nil {
		t.Fatal(err)
	}
	rows := []discovery.InsiderTransactionSnapshot{
		{SecurityID: security.ID, IdentitySHA256: "buy", Accession: "1", OwnerName: "Jane CEO", OfficerTitle: "CEO", Role: "ceo", TransactionDate: time.Now(), TransactionCode: "P", AcquiredDisposedCode: "A", SharesMicros: 2_000_000, PriceMicros: 5_000_000, ValueMicros: 10_000_000, Qualified: true, SourceURL: "https://www.sec.gov/example"},
		{SecurityID: security.ID, IdentitySHA256: "sell", Accession: "2", OwnerName: "John CFO", Role: "cfo", TransactionDate: time.Now().Add(-time.Hour), TransactionCode: "S", AcquiredDisposedCode: "D", ValueMicros: 4_000_000},
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	batch := discovery.UniverseBatch{BatchID: "current-prescreen", Kind: discovery.BatchKindPrescreen, Status: discovery.BatchStatusPublished, EffectiveDate: time.Now().Format(time.DateOnly), StartedAt: time.Now()}
	if err := db.Create(&batch).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&discovery.CurrentBatchPointer{Kind: discovery.BatchKindPrescreen, BatchID: batch.BatchID, UpdatedAt: time.Now()}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&discovery.CandidateScoreSnapshot{BatchID: batch.BatchID, SecurityID: security.ID, Ticker: "EXM", Grade: discovery.CandidateGradeA}).Error; err != nil {
		t.Fatal(err)
	}

	gin.SetMode(gin.TestMode)
	router := gin.New()
	h := &AppHandler{DB: mainDB, DiscoveryDB: db}
	router.GET("/insider-transactions", h.ListInsiderTransactions)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/insider-transactions?ticker=exm&direction=buy", nil)
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Data insiderTransactionPage `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Data.Total != 1 || len(response.Data.Items) != 1 {
		t.Fatalf("unexpected page: %+v", response.Data)
	}
	item := response.Data.Items[0]
	if item.Ticker != "EXM" || item.Direction != "buy" || item.Shares != 2 || item.PriceUSD != 5 || item.ValueUSD != 10 {
		t.Fatalf("unexpected item: %+v", item)
	}
	if response.Data.Summary.Purchases != 1 || response.Data.Summary.Sales != 0 || response.Data.Summary.BuyValueUSD != 10 {
		t.Fatalf("unexpected summary: %+v", response.Data.Summary)
	}

	identityOnly := discovery.Security{CIK: "0000000002", CompanyName: "Identity Only Corp"}
	if err := db.Create(&identityOnly).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&discovery.SecurityBatchIdentity{BatchID: "published-security-batch", SecurityID: identityOnly.ID, CIK: identityOnly.CIK, Ticker: "IDN", CompanyName: identityOnly.CompanyName, CreatedAt: time.Now()}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&discovery.InsiderTransactionSnapshot{SecurityID: identityOnly.ID, IdentitySHA256: "identity-only-buy", Accession: "3", OwnerName: "Identity CEO", Role: "ceo", TransactionDate: time.Now(), TransactionCode: "P", AcquiredDisposedCode: "A", Qualified: true}).Error; err != nil {
		t.Fatal(err)
	}
	if err := mainDB.Create(&model.WatchTarget{Ticker: "IDN", CompanyName: identityOnly.CompanyName, CIK: identityOnly.CIK, TargetType: "stock", Status: "enabled"}).Error; err != nil {
		t.Fatal(err)
	}
	identityRecorder := httptest.NewRecorder()
	identityRequest := httptest.NewRequest(http.MethodGet, "/insider-transactions?ticker=idn", nil)
	router.ServeHTTP(identityRecorder, identityRequest)
	if identityRecorder.Code != http.StatusOK {
		t.Fatalf("identity status=%d body=%s", identityRecorder.Code, identityRecorder.Body.String())
	}
	response = struct {
		Data insiderTransactionPage `json:"data"`
	}{}
	if err := json.Unmarshal(identityRecorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Data.Total != 1 || len(response.Data.Items) != 1 || response.Data.Items[0].Ticker != "IDN" {
		t.Fatalf("unexpected identity-backed page: %+v", response.Data)
	}

	historicalOnly := discovery.Security{CIK: "0000000003", CompanyName: "Historical Only Corp"}
	if err := db.Create(&historicalOnly).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&discovery.SecurityBatchIdentity{BatchID: "old-security-batch", SecurityID: historicalOnly.ID, CIK: historicalOnly.CIK, Ticker: "OLD", CompanyName: historicalOnly.CompanyName, CreatedAt: time.Now().Add(-24 * time.Hour)}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&discovery.InsiderTransactionSnapshot{SecurityID: historicalOnly.ID, IdentitySHA256: "historical-buy", Accession: "4", OwnerName: "Old CEO", Role: "ceo", TransactionDate: time.Now(), TransactionCode: "P", AcquiredDisposedCode: "A", Qualified: true, ValueMicros: 50_000_000}).Error; err != nil {
		t.Fatal(err)
	}
	historicalRecorder := httptest.NewRecorder()
	historicalRequest := httptest.NewRequest(http.MethodGet, "/insider-transactions?ticker=old", nil)
	router.ServeHTTP(historicalRecorder, historicalRequest)
	if historicalRecorder.Code != http.StatusOK {
		t.Fatalf("historical status=%d body=%s", historicalRecorder.Code, historicalRecorder.Body.String())
	}
	response = struct {
		Data insiderTransactionPage `json:"data"`
	}{}
	if err := json.Unmarshal(historicalRecorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Data.Total != 0 || len(response.Data.Items) != 0 || response.Data.Summary.Transactions != 0 {
		t.Fatalf("historical-only ticker leaked into current scope: %+v", response.Data)
	}
}
