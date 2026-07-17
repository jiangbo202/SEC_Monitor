package service

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"sec_monitor/internal/discovery"
	"sec_monitor/internal/model"
	"sec_monitor/internal/sec"
	"sec_monitor/internal/telegram"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type FilingService struct {
	db          *gorm.DB
	sec         sec.Client
	notifier    telegram.Notifier
	configs     *ConfigService
	batches     *NotificationBatchService
	discoveryDB *gorm.DB
}

type FilingFilter struct {
	Ticker             string
	CompanyName        string
	FilingType         string
	NotificationStatus string
	DateFrom           *time.Time
	DateTo             *time.Time
	SortBy             string
	SortOrder          string
	Page               int
	PageSize           int
}

type FilingItem struct {
	model.Filing
	NotificationStatus string `json:"notification_status"`
	NotificationLogID  uint   `json:"notification_log_id"`
}

type RefreshResult struct {
	TargetsChecked int  `json:"targets_checked"`
	NewFilings     int  `json:"new_filings"`
	FailedTargets  int  `json:"failed_targets"`
	SyncRunID      uint `json:"sync_run_id"`
}

type fundFilingFilterResult struct {
	filings       []sec.FilingResult
	skipped       int
	reasonSummary string
}

const fundFilingIdentityIncompleteRetryAfter = time.Hour

type SyncRunFilter struct {
	Status   string
	Trigger  string
	Page     int
	PageSize int
}

type CleanupPreview struct {
	RetentionDays  int        `json:"retention_days"`
	Cutoff         time.Time  `json:"cutoff"`
	DeleteCount    int64      `json:"delete_count"`
	OldestPulledAt *time.Time `json:"oldest_pulled_at"`
	NewestPulledAt *time.Time `json:"newest_pulled_at"`
}

func NewFilingService(db *gorm.DB, secClient sec.Client, notifier telegram.Notifier, configs *ConfigService) *FilingService {
	return &FilingService{db: db, sec: secClient, notifier: notifier, configs: configs, batches: NewNotificationBatchService(db, notifier, configs)}
}

func (s *FilingService) WithDiscoveryDB(db *gorm.DB) *FilingService {
	if s == nil {
		return s
	}
	s.discoveryDB = db
	return s
}

func (s *FilingService) List(ctx context.Context, filter FilingFilter) (PageResult[FilingItem], error) {
	page, pageSize := normalizePage(filter.Page, filter.PageSize)
	query := s.db.WithContext(ctx).Model(&model.Filing{})
	targetID := uint(0)
	if filter.Ticker != "" {
		ticker := strings.ToUpper(strings.TrimSpace(filter.Ticker))
		var target model.WatchTarget
		if err := s.db.WithContext(ctx).Where("ticker = ?", ticker).First(&target).Error; err == nil {
			targetID = target.ID
		}
		query = query.Where(`(
			EXISTS (
				SELECT 1 FROM watch_target_filings target_filings
				JOIN watch_targets target_filter ON target_filter.id = target_filings.target_id
				WHERE target_filings.filing_id = filings.id AND target_filter.ticker = ?
			) OR (
				NOT EXISTS (SELECT 1 FROM watch_target_filings target_filings WHERE target_filings.filing_id = filings.id)
				AND filings.ticker = ?
			)
		)`, ticker, ticker)
	}
	if filter.CompanyName != "" {
		query = query.Where("company_name LIKE ?", "%"+strings.TrimSpace(filter.CompanyName)+"%")
	}
	if filter.FilingType != "" {
		query = query.Where("filing_type = ?", strings.TrimSpace(filter.FilingType))
	}
	notificationStatus := strings.ToLower(strings.TrimSpace(filter.NotificationStatus))
	if targetID != 0 {
		switch notificationStatus {
		case "success":
			query = query.Where(`(
				(SELECT status FROM notification_batch_items WHERE notification_batch_items.filing_id = filings.filing_id AND notification_batch_items.target_id = ? ORDER BY created_at DESC, id DESC LIMIT 1) = 'sent'
				OR (NOT EXISTS (SELECT 1 FROM notification_batch_items WHERE notification_batch_items.filing_id = filings.filing_id AND notification_batch_items.target_id = ?)
					AND (filings.notified_at IS NOT NULL OR (SELECT status FROM notification_logs WHERE notification_logs.filing_id = filings.filing_id ORDER BY created_at DESC, id DESC LIMIT 1) = 'success' OR (SELECT status FROM notification_batch_items WHERE notification_batch_items.filing_id = filings.filing_id AND notification_batch_items.target_id = 0 ORDER BY created_at DESC, id DESC LIMIT 1) = 'sent'))
			)`, targetID, targetID)
		case "failed":
			query = query.Where(`(
				(SELECT status FROM notification_batch_items WHERE notification_batch_items.filing_id = filings.filing_id AND notification_batch_items.target_id = ? ORDER BY created_at DESC, id DESC LIMIT 1) = 'failed'
				OR (NOT EXISTS (SELECT 1 FROM notification_batch_items WHERE notification_batch_items.filing_id = filings.filing_id AND notification_batch_items.target_id = ?)
					AND ((SELECT status FROM notification_logs WHERE notification_logs.filing_id = filings.filing_id ORDER BY created_at DESC, id DESC LIMIT 1) = 'failed' OR (SELECT status FROM notification_batch_items WHERE notification_batch_items.filing_id = filings.filing_id AND notification_batch_items.target_id = 0 ORDER BY created_at DESC, id DESC LIMIT 1) = 'failed'))
			)`, targetID, targetID)
		case "unnotified":
			query = query.Where(`(
				(EXISTS (SELECT 1 FROM notification_batch_items WHERE notification_batch_items.filing_id = filings.filing_id AND notification_batch_items.target_id = ?) AND COALESCE((SELECT status FROM notification_batch_items WHERE notification_batch_items.filing_id = filings.filing_id AND notification_batch_items.target_id = ? ORDER BY created_at DESC, id DESC LIMIT 1), '') NOT IN ('sent', 'failed'))
				OR (NOT EXISTS (SELECT 1 FROM notification_batch_items WHERE notification_batch_items.filing_id = filings.filing_id AND notification_batch_items.target_id = ?)
					AND filings.notified_at IS NULL AND COALESCE((SELECT status FROM notification_logs WHERE notification_logs.filing_id = filings.filing_id ORDER BY created_at DESC, id DESC LIMIT 1), '') NOT IN ('success', 'failed') AND COALESCE((SELECT status FROM notification_batch_items WHERE notification_batch_items.filing_id = filings.filing_id AND notification_batch_items.target_id = 0 ORDER BY created_at DESC, id DESC LIMIT 1), '') NOT IN ('sent', 'failed'))
			)`, targetID, targetID, targetID)
		}
	} else {
		switch notificationStatus {
		case "success":
			query = query.Where("notified_at IS NOT NULL OR (SELECT status FROM notification_logs WHERE notification_logs.filing_id = filings.filing_id ORDER BY created_at DESC, id DESC LIMIT 1) = ?", "success")
		case "failed":
			query = query.Where("(SELECT status FROM notification_logs WHERE notification_logs.filing_id = filings.filing_id ORDER BY created_at DESC, id DESC LIMIT 1) = ? OR (SELECT status FROM notification_batch_items WHERE notification_batch_items.filing_id = filings.filing_id ORDER BY created_at DESC, id DESC LIMIT 1) = ?", "failed", "failed")
		case "unnotified":
			query = query.Where("notified_at IS NULL AND COALESCE((SELECT status FROM notification_logs WHERE notification_logs.filing_id = filings.filing_id ORDER BY created_at DESC, id DESC LIMIT 1), '') NOT IN ('success', 'failed') AND COALESCE((SELECT status FROM notification_batch_items WHERE notification_batch_items.filing_id = filings.filing_id ORDER BY created_at DESC, id DESC LIMIT 1), '') <> 'failed'")
		}
	}
	if filter.DateFrom != nil {
		query = query.Where("filing_date >= ?", *filter.DateFrom)
	}
	if filter.DateTo != nil {
		query = query.Where("filing_date <= ?", *filter.DateTo)
	}

	var total int64
	if err := query.Count(&total).Error; err != nil {
		return PageResult[FilingItem]{}, err
	}

	var filings []model.Filing
	if err := query.Order(filingOrder(filter.SortBy, filter.SortOrder)).Offset((page - 1) * pageSize).Limit(pageSize).Find(&filings).Error; err != nil {
		return PageResult[FilingItem]{}, err
	}
	items, err := s.withNotificationStatus(ctx, filings, targetID)
	if err != nil {
		return PageResult[FilingItem]{}, err
	}
	return newPageResult(items, total, page, pageSize), nil
}

func (s *FilingService) Get(ctx context.Context, id uint) (model.Filing, error) {
	var filing model.Filing
	if err := s.db.WithContext(ctx).First(&filing, id).Error; err != nil {
		return model.Filing{}, mapNotFound(err)
	}
	return filing, nil
}

func (s *FilingService) Refresh(ctx context.Context) (RefreshResult, error) {
	return s.RefreshWithTrigger(ctx, "manual")
}

func (s *FilingService) RefreshWithTrigger(ctx context.Context, trigger string) (RefreshResult, error) {
	return s.refreshTargets(ctx, trigger, nil)
}

func (s *FilingService) RefreshTarget(ctx context.Context, targetID uint) (RefreshResult, error) {
	var target model.WatchTarget
	if err := s.db.WithContext(ctx).First(&target, targetID).Error; err != nil {
		return RefreshResult{}, mapNotFound(err)
	}
	return s.refreshTargets(ctx, "target", []model.WatchTarget{target})
}

// RefreshTargets refreshes the supplied targets without loading the enabled-target list.
// It is used by callers that have already selected an explicit set of targets.
func (s *FilingService) RefreshTargets(ctx context.Context, targets []model.WatchTarget) (RefreshResult, error) {
	return s.refreshTargets(ctx, "targets", targets)
}

func (s *FilingService) refreshTargets(ctx context.Context, trigger string, selected []model.WatchTarget) (RefreshResult, error) {
	startedAt := time.Now().UTC()
	if strings.TrimSpace(trigger) == "" {
		trigger = "manual"
	}
	run := model.SyncRun{StartedAt: startedAt, Status: "running", Trigger: trigger}
	if err := s.db.WithContext(ctx).Create(&run).Error; err != nil {
		return RefreshResult{}, err
	}

	targets := selected
	if targets == nil {
		if err := s.db.WithContext(ctx).Where("status = ?", "enabled").Find(&targets).Error; err != nil {
			s.finishSyncRun(ctx, run.ID, RefreshResult{}, "failed", err.Error())
			return RefreshResult{}, err
		}
	}
	settings, err := s.configs.SECFetchSettings(ctx)
	if err != nil {
		s.finishSyncRun(ctx, run.ID, RefreshResult{TargetsChecked: len(targets)}, "failed", err.Error())
		return RefreshResult{}, err
	}
	notificationSettings, err := s.configs.NotificationSettings(ctx)
	if err != nil {
		s.finishSyncRun(ctx, run.ID, RefreshResult{TargetsChecked: len(targets)}, "failed", err.Error())
		return RefreshResult{}, err
	}

	result := RefreshResult{TargetsChecked: len(targets), SyncRunID: run.ID}
	notificationCandidates := make([]NotificationCandidate, 0)
	for _, target := range targets {
		detailStartedAt := time.Now().UTC()
		detail := model.SyncRunDetail{
			SyncRunID: run.ID,
			TargetID:  target.ID,
			Ticker:    target.Ticker,
			Status:    "running",
			StartedAt: detailStartedAt,
		}
		_ = s.db.WithContext(ctx).Create(&detail).Error
		targetNewFilings := 0
		cik := target.CIK
		companyName := target.CompanyName
		if cik == "" {
			foundCIK, foundName, err := s.sec.LookupCIK(ctx, target.Ticker)
			if err != nil {
				result.FailedTargets++
				s.markTargetSync(ctx, target.ID, "failed", err.Error(), 0)
				s.finishSyncRunDetail(ctx, detail.ID, "failed", 0, detailStartedAt, err.Error(), "")
				continue
			}
			cik = foundCIK
			if foundName != "" {
				companyName = foundName
			}
			_ = s.db.WithContext(ctx).Model(&target).Updates(map[string]any{"cik": cik, "company_name": companyName}).Error
		}

		filings, err := s.listFilingsWithRetry(ctx, sec.FilingQuery{Ticker: target.Ticker, CIK: cik, FetchFullHistory: settings.FetchFullHistory})
		if err != nil {
			result.FailedTargets++
			s.markTargetSync(ctx, target.ID, "failed", err.Error(), 0)
			s.finishSyncRunDetail(ctx, detail.ID, "failed", 0, detailStartedAt, err.Error(), "")
			continue
		}
		filings = applyFetchSettings(filings, target.LastSyncAt == nil, settings, time.Now().UTC())
		target.CIK = cik
		fundFilterResult, err := s.filterFundFilings(ctx, target, filings)
		if err != nil {
			result.FailedTargets++
			errorMessage := "fund identity unavailable: " + err.Error()
			s.markTargetSync(ctx, target.ID, "failed", errorMessage, 0)
			s.finishSyncRunDetail(ctx, detail.ID, "failed", 0, detailStartedAt, errorMessage, "")
			continue
		}
		filings = fundFilterResult.filings
		warning := ""
		if fundFilterResult.skipped > 0 {
			warning = fmt.Sprintf("fund identity filtered %d trust filings", fundFilterResult.skipped)
			if fundFilterResult.reasonSummary != "" {
				warning += " (" + fundFilterResult.reasonSummary + ")"
			}
		}
		for _, item := range filings {
			filing := model.Filing{
				FilingID:        item.FilingID,
				AccessionNumber: item.AccessionNumber,
				Ticker:          valueOrDefault(item.Ticker, target.Ticker),
				CIK:             valueOrDefault(item.CIK, cik),
				CompanyName:     valueOrDefault(item.CompanyName, companyName),
				FilingType:      item.FilingType,
				FilingDate:      item.FilingDate,
				PublishedAt:     item.PublishedAt,
				FilingURL:       item.FilingURL,
				Title:           item.Title,
				RawContent:      item.RawContent,
				PulledAt:        time.Now().UTC(),
			}
			created, associated, storedFiling, err := s.createFilingAndAssociate(ctx, filing, target.ID)
			if err != nil {
				s.finishSyncRunDetail(ctx, detail.ID, "failed", targetNewFilings, detailStartedAt, err.Error(), warning)
				return result, err
			}
			if created {
				result.NewFilings++
				_ = s.recordCandidateRecalcEvent(ctx, storedFiling)
			}
			if associated {
				targetNewFilings++
				notificationCandidates = append(notificationCandidates, filingNotificationCandidateForTarget(storedFiling, target, target.LastSyncAt, notificationSettings, time.Now()))
			}
		}
		s.markTargetSync(ctx, target.ID, "success", "", targetNewFilings)
		s.finishSyncRunDetail(ctx, detail.ID, "success", targetNewFilings, detailStartedAt, "", warning)
	}

	status := "success"
	if result.FailedTargets > 0 {
		status = "partial"
	}
	if len(notificationCandidates) > 0 {
		if _, err := s.batches.Deliver(ctx, NotificationBatchInput{SyncRunID: run.ID, Source: "filing", Trigger: trigger, Candidates: notificationCandidates}); err != nil {
			s.finishSyncRun(ctx, run.ID, result, "failed", err.Error())
			return result, err
		}
	}
	s.finishSyncRun(ctx, run.ID, result, status, "")
	return result, nil
}

func (s *FilingService) recordCandidateRecalcEvent(ctx context.Context, filing model.Filing) error {
	if s == nil || s.discoveryDB == nil {
		return nil
	}
	_, err := discovery.RecordCandidateRecalcEventForFiling(ctx, s.discoveryDB, discovery.CandidateRecalcFilingInput{
		FilingID: filing.FilingID, AccessionNumber: filing.AccessionNumber, Ticker: filing.Ticker, CIK: filing.CIK,
		FilingType: filing.FilingType, FilingDate: filing.FilingDate,
	})
	return err
}

func (s *FilingService) ListSyncRuns(ctx context.Context, filter SyncRunFilter) (PageResult[model.SyncRun], error) {
	page, pageSize := normalizePage(filter.Page, filter.PageSize)
	query := s.db.WithContext(ctx).Model(&model.SyncRun{})
	if filter.Status != "" {
		query = query.Where("status = ?", strings.TrimSpace(filter.Status))
	}
	if filter.Trigger != "" {
		query = query.Where("trigger = ?", strings.TrimSpace(filter.Trigger))
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return PageResult[model.SyncRun]{}, err
	}
	var runs []model.SyncRun
	err := query.Order("started_at DESC, id DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&runs).Error
	return newPageResult(runs, total, page, pageSize), err
}

func (s *FilingService) ListSyncRunDetails(ctx context.Context, syncRunID uint) ([]model.SyncRunDetail, error) {
	var details []model.SyncRunDetail
	err := s.db.WithContext(ctx).
		Where("sync_run_id = ?", syncRunID).
		Order("started_at ASC, id ASC").
		Find(&details).Error
	return details, err
}

func (s *FilingService) ListTargetSyncDetails(ctx context.Context, targetID uint, limit int) ([]model.SyncRunDetail, error) {
	if limit < 1 || limit > 20 {
		limit = 3
	}
	var details []model.SyncRunDetail
	err := s.db.WithContext(ctx).
		Where("target_id = ?", targetID).
		Order("started_at DESC, id DESC").
		Limit(limit).
		Find(&details).Error
	return details, err
}

func (s *FilingService) withNotificationStatus(ctx context.Context, filings []model.Filing, targetID uint) ([]FilingItem, error) {
	items := make([]FilingItem, 0, len(filings))
	if len(filings) == 0 {
		return items, nil
	}
	filingIDs := make([]string, 0, len(filings))
	for _, filing := range filings {
		items = append(items, FilingItem{Filing: filing})
		filingIDs = append(filingIDs, filing.FilingID)
	}
	targetSpecific := map[string]bool{}
	if targetID != 0 {
		var batchItems []model.NotificationBatchItem
		if err := s.db.WithContext(ctx).Where("filing_id IN ? AND target_id = ?", filingIDs, targetID).Order("created_at DESC, id DESC").Find(&batchItems).Error; err != nil {
			return nil, err
		}
		latest := map[string]model.NotificationBatchItem{}
		for _, batchItem := range batchItems {
			if _, exists := latest[batchItem.FilingID]; !exists {
				latest[batchItem.FilingID] = batchItem
			}
		}
		for i := range items {
			if batchItem, ok := latest[items[i].FilingID]; ok {
				targetSpecific[items[i].FilingID] = true
				switch batchItem.Status {
				case "sent":
					items[i].NotificationStatus = "success"
				case "failed":
					items[i].NotificationStatus = "failed"
				}
			}
		}
	}
	var logs []model.NotificationLog
	if err := s.db.WithContext(ctx).
		Where("filing_id IN ?", filingIDs).
		Order("created_at DESC, id DESC").
		Find(&logs).Error; err != nil {
		return nil, err
	}
	var batchItems []model.NotificationBatchItem
	batchQuery := s.db.WithContext(ctx).Where("filing_id IN ?", filingIDs)
	if targetID != 0 {
		batchQuery = batchQuery.Where("target_id = ?", 0)
	}
	if err := batchQuery.
		Order("created_at DESC, id DESC").
		Find(&batchItems).Error; err != nil {
		return nil, err
	}
	latest := map[string]model.NotificationLog{}
	for _, log := range logs {
		if _, exists := latest[log.FilingID]; !exists {
			latest[log.FilingID] = log
		}
	}
	for i := range items {
		if targetSpecific[items[i].FilingID] {
			continue
		}
		if items[i].NotifiedAt != nil {
			items[i].NotificationStatus = "success"
			continue
		}
		if log, ok := latest[items[i].FilingID]; ok {
			items[i].NotificationStatus = log.Status
			items[i].NotificationLogID = log.ID
		}
	}
	latestBatch := map[string]model.NotificationBatchItem{}
	for _, item := range batchItems {
		if _, exists := latestBatch[item.FilingID]; !exists {
			latestBatch[item.FilingID] = item
		}
	}
	for i := range items {
		if targetSpecific[items[i].FilingID] {
			continue
		}
		if items[i].NotificationStatus != "" {
			continue
		}
		if batchItem, ok := latestBatch[items[i].FilingID]; ok {
			switch batchItem.Status {
			case "sent":
				items[i].NotificationStatus = "success"
			case "failed":
				items[i].NotificationStatus = "failed"
			}
		}
	}
	return items, nil
}

func (s *FilingService) CleanupPreview(ctx context.Context, retentionDays int, now time.Time) (CleanupPreview, error) {
	if retentionDays < 1 {
		return CleanupPreview{}, fmt.Errorf("%w: retention_days must be greater than 0", ErrValidation)
	}
	cutoff := now.UTC().AddDate(0, 0, -retentionDays)
	preview := CleanupPreview{RetentionDays: retentionDays, Cutoff: cutoff}
	query := s.db.WithContext(ctx).Model(&model.Filing{}).Where("pulled_at < ?", cutoff)
	if err := query.Count(&preview.DeleteCount).Error; err != nil {
		return CleanupPreview{}, err
	}
	if preview.DeleteCount == 0 {
		return preview, nil
	}
	var oldest model.Filing
	if err := query.Order("pulled_at ASC, id ASC").First(&oldest).Error; err != nil {
		return CleanupPreview{}, err
	}
	var newest model.Filing
	if err := query.Order("pulled_at DESC, id DESC").First(&newest).Error; err != nil {
		return CleanupPreview{}, err
	}
	preview.OldestPulledAt = &oldest.PulledAt
	preview.NewestPulledAt = &newest.PulledAt
	return preview, nil
}

func (s *FilingService) Cleanup(ctx context.Context, retentionDays int, now time.Time) (int64, error) {
	if retentionDays < 1 {
		return 0, fmt.Errorf("%w: retention_days must be greater than 0", ErrValidation)
	}
	cutoff := now.UTC().AddDate(0, 0, -retentionDays)
	var deleted int64
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var filings []model.Filing
		if err := tx.Where("pulled_at < ?", cutoff).Select("id").Find(&filings).Error; err != nil {
			return err
		}
		if len(filings) == 0 {
			return nil
		}
		filingIDs := make([]uint, 0, len(filings))
		for _, filing := range filings {
			filingIDs = append(filingIDs, filing.ID)
		}
		if err := tx.Where("filing_id IN ?", filingIDs).Delete(&model.WatchTargetFiling{}).Error; err != nil {
			return err
		}
		res := tx.Where("id IN ?", filingIDs).Delete(&model.Filing{})
		deleted = res.RowsAffected
		return res.Error
	})
	return deleted, err
}

func (s *FilingService) markTargetSync(ctx context.Context, targetID uint, status string, errorMessage string, newFilings int) {
	now := time.Now().UTC()
	_ = s.db.WithContext(ctx).Model(&model.WatchTarget{}).Where("id = ?", targetID).Updates(map[string]any{
		"last_sync_at":     &now,
		"last_sync_status": status,
		"last_sync_error":  errorMessage,
		"last_new_filings": newFilings,
	}).Error
}

func (s *FilingService) finishSyncRun(ctx context.Context, id uint, result RefreshResult, status string, errorMessage string) {
	finishedAt := time.Now().UTC()
	_ = s.db.WithContext(ctx).Model(&model.SyncRun{}).Where("id = ?", id).Updates(map[string]any{
		"finished_at":     &finishedAt,
		"status":          status,
		"targets_checked": result.TargetsChecked,
		"new_filings":     result.NewFilings,
		"failed_targets":  result.FailedTargets,
		"error_message":   errorMessage,
	}).Error
}

func (s *FilingService) finishSyncRunDetail(ctx context.Context, id uint, status string, newFilings int, startedAt time.Time, errorMessage, warningMessage string) {
	if id == 0 {
		return
	}
	finishedAt := time.Now().UTC()
	_ = s.db.WithContext(ctx).Model(&model.SyncRunDetail{}).Where("id = ?", id).Updates(map[string]any{
		"finished_at":     &finishedAt,
		"status":          status,
		"new_filings":     newFilings,
		"duration_ms":     finishedAt.Sub(startedAt).Milliseconds(),
		"error_message":   errorMessage,
		"warning_message": warningMessage,
	}).Error
}

func (s *FilingService) filterFundFilings(ctx context.Context, target model.WatchTarget, filings []sec.FilingResult) (fundFilingFilterResult, error) {
	seriesID := strings.TrimSpace(target.FundSeriesID)
	classID := strings.TrimSpace(target.FundClassID)
	if seriesID == "" && classID == "" {
		return fundFilingFilterResult{filings: filings}, nil
	}
	if seriesID == "" || classID == "" {
		return fundFilingFilterResult{}, fmt.Errorf("fund identity is incomplete")
	}
	fundClient, ok := s.sec.(sec.FundIdentityClient)
	if !ok {
		return fundFilingFilterResult{}, fmt.Errorf("SEC client does not support fund filing identity matching")
	}

	identity := sec.FundIdentity{
		Ticker: target.Ticker, CIK: target.CIK, SeriesID: seriesID, ClassID: classID,
	}
	metadataClient, hasMetadataClient := s.sec.(sec.FundFilingMetadataClient)
	filtered := make([]sec.FilingResult, 0, len(filings))
	skipped := 0
	reasonCounts := map[string]int{}
	for _, filing := range filings {
		matched, reason, cached, err := s.cachedFundFilingMatch(ctx, identity, filing)
		if err != nil {
			return fundFilingFilterResult{}, err
		}
		if !cached && hasMetadataClient {
			metadata, parseErr := metadataClient.ParseFundFiling(ctx, filing)
			if parseErr != nil {
				_ = s.storeFundFilingParseFailure(ctx, identity, filing, parseErr.Error())
				return fundFilingFilterResult{}, parseErr
			}
			if metadata.Incomplete || len(metadata.Relationships) == 0 {
				_ = s.storeFundFilingParseFailure(ctx, identity, filing, "filing_identity_incomplete")
				// Exact fund identity must fail closed for this one filing, but an
				// incomplete SEC index is not a reason to fail the entire ETF
				// target. The failed parse is cached briefly and retried later.
				matched, reason = false, "filing_identity_incomplete"
			} else {
				if err := s.storeFundFilingMetadata(ctx, identity, filing, metadata); err != nil {
					return fundFilingFilterResult{}, err
				}
				matched, reason = matchFundFilingRelationships(identity, metadata.Relationships)
			}
		}
		if !cached && !hasMetadataClient {
			matched, reason, err = fundClient.MatchFundFiling(ctx, identity, filing)
			if err != nil {
				_ = s.storeFundFilingMatch(ctx, identity, filing, "failed", reason)
				return fundFilingFilterResult{}, err
			}
			if !fundFilingMatchIsConsistent(matched, reason) {
				_ = s.storeFundFilingMatch(ctx, identity, filing, "failed", reason)
				return fundFilingFilterResult{}, fmt.Errorf("filing %s identity result is inconsistent: matched=%t reason=%s", filing.AccessionNumber, matched, reason)
			}
			status := "unmatched"
			if matched {
				status = "matched"
			}
			if err := s.storeFundFilingMatch(ctx, identity, filing, status, reason); err != nil {
				return fundFilingFilterResult{}, err
			}
		}
		if matched {
			filtered = append(filtered, filing)
			continue
		}
		skipped++
		reasonCounts[reason]++
	}
	return fundFilingFilterResult{filings: filtered, skipped: skipped, reasonSummary: summarizeFundFilingSkipReasons(reasonCounts)}, nil
}

func fundFilingMatchReasonVerified(reason string) bool {
	return reason == "matched_class" || reason == "series_not_found" || reason == "class_not_found"
}

func fundFilingMatchIsConsistent(matched bool, reason string) bool {
	if matched {
		return reason == "matched_class"
	}
	return reason == "series_not_found" || reason == "class_not_found"
}

func summarizeFundFilingSkipReasons(reasonCounts map[string]int) string {
	const maxReasons = 4
	reasons := make([]string, 0, len(reasonCounts))
	for reason, count := range reasonCounts {
		if count > 0 && (fundFilingMatchReasonVerified(reason) || reason == "filing_identity_incomplete") {
			reasons = append(reasons, reason)
		}
	}
	sort.Strings(reasons)
	parts := make([]string, 0, min(len(reasons), maxReasons))
	for index, reason := range reasons {
		if index == maxReasons {
			parts = append(parts, fmt.Sprintf("+%d more", len(reasons)-maxReasons))
			break
		}
		parts = append(parts, fmt.Sprintf("%s: %d", reason, reasonCounts[reason]))
	}
	return strings.Join(parts, ", ")
}

func (s *FilingService) cachedFundFilingMatch(ctx context.Context, identity sec.FundIdentity, filing sec.FilingResult) (bool, string, bool, error) {
	if strings.TrimSpace(filing.AccessionNumber) == "" {
		return false, "", false, nil
	}
	var cached model.FundFilingIdentity
	err := s.db.WithContext(ctx).Where("cik = ? AND accession_number = ?", identity.CIK, filing.AccessionNumber).First(&cached).Error
	if err == gorm.ErrRecordNotFound {
		return false, "", false, nil
	}
	if err != nil {
		return false, "", false, err
	}
	if strings.TrimSpace(cached.RelationshipsJSON) != "" {
		var relationships []sec.FundFilingRelationship
		if err := json.Unmarshal([]byte(cached.RelationshipsJSON), &relationships); err != nil || len(relationships) == 0 {
			return false, "", true, fmt.Errorf("cached fund filing metadata is invalid")
		}
		if cached.ParseStatus != "parsed" {
			return false, "", true, fmt.Errorf("cached fund filing metadata is unavailable: %s", cached.ParseMessage)
		}
		matched, reason := matchFundFilingRelationships(identity, relationships)
		return matched, reason, true, nil
	}
	if cached.ParseStatus == "failed" {
		if cached.ParseMessage == "filing_identity_incomplete" {
			if cached.CheckedAt.Add(fundFilingIdentityIncompleteRetryAfter).After(time.Now().UTC()) {
				return false, cached.ParseMessage, true, nil
			}
			// The SEC index can be completed or repaired after its first
			// publication. Re-parse this accession after the short cooldown.
			return false, "", false, nil
		}
		return false, "", true, fmt.Errorf("cached fund filing metadata is unavailable: %s", cached.ParseMessage)
	}
	var seriesIDs, classIDs []string
	if json.Unmarshal([]byte(cached.SeriesIDsJSON), &seriesIDs) != nil || json.Unmarshal([]byte(cached.ClassIDsJSON), &classIDs) != nil || len(seriesIDs) != 1 || len(classIDs) != 1 || seriesIDs[0] != identity.SeriesID || classIDs[0] != identity.ClassID {
		return false, "", false, nil
	}
	switch cached.ParseStatus {
	case "matched":
		if fundFilingMatchIsConsistent(true, cached.ParseMessage) {
			return true, cached.ParseMessage, true, nil
		}
		return false, "", true, fmt.Errorf("cached fund filing identity result is inconsistent")
	case "unmatched":
		if fundFilingMatchIsConsistent(false, cached.ParseMessage) {
			return false, cached.ParseMessage, true, nil
		}
		return false, "", true, fmt.Errorf("cached fund filing identity result is inconsistent")
	default:
		return false, "", false, nil
	}
}

func matchFundFilingRelationships(identity sec.FundIdentity, relationships []sec.FundFilingRelationship) (bool, string) {
	seriesFound := false
	for _, relationship := range relationships {
		if relationship.SeriesID != identity.SeriesID {
			continue
		}
		seriesFound = true
		if relationship.ClassID == identity.ClassID {
			return true, "matched_class"
		}
	}
	if !seriesFound {
		return false, "series_not_found"
	}
	return false, "class_not_found"
}

func (s *FilingService) storeFundFilingMatch(ctx context.Context, identity sec.FundIdentity, filing sec.FilingResult, status, message string) error {
	if strings.TrimSpace(filing.AccessionNumber) == "" {
		return nil
	}
	seriesIDsJSON, err := json.Marshal([]string{identity.SeriesID})
	if err != nil {
		return err
	}
	classIDsJSON, err := json.Marshal([]string{identity.ClassID})
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	return s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "cik"}, {Name: "accession_number"}},
		DoUpdates: clause.Assignments(map[string]any{
			"series_ids_json": string(seriesIDsJSON),
			"class_ids_json":  string(classIDsJSON),
			"parse_status":    status,
			"parse_message":   message,
			"checked_at":      now,
		}),
	}).Create(&model.FundFilingIdentity{
		CIK: identity.CIK, AccessionNumber: filing.AccessionNumber,
		SeriesIDsJSON: string(seriesIDsJSON), ClassIDsJSON: string(classIDsJSON),
		ParseStatus: status, ParseMessage: message, CheckedAt: now,
	}).Error
}

func (s *FilingService) storeFundFilingMetadata(ctx context.Context, identity sec.FundIdentity, filing sec.FilingResult, metadata sec.FundFilingMetadata) error {
	relationshipsJSON, err := json.Marshal(metadata.Relationships)
	if err != nil {
		return err
	}
	seriesIDs := make([]string, 0, len(metadata.Relationships))
	classIDs := make([]string, 0, len(metadata.Relationships))
	seenSeries, seenClass := map[string]bool{}, map[string]bool{}
	for _, relationship := range metadata.Relationships {
		if !seenSeries[relationship.SeriesID] {
			seenSeries[relationship.SeriesID] = true
			seriesIDs = append(seriesIDs, relationship.SeriesID)
		}
		if !seenClass[relationship.ClassID] {
			seenClass[relationship.ClassID] = true
			classIDs = append(classIDs, relationship.ClassID)
		}
	}
	seriesJSON, err := json.Marshal(seriesIDs)
	if err != nil {
		return err
	}
	classJSON, err := json.Marshal(classIDs)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	return s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "cik"}, {Name: "accession_number"}},
		DoUpdates: clause.Assignments(map[string]any{
			"series_ids_json": string(seriesJSON), "class_ids_json": string(classJSON), "relationships_json": string(relationshipsJSON),
			"parse_status": "parsed", "parse_message": "", "checked_at": now,
		}),
	}).Create(&model.FundFilingIdentity{CIK: identity.CIK, AccessionNumber: filing.AccessionNumber, SeriesIDsJSON: string(seriesJSON), ClassIDsJSON: string(classJSON), RelationshipsJSON: string(relationshipsJSON), ParseStatus: "parsed", CheckedAt: now}).Error
}

func (s *FilingService) storeFundFilingParseFailure(ctx context.Context, identity sec.FundIdentity, filing sec.FilingResult, message string) error {
	now := time.Now().UTC()
	return s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "cik"}, {Name: "accession_number"}},
		DoUpdates: clause.Assignments(map[string]any{"parse_status": "failed", "parse_message": message, "checked_at": now}),
	}).Create(&model.FundFilingIdentity{CIK: identity.CIK, AccessionNumber: filing.AccessionNumber, ParseStatus: "failed", ParseMessage: message, CheckedAt: now}).Error
}

func (s *FilingService) createFilingIfNew(ctx context.Context, filing model.Filing) (bool, error) {
	if filing.FilingID == "" {
		return false, fmt.Errorf("%w: filing_id is required", ErrValidation)
	}
	res := s.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&filing)
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected == 1, nil
}

func (s *FilingService) createFilingAndAssociate(ctx context.Context, filing model.Filing, targetID uint) (bool, bool, model.Filing, error) {
	created, err := s.createFilingIfNew(ctx, filing)
	if err != nil {
		return false, false, model.Filing{}, err
	}
	var stored model.Filing
	if err := s.db.WithContext(ctx).Where("filing_id = ?", filing.FilingID).First(&stored).Error; err != nil {
		return false, false, model.Filing{}, err
	}
	if targetID == 0 {
		return created, created, stored, nil
	}
	association := model.WatchTargetFiling{TargetID: targetID, FilingID: stored.ID}
	result := s.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&association)
	if result.Error != nil {
		return false, false, model.Filing{}, result.Error
	}
	return created, result.RowsAffected == 1, stored, nil
}

func (s *FilingService) listFilingsWithRetry(ctx context.Context, query sec.FilingQuery) ([]sec.FilingResult, error) {
	var filings []sec.FilingResult
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		filings, err = s.sec.ListFilings(ctx, query)
		if err == nil {
			return filings, nil
		}
		time.Sleep(time.Duration(attempt+1) * 200 * time.Millisecond)
	}
	return nil, err
}

func filingOrder(sortBy string, sortOrder string) string {
	columns := map[string]string{
		"filing_date":  "filing_date",
		"published_at": "published_at",
		"pulled_at":    "pulled_at",
		"ticker":       "ticker",
		"filing_type":  "filing_type",
	}
	column := columns[strings.TrimSpace(sortBy)]
	if column == "" {
		column = "filing_date"
	}
	direction := "DESC"
	if strings.EqualFold(strings.TrimSpace(sortOrder), "asc") || strings.EqualFold(strings.TrimSpace(sortOrder), "ascending") {
		direction = "ASC"
	}
	return column + " " + direction + ", id DESC"
}

func applyFetchSettings(filings []sec.FilingResult, firstSync bool, settings SECFetchSettings, now time.Time) []sec.FilingResult {
	filtered := make([]sec.FilingResult, 0, len(filings))
	cutoff := time.Time{}
	if settings.SyncWindowDays > 0 {
		cutoff = now.AddDate(0, 0, -settings.SyncWindowDays)
	} else if firstSync && settings.InitialFetchDays > 0 {
		cutoff = now.AddDate(0, 0, -settings.InitialFetchDays)
	}
	for _, filing := range filings {
		if !cutoff.IsZero() && !filing.FilingDate.IsZero() && filing.FilingDate.Before(cutoff) {
			continue
		}
		filtered = append(filtered, filing)
	}
	sort.SliceStable(filtered, func(i, j int) bool {
		return filtered[i].FilingDate.After(filtered[j].FilingDate)
	})
	if settings.MaxFetchCount > 0 && len(filtered) > settings.MaxFetchCount {
		return filtered[:settings.MaxFetchCount]
	}
	return filtered
}

func shouldNotifyFiling(filing model.Filing, settings NotificationSettings, now time.Time) bool {
	if settings.QuietHoursEnabled && inQuietHours(now, settings.QuietHoursStart, settings.QuietHoursEnd) {
		return false
	}
	filingType := strings.ToUpper(strings.TrimSpace(filing.FilingType))
	if settings.ImportantOnly && !isImportantFilingType(filingType) {
		return false
	}
	if len(settings.FilingTypes) > 0 {
		matched := false
		for _, item := range settings.FilingTypes {
			normalized := strings.ToUpper(strings.TrimSpace(item))
			if normalized != "" && filingType == normalized {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	if len(settings.Keywords) > 0 {
		haystack := strings.ToLower(filing.Title + " " + filing.CompanyName + " " + filing.RawContent)
		matched := false
		for _, keyword := range settings.Keywords {
			normalized := strings.ToLower(strings.TrimSpace(keyword))
			if normalized != "" && strings.Contains(haystack, normalized) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	return true
}

func filingNotificationCandidate(filing model.Filing, previousSync *time.Time, settings NotificationSettings, now time.Time) NotificationCandidate {
	eventAt := filing.FilingDate
	if filing.PublishedAt != nil {
		eventAt = *filing.PublishedAt
	}
	return NotificationCandidate{
		EntityKind: "filing", FilingID: filing.FilingID, Ticker: filing.Ticker, CIK: filing.CIK,
		CompanyName: filing.CompanyName, FilingType: filing.FilingType, Title: filing.Title,
		FilingURL: filing.FilingURL, EventAt: eventAt,
		Reason: filingNotificationReason(filing, previousSync, settings, now),
	}
}

func filingNotificationCandidateForTarget(filing model.Filing, target model.WatchTarget, previousSync *time.Time, settings NotificationSettings, now time.Time) NotificationCandidate {
	candidate := filingNotificationCandidate(filing, previousSync, settings, now)
	candidate.TargetID = target.ID
	candidate.Ticker = target.Ticker
	candidate.CIK = valueOrDefault(target.CIK, filing.CIK)
	candidate.CompanyName = valueOrDefault(target.CompanyName, filing.CompanyName)
	return candidate
}

func filingNotificationReason(filing model.Filing, previousSync *time.Time, settings NotificationSettings, now time.Time) string {
	if previousSync == nil {
		return "initial_sync"
	}
	if filing.PublishedAt != nil {
		if filing.PublishedAt.Before(*previousSync) {
			return "history_backfill"
		}
	} else {
		filingDate := filing.FilingDate.UTC()
		previousDate := previousSync.UTC()
		if filingDate.Year() != previousDate.Year() || filingDate.YearDay() < previousDate.YearDay() {
			if filingDate.Before(time.Date(previousDate.Year(), previousDate.Month(), previousDate.Day(), 0, 0, 0, 0, time.UTC)) {
				return "history_backfill"
			}
		}
	}
	if settings.QuietHoursEnabled && inQuietHours(now, settings.QuietHoursStart, settings.QuietHoursEnd) {
		return "quiet_hours"
	}
	settings.QuietHoursEnabled = false
	if !shouldNotifyFiling(filing, settings, now) {
		return "rule_filtered"
	}
	return "eligible"
}

func isImportantFilingType(value string) bool {
	for _, item := range []string{"8-K", "10-K", "10-Q", "S-1", "S-3", "424B", "4", "3", "5", "13D", "13G"} {
		if value == item || strings.HasPrefix(value, item) {
			return true
		}
	}
	return false
}

func inQuietHours(now time.Time, start string, end string) bool {
	startMinute, okStart := parseClockMinute(start)
	endMinute, okEnd := parseClockMinute(end)
	if !okStart || !okEnd || startMinute == endMinute {
		return false
	}
	current := now.Hour()*60 + now.Minute()
	if startMinute < endMinute {
		return current >= startMinute && current < endMinute
	}
	return current >= startMinute || current < endMinute
}

func parseClockMinute(value string) (int, bool) {
	parsed, err := time.Parse("15:04", strings.TrimSpace(value))
	if err != nil {
		return 0, false
	}
	return parsed.Hour()*60 + parsed.Minute(), true
}

func sendWithRetry(ctx context.Context, notifier telegram.Notifier, message telegram.Message, attempts int) error {
	var err error
	for attempt := 0; attempt < attempts; attempt++ {
		err = notifier.Send(ctx, message)
		if err == nil {
			return nil
		}
		time.Sleep(time.Duration(attempt+1) * 200 * time.Millisecond)
	}
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s", SanitizeSensitiveError(err.Error()))
}
