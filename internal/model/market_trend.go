package model

import "time"

// MarketTrendDaily stores one completed US-market daily bar used by the
// market-trend research screen. It is intentionally separate from watched
// securities so index and sector ETF evidence remains available locally.
type MarketTrendDaily struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	Symbol    string    `gorm:"size:32;not null;uniqueIndex:idx_market_trend_symbol_date,priority:1;index" json:"symbol"`
	Label     string    `gorm:"size:96;not null" json:"label"`
	Group     string    `gorm:"column:group_name;size:16;not null;index" json:"group"`
	SortOrder int       `gorm:"not null" json:"sort_order"`
	TradeDate string    `gorm:"size:10;not null;uniqueIndex:idx_market_trend_symbol_date,priority:2;index" json:"trade_date"`
	Open      float64   `json:"open"`
	High      float64   `json:"high"`
	Low       float64   `json:"low"`
	Close     float64   `json:"close"`
	Volume    int64     `json:"volume"`
	Source    string    `gorm:"size:32;not null" json:"source"`
	FetchedAt time.Time `gorm:"index" json:"fetched_at"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// MarketTemperatureDaily is Longbridge's market-level 0-100 composite of
// valuation and sentiment. It is research context, not a trading signal.
type MarketTemperatureDaily struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	Market      string    `gorm:"size:8;not null;uniqueIndex:idx_market_temperature_market_date,priority:1" json:"market"`
	TradeDate   string    `gorm:"size:10;not null;uniqueIndex:idx_market_temperature_market_date,priority:2;index" json:"trade_date"`
	Temperature int       `json:"temperature"`
	Valuation   int       `json:"valuation"`
	Sentiment   int       `json:"sentiment"`
	Description string    `gorm:"size:255" json:"description,omitempty"`
	Source      string    `gorm:"size:32;not null" json:"source"`
	FetchedAt   time.Time `gorm:"index" json:"fetched_at"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}
