package service

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"sec_monitor/internal/discovery"
	"sec_monitor/internal/model"

	"gorm.io/gorm"
)

// CreateTenB5OnePlanDiscoveryNotifications emits at most one durable inbox
// event per ticker, and only for plan rows created during the current task.
// Existing registry rows therefore never cause a notification flood after an
// upgrade or a routine re-scan.
func CreateTenB5OnePlanDiscoveryNotifications(ctx context.Context, mainDB, discoveryDB *gorm.DB, inApp *InAppNotificationService, discoveredSince time.Time) (int, error) {
	if mainDB == nil || discoveryDB == nil || inApp == nil || discoveredSince.IsZero() {
		return 0, nil
	}
	var plans []discovery.InsiderTradingPlan
	if err := discoveryDB.WithContext(ctx).
		Where("created_at >= ? AND status IN ?", discoveredSince.UTC(), []string{discovery.InsiderPlanStatusActive, discovery.InsiderPlanStatusExecuting}).
		Order("created_at ASC, id ASC").Find(&plans).Error; err != nil {
		return 0, err
	}
	if len(plans) == 0 {
		return 0, nil
	}
	securityIDs := make([]uint, 0, len(plans))
	for _, plan := range plans {
		securityIDs = append(securityIDs, plan.SecurityID)
	}
	tickerBySecurity, err := tenB5OneTickerBySecurity(ctx, discoveryDB, securityIDs)
	if err != nil {
		return 0, err
	}
	var targets []model.WatchTarget
	if err := mainDB.WithContext(ctx).Where("status = ?", "enabled").Find(&targets).Error; err != nil {
		return 0, err
	}
	targetByTicker := make(map[string]model.WatchTarget, len(targets))
	for _, target := range targets {
		targetByTicker[strings.ToUpper(strings.TrimSpace(target.Ticker))] = target
	}
	candidateTickers, err := discovery.CurrentCandidateTickers(ctx, discoveryDB)
	if err != nil {
		return 0, err
	}
	candidates := make(map[string]bool, len(candidateTickers))
	for _, ticker := range candidateTickers {
		candidates[strings.ToUpper(strings.TrimSpace(ticker))] = true
	}
	firstPlanByTicker := map[string]discovery.InsiderTradingPlan{}
	for _, plan := range plans {
		ticker := tickerBySecurity[plan.SecurityID]
		if ticker == "" {
			continue
		}
		if _, watched := targetByTicker[ticker]; !watched && !candidates[ticker] {
			continue
		}
		if _, exists := firstPlanByTicker[ticker]; !exists {
			firstPlanByTicker[ticker] = plan
		}
	}
	tickers := make([]string, 0, len(firstPlanByTicker))
	for ticker := range firstPlanByTicker {
		tickers = append(tickers, ticker)
	}
	sort.Strings(tickers)
	created := 0
	for _, ticker := range tickers {
		plan := firstPlanByTicker[ticker]
		scope, targetID, companyName := "candidate", uint(0), ""
		if target, watched := targetByTicker[ticker]; watched {
			scope, targetID, companyName = "watch_target", target.ID, target.CompanyName
		}
		body := fmt.Sprintf("%s披露于 %s 采用 10b5-1 计划", strings.TrimSpace(plan.OwnerName), plan.AdoptionDate.UTC().Format("2006-01-02"))
		if strings.TrimSpace(plan.OwnerName) == "" {
			body = fmt.Sprintf("公开文件披露采用日期为 %s", plan.AdoptionDate.UTC().Format("2006-01-02"))
		}
		_, inserted, err := inApp.Create(ctx, InAppNotificationInput{
			EventKey: fmt.Sprintf("ten_b5_one:first:%s", ticker), Source: "ten_b5_one_plan_discovered",
			Scope: scope, EntityKind: "insider_trading_plan", TargetID: targetID, Ticker: ticker, CompanyName: companyName,
			Severity: "info", Title: fmt.Sprintf("%s 首次发现 10b5-1 计划", ticker), Body: body,
			Link: fmt.Sprintf("/insider-trading?tab=plans&ticker=%s", ticker), OccurredAt: plan.CreatedAt,
		})
		if err != nil {
			return created, err
		}
		if inserted {
			created++
		}
	}
	return created, nil
}

func tenB5OneTickerBySecurity(ctx context.Context, db *gorm.DB, securityIDs []uint) (map[uint]string, error) {
	result := map[uint]string{}
	if len(securityIDs) == 0 {
		return result, nil
	}
	var listings []discovery.Listing
	if err := db.WithContext(ctx).Where("security_id IN ?", securityIDs).Order("valid_from DESC, id DESC").Find(&listings).Error; err != nil {
		return nil, err
	}
	for _, row := range listings {
		if result[row.SecurityID] == "" {
			result[row.SecurityID] = strings.ToUpper(strings.TrimSpace(row.Ticker))
		}
	}
	var identities []discovery.SecurityBatchIdentity
	if err := db.WithContext(ctx).Where("security_id IN ?", securityIDs).Order("created_at DESC, id DESC").Find(&identities).Error; err != nil {
		return nil, err
	}
	for _, row := range identities {
		if result[row.SecurityID] == "" {
			result[row.SecurityID] = strings.ToUpper(strings.TrimSpace(row.Ticker))
		}
	}
	return result, nil
}
