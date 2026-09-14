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
)

const PriceActionCycleRuleVersion = "cycle_v2_daily_timeline"

const (
	PriceActionProfileConservative = "conservative"
	PriceActionProfileStandard     = "standard"
	PriceActionProfileSensitive    = "sensitive"
)

// PriceActionRuleProfile is a bounded, reviewable parameter set. Profiles are
// code-versioned instead of accepting arbitrary production thresholds.
type PriceActionRuleProfile struct {
	Name                  string  `json:"name"`
	Label                 string  `json:"label"`
	RuleVersion           string  `json:"rule_version"`
	EMA10Period           int     `json:"ema_fast_period"`
	EMA20Period           int     `json:"ema_medium_period"`
	EMA50Period           int     `json:"ema_slow_period"`
	ATRPeriod             int     `json:"atr_period"`
	BreakoutLookbackDays  int     `json:"breakout_lookback_days"`
	ATRExtensionThreshold float64 `json:"atr_extension_threshold"`
	VolumeExpansionRatio  float64 `json:"volume_expansion_ratio"`
	ClimaxVolumeRatio     float64 `json:"climax_volume_ratio"`
	RangeContractionPct   float64 `json:"range_contraction_pct"`
	ConfirmationDays      int     `json:"confirmation_days"`
	MinimumConfidence     int     `json:"minimum_confidence"`
}

func PriceActionRuleProfiles() []PriceActionRuleProfile {
	return []PriceActionRuleProfile{
		{Name: PriceActionProfileConservative, Label: "保守", RuleVersion: "cycle_v2_conservative_timeline", EMA10Period: 10, EMA20Period: 21, EMA50Period: 50, ATRPeriod: 14, BreakoutLookbackDays: 20, ATRExtensionThreshold: 2.7, VolumeExpansionRatio: 1.5, ClimaxVolumeRatio: 1.8, RangeContractionPct: 25, ConfirmationDays: 2, MinimumConfidence: 62},
		{Name: PriceActionProfileStandard, Label: "标准", RuleVersion: PriceActionCycleRuleVersion, EMA10Period: 10, EMA20Period: 20, EMA50Period: 50, ATRPeriod: 14, BreakoutLookbackDays: 20, ATRExtensionThreshold: 2.3, VolumeExpansionRatio: 1.2, ClimaxVolumeRatio: 1.5, RangeContractionPct: 20, ConfirmationDays: 1, MinimumConfidence: 50},
		{Name: PriceActionProfileSensitive, Label: "敏感", RuleVersion: "cycle_v2_sensitive_timeline", EMA10Period: 8, EMA20Period: 18, EMA50Period: 45, ATRPeriod: 12, BreakoutLookbackDays: 15, ATRExtensionThreshold: 1.9, VolumeExpansionRatio: 1.05, ClimaxVolumeRatio: 1.3, RangeContractionPct: 15, ConfirmationDays: 1, MinimumConfidence: 45},
	}
}

func priceActionProfile(name string) PriceActionRuleProfile {
	for _, profile := range PriceActionRuleProfiles() {
		if profile.Name == strings.ToLower(strings.TrimSpace(name)) {
			return profile
		}
	}
	return PriceActionRuleProfiles()[1]
}

const (
	PriceActionPhaseUnavailable         = "unavailable"
	PriceActionPhaseUnconfirmed         = "unconfirmed"
	PriceActionPhaseReversalExtension   = "reversal_extension"
	PriceActionPhaseWedgePop            = "wedge_pop"
	PriceActionPhaseEMACrossback        = "ema_crossback"
	PriceActionPhaseBaseBreak           = "base_break"
	PriceActionPhaseExhaustionExtension = "exhaustion_extension"
	PriceActionPhaseWedgeDrop           = "wedge_drop"
)

// PriceActionCycleAnalysis is a deterministic, explainable daily-close
// classification. It is research context only and is deliberately separate
// from the fundamental score and brokerage execution.
type PriceActionCycleAnalysis struct {
	Status              string     `json:"status"`
	Phase               string     `json:"phase"`
	Label               string     `json:"label"`
	Confidence          int        `json:"confidence"`
	RuleVersion         string     `json:"rule_version"`
	TradeDate           string     `json:"trade_date"`
	TargetTradeDate     string     `json:"target_trade_date,omitempty"`
	FreshnessStatus     string     `json:"freshness_status"`
	FreshnessDetail     string     `json:"freshness_detail,omitempty"`
	Evidence            []string   `json:"evidence"`
	CounterEvidence     []string   `json:"counter_evidence"`
	NextConfirmation    string     `json:"next_confirmation"`
	Invalidation        string     `json:"invalidation"`
	EMA10USD            float64    `json:"ema10_usd"`
	EMA20USD            float64    `json:"ema20_usd"`
	EMA50USD            float64    `json:"ema50_usd"`
	MA200USD            float64    `json:"ma200_usd"`
	MA200Available      bool       `json:"ma200_available"`
	ATR14USD            float64    `json:"atr14_usd"`
	DistanceToEMA20ATR  float64    `json:"distance_to_ema20_atr"`
	RangeContractionPct float64    `json:"range_contraction_pct"`
	VolumeRatio20       float64    `json:"volume_ratio_20"`
	StartedAt           *time.Time `json:"started_at,omitempty"`
	DurationTradingDays int        `json:"duration_trading_days"`
}

type PriceActionTimeline struct {
	Ticker                string                     `json:"ticker"`
	RuleVersion           string                     `json:"rule_version"`
	AvailableRuleVersions []string                   `json:"available_rule_versions"`
	ChangesOnly           bool                       `json:"changes_only"`
	Total                 int64                      `json:"total"`
	Items                 []PriceActionPhaseSnapshot `json:"items"`
}

const (
	PriceActionFreshnessCurrent = "current"
	PriceActionFreshnessStale   = "stale"
	PriceActionFreshnessMissing = "missing"
)

// applyPriceActionFreshness keeps classification and data availability
// separate. A previously valid phase remains visible when the latest OHLCV
// has not arrived, but it can no longer masquerade as a current conclusion.
func applyPriceActionFreshness(result *PriceActionCycleAnalysis, targetTradeDate string) {
	if result == nil {
		return
	}
	targetTradeDate = strings.TrimSpace(targetTradeDate)
	result.TargetTradeDate = targetTradeDate
	switch {
	case strings.TrimSpace(result.TradeDate) == "":
		result.FreshnessStatus = PriceActionFreshnessMissing
		result.FreshnessDetail = "尚无可用于价格周期判断的完整 OHLCV"
	case targetTradeDate != "" && result.TradeDate < targetTradeDate:
		result.FreshnessStatus = PriceActionFreshnessStale
		result.FreshnessDetail = fmt.Sprintf("周期结论停留在 %s，目标交易日为 %s", result.TradeDate, targetTradeDate)
	default:
		result.FreshnessStatus = PriceActionFreshnessCurrent
		result.FreshnessDetail = ""
	}
}

type priceActionCandidate struct {
	phase, label, next, invalidation string
	score                            int
	evidence, counter                []string
}

func buildPriceActionCycleAnalysis(rows []PriceSnapshot, relative CandidateRelativeStrength) PriceActionCycleAnalysis {
	return buildPriceActionCycleAnalysisWithProfile(rows, relative, priceActionProfile(PriceActionProfileStandard))
}

func buildPriceActionCycleAnalysisWithProfile(rows []PriceSnapshot, relative CandidateRelativeStrength, profile PriceActionRuleProfile) PriceActionCycleAnalysis {
	result := PriceActionCycleAnalysis{
		Status: PriceActionPhaseUnavailable, Phase: PriceActionPhaseUnavailable, Label: "数据不足",
		FreshnessStatus: PriceActionFreshnessMissing,
		RuleVersion:     profile.RuleVersion, Evidence: []string{}, CounterEvidence: []string{},
		NextConfirmation: "补齐至少 50 个有效交易日的 OHLCV", Invalidation: "-",
	}
	if len(rows) == 0 {
		return result
	}
	allRowCount := len(rows)
	completeRows := make([]PriceSnapshot, 0, len(rows))
	for _, row := range rows {
		if priceSnapshotHasOHLC(row) && priceSnapshotClose(row) > 0 {
			completeRows = append(completeRows, row)
		}
	}
	if len(completeRows) == 0 {
		result.TradeDate = rows[len(rows)-1].TradeDate.Format(time.DateOnly)
		result.Evidence = []string{fmt.Sprintf("完整 OHLC 样本 0/50；本地价格记录共 %d 条", allRowCount)}
		return result
	}
	rows = completeRows
	last := rows[len(rows)-1]
	result.TradeDate = last.TradeDate.Format(time.DateOnly)
	if len(rows) < 50 {
		result.Evidence = append(result.Evidence, fmt.Sprintf("完整 OHLC 样本 %d/50；本地价格记录共 %d 条", len(rows), allRowCount))
		return result
	}

	closes := make([]float64, len(rows))
	for i := range rows {
		closes[i] = priceSnapshotClose(rows[i])
	}
	result.EMA10USD = exponentialMovingAverage(closes, profile.EMA10Period)
	result.EMA20USD = exponentialMovingAverage(closes, profile.EMA20Period)
	result.EMA50USD = exponentialMovingAverage(closes, profile.EMA50Period)
	result.ATR14USD = averageTrueRange(rows, profile.ATRPeriod)
	if len(closes) >= 200 {
		result.MA200USD = averageFloat64(closes[len(closes)-200:])
		result.MA200Available = result.MA200USD > 0
	}
	closeUSD := closes[len(closes)-1]
	if result.ATR14USD > 0 {
		result.DistanceToEMA20ATR = (closeUSD - result.EMA20USD) / result.ATR14USD
	}
	lookback := profile.BreakoutLookbackDays
	if lookback <= 0 || lookback >= len(rows) {
		lookback = 20
	}
	prior20 := rows[len(rows)-lookback-1 : len(rows)-1]
	priorHigh := highestSnapshotHigh(prior20)
	priorLow := lowestSnapshotLow(prior20)
	avgVolume := averageSnapshotVolume(prior20)
	if avgVolume > 0 {
		result.VolumeRatio20 = float64(maxInt64(last.Volume, 0)) / avgVolume
	}
	result.RangeContractionPct = priceRangeContractionPct(rows)
	oscillator := buildCandidateOscillatorAnalysis(rows)
	rsi := 50.0
	if oscillator.RSI14 != nil {
		rsi = *oscillator.RSI14
	}
	uptrend := closeUSD > result.EMA20USD && result.EMA20USD > result.EMA50USD
	nearEMA20 := result.ATR14USD > 0 && math.Abs(closeUSD-result.EMA20USD) <= result.ATR14USD
	relativePositive := relative.Status != "ready" || relative.ExcessReturn20DPct == nil || *relative.ExcessReturn20DPct > 0
	contraction := result.RangeContractionPct >= profile.RangeContractionPct
	volumeExpansion := result.VolumeRatio20 >= profile.VolumeExpansionRatio

	candidates := []priceActionCandidate{}
	add := func(candidate priceActionCandidate) { candidates = append(candidates, candidate) }

	// Risk states take precedence because their purpose is protecting an
	// existing research position, not finding a visually attractive label.
	if closeUSD < priorLow || (closeUSD < result.EMA20USD && result.EMA10USD < result.EMA20USD) {
		score := 58
		evidence := []string{"收盘跌破近期结构或短期均线转为空头排列"}
		if volumeExpansion {
			score += 12
			evidence = append(evidence, "跌破伴随成交量扩张")
		}
		if closeUSD < result.EMA50USD {
			score += 12
			evidence = append(evidence, "收盘低于 EMA50")
		}
		add(priceActionCandidate{phase: PriceActionPhaseWedgeDrop, label: "楔形跌破", score: score, evidence: evidence,
			next: "重新站上 EMA20 并收复最近结构低点", invalidation: "重新站稳 EMA20 后当前跌破判断失效"})
	}
	if uptrend && result.DistanceToEMA20ATR >= profile.ATRExtensionThreshold {
		score := 55
		evidence := []string{fmt.Sprintf("价格高于 EMA20 %.1f ATR", result.DistanceToEMA20ATR)}
		if rsi >= 72 {
			score += 12
			evidence = append(evidence, fmt.Sprintf("RSI(14) %.1f，动量进入高位", rsi))
		}
		if result.VolumeRatio20 >= profile.ClimaxVolumeRatio {
			score += 12
			evidence = append(evidence, "成交量达到 20 日均量 1.5 倍以上")
		}
		add(priceActionCandidate{phase: PriceActionPhaseExhaustionExtension, label: "衰竭延伸", score: score, evidence: evidence,
			next: "观察缩量整理或回归 EMA10/EMA20", invalidation: "价格回归 EMA20 附近且波动收敛后解除衰竭提示"})
	}
	if closeUSD > priorHigh && volumeExpansion {
		phase, label := PriceActionPhaseWedgePop, "楔形突破"
		score := 54
		evidence := []string{"收盘突破前 20 日结构高点", fmt.Sprintf("成交量为 20 日均量 %.2f 倍", result.VolumeRatio20)}
		if contraction {
			score += 12
			evidence = append(evidence, fmt.Sprintf("短期振幅较前序窗口收缩 %.0f%%", result.RangeContractionPct))
		}
		if uptrend {
			phase, label = PriceActionPhaseBaseBreak, "平台突破"
			score += 8
			evidence = append(evidence, "EMA20 位于 EMA50 上方，属于成熟趋势延续")
		}
		if relativePositive {
			score += 6
			evidence = append(evidence, "相对 IWM 强弱未构成反向阻断")
		}
		counter := []string{}
		if !contraction {
			counter = append(counter, "突破前的波动收缩不明显")
		}
		add(priceActionCandidate{phase: phase, label: label, score: score, evidence: evidence, counter: counter,
			next: "等待下一收盘日守住突破位，或回踩缩量确认", invalidation: fmt.Sprintf("收盘重新跌破结构突破位 %.2f", priorHigh)})
	}
	if uptrend && nearEMA20 && closeUSD >= result.EMA20USD {
		score := 56
		evidence := []string{"EMA20 高于 EMA50，趋势结构仍向上", "价格回踩 EMA20 附近后收于其上"}
		if result.VolumeRatio20 <= 1 {
			score += 10
			evidence = append(evidence, "回踩成交量低于 20 日均量")
		}
		if relativePositive {
			score += 6
			evidence = append(evidence, "相对 IWM 强度保持")
		}
		add(priceActionCandidate{phase: PriceActionPhaseEMACrossback, label: "均线回踩", score: score, evidence: evidence,
			next: fmt.Sprintf("放量突破近期结构高点 %.2f", priorHigh), invalidation: fmt.Sprintf("收盘有效跌破 EMA50 %.2f", result.EMA50USD)})
	}
	if !uptrend && closeUSD > result.EMA10USD && rsi >= 40 && result.DistanceToEMA20ATR > -1.2 {
		score := 50
		evidence := []string{"弱势延伸后价格重新站上 EMA10", "动量自低位恢复但长期趋势尚未确认"}
		if result.VolumeRatio20 >= profile.VolumeExpansionRatio {
			score += 10
			evidence = append(evidence, "反转日成交量扩张")
		}
		add(priceActionCandidate{phase: PriceActionPhaseReversalExtension, label: "反转延伸", score: score, evidence: evidence,
			next: "站稳 EMA20 并形成收缩后的结构突破", invalidation: fmt.Sprintf("跌破近期结构低点 %.2f", priorLow)})
	}

	result.Status, result.Phase, result.Label = "ready", PriceActionPhaseUnconfirmed, "阶段未确认"
	result.Confidence = 35
	result.DurationTradingDays = 1
	result.Evidence = []string{"当前价格结构尚未同时满足任一阶段的最低确认条件"}
	result.NextConfirmation = "等待价格、成交量与均线结构形成一致证据"
	result.Invalidation = "-"
	if len(candidates) == 0 {
		return result
	}
	sort.SliceStable(candidates, func(i, j int) bool { return candidates[i].score > candidates[j].score })
	best := candidates[0]
	if best.score < profile.MinimumConfidence {
		return result
	}
	if profile.ConfirmationDays > 1 {
		oneDay := profile
		oneDay.ConfirmationDays = 1
		for offset := 1; offset < profile.ConfirmationDays; offset++ {
			if len(rows)-offset < 50 {
				return result
			}
			prior := buildPriceActionCycleAnalysisWithProfile(rows[:len(rows)-offset], CandidateRelativeStrength{Status: "missing", BenchmarkTicker: "IWM"}, oneDay)
			if prior.Phase != best.phase {
				result.Evidence = []string{fmt.Sprintf("%s 尚未连续确认 %d 个交易日", best.label, profile.ConfirmationDays)}
				result.NextConfirmation = fmt.Sprintf("等待 %s 连续确认 %d 个交易日", best.label, profile.ConfirmationDays)
				return result
			}
		}
	}
	result.Phase, result.Label = best.phase, best.label
	result.Confidence = minInt(best.score, 95)
	result.Evidence, result.CounterEvidence = best.evidence, best.counter
	result.NextConfirmation, result.Invalidation = best.next, best.invalidation
	return result
}

func exponentialMovingAverage(values []float64, period int) float64 {
	if len(values) == 0 || period <= 0 {
		return 0
	}
	start := 0
	if len(values) > period*4 {
		start = len(values) - period*4
	}
	ema := values[start]
	alpha := 2.0 / float64(period+1)
	for _, value := range values[start+1:] {
		ema = alpha*value + (1-alpha)*ema
	}
	return ema
}

func averageTrueRange(rows []PriceSnapshot, period int) float64 {
	if len(rows) < 2 || period <= 0 {
		return 0
	}
	start := maxInt(1, len(rows)-period)
	total, count := 0.0, 0
	for i := start; i < len(rows); i++ {
		high, low, priorClose := priceSnapshotHigh(rows[i]), priceSnapshotLow(rows[i]), priceSnapshotClose(rows[i-1])
		value := math.Max(high-low, math.Max(math.Abs(high-priorClose), math.Abs(low-priorClose)))
		if value > 0 {
			total += value
			count++
		}
	}
	if count == 0 {
		return 0
	}
	return total / float64(count)
}

func priceRangeContractionPct(rows []PriceSnapshot) float64 {
	if len(rows) < 20 {
		return 0
	}
	recent := averageRangePct(rows[len(rows)-5:])
	prior := averageRangePct(rows[len(rows)-20 : len(rows)-5])
	if prior <= 0 {
		return 0
	}
	return (1 - recent/prior) * 100
}

func averageRangePct(rows []PriceSnapshot) float64 {
	total, count := 0.0, 0
	for _, row := range rows {
		close := priceSnapshotClose(row)
		if close > 0 && priceSnapshotHasOHLC(row) {
			total += (priceSnapshotHigh(row) - priceSnapshotLow(row)) / close * 100
			count++
		}
	}
	if count == 0 {
		return 0
	}
	return total / float64(count)
}

func highestSnapshotHigh(rows []PriceSnapshot) float64 {
	value := 0.0
	for _, row := range rows {
		if current := priceSnapshotHigh(row); current > value {
			value = current
		}
	}
	return value
}

func lowestSnapshotLow(rows []PriceSnapshot) float64 {
	value := 0.0
	for _, row := range rows {
		current := priceSnapshotLow(row)
		if current > 0 && (value == 0 || current < value) {
			value = current
		}
	}
	return value
}

// RecordPriceActionPhaseSnapshots persists one immutable result per ticker,
// rule version and trade date. Repeated daily jobs are idempotent.
func RecordPriceActionPhaseSnapshots(ctx context.Context, db *gorm.DB, tickers []string, recordedAt time.Time) (int, error) {
	if db == nil || ctx == nil {
		return 0, errors.New("database and context are required")
	}
	symbols := normalizeTickerFilter(tickers)
	if len(symbols) == 0 {
		return 0, nil
	}
	if recordedAt.IsZero() {
		recordedAt = time.Now().UTC()
	}
	securityIDs, err := tradeSetupSecurityIDs(ctx, db, symbols)
	if err != nil {
		return 0, err
	}
	created := 0
	profile := activePriceActionProfile(ctx, db)
	for _, ticker := range symbols {
		rows, err := technicalPriceHistoryForSymbol(ctx, db, ticker, "", technicalDetailHistoryDays, nil)
		if err != nil {
			return created, err
		}
		relative := CandidateRelativeStrength{Status: "missing", BenchmarkTicker: "IWM"}
		if len(rows) > 0 {
			latest := rows[len(rows)-1]
			benchmarkRows, benchmarkErr := technicalPriceHistoryForSymbol(ctx, db, "IWM", latest.Source, technicalRelativeLongDays+1, &latest.TradeDate)
			if benchmarkErr != nil {
				return created, benchmarkErr
			}
			relative = buildCandidateRelativeStrengthFromRows(rows, benchmarkRows)
		}
		analysis := buildPriceActionCycleAnalysisWithProfile(rows, relative, profile)
		if analysis.TradeDate == "" {
			continue
		}
		var existing int64
		if err := db.WithContext(ctx).Model(&PriceActionPhaseSnapshot{}).Where("ticker = ? AND rule_version = ? AND trade_date = ?", ticker, analysis.RuleVersion, analysis.TradeDate).Count(&existing).Error; err != nil {
			return created, err
		}
		if existing > 0 {
			continue
		}
		var previous PriceActionPhaseSnapshot
		previousPhase := ""
		startedAt := tradeSetupStartedAt(analysis.TradeDate, recordedAt)
		if err := db.WithContext(ctx).Where("ticker = ? AND rule_version = ?", ticker, analysis.RuleVersion).Order("trade_date DESC, id DESC").First(&previous).Error; err == nil {
			previousPhase = previous.Phase
			if previous.Phase == analysis.Phase {
				startedAt = previous.StartedAt
			}
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return created, err
		}
		evidence, counter := marshalPriceActionEvidence(analysis.Evidence, analysis.CounterEvidence)
		contextRows := completePriceActionRows(rows)
		if len(contextRows) == 0 {
			contextRows = rows
		}
		contextRow := contextRows[len(contextRows)-1]
		oscillator := buildCandidateOscillatorAnalysis(contextRows)
		snapshot := PriceActionPhaseSnapshot{SecurityID: securityIDs[ticker], Ticker: ticker, Source: contextRow.Source, RuleVersion: analysis.RuleVersion, TradeDate: analysis.TradeDate,
			Status: analysis.Status, Phase: analysis.Phase, PreviousPhase: previousPhase, Confidence: analysis.Confidence, EvidenceJSON: string(evidence), CounterEvidenceJSON: string(counter),
			NextConfirmation: analysis.NextConfirmation, Invalidation: analysis.Invalidation, CloseUSD: priceSnapshotClose(contextRow), RSI14: oscillator.RSI14,
			KDJ_K: oscillator.K, KDJ_D: oscillator.D, KDJ_J: oscillator.J, KDJMethod: oscillator.KDJMethod,
			EMA10USD: analysis.EMA10USD, EMA20USD: analysis.EMA20USD, EMA50USD: analysis.EMA50USD, ATR14USD: analysis.ATR14USD,
			VolumeRatio20: analysis.VolumeRatio20, RelativeIWM20DPct: relative.ExcessReturn20DPct, StartedAt: startedAt, RecordedAt: recordedAt.UTC()}
		if err := db.WithContext(ctx).Create(&snapshot).Error; err != nil {
			return created, err
		}
		created++
	}
	return created, nil
}

func hydratePriceActionPhaseSince(ctx context.Context, db *gorm.DB, items []CandidateScoreResult) error {
	tickers := make([]string, 0, len(items))
	for _, item := range items {
		tickers = append(tickers, item.Ticker)
	}
	tickers = normalizeTickerFilter(tickers)
	if len(tickers) == 0 {
		return nil
	}
	var rows []PriceActionPhaseSnapshot
	ruleVersion := activePriceActionProfile(ctx, db).RuleVersion
	if err := db.WithContext(ctx).Where("ticker IN ? AND rule_version = ?", tickers, ruleVersion).Order("ticker ASC, trade_date DESC, id DESC").Find(&rows).Error; err != nil {
		return err
	}
	latest := map[string]PriceActionPhaseSnapshot{}
	for _, row := range rows {
		if _, exists := latest[row.Ticker]; !exists {
			latest[row.Ticker] = row
		}
	}
	for i := range items {
		phase := &items[i].Technical.PriceAction
		if row, ok := latest[strings.ToUpper(strings.TrimSpace(items[i].Ticker))]; ok && row.Phase == phase.Phase {
			started := row.StartedAt
			phase.StartedAt = &started
			phase.DurationTradingDays = countTradingRowsSince(items[i].Ticker, row.StartedAt, phase.TradeDate, rows)
		}
	}
	return nil
}

func hydrateTickerPriceActionPhaseSince(ctx context.Context, db *gorm.DB, ticker string, technical *CandidateTechnicalAnalysis) error {
	if technical == nil || technical.PriceAction.Phase == "" {
		return nil
	}
	var row PriceActionPhaseSnapshot
	ruleVersion := activePriceActionProfile(ctx, db).RuleVersion
	err := db.WithContext(ctx).Where("ticker = ? AND rule_version = ?", strings.ToUpper(strings.TrimSpace(ticker)), ruleVersion).Order("trade_date DESC, id DESC").First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if row.Phase != technical.PriceAction.Phase {
		return nil
	}
	started := row.StartedAt
	technical.PriceAction.StartedAt = &started
	technical.PriceAction.DurationTradingDays = countTradingRowsSince(ticker, row.StartedAt, technical.PriceAction.TradeDate, nil)
	return nil
}

func countTradingRowsSince(_ string, started time.Time, tradeDate string, _ []PriceActionPhaseSnapshot) int {
	end, err := time.Parse(time.DateOnly, tradeDate)
	if err != nil {
		return 0
	}
	days := 0
	for day := started; !day.After(end); day = day.AddDate(0, 0, 1) {
		if day.Weekday() != time.Saturday && day.Weekday() != time.Sunday {
			days++
		}
	}
	return maxInt(days, 1)
}

func PriceActionEvidence(snapshot PriceActionPhaseSnapshot) []string {
	result := []string{}
	_ = json.Unmarshal([]byte(snapshot.EvidenceJSON), &result)
	if result == nil {
		return []string{}
	}
	return result
}

func marshalPriceActionEvidence(evidence, counterEvidence []string) ([]byte, []byte) {
	if evidence == nil {
		evidence = []string{}
	}
	if counterEvidence == nil {
		counterEvidence = []string{}
	}
	encodedEvidence, _ := json.Marshal(evidence)
	encodedCounterEvidence, _ := json.Marshal(counterEvidence)
	return encodedEvidence, encodedCounterEvidence
}

func priceActionCounterEvidence(snapshot PriceActionPhaseSnapshot) []string {
	result := []string{}
	_ = json.Unmarshal([]byte(snapshot.CounterEvidenceJSON), &result)
	if result == nil {
		return []string{}
	}
	return result
}

// ListPriceActionPhaseTransitions returns confirmed changes for the supplied
// current research scope. Baseline rows are intentionally excluded.
func ListPriceActionPhaseTransitions(ctx context.Context, db *gorm.DB, tickers []string, limit int) ([]PriceActionPhaseSnapshot, error) {
	if db == nil || ctx == nil {
		return nil, errors.New("database and context are required")
	}
	symbols := normalizeTickerFilter(tickers)
	if len(symbols) == 0 {
		return []PriceActionPhaseSnapshot{}, nil
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	result := []PriceActionPhaseSnapshot{}
	ruleVersion := activePriceActionProfile(ctx, db).RuleVersion
	if err := db.WithContext(ctx).Where("ticker IN ? AND rule_version = ? AND previous_phase <> '' AND previous_phase <> phase", symbols, ruleVersion).Order("trade_date DESC, id DESC").Limit(limit).Find(&result).Error; err != nil {
		return nil, err
	}
	for i := range result {
		result[i].Evidence = PriceActionEvidence(result[i])
		result[i].CounterEvidence = priceActionCounterEvidence(result[i])
	}
	return result, nil
}

// GetPriceActionTimeline returns immutable daily conclusions for one current
// research ticker. Results are chronological even though the bounded query
// selects the newest rows first. The API layer owns scope authorization.
func GetPriceActionTimeline(ctx context.Context, db *gorm.DB, ticker, ruleVersion, fromDate, toDate string, changesOnly bool, limit int) (PriceActionTimeline, error) {
	result := PriceActionTimeline{Ticker: strings.ToUpper(strings.TrimSpace(ticker)), ChangesOnly: changesOnly, Items: []PriceActionPhaseSnapshot{}, AvailableRuleVersions: []string{}}
	if db == nil || ctx == nil || result.Ticker == "" {
		return result, errors.New("database, context and ticker are required")
	}
	if limit <= 0 {
		limit = 260
	}
	if limit > 1000 {
		limit = 1000
	}
	for _, value := range []string{fromDate, toDate} {
		if value != "" {
			if _, err := time.Parse(time.DateOnly, value); err != nil {
				return result, fmt.Errorf("invalid timeline date %q", value)
			}
		}
	}
	if fromDate != "" && toDate != "" && fromDate > toDate {
		return result, errors.New("timeline from date must not be after to date")
	}
	var versions []string
	if err := db.WithContext(ctx).Model(&PriceActionPhaseSnapshot{}).Where("ticker = ?", result.Ticker).Distinct("rule_version").Order("rule_version DESC").Pluck("rule_version", &versions).Error; err != nil {
		return result, err
	}
	result.AvailableRuleVersions = versions
	if strings.TrimSpace(ruleVersion) == "" {
		ruleVersion = activePriceActionProfile(ctx, db).RuleVersion
	}
	result.RuleVersion = strings.TrimSpace(ruleVersion)
	query := db.WithContext(ctx).Model(&PriceActionPhaseSnapshot{}).Where("ticker = ? AND rule_version = ?", result.Ticker, result.RuleVersion)
	if fromDate != "" {
		query = query.Where("trade_date >= ?", fromDate)
	}
	if toDate != "" {
		query = query.Where("trade_date <= ?", toDate)
	}
	if changesOnly {
		query = query.Where("previous_phase = '' OR previous_phase <> phase")
	}
	if err := query.Count(&result.Total).Error; err != nil {
		return result, err
	}
	if err := query.Order("trade_date DESC, id DESC").Limit(limit).Find(&result.Items).Error; err != nil {
		return result, err
	}
	for left, right := 0, len(result.Items)-1; left < right; left, right = left+1, right-1 {
		result.Items[left], result.Items[right] = result.Items[right], result.Items[left]
	}
	for index := range result.Items {
		result.Items[index].Evidence = PriceActionEvidence(result.Items[index])
		result.Items[index].CounterEvidence = priceActionCounterEvidence(result.Items[index])
	}
	return result, nil
}
