package service

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"sec_monitor/internal/discovery"
	"sec_monitor/internal/model"

	"gorm.io/gorm"
)

// LifecycleCleanupPreview covers operational history plus obsolete full
// snapshot copies created by targeted market-price repairs. Current research,
// price history, filings, notifications, and signal events remain outside this
// policy.
type LifecycleCleanupPreview struct {
	RetentionDays              int       `json:"retention_days"`
	Cutoff                     time.Time `json:"cutoff"`
	SyncRuns                   int64     `json:"sync_runs"`
	SyncRunDetails             int64     `json:"sync_run_details"`
	TaskExecutions             int64     `json:"task_executions"`
	NotificationBatches        int64     `json:"notification_batches"`
	NotificationBatchItems     int64     `json:"notification_batch_items"`
	OperationalAlertDeliveries int64     `json:"operational_alert_deliveries"`
	RecoveryDrills             int64     `json:"recovery_drills"`
	LifecycleCleanupRuns       int64     `json:"lifecycle_cleanup_runs"`
	DiscoverySyncRuns          int64     `json:"discovery_sync_runs"`
	DiscoverySyncSteps         int64     `json:"discovery_sync_steps"`
	SupersededMarketRepairs    int64     `json:"superseded_market_repairs"`
	MarketRepairUniverseRows   int64     `json:"market_repair_universe_rows"`
	MarketRepairScoreRows      int64     `json:"market_repair_score_rows"`
	Total                      int64     `json:"total"`
}

type LifecycleCleanupResult struct {
	LifecycleCleanupPreview
	DeletedAt time.Time `json:"deleted_at"`
}

// LifecycleService manages bounded retention for diagnostic execution history.
// The service deliberately uses completed_at/finished_at to preserve running
// jobs, even if their started_at value is old.
type LifecycleService struct {
	mainDB      *gorm.DB
	discoveryDB *gorm.DB
	configs     *ConfigService
	mu          sync.Mutex
}

func NewLifecycleService(mainDB, discoveryDB *gorm.DB, configs *ConfigService) *LifecycleService {
	return &LifecycleService{mainDB: mainDB, discoveryDB: discoveryDB, configs: configs}
}

func (s *LifecycleService) Preview(ctx context.Context, now time.Time) (LifecycleCleanupPreview, error) {
	if s == nil || s.mainDB == nil || s.discoveryDB == nil {
		return LifecycleCleanupPreview{}, errors.New("lifecycle service is not configured")
	}
	retentionDays, err := s.retentionDays(ctx)
	if err != nil {
		return LifecycleCleanupPreview{}, err
	}
	preview := LifecycleCleanupPreview{
		RetentionDays: retentionDays,
		Cutoff:        now.UTC().AddDate(0, 0, -retentionDays),
	}
	if err := s.mainDB.WithContext(ctx).Model(&model.SyncRun{}).
		Where("finished_at IS NOT NULL AND finished_at < ?", preview.Cutoff).Count(&preview.SyncRuns).Error; err != nil {
		return LifecycleCleanupPreview{}, err
	}
	if err := s.mainDB.WithContext(ctx).Model(&model.SyncRunDetail{}).
		Where("sync_run_id IN (?)", s.mainDB.Model(&model.SyncRun{}).Select("id").Where("finished_at IS NOT NULL AND finished_at < ?", preview.Cutoff)).
		Count(&preview.SyncRunDetails).Error; err != nil {
		return LifecycleCleanupPreview{}, err
	}
	if err := s.mainDB.WithContext(ctx).Model(&model.TaskExecution{}).
		Where("finished_at IS NOT NULL AND finished_at < ?", preview.Cutoff).Count(&preview.TaskExecutions).Error; err != nil {
		return LifecycleCleanupPreview{}, err
	}
	if err := s.mainDB.WithContext(ctx).Model(&model.NotificationBatch{}).
		Where("updated_at < ?", preview.Cutoff).Count(&preview.NotificationBatches).Error; err != nil {
		return LifecycleCleanupPreview{}, err
	}
	if err := s.mainDB.WithContext(ctx).Model(&model.NotificationBatchItem{}).
		Where("batch_id IN (?)", s.mainDB.Model(&model.NotificationBatch{}).Select("id").Where("updated_at < ?", preview.Cutoff)).
		Count(&preview.NotificationBatchItems).Error; err != nil {
		return LifecycleCleanupPreview{}, err
	}
	if err := s.mainDB.WithContext(ctx).Model(&model.OperationalAlertDelivery{}).
		Where("updated_at < ?", preview.Cutoff).Count(&preview.OperationalAlertDeliveries).Error; err != nil {
		return LifecycleCleanupPreview{}, err
	}
	if err := s.mainDB.WithContext(ctx).Model(&model.RecoveryDrill{}).
		Where("completed_at IS NOT NULL AND completed_at < ?", preview.Cutoff).Count(&preview.RecoveryDrills).Error; err != nil {
		return LifecycleCleanupPreview{}, err
	}
	if err := s.mainDB.WithContext(ctx).Model(&model.LifecycleCleanupRun{}).
		Where("completed_at IS NOT NULL AND completed_at < ?", preview.Cutoff).Count(&preview.LifecycleCleanupRuns).Error; err != nil {
		return LifecycleCleanupPreview{}, err
	}
	if err := s.discoveryDB.WithContext(ctx).Model(&discovery.DiscoverySyncRun{}).
		Where("completed_at IS NOT NULL AND completed_at < ?", preview.Cutoff).Count(&preview.DiscoverySyncRuns).Error; err != nil {
		return LifecycleCleanupPreview{}, err
	}
	if err := s.discoveryDB.WithContext(ctx).Model(&discovery.DiscoverySyncStep{}).
		Where("run_id IN (?)", s.discoveryDB.Model(&discovery.DiscoverySyncRun{}).Select("id").Where("completed_at IS NOT NULL AND completed_at < ?", preview.Cutoff)).
		Count(&preview.DiscoverySyncSteps).Error; err != nil {
		return LifecycleCleanupPreview{}, err
	}
	marketRepair, err := discovery.PreviewSupersededMarketRepairCleanup(ctx, s.discoveryDB, preview.Cutoff)
	if err != nil {
		return LifecycleCleanupPreview{}, err
	}
	preview.SupersededMarketRepairs = marketRepair.Batches
	preview.MarketRepairUniverseRows = marketRepair.UniverseSnapshots
	preview.MarketRepairScoreRows = marketRepair.CandidateScoreRows
	preview.Total = lifecycleCleanupTotal(preview)
	return preview, nil
}

func (s *LifecycleService) Cleanup(ctx context.Context, now time.Time) (LifecycleCleanupResult, error) {
	if s == nil || !s.mu.TryLock() {
		return LifecycleCleanupResult{}, TaskAlreadyRunning("operation_history_cleanup")
	}
	defer s.mu.Unlock()
	preview, err := s.Preview(ctx, now)
	if err != nil {
		return LifecycleCleanupResult{}, err
	}
	startedAt := time.Now().UTC()
	run := model.LifecycleCleanupRun{Status: "running", RetentionDays: preview.RetentionDays, Cutoff: preview.Cutoff, MainStatus: "pending", DiscoveryStatus: "pending", StartedAt: startedAt}
	if err := s.mainDB.WithContext(ctx).Create(&run).Error; err != nil {
		return LifecycleCleanupResult{}, err
	}
	finishRun := func(status, mainStatus, discoveryStatus string, count int64, runErr error) {
		completedAt := time.Now().UTC()
		values := map[string]any{"status": status, "main_status": mainStatus, "discovery_status": discoveryStatus, "deleted_count": count, "completed_at": &completedAt, "error_message": ""}
		if runErr != nil {
			values["error_message"] = SanitizeSensitiveError(runErr.Error())
		}
		_ = s.mainDB.WithContext(context.Background()).Model(&model.LifecycleCleanupRun{}).Where("id = ?", run.ID).Updates(values).Error
	}
	deleted := LifecycleCleanupPreview{RetentionDays: preview.RetentionDays, Cutoff: preview.Cutoff}
	if err := s.mainDB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		runs := tx.Model(&model.SyncRun{}).Select("id").Where("finished_at IS NOT NULL AND finished_at < ?", preview.Cutoff)
		result := tx.Where("sync_run_id IN (?)", runs).Delete(&model.SyncRunDetail{})
		if result.Error != nil {
			return result.Error
		}
		deleted.SyncRunDetails = result.RowsAffected
		result = tx.Where("finished_at IS NOT NULL AND finished_at < ?", preview.Cutoff).Delete(&model.SyncRun{})
		if result.Error != nil {
			return result.Error
		}
		deleted.SyncRuns = result.RowsAffected
		result = tx.Where("finished_at IS NOT NULL AND finished_at < ?", preview.Cutoff).Delete(&model.TaskExecution{})
		if result.Error != nil {
			return result.Error
		}
		deleted.TaskExecutions = result.RowsAffected
		oldNotificationBatches := tx.Model(&model.NotificationBatch{}).Select("id").Where("updated_at < ?", preview.Cutoff)
		result = tx.Where("batch_id IN (?)", oldNotificationBatches).Delete(&model.NotificationBatchItem{})
		if result.Error != nil {
			return result.Error
		}
		deleted.NotificationBatchItems = result.RowsAffected
		result = tx.Where("updated_at < ?", preview.Cutoff).Delete(&model.NotificationBatch{})
		if result.Error != nil {
			return result.Error
		}
		deleted.NotificationBatches = result.RowsAffected
		result = tx.Where("updated_at < ?", preview.Cutoff).Delete(&model.OperationalAlertDelivery{})
		if result.Error != nil {
			return result.Error
		}
		deleted.OperationalAlertDeliveries = result.RowsAffected
		result = tx.Where("completed_at IS NOT NULL AND completed_at < ?", preview.Cutoff).Delete(&model.RecoveryDrill{})
		if result.Error != nil {
			return result.Error
		}
		deleted.RecoveryDrills = result.RowsAffected
		result = tx.Where("completed_at IS NOT NULL AND completed_at < ?", preview.Cutoff).Delete(&model.LifecycleCleanupRun{})
		if result.Error != nil {
			return result.Error
		}
		deleted.LifecycleCleanupRuns = result.RowsAffected
		return nil
	}); err != nil {
		finishRun("failed", "failed", "pending", lifecycleCleanupTotal(deleted), err)
		return LifecycleCleanupResult{}, err
	}
	if err := s.mainDB.WithContext(ctx).Model(&model.LifecycleCleanupRun{}).Where("id = ?", run.ID).Update("main_status", "completed").Error; err != nil {
		finishRun("failed", "completed", "pending", lifecycleCleanupTotal(deleted), err)
		return LifecycleCleanupResult{}, err
	}
	if err := s.discoveryDB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		runs := tx.Model(&discovery.DiscoverySyncRun{}).Select("id").Where("completed_at IS NOT NULL AND completed_at < ?", preview.Cutoff)
		result := tx.Where("run_id IN (?)", runs).Delete(&discovery.DiscoverySyncStep{})
		if result.Error != nil {
			return result.Error
		}
		deleted.DiscoverySyncSteps = result.RowsAffected
		result = tx.Where("completed_at IS NOT NULL AND completed_at < ?", preview.Cutoff).Delete(&discovery.DiscoverySyncRun{})
		if result.Error != nil {
			return result.Error
		}
		deleted.DiscoverySyncRuns = result.RowsAffected
		return nil
	}); err != nil {
		finishRun("partial", "completed", "failed", lifecycleCleanupTotal(deleted), err)
		return LifecycleCleanupResult{}, PartialTask("运行历史主库清理已完成，小盘工作流日志将于下次任务或手动重试补齐：" + SanitizeSensitiveError(err.Error()))
	}
	marketRepair, err := discovery.CleanupSupersededMarketRepairBatches(ctx, s.discoveryDB, preview.Cutoff)
	if err != nil {
		finishRun("partial", "completed", "failed", lifecycleCleanupTotal(deleted), err)
		return LifecycleCleanupResult{}, PartialTask("运行历史清理已完成，但旧行情修正快照将在下次任务或手动重试补齐：" + SanitizeSensitiveError(err.Error()))
	}
	deleted.SupersededMarketRepairs = marketRepair.Batches
	deleted.MarketRepairUniverseRows = marketRepair.UniverseSnapshots
	deleted.MarketRepairScoreRows = marketRepair.CandidateScoreRows
	deleted.Total = lifecycleCleanupTotal(deleted)
	finishRun("completed", "completed", "completed", deleted.Total, nil)
	return LifecycleCleanupResult{LifecycleCleanupPreview: deleted, DeletedAt: time.Now().UTC()}, nil
}

func lifecycleCleanupTotal(preview LifecycleCleanupPreview) int64 {
	return preview.SyncRuns + preview.SyncRunDetails + preview.TaskExecutions + preview.NotificationBatches + preview.NotificationBatchItems + preview.OperationalAlertDeliveries + preview.RecoveryDrills + preview.LifecycleCleanupRuns + preview.DiscoverySyncRuns + preview.DiscoverySyncSteps + preview.SupersededMarketRepairs + preview.MarketRepairUniverseRows + preview.MarketRepairScoreRows
}

func (s *LifecycleService) retentionDays(ctx context.Context) (int, error) {
	const defaultRetentionDays = 90
	if s.configs == nil {
		return defaultRetentionDays, nil
	}
	value, ok, err := s.configs.GetValue(ctx, "system.operation_history_retention_days")
	if err != nil {
		return 0, err
	}
	if !ok || strings.TrimSpace(value) == "" {
		return defaultRetentionDays, nil
	}
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || parsed < 7 || parsed > 3650 {
		return 0, fmt.Errorf("operation history retention days must be between 7 and 3650")
	}
	return parsed, nil
}
