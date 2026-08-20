package handler

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"sec_monitor/internal/discovery"
	"sec_monitor/internal/service"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type smallCapPolicyPreviewRequest struct {
	ExpectedActiveVersionID uint                           `json:"expected_active_version_id"`
	Name                    string                         `json:"name"`
	Note                    string                         `json:"note"`
	Criteria                smallCapPolicyEditableCriteria `json:"criteria"`
}

type smallCapPolicyActivateRequest struct {
	ExpectedActiveVersionID uint                           `json:"expected_active_version_id"`
	Name                    string                         `json:"name"`
	Note                    string                         `json:"note"`
	Criteria                smallCapPolicyEditableCriteria `json:"criteria"`
}

type smallCapPolicyRollbackRequest struct {
	ExpectedActiveVersionID uint   `json:"expected_active_version_id"`
	Note                    string `json:"note"`
}

type smallCapPolicyPreviewResponse struct {
	Status           string                               `json:"status"`
	BaseBatchID      string                               `json:"base_batch_id,omitempty"`
	DataAsOf         string                               `json:"data_as_of,omitempty"`
	ActivePolicy     *smallCapPolicyVersionResponse       `json:"active_policy,omitempty"`
	ProposedCriteria discovery.CandidateSelectionCriteria `json:"proposed_criteria"`
	Before           discovery.SmallCapPolicyCounts       `json:"before"`
	After            discovery.SmallCapPolicyCounts       `json:"after"`
	Delta            discovery.SmallCapPolicyCountDelta   `json:"delta"`
	ChangedCount     int                                  `json:"changed_count"`
	Changes          []discovery.SmallCapPolicyChange     `json:"changes"`
	ChangesTruncated bool                                 `json:"changes_truncated"`
	CanActivate      bool                                 `json:"can_activate"`
	Warnings         []string                             `json:"warnings"`
}

type smallCapPolicyEditableCriteria struct {
	MarketCapMinUSD           int64 `json:"market_cap_min_usd"`
	AMarketCapMaxExclusiveUSD int64 `json:"a_market_cap_max_exclusive_usd"`
	BMarketCapMaxExclusiveUSD int64 `json:"b_market_cap_max_exclusive_usd"`
}

type smallCapPolicyVersionResponse struct {
	ID          uint                                 `json:"id"`
	Version     int                                  `json:"version"`
	Status      string                               `json:"status"`
	ContentSHA  string                               `json:"content_sha256"`
	Name        string                               `json:"name"`
	Note        string                               `json:"note,omitempty"`
	CreatedBy   string                               `json:"created_by,omitempty"`
	CreatedAt   time.Time                            `json:"created_at"`
	ActivatedAt *time.Time                           `json:"activated_at,omitempty"`
	Criteria    discovery.CandidateSelectionCriteria `json:"criteria"`
}

type smallCapPolicyStateResponse struct {
	Active  *smallCapPolicyVersionResponse  `json:"active"`
	History []smallCapPolicyVersionResponse `json:"history"`
}

type smallCapPolicyVersionPage struct {
	Items    []smallCapPolicyVersionResponse `json:"items"`
	Total    int                             `json:"total"`
	Page     int                             `json:"page"`
	PageSize int                             `json:"page_size"`
	Pages    int                             `json:"pages"`
}

type smallCapPolicyApplyResponse struct {
	Status  string                                `json:"status"`
	Policy  smallCapPolicyVersionResponse         `json:"policy"`
	Rescore discovery.SmallCapPolicyRescoreResult `json:"rescore,omitempty"`
}

func (h *AppHandler) GetDiscoverySmallCapPolicy(c *gin.Context) {
	active, rows, activatedAt, err := h.smallCapPolicyRows(c)
	if err != nil {
		writeSmallCapPolicyError(c, err)
		return
	}
	history := make([]smallCapPolicyVersionResponse, 0, len(rows))
	for _, row := range rows {
		history = append(history, smallCapPolicyVersionDTO(row, active.ID, activatedAt[row.ID]))
	}
	activeDTO := smallCapPolicyVersionDTO(active, active.ID, activatedAt[active.ID])
	OK(c, smallCapPolicyStateResponse{Active: &activeDTO, History: history})
}

func (h *AppHandler) PreviewDiscoverySmallCapPolicy(c *gin.Context) {
	var input smallCapPolicyPreviewRequest
	if err := c.ShouldBindJSON(&input); err != nil {
		Error(c, service.ErrValidation)
		return
	}
	active, err := discovery.GetActiveSmallCapPolicy(c.Request.Context(), h.DiscoveryDB)
	if err != nil {
		writeSmallCapPolicyError(c, err)
		return
	}
	policy, err := mergeSmallCapPolicyCriteria(active.Policy, input.Criteria)
	if err != nil {
		Error(c, fmt.Errorf("%w: %v", service.ErrValidation, err))
		return
	}
	result, err := discovery.PreviewSmallCapPolicyChange(c.Request.Context(), h.DiscoveryDB, policy)
	if err != nil {
		writeSmallCapPolicyError(c, err)
		return
	}
	// A database without a published market batch legitimately returns a
	// needs_bootstrap preview. It is actionable state, not an HTTP failure.
	status := "ready"
	for _, warning := range result.Warnings {
		if warning == "needs_bootstrap" {
			status = "needs_bootstrap"
			break
		}
	}
	activatedAt, _ := h.smallCapPolicyActivationTimes(c)
	activeDTO := smallCapPolicyVersionDTO(result.ActivePolicy, active.ID, activatedAt[result.ActivePolicy.ID])
	OK(c, smallCapPolicyPreviewResponse{
		Status: status, BaseBatchID: result.BaseBatchID, DataAsOf: result.DataAsOf, ActivePolicy: &activeDTO,
		ProposedCriteria: result.ProposedCriteria, Before: result.Before, After: result.After, Delta: result.Delta,
		ChangedCount: result.ChangedCount, Changes: result.Changes, ChangesTruncated: result.ChangesTruncated,
		CanActivate: result.CanActivate, Warnings: result.Warnings,
	})
}

func (h *AppHandler) ActivateDiscoverySmallCapPolicy(c *gin.Context) {
	var input smallCapPolicyActivateRequest
	if err := c.ShouldBindJSON(&input); err != nil || input.ExpectedActiveVersionID == 0 || strings.TrimSpace(input.Name) == "" {
		Error(c, service.ErrValidation)
		return
	}
	before, err := discovery.GetActiveSmallCapPolicy(c.Request.Context(), h.DiscoveryDB)
	if err != nil {
		writeSmallCapPolicyError(c, err)
		return
	}
	policy, err := mergeSmallCapPolicyCriteria(before.Policy, input.Criteria)
	if err != nil {
		Error(c, fmt.Errorf("%w: %v", service.ErrValidation, err))
		return
	}
	actor := operator(c)
	result, err := discovery.ApplySmallCapPolicy(c.Request.Context(), h.DiscoveryDB, input.ExpectedActiveVersionID, discovery.SmallCapPolicyDraftInput{
		Name: strings.TrimSpace(input.Name), Description: strings.TrimSpace(input.Note), CreatedBy: actor, Policy: policy,
	})
	if err != nil {
		writeSmallCapPolicyError(c, err)
		return
	}
	if h.Audit != nil {
		after, readErr := discovery.GetActiveSmallCapPolicy(c.Request.Context(), h.DiscoveryDB)
		if readErr == nil {
			_ = h.Audit.Record(c.Request.Context(), actor, "activate", "small_cap_policy", strconv.FormatUint(uint64(after.ID), 10), before, result)
		}
	}
	activatedAt, _ := h.smallCapPolicyActivationTimes(c)
	OK(c, smallCapPolicyApplyResponse{Status: result.Status, Policy: smallCapPolicyVersionDTO(result.Policy, result.Policy.ID, activatedAt[result.Policy.ID]), Rescore: result.Rescore})
}

func (h *AppHandler) ListDiscoverySmallCapPolicyVersions(c *gin.Context) {
	active, rows, activatedAt, err := h.smallCapPolicyRows(c)
	if err != nil {
		writeSmallCapPolicyError(c, err)
		return
	}
	page, pageSize := pageParams(c)
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}
	total := len(rows)
	start := (page - 1) * pageSize
	if start > total {
		start = total
	}
	end := start + pageSize
	if end > total {
		end = total
	}
	items := make([]smallCapPolicyVersionResponse, 0, end-start)
	for _, row := range rows[start:end] {
		items = append(items, smallCapPolicyVersionDTO(row, active.ID, activatedAt[row.ID]))
	}
	pages := 0
	if total > 0 {
		pages = (total + pageSize - 1) / pageSize
	}
	OK(c, smallCapPolicyVersionPage{Items: items, Total: total, Page: page, PageSize: pageSize, Pages: pages})
}

func (h *AppHandler) RollbackDiscoverySmallCapPolicy(c *gin.Context) {
	targetID := uintParam(c, "id")
	var input smallCapPolicyRollbackRequest
	if targetID == 0 || c.ShouldBindJSON(&input) != nil || input.ExpectedActiveVersionID == 0 {
		Error(c, service.ErrValidation)
		return
	}
	before, err := discovery.GetActiveSmallCapPolicy(c.Request.Context(), h.DiscoveryDB)
	if err != nil {
		writeSmallCapPolicyError(c, err)
		return
	}
	actor := operator(c)
	result, err := discovery.RollbackSmallCapPolicyWithRescore(c.Request.Context(), h.DiscoveryDB, input.ExpectedActiveVersionID, targetID, actor, strings.TrimSpace(input.Note))
	if err != nil {
		writeSmallCapPolicyError(c, err)
		return
	}
	if h.Audit != nil {
		_ = h.Audit.Record(c.Request.Context(), actor, "rollback", "small_cap_policy", strconv.FormatUint(uint64(targetID), 10), before, result)
	}
	activatedAt, _ := h.smallCapPolicyActivationTimes(c)
	OK(c, smallCapPolicyApplyResponse{Status: result.Status, Policy: smallCapPolicyVersionDTO(result.Policy, result.Policy.ID, activatedAt[result.Policy.ID]), Rescore: result.Rescore})
}

func mergeSmallCapPolicyCriteria(base discovery.SmallCapPolicy, criteria smallCapPolicyEditableCriteria) (discovery.SmallCapPolicy, error) {
	base.MarketCapMinUSD = criteria.MarketCapMinUSD
	base.AMarketCapMaxExclusiveUSD = criteria.AMarketCapMaxExclusiveUSD
	base.MarketCapMaxUSD = criteria.BMarketCapMaxExclusiveUSD
	return discovery.NormalizeSmallCapPolicy(base)
}

func smallCapPolicyVersionDTO(row discovery.SmallCapPolicyVersion, activeID uint, activatedAt *time.Time) smallCapPolicyVersionResponse {
	status := "superseded"
	if row.ID == activeID {
		status = "active"
	} else if row.Status == discovery.SmallCapPolicyStatusDraft {
		status = "draft"
	}
	return smallCapPolicyVersionResponse{
		ID: row.ID, Version: row.Version, Status: status, ContentSHA: row.ContentSHA256,
		Name: row.Name, Note: row.Description, CreatedBy: row.CreatedBy, CreatedAt: row.CreatedAt,
		ActivatedAt: activatedAt, Criteria: discovery.CandidateSelectionCriteriaForPolicy(row.Policy),
	}
}

func (h *AppHandler) smallCapPolicyRows(c *gin.Context) (discovery.SmallCapPolicyVersion, []discovery.SmallCapPolicyVersion, map[uint]*time.Time, error) {
	active, err := discovery.GetActiveSmallCapPolicy(c.Request.Context(), h.DiscoveryDB)
	if err != nil {
		return active, nil, nil, err
	}
	rows, err := discovery.ListSmallCapPolicies(c.Request.Context(), h.DiscoveryDB)
	if err != nil {
		return active, nil, nil, err
	}
	activatedAt, err := h.smallCapPolicyActivationTimes(c)
	return active, rows, activatedAt, err
}

func (h *AppHandler) smallCapPolicyActivationTimes(c *gin.Context) (map[uint]*time.Time, error) {
	var activations []discovery.SmallCapPolicyActivation
	if err := h.DiscoveryDB.WithContext(c.Request.Context()).Order("id ASC").Find(&activations).Error; err != nil {
		return nil, err
	}
	result := make(map[uint]*time.Time, len(activations))
	for _, activation := range activations {
		at := activation.ActivatedAt
		result[activation.PolicyVersionID] = &at
	}
	return result, nil
}

func writeSmallCapPolicyError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, discovery.ErrSmallCapPolicyConflict):
		c.JSON(http.StatusConflict, gin.H{"code": "policy_conflict", "message": err.Error()})
	case errors.Is(err, gorm.ErrRecordNotFound):
		Error(c, service.ErrNotFound)
	default:
		Error(c, err)
	}
}
