package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"sec_monitor/internal/discovery"
	"sec_monitor/internal/model"
	"sec_monitor/internal/telegram"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	operationalAlertRepeatAfter      = 12 * time.Hour
	operationalSECSlowTargetLimit    = 2 * time.Minute
	operationalSlowObservationWindow = 48 * time.Hour
)

// OperationalIssue is an actionable, locally computed observation. Routes are
// frontend route names, keeping the API independent from UI URL details.
type OperationalIssue struct {
	Key        string    `json:"key"`
	Category   string    `json:"category"`
	Severity   string    `json:"severity"`
	Title      string    `json:"title"`
	Detail     string    `json:"detail"`
	Action     string    `json:"action"`
	ObservedAt time.Time `json:"observed_at"`
}

type OperationalTaskStatus struct {
	TaskName            string     `json:"task_name"`
	Enabled             bool       `json:"enabled"`
	Running             bool       `json:"running"`
	LastStatus          string     `json:"last_status"`
	LastRunAt           *time.Time `json:"last_run_at,omitempty"`
	NextRunAt           *time.Time `json:"next_run_at,omitempty"`
	RunningSince        *time.Time `json:"running_since,omitempty"`
	ConsecutiveFailures int        `json:"consecutive_failures"`
	ExpectedWithinMins  int        `json:"expected_within_mins"`
}

type OperationalReport struct {
	GeneratedAt            time.Time               `json:"generated_at"`
	Status                 string                  `json:"status"`
	Issues                 []OperationalIssue      `json:"issues"`
	Tasks                  []OperationalTaskStatus `json:"tasks"`
	RetryableTargets       int64                   `json:"retryable_targets"`
	DeferredTargets        int64                   `json:"deferred_targets"`
	CompanyProfileRetryDue int64                   `json:"company_profile_retry_due"`
	MarketPriceRecovery    int                     `json:"market_price_recovery"`
	LowCoverageProviders   int64                   `json:"low_coverage_providers"`
	SlowSECTargets         int64                   `json:"slow_sec_targets"`
	SlowDiscoverySteps     int                     `json:"slow_discovery_steps"`
	ProviderWarnings       int64                   `json:"provider_warnings"`
	Summary                string                  `json:"summary"`
}

type OperationalAlertResult struct {
	Report     OperationalReport `json:"report"`
	Sent       bool              `json:"sent"`
	Suppressed bool              `json:"suppressed"`
	Reason     string            `json:"reason,omitempty"`
}

type OperationalHealthService struct {
	db          *gorm.DB
	discoveryDB *gorm.DB
	notifier    telegram.Notifier
	configs     *ConfigService
	backup      *SQLiteBackupService
}

func NewOperationalHealthService(db, discoveryDB *gorm.DB, notifier telegram.Notifier, configs *ConfigService) *OperationalHealthService {
	return &OperationalHealthService{db: db, discoveryDB: discoveryDB, notifier: notifier, configs: configs}
}

func (s *OperationalHealthService) WithBackup(backup *SQLiteBackupService) *OperationalHealthService {
	if s != nil {
		s.backup = backup
	}
	return s
}

func (s *OperationalHealthService) Report(ctx context.Context) (OperationalReport, error) {
	return s.ReportAt(ctx, time.Now().UTC())
}

func (s *OperationalHealthService) ReportAt(ctx context.Context, now time.Time) (OperationalReport, error) {
	report := OperationalReport{GeneratedAt: now.UTC(), Status: "ok", Issues: []OperationalIssue{}, Tasks: []OperationalTaskStatus{}}
	if s == nil || s.db == nil {
		return report, errors.New("operational health service is not configured")
	}
	var tasks []model.TaskConfig
	if err := s.db.WithContext(ctx).Order("task_name ASC").Find(&tasks).Error; err != nil {
		return report, err
	}
	for _, task := range tasks {
		expected := taskExpectedWithin(task.TaskName)
		report.Tasks = append(report.Tasks, OperationalTaskStatus{
			TaskName: task.TaskName, Enabled: task.Enabled, Running: task.Running, LastStatus: task.LastStatus, LastRunAt: task.LastRunAt, NextRunAt: task.NextRunAt,
			RunningSince: task.RunningSince, ConsecutiveFailures: task.ConsecutiveFailures, ExpectedWithinMins: int(expected.Minutes()),
		})
		if !task.Enabled {
			continue
		}
		if task.Running && task.RunningSince != nil && now.Sub(*task.RunningSince) > 2*expected {
			report.addIssue("task_stuck:"+task.TaskName, "task", "critical", "任务执行时间异常", fmt.Sprintf("%s 已运行 %s，超过预期 %s", task.TaskName, formatOperationalDuration(now.Sub(*task.RunningSince)), formatOperationalDuration(expected)), "scheduler", now)
			continue
		}
		switch task.LastStatus {
		case "failed", "interrupted":
			severity := "warning"
			if task.ConsecutiveFailures >= 3 {
				severity = "critical"
			}
			detail := "最近一次执行失败"
			if message := strings.TrimSpace(SanitizeSensitiveError(task.LastErrorMessage)); message != "" {
				detail += "：" + truncateOperationalText(message, 240)
			}
			report.addIssue("task_failed:"+task.TaskName, "task", severity, "调度任务失败", task.TaskName+" · "+detail, "scheduler", now)
		case "partial":
			report.addIssue("task_partial:"+task.TaskName, "task", "warning", "调度任务部分完成", task.TaskName+" 仍有待自动重试或人工处理的记录", "scheduler", now)
		}
		if !task.Running && task.LastRunAt != nil && now.Sub(*task.LastRunAt) > 2*expected {
			report.addIssue("task_stale:"+task.TaskName, "task", "warning", "调度任务长时间未更新", fmt.Sprintf("%s 距上次完成已 %s（预期不超过 %s）", task.TaskName, formatOperationalDuration(now.Sub(*task.LastRunAt)), formatOperationalDuration(expected)), "scheduler", now)
		}
	}

	if err := s.db.WithContext(ctx).Model(&model.SyncRunDetail{}).Where("status = ? AND retryable = ? AND next_retry_at IS NOT NULL AND next_retry_at <= ?", "failed", true, now).Count(&report.RetryableTargets).Error; err != nil {
		return report, err
	}
	if report.RetryableTargets > 0 {
		report.addIssue("sec_retry_queue", "sec", "warning", "SEC 自动重试队列待处理", fmt.Sprintf("%d 个标的已到自动重试时间；下一次 SEC 调度会继续处理", report.RetryableTargets), "sync-runs", now)
	}
	if err := s.db.WithContext(ctx).Model(&model.SyncRunDetail{}).Where("status = ?", "deferred").Count(&report.DeferredTargets).Error; err != nil {
		return report, err
	}
	if report.DeferredTargets > 0 {
		report.addIssue("sec_deferred_targets", "sec", "warning", "SEC 标的暂缓同步", fmt.Sprintf("%d 个标的因 404、基金身份或配置问题暂缓；可在同步历史手动重试", report.DeferredTargets), "sync-runs", now)
	}
	if err := s.reportSlowSECDetails(ctx, &report, now); err != nil {
		return report, err
	}

	if s.discoveryDB != nil {
		if err := s.reportSlowDiscoverySteps(ctx, &report, now); err != nil {
			return report, err
		}
		var providers []discovery.ProviderHealth
		if err := s.discoveryDB.WithContext(ctx).Order("provider ASC").Find(&providers).Error; err != nil {
			return report, err
		}
		for _, provider := range providers {
			if provider.Status == discovery.ProviderStatusActive && provider.FailureStreak == 0 {
				// Active sources can still have a bad latest coverage result, so
				// continue only after checking their persisted latest run below.
				var latest discovery.ProviderRun
				if err := s.discoveryDB.WithContext(ctx).Where("provider = ?", provider.Provider).Order("created_at DESC, id DESC").First(&latest).Error; err == nil && latest.ExpectedCount > 0 && latest.CoveragePct < 20 {
					report.LowCoverageProviders++
					severity := "warning"
					if latest.CoveragePct < 5 {
						severity = "critical"
					}
					kind, recommendation := classifyOperationalExternalFailure(latest.ErrorMessage)
					detail := fmt.Sprintf("%s 最近覆盖率 %.1f%%（发布阈值 20%%）", provider.Provider, latest.CoveragePct)
					if kind != "unknown" {
						detail += "；失败类型：" + kind
					}
					if recommendation != "" {
						detail += "；" + recommendation
					}
					report.addIssue("provider_coverage:"+provider.Provider, "market", severity, "行情覆盖率过低", detail, "discovery-logs", now)
				}
				continue
			}
			report.ProviderWarnings++
			severity := "warning"
			if provider.Status == discovery.ProviderStatusFailed || provider.FailureStreak >= 3 {
				severity = "critical"
			}
			report.addIssue("provider:"+provider.Provider, "market", severity, "行情数据源需要关注", fmt.Sprintf("%s：状态 %s，连续失败 %d 次，最近交易日 %s", provider.Provider, provider.Status, provider.FailureStreak, valueOrDash(provider.LastTradeDate)), "discovery-logs", now)
		}
		var dueProfiles int64
		if err := s.discoveryDB.WithContext(ctx).Model(&discovery.CompanyProfileSnapshot{}).
			Where("provider = ? AND last_error <> '' AND (next_retry_at IS NULL OR next_retry_at <= ?)", "longbridge", now).
			Count(&dueProfiles).Error; err != nil {
			return report, err
		}
		report.CompanyProfileRetryDue = dueProfiles
		if dueProfiles > 0 {
			report.addIssue("company_profile_retry_due", "longbridge", "warning", "Longbridge 公司资料待补偿", fmt.Sprintf("%d 个公司资料请求已到重试时间；下一次小盘同步会优先补偿，也可在小盘发现日志单独重试", dueProfiles), "discovery-logs", now)
		}
		queue, err := discovery.ListCurrentCandidateMarketPriceRecoveryQueue(ctx, s.discoveryDB)
		if err != nil {
			return report, err
		}
		report.MarketPriceRecovery = len(queue.Items)
		if report.MarketPriceRecovery > 0 {
			report.addIssue("market_price_recovery", "market", "warning", "候选行情需要补偿", fmt.Sprintf("%d 个当前 A/B 候选缺价、过期或使用本地回退价；可在小盘发现日志按标的补齐并重算", report.MarketPriceRecovery), "discovery-logs", now)
		}
	}
	if s.backup != nil {
		if backup, err := s.backup.Health(ctx); err != nil {
			report.addIssue("backup_status_unavailable", "backup", "warning", "SQLite 备份状态不可读", SanitizeSensitiveError(err.Error()), "system-health", now)
		} else if backup.LatestCompleted == nil {
			report.addIssue("backup_missing", "backup", "critical", "尚无完整 SQLite 备份", "请运行 sqlite_backup；恢复需要同一时间戳的 sec_monitor 与 small_cap 快照对", "scheduler", now)
		} else {
			if now.Sub(*backup.LatestCompleted) > 30*time.Hour {
				report.addIssue("backup_stale", "backup", "warning", "SQLite 备份已过期", fmt.Sprintf("最近完整备份距今 %s", formatOperationalDuration(now.Sub(*backup.LatestCompleted))), "scheduler", now)
			}
			if backup.IncompletePairs > 0 {
				report.addIssue("backup_incomplete", "backup", "warning", "发现不完整 SQLite 备份", fmt.Sprintf("%d 组不完整快照不会被作为恢复点", backup.IncompletePairs), "system-health", now)
			}
		}
		if drill, err := s.backup.LatestRecoveryDrill(ctx); err != nil {
			report.addIssue("recovery_drill_status_unavailable", "backup", "warning", "恢复演练记录不可读", SanitizeSensitiveError(err.Error()), "system-health", now)
		} else if drill.ID == 0 {
			report.addIssue("recovery_drill_missing", "backup", "warning", "尚未执行恢复演练", "请在系统健康页执行一次只读恢复演练，确认备份可以独立打开", "system-health", now)
		} else if drill.Status != "ready" {
			report.addIssue("recovery_drill_failed", "backup", "critical", "最近恢复演练失败", truncateOperationalText(SanitizeSensitiveError(drill.ErrorMessage), 240), "system-health", now)
		} else if now.Sub(drill.StartedAt) > 8*24*time.Hour {
			report.addIssue("recovery_drill_stale", "backup", "warning", "恢复演练已过期", fmt.Sprintf("最近一次成功演练距今 %s", formatOperationalDuration(now.Sub(drill.StartedAt))), "system-health", now)
		}
	}
	sort.SliceStable(report.Issues, func(i, j int) bool {
		return operationalSeverityRank(report.Issues[i].Severity) > operationalSeverityRank(report.Issues[j].Severity)
	})
	report.Status = operationalReportStatus(report.Issues)
	report.Summary = renderOperationalSummary(report)
	return report, nil
}

func (s *OperationalHealthService) reportSlowSECDetails(ctx context.Context, report *OperationalReport, now time.Time) error {
	if s == nil || s.db == nil || report == nil {
		return nil
	}
	var details []model.SyncRunDetail
	if err := s.db.WithContext(ctx).
		Where("finished_at IS NOT NULL AND finished_at >= ? AND duration_ms > ?", now.Add(-operationalSlowObservationWindow), operationalSECSlowTargetLimit.Milliseconds()).
		Order("duration_ms DESC, id DESC").
		Limit(5).
		Find(&details).Error; err != nil {
		return err
	}
	if len(details) == 0 {
		return nil
	}
	var count int64
	if err := s.db.WithContext(ctx).Model(&model.SyncRunDetail{}).
		Where("finished_at IS NOT NULL AND finished_at >= ? AND duration_ms > ?", now.Add(-operationalSlowObservationWindow), operationalSECSlowTargetLimit.Milliseconds()).
		Count(&count).Error; err != nil {
		return err
	}
	report.SlowSECTargets = count
	longest := details[0]
	report.addIssue("sec_slow_targets", "sec", "warning", "SEC 单标的同步较慢", fmt.Sprintf("最近 48 小时有 %d 个标的耗时超过 %s；最慢为 %s（%s）。可在同步历史查看请求重试与错误详情", count, formatOperationalDuration(operationalSECSlowTargetLimit), valueOrDash(longest.Ticker), formatOperationalDuration(time.Duration(longest.DurationMS)*time.Millisecond)), "sync-runs", now)
	return nil
}

func (s *OperationalHealthService) reportSlowDiscoverySteps(ctx context.Context, report *OperationalReport, now time.Time) error {
	if s == nil || s.discoveryDB == nil || report == nil {
		return nil
	}
	var steps []discovery.DiscoverySyncStep
	if err := s.discoveryDB.WithContext(ctx).
		Where("started_at >= ?", now.Add(-operationalSlowObservationWindow)).
		Order("started_at DESC, id DESC").
		Limit(500).
		Find(&steps).Error; err != nil {
		return err
	}
	slow := make([]discovery.DiscoverySyncStep, 0)
	var longestDuration time.Duration
	var longest discovery.DiscoverySyncStep
	hasRunning := false
	for _, step := range steps {
		endedAt := now
		if step.CompletedAt != nil {
			endedAt = *step.CompletedAt
		}
		duration := endedAt.Sub(step.StartedAt)
		if duration <= slowDiscoveryStepThreshold(step.Phase) {
			continue
		}
		if step.CompletedAt == nil {
			hasRunning = true
		}
		slow = append(slow, step)
		if duration > longestDuration {
			longest, longestDuration = step, duration
		}
	}
	if len(slow) == 0 {
		return nil
	}
	report.SlowDiscoverySteps = len(slow)
	severity := "warning"
	title := "小盘工作流步骤较慢"
	if hasRunning {
		severity, title = "critical", "小盘工作流步骤长时间运行"
	}
	report.addIssue("discovery_slow_steps", "discovery", severity, title, fmt.Sprintf("最近 48 小时有 %d 个步骤超过对应阈值；最慢为 %s（%s，耗时 %s）。可在小盘发现日志查看阶段进度和 Provider 回退", len(slow), longest.Phase, longest.Status, formatOperationalDuration(longestDuration)), "discovery-logs", now)
	return nil
}

func slowDiscoveryStepThreshold(phase string) time.Duration {
	switch strings.TrimSpace(phase) {
	case "prepare", "build_sources", "publish_summary":
		return 5 * time.Minute
	case "incremental_sec_refresh":
		return 20 * time.Minute
	case "technical_history":
		return 30 * time.Minute
	case "company_profiles":
		return 20 * time.Minute
	case "security_universe", "market_prescreen":
		return 90 * time.Minute
	default:
		return 30 * time.Minute
	}
}

// classifyOperationalExternalFailure turns a persisted provider error into a
// stable recovery category. It operates on already-sanitized local text and
// never makes an external request or inspects credentials.
func classifyOperationalExternalFailure(message string) (string, string) {
	message = strings.ToLower(strings.TrimSpace(message))
	switch {
	case message == "":
		return "unknown", ""
	case strings.Contains(message, "429"), strings.Contains(message, "rate limit"), strings.Contains(message, "rate limited"):
		return "限流", "等待供应商额度窗口恢复后由下一次任务重试；不要反复手动触发全量任务"
	case strings.Contains(message, "timeout"), strings.Contains(message, "deadline exceeded"), strings.Contains(message, "temporary"), strings.Contains(message, "connection reset"):
		return "超时或上游暂不可用", "系统会保留失败记录；确认网络后等待下一次计划任务或执行单标的补偿"
	case strings.Contains(message, "401"), strings.Contains(message, "403"), strings.Contains(message, "unauthorized"), strings.Contains(message, "forbidden"), strings.Contains(message, "credential"):
		return "认证或权限", "检查系统配置中的凭据、权限范围和有效期后再重试"
	case strings.Contains(message, "404"), strings.Contains(message, "not found"):
		return "资源不存在", "确认代码、Ticker 或 SEC 文件是否已变更；此类错误不建议立即重复请求"
	default:
		return "未知上游错误", "查看发现日志中的已脱敏原始错误；如持续出现请检查 Provider 配置"
	}
}

func (s *OperationalHealthService) Notify(ctx context.Context) (OperationalAlertResult, error) {
	report, err := s.Report(ctx)
	if err != nil {
		return OperationalAlertResult{}, err
	}
	result := OperationalAlertResult{Report: report}
	if len(report.Issues) == 0 {
		result.Suppressed = true
		result.Reason = "operational_report_healthy"
		return result, SkipTask("运行状态正常，无需发送运行摘要")
	}
	if s.configs == nil || s.notifier == nil {
		return result, errors.New("operational alert delivery is not configured")
	}
	telegramConfig, err := s.configs.Telegram(ctx)
	if err != nil {
		return result, err
	}
	if !telegramConfig.Enabled {
		result.Suppressed = true
		result.Reason = "telegram_disabled"
		return result, SkipTask("Telegram 未启用，已生成本地运行摘要")
	}
	fingerprint := operationalFingerprint(report)
	var prior model.OperationalAlertDelivery
	if err := s.db.WithContext(ctx).Where("fingerprint = ?", fingerprint).First(&prior).Error; err == nil && prior.LastSentAt != nil && time.Since(*prior.LastSentAt) < operationalAlertRepeatAfter {
		result.Suppressed = true
		result.Reason = "duplicate_within_12h"
		return result, SkipTask("相同运行异常已在 12 小时内通知，已抑制重复推送")
	} else if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return result, err
	}
	message := "SEC Monitor 运行摘要\n" + report.Summary
	if err := s.notifier.Send(ctx, telegram.Message{Text: message}); err != nil {
		_ = s.upsertDelivery(ctx, fingerprint, report, nil, err)
		return result, err
	}
	sentAt := time.Now().UTC()
	if err := s.upsertDelivery(ctx, fingerprint, report, &sentAt, nil); err != nil {
		return result, err
	}
	result.Sent = true
	return result, nil
}

func (s *OperationalHealthService) upsertDelivery(ctx context.Context, fingerprint string, report OperationalReport, sentAt *time.Time, runErr error) error {
	values := map[string]any{"severity": report.Status, "summary": report.Summary, "last_sent_at": sentAt, "last_error": ""}
	if runErr != nil {
		values["last_error"] = SanitizeSensitiveError(runErr.Error())
	}
	return s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "fingerprint"}}, DoUpdates: clause.Assignments(values),
	}).Create(&model.OperationalAlertDelivery{Fingerprint: fingerprint, Severity: report.Status, Summary: report.Summary, LastSentAt: sentAt, LastError: fmt.Sprint(values["last_error"])}).Error
}

func (r *OperationalReport) addIssue(key, category, severity, title, detail, action string, observedAt time.Time) {
	r.Issues = append(r.Issues, OperationalIssue{Key: key, Category: category, Severity: severity, Title: title, Detail: detail, Action: action, ObservedAt: observedAt})
}

func taskExpectedWithin(taskName string) time.Duration {
	switch taskName {
	case "sec_filing_sync":
		return 20 * time.Minute
	case "ipo_radar_sync":
		return 90 * time.Minute
	case "candidate_notification_sync", "trade_setup_notification_sync", "notification_retry_sync":
		return 30 * time.Minute
	case "small_cap_discovery_sync":
		return 36 * time.Hour
	case "small_cap_discovery_full_sync":
		return 9 * 24 * time.Hour
	case "sqlite_backup", "operational_health_notification_sync":
		return 30 * time.Hour
	case "macro_calendar_sync":
		return 30 * time.Hour
	case "operation_history_cleanup":
		return 9 * 24 * time.Hour
	default:
		return 48 * time.Hour
	}
}

func operationalSeverityRank(value string) int {
	switch value {
	case "critical":
		return 3
	case "warning":
		return 2
	default:
		return 1
	}
}

func operationalReportStatus(issues []OperationalIssue) string {
	for _, issue := range issues {
		if issue.Severity == "critical" {
			return "critical"
		}
	}
	if len(issues) > 0 {
		return "warning"
	}
	return "ok"
}

func operationalFingerprint(report OperationalReport) string {
	parts := make([]string, 0, len(report.Issues))
	for _, issue := range report.Issues {
		parts = append(parts, issue.Severity+":"+issue.Key)
	}
	sort.Strings(parts)
	sum := sha256.Sum256([]byte(strings.Join(parts, "|")))
	return hex.EncodeToString(sum[:])
}

func renderOperationalSummary(report OperationalReport) string {
	if len(report.Issues) == 0 {
		return "状态正常：最近任务、SEC 重试队列和行情数据源未发现需要处理的问题。"
	}
	lines := []string{fmt.Sprintf("状态：%s；待办 %d 项", report.Status, len(report.Issues))}
	for index, issue := range report.Issues {
		if index == 6 {
			lines = append(lines, fmt.Sprintf("…其余 %d 项请在系统健康页查看", len(report.Issues)-index))
			break
		}
		lines = append(lines, fmt.Sprintf("[%s] %s：%s", issue.Severity, issue.Title, issue.Detail))
	}
	return strings.Join(lines, "\n")
}

func valueOrDash(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return value
}

func formatOperationalDuration(value time.Duration) string {
	if value < time.Hour {
		return fmt.Sprintf("%dm", int(value.Round(time.Minute).Minutes()))
	}
	return fmt.Sprintf("%.1fh", value.Hours())
}

func truncateOperationalText(value string, limit int) string {
	if len(value) <= limit || limit < 1 {
		return value
	}
	return value[:limit] + "…"
}
