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

var candidateEffectivenessHorizons = []int{1, 5, 20, 60}

const candidateEffectivenessMinimumSamples = 30
const candidateEffectivenessMinimumSignalDates = 5
const candidateEffectivenessRoundTripCostPct = 0.5

type CandidateEffectivenessReport struct {
	GeneratedAt                  time.Time                       `json:"generated_at"`
	Status                       string                          `json:"status"`
	StatusDetail                 string                          `json:"status_detail"`
	ScoringVersion               string                          `json:"scoring_version"`
	MinimumSampleCount           int                             `json:"minimum_sample_count"`
	BenchmarkTicker              string                          `json:"benchmark_ticker"`
	BenchmarkAvailable           bool                            `json:"benchmark_available"`
	BenchmarkHistoryStatus       string                          `json:"benchmark_history_status"`
	BenchmarkHistorySampleDays   int                             `json:"benchmark_history_sample_days"`
	BenchmarkHistoryRequiredDays int                             `json:"benchmark_history_required_days"`
	BenchmarkLatestTradeDate     string                          `json:"benchmark_latest_trade_date"`
	DistinctSignalDates          int                             `json:"distinct_signal_dates"`
	MinimumDistinctSignalDates   int                             `json:"minimum_distinct_signal_dates"`
	CohortSource                 string                          `json:"cohort_source"`
	OutcomeTrackingStatus        string                          `json:"outcome_tracking_status"`
	TrackedOutcomeCount          int                             `json:"tracked_outcome_count"`
	MatureOutcomeCount           int                             `json:"mature_outcome_count"`
	PendingOutcomeCount          int                             `json:"pending_outcome_count"`
	BenchmarkMissingOutcomeCount int                             `json:"benchmark_missing_outcome_count"`
	OutcomeLastEvaluatedAt       *time.Time                      `json:"outcome_last_evaluated_at,omitempty"`
	AssumedRoundTripCostPct      float64                         `json:"assumed_round_trip_cost_pct"`
	Cohorts                      []CandidateEffectivenessCohort  `json:"cohorts"`
	Segments                     []CandidateEffectivenessSegment `json:"segments"`
}

type CandidateEffectivenessSegment struct {
	Dimension      string                       `json:"dimension"`
	Bucket         string                       `json:"bucket"`
	CandidateCount int                          `json:"candidate_count"`
	Window20       CandidateEffectivenessWindow `json:"window_20"`
}

type CandidateEffectivenessCohort struct {
	Grade          string                         `json:"grade"`
	CandidateCount int                            `json:"candidate_count"`
	Windows        []CandidateEffectivenessWindow `json:"windows"`
}

type CandidateEffectivenessWindow struct {
	HorizonDays          int      `json:"horizon_days"`
	SampleCount          int      `json:"sample_count"`
	PendingCount         int      `json:"pending_count"`
	BenchmarkSampleCount int      `json:"benchmark_sample_count"`
	DistinctSignalDates  int      `json:"distinct_signal_dates"`
	VerificationStatus   string   `json:"verification_status"`
	AverageReturnPct     *float64 `json:"average_return_pct"`
	WinRatePct           *float64 `json:"win_rate_pct"`
	MaxDrawdownPct       *float64 `json:"max_drawdown_pct"`
	BenchmarkReturnPct   *float64 `json:"benchmark_return_pct"`
	ExcessReturnPct      *float64 `json:"excess_return_pct"`
	MedianReturnPct      *float64 `json:"median_return_pct"`
	P25ReturnPct         *float64 `json:"p25_return_pct"`
	P75ReturnPct         *float64 `json:"p75_return_pct"`
	NetAverageReturnPct  *float64 `json:"net_average_return_pct"`
	ConfidenceLowPct     *float64 `json:"confidence_low_pct"`
	ConfidenceHighPct    *float64 `json:"confidence_high_pct"`
}

type candidateCohortSeed struct {
	CandidateScoreSnapshot
	SignalEventID   uint
	EventType       string
	BaselineDate    time.Time
	BaselineClose   float64
	SectorCategory  string
	LiquidityBucket string
	MarketCapBucket string
	MarketRegime    string
}

func BuildCandidateEffectiveness(ctx context.Context, db *gorm.DB) (CandidateEffectivenessReport, error) {
	report := CandidateEffectivenessReport{
		GeneratedAt: time.Now().UTC(), BenchmarkTicker: "IWM", Status: "unverified",
		StatusDetail: "尚无达到持有期的有效样本，评分仅用于研究排序", MinimumSampleCount: candidateEffectivenessMinimumSamples,
		BenchmarkHistoryStatus: "missing", BenchmarkHistoryRequiredDays: technicalMA200LookbackDays,
		MinimumDistinctSignalDates: candidateEffectivenessMinimumSignalDates, AssumedRoundTripCostPct: candidateEffectivenessRoundTripCostPct,
		Segments: []CandidateEffectivenessSegment{},
		Cohorts: []CandidateEffectivenessCohort{
			{Grade: "all", Windows: emptyCandidateEffectivenessWindows()},
			{Grade: CandidateGradeA, Windows: emptyCandidateEffectivenessWindows()},
			{Grade: CandidateGradeB, Windows: emptyCandidateEffectivenessWindows()},
		},
	}
	if db == nil {
		return report, errors.New("database is required")
	}
	if ctx == nil {
		return report, errors.New("context is required")
	}
	expectedDate := ""
	if batch, ok, batchErr := currentPublishedPrescreenBatch(ctx, db); batchErr != nil {
		return report, batchErr
	} else if ok {
		expectedDate = batch.EffectiveDate
	}
	benchmarkHistory, err := loadBenchmarkHistoryReadiness(ctx, db, report.BenchmarkTicker, technicalMA200LookbackDays, expectedDate)
	if err != nil {
		return report, err
	}
	report.BenchmarkHistoryStatus = benchmarkHistory.Status
	report.BenchmarkHistorySampleDays = benchmarkHistory.SampleDays
	report.BenchmarkHistoryRequiredDays = benchmarkHistory.Required
	report.BenchmarkLatestTradeDate = benchmarkHistory.LatestDate
	seeds, source, err := candidateCohortSeeds(ctx, db)
	if err != nil {
		return report, err
	}
	report.CohortSource = source
	report.ScoringVersion, err = currentCandidateScoringVersion(ctx, db)
	if err != nil {
		return report, err
	}
	if report.ScoringVersion != "" {
		filtered := make([]candidateCohortSeed, 0, len(seeds))
		for _, seed := range seeds {
			if seed.ScoringVersion == report.ScoringVersion {
				filtered = append(filtered, seed)
			}
		}
		seeds = filtered
	}
	if err := hydrateCandidateEffectivenessDimensions(ctx, db, seeds); err != nil {
		return report, err
	}
	if err := attachCandidateOutcomeTracking(ctx, db, seeds, &report); err != nil {
		return report, err
	}
	segments, err := buildCandidateEffectivenessSegments(ctx, db, seeds, report.BenchmarkTicker)
	if err != nil {
		return report, err
	}
	report.Segments = segments
	signalDates := map[string]struct{}{}
	for _, seed := range seeds {
		date := seed.BaselineDate.Format(time.DateOnly)
		if !seed.BaselineDate.IsZero() {
			signalDates[date] = struct{}{}
		}
	}
	report.DistinctSignalDates = len(signalDates)
	for index := range report.Cohorts {
		grade := report.Cohorts[index].Grade
		selected := make([]candidateCohortSeed, 0, len(seeds))
		for _, seed := range seeds {
			if grade == "all" || seed.Grade == grade {
				selected = append(selected, seed)
			}
		}
		report.Cohorts[index].CandidateCount = len(selected)
		windows, benchmarkAvailable, err := buildCandidateCohortWindows(ctx, db, selected, report.BenchmarkTicker)
		if err != nil {
			return report, err
		}
		report.Cohorts[index].Windows = windows
		report.BenchmarkAvailable = report.BenchmarkAvailable || benchmarkAvailable
	}
	if len(report.Cohorts) > 0 {
		for _, window := range report.Cohorts[0].Windows {
			if window.HorizonDays != 20 {
				continue
			}
			report.Status = window.VerificationStatus
			switch {
			case report.BenchmarkHistoryStatus != "ready":
				report.StatusDetail = "IWM 基准历史未就绪；当前无法验证相对收益"
			case window.VerificationStatus == "validated":
				report.StatusDetail = "20 日样本量、独立信号日期与 IWM 配对覆盖已达到最低验证门槛"
			case window.SampleCount >= candidateEffectivenessMinimumSamples && window.DistinctSignalDates < candidateEffectivenessMinimumSignalDates:
				report.StatusDetail = "20 日证券样本已达到数量门槛，但独立信号日期不足，暂不视为稳定预测能力"
			case window.VerificationStatus == "validating":
				report.StatusDetail = "正在积累 20 日有效样本；当前结果不得解释为稳定预测能力"
			}
		}
	}
	return report, nil
}

func emptyCandidateEffectivenessWindows() []CandidateEffectivenessWindow {
	items := make([]CandidateEffectivenessWindow, 0, len(candidateEffectivenessHorizons))
	for _, horizon := range candidateEffectivenessHorizons {
		items = append(items, CandidateEffectivenessWindow{HorizonDays: horizon, VerificationStatus: "unverified"})
	}
	return items
}

func candidateCohortSeeds(ctx context.Context, db *gorm.DB) ([]candidateCohortSeed, string, error) {
	seeds, err := signalEventCohortSeeds(ctx, db)
	if err != nil {
		return nil, "", err
	}
	if len(seeds) > 0 {
		return seeds, "signal_events", nil
	}
	seeds, err = legacyFirstCandidateCohortSeeds(ctx, db)
	return seeds, "legacy_first_entry", err
}

func signalEventCohortSeeds(ctx context.Context, db *gorm.DB) ([]candidateCohortSeed, error) {
	var events []CandidateSignalEvent
	if err := db.WithContext(ctx).
		Where("event_type IN ?", []string{CandidateSignalEnteredA, CandidateSignalEnteredB, CandidateSignalUpgradedQuality}).
		Order("signal_date ASC").Order("id ASC").
		Find(&events).Error; err != nil {
		return nil, err
	}
	seeds := make([]candidateCohortSeed, 0, len(events))
	for _, event := range events {
		if event.BaselineCloseMicros <= 0 || strings.TrimSpace(event.Ticker) == "" {
			continue
		}
		seeds = append(seeds, candidateCohortSeed{
			CandidateScoreSnapshot: CandidateScoreSnapshot{
				BatchID: event.BatchID, SecurityID: event.SecurityID, Ticker: event.Ticker, Grade: event.Grade,
				TotalScore: event.TotalScore, ScoringVersion: event.ScoringVersion,
				EligibleA: event.Grade == CandidateGradeA, EligibleB: event.Grade == CandidateGradeB,
			},
			SignalEventID: event.ID, EventType: event.EventType, BaselineDate: event.BaselineTradeDate,
			BaselineClose: float64(event.BaselineCloseMicros) / 1_000_000,
		})
	}
	return seeds, nil
}

func legacyFirstCandidateCohortSeeds(ctx context.Context, db *gorm.DB) ([]candidateCohortSeed, error) {
	var rows []candidateCohortSeed
	err := db.WithContext(ctx).
		Table("candidate_score_snapshots AS score").
		Select("score.*").
		Joins("JOIN universe_batches AS batch ON batch.batch_id = score.batch_id").
		Where("batch.kind = ? AND batch.status = ? AND score.grade IN ?", BatchKindPrescreen, BatchStatusPublished, []string{CandidateGradeA, CandidateGradeB}).
		Order("batch.started_at ASC").Order("score.id ASC").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	seen := map[uint]struct{}{}
	first := make([]candidateCohortSeed, 0, len(rows))
	for _, row := range rows {
		if _, ok := seen[row.SecurityID]; ok {
			continue
		}
		seen[row.SecurityID] = struct{}{}
		first = append(first, row)
	}
	return first, nil
}

func buildCandidateCohortWindows(ctx context.Context, db *gorm.DB, seeds []candidateCohortSeed, benchmarkTicker string) ([]CandidateEffectivenessWindow, bool, error) {
	result := emptyCandidateEffectivenessWindows()
	benchmarkAvailable := false
	outcomes := map[uint]map[int]CandidateSignalOutcome{}
	signalIDs := make([]uint, 0, len(seeds))
	for _, seed := range seeds {
		if seed.SignalEventID > 0 {
			signalIDs = append(signalIDs, seed.SignalEventID)
		}
	}
	if len(signalIDs) > 0 {
		var rows []CandidateSignalOutcome
		if err := db.WithContext(ctx).Where("signal_event_id IN ?", signalIDs).Find(&rows).Error; err != nil {
			return result, false, err
		}
		for _, row := range rows {
			if outcomes[row.SignalEventID] == nil {
				outcomes[row.SignalEventID] = map[int]CandidateSignalOutcome{}
			}
			outcomes[row.SignalEventID][row.HorizonDays] = row
		}
	}
	for index, horizon := range candidateEffectivenessHorizons {
		returns := []float64{}
		benchmarkReturns := []float64{}
		matureSignalDates := map[string]struct{}{}
		maxDrawdown := 0.0
		for _, seed := range seeds {
			if outcome, ok := outcomes[seed.SignalEventID][horizon]; ok {
				if outcome.Status == CandidateSignalOutcomePending || outcome.ReturnPct == nil {
					result[index].PendingCount++
					continue
				}
				returns = append(returns, *outcome.ReturnPct)
				matureSignalDates[seed.BaselineDate.Format(time.DateOnly)] = struct{}{}
				if outcome.MaxDrawdownPct != nil && *outcome.MaxDrawdownPct < maxDrawdown {
					maxDrawdown = *outcome.MaxDrawdownPct
				}
				if outcome.BenchmarkReturnPct != nil {
					benchmarkAvailable = true
					benchmarkReturns = append(benchmarkReturns, *outcome.BenchmarkReturnPct)
				}
				continue
			}
			baseDate, baseClose, ok := seed.BaselineDate, seed.BaselineClose, !seed.BaselineDate.IsZero() && seed.BaselineClose > 0
			if !ok {
				var err error
				baseDate, baseClose, ok, err = candidatePerformanceBaseline(ctx, db, CandidateScoreResult{CandidateScoreSnapshot: seed.CandidateScoreSnapshot})
				if err != nil {
					return result, benchmarkAvailable, err
				}
			}
			if !ok {
				continue
			}
			ret, drawdown, mature, err := candidateHorizonReturn(ctx, db, seed.Ticker, baseDate, baseClose, horizon)
			if err != nil {
				return result, benchmarkAvailable, err
			}
			if !mature {
				result[index].PendingCount++
				continue
			}
			returns = append(returns, ret)
			matureSignalDates[baseDate.Format(time.DateOnly)] = struct{}{}
			if drawdown < maxDrawdown {
				maxDrawdown = drawdown
			}
			benchmarkReturn, _, benchmarkMature, err := benchmarkHorizonReturn(ctx, db, benchmarkTicker, baseDate, horizon)
			if err != nil {
				return result, benchmarkAvailable, err
			}
			if benchmarkMature {
				benchmarkAvailable = true
				benchmarkReturns = append(benchmarkReturns, benchmarkReturn)
			}
		}
		if len(returns) == 0 {
			continue
		}
		averageReturn := averageFloat64(returns)
		winRate := 100 * float64(countPositiveReturns(returns)) / float64(len(returns))
		result[index].SampleCount = len(returns)
		result[index].AverageReturnPct = &averageReturn
		result[index].WinRatePct = &winRate
		result[index].MaxDrawdownPct = &maxDrawdown
		median := percentileFloat64(returns, 0.5)
		p25 := percentileFloat64(returns, 0.25)
		p75 := percentileFloat64(returns, 0.75)
		netAverage := averageReturn - candidateEffectivenessRoundTripCostPct
		confidenceLow, confidenceHigh := meanConfidenceInterval95(returns)
		confidenceLow -= candidateEffectivenessRoundTripCostPct
		confidenceHigh -= candidateEffectivenessRoundTripCostPct
		result[index].MedianReturnPct = &median
		result[index].P25ReturnPct = &p25
		result[index].P75ReturnPct = &p75
		result[index].NetAverageReturnPct = &netAverage
		result[index].ConfidenceLowPct = &confidenceLow
		result[index].ConfidenceHighPct = &confidenceHigh
		result[index].BenchmarkSampleCount = len(benchmarkReturns)
		result[index].DistinctSignalDates = len(matureSignalDates)
		result[index].VerificationStatus = "validating"
		if len(returns) >= candidateEffectivenessMinimumSamples && len(benchmarkReturns) == len(returns) && len(matureSignalDates) >= candidateEffectivenessMinimumSignalDates {
			result[index].VerificationStatus = "validated"
		}
		if len(benchmarkReturns) > 0 {
			benchmarkReturn := averageFloat64(benchmarkReturns)
			excessReturn := averageReturn - benchmarkReturn
			result[index].BenchmarkReturnPct = &benchmarkReturn
			result[index].ExcessReturnPct = &excessReturn
		}
	}
	return result, benchmarkAvailable, nil
}

func currentCandidateScoringVersion(ctx context.Context, db *gorm.DB) (string, error) {
	var pointer CurrentBatchPointer
	if err := db.WithContext(ctx).Where("kind = ?", BatchKindPrescreen).First(&pointer).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "", nil
		}
		return "", err
	}
	var score CandidateScoreSnapshot
	if err := db.WithContext(ctx).Where("batch_id = ? AND scoring_version <> ''", pointer.BatchID).Order("id DESC").First(&score).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "", nil
		}
		return "", err
	}
	return score.ScoringVersion, nil
}

func attachCandidateOutcomeTracking(ctx context.Context, db *gorm.DB, seeds []candidateCohortSeed, report *CandidateEffectivenessReport) error {
	report.OutcomeTrackingStatus = "not_started"
	ids := make([]uint, 0, len(seeds))
	for _, seed := range seeds {
		if seed.SignalEventID > 0 {
			ids = append(ids, seed.SignalEventID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	var outcomes []CandidateSignalOutcome
	if err := db.WithContext(ctx).Where("signal_event_id IN ?", ids).Find(&outcomes).Error; err != nil {
		return err
	}
	if len(outcomes) == 0 {
		return nil
	}
	report.TrackedOutcomeCount = len(outcomes)
	for _, outcome := range outcomes {
		switch outcome.Status {
		case CandidateSignalOutcomeMature:
			report.MatureOutcomeCount++
		case CandidateSignalOutcomeBenchmarkMissing:
			report.BenchmarkMissingOutcomeCount++
		default:
			report.PendingOutcomeCount++
		}
		if report.OutcomeLastEvaluatedAt == nil || outcome.EvaluatedAt.After(*report.OutcomeLastEvaluatedAt) {
			value := outcome.EvaluatedAt
			report.OutcomeLastEvaluatedAt = &value
		}
	}
	report.OutcomeTrackingStatus = "tracking"
	if len(outcomes) == len(ids)*len(candidateEffectivenessHorizons) {
		report.OutcomeTrackingStatus = "current"
	}
	return nil
}

func candidateHorizonReturn(ctx context.Context, db *gorm.DB, ticker string, baseDate time.Time, baseClose float64, horizon int) (float64, float64, bool, error) {
	if baseClose <= 0 || horizon <= 0 {
		return 0, 0, false, nil
	}
	var rows []PriceSnapshot
	err := db.WithContext(ctx).Where("symbol = ? AND trade_date > ? AND quality_status = ?", strings.ToUpper(strings.TrimSpace(ticker)), baseDate, QualityStatusValid).Order("trade_date ASC").Limit(horizon).Find(&rows).Error
	if err != nil {
		return 0, 0, false, err
	}
	if len(rows) < horizon {
		return 0, 0, false, nil
	}
	peak := baseClose
	maxDrawdown := 0.0
	for _, row := range rows {
		close := float64(row.CloseMicros) / 1_000_000
		if close > peak {
			peak = close
		}
		if peak > 0 {
			drawdown := (close/peak - 1) * 100
			if drawdown < maxDrawdown {
				maxDrawdown = drawdown
			}
		}
	}
	close := float64(rows[horizon-1].CloseMicros) / 1_000_000
	return (close/baseClose - 1) * 100, maxDrawdown, true, nil
}

func benchmarkHorizonReturn(ctx context.Context, db *gorm.DB, ticker string, baseDate time.Time, horizon int) (float64, float64, bool, error) {
	var base PriceSnapshot
	err := db.WithContext(ctx).Where("symbol = ? AND trade_date <= ? AND quality_status = ?", strings.ToUpper(strings.TrimSpace(ticker)), baseDate, QualityStatusValid).Order("trade_date DESC").First(&base).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, 0, false, nil
	}
	if err != nil {
		return 0, 0, false, err
	}
	baseClose := float64(base.CloseMicros) / 1_000_000
	return candidateHorizonReturn(ctx, db, ticker, base.TradeDate, baseClose, horizon)
}

func averageFloat64(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	total := 0.0
	for _, value := range values {
		total += value
	}
	return total / float64(len(values))
}

func countPositiveReturns(values []float64) int {
	count := 0
	for _, value := range values {
		if value > 0 {
			count++
		}
	}
	return count
}

func percentileFloat64(values []float64, percentile float64) float64 {
	if len(values) == 0 {
		return 0
	}
	ordered := append([]float64{}, values...)
	sort.Float64s(ordered)
	if len(ordered) == 1 {
		return ordered[0]
	}
	position := percentile * float64(len(ordered)-1)
	lower := int(math.Floor(position))
	upper := int(math.Ceil(position))
	if lower == upper {
		return ordered[lower]
	}
	weight := position - float64(lower)
	return ordered[lower]*(1-weight) + ordered[upper]*weight
}

func meanConfidenceInterval95(values []float64) (float64, float64) {
	mean := averageFloat64(values)
	if len(values) < 2 {
		return mean, mean
	}
	variance := 0.0
	for _, value := range values {
		variance += (value - mean) * (value - mean)
	}
	standardError := math.Sqrt(variance/float64(len(values)-1)) / math.Sqrt(float64(len(values)))
	margin := 1.96 * standardError
	return mean - margin, mean + margin
}
