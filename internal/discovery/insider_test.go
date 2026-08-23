package discovery

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestParseForm4OwnershipXMLQualifiesKeyRoleOpenMarketPurchase(t *testing.T) {
	xml := `<ownershipDocument>
  <documentType>4</documentType>
  <periodOfReport>2026-06-20</periodOfReport>
  <issuer><issuerCik>0000001234</issuerCik><issuerTradingSymbol>ACME</issuerTradingSymbol></issuer>
  <reportingOwner>
    <reportingOwnerId><rptOwnerName>Jane Buyer</rptOwnerName></reportingOwnerId>
    <reportingOwnerRelationship><isOfficer>1</isOfficer><officerTitle>Chief Executive Officer</officerTitle></reportingOwnerRelationship>
  </reportingOwner>
  <nonDerivativeTable>
    <nonDerivativeTransaction>
      <securityTitle><value>Common Stock</value></securityTitle>
      <transactionDate><value>2026-06-18</value></transactionDate>
      <transactionCoding><transactionCode>P</transactionCode></transactionCoding>
      <transactionAmounts>
        <transactionShares><value>10000</value></transactionShares>
        <transactionPricePerShare><value>2.50</value></transactionPricePerShare>
        <transactionAcquiredDisposedCode><value>A</value></transactionAcquiredDisposedCode>
      </transactionAmounts>
      <postTransactionAmounts><sharesOwnedFollowingTransaction><value>50000</value></sharesOwnedFollowingTransaction></postTransactionAmounts>
      <ownershipNature><directOrIndirectOwnership><value>D</value></directOrIndirectOwnership></ownershipNature>
    </nonDerivativeTransaction>
  </nonDerivativeTable>
</ownershipDocument>`

	transactions, err := ParseForm4OwnershipXML(strings.NewReader(xml), "0000001234-26-000001", "https://www.sec.gov/form4.xml")
	if err != nil {
		t.Fatal(err)
	}
	if len(transactions) != 1 {
		t.Fatalf("transactions = %#v", transactions)
	}
	tx := transactions[0]
	if !tx.Qualified || tx.ExclusionReason != "" || tx.Role != InsiderRoleCEO {
		t.Fatalf("tx qualification = %#v", tx)
	}
	if tx.TransactionCode != "P" || tx.AcquiredDisposedCode != "A" || tx.ValueUSD != 25_000 || tx.Shares != 10_000 {
		t.Fatalf("tx details = %#v", tx)
	}
	if tx.SharesOwnedBefore != 40_000 || tx.SourceURL == "" || !tx.TransactionDate.Equal(time.Date(2026, 6, 18, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("tx evidence = %#v", tx)
	}
}

func TestParseForm4OwnershipXMLExcludesNonPurchaseAndDerivativeRows(t *testing.T) {
	xml := `<ownershipDocument>
  <documentType>4/A</documentType>
  <issuer><issuerCik>0000001234</issuerCik><issuerTradingSymbol>ACME</issuerTradingSymbol></issuer>
  <reportingOwner>
    <reportingOwnerId><rptOwnerName>John CFO</rptOwnerName></reportingOwnerId>
    <reportingOwnerRelationship><isOfficer>true</isOfficer><officerTitle>Chief Financial Officer</officerTitle></reportingOwnerRelationship>
  </reportingOwner>
  <nonDerivativeTable>
    <nonDerivativeTransaction>
      <transactionDate><value>2026-06-18</value></transactionDate>
      <transactionCoding><transactionCode>A</transactionCode></transactionCoding>
      <transactionAmounts>
        <transactionShares><value>100</value></transactionShares>
        <transactionPricePerShare><value>0</value></transactionPricePerShare>
        <transactionAcquiredDisposedCode><value>A</value></transactionAcquiredDisposedCode>
      </transactionAmounts>
    </nonDerivativeTransaction>
  </nonDerivativeTable>
  <derivativeTable>
    <derivativeTransaction>
      <transactionDate><value>2026-06-18</value></transactionDate>
      <transactionCoding><transactionCode>P</transactionCode></transactionCoding>
      <transactionAmounts>
        <transactionShares><value>100</value></transactionShares>
        <transactionPricePerShare><value>1</value></transactionPricePerShare>
        <transactionAcquiredDisposedCode><value>A</value></transactionAcquiredDisposedCode>
      </transactionAmounts>
    </derivativeTransaction>
  </derivativeTable>
</ownershipDocument>`

	transactions, err := ParseForm4OwnershipXML(strings.NewReader(xml), "0000001234-26-000002", "https://www.sec.gov/form4a.xml")
	if err != nil {
		t.Fatal(err)
	}
	if len(transactions) != 2 {
		t.Fatalf("transactions = %#v", transactions)
	}
	if transactions[0].Qualified || transactions[0].ExclusionReason != InsiderExclusionNotOpenMarketPurchase {
		t.Fatalf("non-derivative grant row = %#v", transactions[0])
	}
	if transactions[1].Qualified || transactions[1].ExclusionReason != InsiderExclusionDerivative {
		t.Fatalf("derivative row = %#v", transactions[1])
	}
}

func TestParseForm4OwnershipXMLFounderTitleRequiresConfirmation(t *testing.T) {
	xml := `<ownershipDocument>
  <documentType>4</documentType>
  <issuer><issuerCik>0000001234</issuerCik></issuer>
  <reportingOwner>
    <reportingOwnerId><rptOwnerName>Founder Person</rptOwnerName></reportingOwnerId>
    <reportingOwnerRelationship><isOfficer>1</isOfficer><officerTitle>Founder and Director</officerTitle></reportingOwnerRelationship>
  </reportingOwner>
  <nonDerivativeTable>
    <nonDerivativeTransaction>
      <transactionDate><value>2026-06-18</value></transactionDate>
      <transactionCoding><transactionCode>P</transactionCode></transactionCoding>
      <transactionAmounts>
        <transactionShares><value>100</value></transactionShares>
        <transactionPricePerShare><value>1</value></transactionPricePerShare>
        <transactionAcquiredDisposedCode><value>A</value></transactionAcquiredDisposedCode>
      </transactionAmounts>
    </nonDerivativeTransaction>
  </nonDerivativeTable>
</ownershipDocument>`

	transactions, err := ParseForm4OwnershipXML(strings.NewReader(xml), "0000001234-26-000003", "https://www.sec.gov/form4.xml")
	if err != nil {
		t.Fatal(err)
	}
	if transactions[0].Qualified || !transactions[0].FounderConfirmationSuggested || transactions[0].ExclusionReason != InsiderExclusionFounderNeedsConfirmation {
		t.Fatalf("founder row = %#v", transactions[0])
	}
}

func TestInsiderTransactionSnapshotUsesMicros(t *testing.T) {
	date := time.Date(2026, 6, 18, 0, 0, 0, 0, time.UTC)
	tx := InsiderTransaction{Accession: "0000001234-26-000001", OwnerName: "Jane", OfficerTitle: "CEO", Role: InsiderRoleCEO, TransactionDate: date, TransactionCode: "P", AcquiredDisposedCode: "A", Shares: 12.5, PricePerShareUSD: 2.25, ValueUSD: 28.125, SharesOwnedAfter: 100, SharesOwnedBefore: 87.5, Qualified: true, SourceURL: "https://www.sec.gov/form4.xml"}
	row := InsiderTransactionToSnapshot(7, tx, date)
	if row.SecurityID != 7 || row.SharesMicros != 12_500_000 || row.PriceMicros != 2_250_000 || row.ValueMicros != 28_125_000 || row.SharesOwnedBeforeMicros != 87_500_000 || row.ParserVersion != InsiderParserVersion {
		t.Fatalf("snapshot = %#v", row)
	}
}

func TestSECForm4InsiderSourceDownloadsRecentAllowedOwnershipXML(t *testing.T) {
	asOf := time.Date(2026, 6, 29, 0, 0, 0, 0, time.UTC)
	xmlBody := `<ownershipDocument>
  <documentType>4</documentType>
  <issuer><issuerCik>0000001234</issuerCik><issuerTradingSymbol>ACME</issuerTradingSymbol></issuer>
  <reportingOwner>
    <reportingOwnerId><rptOwnerName>Jane Buyer</rptOwnerName></reportingOwnerId>
    <reportingOwnerRelationship><isOfficer>1</isOfficer><officerTitle>CEO</officerTitle></reportingOwnerRelationship>
  </reportingOwner>
  <nonDerivativeTable>
    <nonDerivativeTransaction>
      <transactionDate><value>2026-06-18</value></transactionDate>
      <transactionCoding><transactionCode>P</transactionCode></transactionCoding>
      <transactionAmounts>
        <transactionShares><value>100</value></transactionShares>
        <transactionPricePerShare><value>3</value></transactionPricePerShare>
        <transactionAcquiredDisposedCode><value>A</value></transactionAcquiredDisposedCode>
      </transactionAmounts>
    </nonDerivativeTransaction>
  </nonDerivativeTable>
</ownershipDocument>`
	requested := []string{}
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		requested = append(requested, r.URL.String())
		if !strings.Contains(r.URL.String(), "/Archives/edgar/data/1234/000000123426000004/form4.xml") {
			t.Fatalf("unexpected URL %s", r.URL.String())
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(xmlBody)), Header: make(http.Header), Request: r}, nil
	})}
	source := SECForm4InsiderSource{
		Metadata: fakeMetadataSource{records: []SecuritySourceRecord{{CIK: "0000001234", FilingMetadata: []FilingMetadata{
			{CIK: "0000001234", Accession: "0000001234-26-000004", Form: "4", FiledAt: asOf.AddDate(0, 0, -10), PrimaryDocument: "form4.xml"},
			{CIK: "0000001234", Accession: "0000001234-26-000005", Form: "4", FiledAt: asOf.AddDate(0, 0, -9), PrimaryDocument: "../bad.xml"},
			{CIK: "0000001234", Accession: "0000001234-26-000003", Form: "4", FiledAt: asOf.AddDate(0, 0, -181), PrimaryDocument: "old.xml"},
			{CIK: "0000001234", Accession: "0000001234-26-000002", Form: "8-K", FiledAt: asOf.AddDate(0, 0, -2), PrimaryDocument: "8k.xml"},
			{CIK: "0000009999", Accession: "0000009999-26-000001", Form: "4", FiledAt: asOf.AddDate(0, 0, -2), PrimaryDocument: "other.xml"},
		}}}, version: testSourceVersion("metadata", "form4", asOf)},
		Downloader:   &Downloader{Client: client, CacheDir: t.TempDir(), MaxBytes: 1 << 20},
		BaseURL:      "https://www.sec.gov/Archives/edgar/data",
		LookbackDays: 180,
	}

	transactions, version, err := source.LoadInsiderTransactions(context.Background(), map[string]struct{}{"0000001234": {}}, asOf)
	if err != nil {
		t.Fatal(err)
	}
	if len(requested) != 1 {
		t.Fatalf("download count = %d, want 1", len(requested))
	}
	if len(transactions) != 1 || !transactions[0].Qualified || transactions[0].ValueUSD != 300 {
		t.Fatalf("transactions = %#v", transactions)
	}
	if version.Source != "insiders:sec-form4" || !strings.Contains(version.Version, InsiderParserVersion) || version.SHA256 == "" {
		t.Fatalf("version = %#v", version)
	}
}

func TestSECForm4InsiderSourcePrefersRawOwnershipXMLOverXSLWrapper(t *testing.T) {
	asOf := time.Date(2026, 7, 18, 0, 0, 0, 0, time.UTC)
	requested := []string{}
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		requested = append(requested, r.URL.Path)
		body := `<ownershipDocument><documentType>4</documentType><issuer><issuerCik>0000001234</issuerCik><issuerTradingSymbol>ACME</issuerTradingSymbol></issuer></ownershipDocument>`
		if strings.Contains(r.URL.Path, "/xslF345X05/") {
			body = `<html><head><title>SEC Form 4</title></head><body>rendered wrapper</body></html>`
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header), Request: r}, nil
	})}
	source := SECForm4InsiderSource{
		Metadata:   fakeMetadataSource{records: []SecuritySourceRecord{{CIK: "0000001234", FilingMetadata: []FilingMetadata{{CIK: "0000001234", Accession: "0000001234-26-000004", Form: "4", FiledAt: asOf.AddDate(0, 0, -1), PrimaryDocument: "xslF345X05/ownership.xml"}}}}, version: testSourceVersion("metadata", "form4-xsl", asOf)},
		Downloader: &Downloader{Client: client, CacheDir: t.TempDir(), MaxBytes: 1 << 20},
		BaseURL:    "https://www.sec.gov/Archives/edgar/data",
	}
	transactions, coverage, _, err := source.LoadInsiderTransactionsWithCoverage(context.Background(), map[string]struct{}{"0000001234": {}}, asOf)
	if err != nil {
		t.Fatal(err)
	}
	if len(transactions) != 0 || len(coverage) != 1 || coverage[0].Status != InsiderCoverageCoveredNoTransactions || coverage[0].DownloadedDocuments != 1 || coverage[0].ParsedDocuments != 1 {
		t.Fatalf("coverage = %#v transactions = %#v", coverage, transactions)
	}
	if len(requested) != 1 || !strings.HasSuffix(requested[0], "/ownership.xml") || strings.Contains(requested[0], "/xslF345X05/") {
		t.Fatalf("requested = %#v", requested)
	}
}

func TestSECForm4InsiderSourceSkipsMalformedOwnershipDocument(t *testing.T) {
	asOf := time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC)
	requested := []string{}
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		requested = append(requested, r.URL.String())
		body := `<ownershipDocument><documentType>4</documentType><issuer><issuerCik>0000001234</issuerCik><issuerTradingSymbol>ACME</issuerTradingSymbol></issuer></ownershipDocument>`
		if strings.Contains(r.URL.Path, "not-ownership.html") {
			body = `<html><head><meta charset="utf-8"></head><body>SEC archive wrapper</body></html>`
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header), Request: r}, nil
	})}
	source := SECForm4InsiderSource{
		Metadata: fakeMetadataSource{records: []SecuritySourceRecord{{CIK: "0000001234", FilingMetadata: []FilingMetadata{
			{CIK: "0000001234", Accession: "0000001234-26-000010", Form: "4", FiledAt: asOf.AddDate(0, 0, -2), PrimaryDocument: "form4.xml"},
			{CIK: "0000001234", Accession: "0000001234-26-000011", Form: "4", FiledAt: asOf.AddDate(0, 0, -1), PrimaryDocument: "xslF345X05/not-ownership.html"},
		}}}, version: testSourceVersion("metadata", "form4-skip", asOf)},
		Downloader:   &Downloader{Client: client, CacheDir: t.TempDir(), MaxBytes: 1 << 20},
		BaseURL:      "https://www.sec.gov/Archives/edgar/data",
		LookbackDays: 180,
	}

	transactions, version, err := source.LoadInsiderTransactions(context.Background(), map[string]struct{}{"0000001234": {}}, asOf)
	if err != nil {
		t.Fatalf("LoadInsiderTransactions: %v", err)
	}
	// The raw accession-root file is tried first and the XSL wrapper second;
	// this fixture deliberately makes both locations malformed.
	if len(requested) != 3 || len(transactions) != 0 {
		t.Fatalf("requested=%d transactions=%#v", len(requested), transactions)
	}
	if version.SHA256 == "" || !strings.Contains(version.Version, InsiderParserVersion) {
		t.Fatalf("version = %#v", version)
	}
}

func TestSECForm4InsiderSourceSkipsSingleTransientDownloadFailure(t *testing.T) {
	asOf := time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC)
	requested := []string{}
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		requested = append(requested, r.URL.String())
		if strings.Contains(r.URL.Path, "temporarily-unavailable.xml") {
			return nil, errors.New("connection reset by peer")
		}
		body := `<ownershipDocument><documentType>4</documentType><issuer><issuerCik>0000001234</issuerCik><issuerTradingSymbol>ACME</issuerTradingSymbol></issuer></ownershipDocument>`
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header), Request: r}, nil
	})}
	source := SECForm4InsiderSource{
		Metadata: fakeMetadataSource{records: []SecuritySourceRecord{{CIK: "0000001234", FilingMetadata: []FilingMetadata{
			{CIK: "0000001234", Accession: "0000001234-26-000020", Form: "4", FiledAt: asOf.AddDate(0, 0, -2), PrimaryDocument: "form4.xml"},
			{CIK: "0000001234", Accession: "0000001234-26-000021", Form: "4", FiledAt: asOf.AddDate(0, 0, -1), PrimaryDocument: "temporarily-unavailable.xml"},
		}}}, version: testSourceVersion("metadata", "form4-network", asOf)},
		Downloader:   &Downloader{Client: client, CacheDir: t.TempDir(), MaxBytes: 1 << 20},
		BaseURL:      "https://www.sec.gov/Archives/edgar/data",
		LookbackDays: 180,
	}

	transactions, version, err := source.LoadInsiderTransactions(context.Background(), map[string]struct{}{"0000001234": {}}, asOf)
	if err != nil {
		t.Fatalf("LoadInsiderTransactions: %v", err)
	}
	if len(requested) != 2 || len(transactions) != 0 || version.SHA256 == "" {
		t.Fatalf("requested=%d transactions=%#v version=%#v", len(requested), transactions, version)
	}
}

func TestSECForm4InsiderSourceBoundsOneStalledDocument(t *testing.T) {
	asOf := time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC)
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.URL.Path, "stalled.xml") {
			<-r.Context().Done()
			return nil, r.Context().Err()
		}
		body := `<ownershipDocument><documentType>4</documentType><issuer><issuerCik>0000001234</issuerCik><issuerTradingSymbol>ACME</issuerTradingSymbol></issuer></ownershipDocument>`
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header), Request: r}, nil
	})}
	source := SECForm4InsiderSource{
		Metadata: fakeMetadataSource{records: []SecuritySourceRecord{{CIK: "0000001234", FilingMetadata: []FilingMetadata{
			{CIK: "0000001234", Accession: "0000001234-26-000022", Form: "4", FiledAt: asOf.AddDate(0, 0, -2), PrimaryDocument: "stalled.xml"},
			{CIK: "0000001234", Accession: "0000001234-26-000023", Form: "4", FiledAt: asOf.AddDate(0, 0, -1), PrimaryDocument: "form4.xml"},
		}}}, version: testSourceVersion("metadata", "form4-timeout", asOf)},
		Downloader:      &Downloader{Client: client, CacheDir: t.TempDir(), MaxBytes: 1 << 20},
		BaseURL:         "https://www.sec.gov/Archives/edgar/data",
		LookbackDays:    180,
		DocumentTimeout: 20 * time.Millisecond,
	}
	_, coverage, _, err := source.LoadInsiderTransactionsWithCoverage(context.Background(), map[string]struct{}{"0000001234": {}}, asOf)
	if err != nil {
		t.Fatal(err)
	}
	if len(coverage) != 1 || coverage[0].TransientDocumentFailures != 1 || coverage[0].ParsedDocuments != 1 {
		t.Fatalf("coverage = %#v", coverage)
	}
}

func TestSECForm4InsiderSourceSkipsAllPermanentlyMissingDocuments(t *testing.T) {
	asOf := time.Date(2026, 7, 16, 0, 0, 0, 0, time.UTC)
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusNotFound, Body: io.NopCloser(strings.NewReader("missing")), Header: make(http.Header), Request: r}, nil
	})}
	source := SECForm4InsiderSource{
		Metadata: fakeMetadataSource{records: []SecuritySourceRecord{{CIK: "0000001234", FilingMetadata: []FilingMetadata{
			{CIK: "0000001234", Accession: "0000001234-26-000030", Form: "4", FiledAt: asOf.AddDate(0, 0, -2), PrimaryDocument: "removed.xml"},
		}}}, version: testSourceVersion("metadata", "form4-missing", asOf)},
		Downloader:   &Downloader{Client: client, CacheDir: t.TempDir(), MaxBytes: 1 << 20},
		BaseURL:      "https://www.sec.gov/Archives/edgar/data",
		LookbackDays: 180,
	}

	transactions, version, err := source.LoadInsiderTransactions(context.Background(), map[string]struct{}{"0000001234": {}}, asOf)
	if err != nil {
		t.Fatalf("LoadInsiderTransactions: %v", err)
	}
	if len(transactions) != 0 || version.SHA256 == "" || !strings.Contains(version.Version, InsiderParserVersion) {
		t.Fatalf("transactions=%#v version=%#v", transactions, version)
	}
}

func TestSECForm4InsiderSourceReportsCoverageWithoutTreatingNoFilingAsMissing(t *testing.T) {
	asOf := time.Date(2026, 7, 16, 0, 0, 0, 0, time.UTC)
	source := SECForm4InsiderSource{
		Metadata: fakeMetadataSource{records: []SecuritySourceRecord{
			{CIK: "0000001234"},
			{CIK: "0000005678", FilingMetadata: []FilingMetadata{{CIK: "0000005678", Accession: "0000005678-26-000001", Form: "4", FiledAt: asOf.AddDate(0, 0, -1), PrimaryDocument: "missing.xml"}}},
		}, version: testSourceVersion("metadata", "form4-coverage", asOf)},
		Downloader: &Downloader{Client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusNotFound, Body: io.NopCloser(strings.NewReader("missing")), Header: make(http.Header), Request: r}, nil
		})}, CacheDir: t.TempDir(), MaxBytes: 1 << 20},
		BaseURL: "https://www.sec.gov/Archives/edgar/data",
	}
	transactions, coverage, version, err := source.LoadInsiderTransactionsWithCoverage(context.Background(), map[string]struct{}{"0000001234": {}, "0000005678": {}, "0000009999": {}}, asOf)
	if err != nil {
		t.Fatal(err)
	}
	if len(transactions) != 0 || !strings.Contains(version.Version, InsiderCoverageVersion) {
		t.Fatalf("transactions=%#v version=%#v", transactions, version)
	}
	byCIK := map[string]InsiderCoverage{}
	for _, item := range coverage {
		byCIK[item.CIK] = item
	}
	if got := byCIK["0000001234"]; got.Status != InsiderCoverageCoveredNoFilings {
		t.Fatalf("no filing coverage=%#v", got)
	}
	if got := byCIK["0000005678"]; got.Status != InsiderCoverageUnavailable || got.PermanentDocumentFailures != 1 {
		t.Fatalf("permanent missing coverage=%#v", got)
	}
	if got := byCIK["0000009999"]; got.Status != InsiderCoverageUnavailable {
		t.Fatalf("missing metadata coverage=%#v", got)
	}
}

func TestSECForm4InsiderSourceResumesFromCompletedIssuerChunk(t *testing.T) {
	asOf := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)
	records := make([]SecuritySourceRecord, 0, form4CheckpointChunkSize+1)
	allowed := make(map[string]struct{}, form4CheckpointChunkSize+1)
	for i := 1; i <= form4CheckpointChunkSize+1; i++ {
		cik := fmt.Sprintf("%010d", i)
		records = append(records, SecuritySourceRecord{CIK: cik, FilingMetadata: []FilingMetadata{{
			CIK: cik, Accession: fmt.Sprintf("%010d-26-%06d", i, i), Form: "4",
			FiledAt: asOf.AddDate(0, 0, -1), PrimaryDocument: "ownership.xml",
		}}})
		allowed[cik] = struct{}{}
	}
	cacheDir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	firstCalls := 0
	firstClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		firstCalls++
		if firstCalls > form4CheckpointChunkSize {
			cancel()
			<-r.Context().Done()
			return nil, r.Context().Err()
		}
		cik, _ := strconv.Atoi(strings.Split(r.URL.Path, "/")[4])
		body := fmt.Sprintf(`<ownershipDocument><documentType>4</documentType><issuer><issuerCik>%010d</issuerCik><issuerTradingSymbol>T</issuerTradingSymbol></issuer></ownershipDocument>`, cik)
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header), Request: r}, nil
	})}
	metadataVersion := testSourceVersion("metadata", "form4-chunk-resume", asOf)
	first := SECForm4InsiderSource{Downloader: &Downloader{Client: firstClient, CacheDir: cacheDir, MaxBytes: 1 << 20}, LookbackDays: 180}
	if _, _, _, err := first.LoadInsiderTransactionsWithMetadata(ctx, records, metadataVersion, allowed, asOf); !errors.Is(err, context.Canceled) {
		t.Fatalf("first run error=%v want canceled context", err)
	}

	secondCalls := 0
	secondClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		secondCalls++
		cik, _ := strconv.Atoi(strings.Split(r.URL.Path, "/")[4])
		body := fmt.Sprintf(`<ownershipDocument><documentType>4</documentType><issuer><issuerCik>%010d</issuerCik><issuerTradingSymbol>T</issuerTradingSymbol></issuer></ownershipDocument>`, cik)
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header), Request: r}, nil
	})}
	second := SECForm4InsiderSource{Downloader: &Downloader{Client: secondClient, CacheDir: cacheDir, MaxBytes: 1 << 20}, LookbackDays: 180}
	_, coverage, _, err := second.LoadInsiderTransactionsWithMetadata(context.Background(), records, metadataVersion, allowed, asOf)
	if err != nil {
		t.Fatal(err)
	}
	if len(coverage) != form4CheckpointChunkSize+1 {
		t.Fatalf("coverage=%d want=%d", len(coverage), form4CheckpointChunkSize+1)
	}
	if secondCalls != 1 {
		t.Fatalf("retry HTTP calls=%d want=1; completed first chunk was not resumed", secondCalls)
	}
}

func TestForm4DocumentLocationAllowsSafeSECXSLSubdirectory(t *testing.T) {
	filing := FilingMetadata{
		CIK:             "0000001234",
		Accession:       "0000001234-26-000004",
		PrimaryDocument: "xslF345X05/ownership.xml",
	}
	url, cacheKey, err := form4DocumentLocation("https://www.sec.gov/Archives/edgar/data", filing)
	if err != nil {
		t.Fatalf("form4DocumentLocation() error = %v", err)
	}
	if want := "https://www.sec.gov/Archives/edgar/data/1234/000000123426000004/xslF345X05/ownership.xml"; url != want {
		t.Fatalf("url = %q, want %q", url, want)
	}
	if cacheKey != "form4-0000001234-000000123426000004-xslF345X05_ownership.xml" {
		t.Fatalf("cache key = %q", cacheKey)
	}
	filing.PrimaryDocument = "../ownership.xml"
	if _, _, err := form4DocumentLocation("https://www.sec.gov", filing); err == nil {
		t.Fatal("expected traversal path rejection")
	}
}

func TestForm4DocumentLocationUsesAccessionFilerCIK(t *testing.T) {
	filing := FilingMetadata{
		CIK:             "0001070423",
		Accession:       "0001581990-26-000045",
		PrimaryDocument: "form4.xml",
	}
	url, cacheKey, err := form4DocumentLocation("https://www.sec.gov/Archives/edgar/data", filing)
	if err != nil {
		t.Fatalf("form4DocumentLocation() error = %v", err)
	}
	if want := "https://www.sec.gov/Archives/edgar/data/1581990/000158199026000045/form4.xml"; url != want {
		t.Fatalf("url = %q, want %q", url, want)
	}
	if want := "form4-0001581990-000158199026000045-form4.xml"; cacheKey != want {
		t.Fatalf("cache key = %q, want %q", cacheKey, want)
	}
}
