package model

import "time"

type IPOOfferingEvent struct {
	ID            uint       `gorm:"primaryKey" json:"id"`
	FilingID      string     `gorm:"size:255;uniqueIndex;not null" json:"filing_id"`
	CIK           string     `gorm:"size:32;index;not null" json:"cik"`
	CompanyName   string     `gorm:"size:255" json:"company_name"`
	OfferingType  string     `gorm:"size:32;index;not null" json:"offering_type"`
	ParseStatus   string     `gorm:"size:32;index;not null" json:"parse_status"`
	OfferPrice    string     `gorm:"size:64" json:"offer_price"`
	SharesOffered int64      `json:"shares_offered"`
	GrossProceeds string     `gorm:"size:64" json:"gross_proceeds"`
	Fingerprint   string     `gorm:"size:64;index" json:"fingerprint"`
	FilingURL     string     `gorm:"type:text" json:"filing_url"`
	FilingDate    time.Time  `gorm:"index" json:"filing_date"`
	AcceptedAt    *time.Time `gorm:"index" json:"accepted_at,omitempty"`
	ParserVersion int        `json:"parser_version"`
	NotifiedAt    *time.Time `json:"notified_at,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

func (IPOOfferingEvent) TableName() string {
	return "ipo_offering_events"
}
