package discovery

import (
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
