package discovery

import "time"

// A provider-reported aggregate is not a sum of Longbridge major-holder rows.
type FutuInstitutionalPoint struct {
	ID                        uint       `gorm:"primaryKey" json:"-"`
	Ticker                    string     `gorm:"uniqueIndex:idx_futu_ownership_period,priority:1" json:"ticker"`
	Period                    string     `gorm:"uniqueIndex:idx_futu_ownership_period,priority:2" json:"period"`
	InstitutionQuantity       *int64     `json:"institution_quantity"`
	InstitutionQuantityChange *int64     `json:"institution_quantity_change"`
	HolderQuantity            *int64     `json:"holder_quantity"`
	HolderQuantityChange      *int64     `json:"holder_quantity_change"`
	HolderPct                 *float64   `json:"holder_pct"`
	HolderPctChange           *float64   `json:"holder_pct_change"`
	ProviderUpdatedAt         *time.Time `json:"provider_updated_at,omitempty"`
	FetchedAt                 time.Time  `json:"fetched_at"`
	SourceURL                 string     `json:"source_url"`
}

type FutuInstitutionalReceipt struct {
	Ticker    string    `gorm:"primaryKey" json:"ticker"`
	Status    string    `json:"status"`
	NextKey   string    `json:"-"`
	FetchedAt time.Time `json:"fetched_at"`
}
