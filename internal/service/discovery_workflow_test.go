package service

import (
	"context"
	"testing"
	"time"

	"sec_monitor/internal/discovery"
)

func TestDiscoveryWorkflowRefreshReturnsCurrentBatchStatus(t *testing.T) {
	discoveryDB := testDiscoveryDB(t)
	security := discovery.Security{CIK: "0000014001", CompanyName: "Workflow Co", CatalogStatus: discovery.SecurityCatalogPublished}
	if err := discoveryDB.Create(&security).Error; err != nil {
		t.Fatal(err)
	}
	batch := discovery.UniverseBatch{BatchID: "workflow-current", Kind: discovery.BatchKindPrescreen, Status: discovery.BatchStatusPublished, StartedAt: time.Now()}
	if err := discoveryDB.Create(&batch).Error; err != nil {
		t.Fatal(err)
	}
	if err := discoveryDB.Create(&discovery.CurrentBatchPointer{Kind: discovery.BatchKindPrescreen, BatchID: batch.BatchID}).Error; err != nil {
		t.Fatal(err)
	}
	if err := discoveryDB.Create(&discovery.CandidateScoreSnapshot{BatchID: batch.BatchID, SecurityID: security.ID, Ticker: "FLOW", Grade: discovery.CandidateGradeA, EligibleA: true, TotalScore: 89, MarketCapUSD: 190_000_000}).Error; err != nil {
		t.Fatal(err)
	}

	result, err := NewDiscoveryWorkflowService(discoveryDB).Refresh(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != DiscoveryWorkflowReady || result.BatchID != batch.BatchID || result.Summary.TotalA != 1 {
		t.Fatalf("result = %#v", result)
	}
}
