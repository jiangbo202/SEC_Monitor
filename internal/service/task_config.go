package service

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"sec_monitor/internal/model"

	"github.com/robfig/cron/v3"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ErrTaskSkipped marks an intentional no-op. It is not a failed task: for
// example, a scheduled notification job should not add failure noise when
// notifications are deliberately disabled in system settings.
var (
	ErrTaskSkipped        = errors.New("task skipped")
	ErrTaskAlreadyRunning = errors.New("task already running")
	ErrTaskResourceBusy   = errors.New("task resource busy")
)

// TaskBusyError is returned before a task is marked running, so the caller
// can safely retry later without creating a misleading failed run record.
type TaskBusyError struct {
	TaskName     string
	BlockingTask string
}

func (e *TaskBusyError) Error() string {
	if e == nil {
		return ErrTaskAlreadyRunning.Error()
	}
	if strings.TrimSpace(e.BlockingTask) != "" {
		return "SEC 数据源正在被任务 " + strings.TrimSpace(e.BlockingTask) + " 使用，请在其完成后重试"
	}
	return "任务 " + strings.TrimSpace(e.TaskName) + " 正在运行，请稍后刷新状态"
}

func (e *TaskBusyError) Unwrap() error {
	if e != nil && strings.TrimSpace(e.BlockingTask) != "" {
		return ErrTaskResourceBusy
	}
	return ErrTaskAlreadyRunning
}

func TaskAlreadyRunning(taskName string) error {
	return &TaskBusyError{TaskName: strings.TrimSpace(taskName)}
}

func TaskResourceBusy(taskName, blockingTask string) error {
	return &TaskBusyError{TaskName: strings.TrimSpace(taskName), BlockingTask: strings.TrimSpace(blockingTask)}
}

type TaskSkippedError struct {
	Reason string
}

func (e *TaskSkippedError) Error() string {
	if e == nil || strings.TrimSpace(e.Reason) == "" {
		return ErrTaskSkipped.Error()
	}
	return ErrTaskSkipped.Error() + ": " + strings.TrimSpace(e.Reason)
}

func (e *TaskSkippedError) Unwrap() error {
	return ErrTaskSkipped
}

func SkipTask(reason string) error {
	return &TaskSkippedError{Reason: strings.TrimSpace(reason)}
}

// TaskPartialError indicates that a task completed its safe work but left
// individual records pending. It is distinct from a hard failure: the next
// scheduled execution can continue from the recorded per-record state.
type TaskPartialError struct {
	Reason string
}

func (e *TaskPartialError) Error() string {
	if e == nil || strings.TrimSpace(e.Reason) == "" {
		return "task partially completed"
	}
	return "task partially completed: " + strings.TrimSpace(e.Reason)
}

func PartialTask(reason string) error {
	return &TaskPartialError{Reason: strings.TrimSpace(reason)}
}

type TaskConfigService struct {
	db    *gorm.DB
	audit *AuditService
}

type TaskConfigInput struct {
	TaskName string `json:"task_name"`
	CronExpr string `json:"cron_expr"`
	Enabled  bool   `json:"enabled"`
}

type TaskExecutionFilter struct {
	TaskName string
	Status   string
	Trigger  string
	Page     int
	PageSize int
}

func NewTaskConfigService(db *gorm.DB, audit *AuditService) *TaskConfigService {
	return &TaskConfigService{db: db, audit: audit}
}

func (s *TaskConfigService) EnsureDefault(ctx context.Context) error {
	tasks := []model.TaskConfig{
		{TaskName: "candidate_notification_sync", CronExpr: "15 8 * * 2-6", Enabled: false, Running: false},
		{TaskName: "trade_setup_notification_sync", CronExpr: "30 8 * * 2-6", Enabled: false, Running: false},
		// Keep IPO polling offset from the filing task so both SEC consumers
		// remain timely without competing for the same current-filings endpoint.
		{TaskName: "ipo_radar_sync", CronExpr: "5,35 * * * *", Enabled: true, Running: false},
		// IPO compensation stages are intentionally isolated from the primary
		// current-filings scan. This keeps new-filing detection fast while the
		// heavier CIK backfill, 424B4 parsing, and Longbridge verification each
		// retain their own execution history and retry boundary.
		{TaskName: "ipo_lifecycle_reconcile_sync", CronExpr: "15 */2 * * *", Enabled: true, Running: false},
		{TaskName: "ipo_offering_reconcile_sync", CronExpr: "45 1-23/2 * * *", Enabled: true, Running: false},
		{TaskName: "ipo_listing_reconcile_sync", CronExpr: "20 * * * *", Enabled: true, Running: false},
		{TaskName: "notification_retry_sync", CronExpr: "*/10 * * * *", Enabled: true, Running: false},
		// Candidate prices run first. The later watch-target job reuses their
		// local PriceSnapshot rows and requests only symbols outside that set.
		{TaskName: "small_cap_discovery_sync", CronExpr: "5 5 * * 2-6", Enabled: true, Running: false},
		// P1 and P2 call distinct Longbridge endpoint families. They run after
		// the prior US session has settled, with a gap so one provider delay does
		// not consume the other job's early-morning data window.
		{TaskName: "longbridge_candidate_research_sync", CronExpr: "15 7 * * 2-6", Enabled: true, Running: false},
		{TaskName: "longbridge_candidate_valuation_sync", CronExpr: "45 7 * * 2-6", Enabled: true, Running: false},
		// Keep watch-target valuation refresh independent from candidate P2. It
		// runs after both the price history and candidate P2 windows have had a
		// chance to finish, so either job can be retried without blocking the other.
		{TaskName: "longbridge_watch_target_valuation_sync", CronExpr: "15 8 * * 2-6", Enabled: true, Running: false},
		// P1 shareholder/fund-holder snapshots are separate from P2 valuation
		// and run after it, so either provider family can fail or retry alone.
		{TaskName: "longbridge_watch_target_research_sync", CronExpr: "45 8 * * 2-6", Enabled: true, Running: false},
		// Option volume and short-interest are a separate endpoint family. Keep
		// them after P1/P2 and use distinct candidate/watch-target budgets.
		{TaskName: "longbridge_candidate_option_research_sync", CronExpr: "15 9 * * 2-6", Enabled: true, Running: false},
		{TaskName: "longbridge_watch_target_option_research_sync", CronExpr: "45 9 * * 2-6", Enabled: true, Running: false},
		// Asia/Shanghai default: Tuesday-Saturday 05:35 is after the prior
		// US regular session close in both daylight-saving and standard time.
		// The task itself still resolves the latest completed NYSE trading day.
		{TaskName: "watch_target_market_sync", CronExpr: "35 5 * * 2-6", Enabled: true, Running: false},
		// Asia/Shanghai Tuesday-Saturday 06:30 is after the prior US close.
		// The task only updates locally cached earnings dates and estimates; it
		// does not run SEC filing or market-price synchronization.
		{TaskName: "watch_target_earnings_sync", CronExpr: "30 6 * * 2-6", Enabled: true, Running: false},
		// Full calibration is deliberately separate from the daily incremental
		// task. Enable it after choosing a quiet weekly window in the Scheduler
		// page; it downloads/parses the complete SEC archives.
		{TaskName: "small_cap_discovery_full_sync", CronExpr: "30 9 * * 6", Enabled: true, Running: false},
		// Backup is deliberately before the early-morning research window. It
		// must not compete with institutional-holdings or the Saturday full scan
		// for SQLite write locks and disk IO.
		{TaskName: "sqlite_backup", CronExpr: "15 3 * * *", Enabled: true, Running: false},
		// This cleanup removes only completed execution/diagnostic history. It
		// never deletes filings, candidates, price history, or research batches.
		{TaskName: "operation_history_cleanup", CronExpr: "45 9 * * 0", Enabled: true, Running: false},
		// BEA's calendar and releases are public and do not need a token. The
		// first run records that day's pre-release schedule; the second runs
		// after the common 08:30 ET BLS/BEA release window to capture official
		// actuals. Other release times remain visible in the calendar and are
		// captured by the next regular refresh or a manual refresh.
		// Already captured releases are skipped, so ordinary days stay light.
		{TaskName: "macro_calendar_sync", CronExpr: "45 20,21 * * 1-5", Enabled: true, Running: false},
		// 13F parsing can be IO-heavy. Keep it after the candidate/watch-target
		// research sequence and well away from the paired SQLite backup.
		{TaskName: "institutional_holdings_sync", CronExpr: "15 10 * * 1-5", Enabled: true, Running: false},
		// Asia/Shanghai Tuesday-Saturday 05:45 runs after the previous US
		// session has closed and saves the latest completed daily bars.
		{TaskName: "market_trend_sync", CronExpr: "45 5 * * 2-6", Enabled: true, Running: false},
		// The public fallback can be rate limited. Keep this disabled until a
		// licensed/approved US futures provider is configured.
		{TaskName: "us_futures_sync", CronExpr: "5 6 * * 2-6", Enabled: false, Running: false},
		// Disabled by default: when enabled it sends a deduplicated Telegram
		// summary only if the locally recorded operational report has issues.
		// The scheduler's global timezone is authoritative; do not use a
		// per-expression CRON_TZ prefix, which the task editor intentionally
		// rejects to keep future timezone changes coherent.
		{TaskName: "operational_health_notification_sync", CronExpr: "0 11 * * *", Enabled: false, Running: false},
		{TaskName: "sec_filing_sync", CronExpr: "*/10 * * * *", Enabled: true, Running: false},
	}
	if err := s.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&tasks).Error; err != nil {
		return err
	}
	return s.reconcileDefaultScheduleUpgrades(ctx)
}

// reconcileDefaultScheduleUpgrades fixes only known historical defaults. A
// value is changed when it exactly matches an old shipped expression, so a
// user-customized cron is never overwritten during startup.
func (s *TaskConfigService) reconcileDefaultScheduleUpgrades(ctx context.Context) error {
	upgrades := []struct{ taskName, oldExpr, newExpr string }{
		{"sqlite_backup", "15 9 * * *", "15 3 * * *"},
		{"institutional_holdings_sync", "15 9 * * 1-5", "15 10 * * 1-5"},
		{"operational_health_notification_sync", "CRON_TZ=Asia/Shanghai 15 10 * * *", "0 11 * * *"},
	}
	for _, upgrade := range upgrades {
		if err := s.db.WithContext(ctx).Model(&model.TaskConfig{}).
			Where("task_name = ? AND cron_expr = ?", upgrade.taskName, upgrade.oldExpr).
			Updates(map[string]any{"cron_expr": upgrade.newExpr}).Error; err != nil {
			return err
		}
	}
	return nil
}

func (s *TaskConfigService) List(ctx context.Context) ([]model.TaskConfig, error) {
	var tasks []model.TaskConfig
	err := s.db.WithContext(ctx).Order("task_name ASC").Find(&tasks).Error
	return tasks, err
}

func (s *TaskConfigService) Get(ctx context.Context, id uint) (model.TaskConfig, error) {
	var task model.TaskConfig
	err := s.db.WithContext(ctx).First(&task, id).Error
	return task, mapNotFound(err)
}

func (s *TaskConfigService) GetByTaskName(ctx context.Context, taskName string) (model.TaskConfig, error) {
	var task model.TaskConfig
	err := s.db.WithContext(ctx).Where("task_name = ?", strings.TrimSpace(taskName)).First(&task).Error
	return task, mapNotFound(err)
}

// SetNextRunAts stores the scheduler's current execution plan. A nil value is
// intentional: it means the task is disabled or unavailable in this process,
// so the UI must not present a stale next-run time as a promise.
func (s *TaskConfigService) SetNextRunAts(ctx context.Context, nextRuns map[string]*time.Time) error {
	if len(nextRuns) == 0 {
		return nil
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for taskName, nextRunAt := range nextRuns {
			if err := tx.Model(&model.TaskConfig{}).
				Where("task_name = ?", strings.TrimSpace(taskName)).
				Updates(map[string]any{"next_run_at": nextRunAt}).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *TaskConfigService) Update(ctx context.Context, id uint, input TaskConfigInput, operator string) (model.TaskConfig, error) {
	if err := validateTaskCronExpr(input.CronExpr); err != nil {
		return model.TaskConfig{}, err
	}
	var updated model.TaskConfig
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var before model.TaskConfig
		if err := tx.First(&before, id).Error; err != nil {
			return mapNotFound(err)
		}
		if err := tx.Model(&before).Updates(map[string]any{
			"cron_expr": input.CronExpr,
			"enabled":   input.Enabled,
		}).Error; err != nil {
			return err
		}
		if err := tx.First(&updated, id).Error; err != nil {
			return err
		}
		return NewAuditService(tx).Record(ctx, operator, "update", "task_config", strconv.FormatUint(uint64(id), 10), before, updated)
	})
	return updated, err
}

// Task cron expressions deliberately use the scheduler's global timezone.
// Per-expression CRON_TZ/TZ prefixes make the settings page misleading and
// would silently ignore a future global timezone change.
func validateTaskCronExpr(value string) error {
	expr := strings.TrimSpace(value)
	if expr == "" || strings.HasPrefix(expr, "CRON_TZ=") || strings.HasPrefix(expr, "TZ=") {
		return ErrValidation
	}
	if _, err := cron.ParseStandard(expr); err != nil {
		return ErrValidation
	}
	return nil
}

func (s *TaskConfigService) MarkRunStarted(ctx context.Context, taskName string) error {
	now := time.Now().UTC()
	return s.db.WithContext(ctx).Model(&model.TaskConfig{}).
		Where("task_name = ?", taskName).
		Updates(map[string]any{"running": true, "running_since": &now, "last_status": "running"}).Error
}

// StartExecution creates an immutable execution-history row before task work
// starts. The latest-status fields on TaskConfig remain a compact operational
// summary; this table is the historical record used by the UI.
func (s *TaskConfigService) StartExecution(ctx context.Context, taskName, trigger string, startedAt time.Time) (model.TaskExecution, error) {
	execution := model.TaskExecution{
		TaskName:  strings.TrimSpace(taskName),
		Trigger:   normalizedTaskTrigger(trigger),
		Status:    "running",
		StartedAt: startedAt.UTC(),
		Summary:   "任务正在执行",
	}
	if err := s.db.WithContext(ctx).Create(&execution).Error; err != nil {
		return model.TaskExecution{}, err
	}
	return execution, nil
}

// FinishExecution records the final scheduler outcome without changing the
// task's business result. It mirrors TaskConfig's result vocabulary so the
// two pages cannot disagree about a run's state.
func (s *TaskConfigService) FinishExecution(ctx context.Context, id uint, finishedAt time.Time, runErr error) error {
	status, summary, errorMessage := taskExecutionOutcome(runErr)
	finishedAt = finishedAt.UTC()
	updates := map[string]any{
		"status":        status,
		"finished_at":   &finishedAt,
		"summary":       summary,
		"error_message": errorMessage,
		"duration_ms":   gorm.Expr("CASE WHEN started_at <= ? THEN CAST((julianday(?) - julianday(started_at)) * 86400000 AS INTEGER) ELSE 0 END", finishedAt, finishedAt),
	}
	return s.db.WithContext(ctx).Model(&model.TaskExecution{}).Where("id = ?", id).Updates(updates).Error
}

func (s *TaskConfigService) ListExecutions(ctx context.Context, filter TaskExecutionFilter) (PageResult[model.TaskExecution], error) {
	page, pageSize := normalizePage(filter.Page, filter.PageSize)
	query := s.db.WithContext(ctx).Model(&model.TaskExecution{})
	if taskName := strings.TrimSpace(filter.TaskName); taskName != "" {
		query = query.Where("task_name = ?", taskName)
	}
	if status := strings.TrimSpace(filter.Status); status != "" {
		query = query.Where("status = ?", status)
	}
	if trigger := strings.TrimSpace(filter.Trigger); trigger != "" {
		query = query.Where("trigger = ?", trigger)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return PageResult[model.TaskExecution]{}, err
	}
	var executions []model.TaskExecution
	err := query.Order("started_at DESC, id DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&executions).Error
	return newPageResult(executions, total, page, pageSize), err
}

// HasRecentManualSuccess lets scheduled jobs avoid immediately repeating an
// expensive upstream request which a user has just completed manually. This is
// especially important for SEC's current-filings feed, whose anti-abuse layer
// can temporarily time out after duplicate back-to-back scans.
func (s *TaskConfigService) HasRecentManualSuccess(ctx context.Context, taskName string, since time.Time) (bool, error) {
	if s == nil || s.db == nil {
		return false, errors.New("task config service is not configured")
	}
	var count int64
	err := s.db.WithContext(ctx).Model(&model.TaskExecution{}).
		Where("task_name = ? AND trigger = ? AND status = ? AND finished_at IS NOT NULL AND finished_at >= ?", strings.TrimSpace(taskName), "manual", "success", since.UTC()).
		Count(&count).Error
	return count > 0, err
}

// RecoverInterruptedExecutions closes durable running rows left by a process
// restart. The scheduler cannot resume work across a restart, so presenting a
// completed-looking success here would be misleading.
func (s *TaskConfigService) RecoverInterruptedExecutions(ctx context.Context, finishedAt time.Time) (int64, error) {
	finishedAt = finishedAt.UTC()
	result := s.db.WithContext(ctx).Model(&model.TaskExecution{}).Where("status = ?", "running").Updates(map[string]any{
		"status":        "interrupted",
		"finished_at":   &finishedAt,
		"summary":       "服务在任务执行期间重启；任务未在本进程中继续运行",
		"error_message": "服务在任务执行期间重启；任务未在本进程中继续运行",
		"duration_ms":   gorm.Expr("CASE WHEN started_at <= ? THEN CAST((julianday(?) - julianday(started_at)) * 86400000 AS INTEGER) ELSE 0 END", finishedAt, finishedAt),
	})
	return result.RowsAffected, result.Error
}

func normalizedTaskTrigger(value string) string {
	if strings.EqualFold(strings.TrimSpace(value), "scheduled") {
		return "scheduled"
	}
	return "manual"
}

func taskExecutionOutcome(runErr error) (status, summary, errorMessage string) {
	var skipped *TaskSkippedError
	var partial *TaskPartialError
	switch {
	case errors.As(runErr, &skipped):
		return "skipped", taskExecutionValueOrDefault(strings.TrimSpace(skipped.Reason), "任务按当前配置跳过"), ""
	case errors.As(runErr, &partial):
		message := SanitizeSensitiveError(partial.Reason)
		return "partial", taskExecutionValueOrDefault(message, "任务部分完成"), message
	case runErr == nil:
		return "success", "任务执行完成", ""
	default:
		message := SanitizeSensitiveError(runErr.Error())
		return "failed", "任务执行失败", message
	}
}

func taskExecutionValueOrDefault(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}

func (s *TaskConfigService) MarkRunFinished(ctx context.Context, taskName string, ranAt time.Time) error {
	return s.MarkRunOutcome(ctx, taskName, ranAt, nil)
}

// MarkRunOutcome persists both successful and failed job outcomes. This is
// deliberately separate from scheduler process logs so an operator can see a
// repeated failure after a container restart.
func (s *TaskConfigService) MarkRunOutcome(ctx context.Context, taskName string, ranAt time.Time, runErr error) error {
	updates := map[string]any{
		"last_run_at":   ranAt,
		"running":       false,
		"running_since": nil,
	}
	var skipped *TaskSkippedError
	var partial *TaskPartialError
	if errors.As(runErr, &skipped) {
		updates["last_status"] = "skipped"
		updates["last_error_message"] = strings.TrimSpace(skipped.Reason)
		updates["consecutive_failures"] = 0
	} else if errors.As(runErr, &partial) {
		updates["last_status"] = "partial"
		updates["last_error_message"] = SanitizeSensitiveError(partial.Reason)
		updates["consecutive_failures"] = gorm.Expr("consecutive_failures + 1")
	} else if runErr == nil {
		updates["last_status"] = "success"
		updates["last_error_message"] = ""
		updates["consecutive_failures"] = 0
	} else {
		updates["last_status"] = "failed"
		updates["last_error_message"] = SanitizeSensitiveError(runErr.Error())
		updates["consecutive_failures"] = gorm.Expr("consecutive_failures + 1")
	}
	return s.db.WithContext(ctx).Model(&model.TaskConfig{}).
		Where("task_name = ?", taskName).
		Updates(updates).Error
}

// RecoverInterrupted clears the presentation-only running flag after a
// process restart. Scheduled jobs are process-local, therefore a task marked
// running in a freshly started process cannot still be executing here.
func (s *TaskConfigService) RecoverInterrupted(ctx context.Context) (int64, error) {
	result := s.db.WithContext(ctx).Model(&model.TaskConfig{}).Where("running = ?", true).Updates(map[string]any{
		"running":            false,
		"running_since":      nil,
		"last_status":        "interrupted",
		"last_error_message": "服务在任务执行期间重启；任务未在本进程中继续运行",
	})
	return result.RowsAffected, result.Error
}
