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
	if err := db.AutoMigrate(&discovery.Security{}, &discovery.Listing{}, &discovery.SecurityBatchIdentity{}, &discovery.InsiderTransactionSnapshot{}, &discovery.InsiderTradingPlan{}, &discovery.InsiderTradingPlanEvent{}, &discovery.SecuritySourceCheckpoint{}, &discovery.UniverseBatch{}, &discovery.CurrentBatchPointer{}, &discovery.CandidateScoreSnapshot{}); err != nil {
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
		{SecurityID: security.ID, IdentitySHA256: "sell", Accession: "2", OwnerName: "John CFO", Role: "cfo", TransactionDate: time.Now().Add(-time.Hour), TransactionCode: "S", AcquiredDisposedCode: "D", ValueMicros: 4_000_000, IsTenB5One: true, TenB5OneStatus: discovery.TenB5OneStatusConfirmed, TenB5OneEvidenceSource: "form4_checkbox"},
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
	router.GET("/insider-trading-plans", h.ListInsiderTradingPlans)
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
	planRecorder := httptest.NewRecorder()
	planRequest := httptest.NewRequest(http.MethodGet, "/insider-transactions?ticker=exm&direction=sell&ten_b5_1_status=confirmed", nil)
	router.ServeHTTP(planRecorder, planRequest)
	if planRecorder.Code != http.StatusOK {
		t.Fatalf("plan status=%d body=%s", planRecorder.Code, planRecorder.Body.String())
	}
	response = struct {
		Data insiderTransactionPage `json:"data"`
	}{}
	if err := json.Unmarshal(planRecorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Data.Total != 1 || !response.Data.Items[0].IsTenB5One || response.Data.Items[0].ResearchInterpretation != "planned_sale_reduced_bearish" || response.Data.Summary.PlannedSales != 1 {
		t.Fatalf("unexpected 10b5-1 page: %+v", response.Data)
	}
	adopted := time.Date(2025, 9, 19, 0, 0, 0, 0, time.UTC)
	plan := discovery.InsiderTradingPlan{SecurityID: security.ID, IdentitySHA256: "plan-1", OwnerKey: "john cfo", OwnerName: "John CFO", AdoptionDate: adopted, Status: discovery.InsiderPlanStatusExecuting, EvidenceConfidence: discovery.InsiderPlanConfidenceConfirmed, ExecutedSharesMicros: 6_000_000_000, ExecutionCount: 1, PrimarySourceForm: "4", PrimarySourceURL: "https://www.sec.gov/plan"}
	if err := db.Create(&plan).Error; err != nil {
		t.Fatal(err)
	}
	registryRecorder := httptest.NewRecorder()
	planListRequest := httptest.NewRequest(http.MethodGet, "/insider-trading-plans?ticker=exm", nil)
	router.ServeHTTP(registryRecorder, planListRequest)
	if registryRecorder.Code != http.StatusOK {
		t.Fatalf("plan list status=%d body=%s", registryRecorder.Code, registryRecorder.Body.String())
	}
	var planResponse struct {
		Data insiderPlanPage `json:"data"`
	}
	if err := json.Unmarshal(registryRecorder.Body.Bytes(), &planResponse); err != nil {
		t.Fatal(err)
	}
	if planResponse.Data.Total != 1 || len(planResponse.Data.Items) != 1 || planResponse.Data.Items[0].Ticker != "EXM" || planResponse.Data.Items[0].ExecutedShares != 6000 || planResponse.Data.Items[0].MaximumSharesKnown {
		t.Fatalf("unexpected plan registry page: %+v", planResponse.Data)
	}
	if planResponse.Data.Coverage.Status != "pending" || planResponse.Data.Coverage.ScopedTransactions != 2 || planResponse.Data.Coverage.ParsedTransactions != 0 || planResponse.Data.Coverage.RequiredParserVersion != discovery.InsiderParserVersion {
		t.Fatalf("unexpected plan coverage: %+v", planResponse.Data.Coverage)
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
	if err := db.Create(&discovery.InsiderTradingPlan{SecurityID: identityOnly.ID, IdentitySHA256: "plan-watch", OwnerKey: "identity ceo", OwnerName: "Identity CEO", AdoptionDate: adopted, Status: discovery.InsiderPlanStatusActive, EvidenceConfidence: discovery.InsiderPlanConfidenceConfirmed, PrimarySourceForm: "144", PrimarySourceURL: "https://www.sec.gov/watch-plan"}).Error; err != nil {
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
	watchRecorder := httptest.NewRecorder()
	watchRequest := httptest.NewRequest(http.MethodGet, "/insider-transactions?source=watch", nil)
	router.ServeHTTP(watchRecorder, watchRequest)
	if watchRecorder.Code != http.StatusOK {
		t.Fatalf("watch source status=%d body=%s", watchRecorder.Code, watchRecorder.Body.String())
	}
	response = struct {
		Data insiderTransactionPage `json:"data"`
	}{}
	if err := json.Unmarshal(watchRecorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Data.Total != 1 || response.Data.Items[0].Ticker != "IDN" || response.Data.Summary.Issuers != 1 {
		t.Fatalf("unexpected watch source page: %+v", response.Data)
	}
	candidateRecorder := httptest.NewRecorder()
	candidateRequest := httptest.NewRequest(http.MethodGet, "/insider-transactions?source=candidate", nil)
	router.ServeHTTP(candidateRecorder, candidateRequest)
	if candidateRecorder.Code != http.StatusOK {
		t.Fatalf("candidate source status=%d body=%s", candidateRecorder.Code, candidateRecorder.Body.String())
	}
	response = struct {
		Data insiderTransactionPage `json:"data"`
	}{}
	if err := json.Unmarshal(candidateRecorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Data.Total != 2 || response.Data.Items[0].Ticker != "EXM" || response.Data.Summary.Issuers != 1 {
		t.Fatalf("unexpected candidate source page: %+v", response.Data)
	}
	invalidSourceRecorder := httptest.NewRecorder()
	router.ServeHTTP(invalidSourceRecorder, httptest.NewRequest(http.MethodGet, "/insider-transactions?source=unknown", nil))
	if invalidSourceRecorder.Code != http.StatusBadRequest {
		t.Fatalf("invalid source status=%d body=%s", invalidSourceRecorder.Code, invalidSourceRecorder.Body.String())
	}
	watchPlanRecorder := httptest.NewRecorder()
	router.ServeHTTP(watchPlanRecorder, httptest.NewRequest(http.MethodGet, "/insider-trading-plans?source=watch", nil))
	if watchPlanRecorder.Code != http.StatusOK {
		t.Fatalf("watch plan source status=%d body=%s", watchPlanRecorder.Code, watchPlanRecorder.Body.String())
	}
	planResponse = struct {
		Data insiderPlanPage `json:"data"`
	}{}
	if err := json.Unmarshal(watchPlanRecorder.Body.Bytes(), &planResponse); err != nil {
		t.Fatal(err)
	}
	if planResponse.Data.Total != 1 || planResponse.Data.Items[0].Ticker != "IDN" || planResponse.Data.Coverage.RegisteredPlans != 1 {
		t.Fatalf("unexpected watch plan source page: %+v", planResponse.Data)
	}
	candidatePlanRecorder := httptest.NewRecorder()
	router.ServeHTTP(candidatePlanRecorder, httptest.NewRequest(http.MethodGet, "/insider-trading-plans?source=candidate", nil))
	if candidatePlanRecorder.Code != http.StatusOK {
		t.Fatalf("candidate plan source status=%d body=%s", candidatePlanRecorder.Code, candidatePlanRecorder.Body.String())
	}
	planResponse = struct {
		Data insiderPlanPage `json:"data"`
	}{}
	if err := json.Unmarshal(candidatePlanRecorder.Body.Bytes(), &planResponse); err != nil {
		t.Fatal(err)
	}
	if planResponse.Data.Total != 1 || planResponse.Data.Items[0].Ticker != "EXM" || planResponse.Data.Coverage.RegisteredPlans != 1 {
		t.Fatalf("unexpected candidate plan source page: %+v", planResponse.Data)
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

func TestInsiderTransactionSummaryUsesOnlyPricedOpenMarketCashTrades(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:insider-summary-cash?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&discovery.InsiderTransactionSnapshot{}); err != nil {
		t.Fatal(err)
	}
	rows := []discovery.InsiderTransactionSnapshot{
		{SecurityID: 1, IdentitySHA256: "purchase", TransactionCode: "P", AcquiredDisposedCode: "A", PriceMicros: 5_000_000, ValueMicros: 10_000_000},
		{SecurityID: 1, IdentitySHA256: "sale", TransactionCode: "S", AcquiredDisposedCode: "D", PriceMicros: 4_000_000, ValueMicros: 4_000_000, IsTenB5One: true},
		// A stock award can carry a large reference value in Form 4. It is not a
		// cash purchase and must never inflate the headline net-buy amount.
		{SecurityID: 1, IdentitySHA256: "award", TransactionCode: "A", AcquiredDisposedCode: "A", PriceMicros: 100_000_000, ValueMicros: 9_000_000_000_000},
		// An unpriced P transaction remains a public-market record, while its
		// unknown cash value stays outside the amount aggregate.
		{SecurityID: 1, IdentitySHA256: "unpriced", TransactionCode: "P", AcquiredDisposedCode: "A"},
		{SecurityID: 2, IdentitySHA256: "derivative-sale", TransactionCode: "S", AcquiredDisposedCode: "D", Derivative: true, PriceMicros: 7_000_000, ValueMicros: 7_000_000},
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	gin.SetMode(gin.TestMode)
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Request = httptest.NewRequest(http.MethodGet, "/insider-transactions", nil)
	handler := &AppHandler{DiscoveryDB: db}
	summary, err := handler.insiderTransactionSummary(context, db.Model(&discovery.InsiderTransactionSnapshot{}))
	if err != nil {
		t.Fatal(err)
	}
	if summary.Transactions != 5 || summary.Issuers != 2 || summary.Purchases != 2 || summary.PricedPurchases != 1 || summary.Sales != 1 || summary.PricedSales != 1 {
		t.Fatalf("unexpected open-market counts: %+v", summary)
	}
	if summary.OtherAcquisitions != 1 || summary.OtherDispositions != 1 || summary.PlannedSales != 1 {
		t.Fatalf("unexpected non-cash/plan counts: %+v", summary)
	}
	if summary.BuyValueUSD != 10 || summary.SellValueUSD != 4 || summary.NetValueUSD != 6 {
		t.Fatalf("non-cash award leaked into cash summary: %+v", summary)
	}
}
