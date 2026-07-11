package discovery

import (
	"context"
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"
)

var candidateEffectivenessHorizons = []int{1, 5, 20, 60}

type CandidateEffectivenessReport struct {
	GeneratedAt        time.Time                      `json:"generated_at"`
	BenchmarkTicker    string                         `json:"benchmark_ticker"`
	BenchmarkAvailable bool                           `json:"benchmark_available"`
	Cohorts            []CandidateEffectivenessCohort `json:"cohorts"`
}

type CandidateEffectivenessCohort struct {
	Grade          string                         `json:"grade"`
	CandidateCount int                            `json:"candidate_count"`
	Windows        []CandidateEffectivenessWindow `json:"windows"`
}

type CandidateEffectivenessWindow struct {
	HorizonDays        int      `json:"horizon_days"`
	SampleCount        int      `json:"sample_count"`
	AverageReturnPct   *float64 `json:"average_return_pct"`
	WinRatePct         *float64 `json:"win_rate_pct"`
	MaxDrawdownPct     *float64 `json:"max_drawdown_pct"`
	BenchmarkReturnPct *float64 `json:"benchmark_return_pct"`
	ExcessReturnPct    *float64 `json:"excess_return_pct"`
}

type candidateCohortSeed struct {
	CandidateScoreSnapshot
}

func BuildCandidateEffectiveness(ctx context.Context, db *gorm.DB) (CandidateEffectivenessReport, error) {
	report := CandidateEffectivenessReport{
		GeneratedAt: time.Now().UTC(), BenchmarkTicker: "IWM",
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
	seeds, err := firstCandidateCohortSeeds(ctx, db)
	if err != nil {
		return report, err
	}
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
	return report, nil
}

func emptyCandidateEffectivenessWindows() []CandidateEffectivenessWindow {
	items := make([]CandidateEffectivenessWindow, 0, len(candidateEffectivenessHorizons))
	for _, horizon := range candidateEffectivenessHorizons {
		items = append(items, CandidateEffectivenessWindow{HorizonDays: horizon})
	}
	return items
}

func firstCandidateCohortSeeds(ctx context.Context, db *gorm.DB) ([]candidateCohortSeed, error) {
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
	for index, horizon := range candidateEffectivenessHorizons {
		returns := []float64{}
		benchmarkReturns := []float64{}
		maxDrawdown := 0.0
		for _, seed := range seeds {
			baseDate, baseClose, ok, err := candidatePerformanceBaseline(ctx, db, CandidateScoreResult{CandidateScoreSnapshot: seed.CandidateScoreSnapshot})
			if err != nil {
				return result, benchmarkAvailable, err
			}
			if !ok {
				continue
			}
			ret, drawdown, mature, err := candidateHorizonReturn(ctx, db, seed.Ticker, baseDate, baseClose, horizon)
			if err != nil {
				return result, benchmarkAvailable, err
			}
			if !mature {
				continue
			}
			returns = append(returns, ret)
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
		if len(benchmarkReturns) > 0 {
			benchmarkReturn := averageFloat64(benchmarkReturns)
			excessReturn := averageReturn - benchmarkReturn
			result[index].BenchmarkReturnPct = &benchmarkReturn
			result[index].ExcessReturnPct = &excessReturn
		}
	}
	return result, benchmarkAvailable, nil
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
