package model

import "time"

const AIAnalysisSchemaV1 = "ai-research-v1"

// AIAnalysisStructuredResult is the validated, provider-independent research
// result persisted for new analyses. Stable English enum values make the data
// filterable while all user-facing prose remains Chinese (or the template's
// requested language).
type AIAnalysisStructuredResult struct {
	SchemaVersion       string               `json:"schema_version"`
	Stance              string               `json:"stance"`
	Conclusion          string               `json:"conclusion"`
	Evidence            []AIAnalysisEvidence `json:"evidence"`
	CounterEvidence     []AIAnalysisEvidence `json:"counter_evidence"`
	Invalidation        []string             `json:"invalidation_conditions"`
	Catalysts           []string             `json:"catalysts"`
	DataGaps            []string             `json:"data_gaps"`
	RiskNotes           []string             `json:"risk_notes"`
	EvidenceSufficiency string               `json:"evidence_sufficiency"`
}

type AIAnalysisEvidence struct {
	Fact        string   `json:"fact"`
	Inference   string   `json:"inference"`
	Impact      string   `json:"impact"`
	SourcePaths []string `json:"source_paths"`
}

// AIAnalysis is an immutable record of a user-triggered third-party model
// request. Nothing in the scheduler writes this table: keeping the request
// explicit makes provider cost and the research trail auditable.
type AIAnalysis struct {
	ID                uint                        `gorm:"primaryKey" json:"id"`
	Scope             string                      `gorm:"size:32;not null;index" json:"scope"`
	SourceID          string                      `gorm:"size:128;index" json:"source_id,omitempty"`
	SourceURL         string                      `gorm:"type:text" json:"source_url,omitempty"`
	Ticker            string                      `gorm:"size:32;not null;index:idx_ai_analysis_ticker_time,priority:1" json:"ticker"`
	CompanyName       string                      `gorm:"size:255" json:"company_name"`
	TargetType        string                      `gorm:"size:16" json:"target_type"`
	ProviderID        string                      `gorm:"size:64;not null;index" json:"provider_id"`
	ProviderName      string                      `gorm:"size:128" json:"provider_name"`
	Model             string                      `gorm:"size:128" json:"model"`
	TemplateID        string                      `gorm:"size:64;index" json:"template_id"`
	TemplateName      string                      `gorm:"size:128" json:"template_name"`
	PromptVersion     string                      `gorm:"size:32" json:"prompt_version"`
	SystemPrompt      string                      `gorm:"type:text" json:"system_prompt,omitempty"`
	UserPrompt        string                      `gorm:"type:text" json:"user_prompt,omitempty"`
	InputSHA256       string                      `gorm:"size:64;index" json:"input_sha256"`
	AnalysisKeySHA256 string                      `gorm:"size:64;index" json:"analysis_key_sha256"`
	InputSnapshot     string                      `gorm:"type:text" json:"input_snapshot,omitempty"`
	Content           string                      `gorm:"type:text" json:"content,omitempty"`
	SchemaVersion     string                      `gorm:"size:32;index" json:"schema_version,omitempty"`
	ResultJSON        string                      `gorm:"type:text" json:"-"`
	StructuredResult  *AIAnalysisStructuredResult `gorm:"-" json:"structured_result,omitempty"`
	RequestAttempts   int                         `json:"request_attempts"`
	ResponseMode      string                      `gorm:"size:24" json:"response_mode,omitempty"`
	ValidationWarning string                      `gorm:"type:text" json:"validation_warning,omitempty"`
	ReusedFromID      *uint                       `gorm:"index" json:"reused_from_id,omitempty"`
	Status            string                      `gorm:"size:16;not null;index" json:"status"`
	DurationMS        int64                       `json:"duration_ms"`
	ErrorMessage      string                      `gorm:"type:text" json:"error_message,omitempty"`
	RequestedAt       time.Time                   `gorm:"index:idx_ai_analysis_ticker_time,priority:2" json:"requested_at"`
	CompletedAt       *time.Time                  `json:"completed_at,omitempty"`
	CreatedAt         time.Time                   `json:"created_at"`
}
