package model

import "time"

type IPOCompanyMarketData struct {
	ID                    uint       `gorm:"primaryKey" json:"id"`
	CIK                   string     `gorm:"size:32;not null;uniqueIndex" json:"cik"`
	Ticker                string     `gorm:"size:32;index" json:"ticker,omitempty"`
	Exchange              string     `gorm:"size:64;index" json:"exchange,omitempty"`
	OfferPrice            string     `gorm:"size:64" json:"offer_price,omitempty"`
	SharesOffered         int64      `json:"shares_offered,omitempty"`
	GrossProceeds         string     `gorm:"size:64" json:"gross_proceeds,omitempty"`
	OfferingCheckedAt     *time.Time `json:"offering_checked_at,omitempty"`
	OfferingParserVersion int        `json:"offering_parser_version,omitempty"`
	ListedVerifiedAt      *time.Time `json:"listed_verified_at,omitempty"`
	TickerSource          string     `gorm:"type:text" json:"ticker_source,omitempty"`
	OfferingSource        string     `gorm:"type:text" json:"offering_source,omitempty"`
	TickerConfidence      string     `gorm:"size:32" json:"ticker_confidence,omitempty"`
	OfferingConfidence    string     `gorm:"size:32" json:"offering_confidence,omitempty"`
	CreatedAt             time.Time  `json:"created_at"`
	UpdatedAt             time.Time  `json:"updated_at"`
}

func (IPOCompanyMarketData) TableName() string {
	return "ipo_company_market_data"
}
