package service

import (
	"context"
	"strconv"
	"time"

	"sec_monitor/internal/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

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
		{TaskName: "ipo_radar_sync", CronExpr: "*/30 * * * *", Enabled: true, Running: false},
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
	return s.db.WithContext(ctx).Model(&model.TaskConfig{}).
		Where("task_name = ?", taskName).
		Update("running", true).Error
}

func (s *TaskConfigService) MarkRunFinished(ctx context.Context, taskName string, ranAt time.Time) error {
	return s.db.WithContext(ctx).Model(&model.TaskConfig{}).
		Where("task_name = ?", taskName).
		Updates(map[string]any{
			"last_run_at": ranAt,
			"running":     false,
		}).Error
}
