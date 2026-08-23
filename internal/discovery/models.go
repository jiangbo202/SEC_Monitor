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
	SICDescription       string     `json:"sic_description" gorm:"size:255"`
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

// CompanyProfileSnapshot caches a provider-issued company overview separately
// from SEC issuer metadata. It lets detail pages remain local/read-only while
// making the external source and freshness explicit to researchers.
type CompanyProfileSnapshot struct {
	ID            uint       `json:"id"`
	SecurityID    uint       `json:"security_id" gorm:"uniqueIndex:idx_company_profile_provider_security,priority:2;index"`
	Provider      string     `json:"provider" gorm:"size:32;uniqueIndex:idx_company_profile_provider_security,priority:1;index"`
	Ticker        string     `json:"ticker" gorm:"size:32;index"`
	CompanyName   string     `json:"company_name" gorm:"size:255"`
	Profile       string     `json:"profile" gorm:"type:text"`
	Website       string     `json:"website" gorm:"size:1024"`
	Founded       string     `json:"founded" gorm:"size:64"`
	ListingDate   string     `json:"listing_date" gorm:"size:64"`
	Market        string     `json:"market" gorm:"size:128"`
	Address       string     `json:"address" gorm:"size:1024"`
	Employees     string     `json:"employees" gorm:"size:64"`
	Manager       string     `json:"manager" gorm:"size:255"`
	YearEnd       string     `json:"year_end" gorm:"size:64"`
	FetchedAt     *time.Time `json:"fetched_at" gorm:"index"`
	LastAttemptAt *time.Time `json:"last_attempt_at"`
	LastError     string     `json:"last_error" gorm:"type:text"`
	RetryCount    int        `json:"retry_count"`
	NextRetryAt   *time.Time `json:"next_retry_at" gorm:"index"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

// AnalystRatingSnapshot is an immutable, provider-issued analyst consensus
// observation. The application deliberately stores the provider aggregate,
// not an inferred per-analyst recommendation. A missing coverage result is a
// valid observation for small and micro-cap issuers, not a synchronisation
// failure.
type AnalystRatingSnapshot struct {
	ID                    uint       `json:"id"`
	SecurityID            uint       `json:"security_id" gorm:"index"`
	Provider              string     `json:"provider" gorm:"size:32;uniqueIndex:idx_analyst_rating_provider_ticker_hash,priority:1;index"`
	Ticker                string     `json:"ticker" gorm:"size:32;uniqueIndex:idx_analyst_rating_provider_ticker_hash,priority:2;index"`
	Status                string     `json:"status" gorm:"size:32;index"`
	Recommendation        string     `json:"recommendation" gorm:"size:32"`
	StrongBuyCount        int        `json:"strong_buy_count"`
	BuyCount              int        `json:"buy_count"`
	HoldCount             int        `json:"hold_count"`
	UnderperformCount     int        `json:"underperform_count"`
	SellCount             int        `json:"sell_count"`
	NoOpinionCount        int        `json:"no_opinion_count"`
	AnalystCount          int        `json:"analyst_count"`
	TargetAverageMicros   int64      `json:"target_average_micros"`
	TargetHighMicros      int64      `json:"target_high_micros"`
	TargetLowMicros       int64      `json:"target_low_micros"`
	ReferencePriceMicros  int64      `json:"reference_price_micros"`
	Currency              string     `json:"currency" gorm:"size:16"`
	ProviderUpdatedAtText string     `json:"provider_updated_at_text" gorm:"size:255"`
	SnapshotHash          string     `json:"snapshot_hash" gorm:"size:64;uniqueIndex:idx_analyst_rating_provider_ticker_hash,priority:3"`
	ChangeSummary         string     `json:"change_summary" gorm:"type:text"`
	NotificationStatus    string     `json:"notification_status" gorm:"size:32;index"`
	FetchedAt             time.Time  `json:"fetched_at" gorm:"index"`
	NotifiedAt            *time.Time `json:"notified_at,omitempty"`
	CreatedAt             time.Time  `json:"created_at"`
}

// EPSForecastSnapshot is an immutable provider-issued consensus EPS snapshot.
// It is research context only and never contributes to the fundamental score.
type EPSForecastSnapshot struct {
	ID                 uint      `json:"id"`
	SecurityID         uint      `json:"security_id" gorm:"index"`
	Provider           string    `json:"provider" gorm:"size:32;uniqueIndex:idx_eps_forecast_provider_ticker_hash,priority:1;index"`
	Ticker             string    `json:"ticker" gorm:"size:32;uniqueIndex:idx_eps_forecast_provider_ticker_hash,priority:2;index"`
	ForecastStartDate  time.Time `json:"forecast_start_date" gorm:"index"`
	ForecastEndDate    time.Time `json:"forecast_end_date" gorm:"index"`
	Mean               *float64  `json:"mean,omitempty"`
	Median             *float64  `json:"median,omitempty"`
	Low                *float64  `json:"low,omitempty"`
	High               *float64  `json:"high,omitempty"`
	InstitutionTotal   int       `json:"institution_total"`
	InstitutionUp      int       `json:"institution_up"`
	InstitutionDown    int       `json:"institution_down"`
	SnapshotHash       string    `json:"snapshot_hash" gorm:"size:64;uniqueIndex:idx_eps_forecast_provider_ticker_hash,priority:3"`
	ChangeSummary      string    `json:"change_summary" gorm:"type:text"`
	NotificationStatus string    `json:"notification_status" gorm:"size:32;index"`
	FetchedAt          time.Time `json:"fetched_at" gorm:"index"`
	CreatedAt          time.Time `json:"created_at"`
}

// MarketAnomalySnapshot records a provider-issued intraday anomaly. It is a
// short-term review signal only; it does not alter the fundamental score.
type MarketAnomalySnapshot struct {
	ID         uint      `json:"id"`
	SecurityID uint      `json:"security_id" gorm:"index"`
	Provider   string    `json:"provider" gorm:"size:32;uniqueIndex:idx_market_anomaly_provider_event,priority:1;index"`
	Ticker     string    `json:"ticker" gorm:"size:32;index"`
	EventKey   string    `json:"event_key" gorm:"size:128;uniqueIndex:idx_market_anomaly_provider_event,priority:2"`
	Name       string    `json:"name" gorm:"size:255"`
	AlertName  string    `json:"alert_name" gorm:"size:255"`
	AlertTime  time.Time `json:"alert_time" gorm:"index"`
	ValuesJSON string    `json:"values_json" gorm:"type:text"`
	Emotion    int       `json:"emotion"`
	FetchedAt  time.Time `json:"fetched_at" gorm:"index"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// InstitutionalHolderSnapshot preserves a reported major-holder position.
type InstitutionalHolderSnapshot struct {
	ID              uint      `json:"id"`
	SecurityID      uint      `json:"security_id" gorm:"index"`
	Provider        string    `json:"provider" gorm:"size:32;uniqueIndex:idx_institutional_holder_identity,priority:1;index"`
	Ticker          string    `json:"ticker" gorm:"size:32;uniqueIndex:idx_institutional_holder_identity,priority:2;index"`
	HolderName      string    `json:"holder_name" gorm:"size:255;uniqueIndex:idx_institutional_holder_identity,priority:3"`
	InstitutionType string    `json:"institution_type" gorm:"size:128"`
	PercentOfShares *float64  `json:"percent_of_shares,omitempty"`
	SharesChanged   *float64  `json:"shares_changed,omitempty"`
	ReportDate      string    `json:"report_date" gorm:"size:32;uniqueIndex:idx_institutional_holder_identity,priority:4;index"`
	SourceURL       string    `json:"source_url" gorm:"size:2048"`
	FetchedAt       time.Time `json:"fetched_at" gorm:"index"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// FundHolderSnapshot records a fund or ETF's disclosed portfolio weight.
type FundHolderSnapshot struct {
	ID            uint      `json:"id"`
	SecurityID    uint      `json:"security_id" gorm:"index"`
	Provider      string    `json:"provider" gorm:"size:32;uniqueIndex:idx_fund_holder_identity,priority:1;index"`
	Ticker        string    `json:"ticker" gorm:"size:32;uniqueIndex:idx_fund_holder_identity,priority:2;index"`
	FundCode      string    `json:"fund_code" gorm:"size:64;uniqueIndex:idx_fund_holder_identity,priority:3"`
	FundSymbol    string    `json:"fund_symbol" gorm:"size:96"`
	FundName      string    `json:"fund_name" gorm:"size:255"`
	Currency      string    `json:"currency" gorm:"size:16"`
	PositionRatio float64   `json:"position_ratio"`
	ReportDate    string    `json:"report_date" gorm:"size:32;uniqueIndex:idx_fund_holder_identity,priority:4;index"`
	SourceURL     string    `json:"source_url" gorm:"size:2048"`
	FetchedAt     time.Time `json:"fetched_at" gorm:"index"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// LongbridgeValuationSnapshot keeps a provider-issued valuation research
// payload (history, industry percentiles and peers) separate from the local
// SEC-derived valuation used by the candidate workflow.
type LongbridgeValuationSnapshot struct {
	ID            uint      `json:"id"`
	SecurityID    uint      `json:"security_id" gorm:"index"`
	Provider      string    `json:"provider" gorm:"size:32;uniqueIndex:idx_longbridge_valuation_provider_ticker_hash,priority:1;index"`
	Ticker        string    `json:"ticker" gorm:"size:32;uniqueIndex:idx_longbridge_valuation_provider_ticker_hash,priority:2;index"`
	SnapshotHash  string    `json:"snapshot_hash" gorm:"size:64;uniqueIndex:idx_longbridge_valuation_provider_ticker_hash,priority:3"`
	PayloadJSON   string    `json:"-" gorm:"type:text"`
	ChangeSummary string    `json:"change_summary" gorm:"type:text"`
	FetchedAt     time.Time `json:"fetched_at" gorm:"index"`
	CreatedAt     time.Time `json:"created_at"`
}

// OptionResearchSnapshot stores one compact, daily options/short-interest
// observation. It intentionally does not store a full option chain: that data
// is large and changes intraday, while P0 is a reproducible signal dashboard.
type OptionResearchSnapshot struct {
	ID                  uint                    `json:"id"`
	SecurityID          uint                    `json:"security_id" gorm:"index"`
	Provider            string                  `json:"provider" gorm:"size:32;uniqueIndex:idx_option_research_provider_ticker_day,priority:1;index"`
	Ticker              string                  `json:"ticker" gorm:"size:32;uniqueIndex:idx_option_research_provider_ticker_day,priority:2;index"`
	ObservedDate        string                  `json:"observed_date" gorm:"size:16;uniqueIndex:idx_option_research_provider_ticker_day,priority:3;index"`
	Status              string                  `json:"status" gorm:"size:24;index"`
	CallVolume          *int64                  `json:"call_volume,omitempty"`
	PutVolume           *int64                  `json:"put_volume,omitempty"`
	PutCallVolumeRatio  *float64                `json:"put_call_volume_ratio,omitempty"`
	OptionVolumeAsOf    string                  `json:"option_volume_as_of,omitempty"`
	ShortRatioPct       *float64                `json:"short_ratio_pct,omitempty"`
	CurrentSharesShort  *int64                  `json:"current_shares_short,omitempty"`
	AvgDailyShareVolume *int64                  `json:"avg_daily_share_volume,omitempty"`
	DaysToCover         *float64                `json:"days_to_cover,omitempty"`
	ShortReportedAt     string                  `json:"short_reported_at,omitempty"`
	AnomaliesJSON       string                  `json:"-" gorm:"type:text"`
	FetchedAt           time.Time               `json:"fetched_at" gorm:"index"`
	CreatedAt           time.Time               `json:"created_at"`
	UpdatedAt           time.Time               `json:"updated_at"`
	Anomalies           []OptionResearchAnomaly `json:"anomalies" gorm:"-"`
}

type OptionResearchAnomaly struct {
	Kind     string `json:"kind"`
	Severity string `json:"severity"`
	Label    string `json:"label"`
	Detail   string `json:"detail"`
}

// LongbridgeResearchRefreshState is a shared freshness cursor for a ticker and
// one Longbridge data family. Candidate and watch-target jobs read the same
// cursor so overlapping symbols are fetched once per local research day.
type LongbridgeResearchRefreshState struct {
	ID            uint       `json:"id"`
	Ticker        string     `json:"ticker" gorm:"size:32;uniqueIndex:idx_longbridge_refresh_ticker_family,priority:1;index"`
	Family        string     `json:"family" gorm:"size:32;uniqueIndex:idx_longbridge_refresh_ticker_family,priority:2;index"`
	LastAttemptAt time.Time  `json:"last_attempt_at"`
	LastSuccessAt *time.Time `json:"last_success_at,omitempty" gorm:"index"`
	Status        string     `json:"status" gorm:"size:16;index"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
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
	SICDescription       string    `json:"sic_description" gorm:"size:255"`
	StateOfIncorporation string    `json:"state_of_incorporation" gorm:"size:8"`
	LatestAnnualForm     string    `json:"latest_annual_form" gorm:"size:16"`
	CreatedAt            time.Time `json:"created_at"`
}

type ProviderRun struct {
	ID                 uint              `json:"id"`
	BatchID            string            `json:"batch_id" gorm:"size:64;index:idx_provider_runs_batch"`
	Provider           string            `json:"provider" gorm:"size:64;index:idx_provider_runs_provider"`
	Status             string            `json:"status" gorm:"size:16;index:idx_provider_runs_status"`
	SourceVersion      string            `json:"source_version" gorm:"size:128"`
	SHA256             string            `json:"sha256" gorm:"size:64"`
	EffectiveDate      time.Time         `json:"effective_date" gorm:"index:idx_provider_runs_effective_date"`
	RecordCount        int               `json:"record_count"`
	ExpectedCount      int               `json:"expected_count"`
	CoveragePct        float64           `json:"coverage_pct"`
	ValidationErrorPct float64           `json:"validation_error_pct"`
	Timely             bool              `json:"timely"`
	GoldProvider       string            `json:"gold_provider" gorm:"size:64"`
	GoldSourceURL      string            `json:"gold_source_url" gorm:"size:2048"`
	GoldSHA256         string            `json:"gold_sha256" gorm:"size:64"`
	GoldRows           int               `json:"gold_rows"`
	GoldErrorPct       float64           `json:"gold_error_pct"`
	ErrorMessage       string            `json:"error_message" gorm:"type:text"`
	AttemptsJSON       string            `json:"-" gorm:"type:text"`
	Attempts           []ProviderAttempt `json:"provider_attempts" gorm:"-"`
	FallbackUsed       bool              `json:"fallback_used"`
	CreatedAt          time.Time         `json:"created_at"`
}

// ProviderAttempt is the immutable per-child lineage for a provider-chain
// run. It lets operators distinguish a healthy primary source from a batch
// that only completed because a fallback source filled missing records.
type ProviderAttempt struct {
	Provider      string  `json:"provider"`
	Status        string  `json:"status"`
	SourceVersion string  `json:"source_version,omitempty"`
	Expected      int     `json:"expected"`
	Records       int     `json:"records"`
	Remaining     int     `json:"remaining"`
	CoveragePct   float64 `json:"coverage_pct"`
	ElapsedMS     int64   `json:"elapsed_ms"`
	ErrorMessage  string  `json:"error_message,omitempty"`
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
	Source        string    `json:"source" gorm:"size:64;uniqueIndex:idx_price_source_version_symbol_date,priority:1;index:idx_price_symbol_quality_source_trade_date,priority:3"`
	SourceVersion string    `json:"source_version" gorm:"size:128;uniqueIndex:idx_price_source_version_symbol_date,priority:2"`
	Symbol        string    `json:"symbol" gorm:"size:64;uniqueIndex:idx_price_source_version_symbol_date,priority:3;index;index:idx_price_symbol_quality_trade_date,priority:1"`
	TradeDate     time.Time `json:"trade_date" gorm:"uniqueIndex:idx_price_source_version_symbol_date,priority:4;index;index:idx_price_symbol_quality_trade_date,priority:3;index:idx_price_symbol_quality_source_trade_date,priority:4"`
	OpenMicros    int64     `json:"open_micros"`
	HighMicros    int64     `json:"high_micros"`
	LowMicros     int64     `json:"low_micros"`
	CloseMicros   int64     `json:"close_micros"`
	Volume        int64     `json:"volume"`
	Currency      string    `json:"currency" gorm:"size:8"`
	Adjusted      bool      `json:"adjusted"`
	QualityStatus string    `json:"quality_status" gorm:"size:16;index;index:idx_price_symbol_quality_trade_date,priority:2;index:idx_price_symbol_quality_source_trade_date,priority:2"`
	CreatedAt     time.Time `json:"created_at"`
}

// TickerEvaluationSnapshot is an immutable result of a user-triggered,
// single-symbol research evaluation.  It deliberately lives outside the
// universe batches: an ad-hoc review must not alter the ranked candidate set.
type TickerEvaluationSnapshot struct {
	ID          uint      `json:"id"`
	Ticker      string    `json:"ticker" gorm:"size:32;index:idx_ticker_evaluation_ticker_time,priority:1"`
	CIK         string    `json:"cik" gorm:"size:10;index"`
	CompanyName string    `json:"company_name" gorm:"size:255"`
	TargetType  string    `json:"target_type" gorm:"size:16;index"`
	Status      string    `json:"status" gorm:"size:32;index"`
	TotalScore  int       `json:"total_score"`
	ReviewScore int       `json:"review_score"`
	ResultJSON  string    `json:"result_json" gorm:"type:text"`
	EvaluatedAt time.Time `json:"evaluated_at" gorm:"index:idx_ticker_evaluation_ticker_time,priority:2"`
	CreatedAt   time.Time `json:"created_at"`
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

type FinancialFactSnapshot struct {
	ID            uint      `json:"id"`
	SecurityID    uint      `json:"security_id" gorm:"uniqueIndex:idx_financial_fact_identity,priority:1;index;index:idx_financial_fact_readiness,priority:1"`
	Metric        string    `json:"metric" gorm:"size:64;uniqueIndex:idx_financial_fact_identity,priority:2;index;index:idx_financial_fact_readiness,priority:2"`
	Concept       string    `json:"concept" gorm:"size:128;uniqueIndex:idx_financial_fact_identity,priority:3"`
	PeriodStart   time.Time `json:"period_start" gorm:"uniqueIndex:idx_financial_fact_identity,priority:4"`
	PeriodEnd     time.Time `json:"period_end" gorm:"uniqueIndex:idx_financial_fact_identity,priority:5;index;index:idx_financial_fact_readiness,priority:4"`
	Accession     string    `json:"accession" gorm:"size:32;uniqueIndex:idx_financial_fact_identity,priority:6"`
	Unit          string    `json:"unit" gorm:"size:16"`
	AmountMicros  int64     `json:"amount_micros"`
	Form          string    `json:"form" gorm:"size:16"`
	SourceURL     string    `json:"source_url" gorm:"size:2048"`
	QualityStatus string    `json:"quality_status" gorm:"size:16;index;index:idx_financial_fact_readiness,priority:3"`
	FiledAt       time.Time `json:"filed_at"`
	AcceptedAt    time.Time `json:"accepted_at"`
	CreatedAt     time.Time `json:"created_at"`
	Security      Security  `json:"-" gorm:"foreignKey:SecurityID;references:ID;constraint:OnUpdate:RESTRICT,OnDelete:RESTRICT"`
}

type FinancialMetricSnapshot struct {
	ID                         uint      `json:"id"`
	BatchID                    string    `json:"batch_id" gorm:"size:64;uniqueIndex:idx_financial_metric_batch_security,priority:1;index"`
	SecurityID                 uint      `json:"security_id" gorm:"uniqueIndex:idx_financial_metric_batch_security,priority:2;index"`
	ParserVersion              string    `json:"parser_version" gorm:"size:64"`
	RevenueGrowthAvailable     bool      `json:"revenue_growth_available"`
	RunwayAvailable            bool      `json:"runway_available"`
	GrossMarginAvailable       bool      `json:"gross_margin_available"`
	LatestQuarterRevenueUSD    int64     `json:"latest_quarter_revenue_usd"`
	PriorYearQuarterRevenueUSD int64     `json:"prior_year_quarter_revenue_usd"`
	PreviousQuarterRevenueUSD  int64     `json:"previous_quarter_revenue_usd"`
	QuarterlyRevenueYoYPct     float64   `json:"quarterly_revenue_yoy_pct"`
	QuarterlyRevenueQoQPct     float64   `json:"quarterly_revenue_qoq_pct"`
	LatestAnnualRevenueUSD     int64     `json:"latest_annual_revenue_usd"`
	PriorAnnualRevenueUSD      int64     `json:"prior_annual_revenue_usd"`
	AnnualRevenueYoYPct        float64   `json:"annual_revenue_yoy_pct"`
	AnnualRevenueQoQPct        float64   `json:"annual_revenue_qoq_pct"`
	AvailableCashUSD           float64   `json:"available_cash_usd"`
	TTMOperatingCashFlowUSD    float64   `json:"ttm_operating_cash_flow_usd"`
	TTMCapitalExpenditureUSD   float64   `json:"ttm_capital_expenditure_usd"`
	CFOBurnMonthlyUSD          float64   `json:"cfo_burn_monthly_usd"`
	FCFBurnMonthlyUSD          float64   `json:"fcf_burn_monthly_usd"`
	CashRunwayMonths           float64   `json:"cash_runway_months"`
	GrossMarginPct             float64   `json:"gross_margin_pct"`
	QualityFlagsJSON           string    `json:"quality_flags_json" gorm:"type:text"`
	CreatedAt                  time.Time `json:"created_at"`
	Security                   Security  `json:"-" gorm:"foreignKey:SecurityID;references:ID;constraint:OnUpdate:RESTRICT,OnDelete:RESTRICT"`
}

type InsiderTransactionSnapshot struct {
	ID                           uint      `json:"id"`
	SecurityID                   uint      `json:"security_id" gorm:"index"`
	IdentitySHA256               string    `json:"identity_sha256" gorm:"size:64;index"`
	Accession                    string    `json:"accession" gorm:"size:32;index"`
	OwnerName                    string    `json:"owner_name" gorm:"size:255"`
	OfficerTitle                 string    `json:"officer_title" gorm:"size:255"`
	Role                         string    `json:"role" gorm:"size:32;index"`
	Derivative                   bool      `json:"derivative"`
	TransactionDate              time.Time `json:"transaction_date" gorm:"index"`
	TransactionCode              string    `json:"transaction_code" gorm:"size:8;index"`
	AcquiredDisposedCode         string    `json:"acquired_disposed_code" gorm:"size:8"`
	SharesMicros                 int64     `json:"shares_micros"`
	PriceMicros                  int64     `json:"price_micros"`
	ValueMicros                  int64     `json:"value_micros"`
	SharesOwnedAfterMicros       int64     `json:"shares_owned_after_micros"`
	SharesOwnedBeforeMicros      int64     `json:"shares_owned_before_micros"`
	Qualified                    bool      `json:"qualified" gorm:"index"`
	ExclusionReason              string    `json:"exclusion_reason" gorm:"size:64;index"`
	FounderConfirmationSuggested bool      `json:"founder_confirmation_suggested"`
	ParserVersion                string    `json:"parser_version" gorm:"size:64"`
	SourceURL                    string    `json:"source_url" gorm:"size:2048"`
	CreatedAt                    time.Time `json:"created_at"`
	Security                     Security  `json:"-" gorm:"foreignKey:SecurityID;references:ID;constraint:OnUpdate:RESTRICT,OnDelete:RESTRICT"`
}

// InsiderCoverageSnapshot records the result of the Form 4 evidence pass for
// one issuer in one immutable security batch. It prevents an absence of rows
// from being misrepresented as proof that an issuer had no insider activity.
type InsiderCoverageSnapshot struct {
	ID                        uint      `json:"id"`
	BatchID                   string    `json:"batch_id" gorm:"size:64;uniqueIndex:idx_insider_coverage_batch_security,priority:1;index"`
	SecurityID                uint      `json:"security_id" gorm:"uniqueIndex:idx_insider_coverage_batch_security,priority:2;index"`
	CIK                       string    `json:"cik" gorm:"size:10;index"`
	Status                    string    `json:"status" gorm:"size:32;index"`
	EligibleFilings           int       `json:"eligible_filings"`
	DownloadedDocuments       int       `json:"downloaded_documents"`
	ParsedDocuments           int       `json:"parsed_documents"`
	TransactionCount          int       `json:"transaction_count"`
	PermanentDocumentFailures int       `json:"permanent_document_failures"`
	TransientDocumentFailures int       `json:"transient_document_failures"`
	MalformedDocuments        int       `json:"malformed_documents"`
	CheckedAt                 time.Time `json:"checked_at"`
	CreatedAt                 time.Time `json:"created_at"`
	Security                  Security  `json:"-" gorm:"foreignKey:SecurityID;references:ID;constraint:OnUpdate:RESTRICT,OnDelete:RESTRICT"`
}

// SECFilingSnapshot is a compact, immutable index of a filing from SEC
// submissions data. It deliberately stores metadata only; filing documents
// remain on SEC EDGAR and are opened through FilingURL when a user needs them.
type SECFilingSnapshot struct {
	ID              uint       `json:"id"`
	SecurityID      uint       `json:"security_id" gorm:"uniqueIndex:idx_sec_filing_security_accession,priority:1;index"`
	AccessionNumber string     `json:"accession_number" gorm:"size:32;uniqueIndex:idx_sec_filing_security_accession,priority:2;index"`
	FilingType      string     `json:"filing_type" gorm:"size:32;index"`
	FilingDate      time.Time  `json:"filing_date" gorm:"index"`
	ReportDate      *time.Time `json:"report_date"`
	AcceptedAt      *time.Time `json:"accepted_at" gorm:"index"`
	PrimaryDocument string     `json:"primary_document" gorm:"size:512"`
	Items           string     `json:"items" gorm:"size:512"`
	FilingURL       string     `json:"filing_url" gorm:"size:2048"`
	CreatedAt       time.Time  `json:"created_at"`
	Security        Security   `json:"-" gorm:"foreignKey:SecurityID;references:ID;constraint:OnUpdate:RESTRICT,OnDelete:RESTRICT"`
}

type CapitalRiskSnapshot struct {
	ID            uint      `json:"id"`
	BatchID       string    `json:"batch_id" gorm:"size:64;uniqueIndex:idx_capital_risk_identity,priority:1;index"`
	SecurityID    uint      `json:"security_id" gorm:"uniqueIndex:idx_capital_risk_identity,priority:2;index"`
	Kind          string    `json:"kind" gorm:"size:64;uniqueIndex:idx_capital_risk_identity,priority:3;index"`
	Accession     string    `json:"accession" gorm:"size:32;uniqueIndex:idx_capital_risk_identity,priority:4;index"`
	EffectiveAt   time.Time `json:"effective_at" gorm:"uniqueIndex:idx_capital_risk_identity,priority:5;index"`
	AcceptedAt    time.Time `json:"accepted_at" gorm:"index"`
	ActiveUntil   time.Time `json:"active_until" gorm:"index"`
	Active        bool      `json:"active" gorm:"index"`
	BlocksA       bool      `json:"blocks_a" gorm:"index"`
	BlocksB       bool      `json:"blocks_b" gorm:"index"`
	Severity      string    `json:"severity" gorm:"size:16;index"`
	ChangesShares bool      `json:"changes_shares"`
	Reason        string    `json:"reason" gorm:"type:text"`
	CreatedAt     time.Time `json:"created_at"`
	Security      Security  `json:"-" gorm:"foreignKey:SecurityID;references:ID;constraint:OnUpdate:RESTRICT,OnDelete:RESTRICT"`
}

type SocialHeatSnapshot struct {
	ID             uint      `json:"id"`
	BatchID        string    `json:"batch_id" gorm:"size:64;uniqueIndex:idx_social_heat_batch_security_provider,priority:1;index"`
	SecurityID     uint      `json:"security_id" gorm:"uniqueIndex:idx_social_heat_batch_security_provider,priority:2;index"`
	Ticker         string    `json:"ticker" gorm:"size:32;index"`
	Provider       string    `json:"provider" gorm:"size:64;uniqueIndex:idx_social_heat_batch_security_provider,priority:3;index"`
	MentionCount   int       `json:"mention_count"`
	BaselineCount  int       `json:"baseline_count"`
	HeatScore      float64   `json:"heat_score"`
	SentimentScore float64   `json:"sentiment_score"`
	SourceStatus   string    `json:"source_status" gorm:"size:32;index"`
	WindowStart    time.Time `json:"window_start"`
	WindowEnd      time.Time `json:"window_end"`
	SourceURL      string    `json:"source_url" gorm:"size:2048"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
	Security       Security  `json:"-" gorm:"foreignKey:SecurityID;references:ID;constraint:OnUpdate:RESTRICT,OnDelete:RESTRICT"`
}

type CandidateScoreSnapshot struct {
	ID                     uint      `json:"id"`
	BatchID                string    `json:"batch_id" gorm:"size:64;uniqueIndex:idx_candidate_score_batch_security,priority:1;index"`
	SecurityID             uint      `json:"security_id" gorm:"uniqueIndex:idx_candidate_score_batch_security,priority:2;index"`
	Ticker                 string    `json:"ticker" gorm:"size:32;index"`
	MarketCapUSD           int64     `json:"market_cap_usd" gorm:"index"`
	Grade                  string    `json:"grade" gorm:"size:16;index"`
	EligibleA              bool      `json:"eligible_a" gorm:"index"`
	EligibleB              bool      `json:"eligible_b" gorm:"index"`
	TotalScore             int       `json:"total_score" gorm:"index"`
	RevenueGrowthScore     int       `json:"revenue_growth_score"`
	CashRunwayScore        int       `json:"cash_runway_score"`
	InsiderScore           int       `json:"insider_score"`
	GrossMarginScore       int       `json:"gross_margin_score"`
	DilutionRiskScore      int       `json:"dilution_risk_score"`
	SectorScore            int       `json:"sector_score"`
	RevenueGrowthPct       float64   `json:"revenue_growth_pct"`
	CashRunwayMonths       float64   `json:"cash_runway_months"`
	RecentQualifiedInsider bool      `json:"recent_qualified_insider"`
	ActiveBlocksA          bool      `json:"active_blocks_a" gorm:"index"`
	ActiveBlocksB          bool      `json:"active_blocks_b" gorm:"index"`
	ReasonCode             string    `json:"reason_code" gorm:"size:64"`
	ScoringVersion         string    `json:"scoring_version" gorm:"size:64"`
	ScoringRubricSHA256    string    `json:"scoring_rubric_sha256" gorm:"size:64;index"`
	ScoringRubricJSON      string    `json:"-" gorm:"type:text"`
	BusinessModelAtScore   string    `json:"business_model_at_score" gorm:"size:32;index"`
	RevenueScoreCapReason  string    `json:"revenue_score_cap_reason" gorm:"size:128"`
	CreatedAt              time.Time `json:"created_at"`
	Security               Security  `json:"-" gorm:"foreignKey:SecurityID;references:ID;constraint:OnUpdate:RESTRICT,OnDelete:RESTRICT"`
}

// SmallCapEligibilityCheckHistory is an immutable record of a manual
// eligibility check.  It retains the exact condition results shown to the
// researcher, together with the published batches used for that conclusion.
// A manual check deliberately reads local snapshots only and never implies a
// fresh SEC or market-data request.
type SmallCapEligibilityCheckHistory struct {
	ID              uint      `json:"id"`
	RequestedTicker string    `json:"requested_ticker" gorm:"size:32;index"`
	Ticker          string    `json:"ticker" gorm:"size:32;index"`
	CompanyName     string    `json:"company_name" gorm:"size:255"`
	SecurityID      uint      `json:"security_id" gorm:"index"`
	MarketBatchID   string    `json:"market_batch_id" gorm:"size:64;index"`
	SecurityBatchID string    `json:"security_batch_id" gorm:"size:64;index"`
	InSmallCapPool  bool      `json:"in_small_cap_pool" gorm:"index"`
	EligibleA       bool      `json:"eligible_a" gorm:"index"`
	EligibleB       bool      `json:"eligible_b" gorm:"index"`
	Grade           string    `json:"grade" gorm:"size:16;index"`
	ResultJSON      string    `json:"result_json" gorm:"type:text"`
	CreatedAt       time.Time `json:"created_at" gorm:"index"`
}

// CandidateBusinessModelOverride is an audited, user-confirmed classification
// for biotech candidates. New confirmations supersede older active rows rather
// than overwriting them, preserving the research trail.
type CandidateBusinessModelOverride struct {
	ID                         uint       `json:"id"`
	SecurityID                 uint       `json:"security_id" gorm:"index"`
	BusinessModel              string     `json:"business_model" gorm:"size:32;index"`
	RevenueRepeatableConfirmed bool       `json:"revenue_repeatable_confirmed"`
	Reason                     string     `json:"reason" gorm:"type:text"`
	SourceURL                  string     `json:"source_url" gorm:"size:2048"`
	Operator                   string     `json:"operator" gorm:"size:128"`
	ReviewDueAt                *time.Time `json:"review_due_at" gorm:"index"`
	ConfirmedAt                time.Time  `json:"confirmed_at" gorm:"index"`
	Active                     bool       `json:"active" gorm:"index"`
	CreatedAt                  time.Time  `json:"created_at"`
	UpdatedAt                  time.Time  `json:"updated_at"`
	Security                   Security   `json:"-" gorm:"foreignKey:SecurityID;references:ID;constraint:OnUpdate:RESTRICT,OnDelete:RESTRICT"`
}

// CandidateSignalEvent is an immutable, point-in-time research event. It is
// intentionally separate from the daily score snapshot so later filings or
// rescoring cannot rewrite a historical performance baseline.
type CandidateSignalEvent struct {
	ID                  uint      `json:"id"`
	BatchID             string    `json:"batch_id" gorm:"size:64;uniqueIndex:idx_candidate_signal_batch_security_type,priority:1;index"`
	SecurityID          uint      `json:"security_id" gorm:"uniqueIndex:idx_candidate_signal_batch_security_type,priority:2;index"`
	Ticker              string    `json:"ticker" gorm:"size:32;index"`
	Grade               string    `json:"grade" gorm:"size:16;index"`
	EventType           string    `json:"event_type" gorm:"size:32;uniqueIndex:idx_candidate_signal_batch_security_type,priority:3;index"`
	ScoringVersion      string    `json:"scoring_version" gorm:"size:64;index"`
	TotalScore          int       `json:"total_score"`
	SignalDate          time.Time `json:"signal_date" gorm:"index"`
	BaselineTradeDate   time.Time `json:"baseline_trade_date" gorm:"index"`
	BaselineCloseMicros int64     `json:"baseline_close_micros"`
	PriceSource         string    `json:"price_source" gorm:"size:64"`
	CreatedAt           time.Time `json:"created_at"`
}

type CandidateRecalcEvent struct {
	ID              uint      `json:"id"`
	BatchID         string    `json:"batch_id" gorm:"size:64;uniqueIndex:idx_candidate_recalc_filing_batch,priority:2;index"`
	SecurityID      uint      `json:"security_id" gorm:"index"`
	Ticker          string    `json:"ticker" gorm:"size:32;index"`
	CIK             string    `json:"cik" gorm:"size:10;index"`
	FilingID        string    `json:"filing_id" gorm:"size:128;uniqueIndex:idx_candidate_recalc_filing_batch,priority:1"`
	AccessionNumber string    `json:"accession_number" gorm:"size:128;index"`
	FilingType      string    `json:"filing_type" gorm:"size:64;index"`
	FilingDate      time.Time `json:"filing_date" gorm:"index"`
	Status          string    `json:"status" gorm:"size:32;index"`
	Reason          string    `json:"reason" gorm:"type:text"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

type CandidateWatch struct {
	ID                  uint       `json:"id"`
	Ticker              string     `json:"ticker" gorm:"size:32;uniqueIndex;index"`
	SecurityID          uint       `json:"security_id" gorm:"index"`
	CIK                 string     `json:"cik" gorm:"size:10;index"`
	CompanyName         string     `json:"company_name" gorm:"size:255"`
	Status              string     `json:"status" gorm:"size:16;index"`
	Note                string     `json:"note" gorm:"type:text"`
	ResearchStatus      string     `json:"research_status" gorm:"size:16;index"`
	Thesis              string     `json:"thesis" gorm:"type:text"`
	RiskNotes           string     `json:"risk_notes" gorm:"type:text"`
	Invalidation        string     `json:"invalidation" gorm:"type:text"`
	MarketConcern       string     `json:"market_concern" gorm:"type:text"`
	FalsifiableJudgment string     `json:"falsifiable_judgment" gorm:"type:text"`
	Catalyst            string     `json:"catalyst" gorm:"type:text"`
	CatalystSource      string     `json:"catalyst_source" gorm:"size:2048"`
	CatalystDate        *time.Time `json:"catalyst_date" gorm:"index"`
	NextReviewAt        *time.Time `json:"next_review_at" gorm:"index"`
	SourceBatchID       string     `json:"source_batch_id" gorm:"size:64;index"`
	// BaselineJSON is an immutable local snapshot captured when the ticker is
	// first followed. It lets research compare today's published data with the
	// information that was actually available at the time of the decision.
	BaselineBatchID    string     `json:"baseline_batch_id" gorm:"size:64;index"`
	BaselineCapturedAt *time.Time `json:"baseline_captured_at" gorm:"index"`
	BaselineJSON       string     `json:"baseline_json" gorm:"type:text"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
}

// CandidateResearchMemoVersion is an immutable copy of the user-maintained
// research card. It deliberately stores opinions separately from SEC facts.
type CandidateResearchMemoVersion struct {
	ID                  uint       `json:"id"`
	Ticker              string     `json:"ticker" gorm:"size:32;uniqueIndex:idx_candidate_research_memo_ticker_version,priority:1;index"`
	Version             int        `json:"version" gorm:"uniqueIndex:idx_candidate_research_memo_ticker_version,priority:2"`
	SecurityID          uint       `json:"security_id" gorm:"index"`
	Author              string     `json:"author" gorm:"size:128"`
	Thesis              string     `json:"thesis" gorm:"type:text"`
	MarketConcern       string     `json:"market_concern" gorm:"type:text"`
	FalsifiableJudgment string     `json:"falsifiable_judgment" gorm:"type:text"`
	Catalyst            string     `json:"catalyst" gorm:"type:text"`
	CatalystSource      string     `json:"catalyst_source" gorm:"size:2048"`
	CatalystDate        *time.Time `json:"catalyst_date"`
	RiskNotes           string     `json:"risk_notes" gorm:"type:text"`
	Invalidation        string     `json:"invalidation" gorm:"type:text"`
	NextReviewAt        *time.Time `json:"next_review_at"`
	CreatedAt           time.Time  `json:"created_at"`
}

// CandidateResearchPosition is a hand-maintained research allocation. It is
// not connected to a brokerage account and never causes an order to be sent.
type CandidateResearchPosition struct {
	ID                          uint      `json:"id"`
	Ticker                      string    `json:"ticker" gorm:"size:32;uniqueIndex;index"`
	SecurityID                  uint      `json:"security_id" gorm:"index"`
	MaxWeightPct                float64   `json:"max_weight_pct"`
	ReferenceCostUSD            *float64  `json:"reference_cost_usd"`
	MaxDailyVolumeParticipation float64   `json:"max_daily_volume_participation_pct" gorm:"column:max_daily_volume_participation_pct"`
	EventRiskNote               string    `json:"event_risk_note" gorm:"type:text"`
	LiquidityNote               string    `json:"liquidity_note" gorm:"type:text"`
	Note                        string    `json:"note" gorm:"type:text"`
	CreatedAt                   time.Time `json:"created_at"`
	UpdatedAt                   time.Time `json:"updated_at"`
}

// TradeSetupStatusEvent records a state transition of the deterministic
// daily-close trade plan. It is research history, not an order or execution
// record. A row is written only when the status changes, so StartedAt answers
// how long the current condition has been in effect.
type TradeSetupStatusEvent struct {
	ID                    uint      `json:"id"`
	SecurityID            uint      `json:"security_id" gorm:"index"`
	Ticker                string    `json:"ticker" gorm:"size:32;index:idx_trade_setup_status_ticker_started,priority:1"`
	TradeDate             string    `json:"trade_date" gorm:"size:10;index"`
	Status                string    `json:"status" gorm:"size:32;index"`
	PreviousStatus        string    `json:"previous_status" gorm:"size:32"`
	Bias                  string    `json:"bias" gorm:"size:32"`
	EntryTrigger          string    `json:"entry_trigger" gorm:"size:128"`
	ExitReason            string    `json:"exit_reason" gorm:"type:text"`
	ReasonsJSON           string    `json:"reasons_json" gorm:"type:text"`
	Reasons               []string  `json:"reasons" gorm:"-"`
	CloseUSD              float64   `json:"close_usd"`
	StopLossUSD           float64   `json:"stop_loss_usd"`
	RiskPct               float64   `json:"risk_pct"`
	TakeProfitZoneLowUSD  float64   `json:"take_profit_zone_low_usd"`
	TakeProfitZoneHighUSD float64   `json:"take_profit_zone_high_usd"`
	StartedAt             time.Time `json:"started_at" gorm:"index:idx_trade_setup_status_ticker_started,priority:2"`
	RecordedAt            time.Time `json:"recorded_at" gorm:"index"`
	CreatedAt             time.Time `json:"created_at"`
}

// TradePlanSimulation is a daily-close paper-trade record. It stores the
// signal snapshot separately from its subsequently observed lifecycle; it is
// never connected to a brokerage or execution system.
type TradePlanSimulation struct {
	ID               uint       `json:"id"`
	Ticker           string     `json:"ticker" gorm:"size:32;uniqueIndex:idx_trade_plan_simulation_signal,priority:1;index"`
	RuleVersion      string     `json:"rule_version" gorm:"size:32;uniqueIndex:idx_trade_plan_simulation_signal,priority:2;index"`
	SignalDate       time.Time  `json:"signal_date" gorm:"uniqueIndex:idx_trade_plan_simulation_signal,priority:3;index"`
	EntryDate        *time.Time `json:"entry_date" gorm:"index"`
	EntryTrigger     string     `json:"entry_trigger" gorm:"size:128"`
	EntryPriceUSD    float64    `json:"entry_price_usd"`
	StopLossUSD      float64    `json:"stop_loss_usd"`
	TakeProfitUSD    float64    `json:"take_profit_usd"`
	InitialRiskPct   float64    `json:"initial_risk_pct"`
	Status           string     `json:"status" gorm:"size:32;index"`
	ExitDate         *time.Time `json:"exit_date" gorm:"index"`
	ExitPriceUSD     float64    `json:"exit_price_usd"`
	ExitReason       string     `json:"exit_reason" gorm:"type:text"`
	LastMarkPriceUSD float64    `json:"last_mark_price_usd"`
	ReturnPct        float64    `json:"return_pct"`
	RMultiple        float64    `json:"r_multiple"`
	MaxDrawdownPct   float64    `json:"max_drawdown_pct"`
	HoldingDays      int        `json:"holding_days"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
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
	PolicyVersionID       uint                      `json:"policy_version_id" gorm:"index"`
	PolicyVersion         int                       `json:"policy_version"`
	PolicyContentSHA256   string                    `json:"policy_content_sha256" gorm:"size:64;index"`
	PolicySnapshotJSON    string                    `json:"policy_snapshot_json" gorm:"type:text"`
	StartedAt             time.Time                 `json:"started_at"`
	CompletedAt           *time.Time                `json:"completed_at"`
	ErrorMessage          string                    `json:"error_message" gorm:"type:text"`
	ProviderSummary       *BatchProviderSummary     `json:"provider_summary,omitempty" gorm:"-"`
	CandidateCount        int64                     `json:"candidate_count" gorm:"-"`
	Classifications       []ClassificationSnapshot  `json:"-" gorm:"foreignKey:BatchID;references:BatchID;constraint:OnUpdate:RESTRICT,OnDelete:RESTRICT"`
	Identities            []SecurityBatchIdentity   `json:"-" gorm:"foreignKey:BatchID;references:BatchID;constraint:OnUpdate:RESTRICT,OnDelete:RESTRICT"`
	ListingIdentities     []ListingIdentitySnapshot `json:"-" gorm:"foreignKey:BatchID;references:BatchID;constraint:OnUpdate:RESTRICT,OnDelete:RESTRICT"`
	FinancialMetrics      []FinancialMetricSnapshot `json:"-" gorm:"foreignKey:BatchID;references:BatchID;constraint:OnUpdate:RESTRICT,OnDelete:RESTRICT"`
	CapitalRisks          []CapitalRiskSnapshot     `json:"-" gorm:"foreignKey:BatchID;references:BatchID;constraint:OnUpdate:RESTRICT,OnDelete:RESTRICT"`
	CandidateScores       []CandidateScoreSnapshot  `json:"-" gorm:"foreignKey:BatchID;references:BatchID;constraint:OnUpdate:RESTRICT,OnDelete:RESTRICT"`
	ProviderRuns          []ProviderRun             `json:"-" gorm:"foreignKey:BatchID;references:BatchID;constraint:OnUpdate:RESTRICT,OnDelete:RESTRICT"`
	Snapshots             []UniverseSnapshot        `json:"-" gorm:"foreignKey:BatchID;references:BatchID;constraint:OnUpdate:RESTRICT,OnDelete:RESTRICT"`
	ShareSelections       []BatchShareSelection     `json:"-" gorm:"foreignKey:BatchID;references:BatchID;constraint:OnUpdate:RESTRICT,OnDelete:RESTRICT"`
	CurrentPointers       []CurrentBatchPointer     `json:"-" gorm:"foreignKey:BatchID;references:BatchID;constraint:OnUpdate:RESTRICT,OnDelete:RESTRICT"`
}

// DiscoverySyncRun is the user-visible lifecycle record for a small-cap
// workflow. It is independent from published batches so an in-progress or
// failed security phase remains visible even when it never produces a batch.
type DiscoverySyncRun struct {
	ID                  uint       `json:"id" gorm:"primaryKey"`
	Kind                string     `json:"kind" gorm:"size:32;index"`
	Status              string     `json:"status" gorm:"size:16;index"`
	Phase               string     `json:"phase" gorm:"size:32;index"`
	StartedAt           time.Time  `json:"started_at" gorm:"index"`
	CompletedAt         *time.Time `json:"completed_at"`
	SecurityBatchID     string     `json:"security_batch_id" gorm:"size:64"`
	MarketBatchID       string     `json:"market_batch_id" gorm:"size:64"`
	PolicyVersionID     uint       `json:"policy_version_id" gorm:"index"`
	PolicyVersion       int        `json:"policy_version"`
	PolicyContentSHA256 string     `json:"policy_content_sha256" gorm:"size:64;index"`
	PolicySnapshotJSON  string     `json:"policy_snapshot_json" gorm:"type:text"`
	ErrorMessage        string     `json:"error_message" gorm:"type:text"`
	CreatedAt           time.Time  `json:"created_at"`
	UpdatedAt           time.Time  `json:"updated_at"`
}

// DiscoverySyncStep is an append-only, user-visible timeline entry for a
// discovery workflow.  Batches remain the audit record of published output;
// steps explain what the worker was doing before a batch existed and why a
// run stopped.
type DiscoverySyncStep struct {
	ID          uint       `json:"id" gorm:"primaryKey"`
	RunID       uint       `json:"run_id" gorm:"index:idx_discovery_sync_steps_run_sequence,priority:1"`
	Sequence    int        `json:"sequence" gorm:"index:idx_discovery_sync_steps_run_sequence,priority:2"`
	Phase       string     `json:"phase" gorm:"size:48;index"`
	Status      string     `json:"status" gorm:"size:16;index"`
	Message     string     `json:"message" gorm:"type:text"`
	RecordCount int        `json:"record_count"`
	TotalCount  int        `json:"total_count"`
	StartedAt   time.Time  `json:"started_at"`
	CompletedAt *time.Time `json:"completed_at"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

// SecurityStageCheckpoint is an executable checkpoint for one committed
// security-universe chunk. Unlike DiscoverySyncStep (which is only a user
// timeline), a completed checkpoint is consulted by a retry of the same
// deterministic batch so already committed work is not repeated.
type SecurityStageCheckpoint struct {
	ID           uint       `json:"id" gorm:"primaryKey"`
	BatchID      string     `json:"batch_id" gorm:"size:64;uniqueIndex:idx_security_checkpoint_chunk,priority:1;index"`
	Phase        string     `json:"phase" gorm:"size:48;uniqueIndex:idx_security_checkpoint_chunk,priority:2;index"`
	Chunk        int        `json:"chunk" gorm:"uniqueIndex:idx_security_checkpoint_chunk,priority:3"`
	Status       string     `json:"status" gorm:"size:16;index"`
	AttemptCount int        `json:"attempt_count"`
	RecordCount  int        `json:"record_count"`
	ErrorMessage string     `json:"error_message" gorm:"type:text"`
	StartedAt    time.Time  `json:"started_at"`
	CompletedAt  *time.Time `json:"completed_at"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
}

// SecuritySourceCheckpoint tracks a durable parsed-source artifact before the
// final content-addressed universe batch exists. SecurityStageCheckpoint only
// protects database writes after every source has loaded; this checkpoint lets
// a retry skip completed fundamentals, Form 4, and capital-event acquisition
// stages as well. Metadata is intentionally freshness-checked on each run.
type SecuritySourceCheckpoint struct {
	ArtifactKey         string     `json:"artifact_key" gorm:"size:64;primaryKey;autoIncrement:false"`
	Phase               string     `json:"phase" gorm:"size:48;index"`
	EffectiveDate       string     `json:"effective_date" gorm:"size:10;index"`
	ScopeSHA256         string     `json:"scope_sha256" gorm:"size:64;index"`
	PolicyContentSHA256 string     `json:"policy_content_sha256" gorm:"size:64;index"`
	Status              string     `json:"status" gorm:"size:16;index"`
	AttemptCount        int        `json:"attempt_count"`
	RecordCount         int        `json:"record_count"`
	PayloadPath         string     `json:"payload_path" gorm:"type:text"`
	PayloadSHA256       string     `json:"payload_sha256" gorm:"size:64"`
	ErrorMessage        string     `json:"error_message" gorm:"type:text"`
	StartedAt           time.Time  `json:"started_at"`
	CompletedAt         *time.Time `json:"completed_at"`
	CreatedAt           time.Time  `json:"created_at"`
	UpdatedAt           time.Time  `json:"updated_at"`
}

type BatchProviderSummary struct {
	Provider          string            `json:"provider"`
	Status            string            `json:"status"`
	ExpectedCount     int               `json:"expected_count"`
	RecordCount       int               `json:"record_count"`
	CoveragePct       float64           `json:"coverage_pct"`
	Timely            bool              `json:"timely"`
	SourceVersion     string            `json:"source_version"`
	ErrorMessage      string            `json:"error_message"`
	PriceSourceCounts map[string]int64  `json:"price_source_counts"`
	ProviderAttempts  []ProviderAttempt `json:"provider_attempts"`
	FallbackUsed      bool              `json:"fallback_used"`
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
