package model

import "time"

type SystemConfig struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	ConfigKey   string    `gorm:"size:128;not null;uniqueIndex" json:"config_key"`
	ConfigValue string    `gorm:"type:text" json:"config_value"`
	ValueType   string    `gorm:"size:32;not null" json:"value_type"`
	Category    string    `gorm:"size:64;not null;index" json:"category"`
	Encrypted   bool      `gorm:"not null" json:"encrypted"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}
