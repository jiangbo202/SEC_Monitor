package service

import (
	"context"
	"sec_monitor/internal/model"
	"strings"
	"testing"
	"time"
)

func TestAIAnalysisSummaryAndLazyPromptPreserveOriginal(t *testing.T) {
	db := testDB(t)
	if err := db.AutoMigrate(&model.AIAnalysis{}); err != nil {
		t.Fatal(err)
	}
	row := model.AIAnalysis{Ticker: "TEST", Status: "success", RequestedAt: time.Now(), UserPrompt: strings.Repeat("original research evidence", 1000), SystemPrompt: "original instruction", InputSnapshot: "original snapshot", Content: "original result"}
	if err := db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	s := NewAIAnalysisService(db, nil, nil)
	page, err := s.List(context.Background(), AIAnalysisListFilter{Ticker: "TEST", SummaryOnly: true})
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("page=%+v err=%v", page, err)
	}
	if item := page.Items[0]; item.UserPrompt != "" || item.Content != "" || item.InputSnapshot != "" || item.StructuredResult != nil {
		t.Fatalf("summary leaked large evidence: %+v", item)
	}
	detail, err := s.Get(context.Background(), row.ID, false)
	if err != nil || detail.Content != row.Content || detail.UserPrompt != "" {
		t.Fatalf("detail=%+v err=%v", detail, err)
	}
	prompts, err := s.Get(context.Background(), row.ID, true)
	if err != nil || prompts.UserPrompt != row.UserPrompt || prompts.SystemPrompt != row.SystemPrompt {
		t.Fatal("original prompts unavailable", err)
	}
	var original model.AIAnalysis
	if err := db.First(&original, row.ID).Error; err != nil {
		t.Fatal(err)
	}
	if original.InputSnapshot != row.InputSnapshot || original.UserPrompt != row.UserPrompt {
		t.Fatal("read changed original record")
	}
}
