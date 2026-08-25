package discovery

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
)

const (
	ResearchActionGateReady   = "ready"
	ResearchActionGateBlocked = "blocked"
)

// ResearchActionGate is the server-side guard for creating or increasing a
// hand-maintained research allocation. It deliberately evaluates persisted
// daily facts only and is independent from the dashboard UI.
type ResearchActionGate struct {
	Status                string    `json:"status"`
	Allowed               bool      `json:"allowed"`
	ScoringVersion        string    `json:"scoring_version,omitempty"`
	EffectivenessStatus   string    `json:"effectiveness_status,omitempty"`
	OutcomeTrackingStatus string    `json:"outcome_tracking_status,omitempty"`
	AsOf                  string    `json:"as_of,omitempty"`
	Reasons               []string  `json:"reasons"`
	EvaluatedAt           time.Time `json:"evaluated_at"`
}

func BuildResearchActionGate(ctx context.Context, db *gorm.DB, now time.Time) (ResearchActionGate, error) {
	result := ResearchActionGate{Status: ResearchActionGateReady, Allowed: true, Reasons: []string{}, EvaluatedAt: now.UTC()}
	if db == nil {
		return result, errors.New("database is required")
	}
	health, err := BuildCandidateHealth(ctx, db)
	if err != nil {
		return result, err
	}
	result.AsOf = health.PriceEffectiveDate
	block := func(reason string) {
		result.Allowed = false
		result.Status = ResearchActionGateBlocked
		result.Reasons = append(result.Reasons, reason)
	}
	if health.TotalCandidates == 0 || health.Status == CandidateHealthMissing {
		block("当前没有可用的小盘候选批次")
	}
	if health.MissingPriceCandidates > 0 || health.StalePriceCandidates > 0 || health.FallbackPriceCandidates > 0 || health.MissingMarketCap > 0 {
		block(fmt.Sprintf("当前候选存在行情或市值缺口：缺价 %d、过期 %d、回退 %d、缺市值 %d", health.MissingPriceCandidates, health.StalePriceCandidates, health.FallbackPriceCandidates, health.MissingMarketCap))
	}
	if health.OpenDataQualityIncidents > 0 {
		block(fmt.Sprintf("存在 %d 条尚未关闭的数据质量事件", health.OpenDataQualityIncidents))
	}
	effectiveness, err := BuildCandidateEffectiveness(ctx, db)
	if err != nil {
		return result, err
	}
	result.ScoringVersion = effectiveness.ScoringVersion
	result.EffectivenessStatus = effectiveness.Status
	result.OutcomeTrackingStatus = effectiveness.OutcomeTrackingStatus
	if effectiveness.Status != "validated" {
		block("当前评分版本的20日效果尚未达到验证门槛")
	}
	if effectiveness.OutcomeTrackingStatus != "current" {
		block("信号结果闭环尚未完整运行")
	}
	return result, nil
}
