package model

import "time"

type SyncRun struct {
	ID             uint       `gorm:"primaryKey" json:"id"`
	StartedAt      time.Time  `gorm:"index" json:"started_at"`
	FinishedAt     *time.Time `json:"finished_at"`
	Status         string     `gorm:"size:32;not null;index" json:"status"`
	Trigger        string     `gorm:"size:32;not null;index" json:"trigger"`
	TargetsChecked int        `json:"targets_checked"`
	NewFilings     int        `json:"new_filings"`
	FailedTargets  int        `json:"failed_targets"`
	ErrorMessage   string     `gorm:"type:text" json:"error_message"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}
