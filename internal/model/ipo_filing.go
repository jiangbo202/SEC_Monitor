package model

import "time"

type IPOFiling struct {
	ID              uint       `gorm:"primaryKey" json:"id"`
	FilingID        string     `gorm:"size:255;not null;uniqueIndex" json:"filing_id"`
	AccessionNumber string     `gorm:"size:128;index" json:"accession_number"`
	CIK             string     `gorm:"size:32;index" json:"cik"`
	CompanyName     string     `gorm:"size:255;not null;index" json:"company_name"`
	FilingType      string     `gorm:"size:64;not null;index" json:"filing_type"`
	FilingDate      time.Time  `gorm:"index" json:"filing_date"`
	AcceptedAt      *time.Time `gorm:"index" json:"accepted_at"`
	FilingURL       string     `gorm:"type:text" json:"filing_url"`
	Title           string     `gorm:"type:text" json:"title"`
	NotifiedAt      *time.Time `json:"notified_at"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}
