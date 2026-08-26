package discovery

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"
)

const TradePlanSimulationRuleVersion = "daily_ohlc_v2"

const (
	TradePlanSimulationOpen      = "open"
	TradePlanSimulationStopLoss  = "stop_loss"
	TradePlanSimulationTarget    = "take_profit"
	TradePlanSimulationTrendExit = "trend_exit"
)

// TradePlanSimulationReport intentionally describes a daily-close paper
// simulation. It must not be interpreted as intraday execution or brokerage
// performance because this rule version deliberately evaluates daily closes
// even when OHLC bars are available; it does not model intraday execution.
type TradePlanSimulationReport struct {
	GeneratedAt         time.Time             `json:"generated_at"`
	RuleVersion         string                `json:"rule_version"`
	ExecutionConvention string                `json:"execution_convention"`
	TotalCount          int                   `json:"total_count"`
	ClosedCount         int                   `json:"closed_count"`
	OpenCount           int                   `json:"open_count"`
	WinRatePct          *float64              `json:"win_rate_pct"`
	AverageReturnPct    *float64              `json:"average_return_pct"`
	AverageRMultiple    *float64              `json:"average_r_multiple"`
	MaxDrawdownPct      *float64              `json:"max_drawdown_pct"`
	StatusCounts        map[string]int        `json:"status_counts"`
	Items               []TradePlanSimulation `json:"items"`
}

type TradePlanSimulationRebuildResult struct {
	TradePlanSimulationReport
	CreatedCount int `json:"created_count"`
	UpdatedCount int `json:"updated_count"`
	SkippedCount int `json:"skipped_count"`
}

func RebuildTradePlanSimulations(ctx context.Context, db *gorm.DB, tickers []string) (TradePlanSimulationRebuildResult, error) {
	result := TradePlanSimulationRebuildResult{TradePlanSimulationReport: newTradePlanSimulationReport()}
	if db == nil {
		return result, errors.New("database is required")
	}
	if ctx == nil {
		return result, errors.New("context is required")
	}
	for _, ticker := range uniqueTradePlanTickers(tickers) {
		rows, err := tradePlanSimulationPriceHistory(ctx, db, ticker)
		if err != nil {
			return result, err
		}
		if len(rows) <= technicalMA200LookbackDays {
			result.SkippedCount++
			continue
		}
		for _, simulation := range buildTradePlanSimulations(rows) {
			var existing TradePlanSimulation
			err := db.WithContext(ctx).Where("ticker = ? AND rule_version = ? AND signal_date = ?", simulation.Ticker, simulation.RuleVersion, simulation.SignalDate).First(&existing).Error
			if errors.Is(err, gorm.ErrRecordNotFound) {
				if err := db.WithContext(ctx).Create(&simulation).Error; err != nil {
					return result, err
				}
				result.CreatedCount++
				continue
			}
			if err != nil {
				return result, err
			}
			// The entry snapshot is immutable. Rebuilds only refresh the observed
			// post-entry lifecycle as later daily closes become available.
			if err := db.WithContext(ctx).Model(&existing).Updates(map[string]any{
				"status": simulation.Status, "exit_date": simulation.ExitDate, "exit_price_usd": simulation.ExitPriceUSD,
				"exit_reason": simulation.ExitReason, "last_mark_price_usd": simulation.LastMarkPriceUSD,
				"gross_return_pct": simulation.GrossReturnPct, "execution_cost_pct": simulation.ExecutionCostPct,
				"return_pct": simulation.ReturnPct, "r_multiple": simulation.RMultiple,
				"max_drawdown_pct": simulation.MaxDrawdownPct, "holding_days": simulation.HoldingDays,
			}).Error; err != nil {
				return result, err
			}
			result.UpdatedCount++
		}
	}
	report, err := ListTradePlanSimulations(ctx, db, tickers)
	if err != nil {
		return result, err
	}
	result.TradePlanSimulationReport = report
	return result, nil
}

func ListTradePlanSimulations(ctx context.Context, db *gorm.DB, tickers []string) (TradePlanSimulationReport, error) {
	report := newTradePlanSimulationReport()
	if db == nil {
		return report, errors.New("database is required")
	}
	if ctx == nil {
		return report, errors.New("context is required")
	}
	query := db.WithContext(ctx).Where("rule_version = ?", TradePlanSimulationRuleVersion)
	if normalized := uniqueTradePlanTickers(tickers); len(normalized) > 0 {
		query = query.Where("ticker IN ?", normalized)
	} else {
		return report, nil
	}
	if err := query.Order("signal_date DESC").Order("ticker ASC").Find(&report.Items).Error; err != nil {
		return report, err
	}
	report.TotalCount = len(report.Items)
	returns := make([]float64, 0, len(report.Items))
	rMultiples := make([]float64, 0, len(report.Items))
	maxDrawdown := 0.0
	winners := 0
	for _, item := range report.Items {
		report.StatusCounts[item.Status]++
		if item.MaxDrawdownPct < maxDrawdown {
			maxDrawdown = item.MaxDrawdownPct
		}
		if item.Status == TradePlanSimulationOpen {
			report.OpenCount++
			continue
		}
		report.ClosedCount++
		returns = append(returns, item.ReturnPct)
		rMultiples = append(rMultiples, item.RMultiple)
		if item.ReturnPct > 0 {
			winners++
		}
	}
	if len(returns) > 0 {
		winRate := 100 * float64(winners) / float64(len(returns))
		averageReturn := averageFloat64(returns)
		averageR := averageFloat64(rMultiples)
		report.WinRatePct = &winRate
		report.AverageReturnPct = &averageReturn
		report.AverageRMultiple = &averageR
	}
	if report.TotalCount > 0 {
		report.MaxDrawdownPct = &maxDrawdown
	}
	return report, nil
}

func newTradePlanSimulationReport() TradePlanSimulationReport {
	return TradePlanSimulationReport{
		GeneratedAt: time.Now().UTC(), RuleVersion: TradePlanSimulationRuleVersion,
		ExecutionConvention: "信号日收盘确认，下一交易日开盘模拟入场；用日线 OHLC 判断止损和目标，同日双触发按止损优先，并按流动性计入往返成本。",
		StatusCounts:        map[string]int{}, Items: []TradePlanSimulation{},
	}
}

func uniqueTradePlanTickers(tickers []string) []string {
	seen := map[string]struct{}{}
	result := make([]string, 0, len(tickers))
	for _, ticker := range tickers {
		ticker = strings.ToUpper(strings.TrimSpace(ticker))
		if ticker == "" {
			continue
		}
		if _, found := seen[ticker]; found {
			continue
		}
		seen[ticker] = struct{}{}
		result = append(result, ticker)
	}
	sort.Strings(result)
	return result
}

func tradePlanSimulationPriceHistory(ctx context.Context, db *gorm.DB, ticker string) ([]PriceSnapshot, error) {
	var raw []PriceSnapshot
	if err := db.WithContext(ctx).Where("symbol = ? AND quality_status = ?", ticker, QualityStatusValid).Order("trade_date DESC, created_at DESC, id DESC").Find(&raw).Error; err != nil {
		return nil, err
	}
	return technicalPriceHistoryFromRaw(raw, "", len(raw), nil), nil
}

func buildTradePlanSimulations(rows []PriceSnapshot) []TradePlanSimulation {
	if len(rows) <= technicalMA200LookbackDays {
		return []TradePlanSimulation{}
	}
	result := make([]TradePlanSimulation, 0)
	previousStatus := ""
	for index := technicalMA200LookbackDays - 1; index < len(rows)-1; index++ {
		analysis := buildCandidateTechnicalAnalysis(rows[:index+1])
		setup := analysis.TradeSetup
		if setup.Status != TradeSetupEntryCandidate || previousStatus == TradeSetupEntryCandidate {
			previousStatus = setup.Status
			continue
		}
		simulation, terminalIndex := simulateTradePlanLifecycle(rows, index, setup)
		result = append(result, simulation)
		previousStatus = ""
		if terminalIndex > index {
			index = terminalIndex
		}
	}
	return result
}

func simulateTradePlanLifecycle(rows []PriceSnapshot, signalIndex int, setup CandidateTradeSetup) (TradePlanSimulation, int) {
	entryIndex := signalIndex + 1
	entryPrice, entryPriceSource := tradePlanEntryPrice(rows[entryIndex])
	signalDate := rows[signalIndex].TradeDate
	entryDate := rows[entryIndex].TradeDate
	simulation := TradePlanSimulation{
		Ticker: rows[signalIndex].Symbol, RuleVersion: TradePlanSimulationRuleVersion, SignalDate: signalDate,
		EntryDate: &entryDate, EntryTrigger: setup.EntryTrigger, EntryPriceSource: entryPriceSource, EntryPriceUSD: entryPrice,
		StopLossUSD: setup.StopLossUSD, TakeProfitUSD: setup.TakeProfitZoneLowUSD, InitialRiskPct: setup.RiskPct,
		Status: TradePlanSimulationOpen, LastMarkPriceUSD: entryPrice, ExecutionCostPct: tradePlanExecutionCostPct(rows[entryIndex], entryPrice),
	}
	if entryPrice > 0 && simulation.StopLossUSD > 0 && simulation.StopLossUSD < entryPrice {
		simulation.InitialRiskPct = (entryPrice - simulation.StopLossUSD) * 100 / entryPrice
	}
	peak := entryPrice
	for index := entryIndex; index < len(rows); index++ {
		open, high, low, close := tradePlanBarPrices(rows[index])
		if high > peak {
			peak = high
		}
		if peak > 0 {
			drawdown := (low/peak - 1) * 100
			if drawdown < simulation.MaxDrawdownPct {
				simulation.MaxDrawdownPct = drawdown
			}
		}
		// The opening print happens before the day's range. A gap through a
		// threshold therefore fills at the open rather than the planned level.
		if simulation.StopLossUSD > 0 && open <= simulation.StopLossUSD {
			return closeTradePlanSimulation(simulation, rows[index].TradeDate, open, TradePlanSimulationStopLoss, "开盘跳空跌破计划止损", index-entryIndex), index
		}
		if simulation.TakeProfitUSD > 0 && open >= simulation.TakeProfitUSD {
			return closeTradePlanSimulation(simulation, rows[index].TradeDate, open, TradePlanSimulationTarget, "开盘跳空达到第一目标区间", index-entryIndex), index
		}
		stopHit := simulation.StopLossUSD > 0 && low <= simulation.StopLossUSD
		targetHit := simulation.TakeProfitUSD > 0 && high >= simulation.TakeProfitUSD
		if stopHit {
			reason := "日内触发计划止损"
			if targetHit {
				reason = "同一日止损与目标均触发，按保守规则优先计入止损"
			}
			return closeTradePlanSimulation(simulation, rows[index].TradeDate, simulation.StopLossUSD, TradePlanSimulationStopLoss, reason, index-entryIndex), index
		}
		if targetHit {
			return closeTradePlanSimulation(simulation, rows[index].TradeDate, simulation.TakeProfitUSD, TradePlanSimulationTarget, "日内达到第一目标区间", index-entryIndex), index
		}
		status := buildCandidateTechnicalAnalysis(rows[:index+1]).TradeSetup
		if status.Status == TradeSetupExitWarning || status.Status == TradeSetupInvalidated {
			return closeTradePlanSimulation(simulation, rows[index].TradeDate, close, TradePlanSimulationTrendExit, status.ExitReason, index-entryIndex), index
		}
	}
	lastIndex := len(rows) - 1
	simulation.LastMarkPriceUSD = priceSnapshotClose(rows[lastIndex])
	applyTradePlanSimulationReturns(&simulation, simulation.LastMarkPriceUSD)
	simulation.HoldingDays = lastIndex - entryIndex
	return simulation, lastIndex
}

func closeTradePlanSimulation(simulation TradePlanSimulation, exitDate time.Time, exitPrice float64, status, reason string, holdingDays int) TradePlanSimulation {
	simulation.Status = status
	simulation.ExitDate = &exitDate
	simulation.ExitPriceUSD = exitPrice
	simulation.LastMarkPriceUSD = exitPrice
	simulation.ExitReason = reason
	applyTradePlanSimulationReturns(&simulation, exitPrice)
	simulation.HoldingDays = holdingDays
	return simulation
}

func applyTradePlanSimulationReturns(simulation *TradePlanSimulation, mark float64) {
	if simulation == nil {
		return
	}
	simulation.GrossReturnPct = tradePlanSimulationReturnPct(simulation.EntryPriceUSD, mark)
	simulation.ReturnPct = simulation.GrossReturnPct - simulation.ExecutionCostPct
	if simulation.EntryPriceUSD <= 0 || simulation.InitialRiskPct <= 0 {
		simulation.RMultiple = 0
		return
	}
	simulation.RMultiple = simulation.ReturnPct / simulation.InitialRiskPct
}

func tradePlanEntryPrice(row PriceSnapshot) (float64, string) {
	if row.OpenMicros > 0 {
		return float64(row.OpenMicros) / 1_000_000, "next_open"
	}
	return priceSnapshotClose(row), "next_close_fallback"
}

func tradePlanBarPrices(row PriceSnapshot) (float64, float64, float64, float64) {
	close := priceSnapshotClose(row)
	open := close
	high := close
	low := close
	if row.OpenMicros > 0 {
		open = float64(row.OpenMicros) / 1_000_000
	}
	if row.HighMicros > 0 {
		high = float64(row.HighMicros) / 1_000_000
	}
	if row.LowMicros > 0 {
		low = float64(row.LowMicros) / 1_000_000
	}
	if high < open {
		high = open
	}
	if high < close {
		high = close
	}
	if low > open || low <= 0 {
		low = open
	}
	if low > close {
		low = close
	}
	return open, high, low, close
}

// tradePlanExecutionCostPct is an explicitly conservative round-trip model.
// It uses only point-in-time entry-day dollar volume and therefore remains
// reproducible without pretending to know an intraday order book.
func tradePlanExecutionCostPct(row PriceSnapshot, price float64) float64 {
	dollarVolume := price * float64(row.Volume)
	switch {
	case dollarVolume >= 5_000_000:
		return 0.35
	case dollarVolume >= 1_000_000:
		return 0.60
	case dollarVolume >= 250_000:
		return 1.00
	default:
		return 1.50
	}
}

func tradePlanSimulationReturnPct(entry, mark float64) float64 {
	if entry <= 0 || mark <= 0 {
		return 0
	}
	return (mark/entry - 1) * 100
}
