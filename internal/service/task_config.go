package service

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"sec_monitor/internal/model"

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

func NewTaskConfigService(db *gorm.DB, audit *AuditService) *TaskConfigService {
	return &TaskConfigService{db: db, audit: audit}
}

func (s *TaskConfigService) EnsureDefault(ctx context.Context) error {
	tasks := []model.TaskConfig{
		{TaskName: "candidate_notification_sync", CronExpr: "30 9 * * *", Enabled: false, Running: false},
		{TaskName: "trade_setup_notification_sync", CronExpr: "30 17 * * 1-5", Enabled: false, Running: false},
		{TaskName: "ipo_radar_sync", CronExpr: "*/30 * * * *", Enabled: true, Running: false},
		{TaskName: "notification_retry_sync", CronExpr: "*/10 * * * *", Enabled: true, Running: false},
		{TaskName: "small_cap_discovery_sync", CronExpr: "0 8 * * 1-5", Enabled: false, Running: false},
		// Asia/Shanghai default: Tuesday-Saturday 05:30 is after the prior
		// US regular session close in both daylight-saving and standard time.
		// The task itself still resolves the latest completed NYSE trading day.
		{TaskName: "watch_target_market_sync", CronExpr: "30 5 * * 2-6", Enabled: true, Running: false},
		// Asia/Shanghai Tuesday-Saturday 06:30 is after the prior US close.
		// The task only updates locally cached earnings dates and estimates; it
		// does not run SEC filing or market-price synchronization.
		{TaskName: "watch_target_earnings_sync", CronExpr: "30 6 * * 2-6", Enabled: true, Running: false},
		// Full calibration is deliberately separate from the daily incremental
		// task. Enable it after choosing a quiet weekly window in the Scheduler
		// page; it downloads/parses the complete SEC archives.
		{TaskName: "small_cap_discovery_full_sync", CronExpr: "0 8 * * 6", Enabled: false, Running: false},
		{TaskName: "sqlite_backup", CronExpr: "15 3 * * *", Enabled: true, Running: false},
		// This cleanup removes only completed execution/diagnostic history. It
		// never deletes filings, candidates, price history, or research batches.
		{TaskName: "operation_history_cleanup", CronExpr: "45 3 * * 0", Enabled: true, Running: false},
		// BEA's calendar and releases are public and do not need a token. The
		// first run records that day's pre-release schedule; the second runs
		// after the common 08:30 ET BLS/BEA release window to capture official
		// actuals. Other release times remain visible in the calendar and are
		// captured by the next regular refresh or a manual refresh.
		// Already captured releases are skipped, so ordinary days stay light.
		{TaskName: "macro_calendar_sync", CronExpr: "15 20,22 * * 1-5", Enabled: true, Running: false},
		// Disabled by default: when enabled it sends a deduplicated Telegram
		// summary only if the locally recorded operational report has issues.
		{TaskName: "operational_health_notification_sync", CronExpr: "15 9 * * *", Enabled: false, Running: false},
		{TaskName: "sec_filing_sync", CronExpr: "*/5 * * * *", Enabled: true, Running: false},
	}
	return s.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&tasks).Error
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

func (s *TaskConfigService) MarkRunStarted(ctx context.Context, taskName string) error {
	now := time.Now().UTC()
	return s.db.WithContext(ctx).Model(&model.TaskConfig{}).
		Where("task_name = ?", taskName).
		Updates(map[string]any{"running": true, "running_since": &now, "last_status": "running"}).Error
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
