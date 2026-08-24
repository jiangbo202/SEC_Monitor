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
	TotalMaxWeightPct      float64                         `json:"total_max_weight_pct"`
	PositionCount          int                             `json:"position_count"`
	SectorWeights          map[string]float64              `json:"sector_weights"`
	LargestSector          string                          `json:"largest_sector"`
	LargestSectorWeightPct float64                         `json:"largest_sector_weight_pct"`
	ConstrainedCount       int                             `json:"constrained_count"`
	BlockedCount           int                             `json:"blocked_count"`
	DataGapCount           int                             `json:"data_gap_count"`
	EventRiskCount         int                             `json:"event_risk_count"`
	UpcomingCatalystCount  int                             `json:"upcoming_catalyst_count"`
	Warnings               []string                        `json:"warnings"`
	Items                  []CandidateResearchPositionView `json:"items"`
}

type CandidateResearchPositionView struct {
	CandidateResearchPosition
	SectorCategory          string              `json:"sector_category"`
	CurrentPriceUSD         *float64            `json:"current_price_usd,omitempty"`
	ReturnSinceReferencePct *float64            `json:"return_since_reference_pct,omitempty"`
	ResearchReadiness       string              `json:"research_readiness"`
	InvestabilityStatus     string              `json:"investability_status"`
	AverageDollarVolumeUSD  float64             `json:"average_dollar_volume_usd"`
	NextCatalystAt          *time.Time          `json:"next_catalyst_at,omitempty"`
	RiskFlags               []string            `json:"risk_flags"`
	Quality                 DataQualityMetadata `json:"quality"`
}

func ListCandidateResearchPositions(ctx context.Context, db *gorm.DB) (CandidateResearchPortfolio, error) {
	result := CandidateResearchPortfolio{SectorWeights: map[string]float64{}, Items: []CandidateResearchPositionView{}, Warnings: []string{}}
	if db == nil {
		return result, errors.New("database is required")
	}
	var positions []CandidateResearchPosition
	if err := db.WithContext(ctx).Order("max_weight_pct DESC, ticker ASC").Find(&positions).Error; err != nil {
		return result, err
	}
	result.PositionCount = len(positions)
	watchItems := make([]CandidateWatchResult, len(positions))
	for i, position := range positions {
		watchItems[i] = CandidateWatchResult{CandidateWatch: CandidateWatch{Ticker: position.Ticker}}
	}
	if len(watchItems) > 0 {
		if err := attachLatestCandidateScores(ctx, db, watchItems); err != nil {
			return result, err
		}
	}
	watchByTicker := map[string]CandidateWatch{}
	if len(positions) > 0 {
		tickers := make([]string, 0, len(positions))
		for _, position := range positions {
			tickers = append(tickers, position.Ticker)
		}
		var watches []CandidateWatch
		if err := db.WithContext(ctx).Where("ticker IN ? AND status = ?", tickers, CandidateWatchStatusActive).Find(&watches).Error; err != nil {
			return result, err
		}
		for _, watch := range watches {
			watchByTicker[watch.Ticker] = watch
		}
	}
	now := time.Now().UTC()
	for index, position := range positions {
		result.TotalMaxWeightPct += position.MaxWeightPct
		sector := "未分类赛道"
		view := CandidateResearchPositionView{CandidateResearchPosition: position, SectorCategory: sector, RiskFlags: []string{}, Quality: DataQualityMetadata{Layer: DataLayerDecision, Source: "local_user", AsOf: position.UpdatedAt.UTC().Format(time.RFC3339), QualityStatus: QualityStatusValid}}
		if watchItems[index].LatestScore != nil {
			score := watchItems[index].LatestScore
			sector = stringOrDefault(score.SectorCategory, sector)
			view.ResearchReadiness = score.ResearchReadiness.Status
			view.InvestabilityStatus = score.Investability.Status
			view.AverageDollarVolumeUSD = score.Investability.AverageDollarVolumeUSD
			if score.PriceCloseUSD > 0 {
				price := score.PriceCloseUSD
				view.CurrentPriceUSD = &price
				if position.ReferenceCostUSD != nil && *position.ReferenceCostUSD > 0 {
					value := (price / *position.ReferenceCostUSD - 1) * 100
					view.ReturnSinceReferencePct = &value
				}
			}
			if score.ResearchReadiness.Status != CandidateResearchReadinessReady {
				view.RiskFlags = append(view.RiskFlags, "research_data_not_ready")
				result.DataGapCount++
			}
			switch score.Investability.Status {
			case InvestabilityBlocked:
				view.RiskFlags = append(view.RiskFlags, "investability_blocked")
				result.BlockedCount++
			case InvestabilityConstrained:
				view.RiskFlags = append(view.RiskFlags, "investability_constrained")
				result.ConstrainedCount++
			}
		} else if position.SecurityID > 0 {
			var security Security
			if err := db.WithContext(ctx).First(&security, position.SecurityID).Error; err == nil {
				sector = ExplainSectorScore(CandidateScoreSnapshot{}, security).Category
			} else if !errors.Is(err, gorm.ErrRecordNotFound) {
				return result, err
			}
			view.RiskFlags = append(view.RiskFlags, "current_candidate_unavailable")
			view.Quality.QualityStatus = QualityStatusMissing
			result.DataGapCount++
		} else {
			view.RiskFlags = append(view.RiskFlags, "current_candidate_unavailable")
			view.Quality.QualityStatus = QualityStatusMissing
			result.DataGapCount++
		}
		if strings.TrimSpace(position.EventRiskNote) != "" {
			view.RiskFlags = append(view.RiskFlags, "manual_event_risk")
			result.EventRiskCount++
		}
		if watch, ok := watchByTicker[position.Ticker]; ok && watch.CatalystDate != nil {
			date := watch.CatalystDate.UTC()
			view.NextCatalystAt = &date
			if !date.Before(now) && date.Before(now.AddDate(0, 0, 15)) {
				view.RiskFlags = append(view.RiskFlags, "catalyst_within_14_days")
				result.UpcomingCatalystCount++
			}
		}
		if position.MaxWeightPct > 10 {
			view.RiskFlags = append(view.RiskFlags, "single_name_weight_above_10pct")
		}
		view.SectorCategory = sector
		result.SectorWeights[sector] += position.MaxWeightPct
		result.Items = append(result.Items, view)
	}
	for sector, weight := range result.SectorWeights {
		if weight > result.LargestSectorWeightPct {
			result.LargestSector, result.LargestSectorWeightPct = sector, weight
		}
	}
	if result.TotalMaxWeightPct > 100 {
		result.Warnings = append(result.Warnings, "total_research_weight_above_100pct")
	}
	if result.LargestSectorWeightPct > 40 {
		result.Warnings = append(result.Warnings, "sector_concentration_above_40pct")
	}
	if result.BlockedCount > 0 {
		result.Warnings = append(result.Warnings, "blocked_investability_positions")
	}
	if result.DataGapCount > 0 {
		result.Warnings = append(result.Warnings, "position_data_gaps")
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
