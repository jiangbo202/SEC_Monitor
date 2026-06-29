package discovery

import "time"

const (
	BatchStatusDraft     = "draft"
	BatchStatusPublished = "published"
	BatchStatusPartial   = "partial"
	BatchStatusFailed    = "failed"
)

const (
	ProviderStatusValidation = "validation"
	ProviderStatusActive     = "active"
	ProviderStatusDegraded   = "degraded"
	ProviderStatusFailed     = "failed"
)

const (
	QualityStatusValid    = "valid"
	QualityStatusStale    = "stale"
	QualityStatusConflict = "conflict"
	QualityStatusMissing  = "missing"
)

const (
	EffectiveStatusPrescreen        = "prescreen"
	EffectiveStatusIncluded         = "included"
	EffectiveStatusExcluded         = "excluded"
	EffectiveStatusDataInsufficient = "data-insufficient"
)

const (
	MappingStatusCurrent  = "current"
	MappingStatusConflict = "conflict"
	MappingStatusExpired  = "expired"
)

const (
	SecurityCatalogStaged    = "staged"
	SecurityCatalogPublished = "published"
)

const (
	ConfidenceHigh   = "high"
	ConfidenceMedium = "medium"
	ConfidenceLow    = "low"
)

type Security struct {
	ID                   uint       `json:"id"`
	CIK                  string     `json:"cik" gorm:"size:10;uniqueIndex"`
	CompanyName          string     `json:"company_name" gorm:"size:255"`
	SIC                  int        `json:"sic" gorm:"index"`
	StateOfIncorporation string     `json:"state_of_incorporation" gorm:"size:8"`
	LatestAnnualForm     string     `json:"latest_annual_form" gorm:"size:16"`
	CatalogStatus        string     `json:"catalog_status" gorm:"size:16;index;default:staged"`
	PublishedAt          *time.Time `json:"published_at"`
	CreatedBatchID       string     `json:"created_batch_id" gorm:"size:64;index"`
	CreatedAt            time.Time  `json:"created_at"`
	UpdatedAt            time.Time  `json:"updated_at"`
}

type Listing struct {
	ID             uint       `json:"id"`
	SecurityID     uint       `json:"security_id" gorm:"uniqueIndex:idx_listing_security_ticker_from,priority:1;index"`
	Ticker         string     `json:"ticker" gorm:"size:32;uniqueIndex:idx_listing_security_ticker_from,priority:2;index"`
	ProviderTicker string     `json:"provider_ticker" gorm:"size:64"`
	Exchange       string     `json:"exchange" gorm:"size:32;index"`
	ValidFrom      time.Time  `json:"valid_from" gorm:"uniqueIndex:idx_listing_security_ticker_from,priority:3"`
	ValidTo        *time.Time `json:"valid_to"`
	Source         string     `json:"source" gorm:"size:64"`
	MappingStatus  string     `json:"mapping_status" gorm:"size:16;index"`
	Security       Security   `json:"-" gorm:"foreignKey:SecurityID;references:ID;constraint:OnUpdate:RESTRICT,OnDelete:RESTRICT"`
}

type ClassificationSnapshot struct {
	ID           uint      `json:"id"`
	BatchID      string    `json:"batch_id" gorm:"size:64;uniqueIndex:idx_classification_batch_security,priority:1;index"`
	SecurityID   uint      `json:"security_id" gorm:"uniqueIndex:idx_classification_batch_security,priority:2;index"`
	Included     bool      `json:"included"`
	Status       string    `json:"status" gorm:"size:32;index"`
	Confidence   string    `json:"confidence" gorm:"size:16"`
	ReasonCode   string    `json:"reason_code" gorm:"size:64"`
	RuleVersion  string    `json:"rule_version" gorm:"size:64"`
	EvidenceJSON string    `json:"evidence_json" gorm:"type:text"`
	CreatedAt    time.Time `json:"created_at"`
	Security     Security  `json:"-" gorm:"foreignKey:SecurityID;references:ID;constraint:OnUpdate:RESTRICT,OnDelete:RESTRICT"`
}

// SecurityBatchIdentity freezes the market identity used by one published
// security batch. Market runs must never reconstruct it from mutable listings.
type SecurityBatchIdentity struct {
	ID                   uint      `json:"id"`
	BatchID              string    `json:"batch_id" gorm:"size:64;uniqueIndex:idx_security_batch_identity,priority:1;index"`
	SecurityID           uint      `json:"security_id" gorm:"uniqueIndex:idx_security_batch_identity,priority:2;index"`
	CIK                  string    `json:"cik" gorm:"size:10"`
	Ticker               string    `json:"ticker" gorm:"size:32;index"`
	ProviderTicker       string    `json:"provider_ticker" gorm:"size:64"`
	Exchange             string    `json:"exchange" gorm:"size:32"`
	MappingStatus        string    `json:"mapping_status" gorm:"size:16"`
	CompanyName          string    `json:"company_name" gorm:"size:255"`
	SIC                  int       `json:"sic"`
	StateOfIncorporation string    `json:"state_of_incorporation" gorm:"size:8"`
	LatestAnnualForm     string    `json:"latest_annual_form" gorm:"size:16"`
	CreatedAt            time.Time `json:"created_at"`
}

type ProviderRun struct {
	ID                 uint      `json:"id"`
	BatchID            string    `json:"batch_id" gorm:"size:64;index:idx_provider_runs_batch"`
	Provider           string    `json:"provider" gorm:"size:64;index:idx_provider_runs_provider"`
	Status             string    `json:"status" gorm:"size:16;index:idx_provider_runs_status"`
	SourceVersion      string    `json:"source_version" gorm:"size:128"`
	SHA256             string    `json:"sha256" gorm:"size:64"`
	EffectiveDate      time.Time `json:"effective_date" gorm:"index:idx_provider_runs_effective_date"`
	RecordCount        int       `json:"record_count"`
	ExpectedCount      int       `json:"expected_count"`
	CoveragePct        float64   `json:"coverage_pct"`
	ValidationErrorPct float64   `json:"validation_error_pct"`
	Timely             bool      `json:"timely"`
	GoldProvider       string    `json:"gold_provider" gorm:"size:64"`
	GoldSourceURL      string    `json:"gold_source_url" gorm:"size:2048"`
	GoldSHA256         string    `json:"gold_sha256" gorm:"size:64"`
	GoldRows           int       `json:"gold_rows"`
	GoldErrorPct       float64   `json:"gold_error_pct"`
	ErrorMessage       string    `json:"error_message" gorm:"type:text"`
	CreatedAt          time.Time `json:"created_at"`
}

type ProviderHealth struct {
	Provider             string    `json:"provider" gorm:"size:64;primaryKey;autoIncrement:false"`
	Status               string    `json:"status" gorm:"size:16;index"`
	QualifiedTradingDays int       `json:"qualified_trading_days"`
	FailureStreak        int       `json:"failure_streak"`
	LastTradeDate        string    `json:"last_trade_date" gorm:"size:10"`
	WindowJSON           string    `json:"window_json" gorm:"type:text"`
	GoldEvidenceReady    bool      `json:"gold_evidence_ready"`
	GoldSHA256           string    `json:"gold_sha256" gorm:"size:64"`
	UpdatedAt            time.Time `json:"updated_at"`
}

type MarketHoliday struct {
	Date            string    `json:"date" gorm:"size:10;primaryKey;autoIncrement:false"`
	Name            string    `json:"name" gorm:"size:128"`
	CalendarVersion string    `json:"calendar_version" gorm:"size:64;primaryKey;autoIncrement:false;index:idx_market_holidays_calendar_version"`
	SourceURL       string    `json:"source_url" gorm:"size:2048"`
	ReviewedBy      string    `json:"reviewed_by" gorm:"size:128"`
	CompleteYear    bool      `json:"complete_year"`
	ReviewedAt      time.Time `json:"reviewed_at"`
}

type MarketCalendarYear struct {
	CalendarVersion      string    `json:"calendar_version" gorm:"size:64;primaryKey;autoIncrement:false"`
	Year                 int       `json:"year" gorm:"primaryKey;autoIncrement:false"`
	Complete             bool      `json:"complete"`
	ExpectedHolidayCount int       `json:"expected_holiday_count"`
	HolidayDatesSHA256   string    `json:"holiday_dates_sha256" gorm:"size:64"`
	SourceURL            string    `json:"source_url" gorm:"size:2048"`
	ReviewedBy           string    `json:"reviewed_by" gorm:"size:128"`
	ReviewedAt           time.Time `json:"reviewed_at"`
}

type PriceSnapshot struct {
	ID            uint      `json:"id"`
	Source        string    `json:"source" gorm:"size:64;uniqueIndex:idx_price_source_version_symbol_date,priority:1"`
	SourceVersion string    `json:"source_version" gorm:"size:128;uniqueIndex:idx_price_source_version_symbol_date,priority:2"`
	Symbol        string    `json:"symbol" gorm:"size:64;uniqueIndex:idx_price_source_version_symbol_date,priority:3;index"`
	TradeDate     time.Time `json:"trade_date" gorm:"uniqueIndex:idx_price_source_version_symbol_date,priority:4;index"`
	CloseMicros   int64     `json:"close_micros"`
	Volume        int64     `json:"volume"`
	Currency      string    `json:"currency" gorm:"size:8"`
	Adjusted      bool      `json:"adjusted"`
	QualityStatus string    `json:"quality_status" gorm:"size:16;index"`
	CreatedAt     time.Time `json:"created_at"`
}

type ShareSnapshot struct {
	ID            uint      `json:"id"`
	SecurityID    uint      `json:"security_id" gorm:"uniqueIndex:idx_share_security_instant_accession,priority:1;index"`
	Instant       time.Time `json:"instant" gorm:"uniqueIndex:idx_share_security_instant_accession,priority:2"`
	Accession     string    `json:"accession" gorm:"size:32;uniqueIndex:idx_share_security_instant_accession,priority:3"`
	Concept       string    `json:"concept" gorm:"size:128"`
	Form          string    `json:"form" gorm:"size:16"`
	SourceURL     string    `json:"source_url" gorm:"size:2048"`
	QualityStatus string    `json:"quality_status" gorm:"size:16;index"`
	Shares        int64     `json:"shares"`
	FiledAt       time.Time `json:"filed_at"`
	AcceptedAt    time.Time `json:"accepted_at"`
	CreatedAt     time.Time `json:"created_at"`
	Security      Security  `json:"-" gorm:"foreignKey:SecurityID;references:ID;constraint:OnUpdate:RESTRICT,OnDelete:RESTRICT"`
}

type UniverseBatch struct {
	BatchID               string                    `json:"batch_id" gorm:"size:64;primaryKey"`
	Kind                  string                    `json:"kind" gorm:"size:32;index"`
	Status                string                    `json:"status" gorm:"size:16;index"`
	EffectiveDate         string                    `json:"effective_date" gorm:"size:10;index"`
	SourceVersionsJSON    string                    `json:"source_versions_json" gorm:"type:text"`
	ContentSHA256         string                    `json:"content_sha256" gorm:"size:64"`
	RecordCount           int                       `json:"record_count"`
	UniverseSourceVersion string                    `json:"universe_source_version" gorm:"size:128"`
	PriceSourceVersion    string                    `json:"price_source_version" gorm:"size:128"`
	ShareSourceVersion    string                    `json:"share_source_version" gorm:"size:128"`
	StartedAt             time.Time                 `json:"started_at"`
	CompletedAt           *time.Time                `json:"completed_at"`
	ErrorMessage          string                    `json:"error_message" gorm:"type:text"`
	Classifications       []ClassificationSnapshot  `json:"-" gorm:"foreignKey:BatchID;references:BatchID;constraint:OnUpdate:RESTRICT,OnDelete:RESTRICT"`
	Identities            []SecurityBatchIdentity   `json:"-" gorm:"foreignKey:BatchID;references:BatchID;constraint:OnUpdate:RESTRICT,OnDelete:RESTRICT"`
	ListingIdentities     []ListingIdentitySnapshot `json:"-" gorm:"foreignKey:BatchID;references:BatchID;constraint:OnUpdate:RESTRICT,OnDelete:RESTRICT"`
	ProviderRuns          []ProviderRun             `json:"-" gorm:"foreignKey:BatchID;references:BatchID;constraint:OnUpdate:RESTRICT,OnDelete:RESTRICT"`
	Snapshots             []UniverseSnapshot        `json:"-" gorm:"foreignKey:BatchID;references:BatchID;constraint:OnUpdate:RESTRICT,OnDelete:RESTRICT"`
	ShareSelections       []BatchShareSelection     `json:"-" gorm:"foreignKey:BatchID;references:BatchID;constraint:OnUpdate:RESTRICT,OnDelete:RESTRICT"`
	CurrentPointers       []CurrentBatchPointer     `json:"-" gorm:"foreignKey:BatchID;references:BatchID;constraint:OnUpdate:RESTRICT,OnDelete:RESTRICT"`
}

// ListingIdentitySnapshot stages every exchange listing, including identities
// that cannot safely be attached to a Security row without inventing a CIK.
type ListingIdentitySnapshot struct {
	ID             uint      `json:"id"`
	BatchID        string    `json:"batch_id" gorm:"size:64;uniqueIndex:idx_listing_identity_batch_key,priority:1;index"`
	SourceKey      string    `json:"source_key" gorm:"size:128;uniqueIndex:idx_listing_identity_batch_key,priority:2"`
	CIK            string    `json:"cik" gorm:"size:10"`
	Ticker         string    `json:"ticker" gorm:"size:32;index"`
	ProviderTicker string    `json:"provider_ticker" gorm:"size:64"`
	Exchange       string    `json:"exchange" gorm:"size:32"`
	CompanyName    string    `json:"company_name" gorm:"size:255"`
	MappingStatus  string    `json:"mapping_status" gorm:"size:16"`
	Included       bool      `json:"included"`
	Status         string    `json:"status" gorm:"size:32"`
	ReasonCode     string    `json:"reason_code" gorm:"size:64"`
	EvidenceJSON   string    `json:"evidence_json" gorm:"type:text"`
	CreatedAt      time.Time `json:"created_at"`
}

type CurrentBatchPointer struct {
	Kind      string    `json:"kind" gorm:"size:32;primaryKey;autoIncrement:false"`
	BatchID   string    `json:"batch_id" gorm:"size:64;uniqueIndex"`
	UpdatedAt time.Time `json:"updated_at"`
}

type BatchShareSelection struct {
	ID              uint           `json:"id"`
	BatchID         string         `json:"batch_id" gorm:"size:64;uniqueIndex:idx_batch_share_security,priority:1;index"`
	SecurityID      uint           `json:"security_id" gorm:"uniqueIndex:idx_batch_share_security,priority:2;index"`
	ShareSnapshotID *uint          `json:"share_snapshot_id"`
	QualityStatus   string         `json:"quality_status" gorm:"size:16"`
	ReasonCode      string         `json:"reason_code" gorm:"size:64"`
	CreatedAt       time.Time      `json:"created_at"`
	Security        Security       `json:"-" gorm:"foreignKey:SecurityID;references:ID;constraint:OnUpdate:RESTRICT,OnDelete:RESTRICT"`
	ShareEvidence   *ShareSnapshot `json:"-" gorm:"foreignKey:ShareSnapshotID;references:ID;constraint:OnUpdate:RESTRICT,OnDelete:RESTRICT"`
}

type UniverseSnapshot struct {
	ID              uint           `json:"id"`
	BatchID         string         `json:"batch_id" gorm:"size:64;uniqueIndex:idx_universe_batch_security,priority:1;index"`
	SecurityID      uint           `json:"security_id" gorm:"uniqueIndex:idx_universe_batch_security,priority:2;index"`
	Ticker          string         `json:"ticker" gorm:"size:32;index:idx_universe_snapshots_ticker"`
	MarketCapUSD    int64          `json:"market_cap_usd" gorm:"index:idx_universe_snapshots_market_cap"`
	Included        bool           `json:"included" gorm:"index:idx_universe_snapshots_included"`
	Status          string         `json:"status" gorm:"size:32;index:idx_universe_snapshots_status"`
	ReasonCode      string         `json:"reason_code" gorm:"size:64"`
	QualityStatus   string         `json:"quality_status" gorm:"size:16"`
	PriceSnapshotID *uint          `json:"price_snapshot_id"`
	ShareSnapshotID *uint          `json:"share_snapshot_id"`
	CreatedAt       time.Time      `json:"created_at"`
	Security        Security       `json:"-" gorm:"foreignKey:SecurityID;references:ID;constraint:OnUpdate:RESTRICT,OnDelete:RESTRICT"`
	PriceEvidence   *PriceSnapshot `json:"-" gorm:"foreignKey:PriceSnapshotID;references:ID;constraint:OnUpdate:RESTRICT,OnDelete:RESTRICT"`
	ShareEvidence   *ShareSnapshot `json:"-" gorm:"foreignKey:ShareSnapshotID;references:ID;constraint:OnUpdate:RESTRICT,OnDelete:RESTRICT"`
}

type ManualSecurityOverride struct {
	ID              uint      `json:"id"`
	SecurityID      uint      `json:"security_id" gorm:"index"`
	EffectiveStatus string    `json:"effective_status" gorm:"size:32"`
	Reason          string    `json:"reason" gorm:"size:255"`
	SourceURL       string    `json:"source_url" gorm:"size:2048"`
	Operator        string    `json:"operator" gorm:"size:128"`
	Active          bool      `json:"active" gorm:"index"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
	Security        Security  `json:"-" gorm:"foreignKey:SecurityID;references:ID;constraint:OnUpdate:RESTRICT,OnDelete:RESTRICT"`
}

type IdentityVerificationOverride struct {
	ID         uint      `json:"id"`
	CIK        string    `json:"cik" gorm:"size:10;uniqueIndex:idx_identity_override"`
	Ticker     string    `json:"ticker" gorm:"size:32;uniqueIndex:idx_identity_override"`
	VerifiedAt time.Time `json:"verified_at"`
	SourceURL  string    `json:"source_url" gorm:"size:2048"`
	Operator   string    `json:"operator" gorm:"size:128"`
	Active     bool      `json:"active" gorm:"index"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type Evidence struct {
	Field  string `json:"field"`
	Value  string `json:"value"`
	Source string `json:"source"`
}

type SourceVersion struct {
	Source      string    `json:"source"`
	Version     string    `json:"version"`
	SHA256      string    `json:"sha256"`
	EffectiveAt time.Time `json:"effective_at"`
}
