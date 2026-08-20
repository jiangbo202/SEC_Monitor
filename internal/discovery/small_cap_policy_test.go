package discovery

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestSmallCapPolicyDefaultSeedAndCanonicalHash(t *testing.T) {
	db := openMigratedTestDatabase(t)
	active, err := GetActiveSmallCapPolicy(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	if active.Version != 1 || active.Status != SmallCapPolicyStatusFinalized {
		t.Fatalf("active policy = %#v", active)
	}
	wantHash, err := PolicyHash(DefaultSmallCapPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if active.ContentSHA256 != wantHash || active.Policy.MarketCapMaxUSD != MaximumSmallCapUSD {
		t.Fatalf("active policy hash/config = %s %#v", active.ContentSHA256, active.Policy)
	}
	reordered := DefaultSmallCapPolicy()
	reordered.AllowedExchanges = []string{"NYSE American", "Nasdaq", "NYSE"}
	reorderedHash, err := PolicyHash(reordered)
	if err != nil {
		t.Fatal(err)
	}
	if reorderedHash != wantHash {
		t.Fatalf("canonical exchange ordering changed hash: %s != %s", reorderedHash, wantHash)
	}
}

func TestSmallCapPolicyLifecycleFinalizesAndRollbackCreatesNewVersion(t *testing.T) {
	db := openMigratedTestDatabase(t)
	ctx := context.Background()
	base, err := GetActiveSmallCapPolicy(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	custom := base.Policy
	custom.MarketCapMaxUSD = 800_000_000
	draft, err := CreateSmallCapPolicyDraft(ctx, db, SmallCapPolicyDraftInput{Name: "smaller range", CreatedBy: "test", Policy: custom})
	if err != nil {
		t.Fatal(err)
	}
	if draft.Version != 2 || draft.Status != SmallCapPolicyStatusDraft {
		t.Fatalf("draft = %#v", draft)
	}
	active, err := ActivateSmallCapPolicy(ctx, db, draft.ID, "test")
	if err != nil {
		t.Fatal(err)
	}
	if active.Status != SmallCapPolicyStatusFinalized || active.FinalizedAt == nil {
		t.Fatalf("activated = %#v", active)
	}
	if _, err := UpdateSmallCapPolicyDraft(ctx, db, active.ID, SmallCapPolicyDraftInput{Name: "illegal", Policy: custom}); err == nil || !strings.Contains(err.Error(), "not an editable draft") {
		t.Fatalf("finalized policy update error = %v", err)
	}
	rollback, err := RollbackSmallCapPolicy(ctx, db, base.ID, "test")
	if err != nil {
		t.Fatal(err)
	}
	if rollback.ID == base.ID || rollback.Version != 3 || rollback.Status != SmallCapPolicyStatusFinalized || rollback.ContentSHA256 != base.ContentSHA256 {
		t.Fatalf("rollback = %#v, base = %#v", rollback, base)
	}
	latest, err := GetActiveSmallCapPolicy(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if latest.ID != rollback.ID {
		t.Fatalf("active after rollback = %d, want %d", latest.ID, rollback.ID)
	}
}

func TestValidateSmallCapPolicyCrossFieldRules(t *testing.T) {
	policy := DefaultSmallCapPolicy()
	policy.AMarketCapMaxExclusiveUSD = policy.MarketCapMaxUSD
	if err := ValidateSmallCapPolicy(policy); err == nil || !strings.Contains(err.Error(), "strictly between") {
		t.Fatalf("A/max validation error = %v", err)
	}
	policy = DefaultSmallCapPolicy()
	policy.TradableADVUSD = policy.BlockedADVUSD - 1
	if err := ValidateSmallCapPolicy(policy); err == nil || !strings.Contains(err.Error(), "tradable_adv_usd") {
		t.Fatalf("ADV validation error = %v", err)
	}
}

func TestPolicyDrivenMarketCapAndScoringUseExclusiveUpperBound(t *testing.T) {
	policy := DefaultSmallCapPolicy()
	policy.MarketCapMinUSD = 50_000_000
	policy.MarketCapMaxUSD = 750_000_000
	policy.AMarketCapMaxExclusiveUSD = 300_000_000
	marketCap, qualified, err := ComputeSmallCapQualificationWithPolicy(750_000_000_000_000, 1, policy)
	if err != nil {
		t.Fatal(err)
	}
	if marketCap != policy.MarketCapMaxUSD || qualified {
		t.Fatalf("exclusive upper qualification = (%d,%t)", marketCap, qualified)
	}
	input := DiscoveryScoreInput{MarketCapUSD: policy.MarketCapMaxUSD, Financial: FinancialMetricSnapshot{RevenueGrowthAvailable: true, QuarterlyRevenueYoYPct: 100}, SectorScore: 10}
	if score := ScoreDiscoveryCandidateWithPolicy(input, policy); score.EligibleB {
		t.Fatalf("exact upper bound unexpectedly eligible: %#v", score)
	}
}

func TestCoordinatorDraftFreezesActivePolicyBinding(t *testing.T) {
	db := openMigratedTestDatabase(t)
	ctx := context.Background()
	coordinator := &Coordinator{DB: db, Clock: time.Now}
	binding, err := ActiveSmallCapPolicyBinding(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	versions := []SourceVersion{{Source: "test", Version: strings.Repeat("a", 64), SHA256: strings.Repeat("a", 64)}}
	// createDraft validates normalized versions elsewhere in production; this
	// focused test only checks persisted policy lineage.
	batch, _, err := coordinator.createDraft(ctx, BatchKindPrescreen, "2026-08-20", versions, strings.Repeat("b", 64), coordinator.Clock())
	if err != nil {
		t.Fatal(err)
	}
	if batch.PolicyVersionID != binding.PolicyVersionID || batch.PolicyContentSHA256 != binding.PolicyContentSHA256 || batch.PolicySnapshotJSON == "" {
		t.Fatalf("batch policy binding = %#v, want %#v", batch, binding)
	}
}
