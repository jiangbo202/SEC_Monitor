package service

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strings"
	"time"

	"sec_monitor/internal/config"
	"sec_monitor/internal/discovery"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	DiscoverySyncStatusPublished    = "published"
	DiscoverySyncStatusMarketFailed = "market_failed"
)

var ErrDiscoveryMarketSync = errors.New("discovery market sync failed")

type DiscoverySyncRunner interface {
	SyncSecurityUniverse(context.Context) (discovery.UniverseBatch, error)
	SyncMarketPrices(context.Context) (discovery.UniverseBatch, error)
}

type DiscoverySyncService struct {
	db      *gorm.DB
	cfg     config.DiscoveryConfig
	configs *ConfigService
	runner  DiscoverySyncRunner
}

type productionDiscoveryRunner struct {
	security  *discovery.Coordinator
	market    *discovery.Coordinator
	marketErr error
}

type DiscoverySyncResult struct {
	Status                 string                       `json:"status"`
	BatchID                string                       `json:"batch_id"`
	SecurityBatchID        string                       `json:"security_batch_id"`
	MarketBatchID          string                       `json:"market_batch_id"`
	SecurityBatch          discovery.UniverseBatch      `json:"security_batch"`
	MarketBatch            discovery.UniverseBatch      `json:"market_batch"`
	Summary                discovery.CandidateSummary   `json:"summary"`
	Health                 discovery.CandidateHealth    `json:"health"`
	TechnicalHistoryWarmup TechnicalHistoryWarmupResult `json:"technical_history_warmup"`
}

type DiscoveryFinancialRefreshResult struct {
	Events       int `json:"events"`
	Companies    int `json:"companies"`
	Facts        int `json:"facts"`
	Recalculated int `json:"recalculated"`
}

// TechnicalHistoryWarmupResult records the non-blocking daily technical
// history pre-warm. Market candidates remain publishable if the optional
// enrichment source is temporarily rate-limited.
type TechnicalHistoryWarmupResult struct {
	Status       string                                   `json:"status"`
	Result       discovery.TechnicalHistoryBackfillResult `json:"result"`
	ErrorMessage string                                   `json:"error_message,omitempty"`
}

func NewDiscoverySyncService(db *gorm.DB, cfg config.DiscoveryConfig) *DiscoverySyncService {
	return &DiscoverySyncService{db: db, cfg: cfg}
}

func (s *DiscoverySyncService) WithRunner(runner DiscoverySyncRunner) *DiscoverySyncService {
	s.runner = runner
	return s
}

func (s *DiscoverySyncService) WithConfigService(configs *ConfigService) *DiscoverySyncService {
	s.configs = configs
	return s
}

func (s *DiscoverySyncService) withRunner(runner DiscoverySyncRunner) *DiscoverySyncService {
	return s.WithRunner(runner)
}

func (s *DiscoverySyncService) Run(ctx context.Context) (DiscoverySyncResult, error) {
	if s == nil || s.db == nil {
		return DiscoverySyncResult{}, errors.New("discovery sync service is not configured")
	}
	runner := s.runner
	if runner == nil {
		built, err := s.buildRunner()
		if err != nil {
			return DiscoverySyncResult{}, err
		}
		runner = built
	}
	securityBatch, err := runner.SyncSecurityUniverse(ctx)
	result := DiscoverySyncResult{SecurityBatch: securityBatch, SecurityBatchID: securityBatch.BatchID}
	if err != nil {
		return result, err
	}
	marketBatch, err := runner.SyncMarketPrices(ctx)
	result.MarketBatch = marketBatch
	result.MarketBatchID = marketBatch.BatchID
	result.BatchID = marketBatch.BatchID
	if err != nil {
		result.Status = DiscoverySyncStatusMarketFailed
		result.Summary, _ = discovery.BuildCandidateSummary(ctx, s.db, 10)
		result.Health, _ = discovery.BuildCandidateHealth(ctx, s.db)
		return result, fmt.Errorf("%w: %v", ErrDiscoveryMarketSync, err)
	}
	result.Status = DiscoverySyncStatusPublished
	result.TechnicalHistoryWarmup = s.autoWarmTechnicalHistory(ctx)
	result.Summary, err = discovery.BuildCandidateSummary(ctx, s.db, 10)
	if err != nil {
		return result, err
	}
	result.Health, err = discovery.BuildCandidateHealth(ctx, s.db)
	return result, err
}

// RefreshDirtyFinancials consumes financial-report events without requesting
// fresh prices. New SEC facts immediately refresh the current financial metric
// and candidate score; the existing published market batch keeps its price
// evidence and effective date.
func (s *DiscoverySyncService) RefreshDirtyFinancials(ctx context.Context) (DiscoveryFinancialRefreshResult, error) {
	result := DiscoveryFinancialRefreshResult{}
	if s == nil || s.db == nil {
		return result, errors.New("discovery sync service is not configured")
	}
	var events []discovery.CandidateRecalcEvent
	if err := s.db.WithContext(ctx).Where("status = ?", discovery.CandidateRecalcStatusDirty).Order("id ASC").Find(&events).Error; err != nil {
		return result, err
	}
	if len(events) == 0 {
		return result, nil
	}
	result.Events = len(events)
	ciks := map[string]struct{}{}
	for _, event := range events {
		if event.CIK != "" {
			ciks[event.CIK] = struct{}{}
		}
	}
	if len(ciks) == 0 {
		return result, nil
	}
	cfg, err := s.appliedDiscoveryConfig(ctx)
	if err != nil {
		return result, err
	}
	timeout := time.Duration(cfg.TaskTimeoutMin) * time.Minute
	if timeout <= 0 {
		timeout = time.Hour
	}
	source := discovery.SECBulkSource{Downloader: newDiscoveryDownloader(cfg, timeout), CompanyFactsURL: cfg.SECCompanyFactsURL, Limits: discovery.ZIPParseLimits{MaxEntries: 1_000_000, MaxEntryBytes: 512 << 20, MaxTotalBytes: 64 << 30}, CacheTTL: discoveryCacheTTL(cfg)}
	facts, _, err := source.LoadFinancialFacts(ctx, ciks)
	if err != nil {
		return result, err
	}
	byCIK := map[string][]discovery.FinancialFact{}
	for _, fact := range facts {
		byCIK[fact.CIK] = append(byCIK[fact.CIK], fact)
	}
	now := time.Now().UTC()
	processedSecurityIDs := make([]uint, 0, len(byCIK))
	for cik, rows := range byCIK {
		var security discovery.Security
		if err := s.db.WithContext(ctx).Where("cik = ?", cik).First(&security).Error; err != nil {
			return result, err
		}
		for _, fact := range rows {
			item := discovery.FinancialFactSnapshot{SecurityID: security.ID, Metric: fact.Metric, Concept: fact.Concept, PeriodStart: fact.PeriodStart, PeriodEnd: fact.PeriodEnd, Accession: fact.Accession, Unit: fact.Unit, AmountMicros: fact.AmountMicros, Form: fact.Form, SourceURL: fact.SourceURL, QualityStatus: discovery.QualityStatusValid, FiledAt: fact.FiledAt, AcceptedAt: fact.AcceptedAt, CreatedAt: now}
			res := s.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&item)
			if res.Error != nil {
				return result, res.Error
			}
			result.Facts += int(res.RowsAffected)
		}
		result.Companies++
		processedSecurityIDs = append(processedSecurityIDs, security.ID)
	}
	if len(processedSecurityIDs) == 0 {
		return result, nil
	}
	recalculated, err := discovery.RefreshCurrentCandidateFinancials(ctx, s.db, processedSecurityIDs, now)
	if err != nil {
		return result, err
	}
	result.Recalculated = recalculated
	if err := s.db.WithContext(ctx).Model(&discovery.CandidateRecalcEvent{}).Where("status = ? AND security_id IN ?", discovery.CandidateRecalcStatusDirty, processedSecurityIDs).Updates(map[string]any{"status": "recalculated", "updated_at": now}).Error; err != nil {
		return result, err
	}
	return result, nil
}

func (s *DiscoverySyncService) RunMarketOnly(ctx context.Context) (DiscoverySyncResult, error) {
	if s == nil || s.db == nil {
		return DiscoverySyncResult{}, errors.New("discovery sync service is not configured")
	}
	runner := s.runner
	if runner == nil {
		built, err := s.buildRunner()
		if err != nil {
			return DiscoverySyncResult{}, err
		}
		runner = built
	}
	marketBatch, err := runner.SyncMarketPrices(ctx)
	result := DiscoverySyncResult{MarketBatch: marketBatch, MarketBatchID: marketBatch.BatchID, BatchID: marketBatch.BatchID}
	if err != nil {
		result.Status = DiscoverySyncStatusMarketFailed
		result.Summary, _ = discovery.BuildCandidateSummary(ctx, s.db, 10)
		result.Health, _ = discovery.BuildCandidateHealth(ctx, s.db)
		return result, fmt.Errorf("%w: %v", ErrDiscoveryMarketSync, err)
	}
	result.Status = DiscoverySyncStatusPublished
	result.TechnicalHistoryWarmup = s.autoWarmTechnicalHistory(ctx)
	result.Summary, err = discovery.BuildCandidateSummary(ctx, s.db, 10)
	if err != nil {
		return result, err
	}
	result.Health, err = discovery.BuildCandidateHealth(ctx, s.db)
	return result, err
}

// BackfillTechnicalHistory warms local daily-price history for the current A/B
// candidate set. It is intentionally independent of market batch publishing.
func (s *DiscoverySyncService) BackfillTechnicalHistory(ctx context.Context, lookbackDays int) (discovery.TechnicalHistoryBackfillResult, error) {
	if s == nil || s.db == nil {
		return discovery.TechnicalHistoryBackfillResult{}, errors.New("discovery sync service is not configured")
	}
	cfg, err := s.appliedDiscoveryConfig(ctx)
	if err != nil {
		return discovery.TechnicalHistoryBackfillResult{}, err
	}
	return s.backfillTechnicalHistoryWithConfig(ctx, cfg, lookbackDays)
}

func (s *DiscoverySyncService) autoWarmTechnicalHistory(ctx context.Context) TechnicalHistoryWarmupResult {
	result := TechnicalHistoryWarmupResult{Status: "skipped", Result: discovery.TechnicalHistoryBackfillResult{SourceRecordCounts: map[string]int{}}}
	// Custom runners are used by deterministic service tests and embedding
	// callers; only the production scheduler performs external enrichment.
	if s == nil || s.runner != nil {
		return result
	}
	cfg, err := s.appliedDiscoveryConfig(ctx)
	if err != nil {
		result.Status = "warning"
		result.ErrorMessage = SanitizeSensitiveError(err.Error())
		log.Printf("discovery technical history warmup skipped: %s", result.ErrorMessage)
		return result
	}
	if !cfg.AutoTechnicalHistoryWarmup {
		return result
	}
	backfill, err := s.backfillTechnicalHistoryWithConfig(ctx, cfg, 0)
	result.Result = backfill
	if err != nil {
		result.Status = "warning"
		result.ErrorMessage = SanitizeSensitiveError(err.Error())
		log.Printf("discovery technical history warmup warning: %s", result.ErrorMessage)
		return result
	}
	result.Status = "completed"
	log.Printf("discovery technical history warmup completed: candidates=%d requested=%d persisted=%d", backfill.CandidateCount, backfill.RequestedCount, backfill.PersistedCount)
	return result
}

func (s *DiscoverySyncService) appliedDiscoveryConfig(ctx context.Context) (config.DiscoveryConfig, error) {
	cfg := s.cfg
	if s.configs == nil {
		return cfg, nil
	}
	return s.configs.ApplyDiscoveryConfig(ctx, cfg)
}

func newDiscoveryDownloader(cfg config.DiscoveryConfig, timeout time.Duration) *discovery.Downloader {
	idleTimeout := time.Duration(cfg.DownloadIdleTimeoutSec) * time.Second
	if idleTimeout <= 0 {
		idleTimeout = 90 * time.Second
	}
	return &discovery.Downloader{
		Client:          &http.Client{Timeout: timeout},
		CacheDir:        cfg.CacheDir,
		MaxBytes:        8 << 30,
		UserAgent:       cfg.UserAgent,
		ReadIdleTimeout: idleTimeout,
	}
}

func discoveryCacheTTL(cfg config.DiscoveryConfig) time.Duration {
	ttl := time.Duration(cfg.SECBulkCacheTTLHours) * time.Hour
	if ttl <= 0 {
		return 12 * time.Hour
	}
	return ttl
}

func (s *DiscoverySyncService) backfillTechnicalHistoryWithConfig(ctx context.Context, cfg config.DiscoveryConfig, lookbackDays int) (discovery.TechnicalHistoryBackfillResult, error) {
	timeout := time.Duration(cfg.TaskTimeoutMin) * time.Minute
	if timeout <= 0 {
		timeout = 60 * time.Minute
	}
	downloads := newDiscoveryDownloader(cfg, timeout)
	calendar, err := discovery.NewDatabaseMarketCalendar(s.db, discovery.DefaultNYSECalendarVersion)
	if err != nil {
		return discovery.TechnicalHistoryBackfillResult{}, err
	}
	provider, marketErr, err := s.buildPriceProvider(cfg, downloads, calendar)
	if err != nil {
		return discovery.TechnicalHistoryBackfillResult{}, err
	}
	if marketErr != nil {
		return discovery.TechnicalHistoryBackfillResult{}, marketErr
	}
	history, ok := provider.(discovery.HistoricalPriceProvider)
	if !ok {
		return discovery.TechnicalHistoryBackfillResult{}, errors.New("configured price provider does not support technical history backfill")
	}
	return discovery.BackfillCandidateTechnicalHistory(ctx, s.db, history, time.Now(), lookbackDays)
}

func (s *DiscoverySyncService) buildRunner() (DiscoverySyncRunner, error) {
	cfg := s.cfg
	if s.configs != nil {
		applied, err := s.configs.ApplyDiscoveryConfig(context.Background(), cfg)
		if err != nil {
			return nil, err
		}
		cfg = applied
	}
	timeout := time.Duration(cfg.TaskTimeoutMin) * time.Minute
	if timeout <= 0 {
		timeout = 60 * time.Minute
	}
	downloader := newDiscoveryDownloader(cfg, timeout)
	limits := discovery.ZIPParseLimits{MaxEntries: 1_000_000, MaxEntryBytes: 512 << 20, MaxTotalBytes: 64 << 30}
	secBulk := discovery.SECBulkSource{
		Downloader:      downloader,
		TickerURL:       cfg.SECTickerExchangeURL,
		SubmissionsURL:  cfg.SECSubmissionsURL,
		CompanyFactsURL: cfg.SECCompanyFactsURL,
		Limits:          limits,
		CacheTTL:        discoveryCacheTTL(cfg),
	}
	nasdaq := discovery.NasdaqDirectorySource{
		Downloader: downloader,
		ListedURL:  cfg.NasdaqListedURL,
		OtherURL:   cfg.NasdaqOtherListedURL,
		CacheTTL:   discoveryCacheTTL(cfg),
	}
	metadata := discovery.CompositeSecurityMetadataSource{
		Nasdaq:           nasdaq,
		SEC:              secBulk,
		IdentityVerifier: discovery.ManualIdentityVerificationSource{DB: s.db},
	}
	calendar, err := discovery.NewDatabaseMarketCalendar(s.db, discovery.DefaultNYSECalendarVersion)
	if err != nil {
		return nil, err
	}
	security := &discovery.Coordinator{
		DB:         s.db,
		Metadata:   metadata,
		Shares:     secBulk,
		Financials: secBulk,
		Insiders: discovery.SECForm4InsiderSource{
			Metadata:     secBulk,
			Downloader:   downloader,
			LookbackDays: 180,
		},
		Events:   discovery.SECSubmissionsCapitalEventSource{Metadata: secBulk},
		Calendar: calendar,
		Clock:    time.Now,
	}
	prices, marketErr, err := s.buildPriceProvider(cfg, downloader, calendar)
	if err != nil {
		return nil, err
	}
	if marketErr != nil {
		return productionDiscoveryRunner{security: security, marketErr: marketErr}, nil
	}
	market := &discovery.Coordinator{
		DB:         s.db,
		Metadata:   metadata,
		Shares:     secBulk,
		Financials: secBulk,
		Insiders: discovery.SECForm4InsiderSource{
			Metadata:     secBulk,
			Downloader:   downloader,
			LookbackDays: 180,
		},
		Events:                discovery.SECSubmissionsCapitalEventSource{Metadata: secBulk},
		Prices:                prices,
		Calendar:              calendar,
		Clock:                 time.Now,
		ResearchMode:          cfg.ResearchMode,
		MinPublishCoveragePct: cfg.MinPublishCoveragePct,
	}
	return productionDiscoveryRunner{security: security, market: market}, nil
}

func (s *DiscoverySyncService) buildPriceProvider(cfg config.DiscoveryConfig, downloader *discovery.Downloader, calendar discovery.MarketCalendar) (discovery.PriceProvider, error, error) {
	provider := strings.ToLower(strings.TrimSpace(cfg.PriceProvider))
	if provider == "" && strings.TrimSpace(cfg.TiingoAPIToken) != "" {
		provider = "tiingo"
	}
	if provider == "" && len(cfg.TiingoAPITokens) > 0 {
		provider = "tiingo"
	}
	if strings.Contains(provider, ",") {
		parts := strings.Split(provider, ",")
		children := make([]discovery.PriceProvider, 0, len(parts))
		var setupErrs []string
		for _, part := range parts {
			childName := strings.ToLower(strings.TrimSpace(part))
			if childName == "" {
				continue
			}
			childCfg := cfg
			childCfg.PriceProvider = childName
			child, marketErr, err := s.buildSinglePriceProvider(childCfg, downloader, calendar)
			if err != nil {
				log.Printf("price provider chain setup skipped provider=%s reason=%q", childName, err.Error())
				setupErrs = append(setupErrs, err.Error())
				continue
			}
			if marketErr != nil {
				log.Printf("price provider chain setup skipped provider=%s reason=%q", childName, marketErr.Error())
				setupErrs = append(setupErrs, marketErr.Error())
				continue
			}
			children = append(children, child)
		}
		if len(children) == 0 {
			if len(setupErrs) > 0 {
				return nil, nil, fmt.Errorf("price provider chain has no usable provider: %s", strings.Join(setupErrs, "; "))
			}
			return nil, nil, errors.New("price provider chain has no usable provider")
		}
		chain, err := discovery.NewPriceProviderChain(discovery.PriceProviderChainOptions{
			Providers: children,
			Calendar:  calendar,
			Now:       time.Now,
			Diagnostics: func(event discovery.PriceProviderChainDiagnostic) {
				log.Printf(
					"price provider chain: event=%s provider=%s expected=%d records=%d remaining=%d coverage=%.2f elapsed=%s error=%q",
					event.Event,
					event.Provider,
					event.Expected,
					event.Records,
					event.Remaining,
					event.CoveragePct,
					event.Elapsed.Round(time.Second),
					event.Error,
				)
			},
		})
		return chain, nil, err
	}
	return s.buildSinglePriceProvider(cfg, downloader, calendar)
}

func (s *DiscoverySyncService) buildSinglePriceProvider(cfg config.DiscoveryConfig, downloader *discovery.Downloader, calendar discovery.MarketCalendar) (discovery.PriceProvider, error, error) {
	provider := strings.ToLower(strings.TrimSpace(cfg.PriceProvider))
	switch provider {
	case "", "stooq":
		if len(cfg.StooqURLs) == 0 {
			return nil, errors.New("SMALL_CAP_STOOQ_URLS is required for market price sync"), nil
		}
		priceURL := strings.TrimSpace(cfg.StooqURLs[0])
		format := discovery.PriceFormatStooq
		if strings.HasSuffix(strings.ToLower(priceURL), ".zip") {
			format = discovery.PriceFormatStooqZIP
		}
		prices, err := discovery.NewDownloadedPriceProvider(discovery.DownloadedPriceProviderOptions{
			Provider:   "stooq",
			URL:        priceURL,
			CacheKey:   "stooq-us-daily",
			Format:     format,
			Downloader: downloader,
			Validation: discovery.PriceValidationOptions{Now: time.Now().UTC(), Calendar: calendar},
		})
		return prices, nil, err
	case "tiingo":
		if strings.TrimSpace(cfg.TiingoAPIToken) == "" && len(cfg.TiingoAPITokens) == 0 {
			return nil, nil, errors.New("TIINGO_API_TOKEN or TIINGO_API_TOKENS is required when SMALL_CAP_PRICE_PROVIDER=tiingo")
		}
		prices, err := discovery.NewTiingoPriceProvider(discovery.TiingoPriceProviderOptions{
			Token:           cfg.TiingoAPIToken,
			Tokens:          cfg.TiingoAPITokens,
			BaseURL:         cfg.TiingoBaseURL,
			Calendar:        calendar,
			CacheDir:        cfg.CacheDir,
			Now:             time.Now,
			Concurrency:     cfg.TiingoConcurrency,
			RequestBudget:   cfg.TiingoRequestBudget,
			RequestInterval: time.Duration(cfg.TiingoRequestIntervalMS) * time.Millisecond,
			ProgressEvery:   100,
			Progress: func(update discovery.TiingoProgress) {
				reasons := ""
				if len(update.SkipReasons) > 0 {
					parts := make([]string, 0, len(update.SkipReasons))
					for reason, count := range update.SkipReasons {
						parts = append(parts, fmt.Sprintf("%s=%d", reason, count))
					}
					sort.Strings(parts)
					reasons = " reasons=" + strings.Join(parts, ",")
				}
				log.Printf("tiingo price sync progress: processed=%d/%d records=%d skipped=%d elapsed=%s%s", update.Processed, update.Total, update.Records, update.Skipped, update.Elapsed.Round(time.Second), reasons)
			},
		})
		return prices, nil, err
	case "twelvedata":
		if strings.TrimSpace(cfg.TwelveDataAPIKey) == "" {
			return nil, nil, errors.New("TWELVE_DATA_API_KEY is required when SMALL_CAP_PRICE_PROVIDER=twelvedata")
		}
		prices, err := discovery.NewTwelveDataPriceProvider(discovery.TwelveDataPriceProviderOptions{
			APIKey:          cfg.TwelveDataAPIKey,
			BaseURL:         cfg.TwelveDataBaseURL,
			Calendar:        calendar,
			CacheDir:        cfg.CacheDir,
			Now:             time.Now,
			RequestBudget:   cfg.TwelveDataRequestBudget,
			RequestInterval: time.Duration(cfg.TwelveDataRequestIntervalMS) * time.Millisecond,
			ProgressEvery:   25,
			Progress: func(update discovery.TwelveDataProgress) {
				reasons := ""
				if len(update.SkipReasons) > 0 {
					parts := make([]string, 0, len(update.SkipReasons))
					for reason, count := range update.SkipReasons {
						parts = append(parts, fmt.Sprintf("%s=%d", reason, count))
					}
					sort.Strings(parts)
					reasons = " reasons=" + strings.Join(parts, ",")
				}
				log.Printf("twelve data price sync progress: processed=%d/%d records=%d skipped=%d elapsed=%s%s", update.Processed, update.Total, update.Records, update.Skipped, update.Elapsed.Round(time.Second), reasons)
			},
		})
		return prices, nil, err
	case "yahoo":
		prices, err := discovery.NewYahooPriceProvider(discovery.YahooPriceProviderOptions{
			BaseURL:         cfg.YahooBaseURL,
			Calendar:        calendar,
			Now:             time.Now,
			RequestBudget:   cfg.YahooRequestBudget,
			RequestInterval: time.Duration(cfg.YahooRequestIntervalMS) * time.Millisecond,
		})
		return prices, nil, err
	default:
		return nil, nil, fmt.Errorf("unsupported SMALL_CAP_PRICE_PROVIDER %q", cfg.PriceProvider)
	}
}

func (r productionDiscoveryRunner) SyncSecurityUniverse(ctx context.Context) (discovery.UniverseBatch, error) {
	if r.security == nil {
		return discovery.UniverseBatch{}, errors.New("security discovery runner is not configured")
	}
	return r.security.SyncSecurityUniverse(ctx)
}

func (r productionDiscoveryRunner) SyncMarketPrices(ctx context.Context) (discovery.UniverseBatch, error) {
	if r.marketErr != nil {
		return discovery.UniverseBatch{}, r.marketErr
	}
	if r.market == nil {
		return discovery.UniverseBatch{}, errors.New("market discovery runner is not configured")
	}
	return r.market.SyncMarketPrices(ctx)
}
