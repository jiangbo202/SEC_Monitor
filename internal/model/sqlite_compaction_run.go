package model

import "time"

// SQLiteCompactionRun records a manual low-traffic VACUUM operation. It is
// intentionally separate from backup records: backups are routine snapshots,
// while compaction temporarily needs an exclusive SQLite write lock.
type SQLiteCompactionRun struct {
	ID                   uint       `json:"id"`
	Status               string     `json:"status" gorm:"size:16;index"`
	StartedAt            time.Time  `json:"started_at" gorm:"index"`
	CompletedAt          *time.Time `json:"completed_at"`
	DurationMS           int64      `json:"duration_ms"`
	MainBeforeBytes      int64      `json:"main_before_bytes"`
	MainAfterBytes       int64      `json:"main_after_bytes"`
	DiscoveryBeforeBytes int64      `json:"discovery_before_bytes"`
	DiscoveryAfterBytes  int64      `json:"discovery_after_bytes"`
	ErrorMessage         string     `json:"error_message" gorm:"type:text"`
	CreatedAt            time.Time  `json:"created_at"`
	UpdatedAt            time.Time  `json:"updated_at"`
}
