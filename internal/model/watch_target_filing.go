package model

import "time"

// WatchTargetFiling associates a globally deduplicated SEC document with each
// target whose identity matched it.
type WatchTargetFiling struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	TargetID  uint      `gorm:"not null;uniqueIndex:idx_watch_target_filing" json:"target_id"`
	FilingID  uint      `gorm:"not null;uniqueIndex:idx_watch_target_filing" json:"filing_id"`
	CreatedAt time.Time `json:"created_at"`
}
