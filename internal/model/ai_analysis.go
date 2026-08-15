package model

import "time"

// AIAnalysis is an immutable record of a user-triggered third-party model
// request. Nothing in the scheduler writes this table: keeping the request
// explicit makes provider cost and the research trail auditable.
type AIAnalysis struct {
	ID            uint       `gorm:"primaryKey" json:"id"`
	Scope         string     `gorm:"size:32;not null;index" json:"scope"`
	SourceID      string     `gorm:"size:128;index" json:"source_id,omitempty"`
	SourceURL     string     `gorm:"type:text" json:"source_url,omitempty"`
	Ticker        string     `gorm:"size:32;not null;index:idx_ai_analysis_ticker_time,priority:1" json:"ticker"`
	CompanyName   string     `gorm:"size:255" json:"company_name"`
	TargetType    string     `gorm:"size:16" json:"target_type"`
	ProviderID    string     `gorm:"size:64;not null;index" json:"provider_id"`
	ProviderName  string     `gorm:"size:128" json:"provider_name"`
	Model         string     `gorm:"size:128" json:"model"`
	TemplateID    string     `gorm:"size:64;index" json:"template_id"`
	TemplateName  string     `gorm:"size:128" json:"template_name"`
	PromptVersion string     `gorm:"size:32" json:"prompt_version"`
	SystemPrompt  string     `gorm:"type:text" json:"system_prompt,omitempty"`
	UserPrompt    string     `gorm:"type:text" json:"user_prompt,omitempty"`
	InputSHA256   string     `gorm:"size:64;index" json:"input_sha256"`
	InputSnapshot string     `gorm:"type:text" json:"input_snapshot,omitempty"`
	Content       string     `gorm:"type:text" json:"content,omitempty"`
	Status        string     `gorm:"size:16;not null;index" json:"status"`
	DurationMS    int64      `json:"duration_ms"`
	ErrorMessage  string     `gorm:"type:text" json:"error_message,omitempty"`
	RequestedAt   time.Time  `gorm:"index:idx_ai_analysis_ticker_time,priority:2" json:"requested_at"`
	CompletedAt   *time.Time `json:"completed_at,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
}
