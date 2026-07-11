package discovery

import (
	"context"
	"math"
	"sort"
	"strings"

	"gorm.io/gorm"
)

type CandidateMarketQuality struct {
	SampleDays          int     `json:"sample_days"`
	AverageDollarVolume float64 `json:"average_dollar_volume_usd"`
	VolatilityPct       float64 `json:"volatility_pct"`
	MomentumPct         float64 `json:"momentum_pct"`
	MaxDrawdownPct      float64 `json:"max_drawdown_pct"`
	Status              string  `json:"status"`
}

func hydrateCandidateMarketQuality(ctx context.Context, db *gorm.DB, items []CandidateScoreResult) error {
	for i := range items {
		var rows []PriceSnapshot
		if err := db.WithContext(ctx).Where("symbol = ? AND quality_status = ?", strings.ToUpper(items[i].Ticker), QualityStatusValid).Order("trade_date DESC").Limit(21).Find(&rows).Error; err != nil {
			return err
		}
		items[i].MarketQuality = buildCandidateMarketQuality(rows)
	}
	return nil
}

func buildCandidateMarketQuality(rows []PriceSnapshot) CandidateMarketQuality {
	if len(rows) == 0 {
		return CandidateMarketQuality{Status: "missing"}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].TradeDate.Before(rows[j].TradeDate) })
	quality := CandidateMarketQuality{SampleDays: len(rows), Status: "healthy"}
	peak, sumDollar := 0.0, 0.0
	returns := []float64{}
	for i, row := range rows {
		close := float64(row.CloseMicros) / 1_000_000
		sumDollar += close * float64(row.Volume)
		if close > peak {
			peak = close
		}
		if peak > 0 {
			quality.MaxDrawdownPct = math.Min(quality.MaxDrawdownPct, (close/peak-1)*100)
		}
		if i > 0 {
			previous := float64(rows[i-1].CloseMicros) / 1_000_000
			if previous > 0 {
				returns = append(returns, (close/previous-1)*100)
			}
		}
	}
	quality.AverageDollarVolume = sumDollar / float64(len(rows))
	first, last := float64(rows[0].CloseMicros)/1_000_000, float64(rows[len(rows)-1].CloseMicros)/1_000_000
	if first > 0 {
		quality.MomentumPct = (last/first - 1) * 100
	}
	if len(returns) > 1 {
		mean := averageFloat64(returns)
		sum := 0.0
		for _, value := range returns {
			sum += math.Pow(value-mean, 2)
		}
		quality.VolatilityPct = math.Sqrt(sum / float64(len(returns)-1))
	}
	if quality.AverageDollarVolume < 500_000 || quality.VolatilityPct >= 10 || quality.MomentumPct <= -20 {
		quality.Status = "risk"
	}
	return quality
}
