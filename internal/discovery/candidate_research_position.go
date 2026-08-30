package discovery

import (
	"context"
	"errors"
	"math"
	"sort"
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
	GateOverride                bool     `json:"gate_override"`
	GateOverrideReason          string   `json:"gate_override_reason"`
}

type CandidateResearchPortfolio struct {
	TotalMaxWeightPct      float64                         `json:"total_max_weight_pct"`
	PositionCount          int                             `json:"position_count"`
	SectorWeights          map[string]float64              `json:"sector_weights"`
	LargestSector          string                          `json:"largest_sector"`
	LargestSectorWeightPct float64                         `json:"largest_sector_weight_pct"`
	LargestPosition        string                          `json:"largest_position"`
	LargestPositionWeight  float64                         `json:"largest_position_weight_pct"`
	TopThreeWeightPct      float64                         `json:"top_three_weight_pct"`
	ConcentrationIndex     float64                         `json:"concentration_index"`
	ReferenceWeightPct     float64                         `json:"reference_weight_pct"`
	WeightedReferencePnL   *float64                        `json:"weighted_reference_return_pct,omitempty"`
	EstimatedDailyCapacity float64                         `json:"estimated_daily_capacity_usd"`
	ConstrainedCount       int                             `json:"constrained_count"`
	BlockedCount           int                             `json:"blocked_count"`
	DataGapCount           int                             `json:"data_gap_count"`
	EventRiskCount         int                             `json:"event_risk_count"`
	UpcomingCatalystCount  int                             `json:"upcoming_catalyst_count"`
	ConstrainedWeightPct   float64                         `json:"constrained_weight_pct"`
	BlockedWeightPct       float64                         `json:"blocked_weight_pct"`
	DataGapWeightPct       float64                         `json:"data_gap_weight_pct"`
	EventRiskWeightPct     float64                         `json:"event_risk_weight_pct"`
	CatalystWeightPct      float64                         `json:"upcoming_catalyst_weight_pct"`
	RiskCoverage           map[string]string               `json:"risk_coverage"`
	RiskAnalysis           CandidatePortfolioRiskAnalysis  `json:"risk_analysis"`
	Warnings               []string                        `json:"warnings"`
	Items                  []CandidateResearchPositionView `json:"items"`
}

type CandidatePortfolioRiskAnalysis struct {
	Benchmark          string                              `json:"benchmark"`
	AsOf               string                              `json:"as_of,omitempty"`
	ObservationDays    int                                 `json:"observation_days"`
	WeightedMarketBeta *float64                            `json:"weighted_market_beta,omitempty"`
	BetaCoveredWeight  float64                             `json:"beta_covered_weight_pct"`
	FactorExposures    []CandidatePortfolioFactorExposure  `json:"factor_exposures"`
	PositionMetrics    []CandidatePortfolioPositionRisk    `json:"position_metrics"`
	Correlations       []CandidatePortfolioCorrelation     `json:"correlations"`
	Scenarios          []CandidatePortfolioStressScenario  `json:"scenarios"`
	SharedEventRisks   []CandidatePortfolioSharedEventRisk `json:"shared_event_risks"`
	Warnings           []string                            `json:"warnings"`
}

type CandidatePortfolioPositionRisk struct {
	Ticker              string   `json:"ticker"`
	WeightPct           float64  `json:"weight_pct"`
	MarketBeta          *float64 `json:"market_beta,omitempty"`
	AnnualVolatility    *float64 `json:"annual_volatility_pct,omitempty"`
	Momentum20Day       *float64 `json:"momentum_20d_pct,omitempty"`
	AverageDollarVolume float64  `json:"average_dollar_volume_usd"`
	ObservationDays     int      `json:"observation_days"`
	Status              string   `json:"status"`
}

type CandidatePortfolioFactorExposure struct {
	Factor      string  `json:"factor"`
	Value       float64 `json:"value"`
	Unit        string  `json:"unit"`
	CoveragePct float64 `json:"coverage_pct"`
	Meaning     string  `json:"meaning"`
}

type CandidatePortfolioCorrelation struct {
	Left            string   `json:"left"`
	Right           string   `json:"right"`
	Correlation     *float64 `json:"correlation,omitempty"`
	ObservationDays int      `json:"observation_days"`
	Status          string   `json:"status"`
}

type CandidatePortfolioStressScenario struct {
	Key              string   `json:"key"`
	Label            string   `json:"label"`
	ShockPct         float64  `json:"shock_pct"`
	EstimatedLossPct *float64 `json:"estimated_loss_pct,omitempty"`
	CoveredWeightPct float64  `json:"covered_weight_pct"`
	Method           string   `json:"method"`
	Status           string   `json:"status"`
}

type CandidatePortfolioSharedEventRisk struct {
	Key       string  `json:"key"`
	Label     string  `json:"label"`
	Count     int     `json:"count"`
	WeightPct float64 `json:"weight_pct"`
	ReviewBy  string  `json:"review_by,omitempty"`
}

type CandidateResearchPositionView struct {
	CandidateResearchPosition
	SectorCategory          string              `json:"sector_category"`
	CurrentPriceUSD         *float64            `json:"current_price_usd,omitempty"`
	ReturnSinceReferencePct *float64            `json:"return_since_reference_pct,omitempty"`
	ResearchReadiness       string              `json:"research_readiness"`
	InvestabilityStatus     string              `json:"investability_status"`
	AverageDollarVolumeUSD  float64             `json:"average_dollar_volume_usd"`
	EstimatedDailyCapacity  float64             `json:"estimated_daily_capacity_usd"`
	NextCatalystAt          *time.Time          `json:"next_catalyst_at,omitempty"`
	RiskFlags               []string            `json:"risk_flags"`
	Quality                 DataQualityMetadata `json:"quality"`
}

func ListCandidateResearchPositions(ctx context.Context, db *gorm.DB) (CandidateResearchPortfolio, error) {
	result := CandidateResearchPortfolio{
		SectorWeights: map[string]float64{}, RiskCoverage: map[string]string{
			"sector": "available", "liquidity": "missing", "reference_pnl": "missing",
			"market_beta": "missing", "style_factors": "missing",
		}, RiskAnalysis: CandidatePortfolioRiskAnalysis{Benchmark: "IWM", FactorExposures: []CandidatePortfolioFactorExposure{}, PositionMetrics: []CandidatePortfolioPositionRisk{}, Correlations: []CandidatePortfolioCorrelation{}, Scenarios: []CandidatePortfolioStressScenario{}, SharedEventRisks: []CandidatePortfolioSharedEventRisk{}, Warnings: []string{}}, Items: []CandidateResearchPositionView{}, Warnings: []string{},
	}
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
	weights := make([]float64, 0, len(positions))
	weightedReferenceReturn := 0.0
	liquidityCovered := 0
	for index, position := range positions {
		result.TotalMaxWeightPct += position.MaxWeightPct
		weights = append(weights, position.MaxWeightPct)
		if position.MaxWeightPct > result.LargestPositionWeight {
			result.LargestPosition, result.LargestPositionWeight = position.Ticker, position.MaxWeightPct
		}
		sector := "未分类赛道"
		view := CandidateResearchPositionView{CandidateResearchPosition: position, SectorCategory: sector, RiskFlags: []string{}, Quality: DataQualityMetadata{Layer: DataLayerDecision, Source: "local_user", AsOf: position.UpdatedAt.UTC().Format(time.RFC3339), QualityStatus: QualityStatusValid}}
		if watchItems[index].LatestScore != nil {
			score := watchItems[index].LatestScore
			sector = stringOrDefault(score.SectorCategory, sector)
			view.ResearchReadiness = score.ResearchReadiness.Status
			view.InvestabilityStatus = score.Investability.Status
			view.AverageDollarVolumeUSD = score.Investability.AverageDollarVolumeUSD
			if view.AverageDollarVolumeUSD > 0 && position.MaxDailyVolumeParticipation > 0 {
				view.EstimatedDailyCapacity = view.AverageDollarVolumeUSD * position.MaxDailyVolumeParticipation / 100
				result.EstimatedDailyCapacity += view.EstimatedDailyCapacity
				liquidityCovered++
			}
			if score.PriceCloseUSD > 0 {
				price := score.PriceCloseUSD
				view.CurrentPriceUSD = &price
				if position.ReferenceCostUSD != nil && *position.ReferenceCostUSD > 0 {
					value := (price / *position.ReferenceCostUSD - 1) * 100
					view.ReturnSinceReferencePct = &value
					result.ReferenceWeightPct += position.MaxWeightPct
					weightedReferenceReturn += value * position.MaxWeightPct
				}
			}
			if score.ResearchReadiness.Status != CandidateResearchReadinessReady {
				view.RiskFlags = append(view.RiskFlags, "research_data_not_ready")
				result.DataGapCount++
				result.DataGapWeightPct += position.MaxWeightPct
			}
			switch score.Investability.Status {
			case InvestabilityBlocked:
				view.RiskFlags = append(view.RiskFlags, "investability_blocked")
				result.BlockedCount++
				result.BlockedWeightPct += position.MaxWeightPct
			case InvestabilityConstrained:
				view.RiskFlags = append(view.RiskFlags, "investability_constrained")
				result.ConstrainedCount++
				result.ConstrainedWeightPct += position.MaxWeightPct
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
			result.DataGapWeightPct += position.MaxWeightPct
		} else {
			view.RiskFlags = append(view.RiskFlags, "current_candidate_unavailable")
			view.Quality.QualityStatus = QualityStatusMissing
			result.DataGapCount++
			result.DataGapWeightPct += position.MaxWeightPct
		}
		if strings.TrimSpace(position.EventRiskNote) != "" {
			view.RiskFlags = append(view.RiskFlags, "manual_event_risk")
			result.EventRiskCount++
			result.EventRiskWeightPct += position.MaxWeightPct
		}
		if watch, ok := watchByTicker[position.Ticker]; ok && watch.CatalystDate != nil {
			date := watch.CatalystDate.UTC()
			view.NextCatalystAt = &date
			if !date.Before(now) && date.Before(now.AddDate(0, 0, 15)) {
				view.RiskFlags = append(view.RiskFlags, "catalyst_within_14_days")
				result.UpcomingCatalystCount++
				result.CatalystWeightPct += position.MaxWeightPct
			}
		}
		if position.MaxWeightPct > 10 {
			view.RiskFlags = append(view.RiskFlags, "single_name_weight_above_10pct")
		}
		view.SectorCategory = sector
		result.SectorWeights[sector] += position.MaxWeightPct
		result.Items = append(result.Items, view)
	}
	sort.Slice(weights, func(i, j int) bool { return weights[i] > weights[j] })
	for index := 0; index < len(weights) && index < 3; index++ {
		result.TopThreeWeightPct += weights[index]
	}
	if result.TotalMaxWeightPct > 0 {
		for _, weight := range weights {
			share := weight / result.TotalMaxWeightPct
			result.ConcentrationIndex += share * share * 100
		}
	}
	if result.ReferenceWeightPct > 0 {
		value := weightedReferenceReturn / result.ReferenceWeightPct
		result.WeightedReferencePnL = &value
		result.RiskCoverage["reference_pnl"] = "partial"
	}
	if len(positions) > 0 {
		switch {
		case liquidityCovered == len(positions):
			result.RiskCoverage["liquidity"] = "available"
		case liquidityCovered > 0:
			result.RiskCoverage["liquidity"] = "partial"
		}
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
	if result.LargestPositionWeight > 10 || result.TopThreeWeightPct > 50 {
		result.Warnings = append(result.Warnings, "position_concentration_high")
	}
	if len(result.Items) > 0 {
		result.RiskAnalysis = buildCandidatePortfolioRiskAnalysis(ctx, db, result.Items, now)
		result.RiskCoverage["market_beta"] = coverageStatus(result.RiskAnalysis.BetaCoveredWeight, result.TotalMaxWeightPct)
		result.RiskCoverage["style_factors"] = coverageStatus(result.RiskAnalysis.BetaCoveredWeight, result.TotalMaxWeightPct)
		if result.RiskCoverage["market_beta"] == "missing" {
			result.Warnings = append(result.Warnings, "market_beta_unavailable")
		}
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

func FindCandidateResearchPosition(ctx context.Context, db *gorm.DB, ticker string) (CandidateResearchPosition, bool, error) {
	var result CandidateResearchPosition
	if db == nil {
		return result, false, errors.New("database is required")
	}
	err := db.WithContext(ctx).Where("ticker = ?", normalizeTicker(ticker)).First(&result).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return result, false, nil
	}
	return result, err == nil, err
}

// ResearchPositionIncreasesRisk distinguishes a new/action-increasing plan
// from a reduction or a notes-only update. A blocked gate must never prevent
// the user from reducing exposure or documenting risk.
func ResearchPositionIncreasesRisk(before CandidateResearchPosition, found bool, input CandidateResearchPositionInput) bool {
	if !found {
		return input.MaxWeightPct > 0 || input.ReferenceCostUSD != nil
	}
	if input.MaxWeightPct > before.MaxWeightPct {
		return true
	}
	return before.ReferenceCostUSD == nil && input.ReferenceCostUSD != nil
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
