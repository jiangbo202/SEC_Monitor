package discovery

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestPersistRecentSECFilingSnapshotsKeepsLatestTwenty(t *testing.T) {
	db := openMigratedTestDatabase(t)
	security := Security{CIK: "0000012345", CompanyName: "Filing Co", CatalogStatus: SecurityCatalogPublished}
	if err := db.Create(&security).Error; err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	filings := make([]FilingMetadata, 0, recentSECFilingLimit+1)
	for index := 0; index <= recentSECFilingLimit; index++ {
		filings = append(filings, FilingMetadata{
			CIK: "0000012345", Accession: fmt.Sprintf("0000012345-26-%06d", index), Form: "8-K",
			FiledAt: start.AddDate(0, 0, index), PrimaryDocument: fmt.Sprintf("filing-%d.htm", index),
		})
	}
	coordinator := Coordinator{DB: db}
	if err := coordinator.persistRecentSECFilingSnapshots(context.Background(), map[string]uint{security.CIK: security.ID}, []metadataGroup{{Primary: SecuritySourceRecord{CIK: security.CIK, FilingMetadata: filings}}}, start); err != nil {
		t.Fatal(err)
	}
	if err := coordinator.persistRecentSECFilingSnapshots(context.Background(), map[string]uint{security.CIK: security.ID}, []metadataGroup{{Primary: SecuritySourceRecord{CIK: security.CIK, FilingMetadata: filings}}}, start); err != nil {
		t.Fatal(err)
	}
	var rows []SECFilingSnapshot
	if err := db.Where("security_id = ?", security.ID).Order("filing_date DESC").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != recentSECFilingLimit || rows[0].PrimaryDocument != "filing-20.htm" || rows[0].FilingURL != "https://www.sec.gov/Archives/edgar/data/12345/000001234526000020/filing-20.htm" {
		t.Fatalf("filing snapshots = %#v", rows)
	}
}
