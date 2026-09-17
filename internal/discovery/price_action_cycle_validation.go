package discovery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var priceActionValidationHorizons = []int{1, 5, 20, 60}

const (
	priceActionMinimumSamples     = 30
	priceActionMinimumSignalDates = 5
	priceActionBenchmarkCoverage  = 90.0
)

type PriceActionReplayScope struct {
	Ticker string `json:"ticker"`
	Source string `json:"source"`
}

type PriceActionOutcome struct {
	HorizonDays        int      `json:"horizon_days"`
	Status             string   `json:"status"`
	OutcomeDate        string   `json:"outcome_date,omitempty"`
	ReturnPct          *float64 `json:"return_pct,omitempty"`
	BenchmarkReturnPct *float64 `json:"benchmark_return_pct,omitempty"`
	ExcessReturnPct    *float64 `json:"excess_return_pct,omitempty"`
}

type PriceActionReplayResult struct {
	StartedAt       time.Time `json:"started_at"`
	CompletedAt     time.Time `json:"completed_at"`
	DurationMS      int64     `json:"duration_ms"`
	TickerCount     int       `json:"ticker_count"`
	ProfileCount    int       `json:"profile_count"`
	EventCount      int       `json:"event_count"`
	PersistedCount  int       `json:"persisted_count"`
	TimelineCount   int       `json:"timeline_snapshot_count"`
	TimelineCreated int64     `json:"timeline_created_count"`
	SkippedAdjusted int       `json:"skipped_adjustment_review"`
	LatestTradeDate string    `json:"latest_trade_date"`
	TargetTradeDate string    `json:"target_trade_date"`
	ProductStatus   string    `json:"product_status"`
	CurrentCount    int       `json:"current_count"`
	StaleCount      int       `json:"stale_count"`
	MissingCount    int       `json:"missing_count"`
	RepairAttempted int       `json:"repair_attempted"`
	RepairSucceeded int       `json:"repair_succeeded"`
	RepairFailed    int       `json:"repair_failed"`
	StaleTickers    []string  `json:"stale_tickers"`
	MissingTickers  []string  `json:"missing_tickers"`
}

type PriceActionEffectivenessWindow struct {
	HorizonDays          int      `json:"horizon_days"`
	SampleCount          int      `json:"sample_count"`
	PendingCount         int      `json:"pending_count"`
	BenchmarkSampleCount int      `json:"benchmark_sample_count"`
	DistinctSignalDates  int      `json:"distinct_signal_dates"`
	AverageReturnPct     *float64 `json:"average_return_pct,omitempty"`
	WinRatePct           *float64 `json:"win_rate_pct,omitempty"`
	AverageBenchmarkPct  *float64 `json:"average_benchmark_pct,omitempty"`
	AverageExcessPct     *float64 `json:"average_excess_pct,omitempty"`
	ConfidenceLowPct     *float64 `json:"confidence_low_pct,omitempty"`
	ConfidenceHighPct    *float64 `json:"confidence_high_pct,omitempty"`
}

type PriceActionPhaseEffectiveness struct {
	Phase                 string                           `json:"phase"`
	EventCount            int                              `json:"event_count"`
	DistinctSignalDates   int                              `json:"distinct_signal_dates"`
	AverageDurationDays   *float64                         `json:"average_duration_days,omitempty"`
	TransitionSuccessPct  *float64                         `json:"transition_success_pct,omitempty"`
	FalseBreakoutPct      *float64                         `json:"false_breakout_pct,omitempty"`
	MaxFavorablePct20     *float64                         `json:"max_favorable_pct_20,omitempty"`
	MaxAdversePct20       *float64                         `json:"max_adverse_pct_20,omitempty"`
	WorstMaxDrawdownPct20 *float64                         `json:"worst_max_drawdown_pct_20,omitempty"`
	AverageRMultiple20    *float64                         `json:"average_r_multiple_20,omitempty"`
	Windows               []PriceActionEffectivenessWindow `json:"windows"`
}

type PriceActionEffectivenessSegment struct {
	Dimension string                         `json:"dimension"`
	Bucket    string                         `json:"bucket"`
	Phase     string                         `json:"phase"`
	Window20  PriceActionEffectivenessWindow `json:"window_20"`
}

type PriceActionEffectivenessReport struct {
	GeneratedAt                 time.Time                         `json:"generated_at"`
	Profile                     string                            `json:"profile"`
	RuleVersion                 string                            `json:"rule_version"`
	Status                      string                            `json:"status"`
	StatusDetail                string                            `json:"status_detail"`
	CanInfluencePriority        bool                              `json:"can_influence_research_priority"`
	EventCount                  int                               `json:"event_count"`
	Mature20Count               int                               `json:"mature_20_count"`
	DistinctSignalDates         int                               `json:"distinct_signal_dates"`
	MinimumSamples              int                               `json:"minimum_samples"`
	MinimumDistinctSignalDates  int                               `json:"minimum_distinct_signal_dates"`
	BenchmarkCoveragePct        float64                           `json:"benchmark_coverage_pct"`
	MinimumBenchmarkCoveragePct float64                           `json:"minimum_benchmark_coverage_pct"`
	LatestSignalDate            string                            `json:"latest_signal_date"`
	LatestOutcomeDate           string                            `json:"latest_outcome_date"`
	Phases                      []PriceActionPhaseEffectiveness   `json:"phases"`
	Segments                    []PriceActionEffectivenessSegment `json:"segments"`
}

type PriceActionShadowComparison struct {
	ActiveProfile        string  `json:"active_profile"`
	ActiveRuleVersion    string  `json:"active_rule_version"`
	ShadowProfile        string  `json:"shadow_profile"`
	ShadowRuleVersion    string  `json:"shadow_rule_version"`
	ComparedTickers      int     `json:"compared_tickers"`
	AgreementCount       int     `json:"agreement_count"`
	DisagreementCount    int     `json:"disagreement_count"`
	AgreementPct         float64 `json:"agreement_pct"`
	ShadowValidated      bool    `json:"shadow_validated"`
	PromotionEligible    bool    `json:"promotion_eligible"`
	PromotionBlockReason string  `json:"promotion_block_reason,omitempty"`
}

type PriceActionCycleConfig struct {
	ActiveProfile   string                   `json:"active_profile"`
	ShadowProfile   string                   `json:"shadow_profile"`
	PreviousProfile string                   `json:"previous_profile"`
	Profiles        []PriceActionRuleProfile `json:"profiles"`
	UpdatedAt       time.Time                `json:"updated_at"`
}

type PriceActionCycleHealth struct {
	Status                  string         `json:"status"`
	ScopeCount              int            `json:"scope_count"`
	ReadyCount              int            `json:"ready_count"`
	CoveragePct             float64        `json:"coverage_pct"`
	OHLCMissingCount        int            `json:"ohlc_missing_count"`
	AdjustmentBlockedCount  int            `json:"adjustment_blocked_count"`
	IWMStatus               string         `json:"iwm_status"`
	IWMSampleDays           int            `json:"iwm_sample_days"`
	IWMLatestTradeDate      string         `json:"iwm_latest_trade_date"`
	ActiveRuleVersion       string         `json:"active_rule_version"`
	RuleVersionDistribution map[string]int `json:"rule_version_distribution"`
	LastReplayStatus        string         `json:"last_replay_status"`
	LastReplayAt            *time.Time     `json:"last_replay_at,omitempty"`
	LastReplayDurationMS    int64          `json:"last_replay_duration_ms"`
	EffectivenessStatus     string         `json:"effectiveness_status"`
	EffectivenessLatestDate string         `json:"effectiveness_latest_date"`
	CurrentThroughLatestIWM bool           `json:"current_through_latest_iwm"`
	BenchmarkCurrent        bool           `json:"benchmark_current"`
	OutcomesCurrent         bool           `json:"outcomes_current"`
	ScopeCurrent            bool           `json:"scope_current"`
	ScopeCurrentCount       int            `json:"scope_current_count"`
	ScopeStaleCount         int            `json:"scope_stale_count"`
	ScopeMissingCount       int            `json:"scope_missing_count"`
	ScopeStaleTickers       []string       `json:"scope_stale_tickers"`
	ScopeMissingTickers     []string       `json:"scope_missing_tickers"`
}

func GetPriceActionCycleConfig(ctx context.Context, db *gorm.DB) (PriceActionCycleConfig, error) {
	setting, err := loadPriceActionCycleSetting(ctx, db)
	return PriceActionCycleConfig{ActiveProfile: setting.ActiveProfile, ShadowProfile: setting.ShadowProfile, PreviousProfile: setting.PreviousProfile, Profiles: PriceActionRuleProfiles(), UpdatedAt: setting.UpdatedAt}, err
}

func loadPriceActionCycleSetting(ctx context.Context, db *gorm.DB) (PriceActionCycleSetting, error) {
	result := PriceActionCycleSetting{ID: 1, ActiveProfile: PriceActionProfileStandard, ShadowProfile: PriceActionProfileConservative}
	if db == nil || ctx == nil {
		return result, errors.New("database and context are required")
	}
	err := db.WithContext(ctx).First(&result, 1).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		err = db.WithContext(ctx).Create(&result).Error
	}
	if result.ActiveProfile == "" {
		result.ActiveProfile = PriceActionProfileStandard
	}
	if result.ShadowProfile == "" || result.ShadowProfile == result.ActiveProfile {
		result.ShadowProfile = defaultShadowProfile(result.ActiveProfile)
	}
	return result, err
}

func defaultShadowProfile(active string) string {
	if active == PriceActionProfileStandard {
		return PriceActionProfileConservative
	}
	return PriceActionProfileStandard
}

func isPriceActionProfile(value string) bool {
	for _, profile := range PriceActionRuleProfiles() {
		if profile.Name == value {
			return true
		}
	}
	return false
}

func UpdatePriceActionCycleConfig(ctx context.Context, db *gorm.DB, active, shadow string) (PriceActionCycleConfig, error) {
	active, shadow = strings.ToLower(strings.TrimSpace(active)), strings.ToLower(strings.TrimSpace(shadow))
	if !isPriceActionProfile(active) || !isPriceActionProfile(shadow) || active == shadow {
		return PriceActionCycleConfig{}, errors.New("active and shadow profiles must be different supported profiles")
	}
	setting, err := loadPriceActionCycleSetting(ctx, db)
	if err != nil {
		return PriceActionCycleConfig{}, err
	}
	if active != setting.ActiveProfile {
		report, reportErr := BuildPriceActionEffectiveness(ctx, db, active)
		if reportErr != nil {
			return PriceActionCycleConfig{}, reportErr
		}
		if !report.CanInfluencePriority {
			return PriceActionCycleConfig{}, fmt.Errorf("shadow profile %s is not validated: %s", active, report.StatusDetail)
		}
		setting.PreviousProfile = setting.ActiveProfile
	}
	setting.ActiveProfile, setting.ShadowProfile = active, shadow
	if err := db.WithContext(ctx).Save(&setting).Error; err != nil {
		return PriceActionCycleConfig{}, err
	}
	return GetPriceActionCycleConfig(ctx, db)
}

func RollbackPriceActionCycleConfig(ctx context.Context, db *gorm.DB) (PriceActionCycleConfig, error) {
	setting, err := loadPriceActionCycleSetting(ctx, db)
	if err != nil {
		return PriceActionCycleConfig{}, err
	}
	if !isPriceActionProfile(setting.PreviousProfile) || setting.PreviousProfile == setting.ActiveProfile {
		return PriceActionCycleConfig{}, errors.New("no previous price-action profile is available")
	}
	current := setting.ActiveProfile
	setting.ActiveProfile = setting.PreviousProfile
	setting.PreviousProfile = current
	if setting.ShadowProfile == setting.ActiveProfile {
		setting.ShadowProfile = current
	}
	if err := db.WithContext(ctx).Save(&setting).Error; err != nil {
		return PriceActionCycleConfig{}, err
	}
	return GetPriceActionCycleConfig(ctx, db)
}

func activePriceActionProfile(ctx context.Context, db *gorm.DB) PriceActionRuleProfile {
	setting, err := loadPriceActionCycleSetting(ctx, db)
	if err != nil {
		return priceActionProfile(PriceActionProfileStandard)
	}
	return priceActionProfile(setting.ActiveProfile)
}

func ReplayPriceActionCycleHistory(ctx context.Context, db *gorm.DB, scope []PriceActionReplayScope, now time.Time) (result PriceActionReplayResult, resultErr error) {
	result.StartedAt = now.UTC()
	if result.StartedAt.IsZero() {
		result.StartedAt = time.Now().UTC()
	}
	setting, err := loadPriceActionCycleSetting(ctx, db)
	if err != nil {
		return result, err
	}
	_ = db.WithContext(ctx).Model(&setting).Updates(map[string]any{"last_replay_status": "running", "last_replay_error": ""}).Error
	defer func() {
		result.CompletedAt = time.Now().UTC()
		result.DurationMS = result.CompletedAt.Sub(result.StartedAt).Milliseconds()
		status := "success"
		if result.ProductStatus == "degraded" {
			status = "degraded"
		}
		updates := map[string]any{"last_replay_status": status, "last_replay_error": "", "last_replay_at": result.CompletedAt, "last_replay_duration_ms": result.DurationMS}
		if resultErr != nil {
			updates["last_replay_status"] = "failed"
			updates["last_replay_error"] = resultErr.Error()
		}
		_ = db.WithContext(context.Background()).Model(&PriceActionCycleSetting{}).Where("id = ?", 1).Updates(updates).Error
	}()

	scope = normalizePriceActionScope(scope)
	result.TickerCount, result.ProfileCount = len(scope), len(PriceActionRuleProfiles())
	securityIDs, err := tradeSetupSecurityIDs(ctx, db, priceActionScopeTickers(scope))
	if err != nil {
		return result, err
	}
	blocked, err := priceActionAdjustmentBlockedSecurities(ctx, db, securityIDs)
	if err != nil {
		return result, err
	}
	iwmRows, err := technicalPriceHistoryForSymbol(ctx, db, "IWM", "", technicalDetailHistoryDays, nil)
	if err != nil {
		return result, err
	}
	result.ProductStatus = "current"
	result.StaleTickers = []string{}
	result.MissingTickers = []string{}
	if len(iwmRows) > 0 {
		result.TargetTradeDate = iwmRows[len(iwmRows)-1].TradeDate.Format(time.DateOnly)
	}
	marketCaps, err := historicalPriceActionMarketCaps(ctx, db, priceActionScopeTickers(scope))
	if err != nil {
		return result, err
	}
	allEvents := make([]PriceActionReplayEvent, 0, len(scope)*12)
	allSnapshots := make([]PriceActionPhaseSnapshot, 0, len(scope)*len(PriceActionRuleProfiles())*200)
	for _, item := range scope {
		rows, err := technicalPriceHistoryForSymbol(ctx, db, item.Ticker, "", technicalDetailHistoryDays, nil)
		if err != nil {
			return result, err
		}
		rows = completePriceActionRows(rows)
		// A same-day quote is not sufficient to produce a trustworthy cycle
		// conclusion. Keep replay status aligned with the health endpoint: fewer
		// than 50 complete candles is an explicit missing/insufficient result,
		// never a misleading "current" conclusion.
		if len(rows) < 50 {
			result.MissingCount++
			result.MissingTickers = append(result.MissingTickers, item.Ticker)
		} else if result.TargetTradeDate != "" && rows[len(rows)-1].TradeDate.Format(time.DateOnly) < result.TargetTradeDate {
			result.StaleCount++
			result.StaleTickers = append(result.StaleTickers, item.Ticker)
		} else {
			result.CurrentCount++
		}
		if len(rows) > 0 && rows[len(rows)-1].TradeDate.Format(time.DateOnly) > result.LatestTradeDate {
			result.LatestTradeDate = rows[len(rows)-1].TradeDate.Format(time.DateOnly)
		}
		if blocked[securityIDs[item.Ticker]] && hasUnadjustedPriceRows(rows) {
			result.SkippedAdjusted++
			continue
		}
		for _, profile := range PriceActionRuleProfiles() {
			events := replayPriceActionTicker(rows, iwmRows, securityIDs[item.Ticker], item, profile, result.StartedAt)
			result.EventCount += len(events)
			for _, event := range events {
				event.MarketCapBucket = historicalMarketCapBucket(marketCaps[item.Ticker], event.SignalDate)
				allEvents = append(allEvents, event)
			}
			snapshots := replayPriceActionTimeline(rows, iwmRows, securityIDs[item.Ticker], item, profile, result.StartedAt)
			result.TimelineCount += len(snapshots)
			allSnapshots = append(allSnapshots, snapshots...)
		}
	}
	if result.StaleCount > 0 || result.MissingCount > 0 || result.TargetTradeDate == "" {
		result.ProductStatus = "degraded"
	}
	sort.Strings(result.StaleTickers)
	sort.Strings(result.MissingTickers)
	if len(allEvents) > 0 {
		outcome := db.WithContext(ctx).Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "ticker"}, {Name: "source"}, {Name: "rule_version"}, {Name: "signal_date"}},
			DoUpdates: clause.AssignmentColumns([]string{"phase", "confidence", "market_regime", "market_cap_bucket", "liquidity_bucket", "relative_strength_bucket", "entry_close_usd", "atr14_usd", "phase_duration_days", "transition_succeeded", "transition_mature", "false_breakout", "false_breakout_mature", "max_favorable_pct20", "max_adverse_pct20", "max_drawdown_pct20", "r_multiple20", "outcomes_json", "replayed_at", "updated_at"}),
		}).CreateInBatches(&allEvents, 200)
		if outcome.Error != nil {
			return result, outcome.Error
		}
		result.PersistedCount = len(allEvents)
	}
	if len(allSnapshots) > 0 {
		outcome := db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).CreateInBatches(&allSnapshots, 200)
		if outcome.Error != nil {
			return result, outcome.Error
		}
		result.TimelineCreated = outcome.RowsAffected
	}
	if err := PersistPriceActionEffectivenessReports(ctx, db); err != nil {
		return result, fmt.Errorf("persist price-action effectiveness: %w", err)
	}
	return result, nil
}

type historicalPriceActionMarketCap struct {
	EffectiveDate string
	MarketCapUSD  int64
}

func historicalPriceActionMarketCaps(ctx context.Context, db *gorm.DB, tickers []string) (map[string][]historicalPriceActionMarketCap, error) {
	type row struct {
		Ticker        string
		EffectiveDate string
		MarketCapUSD  int64
	}
	var rows []row
	if len(tickers) > 0 {
		if err := db.WithContext(ctx).Table("candidate_score_snapshots").
			Select("candidate_score_snapshots.ticker, universe_batches.effective_date, candidate_score_snapshots.market_cap_usd").
			Joins("JOIN universe_batches ON universe_batches.batch_id = candidate_score_snapshots.batch_id").
			Where("candidate_score_snapshots.ticker IN ?", tickers).
			Order("universe_batches.effective_date ASC").Scan(&rows).Error; err != nil {
			return nil, err
		}
	}
	result := map[string][]historicalPriceActionMarketCap{}
	for _, item := range rows {
		ticker := strings.ToUpper(strings.TrimSpace(item.Ticker))
		result[ticker] = append(result[ticker], historicalPriceActionMarketCap{EffectiveDate: item.EffectiveDate, MarketCapUSD: item.MarketCapUSD})
	}
	return result, nil
}

func historicalMarketCapBucket(rows []historicalPriceActionMarketCap, signalDate string) string {
	value := int64(0)
	for _, row := range rows {
		if row.EffectiveDate > signalDate {
			break
		}
		value = row.MarketCapUSD
	}
	if value <= 0 {
		return "未回溯"
	}
	return candidateMarketCapBucket(value)
}

func normalizePriceActionScope(scope []PriceActionReplayScope) []PriceActionReplayScope {
	merged := map[string]map[string]bool{}
	for _, item := range scope {
		ticker := strings.ToUpper(strings.TrimSpace(item.Ticker))
		if ticker == "" || ticker == "IWM" {
			continue
		}
		if merged[ticker] == nil {
			merged[ticker] = map[string]bool{}
		}
		for _, source := range strings.Split(item.Source, "+") {
			source = strings.TrimSpace(source)
			if source != "" {
				merged[ticker][source] = true
			}
		}
	}
	tickers := make([]string, 0, len(merged))
	for ticker := range merged {
		tickers = append(tickers, ticker)
	}
	sort.Strings(tickers)
	result := make([]PriceActionReplayScope, 0, len(tickers))
	for _, ticker := range tickers {
		sources := make([]string, 0, len(merged[ticker]))
		for source := range merged[ticker] {
			sources = append(sources, source)
		}
		sort.Strings(sources)
		result = append(result, PriceActionReplayScope{Ticker: ticker, Source: strings.Join(sources, "+")})
	}
	return result
}

func priceActionScopeTickers(scope []PriceActionReplayScope) []string {
	result := make([]string, 0, len(scope))
	for _, item := range scope {
		result = append(result, item.Ticker)
	}
	return result
}

func completePriceActionRows(rows []PriceSnapshot) []PriceSnapshot {
	result := make([]PriceSnapshot, 0, len(rows))
	for _, row := range rows {
		if priceSnapshotHasOHLC(row) && priceSnapshotClose(row) > 0 {
			result = append(result, row)
		}
	}
	return result
}

func priceActionAdjustmentBlockedSecurities(ctx context.Context, db *gorm.DB, securityIDs map[string]uint) (map[uint]bool, error) {
	ids := make([]uint, 0, len(securityIDs))
	for _, id := range securityIDs {
		if id > 0 {
			ids = append(ids, id)
		}
	}
	result := map[uint]bool{}
	if len(ids) == 0 {
		return result, nil
	}
	var rows []CapitalRiskSnapshot
	if err := db.WithContext(ctx).Where("security_id IN ? AND active = ? AND kind = ?", ids, true, CapitalEventReverseSplit).Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		result[row.SecurityID] = true
	}
	return result, nil
}

func replayPriceActionTicker(rows, iwmRows []PriceSnapshot, securityID uint, scope PriceActionReplayScope, profile PriceActionRuleProfile, replayedAt time.Time) []PriceActionReplayEvent {
	if len(rows) < 51 {
		return nil
	}
	result := []PriceActionReplayEvent{}
	lastPhase := ""
	lastEventIndex := -1
	for index := 49; index < len(rows); index++ {
		asOf := rows[index].TradeDate
		benchmark := pointInTimePriceWindow(iwmRows, asOf, technicalRelativeLongDays+1)
		relative := buildCandidateRelativeStrengthFromRows(rows[:index+1], benchmark)
		analysis := buildPriceActionCycleAnalysisWithProfile(rows[:index+1], relative, profile)
		if analysis.Phase == lastPhase {
			continue
		}
		lastPhase = analysis.Phase
		if analysis.Status != "ready" || analysis.Phase == PriceActionPhaseUnconfirmed || analysis.Phase == PriceActionPhaseUnavailable {
			continue
		}
		if lastEventIndex >= 0 {
			result[lastEventIndex].PhaseDurationDays = maxInt(1, index-priceActionRowIndex(rows, result[lastEventIndex].SignalDate))
		}
		event := buildPriceActionReplayEvent(rows, iwmRows, index, securityID, scope, analysis, relative, replayedAt)
		result = append(result, event)
		lastEventIndex = len(result) - 1
	}
	if lastEventIndex >= 0 && result[lastEventIndex].PhaseDurationDays == 0 {
		result[lastEventIndex].PhaseDurationDays = maxInt(1, len(rows)-priceActionRowIndex(rows, result[lastEventIndex].SignalDate))
	}
	return result
}

func replayPriceActionTimeline(rows, iwmRows []PriceSnapshot, securityID uint, scope PriceActionReplayScope, profile PriceActionRuleProfile, replayedAt time.Time) []PriceActionPhaseSnapshot {
	if len(rows) == 0 {
		return nil
	}
	startIndex := 49
	if len(rows) < 50 {
		startIndex = len(rows) - 1
	}
	oscillators := calculateCloseOscillators(rows)
	result := make([]PriceActionPhaseSnapshot, 0, len(rows)-startIndex)
	previousPhase := ""
	startedAt := rows[startIndex].TradeDate.UTC()
	for index := startIndex; index < len(rows); index++ {
		asOf := rows[index].TradeDate
		window := rows[:index+1]
		benchmark := pointInTimePriceWindow(iwmRows, asOf, technicalRelativeLongDays+1)
		relative := buildCandidateRelativeStrengthFromRows(window, benchmark)
		analysis := buildPriceActionCycleAnalysisWithProfile(window, relative, profile)
		if analysis.TradeDate == "" {
			continue
		}
		if previousPhase != "" && previousPhase != analysis.Phase {
			startedAt = asOf.UTC()
		}
		evidence, counter := marshalPriceActionEvidence(analysis.Evidence, analysis.CounterEvidence)
		oscillator := oscillators[index]
		source := strings.TrimSpace(scope.Source)
		if source == "" {
			source = strings.TrimSpace(rows[index].Source)
		}
		result = append(result, PriceActionPhaseSnapshot{
			SecurityID: securityID, Ticker: scope.Ticker, Source: source, RuleVersion: analysis.RuleVersion, TradeDate: analysis.TradeDate,
			Status: analysis.Status, Phase: analysis.Phase, PreviousPhase: previousPhase, Confidence: analysis.Confidence,
			EvidenceJSON: string(evidence), CounterEvidenceJSON: string(counter), NextConfirmation: analysis.NextConfirmation, Invalidation: analysis.Invalidation,
			CloseUSD: priceSnapshotClose(rows[index]), RSI14: oscillator.RSI14, KDJ_K: oscillator.K, KDJ_D: oscillator.D, KDJ_J: oscillator.J, KDJMethod: oscillator.KDJMethod,
			EMA10USD: analysis.EMA10USD, EMA20USD: analysis.EMA20USD, EMA50USD: analysis.EMA50USD, ATR14USD: analysis.ATR14USD,
			VolumeRatio20: analysis.VolumeRatio20, RelativeIWM20DPct: relative.ExcessReturn20DPct,
			StartedAt: startedAt, RecordedAt: replayedAt.UTC(),
		})
		previousPhase = analysis.Phase
	}
	return result
}

func priceActionRowIndex(rows []PriceSnapshot, date string) int {
	for index, row := range rows {
		if row.TradeDate.Format(time.DateOnly) == date {
			return index
		}
	}
	return len(rows) - 1
}

func buildPriceActionReplayEvent(rows, iwmRows []PriceSnapshot, index int, securityID uint, scope PriceActionReplayScope, analysis PriceActionCycleAnalysis, relative CandidateRelativeStrength, replayedAt time.Time) PriceActionReplayEvent {
	entry := priceSnapshotClose(rows[index])
	outcomes := make([]PriceActionOutcome, 0, len(priceActionValidationHorizons))
	for _, horizon := range priceActionValidationHorizons {
		outcome := PriceActionOutcome{HorizonDays: horizon, Status: "pending"}
		if index+horizon < len(rows) {
			end := rows[index+horizon]
			value := percentChange(entry, priceSnapshotClose(end))
			outcome.Status, outcome.OutcomeDate, outcome.ReturnPct = "mature", end.TradeDate.Format(time.DateOnly), float64Ptr(value)
			if startIWM, ok := closeOnDate(iwmRows, rows[index].TradeDate); ok {
				if endIWM, ok := closeOnDate(iwmRows, end.TradeDate); ok {
					benchmark := percentChange(startIWM, endIWM)
					excess := value - benchmark
					outcome.BenchmarkReturnPct, outcome.ExcessReturnPct = float64Ptr(benchmark), float64Ptr(excess)
				}
			}
		}
		outcomes = append(outcomes, outcome)
	}
	outcomesJSON, _ := json.Marshal(outcomes)
	event := PriceActionReplayEvent{SecurityID: securityID, Ticker: scope.Ticker, Source: scope.Source, RuleVersion: analysis.RuleVersion, SignalDate: analysis.TradeDate, Phase: analysis.Phase, Confidence: analysis.Confidence,
		MarketRegime: candidateMarketRegime(pointInTimePriceWindow(iwmRows, rows[index].TradeDate, 21)), MarketCapBucket: "未回溯", LiquidityBucket: candidateLiquidityBucket(pointInTimePriceWindow(rows, rows[index].TradeDate, 20)), RelativeStrengthBucket: priceActionRelativeBucket(relative),
		EntryCloseUSD: entry, ATR14USD: analysis.ATR14USD, OutcomesJSON: string(outcomesJSON), ReplayedAt: replayedAt}
	if index+5 < len(rows) {
		event.FalseBreakoutMature = true
		if analysis.Phase == PriceActionPhaseWedgePop || analysis.Phase == PriceActionPhaseBaseBreak {
			minClose := entry
			for _, row := range rows[index+1 : index+6] {
				minClose = math.Min(minClose, priceSnapshotClose(row))
			}
			event.FalseBreakout = minClose < entry-analysis.ATR14USD
		}
	}
	if index+20 < len(rows) {
		window := rows[index+1 : index+21]
		mfe, mae, drawdown := priceActionExcursions(entry, window)
		event.MaxFavorablePct20, event.MaxAdversePct20, event.MaxDrawdownPct20 = float64Ptr(mfe), float64Ptr(mae), float64Ptr(drawdown)
		if analysis.ATR14USD > 0 && entry > 0 {
			value := percentChange(entry, priceSnapshotClose(rows[index+20])) / (analysis.ATR14USD / entry * 100)
			event.RMultiple20 = float64Ptr(value)
		}
		event.TransitionMature = true
		return20 := percentChange(entry, priceSnapshotClose(rows[index+20]))
		if analysis.Phase == PriceActionPhaseWedgeDrop || analysis.Phase == PriceActionPhaseExhaustionExtension {
			event.TransitionSucceeded = return20 < 0
		} else {
			event.TransitionSucceeded = return20 > 0
		}
	}
	return event
}

func priceActionRelativeBucket(relative CandidateRelativeStrength) string {
	if relative.Status != "ready" || relative.ExcessReturn20DPct == nil {
		return "样本不足"
	}
	switch {
	case *relative.ExcessReturn20DPct >= 5:
		return "强于 IWM ≥5%"
	case *relative.ExcessReturn20DPct <= -5:
		return "弱于 IWM ≤-5%"
	default:
		return "接近 IWM"
	}
}

func closeOnDate(rows []PriceSnapshot, date time.Time) (float64, bool) {
	key := date.Format(time.DateOnly)
	for _, row := range rows {
		if row.TradeDate.Format(time.DateOnly) == key && priceSnapshotClose(row) > 0 {
			return priceSnapshotClose(row), true
		}
	}
	return 0, false
}

func percentChange(start, end float64) float64 {
	if start <= 0 {
		return 0
	}
	return (end/start - 1) * 100
}

func float64Ptr(value float64) *float64 { return &value }

func priceActionExcursions(entry float64, rows []PriceSnapshot) (float64, float64, float64) {
	maxHigh, minLow, peak, maxDrawdown := entry, entry, entry, 0.0
	for _, row := range rows {
		maxHigh = math.Max(maxHigh, priceSnapshotHigh(row))
		minLow = math.Min(minLow, priceSnapshotLow(row))
		close := priceSnapshotClose(row)
		peak = math.Max(peak, close)
		if peak > 0 {
			maxDrawdown = math.Min(maxDrawdown, (close/peak-1)*100)
		}
	}
	return percentChange(entry, maxHigh), percentChange(entry, minLow), maxDrawdown
}

func BuildPriceActionEffectiveness(ctx context.Context, db *gorm.DB, profileName string) (PriceActionEffectivenessReport, error) {
	profile := priceActionProfile(profileName)
	report := PriceActionEffectivenessReport{GeneratedAt: time.Now().UTC(), Profile: profile.Name, RuleVersion: profile.RuleVersion, Status: "unverified", StatusDetail: "尚未完成历史回放", MinimumSamples: priceActionMinimumSamples, MinimumDistinctSignalDates: priceActionMinimumSignalDates, MinimumBenchmarkCoveragePct: priceActionBenchmarkCoverage, Phases: []PriceActionPhaseEffectiveness{}, Segments: []PriceActionEffectivenessSegment{}}
	if db == nil || ctx == nil {
		return report, errors.New("database and context are required")
	}
	var events []PriceActionReplayEvent
	if err := db.WithContext(ctx).Where("rule_version = ?", profile.RuleVersion).Order("signal_date ASC, id ASC").Find(&events).Error; err != nil {
		return report, err
	}
	report.EventCount = len(events)
	if len(events) == 0 {
		return report, nil
	}
	phaseGroups := map[string][]PriceActionReplayEvent{}
	matureDates := map[string]bool{}
	benchmarkCount := 0
	for _, event := range events {
		phaseGroups[event.Phase] = append(phaseGroups[event.Phase], event)
		for _, outcome := range decodePriceActionOutcomes(event.OutcomesJSON) {
			if outcome.HorizonDays == 20 && outcome.Status == "mature" {
				report.Mature20Count++
				matureDates[event.SignalDate] = true
				if outcome.ExcessReturnPct != nil {
					benchmarkCount++
				}
				if outcome.OutcomeDate > report.LatestOutcomeDate {
					report.LatestOutcomeDate = outcome.OutcomeDate
				}
			}
		}
		if event.SignalDate > report.LatestSignalDate {
			report.LatestSignalDate = event.SignalDate
		}
	}
	report.DistinctSignalDates = len(matureDates)
	if report.Mature20Count > 0 {
		report.BenchmarkCoveragePct = float64(benchmarkCount) / float64(report.Mature20Count) * 100
	}
	for _, phase := range []string{PriceActionPhaseReversalExtension, PriceActionPhaseWedgePop, PriceActionPhaseEMACrossback, PriceActionPhaseBaseBreak, PriceActionPhaseExhaustionExtension, PriceActionPhaseWedgeDrop} {
		report.Phases = append(report.Phases, summarizePriceActionPhase(phase, phaseGroups[phase]))
	}
	report.Segments = buildPriceActionSegments(events)
	switch {
	case report.Mature20Count >= priceActionMinimumSamples && report.DistinctSignalDates >= priceActionMinimumSignalDates && report.BenchmarkCoveragePct >= priceActionBenchmarkCoverage:
		report.Status, report.CanInfluencePriority = "validated", true
		report.StatusDetail = "20 日样本、独立信号日期和 IWM 配对覆盖均达到最低门槛"
	case report.Mature20Count > 0:
		report.Status = "validating"
		report.StatusDetail = fmt.Sprintf("继续积累样本：20 日成熟 %d/%d，独立日期 %d/%d，IWM 覆盖 %.1f%%/%.0f%%", report.Mature20Count, priceActionMinimumSamples, report.DistinctSignalDates, priceActionMinimumSignalDates, report.BenchmarkCoveragePct, priceActionBenchmarkCoverage)
	}
	return report, nil
}

// PersistPriceActionEffectivenessReports materializes the expensive aggregate
// after a replay. It runs for all profiles because the UI compares the active
// and shadow rules together.
func PersistPriceActionEffectivenessReports(ctx context.Context, db *gorm.DB) error {
	for _, profile := range PriceActionRuleProfiles() {
		report, err := BuildPriceActionEffectiveness(ctx, db, profile.Name)
		if err != nil {
			return err
		}
		raw, err := json.Marshal(report)
		if err != nil {
			return err
		}
		snapshot := PriceActionEffectivenessSnapshot{Profile: profile.Name, RuleVersion: profile.RuleVersion, ReportJSON: string(raw), GeneratedAt: report.GeneratedAt}
		if err := db.WithContext(ctx).Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "profile"}},
			DoUpdates: clause.AssignmentColumns([]string{"rule_version", "report_json", "generated_at", "updated_at"}),
		}).Create(&snapshot).Error; err != nil {
			return err
		}
	}
	return nil
}

// GetPriceActionEffectiveness returns the materialized report when it matches
// the current rule version. Older installations transparently build once from
// replay facts until the next scheduled replay persists the cache.
func GetPriceActionEffectiveness(ctx context.Context, db *gorm.DB, profileName string) (PriceActionEffectivenessReport, error) {
	profile := priceActionProfile(profileName)
	var snapshot PriceActionEffectivenessSnapshot
	err := db.WithContext(ctx).Where("profile = ? AND rule_version = ?", profile.Name, profile.RuleVersion).First(&snapshot).Error
	if err == nil && strings.TrimSpace(snapshot.ReportJSON) != "" {
		var report PriceActionEffectivenessReport
		if json.Unmarshal([]byte(snapshot.ReportJSON), &report) == nil {
			return report, nil
		}
	}
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return PriceActionEffectivenessReport{}, err
	}
	return BuildPriceActionEffectiveness(ctx, db, profile.Name)
}

func decodePriceActionOutcomes(raw string) []PriceActionOutcome {
	result := []PriceActionOutcome{}
	_ = json.Unmarshal([]byte(raw), &result)
	return result
}

func summarizePriceActionPhase(phase string, events []PriceActionReplayEvent) PriceActionPhaseEffectiveness {
	result := PriceActionPhaseEffectiveness{Phase: phase, EventCount: len(events), Windows: []PriceActionEffectivenessWindow{}}
	dates, durations, successes, falseBreakouts := map[string]bool{}, []float64{}, []float64{}, []float64{}
	mfe, mae, drawdowns, rMultiples := []float64{}, []float64{}, []float64{}, []float64{}
	for _, event := range events {
		dates[event.SignalDate] = true
		if event.PhaseDurationDays > 0 {
			durations = append(durations, float64(event.PhaseDurationDays))
		}
		if event.TransitionMature {
			if event.TransitionSucceeded {
				successes = append(successes, 100)
			} else {
				successes = append(successes, 0)
			}
		}
		if event.FalseBreakoutMature && (phase == PriceActionPhaseWedgePop || phase == PriceActionPhaseBaseBreak) {
			if event.FalseBreakout {
				falseBreakouts = append(falseBreakouts, 100)
			} else {
				falseBreakouts = append(falseBreakouts, 0)
			}
		}
		if event.MaxFavorablePct20 != nil {
			mfe = append(mfe, *event.MaxFavorablePct20)
		}
		if event.MaxAdversePct20 != nil {
			mae = append(mae, *event.MaxAdversePct20)
		}
		if event.MaxDrawdownPct20 != nil {
			drawdowns = append(drawdowns, *event.MaxDrawdownPct20)
		}
		if event.RMultiple20 != nil {
			rMultiples = append(rMultiples, *event.RMultiple20)
		}
	}
	result.DistinctSignalDates = len(dates)
	result.AverageDurationDays = averagePointer(durations)
	result.TransitionSuccessPct = averagePointer(successes)
	result.FalseBreakoutPct = averagePointer(falseBreakouts)
	result.MaxFavorablePct20 = averagePointer(mfe)
	result.MaxAdversePct20 = averagePointer(mae)
	result.WorstMaxDrawdownPct20 = minimumPointer(drawdowns)
	result.AverageRMultiple20 = averagePointer(rMultiples)
	for _, horizon := range priceActionValidationHorizons {
		result.Windows = append(result.Windows, summarizePriceActionWindow(events, horizon, phase))
	}
	return result
}

func summarizePriceActionWindow(events []PriceActionReplayEvent, horizon int, phase string) PriceActionEffectivenessWindow {
	result := PriceActionEffectivenessWindow{HorizonDays: horizon}
	returns, benchmarks, excess := []float64{}, []float64{}, []float64{}
	dates := map[string]bool{}
	wins := 0
	for _, event := range events {
		found := false
		for _, outcome := range decodePriceActionOutcomes(event.OutcomesJSON) {
			if outcome.HorizonDays != horizon {
				continue
			}
			found = true
			if outcome.Status != "mature" || outcome.ReturnPct == nil {
				result.PendingCount++
				break
			}
			value := *outcome.ReturnPct
			returns = append(returns, value)
			dates[event.SignalDate] = true
			if (phase == PriceActionPhaseWedgeDrop || phase == PriceActionPhaseExhaustionExtension) && value < 0 || phase != PriceActionPhaseWedgeDrop && phase != PriceActionPhaseExhaustionExtension && value > 0 {
				wins++
			}
			if outcome.BenchmarkReturnPct != nil {
				benchmarks = append(benchmarks, *outcome.BenchmarkReturnPct)
			}
			if outcome.ExcessReturnPct != nil {
				excess = append(excess, *outcome.ExcessReturnPct)
			}
			break
		}
		if !found {
			result.PendingCount++
		}
	}
	result.SampleCount, result.BenchmarkSampleCount, result.DistinctSignalDates = len(returns), len(excess), len(dates)
	result.AverageReturnPct, result.AverageBenchmarkPct, result.AverageExcessPct = averagePointer(returns), averagePointer(benchmarks), averagePointer(excess)
	if len(returns) > 0 {
		value := float64(wins) / float64(len(returns)) * 100
		result.WinRatePct = &value
	}
	result.ConfidenceLowPct, result.ConfidenceHighPct = meanConfidenceInterval(returns)
	return result
}

func averagePointer(values []float64) *float64 {
	if len(values) == 0 {
		return nil
	}
	value := averageFloat64(values)
	return &value
}

func minimumPointer(values []float64) *float64 {
	if len(values) == 0 {
		return nil
	}
	value := values[0]
	for _, current := range values[1:] {
		value = math.Min(value, current)
	}
	return &value
}

func meanConfidenceInterval(values []float64) (*float64, *float64) {
	if len(values) < 2 {
		return nil, nil
	}
	mean := averageFloat64(values)
	variance := 0.0
	for _, value := range values {
		variance += math.Pow(value-mean, 2)
	}
	standardError := math.Sqrt(variance/float64(len(values)-1)) / math.Sqrt(float64(len(values)))
	low, high := mean-1.96*standardError, mean+1.96*standardError
	return &low, &high
}

func buildPriceActionSegments(events []PriceActionReplayEvent) []PriceActionEffectivenessSegment {
	dimensions := []struct {
		name  string
		value func(PriceActionReplayEvent) string
	}{
		{"market_regime", func(event PriceActionReplayEvent) string { return event.MarketRegime }},
		{"market_cap", func(event PriceActionReplayEvent) string { return event.MarketCapBucket }},
		{"liquidity", func(event PriceActionReplayEvent) string { return event.LiquidityBucket }},
		{"relative_strength", func(event PriceActionReplayEvent) string { return event.RelativeStrengthBucket }},
		{"confidence", func(event PriceActionReplayEvent) string {
			if event.Confidence >= 80 {
				return "≥80%"
			}
			if event.Confidence >= 60 {
				return "60%–79%"
			}
			return "<60%"
		}},
		{"source", func(event PriceActionReplayEvent) string { return event.Source }},
	}
	result := []PriceActionEffectivenessSegment{}
	for _, dimension := range dimensions {
		groups := map[string][]PriceActionReplayEvent{}
		for _, event := range events {
			key := stringOrDefault(dimension.value(event), "未知") + "\x00" + event.Phase
			groups[key] = append(groups[key], event)
		}
		keys := make([]string, 0, len(groups))
		for key := range groups {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			parts := strings.SplitN(key, "\x00", 2)
			result = append(result, PriceActionEffectivenessSegment{Dimension: dimension.name, Bucket: parts[0], Phase: parts[1], Window20: summarizePriceActionWindow(groups[key], 20, parts[1])})
		}
	}
	return result
}

func BuildPriceActionShadowComparison(ctx context.Context, db *gorm.DB, scope []PriceActionReplayScope) (PriceActionShadowComparison, error) {
	setting, err := loadPriceActionCycleSetting(ctx, db)
	if err != nil {
		return PriceActionShadowComparison{}, err
	}
	active, shadow := priceActionProfile(setting.ActiveProfile), priceActionProfile(setting.ShadowProfile)
	result := PriceActionShadowComparison{ActiveProfile: active.Name, ActiveRuleVersion: active.RuleVersion, ShadowProfile: shadow.Name, ShadowRuleVersion: shadow.RuleVersion}
	for _, item := range normalizePriceActionScope(scope) {
		rows, err := technicalPriceHistoryForSymbol(ctx, db, item.Ticker, "", technicalDetailHistoryDays, nil)
		if err != nil {
			return result, err
		}
		rows = completePriceActionRows(rows)
		if len(rows) < 50 {
			continue
		}
		iwm, err := technicalPriceHistoryForSymbol(ctx, db, "IWM", rows[len(rows)-1].Source, technicalRelativeLongDays+1, &rows[len(rows)-1].TradeDate)
		if err != nil {
			return result, err
		}
		relative := buildCandidateRelativeStrengthFromRows(rows, iwm)
		activeResult := buildPriceActionCycleAnalysisWithProfile(rows, relative, active)
		shadowResult := buildPriceActionCycleAnalysisWithProfile(rows, relative, shadow)
		result.ComparedTickers++
		if activeResult.Phase == shadowResult.Phase {
			result.AgreementCount++
		} else {
			result.DisagreementCount++
		}
	}
	if result.ComparedTickers > 0 {
		result.AgreementPct = float64(result.AgreementCount) / float64(result.ComparedTickers) * 100
	}
	report, err := BuildPriceActionEffectiveness(ctx, db, shadow.Name)
	if err != nil {
		return result, err
	}
	result.ShadowValidated, result.PromotionEligible = report.Status == "validated", report.CanInfluencePriority
	if !result.PromotionEligible {
		result.PromotionBlockReason = report.StatusDetail
	}
	return result, nil
}

func BuildPriceActionCycleHealth(ctx context.Context, db *gorm.DB, scope []PriceActionReplayScope) (PriceActionCycleHealth, error) {
	setting, err := loadPriceActionCycleSetting(ctx, db)
	if err != nil {
		return PriceActionCycleHealth{}, err
	}
	profile := priceActionProfile(setting.ActiveProfile)
	result := PriceActionCycleHealth{Status: "ok", ScopeCount: len(normalizePriceActionScope(scope)), ActiveRuleVersion: profile.RuleVersion, RuleVersionDistribution: map[string]int{}, LastReplayStatus: setting.LastReplayStatus, LastReplayAt: setting.LastReplayAt, LastReplayDurationMS: setting.LastReplayDurationMS}
	blockedIDs, err := tradeSetupSecurityIDs(ctx, db, priceActionScopeTickers(normalizePriceActionScope(scope)))
	if err != nil {
		return result, err
	}
	blocked, err := priceActionAdjustmentBlockedSecurities(ctx, db, blockedIDs)
	if err != nil {
		return result, err
	}
	benchmark, err := loadBenchmarkHistoryReadiness(ctx, db, "IWM", technicalMA200LookbackDays, "")
	if err != nil {
		return result, err
	}
	result.IWMStatus, result.IWMSampleDays, result.IWMLatestTradeDate = benchmark.Status, benchmark.SampleDays, benchmark.LatestDate
	result.BenchmarkCurrent = benchmark.Status == "ready" && benchmark.LatestDate != ""
	result.ScopeStaleTickers = []string{}
	result.ScopeMissingTickers = []string{}
	for _, item := range normalizePriceActionScope(scope) {
		rows, err := technicalPriceHistoryForSymbol(ctx, db, item.Ticker, "", technicalDetailHistoryDays, nil)
		if err != nil {
			return result, err
		}
		complete := completePriceActionRows(rows)
		if blocked[blockedIDs[item.Ticker]] && hasUnadjustedPriceRows(rows) {
			result.AdjustmentBlockedCount++
			continue
		}
		if len(complete) < 50 {
			result.OHLCMissingCount++
			result.ScopeMissingCount++
			result.ScopeMissingTickers = append(result.ScopeMissingTickers, item.Ticker)
			continue
		}
		result.ReadyCount++
		latestDate := complete[len(complete)-1].TradeDate.Format(time.DateOnly)
		if result.IWMLatestTradeDate != "" && latestDate < result.IWMLatestTradeDate {
			result.ScopeStaleCount++
			result.ScopeStaleTickers = append(result.ScopeStaleTickers, item.Ticker)
			continue
		}
		result.ScopeCurrentCount++
	}
	if result.ScopeCount > 0 {
		result.CoveragePct = float64(result.ReadyCount) / float64(result.ScopeCount) * 100
	}
	result.ScopeCurrent = result.ScopeStaleCount == 0 && result.ScopeMissingCount == 0 && result.AdjustmentBlockedCount == 0 && result.ScopeCount > 0
	sort.Strings(result.ScopeStaleTickers)
	sort.Strings(result.ScopeMissingTickers)
	var versions []struct {
		RuleVersion string
		Count       int
	}
	if err := db.WithContext(ctx).Model(&PriceActionPhaseSnapshot{}).Select("rule_version, count(*) as count").Group("rule_version").Scan(&versions).Error; err != nil {
		return result, err
	}
	for _, version := range versions {
		result.RuleVersionDistribution[version.RuleVersion] = version.Count
	}
	effectiveness, err := GetPriceActionEffectiveness(ctx, db, setting.ActiveProfile)
	if err != nil {
		return result, err
	}
	result.EffectivenessStatus, result.EffectivenessLatestDate = effectiveness.Status, effectiveness.LatestOutcomeDate
	result.OutcomesCurrent = result.IWMLatestTradeDate != "" && result.EffectivenessLatestDate >= result.IWMLatestTradeDate
	result.CurrentThroughLatestIWM = result.BenchmarkCurrent && result.OutcomesCurrent && result.ScopeCurrent
	if result.CoveragePct < 90 || !result.BenchmarkCurrent || !result.ScopeCurrent || setting.LastReplayStatus == "failed" {
		result.Status = "warning"
	}
	return result, nil
}
