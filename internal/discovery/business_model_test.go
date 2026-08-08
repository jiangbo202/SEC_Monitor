package discovery

import (
	"context"
	"testing"
	"time"
)

func TestCandidateBusinessModelEvidenceCapsUnconfirmedBiotech(t *testing.T) {
	clinical := candidateBusinessModelEvidence(&CandidateBusinessModelOverride{BusinessModel: CandidateBusinessModelClinicalPreRevenue}, true)
	if clinical.RevenueScoreCap != 10 || clinical.RequiresReview || clinical.Model != CandidateBusinessModelClinicalPreRevenue {
		t.Fatalf("clinical evidence = %#v", clinical)
	}
	mixed := candidateBusinessModelEvidence(&CandidateBusinessModelOverride{BusinessModel: CandidateBusinessModelMixedOrLicensing}, true)
	if mixed.RevenueScoreCap != 10 || !mixed.RequiresReview {
		t.Fatalf("mixed evidence = %#v", mixed)
	}
	commercial := candidateBusinessModelEvidence(&CandidateBusinessModelOverride{BusinessModel: CandidateBusinessModelCommercial}, true)
	if commercial.RevenueScoreCap != 30 || commercial.RequiresReview {
		t.Fatalf("commercial evidence = %#v", commercial)
	}
	unknown := candidateBusinessModelEvidence(nil, true)
	if unknown.Model != CandidateBusinessModelUnknown || unknown.RevenueScoreCap != 10 || !unknown.RequiresReview {
		t.Fatalf("unknown evidence = %#v", unknown)
	}
}

func TestUpsertCandidateBusinessModelKeepsConfirmationHistory(t *testing.T) {
	db := openMigratedTestDatabase(t)
	now := time.Date(2026, 7, 17, 0, 0, 0, 0, time.UTC)
	security := Security{CIK: "0000099010", CompanyName: "Biotech", CatalogStatus: SecurityCatalogPublished}
	mustCreate(t, db, &security)
	securityBatch := UniverseBatch{BatchID: "business-security", Kind: BatchKindSecurity, Status: BatchStatusPublished, StartedAt: now}
	marketBatch := UniverseBatch{BatchID: "business-market", Kind: BatchKindPrescreen, Status: BatchStatusPublished, UniverseSourceVersion: securityBatch.BatchID, StartedAt: now}
	mustCreate(t, db, &securityBatch)
	mustCreate(t, db, &marketBatch)
	mustCreate(t, db, &CurrentBatchPointer{Kind: BatchKindPrescreen, BatchID: marketBatch.BatchID, UpdatedAt: now})
	mustCreate(t, db, &SecurityBatchIdentity{BatchID: securityBatch.BatchID, SecurityID: security.ID, CIK: security.CIK, Ticker: "BIOM", SIC: 2834, MappingStatus: MappingStatusCurrent, CreatedAt: now})
	mustCreate(t, db, &CandidateScoreSnapshot{BatchID: marketBatch.BatchID, SecurityID: security.ID, Ticker: "BIOM", Grade: CandidateGradeB, EligibleB: true})

	first, err := UpsertCandidateBusinessModel(context.Background(), db, CandidateBusinessModelInput{Ticker: "biom", BusinessModel: CandidateBusinessModelMixedOrLicensing, Reason: "license revenue", Operator: "analyst"})
	if err != nil || first.RevenueScoreCap != 10 || !first.RequiresReview {
		t.Fatalf("first = %#v err=%v", first, err)
	}
	second, err := UpsertCandidateBusinessModel(context.Background(), db, CandidateBusinessModelInput{Ticker: "BIOM", BusinessModel: CandidateBusinessModelCommercial, RevenueRepeatableConfirmed: true, Reason: "product revenue", Operator: "analyst"})
	if err != nil || second.Model != CandidateBusinessModelCommercial || second.RevenueScoreCap != 30 {
		t.Fatalf("second = %#v err=%v", second, err)
	}
	var rows []CandidateBusinessModelOverride
	if err := db.Order("id ASC").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].Active || !rows[1].Active {
		t.Fatalf("rows = %#v", rows)
	}
}
