package handler

import (
	"context"
	"errors"
	"strings"

	"sec_monitor/internal/discovery"
	"sec_monitor/internal/service"

	"github.com/gin-gonic/gin"
)

type aiTickerAnalysisRequest struct {
	ProviderID string                           `json:"provider_id"`
	TemplateID string                           `json:"template_id"`
	Evaluation discovery.TickerEvaluationResult `json:"evaluation"`
}

type aiSECFilingAnalysisRequest struct {
	ProviderID string `json:"provider_id"`
	TemplateID string `json:"template_id"`
}

// ListAIProviders returns only safe display fields. API keys remain encrypted
// server-side and are intentionally unavailable to the assessment UI.
func (h *AppHandler) ListAIProviders(c *gin.Context) {
	if h.Configs == nil {
		Error(c, errors.New("configuration service is not configured"))
		return
	}
	providers, err := h.Configs.AIProviderConfigForDisplay(c.Request.Context())
	if err != nil {
		Error(c, err)
		return
	}
	for index := range providers {
		providers[index].APIKey = ""
	}
	OK(c, providers)
}

func (h *AppHandler) GetAIProviderConfig(c *gin.Context) {
	if h.Configs == nil {
		Error(c, errors.New("configuration service is not configured"))
		return
	}
	providers, err := h.Configs.AIProviderConfigForDisplay(c.Request.Context())
	if err != nil {
		Error(c, err)
		return
	}
	OK(c, providers)
}

func (h *AppHandler) UpdateAIProviderConfig(c *gin.Context) {
	if h.Configs == nil {
		Error(c, errors.New("configuration service is not configured"))
		return
	}
	var providers []service.AIProviderConfig
	if err := c.ShouldBindJSON(&providers); err != nil {
		Error(c, service.ErrValidation)
		return
	}
	if err := h.Configs.SaveAIProviders(c.Request.Context(), providers, operator(c)); err != nil {
		Error(c, err)
		return
	}
	providers, err := h.Configs.AIProviderConfigForDisplay(c.Request.Context())
	if err != nil {
		Error(c, err)
		return
	}
	OK(c, providers)
}

func (h *AppHandler) ListAIPromptTemplates(c *gin.Context) {
	if h.Configs == nil {
		Error(c, errors.New("configuration service is not configured"))
		return
	}
	templates, err := h.Configs.AIPromptTemplatesForScope(c.Request.Context(), c.Query("scope"))
	if err != nil {
		Error(c, err)
		return
	}
	OK(c, templates)
}

func (h *AppHandler) UpdateAIPromptTemplates(c *gin.Context) {
	if h.Configs == nil {
		Error(c, errors.New("configuration service is not configured"))
		return
	}
	var templates []service.AIPromptTemplate
	if err := c.ShouldBindJSON(&templates); err != nil {
		Error(c, service.ErrValidation)
		return
	}
	if err := h.Configs.SaveAIPromptTemplates(c.Request.Context(), templates, operator(c)); err != nil {
		Error(c, err)
		return
	}
	templates, err := h.Configs.AIPromptTemplates(c.Request.Context())
	if err != nil {
		Error(c, err)
		return
	}
	OK(c, templates)
}

func (h *AppHandler) GenerateTickerAIAnalysis(c *gin.Context) {
	if h.AIAnalysis == nil {
		Error(c, errors.New("AI analysis service is not configured"))
		return
	}
	var request aiTickerAnalysisRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		Error(c, service.ErrValidation)
		return
	}
	request.ProviderID, request.TemplateID = strings.TrimSpace(request.ProviderID), strings.TrimSpace(request.TemplateID)
	if request.ProviderID == "" || request.TemplateID == "" || strings.TrimSpace(request.Evaluation.Ticker) == "" {
		Error(c, service.ErrValidation)
		return
	}
	result, err := h.AIAnalysis.QueueTickerAnalysis(c.Request.Context(), service.AIAnalysisInput{ProviderID: request.ProviderID, TemplateID: request.TemplateID, Evaluation: request.Evaluation}, operator(c))
	if err != nil {
		Error(c, err)
		return
	}
	go h.runAIAnalysis(result.ID, operator(c))
	Accepted(c, result)
}

// GenerateAIAnalysis accepts a locally displayed detail snapshot. The caller
// must explicitly click the button; this endpoint is not used by schedules or
// background refreshes. Scope lets the audit trail distinguish candidates,
// monitored targets, and future research pages.
func (h *AppHandler) GenerateAIAnalysis(c *gin.Context) {
	if h.AIAnalysis == nil {
		Error(c, errors.New("AI analysis service is not configured"))
		return
	}
	var request service.AIAnalysisInput
	if err := c.ShouldBindJSON(&request); err != nil {
		Error(c, service.ErrValidation)
		return
	}
	request.ProviderID, request.TemplateID, request.Scope = strings.TrimSpace(request.ProviderID), strings.TrimSpace(request.TemplateID), strings.TrimSpace(request.Scope)
	request.Ticker = strings.ToUpper(strings.TrimSpace(request.Ticker))
	if request.ProviderID == "" || request.TemplateID == "" || request.Ticker == "" || request.Context == nil {
		Error(c, service.ErrValidation)
		return
	}
	if request.Scope != "candidate_detail" && request.Scope != "watch_target_detail" {
		Error(c, service.ErrValidation)
		return
	}
	result, err := h.AIAnalysis.QueueTickerAnalysis(c.Request.Context(), request, operator(c))
	if err != nil {
		Error(c, err)
		return
	}
	go h.runAIAnalysis(result.ID, operator(c))
	Accepted(c, result)
}

// GenerateSECFilingAIAnalysis accepts only a persisted filing ID. The SEC URL
// is loaded server-side from that record and fetched by the background worker,
// preventing this manual research feature from becoming an arbitrary URL API.
func (h *AppHandler) GenerateSECFilingAIAnalysis(c *gin.Context) {
	if h.AIAnalysis == nil || h.Filings == nil {
		Error(c, errors.New("AI analysis service is not configured"))
		return
	}
	var request aiSECFilingAnalysisRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		Error(c, service.ErrValidation)
		return
	}
	request.ProviderID, request.TemplateID = strings.TrimSpace(request.ProviderID), strings.TrimSpace(request.TemplateID)
	if request.ProviderID == "" || request.TemplateID == "" {
		Error(c, service.ErrValidation)
		return
	}
	filing, err := h.Filings.Get(c.Request.Context(), uintParam(c, "id"))
	if err != nil {
		Error(c, err)
		return
	}
	result, err := h.AIAnalysis.QueueSECFilingAnalysis(c.Request.Context(), request.ProviderID, request.TemplateID, filing, operator(c))
	if err != nil {
		Error(c, err)
		return
	}
	go h.runAIAnalysis(result.ID, operator(c))
	Accepted(c, result)
}

func (h *AppHandler) runAIAnalysis(id uint, operator string) {
	// A detached context prevents a browser refresh or a client timeout from
	// cancelling an explicitly approved, already-persisted request.
	_, _ = h.AIAnalysis.ProcessTickerAnalysis(context.Background(), id, operator)
}

func (h *AppHandler) ListAIAnalyses(c *gin.Context) {
	if h.AIAnalysis == nil {
		Error(c, errors.New("AI analysis service is not configured"))
		return
	}
	page, pageSize := pageParams(c)
	result, err := h.AIAnalysis.List(c.Request.Context(), service.AIAnalysisListFilter{Ticker: c.Query("ticker"), Scope: c.Query("scope"), Page: page, PageSize: pageSize})
	if err != nil {
		Error(c, err)
		return
	}
	OK(c, result)
}
