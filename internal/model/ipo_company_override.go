package model

import "time"

type IPOCompanyOverride struct {
	ID             uint       `gorm:"primaryKey" json:"id"`
	CIK            string     `gorm:"size:32;not null;uniqueIndex" json:"cik"`
	StatusOverride string     `gorm:"size:32;index" json:"status_override"`
	FinalTicker    string     `gorm:"size:32;index" json:"final_ticker"`
	Exchange       string     `gorm:"size:64;index" json:"exchange"`
	OfferPrice     string     `gorm:"size:64" json:"offer_price"`
	SharesOffered  int64      `json:"shares_offered"`
	ListingDate    *time.Time `json:"listing_date,omitempty"`
	Note           string     `gorm:"type:text" json:"note"`
	UpdatedAt      time.Time  `json:"updated_at"`
	CreatedAt      time.Time  `json:"created_at"`
}

func (IPOCompanyOverride) TableName() string {
	return "ipo_company_overrides"
}
