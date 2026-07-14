package discovery

import (
	"context"
	"sort"
	"strings"

	"gorm.io/gorm"
)

const (
	technicalLookbackDays      = 20
	technicalMinimumSamples    = technicalLookbackDays + 1
	technicalDetailHistoryDays = 35
	technicalVolumeMultiple    = 1.5
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
	Status                 string                     `json:"status"`
	SampleDays             int                        `json:"sample_days"`
	RequiredSampleDays     int                        `json:"required_sample_days"`
	TradeDate              string                     `json:"trade_date"`
	CloseUSD               float64                    `json:"close_usd"`
	MA20USD                float64                    `json:"ma20_usd"`
	PriorCloseUSD          float64                    `json:"prior_close_usd"`
	PriorMA20USD           float64                    `json:"prior_ma20_usd"`
	DistanceToMA20Pct      float64                    `json:"distance_to_ma20_pct"`
	Prior20DayHighUSD      float64                    `json:"prior_20d_high_usd"`
	DistanceTo20DayHighPct float64                    `json:"distance_to_20d_high_pct"`
	AverageVolume20        float64                    `json:"average_volume_20"`
	VolumeRatio20          float64                    `json:"volume_ratio_20"`
	Signals                []CandidateTechnicalSignal `json:"signals"`
}

// CandidateTechnicalHistoryRow is one local daily price record used for
// technical research. Backfilled distinguishes a one-time history fetch from
// the regular daily market sync.
type CandidateTechnicalHistoryRow struct {
	TradeDate     string  `json:"trade_date"`
	CloseUSD      float64 `json:"close_usd"`
	Volume        int64   `json:"volume"`
	Source        string  `json:"source"`
	SourceVersion string  `json:"source_version"`
	Backfilled    bool    `json:"backfilled"`
}

func hydrateCandidateTechnicalAnalysis(ctx context.Context, db *gorm.DB, items []CandidateScoreResult) error {
	for i := range items {
		rows, err := candidateTechnicalPriceHistory(ctx, db, items[i])
		if err != nil {
			return err
		}
		items[i].Technical = buildCandidateTechnicalAnalysis(rows)
	}
	return nil
}

func candidateTechnicalPriceHistory(ctx context.Context, db *gorm.DB, item CandidateScoreResult) ([]PriceSnapshot, error) {
	return candidateTechnicalPriceHistoryLimit(ctx, db, item, technicalMinimumSamples)
}

func candidateTechnicalPriceHistoryLimit(ctx context.Context, db *gorm.DB, item CandidateScoreResult, limit int) ([]PriceSnapshot, error) {
	if limit <= 0 {
		limit = technicalMinimumSamples
	}
	query := db.WithContext(ctx).Where("symbol = ? AND quality_status = ?", strings.ToUpper(strings.TrimSpace(item.Ticker)), QualityStatusValid)
	var raw []PriceSnapshot
	if err := query.Order("trade_date DESC, created_at DESC, id DESC").Limit(limit * 12).Find(&raw).Error; err != nil {
		return nil, err
	}
	preferredSource := strings.TrimSpace(item.PriceSource)
	byDate := map[string]PriceSnapshot{}
	for _, row := range raw {
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
	return rows, nil
}

func candidateTechnicalHistoryRows(rows []PriceSnapshot) []CandidateTechnicalHistoryRow {
	result := make([]CandidateTechnicalHistoryRow, 0, len(rows))
	// The calculation uses chronological data, while the detail table should
	// lead with the newest available trading day.
	for index := len(rows) - 1; index >= 0; index-- {
		row := rows[index]
		result = append(result, CandidateTechnicalHistoryRow{
			TradeDate:     row.TradeDate.Format("2006-01-02"),
			CloseUSD:      priceSnapshotClose(row),
			Volume:        row.Volume,
			Source:        row.Source,
			SourceVersion: row.SourceVersion,
			Backfilled:    strings.Contains(row.SourceVersion, ":technical-history:"),
		})
	}
	return result
}

func buildCandidateTechnicalAnalysis(rows []PriceSnapshot) CandidateTechnicalAnalysis {
	analysis := CandidateTechnicalAnalysis{
		Status:             TechnicalStatusMissing,
		SampleDays:         len(rows),
		RequiredSampleDays: technicalMinimumSamples,
		Signals:            []CandidateTechnicalSignal{},
	}
	if len(rows) == 0 {
		return analysis
	}
	analysis.Status = TechnicalStatusDataInsufficient
	last := rows[len(rows)-1]
	analysis.TradeDate = last.TradeDate.Format("2006-01-02")
	analysis.CloseUSD = priceSnapshotClose(last)
	if len(rows) < technicalMinimumSamples {
		return analysis
	}
	analysis.Status = TechnicalStatusReady
	previous := rows[len(rows)-2]
	analysis.PriorCloseUSD = priceSnapshotClose(previous)
	analysis.PriorMA20USD = averageSnapshotClose(rows[:technicalLookbackDays])
	analysis.MA20USD = averageSnapshotClose(rows[1:])
	if analysis.MA20USD > 0 {
		analysis.DistanceToMA20Pct = (analysis.CloseUSD/analysis.MA20USD - 1) * 100
	}
	// PriceSnapshot stores daily closes, rather than OHLC bars. The breakout
	// baseline is therefore the highest prior close, not an intraday high.
	analysis.Prior20DayHighUSD = highestSnapshotClose(rows[:technicalLookbackDays])
	if analysis.Prior20DayHighUSD > 0 {
		analysis.DistanceTo20DayHighPct = (analysis.CloseUSD/analysis.Prior20DayHighUSD - 1) * 100
	}
	analysis.AverageVolume20 = averageSnapshotVolume(rows[:technicalLookbackDays])
	if analysis.AverageVolume20 > 0 {
		analysis.VolumeRatio20 = float64(last.Volume) / analysis.AverageVolume20
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
	return analysis
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
