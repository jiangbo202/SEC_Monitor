package service

import (
	"sec_monitor/internal/model"
	"strings"
	"time"
)

func annotateOperationalRecovery(report *OperationalReport, tasks []model.TaskConfig, now time.Time) {
	byName := map[string]model.TaskConfig{}
	for _, task := range tasks {
		byName[task.TaskName] = task
	}
	for index := range report.Issues {
		issue := &report.Issues[index]
		issue.RecoveryStatus = "manual_review"
		name := ""
		if strings.HasPrefix(issue.Key, "task_") {
			_, name, _ = strings.Cut(issue.Key, ":")
		}
		if issue.Category == "macro" {
			name = "macro_calendar_sync"
		}
		task, ok := byName[name]
		if !ok {
			continue
		}
		issue.TaskName = name
		switch {
		case task.Running:
			issue.RecoveryStatus = "running"
		case !task.Enabled:
			issue.RecoveryStatus = "task_disabled"
		case task.RetryNotBefore != nil && task.AutoRetryAttempts < MaxTaskAutoRetries:
			issue.RecoveryStatus = "retry_scheduled"
			issue.NextAttemptAt = task.RetryNotBefore
		case task.NextRunAt != nil && task.NextRunAt.After(now):
			issue.RecoveryStatus = "scheduled"
			issue.NextAttemptAt = task.NextRunAt
		}
	}
}
