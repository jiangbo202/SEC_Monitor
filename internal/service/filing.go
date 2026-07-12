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
	if filter.Ticker != "" {
		query = query.Where("ticker = ?", strings.ToUpper(strings.TrimSpace(filter.Ticker)))
	}
	if filter.CompanyName != "" {
		query = query.Where("company_name LIKE ?", "%"+strings.TrimSpace(filter.CompanyName)+"%")
	}
	if filter.FilingType != "" {
		query = query.Where("filing_type = ?", strings.TrimSpace(filter.FilingType))
	}
	notificationStatus := strings.ToLower(strings.TrimSpace(filter.NotificationStatus))
	switch notificationStatus {
	case "success":
		query = query.Where("notified_at IS NOT NULL OR (SELECT status FROM notification_logs WHERE notification_logs.filing_id = filings.filing_id ORDER BY created_at DESC, id DESC LIMIT 1) = ?", "success")
	case "failed":
		query = query.Where("(SELECT status FROM notification_logs WHERE notification_logs.filing_id = filings.filing_id ORDER BY created_at DESC, id DESC LIMIT 1) = ? OR (SELECT status FROM notification_batch_items WHERE notification_batch_items.filing_id = filings.filing_id ORDER BY created_at DESC, id DESC LIMIT 1) = ?", "failed", "failed")
	case "unnotified":
		query = query.Where("notified_at IS NULL AND COALESCE((SELECT status FROM notification_logs WHERE notification_logs.filing_id = filings.filing_id ORDER BY created_at DESC, id DESC LIMIT 1), '') NOT IN ('success', 'failed') AND COALESCE((SELECT status FROM notification_batch_items WHERE notification_batch_items.filing_id = filings.filing_id ORDER BY created_at DESC, id DESC LIMIT 1), '') <> 'failed'")
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
	items, err := s.withNotificationStatus(ctx, filings)
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
		filings, skipped, err := s.filterFundFilings(ctx, target, filings)
		if err != nil {
			result.FailedTargets++
			errorMessage := "fund identity unavailable: " + err.Error()
			s.markTargetSync(ctx, target.ID, "failed", errorMessage, 0)
			s.finishSyncRunDetail(ctx, detail.ID, "failed", 0, detailStartedAt, errorMessage, "")
			continue
		}
		warning := ""
		if skipped > 0 {
			warning = fmt.Sprintf("fund identity filtered %d trust filings", skipped)
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
			created, err := s.createFilingIfNew(ctx, filing)
			if err != nil {
				s.finishSyncRunDetail(ctx, detail.ID, "failed", targetNewFilings, detailStartedAt, err.Error(), warning)
				return result, err
			}
			if created {
				result.NewFilings++
				targetNewFilings++
				_ = s.recordCandidateRecalcEvent(ctx, filing)
				notificationCandidates = append(notificationCandidates, filingNotificationCandidate(filing, target.LastSyncAt, notificationSettings, time.Now()))
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

func (s *FilingService) withNotificationStatus(ctx context.Context, filings []model.Filing) ([]FilingItem, error) {
	items := make([]FilingItem, 0, len(filings))
	if len(filings) == 0 {
		return items, nil
	}
	filingIDs := make([]string, 0, len(filings))
	for _, filing := range filings {
		items = append(items, FilingItem{Filing: filing})
		filingIDs = append(filingIDs, filing.FilingID)
	}
	var logs []model.NotificationLog
	if err := s.db.WithContext(ctx).
		Where("filing_id IN ?", filingIDs).
		Order("created_at DESC, id DESC").
		Find(&logs).Error; err != nil {
		return nil, err
	}
	var batchItems []model.NotificationBatchItem
	if err := s.db.WithContext(ctx).
		Where("filing_id IN ?", filingIDs).
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
		if items[i].NotificationStatus != "" {
			continue
		}
		if batchItem, ok := latestBatch[items[i].FilingID]; ok && batchItem.Status == "failed" {
			items[i].NotificationStatus = "failed"
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
	res := s.db.WithContext(ctx).Where("pulled_at < ?", cutoff).Delete(&model.Filing{})
	return res.RowsAffected, res.Error
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

func (s *FilingService) filterFundFilings(ctx context.Context, target model.WatchTarget, filings []sec.FilingResult) ([]sec.FilingResult, int, error) {
	seriesID := strings.TrimSpace(target.FundSeriesID)
	classID := strings.TrimSpace(target.FundClassID)
	if seriesID == "" && classID == "" {
		return filings, 0, nil
	}
	if seriesID == "" || classID == "" {
		return nil, 0, fmt.Errorf("fund identity is incomplete")
	}
	fundClient, ok := s.sec.(sec.FundIdentityClient)
	if !ok {
		return nil, 0, fmt.Errorf("SEC client does not support fund filing identity matching")
	}

	identity := sec.FundIdentity{
		Ticker: target.Ticker, CIK: target.CIK, SeriesID: seriesID, ClassID: classID,
	}
	filtered := make([]sec.FilingResult, 0, len(filings))
	skipped := 0
	for _, filing := range filings {
		matched, cached, err := s.cachedFundFilingMatch(ctx, identity, filing)
		if err != nil {
			return nil, 0, err
		}
		if !cached {
			var reason string
			matched, reason, err = fundClient.MatchFundFiling(ctx, identity, filing)
			if err != nil {
				_ = s.storeFundFilingMatch(ctx, identity, filing, "failed", reason)
				return nil, 0, err
			}
			if !fundFilingMatchReasonVerified(reason) {
				_ = s.storeFundFilingMatch(ctx, identity, filing, "failed", reason)
				return nil, 0, fmt.Errorf("filing %s identity cannot be verified: %s", filing.AccessionNumber, reason)
			}
			status := "unmatched"
			if matched {
				status = "matched"
			}
			if err := s.storeFundFilingMatch(ctx, identity, filing, status, reason); err != nil {
				return nil, 0, err
			}
		}
		if matched {
			filtered = append(filtered, filing)
			continue
		}
		skipped++
	}
	return filtered, skipped, nil
}

func fundFilingMatchReasonVerified(reason string) bool {
	return reason == "matched_class" || reason == "series_not_found" || reason == "class_not_found"
}

func (s *FilingService) cachedFundFilingMatch(ctx context.Context, identity sec.FundIdentity, filing sec.FilingResult) (bool, bool, error) {
	if strings.TrimSpace(filing.AccessionNumber) == "" {
		return false, false, nil
	}
	var cached model.FundFilingIdentity
	err := s.db.WithContext(ctx).Where("cik = ? AND accession_number = ?", identity.CIK, filing.AccessionNumber).First(&cached).Error
	if err == gorm.ErrRecordNotFound {
		return false, false, nil
	}
	if err != nil {
		return false, false, err
	}
	var seriesIDs, classIDs []string
	if json.Unmarshal([]byte(cached.SeriesIDsJSON), &seriesIDs) != nil || json.Unmarshal([]byte(cached.ClassIDsJSON), &classIDs) != nil || len(seriesIDs) != 1 || len(classIDs) != 1 || seriesIDs[0] != identity.SeriesID || classIDs[0] != identity.ClassID {
		return false, false, nil
	}
	switch cached.ParseStatus {
	case "matched":
		return true, true, nil
	case "unmatched":
		return false, true, nil
	default:
		return false, false, nil
	}
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
