package model

import "time"

type TaskConfig struct {
	ID        uint       `gorm:"primaryKey" json:"id"`
	TaskName  string     `gorm:"size:128;not null;uniqueIndex" json:"task_name"`
	CronExpr  string     `gorm:"size:128;not null" json:"cron_expr"`
	Enabled   bool       `gorm:"not null;index" json:"enabled"`
	LastRunAt *time.Time `json:"last_run_at"`
	NextRunAt *time.Time `json:"next_run_at"`
	Running   bool       `gorm:"not null" json:"running"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
}
