package model

import "time"

// InAppNotification is the local, user-readable event inbox. It is purposely
// independent from Telegram delivery: an event remains visible even when an
// external notification channel is disabled or temporarily unavailable.
type InAppNotification struct {
	ID          uint       `gorm:"primaryKey" json:"id"`
	EventKey    string     `gorm:"size:255;not null;uniqueIndex" json:"event_key"`
	Source      string     `gorm:"size:32;not null;index" json:"source"`
	Scope       string     `gorm:"size:32;not null;index" json:"scope"`
	EntityKind  string     `gorm:"size:32;not null;index" json:"entity_kind"`
	TargetID    uint       `gorm:"index" json:"target_id,omitempty"`
	Ticker      string     `gorm:"size:32;index" json:"ticker,omitempty"`
	CompanyName string     `gorm:"size:255" json:"company_name,omitempty"`
	Severity    string     `gorm:"size:16;not null;index" json:"severity"`
	Title       string     `gorm:"type:text;not null" json:"title"`
	Body        string     `gorm:"type:text" json:"body,omitempty"`
	Link        string     `gorm:"type:text" json:"link,omitempty"`
	OccurredAt  time.Time  `gorm:"not null;index" json:"occurred_at"`
	ReadAt      *time.Time `gorm:"index" json:"read_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

func (InAppNotification) TableName() string { return "in_app_notifications" }
