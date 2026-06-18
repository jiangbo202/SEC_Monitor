package model

import "time"

type NotificationLog struct {
	ID           uint       `gorm:"primaryKey" json:"id"`
	FilingID     string     `gorm:"size:128;not null;index" json:"filing_id"`
	Channel      string     `gorm:"size:64;not null;index" json:"channel"`
	Target       string     `gorm:"size:255" json:"target"`
	Status       string     `gorm:"size:32;not null;index" json:"status"`
	RetryCount   int        `gorm:"not null" json:"retry_count"`
	ErrorMessage string     `gorm:"type:text" json:"error_message,omitempty"`
	SentAt       *time.Time `json:"sent_at"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
}
