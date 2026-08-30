package service

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"sec_monitor/internal/discovery"
	"sec_monitor/internal/model"
)

func thesisTestService(t *testing.T) *ResearchThesisService {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "main.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err = db.AutoMigrate(&model.ResearchThesis{}, &model.ResearchThesisRevision{}, &model.OperationLog{}, &model.Filing{}, &model.AIAnalysis{}, &model.InAppNotification{}); err != nil {
		t.Fatal(err)
	}
	research, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "research.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err = research.AutoMigrate(&discovery.Security{}, &discovery.Listing{}, &discovery.SecurityBatchIdentity{}, &discovery.InsiderTransactionSnapshot{}); err != nil {
		t.Fatal(err)
	}
	for _, d := range []*gorm.DB{db, research} {
		sqlDB, _ := d.DB()
		t.Cleanup(func() { _ = sqlDB.Close() })
	}
	return NewResearchThesisService(db, research)
}
func thesisActiveInput(t *testing.T, s *ResearchThesisService) ThesisWrite {
	t.Helper()
	row := model.Filing{Ticker: "TEST", FilingID: "test-1", FilingType: "8-K", Title: "订单增长", FilingURL: "https://www.sec.gov/test"}
	if err := s.db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	next := time.Now().Add(24 * time.Hour)
	return ThesisWrite{Status: "active", Rationale: "订单改善", Invalidation: "收入下降", NextCheck: "核对下季收入", NextReviewAt: &next, ReviewNote: "原文支持初始判断", Evidence: []model.ThesisEvidence{{Kind: "filing", ID: row.ID, Summary: "伪造摘要"}}}
}
func TestResearchThesisLifecycleAndImmutableEvidence(t *testing.T) {
	s := thesisTestService(t)
	ctx := context.Background()
	in := thesisActiveInput(t, s)
	first, err := s.Save(ctx, " test ", in)
	if err != nil {
		t.Fatal(err)
	}
	if first.Version != 1 || first.ReviewedAt == nil || first.Evidence[0].Summary != "订单增长" || len(first.Evidence[0].SHA256) != 64 {
		t.Fatalf("bad snapshot: %+v", first)
	}
	if _, err = s.Save(ctx, "TEST", in); !errors.Is(err, ErrThesisConflict) {
		t.Fatalf("expected conflict, got %v", err)
	}
	// Source deletion does not destroy evidence retained by a previous revision.
	s.db.Delete(&model.Filing{}, first.Evidence[0].ID)
	in.Version = 1
	in.Status = "invalidated"
	in.ReviewNote = "新收入证据推翻假设"
	second, err := s.Save(ctx, "TEST", in)
	if err != nil {
		t.Fatal(err)
	}
	if second.NextReviewAt != nil || second.Evidence[0].SHA256 != first.Evidence[0].SHA256 {
		t.Fatal("closed snapshot/evidence not preserved")
	}
	in.Version = 2
	in.Status = "active"
	if _, err = s.Save(ctx, "TEST", in); !errors.Is(err, ErrValidation) {
		t.Fatalf("closed thesis reopened silently: %v", err)
	}
	in.Status = "draft"
	draft, err := s.Save(ctx, "TEST", in)
	if err != nil {
		t.Fatal(err)
	}
	in.Version = draft.Version
	in.Status = "active"
	if _, err = s.Save(ctx, "TEST", in); err != nil {
		t.Fatal(err)
	}
	detail, err := s.Get(ctx, "TEST")
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Revisions) != 4 || detail.Revisions[3].Snapshot.Status != "active" || detail.Revisions[2].Snapshot.Status != "invalidated" {
		t.Fatalf("bad history %+v", detail)
	}
	var audits int64
	s.db.Model(&model.OperationLog{}).Count(&audits)
	if audits != 4 {
		t.Fatalf("audits %d", audits)
	}
}
func TestResearchThesisValidation(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*ThesisWrite)
	}{
		{"missing evidence", func(in *ThesisWrite) { in.Evidence = nil }},
		{"missing invalidation", func(in *ThesisWrite) { in.Invalidation = " " }},
		{"missing review note", func(in *ThesisWrite) { in.ReviewNote = "" }},
		{"past review", func(in *ThesisWrite) { past := time.Now().Add(-time.Hour); in.NextReviewAt = &past }},
		{"invalid status", func(in *ThesisWrite) { in.Status = "buy" }},
		{"wrong symbol evidence", func(in *ThesisWrite) { in.Evidence = []model.ThesisEvidence{{Kind: "filing", ID: 999}} }},
		{"unknown evidence kind", func(in *ThesisWrite) { in.Evidence[0].Kind = "remote" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			s := thesisTestService(t)
			in := thesisActiveInput(t, s)
			test.change(&in)
			if _, err := s.Save(context.Background(), "TEST", in); !errors.Is(err, ErrValidation) {
				t.Fatalf("wanted validation error, got %v", err)
			}
		})
	}
	s := thesisTestService(t)
	if _, err := s.Get(context.Background(), "../bad"); !errors.Is(err, ErrValidation) {
		t.Fatal(err)
	}
}
func TestResearchThesisAtomicAuditAndCrossTickerEvidence(t *testing.T) {
	s := thesisTestService(t)
	in := thesisActiveInput(t, s)
	ctx := context.Background()
	if _, err := s.Save(ctx, "OTHER", in); !errors.Is(err, ErrValidation) {
		t.Fatalf("cross ticker evidence accepted: %v", err)
	}
	s.db.Migrator().DropTable(&model.OperationLog{})
	if _, err := s.Save(ctx, "TEST", in); err == nil {
		t.Fatal("audit failure not returned")
	}
	var count int64
	s.db.Model(&model.ResearchThesis{}).Count(&count)
	if count != 0 {
		t.Fatal("thesis persisted despite failed audit")
	}
	s.db.Model(&model.ResearchThesisRevision{}).Count(&count)
	if count != 0 {
		t.Fatal("revision persisted despite failed audit")
	}
}
func TestResearchThesisDueAndSourceCoverage(t *testing.T) {
	s := thesisTestService(t)
	ctx := context.Background()
	in := thesisActiveInput(t, s)
	first, err := s.Save(ctx, "TEST", in)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 25; i++ {
		if err = s.db.Create(&model.InAppNotification{Ticker: "TEST", EventKey: time.Now().String(), Title: "新证据", Link: "javascript:alert(1)", CreatedAt: first.ReviewedAt.Add(time.Minute)}).Error; err != nil {
			t.Fatal(err)
		}
	}
	sources, err := s.Sources(ctx, "TEST")
	if err != nil {
		t.Fatal(err)
	}
	if sources.NewCount != 25 || len(sources.Items) != 21 || len(sources.Unavailable) != 0 {
		t.Fatalf("bad sources %+v", sources)
	}
	for _, item := range sources.Items {
		if item.Kind == "notification" && item.URL != "" {
			t.Fatal("unsafe URL")
		}
	}
	past := time.Now().Add(-time.Hour)
	s.db.Model(&model.ResearchThesis{}).Where("ticker = ?", "TEST").Update("next_review_at", past)
	queue, err := s.Due(ctx)
	if err != nil || len(queue) != 1 {
		t.Fatalf("due %v %v", queue, err)
	}
	detail, _ := s.Get(ctx, "TEST")
	if !detail.ReviewDue {
		t.Fatal("overdue not indicated")
	}
	s.research.Migrator().DropTable(&discovery.InsiderTransactionSnapshot{})
	sources, err = s.Sources(ctx, "TEST")
	if err != nil || len(sources.Unavailable) != 1 {
		t.Fatalf("partial source failure hidden: %+v %v", sources, err)
	}
}
func TestResearchThesisInsiderAndAIEvidence(t *testing.T) {
	s := thesisTestService(t)
	ctx := context.Background()
	security := discovery.Security{CIK: "0000000001"}
	s.research.Create(&security)
	s.research.Create(&discovery.Listing{SecurityID: security.ID, Ticker: "TEST"})
	insider := discovery.InsiderTransactionSnapshot{SecurityID: security.ID, OwnerName: "Director", TransactionCode: "S", SharesMicros: 1000000}
	s.research.Create(&insider)
	ai := model.AIAnalysis{Ticker: "TEST", Status: "success", ResultJSON: `{"conclusion":"需要复核"}`}
	s.db.Create(&ai)
	in := ThesisWrite{Status: "draft", Evidence: []model.ThesisEvidence{{Kind: "insider", ID: insider.ID}, {Kind: "ai", ID: ai.ID}}}
	saved, err := s.Save(ctx, "TEST", in)
	if err != nil || len(saved.Evidence) != 2 {
		t.Fatalf("%+v %v", saved, err)
	}
	if saved.Evidence[1].Summary != "需要复核" {
		t.Fatal("AI snapshot missing")
	}
	if _, err = s.Save(ctx, "OTHER", in); !errors.Is(err, ErrValidation) {
		t.Fatal("insider cross ticker accepted")
	}
}

func TestResearchThesisBatchIdentityFallbackDoesNotReuseOldTicker(t *testing.T) {
	s := thesisTestService(t)
	ctx := context.Background()
	security := discovery.Security{CIK: "0000000002"}
	s.research.Create(&security)
	s.research.Create(&discovery.SecurityBatchIdentity{SecurityID: security.ID, BatchID: "old", Ticker: "OLD", CreatedAt: time.Now().UTC().Add(-time.Hour)})
	s.research.Create(&discovery.SecurityBatchIdentity{SecurityID: security.ID, BatchID: "new", Ticker: "TEST", CreatedAt: time.Now().UTC()})
	row := discovery.InsiderTransactionSnapshot{SecurityID: security.ID, OwnerName: "Owner"}
	s.research.Create(&row)
	if _, err := s.resolveEvidence(ctx, s.db, "TEST", "insider", row.ID); err != nil {
		t.Fatalf("latest batch missing: %v", err)
	}
	if _, err := s.resolveEvidence(ctx, s.db, "OLD", "insider", row.ID); !errors.Is(err, ErrValidation) {
		t.Fatalf("old ticker accepted: %v", err)
	}
	s.research.Create(&discovery.Listing{SecurityID: security.ID, Ticker: "NEW"})
	if _, err := s.resolveEvidence(ctx, s.db, "TEST", "insider", row.ID); !errors.Is(err, ErrValidation) {
		t.Fatalf("stale batch overrides current listing: %v", err)
	}
	if _, err := s.resolveEvidence(ctx, s.db, "NEW", "insider", row.ID); err != nil {
		t.Fatal(err)
	}
}
