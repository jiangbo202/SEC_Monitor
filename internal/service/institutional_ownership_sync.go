package service

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"sec_monitor/internal/discovery"
	"sec_monitor/internal/model"
)

const ownershipRefreshFamily = "institutional_ownership"

// Independent cursor and caps avoid spending the P1 queue's budget. Each source
// contributes at most two issuers; each issuer costs at most one top + five details.
func (s *DiscoverySyncService) SyncInstitutionalOwnership(ctx context.Context) (discovery.CandidateMarketResearchSyncResult, error) {
	result := discovery.CandidateMarketResearchSyncResult{Warnings: []string{}}
	if s == nil || s.db == nil {
		return result, errors.New("institutional ownership sync is not configured")
	}
	cfg, err := s.appliedDiscoveryConfig(ctx)
	if err != nil {
		return result, err
	}
	last := map[string]time.Time{}
	if err := discovery.MergeLongbridgeResearchAttempts(ctx, s.db, ownershipRefreshFamily, last); err != nil {
		return result, err
	}
	now := time.Now().UTC()
	tickers := []string{}
	if cfg.LongbridgeWatchTargetResearchEnabled && cfg.LongbridgeWatchTargetResearchRequestBudget > 0 && s.watchDB != nil {
		var targets []model.WatchTarget
		if err := s.watchDB.WithContext(ctx).Where("status = ? AND target_type = ?", "enabled", "stock").Find(&targets).Error; err != nil {
			return result, err
		}
		watch := []string{}
		for _, target := range targets {
			watch = append(watch, strings.ToUpper(strings.TrimSpace(target.Ticker)))
		}
		sort.Slice(watch, func(i, j int) bool {
			if last[watch[i]].Equal(last[watch[j]]) {
				return watch[i] < watch[j]
			}
			return last[watch[i]].Before(last[watch[j]])
		})
		limit := min(2, cfg.LongbridgeWatchTargetResearchRequestBudget)
		for _, ticker := range watch {
			if ticker != "" && now.Sub(last[ticker]) >= 24*time.Hour {
				tickers = append(tickers, ticker)
				if len(tickers) >= limit {
					break
				}
			}
		}
	}
	if cfg.LongbridgeCandidateResearchEnabled && cfg.LongbridgeCandidateResearchRequestBudget > 0 {
		candidates, err := discovery.OwnershipCandidateTickers(ctx, s.db, min(2, cfg.LongbridgeCandidateResearchRequestBudget), last)
		if err != nil {
			return result, err
		}
		tickers = append(tickers, candidates...)
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	seen := map[string]bool{}
	for _, ticker := range tickers {
		if seen[ticker] || now.Sub(last[ticker]) < 24*time.Hour {
			continue
		}
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		seen[ticker] = true
		if err := discovery.MarkLongbridgeResearchAttempt(ctx, s.db, ownershipRefreshFamily, ticker, now); err != nil {
			return result, err
		}
		result.Attempted++
		refreshed, err := discovery.RefreshInstitutionalOwnershipBudgeted(ctx, s.db, cfg, ticker, 5)
		if err != nil {
			result.Failed++
			result.Warnings = append(result.Warnings, ticker+": "+discovery.SanitizeLongbridgeCandidateResearchError(err))
			continue
		}
		if refreshed.FailedRequests > 0 {
			result.Failed++
			result.Warnings = append(result.Warnings, ticker+": 部分机构明细查询失败，已保存可用历史；等待下一次轮转")
			continue
		}
		if err := discovery.MarkLongbridgeResearchSuccess(ctx, s.db, ownershipRefreshFamily, ticker, now); err != nil {
			return result, err
		}
		result.Fetched++
	}
	if result.Attempted == 0 {
		result.Skipped = true
		result.Message = "研究开关关闭、预算为零、暂无标的或轮转队列在 24 小时内已尝试"
	}
	return result, nil
}
