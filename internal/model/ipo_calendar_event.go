package model

import "time"

// IPOCalendarEvent is a cached Longbridge IPO calendar entry. Calendar
// entries intentionally remain separate from SEC filings because a calendar
// event may not expose a CIK or a corresponding registration statement yet.
type IPOCalendarEvent struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	EventKey    string    `gorm:"size:192;not null;uniqueIndex" json:"event_key"`
	Symbol      string    `gorm:"size:48;index" json:"symbol,omitempty"`
	Market      string    `gorm:"size:24;index" json:"market,omitempty"`
	CompanyName string    `gorm:"size:255;index" json:"company_name,omitempty"`
	EventDate   time.Time `gorm:"index" json:"event_date"`
	Session     string    `gorm:"size:64" json:"session,omitempty"`
	Content     string    `gorm:"type:text" json:"content,omitempty"`
	Currency    string    `gorm:"size:16" json:"currency,omitempty"`
	Source      string    `gorm:"size:64;not null" json:"source"`
	LastSeenAt  time.Time `gorm:"index" json:"last_seen_at"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func (IPOCalendarEvent) TableName() string {
	return "ipo_calendar_events"
}
