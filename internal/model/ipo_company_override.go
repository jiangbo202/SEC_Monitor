package model

import "time"

type IPOCompanyOverride struct {
	ID             uint      `gorm:"primaryKey" json:"id"`
	CIK            string    `gorm:"size:32;not null;uniqueIndex" json:"cik"`
	StatusOverride string    `gorm:"size:32;index" json:"status_override"`
	FinalTicker    string    `gorm:"size:32;index" json:"final_ticker"`
	Note           string    `gorm:"type:text" json:"note"`
	UpdatedAt      time.Time `json:"updated_at"`
	CreatedAt      time.Time `json:"created_at"`
}

func (IPOCompanyOverride) TableName() string {
	return "ipo_company_overrides"
}
