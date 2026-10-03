package discovery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	lbconfig "github.com/longbridge/openapi-go/config"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Budget counters are application safeguards, never vendor quota estimates.
type APIProviderPolicy struct {
	Provider       string     `gorm:"primaryKey" json:"provider"`
	Paused         bool       `json:"paused"`
	DailyBudget    int        `json:"daily_budget"`
	MinIntervalMS  int        `json:"min_interval_ms"`
	BudgetDate     string     `json:"budget_date"`
	DailyUsed      int        `json:"daily_used"`
	LastDispatchAt *time.Time `json:"-"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

type APICallRecord struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	Provider  string    `gorm:"index:idx_api_calls_window,priority:1" json:"provider"`
	Endpoint  string    `json:"endpoint"`
	Ticker    string    `json:"ticker"`
	Trigger   string    `json:"trigger"`
	Status    string    `json:"status"`
	ErrorKind string    `json:"error_kind,omitempty"`
	ElapsedMS int64     `json:"elapsed_ms"`
	StartedAt time.Time `gorm:"index:idx_api_calls_window,priority:2" json:"started_at"`
}

type apiTriggerKey struct{}
type apiIntervalWait struct{ until time.Time }

func (e *apiIntervalWait) Error() string { return "waiting for shared API interval" }

func WithAPITrigger(ctx context.Context, trigger string) context.Context {
	return context.WithValue(ctx, apiTriggerKey{}, trigger)
}

type APIMonitor struct {
	DB        *gorm.DB
	mu        sync.Mutex
	next      map[string]time.Time
	prunedDay string
}

var activeAPIMonitor struct {
	sync.RWMutex
	monitor *APIMonitor
}

func ConfigureAPIMonitor(db *gorm.DB) *APIMonitor {
	var m *APIMonitor
	if db != nil {
		m = NewAPIMonitor(db)
	}
	activeAPIMonitor.Lock()
	activeAPIMonitor.monitor = m
	activeAPIMonitor.Unlock()
	return m
}
func NewAPIMonitor(db *gorm.DB) *APIMonitor { return &APIMonitor{DB: db, next: map[string]time.Time{}} }
func CurrentAPIMonitor() *APIMonitor {
	activeAPIMonitor.RLock()
	defer activeAPIMonitor.RUnlock()
	return activeAPIMonitor.monitor
}

func EnsureAPIPolicies(ctx context.Context, db *gorm.DB) error {
	if err := db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&[]APIProviderPolicy{
		{Provider: "longbridge", DailyBudget: 0, MinIntervalMS: 1100},
		{Provider: "futu", Paused: true, DailyBudget: 50, MinIntervalMS: 1100},
	}).Error; err != nil {
		return err
	}
	return EnsureAPIModules(ctx, db)
}

// Acquire reserves the shared budget before dispatch. An UPDATE first obtains
// SQLite's write lock, so separate processes cannot race the daily allowance.
func (m *APIMonitor) Acquire(ctx context.Context, provider, endpoint, ticker string) (*APICallRecord, error) {
	if m == nil {
		return nil, nil
	}
	for {
		if err := CheckAPIModuleEndpoint(ctx, m.DB, provider, endpoint); err != nil {
			return nil, err
		}
		m.mu.Lock()
		wait := time.Until(m.next[provider])
		if wait > 0 {
			m.mu.Unlock()
			timer := time.NewTimer(wait)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil, ctx.Err()
			case <-timer.C:
			}
			continue
		}
		now := time.Now().UTC()
		day := now.In(time.FixedZone("HK", 8*3600)).Format(time.DateOnly)
		ticker = strings.TrimSuffix(strings.ToUpper(strings.TrimSpace(ticker)), ".US")
		if len(ticker) > 200 {
			ticker = "[batch]"
		}
		row := APICallRecord{Provider: provider, Endpoint: endpoint, Ticker: ticker, Trigger: "background", Status: "started", StartedAt: now}
		if value, ok := ctx.Value(apiTriggerKey{}).(string); ok {
			row.Trigger = value
		}
		var policy APIProviderPolicy
		err := m.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if err := tx.Model(&APIProviderPolicy{}).Where("provider = ?", provider).UpdateColumn("updated_at", now).Error; err != nil {
				return err
			}
			if err := tx.First(&policy, "provider = ?", provider).Error; err != nil {
				return err
			}
			if policy.Paused {
				return errors.New("API provider is paused")
			}
			if policy.LastDispatchAt != nil {
				until := policy.LastDispatchAt.Add(time.Duration(policy.MinIntervalMS) * time.Millisecond)
				if until.After(now) {
					return &apiIntervalWait{until: until}
				}
			}
			if policy.BudgetDate != day {
				policy.DailyUsed = 0
				policy.BudgetDate = day
			}
			if policy.DailyBudget > 0 && policy.DailyUsed >= policy.DailyBudget {
				return errors.New("local daily API budget exhausted")
			}
			policy.DailyUsed++
			policy.LastDispatchAt = &now
			if err := tx.Save(&policy).Error; err != nil {
				return err
			}
			if m.prunedDay != day {
				if err := tx.Where("started_at < ?", now.Add(-30*24*time.Hour)).Delete(&APICallRecord{}).Error; err != nil {
					return err
				}
			}
			return tx.Create(&row).Error
		})
		if err == nil {
			m.prunedDay = day
			m.next[provider] = now.Add(time.Duration(policy.MinIntervalMS) * time.Millisecond)
		}
		var interval *apiIntervalWait
		if errors.As(err, &interval) {
			m.next[provider] = interval.until
		}
		m.mu.Unlock()
		if interval != nil {
			continue
		}
		if err != nil {
			return nil, err
		}
		return &row, nil
	}
}

func (m *APIMonitor) Finish(record *APICallRecord, err error) {
	if m == nil || record == nil {
		return
	}
	record.ElapsedMS = time.Since(record.StartedAt).Milliseconds()
	record.Status = "success"
	if err != nil {
		record.Status = "failed"
		record.ErrorKind = "request_failed"
		message := strings.ToLower(err.Error())
		switch {
		case strings.Contains(message, "429") || strings.Contains(message, "rate") || strings.Contains(message, "限流"):
			record.ErrorKind = "rate_limited"
		case strings.Contains(message, "401") || strings.Contains(message, "403") || strings.Contains(message, "permission") || strings.Contains(message, "token"):
			record.ErrorKind = "authorization"
		case strings.Contains(message, "timeout") || errors.Is(err, context.DeadlineExceeded):
			record.ErrorKind = "timeout"
		}
	}
	// Only fixed categories are persisted. Never persist response bodies, query
	// strings, headers, vendor messages, OAuth codes, or credentials.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = m.DB.WithContext(ctx).Model(&APICallRecord{}).Where("id = ?", record.ID).Updates(map[string]any{"status": record.Status, "error_kind": record.ErrorKind, "elapsed_ms": record.ElapsedMS}).Error
}

type apiTransport struct {
	base     http.RoundTripper
	monitor  *APIMonitor
	provider string
}
type monitoredBody struct {
	io.ReadCloser
	captured  []byte
	record    *APICallRecord
	transport *apiTransport
	failed    error
	once      sync.Once
}

func (b *monitoredBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if len(b.captured)+n <= 4<<20 {
		b.captured = append(b.captured, p[:n]...)
	}
	if err == io.EOF {
		b.finish()
	} else if err != nil {
		b.failed = err
		b.finish()
	}
	return n, err
}
func (b *monitoredBody) finish() {
	b.once.Do(func() {
		err := b.failed
		if err == nil {
			var envelope struct {
				Code    *int `json:"code"`
				RetCode *int `json:"ret_code"`
			}
			if decodeErr := json.Unmarshal(b.captured, &envelope); decodeErr != nil {
				err = errors.New("invalid API response")
			} else if (b.transport.provider == "futu" && envelope.RetCode == nil) || (b.transport.provider == "longbridge" && envelope.Code == nil) {
				err = errors.New("missing API response code")
			} else if (envelope.Code != nil && *envelope.Code != 0) || (envelope.RetCode != nil && *envelope.RetCode != 0) {
				err = errors.New("vendor API response error")
				if b.transport.provider == "futu" && envelope.RetCode != nil && *envelope.RetCode == -10 {
					err = nil
					b.record.Status = "no_coverage"
				}
			}
		}
		status := b.record.Status
		b.transport.monitor.Finish(b.record, err)
		if status == "no_coverage" {
			_ = b.transport.monitor.DB.Model(&APICallRecord{}).Where("id = ?", b.record.ID).Update("status", status).Error
		}
	})
}
func (b *monitoredBody) Close() error { err := b.ReadCloser.Close(); b.finish(); return err }

func (t *apiTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// The SDK creates OTP/reconnect requests with a fresh background context.
	// Do not misattribute those requests to a scheduled task.
	if req.Context().Value(apiTriggerKey{}) == nil {
		req = req.WithContext(WithAPITrigger(req.Context(), "sdk_internal"))
	}
	ticker := req.URL.Query().Get("symbol")
	if ticker == "" {
		id := req.URL.Query().Get("counter_id")
		if strings.HasPrefix(id, "ST/US/") {
			ticker = strings.TrimPrefix(id, "ST/US/")
		}
	}
	endpoint := req.URL.Path
	if len(ticker) > 200 {
		ticker = "[batch]"
	}
	if t.provider == "futu" {
		parts := strings.Split(endpoint, "/")
		if len(parts) > 4 && strings.HasPrefix(parts[4], "US.") {
			ticker = strings.TrimPrefix(parts[4], "US.")
			parts[4] = "{symbol}"
			endpoint = strings.Join(parts, "/")
		}
	}
	row, err := t.monitor.Acquire(req.Context(), t.provider, endpoint, ticker)
	if err != nil {
		return nil, err
	}
	resp, err := t.base.RoundTrip(req)
	if err != nil {
		t.monitor.Finish(row, err)
		return nil, err
	}
	var failed error
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		failed = fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	resp.Body = &monitoredBody{ReadCloser: resp.Body, record: row, transport: t, failed: failed}
	return resp, nil
}

func (m *APIMonitor) HTTPClient(provider string) *http.Client {
	if m == nil {
		return &http.Client{Timeout: 25 * time.Second}
	}
	return &http.Client{Timeout: 25 * time.Second, Transport: &apiTransport{base: http.DefaultTransport, monitor: m, provider: provider}, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("API redirects are not permitted") }}
}

// Every fundamental SDK factory uses this client, including SDK counter
// resolution calls and retry attempts. WebSocket data commands are measured separately.
func MonitorLongbridgeConfig(cfg *lbconfig.Config) {
	if m := CurrentAPIMonitor(); m != nil {
		cfg.Client = m.HTTPClient("longbridge")
	}
}
