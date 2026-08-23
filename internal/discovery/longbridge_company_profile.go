package discovery

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"sec_monitor/internal/config"

	lbconfig "github.com/longbridge/openapi-go/config"
	lbfundamental "github.com/longbridge/openapi-go/fundamental"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const longbridgeCompanyProfileProvider = "longbridge"

const companyProfileBulkRetrySameFailureLimit = 3

var companyProfileBulkRetryMu sync.Mutex

// LongbridgeCompanyOverview is a deliberately small, provider-neutral copy of
// the fields that are useful in an issuer detail panel. Keeping it local also
// means opening a detail page never triggers an external request.
type LongbridgeCompanyOverview struct {
	CompanyName string
	Profile     string
	Website     string
	Founded     string
	ListingDate string
	Market      string
	Address     string
	Employees   string
	Manager     string
	YearEnd     string
}

type longbridgeCompanyClient interface {
	Company(context.Context, string) (LongbridgeCompanyOverview, error)
}

type LongbridgeCompanyProfileOptions struct {
	AppKey          string
	AppSecret       string
	AccessToken     string
	TTLDays         int
	RequestInterval time.Duration
	Now             func() time.Time
	NewClient       func(appKey, appSecret, accessToken string) (longbridgeCompanyClient, error)
}

// FetchLongbridgeCompanyOverview reads one issuer overview without requiring a
// pre-existing Security/Listing row. It is intended for an explicit ad-hoc
// evaluation, whose returned snapshot is persisted by the caller; regular
// detail pages must continue to use RefreshLongbridgeCompanyProfile and the
// local cache instead.
func FetchLongbridgeCompanyOverview(ctx context.Context, cfg config.DiscoveryConfig, ticker string) (LongbridgeCompanyOverview, error) {
	ticker = strings.ToUpper(strings.TrimSpace(ticker))
	if ticker == "" {
		return LongbridgeCompanyOverview{}, errors.New("ticker is required")
	}
	if !cfg.LongbridgeCompanyProfileEnabled || cfg.LongbridgeCompanyProfileRequestBudget <= 0 {
		return LongbridgeCompanyOverview{}, errors.New("Longbridge company profile sync is disabled or budget is 0")
	}
	if strings.TrimSpace(cfg.LongbridgeAppKey) == "" || strings.TrimSpace(cfg.LongbridgeAppSecret) == "" || strings.TrimSpace(cfg.LongbridgeAccessToken) == "" {
		return LongbridgeCompanyOverview{}, errors.New("Longbridge app key, app secret, and access token are required")
	}
	client, err := newLongbridgeCompanySDKClient(cfg.LongbridgeAppKey, cfg.LongbridgeAppSecret, cfg.LongbridgeAccessToken)
	if err != nil {
		return LongbridgeCompanyOverview{}, fmt.Errorf("create Longbridge company profile client: %w", err)
	}
	requestCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 25*time.Second)
	defer cancel()
	overview, err := longbridgeFundamentalCall(requestCtx, time.Duration(cfg.LongbridgeFundamentalRequestIntervalMS)*time.Millisecond, func(callCtx context.Context) (LongbridgeCompanyOverview, error) {
		return client.Company(callCtx, ticker+".US")
	})
	if err != nil {
		return LongbridgeCompanyOverview{}, fmt.Errorf("load Longbridge company overview: %w", err)
	}
	return overview, nil
}

type CompanyProfileRefreshResult struct {
	Ticker    string     `json:"ticker"`
	Fetched   bool       `json:"fetched"`
	Cached    bool       `json:"cached"`
	Deferred  bool       `json:"deferred"`
	Stale     bool       `json:"stale"`
	Message   string     `json:"message"`
	FetchedAt *time.Time `json:"fetched_at,omitempty"`
}

type CompanyProfileSyncResult struct {
	CandidateCount int    `json:"candidate_count"`
	Attempted      int    `json:"attempted"`
	Fetched        int    `json:"fetched"`
	Cached         int    `json:"cached"`
	Deferred       int    `json:"deferred"`
	Failed         int    `json:"failed"`
	Skipped        bool   `json:"skipped"`
	Message        string `json:"message"`
}

// CompanyProfileBulkRetryResult is the bounded outcome of an operator's
// one-click retry action. Individual provider failures are recorded on the
// affected issuer and reported here rather than failing the entire operation.
type CompanyProfileBulkRetryResult struct {
	QueueCount int    `json:"queue_count"`
	Budget     int    `json:"budget"`
	Attempted  int    `json:"attempted"`
	Fetched    int    `json:"fetched"`
	Failed     int    `json:"failed"`
	Stopped    bool   `json:"stopped"`
	StopReason string `json:"stop_reason,omitempty"`
	Skipped    bool   `json:"skipped"`
	Message    string `json:"message"`
}

// CompanyProfileRecoveryQueue exposes only local, retryable company-profile
// failures for the current candidate universe. It does not make a provider
// call while the discovery-log page is loading.
type CompanyProfileRecoveryQueue struct {
	Items []CompanyProfileRecoveryItem `json:"items"`
}

type CompanyProfileRecoveryItem struct {
	Ticker        string     `json:"ticker"`
	CompanyName   string     `json:"company_name"`
	CIK           string     `json:"cik"`
	SecurityID    uint       `json:"security_id"`
	RetryCount    int        `json:"retry_count"`
	LastAttemptAt *time.Time `json:"last_attempt_at,omitempty"`
	NextRetryAt   *time.Time `json:"next_retry_at,omitempty"`
	LastError     string     `json:"last_error"`
	RetryDue      bool       `json:"retry_due"`
}

// RefreshLongbridgeCompanyProfile refreshes one explicitly requested issuer.
// force should only be set by an operator action; normal scheduled syncing
// respects the local freshness window.
func RefreshLongbridgeCompanyProfile(ctx context.Context, db *gorm.DB, cfg config.DiscoveryConfig, ticker, cik string, force bool) (CompanyProfileRefreshResult, error) {
	result := CompanyProfileRefreshResult{Ticker: strings.ToUpper(strings.TrimSpace(ticker))}
	if db == nil {
		return result, errors.New("database is required")
	}
	if ctx == nil {
		return result, errors.New("context is required")
	}
	security, listing, err := resolveCompanyProfileSecurity(ctx, db, result.Ticker, cik)
	if err != nil {
		return result, err
	}
	if result.Ticker == "" {
		result.Ticker = strings.ToUpper(strings.TrimSpace(listing.Ticker))
	}
	options := LongbridgeCompanyProfileOptions{
		AppKey: cfg.LongbridgeAppKey, AppSecret: cfg.LongbridgeAppSecret, AccessToken: cfg.LongbridgeAccessToken,
		TTLDays: cfg.LongbridgeCompanyProfileTTLDays, RequestInterval: time.Duration(cfg.LongbridgeFundamentalRequestIntervalMS) * time.Millisecond,
	}
	return refreshLongbridgeCompanyProfile(ctx, db, security, listing, options, force)
}

// SyncCurrentCandidateLongbridgeCompanyProfiles fills missing or expired
// company descriptions for the current candidate universe. It is deliberately
// bounded by request budget: a run can be repeated safely without re-fetching
// already-fresh issuers.
func SyncCurrentCandidateLongbridgeCompanyProfiles(ctx context.Context, db *gorm.DB, cfg config.DiscoveryConfig) (CompanyProfileSyncResult, error) {
	result := CompanyProfileSyncResult{}
	if db == nil {
		return result, errors.New("database is required")
	}
	if !cfg.LongbridgeCompanyProfileEnabled {
		result.Skipped, result.Message = true, "Longbridge 公司资料补充已关闭"
		return result, nil
	}
	if strings.TrimSpace(cfg.LongbridgeAppKey) == "" || strings.TrimSpace(cfg.LongbridgeAppSecret) == "" || strings.TrimSpace(cfg.LongbridgeAccessToken) == "" {
		result.Skipped, result.Message = true, "Longbridge 凭据未配置，已跳过公司资料补充"
		return result, nil
	}
	if cfg.LongbridgeCompanyProfileRequestBudget <= 0 {
		result.Skipped, result.Message = true, "Longbridge 公司资料请求预算为 0，已跳过"
		return result, nil
	}
	batch, ok, err := currentPublishedPrescreenBatch(ctx, db)
	if err != nil {
		return result, err
	}
	if !ok {
		result.Skipped, result.Message = true, "暂无已发布的小盘候选批次"
		return result, nil
	}
	var scores []CandidateScoreSnapshot
	if err := db.WithContext(ctx).Where("batch_id = ?", batch.BatchID).Order("total_score DESC, ticker ASC").Find(&scores).Error; err != nil {
		return result, fmt.Errorf("load current candidate profiles: %w", err)
	}
	result.CandidateCount = len(scores)
	profileBySecurity, err := companyProfileSnapshotsBySecurity(ctx, db, scores)
	if err != nil {
		return result, err
	}
	now := time.Now().UTC()
	sort.SliceStable(scores, func(left, right int) bool {
		return companyProfileRefreshPriority(profileBySecurity[scores[left].SecurityID], cfg.LongbridgeCompanyProfileTTLDays, now) <
			companyProfileRefreshPriority(profileBySecurity[scores[right].SecurityID], cfg.LongbridgeCompanyProfileTTLDays, now)
	})
	seen := make(map[uint]struct{}, len(scores))
	for _, score := range scores {
		if result.Attempted >= cfg.LongbridgeCompanyProfileRequestBudget {
			break
		}
		if _, duplicate := seen[score.SecurityID]; duplicate || score.SecurityID == 0 {
			continue
		}
		seen[score.SecurityID] = struct{}{}
		var candidateSecurity Security
		if err := db.WithContext(ctx).First(&candidateSecurity, score.SecurityID).Error; err != nil {
			result.Failed++
			continue
		}
		security, listing, err := resolveCompanyProfileSecurity(ctx, db, score.Ticker, candidateSecurity.CIK)
		if err != nil {
			_ = saveLongbridgeCompanyProfileAttempt(ctx, db, candidateSecurity.ID, score.Ticker, now, err)
			result.Failed++
			continue
		}
		refreshed, err := refreshLongbridgeCompanyProfile(ctx, db, security, listing, LongbridgeCompanyProfileOptions{
			AppKey: cfg.LongbridgeAppKey, AppSecret: cfg.LongbridgeAppSecret, AccessToken: cfg.LongbridgeAccessToken,
			TTLDays: cfg.LongbridgeCompanyProfileTTLDays, RequestInterval: time.Duration(cfg.LongbridgeFundamentalRequestIntervalMS) * time.Millisecond,
		}, false)
		if refreshed.Cached {
			result.Cached++
			continue
		}
		if refreshed.Deferred {
			result.Deferred++
			continue
		}
		result.Attempted++
		if err != nil {
			result.Failed++
			continue
		}
		if refreshed.Fetched {
			result.Fetched++
		}
	}
	if result.Attempted == 0 && result.Cached > 0 {
		result.Message = "当前候选的 Longbridge 公司资料均在有效期内"
	} else {
		result.Message = fmt.Sprintf("已补充 %d 家公司资料，失败 %d 家，延后重试 %d 家", result.Fetched, result.Failed, result.Deferred)
	}
	return result, nil
}

// RetryCurrentCandidateLongbridgeCompanyProfiles retries the current local
// company-profile recovery queue in order. It deliberately uses the same
// configured request budget as scheduled enrichment and stops early when the
// provider is clearly unavailable, so one click cannot fan out into a large
// burst of known-failing Longbridge requests.
func RetryCurrentCandidateLongbridgeCompanyProfiles(ctx context.Context, db *gorm.DB, cfg config.DiscoveryConfig) (CompanyProfileBulkRetryResult, error) {
	if !companyProfileBulkRetryMu.TryLock() {
		return CompanyProfileBulkRetryResult{}, errors.New("company profile bulk retry is already running")
	}
	defer companyProfileBulkRetryMu.Unlock()
	return retryCurrentCandidateLongbridgeCompanyProfiles(ctx, db, cfg, LongbridgeCompanyProfileOptions{
		AppKey: cfg.LongbridgeAppKey, AppSecret: cfg.LongbridgeAppSecret, AccessToken: cfg.LongbridgeAccessToken,
		TTLDays: cfg.LongbridgeCompanyProfileTTLDays, RequestInterval: time.Duration(cfg.LongbridgeFundamentalRequestIntervalMS) * time.Millisecond,
	})
}

func retryCurrentCandidateLongbridgeCompanyProfiles(ctx context.Context, db *gorm.DB, cfg config.DiscoveryConfig, options LongbridgeCompanyProfileOptions) (CompanyProfileBulkRetryResult, error) {
	result := CompanyProfileBulkRetryResult{Budget: cfg.LongbridgeCompanyProfileRequestBudget}
	if db == nil {
		return result, errors.New("database is required")
	}
	if !cfg.LongbridgeCompanyProfileEnabled {
		result.Skipped, result.Message = true, "Longbridge 公司资料补充已关闭"
		return result, nil
	}
	if strings.TrimSpace(options.AppKey) == "" || strings.TrimSpace(options.AppSecret) == "" || strings.TrimSpace(options.AccessToken) == "" {
		result.Skipped, result.Message = true, "Longbridge 凭据未配置，已跳过一键重试"
		return result, nil
	}
	if result.Budget <= 0 {
		result.Skipped, result.Message = true, "Longbridge 公司资料请求预算为 0，已跳过一键重试"
		return result, nil
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	now := options.Now().UTC()
	queue, err := ListCurrentCandidateCompanyProfileRecoveryQueue(ctx, db, options.TTLDays, now)
	if err != nil {
		return result, err
	}
	result.QueueCount = len(queue.Items)
	if result.QueueCount == 0 {
		result.Message = "当前候选没有待补偿的公司资料"
		return result, nil
	}

	lastFailureKind, sameFailureCount := "", 0
	for _, item := range queue.Items {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		if result.Attempted >= result.Budget {
			break
		}
		result.Attempted++
		security, listing, err := resolveCompanyProfileSecurity(ctx, db, item.Ticker, item.CIK)
		if err == nil {
			_, err = refreshLongbridgeCompanyProfile(ctx, db, security, listing, options, true)
		}
		if err == nil {
			result.Fetched++
			lastFailureKind, sameFailureCount = "", 0
			continue
		}
		result.Failed++
		failureKind := companyProfileBulkRetryFailureKind(err)
		if failureKind == lastFailureKind {
			sameFailureCount++
		} else {
			lastFailureKind, sameFailureCount = failureKind, 1
		}
		if companyProfileBulkRetryMustStop(failureKind, sameFailureCount) {
			result.Stopped = true
			result.StopReason = companyProfileBulkRetryStopReason(failureKind, sameFailureCount)
			break
		}
	}
	result.Message = fmt.Sprintf("一键重试已尝试 %d/%d 家，成功 %d 家，失败 %d 家", result.Attempted, result.QueueCount, result.Fetched, result.Failed)
	if result.Stopped {
		result.Message += "；" + result.StopReason
	} else if result.Attempted < result.QueueCount && result.Attempted >= result.Budget {
		result.Message += fmt.Sprintf("；已达到本次请求预算 %d", result.Budget)
	}
	return result, nil
}

func companyProfileBulkRetryFailureKind(err error) string {
	message := strings.ToLower(err.Error())
	switch {
	case strings.Contains(message, "429"), strings.Contains(message, "rate limit"):
		return "rate_limited"
	case strings.Contains(message, "401"), strings.Contains(message, "403"), strings.Contains(message, "unauthorized"), strings.Contains(message, "forbidden"), strings.Contains(message, "credential"):
		return "credentials"
	case strings.Contains(message, "eof"), strings.Contains(message, "timeout"), strings.Contains(message, "deadline exceeded"), strings.Contains(message, "connection reset"), strings.Contains(message, "connection refused"):
		return "transport"
	default:
		return "other"
	}
}

func companyProfileBulkRetryMustStop(failureKind string, sameFailureCount int) bool {
	return failureKind == "rate_limited" || failureKind == "credentials" || sameFailureCount >= companyProfileBulkRetrySameFailureLimit
}

func companyProfileBulkRetryStopReason(failureKind string, sameFailureCount int) string {
	switch failureKind {
	case "rate_limited":
		return "Longbridge 已限流，已停止后续请求"
	case "credentials":
		return "Longbridge 凭据或权限异常，已停止后续请求"
	default:
		return fmt.Sprintf("连续 %d 次出现相同的 Longbridge 上游错误，已停止后续请求", sameFailureCount)
	}
}

// ListCurrentCandidateCompanyProfileRecoveryQueue reads persisted failed
// attempts and turns them into an operator-friendly, single-issuer retry
// queue. Fresh successful profiles are intentionally omitted even if a later
// forced refresh happened to fail: the local profile remains usable.
func ListCurrentCandidateCompanyProfileRecoveryQueue(ctx context.Context, db *gorm.DB, ttlDays int, now time.Time) (CompanyProfileRecoveryQueue, error) {
	result := CompanyProfileRecoveryQueue{Items: []CompanyProfileRecoveryItem{}}
	if db == nil {
		return result, errors.New("database is required")
	}
	if ttlDays <= 0 {
		ttlDays = 30
	}
	batch, ok, err := currentPublishedPrescreenBatch(ctx, db)
	if err != nil || !ok {
		return result, err
	}
	var scores []CandidateScoreSnapshot
	if err := db.WithContext(ctx).Select("security_id", "ticker").Where("batch_id = ?", batch.BatchID).Find(&scores).Error; err != nil {
		return result, fmt.Errorf("load current candidate profile recovery queue: %w", err)
	}
	profiles, err := companyProfileSnapshotsBySecurity(ctx, db, scores)
	if err != nil {
		return result, err
	}
	securityIDs := make([]uint, 0, len(scores))
	for _, score := range scores {
		if score.SecurityID != 0 {
			securityIDs = append(securityIDs, score.SecurityID)
		}
	}
	var securities []Security
	if len(securityIDs) > 0 {
		if err := db.WithContext(ctx).Where("id IN ?", securityIDs).Find(&securities).Error; err != nil {
			return result, fmt.Errorf("load candidate profile recovery issuers: %w", err)
		}
	}
	securityByID := make(map[uint]Security, len(securities))
	for _, security := range securities {
		securityByID[security.ID] = security
	}
	for _, score := range scores {
		snapshot, ok := profiles[score.SecurityID]
		if !ok || strings.TrimSpace(snapshot.LastError) == "" || companyProfileSnapshotFresh(snapshot, ttlDays, now) {
			continue
		}
		security := securityByID[score.SecurityID]
		retryDue := snapshot.NextRetryAt == nil || !snapshot.NextRetryAt.After(now)
		result.Items = append(result.Items, CompanyProfileRecoveryItem{
			Ticker: strings.ToUpper(strings.TrimSpace(score.Ticker)), CompanyName: security.CompanyName, CIK: security.CIK, SecurityID: score.SecurityID,
			RetryCount: snapshot.RetryCount, LastAttemptAt: snapshot.LastAttemptAt, NextRetryAt: snapshot.NextRetryAt,
			LastError: snapshot.LastError, RetryDue: retryDue,
		})
	}
	sort.Slice(result.Items, func(left, right int) bool {
		if result.Items[left].RetryDue != result.Items[right].RetryDue {
			return result.Items[left].RetryDue
		}
		leftAt, rightAt := result.Items[left].NextRetryAt, result.Items[right].NextRetryAt
		if leftAt == nil || rightAt == nil {
			return leftAt != nil
		}
		if !leftAt.Equal(*rightAt) {
			return leftAt.Before(*rightAt)
		}
		return result.Items[left].Ticker < result.Items[right].Ticker
	})
	return result, nil
}

func companyProfileSnapshotsBySecurity(ctx context.Context, db *gorm.DB, scores []CandidateScoreSnapshot) (map[uint]CompanyProfileSnapshot, error) {
	result := map[uint]CompanyProfileSnapshot{}
	securityIDs := make([]uint, 0, len(scores))
	for _, score := range scores {
		if score.SecurityID != 0 {
			securityIDs = append(securityIDs, score.SecurityID)
		}
	}
	if len(securityIDs) == 0 {
		return result, nil
	}
	var snapshots []CompanyProfileSnapshot
	if err := db.WithContext(ctx).Where("provider = ? AND security_id IN ?", longbridgeCompanyProfileProvider, securityIDs).Find(&snapshots).Error; err != nil {
		return nil, fmt.Errorf("load current candidate company profile attempts: %w", err)
	}
	for _, snapshot := range snapshots {
		result[snapshot.SecurityID] = snapshot
	}
	return result, nil
}

func companyProfileRefreshPriority(snapshot CompanyProfileSnapshot, ttlDays int, now time.Time) int {
	if strings.TrimSpace(snapshot.LastError) != "" && !companyProfileSnapshotFresh(snapshot, ttlDays, now) && (snapshot.NextRetryAt == nil || !snapshot.NextRetryAt.After(now)) {
		return 0 // due failures are repaired before requesting new profiles.
	}
	if snapshot.FetchedAt == nil {
		return 1
	}
	if !companyProfileSnapshotFresh(snapshot, ttlDays, now) {
		return 2
	}
	return 3
}

func companyProfileSnapshotFresh(snapshot CompanyProfileSnapshot, ttlDays int, now time.Time) bool {
	if snapshot.FetchedAt == nil {
		return false
	}
	if ttlDays <= 0 {
		ttlDays = 30
	}
	return snapshot.FetchedAt.AddDate(0, 0, ttlDays).After(now)
}

func refreshLongbridgeCompanyProfile(ctx context.Context, db *gorm.DB, security Security, listing Listing, options LongbridgeCompanyProfileOptions, force bool) (CompanyProfileRefreshResult, error) {
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.TTLDays <= 0 {
		options.TTLDays = 30
	}
	now := options.Now().UTC()
	result := CompanyProfileRefreshResult{Ticker: strings.ToUpper(strings.TrimSpace(listing.Ticker))}
	var cached CompanyProfileSnapshot
	err := db.WithContext(ctx).Where("provider = ? AND security_id = ?", longbridgeCompanyProfileProvider, security.ID).First(&cached).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return result, fmt.Errorf("load cached Longbridge company profile: %w", err)
	}
	if err == nil && cached.FetchedAt != nil {
		result.FetchedAt = cached.FetchedAt
		fresh := companyProfileSnapshotFresh(cached, options.TTLDays, now)
		if fresh && !force {
			result.Cached, result.Message = true, "已使用本地 Longbridge 公司资料缓存"
			return result, nil
		}
		result.Stale = !fresh
	}
	if !force && cached.NextRetryAt != nil && cached.NextRetryAt.After(now) {
		result.Deferred = true
		result.Message = fmt.Sprintf("上次请求失败，已延后至 %s 自动重试", cached.NextRetryAt.UTC().Format(time.RFC3339))
		return result, nil
	}
	if strings.TrimSpace(options.AppKey) == "" || strings.TrimSpace(options.AppSecret) == "" || strings.TrimSpace(options.AccessToken) == "" {
		return result, errors.New("Longbridge app key, app secret, and access token are required")
	}
	if options.NewClient == nil {
		options.NewClient = newLongbridgeCompanySDKClient
	}
	client, err := options.NewClient(options.AppKey, options.AppSecret, options.AccessToken)
	if err != nil {
		return result, fmt.Errorf("create Longbridge company client: %w", err)
	}
	overview, err := longbridgeFundamentalCall(ctx, options.RequestInterval, func(callCtx context.Context) (LongbridgeCompanyOverview, error) {
		return client.Company(callCtx, longbridgeSymbol(listing))
	})
	if err != nil {
		_ = saveLongbridgeCompanyProfileAttempt(ctx, db, security.ID, listing.Ticker, now, err)
		return result, fmt.Errorf("load Longbridge company overview: %w", err)
	}
	snapshot := CompanyProfileSnapshot{
		SecurityID: security.ID, Provider: longbridgeCompanyProfileProvider, Ticker: strings.ToUpper(strings.TrimSpace(listing.Ticker)),
		CompanyName: strings.TrimSpace(overview.CompanyName), Profile: strings.TrimSpace(overview.Profile), Website: strings.TrimSpace(overview.Website),
		Founded: strings.TrimSpace(overview.Founded), ListingDate: strings.TrimSpace(overview.ListingDate), Market: strings.TrimSpace(overview.Market),
		Address: strings.TrimSpace(overview.Address), Employees: strings.TrimSpace(overview.Employees), Manager: strings.TrimSpace(overview.Manager), YearEnd: strings.TrimSpace(overview.YearEnd),
		FetchedAt: &now, LastAttemptAt: &now,
	}
	if err := db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "provider"}, {Name: "security_id"}},
		DoUpdates: clause.Assignments(map[string]any{
			"ticker": snapshot.Ticker, "company_name": snapshot.CompanyName, "profile": snapshot.Profile, "website": snapshot.Website,
			"founded": snapshot.Founded, "listing_date": snapshot.ListingDate, "market": snapshot.Market, "address": snapshot.Address,
			"employees": snapshot.Employees, "manager": snapshot.Manager, "year_end": snapshot.YearEnd, "fetched_at": now,
			"last_attempt_at": now, "last_error": "", "retry_count": 0, "next_retry_at": gorm.Expr("NULL"), "updated_at": now,
		}),
	}).Create(&snapshot).Error; err != nil {
		return result, fmt.Errorf("save Longbridge company overview: %w", err)
	}
	// SQLite conflict updates can retain the old nullable value when the
	// incoming struct field is nil. Clear retry state explicitly so a recovered
	// profile never remains in the operator retry queue.
	if err := db.WithContext(ctx).Model(&CompanyProfileSnapshot{}).
		Where("provider = ? AND security_id = ?", longbridgeCompanyProfileProvider, security.ID).
		Update("next_retry_at", nil).Error; err != nil {
		return result, fmt.Errorf("clear Longbridge company profile retry state: %w", err)
	}
	result.Fetched, result.FetchedAt, result.Message = true, &now, "已从 Longbridge 更新公司资料"
	return result, nil
}

func saveLongbridgeCompanyProfileAttempt(ctx context.Context, db *gorm.DB, securityID uint, ticker string, attemptedAt time.Time, fetchErr error) error {
	retryCount := 1
	var existing CompanyProfileSnapshot
	if err := db.WithContext(ctx).Where("provider = ? AND security_id = ?", longbridgeCompanyProfileProvider, securityID).First(&existing).Error; err == nil {
		retryCount = existing.RetryCount + 1
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return fmt.Errorf("load Longbridge company profile retry state: %w", err)
	}
	nextRetryAt := attemptedAt.Add(companyProfileRetryDelay(fetchErr, retryCount))
	snapshot := CompanyProfileSnapshot{
		SecurityID: securityID, Provider: longbridgeCompanyProfileProvider, Ticker: strings.ToUpper(strings.TrimSpace(ticker)),
		LastAttemptAt: &attemptedAt, LastError: fetchErr.Error(), RetryCount: retryCount, NextRetryAt: &nextRetryAt,
	}
	return db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "provider"}, {Name: "security_id"}},
		DoUpdates: clause.Assignments(map[string]any{"ticker": snapshot.Ticker, "last_attempt_at": attemptedAt, "last_error": fetchErr.Error(), "retry_count": retryCount, "next_retry_at": nextRetryAt, "updated_at": attemptedAt}),
	}).Create(&snapshot).Error
}

func companyProfileRetryDelay(fetchErr error, retryCount int) time.Duration {
	if retryCount < 1 {
		retryCount = 1
	}
	if strings.Contains(strings.ToLower(fetchErr.Error()), "429") || strings.Contains(strings.ToLower(fetchErr.Error()), "rate limit") {
		return 30 * time.Minute
	}
	delays := []time.Duration{5 * time.Minute, 15 * time.Minute, time.Hour, 6 * time.Hour, 24 * time.Hour}
	if retryCount > len(delays) {
		return delays[len(delays)-1]
	}
	return delays[retryCount-1]
}

func resolveCompanyProfileSecurity(ctx context.Context, db *gorm.DB, ticker, cik string) (Security, Listing, error) {
	var security Security
	query := db.WithContext(ctx).Model(&Security{})
	if normalizedCIK := strings.TrimSpace(cik); normalizedCIK != "" {
		query = query.Where("cik = ?", normalizedCIK)
	} else if normalizedTicker := strings.ToUpper(strings.TrimSpace(ticker)); normalizedTicker != "" {
		query = query.Joins("JOIN listings ON listings.security_id = securities.id").Where("listings.ticker = ?", normalizedTicker)
	} else {
		return security, Listing{}, errors.New("ticker or cik is required")
	}
	if err := query.First(&security).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return security, Listing{}, errors.New("issuer is not present in the local SEC security universe")
		}
		return security, Listing{}, fmt.Errorf("resolve local issuer: %w", err)
	}
	var listing Listing
	if err := db.WithContext(ctx).Where("security_id = ?", security.ID).Order("valid_to IS NULL DESC, valid_from DESC, id DESC").First(&listing).Error; err == nil {
		return security, listing, nil
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return security, listing, fmt.Errorf("resolve issuer listing: %w", err)
	}

	// A security can be represented solely by a published batch identity (for
	// example after a catalog reconciliation). That snapshot is the frozen
	// ticker mapping used by the current candidate batch and is safe to use for
	// a provider lookup; do not reject a detail-page refresh just because the
	// mutable listing catalog has not been materialized yet.
	var identity SecurityBatchIdentity
	err := db.WithContext(ctx).Model(&SecurityBatchIdentity{}).
		Joins("JOIN universe_batches ON universe_batches.batch_id = security_batch_identities.batch_id").
		Where("security_batch_identities.security_id = ? AND universe_batches.kind = ? AND universe_batches.status = ?", security.ID, BatchKindSecurity, BatchStatusPublished).
		Order("security_batch_identities.created_at DESC, security_batch_identities.id DESC").First(&identity).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return security, listing, errors.New("issuer has no published ticker mapping in the local SEC security universe")
		}
		return security, listing, fmt.Errorf("resolve published issuer listing: %w", err)
	}
	return security, Listing{SecurityID: security.ID, Ticker: identity.Ticker, ProviderTicker: identity.ProviderTicker, Exchange: identity.Exchange, ValidFrom: identity.CreatedAt, MappingStatus: identity.MappingStatus}, nil
}

type longbridgeCompanySDKClient struct {
	fundamental *lbfundamental.FundamentalContext
}

func newLongbridgeCompanySDKClient(appKey, appSecret, accessToken string) (longbridgeCompanyClient, error) {
	cfg, err := lbconfig.New(lbconfig.WithConfigKey(appKey, appSecret, accessToken))
	if err != nil {
		return nil, err
	}
	client, err := lbfundamental.NewFromCfg(cfg)
	if err != nil {
		return nil, err
	}
	return &longbridgeCompanySDKClient{fundamental: client}, nil
}

func (c *longbridgeCompanySDKClient) Company(ctx context.Context, symbol string) (LongbridgeCompanyOverview, error) {
	overview, err := c.fundamental.Company(ctx, symbol)
	if err != nil {
		return LongbridgeCompanyOverview{}, err
	}
	if overview == nil {
		return LongbridgeCompanyOverview{}, errors.New("Longbridge returned an empty company overview")
	}
	return LongbridgeCompanyOverview{CompanyName: overview.CompanyName, Profile: overview.Profile, Website: overview.Website, Founded: overview.Founded, ListingDate: overview.ListingDate, Market: overview.Market, Address: overview.Address, Employees: overview.Employees, Manager: overview.Manager, YearEnd: overview.YearEnd}, nil
}
