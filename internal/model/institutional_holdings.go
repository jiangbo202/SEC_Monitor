package model

import "time"

// InstitutionalFiling is a locally retained SEC Form 13F disclosure, the
// primary source used by Longbridge Terminal's investor view.
type InstitutionalFiling struct {
	ID              uint      `json:"id"`
	AccessionNumber string    `json:"accession_number" gorm:"uniqueIndex;size:32"`
	CIK             string    `json:"cik" gorm:"index;size:16"`
	Firm            string    `json:"firm" gorm:"index;size:255"`
	ReportDate      string    `json:"report_date" gorm:"index;size:16"`
	FilingDate      string    `json:"filing_date" gorm:"size:16"`
	TotalHoldings   int       `json:"total_holdings"`
	TotalValueUSD   int64     `json:"total_value_usd"`
	SourceURL       string    `json:"source_url" gorm:"size:2048"`
	DatasetURL      string    `json:"dataset_url" gorm:"size:2048"`
	FetchedAt       time.Time `json:"fetched_at"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

type InstitutionalPortfolioHolding struct {
	ID              uint      `json:"id"`
	AccessionNumber string    `json:"accession_number" gorm:"uniqueIndex:idx_institutional_portfolio_holding,priority:1;index;size:32"`
	CUSIP           string    `json:"cusip" gorm:"uniqueIndex:idx_institutional_portfolio_holding,priority:2;size:16"`
	Issuer          string    `json:"issuer" gorm:"size:255"`
	TitleOfClass    string    `json:"title_of_class" gorm:"size:64"`
	Shares          int64     `json:"shares"`
	ValueUSD        int64     `json:"value_usd"`
	WeightPct       float64   `json:"weight_pct"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}
