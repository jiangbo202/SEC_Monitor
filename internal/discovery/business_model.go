package discovery

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
)

const (
	CandidateBusinessModelCommercial         = "commercial"
	CandidateBusinessModelClinicalPreRevenue = "clinical_pre_revenue"
	CandidateBusinessModelMixedOrLicensing   = "mixed_or_licensing"
	CandidateBusinessModelUnknown            = "unknown"
	CandidateBusinessModelNotApplicable      = "not_applicable"
)

// CandidateBusinessModelEvidence is kept separate from a score: a manual
// confirmation can change over time, while score snapshots remain historical.
type CandidateBusinessModelEvidence struct {
	Model                      string     `json:"model"`
	RevenueRepeatableConfirmed bool       `json:"revenue_repeatable_confirmed"`
	RevenueScoreCap            int        `json:"revenue_score_cap"`
	RevenueScoreCapReason      string     `json:"revenue_score_cap_reason"`
	RequiresReview             bool       `json:"requires_review"`
	Reason                     string     `json:"reason"`
	SourceURL                  string     `json:"source_url"`
	Operator                   string     `json:"operator"`
	ConfirmedAt                *time.Time `json:"confirmed_at,omitempty"`
	ReviewDueAt                *time.Time `json:"review_due_at,omitempty"`
}

type CandidateBusinessModelInput struct {
	Ticker                     string     `json:"ticker"`
	BusinessModel              string     `json:"business_model"`
	RevenueRepeatableConfirmed bool       `json:"revenue_repeatable_confirmed"`
	Reason                     string     `json:"reason"`
	SourceURL                  string     `json:"source_url"`
	Operator                   string     `json:"operator"`
	ReviewDueAt                *time.Time `json:"review_due_at"`
}

func normalizedCandidateBusinessModel(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func validCandidateBusinessModel(value string) bool {
	switch normalizedCandidateBusinessModel(value) {
	case CandidateBusinessModelCommercial, CandidateBusinessModelClinicalPreRevenue, CandidateBusinessModelMixedOrLicensing, CandidateBusinessModelUnknown:
		return true
	default:
		return false
	}
}

func candidateBusinessModelEvidence(override *CandidateBusinessModelOverride, isBiotech bool) CandidateBusinessModelEvidence {
	if !isBiotech {
		return CandidateBusinessModelEvidence{Model: CandidateBusinessModelNotApplicable, RevenueScoreCap: 30}
	}
	if override == nil {
		return CandidateBusinessModelEvidence{
			Model: CandidateBusinessModelUnknown, RevenueScoreCap: 10, RequiresReview: true,
			RevenueScoreCapReason: "生物医药业务模型尚未人工确认；收入增长不作为默认主推荐依据。",
		}
	}
	result := CandidateBusinessModelEvidence{
		Model: normalizedCandidateBusinessModel(override.BusinessModel), RevenueRepeatableConfirmed: override.RevenueRepeatableConfirmed,
		Reason: override.Reason, SourceURL: override.SourceURL, Operator: override.Operator,
		ConfirmedAt: &override.ConfirmedAt, ReviewDueAt: override.ReviewDueAt, RevenueScoreCap: 30,
	}
	if override.ReviewDueAt != nil && !override.ReviewDueAt.After(time.Now().UTC()) {
		result.RequiresReview = true
	}
	switch result.Model {
	case CandidateBusinessModelClinicalPreRevenue:
		result.RevenueScoreCap = 10
		result.RevenueScoreCapReason = "临床前/临床期公司的一次性合作或里程碑收入不可作为完整增长信号；收入分上限为 10。"
	case CandidateBusinessModelMixedOrLicensing:
		if !result.RevenueRepeatableConfirmed {
			result.RevenueScoreCap = 10
			result.RequiresReview = true
			result.RevenueScoreCapReason = "授权/里程碑收入的可重复性尚未确认；收入分上限为 10。"
		}
	case CandidateBusinessModelUnknown:
		result.RevenueScoreCap = 10
		result.RequiresReview = true
		result.RevenueScoreCapReason = "生物医药业务模型尚未确认；收入增长不作为默认主推荐依据。"
	}
	return result
}

func activeCandidateBusinessModels(ctx context.Context, db *gorm.DB, securityIDs []uint) (map[uint]CandidateBusinessModelOverride, error) {
	result := map[uint]CandidateBusinessModelOverride{}
	if len(securityIDs) == 0 {
		return result, nil
	}
	var rows []CandidateBusinessModelOverride
	if err := db.WithContext(ctx).Where("security_id IN ? AND active = ?", securityIDs, true).Order("confirmed_at DESC").Order("id DESC").Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		if _, exists := result[row.SecurityID]; !exists {
			result[row.SecurityID] = row
		}
	}
	return result, nil
}

// UpsertCandidateBusinessModel appends a new confirmation and retires the
// older active confirmation in one transaction, which gives manual research an
// audit trail while making the active classification unambiguous.
func UpsertCandidateBusinessModel(ctx context.Context, db *gorm.DB, input CandidateBusinessModelInput) (CandidateBusinessModelEvidence, error) {
	if db == nil {
		return CandidateBusinessModelEvidence{}, errors.New("database is required")
	}
	if ctx == nil {
		return CandidateBusinessModelEvidence{}, errors.New("context is required")
	}
	input.Ticker = strings.ToUpper(strings.TrimSpace(input.Ticker))
	input.BusinessModel = normalizedCandidateBusinessModel(input.BusinessModel)
	input.Reason = strings.TrimSpace(input.Reason)
	input.Operator = strings.TrimSpace(input.Operator)
	if input.Ticker == "" || !validCandidateBusinessModel(input.BusinessModel) || input.Reason == "" || input.Operator == "" {
		return CandidateBusinessModelEvidence{}, errors.New("ticker, valid business model, reason and operator are required")
	}
	batch, ok, err := currentPublishedPrescreenBatch(ctx, db)
	if err != nil {
		return CandidateBusinessModelEvidence{}, err
	}
	if !ok {
		return CandidateBusinessModelEvidence{}, gorm.ErrRecordNotFound
	}
	var score CandidateScoreSnapshot
	if err := db.WithContext(ctx).First(&score, "batch_id = ? AND ticker = ?", batch.BatchID, input.Ticker).Error; err != nil {
		return CandidateBusinessModelEvidence{}, err
	}
	securityBatchID := strings.TrimSpace(batch.UniverseSourceVersion)
	if securityBatchID == "" {
		securityBatchID = batch.BatchID
	}
	var identity SecurityBatchIdentity
	if err := db.WithContext(ctx).First(&identity, "batch_id = ? AND security_id = ?", securityBatchID, score.SecurityID).Error; err != nil {
		return CandidateBusinessModelEvidence{}, err
	}
	if SectorRatingForSIC(identity.SIC).Category != "生物医药" {
		return CandidateBusinessModelEvidence{}, fmt.Errorf("business model calibration is currently limited to biotech candidates")
	}
	now := time.Now().UTC()
	row := CandidateBusinessModelOverride{
		SecurityID: score.SecurityID, BusinessModel: input.BusinessModel, RevenueRepeatableConfirmed: input.RevenueRepeatableConfirmed,
		Reason: input.Reason, SourceURL: strings.TrimSpace(input.SourceURL), Operator: input.Operator,
		ReviewDueAt: input.ReviewDueAt, ConfirmedAt: now, Active: true, CreatedAt: now, UpdatedAt: now,
	}
	if err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&CandidateBusinessModelOverride{}).Where("security_id = ? AND active = ?", row.SecurityID, true).Updates(map[string]any{"active": false, "updated_at": now}).Error; err != nil {
			return err
		}
		return tx.Create(&row).Error
	}); err != nil {
		return CandidateBusinessModelEvidence{}, err
	}
	return candidateBusinessModelEvidence(&row, true), nil
}

func hydrateCandidateBusinessModels(ctx context.Context, db *gorm.DB, items []CandidateScoreResult) error {
	securityIDs := make([]uint, 0, len(items))
	for _, item := range items {
		securityIDs = append(securityIDs, item.SecurityID)
	}
	overrides, err := activeCandidateBusinessModels(ctx, db, securityIDs)
	if err != nil {
		return err
	}
	for i := range items {
		var selected *CandidateBusinessModelOverride
		if row, ok := overrides[items[i].SecurityID]; ok {
			selected = &row
		}
		items[i].BusinessModel = candidateBusinessModelEvidence(selected, items[i].SectorCategory == "生物医药")
	}
	return nil
}
