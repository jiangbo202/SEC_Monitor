package service

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/robfig/cron/v3"
	"sec_monitor/internal/discovery"
	"sec_monitor/internal/model"
)

type APICapabilitySchedule struct {
	TaskName               string     `json:"task_name"`
	Enabled                bool       `json:"enabled"`
	NextRunAt              *time.Time `json:"next_run_at,omitempty"`
	LastStatus             string     `json:"last_status"`
	UniverseSize           int        `json:"universe_size"`
	ReceiptMissing         int        `json:"receipt_missing"`
	ReceiptStale           int        `json:"receipt_stale"`
	MinimumRounds          int        `json:"minimum_rounds"`
	EarliestFullRotationAt *time.Time `json:"earliest_full_rotation_at,omitempty"`
	FreshnessFeasible      *bool      `json:"freshness_feasible,omitempty"`
	ResearchTTLHours       int        `json:"research_ttl_hours"`
	Note                   string     `json:"note"`
}

func (s *APIManagementService) annotateCapabilitySchedules(ctx context.Context, result *APIManagementOverview, tasks []model.TaskConfig, now time.Time) error {
	byTask := map[string]model.TaskConfig{}
	for _, task := range tasks {
		byTask[task.TaskName] = task
	}
	loc, _, err := s.configs.SchedulerTimezone(ctx)
	if err != nil {
		return err
	}
	// This is the same complete published score set used by candidate P1/P2
	// rotation. It deliberately includes grades outside the visible A/B table.
	var candidates []string
	if err := s.db.WithContext(ctx).Model(&discovery.CandidateScoreSnapshot{}).Where("batch_id IN (?)", s.db.Model(&discovery.CurrentBatchPointer{}).Select("batch_id").Where("kind = ?", discovery.BatchKindPrescreen)).Distinct("ticker").Pluck("ticker", &candidates).Error; err != nil {
		return err
	}
	var watches []string
	if err := s.main.WithContext(ctx).Model(&model.WatchTarget{}).Where("status = ? AND target_type = ?", "enabled", "stock").Distinct("ticker").Pluck("ticker", &watches).Error; err != nil {
		return err
	}
	bindings := map[string][]string{

		"eps": {"longbridge_candidate_research_sync"}, "watch_research": {"longbridge_watch_target_research_sync"},
		"valuation": {"longbridge_candidate_valuation_sync"}, "watch_valuation": {"longbridge_watch_target_valuation_sync"},
		"ownership": {"longbridge_institutional_ownership_sync"}, "aggregate": {"futu_institutional_ownership_sync"},
		"options": {"longbridge_candidate_option_research_sync", "longbridge_watch_target_option_research_sync"},
	}
	modules := map[string]APIModuleView{}
	for _, module := range result.Modules {
		modules[module.Key] = module
	}
	for index := range result.Capabilities {
		cap := &result.Capabilities[index]
		if !cap.Implemented {
			cap.EffectiveStatus = "unimplemented"
			continue
		}
		if len(bindings[cap.Key]) == 0 {
			continue
		}
		moduleKey := cap.Key
		if cap.Key == "aggregate" {
			moduleKey = "futu_ownership"
		}
		if cap.Key == "watch_research" {
			moduleKey = "eps"
		}
		if cap.Key == "watch_valuation" {
			moduleKey = "valuation"
		}
		cap.EffectiveStatus = "task_disabled"
		anyEnabled := false
		for _, taskName := range bindings[cap.Key] {
			task, ok := byTask[taskName]
			if !ok {
				continue
			}
			anyEnabled = anyEnabled || task.Enabled
			universe := candidates
			if cap.Key == "watch_research" || cap.Key == "watch_valuation" || taskName == "longbridge_watch_target_option_research_sync" {
				universe = watches
			}
			// Ownership tasks have dedicated queue selection; do not substitute
			// the candidate table for that task's actual scope.
			if cap.Key == "ownership" || cap.Key == "aggregate" {
				universe = nil
			}
			ttl := cap.TTLHours
			if ttl == 24 {
				ttl = 7 * 24
			}
			row := APICapabilitySchedule{TaskName: taskName, Enabled: task.Enabled, NextRunAt: task.NextRunAt, LastStatus: task.LastStatus, UniverseSize: len(universe), ResearchTTLHours: ttl, Note: "理论下限假设每轮预算用满且全部成功；无覆盖回执也计为成功确认。每轮标的预算不是 HTTP 请求预算。"}
			if universe == nil {
				row.Note = "独立任务按监控、候选和待同步队列选择；研究新鲜度 168 小时与手动刷新 24 小时缓存不同。"
			}
			family := cap.Key
			if family == "watch_research" {
				family = "eps"
			}
			if family == "watch_valuation" {
				family = "valuation"
			}
			receipts, err := discovery.APIDataSyncReceipts(ctx, s.db, cap.Provider, family)
			if err != nil {
				return err
			}
			for _, ticker := range universe {
				receipt, ok := receipts[ticker]
				if !ok || receipt.LastSuccessAt == nil {
					row.ReceiptMissing++
				} else if now.Sub(*receipt.LastSuccessAt) > time.Duration(ttl)*time.Hour {
					row.ReceiptStale++
				}
			}
			if cap.IssuerBudget > 0 && len(universe) > 0 {
				row.MinimumRounds = int(math.Ceil(float64(len(universe)) / float64(cap.IssuerBudget)))
				if schedule, err := cron.ParseStandard(task.CronExpr); err == nil && task.Enabled {
					at := now.In(loc)
					for n := 0; n < row.MinimumRounds; n++ {
						at = schedule.Next(at)
					}
					if !at.IsZero() {
						at = at.UTC()
						row.EarliestFullRotationAt = &at
						feasible := at.Sub(now) <= time.Duration(ttl)*time.Hour
						row.FreshnessFeasible = &feasible
					}
				}
			}
			cap.Schedules = append(cap.Schedules, row)
		}
		if cap.ConfigKey == "" {
			cap.AutoEnabled = anyEnabled
		}
		if anyEnabled && (cap.ConfigKey == "" || cap.AutoEnabled) {
			cap.EffectiveStatus = "scheduled"
		} else if !cap.AutoEnabled && cap.ConfigKey != "" {
			cap.EffectiveStatus = "capability_disabled"
		}
		if module, ok := modules[moduleKey]; ok && module.Status != "ready" && module.Status != "ready_unverified" {
			cap.EffectiveStatus = module.Status
		}
		if cap.EffectiveStatus == "scheduled" {
			for _, row := range cap.Schedules {
				if row.FreshnessFeasible != nil && !*row.FreshnessFeasible {
					cap.MetricScope += fmt.Sprintf("；当前预算完整轮转至少 %d 轮，超过 %d 小时新鲜度目标", row.MinimumRounds, row.ResearchTTLHours)
					break
				}
			}
		}
	}
	return nil
}
