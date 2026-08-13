package model

import "time"

// TaskExecution is the durable, task-level audit trail for scheduler jobs.
// It deliberately excludes the small-cap discovery workflow, which retains
// its richer, independent workflow and provider logs in the discovery store.
type TaskExecution struct {
	ID           uint       `gorm:"primaryKey" json:"id"`
	TaskName     string     `gorm:"size:128;not null;index:idx_task_execution_task_started" json:"task_name"`
	Trigger      string     `gorm:"size:16;not null;index" json:"trigger"`
	Status       string     `gorm:"size:16;not null;index" json:"status"`
	StartedAt    time.Time  `gorm:"index:idx_task_execution_task_started" json:"started_at"`
	FinishedAt   *time.Time `json:"finished_at"`
	DurationMS   int64      `gorm:"not null;default:0" json:"duration_ms"`
	Summary      string     `gorm:"type:text" json:"summary"`
	ErrorMessage string     `gorm:"type:text" json:"error_message"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
}
