package service

import (
	"context"

	"sec_monitor/internal/model"

	"gorm.io/gorm"
)

type NotificationService struct {
	db *gorm.DB
}

type NotificationLogFilter struct {
	Status   string
	Channel  string
	Page     int
	PageSize int
}

func NewNotificationService(db *gorm.DB) *NotificationService {
	return &NotificationService{db: db}
}

func (s *NotificationService) List(ctx context.Context, filter NotificationLogFilter) (PageResult[model.NotificationLog], error) {
	page, pageSize := normalizePage(filter.Page, filter.PageSize)
	query := s.db.WithContext(ctx).Model(&model.NotificationLog{})
	if filter.Status != "" {
		query = query.Where("status = ?", filter.Status)
	}
	if filter.Channel != "" {
		query = query.Where("channel = ?", filter.Channel)
	}

	var total int64
	if err := query.Count(&total).Error; err != nil {
		return PageResult[model.NotificationLog]{}, err
	}

	var logs []model.NotificationLog
	err := query.Order("created_at DESC, id DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&logs).Error
	return newPageResult(logs, total, page, pageSize), err
}
