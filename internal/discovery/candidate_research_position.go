package discovery

import (
	"context"
	"errors"
	"math"
	"strings"
	"time"

	"gorm.io/gorm"
)

// CandidateResearchPositionInput only stores a user research plan. It is not
// a trading instruction and must never be used by the scheduler or notifier.
type CandidateResearchPositionInput struct {
	Ticker                      string   `json:"ticker"`
	MaxWeightPct                float64  `json:"max_weight_pct"`
	ReferenceCostUSD            *float64 `json:"reference_cost_usd"`
	ClearReferenceCostUSD       bool     `json:"clear_reference_cost_usd"`
	MaxDailyVolumeParticipation float64  `json:"max_daily_volume_participation_pct"`
	EventRiskNote               string   `json:"event_risk_note"`
	LiquidityNote               string   `json:"liquidity_note"`
	Note                        string   `json:"note"`
}

type CandidateResearchPortfolio struct {
	TotalMaxWeightPct float64                     `json:"total_max_weight_pct"`
	PositionCount     int                         `json:"position_count"`
	SectorWeights     map[string]float64          `json:"sector_weights"`
	Items             []CandidateResearchPosition `json:"items"`
}

func ListCandidateResearchPositions(ctx context.Context, db *gorm.DB) (CandidateResearchPortfolio, error) {
	result := CandidateResearchPortfolio{SectorWeights: map[string]float64{}, Items: []CandidateResearchPosition{}}
	if db == nil {
		return result, errors.New("database is required")
	}
	if err := db.WithContext(ctx).Order("max_weight_pct DESC, ticker ASC").Find(&result.Items).Error; err != nil {
		return result, err
	}
	result.PositionCount = len(result.Items)
	for _, position := range result.Items {
		result.TotalMaxWeightPct += position.MaxWeightPct
		sector := "未分类赛道"
		if position.SecurityID > 0 {
			var security Security
			if err := db.WithContext(ctx).First(&security, position.SecurityID).Error; err == nil {
				sector = ExplainSectorScore(CandidateScoreSnapshot{}, security).Category
			} else if !errors.Is(err, gorm.ErrRecordNotFound) {
				return result, err
			}
		}
		result.SectorWeights[sector] += position.MaxWeightPct
	}
	return result, nil
}

func UpsertCandidateResearchPosition(ctx context.Context, db *gorm.DB, input CandidateResearchPositionInput) (CandidateResearchPosition, error) {
	if db == nil {
		return CandidateResearchPosition{}, errors.New("database is required")
	}
	ticker := normalizeTicker(input.Ticker)
	if ticker == "" {
		return CandidateResearchPosition{}, errors.New("ticker is required")
	}
	if !validResearchPercent(input.MaxWeightPct) || !validResearchPercent(input.MaxDailyVolumeParticipation) {
		return CandidateResearchPosition{}, errors.New("weight and daily-volume limits must be between 0 and 100")
	}
	if input.ReferenceCostUSD != nil && (!finitePositive(*input.ReferenceCostUSD)) {
		return CandidateResearchPosition{}, errors.New("reference cost must be a positive finite number")
	}
	position := CandidateResearchPosition{
		Ticker: ticker, MaxWeightPct: input.MaxWeightPct, MaxDailyVolumeParticipation: input.MaxDailyVolumeParticipation,
		EventRiskNote: strings.TrimSpace(input.EventRiskNote), LiquidityNote: strings.TrimSpace(input.LiquidityNote),
		Note: strings.TrimSpace(input.Note), UpdatedAt: time.Now().UTC(),
	}
	if input.ReferenceCostUSD != nil {
		cost := *input.ReferenceCostUSD
		position.ReferenceCostUSD = &cost
	}
	if score, ok, err := currentCandidateScoreByTicker(ctx, db, ticker); err != nil {
		return CandidateResearchPosition{}, err
	} else if ok {
		position.SecurityID = score.SecurityID
	}

	var existing CandidateResearchPosition
	err := db.WithContext(ctx).First(&existing, "ticker = ?", ticker).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		if err := db.WithContext(ctx).Create(&position).Error; err != nil {
			return CandidateResearchPosition{}, err
		}
		return position, nil
	}
	if err != nil {
		return CandidateResearchPosition{}, err
	}
	updates := map[string]any{
		"security_id": position.SecurityID, "max_weight_pct": position.MaxWeightPct,
		"max_daily_volume_participation_pct": position.MaxDailyVolumeParticipation, "event_risk_note": position.EventRiskNote,
		"liquidity_note": position.LiquidityNote, "note": position.Note, "updated_at": position.UpdatedAt,
	}
	if input.ClearReferenceCostUSD {
		updates["reference_cost_usd"] = nil
	} else if position.ReferenceCostUSD != nil {
		updates["reference_cost_usd"] = position.ReferenceCostUSD
	}
	if err := db.WithContext(ctx).Model(&existing).Updates(updates).Error; err != nil {
		return CandidateResearchPosition{}, err
	}
	if err := db.WithContext(ctx).First(&existing, existing.ID).Error; err != nil {
		return CandidateResearchPosition{}, err
	}
	return existing, nil
}

func DeleteCandidateResearchPosition(ctx context.Context, db *gorm.DB, id uint) error {
	if db == nil {
		return errors.New("database is required")
	}
	if id == 0 {
		return errors.New("id is required")
	}
	return db.WithContext(ctx).Delete(&CandidateResearchPosition{}, id).Error
}

func validResearchPercent(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0 && value <= 100
}

func finitePositive(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value > 0
}
