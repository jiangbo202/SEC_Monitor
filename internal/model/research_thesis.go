package model

import "time"

// ResearchThesis is a user-authored conclusion, never a trading instruction.
// Evidence is snapshotted so source retention cannot erase the research trail.
type ResearchThesis struct {
	Ticker       string           `json:"ticker" gorm:"primaryKey;size:32"`
	Version      uint             `json:"version"`
	Status       string           `json:"status" gorm:"size:24;index"`
	Rationale    string           `json:"rationale" gorm:"type:text"`
	Invalidation string           `json:"invalidation" gorm:"type:text"`
	NextCheck    string           `json:"next_check" gorm:"type:text"`
	NextReviewAt *time.Time       `json:"next_review_at" gorm:"index"`
	ReviewNote   string           `json:"review_note" gorm:"type:text"`
	ReviewedAt   *time.Time       `json:"reviewed_at"`
	Evidence     []ThesisEvidence `json:"evidence" gorm:"serializer:json;type:text"`
	CreatedAt    time.Time        `json:"created_at"`
	UpdatedAt    time.Time        `json:"updated_at"`
}

type ThesisEvidence struct {
	Kind       string    `json:"kind"`
	ID         uint      `json:"id"`
	Label      string    `json:"label"`
	URL        string    `json:"url"`
	Summary    string    `json:"summary"`
	SHA256     string    `json:"sha256"`
	RecordedAt time.Time `json:"recorded_at"`
}

// Revisions are append-only; there is deliberately no delete/update endpoint.
type ResearchThesisRevision struct {
	ID        uint           `json:"id" gorm:"primaryKey"`
	Ticker    string         `json:"ticker" gorm:"uniqueIndex:idx_thesis_revision,priority:1;size:32"`
	Version   uint           `json:"version" gorm:"uniqueIndex:idx_thesis_revision,priority:2"`
	Snapshot  ResearchThesis `json:"snapshot" gorm:"serializer:json;type:text"`
	CreatedAt time.Time      `json:"created_at"`
}
