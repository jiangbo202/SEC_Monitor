package model

import "time"

type FundFilingIdentity struct {
	ID                uint      `gorm:"primaryKey"`
	CIK               string    `gorm:"size:32;not null;uniqueIndex:idx_fund_filing_identity"`
	AccessionNumber   string    `gorm:"size:128;not null;uniqueIndex:idx_fund_filing_identity"`
	SeriesIDsJSON     string    `gorm:"type:text"`
	ClassIDsJSON      string    `gorm:"type:text"`
	RelationshipsJSON string    `gorm:"type:text"`
	ParseStatus       string    `gorm:"size:32;not null;index"`
	ParseMessage      string    `gorm:"type:text"`
	CheckedAt         time.Time `gorm:"index"`
}
