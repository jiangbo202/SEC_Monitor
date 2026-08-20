package discovery

import (
	"context"
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	securityCheckpointRunning   = "running"
	securityCheckpointCompleted = "completed"
	securityCheckpointFailed    = "failed"
)

// runSecurityStageChunk commits the data rows and their executable checkpoint
// atomically. A retry of the same content-addressed batch skips completed
// chunks. The post-commit hook deliberately runs afterwards: if the process
// stops after commit, the next attempt still observes the durable checkpoint.
func (c *Coordinator) runSecurityStageChunk(ctx context.Context, batchID, phase string, chunk, recordCount int, write func(*gorm.DB) error) error {
	if c == nil || c.DB == nil || strings.TrimSpace(batchID) == "" || strings.TrimSpace(phase) == "" || chunk < 0 || write == nil {
		return errors.New("security checkpoint input is invalid")
	}
	var existing SecurityStageCheckpoint
	err := c.DB.WithContext(ctx).Where("batch_id = ? AND phase = ? AND chunk = ?", batchID, phase, chunk).First(&existing).Error
	if err == nil && existing.Status == securityCheckpointCompleted {
		return nil
	}
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	now := c.securityCheckpointNow()
	checkpoint := SecurityStageCheckpoint{BatchID: batchID, Phase: phase, Chunk: chunk, Status: securityCheckpointRunning, AttemptCount: 1, RecordCount: recordCount, StartedAt: now}
	if existing.ID == 0 {
		if err := c.DB.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&checkpoint).Error; err != nil {
			return err
		}
	}
	if err := c.DB.WithContext(ctx).Model(&SecurityStageCheckpoint{}).
		Where("batch_id = ? AND phase = ? AND chunk = ?", batchID, phase, chunk).
		Updates(map[string]any{
			"status": securityCheckpointRunning, "attempt_count": gorm.Expr("attempt_count + ?", 1),
			"record_count": recordCount, "error_message": "", "started_at": now, "completed_at": nil,
		}).Error; err != nil {
		return err
	}
	// The insert above starts at one attempt; avoid counting the same first
	// attempt twice. Existing rows genuinely increment on every retry.
	if existing.ID == 0 {
		if err := c.DB.WithContext(ctx).Model(&SecurityStageCheckpoint{}).
			Where("batch_id = ? AND phase = ? AND chunk = ?", batchID, phase, chunk).
			Update("attempt_count", 1).Error; err != nil {
			return err
		}
	}
	err = c.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := write(tx); err != nil {
			return err
		}
		completed := c.securityCheckpointNow()
		result := tx.Model(&SecurityStageCheckpoint{}).
			Where("batch_id = ? AND phase = ? AND chunk = ?", batchID, phase, chunk).
			Updates(map[string]any{"status": securityCheckpointCompleted, "record_count": recordCount, "completed_at": completed, "error_message": ""})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return errors.New("security checkpoint disappeared before commit")
		}
		return nil
	})
	if err != nil {
		failedAt := c.securityCheckpointNow()
		_ = c.DB.WithContext(context.WithoutCancel(ctx)).Model(&SecurityStageCheckpoint{}).
			Where("batch_id = ? AND phase = ? AND chunk = ?", batchID, phase, chunk).
			Updates(map[string]any{"status": securityCheckpointFailed, "completed_at": failedAt, "error_message": err.Error()}).Error
		return err
	}
	if c.AfterStageChunk != nil {
		return c.AfterStageChunk(phase, chunk)
	}
	return nil
}

func (c *Coordinator) securityCheckpointNow() time.Time {
	if c != nil && c.Clock != nil {
		return c.Clock().UTC()
	}
	return time.Now().UTC()
}

type SecurityStageCheckpointSummary struct {
	Phase           string     `json:"phase"`
	Status          string     `json:"status"`
	CompletedChunks int        `json:"completed_chunks"`
	FailedChunks    int        `json:"failed_chunks"`
	RecordCount     int        `json:"record_count"`
	AttemptCount    int        `json:"attempt_count"`
	StartedAt       time.Time  `json:"started_at"`
	CompletedAt     *time.Time `json:"completed_at,omitempty"`
	ErrorMessage    string     `json:"error_message,omitempty"`
}

// ListSecurityStageCheckpointSummaries exposes one compact row per phase for
// the workflow timeline while retaining chunk-level rows for recovery.
func ListSecurityStageCheckpointSummaries(ctx context.Context, db *gorm.DB, batchID string) ([]SecurityStageCheckpointSummary, error) {
	if db == nil || ctx == nil || strings.TrimSpace(batchID) == "" {
		return nil, errors.New("security checkpoint query is invalid")
	}
	var rows []SecurityStageCheckpoint
	if err := db.WithContext(ctx).Where("batch_id = ?", batchID).Order("id ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	order := make([]string, 0)
	byPhase := make(map[string]*SecurityStageCheckpointSummary)
	for _, row := range rows {
		summary := byPhase[row.Phase]
		if summary == nil {
			order = append(order, row.Phase)
			summary = &SecurityStageCheckpointSummary{Phase: row.Phase, Status: securityCheckpointCompleted, StartedAt: row.StartedAt}
			byPhase[row.Phase] = summary
		}
		if row.StartedAt.Before(summary.StartedAt) {
			summary.StartedAt = row.StartedAt
		}
		summary.AttemptCount += row.AttemptCount
		if row.Status == securityCheckpointCompleted {
			summary.CompletedChunks++
			summary.RecordCount += row.RecordCount
		} else {
			summary.Status = row.Status
			if row.Status == securityCheckpointFailed {
				summary.FailedChunks++
				summary.ErrorMessage = row.ErrorMessage
			}
		}
		if row.CompletedAt != nil && (summary.CompletedAt == nil || row.CompletedAt.After(*summary.CompletedAt)) {
			value := *row.CompletedAt
			summary.CompletedAt = &value
		}
	}
	result := make([]SecurityStageCheckpointSummary, 0, len(order))
	for _, phase := range order {
		result = append(result, *byPhase[phase])
	}
	return result, nil
}
