package model

import "time"

// TradeSetupNotificationState records the last observed daily trade-plan
// state for an enabled watch target so notifications are transition-based.
type TradeSetupNotificationState struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	TargetID  uint      `gorm:"not null;uniqueIndex" json:"target_id"`
	Ticker    string    `gorm:"size:32;not null;index" json:"ticker"`
	Status    string    `gorm:"size:32;not null;index" json:"status"`
	TradeDate string    `gorm:"size:10" json:"trade_date"`
	UpdatedAt time.Time `json:"updated_at"`
}
