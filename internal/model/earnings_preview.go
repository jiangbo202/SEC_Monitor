package model

import "time"

// EarningsPreview is the locally cached upcoming earnings event for one watch
// target. Provider data is intentionally copied here so page views never make
// a market-data request.
type EarningsPreview struct {
	ID                uint       `gorm:"primaryKey" json:"id"`
	TargetID          uint       `gorm:"not null;uniqueIndex" json:"target_id"`
	Ticker            string     `gorm:"size:32;not null;index" json:"ticker"`
	CompanyName       string     `gorm:"size:255" json:"company_name"`
	Provider          string     `gorm:"size:32;not null;index" json:"provider"`
	Status            string     `gorm:"size:32;not null;index" json:"status"`
	EventKey          string     `gorm:"size:255;index" json:"event_key,omitempty"`
	ReportAt          *time.Time `gorm:"index" json:"report_at,omitempty"`
	Session           string     `gorm:"size:64" json:"session,omitempty"`
	EventContent      string     `gorm:"type:text" json:"event_content,omitempty"`
	FiscalYear        int        `json:"fiscal_year,omitempty"`
	FiscalPeriod      string     `gorm:"size:32" json:"fiscal_period,omitempty"`
	Currency          string     `gorm:"size:16" json:"currency,omitempty"`
	EPSEstimate       *float64   `json:"eps_estimate,omitempty"`
	EPSActual         *float64   `json:"eps_actual,omitempty"`
	EPSSurprise       *float64   `json:"eps_surprise,omitempty"`
	RevenueEstimate   *float64   `json:"revenue_estimate,omitempty"`
	RevenueActual     *float64   `json:"revenue_actual,omitempty"`
	RevenueSurprise   *float64   `json:"revenue_surprise,omitempty"`
	ProviderUpdatedAt *time.Time `json:"provider_updated_at,omitempty"`
	FetchedAt         *time.Time `gorm:"index" json:"fetched_at,omitempty"`
	ChangedAt         *time.Time `json:"changed_at,omitempty"`
	ChangeSummary     string     `gorm:"type:text" json:"change_summary,omitempty"`
	LastError         string     `gorm:"type:text" json:"last_error,omitempty"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
}

func (EarningsPreview) TableName() string { return "earnings_previews" }

// EarningsPreviewNotice is the local de-duplication ledger for earnings-date
// reminders and calendar changes. Its unique key means scheduler restarts and
// manual refreshes cannot repeat the same notice.
type EarningsPreviewNotice struct {
	ID        uint       `gorm:"primaryKey" json:"id"`
	TargetID  uint       `gorm:"not null;uniqueIndex:idx_earnings_notice_once" json:"target_id"`
	EventKey  string     `gorm:"size:255;not null;uniqueIndex:idx_earnings_notice_once" json:"event_key"`
	NoticeKey string     `gorm:"size:32;not null;uniqueIndex:idx_earnings_notice_once" json:"notice_key"`
	Status    string     `gorm:"size:32;not null;index" json:"status"`
	SentAt    *time.Time `json:"sent_at,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
}

func (EarningsPreviewNotice) TableName() string { return "earnings_preview_notices" }

// CandidateEarningsPreview is the same local Longbridge calendar observation
// for the current small-cap universe. It is intentionally separate from a
// WatchTarget preview: a candidate does not need to be added to monitoring in
// order to appear in the earnings filter.
type CandidateEarningsPreview struct {
	ID        uint       `gorm:"primaryKey" json:"id"`
	Ticker    string     `gorm:"size:32;not null;uniqueIndex" json:"ticker"`
	Provider  string     `gorm:"size:32;not null" json:"provider"`
	Status    string     `gorm:"size:32;not null;index" json:"status"`
	EventKey  string     `gorm:"size:255" json:"event_key,omitempty"`
	ReportAt  *time.Time `gorm:"index" json:"report_at,omitempty"`
	Session   string     `gorm:"size:64" json:"session,omitempty"`
	FetchedAt *time.Time `gorm:"index" json:"fetched_at,omitempty"`
	LastError string     `gorm:"type:text" json:"last_error,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
}

func (CandidateEarningsPreview) TableName() string { return "candidate_earnings_previews" }

// EarningsExpectationSnapshot is append-only. It freezes the provider values
// visible at one point in time so an earnings outcome can never be compared
// with a consensus fetched after the report.
type EarningsExpectationSnapshot struct {
	ID                uint       `gorm:"primaryKey" json:"id"`
	TargetID          uint       `gorm:"not null;index;uniqueIndex:idx_earnings_expectation_identity,priority:1" json:"target_id"`
	Ticker            string     `gorm:"size:32;not null;index" json:"ticker"`
	EventKey          string     `gorm:"size:255;index" json:"event_key,omitempty"`
	FiscalYear        int        `json:"fiscal_year,omitempty"`
	FiscalPeriod      string     `gorm:"size:32" json:"fiscal_period,omitempty"`
	ReportAt          *time.Time `gorm:"index" json:"report_at,omitempty"`
	Currency          string     `gorm:"size:16" json:"currency,omitempty"`
	EPSEstimate       *float64   `json:"eps_estimate,omitempty"`
	EPSActual         *float64   `json:"eps_actual,omitempty"`
	RevenueEstimate   *float64   `json:"revenue_estimate,omitempty"`
	RevenueActual     *float64   `json:"revenue_actual,omitempty"`
	Provider          string     `gorm:"size:32;not null" json:"provider"`
	ProviderUpdatedAt *time.Time `json:"provider_updated_at,omitempty"`
	FetchedAt         time.Time  `gorm:"not null;index" json:"fetched_at"`
	SnapshotHash      string     `gorm:"size:64;not null;uniqueIndex:idx_earnings_expectation_identity,priority:2" json:"snapshot_hash"`
	CreatedAt         time.Time  `json:"created_at"`
}

func (EarningsExpectationSnapshot) TableName() string { return "earnings_expectation_snapshots" }
