package service

import (
	"context"
	"crypto/sha256"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"sec_monitor/internal/config"
	"sec_monitor/internal/model"
	"sec_monitor/internal/sec"
	"sec_monitor/internal/telegram"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type IPORadarService struct {
	db                             *gorm.DB
	sec                            sec.CurrentFilingsClient
	configs                        *ConfigService
	batches                        *NotificationBatchService
	inApp                          *InAppNotificationService
	longbridgeRuntime              config.DiscoveryConfig
	newLongbridgeListingClient     func(string, string, string) (longbridgeIPOListingClient, error)
	newLongbridgeIPOCalendarClient func(string, string, string) (longbridgeIPOCalendarClient, error)
}

type IPOFilingFilter struct {
	CompanyName string
	CIK         string
	FilingType  string
	Notified    string
	Sort        string
	Page        int
	PageSize    int
}

type IPOCompanyFilter struct {
	CompanyName  string
	CIK          string
	Ticker       string
	Status       string
	Attention    string
	IncludeEnded bool
	Followed     *bool
	SortBy       string
	SortOrder    string
	Page         int
	PageSize     int
}

type IPORadarHealth struct {
	PendingListing            int                 `json:"pending_listing"`
	MissingMarketMapping      int                 `json:"missing_market_mapping"`
	StaleLifecycleChecks      int                 `json:"stale_lifecycle_checks"`
	UnsupportedOfferingEvents int                 `json:"unsupported_offering_events"`
	FailedNotificationBatches int                 `json:"failed_notification_batches"`
	DueRetryBatches           int                 `json:"due_retry_batches"`
	DeadLetterBatches         int                 `json:"dead_letter_batches"`
	LatestSync                *IPORadarSyncHealth `json:"latest_sync"`
	Actions                   []IPORadarAction    `json:"actions"`
}

// IPORadarAction is an operator-facing next step derived from the IPO health
// counters. Keys intentionally remain stable so that API clients can localize
// the copy while the service owns the priority and routing semantics.
type IPORadarAction struct {
	Key         string `json:"key"`
	Severity    string `json:"severity"`
	Disposition string `json:"disposition"`
	Count       int    `json:"count"`
	Attention   string `json:"attention,omitempty"`
	Route       string `json:"route,omitempty"`
	Status      string `json:"status,omitempty"`
}

type IPORadarSyncHealth struct {
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at"`
	Status     string     `json:"status"`
	NewFilings int        `json:"new_filings"`
}

type IPOCompanyItem struct {
	CIK                          string     `json:"cik"`
	CompanyName                  string     `json:"company_name"`
	Status                       string     `json:"status"`
	FirstFilingDate              time.Time  `json:"first_filing_date"`
	LatestFilingDate             time.Time  `json:"latest_filing_date"`
	LatestAcceptedAt             *time.Time `json:"latest_accepted_at"`
	LatestFilingType             string     `json:"latest_filing_type"`
	LatestFilingURL              string     `json:"latest_filing_url"`
	LatestTitle                  string     `json:"latest_title"`
	FilingCount                  int        `json:"filing_count"`
	Notified                     bool       `json:"notified"`
	MatchedTicker                string     `json:"matched_ticker,omitempty"`
	StatusReason                 string     `json:"status_reason"`
	StatusConfidence             string     `json:"status_confidence"`
	StatusSource                 string     `json:"status_source"`
	FinalTicker                  string     `json:"final_ticker,omitempty"`
	Exchange                     string     `json:"exchange,omitempty"`
	OfferPrice                   string     `json:"offer_price,omitempty"`
	SharesOffered                int64      `json:"shares_offered,omitempty"`
	GrossProceeds                string     `json:"gross_proceeds,omitempty"`
	ListedVerifiedAt             *time.Time `json:"listed_verified_at,omitempty"`
	ListingDate                  *time.Time `json:"listing_date,omitempty"`
	LongbridgeListingCheckCount  int        `json:"longbridge_listing_check_count,omitempty"`
	LongbridgeListingLastResult  string     `json:"longbridge_listing_last_result,omitempty"`
	LongbridgeListingNextRetryAt *time.Time `json:"longbridge_listing_next_retry_at,omitempty"`
	MarketDataSource             string     `json:"market_data_source,omitempty"`
	MarketDataConfidence         string     `json:"market_data_confidence,omitempty"`
	MarketDataUpdatedAt          *time.Time `json:"market_data_updated_at,omitempty"`
	AutomaticTicker              string     `json:"automatic_ticker,omitempty"`
	AutomaticExchange            string     `json:"automatic_exchange,omitempty"`
	AutomaticOfferPrice          string     `json:"automatic_offer_price,omitempty"`
	AutomaticShares              int64      `json:"automatic_shares_offered,omitempty"`
	AutomaticGross               string     `json:"automatic_gross_proceeds,omitempty"`
	LifecycleCheckedAt           *time.Time `json:"lifecycle_checked_at,omitempty"`
	OverrideFinalTicker          string     `json:"override_final_ticker,omitempty"`
	OverrideExchange             string     `json:"override_exchange,omitempty"`
	OverrideOfferPrice           string     `json:"override_offer_price,omitempty"`
	OverrideShares               int64      `json:"override_shares_offered,omitempty"`
	OverrideListingDate          *time.Time `json:"override_listing_date,omitempty"`
	OverrideNote                 string     `json:"override_note,omitempty"`
	OverrideUpdatedAt            *time.Time `json:"override_updated_at,omitempty"`
	Followed                     bool       `json:"followed"`
}

type IPORadarRefreshResult struct {
	Checked    int  `json:"checked"`
	NewFilings int  `json:"new_filings"`
	Notified   int  `json:"notified"`
	SyncRunID  uint `json:"sync_run_id"`
}

// IPOReconciliationResult is the concise, task-level outcome of one isolated
// compensation stage. It is intentionally separate from the primary filing
// scan so slow provider work cannot hide whether SEC ingestion succeeded.
type IPOReconciliationResult struct {
	Checked   int    `json:"checked"`
	Updated   int    `json:"updated"`
	Confirmed int    `json:"confirmed"`
	Warning   string `json:"warning,omitempty"`
}

type IPOCompanyOverrideInput struct {
	StatusOverride string `json:"status_override"`
	FinalTicker    string `json:"final_ticker"`
	Exchange       string `json:"exchange"`
	OfferPrice     string `json:"offer_price"`
	SharesOffered  int64  `json:"shares_offered"`
	ListingDate    string `json:"listing_date"`
	Note           string `json:"note"`
}

func NewIPORadarService(db *gorm.DB, secClient sec.CurrentFilingsClient, notifier telegram.Notifier, configs *ConfigService) *IPORadarService {
	return &IPORadarService{db: db, sec: secClient, configs: configs, batches: NewNotificationBatchService(db, notifier, configs), newLongbridgeListingClient: newLongbridgeIPOListingClient, newLongbridgeIPOCalendarClient: newLongbridgeIPOCalendarClient}
}

func (s *IPORadarService) WithNotificationCenter(center *NotificationBatchService) *IPORadarService {
	if s != nil && center != nil {
		s.batches = center
	}
	return s
}

func (s *IPORadarService) WithInAppNotifications(inApp *InAppNotificationService) *IPORadarService {
	if s != nil && inApp != nil {
		s.inApp = inApp
	}
	return s
}

// WithLongbridgeListingRuntime supplies the environment defaults that are
// merged with encrypted, operator-saved Longbridge credentials at run time.
func (s *IPORadarService) WithLongbridgeListingRuntime(runtime config.DiscoveryConfig) *IPORadarService {
	s.longbridgeRuntime = runtime
	return s
}

// Health summarizes operator-visible IPO work. All timestamps remain UTC so
// API clients can present them in their configured local timezone.
func (s *IPORadarService) Health(ctx context.Context, now time.Time) (IPORadarHealth, error) {
	now = now.UTC()
	settings, err := s.configs.IPORadarSettings(ctx)
	if err != nil {
		return IPORadarHealth{}, err
	}
	companies, err := s.allCompanies(ctx, now)
	if err != nil {
		return IPORadarHealth{}, err
	}
	health := IPORadarHealth{}
	staleBefore := now.Add(-time.Duration(settings.LifecycleRecheckHours) * time.Hour)
	for _, company := range companies {
		if company.Status == "listing_pending" {
			health.PendingListing++
		}
		// A ticker is not normally available while an issuer is only preparing
		// or amending its registration. Surface a mapping action only once the
		// SEC lifecycle indicates that the security is close to, or at, listing.
		// This keeps the operator queue focused on genuinely actionable gaps.
		if ipoRequiresMarketMapping(company) {
			health.MissingMarketMapping++
		}
		if ipoLifecycleCheckStale(company, staleBefore) {
			health.StaleLifecycleChecks++
		}
	}
	var count int64
	if err := s.db.WithContext(ctx).Model(&model.IPOOfferingEvent{}).Where("parse_status = ?", "unsupported").Count(&count).Error; err != nil {
		return IPORadarHealth{}, err
	}
	health.UnsupportedOfferingEvents = int(count)
	batchCount := func(query *gorm.DB, target *int) error {
		count = 0
		if err := query.Count(&count).Error; err != nil {
			return err
		}
		*target = int(count)
		return nil
	}
	if err := batchCount(s.db.WithContext(ctx).Model(&model.NotificationBatch{}).Where("source IN ? AND status = ?", ipoNotificationSources, "failed"), &health.FailedNotificationBatches); err != nil {
		return IPORadarHealth{}, err
	}
	if err := batchCount(s.db.WithContext(ctx).Model(&model.NotificationBatch{}).Where("source IN ? AND status = ? AND next_retry_at IS NOT NULL AND next_retry_at <= ?", ipoNotificationSources, "failed", now), &health.DueRetryBatches); err != nil {
		return IPORadarHealth{}, err
	}
	if err := batchCount(s.db.WithContext(ctx).Model(&model.NotificationBatch{}).Where("source IN ? AND status = ?", ipoNotificationSources, "dead_letter"), &health.DeadLetterBatches); err != nil {
		return IPORadarHealth{}, err
	}
	var latest model.SyncRun
	if err := s.db.WithContext(ctx).Where("trigger IN ?", []string{"ipo_manual", "ipo_scheduler"}).Order("started_at DESC, id DESC").First(&latest).Error; err != nil && err != gorm.ErrRecordNotFound {
		return IPORadarHealth{}, err
	} else if err == nil {
		health.LatestSync = &IPORadarSyncHealth{StartedAt: latest.StartedAt, FinishedAt: latest.FinishedAt, Status: latest.Status, NewFilings: latest.NewFilings}
	}
	health.Actions = buildIPORadarActions(health)
	return health, nil
}

func buildIPORadarActions(health IPORadarHealth) []IPORadarAction {
	actions := make([]IPORadarAction, 0, 7)
	add := func(key, severity, disposition string, count int, attention, route, status string) {
		if count <= 0 {
			return
		}
		actions = append(actions, IPORadarAction{
			Key: key, Severity: severity, Disposition: disposition, Count: count, Attention: attention, Route: route, Status: status,
		})
	}
	// Delivery failures are first because a dead-lettered IPO alert cannot
	// recover without an explicit operator action.
	add("review_dead_letters", "danger", "manual", health.DeadLetterBatches, "", "notification_logs", "dead_letter")
	add("retry_notifications", "danger", "automatic", health.DueRetryBatches, "", "notification_logs", "failed")
	add("verify_listing", "warning", "automatic", health.PendingListing, "listing_pending", "", "")
	add("complete_market_mapping", "warning", "automatic", health.MissingMarketMapping, "missing_market_mapping", "", "")
	add("recheck_lifecycle", "warning", "automatic", health.StaleLifecycleChecks, "lifecycle_stale", "", "")
	add("review_offering_parse", "warning", "automatic", health.UnsupportedOfferingEvents, "parse_failed", "", "")
	if health.LatestSync != nil && health.LatestSync.Status == "failed" {
		add("retry_sync", "danger", "manual", 1, "", "refresh", "")
	}
	return actions
}

var ipoNotificationSources = []string{"ipo", "ipo_offering"}

func (s *IPORadarService) List(ctx context.Context, filter IPOFilingFilter) (PageResult[model.IPOFiling], error) {
	page, pageSize := normalizePage(filter.Page, filter.PageSize)
	query := scopeIPOCandidateFilings(s.db.WithContext(ctx).Model(&model.IPOFiling{}), s.db.WithContext(ctx))
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
	order := "created_at DESC, accepted_at DESC, filing_date DESC, id DESC"
	if strings.EqualFold(strings.TrimSpace(filter.Sort), "timeline") {
		order = "accepted_at ASC, filing_date ASC, id ASC"
	}
	err := query.Order(order).Offset((page - 1) * pageSize).Limit(pageSize).Find(&items).Error
	return newPageResult(items, total, page, pageSize), err
}

func (s *IPORadarService) ListOfferingEvents(ctx context.Context, cik string, page int, pageSize int) (PageResult[model.IPOOfferingEvent], error) {
	page, pageSize = normalizePage(page, pageSize)
	query := s.db.WithContext(ctx).Model(&model.IPOOfferingEvent{}).Where("cik = ?", strings.TrimSpace(cik))
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return PageResult[model.IPOOfferingEvent]{}, err
	}
	var items []model.IPOOfferingEvent
	err := query.Order("accepted_at DESC, filing_date DESC, id DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&items).Error
	return newPageResult(items, total, page, pageSize), err
}

func (s *IPORadarService) ListCompanies(ctx context.Context, filter IPOCompanyFilter, now time.Time) (PageResult[IPOCompanyItem], error) {
	page, pageSize := normalizePage(filter.Page, filter.PageSize)
	attention := strings.ToLower(strings.TrimSpace(filter.Attention))
	if !validIPOCompanyAttention(attention) {
		return PageResult[IPOCompanyItem]{}, fmt.Errorf("%w: invalid IPO attention filter", ErrValidation)
	}
	query := scopeIPOCandidateFilings(s.db.WithContext(ctx).Model(&model.IPOFiling{}), s.db.WithContext(ctx))
	if filter.CompanyName != "" {
		query = query.Where("company_name LIKE ?", "%"+strings.TrimSpace(filter.CompanyName)+"%")
	}
	if filter.CIK != "" {
		query = query.Where("cik = ?", strings.TrimSpace(filter.CIK))
	}

	var filings []model.IPOFiling
	if err := query.Order("cik ASC, filing_date ASC, accepted_at ASC, id ASC").Find(&filings).Error; err != nil {
		return PageResult[IPOCompanyItem]{}, err
	}
	if len(filings) == 0 {
		return newPageResult([]IPOCompanyItem{}, 0, page, pageSize), nil
	}

	ciks := make([]string, 0, len(filings))
	seenCIK := map[string]bool{}
	for _, filing := range filings {
		if filing.CIK != "" && !seenCIK[filing.CIK] {
			seenCIK[filing.CIK] = true
			ciks = append(ciks, filing.CIK)
		}
	}
	var targets []model.WatchTarget
	if len(ciks) > 0 {
		if err := s.db.WithContext(ctx).Where("cik IN ?", ciks).Find(&targets).Error; err != nil {
			return PageResult[IPOCompanyItem]{}, err
		}
	}
	tickerByCIK := map[string]string{}
	for _, target := range targets {
		if target.CIK != "" && target.Ticker != "" {
			tickerByCIK[target.CIK] = target.Ticker
		}
	}
	var overrides []model.IPOCompanyOverride
	if len(ciks) > 0 {
		if err := s.db.WithContext(ctx).Where("cik IN ?", ciks).Find(&overrides).Error; err != nil {
			return PageResult[IPOCompanyItem]{}, err
		}
	}
	overrideByCIK := map[string]model.IPOCompanyOverride{}
	for _, override := range overrides {
		overrideByCIK[override.CIK] = override
	}
	var marketRows []model.IPOCompanyMarketData
	if len(ciks) > 0 {
		if err := s.db.WithContext(ctx).Where("cik IN ?", ciks).Find(&marketRows).Error; err != nil {
			return PageResult[IPOCompanyItem]{}, err
		}
	}
	marketByCIK := map[string]model.IPOCompanyMarketData{}
	for _, row := range marketRows {
		marketByCIK[row.CIK] = row
	}
	var follows []model.IPOCompanyFollow
	if len(ciks) > 0 && s.db.Migrator().HasTable(&model.IPOCompanyFollow{}) {
		if err := s.db.WithContext(ctx).Where("cik IN ?", ciks).Find(&follows).Error; err != nil {
			return PageResult[IPOCompanyItem]{}, err
		}
	}
	followedCIKs := make(map[string]bool, len(follows))
	for _, follow := range follows {
		followedCIKs[normalizedIPOCompanyCIK(follow.CIK)] = true
	}
	attentionCIKs, staleBefore, err := s.attentionCompanyCIKs(ctx, attention, ciks, now)
	if err != nil {
		return PageResult[IPOCompanyItem]{}, err
	}

	grouped := map[string][]model.IPOFiling{}
	for _, filing := range filings {
		key := valueOrDefault(filing.CIK, strings.ToLower(strings.TrimSpace(filing.CompanyName)))
		grouped[key] = append(grouped[key], filing)
	}

	items := make([]IPOCompanyItem, 0, len(grouped))
	for _, group := range grouped {
		item := buildIPOCompanyItem(group, tickerByCIK, marketByCIK, overrideByCIK, now)
		item.Followed = followedCIKs[normalizedIPOCompanyCIK(item.CIK)]
		if filter.Followed != nil && item.Followed != *filter.Followed {
			continue
		}
		if !matchesIPOCompanyTicker(item, filter.Ticker) {
			continue
		}
		if status := strings.TrimSpace(filter.Status); status != "" && item.Status != status {
			continue
		}
		// The default company view is an active IPO work queue. A selected
		// terminal status or attention filter remains inspectable without
		// requiring callers to opt into the full historical archive.
		if strings.TrimSpace(filter.Status) == "" && attention == "" && !filter.IncludeEnded && (filter.Followed == nil || !*filter.Followed) && !activeIPOCompanyStatus(item.Status) {
			continue
		}
		if !matchesIPOCompanyAttention(item, attention, attentionCIKs, staleBefore) {
			continue
		}
		items = append(items, item)
	}
	sortIPOCompanies(items, filter.SortBy, filter.SortOrder)

	total := int64(len(items))
	start := (page - 1) * pageSize
	if start >= len(items) {
		return newPageResult([]IPOCompanyItem{}, total, page, pageSize), nil
	}
	end := start + pageSize
	if end > len(items) {
		end = len(items)
	}
	return newPageResult(items[start:end], total, page, pageSize), nil
}

// SetCompanyFollow changes the local IPO follow list. Enabling follow records
// the current company snapshot as a baseline, so historical filings never
// generate an alert simply because an operator started following a company.
func (s *IPORadarService) SetCompanyFollow(ctx context.Context, cik string, followed bool, now time.Time) (model.IPOCompanyFollow, error) {
	if s == nil || s.db == nil || !s.db.Migrator().HasTable(&model.IPOCompanyFollow{}) {
		return model.IPOCompanyFollow{}, fmt.Errorf("%w: ipo follow storage is not initialized", ErrValidation)
	}
	cik = strings.TrimSpace(cik)
	if cik == "" {
		return model.IPOCompanyFollow{}, fmt.Errorf("%w: cik is required", ErrValidation)
	}
	if !followed {
		if err := s.db.WithContext(ctx).Where("cik = ?", cik).Delete(&model.IPOCompanyFollow{}).Error; err != nil {
			return model.IPOCompanyFollow{}, err
		}
		return model.IPOCompanyFollow{CIK: cik}, nil
	}
	company, err := s.companyByCIK(ctx, cik, now)
	if err != nil {
		return model.IPOCompanyFollow{}, err
	}
	var existing model.IPOCompanyFollow
	if err := s.db.WithContext(ctx).Where("cik = ?", cik).First(&existing).Error; err == nil {
		return existing, nil
	} else if err != gorm.ErrRecordNotFound {
		return model.IPOCompanyFollow{}, err
	}
	follow := model.IPOCompanyFollow{CIK: cik, CompanyName: company.CompanyName, LastProgressKey: ipoCompanyProgressKey(company)}
	if err := s.db.WithContext(ctx).Create(&follow).Error; err != nil {
		return model.IPOCompanyFollow{}, err
	}
	return follow, nil
}

func (s *IPORadarService) companyByCIK(ctx context.Context, cik string, now time.Time) (IPOCompanyItem, error) {
	page, err := s.ListCompanies(ctx, IPOCompanyFilter{CIK: cik, IncludeEnded: true, Page: 1, PageSize: 10}, now)
	if err != nil {
		return IPOCompanyItem{}, err
	}
	for _, item := range page.Items {
		if item.CIK == cik || normalizedIPOCompanyCIK(item.CIK) == normalizedIPOCompanyCIK(cik) {
			return item, nil
		}
	}
	return IPOCompanyItem{}, fmt.Errorf("%w: ipo company not found", ErrNotFound)
}

// matchesIPOCompanyTicker accepts the final (including manual), automatic,
// or watch-target ticker. Users may paste a common market suffix or '$'
// prefix without missing the IPO company.
func matchesIPOCompanyTicker(item IPOCompanyItem, filter string) bool {
	filter = normalizeIPOCompanyTicker(filter)
	if filter == "" {
		return true
	}
	for _, ticker := range []string{item.FinalTicker, item.AutomaticTicker, item.MatchedTicker} {
		if normalizeIPOCompanyTicker(ticker) == filter {
			return true
		}
	}
	return false
}

func normalizeIPOCompanyTicker(value string) string {
	value = strings.ToUpper(strings.TrimSpace(value))
	value = strings.TrimPrefix(value, "$")
	value = strings.TrimSuffix(value, ".US")
	return value
}

func (s *IPORadarService) allCompanies(ctx context.Context, now time.Time) ([]IPOCompanyItem, error) {
	items := make([]IPOCompanyItem, 0)
	for page := 1; ; page++ {
		result, err := s.ListCompanies(ctx, IPOCompanyFilter{IncludeEnded: true, Page: page, PageSize: 200}, now)
		if err != nil {
			return nil, err
		}
		items = append(items, result.Items...)
		if len(items) >= int(result.Total) {
			return items, nil
		}
	}
}

func validIPOCompanyAttention(attention string) bool {
	switch attention {
	case "", "listing_pending", "parse_failed", "lifecycle_stale", "notification_failed", "missing_market_mapping":
		return true
	default:
		return false
	}
}

func (s *IPORadarService) attentionCompanyCIKs(ctx context.Context, attention string, ciks []string, now time.Time) (map[string]bool, time.Time, error) {
	matched := map[string]bool{}
	if attention == "" || attention == "listing_pending" || attention == "lifecycle_stale" || attention == "missing_market_mapping" {
		if attention != "lifecycle_stale" {
			return matched, time.Time{}, nil
		}
		settings, err := s.configs.IPORadarSettings(ctx)
		if err != nil {
			return nil, time.Time{}, err
		}
		return matched, now.UTC().Add(-time.Duration(settings.LifecycleRecheckHours) * time.Hour), nil
	}
	if len(ciks) == 0 {
		return matched, time.Time{}, nil
	}
	if attention == "parse_failed" {
		var values []string
		if err := s.db.WithContext(ctx).Model(&model.IPOOfferingEvent{}).Where("cik IN ? AND parse_status = ?", ciks, "unsupported").Distinct("cik").Pluck("cik", &values).Error; err != nil {
			return nil, time.Time{}, err
		}
		for _, cik := range values {
			matched[cik] = true
		}
		return matched, time.Time{}, nil
	}
	var values []string
	err := s.db.WithContext(ctx).Table("notification_batch_items").
		Select("DISTINCT notification_batch_items.cik").
		Joins("JOIN notification_batches ON notification_batches.id = notification_batch_items.batch_id").
		Where("notification_batch_items.cik IN ? AND notification_batches.source IN ? AND notification_batches.status IN ?", ciks, ipoNotificationSources, []string{"failed", "dead_letter"}).
		Pluck("notification_batch_items.cik", &values).Error
	if err != nil {
		return nil, time.Time{}, err
	}
	for _, cik := range values {
		matched[cik] = true
	}
	return matched, time.Time{}, nil
}

func matchesIPOCompanyAttention(item IPOCompanyItem, attention string, matchedCIKs map[string]bool, staleBefore time.Time) bool {
	switch attention {
	case "":
		return true
	case "listing_pending":
		return item.Status == "listing_pending"
	case "parse_failed", "notification_failed":
		return matchedCIKs[item.CIK]
	case "lifecycle_stale":
		return ipoLifecycleCheckStale(item, staleBefore)
	case "missing_market_mapping":
		return ipoRequiresMarketMapping(item)
	default:
		return false
	}
}

func ipoLifecycleCheckStale(item IPOCompanyItem, staleBefore time.Time) bool {
	return activeIPOCompanyStatus(item.Status) && (item.LifecycleCheckedAt == nil || item.LifecycleCheckedAt.Before(staleBefore))
}

func (s *IPORadarService) UpsertCompanyOverride(ctx context.Context, cik string, input IPOCompanyOverrideInput) (model.IPOCompanyOverride, error) {
	cik = strings.TrimSpace(cik)
	if cik == "" {
		return model.IPOCompanyOverride{}, fmt.Errorf("%w: cik is required", ErrValidation)
	}
	status := strings.TrimSpace(input.StatusOverride)
	if status != "" && !validIPOStatus(status) {
		return model.IPOCompanyOverride{}, fmt.Errorf("%w: invalid ipo status", ErrValidation)
	}
	offerPrice := strings.TrimSpace(input.OfferPrice)
	if offerPrice != "" {
		price, err := strconv.ParseFloat(offerPrice, 64)
		if err != nil || price <= 0 {
			return model.IPOCompanyOverride{}, fmt.Errorf("%w: invalid offer price", ErrValidation)
		}
	}
	if input.SharesOffered < 0 {
		return model.IPOCompanyOverride{}, fmt.Errorf("%w: invalid shares offered", ErrValidation)
	}
	var listingDate *time.Time
	if value := strings.TrimSpace(input.ListingDate); value != "" {
		parsed, err := time.Parse("2006-01-02", value)
		if err != nil {
			return model.IPOCompanyOverride{}, fmt.Errorf("%w: invalid listing date", ErrValidation)
		}
		listingDate = &parsed
	}
	override := model.IPOCompanyOverride{
		CIK:            cik,
		StatusOverride: status,
		FinalTicker:    strings.ToUpper(strings.TrimSpace(input.FinalTicker)),
		Exchange:       strings.TrimSpace(input.Exchange),
		OfferPrice:     offerPrice,
		SharesOffered:  input.SharesOffered,
		ListingDate:    listingDate,
		Note:           strings.TrimSpace(input.Note),
	}
	err := s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "cik"}},
		DoUpdates: clause.Assignments(map[string]any{
			"status_override": override.StatusOverride,
			"final_ticker":    override.FinalTicker,
			"exchange":        override.Exchange,
			"offer_price":     override.OfferPrice,
			"shares_offered":  override.SharesOffered,
			"listing_date":    override.ListingDate,
			"note":            override.Note,
			"updated_at":      time.Now().UTC(),
		}),
	}).Create(&override).Error
	if err != nil {
		return model.IPOCompanyOverride{}, err
	}
	if err := s.db.WithContext(ctx).Where("cik = ?", cik).First(&override).Error; err != nil {
		return model.IPOCompanyOverride{}, err
	}
	return override, nil
}

func (s *IPORadarService) Refresh(ctx context.Context) (IPORadarRefreshResult, error) {
	return s.RefreshWithTrigger(ctx, "ipo_manual")
}

// ReconcileLifecycle performs only the bounded SEC submissions backfill for
// active IPO candidates. It is scheduled independently from the current-feed
// scan so a slow historical CIK cannot delay new IPO detection.
func (s *IPORadarService) ReconcileLifecycle(ctx context.Context) (IPOReconciliationResult, error) {
	settings, err := s.configs.IPORadarSettings(ctx)
	if err != nil {
		return IPOReconciliationResult{}, err
	}
	if !settings.Enabled || !settings.LifecycleSweepEnabled {
		return IPOReconciliationResult{}, nil
	}
	added, err := s.sweepCompanyLifecycleFilings(ctx, settings, map[string]bool{})
	if err != nil {
		return IPOReconciliationResult{}, err
	}
	return IPOReconciliationResult{Checked: settings.LifecycleMaxCIKs, Updated: len(added)}, nil
}

// ReconcileOfferings retries a bounded set of historical 424B4 documents.
// New offerings remain handled during a manual full scan; this job exists to
// repair temporary document failures and parser improvements independently.
func (s *IPORadarService) ReconcileOfferings(ctx context.Context) (IPOReconciliationResult, error) {
	settings, err := s.configs.IPORadarSettings(ctx)
	if err != nil {
		return IPOReconciliationResult{}, err
	}
	if !settings.Enabled {
		return IPOReconciliationResult{}, nil
	}
	warning, _ := s.enrichIPOMarketDataWithListingMapping(ctx, nil, false, true)
	if warning != "" {
		return IPOReconciliationResult{Warning: warning}, nil
	}
	return IPOReconciliationResult{Checked: ipoOfferingUnsupportedRetryMaxBatch}, nil
}

// ReconcileListings queries only the complementary Longbridge sources. The
// official SEC ticker/exchange mapping remains part of the primary SEC scan.
func (s *IPORadarService) ReconcileListings(ctx context.Context) (IPOReconciliationResult, error) {
	settings, err := s.configs.IPORadarSettings(ctx)
	if err != nil {
		return IPOReconciliationResult{}, err
	}
	if !settings.Enabled {
		return IPOReconciliationResult{}, nil
	}
	confirmed, listingWarning := s.confirmListedCompaniesWithLongbridge(ctx, settings)
	calendarWarning := s.syncLongbridgeIPOCalendar(ctx, settings)
	warnings := make([]string, 0, 2)
	if listingWarning != "" {
		warnings = append(warnings, listingWarning)
	}
	if calendarWarning != "" {
		warnings = append(warnings, calendarWarning)
	}
	return IPOReconciliationResult{Checked: settings.LongbridgeListingRequestBudget, Confirmed: len(confirmed), Warning: strings.Join(warnings, "; ")}, nil
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
	confirmedListedCIKs, listingWarning := s.refreshListedCompanyMappings(ctx)
	fullReconciliation := trigger != "ipo_scheduler"
	longbridgeListingWarning := ""
	if fullReconciliation {
		longbridgeConfirmedCIKs, warning := s.confirmListedCompaniesWithLongbridge(ctx, settings)
		longbridgeListingWarning = warning
		for cik := range longbridgeConfirmedCIKs {
			confirmedListedCIKs[cik] = true
		}
	}
	var existingFilings int64
	if err := s.db.WithContext(ctx).Model(&model.IPOFiling{}).Count(&existingFilings).Error; err != nil {
		s.finishSyncRun(ctx, run.ID, out, "failed", err.Error())
		return out, err
	}
	initialBaseline := existingFilings == 0
	results, err := s.sec.ListCurrentFilings(ctx, sec.CurrentFilingQuery{FormTypes: currentIPOFilingFormTypes(settings.FormTypes), Count: settings.MaxResults})
	if err != nil {
		s.finishSyncRun(ctx, run.ID, out, "failed", err.Error())
		return out, err
	}
	cutoff := time.Now().UTC().AddDate(0, 0, -settings.LookbackDays)
	out.Checked = len(results)
	backfilledCIK := map[string]bool{}
	candidateCIKs, err := s.ipoCandidateCIKSet(ctx)
	if err != nil {
		s.finishSyncRun(ctx, run.ID, out, "failed", err.Error())
		return out, err
	}
	notificationCandidates := make([]NotificationCandidate, 0)
	newFilings := make([]model.IPOFiling, 0)
	for _, item := range results {
		if !item.FilingDate.IsZero() && item.FilingDate.Before(cutoff) {
			continue
		}
		form := strings.ToUpper(strings.TrimSpace(item.FilingType))
		isRegistration := isIPORegistrationFilingType(form)
		isLifecycle := isRequiredIPOLifecycleForm(form)
		if (!isRegistration && !isLifecycle) || (isRegistration && !ipoKeywordMatch(item, settings.Keywords)) || (isLifecycle && !candidateCIKs[item.CIK]) {
			continue
		}
		confirmedListed := confirmedListedCIKs[normalizedIPOCompanyCIK(item.CIK)]
		// Current S-1/F-1 registration amendments from an SEC-confirmed listed
		// company are not a new IPO opportunity. In particular, do not let one
		// seed a full historical company backfill years after the actual listing.
		// A newly filed 424B4 is deliberately not covered by this exclusion: it
		// can carry a material offering-price event for an already listed issuer.
		// Its generic IPO-filing alert remains suppressed below, while the
		// dedicated offering alert can still be evaluated.
		if confirmedListed && isRegistration {
			continue
		}
		filing := currentFilingToIPOModel(item)
		created, err := s.createIfNew(ctx, filing)
		if err != nil {
			s.finishSyncRun(ctx, run.ID, out, "failed", err.Error())
			return out, err
		}
		if created {
			out.NewFilings++
			newFilings = append(newFilings, filing)
			if !confirmedListed {
				notificationCandidates = append(notificationCandidates, ipoNotificationCandidate(filing, settings, initialBaseline, false))
			}
		}
		if confirmedListed {
			continue
		}
		if isRegistration {
			candidateCIKs[item.CIK] = true
		}
		cik := strings.TrimSpace(item.CIK)
		if cik == "" || backfilledCIK[cik] {
			continue
		}
		backfilledCIK[cik] = true
		added, err := s.backfillCompanyLifecycleFilings(ctx, item, settings)
		if err != nil {
			s.finishSyncRun(ctx, run.ID, out, "failed", err.Error())
			return out, err
		}
		out.NewFilings += len(added)
		newFilings = append(newFilings, added...)
		for _, filing := range added {
			notificationCandidates = append(notificationCandidates, ipoNotificationCandidate(filing, settings, initialBaseline, true))
		}
	}
	if fullReconciliation {
		sweepAdded, err := s.sweepCompanyLifecycleFilings(ctx, settings, backfilledCIK)
		if err != nil {
			s.finishSyncRun(ctx, run.ID, out, "failed", err.Error())
			return out, err
		}
		out.NewFilings += len(sweepAdded)
		newFilings = append(newFilings, sweepAdded...)
		for _, filing := range sweepAdded {
			notificationCandidates = append(notificationCandidates, ipoNotificationCandidate(filing, settings, initialBaseline, true))
		}
	}
	if len(notificationCandidates) > 0 {
		batch, err := s.batches.Deliver(ctx, NotificationBatchInput{SyncRunID: run.ID, Source: "ipo", Trigger: trigger, Candidates: notificationCandidates})
		if err != nil {
			s.finishSyncRun(ctx, run.ID, out, "failed", err.Error())
			return out, err
		}
		out.Notified = batch.SentCount
	}
	calendarWarning := ""
	warning := ""
	offeringCandidates := []NotificationCandidate(nil)
	if fullReconciliation {
		calendarWarning = s.syncLongbridgeIPOCalendar(ctx, settings)
		warning, offeringCandidates = s.enrichIPOMarketDataWithListingMapping(ctx, newFilings, initialBaseline, true)
	}
	warnings := make([]string, 0, 4)
	if listingWarning != "" {
		warnings = append(warnings, listingWarning)
	}
	if longbridgeListingWarning != "" {
		warnings = append(warnings, longbridgeListingWarning)
	}
	if calendarWarning != "" {
		warnings = append(warnings, calendarWarning)
	}
	if warning != "" {
		warnings = append(warnings, warning)
	}
	if len(warnings) > 0 {
		_ = s.db.WithContext(ctx).Model(&model.SyncRun{}).Where("id = ?", run.ID).Update("warning_message", strings.Join(warnings, "; ")).Error
	}
	if len(offeringCandidates) > 0 {
		if _, err := s.batches.Deliver(ctx, NotificationBatchInput{SyncRunID: run.ID, Source: "ipo_offering", Trigger: trigger, Candidates: offeringCandidates}); err != nil {
			s.finishSyncRun(ctx, run.ID, out, "failed", err.Error())
			return out, err
		}
	}
	if err := s.notifyFollowedCompanyProgress(ctx, run.ID, trigger); err != nil {
		s.finishSyncRun(ctx, run.ID, out, "failed", err.Error())
		return out, err
	}
	s.finishSyncRun(ctx, run.ID, out, "success", "")
	return out, nil
}

func (s *IPORadarService) notifyFollowedCompanyProgress(ctx context.Context, syncRunID uint, trigger string) error {
	if s == nil || s.db == nil {
		return nil
	}
	if !s.db.Migrator().HasTable(&model.IPOCompanyFollow{}) {
		return nil
	}
	var follows []model.IPOCompanyFollow
	if err := s.db.WithContext(ctx).Find(&follows).Error; err != nil || len(follows) == 0 {
		return err
	}
	companies, err := s.allCompanies(ctx, time.Now().UTC())
	if err != nil {
		return err
	}
	byCIK := make(map[string]IPOCompanyItem, len(companies))
	for _, company := range companies {
		byCIK[normalizedIPOCompanyCIK(company.CIK)] = company
	}
	candidates := make([]NotificationCandidate, 0)
	updates := make([]model.IPOCompanyFollow, 0)
	for _, follow := range follows {
		company, found := byCIK[normalizedIPOCompanyCIK(follow.CIK)]
		if !found {
			continue
		}
		progressKey := ipoCompanyProgressKey(company)
		if progressKey == "" || progressKey == follow.LastProgressKey {
			continue
		}
		occurredAt := company.LatestFilingDate
		if company.LatestAcceptedAt != nil {
			occurredAt = *company.LatestAcceptedAt
		}
		if s.inApp != nil {
			_, _, err := s.inApp.Create(ctx, InAppNotificationInput{
				EventKey: "ipo-progress:" + company.CIK + ":" + progressKey, Source: "ipo_progress", Scope: "ipo_follow",
				EntityKind: "ipo_company", Ticker: valueOrDefault(company.FinalTicker, company.MatchedTicker), CompanyName: company.CompanyName,
				Severity: "info", Title: "关注 IPO 进展：" + company.CompanyName, Body: ipoCompanyProgressText(company), Link: "/ipo-radar?cik=" + company.CIK, OccurredAt: occurredAt,
			})
			if err != nil {
				return err
			}
		}
		candidates = append(candidates, NotificationCandidate{
			EntityKind: "ipo_company", FilingID: "ipo-follow:" + company.CIK + ":" + progressKey, CIK: company.CIK,
			Ticker: valueOrDefault(company.FinalTicker, company.MatchedTicker), CompanyName: company.CompanyName, FilingType: company.LatestFilingType,
			Title: ipoCompanyProgressText(company), FilingURL: company.LatestFilingURL, Reason: "eligible", EventAt: occurredAt,
		})
		follow.CompanyName, follow.LastProgressKey = company.CompanyName, progressKey
		updates = append(updates, follow)
	}
	if len(candidates) > 0 {
		if _, err := s.batches.Deliver(ctx, NotificationBatchInput{SyncRunID: syncRunID, Source: "ipo_progress", Trigger: trigger, Candidates: candidates}); err != nil {
			return err
		}
	}
	for _, follow := range updates {
		if err := s.db.WithContext(ctx).Model(&model.IPOCompanyFollow{}).Where("id = ?", follow.ID).Updates(map[string]any{"company_name": follow.CompanyName, "last_progress_key": follow.LastProgressKey}).Error; err != nil {
			return err
		}
	}
	return nil
}

func ipoCompanyProgressKey(item IPOCompanyItem) string {
	acceptedAt := ""
	if item.LatestAcceptedAt != nil {
		acceptedAt = item.LatestAcceptedAt.UTC().Format(time.RFC3339Nano)
	}
	listingDate := ""
	if item.ListingDate != nil {
		listingDate = item.ListingDate.UTC().Format(time.DateOnly)
	}
	raw := strings.Join([]string{item.CIK, acceptedAt, item.LatestFilingType, item.Status, item.FinalTicker, item.Exchange, item.OfferPrice, strconv.FormatInt(item.SharesOffered, 10), item.GrossProceeds, listingDate}, "|")
	if raw == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(raw))
	return fmt.Sprintf("%x", sum[:])
}

func ipoCompanyProgressText(item IPOCompanyItem) string {
	parts := []string{valueOrDefault(item.LatestFilingType, "IPO 状态更新"), "状态：" + valueOrDefault(item.Status, "未知")}
	if ticker := valueOrDefault(item.FinalTicker, item.MatchedTicker); ticker != "" {
		parts = append(parts, "代码："+ticker)
	}
	if item.Exchange != "" {
		parts = append(parts, "交易所："+item.Exchange)
	}
	if item.OfferPrice != "" {
		parts = append(parts, "发行价：$"+item.OfferPrice)
	}
	return strings.Join(parts, " · ")
}

func (s *IPORadarService) backfillCompanyLifecycleFilings(ctx context.Context, seed sec.CurrentFilingResult, settings IPORadarSettings) ([]model.IPOFiling, error) {
	cik := strings.TrimSpace(seed.CIK)
	if cik == "" {
		return nil, nil
	}
	results, err := s.sec.ListFilings(ctx, sec.FilingQuery{CIK: cik, FetchFullHistory: true})
	if err != nil {
		return nil, err
	}
	added := make([]model.IPOFiling, 0)
	for _, result := range results {
		if !isIPOLifecycleFilingType(result.FilingType, settings.FormTypes) {
			continue
		}
		if isIPORegistrationFilingType(result.FilingType) && !ipoKeywordMatch(filingResultToCurrent(result), settings.Keywords) {
			continue
		}
		filing := filingResultToIPOModel(result, seed)
		created, err := s.createIfNew(ctx, filing)
		if err != nil {
			return added, err
		}
		if created {
			added = append(added, filing)
		}
	}
	return added, nil
}

func (s *IPORadarService) ipoCandidateCIKSet(ctx context.Context) (map[string]bool, error) {
	var ciks []string
	if err := s.db.WithContext(ctx).Model(&model.IPOFiling{}).
		Where("cik <> '' AND UPPER(TRIM(filing_type)) IN ?", ipoRegistrationFilingTypes).
		Distinct("cik").Pluck("cik", &ciks).Error; err != nil {
		return nil, err
	}
	set := make(map[string]bool, len(ciks))
	for _, cik := range ciks {
		set[cik] = true
	}
	return set, nil
}

func (s *IPORadarService) sweepCompanyLifecycleFilings(ctx context.Context, settings IPORadarSettings, alreadyBackfilled map[string]bool) ([]model.IPOFiling, error) {
	if !settings.LifecycleSweepEnabled || settings.LifecycleMaxCIKs <= 0 {
		return nil, nil
	}
	recheckBefore := time.Now().UTC().Add(-time.Duration(settings.LifecycleRecheckHours) * time.Hour)
	ciks, err := s.lifecycleSweepCandidateCIKs(ctx, recheckBefore, alreadyBackfilled, settings.LifecycleMaxCIKs)
	if err != nil {
		return nil, err
	}
	added := make([]model.IPOFiling, 0)
	for _, cik := range ciks {
		var seed model.IPOFiling
		if err := s.db.WithContext(ctx).Where("cik = ?", cik).Order("filing_date DESC, accepted_at DESC, id DESC").First(&seed).Error; err != nil {
			return added, err
		}
		newFilings, err := s.backfillCompanyLifecycleFilings(ctx, sec.CurrentFilingResult{
			FilingID: seed.FilingID, AccessionNumber: seed.AccessionNumber, CIK: seed.CIK, CompanyName: seed.CompanyName,
			FilingType: seed.FilingType, FilingDate: seed.FilingDate, AcceptedAt: seed.AcceptedAt, FilingURL: seed.FilingURL, Title: seed.Title,
		}, settings)
		if err != nil {
			return added, err
		}
		checkedAt := time.Now().UTC()
		if err := s.db.WithContext(ctx).Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "cik"}},
			DoUpdates: clause.Assignments(map[string]any{"lifecycle_checked_at": &checkedAt, "updated_at": checkedAt}),
		}).Create(&model.IPOCompanyMarketData{CIK: cik, LifecycleCheckedAt: &checkedAt}).Error; err != nil {
			return added, err
		}
		added = append(added, newFilings...)
	}
	return added, nil
}

func (s *IPORadarService) lifecycleSweepCandidateCIKs(ctx context.Context, recheckBefore time.Time, alreadyBackfilled map[string]bool, limit int) ([]string, error) {
	query := scopeIPOCandidateFilings(s.db.WithContext(ctx).Model(&model.IPOFiling{}), s.db.WithContext(ctx))
	var filings []model.IPOFiling
	if err := query.Order("cik ASC, filing_date ASC, accepted_at ASC, id ASC").Find(&filings).Error; err != nil {
		return nil, err
	}
	if len(filings) == 0 {
		return nil, nil
	}

	grouped := make(map[string][]model.IPOFiling)
	ciks := make([]string, 0)
	seenCIK := make(map[string]bool)
	for _, filing := range filings {
		cik := strings.TrimSpace(filing.CIK)
		if cik == "" {
			continue
		}
		grouped[cik] = append(grouped[cik], filing)
		if !seenCIK[cik] {
			seenCIK[cik] = true
			ciks = append(ciks, cik)
		}
	}

	var marketRows []model.IPOCompanyMarketData
	if err := s.db.WithContext(ctx).Where("cik IN ?", ciks).Find(&marketRows).Error; err != nil {
		return nil, err
	}
	marketByCIK := make(map[string]model.IPOCompanyMarketData, len(marketRows))
	for _, market := range marketRows {
		marketByCIK[market.CIK] = market
	}
	var overrides []model.IPOCompanyOverride
	if err := s.db.WithContext(ctx).Where("cik IN ?", ciks).Find(&overrides).Error; err != nil {
		return nil, err
	}
	overrideByCIK := make(map[string]model.IPOCompanyOverride, len(overrides))
	for _, override := range overrides {
		overrideByCIK[override.CIK] = override
	}

	type candidate struct {
		cik       string
		checkedAt *time.Time
	}
	candidates := make([]candidate, 0, len(ciks))
	now := time.Now().UTC()
	for cik, companyFilings := range grouped {
		if alreadyBackfilled[cik] {
			continue
		}
		if !hasRecentIPOLifecycleFiling(companyFilings, now) {
			continue
		}
		market := marketByCIK[cik]
		if market.LifecycleCheckedAt != nil && market.LifecycleCheckedAt.After(recheckBefore) {
			continue
		}
		item := buildIPOCompanyItem(companyFilings, nil, marketByCIK, overrideByCIK, now)
		if !activeIPOCompanyStatus(item.Status) {
			continue
		}
		candidates = append(candidates, candidate{cik: cik, checkedAt: market.LifecycleCheckedAt})
	}
	sort.Slice(candidates, func(i, j int) bool {
		left, right := candidates[i], candidates[j]
		if left.checkedAt == nil || right.checkedAt == nil {
			if left.checkedAt == nil && right.checkedAt != nil {
				return true
			}
			if left.checkedAt != nil && right.checkedAt == nil {
				return false
			}
			return left.cik < right.cik
		}
		if !left.checkedAt.Equal(*right.checkedAt) {
			return left.checkedAt.Before(*right.checkedAt)
		}
		return left.cik < right.cik
	})
	if limit > len(candidates) {
		limit = len(candidates)
	}
	selected := make([]string, 0, limit)
	for _, candidate := range candidates[:limit] {
		selected = append(selected, candidate.cik)
	}
	return selected, nil
}

func hasRecentIPOLifecycleFiling(filings []model.IPOFiling, now time.Time) bool {
	cutoff := now.UTC().AddDate(0, 0, -180)
	for _, filing := range filings {
		if !filing.FilingDate.Before(cutoff) && isIPOLifecycleFilingType(filing.FilingType, ipoRegistrationFilingTypes) {
			return true
		}
	}
	return false
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

func currentFilingToIPOModel(item sec.CurrentFilingResult) model.IPOFiling {
	return model.IPOFiling{
		FilingID:        canonicalIPOFilingID(item.FilingID, item.AccessionNumber, item.FilingURL),
		AccessionNumber: item.AccessionNumber,
		CIK:             item.CIK,
		CompanyName:     valueOrDefault(item.CompanyName, "Unknown"),
		FilingType:      item.FilingType,
		FilingDate:      item.FilingDate,
		AcceptedAt:      item.AcceptedAt,
		FilingURL:       item.FilingURL,
		Title:           item.Title,
	}
}

func filingResultToIPOModel(item sec.FilingResult, seed sec.CurrentFilingResult) model.IPOFiling {
	accession := valueOrDefault(item.AccessionNumber, seed.AccessionNumber)
	filingURL := valueOrDefault(item.FilingURL, seed.FilingURL)
	return model.IPOFiling{
		FilingID:        canonicalIPOFilingID(item.FilingID, accession, filingURL),
		AccessionNumber: accession,
		CIK:             valueOrDefault(item.CIK, seed.CIK),
		CompanyName:     valueOrDefault(item.CompanyName, valueOrDefault(seed.CompanyName, "Unknown")),
		FilingType:      item.FilingType,
		FilingDate:      item.FilingDate,
		AcceptedAt:      item.PublishedAt,
		FilingURL:       filingURL,
		Title:           item.Title,
	}
}

var ipoAccessionNumberPattern = regexp.MustCompile(`\b\d{10}-\d{2}-\d{6}\b`)

// canonicalIPOFilingID prevents the same SEC submission from being persisted
// once under an accession number and again under an EDGAR index URL. The SEC
// accession is stable across feeds and is therefore the only safe identity.
func canonicalIPOFilingID(filingID, accessionNumber, filingURL string) string {
	if accession := strings.TrimSpace(accessionNumber); accession != "" {
		return accession
	}
	for _, value := range []string{filingID, filingURL} {
		if accession := ipoAccessionNumberPattern.FindString(value); accession != "" {
			return accession
		}
	}
	return valueOrDefault(strings.TrimSpace(filingID), strings.TrimSpace(filingURL))
}

func filingResultToCurrent(item sec.FilingResult) sec.CurrentFilingResult {
	return sec.CurrentFilingResult{
		FilingID:    item.FilingID,
		CIK:         item.CIK,
		CompanyName: item.CompanyName,
		FilingType:  item.FilingType,
		FilingDate:  item.FilingDate,
		AcceptedAt:  item.PublishedAt,
		FilingURL:   item.FilingURL,
		Title:       item.Title,
	}
}

var ipoRegistrationFilingTypes = []string{"S-1", "S-1/A", "F-1", "F-1/A", "S-1MEF"}
var ipoRequiredLifecycleFilingTypes = []string{"EFFECT", "424B4", "RW"}

func currentIPOFilingFormTypes(configured []string) []string {
	forms := make([]string, 0, len(configured)+len(ipoRequiredLifecycleFilingTypes))
	seen := map[string]bool{}
	for _, form := range append(append([]string{}, configured...), ipoRequiredLifecycleFilingTypes...) {
		form = strings.ToUpper(strings.TrimSpace(form))
		if form != "" && !seen[form] {
			seen[form] = true
			forms = append(forms, form)
		}
	}
	return forms
}

func isIPORegistrationFilingType(filingType string) bool {
	form := strings.ToUpper(strings.TrimSpace(filingType))
	for _, registrationType := range ipoRegistrationFilingTypes {
		if form == registrationType {
			return true
		}
	}
	return false
}

func isRequiredIPOLifecycleForm(filingType string) bool {
	form := strings.ToUpper(strings.TrimSpace(filingType))
	return form == "EFFECT" || strings.HasPrefix(form, "424B4") || form == "RW" || strings.HasPrefix(form, "RW ")
}

func scopeIPOCandidateFilings(query *gorm.DB, db *gorm.DB) *gorm.DB {
	candidateCIKs := db.Model(&model.IPOFiling{}).
		Select("cik").
		Where("cik <> '' AND UPPER(TRIM(filing_type)) IN ?", ipoRegistrationFilingTypes)
	visibleTypes := []string{"S-1", "S-1/A", "F-1", "F-1/A", "S-1MEF", "EFFECT", "RW", "RW WD"}
	return query.
		Where("cik IN (?)", candidateCIKs).
		Where("UPPER(TRIM(filing_type)) IN ? OR UPPER(TRIM(filing_type)) LIKE ? OR UPPER(TRIM(filing_type)) LIKE ?", visibleTypes, "424B4%", "RW %")
}

func isIPOLifecycleFilingType(filingType string, configured []string) bool {
	form := strings.ToUpper(strings.TrimSpace(filingType))
	if form == "" {
		return false
	}
	for _, configuredType := range configured {
		if form == strings.ToUpper(strings.TrimSpace(configuredType)) {
			return true
		}
	}
	return isRequiredIPOLifecycleForm(form)
}

func selectPrimaryListedCompany(candidates []sec.ListedCompany) (sec.ListedCompany, bool) {
	filtered := make([]sec.ListedCompany, 0, len(candidates))
	for _, candidate := range candidates {
		candidate.Ticker = strings.ToUpper(strings.TrimSpace(candidate.Ticker))
		candidate.Exchange = strings.TrimSpace(candidate.Exchange)
		if candidate.Ticker != "" {
			filtered = append(filtered, candidate)
		}
	}
	if len(filtered) == 0 {
		return sec.ListedCompany{}, false
	}
	sort.SliceStable(filtered, func(i, j int) bool {
		left, right := filtered[i], filtered[j]
		leftExchange, rightExchange := left.Exchange != "", right.Exchange != ""
		if leftExchange != rightExchange {
			return leftExchange
		}
		leftComposite := strings.ContainsAny(left.Ticker, "-./")
		rightComposite := strings.ContainsAny(right.Ticker, "-./")
		if leftComposite != rightComposite {
			return !leftComposite
		}
		if len(left.Ticker) != len(right.Ticker) {
			return len(left.Ticker) < len(right.Ticker)
		}
		return left.Ticker < right.Ticker
	})
	return filtered[0], true
}

func buildIPOCompanyItem(filings []model.IPOFiling, tickerByCIK map[string]string, marketByCIK map[string]model.IPOCompanyMarketData, overrideByCIK map[string]model.IPOCompanyOverride, now time.Time) IPOCompanyItem {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	item := IPOCompanyItem{FilingCount: len(filings)}
	for i, filing := range filings {
		if i == 0 || filing.FilingDate.Before(item.FirstFilingDate) {
			item.FirstFilingDate = filing.FilingDate
		}
		if i == 0 || filingAfter(filing, model.IPOFiling{FilingDate: item.LatestFilingDate, AcceptedAt: item.LatestAcceptedAt}) {
			item.CIK = filing.CIK
			item.CompanyName = filing.CompanyName
			item.LatestFilingDate = filing.FilingDate
			item.LatestAcceptedAt = filing.AcceptedAt
			item.LatestFilingType = filing.FilingType
			item.LatestFilingURL = filing.FilingURL
			item.LatestTitle = filing.Title
		}
		if filing.NotifiedAt != nil {
			item.Notified = true
		}
		if item.CIK == "" {
			item.CIK = filing.CIK
		}
		if item.CompanyName == "" {
			item.CompanyName = filing.CompanyName
		}
	}
	item.MatchedTicker = tickerByCIK[item.CIK]
	if market, ok := marketByCIK[item.CIK]; ok {
		item.AutomaticTicker = market.Ticker
		item.AutomaticExchange = market.Exchange
		item.AutomaticOfferPrice = market.OfferPrice
		item.AutomaticShares = market.SharesOffered
		item.AutomaticGross = market.GrossProceeds
		item.LifecycleCheckedAt = market.LifecycleCheckedAt
		item.FinalTicker = market.Ticker
		item.Exchange = market.Exchange
		item.OfferPrice = market.OfferPrice
		item.SharesOffered = market.SharesOffered
		item.GrossProceeds = market.GrossProceeds
		item.ListedVerifiedAt = market.ListedVerifiedAt
		item.ListingDate = market.ListingDate
		item.LongbridgeListingCheckCount = market.LongbridgeListingCheckCount
		item.LongbridgeListingLastResult = market.LongbridgeListingLastResult
		item.LongbridgeListingNextRetryAt = market.LongbridgeListingNextRetryAt
		item.MarketDataUpdatedAt = &market.UpdatedAt
		if market.ListingSource == "longbridge" && market.ListedVerifiedAt != nil {
			item.MarketDataSource = "longbridge"
			item.MarketDataConfidence = market.ListingConfidence
		} else if market.ListingSource == longbridgeIPOCalendarSource || market.TickerSource == longbridgeIPOCalendarSource {
			item.MarketDataSource = "longbridge_calendar"
			item.MarketDataConfidence = market.ListingConfidence
		} else if market.Ticker != "" {
			item.MarketDataSource = "sec"
			item.MarketDataConfidence = market.TickerConfidence
		} else if market.OfferPrice != "" || market.SharesOffered > 0 {
			item.MarketDataSource = "sec"
			item.MarketDataConfidence = market.OfferingConfidence
		}
	}
	statusTicker := ""
	if item.FinalTicker != "" && item.Exchange != "" && item.ListedVerifiedAt != nil {
		statusTicker = item.FinalTicker
	}
	pendingTicker := ""
	if item.FinalTicker != "" && item.Exchange == "" {
		pendingTicker = item.FinalTicker
	}
	item.Status, item.StatusReason, item.StatusConfidence = inferIPOCompanyStatus(filings, statusTicker, pendingTicker, item.LatestFilingDate, now)
	item.StatusSource = "system"
	if item.Status == "listed" && item.MarketDataSource == "longbridge" {
		item.StatusReason = "Longbridge confirmed market " + strings.TrimSpace(item.Exchange)
		item.StatusConfidence = "medium"
		item.StatusSource = "longbridge"
	}
	if item.Status == "listing_pending" && item.LongbridgeListingCheckCount > 0 {
		switch item.LongbridgeListingLastResult {
		case "no_data":
			item.StatusReason = fmt.Sprintf("Longbridge queried %d time(s); no listing market information returned", item.LongbridgeListingCheckCount)
		case "no_data_review":
			item.StatusReason = fmt.Sprintf("Longbridge queried %d time(s); no listing market information returned; moved to weekly automatic review", item.LongbridgeListingCheckCount)
		case "unavailable":
			item.StatusReason = fmt.Sprintf("Longbridge queried %d time(s); latest request was unavailable", item.LongbridgeListingCheckCount)
			if item.LongbridgeListingNextRetryAt != nil {
				item.StatusReason += "; retry after " + item.LongbridgeListingNextRetryAt.Format(time.RFC3339)
			}
		case "unavailable_review":
			item.StatusReason = fmt.Sprintf("Longbridge queried %d time(s); requests remain unavailable; moved to weekly automatic review", item.LongbridgeListingCheckCount)
			if item.LongbridgeListingNextRetryAt != nil {
				item.StatusReason += "; retry after " + item.LongbridgeListingNextRetryAt.Format(time.RFC3339)
			}
		}
	}
	if item.MarketDataSource == "longbridge_calendar" && item.ListingDate != nil && item.Status != "listed" && item.Status != "withdrawn" {
		item.StatusReason = "Longbridge IPO calendar: expected listing " + item.ListingDate.Format(time.DateOnly)
		item.StatusConfidence = "medium"
		item.StatusSource = "longbridge_calendar"
	}
	if override, ok := overrideByCIK[item.CIK]; ok {
		item.OverrideFinalTicker = override.FinalTicker
		item.OverrideExchange = override.Exchange
		item.OverrideOfferPrice = override.OfferPrice
		item.OverrideShares = override.SharesOffered
		item.OverrideListingDate = override.ListingDate
		if override.FinalTicker != "" {
			item.FinalTicker = override.FinalTicker
		}
		if override.Exchange != "" {
			item.Exchange = override.Exchange
		}
		if override.OfferPrice != "" {
			item.OfferPrice = override.OfferPrice
		}
		if override.SharesOffered > 0 {
			item.SharesOffered = override.SharesOffered
		}
		if override.ListingDate != nil {
			item.ListingDate = override.ListingDate
		}
		if override.FinalTicker != "" || override.Exchange != "" || override.OfferPrice != "" || override.SharesOffered > 0 || override.ListingDate != nil {
			item.MarketDataSource = "manual"
			item.MarketDataConfidence = "manual"
			item.MarketDataUpdatedAt = &override.UpdatedAt
		}
		item.OverrideNote = override.Note
		item.OverrideUpdatedAt = &override.UpdatedAt
		if strings.TrimSpace(override.StatusOverride) != "" {
			item.Status = override.StatusOverride
			item.StatusReason = "manual override"
			item.StatusConfidence = "manual"
			item.StatusSource = "manual"
		}
		if item.MatchedTicker == "" && override.FinalTicker != "" {
			item.MatchedTicker = override.FinalTicker
		}
	}
	return item
}

const (
	ipoOfferingParserVersion            = 5
	ipoOfferingUnsupportedRetryAfter    = 24 * time.Hour
	ipoOfferingUnsupportedRetryMaxBatch = 25
)

func (s *IPORadarService) enrichIPOMarketData(ctx context.Context, newFilings []model.IPOFiling, initialBaseline bool) (string, []NotificationCandidate) {
	return s.enrichIPOMarketDataWithListingMapping(ctx, newFilings, initialBaseline, false)
}

func (s *IPORadarService) enrichIPOMarketDataWithListingMapping(ctx context.Context, newFilings []model.IPOFiling, initialBaseline bool, listingMappingAlreadyRefreshed bool) (string, []NotificationCandidate) {
	client, ok := s.sec.(sec.IPOMarketClient)
	if !ok {
		return "", nil
	}
	warnings := make([]string, 0)
	offeringCandidates := make([]NotificationCandidate, 0)
	if !listingMappingAlreadyRefreshed {
		if _, warning := s.refreshListedCompanyMappings(ctx); warning != "" {
			warnings = append(warnings, warning)
		}
	}
	pending, err := s.pending424B4Filings(ctx)
	if err != nil {
		warnings = append(warnings, "424B4 backfill: "+err.Error())
		return strings.Join(warnings, "; "), nil
	}
	newFilingIDs := map[string]bool{}
	for _, filing := range newFilings {
		if strings.HasPrefix(strings.ToUpper(strings.TrimSpace(filing.FilingType)), "424B4") {
			newFilingIDs[filing.FilingID] = true
		}
	}
	for _, filing := range pending {
		document, err := client.FetchFilingDocument(ctx, filing.FilingURL)
		if err != nil {
			if recordErr := s.recordUnsupportedIPOOffering(ctx, filing, "fetch_failed"); recordErr != nil {
				warnings = append(warnings, "424B4 "+filing.FilingID+": "+recordErr.Error())
			}
			warnings = append(warnings, "424B4 "+filing.FilingID+": "+err.Error())
			continue
		}
		offering, ok := sec.Parse424B4Offering(document)
		if !ok {
			if err := s.recordUnsupportedIPOOffering(ctx, filing, offering.ParseMessage); err != nil {
				warnings = append(warnings, "424B4 "+filing.FilingID+": "+err.Error())
			}
			continue
		}
		event, updateSummary, notify, err := s.recordIPOOffering(ctx, filing, offering)
		if err != nil {
			warnings = append(warnings, "424B4 "+filing.FilingID+": "+err.Error())
			continue
		}
		if updateSummary {
			if err := s.upsertIPOOffering(ctx, filing.CIK, filing.FilingURL, offering); err != nil {
				warnings = append(warnings, "424B4 "+filing.FilingID+": "+err.Error())
				continue
			}
		}
		if notify && newFilingIDs[filing.FilingID] && !initialBaseline {
			candidate, err := s.ipoOfferingNotificationCandidate(ctx, filing, offering, event.OfferingType)
			if err != nil {
				warnings = append(warnings, "424B4 "+filing.FilingID+": "+err.Error())
				continue
			}
			offeringCandidates = append(offeringCandidates, candidate)
		}
	}
	return strings.Join(warnings, "; "), offeringCandidates
}

// refreshListedCompanyMappings refreshes the official SEC ticker/exchange
// mapping before the current-feed scan. A ticker alone is not confirmation:
// both ticker and exchange must be present before an issuer is treated as
// already listed and excluded from registration-form backfills.
func (s *IPORadarService) refreshListedCompanyMappings(ctx context.Context) (map[string]bool, string) {
	client, ok := s.sec.(sec.IPOMarketClient)
	if !ok {
		return map[string]bool{}, ""
	}
	listed, err := client.ListListedCompanies(ctx)
	if err != nil {
		return map[string]bool{}, "listed company mapping: " + err.Error()
	}
	if err := s.upsertListedCompanies(ctx, listed); err != nil {
		return map[string]bool{}, "listed company mapping: " + err.Error()
	}
	confirmed := make(map[string]bool, len(listed))
	for _, company := range listed {
		if strings.TrimSpace(company.Ticker) == "" || strings.TrimSpace(company.Exchange) == "" {
			continue
		}
		if cik := normalizedIPOCompanyCIK(company.CIK); cik != "" {
			confirmed[cik] = true
		}
	}
	return confirmed, ""
}

func normalizedIPOCompanyCIK(cik string) string {
	cik = strings.TrimSpace(cik)
	cik = strings.TrimLeft(cik, "0")
	if cik == "" {
		return ""
	}
	return cik
}

func (s *IPORadarService) pending424B4Filings(ctx context.Context) ([]model.IPOFiling, error) {
	query := scopeIPOCandidateFilings(s.db.WithContext(ctx).Model(&model.IPOFiling{}), s.db.WithContext(ctx)).
		Where("UPPER(TRIM(filing_type)) LIKE ?", "424B4%")
	var filings []model.IPOFiling
	if err := query.Order("accepted_at ASC, filing_date ASC, id ASC").Find(&filings).Error; err != nil {
		return nil, err
	}
	if len(filings) == 0 {
		return nil, nil
	}
	filingIDs := make([]string, 0, len(filings))
	for _, filing := range filings {
		filingIDs = append(filingIDs, filing.FilingID)
	}
	var rows []model.IPOOfferingEvent
	if err := s.db.WithContext(ctx).Where("filing_id IN ?", filingIDs).Find(&rows).Error; err != nil {
		return nil, err
	}
	events := map[string]model.IPOOfferingEvent{}
	for _, row := range rows {
		events[row.FilingID] = row
	}
	pending := make([]model.IPOFiling, 0)
	unsupportedRetried := 0
	retryBefore := time.Now().UTC().Add(-ipoOfferingUnsupportedRetryAfter)
	for _, filing := range filings {
		event, exists := events[filing.FilingID]
		if !exists || event.ParserVersion < ipoOfferingParserVersion {
			pending = append(pending, filing)
			continue
		}
		// Parser failures can be caused by a temporarily unavailable SEC
		// document or a document shape that the parser did not previously
		// understand. Retry them once a day in a bounded batch so improvements
		// to the parser repair historical data automatically without turning
		// each 30-minute IPO scan into a large document crawl.
		if event.ParseStatus == "unsupported" && event.UpdatedAt.Before(retryBefore) && unsupportedRetried < ipoOfferingUnsupportedRetryMaxBatch {
			pending = append(pending, filing)
			unsupportedRetried++
		}
	}
	return pending, nil
}

func (s *IPORadarService) ipoOfferingNotificationCandidate(ctx context.Context, filing model.IPOFiling, offering sec.IPOOffering, eventType string) (NotificationCandidate, error) {
	var market model.IPOCompanyMarketData
	if err := s.db.WithContext(ctx).Where("cik = ?", filing.CIK).First(&market).Error; err != nil {
		return NotificationCandidate{}, err
	}
	titlePrefix := "发行价"
	if eventType == "correction" {
		titlePrefix = "定价更新"
	}
	return NotificationCandidate{
		EntityKind: "ipo_offering", FilingID: filing.FilingID, Ticker: market.Ticker, CIK: filing.CIK,
		CompanyName: filing.CompanyName, FilingType: filing.FilingType, FilingURL: filing.FilingURL,
		Title:   fmt.Sprintf("%s $%s | 发行数量 %s | 预计募资 $%s", titlePrefix, offering.OfferPrice, formatInteger(offering.SharesOffered), formatDecimal(offering.GrossProceeds)),
		EventAt: filing.FilingDate, Reason: "eligible",
	}, nil
}

func (s *IPORadarService) recordIPOOffering(ctx context.Context, filing model.IPOFiling, offering sec.IPOOffering) (model.IPOOfferingEvent, bool, bool, error) {
	event := model.IPOOfferingEvent{
		FilingID: filing.FilingID, CIK: filing.CIK, CompanyName: filing.CompanyName,
		ParseStatus: "parsed", OfferPrice: offering.OfferPrice, SharesOffered: offering.SharesOffered,
		GrossProceeds: offering.GrossProceeds, ParseMessage: offering.ParseMessage, Fingerprint: ipoOfferingFingerprint(offering),
		FilingURL: filing.FilingURL, FilingDate: filing.FilingDate, AcceptedAt: filing.AcceptedAt,
		ParserVersion: ipoOfferingParserVersion,
	}
	updateSummary := false
	notify := false
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var existing model.IPOOfferingEvent
		err := tx.Where("filing_id = ?", filing.FilingID).First(&existing).Error
		if err == nil && existing.ParserVersion >= ipoOfferingParserVersion {
			event = existing
			return nil
		}
		if err != nil && err != gorm.ErrRecordNotFound {
			return err
		}

		var previous model.IPOOfferingEvent
		previousErr := tx.Where("cik = ? AND filing_id <> ? AND parse_status = ? AND offering_type IN ?", filing.CIK, filing.FilingID, "parsed", []string{"initial", "correction"}).
			Order("accepted_at DESC, filing_date DESC, id DESC").First(&previous).Error
		switch {
		case previousErr == gorm.ErrRecordNotFound:
			event.OfferingType = "initial"
			updateSummary, notify = true, true
		case previousErr != nil:
			return previousErr
		case offering.OfferingType == "follow_on":
			event.OfferingType = "follow_on"
		case previous.Fingerprint == event.Fingerprint:
			event.OfferingType = "duplicate"
		default:
			event.OfferingType = "correction"
			updateSummary, notify = true, true
		}
		if existing.ID != 0 {
			event.ID = existing.ID
			event.CreatedAt = existing.CreatedAt
		}
		return tx.Save(&event).Error
	})
	return event, updateSummary, notify, err
}

func (s *IPORadarService) recordUnsupportedIPOOffering(ctx context.Context, filing model.IPOFiling, parseMessage string) error {
	event := model.IPOOfferingEvent{
		FilingID: filing.FilingID, CIK: filing.CIK, CompanyName: filing.CompanyName,
		OfferingType: "unknown", ParseStatus: "unsupported", ParseMessage: parseMessage, FilingURL: filing.FilingURL,
		FilingDate: filing.FilingDate, AcceptedAt: filing.AcceptedAt, ParserVersion: ipoOfferingParserVersion,
	}
	return s.db.WithContext(ctx).Where("filing_id = ?", filing.FilingID).Assign(event).FirstOrCreate(&event).Error
}

func ipoOfferingFingerprint(offering sec.IPOOffering) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("%d|%s", offering.SharesOffered, offering.GrossProceeds))))
}

func formatInteger(value int64) string {
	digits := strconv.FormatInt(value, 10)
	for index := len(digits) - 3; index > 0; index -= 3 {
		digits = digits[:index] + "," + digits[index:]
	}
	return digits
}

func formatDecimal(value string) string {
	parts := strings.SplitN(value, ".", 2)
	formatted := parts[0]
	for index := len(formatted) - 3; index > 0; index -= 3 {
		formatted = formatted[:index] + "," + formatted[index:]
	}
	if len(parts) == 2 {
		formatted += "." + parts[1]
	}
	return formatted
}

func (s *IPORadarService) upsertListedCompanies(ctx context.Context, listed []sec.ListedCompany) error {
	var ciks []string
	if err := s.db.WithContext(ctx).Model(&model.IPOFiling{}).
		Where("cik <> '' AND UPPER(TRIM(filing_type)) IN ?", ipoRegistrationFilingTypes).
		Distinct("cik").Pluck("cik", &ciks).Error; err != nil {
		return err
	}
	tracked := map[string]string{}
	for _, cik := range ciks {
		tracked[strings.TrimLeft(cik, "0")] = cik
	}
	now := time.Now().UTC()
	matched := map[string]bool{}
	companiesByCIK := map[string][]sec.ListedCompany{}
	for _, company := range listed {
		normalizedCIK := strings.TrimLeft(company.CIK, "0")
		if _, ok := tracked[normalizedCIK]; !ok {
			continue
		}
		companiesByCIK[normalizedCIK] = append(companiesByCIK[normalizedCIK], company)
	}
	for normalizedCIK, candidates := range companiesByCIK {
		storedCIK, ok := tracked[normalizedCIK]
		if !ok {
			continue
		}
		company, ok := selectPrimaryListedCompany(candidates)
		if !ok {
			continue
		}
		matched[storedCIK] = true
		var row model.IPOCompanyMarketData
		err := s.db.WithContext(ctx).Where("cik = ?", storedCIK).First(&row).Error
		if err == gorm.ErrRecordNotFound {
			row = model.IPOCompanyMarketData{CIK: storedCIK, ListedVerifiedAt: &now}
		} else if err != nil {
			return err
		}
		newTicker := strings.ToUpper(strings.TrimSpace(company.Ticker))
		newExchange := strings.TrimSpace(company.Exchange)
		keepLongbridgeConfirmation := newTicker != "" && newExchange == "" && row.Ticker == newTicker && row.ListingSource == "longbridge" && row.ListedVerifiedAt != nil && strings.TrimSpace(row.Exchange) != ""
		if newTicker != "" && newExchange != "" {
			if row.ListedVerifiedAt == nil {
				row.ListedVerifiedAt = &now
			}
			row.Exchange = newExchange
			row.ListingSource = "sec"
			row.ListingConfidence = "high"
		} else if !keepLongbridgeConfirmation {
			row.Exchange = ""
			row.ListedVerifiedAt = nil
			row.ListingDate = nil
			row.ListingSource = ""
			row.ListingConfidence = ""
			row.ListingCheckedAt = nil
			if row.Ticker != newTicker {
				row.LongbridgeListingCheckCount = 0
				row.LongbridgeListingLastResult = ""
				row.LongbridgeListingNextRetryAt = nil
			}
		}
		row.Ticker = newTicker
		row.TickerSource = "https://www.sec.gov/files/company_tickers_exchange.json"
		row.TickerConfidence = "high"
		if err := s.db.WithContext(ctx).Save(&row).Error; err != nil {
			return err
		}
	}
	for _, storedCIK := range tracked {
		if matched[storedCIK] {
			continue
		}
		var row model.IPOCompanyMarketData
		if err := s.db.WithContext(ctx).Where("cik = ?", storedCIK).First(&row).Error; err != nil && err != gorm.ErrRecordNotFound {
			return err
		} else if err == nil && (row.TickerSource == longbridgeIPOCalendarSource || row.ListingSource == longbridgeIPOCalendarSource) {
			// The SEC listed-company file cannot disprove a calendar entry. Keep
			// the planned ticker/date until an SEC mapping or a future calendar
			// refresh supersedes it.
			continue
		}
		if err := s.db.WithContext(ctx).Model(&model.IPOCompanyMarketData{}).Where("cik = ?", storedCIK).Updates(map[string]any{
			"ticker": "", "exchange": "", "listed_verified_at": nil, "listing_date": nil, "listing_checked_at": nil,
			"ticker_source": "", "ticker_confidence": "", "listing_source": "", "listing_confidence": "", "longbridge_listing_check_count": 0, "longbridge_listing_last_result": "", "longbridge_listing_next_retry_at": nil, "updated_at": now,
		}).Error; err != nil {
			return err
		}
	}
	return nil
}

func (s *IPORadarService) upsertIPOOffering(ctx context.Context, cik string, source string, offering sec.IPOOffering) error {
	if strings.TrimSpace(cik) == "" {
		return nil
	}
	var row model.IPOCompanyMarketData
	err := s.db.WithContext(ctx).Where("cik = ?", cik).First(&row).Error
	if err == gorm.ErrRecordNotFound {
		row = model.IPOCompanyMarketData{CIK: cik}
	} else if err != nil {
		return err
	}
	row.OfferPrice = offering.OfferPrice
	row.SharesOffered = offering.SharesOffered
	row.GrossProceeds = offering.GrossProceeds
	row.OfferingSource = source
	row.OfferingConfidence = offering.Confidence
	now := time.Now().UTC()
	row.OfferingCheckedAt = &now
	row.OfferingParserVersion = ipoOfferingParserVersion
	return s.db.WithContext(ctx).Save(&row).Error
}

func filingAfter(left model.IPOFiling, right model.IPOFiling) bool {
	if left.FilingDate.After(right.FilingDate) {
		return true
	}
	if left.FilingDate.Before(right.FilingDate) {
		return false
	}
	if left.AcceptedAt != nil && right.AcceptedAt == nil {
		return true
	}
	if left.AcceptedAt != nil && right.AcceptedAt != nil && left.AcceptedAt.After(*right.AcceptedAt) {
		return true
	}
	return false
}

func inferIPOCompanyStatus(filings []model.IPOFiling, matchedTicker string, pendingTicker string, latestFilingDate time.Time, now time.Time) (string, string, string) {
	hasAmendment := false
	hasEffect := false
	hasPriced := false
	hasWithdrawn := false
	for _, filing := range filings {
		form := strings.ToUpper(strings.TrimSpace(filing.FilingType))
		if form == "RW" || strings.HasPrefix(form, "RW ") {
			hasWithdrawn = true
		}
		if form == "EFFECT" {
			hasEffect = true
		}
		if strings.HasPrefix(form, "424B4") {
			hasPriced = true
		}
		if strings.HasSuffix(form, "/A") {
			hasAmendment = true
		}
	}
	switch {
	case hasWithdrawn:
		return "withdrawn", "detected RW withdrawal filing", "high"
	case strings.TrimSpace(matchedTicker) != "":
		return "listed", "matched ticker " + strings.TrimSpace(matchedTicker), "high"
	case strings.TrimSpace(pendingTicker) != "":
		return "listing_pending", "SEC ticker " + strings.TrimSpace(pendingTicker) + " awaits exchange confirmation", "medium"
	case hasPriced:
		return "priced", "detected 424B pricing filing", "high"
	case hasEffect:
		return "effective", "detected EFFECT filing", "high"
	case !latestFilingDate.IsZero() && latestFilingDate.Before(now.UTC().AddDate(0, 0, -60)):
		return "stale", "no IPO filing update for over 60 days", "medium"
	case hasAmendment:
		return "updating", "detected amendment filing", "high"
	default:
		return "new", "detected initial IPO registration filing", "medium"
	}
}

func validIPOStatus(status string) bool {
	switch status {
	case "new", "updating", "effective", "priced", "listing_pending", "listed", "withdrawn", "stale":
		return true
	default:
		return false
	}
}

func sortIPOCompanies(items []IPOCompanyItem, sortBy string, sortOrder string) {
	statusRank := map[string]int{
		"new":             0,
		"updating":        1,
		"effective":       2,
		"priced":          3,
		"listing_pending": 4,
		"listed":          5,
		"withdrawn":       6,
		"stale":           7,
	}
	sortBy = strings.ToLower(strings.TrimSpace(sortBy))
	defaultSort := sortBy == ""
	if sortBy != "status" && sortBy != "latest_update" {
		sortBy = "latest_update"
	}
	ascending := strings.EqualFold(strings.TrimSpace(sortOrder), "asc")
	sort.SliceStable(items, func(i, j int) bool {
		left, right := items[i], items[j]
		if defaultSort {
			leftActive := activeIPOCompanyStatus(left.Status)
			rightActive := activeIPOCompanyStatus(right.Status)
			if leftActive != rightActive {
				return leftActive
			}
		}
		if sortBy == "status" {
			leftRank := statusRank[left.Status]
			rightRank := statusRank[right.Status]
			if leftRank != rightRank {
				if ascending {
					return leftRank < rightRank
				}
				return leftRank > rightRank
			}
		}
		leftActivity := ipoCompanyLatestActivity(left)
		rightActivity := ipoCompanyLatestActivity(right)
		if !leftActivity.Equal(rightActivity) {
			if sortBy == "latest_update" && ascending {
				return leftActivity.Before(rightActivity)
			}
			return leftActivity.After(rightActivity)
		}
		leftName := strings.ToLower(left.CompanyName)
		rightName := strings.ToLower(right.CompanyName)
		if leftName != rightName {
			return leftName < rightName
		}
		return left.CIK < right.CIK
	})
}

func activeIPOCompanyStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "new", "updating", "effective", "priced", "listing_pending":
		return true
	default:
		return false
	}
}

func ipoRequiresMarketMapping(item IPOCompanyItem) bool {
	if strings.TrimSpace(item.FinalTicker) != "" {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(item.Status)) {
	case "effective", "priced", "listing_pending":
		return true
	default:
		return false
	}
}

func ipoCompanyLatestActivity(item IPOCompanyItem) time.Time {
	if item.LatestAcceptedAt != nil && !item.LatestAcceptedAt.IsZero() {
		return *item.LatestAcceptedAt
	}
	return item.LatestFilingDate
}

func shouldNotifyIPOFiling(filing model.IPOFiling, settings IPORadarSettings) bool {
	if len(settings.NotifyFormTypes) == 0 {
		return true
	}
	form := strings.ToUpper(strings.TrimSpace(filing.FilingType))
	for _, allowed := range settings.NotifyFormTypes {
		if form == strings.ToUpper(strings.TrimSpace(allowed)) {
			return true
		}
	}
	return false
}

func ipoNotificationCandidate(filing model.IPOFiling, settings IPORadarSettings, initialBaseline bool, lifecycleBackfill bool) NotificationCandidate {
	eventAt := filing.FilingDate
	if filing.AcceptedAt != nil {
		eventAt = *filing.AcceptedAt
	}
	return NotificationCandidate{
		EntityKind: "ipo_filing", FilingID: filing.FilingID, CIK: filing.CIK,
		CompanyName: filing.CompanyName, FilingType: filing.FilingType, Title: filing.Title,
		FilingURL: filing.FilingURL, EventAt: eventAt,
		Reason: ipoNotificationReason(filing, settings, initialBaseline, lifecycleBackfill),
	}
}

func ipoNotificationReason(filing model.IPOFiling, settings IPORadarSettings, initialBaseline bool, lifecycleBackfill bool) string {
	if lifecycleBackfill {
		return "lifecycle_backfill"
	}
	if initialBaseline {
		return "initial_sync"
	}
	if !settings.NotifyEnabled || !shouldNotifyIPOFiling(filing, settings) {
		return "rule_filtered"
	}
	return "eligible"
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
