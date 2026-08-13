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
	"sec_monitor/internal/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	DiscoverySyncStatusPublished    = "published"
	DiscoverySyncStatusMarketFailed = "market_failed"
	DiscoverySyncRunStatusFailed    = "failed"
)

const discoverySyncRecoveryGrace = 15 * time.Minute

var ErrDiscoveryMarketSync = errors.New("discovery market sync failed")

type DiscoverySyncRunner interface {
	SyncSecurityUniverse(context.Context) (discovery.UniverseBatch, error)
	SyncMarketPrices(context.Context) (discovery.UniverseBatch, error)
}

type DiscoverySyncService struct {
	db                   *gorm.DB
	watchDB              *gorm.DB
	cfg                  config.DiscoveryConfig
	configs              *ConfigService
	runner               DiscoverySyncRunner
	analystNotifications *AnalystRatingNotificationService
	inApp                *InAppNotificationService
	batches              *NotificationBatchService
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

// WatchTargetMarketSyncResult describes one local EOD refresh for enabled SEC
// watch targets. It does not read SEC filings or alter small-cap scores.
type WatchTargetMarketSyncResult struct {
	TargetCount         int                      `json:"target_count"`
	RequestedCount      int                      `json:"requested_count"`
	AlreadyCurrentCount int                      `json:"already_current_count"`
	RecordCount         int                      `json:"record_count"`
	PersistedCount      int                      `json:"persisted_count"`
	EffectiveDate       string                   `json:"effective_date"`
	SourceRecordCount   map[string]int           `json:"source_record_count"`
	ProviderResult      discovery.ProviderResult `json:"provider_result"`
	Skipped             bool                     `json:"skipped"`
	Message             string                   `json:"message"`
}

// TickerEvaluationRequest identifies the symbol that an explicit, user-run
// research evaluation should inspect. It never publishes a universe batch.
type TickerEvaluationRequest struct {
	Ticker      string
	CIK         string
	CompanyName string
	TargetType  string
}

type fixedSecurityMetadataSource struct {
	records []discovery.SecuritySourceRecord
}

func (s fixedSecurityMetadataSource) Load(_ context.Context) ([]discovery.SecuritySourceRecord, discovery.SourceVersion, error) {
	return append([]discovery.SecuritySourceRecord(nil), s.records...), discovery.SourceVersion{Source: "ticker-evaluation"}, nil
}

// EvaluateTicker reuses the SEC fundamental and candidate-review primitives
// for one symbol. Price refresh is intentional and happens only on this
// explicit endpoint, never when history is viewed.
func (s *DiscoverySyncService) EvaluateTicker(ctx context.Context, request TickerEvaluationRequest) (discovery.TickerEvaluationResult, error) {
	if s == nil || s.db == nil {
		return discovery.TickerEvaluationResult{}, errors.New("discovery sync service is not configured")
	}
	ticker := strings.ToUpper(strings.TrimSpace(request.Ticker))
	if ticker == "" {
		return discovery.TickerEvaluationResult{}, errors.New("ticker is required")
	}
	if err := ctx.Err(); err != nil {
		return discovery.TickerEvaluationResult{}, err
	}
	if _, err := s.BackfillTickerTechnicalHistory(ctx, ticker, 0); err != nil {
		return discovery.TickerEvaluationResult{}, fmt.Errorf("refresh market history: %w", err)
	}
	prices, err := discovery.TickerEvaluationPriceHistory(ctx, s.db, ticker)
	if err != nil {
		return discovery.TickerEvaluationResult{}, err
	}
	now := time.Now().UTC()
	warnings := []string{}
	targetType := strings.ToLower(strings.TrimSpace(request.TargetType))
	if targetType == "" {
		targetType = "stock"
	}
	cfg, err := s.appliedDiscoveryConfig(ctx)
	if err != nil {
		return discovery.TickerEvaluationResult{}, err
	}
	if targetType == "etf" {
		warnings = append(warnings, "ETF 不适用 SEC 发行人基本面和 Form 4 规则；保留趋势、动量、量价、流动性与交易纪律结果。")
		result := discovery.BuildTickerEvaluationResult(ticker, request.CIK, request.CompanyName, targetType, discovery.CandidateScoreSnapshot{Ticker: ticker}, discovery.FinancialMetricSnapshot{}, nil, prices, now, "not_applicable", warnings)
		s.enrichTickerEvaluationResearch(ctx, cfg, &result, true)
		return discovery.SaveTickerEvaluation(ctx, s.db, result)
	}
	timeout := time.Duration(cfg.TaskTimeoutMin) * time.Minute
	if timeout <= 0 {
		timeout = time.Hour
	}
	downloader := newDiscoveryDownloader(cfg, timeout)
	bulk := discovery.SECBulkSource{Downloader: downloader, TickerURL: cfg.SECTickerExchangeURL, SubmissionsURL: cfg.SECSubmissionsURL, CompanyFactsURL: cfg.SECCompanyFactsURL, Limits: discovery.ZIPParseLimits{MaxEntries: 1_000_000, MaxEntryBytes: 512 << 20, MaxTotalBytes: 64 << 30}, CacheTTL: discoveryCacheTTL(cfg)}
	issuer, err := bulk.LoadIncrementalIssuerData(ctx, []discovery.SecuritySourceRecord{{CIK: strings.TrimSpace(request.CIK), Ticker: ticker, ProviderTicker: ticker, CompanyName: request.CompanyName, MappingStatus: discovery.MappingStatusCurrent}})
	if err != nil {
		return discovery.TickerEvaluationResult{}, fmt.Errorf("load SEC issuer facts: %w", err)
	}
	warnings = append(warnings, issuer.Warnings...)
	if len(issuer.Records) == 0 {
		warnings = append(warnings, "SEC 未返回该发行人的可用元数据。")
	}
	record := discovery.SecuritySourceRecord{CIK: request.CIK, Ticker: ticker, CompanyName: request.CompanyName}
	if len(issuer.Records) > 0 {
		record = issuer.Records[0]
		if record.CompanyName == "" {
			record.CompanyName = request.CompanyName
		}
	}
	metric, metricErr := discovery.FinancialMetricFromFacts("", 0, issuer.FinancialFacts, now)
	fundamentalStatus := "available"
	if metricErr != nil || len(issuer.FinancialFacts) == 0 {
		fundamentalStatus = "unavailable"
		warnings = append(warnings, "未取得足够的 SEC Company Facts，基本面分数不完整。")
	}
	allowed := map[string]struct{}{record.CIK: {}}
	metadata := fixedSecurityMetadataSource{records: []discovery.SecuritySourceRecord{record}}
	events, _, eventErr := (discovery.SECSubmissionsCapitalEventSource{Metadata: metadata}).Load(ctx, allowed, now)
	if eventErr != nil {
		warnings = append(warnings, "资本风险解析不可用："+eventErr.Error())
	}
	risks := make([]discovery.CapitalRiskSnapshot, 0)
	for _, risk := range discovery.AssessCapitalRisks(events, now) {
		risks = append(risks, discovery.CapitalRiskToSnapshot("", 0, risk, now))
	}
	insiderRows := make([]discovery.InsiderTransactionSnapshot, 0)
	var coverage *discovery.InsiderCoverage
	transactions, coverages, _, insiderErr := (discovery.SECForm4InsiderSource{Metadata: metadata, Downloader: downloader, LookbackDays: discovery.CandidateInsiderLookbackDays}).LoadInsiderTransactionsWithCoverage(ctx, allowed, now)
	if insiderErr != nil {
		warnings = append(warnings, "Form 4 解析不可用："+insiderErr.Error())
	} else {
		for _, tx := range transactions {
			insiderRows = append(insiderRows, discovery.InsiderTransactionToSnapshot(0, tx, now))
		}
		if len(coverages) > 0 {
			coverage = &coverages[0]
		}
	}
	shares := discovery.SelectShareSnapshot(issuer.Shares, events, now)
	marketCap := int64(0)
	if len(prices) > 0 && shares.Fact != nil {
		if value, computeErr := discovery.ComputeMarketCapUSD(prices[len(prices)-1].CloseMicros, shares.Fact.Shares); computeErr == nil {
			marketCap = value
		}
	}
	if shares.Fact == nil {
		warnings = append(warnings, "未取得可用流通股本，市值与小盘适配性无法完整判断。")
	}
	scored := discovery.ScoreDiscoveryCandidate(discovery.DiscoveryScoreInput{Ticker: ticker, MarketCapUSD: marketCap, Financial: metric, Insiders: insiderRows, Risks: risks, GrossMarginPct: metric.GrossMarginPct, SectorScore: discovery.SectorRatingForSIC(record.SIC).Score, AsOf: now})
	snapshot := discovery.CandidateScoreToSnapshot("", scored, now)
	result := discovery.BuildTickerEvaluationResult(ticker, record.CIK, record.CompanyName, targetType, snapshot, metric, risks, prices, now, fundamentalStatus, warnings)
	result.InsiderCoverage = coverage
	result.Research.Profile = tickerEvaluationProfileFromRecord(record, result)
	s.enrichTickerEvaluationResearch(ctx, cfg, &result, true)
	return discovery.SaveTickerEvaluation(ctx, s.db, result)
}

// HydrateTickerEvaluationResearch overlays the current locally cached research
// context onto a result without requesting a third-party provider. It is used
// by candidate/watch-target cache responses so looking up an existing symbol
// remains fast and does not consume Longbridge quota.
func (s *DiscoverySyncService) HydrateTickerEvaluationResearch(ctx context.Context, result discovery.TickerEvaluationResult) discovery.TickerEvaluationResult {
	if s == nil || s.db == nil {
		return result
	}
	s.hydrateTickerEvaluationResearch(ctx, &result)
	return result
}

func (s *DiscoverySyncService) enrichTickerEvaluationResearch(ctx context.Context, cfg config.DiscoveryConfig, result *discovery.TickerEvaluationResult, refresh bool) {
	if result == nil {
		return
	}
	if refresh {
		if cfg.LongbridgeCompanyProfileEnabled && cfg.LongbridgeCompanyProfileRequestBudget > 0 && longbridgeCredentialsConfigured(cfg) {
			if overview, err := discovery.FetchLongbridgeCompanyOverview(ctx, cfg, result.Ticker); err != nil {
				result.Research.RefreshNotes = append(result.Research.RefreshNotes, "公司资料未更新："+discovery.SanitizeLongbridgeCandidateResearchError(err))
			} else {
				applyTickerEvaluationCompanyOverview(&result.Research.Profile, overview)
			}
		} else {
			result.Research.RefreshNotes = append(result.Research.RefreshNotes, "公司资料补充已在系统配置中关闭、预算为 0 或 Longbridge 凭据未配置。")
		}
		if cfg.LongbridgeAnalystRatingEnabled && cfg.LongbridgeAnalystRatingRequestBudget > 0 && longbridgeCredentialsConfigured(cfg) {
			if _, err := discovery.RefreshLongbridgeAnalystRating(ctx, s.db, cfg, result.Ticker, result.CIK); err != nil {
				result.Research.RefreshNotes = append(result.Research.RefreshNotes, "分析师共识未更新："+discovery.SanitizeLongbridgeCandidateResearchError(err))
			}
		} else {
			result.Research.RefreshNotes = append(result.Research.RefreshNotes, "分析师共识同步已在系统配置中关闭或预算为 0。")
		}
		if cfg.LongbridgeCandidateResearchEnabled && cfg.LongbridgeCandidateResearchRequestBudget > 0 && longbridgeCredentialsConfigured(cfg) {
			if refreshed, err := discovery.RefreshLongbridgeCandidateMarketResearch(ctx, s.db, cfg, result.Ticker, result.CIK); err != nil {
				result.Research.RefreshNotes = append(result.Research.RefreshNotes, "EPS、异动及持仓未更新："+discovery.SanitizeLongbridgeCandidateResearchError(err))
			} else {
				result.Research.RefreshNotes = append(result.Research.RefreshNotes, refreshed.Warnings...)
			}
		} else {
			result.Research.RefreshNotes = append(result.Research.RefreshNotes, "EPS、异动及持仓同步已在系统配置中关闭或预算为 0。")
		}
		if cfg.LongbridgeCandidateValuationEnabled && cfg.LongbridgeCandidateValuationRequestBudget > 0 && longbridgeCredentialsConfigured(cfg) {
			if refreshed, err := discovery.RefreshLongbridgeCandidateValuationResearch(ctx, s.db, cfg, result.Ticker, result.CIK); err != nil {
				result.Research.RefreshNotes = append(result.Research.RefreshNotes, "估值与同业比较未更新："+discovery.SanitizeLongbridgeCandidateResearchError(err))
			} else {
				result.Research.RefreshNotes = append(result.Research.RefreshNotes, refreshed.Warnings...)
			}
		} else {
			result.Research.RefreshNotes = append(result.Research.RefreshNotes, "估值研究同步已在系统配置中关闭或预算为 0。")
		}
		if cfg.LongbridgeOptionResearchEnabled && cfg.LongbridgeCandidateOptionResearchBudget > 0 && longbridgeCredentialsConfigured(cfg) {
			if refreshed, err := discovery.RefreshLongbridgeOptionResearch(ctx, s.db, cfg, result.Ticker, result.CIK); err != nil {
				result.Research.RefreshNotes = append(result.Research.RefreshNotes, "期权与空头研究未更新："+discovery.SanitizeLongbridgeCandidateResearchError(err))
			} else {
				result.Research.RefreshNotes = append(result.Research.RefreshNotes, refreshed.Warnings...)
			}
		} else {
			result.Research.RefreshNotes = append(result.Research.RefreshNotes, "期权与空头研究同步已在系统配置中关闭或预算为 0。")
		}
	}
	s.hydrateTickerEvaluationResearch(ctx, result)
}

func longbridgeCredentialsConfigured(cfg config.DiscoveryConfig) bool {
	return strings.TrimSpace(cfg.LongbridgeAppKey) != "" && strings.TrimSpace(cfg.LongbridgeAppSecret) != "" && strings.TrimSpace(cfg.LongbridgeAccessToken) != ""
}

func applyTickerEvaluationCompanyOverview(profile *discovery.CompanyProfile, overview discovery.LongbridgeCompanyOverview) {
	if profile == nil {
		return
	}
	if strings.TrimSpace(overview.CompanyName) != "" {
		profile.CompanyName = strings.TrimSpace(overview.CompanyName)
	}
	if strings.TrimSpace(overview.Profile) != "" {
		profile.BusinessSummary = strings.TrimSpace(overview.Profile)
		profile.SummarySource = "Longbridge company overview（本次评估）"
		profile.ProfileProvider = "Longbridge company overview"
		profile.Status = "available"
	}
	profile.Website, profile.Founded, profile.ListingDate, profile.Market = overview.Website, overview.Founded, overview.ListingDate, overview.Market
	profile.Address, profile.Employees, profile.Manager, profile.YearEnd = overview.Address, overview.Employees, overview.Manager, overview.YearEnd
	at := time.Now().UTC()
	profile.ProfileFetchedAt, profile.ProfileFreshness = &at, "fresh"
}

func (s *DiscoverySyncService) hydrateTickerEvaluationResearch(ctx context.Context, result *discovery.TickerEvaluationResult) {
	if result == nil {
		return
	}
	research := result.Research
	if profile, err := discovery.GetCompanyProfile(ctx, s.db, result.Ticker, result.CIK); err == nil {
		research.Profile = mergeTickerEvaluationProfile(research.Profile, profile)
	} else {
		research.RefreshNotes = append(research.RefreshNotes, "公司资料本地快照读取失败。")
	}
	if research.Profile.Ticker == "" {
		research.Profile.Ticker = result.Ticker
	}
	if research.Profile.CIK == "" {
		research.Profile.CIK = result.CIK
	}
	if research.Profile.CompanyName == "" {
		research.Profile.CompanyName = result.CompanyName
	}
	if research.Profile.BusinessSummary == "" {
		if result.TargetType == "etf" {
			research.Profile.BusinessSummary = "该标的是基金 / ETF。发行人基本面和 Form 4 规则不适用；本评估保留行情、趋势、动量、量价与交易纪律。"
			research.Profile.SummarySource = "标的类型规则"
		} else {
			research.Profile.BusinessSummary = "尚未获得可核验的公司业务简介；请检查 Longbridge 公司资料补充或后续 SEC 元数据同步。"
			research.Profile.SummarySource = "待补充"
		}
	}
	if research.Profile.Status == "" {
		research.Profile.Status = "partial"
	}

	if analyst, err := discovery.GetAnalystRating(ctx, s.db, result.Ticker); err == nil {
		research.AnalystRating = analyst
	}
	if market, err := discovery.GetCandidateMarketResearch(ctx, s.db, result.Ticker); err == nil {
		research.MarketResearch = market
	}
	if option, err := discovery.GetOptionResearch(ctx, s.db, result.Ticker); err == nil {
		research.OptionResearch = option
	}
	if valuation, err := discovery.GetCandidateValuationResearch(ctx, s.db, result.Ticker); err == nil {
		research.ValuationResearch = valuation
	}
	research.Sources = tickerEvaluationResearchSources(research, result.TargetType)
	if result.TargetType == "etf" {
		research.HoldingsScopeNote = "当前持仓表展示的是持有该 ETF 的机构及基金 / ETF 披露，不是该 ETF 自身的成分股与组合权重；ETF 成分披露需接入基金持仓原始申报后单独展示。"
	} else {
		research.HoldingsScopeNote = "机构股东与基金 / ETF 持仓均为 Longbridge 返回的报告日快照；持股比例和组合权重按提供方口径展示，不参与基本面总分。"
	}
	result.Research = research
}

func tickerEvaluationProfileFromRecord(record discovery.SecuritySourceRecord, result discovery.TickerEvaluationResult) discovery.CompanyProfile {
	profile := discovery.CompanyProfile{Ticker: result.Ticker, CIK: result.CIK, CompanyName: result.CompanyName, Exchange: record.Exchange, SIC: record.SIC, SICDescription: record.SICDescription, StateOfIncorporation: record.StateOfIncorporation, LatestAnnualForm: record.LatestAnnualForm, Status: "partial"}
	if profile.SICDescription != "" {
		profile.BusinessSummary = fmt.Sprintf("SEC 将该发行人归入“%s”（SIC %d）。这是申报资料中的行业分类，并非完整产品介绍。", profile.SICDescription, profile.SIC)
		profile.SummarySource = "SEC submissions / sicDescription"
		profile.Status = "available"
	}
	return profile
}

func mergeTickerEvaluationProfile(base, local discovery.CompanyProfile) discovery.CompanyProfile {
	if local.Ticker != "" {
		base.Ticker = local.Ticker
	}
	if local.CIK != "" {
		base.CIK = local.CIK
	}
	if local.CompanyName != "" {
		base.CompanyName = local.CompanyName
	}
	if local.Exchange != "" {
		base.Exchange = local.Exchange
	}
	if local.SIC != 0 {
		base.SIC = local.SIC
	}
	if local.SICDescription != "" {
		base.SICDescription = local.SICDescription
	}
	if local.SectorCategory != "" {
		base.SectorCategory = local.SectorCategory
	}
	if local.StateOfIncorporation != "" {
		base.StateOfIncorporation = local.StateOfIncorporation
	}
	if local.LatestAnnualForm != "" {
		base.LatestAnnualForm = local.LatestAnnualForm
	}
	if local.BusinessSummary != "" && !strings.Contains(base.SummarySource, "本次评估") {
		base.BusinessSummary, base.SummarySource, base.Status = local.BusinessSummary, local.SummarySource, local.Status
	}
	if local.ProfileProvider != "" {
		base.ProfileProvider, base.ProfileFetchedAt, base.ProfileFreshness = local.ProfileProvider, local.ProfileFetchedAt, local.ProfileFreshness
		base.Website, base.Founded, base.ListingDate, base.Market = local.Website, local.Founded, local.ListingDate, local.Market
		base.Address, base.Employees, base.Manager, base.YearEnd = local.Address, local.Employees, local.Manager, local.YearEnd
	}
	if local.MetadataAsOf != nil {
		base.MetadataAsOf = local.MetadataAsOf
	}
	return base
}

func tickerEvaluationResearchSources(research discovery.TickerEvaluationResearchSnapshot, targetType string) []discovery.TickerEvaluationResearchSource {
	sources := []discovery.TickerEvaluationResearchSource{{Name: "公司资料 / 行业", Status: research.Profile.Status, Note: research.Profile.SummarySource}}
	if research.Profile.ProfileFetchedAt != nil {
		sources[0].AsOf = research.Profile.ProfileFetchedAt.Format(time.RFC3339)
	}
	analyst := discovery.TickerEvaluationResearchSource{Name: "分析师共识", Status: "unavailable", Note: research.AnalystRating.Message}
	if research.AnalystRating.Latest != nil {
		analyst.Status, analyst.AsOf = research.AnalystRating.Latest.Status, research.AnalystRating.Latest.FetchedAt.Format(time.RFC3339)
	}
	eps := discovery.TickerEvaluationResearchSource{Name: "EPS / 异动 / 持仓", Status: "unavailable", Note: research.MarketResearch.EPSForecast.Message}
	if research.MarketResearch.EPSForecast.Latest != nil {
		eps.Status, eps.AsOf = "available", research.MarketResearch.EPSForecast.Latest.FetchedAt.Format(time.RFC3339)
	} else if len(research.MarketResearch.InstitutionalHolders)+len(research.MarketResearch.FundHolders) > 0 {
		eps.Status = "partial"
	}
	valuation := discovery.TickerEvaluationResearchSource{Name: "估值 / 同业比较", Status: "unavailable", Note: research.ValuationResearch.Message}
	if research.ValuationResearch.Latest != nil {
		valuation.Status, valuation.AsOf = "available", research.ValuationResearch.Latest.FetchedAt.Format(time.RFC3339)
	}
	sources = append(sources, analyst, eps, valuation)
	options := discovery.TickerEvaluationResearchSource{Name: "期权 / 空头研究", Status: "unavailable", Note: research.OptionResearch.Message}
	if research.OptionResearch.Latest != nil {
		options.Status, options.AsOf = research.OptionResearch.Latest.Status, research.OptionResearch.Latest.FetchedAt.Format(time.RFC3339)
	}
	sources = append(sources, options)
	if targetType == "etf" {
		sources = append(sources, discovery.TickerEvaluationResearchSource{Name: "ETF 自身成分权重", Status: "not_synced", Note: "尚未接入基金持仓原始申报；当前持仓仅表示谁持有该 ETF。"})
	}
	return sources
}

// RefreshLongbridgeCompanyProfile exposes an explicitly user-triggered issuer
// refresh. Detail pages read the resulting cache and never call Longbridge on
// their own.
func (s *DiscoverySyncService) RefreshLongbridgeCompanyProfile(ctx context.Context, ticker, cik string, force bool) (discovery.CompanyProfileRefreshResult, error) {
	if s == nil || s.db == nil {
		return discovery.CompanyProfileRefreshResult{}, errors.New("discovery sync service is not configured")
	}
	cfg, err := s.appliedDiscoveryConfig(ctx)
	if err != nil {
		return discovery.CompanyProfileRefreshResult{}, err
	}
	return discovery.RefreshLongbridgeCompanyProfile(ctx, s.db, cfg, ticker, cik, force)
}

// RefreshLongbridgeAnalystRating refreshes one issuer's rating aggregate. It
// is intentionally isolated from all SEC and price sync workflows.
func (s *DiscoverySyncService) RefreshLongbridgeAnalystRating(ctx context.Context, ticker, cik string) (discovery.AnalystRatingRefreshResult, error) {
	if s == nil || s.db == nil {
		return discovery.AnalystRatingRefreshResult{}, errors.New("discovery sync service is not configured")
	}
	cfg, err := s.appliedDiscoveryConfig(ctx)
	if err != nil {
		return discovery.AnalystRatingRefreshResult{}, err
	}
	result, err := discovery.RefreshLongbridgeAnalystRating(ctx, s.db, cfg, ticker, cik)
	if err != nil {
		return result, err
	}
	// A detail-page refresh still uses the same persisted notification path as
	// the scheduled job. It never invokes SEC or a broad market refresh.
	if result.Changed && s.analystNotifications != nil {
		if notifyErr := s.analystNotifications.DeliverPending(ctx, []discovery.AnalystRatingSnapshot{result.Snapshot}); notifyErr != nil {
			log.Printf("analyst rating notification deferred: %v", notifyErr)
		}
	}
	return result, nil
}

// RefreshLongbridgeCandidateMarketResearch updates the selected candidate's
// EPS expectation, anomaly and institutional-holder snapshots only.
func (s *DiscoverySyncService) RefreshLongbridgeCandidateMarketResearch(ctx context.Context, ticker, cik string) (discovery.CandidateMarketResearchRefreshResult, error) {
	if s == nil || s.db == nil {
		return discovery.CandidateMarketResearchRefreshResult{}, errors.New("discovery sync service is not configured")
	}
	cfg, err := s.appliedDiscoveryConfig(ctx)
	if err != nil {
		return discovery.CandidateMarketResearchRefreshResult{}, err
	}
	result, err := discovery.RefreshLongbridgeCandidateMarketResearch(ctx, s.db, cfg, ticker, cik)
	if err == nil {
		err = discovery.MarkLongbridgeResearchSuccess(ctx, s.db, discovery.LongbridgeRefreshFamilyMarketResearch, ticker, time.Now().UTC())
	}
	return result, err
}

func (s *DiscoverySyncService) RefreshLongbridgeCandidateValuationResearch(ctx context.Context, ticker, cik string) (discovery.ValuationResearchRefreshResult, error) {
	if s == nil || s.db == nil {
		return discovery.ValuationResearchRefreshResult{}, errors.New("discovery sync service is not configured")
	}
	cfg, err := s.appliedDiscoveryConfig(ctx)
	if err != nil {
		return discovery.ValuationResearchRefreshResult{}, err
	}
	result, err := discovery.RefreshLongbridgeCandidateValuationResearch(ctx, s.db, cfg, ticker, cik)
	if err == nil {
		err = discovery.MarkLongbridgeResearchSuccess(ctx, s.db, discovery.LongbridgeRefreshFamilyValuation, ticker, time.Now().UTC())
	}
	return result, err
}

// RefreshLongbridgeOptionResearch updates one explicit symbol's compact
// Call/Put volume and short-interest snapshot. It is separate from P1/P2 and
// never changes the candidate's fundamental or review score.
func (s *DiscoverySyncService) RefreshLongbridgeOptionResearch(ctx context.Context, ticker, cik string) (discovery.OptionResearchRefreshResult, error) {
	if s == nil || s.db == nil {
		return discovery.OptionResearchRefreshResult{}, errors.New("discovery sync service is not configured")
	}
	cfg, err := s.appliedDiscoveryConfig(ctx)
	if err != nil {
		return discovery.OptionResearchRefreshResult{}, err
	}
	if !cfg.LongbridgeOptionResearchEnabled {
		return discovery.OptionResearchRefreshResult{}, errors.New("Longbridge 期权与空头研究同步已关闭")
	}
	return discovery.RefreshLongbridgeOptionResearch(ctx, s.db, cfg, ticker, cik)
}

func (s *DiscoverySyncService) GetOptionResearch(ctx context.Context, ticker string) (discovery.OptionResearchView, error) {
	if s == nil || s.db == nil {
		return discovery.OptionResearchView{}, errors.New("discovery sync service is not configured")
	}
	return discovery.GetOptionResearch(ctx, s.db, ticker)
}

func (s *DiscoverySyncService) SyncLongbridgeCandidateOptionResearch(ctx context.Context) (discovery.OptionResearchSyncResult, error) {
	if s == nil || s.db == nil {
		return discovery.OptionResearchSyncResult{}, errors.New("discovery sync service is not configured")
	}
	cfg, err := s.appliedDiscoveryConfig(ctx)
	if err != nil {
		return discovery.OptionResearchSyncResult{}, err
	}
	return discovery.SyncCurrentCandidateOptionResearch(ctx, s.db, cfg)
}

// SyncEnabledWatchTargetOptionResearch has its own budget so an expanding
// candidate universe cannot starve the monitoring list.
func (s *DiscoverySyncService) SyncEnabledWatchTargetOptionResearch(ctx context.Context) (discovery.OptionResearchSyncResult, error) {
	result := discovery.OptionResearchSyncResult{Warnings: []string{}}
	if s == nil || s.db == nil || s.watchDB == nil {
		return result, errors.New("watch-target option research is not configured")
	}
	cfg, err := s.appliedDiscoveryConfig(ctx)
	if err != nil {
		return result, err
	}
	if !cfg.LongbridgeOptionResearchEnabled || cfg.LongbridgeWatchTargetOptionResearchBudget <= 0 {
		result.Skipped, result.Message = true, "Longbridge 监控标的期权与空头研究同步已关闭或预算为 0"
		return result, nil
	}
	var targets []model.WatchTarget
	// WatchTarget uses the string status column (enabled/disabled), not an
	// historical boolean enabled column. Keeping this predicate consistent
	// with the other watch-target research jobs prevents a persisted SQLite
	// database from failing this scheduled task with "no such column: enabled".
	if err := s.watchDB.WithContext(ctx).Where("status = ? AND target_type = ?", "enabled", "stock").Order("ticker ASC").Limit(cfg.LongbridgeWatchTargetOptionResearchBudget).Find(&targets).Error; err != nil {
		return result, err
	}
	if len(targets) == 0 {
		result.Skipped, result.Message = true, "暂无已启用监控标的"
		return result, nil
	}
	result.CandidateCount = len(targets)
	batchTargets := make([]discovery.OptionResearchTarget, 0, len(targets))
	for _, target := range targets {
		batchTargets = append(batchTargets, discovery.OptionResearchTarget{Ticker: target.Ticker, CIK: target.CIK})
	}
	items, err := discovery.RefreshLongbridgeOptionResearchBatch(ctx, s.db, cfg, batchTargets)
	if err != nil {
		return result, err
	}
	for index, item := range items {
		result.Attempted++
		if item.Err != nil {
			result.Failed++
			result.Warnings = append(result.Warnings, batchTargets[index].Ticker+"："+discovery.SanitizeLongbridgeCandidateResearchError(item.Err))
			continue
		}
		if item.Result.Fetched {
			result.Fetched++
		}
		result.Warnings = append(result.Warnings, item.Result.Warnings...)
	}
	result.Message = fmt.Sprintf("已更新 %d/%d 个监控标的的期权与空头研究快照", result.Fetched, result.Attempted)
	return result, nil
}

// SyncLongbridgeCandidateMarketResearch runs the bounded P1 data family
// (EPS expectations, market anomalies, and institutional holdings). It is
// intentionally scheduled separately from candidate discovery and P2.
func (s *DiscoverySyncService) SyncLongbridgeCandidateMarketResearch(ctx context.Context) (discovery.CandidateMarketResearchSyncResult, error) {
	if s == nil || s.db == nil {
		return discovery.CandidateMarketResearchSyncResult{}, errors.New("discovery sync service is not configured")
	}
	cfg, err := s.appliedDiscoveryConfig(ctx)
	if err != nil {
		return discovery.CandidateMarketResearchSyncResult{}, err
	}
	return discovery.SyncCurrentCandidateLongbridgeMarketResearch(ctx, s.db, cfg)
}

// WatchTargetMarketResearchSyncResult describes the independent P1 refresh for
// enabled stock watch targets. It is deliberately separate from candidate P1
// because shareholder and fund-holder coverage should not depend on the size
// or cadence of the candidate universe.
type WatchTargetMarketResearchSyncResult struct {
	TargetCount int      `json:"target_count"`
	Attempted   int      `json:"attempted"`
	Fetched     int      `json:"fetched"`
	Failed      int      `json:"failed"`
	Skipped     bool     `json:"skipped"`
	Message     string   `json:"message"`
	Warnings    []string `json:"warnings"`
}

// SyncEnabledWatchTargetMarketResearch refreshes a bounded least-recently
// updated subset of enabled stock watch targets. It only calls Longbridge P1
// endpoints (EPS, anomaly, shareholder, fund-holder), and never starts SEC,
// price, candidate, or P2 valuation work.
func (s *DiscoverySyncService) SyncEnabledWatchTargetMarketResearch(ctx context.Context) (WatchTargetMarketResearchSyncResult, error) {
	result := WatchTargetMarketResearchSyncResult{Warnings: []string{}}
	if s == nil || s.db == nil || s.watchDB == nil {
		return result, errors.New("watch target market research sync service is not configured")
	}
	cfg, err := s.appliedDiscoveryConfig(ctx)
	if err != nil {
		return result, err
	}
	if !cfg.LongbridgeWatchTargetResearchEnabled || cfg.LongbridgeWatchTargetResearchRequestBudget <= 0 {
		result.Skipped, result.Message = true, "监控标的 Longbridge 市场研究同步已关闭或预算为 0"
		return result, nil
	}
	var targets []model.WatchTarget
	if err := s.watchDB.WithContext(ctx).Where("status = ? AND target_type = ?", "enabled", "stock").Find(&targets).Error; err != nil {
		return result, err
	}
	result.TargetCount = len(targets)
	if len(targets) == 0 {
		result.Skipped, result.Message = true, "暂无已启用的股票监控标的"
		return result, nil
	}
	latestByTicker := make(map[string]time.Time, len(targets))
	fresh, freshErr := discovery.FreshLongbridgeResearchTickers(ctx, s.db, discovery.LongbridgeRefreshFamilyMarketResearch, time.Now().UTC())
	if freshErr != nil {
		return result, freshErr
	}
	for _, target := range targets {
		cached, cacheErr := discovery.GetCandidateMarketResearch(ctx, s.db, target.Ticker)
		if cacheErr == nil && cached.EPSForecast.Latest != nil {
			latestByTicker[strings.ToUpper(strings.TrimSpace(target.Ticker))] = cached.EPSForecast.Latest.FetchedAt
		}
	}
	sort.SliceStable(targets, func(left, right int) bool {
		leftAt, leftOK := latestByTicker[strings.ToUpper(strings.TrimSpace(targets[left].Ticker))]
		rightAt, rightOK := latestByTicker[strings.ToUpper(strings.TrimSpace(targets[right].Ticker))]
		if leftOK != rightOK {
			return !leftOK
		}
		if !leftAt.Equal(rightAt) {
			return leftAt.Before(rightAt)
		}
		return targets[left].Ticker < targets[right].Ticker
	})
	for _, target := range targets {
		if result.Attempted >= cfg.LongbridgeWatchTargetResearchRequestBudget {
			break
		}
		if fresh[strings.ToUpper(strings.TrimSpace(target.Ticker))] {
			continue
		}
		result.Attempted++
		refreshed, refreshErr := discovery.RefreshLongbridgeCandidateMarketResearch(ctx, s.db, cfg, target.Ticker, target.CIK)
		if refreshErr != nil {
			result.Failed++
			result.Warnings = append(result.Warnings, target.Ticker+"："+discovery.SanitizeLongbridgeCandidateResearchError(refreshErr))
			continue
		}
		if markErr := discovery.MarkLongbridgeResearchSuccess(ctx, s.db, discovery.LongbridgeRefreshFamilyMarketResearch, target.Ticker, time.Now().UTC()); markErr != nil {
			return result, markErr
		}
		if refreshed.EPSFetched || refreshed.AnomaliesSaved > 0 || refreshed.ShareholdersSaved > 0 || refreshed.FundHoldersSaved > 0 || len(refreshed.Warnings) == 0 {
			result.Fetched++
		}
		result.Warnings = append(result.Warnings, refreshed.Warnings...)
	}
	result.Message = fmt.Sprintf("已同步 %d/%d 个监控标的的 Longbridge 市场研究，失败 %d 个", result.Fetched, result.Attempted, result.Failed)
	return result, nil
}

// SyncLongbridgeCandidateValuationResearch runs the independently budgeted P2
// valuation, industry percentile, and peer-comparison refresh.
func (s *DiscoverySyncService) SyncLongbridgeCandidateValuationResearch(ctx context.Context) (discovery.CandidateValuationResearchSyncResult, error) {
	if s == nil || s.db == nil {
		return discovery.CandidateValuationResearchSyncResult{}, errors.New("discovery sync service is not configured")
	}
	cfg, err := s.appliedDiscoveryConfig(ctx)
	if err != nil {
		return discovery.CandidateValuationResearchSyncResult{}, err
	}
	return discovery.SyncCurrentCandidateLongbridgeValuationResearch(ctx, s.db, cfg)
}

// WatchTargetValuationResearchSyncResult describes the independent P2 refresh
// for enabled stock watch targets. It has a separate budget from candidates so
// a large candidate universe cannot starve a user's monitored securities.
type WatchTargetValuationResearchSyncResult struct {
	TargetCount int      `json:"target_count"`
	Attempted   int      `json:"attempted"`
	Fetched     int      `json:"fetched"`
	Failed      int      `json:"failed"`
	Skipped     bool     `json:"skipped"`
	Message     string   `json:"message"`
	Warnings    []string `json:"warnings"`
}

// SyncEnabledWatchTargetValuationResearch refreshes a bounded, least-recently
// refreshed subset of enabled stock watch targets. It only uses P2 endpoints;
// it does not run SEC, filing, price, or candidate synchronization.
func (s *DiscoverySyncService) SyncEnabledWatchTargetValuationResearch(ctx context.Context) (WatchTargetValuationResearchSyncResult, error) {
	result := WatchTargetValuationResearchSyncResult{Warnings: []string{}}
	if s == nil || s.db == nil || s.watchDB == nil {
		return result, errors.New("watch target valuation sync service is not configured")
	}
	cfg, err := s.appliedDiscoveryConfig(ctx)
	if err != nil {
		return result, err
	}
	if !cfg.LongbridgeWatchTargetValuationEnabled || cfg.LongbridgeWatchTargetValuationRequestBudget <= 0 {
		result.Skipped, result.Message = true, "监控标的 Longbridge 估值研究同步已关闭或预算为 0"
		return result, nil
	}
	var targets []model.WatchTarget
	if err := s.watchDB.WithContext(ctx).Where("status = ? AND target_type = ?", "enabled", "stock").Find(&targets).Error; err != nil {
		return result, err
	}
	result.TargetCount = len(targets)
	if len(targets) == 0 {
		result.Skipped, result.Message = true, "暂无已启用的股票监控标的"
		return result, nil
	}
	latestByTicker := make(map[string]time.Time, len(targets))
	fresh, freshErr := discovery.FreshLongbridgeResearchTickers(ctx, s.db, discovery.LongbridgeRefreshFamilyValuation, time.Now().UTC())
	if freshErr != nil {
		return result, freshErr
	}
	for _, target := range targets {
		cached, cacheErr := discovery.GetCandidateValuationResearch(ctx, s.db, target.Ticker)
		if cacheErr == nil && cached.Latest != nil {
			latestByTicker[strings.ToUpper(strings.TrimSpace(target.Ticker))] = cached.Latest.FetchedAt
		}
	}
	sort.SliceStable(targets, func(left, right int) bool {
		leftAt, leftOK := latestByTicker[strings.ToUpper(strings.TrimSpace(targets[left].Ticker))]
		rightAt, rightOK := latestByTicker[strings.ToUpper(strings.TrimSpace(targets[right].Ticker))]
		if leftOK != rightOK {
			return !leftOK
		}
		if !leftAt.Equal(rightAt) {
			return leftAt.Before(rightAt)
		}
		return targets[left].Ticker < targets[right].Ticker
	})
	for _, target := range targets {
		if result.Attempted >= cfg.LongbridgeWatchTargetValuationRequestBudget {
			break
		}
		if fresh[strings.ToUpper(strings.TrimSpace(target.Ticker))] {
			continue
		}
		result.Attempted++
		refreshed, refreshErr := discovery.RefreshLongbridgeCandidateValuationResearch(ctx, s.db, cfg, target.Ticker, target.CIK)
		if refreshErr != nil {
			result.Failed++
			result.Warnings = append(result.Warnings, target.Ticker+"："+discovery.SanitizeLongbridgeCandidateResearchError(refreshErr))
			continue
		}
		if markErr := discovery.MarkLongbridgeResearchSuccess(ctx, s.db, discovery.LongbridgeRefreshFamilyValuation, target.Ticker, time.Now().UTC()); markErr != nil {
			return result, markErr
		}
		if refreshed.Fetched || refreshed.Cached {
			result.Fetched++
		}
		result.Warnings = append(result.Warnings, refreshed.Warnings...)
	}
	result.Message = fmt.Sprintf("已同步 %d/%d 个监控标的的 Longbridge 估值研究，失败 %d 个", result.Fetched, result.Attempted, result.Failed)
	return result, nil
}

// RetryCurrentLongbridgeCompanyProfiles performs a bounded, operator-triggered
// retry of the locally persisted company-profile recovery queue.
func (s *DiscoverySyncService) RetryCurrentLongbridgeCompanyProfiles(ctx context.Context) (discovery.CompanyProfileBulkRetryResult, error) {
	if s == nil || s.db == nil {
		return discovery.CompanyProfileBulkRetryResult{}, errors.New("discovery sync service is not configured")
	}
	cfg, err := s.appliedDiscoveryConfig(ctx)
	if err != nil {
		return discovery.CompanyProfileBulkRetryResult{}, err
	}
	return discovery.RetryCurrentCandidateLongbridgeCompanyProfiles(ctx, s.db, cfg)
}

// ProbeLongbridgeQuote checks one authenticated quote WebSocket request using
// the same persisted configuration as the candidate market sync. It does not
// alter price snapshots or consume any fallback-provider budget.
func (s *DiscoverySyncService) ProbeLongbridgeQuote(ctx context.Context) discovery.LongbridgeQuoteProbeResult {
	if s == nil {
		return discovery.LongbridgeQuoteProbeResult{Provider: "longbridge", Status: "failed", ErrorKind: "configuration", Message: "discovery sync service is not configured"}
	}
	cfg, err := s.appliedDiscoveryConfig(ctx)
	if err != nil {
		return discovery.LongbridgeQuoteProbeResult{Provider: "longbridge", Status: "failed", ErrorKind: "configuration", Message: SanitizeSensitiveError(err.Error())}
	}
	return discovery.ProbeLongbridgeQuote(ctx, cfg.LongbridgeAppKey, cfg.LongbridgeAppSecret, cfg.LongbridgeAccessToken)
}

func (s *DiscoverySyncService) autoRefreshLongbridgeCompanyProfiles(ctx context.Context) discovery.CompanyProfileSyncResult {
	if s == nil || s.db == nil {
		return discovery.CompanyProfileSyncResult{Skipped: true, Message: "公司资料同步服务未配置"}
	}
	cfg, err := s.appliedDiscoveryConfig(ctx)
	if err != nil {
		return discovery.CompanyProfileSyncResult{Message: err.Error(), Failed: 1}
	}
	result, err := discovery.SyncCurrentCandidateLongbridgeCompanyProfiles(ctx, s.db, cfg)
	if err != nil {
		result.Failed++
		result.Message = err.Error()
	}
	return result
}

func (s *DiscoverySyncService) autoRefreshLongbridgeAnalystRatings(ctx context.Context) discovery.AnalystRatingSyncResult {
	if s == nil || s.db == nil {
		return discovery.AnalystRatingSyncResult{Skipped: true, Message: "分析师评级同步服务未配置", Changes: []discovery.AnalystRatingSnapshot{}}
	}
	cfg, err := s.appliedDiscoveryConfig(ctx)
	if err != nil {
		return discovery.AnalystRatingSyncResult{Failed: 1, Message: err.Error(), Changes: []discovery.AnalystRatingSnapshot{}}
	}
	// Reserve a small, bounded portion of the daily quota for enabled stock
	// watch targets. Candidates and watch targets are both first-class project
	// subjects; without this reservation a large candidate universe could starve
	// the watchlist indefinitely.
	var targets []model.WatchTarget
	reservedForTargets := 0
	var targetLoadErr error
	if s.watchDB != nil && cfg.LongbridgeAnalystRatingRequestBudget >= 2 {
		targetLoadErr = s.watchDB.WithContext(ctx).
			Where("status = ? AND target_type = ?", "enabled", "stock").
			Find(&targets).Error
		if targetLoadErr == nil && len(targets) > 0 {
			reservedForTargets = cfg.LongbridgeAnalystRatingRequestBudget / 4
			if reservedForTargets < 1 {
				reservedForTargets = 1
			}
			if reservedForTargets > len(targets) {
				reservedForTargets = len(targets)
			}
		}
	}
	candidateCfg := cfg
	candidateCfg.LongbridgeAnalystRatingRequestBudget -= reservedForTargets
	result, err := discovery.SyncCurrentCandidateLongbridgeAnalystRatings(ctx, s.db, candidateCfg)
	if err != nil {
		result.Failed++
		result.Message = err.Error()
		return result
	}
	if targetLoadErr != nil {
		result.Failed++
		result.Message = strings.TrimSpace(result.Message + "; load watch targets: " + targetLoadErr.Error())
	}
	// Enabled stock watch targets share the same daily provider budget as the
	// candidate universe. This keeps monitoring useful without letting a large
	// watchlist silently consume an unbounded number of market-data requests.
	if !result.Skipped && targetLoadErr == nil && len(targets) > 0 && result.Attempted < cfg.LongbridgeAnalystRatingRequestBudget {
		result.WatchTargetCount = len(targets)
		latestByTicker := make(map[string]time.Time, len(targets))
		for _, target := range targets {
			cached, cacheErr := discovery.GetAnalystRating(ctx, s.db, target.Ticker)
			if cacheErr == nil && cached.Latest != nil {
				latestByTicker[strings.ToUpper(strings.TrimSpace(target.Ticker))] = cached.Latest.FetchedAt
			}
		}
		sort.SliceStable(targets, func(left, right int) bool {
			leftAt, leftOK := latestByTicker[strings.ToUpper(strings.TrimSpace(targets[left].Ticker))]
			rightAt, rightOK := latestByTicker[strings.ToUpper(strings.TrimSpace(targets[right].Ticker))]
			if leftOK != rightOK {
				return !leftOK
			}
			if !leftAt.Equal(rightAt) {
				return leftAt.Before(rightAt)
			}
			return targets[left].Ticker < targets[right].Ticker
		})
		for _, target := range targets {
			if result.Attempted >= cfg.LongbridgeAnalystRatingRequestBudget {
				break
			}
			if fetchedAt, ok := latestByTicker[strings.ToUpper(strings.TrimSpace(target.Ticker))]; ok && fetchedAt.UTC().Format("2006-01-02") == time.Now().UTC().Format("2006-01-02") {
				continue
			}
			result.Attempted++
			refreshed, refreshErr := discovery.RefreshLongbridgeAnalystRating(ctx, s.db, cfg, target.Ticker, target.CIK)
			if refreshErr != nil {
				result.Failed++
				continue
			}
			if refreshed.Cached {
				result.Cached++
			} else if refreshed.Fetched {
				result.Fetched++
			}
			if refreshed.Changed {
				result.Changed++
				result.Changes = append(result.Changes, refreshed.Snapshot)
			}
		}
	}
	if s.analystNotifications != nil {
		if notifyErr := s.analystNotifications.DeliverQueued(ctx, 100); notifyErr != nil {
			log.Printf("analyst rating notification deferred: %v", notifyErr)
		}
	}
	return result
}

func (s *DiscoverySyncService) autoRefreshLongbridgeCandidateMarketResearch(ctx context.Context) discovery.CandidateMarketResearchSyncResult {
	if s == nil || s.db == nil {
		return discovery.CandidateMarketResearchSyncResult{Skipped: true, Message: "P1 候选研究同步服务未配置"}
	}
	cfg, err := s.appliedDiscoveryConfig(ctx)
	if err != nil {
		return discovery.CandidateMarketResearchSyncResult{Failed: 1, Message: err.Error(), Warnings: []string{}}
	}
	result, err := discovery.SyncCurrentCandidateLongbridgeMarketResearch(ctx, s.db, cfg)
	if err != nil {
		result.Failed++
		result.Message = err.Error()
	}
	return result
}

type DiscoveryFinancialRefreshResult struct {
	Events       int `json:"events"`
	Companies    int `json:"companies"`
	Facts        int `json:"facts"`
	Recalculated int `json:"recalculated"`
}

// IncrementalListingDiscoveryResult records the lightweight daily issuer
// discovery pass.  It is intentionally separate from full calibration: only
// newly observed CIK/ticker identities trigger per-issuer SEC requests.
type IncrementalListingDiscoveryResult struct {
	Discovery discovery.IncrementalListingResult
	Warnings  []string
}

// TechnicalHistoryWarmupResult records the non-blocking daily technical
// history pre-warm. Market candidates remain publishable if the optional
// enrichment source is temporarily rate-limited.
type TechnicalHistoryWarmupResult struct {
	Status       string                                   `json:"status"`
	Result       discovery.TechnicalHistoryBackfillResult `json:"result"`
	ErrorMessage string                                   `json:"error_message,omitempty"`
}

// CandidateMarketHistoryRefreshResult records the two explicit stages of a
// one-symbol market repair: local EOD history first, then an immutable market
// correction batch that recalculates the symbol's market cap and score.
type CandidateMarketHistoryRefreshResult struct {
	History discovery.TechnicalHistoryBackfillResult `json:"history"`
	Reprice discovery.MarketPriceRepriceResult       `json:"reprice"`
}

func NewDiscoverySyncService(db *gorm.DB, cfg config.DiscoveryConfig) *DiscoverySyncService {
	return &DiscoverySyncService{db: db, cfg: cfg}
}

// WithWatchTargetDB provides the core monitoring database to the discovery
// service. Price history remains in the discovery database while watch-target
// membership remains owned by the SEC-monitor database.
func (s *DiscoverySyncService) WithWatchTargetDB(db *gorm.DB) *DiscoverySyncService {
	s.watchDB = db
	return s
}

func (s *DiscoverySyncService) WithRunner(runner DiscoverySyncRunner) *DiscoverySyncService {
	s.runner = runner
	return s
}

// RecoverInterruptedRuns closes persisted workflows whose heartbeat stopped
// before this process started. Without this recovery a Docker restart leaves
// the UI reporting a permanent “running” workflow even though the in-memory
// worker no longer exists.
func (s *DiscoverySyncService) RecoverInterruptedRuns(ctx context.Context) (int64, error) {
	if s == nil || s.db == nil {
		return 0, nil
	}
	// Router/unit-test bootstraps may intentionally use a minimal discovery
	// schema. Normal application startup migrates this table before recovery.
	if !s.db.Migrator().HasTable(&discovery.DiscoverySyncRun{}) {
		return 0, nil
	}
	timeout := time.Hour
	if s.configs != nil {
		if cfg, err := s.appliedDiscoveryConfig(ctx); err == nil && cfg.TaskTimeoutMin > 0 {
			timeout = time.Duration(cfg.TaskTimeoutMin) * time.Minute
		}
	}
	now := time.Now().UTC()
	cutoff := now.Add(-(timeout + discoverySyncRecoveryGrace))
	message := "同步进程已中断或超过心跳恢复窗口；请重新运行任务"
	var recovered int64
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var staleRuns []discovery.DiscoverySyncRun
		if err := tx.Where("status = ? AND updated_at < ?", "running", cutoff).Find(&staleRuns).Error; err != nil {
			return err
		}
		if len(staleRuns) == 0 {
			return nil
		}
		runIDs := make([]uint, 0, len(staleRuns))
		for _, run := range staleRuns {
			runIDs = append(runIDs, run.ID)
		}
		result := tx.Model(&discovery.DiscoverySyncRun{}).
			Where("id IN ?", runIDs).
			Updates(map[string]any{"status": DiscoverySyncRunStatusFailed, "phase": "failed", "completed_at": now, "updated_at": now, "error_message": message})
		if result.Error != nil {
			return result.Error
		}
		recovered = result.RowsAffected
		if recovered == 0 {
			return nil
		}
		return tx.Model(&discovery.DiscoverySyncStep{}).
			Where("status = ? AND run_id IN ?", "running", runIDs).
			Updates(map[string]any{"status": "failed", "completed_at": now, "updated_at": now, "message": message}).Error
	})
	return recovered, err
}

func (s *DiscoverySyncService) WithConfigService(configs *ConfigService) *DiscoverySyncService {
	s.configs = configs
	return s
}

func (s *DiscoverySyncService) WithAnalystRatingNotifications(notifications *AnalystRatingNotificationService) *DiscoverySyncService {
	s.analystNotifications = notifications
	return s
}

func (s *DiscoverySyncService) WithInAppNotifications(inApp *InAppNotificationService) *DiscoverySyncService {
	s.inApp = inApp
	return s
}

// WithNotificationCenter gives candidate-originated events the same durable
// Telegram queue used by the rest of the application.
func (s *DiscoverySyncService) WithNotificationCenter(center *NotificationBatchService) *DiscoverySyncService {
	s.batches = center
	return s
}

func (s *DiscoverySyncService) withRunner(runner DiscoverySyncRunner) *DiscoverySyncService {
	return s.WithRunner(runner)
}

func (s *DiscoverySyncService) Run(ctx context.Context) (DiscoverySyncResult, error) {
	if s == nil || s.db == nil {
		return DiscoverySyncResult{}, errors.New("discovery sync service is not configured")
	}
	run, err := s.startDiscoverySyncRun("full")
	if err != nil {
		return DiscoverySyncResult{}, err
	}
	stopHeartbeat := s.heartbeatDiscoverySyncRun(run.ID)
	defer stopHeartbeat()
	prepareStep := s.startDiscoverySyncStep(run.ID, "prepare", "准备运行配置与缓存清理")
	taskCtx, cancel, err := s.discoveryTaskContext(ctx)
	if err != nil {
		s.finishDiscoverySyncStep(prepareStep.ID, "failed", 0, err)
		s.finishDiscoverySyncRun(run.ID, DiscoverySyncRunStatusFailed, "failed", "", "", err)
		return DiscoverySyncResult{}, err
	}
	defer cancel()
	s.cleanupExpiredDiscoveryCache(taskCtx)
	s.finishDiscoverySyncStep(prepareStep.ID, "completed", 0, nil)
	runner := s.runner
	if runner == nil {
		buildStep := s.startDiscoverySyncStep(run.ID, "build_sources", "装载 SEC、行情和技术指标数据源")
		built, err := s.buildRunner()
		if err != nil {
			s.finishDiscoverySyncStep(buildStep.ID, "failed", 0, err)
			s.finishDiscoverySyncRun(run.ID, DiscoverySyncRunStatusFailed, "failed", "", "", err)
			return DiscoverySyncResult{}, err
		}
		s.finishDiscoverySyncStep(buildStep.ID, "completed", 0, nil)
		runner = built
	}
	securityStep := s.startDiscoverySyncStep(run.ID, "security_universe", "同步 SEC 标的、财务、股本、Form 4 与融资风险")
	securityBatch, err := runner.SyncSecurityUniverse(taskCtx)
	result := DiscoverySyncResult{SecurityBatch: securityBatch, SecurityBatchID: securityBatch.BatchID}
	if err != nil {
		s.finishDiscoverySyncStep(securityStep.ID, "failed", securityBatch.RecordCount, err)
		s.finishDiscoverySyncRun(run.ID, DiscoverySyncRunStatusFailed, "failed", securityBatch.BatchID, "", err)
		return result, err
	}
	s.finishDiscoverySyncStep(securityStep.ID, "completed", securityBatch.RecordCount, nil)
	s.updateDiscoverySyncRun(run.ID, "market_prescreen", securityBatch.BatchID, "")
	marketStep := s.startDiscoverySyncStep(run.ID, "market_prescreen", "拉取价格、成交量并完成市值预筛")
	// SEC bulk processing and the market-provider chain have independent
	// latency budgets. A long but successful SEC phase must not leave the
	// market phase with an already-expired context.
	marketCtx, marketCancel, contextErr := s.discoveryTaskContext(ctx)
	if contextErr != nil {
		s.finishDiscoverySyncStep(marketStep.ID, "failed", 0, contextErr)
		s.finishDiscoverySyncRun(run.ID, DiscoverySyncRunStatusFailed, "failed", securityBatch.BatchID, "", contextErr)
		return result, contextErr
	}
	defer marketCancel()
	marketBatch, err := runner.SyncMarketPrices(marketCtx)
	result.MarketBatch = marketBatch
	result.MarketBatchID = marketBatch.BatchID
	result.BatchID = marketBatch.BatchID
	if err != nil {
		s.finishDiscoverySyncStep(marketStep.ID, "failed", marketBatch.RecordCount, err)
		result.Status = DiscoverySyncStatusMarketFailed
		result.Summary, _ = discovery.BuildCandidateSummary(marketCtx, s.db, 10)
		result.Health, _ = discovery.BuildCandidateHealth(marketCtx, s.db)
		s.finishDiscoverySyncRun(run.ID, DiscoverySyncRunStatusFailed, "failed", securityBatch.BatchID, marketBatch.BatchID, err)
		return result, fmt.Errorf("%w: %v", ErrDiscoveryMarketSync, err)
	}
	s.finishDiscoverySyncStep(marketStep.ID, "completed", marketBatch.RecordCount, nil)
	result.Status = DiscoverySyncStatusPublished
	s.updateDiscoverySyncRun(run.ID, "technical_history", securityBatch.BatchID, marketBatch.BatchID)
	technicalStep := s.startDiscoverySyncStep(run.ID, "technical_history", "回填候选技术指标历史（非阻断）")
	result.TechnicalHistoryWarmup = s.autoWarmTechnicalHistory(taskCtx)
	if result.TechnicalHistoryWarmup.ErrorMessage != "" {
		s.finishDiscoverySyncStep(technicalStep.ID, "warning", result.TechnicalHistoryWarmup.Result.PersistedCount, errors.New(result.TechnicalHistoryWarmup.ErrorMessage))
	} else {
		s.finishDiscoverySyncStep(technicalStep.ID, result.TechnicalHistoryWarmup.Status, result.TechnicalHistoryWarmup.Result.PersistedCount, nil)
	}
	s.recordCurrentCandidateTradeSetupHistory(taskCtx)
	s.createInAppCandidateEarningsReleases(taskCtx)
	s.updateDiscoverySyncRun(run.ID, "company_profiles", securityBatch.BatchID, marketBatch.BatchID)
	profileStep := s.startDiscoverySyncStep(run.ID, "company_profiles", "增量补充 Longbridge 公司资料（非阻断）")
	profiles := s.autoRefreshLongbridgeCompanyProfiles(taskCtx)
	if profiles.Failed > 0 {
		s.finishDiscoverySyncStep(profileStep.ID, "warning", profiles.Fetched, errors.New(profiles.Message))
	} else {
		s.finishDiscoverySyncStep(profileStep.ID, "completed", profiles.Fetched, nil)
	}
	s.updateDiscoverySyncRun(run.ID, "analyst_ratings", securityBatch.BatchID, marketBatch.BatchID)
	analystStep := s.startDiscoverySyncStep(run.ID, "analyst_ratings", "增量同步 Longbridge 分析师共识（非阻断）")
	analystRatings := s.autoRefreshLongbridgeAnalystRatings(taskCtx)
	if analystRatings.Failed > 0 {
		s.finishDiscoverySyncStep(analystStep.ID, "warning", analystRatings.Fetched, errors.New(analystRatings.Message))
	} else {
		s.finishDiscoverySyncStep(analystStep.ID, "completed", analystRatings.Fetched, nil)
	}
	summaryStep := s.startDiscoverySyncStep(run.ID, "publish_summary", "生成候选摘要与数据健康检查")
	result.Summary, err = discovery.BuildCandidateSummary(taskCtx, s.db, 10)
	if err != nil {
		s.finishDiscoverySyncStep(summaryStep.ID, "failed", 0, err)
		s.finishDiscoverySyncRun(run.ID, DiscoverySyncRunStatusFailed, "failed", securityBatch.BatchID, marketBatch.BatchID, err)
		return result, err
	}
	result.Health, err = discovery.BuildCandidateHealth(taskCtx, s.db)
	if err != nil {
		s.finishDiscoverySyncStep(summaryStep.ID, "failed", 0, err)
		s.finishDiscoverySyncRun(run.ID, DiscoverySyncRunStatusFailed, "failed", securityBatch.BatchID, marketBatch.BatchID, err)
		return result, err
	}
	s.finishDiscoverySyncStep(summaryStep.ID, "completed", result.Summary.TotalA+result.Summary.TotalB, nil)
	s.finishDiscoverySyncRun(run.ID, DiscoverySyncStatusPublished, "completed", securityBatch.BatchID, marketBatch.BatchID, nil)
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
	// A dirty event names specific issuers. Fetching their compact JSON facts is
	// both fresher and dramatically cheaper than parsing companyfacts.zip.
	facts, _, err := source.LoadIncrementalFinancialFacts(ctx, ciks)
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
	return s.runMarketOnly(ctx, "market", false)
}

// RunMarketOnlyForceLive repairs the latest completed market close on demand.
// Unlike the scheduled market workflow, it may call configured quote providers
// before the current US session closes when local price-cache coverage is low.
// It deliberately skips the SEC universe phase.
func (s *DiscoverySyncService) RunMarketOnlyForceLive(ctx context.Context) (DiscoverySyncResult, error) {
	return s.runMarketOnly(ctx, "market-force", true)
}

// RunIncremental is the daily, cache-first workflow. It consumes newly
// detected 10-Q/10-K events, discovers wholly new CIK/ticker identities from
// the compact Nasdaq/SEC directories, enriches only those issuers' JSON
// documents, and then refreshes the market prescreen. It intentionally never
// downloads and parses the multi-gigabyte SEC archives; weekly calibration
// remains responsible for identity changes and full-universe reconciliation.
func (s *DiscoverySyncService) RunIncremental(ctx context.Context) (DiscoverySyncResult, error) {
	// Injected runners are deterministic test/embedding implementations of the
	// complete coordinator contract. Preserve that contract instead of silently
	// skipping their SEC phase.
	if s != nil && s.runner != nil {
		return s.Run(ctx)
	}
	return s.runMarketOnly(ctx, "incremental", false)
}

func (s *DiscoverySyncService) discoverIncrementalListings(ctx context.Context) (IncrementalListingDiscoveryResult, error) {
	if s == nil || s.db == nil {
		return IncrementalListingDiscoveryResult{}, errors.New("discovery sync service is not configured")
	}
	cfg, err := s.appliedDiscoveryConfig(ctx)
	if err != nil {
		return IncrementalListingDiscoveryResult{}, err
	}
	timeout := time.Duration(cfg.TaskTimeoutMin) * time.Minute
	if timeout <= 0 {
		timeout = time.Hour
	}
	downloader := newDiscoveryDownloader(cfg, timeout)
	limits := discovery.ZIPParseLimits{MaxEntries: 1_000_000, MaxEntryBytes: 512 << 20, MaxTotalBytes: 64 << 30}
	sec := discovery.SECBulkSource{Downloader: downloader, TickerURL: cfg.SECTickerExchangeURL, SubmissionsURL: cfg.SECSubmissionsURL, CompanyFactsURL: cfg.SECCompanyFactsURL, Limits: limits, CacheTTL: discoveryCacheTTL(cfg)}
	metadata := discovery.CompositeSecurityMetadataSource{
		Nasdaq:           discovery.NasdaqDirectorySource{Downloader: downloader, ListedURL: cfg.NasdaqListedURL, OtherURL: cfg.NasdaqOtherListedURL, CacheTTL: discoveryCacheTTL(cfg)},
		SEC:              discovery.SECTickerMappingSource{Bulk: sec},
		IdentityVerifier: discovery.ManualIdentityVerificationSource{DB: s.db},
	}
	records, metadataVersion, err := metadata.Load(ctx)
	if err != nil {
		return IncrementalListingDiscoveryResult{}, fmt.Errorf("load current listing directories: %w", err)
	}
	coordinator := &discovery.Coordinator{DB: s.db, Clock: time.Now}
	plan, err := coordinator.FindNewListings(ctx, records)
	if err != nil {
		return IncrementalListingDiscoveryResult{}, fmt.Errorf("compare current listings: %w", err)
	}
	result := IncrementalListingDiscoveryResult{Discovery: discovery.IncrementalListingResult{PreviousBatch: plan.Previous, Batch: plan.Previous, Discovered: len(records), Skipped: plan.Skipped}}
	if len(plan.Records) == 0 {
		return result, nil
	}
	issuerData, err := sec.LoadIncrementalIssuerData(ctx, plan.Records)
	if err != nil {
		return result, fmt.Errorf("load new issuer SEC facts: %w", err)
	}
	result.Warnings = issuerData.Warnings
	result.Discovery, err = coordinator.SyncIncrementalListings(ctx, discovery.IncrementalListingInput{
		Records:        issuerData.Records,
		Shares:         issuerData.Shares,
		FinancialFacts: issuerData.FinancialFacts,
		SourceVersions: []discovery.SourceVersion{metadataVersion, issuerData.Version},
	})
	if err != nil {
		return result, fmt.Errorf("publish incremental listing universe: %w", err)
	}
	if len(result.Warnings) > 0 {
		log.Printf("incremental listing discovery completed with %d issuer warnings: %s", len(result.Warnings), strings.Join(result.Warnings, "; "))
	}
	return result, nil
}

func (s *DiscoverySyncService) runMarketOnly(ctx context.Context, kind string, forceLivePriceFetch bool) (DiscoverySyncResult, error) {
	if s == nil || s.db == nil {
		return DiscoverySyncResult{}, errors.New("discovery sync service is not configured")
	}
	run, err := s.startDiscoverySyncRun(kind)
	if err != nil {
		return DiscoverySyncResult{}, err
	}
	stopHeartbeat := s.heartbeatDiscoverySyncRun(run.ID)
	defer stopHeartbeat()
	prepareStep := s.startDiscoverySyncStep(run.ID, "prepare", "准备运行配置与缓存清理")
	taskCtx, cancel, err := s.discoveryTaskContext(ctx)
	if err != nil {
		s.finishDiscoverySyncStep(prepareStep.ID, "failed", 0, err)
		s.finishDiscoverySyncRun(run.ID, DiscoverySyncRunStatusFailed, "failed", "", "", err)
		return DiscoverySyncResult{}, err
	}
	defer cancel()
	s.cleanupExpiredDiscoveryCache(taskCtx)
	s.finishDiscoverySyncStep(prepareStep.ID, "completed", 0, nil)
	if kind == "incremental" {
		s.updateDiscoverySyncRun(run.ID, "incremental_sec_refresh", "", "")
		financialStep := s.startDiscoverySyncStep(run.ID, "incremental_sec_refresh", "消费新发现的 10-Q/10-K 并仅刷新受影响公司的财务快照")
		financials, refreshErr := s.RefreshDirtyFinancials(taskCtx)
		if refreshErr != nil {
			s.finishDiscoverySyncStep(financialStep.ID, "failed", financials.Recalculated, refreshErr)
			s.finishDiscoverySyncRun(run.ID, DiscoverySyncRunStatusFailed, "failed", "", "", refreshErr)
			return DiscoverySyncResult{}, refreshErr
		}
		s.finishDiscoverySyncStep(financialStep.ID, "completed", financials.Recalculated, nil)
		s.updateDiscoverySyncRun(run.ID, "incremental_listing_discovery", "", "")
		listingStep := s.startDiscoverySyncStep(run.ID, "incremental_listing_discovery", "对比 Nasdaq/SEC 目录并按需补充新上市标的（不下载全量 SEC 档案）")
		listings, listingErr := s.discoverIncrementalListings(taskCtx)
		if listingErr != nil {
			s.finishDiscoverySyncStep(listingStep.ID, "failed", listings.Discovery.Added, listingErr)
			s.finishDiscoverySyncRun(run.ID, DiscoverySyncRunStatusFailed, "failed", listings.Discovery.Batch.BatchID, "", listingErr)
			return DiscoverySyncResult{}, listingErr
		}
		listingStatus := "completed"
		var listingWarning error
		if len(listings.Warnings) > 0 {
			listingStatus = "warning"
			listingWarning = fmt.Errorf("%d 个新增标的 SEC 补数不完整；将在下次增量同步重试", len(listings.Warnings))
		}
		s.finishDiscoverySyncStep(listingStep.ID, listingStatus, listings.Discovery.Added, listingWarning)
		if listings.Discovery.Batch.BatchID != "" {
			s.updateDiscoverySyncRun(run.ID, "incremental_listing_discovery", listings.Discovery.Batch.BatchID, "")
		}
	}
	runner := s.runner
	if runner == nil {
		buildStep := s.startDiscoverySyncStep(run.ID, "build_sources", "装载行情数据源")
		built, err := s.buildRunnerWithForceLivePriceFetch(forceLivePriceFetch)
		if err != nil {
			s.finishDiscoverySyncStep(buildStep.ID, "failed", 0, err)
			s.finishDiscoverySyncRun(run.ID, DiscoverySyncRunStatusFailed, "failed", "", "", err)
			return DiscoverySyncResult{}, err
		}
		s.finishDiscoverySyncStep(buildStep.ID, "completed", 0, nil)
		runner = built
	}
	marketStep := s.startDiscoverySyncStep(run.ID, "market_prescreen", "拉取价格、成交量并完成市值预筛")
	// The incremental SEC/identity pass and market synchronization each get a
	// full configured timeout. Otherwise a successful SEC pass can consume the
	// only context before the market provider or local-cache fallback starts.
	marketCtx, marketCancel, contextErr := s.discoveryTaskContext(ctx)
	if contextErr != nil {
		s.finishDiscoverySyncStep(marketStep.ID, "failed", 0, contextErr)
		s.finishDiscoverySyncRun(run.ID, DiscoverySyncRunStatusFailed, "failed", "", "", contextErr)
		return DiscoverySyncResult{}, contextErr
	}
	defer marketCancel()
	marketBatch, err := runner.SyncMarketPrices(marketCtx)
	result := DiscoverySyncResult{MarketBatch: marketBatch, MarketBatchID: marketBatch.BatchID, BatchID: marketBatch.BatchID}
	if err != nil {
		s.finishDiscoverySyncStep(marketStep.ID, "failed", marketBatch.RecordCount, err)
		result.Status = DiscoverySyncStatusMarketFailed
		result.Summary, _ = discovery.BuildCandidateSummary(marketCtx, s.db, 10)
		result.Health, _ = discovery.BuildCandidateHealth(marketCtx, s.db)
		s.finishDiscoverySyncRun(run.ID, DiscoverySyncRunStatusFailed, "failed", "", marketBatch.BatchID, err)
		return result, fmt.Errorf("%w: %v", ErrDiscoveryMarketSync, err)
	}
	s.finishDiscoverySyncStep(marketStep.ID, "completed", marketBatch.RecordCount, nil)
	result.Status = DiscoverySyncStatusPublished
	s.updateDiscoverySyncRun(run.ID, "technical_history", "", marketBatch.BatchID)
	technicalStep := s.startDiscoverySyncStep(run.ID, "technical_history", "回填候选技术指标历史（非阻断）")
	result.TechnicalHistoryWarmup = s.autoWarmTechnicalHistory(taskCtx)
	if result.TechnicalHistoryWarmup.ErrorMessage != "" {
		s.finishDiscoverySyncStep(technicalStep.ID, "warning", result.TechnicalHistoryWarmup.Result.PersistedCount, errors.New(result.TechnicalHistoryWarmup.ErrorMessage))
	} else {
		s.finishDiscoverySyncStep(technicalStep.ID, result.TechnicalHistoryWarmup.Status, result.TechnicalHistoryWarmup.Result.PersistedCount, nil)
	}
	s.recordCurrentCandidateTradeSetupHistory(taskCtx)
	s.createInAppCandidateEarningsReleases(taskCtx)
	s.updateDiscoverySyncRun(run.ID, "company_profiles", "", marketBatch.BatchID)
	profileStep := s.startDiscoverySyncStep(run.ID, "company_profiles", "增量补充 Longbridge 公司资料（非阻断）")
	profiles := s.autoRefreshLongbridgeCompanyProfiles(taskCtx)
	if profiles.Failed > 0 {
		s.finishDiscoverySyncStep(profileStep.ID, "warning", profiles.Fetched, errors.New(profiles.Message))
	} else {
		s.finishDiscoverySyncStep(profileStep.ID, "completed", profiles.Fetched, nil)
	}
	s.updateDiscoverySyncRun(run.ID, "analyst_ratings", "", marketBatch.BatchID)
	analystStep := s.startDiscoverySyncStep(run.ID, "analyst_ratings", "增量同步 Longbridge 分析师共识（非阻断）")
	analystRatings := s.autoRefreshLongbridgeAnalystRatings(taskCtx)
	if analystRatings.Failed > 0 {
		s.finishDiscoverySyncStep(analystStep.ID, "warning", analystRatings.Fetched, errors.New(analystRatings.Message))
	} else {
		s.finishDiscoverySyncStep(analystStep.ID, "completed", analystRatings.Fetched, nil)
	}
	summaryStep := s.startDiscoverySyncStep(run.ID, "publish_summary", "生成候选摘要与数据健康检查")
	result.Summary, err = discovery.BuildCandidateSummary(taskCtx, s.db, 10)
	if err != nil {
		s.finishDiscoverySyncStep(summaryStep.ID, "failed", 0, err)
		s.finishDiscoverySyncRun(run.ID, DiscoverySyncRunStatusFailed, "failed", "", marketBatch.BatchID, err)
		return result, err
	}
	result.Health, err = discovery.BuildCandidateHealth(taskCtx, s.db)
	if err != nil {
		s.finishDiscoverySyncStep(summaryStep.ID, "failed", 0, err)
		s.finishDiscoverySyncRun(run.ID, DiscoverySyncRunStatusFailed, "failed", "", marketBatch.BatchID, err)
		return result, err
	}
	s.finishDiscoverySyncStep(summaryStep.ID, "completed", result.Summary.TotalA+result.Summary.TotalB, nil)
	s.finishDiscoverySyncRun(run.ID, DiscoverySyncStatusPublished, "completed", "", marketBatch.BatchID, nil)
	return result, err
}

// discoveryTaskContext enforces the configurable end-to-end runtime limit.
// Downloader timeouts alone only bound an individual HTTP request; without a
// task context a long sequence of otherwise successful SEC downloads can run
// indefinitely.
func (s *DiscoverySyncService) discoveryTaskContext(ctx context.Context) (context.Context, context.CancelFunc, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	cfg, err := s.appliedDiscoveryConfig(ctx)
	if err != nil {
		return nil, nil, err
	}
	timeout := time.Duration(cfg.TaskTimeoutMin) * time.Minute
	if timeout <= 0 {
		timeout = time.Hour
	}
	taskCtx, cancel := context.WithTimeout(ctx, timeout)
	return taskCtx, cancel, nil
}

func (s *DiscoverySyncService) cleanupExpiredDiscoveryCache(ctx context.Context) {
	cfg, err := s.appliedDiscoveryConfig(ctx)
	if err != nil {
		log.Printf("discovery cache cleanup skipped: load config: %v", err)
		return
	}
	retentionDays := cfg.CacheRetentionDays
	if retentionDays <= 0 {
		retentionDays = 14
	}
	result, err := discovery.CleanupExpiredCache(cfg.CacheDir, retentionDays, time.Now())
	if err != nil {
		log.Printf("discovery cache cleanup skipped: %v", err)
		return
	}
	if result.FileCount > 0 {
		log.Printf("discovery cache cleanup completed: files=%d bytes=%d retention_days=%d", result.FileCount, result.Bytes, result.RetentionDays)
	}
}

func (s *DiscoverySyncService) startDiscoverySyncRun(kind string) (discovery.DiscoverySyncRun, error) {
	run := discovery.DiscoverySyncRun{Kind: kind, Status: "running", Phase: "security_universe", StartedAt: time.Now().UTC()}
	if kind == "market" {
		run.Phase = "market_prescreen"
	}
	if err := s.db.WithContext(context.Background()).Create(&run).Error; err != nil {
		return discovery.DiscoverySyncRun{}, err
	}
	return run, nil
}

func (s *DiscoverySyncService) heartbeatDiscoverySyncRun(runID uint) func() {
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case now := <-ticker.C:
				_ = s.db.WithContext(context.Background()).Model(&discovery.DiscoverySyncRun{}).Where("id = ? AND status = ?", runID, "running").Update("updated_at", now.UTC()).Error
			}
		}
	}()
	return func() {
		close(stop)
		<-done
	}
}

func (s *DiscoverySyncService) updateDiscoverySyncRun(runID uint, phase, securityBatchID, marketBatchID string) {
	updates := map[string]any{"phase": phase, "updated_at": time.Now().UTC()}
	if securityBatchID != "" {
		updates["security_batch_id"] = securityBatchID
	}
	if marketBatchID != "" {
		updates["market_batch_id"] = marketBatchID
	}
	_ = s.db.WithContext(context.Background()).Model(&discovery.DiscoverySyncRun{}).Where("id = ?", runID).Updates(updates).Error
}

func (s *DiscoverySyncService) startDiscoverySyncStep(runID uint, phase, message string) discovery.DiscoverySyncStep {
	step := discovery.DiscoverySyncStep{
		RunID: runID, Phase: phase, Status: "running", Message: message,
		StartedAt: time.Now().UTC(),
	}
	if err := s.db.WithContext(context.Background()).Create(&step).Error; err != nil {
		log.Printf("discovery sync step start skipped: run=%d phase=%s error=%v", runID, phase, err)
	}
	return step
}

func (s *DiscoverySyncService) finishDiscoverySyncStep(stepID uint, status string, recordCount int, stepErr error) {
	if stepID == 0 {
		return
	}
	if status == "" || status == "running" {
		status = "completed"
	}
	now := time.Now().UTC()
	updates := map[string]any{"status": status, "record_count": recordCount, "completed_at": now, "updated_at": now}
	if stepErr != nil {
		updates["message"] = SanitizeSensitiveError(stepErr.Error())
	}
	if err := s.db.WithContext(context.Background()).Model(&discovery.DiscoverySyncStep{}).Where("id = ?", stepID).Updates(updates).Error; err != nil {
		log.Printf("discovery sync step finish skipped: step=%d error=%v", stepID, err)
	}
}

func (s *DiscoverySyncService) finishDiscoverySyncRun(runID uint, status, phase, securityBatchID, marketBatchID string, runErr error) {
	now := time.Now().UTC()
	updates := map[string]any{"status": status, "phase": phase, "completed_at": now, "updated_at": now}
	if securityBatchID != "" {
		updates["security_batch_id"] = securityBatchID
	}
	if marketBatchID != "" {
		updates["market_batch_id"] = marketBatchID
	}
	if runErr != nil {
		updates["error_message"] = SanitizeSensitiveError(runErr.Error())
	}
	_ = s.db.WithContext(context.Background()).Model(&discovery.DiscoverySyncRun{}).Where("id = ?", runID).Updates(updates).Error
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

// BackfillTickerTechnicalHistory warms local price history for one manually
// monitored symbol. This is deliberately explicit so viewing a target does
// not silently consume a market-data provider request.
func (s *DiscoverySyncService) BackfillTickerTechnicalHistory(ctx context.Context, ticker string, lookbackDays int) (discovery.TechnicalHistoryBackfillResult, error) {
	if s == nil || s.db == nil {
		return discovery.TechnicalHistoryBackfillResult{}, errors.New("discovery sync service is not configured")
	}
	cfg, err := s.appliedDiscoveryConfig(ctx)
	if err != nil {
		return discovery.TechnicalHistoryBackfillResult{}, err
	}
	return s.backfillTickerTechnicalHistoryWithConfig(ctx, cfg, ticker, lookbackDays)
}

// SyncEnabledWatchTargetMarketPrices performs one incremental EOD refresh for
// every enabled monitoring target. It writes local PriceSnapshots only; SEC
// filings, target settings, candidate eligibility, and candidate scores are
// deliberately outside this job's scope.
func (s *DiscoverySyncService) SyncEnabledWatchTargetMarketPrices(ctx context.Context) (WatchTargetMarketSyncResult, error) {
	result := WatchTargetMarketSyncResult{SourceRecordCount: map[string]int{}}
	if s == nil || s.db == nil || s.watchDB == nil {
		return result, errors.New("watch target market sync service is not configured")
	}
	cfg, err := s.appliedDiscoveryConfig(ctx)
	if err != nil {
		return result, err
	}
	return s.syncEnabledWatchTargetMarketPricesWithConfig(ctx, cfg, time.Now())
}

func (s *DiscoverySyncService) syncEnabledWatchTargetMarketPricesWithConfig(ctx context.Context, cfg config.DiscoveryConfig, now time.Time) (WatchTargetMarketSyncResult, error) {
	var enabledCount int64
	if err := s.watchDB.WithContext(ctx).Model(&model.WatchTarget{}).Where("status = ?", "enabled").Count(&enabledCount).Error; err != nil {
		return WatchTargetMarketSyncResult{SourceRecordCount: map[string]int{}}, fmt.Errorf("count enabled watch targets: %w", err)
	}
	if enabledCount == 0 {
		return WatchTargetMarketSyncResult{SourceRecordCount: map[string]int{}, Skipped: true, Message: "没有已启用的监控标的，已跳过日线同步"}, nil
	}
	calendar, err := discovery.NewDatabaseMarketCalendar(s.db, discovery.DefaultNYSECalendarVersion)
	if err != nil {
		return WatchTargetMarketSyncResult{SourceRecordCount: map[string]int{}}, err
	}
	timeout := time.Duration(cfg.TaskTimeoutMin) * time.Minute
	if timeout <= 0 {
		timeout = 60 * time.Minute
	}
	downloads := newDiscoveryDownloader(cfg, timeout)
	provider, marketErr, err := s.buildPriceProvider(cfg, downloads, calendar)
	if err != nil {
		return WatchTargetMarketSyncResult{SourceRecordCount: map[string]int{}}, err
	}
	if marketErr != nil {
		return WatchTargetMarketSyncResult{SourceRecordCount: map[string]int{}}, marketErr
	}
	dated, ok := provider.(discovery.DatedPriceProvider)
	if !ok {
		return WatchTargetMarketSyncResult{SourceRecordCount: map[string]int{}}, errors.New("configured price provider does not support dated daily-close sync")
	}
	return s.syncEnabledWatchTargetMarketPricesWithProvider(ctx, calendar, dated, now)
}

func (s *DiscoverySyncService) syncEnabledWatchTargetMarketPricesWithProvider(ctx context.Context, calendar discovery.MarketCalendar, provider discovery.DatedPriceProvider, now time.Time) (WatchTargetMarketSyncResult, error) {
	result := WatchTargetMarketSyncResult{SourceRecordCount: map[string]int{}}
	var targets []model.WatchTarget
	if err := s.watchDB.WithContext(ctx).Where("status = ?", "enabled").Order("ticker ASC").Find(&targets).Error; err != nil {
		return result, fmt.Errorf("load enabled watch targets: %w", err)
	}
	result.TargetCount = len(targets)
	if len(targets) == 0 {
		result.Skipped, result.Message = true, "没有已启用的监控标的，已跳过日线同步"
		return result, nil
	}
	effectiveDate, trading, err := latestCompletedWatchTargetTradingDate(ctx, calendar, now)
	if err != nil {
		return result, err
	}
	result.EffectiveDate = effectiveDate.Format(time.DateOnly)
	if !trading {
		result.Skipped, result.Message = true, "美股尚未完成新的交易日收盘，已跳过日线同步"
		return result, nil
	}
	listings := enabledWatchTargetListings(targets)
	result.RequestedCount = len(listings)
	if len(listings) == 0 {
		result.Skipped, result.Message = true, "已启用监控标的缺少有效 Ticker，已跳过日线同步"
		return result, nil
	}
	currentCount, missing, err := currentWatchTargetPriceListings(ctx, s.db, listings, effectiveDate)
	if err != nil {
		return result, err
	}
	result.AlreadyCurrentCount = currentCount
	if len(missing) == 0 {
		s.recordTickerTradeSetupHistory(ctx, watchTargetTickers(targets))
		result.Skipped, result.Message = true, fmt.Sprintf("全部 %d 个监控标的已具备 %s 的本地收盘数据，已跳过重复请求", currentCount, result.EffectiveDate)
		return result, nil
	}
	// Candidate and watch-target jobs share PriceSnapshot. By only requesting
	// local gaps, a symbol refreshed by the preceding candidate job is never
	// fetched again merely because another watched symbol needs a price.
	records, providerResult, err := provider.LoadForDate(ctx, missing, result.EffectiveDate)
	result.ProviderResult = providerResult
	if err != nil {
		return result, fmt.Errorf("load enabled watch target daily prices: %w", err)
	}
	result.RecordCount = len(records)
	for _, record := range records {
		source := strings.ToLower(strings.TrimSpace(record.Source))
		if source != "" {
			result.SourceRecordCount[source]++
		}
	}
	result.PersistedCount, err = discovery.PersistTechnicalPriceHistory(ctx, s.db, records, "watch-target-daily:"+result.EffectiveDate)
	if err != nil {
		return result, fmt.Errorf("persist enabled watch target daily prices: %w", err)
	}
	s.recordTickerTradeSetupHistory(ctx, watchTargetTickers(targets))
	result.Message = fmt.Sprintf("已同步 %d 个缺口标的；%d/%d 个监控标的已复用 %s 本地收盘数据", result.RecordCount, result.AlreadyCurrentCount, result.RequestedCount, result.EffectiveDate)
	return result, nil
}

func watchTargetTickers(targets []model.WatchTarget) []string {
	result := make([]string, 0, len(targets))
	for _, target := range targets {
		if ticker := strings.ToUpper(strings.TrimSpace(target.Ticker)); ticker != "" {
			result = append(result, ticker)
		}
	}
	return result
}

func currentWatchTargetPriceListings(ctx context.Context, db *gorm.DB, listings []discovery.Listing, date time.Time) (int, []discovery.Listing, error) {
	if len(listings) == 0 {
		return 0, []discovery.Listing{}, nil
	}
	tickers := make([]string, 0, len(listings))
	for _, listing := range listings {
		if ticker := strings.ToUpper(strings.TrimSpace(listing.Ticker)); ticker != "" {
			tickers = append(tickers, ticker)
		}
	}
	if len(tickers) == 0 {
		return 0, append([]discovery.Listing(nil), listings...), nil
	}
	var rows []struct{ Symbol string }
	if err := db.WithContext(ctx).Model(&discovery.PriceSnapshot{}).Distinct("symbol").
		Where("symbol IN ? AND quality_status = ? AND DATE(trade_date) = ?", tickers, discovery.QualityStatusValid, date.Format(time.DateOnly)).
		Find(&rows).Error; err != nil {
		return 0, nil, fmt.Errorf("load current watch target prices: %w", err)
	}
	current := make(map[string]struct{}, len(rows))
	for _, row := range rows {
		current[strings.ToUpper(strings.TrimSpace(row.Symbol))] = struct{}{}
	}
	missing := make([]discovery.Listing, 0, len(listings))
	for _, listing := range listings {
		if _, ok := current[strings.ToUpper(strings.TrimSpace(listing.Ticker))]; !ok {
			missing = append(missing, listing)
		}
	}
	return len(rows), missing, nil
}

func enabledWatchTargetListings(targets []model.WatchTarget) []discovery.Listing {
	seen := make(map[string]struct{}, len(targets))
	result := make([]discovery.Listing, 0, len(targets))
	for _, target := range targets {
		ticker := strings.ToUpper(strings.TrimSpace(target.Ticker))
		if ticker == "" {
			continue
		}
		if _, exists := seen[ticker]; exists {
			continue
		}
		seen[ticker] = struct{}{}
		result = append(result, discovery.Listing{Ticker: ticker, ProviderTicker: ticker, MappingStatus: discovery.MappingStatusCurrent})
	}
	return result
}

// latestCompletedWatchTargetTradingDate resolves the latest *closed* NYSE
// session. The scheduler may be configured in any local timezone; this guard
// still prevents it from persisting an intraday quote as a daily close.
func latestCompletedWatchTargetTradingDate(ctx context.Context, calendar discovery.MarketCalendar, now time.Time) (time.Time, bool, error) {
	newYork, err := time.LoadLocation("America/New_York")
	if err != nil {
		return time.Time{}, false, err
	}
	local := now.In(newYork)
	candidate := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, newYork)
	marketClose := time.Date(local.Year(), local.Month(), local.Day(), 16, 15, 0, 0, newYork)
	if local.Before(marketClose) {
		candidate = candidate.AddDate(0, 0, -1)
	}
	for offset := 0; offset <= 14; offset++ {
		trading, err := calendar.IsTradingDate(ctx, candidate.Format(time.DateOnly))
		if err != nil {
			return time.Time{}, false, err
		}
		if trading {
			return candidate, true, nil
		}
		candidate = candidate.AddDate(0, 0, -1)
	}
	return time.Time{}, false, errors.New("previous completed NYSE trading date not found")
}

// RefreshCandidateMarketHistoryAndScore repairs one candidate's local daily
// series, then publishes an immutable market correction for that symbol. It
// never downloads SEC data or queries unrelated market symbols.
func (s *DiscoverySyncService) RefreshCandidateMarketHistoryAndScore(ctx context.Context, ticker string) (CandidateMarketHistoryRefreshResult, error) {
	result := CandidateMarketHistoryRefreshResult{}
	if s == nil || s.db == nil {
		return result, errors.New("discovery sync service is not configured")
	}
	cfg, err := s.appliedDiscoveryConfig(ctx)
	if err != nil {
		return result, err
	}
	result.History, err = s.backfillTickerTechnicalHistoryWithConfig(ctx, cfg, ticker, 0)
	if err != nil {
		return result, err
	}
	result.Reprice, err = discovery.RepriceCurrentCandidateFromLocalHistory(ctx, s.db, ticker, time.Now())
	if err != nil {
		return result, err
	}
	return result, nil
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
	// Only candidates without the full MA200 baseline are requested. Passing
	// zero selects the technical-history default (roughly 220 trading days),
	// so existing candidates never re-download their history during daily runs.
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

// recordCurrentCandidateTradeSetupHistory is intentionally non-blocking: the
// market batch remains publishable if the local research-history write has a
// transient SQLite contention. It records only actual plan-status changes.
func (s *DiscoverySyncService) recordCurrentCandidateTradeSetupHistory(ctx context.Context) {
	if s == nil || s.db == nil {
		return
	}
	tickers, err := discovery.CurrentCandidateTickers(ctx, s.db)
	if err != nil {
		log.Printf("record candidate trade setup history: load tickers: %v", err)
		return
	}
	recordedAt := time.Now().UTC()
	if created, err := discovery.RecordTradeSetupStatusTransitions(ctx, s.db, tickers, recordedAt); err != nil {
		log.Printf("record candidate trade setup history: %v", err)
	} else if created > 0 {
		s.createInAppCandidateTechnicalSignals(ctx, tickers, recordedAt)
	}
}

func (s *DiscoverySyncService) createInAppCandidateTechnicalSignals(ctx context.Context, tickers []string, recordedAt time.Time) {
	if s == nil || s.db == nil || len(tickers) == 0 {
		return
	}
	var events []discovery.TradeSetupStatusEvent
	if err := s.db.WithContext(ctx).Where("ticker IN ? AND recorded_at >= ?", tickers, recordedAt.Add(-time.Second)).Find(&events).Error; err != nil {
		log.Printf("record candidate in-app technical signals: %v", err)
		return
	}
	candidates := make([]NotificationCandidate, 0, len(events))
	for _, event := range events {
		if !isActionableCandidateTradeSetupStatus(event.Status) || (event.PreviousStatus == "" && event.Status != discovery.TradeSetupEntryCandidate) {
			continue
		}
		severity := "info"
		if event.Status == discovery.TradeSetupExitWarning {
			severity = "warning"
		} else if event.Status == discovery.TradeSetupInvalidated {
			severity = "danger"
		}
		body := event.EntryTrigger
		if body == "" {
			body = event.ExitReason
		}
		title := "小盘候选技术信号变化：" + candidateTradeSetupStatusLabel(event.Status)
		if s.inApp != nil {
			if _, _, err := s.inApp.Create(ctx, InAppNotificationInput{
				EventKey: fmt.Sprintf("technical-signal:candidate:%s:%s:%s", event.Ticker, event.Status, event.TradeDate), Source: "technical_signal_candidate",
				Scope: "candidate", EntityKind: "trade_setup", Ticker: event.Ticker, Severity: severity,
				Title: title, Body: body, Link: "/discovery-candidates?ticker=" + event.Ticker, OccurredAt: event.RecordedAt,
			}); err != nil {
				log.Printf("create candidate in-app technical signal: %v", err)
			}
		}
		candidates = append(candidates, NotificationCandidate{EntityKind: "trade_setup", FilingID: fmt.Sprintf("technical-signal:candidate:%s:%s:%s", event.Ticker, event.Status, event.TradeDate), Ticker: event.Ticker, Title: title, Reason: "eligible", EventAt: event.RecordedAt})
	}
	if len(candidates) > 0 && s.batches != nil {
		if _, err := s.batches.Deliver(ctx, NotificationBatchInput{Source: "technical_signal_candidate", Trigger: "discovery_sync", Candidates: candidates}); err != nil {
			log.Printf("deliver candidate technical signal notification: %v", err)
		}
	}
}

func isActionableCandidateTradeSetupStatus(status string) bool {
	return status == discovery.TradeSetupEntryCandidate || status == discovery.TradeSetupExitWarning || status == discovery.TradeSetupInvalidated
}

func candidateTradeSetupStatusLabel(status string) string {
	switch status {
	case discovery.TradeSetupEntryCandidate:
		return "入场候选"
	case discovery.TradeSetupExitWarning:
		return "离场预警"
	case discovery.TradeSetupInvalidated:
		return "趋势失效"
	default:
		return status
	}
}

func (s *DiscoverySyncService) createInAppCandidateEarningsReleases(ctx context.Context) {
	if s == nil || s.db == nil {
		return
	}
	var pointer discovery.CurrentBatchPointer
	if err := s.db.WithContext(ctx).First(&pointer, "kind = ?", discovery.BatchKindPrescreen).Error; err != nil {
		return
	}
	var scores []discovery.CandidateScoreSnapshot
	if err := s.db.WithContext(ctx).Where("batch_id = ? AND grade IN ?", pointer.BatchID, []string{discovery.CandidateGradeA, discovery.CandidateGradeB}).Find(&scores).Error; err != nil || len(scores) == 0 {
		return
	}
	securityIDs := make([]uint, 0, len(scores))
	tickerBySecurity := make(map[uint]string, len(scores))
	for _, score := range scores {
		securityIDs = append(securityIDs, score.SecurityID)
		tickerBySecurity[score.SecurityID] = score.Ticker
	}
	var filings []discovery.SECFilingSnapshot
	cutoff := time.Now().UTC().AddDate(0, 0, -3)
	if err := s.db.WithContext(ctx).Where("security_id IN ? AND filing_date >= ?", securityIDs, cutoff).Find(&filings).Error; err != nil {
		log.Printf("load candidate earnings releases: %v", err)
		return
	}
	candidates := make([]NotificationCandidate, 0, len(filings))
	for _, filing := range filings {
		if !isCandidateEarningsReleaseFiling(filing) {
			continue
		}
		occurredAt := filing.FilingDate
		if filing.AcceptedAt != nil {
			occurredAt = *filing.AcceptedAt
		}
		ticker := tickerBySecurity[filing.SecurityID]
		eventKey := fmt.Sprintf("earnings-release:candidate:%s", filing.AccessionNumber)
		title := "小盘候选财报已发布：" + filing.FilingType
		if s.inApp != nil {
			if _, _, err := s.inApp.Create(ctx, InAppNotificationInput{
				EventKey: eventKey, Source: "earnings_release_candidate", Scope: "candidate",
				EntityKind: "filing", Ticker: ticker, Severity: "success", Title: title,
				Body: "SEC 已发布对应定期财报或业绩公告。", Link: "/discovery-candidates?ticker=" + ticker, OccurredAt: occurredAt,
			}); err != nil {
				log.Printf("create candidate earnings-release message: %v", err)
			}
		}
		if !s.candidateNotificationAlreadyQueued(ctx, "earnings_release_candidate", eventKey) {
			candidates = append(candidates, NotificationCandidate{EntityKind: "filing", FilingID: eventKey, Ticker: ticker, FilingType: filing.FilingType, Title: title, FilingURL: filing.FilingURL, Reason: "eligible", EventAt: occurredAt})
		}
	}
	if len(candidates) > 0 && s.batches != nil {
		if _, err := s.batches.Deliver(ctx, NotificationBatchInput{Source: "earnings_release_candidate", Trigger: "discovery_sync", Candidates: candidates}); err != nil {
			log.Printf("deliver candidate earnings-release notification: %v", err)
		}
	}
}

func (s *DiscoverySyncService) candidateNotificationAlreadyQueued(ctx context.Context, source, filingID string) bool {
	if s == nil || s.batches == nil || strings.TrimSpace(filingID) == "" {
		return false
	}
	var count int64
	err := s.batches.db.WithContext(ctx).Model(&model.NotificationBatchItem{}).
		Joins("JOIN notification_batches ON notification_batches.id = notification_batch_items.batch_id").
		Where("notification_batches.source = ? AND notification_batch_items.filing_id = ?", source, filingID).
		Count(&count).Error
	if err != nil {
		log.Printf("check candidate notification deduplication: %v", err)
		return false
	}
	return count > 0
}

func isCandidateEarningsReleaseFiling(filing discovery.SECFilingSnapshot) bool {
	switch strings.ToUpper(strings.TrimSpace(filing.FilingType)) {
	case "10-K", "10-Q", "20-F", "40-F":
		return true
	case "8-K", "6-K":
		return strings.Contains(strings.ToLower(filing.Items), "2.02") || strings.Contains(strings.ToLower(filing.Items), "earnings")
	default:
		return false
	}
}

func (s *DiscoverySyncService) recordTickerTradeSetupHistory(ctx context.Context, tickers []string) {
	if s == nil || s.db == nil {
		return
	}
	recordedAt := time.Now().UTC()
	if created, err := discovery.RecordTradeSetupStatusTransitions(ctx, s.db, tickers, recordedAt); err != nil {
		log.Printf("record watch target trade setup history: %v", err)
	} else if created > 0 {
		s.createInAppWatchTargetTechnicalSignals(ctx, tickers, recordedAt)
	}
}

func (s *DiscoverySyncService) createInAppWatchTargetTechnicalSignals(ctx context.Context, tickers []string, recordedAt time.Time) {
	if s == nil || s.inApp == nil || s.db == nil || s.watchDB == nil || len(tickers) == 0 {
		return
	}
	var targets []model.WatchTarget
	if err := s.watchDB.WithContext(ctx).Where("status = ? AND ticker IN ?", "enabled", tickers).Find(&targets).Error; err != nil {
		log.Printf("load watch targets for in-app technical signals: %v", err)
		return
	}
	targetByTicker := make(map[string]model.WatchTarget, len(targets))
	for _, target := range targets {
		targetByTicker[strings.ToUpper(strings.TrimSpace(target.Ticker))] = target
	}
	var events []discovery.TradeSetupStatusEvent
	if err := s.db.WithContext(ctx).Where("ticker IN ? AND recorded_at >= ?", tickers, recordedAt.Add(-time.Second)).Find(&events).Error; err != nil {
		log.Printf("load watch in-app technical signals: %v", err)
		return
	}
	for _, event := range events {
		target, found := targetByTicker[strings.ToUpper(strings.TrimSpace(event.Ticker))]
		if !found || !isActionableCandidateTradeSetupStatus(event.Status) || (event.PreviousStatus == "" && event.Status != discovery.TradeSetupEntryCandidate) {
			continue
		}
		severity := "info"
		if event.Status == discovery.TradeSetupExitWarning {
			severity = "warning"
		} else if event.Status == discovery.TradeSetupInvalidated {
			severity = "danger"
		}
		body := event.EntryTrigger
		if body == "" {
			body = event.ExitReason
		}
		if _, _, err := s.inApp.Create(ctx, InAppNotificationInput{
			EventKey: fmt.Sprintf("technical-signal:watch_target:%d:%s:%s", target.ID, event.Status, event.TradeDate), Source: "technical_signal_watch_target",
			Scope: "watch_target", EntityKind: "trade_setup", TargetID: target.ID, Ticker: event.Ticker, CompanyName: target.CompanyName,
			Severity: severity, Title: "技术信号变化：" + candidateTradeSetupStatusLabel(event.Status), Body: body, Link: "/targets", OccurredAt: event.RecordedAt,
		}); err != nil {
			log.Printf("create watch in-app technical signal: %v", err)
		}
	}
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

func (s *DiscoverySyncService) backfillTickerTechnicalHistoryWithConfig(ctx context.Context, cfg config.DiscoveryConfig, ticker string, lookbackDays int) (discovery.TechnicalHistoryBackfillResult, error) {
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
	return discovery.BackfillTickerTechnicalHistory(ctx, s.db, history, ticker, time.Now(), lookbackDays)
}

func (s *DiscoverySyncService) buildRunner() (DiscoverySyncRunner, error) {
	return s.buildRunnerWithForceLivePriceFetch(false)
}

func (s *DiscoverySyncService) buildRunnerWithForceLivePriceFetch(forceLivePriceFetch bool) (DiscoverySyncRunner, error) {
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
		ForceLivePriceFetch:   forceLivePriceFetch,
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
	case "longbridge":
		if strings.TrimSpace(cfg.LongbridgeAppKey) == "" || strings.TrimSpace(cfg.LongbridgeAppSecret) == "" || strings.TrimSpace(cfg.LongbridgeAccessToken) == "" {
			return nil, nil, errors.New("LONGBRIDGE credentials are required when SMALL_CAP_PRICE_PROVIDER=longbridge")
		}
		prices, err := discovery.NewLongbridgePriceProvider(discovery.LongbridgePriceProviderOptions{
			AppKey:      cfg.LongbridgeAppKey,
			AppSecret:   cfg.LongbridgeAppSecret,
			AccessToken: cfg.LongbridgeAccessToken,
			Calendar:    calendar,
			Now:         time.Now,
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
