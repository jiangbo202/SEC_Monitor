package scheduler

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"sec_monitor/internal/service"

	"github.com/robfig/cron/v3"
)

const (
	candidateNotificationSyncTaskName               = "candidate_notification_sync"
	tradeSetupNotificationSyncTaskName              = "trade_setup_notification_sync"
	ipoRadarSyncTaskName                            = "ipo_radar_sync"
	ipoLifecycleReconcileSyncTaskName               = "ipo_lifecycle_reconcile_sync"
	ipoOfferingReconcileSyncTaskName                = "ipo_offering_reconcile_sync"
	ipoListingReconcileSyncTaskName                 = "ipo_listing_reconcile_sync"
	smallCapDiscoverySyncTaskName                   = "small_cap_discovery_sync"
	smallCapDiscoveryFullSyncTaskName               = "small_cap_discovery_full_sync"
	watchTargetMarketSyncTaskName                   = "watch_target_market_sync"
	watchTargetEarningsSyncTaskName                 = "watch_target_earnings_sync"
	secFilingSyncTaskName                           = "sec_filing_sync"
	notificationRetrySyncTaskName                   = "notification_retry_sync"
	sqliteBackupTaskName                            = "sqlite_backup"
	operationHistoryCleanupTaskName                 = "operation_history_cleanup"
	operationalHealthNotificationTaskName           = "operational_health_notification_sync"
	macroCalendarSyncTaskName                       = "macro_calendar_sync"
	marketTrendSyncTaskName                         = "market_trend_sync"
	usFuturesSyncTaskName                           = "us_futures_sync"
	institutionalHoldingsSyncTaskName               = "institutional_holdings_sync"
	longbridgeCandidateResearchSyncTaskName         = "longbridge_candidate_research_sync"
	longbridgeCandidateValuationSyncTaskName        = "longbridge_candidate_valuation_sync"
	longbridgeWatchTargetValuationSyncTaskName      = "longbridge_watch_target_valuation_sync"
	longbridgeWatchTargetResearchSyncTaskName       = "longbridge_watch_target_research_sync"
	longbridgeCandidateOptionResearchSyncTaskName   = "longbridge_candidate_option_research_sync"
	longbridgeWatchTargetOptionResearchSyncTaskName = "longbridge_watch_target_option_research_sync"
)

const ipoRecentManualSuccessCooldown = 10 * time.Minute

type Scheduler struct {
	cron                    *cron.Cron
	tasks                   *service.TaskConfigService
	configs                 *service.ConfigService
	filings                 *service.FilingService
	ipo                     *service.IPORadarService
	candidateNotifications  *service.CandidateNotificationService
	tradeSetupNotifications *service.TradeSetupNotificationService
	discoverySync           *service.DiscoverySyncService
	notificationBatches     *service.NotificationBatchService
	backup                  *service.SQLiteBackupService
	lifecycle               *service.LifecycleService
	operationalHealth       *service.OperationalHealthService
	macroCalendar           *service.MacroCalendarService
	marketTrend             *service.MarketTrendService
	usFutures               *service.USFuturesService
	institutionalHoldings   *service.InstitutionalHoldingsService
	earningsPreview         *service.EarningsPreviewService
	mu                      sync.Mutex
	runningTasks            map[string]bool
	runningSECTask          string
	started                 bool
}

func New(tasks *service.TaskConfigService, filings *service.FilingService, services ...any) *Scheduler {
	var ipoService *service.IPORadarService
	var candidateNotifications *service.CandidateNotificationService
	var tradeSetupNotifications *service.TradeSetupNotificationService
	var discoverySync *service.DiscoverySyncService
	var notificationBatches *service.NotificationBatchService
	var backup *service.SQLiteBackupService
	var lifecycle *service.LifecycleService
	var operationalHealth *service.OperationalHealthService
	var macroCalendar *service.MacroCalendarService
	var marketTrend *service.MarketTrendService
	var usFutures *service.USFuturesService
	var institutionalHoldings *service.InstitutionalHoldingsService
	var earningsPreview *service.EarningsPreviewService
	var configs *service.ConfigService
	for _, svc := range services {
		switch typed := svc.(type) {
		case *service.ConfigService:
			configs = typed
		case *service.IPORadarService:
			ipoService = typed
		case *service.CandidateNotificationService:
			candidateNotifications = typed
		case *service.TradeSetupNotificationService:
			tradeSetupNotifications = typed
		case *service.DiscoverySyncService:
			discoverySync = typed
		case *service.NotificationBatchService:
			notificationBatches = typed
		case *service.SQLiteBackupService:
			backup = typed
		case *service.LifecycleService:
			lifecycle = typed
		case *service.OperationalHealthService:
			operationalHealth = typed
		case *service.MacroCalendarService:
			macroCalendar = typed
		case *service.MarketTrendService:
			marketTrend = typed
		case *service.USFuturesService:
			usFutures = typed
		case *service.InstitutionalHoldingsService:
			institutionalHoldings = typed
		case *service.EarningsPreviewService:
			earningsPreview = typed
		}
	}
	return &Scheduler{
		cron:                    cron.New(cron.WithChain(cron.Recover(cron.PrintfLogger(log.Default())))),
		tasks:                   tasks,
		configs:                 configs,
		filings:                 filings,
		ipo:                     ipoService,
		candidateNotifications:  candidateNotifications,
		tradeSetupNotifications: tradeSetupNotifications,
		discoverySync:           discoverySync,
		notificationBatches:     notificationBatches,
		backup:                  backup,
		lifecycle:               lifecycle,
		operationalHealth:       operationalHealth,
		macroCalendar:           macroCalendar,
		marketTrend:             marketTrend,
		usFutures:               usFutures,
		institutionalHoldings:   institutionalHoldings,
		earningsPreview:         earningsPreview,
		runningTasks:            make(map[string]bool),
	}
}

func (s *Scheduler) Start(ctx context.Context) error {
	if err := s.Reload(ctx); err != nil {
		return err
	}
	s.mu.Lock()
	s.cron.Start()
	s.started = true
	s.mu.Unlock()
	return nil
}

func (s *Scheduler) Stop() context.Context {
	s.mu.Lock()
	cronInstance := s.cron
	s.started = false
	s.mu.Unlock()
	return cronInstance.Stop()
}

func (s *Scheduler) Reload(ctx context.Context) error {
	location, err := s.schedulerLocation(ctx)
	if err != nil {
		return err
	}
	nextCron := cron.New(cron.WithLocation(location), cron.WithChain(cron.Recover(cron.PrintfLogger(log.Default()))))
	tasks, err := s.tasks.List(ctx)
	if err != nil {
		return err
	}
	nextRuns := make(map[string]*time.Time, len(tasks))
	now := time.Now().In(location)
	for _, task := range tasks {
		if !task.Enabled {
			nextRuns[task.TaskName] = nil
			continue
		}
		taskName := task.TaskName
		if !s.canRunTask(taskName) {
			nextRuns[taskName] = nil
			continue
		}
		schedule, err := cron.ParseStandard(task.CronExpr)
		if err != nil {
			return err
		}
		nextRunAt := schedule.Next(now).UTC()
		nextRuns[taskName] = &nextRunAt
		if _, err := nextCron.AddFunc(task.CronExpr, func() {
			_ = s.runTaskWithTrigger(context.Background(), taskName, "scheduled")
		}); err != nil {
			return err
		}
	}
	if err := s.tasks.SetNextRunAts(ctx, nextRuns); err != nil {
		return fmt.Errorf("persist scheduler next-run plan: %w", err)
	}
	s.mu.Lock()
	previousCron := s.cron
	wasStarted := s.started
	s.cron = nextCron
	if wasStarted {
		s.cron.Start()
	}
	s.mu.Unlock()
	if wasStarted && previousCron != nil {
		<-previousCron.Stop().Done()
	}
	return nil
}

func (s *Scheduler) schedulerLocation(ctx context.Context) (*time.Location, error) {
	if s == nil || s.configs == nil {
		return time.UTC, nil
	}
	location, _, err := s.configs.SchedulerTimezone(ctx)
	if err != nil {
		return nil, err
	}
	return location, nil
}

func (s *Scheduler) RunOnce(ctx context.Context) error {
	return s.RunTask(ctx, secFilingSyncTaskName)
}

func (s *Scheduler) RunTask(ctx context.Context, taskName string) error {
	return s.runTaskWithTrigger(ctx, taskName, "manual")
}

func (s *Scheduler) runTaskWithTrigger(ctx context.Context, taskName, trigger string) (err error) {
	s.mu.Lock()
	if s.runningTasks[taskName] {
		s.mu.Unlock()
		return service.TaskAlreadyRunning(taskName)
	}
	usesSEC := taskUsesLiveSEC(taskName)
	if usesSEC && s.runningSECTask != "" {
		blockingTask := s.runningSECTask
		s.mu.Unlock()
		return service.TaskResourceBusy(taskName, blockingTask)
	}
	s.runningTasks[taskName] = true
	if usesSEC {
		s.runningSECTask = taskName
	}
	s.mu.Unlock()

	if err := s.tasks.MarkRunStarted(ctx, taskName); err != nil {
		s.mu.Lock()
		delete(s.runningTasks, taskName)
		if usesSEC && s.runningSECTask == taskName {
			s.runningSECTask = ""
		}
		s.mu.Unlock()
		return err
	}
	var executionID uint
	startedAt := time.Now().UTC()
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("scheduler task %s panicked: %v", taskName, recovered)
			log.Printf("%v", err)
		}
		finishedAt := time.Now().UTC()
		if executionID != 0 {
			if executionErr := s.tasks.FinishExecution(ctx, executionID, finishedAt, err); executionErr != nil {
				log.Printf("persist execution history for scheduler task %s: %v", taskName, executionErr)
			}
		}
		if finishErr := s.tasks.MarkRunOutcome(ctx, taskName, finishedAt, err); finishErr != nil {
			// A persistence error must not be hidden behind an otherwise benign
			// skipped outcome; operators need to know the task state was not saved.
			if err == nil || errors.Is(err, service.ErrTaskSkipped) {
				err = finishErr
			}
		}
		if nextRunErr := s.refreshTaskNextRun(taskName, finishedAt); nextRunErr != nil {
			log.Printf("refresh next run for scheduler task %s: %v", taskName, nextRunErr)
		}
		if errors.Is(err, service.ErrTaskSkipped) {
			// A disabled optional capability is an intentional no-op. Persist the
			// skipped state, but do not report it as a failed manual or cron run.
			err = nil
		}
		s.mu.Lock()
		delete(s.runningTasks, taskName)
		if usesSEC && s.runningSECTask == taskName {
			s.runningSECTask = ""
		}
		s.mu.Unlock()
	}()
	if taskUsesStandaloneDiscoveryLog(taskName) {
		return s.runTask(ctx, taskName)
	}
	execution, executionErr := s.tasks.StartExecution(ctx, taskName, trigger, startedAt)
	if executionErr != nil {
		return fmt.Errorf("start execution history for scheduler task %s: %w", taskName, executionErr)
	}
	executionID = execution.ID
	if taskName == ipoRadarSyncTaskName && trigger == "scheduled" {
		recentManualSuccess, recentErr := s.tasks.HasRecentManualSuccess(ctx, taskName, startedAt.Add(-ipoRecentManualSuccessCooldown))
		if recentErr != nil {
			return fmt.Errorf("check recent manual IPO scan: %w", recentErr)
		}
		if recentManualSuccess {
			return service.SkipTask("最近 10 分钟内已手动完成 IPO 扫描；本轮定时扫描自动跳过，避免重复请求 SEC")
		}
	}
	return s.runTask(ctx, taskName)
}

// Small-cap discovery persists a richer workflow, step and provider trail in
// its own database. Keeping it out of the generic task timeline avoids two
// competing histories for the same run.
func taskUsesStandaloneDiscoveryLog(taskName string) bool {
	return taskName == smallCapDiscoverySyncTaskName || taskName == smallCapDiscoveryFullSyncTaskName
}

func (s *Scheduler) refreshTaskNextRun(taskName string, from time.Time) error {
	if s == nil || s.tasks == nil {
		return nil
	}
	ctx := context.Background()
	task, err := s.tasks.GetByTaskName(ctx, taskName)
	if err != nil {
		return err
	}
	if !task.Enabled || !s.canRunTask(task.TaskName) {
		return s.tasks.SetNextRunAts(ctx, map[string]*time.Time{task.TaskName: nil})
	}
	location, err := s.schedulerLocation(ctx)
	if err != nil {
		return err
	}
	schedule, err := cron.ParseStandard(task.CronExpr)
	if err != nil {
		return err
	}
	nextRunAt := schedule.Next(from.In(location)).UTC()
	return s.tasks.SetNextRunAts(ctx, map[string]*time.Time{task.TaskName: &nextRunAt})
}

// SEC's current-filings and submissions endpoints are shared by these jobs.
// The discovery bulk pipeline uses separately cached archives and remains
// independent so an expensive calibration cannot starve regular filing sync.
func taskUsesLiveSEC(taskName string) bool {
	return taskName == secFilingSyncTaskName || taskName == ipoRadarSyncTaskName || taskName == ipoLifecycleReconcileSyncTaskName || taskName == ipoOfferingReconcileSyncTaskName
}

func (s *Scheduler) canRunTask(taskName string) bool {
	switch taskName {
	case secFilingSyncTaskName:
		return s.filings != nil
	case ipoRadarSyncTaskName, ipoLifecycleReconcileSyncTaskName, ipoOfferingReconcileSyncTaskName, ipoListingReconcileSyncTaskName:
		return s.ipo != nil
	case candidateNotificationSyncTaskName:
		return s.candidateNotifications != nil
	case tradeSetupNotificationSyncTaskName:
		return s.tradeSetupNotifications != nil
	case smallCapDiscoverySyncTaskName, smallCapDiscoveryFullSyncTaskName, watchTargetMarketSyncTaskName, longbridgeCandidateResearchSyncTaskName, longbridgeCandidateValuationSyncTaskName, longbridgeWatchTargetValuationSyncTaskName, longbridgeWatchTargetResearchSyncTaskName, longbridgeCandidateOptionResearchSyncTaskName, longbridgeWatchTargetOptionResearchSyncTaskName:
		return s.discoverySync != nil
	case watchTargetEarningsSyncTaskName:
		return s.earningsPreview != nil
	case notificationRetrySyncTaskName:
		return s.notificationBatches != nil
	case sqliteBackupTaskName:
		return s.backup != nil
	case operationHistoryCleanupTaskName:
		return s.lifecycle != nil
	case operationalHealthNotificationTaskName:
		return s.operationalHealth != nil
	case macroCalendarSyncTaskName:
		return s.macroCalendar != nil
	case marketTrendSyncTaskName:
		return s.marketTrend != nil
	case usFuturesSyncTaskName:
		return s.usFutures != nil
	case institutionalHoldingsSyncTaskName:
		return s.institutionalHoldings != nil
	default:
		return false
	}
}

func (s *Scheduler) runTask(ctx context.Context, taskName string) error {
	switch taskName {
	case secFilingSyncTaskName:
		result, err := s.filings.RefreshWithTrigger(ctx, "scheduler")
		if err != nil {
			return err
		}
		recovery, err := s.filings.RetryRecoverableFailures(ctx, result.SyncRunID)
		if err != nil {
			return err
		}
		if s.discoverySync != nil {
			_, err = s.discoverySync.RefreshDirtyFinancials(ctx)
			if err != nil {
				return err
			}
		}
		unresolved := result.FailedTargets - recovery.TargetsChecked + recovery.FailedTargets + result.DeferredTargets
		if unresolved > 0 {
			return service.PartialTask(fmt.Sprintf("SEC 同步部分完成：%d 个标的等待自动重试或人工处理", unresolved))
		}
		return nil
	case ipoRadarSyncTaskName:
		_, err := s.ipo.RefreshWithTrigger(ctx, "ipo_scheduler")
		return err
	case ipoLifecycleReconcileSyncTaskName:
		result, err := s.ipo.ReconcileLifecycle(ctx)
		if err != nil {
			return err
		}
		if result.Warning != "" {
			return service.PartialTask(result.Warning)
		}
		return nil
	case ipoOfferingReconcileSyncTaskName:
		result, err := s.ipo.ReconcileOfferings(ctx)
		if err != nil {
			return err
		}
		if result.Warning != "" {
			return service.PartialTask(result.Warning)
		}
		return nil
	case ipoListingReconcileSyncTaskName:
		result, err := s.ipo.ReconcileListings(ctx)
		if err != nil {
			return err
		}
		if result.Warning != "" {
			return service.PartialTask(result.Warning)
		}
		return nil
	case candidateNotificationSyncTaskName:
		preview, err := s.candidateNotifications.Preview(ctx)
		if err != nil {
			return err
		}
		if preview.SuppressedReason == "candidate_notification_disabled" {
			return service.SkipTask("候选通知功能未启用")
		}
		_, err = s.candidateNotifications.Send(ctx, service.CandidateNotificationSendInput{Confirm: true})
		return err
	case tradeSetupNotificationSyncTaskName:
		_, err := s.tradeSetupNotifications.Send(ctx, service.TradeSetupNotificationSendInput{Confirm: true})
		return err
	case smallCapDiscoverySyncTaskName:
		_, err := s.discoverySync.RunIncremental(ctx)
		return err
	case smallCapDiscoveryFullSyncTaskName:
		_, err := s.discoverySync.Run(ctx)
		return err
	case longbridgeCandidateResearchSyncTaskName:
		result, err := s.discoverySync.SyncLongbridgeCandidateMarketResearch(ctx)
		if err != nil {
			return err
		}
		if result.Skipped {
			return service.SkipTask(result.Message)
		}
		if result.Failed > 0 || len(result.Warnings) > 0 {
			return service.PartialTask(fmt.Sprintf("Longbridge P1 已更新 %d/%d 个候选；%d 个待重试", result.Fetched, result.Attempted, result.Failed))
		}
		return nil
	case longbridgeCandidateValuationSyncTaskName:
		result, err := s.discoverySync.SyncLongbridgeCandidateValuationResearch(ctx)
		if err != nil {
			return err
		}
		if result.Skipped {
			return service.SkipTask(result.Message)
		}
		if result.Failed > 0 || len(result.Warnings) > 0 {
			return service.PartialTask(fmt.Sprintf("Longbridge P2 已更新 %d/%d 个候选；%d 个待重试", result.Fetched, result.Attempted, result.Failed))
		}
		return nil
	case longbridgeWatchTargetValuationSyncTaskName:
		result, err := s.discoverySync.SyncEnabledWatchTargetValuationResearch(ctx)
		if err != nil {
			return err
		}
		if result.Skipped {
			return service.SkipTask(result.Message)
		}
		if result.Failed > 0 || len(result.Warnings) > 0 {
			return service.PartialTask(fmt.Sprintf("Longbridge 监控标的估值已更新 %d/%d 个；%d 个待重试", result.Fetched, result.Attempted, result.Failed))
		}
		return nil
	case longbridgeWatchTargetResearchSyncTaskName:
		result, err := s.discoverySync.SyncEnabledWatchTargetMarketResearch(ctx)
		if err != nil {
			return err
		}
		if result.Skipped {
			return service.SkipTask(result.Message)
		}
		if result.Failed > 0 || len(result.Warnings) > 0 {
			return service.PartialTask(fmt.Sprintf("Longbridge 监控标的机构持仓已更新 %d/%d 个；%d 个待重试", result.Fetched, result.Attempted, result.Failed))
		}
		return nil
	case longbridgeCandidateOptionResearchSyncTaskName:
		result, err := s.discoverySync.SyncLongbridgeCandidateOptionResearch(ctx)
		if err != nil {
			return err
		}
		if result.Skipped {
			return service.SkipTask(result.Message)
		}
		// Partial coverage is recorded per ticker; only failed requests retry.
		if result.Failed > 0 {
			return service.PartialTask(fmt.Sprintf("Longbridge 候选期权/空头研究已更新 %d/%d 个；%d 个待重试", result.Fetched, result.Attempted, result.Failed))
		}
		return nil
	case longbridgeWatchTargetOptionResearchSyncTaskName:
		result, err := s.discoverySync.SyncEnabledWatchTargetOptionResearch(ctx)
		if err != nil {
			return err
		}
		if result.Skipped {
			return service.SkipTask(result.Message)
		}
		if result.Failed > 0 {
			return service.PartialTask(fmt.Sprintf("Longbridge 监控标的期权/空头研究已更新 %d/%d 个；%d 个待重试", result.Fetched, result.Attempted, result.Failed))
		}
		return nil
	case watchTargetMarketSyncTaskName:
		result, err := s.discoverySync.SyncEnabledWatchTargetMarketPrices(ctx)
		if err != nil {
			return err
		}
		if result.Skipped {
			return service.SkipTask(result.Message)
		}
		if result.RecordCount < result.RequestedCount {
			return service.PartialTask(fmt.Sprintf("监控标的日线仅同步 %d/%d 家；下次任务会继续补齐", result.RecordCount, result.RequestedCount))
		}
		return nil
	case watchTargetEarningsSyncTaskName:
		result, err := s.earningsPreview.SyncEnabled(ctx)
		if err != nil {
			return err
		}
		if result.Skipped {
			return service.SkipTask(result.Message)
		}
		if result.Failed > 0 {
			return service.PartialTask(fmt.Sprintf("财报预告已更新 %d/%d 个标的，%d 个失败", result.Fetched, result.TargetCount, result.Failed))
		}
		if _, candidateErr := s.earningsPreview.SyncCurrentCandidates(ctx); candidateErr != nil {
			return service.PartialTask("监控标的财报预告已更新；小盘候选财报日历待下次重试：" + candidateErr.Error())
		}
		return nil
	case notificationRetrySyncTaskName:
		// The normal delivery ladder handles short failures within hours. A
		// delayed, conservative recovery then requeues only timeouts/limits/5xx
		// dead letters; credential and payload failures remain operator-visible.
		if _, err := s.notificationBatches.RecoverTransientDeadLetters(ctx, time.Now().UTC()); err != nil {
			return err
		}
		_, err := s.notificationBatches.RetryDue(ctx, time.Now().UTC())
		return err
	case sqliteBackupTaskName:
		_, err := s.backup.Backup(ctx)
		return err
	case operationHistoryCleanupTaskName:
		_, err := s.lifecycle.Cleanup(ctx, time.Now().UTC())
		return err
	case operationalHealthNotificationTaskName:
		_, err := s.operationalHealth.Notify(ctx)
		return err
	case macroCalendarSyncTaskName:
		_, err := s.macroCalendar.SyncOfficialBEA(ctx)
		return err
	case institutionalHoldingsSyncTaskName:
		result, err := s.institutionalHoldings.Sync(ctx)
		if err != nil {
			return err
		}
		if result.Skipped {
			return service.SkipTask(result.Message)
		}
		return nil
	case marketTrendSyncTaskName:
		result, err := s.marketTrend.Refresh(ctx)
		if err != nil {
			return err
		}
		if result.SymbolsUpdated == 0 {
			return service.SkipTask("Longbridge 大盘趋势暂无可更新日线")
		}
		if len(result.Warnings) > 0 {
			return service.PartialTask(fmt.Sprintf("大盘趋势已更新 %d/%d 个标的；%d 个待下次重试", result.SymbolsUpdated, result.SymbolsRequested, len(result.Warnings)))
		}
		return nil
	case usFuturesSyncTaskName:
		result, err := s.usFutures.Refresh(ctx)
		if err != nil {
			return err
		}
		if result.SymbolsUpdated == 0 {
			return service.SkipTask("美股期货暂无可更新日线")
		}
		if len(result.Warnings) > 0 {
			return service.PartialTask(fmt.Sprintf("美股期货已更新 %d/%d 个连续合约；%d 个待下次重试", result.SymbolsUpdated, result.SymbolsRequested, len(result.Warnings)))
		}
		return nil
	default:
		return nil
	}
}
