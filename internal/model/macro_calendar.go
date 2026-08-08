package model

import "time"

// MacroRelease is one scheduled or published official economic release. The
// event and its values are stored separately so later revisions never replace
// the original, release-time observation.
type MacroRelease struct {
	ID              uint       `gorm:"primaryKey" json:"id"`
	Provider        string     `gorm:"size:32;not null;uniqueIndex:idx_macro_release_provider_source,priority:1;index" json:"provider"`
	Category        string     `gorm:"size:64;not null;index" json:"category"`
	Title           string     `gorm:"size:512;not null" json:"title"`
	ReferencePeriod string     `gorm:"size:64;index" json:"reference_period"`
	ReleaseStage    string     `gorm:"size:32;index" json:"release_stage"`
	Status          string     `gorm:"size:16;not null;index" json:"status"`
	ScheduledAt     *time.Time `gorm:"index" json:"scheduled_at,omitempty"`
	PublishedAt     *time.Time `gorm:"index" json:"published_at,omitempty"`
	SourceURL       string     `gorm:"size:2048;not null;uniqueIndex:idx_macro_release_provider_source,priority:2" json:"source_url"`
	SourceHash      string     `gorm:"size:64" json:"source_hash,omitempty"`
	FetchedAt       time.Time  `gorm:"index" json:"fetched_at"`
	LastError       string     `gorm:"type:text" json:"last_error,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

// MacroObservation preserves the official actual value associated with one
// release. Forecasts are intentionally absent: official agencies do not
// publish market consensus estimates.
type MacroObservation struct {
	ID                uint       `gorm:"primaryKey" json:"id"`
	ReleaseID         uint       `gorm:"not null;uniqueIndex:idx_macro_observation_release_indicator,priority:1;index" json:"release_id"`
	IndicatorCode     string     `gorm:"size:96;not null;uniqueIndex:idx_macro_observation_release_indicator,priority:2;index" json:"indicator_code"`
	IndicatorName     string     `gorm:"size:255;not null" json:"indicator_name"`
	Frequency         string     `gorm:"size:16" json:"frequency"`
	Unit              string     `gorm:"size:64" json:"unit"`
	ActualValue       *float64   `json:"actual_value,omitempty"`
	PreviousValue     *float64   `json:"previous_value,omitempty"`
	PreviousRevised   bool       `json:"previous_revised"`
	SourceField       string     `gorm:"size:255" json:"source_field"`
	SourceURL         string     `gorm:"size:2048" json:"source_url"`
	ProviderUpdatedAt *time.Time `json:"provider_updated_at,omitempty"`
	FetchedAt         time.Time  `json:"fetched_at"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
}
