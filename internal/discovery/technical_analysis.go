package discovery

import (
	"context"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"
)

const (
	technicalLookbackDays      = 20
	technicalMinimumSamples    = technicalLookbackDays + 1
	technicalLongLookbackDays  = 50
	technicalMA200LookbackDays = 200
	// Technical history is retained beyond the minimum needed by the MA20
	// signals so that the detail chart can also present MA50/MA200.  MA200
	// needs its current day plus 199 earlier trading days.
	// Daily automatic warm-up only maintains the existing MA50 baseline. The
	// longer MA200 history is deliberately requested by the explicit manual
	// backfill, so a normal daily sync does not multiply API usage.
	technicalHistorySamplesRequired = technicalLongLookbackDays
	technicalDetailHistoryDays      = 260
	technicalRelativeLongDays       = 60
	technicalVolumeMultiple         = 1.5
	technicalAnchoredVWAPMinSamples = 3
)

const (
	TechnicalStatusReady                = "ready"
	TechnicalStatusDataInsufficient     = "data_insufficient"
	TechnicalStatusMissing              = "missing"
	TechnicalSignalCrossAboveMA20       = "cross_above_ma20"
	TechnicalSignalBreakout20DayHigh    = "breakout_20d_high"
	TechnicalSignalVolumeBackedBreakout = "volume_backed_breakout"
)

type CandidateTechnicalSignal struct {
	Kind      string `json:"kind"`
	Label     string `json:"label"`
	Direction string `json:"direction"`
}

// CandidateTechnicalAnalysis contains price-derived research signals only.
// It is intentionally excluded from the fundamental candidate score.
type CandidateTechnicalAnalysis struct {
	Status                  string                     `json:"status"`
	SampleDays              int                        `json:"sample_days"`
	RequiredSampleDays      int                        `json:"required_sample_days"`
	TradeDate               string                     `json:"trade_date"`
	CloseUSD                float64                    `json:"close_usd"`
	MA20USD                 float64                    `json:"ma20_usd"`
	MA50USD                 float64                    `json:"ma50_usd"`
	MA200USD                float64                    `json:"ma200_usd"`
	MA200Available          bool                       `json:"ma200_available"`
	PriorCloseUSD           float64                    `json:"prior_close_usd"`
	PriorMA20USD            float64                    `json:"prior_ma20_usd"`
	DistanceToMA20Pct       float64                    `json:"distance_to_ma20_pct"`
	Prior20DayHighUSD       float64                    `json:"prior_20d_high_usd"`
	DistanceTo20DayHighPct  float64                    `json:"distance_to_20d_high_pct"`
	AverageVolume20         float64                    `json:"average_volume_20"`
	VolumeRatio20           float64                    `json:"volume_ratio_20"`
	DollarVolumeUSD         float64                    `json:"dollar_volume_usd"`
	AverageDollarVolume20   float64                    `json:"average_dollar_volume_20"`
	DollarVolumeRatio20     float64                    `json:"dollar_volume_ratio_20"`
	LiquidityStatus         string                     `json:"liquidity_status"`
	High50DayUSD            float64                    `json:"high_50d_usd"`
	DistanceTo50DayHighPct  float64                    `json:"distance_to_50d_high_pct"`
	High200DayUSD           float64                    `json:"high_200d_usd"`
	DistanceTo200DayHighPct float64                    `json:"distance_to_200d_high_pct"`
	RelativeStrength        CandidateRelativeStrength  `json:"relative_strength"`
	AnchoredVWAP            CandidateAnchoredVWAP      `json:"anchored_vwap"`
	Signals                 []CandidateTechnicalSignal `json:"signals"`
	TradeSetup              CandidateTradeSetup        `json:"trade_setup"`
}

// CandidateAnchoredVWAP is a daily-close, volume-weighted approximation from
// the most recent auditable candidate research event. It is deliberately not
// called intraday VWAP: free EOD sources do not expose the intraday path.
type CandidateAnchoredVWAP struct {
	Status             string  `json:"status"`
	AnchorEventType    string  `json:"anchor_event_type"`
	AnchorLabel        string  `json:"anchor_label"`
	AnchorTradeDate    string  `json:"anchor_trade_date"`
	PriceTradeDate     string  `json:"price_trade_date"`
	TradingDays        int     `json:"trading_days"`
	ApproximateVWAPUSD float64 `json:"approximate_vwap_usd"`
	DistancePct        float64 `json:"distance_pct"`
	PriceSource        string  `json:"price_source"`
}

// CandidateRelativeStrength compares the candidate with IWM on matched local
// trading days. It is research context only, not a ranking or score input.
type CandidateRelativeStrength struct {
	Status                string   `json:"status"`
	BenchmarkTicker       string   `json:"benchmark_ticker"`
	MatchedSampleDays     int      `json:"matched_sample_days"`
	CandidateReturn20DPct *float64 `json:"candidate_return_20d_pct,omitempty"`
	BenchmarkReturn20DPct *float64 `json:"benchmark_return_20d_pct,omitempty"`
	ExcessReturn20DPct    *float64 `json:"excess_return_20d_pct,omitempty"`
	CandidateReturn60DPct *float64 `json:"candidate_return_60d_pct,omitempty"`
	BenchmarkReturn60DPct *float64 `json:"benchmark_return_60d_pct,omitempty"`
	ExcessReturn60DPct    *float64 `json:"excess_return_60d_pct,omitempty"`
}

type candidateBenchmarkMatchedPrice struct {
	candidate float64
	benchmark float64
}

// CandidateTechnicalHistoryRow is one local daily price record used for
// technical research. Backfilled distinguishes a one-time history fetch from
// the regular daily market sync.
type CandidateTechnicalHistoryRow struct {
	TradeDate       string  `json:"trade_date"`
	CloseUSD        float64 `json:"close_usd"`
	Volume          int64   `json:"volume"`
	DollarVolumeUSD float64 `json:"dollar_volume_usd"`
	Source          string  `json:"source"`
	SourceVersion   string  `json:"source_version"`
	Backfilled      bool    `json:"backfilled"`
}

func hydrateCandidateTechnicalAnalysis(ctx context.Context, db *gorm.DB, items []CandidateScoreResult) error {
	priceHistories, err := candidateTechnicalPriceHistories(ctx, db, items, technicalMA200LookbackDays)
	if err != nil {
		return err
	}
	return hydrateCandidateTechnicalAnalysisWithPriceHistories(ctx, db, items, priceHistories)
}

func hydrateCandidateTechnicalAnalysisWithPriceHistories(ctx context.Context, db *gorm.DB, items []CandidateScoreResult, priceHistories map[uint][]PriceSnapshot) error {
	securityIDs := make([]uint, 0, len(items))
	for _, item := range items {
		securityIDs = append(securityIDs, item.SecurityID)
	}
	var events []CandidateSignalEvent
	if len(securityIDs) > 0 {
		if err := db.WithContext(ctx).Where("security_id IN ?", securityIDs).Order("signal_date DESC, id DESC").Find(&events).Error; err != nil {
			return err
		}
	}
	eventsBySecurity := make(map[uint][]CandidateSignalEvent, len(securityIDs))
	for _, event := range events {
		eventsBySecurity[event.SecurityID] = append(eventsBySecurity[event.SecurityID], event)
	}
	benchmarkCache := map[string][]PriceSnapshot{}
	for i := range items {
		rows := priceHistories[items[i].SecurityID]
		items[i].Technical = buildCandidateTechnicalAnalysis(rows)
		cacheKey := strings.TrimSpace(items[i].PriceSource) + "|"
		if items[i].PriceTradeDate != nil {
			cacheKey += items[i].PriceTradeDate.Format(time.DateOnly)
		}
		benchmarkRows, found := benchmarkCache[cacheKey]
		if !found {
			benchmarkRows, err := technicalPriceHistoryForSymbol(ctx, db, "IWM", items[i].PriceSource, technicalRelativeLongDays+1, items[i].PriceTradeDate)
			if err != nil {
				return err
			}
			benchmarkCache[cacheKey] = benchmarkRows
		}
		items[i].Technical.RelativeStrength = buildCandidateRelativeStrengthFromRows(rows, benchmarkRows)
		items[i].Technical.AnchoredVWAP = buildCandidateAnchoredVWAP(rows, eventsBySecurity[items[i].SecurityID], items[i].PriceTradeDate, items[i].PriceSource)
		items[i].Technical.TradeSetup = buildCandidateTradeSetup(items[i].Technical)
	}
	return hydrateTradeSetupStatusSince(ctx, db, items)
}

// candidateTechnicalPriceHistories reads the common price-history window for
// the whole candidate page in one query. The list used to issue one historical
// price query per candidate, which is needlessly slow against a large local
// SQLite price store. If the selected quote is a one-day local cache record,
// it falls back to the bounded multi-provider window once for the whole page.
func candidateTechnicalPriceHistories(ctx context.Context, db *gorm.DB, items []CandidateScoreResult, limit int) (map[uint][]PriceSnapshot, error) {
	result := make(map[uint][]PriceSnapshot, len(items))
	if len(items) == 0 {
		return result, nil
	}
	if limit <= 0 {
		limit = technicalMinimumSamples
	}
	windowDays := candidateTechnicalHistoryWindowDays(limit)

	symbols := make([]string, 0, len(items))
	sources := make([]string, 0, len(items))
	seenSymbols := make(map[string]struct{}, len(items))
	seenSources := make(map[string]struct{}, len(items))
	earliestCutoff := time.Time{}
	latestCutoff := time.Time{}
	for _, item := range items {
		symbol := strings.ToUpper(strings.TrimSpace(item.Ticker))
		source := strings.TrimSpace(item.PriceSource)
		if symbol == "" || source == "" {
			continue
		}
		if _, seen := seenSymbols[symbol]; !seen {
			symbols = append(symbols, symbol)
			seenSymbols[symbol] = struct{}{}
		}
		if _, seen := seenSources[source]; !seen {
			sources = append(sources, source)
			seenSources[source] = struct{}{}
		}
		cutoff := time.Now().UTC()
		if item.PriceTradeDate != nil && !item.PriceTradeDate.IsZero() {
			cutoff = *item.PriceTradeDate
		}
		if earliestCutoff.IsZero() || cutoff.Before(earliestCutoff) {
			earliestCutoff = cutoff
		}
		if latestCutoff.IsZero() || cutoff.After(latestCutoff) {
			latestCutoff = cutoff
		}
	}
	if earliestCutoff.IsZero() {
		earliestCutoff = time.Now().UTC()
		latestCutoff = earliestCutoff
	}
	if len(symbols) > 0 && len(sources) > 0 {
		windowStart := earliestCutoff.AddDate(0, 0, -windowDays)
		var raw []PriceSnapshot
		if err := db.WithContext(ctx).
			Where("symbol IN ? AND source IN ? AND quality_status = ?", symbols, sources, QualityStatusValid).
			Where("trade_date >= ? AND trade_date <= ?", windowStart, latestCutoff).
			Order("trade_date DESC, created_at DESC, id DESC").
			Find(&raw).Error; err != nil {
			return nil, err
		}
		bySymbolSource := make(map[string][]PriceSnapshot, len(symbols))
		for _, row := range raw {
			key := strings.ToUpper(strings.TrimSpace(row.Symbol)) + "\x00" + strings.TrimSpace(row.Source)
			bySymbolSource[key] = append(bySymbolSource[key], row)
		}
		for _, item := range items {
			symbol := strings.ToUpper(strings.TrimSpace(item.Ticker))
			source := strings.TrimSpace(item.PriceSource)
			if symbol == "" || source == "" {
				continue
			}
			rows := technicalPriceHistoryFromRaw(bySymbolSource[symbol+"\x00"+source], source, limit, item.PriceTradeDate)
			if len(rows) >= limit {
				result[item.SecurityID] = rows
			}
		}
	}
	// A research batch can use a one-day local-cache quote when a provider is
	// temporarily unavailable. That quote is correct for the list's latest
	// price, but cannot by itself supply MA/technical history. Read all local
	// provider rows for the unresolved symbols in one bounded query, then let
	// the existing source-preference selector choose one record per date.
	missingSymbols := make([]string, 0, len(items))
	missingSeen := make(map[string]struct{}, len(items))
	for _, item := range items {
		if len(result[item.SecurityID]) >= technicalMinimumSamples {
			continue
		}
		symbol := strings.ToUpper(strings.TrimSpace(item.Ticker))
		if symbol == "" {
			continue
		}
		if _, seen := missingSeen[symbol]; !seen {
			missingSymbols = append(missingSymbols, symbol)
			missingSeen[symbol] = struct{}{}
		}
	}
	if len(missingSymbols) > 0 {
		windowStart := earliestCutoff.AddDate(0, 0, -windowDays)
		var fallbackRaw []PriceSnapshot
		if err := db.WithContext(ctx).
			Where("symbol IN ? AND quality_status = ?", missingSymbols, QualityStatusValid).
			Where("trade_date >= ? AND trade_date <= ?", windowStart, latestCutoff).
			Order("trade_date DESC, created_at DESC, id DESC").
			Find(&fallbackRaw).Error; err != nil {
			return nil, err
		}
		bySymbol := make(map[string][]PriceSnapshot, len(missingSymbols))
		for _, row := range fallbackRaw {
			symbol := strings.ToUpper(strings.TrimSpace(row.Symbol))
			bySymbol[symbol] = append(bySymbol[symbol], row)
		}
		for _, item := range items {
			if len(result[item.SecurityID]) >= technicalMinimumSamples {
				continue
			}
			symbol := strings.ToUpper(strings.TrimSpace(item.Ticker))
			rows := technicalPriceHistoryFromRaw(bySymbol[symbol], item.PriceSource, limit, item.PriceTradeDate)
			// Keep an empty/short result as a resolved data-insufficient state.
			// The query already covered the full bounded MA200 window, so a
			// second identical per-symbol lookup only adds latency and cannot
			// produce a usable technical signal.
			result[item.SecurityID] = rows
		}
	}
	// A short research-readiness window is enough for most symbols. For an
	// infrequently traded symbol, retry only that small unresolved set with the
	// full MA200 range so its investability result remains identical to the
	// previous all-long-window behavior.
	if windowDays < 450 {
		unresolved := make([]string, 0, len(items))
		unresolvedSeen := make(map[string]struct{}, len(items))
		for _, item := range items {
			if len(result[item.SecurityID]) >= limit {
				continue
			}
			symbol := strings.ToUpper(strings.TrimSpace(item.Ticker))
			if symbol != "" {
				if _, seen := unresolvedSeen[symbol]; !seen {
					unresolved = append(unresolved, symbol)
					unresolvedSeen[symbol] = struct{}{}
				}
			}
		}
		if len(unresolved) > 0 {
			var longWindowRows []PriceSnapshot
			if err := db.WithContext(ctx).
				Where("symbol IN ? AND quality_status = ?", unresolved, QualityStatusValid).
				Where("trade_date >= ? AND trade_date <= ?", earliestCutoff.AddDate(0, 0, -450), latestCutoff).
				Order("trade_date DESC, created_at DESC, id DESC").
				Find(&longWindowRows).Error; err != nil {
				return nil, err
			}
			bySymbol := make(map[string][]PriceSnapshot, len(unresolved))
			for _, row := range longWindowRows {
				symbol := strings.ToUpper(strings.TrimSpace(row.Symbol))
				bySymbol[symbol] = append(bySymbol[symbol], row)
			}
			for _, item := range items {
				if len(result[item.SecurityID]) >= limit {
					continue
				}
				symbol := strings.ToUpper(strings.TrimSpace(item.Ticker))
				result[item.SecurityID] = technicalPriceHistoryFromRaw(bySymbol[symbol], item.PriceSource, limit, item.PriceTradeDate)
			}
		}
	}
	return result, nil
}

func candidateTechnicalHistoryWindowDays(limit int) int {
	if limit >= technicalMA200LookbackDays {
		// 450 calendar days safely covers the 200 completed trading sessions
		// required by MA200 while preventing a scan of all historical prices.
		return 450
	}
	// A 90-day window is generous for the 21-session liquidity calculation,
	// including normal exchange holidays. Sparse symbols are retried above.
	return 90
}

func candidateTechnicalPriceHistory(ctx context.Context, db *gorm.DB, item CandidateScoreResult) ([]PriceSnapshot, error) {
	// Technical signals shown in a published candidate batch must not use a
	// later manual history backfill. The batch's selected price is the common
	// as-of point for the list price, market quality, and technical signals.
	// Automatic maintenance only guarantees the MA50 baseline, but an explicit
	// history backfill can provide the 200 samples required for MA200. Read that
	// larger window when it is available; this is a local SQLite read and does
	// not add any provider API requests to normal daily synchronization.
	return candidateTechnicalPriceHistoryLimitAtOrBefore(ctx, db, item, technicalMA200LookbackDays, item.PriceTradeDate)
}

func candidateTechnicalPriceHistoryLimit(ctx context.Context, db *gorm.DB, item CandidateScoreResult, limit int) ([]PriceSnapshot, error) {
	return candidateTechnicalPriceHistoryLimitAtOrBefore(ctx, db, item, limit, nil)
}

func candidateTechnicalPriceHistoryLimitAtOrBefore(ctx context.Context, db *gorm.DB, item CandidateScoreResult, limit int, cutoff *time.Time) ([]PriceSnapshot, error) {
	return technicalPriceHistoryForSymbol(ctx, db, item.Ticker, item.PriceSource, limit, cutoff)
}

func technicalPriceHistoryForSymbol(ctx context.Context, db *gorm.DB, symbol, preferredSource string, limit int, cutoff *time.Time) ([]PriceSnapshot, error) {
	if limit <= 0 {
		limit = technicalMinimumSamples
	}
	buildQuery := func() *gorm.DB {
		query := db.WithContext(ctx).Where("symbol = ? AND quality_status = ?", strings.ToUpper(strings.TrimSpace(symbol)), QualityStatusValid)
		if cutoff != nil && !cutoff.IsZero() {
			query = query.Where("trade_date <= ?", *cutoff)
		}
		return query
	}
	preferredSource = strings.TrimSpace(preferredSource)
	// The selected candidate quote already identifies the preferred provider.
	// Read its bounded history first: this is normally complete after the local
	// technical backfill and avoids loading thousands of duplicate snapshots
	// from fallback providers for every list row.
	if preferredSource != "" {
		var preferredRows []PriceSnapshot
		if err := buildQuery().Where("source = ?", preferredSource).Order("trade_date DESC, created_at DESC, id DESC").Limit(limit).Find(&preferredRows).Error; err != nil {
			return nil, err
		}
		selected := technicalPriceHistoryFromRaw(preferredRows, preferredSource, limit, cutoff)
		if len(selected) >= limit {
			return selected, nil
		}
	}
	var raw []PriceSnapshot
	// Use a fresh base query here. GORM chain state can retain the source
	// predicate from the preferred-source read; that would make a one-day
	// local-cache record hide a complete historical series from the detail
	// chart and technical fallback.
	if err := buildQuery().Order("trade_date DESC, created_at DESC, id DESC").Limit(limit * 12).Find(&raw).Error; err != nil {
		return nil, err
	}
	return technicalPriceHistoryFromRaw(raw, preferredSource, limit, cutoff), nil
}

func technicalPriceHistoryFromRaw(raw []PriceSnapshot, preferredSource string, limit int, cutoff *time.Time) []PriceSnapshot {
	if limit <= 0 {
		limit = technicalMinimumSamples
	}
	ordered := append([]PriceSnapshot(nil), raw...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if !ordered[i].TradeDate.Equal(ordered[j].TradeDate) {
			return ordered[i].TradeDate.After(ordered[j].TradeDate)
		}
		if !ordered[i].CreatedAt.Equal(ordered[j].CreatedAt) {
			return ordered[i].CreatedAt.After(ordered[j].CreatedAt)
		}
		return ordered[i].ID > ordered[j].ID
	})
	preferredSource = strings.TrimSpace(preferredSource)
	byDate := map[string]PriceSnapshot{}
	for _, row := range ordered {
		if cutoff != nil && !cutoff.IsZero() && row.TradeDate.After(*cutoff) {
			continue
		}
		date := row.TradeDate.Format("2006-01-02")
		existing, found := byDate[date]
		if !found || (row.Source == preferredSource && existing.Source != preferredSource) {
			byDate[date] = row
		}
	}
	rows := make([]PriceSnapshot, 0, minInt(len(byDate), limit))
	for _, row := range byDate {
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].TradeDate.After(rows[j].TradeDate) })
	if len(rows) > limit {
		rows = rows[:limit]
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].TradeDate.Before(rows[j].TradeDate) })
	return rows
}

func candidateTechnicalHistoryRows(rows []PriceSnapshot) []CandidateTechnicalHistoryRow {
	result := make([]CandidateTechnicalHistoryRow, 0, len(rows))
	// The calculation uses chronological data, while the detail table should
	// lead with the newest available trading day.
	for index := len(rows) - 1; index >= 0; index-- {
		row := rows[index]
		result = append(result, CandidateTechnicalHistoryRow{
			TradeDate:       row.TradeDate.Format("2006-01-02"),
			CloseUSD:        priceSnapshotClose(row),
			Volume:          row.Volume,
			DollarVolumeUSD: priceSnapshotClose(row) * float64(maxInt64(row.Volume, 0)),
			Source:          row.Source,
			SourceVersion:   row.SourceVersion,
			Backfilled:      strings.Contains(row.SourceVersion, ":technical-history:"),
		})
	}
	return result
}

func buildCandidateTechnicalAnalysis(rows []PriceSnapshot) CandidateTechnicalAnalysis {
	analysis := CandidateTechnicalAnalysis{
		Status:             TechnicalStatusMissing,
		LiquidityStatus:    "unknown",
		SampleDays:         len(rows),
		RequiredSampleDays: technicalMinimumSamples,
		RelativeStrength:   CandidateRelativeStrength{Status: "missing", BenchmarkTicker: "IWM"},
		AnchoredVWAP:       CandidateAnchoredVWAP{Status: "anchor_unavailable"},
		Signals:            []CandidateTechnicalSignal{},
		TradeSetup:         unavailableCandidateTradeSetup(TechnicalStatusMissing),
	}
	if len(rows) == 0 {
		return analysis
	}
	analysis.Status = TechnicalStatusDataInsufficient
	last := rows[len(rows)-1]
	analysis.TradeDate = last.TradeDate.Format("2006-01-02")
	analysis.CloseUSD = priceSnapshotClose(last)
	analysis.DollarVolumeUSD = analysis.CloseUSD * float64(maxInt64(last.Volume, 0))
	if len(rows) < technicalMinimumSamples {
		analysis.TradeSetup = unavailableCandidateTradeSetup(analysis.Status)
		return analysis
	}
	analysis.Status = TechnicalStatusReady
	previous := rows[len(rows)-2]
	analysis.PriorCloseUSD = priceSnapshotClose(previous)
	prior20 := rows[len(rows)-technicalLookbackDays-1 : len(rows)-1]
	current20 := rows[len(rows)-technicalLookbackDays:]
	analysis.PriorMA20USD = averageSnapshotClose(prior20)
	analysis.MA20USD = averageSnapshotClose(current20)
	if len(rows) >= technicalLongLookbackDays {
		analysis.MA50USD = averageSnapshotClose(rows[len(rows)-technicalLongLookbackDays:])
	}
	if len(rows) >= technicalMA200LookbackDays {
		analysis.MA200USD = averageSnapshotClose(rows[len(rows)-technicalMA200LookbackDays:])
		analysis.MA200Available = analysis.MA200USD > 0
	}
	if analysis.MA20USD > 0 {
		analysis.DistanceToMA20Pct = (analysis.CloseUSD/analysis.MA20USD - 1) * 100
	}
	// PriceSnapshot stores daily closes, rather than OHLC bars. The breakout
	// baseline is therefore the highest prior close, not an intraday high.
	analysis.Prior20DayHighUSD = highestSnapshotClose(prior20)
	if analysis.Prior20DayHighUSD > 0 {
		analysis.DistanceTo20DayHighPct = (analysis.CloseUSD/analysis.Prior20DayHighUSD - 1) * 100
	}
	analysis.AverageVolume20 = averageSnapshotVolume(prior20)
	if analysis.AverageVolume20 > 0 {
		analysis.VolumeRatio20 = float64(last.Volume) / analysis.AverageVolume20
	}
	analysis.AverageDollarVolume20 = averageSnapshotDollarVolume(prior20)
	if analysis.AverageDollarVolume20 > 0 {
		analysis.DollarVolumeRatio20 = analysis.DollarVolumeUSD / analysis.AverageDollarVolume20
	}
	analysis.LiquidityStatus = technicalLiquidityStatus(analysis.AverageDollarVolume20)
	if len(rows) >= technicalLongLookbackDays {
		analysis.High50DayUSD = highestSnapshotClose(rows[len(rows)-technicalLongLookbackDays:])
		if analysis.High50DayUSD > 0 {
			analysis.DistanceTo50DayHighPct = (analysis.CloseUSD/analysis.High50DayUSD - 1) * 100
		}
	}
	if len(rows) >= technicalMA200LookbackDays {
		analysis.High200DayUSD = highestSnapshotClose(rows[len(rows)-technicalMA200LookbackDays:])
		if analysis.High200DayUSD > 0 {
			analysis.DistanceTo200DayHighPct = (analysis.CloseUSD/analysis.High200DayUSD - 1) * 100
		}
	}

	crossedAboveMA20 := analysis.PriorCloseUSD <= analysis.PriorMA20USD && analysis.CloseUSD > analysis.MA20USD
	if crossedAboveMA20 {
		analysis.Signals = append(analysis.Signals, CandidateTechnicalSignal{Kind: TechnicalSignalCrossAboveMA20, Label: "上穿 20 日均线", Direction: "bullish"})
	}
	broken20DayHigh := analysis.Prior20DayHighUSD > 0 && analysis.CloseUSD > analysis.Prior20DayHighUSD
	if broken20DayHigh {
		analysis.Signals = append(analysis.Signals, CandidateTechnicalSignal{Kind: TechnicalSignalBreakout20DayHigh, Label: "突破 20 日最高收盘价", Direction: "bullish"})
	}
	if (crossedAboveMA20 || broken20DayHigh) && analysis.VolumeRatio20 >= technicalVolumeMultiple {
		analysis.Signals = append(analysis.Signals, CandidateTechnicalSignal{Kind: TechnicalSignalVolumeBackedBreakout, Label: "放量突破", Direction: "bullish"})
	}
	analysis.TradeSetup = buildCandidateTradeSetup(analysis)
	return analysis
}

// MissingCandidateTechnicalAnalysis returns the API-safe zero-history state
// used by callers that cannot access a local price series.
func MissingCandidateTechnicalAnalysis() CandidateTechnicalAnalysis {
	return buildCandidateTechnicalAnalysis(nil)
}

func averageSnapshotDollarVolume(rows []PriceSnapshot) float64 {
	if len(rows) == 0 {
		return 0
	}
	total := 0.0
	for _, row := range rows {
		total += priceSnapshotClose(row) * float64(maxInt64(row.Volume, 0))
	}
	return total / float64(len(rows))
}

func technicalLiquidityStatus(averageDollarVolume float64) string {
	switch {
	case averageDollarVolume <= 0:
		return "unknown"
	case averageDollarVolume < 1_000_000:
		return "low"
	case averageDollarVolume < 5_000_000:
		return "limited"
	default:
		return "normal"
	}
}

func maxInt64(value, minimum int64) int64 {
	if value < minimum {
		return minimum
	}
	return value
}

func buildCandidateAnchoredVWAP(rows []PriceSnapshot, events []CandidateSignalEvent, cutoff *time.Time, preferredSource string) CandidateAnchoredVWAP {
	result := CandidateAnchoredVWAP{Status: "anchor_unavailable"}
	if len(rows) == 0 || cutoff == nil || cutoff.IsZero() {
		return result
	}
	var anchor CandidateSignalEvent
	found := false
	for _, event := range events {
		anchorDate := event.BaselineTradeDate
		if anchorDate.IsZero() {
			anchorDate = event.SignalDate
		}
		if anchorDate.IsZero() || anchorDate.After(*cutoff) {
			continue
		}
		anchor, found = event, true
		break
	}
	if !found {
		return result
	}
	anchorDate := anchor.BaselineTradeDate
	if anchorDate.IsZero() {
		anchorDate = anchor.SignalDate
	}
	result.AnchorEventType = anchor.EventType
	result.AnchorLabel = candidateSignalEventLabel(anchor.EventType)
	result.AnchorTradeDate = anchorDate.Format(time.DateOnly)
	result.PriceTradeDate = cutoff.Format(time.DateOnly)
	result.PriceSource = strings.TrimSpace(preferredSource)
	if result.PriceSource == "" {
		result.PriceSource = rows[len(rows)-1].Source
	}
	start := -1
	for index, row := range rows {
		if !row.TradeDate.Before(anchorDate) {
			start = index
			break
		}
	}
	if start < 0 {
		result.Status = "anchor_outside_local_history"
		return result
	}
	weightedSum, volumeSum := 0.0, int64(0)
	for _, row := range rows[start:] {
		close := priceSnapshotClose(row)
		if close <= 0 || row.Volume <= 0 {
			continue
		}
		weightedSum += close * float64(row.Volume)
		volumeSum += row.Volume
		result.TradingDays++
	}
	if result.TradingDays < technicalAnchoredVWAPMinSamples || volumeSum <= 0 {
		result.Status = "insufficient_price_history"
		return result
	}
	result.ApproximateVWAPUSD = weightedSum / float64(volumeSum)
	latest := priceSnapshotClose(rows[len(rows)-1])
	if result.ApproximateVWAPUSD > 0 && latest > 0 {
		result.DistancePct = (latest/result.ApproximateVWAPUSD - 1) * 100
	}
	result.Status = "ready"
	return result
}

func candidateSignalEventLabel(eventType string) string {
	switch eventType {
	case CandidateSignalEnteredA:
		return "进入 A 级候选"
	case CandidateSignalEnteredB:
		return "进入 B 级候选"
	case CandidateSignalUpgradedQuality:
		return "候选质量升级"
	default:
		return eventType
	}
}

func buildCandidateRelativeStrength(ctx context.Context, db *gorm.DB, item CandidateScoreResult, candidateRows []PriceSnapshot) (CandidateRelativeStrength, error) {
	benchmarkRows, err := technicalPriceHistoryForSymbol(ctx, db, "IWM", item.PriceSource, technicalRelativeLongDays+1, item.PriceTradeDate)
	if err != nil {
		return CandidateRelativeStrength{Status: "missing", BenchmarkTicker: "IWM"}, err
	}
	return buildCandidateRelativeStrengthFromRows(candidateRows, benchmarkRows), nil
}

func buildCandidateRelativeStrengthFromRows(candidateRows, benchmarkRows []PriceSnapshot) CandidateRelativeStrength {
	result := CandidateRelativeStrength{Status: "missing", BenchmarkTicker: "IWM"}
	if len(candidateRows) < technicalLookbackDays+1 {
		result.Status = "insufficient_candidate_history"
		return result
	}
	if len(benchmarkRows) == 0 {
		return result
	}
	benchmarkByDate := make(map[string]PriceSnapshot, len(benchmarkRows))
	for _, row := range benchmarkRows {
		benchmarkByDate[row.TradeDate.Format(time.DateOnly)] = row
	}
	matched := make([]candidateBenchmarkMatchedPrice, 0, minInt(len(candidateRows), len(benchmarkRows)))
	for _, row := range candidateRows {
		benchmark, ok := benchmarkByDate[row.TradeDate.Format(time.DateOnly)]
		if !ok || priceSnapshotClose(row) <= 0 || priceSnapshotClose(benchmark) <= 0 {
			continue
		}
		matched = append(matched, candidateBenchmarkMatchedPrice{candidate: priceSnapshotClose(row), benchmark: priceSnapshotClose(benchmark)})
	}
	result.MatchedSampleDays = len(matched)
	if len(matched) < technicalLookbackDays+1 {
		result.Status = "insufficient_benchmark_history"
		return result
	}
	result.Status = "partial"
	populateRelativeStrengthWindow(&result, matched, technicalLookbackDays)
	if len(matched) >= technicalRelativeLongDays+1 {
		populateRelativeStrengthWindow(&result, matched, technicalRelativeLongDays)
		result.Status = "ready"
	}
	return result
}

func populateRelativeStrengthWindow(result *CandidateRelativeStrength, rows []candidateBenchmarkMatchedPrice, days int) {
	end := rows[len(rows)-1]
	start := rows[len(rows)-1-days]
	if start.candidate <= 0 || start.benchmark <= 0 {
		return
	}
	candidateReturn := (end.candidate/start.candidate - 1) * 100
	benchmarkReturn := (end.benchmark/start.benchmark - 1) * 100
	excessReturn := candidateReturn - benchmarkReturn
	if days == technicalLookbackDays {
		result.CandidateReturn20DPct = &candidateReturn
		result.BenchmarkReturn20DPct = &benchmarkReturn
		result.ExcessReturn20DPct = &excessReturn
		return
	}
	result.CandidateReturn60DPct = &candidateReturn
	result.BenchmarkReturn60DPct = &benchmarkReturn
	result.ExcessReturn60DPct = &excessReturn
}

func candidateHasTechnicalSignal(analysis CandidateTechnicalAnalysis, signal string) bool {
	for _, item := range analysis.Signals {
		if item.Kind == signal {
			return true
		}
	}
	return false
}

func priceSnapshotClose(row PriceSnapshot) float64 {
	return float64(row.CloseMicros) / 1_000_000
}

func averageSnapshotClose(rows []PriceSnapshot) float64 {
	if len(rows) == 0 {
		return 0
	}
	sum := 0.0
	for _, row := range rows {
		sum += priceSnapshotClose(row)
	}
	return sum / float64(len(rows))
}

func highestSnapshotClose(rows []PriceSnapshot) float64 {
	highest := 0.0
	for _, row := range rows {
		value := priceSnapshotClose(row)
		if value > highest {
			highest = value
		}
	}
	return highest
}

func averageSnapshotVolume(rows []PriceSnapshot) float64 {
	if len(rows) == 0 {
		return 0
	}
	sum := int64(0)
	for _, row := range rows {
		sum += row.Volume
	}
	return float64(sum) / float64(len(rows))
}
