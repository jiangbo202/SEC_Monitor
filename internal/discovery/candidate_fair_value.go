package discovery

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"gorm.io/gorm"
)

// GetTickerFairValueEstimate builds the same cached research view for any
// stock ticker. It intentionally performs no Longbridge request, so watch
// target and candidate detail pages remain fast and deterministic.
func GetTickerFairValueEstimate(ctx context.Context, db *gorm.DB, ticker string) (CandidateFairValueEstimate, error) {
	if db == nil {
		return CandidateFairValueEstimate{}, errors.New("database is required")
	}
	history, err := GetTickerTechnicalHistory(ctx, db, ticker)
	if err != nil {
		return CandidateFairValueEstimate{}, err
	}
	analyst, err := GetAnalystRating(ctx, db, ticker)
	if err != nil {
		return CandidateFairValueEstimate{}, err
	}
	valuation, err := GetCandidateValuationResearch(ctx, db, ticker)
	if err != nil {
		return CandidateFairValueEstimate{}, err
	}
	return buildCandidateFairValueEstimate(history.Technical, analyst.Latest, valuation.Latest), nil
}

const (
	CandidateFairValueStatusAvailable    = "available"
	CandidateFairValueStatusInsufficient = "insufficient"
)

// CandidateFairValueEstimate keeps two deliberately different concepts side
// by side: Longbridge's provider-issued analyst target and a local,
// reproducible historical-multiple scenario. Neither is presented as a
// provider-issued intrinsic value or as investment advice.
type CandidateFairValueEstimate struct {
	Status                   string                    `json:"status"`
	Currency                 string                    `json:"currency"`
	ReferencePrice           *float64                  `json:"reference_price,omitempty"`
	ReferencePriceDate       string                    `json:"reference_price_date,omitempty"`
	ReferencePriceSource     string                    `json:"reference_price_source,omitempty"`
	MarketConsensusTarget    *float64                  `json:"market_consensus_target,omitempty"`
	MarketConsensusLow       *float64                  `json:"market_consensus_low,omitempty"`
	MarketConsensusHigh      *float64                  `json:"market_consensus_high,omitempty"`
	MarketConsensusUpsidePct *float64                  `json:"market_consensus_upside_pct,omitempty"`
	AnalystCount             int                       `json:"analyst_count"`
	LocalHistoricalScenario  *FairValueScenarioRange   `json:"local_historical_scenario,omitempty"`
	MetricScenarios          []FairValueMetricScenario `json:"metric_scenarios"`
	Methodology              string                    `json:"methodology"`
	Message                  string                    `json:"message"`
	Quality                  DataQualityMetadata       `json:"quality"`
}

type FairValueScenarioRange struct {
	Low     float64 `json:"low"`
	Mid     float64 `json:"mid"`
	High    float64 `json:"high"`
	Metrics int     `json:"metrics"`
}

type FairValueMetricScenario struct {
	Metric          string  `json:"metric"`
	CurrentMultiple float64 `json:"current_multiple"`
	HistoricalLow   float64 `json:"historical_low"`
	HistoricalMid   float64 `json:"historical_mid"`
	HistoricalHigh  float64 `json:"historical_high"`
	PriceLow        float64 `json:"price_low"`
	PriceMid        float64 `json:"price_mid"`
	PriceHigh       float64 `json:"price_high"`
}

func buildCandidateFairValueEstimate(technical CandidateTechnicalAnalysis, analyst *AnalystRatingSnapshot, valuation *ValuationResearchSnapshot) CandidateFairValueEstimate {
	result := CandidateFairValueEstimate{
		Status:          CandidateFairValueStatusInsufficient,
		Currency:        "USD",
		MetricScenarios: []FairValueMetricScenario{},
		Methodology:     "本地历史倍数情景：参考收盘价 ×（Longbridge 历史 P/E、P/B、P/S 低/中/高倍数 ÷ 当前对应倍数），再对可用指标等权平均。非 DCF、非 Longbridge 官方公允价值，也不构成投资建议。",
		Quality:         DataQualityMetadata{Layer: DataLayerFeature, Source: "local_research", SourceVersion: "fair-value-scenario-v1", QualityStatus: QualityStatusMissing},
	}
	if analyst != nil && strings.TrimSpace(analyst.Currency) != "" {
		result.Currency = strings.TrimSpace(analyst.Currency)
	}
	if technical.CloseUSD > 0 {
		value := technical.CloseUSD
		result.ReferencePrice = &value
		result.ReferencePriceDate = technical.TradeDate
		result.ReferencePriceSource = "本地日线收盘价"
		result.Quality.AsOf = technical.TradeDate
	} else if analyst != nil && analyst.ReferencePriceMicros > 0 {
		value := float64(analyst.ReferencePriceMicros) / 1_000_000
		result.ReferencePrice = &value
		result.ReferencePriceSource = "Longbridge 机构目标价参考收盘价"
	}
	if analyst != nil && analyst.Status == AnalystRatingStatusAvailable {
		result.AnalystCount = analyst.AnalystCount
		if analyst.TargetAverageMicros > 0 {
			value := float64(analyst.TargetAverageMicros) / 1_000_000
			result.MarketConsensusTarget = &value
		}
		if analyst.TargetLowMicros > 0 {
			value := float64(analyst.TargetLowMicros) / 1_000_000
			result.MarketConsensusLow = &value
		}
		if analyst.TargetHighMicros > 0 {
			value := float64(analyst.TargetHighMicros) / 1_000_000
			result.MarketConsensusHigh = &value
		}
	}
	if result.ReferencePrice != nil && result.MarketConsensusTarget != nil && *result.ReferencePrice > 0 {
		value := (*result.MarketConsensusTarget / *result.ReferencePrice - 1) * 100
		result.MarketConsensusUpsidePct = &value
	}
	if result.ReferencePrice != nil && valuation != nil {
		for _, input := range []struct {
			name   string
			metric ValuationMetric
		}{
			{name: "PE", metric: valuation.Metrics.PE},
			{name: "PB", metric: valuation.Metrics.PB},
			{name: "PS", metric: valuation.Metrics.PS},
		} {
			if scenario, ok := fairValueScenarioForMetric(input.name, *result.ReferencePrice, input.metric); ok {
				result.MetricScenarios = append(result.MetricScenarios, scenario)
			}
		}
	}
	if len(result.MetricScenarios) > 0 {
		low, mid, high := 0.0, 0.0, 0.0
		for _, scenario := range result.MetricScenarios {
			low += scenario.PriceLow
			mid += scenario.PriceMid
			high += scenario.PriceHigh
		}
		count := float64(len(result.MetricScenarios))
		result.LocalHistoricalScenario = &FairValueScenarioRange{Low: low / count, Mid: mid / count, High: high / count, Metrics: len(result.MetricScenarios)}
	}
	if result.MarketConsensusTarget != nil && result.LocalHistoricalScenario != nil {
		result.Status = CandidateFairValueStatusAvailable
		result.Quality.QualityStatus = QualityStatusValid
		result.Message = "市场一致目标价来自 Longbridge 机构评级；本地情景区间仅用 Longbridge 历史估值倍数归一计算。两者均不等同于公允价值。"
		return result
	}
	if result.MarketConsensusTarget != nil {
		result.Status = CandidateFairValueStatusAvailable
		result.Quality.QualityStatus = QualityStatusValid
		result.Message = "已取得 Longbridge 市场一致目标价；当前没有可同时使用的正值 P/E、P/B 或 P/S 历史倍数，因此未生成本地历史估值情景。"
		return result
	}
	if result.LocalHistoricalScenario != nil {
		result.Status = CandidateFairValueStatusAvailable
		result.Quality.QualityStatus = QualityStatusValid
		result.Message = "已生成本地历史估值情景；Longbridge 暂无该标的可用的机构市场一致目标价。"
		return result
	}
	missing := make([]string, 0, 2)
	if analyst == nil || analyst.TargetAverageMicros <= 0 {
		missing = append(missing, "机构目标价")
	}
	if valuation == nil || result.ReferencePrice == nil {
		missing = append(missing, "可用估值倍数或参考收盘价")
	}
	sort.Strings(missing)
	result.Message = fmt.Sprintf("暂无法生成市场目标价或本地历史估值情景：缺少%s。", strings.Join(missing, "、"))
	return result
}

func fairValueScenarioForMetric(name string, referencePrice float64, metric ValuationMetric) (FairValueMetricScenario, bool) {
	if referencePrice <= 0 || metric.Current == nil || metric.Low == nil || metric.Median == nil || metric.High == nil || *metric.Current <= 0 || *metric.Low <= 0 || *metric.Median <= 0 || *metric.High <= 0 {
		return FairValueMetricScenario{}, false
	}
	result := FairValueMetricScenario{
		Metric: name, CurrentMultiple: *metric.Current, HistoricalLow: *metric.Low, HistoricalMid: *metric.Median, HistoricalHigh: *metric.High,
		PriceLow:  referencePrice * *metric.Low / *metric.Current,
		PriceMid:  referencePrice * *metric.Median / *metric.Current,
		PriceHigh: referencePrice * *metric.High / *metric.Current,
	}
	return result, true
}
