package model

import "time"

type SyncRunDetail struct {
	ID           uint       `gorm:"primaryKey" json:"id"`
	SyncRunID    uint       `gorm:"not null;index" json:"sync_run_id"`
	TargetID     uint       `gorm:"index" json:"target_id"`
	Ticker       string     `gorm:"size:32;not null;index" json:"ticker"`
	Status       string     `gorm:"size:32;not null;index" json:"status"`
	NewFilings   int        `json:"new_filings"`
	StartedAt    time.Time  `json:"started_at"`
	FinishedAt   *time.Time `json:"finished_at"`
	DurationMS   int64      `json:"duration_ms"`
	ErrorMessage string     `gorm:"type:text" json:"error_message"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
}
