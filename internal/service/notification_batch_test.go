package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"sec_monitor/internal/model"
	"sec_monitor/internal/telegram"

	"gorm.io/gorm"
)

func TestNotificationBatchFailureSchedulesExponentialRetry(t *testing.T) {
	db := testDB(t)
	now := time.Now().UTC()
	if err := db.Create(&model.Filing{FilingID: "retry-filing", Ticker: "TSLA", CompanyName: "Tesla", FilingType: "8-K", FilingDate: now, PulledAt: now}).Error; err != nil {
		t.Fatalf("seed filing: %v", err)
	}
	seedNotificationTelegramConfig(t, db)
	notifier := &fakeNotifier{errs: []error{
		context.DeadlineExceeded,
		context.DeadlineExceeded,
		fmt.Errorf(`Post "https://api.telegram.org/bot123456:secret-token/sendMessage": timeout`),
	}}
	batch, err := NewNotificationBatchService(db, notifier, NewConfigService(db, NewAuditService(db))).Deliver(context.Background(), NotificationBatchInput{
		Source: "filing", Trigger: "manual", Candidates: []NotificationCandidate{{
			EntityKind: "filing", FilingID: "retry-filing", Ticker: "TSLA", CompanyName: "Tesla", FilingType: "8-K", Reason: "eligible", EventAt: now,
		}},
	})
	if err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if batch.Status != "failed" || batch.RetryCount != 1 || batch.LastAttemptAt == nil || batch.NextRetryAt == nil {
		t.Fatalf("batch = %+v, want first failed retry state", batch)
	}
	if got := batch.NextRetryAt.Sub(*batch.LastAttemptAt); got != 5*time.Minute {
		t.Fatalf("next retry delay = %s, want 5m", got)
	}
	if strings.Contains(batch.ErrorMessage, "secret-token") || !strings.Contains(batch.ErrorMessage, "******") {
		t.Fatalf("error_message was not sanitized: %q", batch.ErrorMessage)
	}
}

func TestRetryDueSchedulesFiveRetryRoundsAfterInitialDeliveryFailure(t *testing.T) {
	db := testDB(t)
	now := time.Date(2026, 7, 11, 10, 0, 0, 0, time.UTC)
	if err := db.Create(&model.Filing{FilingID: "retry-filing", Ticker: "TSLA", CompanyName: "Tesla", FilingType: "8-K", FilingDate: now, PulledAt: now}).Error; err != nil {
		t.Fatalf("seed filing: %v", err)
	}
	seedNotificationTelegramConfig(t, db)
	notifier := &fakeNotifier{errs: make([]error, 18)}
	for i := range notifier.errs {
		notifier.errs[i] = context.DeadlineExceeded
	}
	svc := NewNotificationBatchService(db, notifier, NewConfigService(db, NewAuditService(db)))
	batch, err := svc.Deliver(context.Background(), NotificationBatchInput{Source: "filing", Trigger: "scheduler", Candidates: []NotificationCandidate{{
		EntityKind: "filing", FilingID: "retry-filing", Ticker: "TSLA", CompanyName: "Tesla", FilingType: "8-K", Reason: "eligible", EventAt: now,
	}}})
	if err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if batch.Status != "failed" || batch.RetryCount != 1 || batch.NextRetryAt == nil || batch.NextRetryAt.Sub(*batch.LastAttemptAt) != 5*time.Minute {
		t.Fatalf("initial delivery batch = %+v, want first retry at 5m", batch)
	}
	now = *batch.NextRetryAt
	delays := []time.Duration{5 * time.Minute, 15 * time.Minute, 45 * time.Minute, 2 * time.Hour, 6 * time.Hour}
	for round := range delays {
		result, err := svc.RetryDue(context.Background(), now)
		if err != nil {
			t.Fatalf("RetryDue round %d: %v", round+1, err)
		}
		if result.Attempted != 1 {
			t.Fatalf("RetryDue round %d result = %+v", round+1, result)
		}
		batchID := batch.ID
		batch = model.NotificationBatch{}
		if err := db.First(&batch, batchID).Error; err != nil {
			t.Fatalf("load batch round %d: %v", round+1, err)
		}
		if batch.RetryCount != round+2 {
			t.Fatalf("retry_count round %d = %d", round+1, batch.RetryCount)
		}
		if round == len(delays)-1 {
			if batch.Status != "dead_letter" || batch.NextRetryAt != nil || result.DeadLetter != 1 {
				t.Fatalf("final batch = %+v, result = %+v", batch, result)
			}
			continue
		}
		if batch.Status != "failed" || batch.NextRetryAt == nil || !batch.NextRetryAt.Equal(now.Add(delays[round+1])) || result.Failed != 1 {
			t.Fatalf("round %d batch = %+v, result = %+v", round+1, batch, result)
		}
		now = *batch.NextRetryAt
	}
	if notifier.calls != 18 {
		t.Fatalf("notifier calls = %d, want 18", notifier.calls)
	}
}

func TestRetryDueClaimsBatchBeforeExternalSend(t *testing.T) {
	db := testDB(t)
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("database connection: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	now := time.Date(2026, 7, 11, 10, 0, 0, 0, time.UTC)
	batch := model.NotificationBatch{Source: "filing", Trigger: "scheduler", Channel: "telegram", Status: "failed", ItemCount: 1, FailedCount: 1, RetryCount: 1, NextRetryAt: &now, CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&batch).Error; err != nil {
		t.Fatalf("seed batch: %v", err)
	}
	if err := db.Create(&model.NotificationBatchItem{BatchID: batch.ID, EntityKind: "filing", FilingID: "retry-filing", Ticker: "TSLA", CompanyName: "Tesla", FilingType: "8-K", EventAt: now, Status: "failed", Reason: "delivery_failed"}).Error; err != nil {
		t.Fatalf("seed batch item: %v", err)
	}
	notifier := &blockingNotifier{started: make(chan struct{}), release: make(chan struct{})}
	svc := NewNotificationBatchService(db, notifier, NewConfigService(db, NewAuditService(db)))
	firstResult := make(chan NotificationRetryResult, 1)
	firstErr := make(chan error, 1)
	go func() {
		result, err := svc.RetryDue(context.Background(), now)
		firstResult <- result
		firstErr <- err
	}()
	<-notifier.started

	second, err := svc.RetryDue(context.Background(), now)
	if err != nil {
		t.Fatalf("competing RetryDue: %v", err)
	}
	if second.Attempted != 0 || notifier.callCount() != 1 {
		t.Fatalf("competing RetryDue = %+v, notifier calls = %d, want no second send", second, notifier.callCount())
	}
	close(notifier.release)
	if err := <-firstErr; err != nil {
		t.Fatalf("first RetryDue: %v", err)
	}
	if result := <-firstResult; result.Attempted != 1 || result.Sent != 1 {
		t.Fatalf("first RetryDue = %+v", result)
	}
}

type blockingNotifier struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
	mu      sync.Mutex
	calls   int
}

func (n *blockingNotifier) Send(ctx context.Context, _ telegram.Message) error {
	n.mu.Lock()
	n.calls++
	n.mu.Unlock()
	n.once.Do(func() { close(n.started) })
	select {
	case <-n.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (n *blockingNotifier) callCount() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.calls
}

func TestRequeueOnlyAcceptsFailedOrDeadLetter(t *testing.T) {
	db := testDB(t)
	now := time.Date(2026, 7, 11, 10, 0, 0, 0, time.UTC)
	future := now.Add(time.Hour)
	batches := []model.NotificationBatch{
		{Source: "filing", Trigger: "manual", Channel: "telegram", Status: "sent", NextRetryAt: &future},
		{Source: "filing", Trigger: "manual", Channel: "telegram", Status: "suppressed", NextRetryAt: &future},
		{Source: "filing", Trigger: "manual", Channel: "telegram", Status: "failed", RetryCount: 2, NextRetryAt: &future},
		{Source: "filing", Trigger: "manual", Channel: "telegram", Status: "dead_letter", RetryCount: 5},
	}
	if err := db.Create(&batches).Error; err != nil {
		t.Fatalf("seed batches: %v", err)
	}
	svc := NewNotificationBatchService(db, &fakeNotifier{}, NewConfigService(db, NewAuditService(db)))
	for _, batch := range batches[:2] {
		if _, err := svc.Requeue(context.Background(), batch.ID, now); !errors.Is(err, ErrValidation) {
			t.Fatalf("Requeue %s error = %v, want validation error", batch.Status, err)
		}
	}
	for _, batch := range batches[2:] {
		got, err := svc.Requeue(context.Background(), batch.ID, now)
		if err != nil {
			t.Fatalf("Requeue %s: %v", batch.Status, err)
		}
		if got.Status != "failed" || got.NextRetryAt == nil || !got.NextRetryAt.Equal(now) {
			t.Fatalf("requeued %s batch = %+v", batch.Status, got)
		}
	}
}

func seedNotificationTelegramConfig(t *testing.T, db *gorm.DB) {
	t.Helper()
	if err := db.Create(&[]model.SystemConfig{
		{ConfigKey: "telegram.enabled", ConfigValue: "true", ValueType: "bool", Category: "telegram"},
		{ConfigKey: "telegram.bot_token", ConfigValue: "token", ValueType: "string", Category: "telegram"},
		{ConfigKey: "telegram.chat_id", ConfigValue: "chat", ValueType: "string", Category: "telegram"},
	}).Error; err != nil {
		t.Fatalf("seed telegram config: %v", err)
	}
}

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
