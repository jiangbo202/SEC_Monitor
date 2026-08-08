package model

import "time"

// OperationalAlertDelivery is a small local deduplication ledger for
// operator-facing Telegram alerts. It intentionally stores only a safe
// fingerprint and rendered summary, never provider credentials or raw tokens.
type OperationalAlertDelivery struct {
	ID          uint       `gorm:"primaryKey" json:"id"`
	Fingerprint string     `gorm:"size:64;not null;uniqueIndex" json:"fingerprint"`
	Severity    string     `gorm:"size:16;not null;index" json:"severity"`
	Summary     string     `gorm:"type:text" json:"summary"`
	LastSentAt  *time.Time `gorm:"index" json:"last_sent_at"`
	LastError   string     `gorm:"type:text" json:"last_error,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}
