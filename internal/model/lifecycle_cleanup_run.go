package model

import "time"

// LifecycleCleanupRun gives a durable operator trail for cleanup spanning the
// main and discovery databases. A retry is safe: each phase is idempotent and
// only touches completed operational history older than the fixed cutoff.
type LifecycleCleanupRun struct {
	ID              uint       `gorm:"primaryKey" json:"id"`
	Status          string     `gorm:"size:16;not null;index" json:"status"`
	RetentionDays   int        `json:"retention_days"`
	Cutoff          time.Time  `gorm:"index" json:"cutoff"`
	MainStatus      string     `gorm:"size:16" json:"main_status"`
	DiscoveryStatus string     `gorm:"size:16" json:"discovery_status"`
	DeletedCount    int64      `json:"deleted_count"`
	ErrorMessage    string     `gorm:"type:text" json:"error_message,omitempty"`
	StartedAt       time.Time  `gorm:"index" json:"started_at"`
	CompletedAt     *time.Time `json:"completed_at,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}
