package model

import "time"

// One bounded window is retained per consumer. Only public calendar facts
// and the continuation date are stored; never provider credentials.
type EarningsCalendarCheckpoint struct {
	Scope       string `gorm:"primaryKey;size:32"`
	WindowStart string `gorm:"size:10"`
	WindowEnd   string `gorm:"size:10"`
	NextDate    string `gorm:"size:10"`
	EventsJSON  string `gorm:"type:text"`
	Complete    bool
	UpdatedAt   time.Time
}
