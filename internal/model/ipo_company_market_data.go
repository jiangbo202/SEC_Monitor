package model

import "time"

type IPOCompanyMarketData struct {
	ID                           uint       `gorm:"primaryKey" json:"id"`
	CIK                          string     `gorm:"size:32;not null;uniqueIndex" json:"cik"`
	Ticker                       string     `gorm:"size:32;index" json:"ticker,omitempty"`
	Exchange                     string     `gorm:"size:64;index" json:"exchange,omitempty"`
	OfferPrice                   string     `gorm:"size:64" json:"offer_price,omitempty"`
	SharesOffered                int64      `json:"shares_offered,omitempty"`
	GrossProceeds                string     `gorm:"size:64" json:"gross_proceeds,omitempty"`
	OfferingCheckedAt            *time.Time `json:"offering_checked_at,omitempty"`
	OfferingParserVersion        int        `json:"offering_parser_version,omitempty"`
	LifecycleCheckedAt           *time.Time `json:"lifecycle_checked_at,omitempty"`
	ListedVerifiedAt             *time.Time `json:"listed_verified_at,omitempty"`
	ListingDate                  *time.Time `json:"listing_date,omitempty"`
	ListingCheckedAt             *time.Time `json:"listing_checked_at,omitempty"`
	LongbridgeListingCheckCount  int        `json:"longbridge_listing_check_count,omitempty"`
	LongbridgeListingLastResult  string     `gorm:"size:32" json:"longbridge_listing_last_result,omitempty"`
	LongbridgeListingNextRetryAt *time.Time `json:"longbridge_listing_next_retry_at,omitempty"`
	TickerSource                 string     `gorm:"type:text" json:"ticker_source,omitempty"`
	ListingSource                string     `gorm:"type:text" json:"listing_source,omitempty"`
	OfferingSource               string     `gorm:"type:text" json:"offering_source,omitempty"`
	TickerConfidence             string     `gorm:"size:32" json:"ticker_confidence,omitempty"`
	ListingConfidence            string     `gorm:"size:32" json:"listing_confidence,omitempty"`
	OfferingConfidence           string     `gorm:"size:32" json:"offering_confidence,omitempty"`
	CreatedAt                    time.Time  `json:"created_at"`
	UpdatedAt                    time.Time  `json:"updated_at"`
}

func (IPOCompanyMarketData) TableName() string {
	return "ipo_company_market_data"
}
