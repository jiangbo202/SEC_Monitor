package model

import "time"

type OperationLog struct {
	ID         uint      `gorm:"primaryKey" json:"id"`
	OperatedAt time.Time `gorm:"index" json:"operated_at"`
	Operator   string    `gorm:"size:128;index" json:"operator"`
	Action     string    `gorm:"size:64;not null;index" json:"action"`
	ObjectType string    `gorm:"size:64;not null;index" json:"object_type"`
	ObjectID   string    `gorm:"size:128;index" json:"object_id"`
	BeforeData string    `gorm:"type:text" json:"before_data,omitempty"`
	AfterData  string    `gorm:"type:text" json:"after_data,omitempty"`
	IP         string    `gorm:"size:64" json:"ip,omitempty"`
	UserAgent  string    `gorm:"size:255" json:"user_agent,omitempty"`
}
