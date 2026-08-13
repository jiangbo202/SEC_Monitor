package model

import "time"

type NotificationBatch struct {
	ID                 uint   `gorm:"primaryKey" json:"id"`
	SyncRunID          uint   `gorm:"not null;index" json:"sync_run_id"`
	Source             string `gorm:"size:32;not null;index" json:"source"`
	Trigger            string `gorm:"size:32;not null;index" json:"trigger"`
	Channel            string `gorm:"size:64;not null;index" json:"channel"`
	Target             string `gorm:"size:255" json:"target"`
	Status             string `gorm:"size:32;not null;index" json:"status"`
	ItemCount          int    `gorm:"not null" json:"item_count"`
	SentCount          int    `gorm:"not null" json:"sent_count"`
	SuppressedCount    int    `gorm:"not null" json:"suppressed_count"`
	FailedCount        int    `gorm:"not null" json:"failed_count"`
	RetryCount         int    `gorm:"not null" json:"retry_count"`
	SuppressionSummary string `gorm:"type:text" json:"suppression_summary,omitempty"`
	// MessageText preserves the final payload for deterministic retries and a
	// single audit trail, including non-filing system events.
	MessageText     string     `gorm:"type:text" json:"message_text,omitempty"`
	ErrorMessage    string     `gorm:"type:text" json:"error_message,omitempty"`
	SentAt          *time.Time `json:"sent_at"`
	NextRetryAt     *time.Time `gorm:"index" json:"next_retry_at"`
	LastAttemptAt   *time.Time `json:"last_attempt_at"`
	RetryLeaseUntil *time.Time `gorm:"index" json:"retry_lease_until"`
	RetryLeaseToken string     `gorm:"size:64;index" json:"-"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

func (NotificationBatch) TableName() string {
	return "notification_batches"
}

type NotificationBatchItem struct {
	ID      uint `gorm:"primaryKey" json:"id"`
	BatchID uint `gorm:"not null;index" json:"batch_id"`
	// EventKey is a durable Telegram idempotency key. It is nullable so legacy
	// delivery rows can be migrated without inventing historical identities;
	// every newly-created queue item receives a globally unique value.
	EventKey    *string   `gorm:"size:96;uniqueIndex" json:"event_key,omitempty"`
	TargetID    uint      `gorm:"index" json:"target_id,omitempty"`
	EntityKind  string    `gorm:"size:32;not null;index" json:"entity_kind"`
	FilingID    string    `gorm:"size:255;not null;index" json:"filing_id"`
	Ticker      string    `gorm:"size:32;index" json:"ticker,omitempty"`
	CIK         string    `gorm:"size:32;index" json:"cik,omitempty"`
	CompanyName string    `gorm:"size:255;index" json:"company_name"`
	FilingType  string    `gorm:"size:64;index" json:"filing_type"`
	Title       string    `gorm:"type:text" json:"title"`
	FilingURL   string    `gorm:"type:text" json:"filing_url"`
	EventAt     time.Time `gorm:"index" json:"event_at"`
	Status      string    `gorm:"size:32;not null;index" json:"status"`
	Reason      string    `gorm:"size:64;not null;index" json:"reason"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func (NotificationBatchItem) TableName() string {
	return "notification_batch_items"
}
