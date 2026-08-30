package handler

import (
	"context"
	"strings"
	"testing"
	"time"

	"sec_monitor/internal/discovery"
	"sec_monitor/internal/sec"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type fakeInsiderPlanBackfillSEC struct {
	documents map[string]string
	filings   map[string][]sec.FilingResult
}

func (f *fakeInsiderPlanBackfillSEC) LookupCIK(context.Context, string) (string, string, error) {
	return "", "", nil
}

func (f *fakeInsiderPlanBackfillSEC) ListFilings(_ context.Context, query sec.FilingQuery) ([]sec.FilingResult, error) {
	return f.filings[query.CIK], nil
}

func (f *fakeInsiderPlanBackfillSEC) FetchFilingDocument(_ context.Context, filingURL string) (string, error) {
	return f.documents[filingURL], nil
}

func TestBackfillInsiderPlanHistoryReparsesPersistedForm4(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:insider-plan-backfill?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&discovery.Security{}, &discovery.Listing{}, &discovery.SecurityBatchIdentity{}, &discovery.InsiderTransactionSnapshot{}, &discovery.InsiderTradingPlan{}, &discovery.InsiderTradingPlanEvent{}, &discovery.InsiderPlanDocumentReceipt{}, &discovery.SecuritySourceCheckpoint{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`CREATE UNIQUE INDEX idx_insider_tx_identity_v2 ON insider_transaction_snapshots(security_id, identity_sha256)`).Error; err != nil {
		t.Fatal(err)
	}
	security := discovery.Security{CIK: "0000001234", CompanyName: "Example Corp"}
	if err := db.Create(&security).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&discovery.Listing{SecurityID: security.ID, Ticker: "ACME", ValidFrom: time.Now().AddDate(-1, 0, 0)}).Error; err != nil {
		t.Fatal(err)
	}
	const sourceURL = "https://www.sec.gov/Archives/edgar/data/1234/000000123426000010/form4.xml"
	const accession = "0000001234-26-000010"
	const form4 = `<ownershipDocument>
  <documentType>4</documentType><aff10b5One>true</aff10b5One>
  <issuer><issuerCik>0000001234</issuerCik><issuerTradingSymbol>ACME</issuerTradingSymbol></issuer>
  <reportingOwner><reportingOwnerId><rptOwnerCik>0000005678</rptOwnerCik><rptOwnerName>Plan Seller</rptOwnerName></reportingOwnerId><reportingOwnerRelationship><isOfficer>1</isOfficer><officerTitle>Chief Financial Officer</officerTitle></reportingOwnerRelationship></reportingOwner>
  <nonDerivativeTable><nonDerivativeTransaction><transactionDate><value>2026-08-27</value></transactionDate><transactionCoding><transactionCode>S</transactionCode></transactionCoding><transactionAmounts><transactionShares><value>6034</value></transactionShares><transactionPricePerShare><value>66.65</value></transactionPricePerShare><transactionAcquiredDisposedCode><value>D</value></transactionAcquiredDisposedCode></transactionAmounts></nonDerivativeTransaction></nonDerivativeTable>
  <footnotes><footnote id="F1">The transaction was effected pursuant to a Rule 10b5-1 trading plan adopted on September 19, 2025.</footnote></footnotes>
</ownershipDocument>`
	parsed, err := discovery.ParseForm4OwnershipXML(strings.NewReader(form4), accession, sourceURL)
	if err != nil || len(parsed) != 1 {
		t.Fatalf("parse fixture: transactions=%d err=%v", len(parsed), err)
	}
	row := discovery.InsiderTransactionToSnapshot(security.ID, parsed[0], time.Now())
	row.ParserVersion = "form4-parser-v4"
	row.IsTenB5One = false
	row.TenB5OneStatus = discovery.TenB5OneStatusNotDisclosed
	row.TenB5OnePlanAdoptionDate = nil
	if err := db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	h := &AppHandler{DiscoveryDB: db}
	const renderedForm144URL = "https://www.sec.gov/Archives/edgar/data/5678/000000567826000020/xsl144X01/primary_doc.xml"
	const rawForm144URL = "https://www.sec.gov/Archives/edgar/data/5678/000000567826000020/primary_doc.xml"
	const form144 = `<edgarSubmission><formData><issuerInfo><issuerCik>0000001234</issuerCik><nameOfPersonForWhoseAccountTheSecuritiesAreToBeSold>Plan Seller</nameOfPersonForWhoseAccountTheSecuritiesAreToBeSold><relationshipsToIssuer><relationshipToIssuer>Officer</relationshipToIssuer></relationshipsToIssuer></issuerInfo><securitiesInformation><noOfUnitsSold>6034</noOfUnitsSold><aggregateMarketValue>402156.10</aggregateMarketValue><approxSaleDate>08/27/2026</approxSaleDate></securitiesInformation><noticeSignature><noticeDate>08/24/2026</noticeDate><planAdoptionDates><planAdoptionDate>09/19/2025</planAdoptionDate></planAdoptionDates></noticeSignature></formData></edgarSubmission>`
	client := &fakeInsiderPlanBackfillSEC{
		documents: map[string]string{sourceURL: form4, renderedForm144URL: "<html><head><link></head></html>", rawForm144URL: form144},
		filings:   map[string][]sec.FilingResult{"0000005678": {{AccessionNumber: "0000005678-26-000020", FilingType: "144", FilingDate: time.Date(2026, 8, 24, 0, 0, 0, 0, time.UTC), FilingURL: renderedForm144URL}}},
	}
	result, err := h.backfillInsiderPlanHistory(context.Background(), client, []string{"ACME"}, time.Date(2026, 8, 29, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if result.PendingForm4Documents != 1 || result.ParsedForm4Documents != 1 || result.FailedForm4Documents != 0 || result.ParsedForm144Documents != 1 || result.RegisteredPlans != 1 {
		t.Fatalf("unexpected result: %+v", result)
	}
	var updated discovery.InsiderTransactionSnapshot
	if err := db.First(&updated, row.ID).Error; err != nil {
		t.Fatal(err)
	}
	if updated.ParserVersion != discovery.InsiderParserVersion || !updated.IsTenB5One || updated.TenB5OnePlanAdoptionDate == nil {
		t.Fatalf("transaction was not backfilled: %+v", updated)
	}
	var planCount int64
	if err := db.Model(&discovery.InsiderTradingPlan{}).Count(&planCount).Error; err != nil || planCount != 1 {
		t.Fatalf("plan count=%d err=%v", planCount, err)
	}
	var plan discovery.InsiderTradingPlan
	if err := db.First(&plan).Error; err != nil || plan.PrimarySourceForm != "144" || plan.PrimarySourceURL != rawForm144URL {
		t.Fatalf("Form 144 evidence was not linked: plan=%+v err=%v", plan, err)
	}
	second, err := h.backfillInsiderPlanHistory(context.Background(), client, []string{"ACME"}, time.Date(2026, 8, 30, 0, 0, 0, 0, time.UTC))
	if err != nil || second.ParsedForm144Documents != 0 {
		t.Fatalf("successful Form 144 should not be downloaded twice: result=%+v err=%v", second, err)
	}
}

func TestRawSECXMLFallbackURL(t *testing.T) {
	got, ok := rawSECXMLFallbackURL("https://www.sec.gov/Archives/edgar/data/5678/123/xsl144X01/primary_doc.xml", "xsl144")
	if !ok || got != "https://www.sec.gov/Archives/edgar/data/5678/123/primary_doc.xml" {
		t.Fatalf("fallback=%q ok=%v", got, ok)
	}
	for _, invalid := range []string{
		"https://example.com/Archives/edgar/data/1/2/xsl144X01/primary_doc.xml",
		"https://www.sec.gov/Archives/edgar/data/1/2/primary_doc.xml",
		"https://www.sec.gov/Archives/edgar/data/1/2/xsl144X01/primary_doc.htm",
	} {
		if fallback, ok := rawSECXMLFallbackURL(invalid, "xsl144"); ok {
			t.Fatalf("unexpected fallback for %q: %q", invalid, fallback)
		}
	}
}
