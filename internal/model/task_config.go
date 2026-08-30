package model

import "time"

type TaskConfig struct {
	ID                  uint       `gorm:"primaryKey" json:"id"`
	TaskName            string     `gorm:"size:128;not null;uniqueIndex" json:"task_name"`
	CronExpr            string     `gorm:"size:128;not null" json:"cron_expr"`
	Enabled             bool       `gorm:"not null;index" json:"enabled"`
	LastRunAt           *time.Time `json:"last_run_at"`
	NextRunAt           *time.Time `json:"next_run_at"`
	Running             bool       `gorm:"not null" json:"running"`
	RunningSince        *time.Time `gorm:"index" json:"running_since"`
	LastStatus          string     `gorm:"size:16;index" json:"last_status"`
	LastErrorMessage    string     `gorm:"type:text" json:"last_error_message"`
	ConsecutiveFailures int        `gorm:"not null;default:0" json:"consecutive_failures"`
	RetryNotBefore      *time.Time `gorm:"index" json:"retry_not_before,omitempty"`
	AutoRetryAttempts   int        `gorm:"not null;default:0" json:"auto_retry_attempts"`
	PendingCount        *int       `json:"pending_count"`
	CreatedAt           time.Time  `json:"created_at"`
	UpdatedAt           time.Time  `json:"updated_at"`
}
