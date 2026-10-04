package service

import (
	"sec_monitor/internal/model"
	"testing"
	"time"
)

func TestOperationalRecoveryRequiresActualScheduledAttempt(t *testing.T) {
	now := time.Now()
	next := now.Add(time.Hour)
	for _, tc := range []struct {
		task      model.TaskConfig
		status    string
		scheduled bool
	}{
		{model.TaskConfig{Enabled: false, NextRunAt: &next}, "task_disabled", false},
		{model.TaskConfig{Enabled: true}, "manual_review", false},
		{model.TaskConfig{Enabled: true, NextRunAt: &next}, "scheduled", true},
		{model.TaskConfig{Enabled: true, RetryNotBefore: &next}, "retry_scheduled", true},
		{model.TaskConfig{Enabled: true, Running: true, NextRunAt: &next}, "running", false},
		{model.TaskConfig{Enabled: true, AutoRetryAttempts: MaxTaskAutoRetries, RetryNotBefore: &next}, "manual_review", false},
	} {
		tc.task.TaskName = "macro_calendar_sync"
		report := OperationalReport{Issues: []OperationalIssue{{Key: "macro_schedule_coverage:bls", Category: "macro"}}}
		annotateOperationalRecovery(&report, []model.TaskConfig{tc.task}, now)
		if report.Issues[0].RecoveryStatus != tc.status || (report.Issues[0].NextAttemptAt != nil) != tc.scheduled {
			t.Fatalf("task=%+v issue=%+v", tc.task, report.Issues[0])
		}
	}
}
