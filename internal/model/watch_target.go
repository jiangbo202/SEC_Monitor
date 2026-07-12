package model

import "time"

type WatchTarget struct {
	ID                 uint       `gorm:"primaryKey" json:"id"`
	Ticker             string     `gorm:"size:32;not null;uniqueIndex" json:"ticker"`
	CompanyName        string     `gorm:"size:255;not null" json:"company_name"`
	CIK                string     `gorm:"size:32;index" json:"cik"`
	TargetType         string     `gorm:"size:32;not null;index" json:"target_type"`
	FundSeriesID       string     `gorm:"size:32;index" json:"fund_series_id,omitempty"`
	FundClassID        string     `gorm:"size:32;index" json:"fund_class_id,omitempty"`
	IdentitySource     string     `gorm:"size:64" json:"identity_source,omitempty"`
	IdentityVerifiedAt *time.Time `json:"identity_verified_at,omitempty"`
	IdentityNote       string     `gorm:"type:text" json:"identity_note,omitempty"`
	Group              string     `gorm:"size:64;index" json:"group"`
	Status             string     `gorm:"size:32;not null;index" json:"status"`
	LastSyncAt         *time.Time `json:"last_sync_at"`
	LastSyncStatus     string     `gorm:"size:32;index" json:"last_sync_status"`
	LastSyncError      string     `gorm:"type:text" json:"last_sync_error"`
	LastNewFilings     int        `json:"last_new_filings"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
}
