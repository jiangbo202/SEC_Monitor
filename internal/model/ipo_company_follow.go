package model

import "time"

// IPOCompanyFollow is a local, single-user follow list keyed by SEC CIK.
// LastProgressKey is the baseline used to notify only changes occurring after
// the company has been followed.
type IPOCompanyFollow struct {
	ID              uint      `gorm:"primaryKey" json:"id"`
	CIK             string    `gorm:"size:32;not null;uniqueIndex" json:"cik"`
	CompanyName     string    `gorm:"size:255;not null" json:"company_name"`
	LastProgressKey string    `gorm:"size:128" json:"last_progress_key"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}
