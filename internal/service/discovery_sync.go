package service

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"sec_monitor/internal/config"
	"sec_monitor/internal/discovery"

	"gorm.io/gorm"
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
	db     *gorm.DB
	cfg    config.DiscoveryConfig
	runner DiscoverySyncRunner
}

type productionDiscoveryRunner struct {
	security  *discovery.Coordinator
	market    *discovery.Coordinator
	marketErr error
}

type DiscoverySyncResult struct {
	Status          string                     `json:"status"`
	BatchID         string                     `json:"batch_id"`
	SecurityBatchID string                     `json:"security_batch_id"`
	MarketBatchID   string                     `json:"market_batch_id"`
	SecurityBatch   discovery.UniverseBatch    `json:"security_batch"`
	MarketBatch     discovery.UniverseBatch    `json:"market_batch"`
	Summary         discovery.CandidateSummary `json:"summary"`
	Health          discovery.CandidateHealth  `json:"health"`
}

func NewDiscoverySyncService(db *gorm.DB, cfg config.DiscoveryConfig) *DiscoverySyncService {
	return &DiscoverySyncService{db: db, cfg: cfg}
}

func (s *DiscoverySyncService) WithRunner(runner DiscoverySyncRunner) *DiscoverySyncService {
	s.runner = runner
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
	result.Summary, err = discovery.BuildCandidateSummary(ctx, s.db, 10)
	if err != nil {
		return result, err
	}
	result.Health, err = discovery.BuildCandidateHealth(ctx, s.db)
	return result, err
}

func (s *DiscoverySyncService) buildRunner() (DiscoverySyncRunner, error) {
	timeout := time.Duration(s.cfg.TaskTimeoutMin) * time.Minute
	if timeout <= 0 {
		timeout = 60 * time.Minute
	}
	downloader := &discovery.Downloader{
		Client:    &http.Client{Timeout: timeout},
		CacheDir:  s.cfg.CacheDir,
		MaxBytes:  8 << 30,
		UserAgent: s.cfg.UserAgent,
	}
	limits := discovery.ZIPParseLimits{MaxEntries: 1_000_000, MaxEntryBytes: 512 << 20, MaxTotalBytes: 64 << 30}
	secBulk := discovery.SECBulkSource{
		Downloader:      downloader,
		TickerURL:       s.cfg.SECTickerExchangeURL,
		SubmissionsURL:  s.cfg.SECSubmissionsURL,
		CompanyFactsURL: s.cfg.SECCompanyFactsURL,
		Limits:          limits,
	}
	nasdaq := discovery.NasdaqDirectorySource{
		Downloader: downloader,
		ListedURL:  s.cfg.NasdaqListedURL,
		OtherURL:   s.cfg.NasdaqOtherListedURL,
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
	prices, marketErr, err := s.buildPriceProvider(downloader, calendar)
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
		Events:   discovery.SECSubmissionsCapitalEventSource{Metadata: secBulk},
		Prices:   prices,
		Calendar: calendar,
		Clock:    time.Now,
	}
	return productionDiscoveryRunner{security: security, market: market}, nil
}

func (s *DiscoverySyncService) buildPriceProvider(downloader *discovery.Downloader, calendar discovery.MarketCalendar) (discovery.PriceProvider, error, error) {
	provider := strings.ToLower(strings.TrimSpace(s.cfg.PriceProvider))
	if provider == "" && strings.TrimSpace(s.cfg.TiingoAPIToken) != "" {
		provider = "tiingo"
	}
	switch provider {
	case "", "stooq":
		if len(s.cfg.StooqURLs) == 0 {
			return nil, errors.New("SMALL_CAP_STOOQ_URLS is required for market price sync"), nil
		}
		priceURL := strings.TrimSpace(s.cfg.StooqURLs[0])
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
		if strings.TrimSpace(s.cfg.TiingoAPIToken) == "" {
			return nil, nil, errors.New("TIINGO_API_TOKEN is required when SMALL_CAP_PRICE_PROVIDER=tiingo")
		}
		prices, err := discovery.NewTiingoPriceProvider(discovery.TiingoPriceProviderOptions{
			Token:    s.cfg.TiingoAPIToken,
			BaseURL:  s.cfg.TiingoBaseURL,
			Calendar: calendar,
			Now:      time.Now,
		})
		return prices, nil, err
	default:
		return nil, nil, fmt.Errorf("unsupported SMALL_CAP_PRICE_PROVIDER %q", s.cfg.PriceProvider)
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
