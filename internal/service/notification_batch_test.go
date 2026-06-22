package service

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"sec_monitor/internal/model"
)

func TestNotificationBatchDeliverTableDriven(t *testing.T) {
	now := time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name            string
		candidates      []NotificationCandidate
		notifierErrors  []error
		wantStatus      string
		wantSent        int
		wantSuppressed  int
		wantFailed      int
		wantCalls       int
		wantNotified    bool
		telegramEnabled *bool
	}{
		{
			name: "sends one summary and marks eligible filings",
			candidates: []NotificationCandidate{
				{EntityKind: "filing", FilingID: "filing-1", Ticker: "TSLA", CompanyName: "Tesla", FilingType: "8-K", Title: "Event", FilingURL: "https://sec.test/1", EventAt: now, Reason: "eligible"},
				{EntityKind: "ipo_filing", FilingID: "ipo-1", CIK: "0001", CompanyName: "Acme", FilingType: "S-1/A", Title: "Amendment", FilingURL: "https://sec.test/2", EventAt: now.Add(-time.Minute), Reason: "eligible"},
				{EntityKind: "filing", FilingID: "filing-2", Ticker: "TSLA", CompanyName: "Tesla", FilingType: "10-K", EventAt: now.Add(-time.Hour), Reason: "history_backfill"},
			},
			wantStatus: "sent", wantSent: 2, wantSuppressed: 1, wantCalls: 1, wantNotified: true,
		},
		{
			name: "persists suppressed batch without sending",
			candidates: []NotificationCandidate{
				{EntityKind: "filing", FilingID: "filing-2", Ticker: "TSLA", CompanyName: "Tesla", FilingType: "10-K", EventAt: now, Reason: "initial_sync"},
			},
			wantStatus: "suppressed", wantSuppressed: 1,
		},
		{
			name: "records failed delivery without notification timestamp",
			candidates: []NotificationCandidate{
				{EntityKind: "filing", FilingID: "filing-1", Ticker: "TSLA", CompanyName: "Tesla", FilingType: "8-K", EventAt: now, Reason: "eligible"},
			},
			notifierErrors: []error{context.DeadlineExceeded, context.DeadlineExceeded, context.DeadlineExceeded},
			wantStatus:     "failed", wantFailed: 1, wantCalls: 3,
		},
		{
			name: "suppresses eligible items when telegram is disabled",
			candidates: []NotificationCandidate{
				{EntityKind: "filing", FilingID: "filing-1", Ticker: "TSLA", CompanyName: "Tesla", FilingType: "8-K", EventAt: now, Reason: "eligible"},
			},
			wantStatus: "suppressed", wantSuppressed: 1,
			telegramEnabled: boolPointer(false),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := testDB(t)
			seed := []model.Filing{{FilingID: "filing-1", Ticker: "TSLA", CompanyName: "Tesla", FilingType: "8-K", FilingDate: now, FilingURL: "https://sec.test/1", PulledAt: now}, {FilingID: "filing-2", Ticker: "TSLA", CompanyName: "Tesla", FilingType: "10-K", FilingDate: now, FilingURL: "https://sec.test/3", PulledAt: now}}
			if err := db.Create(&seed).Error; err != nil {
				t.Fatalf("seed filings: %v", err)
			}
			if err := db.Create(&model.IPOFiling{FilingID: "ipo-1", CIK: "0001", CompanyName: "Acme", FilingType: "S-1/A", FilingDate: now, FilingURL: "https://sec.test/2"}).Error; err != nil {
				t.Fatalf("seed ipo filing: %v", err)
			}
			enabled := true
			if tt.telegramEnabled != nil {
				enabled = *tt.telegramEnabled
			}
			if err := db.Create(&[]model.SystemConfig{
				{ConfigKey: "telegram.enabled", ConfigValue: fmt.Sprintf("%t", enabled), ValueType: "bool", Category: "telegram"},
				{ConfigKey: "telegram.bot_token", ConfigValue: "token", ValueType: "string", Category: "telegram"},
				{ConfigKey: "telegram.chat_id", ConfigValue: "chat", ValueType: "string", Category: "telegram"},
			}).Error; err != nil {
				t.Fatalf("seed telegram config: %v", err)
			}
			notifier := &fakeNotifier{errs: tt.notifierErrors}
			configs := NewConfigService(db, NewAuditService(db))
			svc := NewNotificationBatchService(db, notifier, configs)
			batch, err := svc.Deliver(context.Background(), NotificationBatchInput{SyncRunID: 7, Source: "filing", Trigger: "scheduler", Candidates: tt.candidates})
			if err != nil {
				t.Fatalf("Deliver: %v", err)
			}
			if batch.Status != tt.wantStatus || batch.SentCount != tt.wantSent || batch.SuppressedCount != tt.wantSuppressed || batch.FailedCount != tt.wantFailed {
				t.Fatalf("batch = %+v", batch)
			}
			if notifier.calls != tt.wantCalls {
				t.Fatalf("notifier calls = %d, want %d", notifier.calls, tt.wantCalls)
			}
			var filing model.Filing
			if err := db.Where("filing_id = ?", "filing-1").First(&filing).Error; err != nil {
				t.Fatalf("load filing: %v", err)
			}
			if (filing.NotifiedAt != nil) != tt.wantNotified {
				t.Fatalf("filing notified_at = %v, want notified=%v", filing.NotifiedAt, tt.wantNotified)
			}
			if tt.wantCalls == 1 && (len(notifier.messages) != 1 || !strings.Contains(notifier.messages[0].Text, "TSLA")) {
				t.Fatalf("messages = %+v", notifier.messages)
			}
		})
	}
}

func TestNotificationBatchListTableDriven(t *testing.T) {
	db := testDB(t)
	now := time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)
	batches := []model.NotificationBatch{
		{Source: "filing", Trigger: "scheduler", Channel: "telegram", Status: "sent", ItemCount: 2, CreatedAt: now},
		{Source: "ipo", Trigger: "manual", Channel: "telegram", Status: "suppressed", ItemCount: 1, CreatedAt: now.Add(-24 * time.Hour)},
	}
	if err := db.Create(&batches).Error; err != nil {
		t.Fatalf("seed batches: %v", err)
	}
	if err := db.Create(&[]model.NotificationBatchItem{
		{BatchID: batches[0].ID, FilingID: "newer", Status: "sent", Reason: "eligible", EventAt: now},
		{BatchID: batches[0].ID, FilingID: "older", Status: "suppressed", Reason: "history_backfill", EventAt: now.Add(-time.Hour)},
	}).Error; err != nil {
		t.Fatalf("seed batch items: %v", err)
	}

	service := NewNotificationBatchService(db, &fakeNotifier{}, NewConfigService(db, NewAuditService(db)))
	tests := []struct {
		name       string
		filter     NotificationBatchFilter
		wantTotal  int64
		wantSource string
	}{
		{name: "filters sent filing batches", filter: NotificationBatchFilter{Source: "filing", Status: "sent", Trigger: "scheduler", Page: 1, PageSize: 10}, wantTotal: 1, wantSource: "filing"},
		{name: "filters inclusive date range", filter: NotificationBatchFilter{DateFrom: timePointer(now.Add(-time.Hour)), DateTo: timePointer(now), Page: 1, PageSize: 10}, wantTotal: 1, wantSource: "filing"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := service.List(context.Background(), tt.filter)
			if err != nil {
				t.Fatalf("List: %v", err)
			}
			if result.Total != tt.wantTotal || len(result.Items) != 1 || result.Items[0].Source != tt.wantSource {
				t.Fatalf("result = %+v", result)
			}
		})
	}

	items, err := service.ListItems(context.Background(), batches[0].ID, 1, 1)
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	if items.Total != 2 || len(items.Items) != 1 || items.Items[0].FilingID != "newer" {
		t.Fatalf("items = %+v", items)
	}
}

func boolPointer(value bool) *bool { return &value }

func timePointer(value time.Time) *time.Time { return &value }

func TestRenderNotificationBatchSummaryTruncatesTableDriven(t *testing.T) {
	tests := []struct {
		name       string
		title      string
		wantLinks  int
		wantPhrase string
	}{
		{name: "limits details to ten items", title: "Event", wantLinks: 10, wantPhrase: "另有 2 条"},
		{name: "limits telegram message length", title: strings.Repeat("long title ", 100), wantPhrase: "内容过长"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			candidates := make([]NotificationCandidate, 0, 12)
			for i := 0; i < 12; i++ {
				candidates = append(candidates, NotificationCandidate{Ticker: "TSLA", CompanyName: "Tesla", FilingType: "8-K", Title: tt.title, FilingURL: "https://sec.test", Reason: "eligible"})
			}
			message := renderNotificationBatchSummary("filing", candidates)
			if len([]rune(message)) > 4000 || !strings.Contains(message, tt.wantPhrase) {
				t.Fatalf("message length = %d, message = %q", len([]rune(message)), message)
			}
			if tt.wantLinks > 0 && strings.Count(message, "https://sec.test") != tt.wantLinks {
				t.Fatalf("link count = %d, want %d", strings.Count(message, "https://sec.test"), tt.wantLinks)
			}
		})
	}
}
