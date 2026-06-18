package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"sec_monitor/internal/model"
	"sec_monitor/internal/sec"
	"sec_monitor/internal/telegram"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type IPORadarService struct {
	db       *gorm.DB
	sec      sec.CurrentFilingsClient
	notifier telegram.Notifier
	configs  *ConfigService
}

type IPOFilingFilter struct {
	CompanyName string
	CIK         string
	FilingType  string
	Notified    string
	Page        int
	PageSize    int
}

type IPORadarRefreshResult struct {
	Checked    int  `json:"checked"`
	NewFilings int  `json:"new_filings"`
	Notified   int  `json:"notified"`
	SyncRunID  uint `json:"sync_run_id"`
}

func NewIPORadarService(db *gorm.DB, secClient sec.CurrentFilingsClient, notifier telegram.Notifier, configs *ConfigService) *IPORadarService {
	return &IPORadarService{db: db, sec: secClient, notifier: notifier, configs: configs}
}

func (s *IPORadarService) List(ctx context.Context, filter IPOFilingFilter) (PageResult[model.IPOFiling], error) {
	page, pageSize := normalizePage(filter.Page, filter.PageSize)
	query := s.db.WithContext(ctx).Model(&model.IPOFiling{})
	if filter.CompanyName != "" {
		query = query.Where("company_name LIKE ?", "%"+strings.TrimSpace(filter.CompanyName)+"%")
	}
	if filter.CIK != "" {
		query = query.Where("cik = ?", strings.TrimSpace(filter.CIK))
	}
	if filter.FilingType != "" {
		query = query.Where("filing_type = ?", strings.TrimSpace(filter.FilingType))
	}
	switch strings.ToLower(strings.TrimSpace(filter.Notified)) {
	case "yes":
		query = query.Where("notified_at IS NOT NULL")
	case "no":
		query = query.Where("notified_at IS NULL")
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return PageResult[model.IPOFiling]{}, err
	}
	var items []model.IPOFiling
	err := query.Order("filing_date DESC, accepted_at DESC, id DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&items).Error
	return newPageResult(items, total, page, pageSize), err
}

func (s *IPORadarService) Refresh(ctx context.Context) (IPORadarRefreshResult, error) {
	return s.RefreshWithTrigger(ctx, "ipo_manual")
}

func (s *IPORadarService) RefreshWithTrigger(ctx context.Context, trigger string) (IPORadarRefreshResult, error) {
	startedAt := time.Now().UTC()
	if strings.TrimSpace(trigger) == "" {
		trigger = "ipo_manual"
	}
	run := model.SyncRun{StartedAt: startedAt, Status: "running", Trigger: trigger}
	if err := s.db.WithContext(ctx).Create(&run).Error; err != nil {
		return IPORadarRefreshResult{}, err
	}
	out := IPORadarRefreshResult{SyncRunID: run.ID}
	settings, err := s.configs.IPORadarSettings(ctx)
	if err != nil {
		s.finishSyncRun(ctx, run.ID, out, "failed", err.Error())
		return out, err
	}
	if !settings.Enabled {
		s.finishSyncRun(ctx, run.ID, out, "success", "")
		return out, nil
	}
	results, err := s.sec.ListCurrentFilings(ctx, sec.CurrentFilingQuery{FormTypes: settings.FormTypes, Count: settings.MaxResults})
	if err != nil {
		s.finishSyncRun(ctx, run.ID, out, "failed", err.Error())
		return out, err
	}
	cutoff := time.Now().UTC().AddDate(0, 0, -settings.LookbackDays)
	out.Checked = len(results)
	for _, item := range results {
		if !item.FilingDate.IsZero() && item.FilingDate.Before(cutoff) {
			continue
		}
		if !ipoKeywordMatch(item, settings.Keywords) {
			continue
		}
		filing := model.IPOFiling{
			FilingID:        valueOrDefault(item.FilingID, item.FilingURL),
			AccessionNumber: item.AccessionNumber,
			CIK:             item.CIK,
			CompanyName:     valueOrDefault(item.CompanyName, "Unknown"),
			FilingType:      item.FilingType,
			FilingDate:      item.FilingDate,
			AcceptedAt:      item.AcceptedAt,
			FilingURL:       item.FilingURL,
			Title:           item.Title,
		}
		created, err := s.createIfNew(ctx, filing)
		if err != nil {
			s.finishSyncRun(ctx, run.ID, out, "failed", err.Error())
			return out, err
		}
		if !created {
			continue
		}
		out.NewFilings++
		notified, err := s.notify(ctx, filing, settings)
		if err != nil {
			s.finishSyncRun(ctx, run.ID, out, "failed", err.Error())
			return out, err
		}
		if notified {
			out.Notified++
		}
	}
	s.finishSyncRun(ctx, run.ID, out, "success", "")
	return out, nil
}

func (s *IPORadarService) finishSyncRun(ctx context.Context, id uint, result IPORadarRefreshResult, status string, errorMessage string) {
	finishedAt := time.Now().UTC()
	_ = s.db.WithContext(ctx).Model(&model.SyncRun{}).Where("id = ?", id).Updates(map[string]any{
		"finished_at":     &finishedAt,
		"status":          status,
		"targets_checked": result.Checked,
		"new_filings":     result.NewFilings,
		"failed_targets":  0,
		"error_message":   errorMessage,
	}).Error
}

func (s *IPORadarService) createIfNew(ctx context.Context, filing model.IPOFiling) (bool, error) {
	if strings.TrimSpace(filing.FilingID) == "" {
		return false, fmt.Errorf("%w: filing_id is required", ErrValidation)
	}
	res := s.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&filing)
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected == 1, nil
}

func (s *IPORadarService) notify(ctx context.Context, filing model.IPOFiling, settings IPORadarSettings) (bool, error) {
	if !settings.NotifyEnabled {
		return false, nil
	}
	cfg, err := s.configs.Telegram(ctx)
	if err != nil || !cfg.Enabled || cfg.ChatID == "" || cfg.BotToken == "" {
		return false, err
	}
	message := telegram.Message{
		Text: fmt.Sprintf("IPO Radar: %s %s\n%s\n%s\n%s", filing.CompanyName, filing.FilingType, filing.Title, filing.FilingDate.Format("2006-01-02"), filing.FilingURL),
	}
	status := "success"
	errorMessage := ""
	retryCount := 0
	if err := sendWithRetry(ctx, s.notifier, message, 3); err != nil {
		status = "failed"
		errorMessage = err.Error()
		retryCount = 3
	}
	now := time.Now().UTC()
	log := model.NotificationLog{
		FilingID:     filing.FilingID,
		Channel:      "telegram",
		Target:       cfg.ChatID,
		Status:       status,
		RetryCount:   retryCount,
		ErrorMessage: errorMessage,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if status == "success" {
		log.SentAt = &now
	}
	if err := s.db.WithContext(ctx).Create(&log).Error; err != nil {
		return false, err
	}
	if status != "success" {
		return false, nil
	}
	return true, s.db.WithContext(ctx).Model(&model.IPOFiling{}).Where("filing_id = ?", filing.FilingID).Update("notified_at", &now).Error
}

func ipoKeywordMatch(item sec.CurrentFilingResult, keywords []string) bool {
	if len(keywords) == 0 {
		return true
	}
	haystack := strings.ToLower(item.CompanyName + " " + item.Title)
	for _, keyword := range keywords {
		needle := strings.ToLower(strings.TrimSpace(keyword))
		if needle != "" && strings.Contains(haystack, needle) {
			return true
		}
	}
	return false
}
