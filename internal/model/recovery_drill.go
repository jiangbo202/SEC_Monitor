package model

import "time"

// RecoveryDrill records a non-destructive restore rehearsal of the latest
// SQLite snapshot pair. It never contains a database path, credential, or
// backup content; detailed filesystem information stays in the backup folder.
type RecoveryDrill struct {
	ID              uint       `gorm:"primaryKey" json:"id"`
	Status          string     `gorm:"size:16;not null;index" json:"status"`
	LocalStatus     string     `gorm:"size:16" json:"local_status"`
	ReplicaStatus   string     `gorm:"size:16" json:"replica_status"`
	LocalReason     string     `gorm:"type:text" json:"local_reason,omitempty"`
	ReplicaReason   string     `gorm:"type:text" json:"replica_reason,omitempty"`
	BackupTimestamp *time.Time `gorm:"index" json:"backup_timestamp,omitempty"`
	StartedAt       time.Time  `gorm:"index" json:"started_at"`
	CompletedAt     *time.Time `json:"completed_at,omitempty"`
	DurationMS      int64      `json:"duration_ms"`
	ErrorMessage    string     `gorm:"type:text" json:"error_message,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}
