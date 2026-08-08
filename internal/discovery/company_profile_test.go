package discovery

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestGetCompanyProfileUsesLatestSECIdentityMetadata(t *testing.T) {
	db := openMigratedTestDatabase(t)
	security := Security{CIK: "0000012345", CompanyName: "Fallback Name", SIC: 7372, CatalogStatus: SecurityCatalogPublished}
	if err := db.Create(&security).Error; err != nil {
		t.Fatal(err)
	}
	old := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	latest := old.AddDate(0, 0, 1)
	if err := db.Create(&[]UniverseBatch{
		{BatchID: "old", Kind: BatchKindSecurity, Status: BatchStatusPublished, StartedAt: old, CompletedAt: &old},
		{BatchID: "new", Kind: BatchKindSecurity, Status: BatchStatusPublished, StartedAt: latest, CompletedAt: &latest},
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&[]SecurityBatchIdentity{
		{BatchID: "old", SecurityID: security.ID, CIK: security.CIK, Ticker: "PROF", CompanyName: "Old Name", SIC: 7372, SICDescription: "SERVICES-PREPACKAGED SOFTWARE", CreatedAt: old},
		{BatchID: "new", SecurityID: security.ID, CIK: security.CIK, Ticker: "PROF", CompanyName: "Profile Co", Exchange: "Nasdaq", SIC: 7372, SICDescription: "SERVICES-PREPACKAGED SOFTWARE", StateOfIncorporation: "DE", LatestAnnualForm: "10-K", CreatedAt: latest},
	}).Error; err != nil {
		t.Fatal(err)
	}

	profile, err := GetCompanyProfile(context.Background(), db, "prof", security.CIK)
	if err != nil {
		t.Fatal(err)
	}
	if profile.CompanyName != "Profile Co" || profile.Exchange != "Nasdaq" || profile.SectorCategory != "软件与数据服务" || profile.Status != "available" {
		t.Fatalf("profile = %#v", profile)
	}
	if !strings.Contains(profile.BusinessSummary, "SERVICES-PREPACKAGED SOFTWARE") || profile.SummarySource != "SEC submissions / sicDescription" {
		t.Fatalf("summary = %#v", profile)
	}
}

func TestGetCompanyProfileFallsBackToSICSector(t *testing.T) {
	db := openMigratedTestDatabase(t)
	if err := db.Create(&Security{CIK: "0000099999", CompanyName: "Fallback Co", SIC: 2834, CatalogStatus: SecurityCatalogPublished}).Error; err != nil {
		t.Fatal(err)
	}
	profile, err := GetCompanyProfile(context.Background(), db, "FBCK", "0000099999")
	if err != nil {
		t.Fatal(err)
	}
	if profile.Status != "partial" || profile.SectorCategory != "生物医药" || !strings.Contains(profile.BusinessSummary, "生物医药") {
		t.Fatalf("profile = %#v", profile)
	}
}
