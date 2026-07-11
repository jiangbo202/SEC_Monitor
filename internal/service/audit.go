package service

import (
	"context"
	"encoding/json"
	"time"

	"sec_monitor/internal/model"

	"gorm.io/gorm"
)

type AuditService struct {
	db *gorm.DB
}

type AuditLogFilter struct {
	Action     string
	ObjectType string
	Page       int
	PageSize   int
}

func NewAuditService(db *gorm.DB) *AuditService {
	return &AuditService{db: db}
}

func (s *AuditService) Record(ctx context.Context, operator string, action string, objectType string, objectID string, before any, after any) error {
	beforeData, err := marshalAuditData(before)
	if err != nil {
		return err
	}
	afterData, err := marshalAuditData(after)
	if err != nil {
		return err
	}
	beforeData = sanitizeOperationLogData(beforeData)
	afterData = sanitizeOperationLogData(afterData)

	log := model.OperationLog{
		OperatedAt: time.Now().UTC(),
		Operator:   operator,
		Action:     action,
		ObjectType: objectType,
		ObjectID:   objectID,
		BeforeData: beforeData,
		AfterData:  afterData,
	}
	return s.db.WithContext(ctx).Create(&log).Error
}

func (s *AuditService) List(ctx context.Context, filter AuditLogFilter) (PageResult[model.OperationLog], error) {
	page, pageSize := normalizePage(filter.Page, filter.PageSize)
	query := s.db.WithContext(ctx).Model(&model.OperationLog{})
	if filter.Action != "" {
		query = query.Where("action = ?", filter.Action)
	}
	if filter.ObjectType != "" {
		query = query.Where("object_type = ?", filter.ObjectType)
	}

	var total int64
	if err := query.Count(&total).Error; err != nil {
		return PageResult[model.OperationLog]{}, err
	}

	var logs []model.OperationLog
	err := query.Order("operated_at DESC, id DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&logs).Error
	for i := range logs {
		logs[i].BeforeData = sanitizeOperationLogData(logs[i].BeforeData)
		logs[i].AfterData = sanitizeOperationLogData(logs[i].AfterData)
	}
	return newPageResult(logs, total, page, pageSize), err
}

func marshalAuditData(value any) (string, error) {
	if value == nil {
		return "", nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// sanitizeOperationLogData masks encrypted configuration values in audit JSON.
// It tolerates unrelated or invalid audit data so audit reads remain backwards compatible.
func sanitizeOperationLogData(data string) string {
	if data == "" {
		return data
	}
	var payload any
	if err := json.Unmarshal([]byte(data), &payload); err != nil || !redactEncryptedConfigValues(payload) {
		return data
	}
	sanitized, err := json.Marshal(payload)
	if err != nil {
		return data
	}
	return string(sanitized)
}

func redactEncryptedConfigValues(payload any) bool {
	switch value := payload.(type) {
	case []any:
		changed := false
		for _, item := range value {
			changed = redactEncryptedConfigValues(item) || changed
		}
		return changed
	case map[string]any:
		changed := false
		if encrypted, ok := value["encrypted"].(bool); ok && encrypted {
			for _, key := range []string{"value", "config_value"} {
				if _, ok := value[key].(string); ok {
					value[key] = maskedSecretMarker
					changed = true
				}
			}
		}
		for _, item := range value {
			changed = redactEncryptedConfigValues(item) || changed
		}
		return changed
	default:
		return false
	}
}
