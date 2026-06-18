package service

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"sec_monitor/internal/model"

	"gorm.io/gorm"
)

type WatchTargetService struct {
	db    *gorm.DB
	audit *AuditService
}

type WatchTargetInput struct {
	Ticker      string `json:"ticker"`
	CompanyName string `json:"company_name"`
	CIK         string `json:"cik"`
	TargetType  string `json:"target_type"`
	Group       string `json:"group"`
	Status      string `json:"status"`
}

type WatchTargetFilter struct {
	Ticker     string
	Status     string
	TargetType string
	Group      string
	Page       int
	PageSize   int
}

func NewWatchTargetService(db *gorm.DB, audit *AuditService) *WatchTargetService {
	return &WatchTargetService{db: db, audit: audit}
}

func (s *WatchTargetService) Create(ctx context.Context, input WatchTargetInput, operator string) (model.WatchTarget, error) {
	target, err := input.toModel()
	if err != nil {
		return model.WatchTarget{}, err
	}
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&target).Error; err != nil {
			return err
		}
		return NewAuditService(tx).Record(ctx, operator, "create", "watch_target", strconv.FormatUint(uint64(target.ID), 10), nil, target)
	})
	return target, err
}

func (s *WatchTargetService) Update(ctx context.Context, id uint, input WatchTargetInput, operator string) (model.WatchTarget, error) {
	next, err := input.toModel()
	if err != nil {
		return model.WatchTarget{}, err
	}
	var updated model.WatchTarget
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var before model.WatchTarget
		if err := tx.First(&before, id).Error; err != nil {
			return mapNotFound(err)
		}
		next.ID = before.ID
		if err := tx.Model(&before).Updates(map[string]any{
			"ticker":       next.Ticker,
			"company_name": next.CompanyName,
			"cik":          next.CIK,
			"target_type":  next.TargetType,
			"group":        next.Group,
			"status":       next.Status,
		}).Error; err != nil {
			return err
		}
		if err := tx.First(&updated, id).Error; err != nil {
			return err
		}
		return NewAuditService(tx).Record(ctx, operator, "update", "watch_target", strconv.FormatUint(uint64(id), 10), before, updated)
	})
	return updated, err
}

func (s *WatchTargetService) Delete(ctx context.Context, id uint, operator string) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var before model.WatchTarget
		if err := tx.First(&before, id).Error; err != nil {
			return mapNotFound(err)
		}
		if err := tx.Delete(&before).Error; err != nil {
			return err
		}
		return NewAuditService(tx).Record(ctx, operator, "delete", "watch_target", strconv.FormatUint(uint64(id), 10), before, nil)
	})
}

func (s *WatchTargetService) SetStatus(ctx context.Context, id uint, status string, operator string) (model.WatchTarget, error) {
	status = strings.ToLower(strings.TrimSpace(status))
	if status != "enabled" && status != "disabled" {
		return model.WatchTarget{}, fmt.Errorf("%w: status must be enabled or disabled", ErrValidation)
	}

	var updated model.WatchTarget
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var before model.WatchTarget
		if err := tx.First(&before, id).Error; err != nil {
			return mapNotFound(err)
		}
		if err := tx.Model(&before).Update("status", status).Error; err != nil {
			return err
		}
		if err := tx.First(&updated, id).Error; err != nil {
			return err
		}
		return NewAuditService(tx).Record(ctx, operator, "update_status", "watch_target", strconv.FormatUint(uint64(id), 10), before, updated)
	})
	return updated, err
}

func (s *WatchTargetService) Get(ctx context.Context, id uint) (model.WatchTarget, error) {
	var target model.WatchTarget
	if err := s.db.WithContext(ctx).First(&target, id).Error; err != nil {
		return model.WatchTarget{}, mapNotFound(err)
	}
	return target, nil
}

func (s *WatchTargetService) List(ctx context.Context, filter WatchTargetFilter) (PageResult[model.WatchTarget], error) {
	page, pageSize := normalizePage(filter.Page, filter.PageSize)
	query := s.db.WithContext(ctx).Model(&model.WatchTarget{})
	if filter.Ticker != "" {
		query = query.Where("ticker LIKE ?", "%"+strings.ToUpper(strings.TrimSpace(filter.Ticker))+"%")
	}
	if filter.Status != "" {
		query = query.Where("status = ?", strings.ToLower(strings.TrimSpace(filter.Status)))
	}
	if filter.TargetType != "" {
		query = query.Where("target_type = ?", strings.ToLower(strings.TrimSpace(filter.TargetType)))
	}
	if filter.Group != "" {
		query = query.Where("`group` = ?", strings.TrimSpace(filter.Group))
	}

	var total int64
	if err := query.Count(&total).Error; err != nil {
		return PageResult[model.WatchTarget]{}, err
	}

	var targets []model.WatchTarget
	err := query.Order("created_at DESC, id DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&targets).Error
	return newPageResult(targets, total, page, pageSize), err
}

func (input WatchTargetInput) toModel() (model.WatchTarget, error) {
	ticker := strings.ToUpper(strings.TrimSpace(input.Ticker))
	companyName := strings.TrimSpace(input.CompanyName)
	targetType := strings.ToLower(strings.TrimSpace(input.TargetType))
	status := strings.ToLower(strings.TrimSpace(input.Status))
	if status == "" {
		status = "enabled"
	}
	if ticker == "" {
		return model.WatchTarget{}, fmt.Errorf("%w: ticker is required", ErrValidation)
	}
	if companyName == "" {
		return model.WatchTarget{}, fmt.Errorf("%w: company_name is required", ErrValidation)
	}
	if targetType != "stock" && targetType != "etf" {
		return model.WatchTarget{}, fmt.Errorf("%w: target_type must be stock or etf", ErrValidation)
	}
	if status != "enabled" && status != "disabled" {
		return model.WatchTarget{}, fmt.Errorf("%w: status must be enabled or disabled", ErrValidation)
	}
	return model.WatchTarget{
		Ticker:      ticker,
		CompanyName: companyName,
		CIK:         strings.TrimSpace(input.CIK),
		TargetType:  targetType,
		Group:       strings.TrimSpace(input.Group),
		Status:      status,
	}, nil
}

func mapNotFound(err error) error {
	if err == gorm.ErrRecordNotFound {
		return ErrNotFound
	}
	return err
}
