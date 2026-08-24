package discovery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	BatchKindSecurity          = "security-universe"
	BatchKindPrescreen         = "market-prescreen"
	PriceSourceLocalCache      = "local-cache"
	universeChunkSize          = 1000
	maxResearchCoverageDropPct = 15.0
	recentSECFilingLimit       = 20
	// Daily close providers can lag the 16:00 New York regular-session close
	// by a few minutes. Keeping a small buffer avoids treating an intraday or
	// not-yet-final bar as the official close.
	marketCloseAvailabilityHour   = 16
	marketCloseAvailabilityMinute = 15
)

const (
	ReasonPriceMissing       = "price_missing"
	ReasonPriceConflict      = "price_conflict"
	ReasonPriceStale         = "price_stale"
	ReasonPriceFuture        = "price_future"
	ReasonPriceNonTrading    = "price_non_trading"
	ReasonPriceAdjusted      = "price_adjusted"
	ReasonPriceCurrency      = "price_currency"
	ReasonPriceZero          = "price_zero"
	ReasonPriceNegative      = "price_negative"
	ReasonMarketCapOverflow  = "market_cap_overflow"
	ReasonProviderInactive   = "provider_inactive"
	ReasonOutsideMarketCap   = "outside_market_cap"
	ReasonQualifiedSmallCap  = "qualified_small_cap"
	ReasonExchangeNotAllowed = "exchange_not_allowed"
	ReasonClassificationData = "classification_not_valid"
)

var coordinatorRunGate = make(chan struct{}, 1)

func acquireCoordinatorRun(ctx context.Context) (func(), error) {
	select {
	case coordinatorRunGate <- struct{}{}:
		return func() { <-coordinatorRunGate }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

type Coordinator struct {
	DB         *gorm.DB
	Metadata   SecurityMetadataSource
	Shares     ShareFactSource
	Financials FinancialFactSource
	Insiders   InsiderTransactionSource
	Events     CapitalEventSource
	Prices     PriceProvider
	Calendar   MarketCalendar
	Clock      func() time.Time
	// PolicyBinding freezes the exact policy used for this coordinator run.
	// A zero value resolves the active version from DB, with the code default as
	// a compatibility fallback for focused unit tests without policy tables.
	PolicyBinding SmallCapPolicyBinding
	// ResearchMode allows publishing auditable research batches while a price
	// provider is still in validation/degraded state. It is intentionally scoped
	// to discovery output and does not promote the provider health state.
	ResearchMode          bool
	MinPublishCoveragePct float64
	// ForceLivePriceFetch is reserved for an explicit operator action. It
	// bypasses the normal pre-close local-cache reuse so a missed prior close
	// can be repaired without waiting for the next US market close.
	ForceLivePriceFetch bool
	// SecurityStageTimeout bounds one external acquisition stage rather than
	// the whole security workflow. A completed stage can therefore be resumed
	// without inheriting an already-exhausted global deadline.
	SecurityStageTimeout time.Duration
	// SecurityInsiderStageTimeout independently bounds candidate-scoped Form 4
	// enrichment so one provider stage cannot consume the whole workflow budget.
	SecurityInsiderStageTimeout time.Duration
	// SecurityArtifactDir stores compressed, parsed source artifacts used by
	// retries. It normally points at the existing discovery download cache.
	SecurityArtifactDir string
	// SecurityArtifactTTL follows the upstream SEC bulk-cache freshness window.
	// A retry can resume immediately without hiding a genuinely newer archive.
	SecurityArtifactTTL time.Duration
	// OnSecuritySourceStage exposes source acquisition progress to the workflow
	// timeline without coupling discovery to the HTTP/service layer.
	OnSecuritySourceStage func(SecuritySourceStageProgress)
	// AfterStageChunk is a test/operations fault-injection hook. It runs only
	// after a chunk transaction commits and before the next chunk begins.
	AfterStageChunk      func(kind string, chunk int) error
	providerDayEvaluator func(ProviderResult, []PriceRecord, time.Time) (ProviderDayResult, error)
}

type metadataGroup struct {
	Primary  SecuritySourceRecord
	Listings []SecuritySourceRecord
}

type SecuritySourceStageProgress struct {
	Phase       string
	Status      string
	RecordCount int
	TotalCount  int
	Message     string
}

type securityFundamentalsLoader interface {
	LoadSecurityFundamentals(context.Context, map[string]struct{}, []SecuritySourceRecord, SourceVersion) (SecurityFundamentals, error)
}

type prefetchedInsiderLoader interface {
	LoadInsiderTransactionsWithMetadata(context.Context, []SecuritySourceRecord, SourceVersion, map[string]struct{}, time.Time) ([]InsiderTransaction, []InsiderCoverage, SourceVersion, error)
}

type form4ProgressSource interface {
	SetProgressCallback(func(Form4IngestionProgress))
}

type prefetchedCapitalEventLoader interface {
	LoadWithMetadata(context.Context, []SecuritySourceRecord, SourceVersion, map[string]struct{}, time.Time) ([]CapitalEvent, SourceVersion, error)
}

type securitySharesArtifact struct {
	Facts   []ShareFact
	Version SourceVersion
}

type securityFinancialArtifact struct {
	Facts   []FinancialFact
	Version SourceVersion
}

type securityFundamentalsArtifact struct {
	Fundamentals SecurityFundamentals
}

type securityInsiderArtifact struct {
	Transactions []InsiderTransaction
	Coverage     []InsiderCoverage
	Version      SourceVersion
}

type securityCapitalEventArtifact struct {
	Events  []CapitalEvent
	Version SourceVersion
}

func (c *Coordinator) emitSecuritySourceStage(phase, status string, count int, message string) {
	c.emitSecurityStageProgress(phase, status, count, 0, message)
}

func (c *Coordinator) emitSecurityStageProgress(phase, status string, count, total int, message string) {
	if c != nil && c.OnSecuritySourceStage != nil {
		c.OnSecuritySourceStage(SecuritySourceStageProgress{Phase: phase, Status: status, RecordCount: count, TotalCount: total, Message: message})
	}
}

func (c *Coordinator) securitySourceStageContext(ctx context.Context, phase string) (context.Context, context.CancelFunc) {
	timeout := time.Duration(0)
	if c != nil {
		timeout = c.SecurityStageTimeout
		if phase == "security-insiders" && c.SecurityInsiderStageTimeout > 0 {
			timeout = c.SecurityInsiderStageTimeout
		}
	}
	if timeout > 0 {
		return context.WithTimeout(ctx, timeout)
	}
	return context.WithCancel(ctx)
}

func sortedAllowedCIKs(allowed map[string]struct{}) []string {
	result := make([]string, 0, len(allowed))
	for cik := range allowed {
		result = append(result, cik)
	}
	sort.Strings(result)
	return result
}

func runSecuritySourceStage[T any](ctx context.Context, c *Coordinator, effectiveDate, phase, scopeSHA, policySHA string, load func(context.Context) (T, int, error)) (T, error) {
	var zero T
	if ctx == nil || c == nil || load == nil {
		return zero, errors.New("security source stage is invalid")
	}
	artifactKey, err := securitySourceArtifactKey(effectiveDate, phase, scopeSHA, policySHA)
	if err != nil {
		return zero, err
	}
	if strings.TrimSpace(c.SecurityArtifactDir) != "" {
		var cached T
		checkpoint, ok, loadErr := loadSecuritySourceArtifact(ctx, c.DB, c.SecurityArtifactDir, artifactKey, c.SecurityArtifactTTL, &cached)
		if loadErr != nil {
			return zero, loadErr
		}
		if ok {
			c.emitSecuritySourceStage(phase, "resumed", checkpoint.RecordCount, "复用已完成的数据采集检查点")
			return cached, nil
		}
	}
	checkpoint := SecuritySourceCheckpoint{ArtifactKey: artifactKey, Phase: phase, EffectiveDate: effectiveDate, ScopeSHA256: scopeSHA, PolicyContentSHA256: policySHA}
	if err := beginSecuritySourceCheckpoint(ctx, c.DB, checkpoint); err != nil {
		return zero, err
	}
	c.emitSecuritySourceStage(phase, securityCheckpointRunning, 0, "开始采集")
	stageCtx, cancel := c.securitySourceStageContext(ctx, phase)
	payload, count, err := load(stageCtx)
	cancel()
	if err != nil {
		failSecuritySourceCheckpoint(ctx, c.DB, artifactKey, err)
		c.emitSecuritySourceStage(phase, securityCheckpointFailed, count, err.Error())
		return zero, err
	}
	if strings.TrimSpace(c.SecurityArtifactDir) != "" {
		if err := saveSecuritySourceArtifact(ctx, c.DB, c.SecurityArtifactDir, checkpoint, payload, count); err != nil {
			failSecuritySourceCheckpoint(ctx, c.DB, artifactKey, err)
			c.emitSecuritySourceStage(phase, securityCheckpointFailed, count, err.Error())
			return zero, err
		}
	} else {
		now := time.Now().UTC()
		if err := c.DB.WithContext(context.WithoutCancel(ctx)).Model(&SecuritySourceCheckpoint{}).Where("artifact_key = ?", artifactKey).Updates(map[string]any{
			"status": securityCheckpointCompleted, "record_count": count, "completed_at": now,
		}).Error; err != nil {
			return zero, err
		}
	}
	c.emitSecuritySourceStage(phase, securityCheckpointCompleted, count, "采集完成")
	return payload, nil
}

func (c *Coordinator) SyncSecurityUniverse(ctx context.Context) (UniverseBatch, error) {
	release, err := acquireCoordinatorRun(ctx)
	if err != nil {
		return UniverseBatch{}, err
	}
	defer release()
	if err := c.validateBase(ctx); err != nil {
		return UniverseBatch{}, err
	}
	now := c.Clock()
	if c.Metadata == nil || c.Shares == nil || c.Events == nil {
		return c.recordEarlyFailure(ctx, BatchKindSecurity, now, "security-sources", errors.New("metadata, share, and capital event sources are required"))
	}
	if safety, ok := c.Events.(interface{ ProductionSafe() bool }); ok && !safety.ProductionSafe() {
		if noEvents, testOnly := c.Events.(NoCapitalEventsSource); !testOnly || !noEvents.TestOnly {
			return c.recordEarlyFailure(ctx, BatchKindSecurity, now, "capital-events-unsafe", errors.New("disabled capital event source is not production safe"))
		}
	}
	date, err := nyCivilDate(now)
	if err != nil {
		return UniverseBatch{}, err
	}
	binding, err := c.effectivePolicyBinding(ctx)
	if err != nil {
		return UniverseBatch{}, err
	}
	c.emitSecuritySourceStage("security-metadata", securityCheckpointRunning, 0, "加载 Nasdaq 与 SEC 标的元数据")
	metadataCtx, metadataCancel := c.securitySourceStageContext(ctx, "security-metadata")
	records, metadataVersion, err := c.Metadata.Load(metadataCtx)
	metadataCancel()
	if err != nil {
		c.emitSecuritySourceStage("security-metadata", securityCheckpointFailed, 0, err.Error())
		return c.recordEarlyFailure(ctx, BatchKindSecurity, now, "metadata", fmt.Errorf("load security metadata: %w", err))
	}
	c.emitSecuritySourceStage("security-metadata", securityCheckpointCompleted, len(records), "标的元数据加载完成")
	if err := ctx.Err(); err != nil {
		return UniverseBatch{}, err
	}
	if len(records) == 0 {
		return c.recordEarlyFailure(ctx, BatchKindSecurity, now, "metadata-empty", errors.New("security metadata is empty"))
	}
	records, err = normalizeMetadataRecords(records)
	if err != nil {
		return c.recordEarlyFailure(ctx, BatchKindSecurity, now, "metadata-normalization", err)
	}
	allowed := make(map[string]struct{}, len(records))
	for _, record := range records {
		if validCIK(record.CIK) {
			allowed[record.CIK] = struct{}{}
		}
	}
	allowedCIKs := sortedAllowedCIKs(allowed)
	var facts []ShareFact
	var shareVersion SourceVersion
	var financialFacts []FinancialFact
	var financialVersion SourceVersion
	if loader, ok := c.Shares.(securityFundamentalsLoader); ok && c.Financials != nil {
		scopeSHA, hashErr := securitySourceScopeSHA(struct {
			MetadataSHA string
			Allowed     []string
			Parser      string
		}{metadataVersion.SHA256, allowedCIKs, FinancialParserVersion})
		if hashErr != nil {
			return UniverseBatch{}, hashErr
		}
		artifact, loadErr := runSecuritySourceStage(ctx, c, date, "security-fundamentals", scopeSHA, binding.PolicyContentSHA256, func(stageCtx context.Context) (securityFundamentalsArtifact, int, error) {
			loaded, stageErr := loader.LoadSecurityFundamentals(stageCtx, allowed, records, metadataVersion)
			return securityFundamentalsArtifact{Fundamentals: loaded}, len(loaded.Shares) + len(loaded.FinancialFacts), stageErr
		})
		if loadErr != nil {
			return c.recordEarlyFailure(ctx, BatchKindSecurity, now, "fundamentals", fmt.Errorf("load security fundamentals: %w", loadErr))
		}
		facts = artifact.Fundamentals.Shares
		shareVersion = artifact.Fundamentals.ShareVersion
		financialFacts = artifact.Fundamentals.FinancialFacts
		financialVersion = artifact.Fundamentals.FinancialVersion
	} else {
		shareScope, hashErr := securitySourceScopeSHA(struct {
			MetadataSHA string
			Allowed     []string
		}{metadataVersion.SHA256, allowedCIKs})
		if hashErr != nil {
			return UniverseBatch{}, hashErr
		}
		shareArtifact, loadErr := runSecuritySourceStage(ctx, c, date, "security-shares", shareScope, binding.PolicyContentSHA256, func(stageCtx context.Context) (securitySharesArtifact, int, error) {
			loadedFacts, loadedVersion, stageErr := c.Shares.LoadLatestShares(stageCtx, allowed)
			return securitySharesArtifact{Facts: loadedFacts, Version: loadedVersion}, len(loadedFacts), stageErr
		})
		if loadErr != nil {
			return c.recordEarlyFailure(ctx, BatchKindSecurity, now, "shares", fmt.Errorf("load share facts: %w", loadErr))
		}
		facts, shareVersion = shareArtifact.Facts, shareArtifact.Version
		if c.Financials != nil {
			financialScope, hashErr := securitySourceScopeSHA(struct {
				MetadataSHA string
				Allowed     []string
				Parser      string
			}{metadataVersion.SHA256, allowedCIKs, FinancialParserVersion})
			if hashErr != nil {
				return UniverseBatch{}, hashErr
			}
			financialArtifact, loadErr := runSecuritySourceStage(ctx, c, date, "security-financials", financialScope, binding.PolicyContentSHA256, func(stageCtx context.Context) (securityFinancialArtifact, int, error) {
				loadedFacts, loadedVersion, stageErr := c.Financials.LoadFinancialFacts(stageCtx, allowed)
				return securityFinancialArtifact{Facts: loadedFacts, Version: loadedVersion}, len(loadedFacts), stageErr
			})
			if loadErr != nil {
				return c.recordEarlyFailure(ctx, BatchKindSecurity, now, "financials", fmt.Errorf("load financial facts: %w", loadErr))
			}
			financialFacts, financialVersion = financialArtifact.Facts, financialArtifact.Version
		}
	}
	if err := ctx.Err(); err != nil {
		return UniverseBatch{}, err
	}
	if c.Financials != nil && financialVersion.Source == "" {
		return c.recordEarlyFailure(ctx, BatchKindSecurity, now, "financials", errors.New("financial source version is missing"))
	}
	if shareVersion.Source == "" {
		return c.recordEarlyFailure(ctx, BatchKindSecurity, now, "shares", errors.New("share source version is missing"))
	}
	financialFactTotal := len(financialFacts)
	c.emitSecurityStageProgress("security-financial-normalization", securityCheckpointRunning, 0, financialFactTotal, "开始校验并归一化财务事实")
	financialFacts, err = normalizeFinancialFactsWithProgress(financialFacts, func(processed, total int, message string) {
		c.emitSecurityStageProgress("security-financial-normalization", securityCheckpointRunning, processed, total, message)
	})
	if err != nil {
		c.emitSecurityStageProgress("security-financial-normalization", securityCheckpointFailed, 0, financialFactTotal, err.Error())
		return c.recordEarlyFailure(ctx, BatchKindSecurity, now, "financial-normalization", err)
	}
	c.emitSecurityStageProgress("security-financial-normalization", securityCheckpointCompleted, len(financialFacts), len(financialFacts), fmt.Sprintf("已归一化 %d 条财务事实", len(financialFacts)))
	// Form 4 is an enrichment source, not a universe-discovery source. Restrict
	// it to issuers whose financial growth can reach the B candidate threshold,
	// plus the previous A/B pool whose evidence must remain fresh. In particular,
	// a first calibration must never fall back to every exchange-listed issuer.
	insiderAllowed, err := c.candidateInsiderAllowlist(ctx, allowed, financialFacts, binding.Policy, now)
	if err != nil {
		return c.recordEarlyFailure(ctx, BatchKindSecurity, now, "insider-allowlist", err)
	}
	var insiderTransactions []InsiderTransaction
	var insiderCoverage []InsiderCoverage
	var insiderVersion SourceVersion
	if c.Insiders != nil {
		if source, ok := c.Insiders.(form4ProgressSource); ok {
			source.SetProgressCallback(func(progress Form4IngestionProgress) {
				message := fmt.Sprintf("已处理 %d/%d 个发行人的 Form 4", progress.ProcessedIssuers, progress.TotalIssuers)
				if c.OnSecuritySourceStage != nil {
					c.OnSecuritySourceStage(SecuritySourceStageProgress{
						Phase: "security-insiders", Status: securityCheckpointRunning,
						RecordCount: progress.ProcessedIssuers, TotalCount: progress.TotalIssuers, Message: message,
					})
				}
			})
		}
		insiderScope, hashErr := securitySourceScopeSHA(struct {
			MetadataSHA string
			Allowed     []string
			Effective   string
			Parser      string
			Coverage    string
		}{metadataVersion.SHA256, sortedAllowedCIKs(insiderAllowed), date, InsiderParserVersion, InsiderCoverageVersion})
		if hashErr != nil {
			return UniverseBatch{}, hashErr
		}
		artifact, loadErr := runSecuritySourceStage(ctx, c, date, "security-insiders", insiderScope, binding.PolicyContentSHA256, func(stageCtx context.Context) (securityInsiderArtifact, int, error) {
			var transactions []InsiderTransaction
			var coverage []InsiderCoverage
			var version SourceVersion
			var stageErr error
			if source, ok := c.Insiders.(prefetchedInsiderLoader); ok {
				transactions, coverage, version, stageErr = source.LoadInsiderTransactionsWithMetadata(stageCtx, records, metadataVersion, insiderAllowed, now)
			} else if source, ok := c.Insiders.(InsiderTransactionCoverageSource); ok {
				transactions, coverage, version, stageErr = source.LoadInsiderTransactionsWithCoverage(stageCtx, insiderAllowed, now)
			} else {
				transactions, version, stageErr = c.Insiders.LoadInsiderTransactions(stageCtx, insiderAllowed, now)
			}
			return securityInsiderArtifact{Transactions: transactions, Coverage: coverage, Version: version}, len(coverage), stageErr
		})
		if loadErr != nil {
			return c.recordEarlyFailure(ctx, BatchKindSecurity, now, "insiders", fmt.Errorf("load insider transactions: %w", loadErr))
		}
		insiderTransactions, insiderCoverage, insiderVersion = artifact.Transactions, artifact.Coverage, artifact.Version
	}
	eventScope, hashErr := securitySourceScopeSHA(struct {
		MetadataSHA string
		Allowed     []string
		Policy      string
	}{metadataVersion.SHA256, allowedCIKs, CapitalRiskPolicyVersion})
	if hashErr != nil {
		return UniverseBatch{}, hashErr
	}
	eventArtifact, loadErr := runSecuritySourceStage(ctx, c, date, "security-capital-events", eventScope, binding.PolicyContentSHA256, func(stageCtx context.Context) (securityCapitalEventArtifact, int, error) {
		var loaded []CapitalEvent
		var version SourceVersion
		var stageErr error
		if source, ok := c.Events.(prefetchedCapitalEventLoader); ok {
			loaded, version, stageErr = source.LoadWithMetadata(stageCtx, records, metadataVersion, allowed, now)
		} else {
			loaded, version, stageErr = c.Events.Load(stageCtx, allowed, now)
		}
		return securityCapitalEventArtifact{Events: loaded, Version: version}, len(loaded), stageErr
	})
	if loadErr != nil {
		return c.recordEarlyFailure(ctx, BatchKindSecurity, now, "capital-events", fmt.Errorf("load capital events: %w", loadErr))
	}
	events, eventVersion := eventArtifact.Events, eventArtifact.Version
	evidenceTotal := len(facts) + len(insiderTransactions) + len(events)
	c.emitSecurityStageProgress("security-evidence-normalization", securityCheckpointRunning, 0, evidenceTotal, "开始归一化股份、内幕交易和融资风险证据")
	facts, err = normalizeShareFacts(facts)
	if err != nil {
		c.emitSecurityStageProgress("security-evidence-normalization", securityCheckpointFailed, 0, evidenceTotal, err.Error())
		return c.recordEarlyFailure(ctx, BatchKindSecurity, now, "share-normalization", err)
	}
	c.emitSecurityStageProgress("security-evidence-normalization", securityCheckpointRunning, len(facts), evidenceTotal, fmt.Sprintf("已归一化 %d 条股份事实，继续处理内幕交易", len(facts)))
	insiderTransactions, err = normalizeInsiderTransactions(insiderTransactions)
	if err != nil {
		c.emitSecurityStageProgress("security-evidence-normalization", securityCheckpointFailed, len(facts), evidenceTotal, err.Error())
		return c.recordEarlyFailure(ctx, BatchKindSecurity, now, "insider-normalization", err)
	}
	evidenceProcessed := len(facts) + len(insiderTransactions)
	c.emitSecurityStageProgress("security-evidence-normalization", securityCheckpointRunning, evidenceProcessed, evidenceTotal, fmt.Sprintf("已归一化股份与内幕证据 %d 条，继续处理融资风险", evidenceProcessed))
	events, err = normalizeCapitalEvents(events)
	if err != nil {
		c.emitSecurityStageProgress("security-evidence-normalization", securityCheckpointFailed, evidenceProcessed, evidenceTotal, err.Error())
		return c.recordEarlyFailure(ctx, BatchKindSecurity, now, "event-normalization", err)
	}
	evidenceProcessed += len(events)
	c.emitSecurityStageProgress("security-evidence-normalization", securityCheckpointCompleted, evidenceProcessed, evidenceProcessed, fmt.Sprintf("已归一化 %d 条股份、内幕和融资风险证据", evidenceProcessed))
	sourceVersions := []SourceVersion{metadataVersion, shareVersion, eventVersion}
	if c.Financials != nil {
		sourceVersions = append(sourceVersions, financialVersion)
	}
	if c.Insiders != nil {
		sourceVersions = append(sourceVersions, insiderVersion)
	}
	sourceVersions, err = alignSourceVersionsToBatchDate(date, sourceVersions)
	if err != nil {
		return c.recordEarlyFailure(ctx, BatchKindSecurity, now, "source-versions", err)
	}
	versions, err := normalizeSourceVersions(date, sourceVersions...)
	if err != nil {
		return c.recordEarlyFailure(ctx, BatchKindSecurity, now, "source-versions", err)
	}
	var overrides []ManualSecurityOverride
	if err := c.DB.WithContext(ctx).Where("active = ?", true).Order("security_id, id").Find(&overrides).Error; err != nil {
		return c.recordEarlyFailure(ctx, BatchKindSecurity, now, "manual-overrides", err)
	}
	overrideVersion, err := sourceVersionForOverrides(overrides, now)
	if err != nil {
		return UniverseBatch{}, err
	}
	sourceVersions = append(sourceVersions, overrideVersion)
	sourceVersions, err = alignSourceVersionsToBatchDate(date, sourceVersions)
	if err != nil {
		return c.recordEarlyFailure(ctx, BatchKindSecurity, now, "source-versions", err)
	}
	versions, err = normalizeSourceVersions(date, sourceVersions...)
	if err != nil {
		return c.recordEarlyFailure(ctx, BatchKindSecurity, now, "source-versions", err)
	}
	hashTotal := len(records) + len(facts) + len(financialFacts) + len(insiderTransactions) + len(insiderCoverage) + len(events) + len(overrides)
	c.emitSecurityStageProgress("security-content-hash", securityCheckpointRunning, 0, hashTotal, fmt.Sprintf("正在计算 %d 条归一化输入的内容指纹", hashTotal))
	contentSHA, err := hashSecurityInputs(records, facts, financialFacts, insiderTransactions, insiderCoverage, events, overrides)
	if err != nil {
		c.emitSecurityStageProgress("security-content-hash", securityCheckpointFailed, 0, hashTotal, err.Error())
		return UniverseBatch{}, err
	}
	c.emitSecurityStageProgress("security-content-hash", securityCheckpointCompleted, hashTotal, hashTotal, "内容指纹计算完成")
	batch, existed, err := c.createDraft(ctx, BatchKindSecurity, date, versions, contentSHA, now)
	if err != nil || existed {
		return batch, err
	}

	classifications, selections, stageErr := c.stageSecurity(ctx, batch, records, facts, financialFacts, insiderTransactions, insiderCoverage, events, overrides, now)
	if stageErr == nil {
		stageErr = c.runSecurityStageChunk(ctx, batch.BatchID, "security-validation", 0, classifications, func(tx *gorm.DB) error {
			return validateSecurityStage(tx, batch.BatchID, classifications, selections)
		})
	}
	if stageErr != nil {
		return c.failBatch(ctx, batch, stageErr)
	}
	return c.publish(ctx, batch, classifications)
}

func (c *Coordinator) candidateInsiderAllowlist(ctx context.Context, allowed map[string]struct{}, financialFacts []FinancialFact, policy SmallCapPolicy, asOf time.Time) (map[string]struct{}, error) {
	selected := make(map[string]struct{})
	if len(allowed) == 0 {
		return selected, nil
	}
	factsByCIK := make(map[string][]FinancialFact)
	for _, fact := range financialFacts {
		if _, ok := allowed[fact.CIK]; ok {
			factsByCIK[fact.CIK] = append(factsByCIK[fact.CIK], fact)
		}
	}
	for cik, facts := range factsByCIK {
		summary := BuildFinancialSummary(facts, asOf)
		growth, available := summary.QuarterlyRevenueYoYPct, summary.RevenueGrowthAvailable
		if !available && summary.LatestAnnualRevenueUSD > 0 && summary.PriorAnnualRevenueUSD > 0 {
			growth, available = summary.AnnualRevenueYoYPct, true
		}
		if available && growth > policy.BRevenueGrowthMinPct {
			selected[cik] = struct{}{}
		}
	}
	batch, err := currentPublishedBatch(ctx, c.DB, BatchKindPrescreen)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return selected, nil
	}
	if err != nil {
		return nil, err
	}
	var securityIDs []uint
	if err := c.DB.WithContext(ctx).
		Model(&CandidateScoreSnapshot{}).
		Where("batch_id = ? AND grade IN ?", batch.BatchID, []string{CandidateGradeA, CandidateGradeB}).
		Distinct("security_id").
		Pluck("security_id", &securityIDs).Error; err != nil {
		return nil, err
	}
	if len(securityIDs) == 0 {
		return selected, nil
	}
	var ciks []string
	if err := c.DB.WithContext(ctx).Model(&Security{}).Where("id IN ?", securityIDs).Pluck("cik", &ciks).Error; err != nil {
		return nil, err
	}
	for _, cik := range ciks {
		if _, ok := allowed[cik]; ok {
			selected[cik] = struct{}{}
		}
	}
	return selected, nil
}

func (c *Coordinator) SyncMarketPrices(ctx context.Context) (UniverseBatch, error) {
	release, err := acquireCoordinatorRun(ctx)
	if err != nil {
		return UniverseBatch{}, err
	}
	defer release()
	if err := c.validateBase(ctx); err != nil {
		return UniverseBatch{}, err
	}
	now := c.Clock()
	if c.Prices == nil || c.Calendar == nil {
		return c.recordEarlyFailure(ctx, BatchKindPrescreen, now, "market-dependencies", errors.New("price provider and market calendar are required"))
	}
	named, ok := c.Prices.(interface{ ProviderName() string })
	if !ok || strings.TrimSpace(named.ProviderName()) == "" {
		return c.recordEarlyFailure(ctx, BatchKindPrescreen, now, "price-provider", errors.New("price provider must expose its provider name"))
	}
	providerName := strings.TrimSpace(named.ProviderName())
	securityBatch, err := currentPublishedBatch(ctx, c.DB, BatchKindSecurity)
	if err != nil {
		return c.recordEarlyFailure(ctx, BatchKindPrescreen, now, "security-pointer", err)
	}
	var date string
	var useLocalOnlyPrices bool
	if c.ResearchMode {
		date, useLocalOnlyPrices, err = marketPriceRunPlan(ctx, c.Calendar, now)
		if err != nil {
			return c.recordEarlyFailure(ctx, BatchKindPrescreen, now, "market-calendar", err)
		}
		if c.ForceLivePriceFetch {
			useLocalOnlyPrices = false
		}
	} else {
		date, err = nyCivilDate(now)
		if err != nil {
			return UniverseBatch{}, err
		}
	}
	if !c.ResearchMode && securityBatch.EffectiveDate != date {
		return UniverseBatch{}, fmt.Errorf("security batch date %s does not match price run date %s", securityBatch.EffectiveDate, date)
	}
	effectiveAt, err := parseNYCivilDate(date)
	if err != nil {
		return UniverseBatch{}, err
	}
	var health ProviderHealth
	if err := c.DB.WithContext(ctx).First(&health, "provider = ?", providerName).Error; err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			cause := fmt.Errorf("load provider health: %w", err)
			return c.recordPreflightFailure(ctx, BatchKindPrescreen, date, securityBatch, providerName, "health-load-failed", now, cause)
		}
		health = ProviderHealth{Provider: providerName, Status: ProviderStatusValidation}
	}
	if health.Status == ProviderStatusFailed {
		cause := fmt.Errorf("%s: %s", ReasonProviderInactive, health.Status)
		return c.recordPreflightFailure(ctx, BatchKindPrescreen, date, securityBatch, providerName, health.Status, now, cause)
	}
	var window []providerWindowDay
	if strings.TrimSpace(health.WindowJSON) != "" {
		if err := json.Unmarshal([]byte(health.WindowJSON), &window); err != nil {
			cause := fmt.Errorf("decode provider health: %w", err)
			return c.recordPreflightFailure(ctx, BatchKindPrescreen, date, securityBatch, providerName, "health-invalid", now, cause)
		}
	}
	if err := validateProviderHealthWindow(ctx, c.Calendar, health, window); err != nil {
		cause := fmt.Errorf("validate provider health: %w", err)
		return c.recordPreflightFailure(ctx, BatchKindPrescreen, date, securityBatch, providerName, "calendar-or-health-invalid", now, cause)
	}
	if !c.ResearchMode {
		trading, calendarErr := c.Calendar.IsTradingDate(ctx, date)
		if calendarErr != nil || !trading {
			if calendarErr == nil {
				calendarErr = fmt.Errorf("%s is not a trading date", date)
			}
			cause := fmt.Errorf("validate price run calendar: %w", calendarErr)
			return c.recordPreflightFailure(ctx, BatchKindPrescreen, date, securityBatch, providerName, "calendar-invalid", now, cause)
		}
	}
	expected, err := c.currentPriceRequestListings(ctx, securityBatch.BatchID)
	if err != nil {
		return c.recordPreflightFailure(ctx, BatchKindPrescreen, date, securityBatch, providerName, "security-stage-invalid", now, err)
	}
	var records []PriceRecord
	var result ProviderResult
	var loadErr error
	if c.ResearchMode && len(expected) == 0 {
		result, err = emptyResearchProviderResult(providerName, date)
	} else if useLocalOnlyPrices {
		// Weekends and exchange holidays have no new official close. Reusing the
		// last valid local close is both more accurate than querying providers
		// for a nonexistent session and prevents needless API-credit usage.
		records, result, loadErr = c.mergeResearchLocalPriceFallback(ctx, providerName, date, expected, nil, ProviderResult{Provider: providerName}, nil, now)
	} else if dated, ok := c.Prices.(DatedPriceProvider); ok {
		records, result, loadErr = dated.LoadForDate(ctx, expected, date)
	} else {
		records, result, loadErr = c.Prices.Load(ctx, expected)
	}
	if err != nil {
		cause := fmt.Errorf("prepare empty research market prices: %w", err)
		return c.recordPreflightFailure(ctx, BatchKindPrescreen, date, securityBatch, providerName, "load-failed", now, cause)
	}
	if c.ResearchMode && len(expected) > 0 && !useLocalOnlyPrices {
		records, result, err = c.mergeResearchLocalPriceFallback(ctx, providerName, date, expected, records, result, loadErr, now)
		loadErr = nil
	}
	if err != nil {
		cause := fmt.Errorf("merge local market price fallback: %w", err)
		return c.recordPreflightFailure(ctx, BatchKindPrescreen, date, securityBatch, providerName, "load-failed", now, cause)
	}
	if loadErr != nil {
		cause := fmt.Errorf("load market prices: %w", loadErr)
		return c.recordPreflightFailure(ctx, BatchKindPrescreen, date, securityBatch, providerName, "load-failed", now, cause)
	}
	if result.Provider != providerName {
		cause := errors.New("price provider identity changed during load")
		return c.recordPreflightFailure(ctx, BatchKindPrescreen, date, securityBatch, providerName, "identity-invalid", now, cause)
	}
	allowedRecordSources := allowedPriceRecordSources(c.Prices, providerName)
	for _, record := range records {
		if _, ok := allowedRecordSources[strings.ToLower(strings.TrimSpace(record.Source))]; !ok || strings.TrimSpace(record.Symbol) == "" {
			cause := errors.New("price records do not match provider identity")
			return c.recordPreflightFailure(ctx, BatchKindPrescreen, date, securityBatch, providerName, "records-invalid", now, cause)
		}
	}
	priceVersion := SourceVersion{Source: "price:" + result.Provider, Version: result.SourceVersion, SHA256: result.SHA256, EffectiveAt: result.EffectiveDate}
	securityVersion := SourceVersion{Source: BatchKindSecurity, Version: securityBatch.BatchID, SHA256: securityBatch.BatchID, EffectiveAt: effectiveAt}
	var inherited []SourceVersion
	if err := json.Unmarshal([]byte(securityBatch.SourceVersionsJSON), &inherited); err != nil {
		cause := fmt.Errorf("decode security batch source versions: %w", err)
		return c.recordPreflightFailure(ctx, BatchKindPrescreen, date, securityBatch, providerName, "source-versions-invalid", now, cause)
	}
	// SEC metadata may have been published on a different calendar day than
	// the market close. The price batch intentionally references that frozen
	// security universe, but its evidence timestamp must be aligned to the
	// market batch's effective trading date.
	inherited, err = alignSourceVersionsToBatchDate(date, inherited)
	if err != nil {
		cause := fmt.Errorf("align security source versions: %w", err)
		return c.recordPreflightFailure(ctx, BatchKindPrescreen, date, securityBatch, providerName, "source-versions-invalid", now, cause)
	}
	versions, err := normalizeSourceVersions(date, append(inherited, securityVersion, priceVersion)...)
	if err != nil {
		return UniverseBatch{}, err
	}
	contentSHA, err := hashPriceInputs(securityBatch.BatchID, records)
	if err != nil {
		return UniverseBatch{}, err
	}
	batch, existed, err := c.createDraft(ctx, BatchKindPrescreen, date, versions, contentSHA, now)
	if err != nil || existed {
		return batch, err
	}
	// Persist source evidence before applying publication gates so every
	// rejected value remains auditable by its snapshot ID.
	if err := c.persistPrices(ctx, records, result.SourceVersion); err != nil {
		return c.failBatch(ctx, batch, err)
	}
	evaluator := c.providerDayEvaluator
	if evaluator == nil {
		evaluator = EvaluateProviderDay
	}
	var day ProviderDayResult
	if c.ResearchMode && result.Expected == 0 {
		day = emptyResearchProviderDay(result)
	} else {
		day, err = evaluator(result, records, now)
		if err != nil {
			return c.failBatch(ctx, batch, fmt.Errorf("evaluate provider day: %w", err))
		}
	}
	nextHealth := health
	if !(c.ResearchMode && health.LastTradeDate == day.TradeDate.Format(time.DateOnly)) {
		nextHealth, err = AdvanceProviderHealth(ctx, c.Calendar, health, day)
		if err != nil {
			return c.failBatch(ctx, batch, fmt.Errorf("advance provider health: %w", err))
		}
	}
	nextHealth.Provider = providerName
	nextHealth.UpdatedAt = now
	if err := c.DB.WithContext(context.WithoutCancel(ctx)).Transaction(func(tx *gorm.DB) error {
		if err := tx.Save(&nextHealth).Error; err != nil {
			return err
		}
		return c.persistProviderRunDB(tx, batch.BatchID, result, day, nextHealth.Status)
	}); err != nil {
		return c.failBatch(ctx, batch, fmt.Errorf("persist provider diagnostics: %w", err))
	}
	if !c.ResearchMode && (nextHealth.Status != ProviderStatusActive || !providerWindowDayPasses(providerWindowDay{Date: day.TradeDate.Format(time.DateOnly), CoveragePct: day.coveragePct, Timely: day.timely, ValidationOK: day.validationOK, GoldReady: day.goldReady})) {
		return c.failBatch(ctx, batch, errors.New("current provider day failed publication gates"))
	}
	if c.ResearchMode && c.MinPublishCoveragePct > 0 && result.Expected > 0 && result.CoveragePct < c.MinPublishCoveragePct {
		return c.failBatch(ctx, batch, fmt.Errorf("market price coverage %.1f%% is below publish threshold %.1f%%", result.CoveragePct, c.MinPublishCoveragePct))
	}
	if c.ResearchMode && result.Expected > 0 {
		if err := c.rejectResearchCoverageDrop(ctx, batch, result); err != nil {
			return c.failBatch(ctx, batch, err)
		}
	}
	snapshots, stageErr := c.buildUniverseSnapshots(ctx, securityBatch.BatchID, batch.BatchID, records, result, now)
	if stageErr == nil {
		stageErr = c.persistUniverseSnapshots(ctx, snapshots)
	}
	if stageErr == nil {
		stageErr = c.persistCandidateScoreSnapshots(ctx, securityBatch.BatchID, batch.BatchID, snapshots, now)
	}
	if stageErr == nil {
		stageErr = validateMarketStage(c.DB.WithContext(ctx), batch.BatchID, len(snapshots))
	}
	if stageErr != nil {
		return c.failBatch(ctx, batch, stageErr)
	}
	return c.publish(ctx, batch, len(snapshots))
}

func (c *Coordinator) rejectResearchCoverageDrop(ctx context.Context, batch UniverseBatch, result ProviderResult) error {
	var previous ProviderRun
	err := c.DB.WithContext(ctx).
		Table("provider_runs AS run").
		Select("run.*").
		Joins("JOIN universe_batches AS previous_batch ON previous_batch.batch_id = run.batch_id").
		Where("previous_batch.kind = ? AND previous_batch.status = ? AND previous_batch.started_at < ? AND run.provider = ?", BatchKindPrescreen, BatchStatusPublished, batch.StartedAt, result.Provider).
		Order("previous_batch.started_at DESC, run.id DESC").
		First(&previous).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("load previous market coverage: %w", err)
	}
	if previous.ExpectedCount <= 0 || previous.CoveragePct-result.CoveragePct <= maxResearchCoverageDropPct {
		return nil
	}
	return fmt.Errorf("market price coverage dropped from %.1f%% to %.1f%% (maximum decline %.1f%%)", previous.CoveragePct, result.CoveragePct, maxResearchCoverageDropPct)
}

func (c *Coordinator) validateBase(ctx context.Context) error {
	if ctx == nil {
		return errors.New("context is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if c.DB == nil || c.Clock == nil {
		return errors.New("database and clock are required")
	}
	if c.Clock().IsZero() {
		return errors.New("clock returned zero time")
	}
	return nil
}

func nyCivilDate(value time.Time) (string, error) {
	location, err := time.LoadLocation("America/New_York")
	if err != nil {
		return "", err
	}
	return value.In(location).Format(time.DateOnly), nil
}

// marketPriceRunPlan separates the market-price target from the SEC batch
// date. SEC filing data can be refreshed over a weekend, while the next valid
// official US close is still Friday. Before the regular close (plus a small
// provider-finalization buffer), and on non-trading days, local valid prices
// are reused without spending provider quota. After the close, the target is
// the current New York trading date and providers are queried for that close.
func marketPriceRunPlan(ctx context.Context, calendar MarketCalendar, now time.Time) (effectiveDate string, useLocalOnly bool, err error) {
	if calendar == nil {
		return "", false, errors.New("market calendar is required")
	}
	newYork, locationErr := time.LoadLocation("America/New_York")
	if locationErr != nil {
		return "", false, locationErr
	}
	localNow := now.In(newYork)
	today := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 0, 0, 0, 0, newYork)
	todayText := today.Format(time.DateOnly)
	trading, calendarErr := calendar.IsTradingDate(ctx, todayText)
	if calendarErr != nil {
		return "", false, fmt.Errorf("check market calendar for %s: %w", todayText, calendarErr)
	}
	closeAvailableAt := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), marketCloseAvailabilityHour, marketCloseAvailabilityMinute, 0, 0, newYork)
	if trading && !localNow.Before(closeAvailableAt) {
		return todayText, false, nil
	}
	previous, previousErr := previousTradingDate(ctx, calendar, today)
	if previousErr != nil {
		return "", false, fmt.Errorf("find latest completed trading date: %w", previousErr)
	}
	return previous.Format(time.DateOnly), true, nil
}

func parseNYCivilDate(value string) (time.Time, error) {
	location, err := time.LoadLocation("America/New_York")
	if err != nil {
		return time.Time{}, err
	}
	parsed, err := time.ParseInLocation(time.DateOnly, strings.TrimSpace(value), location)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid New York civil date %q", value)
	}
	return parsed, nil
}

func emptyResearchProviderResult(provider, date string) (ProviderResult, error) {
	date = strings.TrimSpace(date)
	if date == "" {
		return ProviderResult{}, errors.New("market effective date is required")
	}
	effectiveDate, err := parseNYCivilDate(date)
	if err != nil {
		return ProviderResult{}, err
	}
	seed := provider + "\x00research-empty\x00" + date
	sha := sha256.Sum256([]byte(seed))
	return ProviderResult{
		Provider:           provider,
		Status:             ProviderStatusValidation,
		SourceVersion:      provider + ":research-empty:" + date,
		SHA256:             hex.EncodeToString(sha[:]),
		EffectiveDate:      effectiveDate,
		Records:            0,
		Expected:           0,
		CoveragePct:        100,
		Timely:             true,
		ValidationErrorPct: 0,
	}, nil
}

func allowedPriceRecordSources(provider PriceProvider, providerName string) map[string]struct{} {
	allowed := map[string]struct{}{strings.ToLower(strings.TrimSpace(providerName)): {}, PriceSourceLocalCache: {}}
	if provider == nil {
		return allowed
	}
	if sourceProvider, ok := provider.(RecordSourceAllowlistProvider); ok {
		for _, source := range sourceProvider.AllowedRecordSources() {
			if source = strings.ToLower(strings.TrimSpace(source)); source != "" {
				allowed[source] = struct{}{}
			}
		}
	}
	return allowed
}

// mergeResearchLocalPriceFallback fills only symbols missing from the live
// provider chain with a valid local quote from the effective or immediately
// previous trading day. It is deliberately limited to research publishing:
// production provider-health validation must never be advanced by cached data.
func (c *Coordinator) mergeResearchLocalPriceFallback(ctx context.Context, providerName, effectiveDate string, expected []Listing, live []PriceRecord, liveResult ProviderResult, loadErr error, now time.Time) ([]PriceRecord, ProviderResult, error) {
	effective, err := parseNYCivilDate(effectiveDate)
	if err != nil {
		return nil, ProviderResult{}, err
	}
	previous, err := previousTradingDate(ctx, c.Calendar, effective)
	if err != nil {
		// Local fallback is an enrichment for research-mode coverage. When the
		// provider already returned usable records, reaching the workflow
		// deadline while looking up cached rows must not discard those records
		// or fail the whole market batch.
		if len(live) > 0 && isContextExpiry(err) {
			return live, liveResult, nil
		}
		return nil, ProviderResult{}, fmt.Errorf("find local fallback trading date: %w", err)
	}
	covered := make(map[string]struct{}, len(live))
	for _, record := range live {
		if symbol := strings.ToUpper(strings.TrimSpace(record.Symbol)); symbol != "" {
			covered[symbol] = struct{}{}
		}
	}
	fallback, err := c.localPriceFallbackRecords(ctx, expected, covered, previous, effective)
	if err != nil {
		if len(live) > 0 && isContextExpiry(err) {
			return live, liveResult, nil
		}
		return nil, ProviderResult{}, fmt.Errorf("load local price fallback: %w", err)
	}
	if len(live) == 0 && len(fallback) == 0 && loadErr != nil {
		return nil, ProviderResult{}, loadErr
	}
	merged := append(append([]PriceRecord(nil), live...), fallback...)
	if len(merged) == 0 {
		if loadErr != nil {
			return nil, ProviderResult{}, loadErr
		}
		return nil, ProviderResult{}, errors.New("no live or local fallback market prices available")
	}
	// Preserve the provider's original evidence version when it already covered
	// everything it returned and no local evidence was required. Apart from
	// avoiding needless writes, this keeps deterministic retry IDs stable.
	if len(fallback) == 0 && loadErr == nil {
		return live, liveResult, nil
	}
	childVersions := []string{"local-cache:none"}
	if strings.TrimSpace(liveResult.SourceVersion) != "" {
		childVersions[0] = "live:" + liveResult.SourceVersion
	}
	if len(fallback) > 0 {
		childVersions = append(childVersions, fmt.Sprintf("local-cache:%d", len(fallback)))
	}
	sortPriceRecordsByExpected(merged, expected)
	version, sha := chainSourceVersion(providerName, effective, childVersions, merged)
	result, err := validatePriceBatch(ctx, merged, PriceValidationOptions{
		Provider:                      providerName,
		SourceVersion:                 version,
		EffectiveDate:                 effective,
		Now:                           now,
		Calendar:                      c.Calendar,
		Expected:                      expected,
		AllowPreviousTradingDatePrice: true,
	})
	if err != nil {
		return nil, ProviderResult{}, err
	}
	result.SHA256 = sha
	return merged, result, nil
}

func isContextExpiry(err error) bool {
	return errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled)
}

func (c *Coordinator) localPriceFallbackRecords(ctx context.Context, expected []Listing, covered map[string]struct{}, previous, effective time.Time) ([]PriceRecord, error) {
	symbols := make([]string, 0, len(expected)*2)
	seen := map[string]struct{}{}
	for _, listing := range expected {
		for _, symbol := range []string{listing.Ticker, listing.ProviderTicker} {
			symbol = strings.ToUpper(strings.TrimSpace(symbol))
			if symbol == "" {
				continue
			}
			if _, exists := seen[symbol]; !exists {
				seen[symbol] = struct{}{}
				symbols = append(symbols, symbol)
			}
		}
	}
	if len(symbols) == 0 {
		return nil, nil
	}
	var rows []PriceSnapshot
	if err := c.DB.WithContext(ctx).
		Where("symbol IN ? AND quality_status = ? AND trade_date >= ? AND trade_date <= ?", symbols, QualityStatusValid, previous, effective).
		Order("trade_date DESC, created_at DESC, id DESC").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	bySymbol := make(map[string]PriceSnapshot, len(rows))
	for _, row := range rows {
		symbol := strings.ToUpper(strings.TrimSpace(row.Symbol))
		if _, exists := bySymbol[symbol]; !exists {
			bySymbol[symbol] = row
		}
	}
	result := make([]PriceRecord, 0, len(expected))
	for _, listing := range expected {
		canonical := strings.ToUpper(strings.TrimSpace(listing.Ticker))
		if canonical == "" {
			continue
		}
		if _, exists := covered[canonical]; exists {
			continue
		}
		row, found := bySymbol[canonical]
		if !found {
			row, found = bySymbol[strings.ToUpper(strings.TrimSpace(listing.ProviderTicker))]
		}
		if !found {
			continue
		}
		result = append(result, PriceRecord{
			Symbol: canonical, TradeDate: row.TradeDate,
			CloseMicros: row.CloseMicros, Volume: row.Volume, Currency: row.Currency,
			Adjusted: row.Adjusted, Source: PriceSourceLocalCache,
		})
	}
	return result, nil
}

func emptyResearchProviderDay(result ProviderResult) ProviderDayResult {
	sha := sha256.Sum256([]byte(result.Provider + "\x00research-empty\x00" + result.SourceVersion))
	return ProviderDayResult{
		TradeDate:    result.EffectiveDate,
		coveragePct:  result.CoveragePct,
		timely:       result.Timely,
		validationOK: true,
		goldSHA256:   hex.EncodeToString(sha[:]),
	}
}

func normalizeSourceVersions(effectiveDate string, input ...SourceVersion) ([]SourceVersion, error) {
	if len(input) == 0 {
		return nil, errors.New("source versions are required")
	}
	seen := make(map[string]struct{}, len(input))
	result := append([]SourceVersion(nil), input...)
	for i := range result {
		result[i].Source = strings.TrimSpace(result[i].Source)
		result[i].Version = strings.TrimSpace(result[i].Version)
		result[i].SHA256 = strings.ToLower(strings.TrimSpace(result[i].SHA256))
		if result[i].Source == "" || result[i].Version == "" || !validSHA256(result[i].SHA256) {
			return nil, fmt.Errorf("invalid source version at index %d", i)
		}
		if _, duplicate := seen[result[i].Source]; duplicate {
			return nil, fmt.Errorf("duplicate source version %q", result[i].Source)
		}
		seen[result[i].Source] = struct{}{}
		if !result[i].EffectiveAt.IsZero() {
			date, err := nyCivilDate(result[i].EffectiveAt)
			if err != nil || date != effectiveDate {
				return nil, fmt.Errorf("source %q effective date does not match %s", result[i].Source, effectiveDate)
			}
		}
		// Batch identity is based on source hashes and the civil date, never on
		// incidental fetch timestamps within that date.
		newYork, locationErr := time.LoadLocation("America/New_York")
		if locationErr != nil {
			return nil, locationErr
		}
		result[i].EffectiveAt, _ = time.ParseInLocation(time.DateOnly, effectiveDate, newYork)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Source < result[j].Source })
	return result, nil
}

func alignSourceVersionsToBatchDate(effectiveDate string, input []SourceVersion) ([]SourceVersion, error) {
	effectiveAt, err := parseNYCivilDate(effectiveDate)
	if err != nil {
		return nil, err
	}
	result := append([]SourceVersion(nil), input...)
	for i := range result {
		if !result[i].EffectiveAt.IsZero() {
			result[i].EffectiveAt = effectiveAt
		}
	}
	return result, nil
}

func (c *Coordinator) recordPreflightFailure(ctx context.Context, kind, date string, securityBatch UniverseBatch, provider, state string, now time.Time, cause error) (UniverseBatch, error) {
	sha := sha256.Sum256([]byte(provider + "\x00" + state))
	effectiveAt, effectiveErr := parseNYCivilDate(date)
	if effectiveErr != nil {
		effectiveAt = now
	}
	versions, versionErr := normalizeSourceVersions(date,
		SourceVersion{Source: BatchKindSecurity, Version: securityBatch.BatchID, SHA256: securityBatch.BatchID, EffectiveAt: effectiveAt},
		SourceVersion{Source: "provider-preflight:" + provider, Version: state, SHA256: hex.EncodeToString(sha[:]), EffectiveAt: effectiveAt},
	)
	if versionErr != nil {
		return UniverseBatch{}, cause
	}
	batch, existed, createErr := c.createDraft(ctx, kind, date, versions, hex.EncodeToString(sha[:]), now)
	if createErr != nil && !existed {
		return UniverseBatch{}, cause
	}
	if batch.Status == BatchStatusPublished {
		return batch, cause
	}
	return c.failBatch(ctx, batch, cause)
}

func (c *Coordinator) recordEarlyFailure(ctx context.Context, kind string, now time.Time, stage string, cause error) (UniverseBatch, error) {
	date, err := nyCivilDate(now)
	if err != nil {
		return UniverseBatch{}, cause
	}
	seed := fmt.Sprintf("%s\x00%s\x00%d\x00%s", kind, stage, now.UnixNano(), cause.Error())
	sha := sha256.Sum256([]byte(seed))
	version := SourceVersion{Source: "failed:" + stage, Version: fmt.Sprintf("run-%d", now.UnixNano()), SHA256: hex.EncodeToString(sha[:]), EffectiveAt: now}
	versions, err := normalizeSourceVersions(date, version)
	if err != nil {
		return UniverseBatch{}, cause
	}
	batch, _, err := c.createDraft(context.WithoutCancel(ctx), kind, date, versions, hex.EncodeToString(sha[:]), now)
	if err != nil {
		return UniverseBatch{}, cause
	}
	return c.failBatch(ctx, batch, cause)
}

func batchIdentity(kind, date string, versions []SourceVersion, contentSHA string) (string, string, error) {
	return batchIdentityWithPolicy(kind, date, versions, contentSHA, defaultSmallCapPolicyBinding().PolicyContentSHA256)
}

func batchIdentityWithPolicy(kind, date string, versions []SourceVersion, contentSHA, policySHA string) (string, string, error) {
	if !validSHA256(contentSHA) {
		return "", "", errors.New("batch content SHA256 is required")
	}
	encoded, err := json.Marshal(versions)
	if err != nil {
		return "", "", err
	}
	seed := append([]byte(kind+"\n"+date+"\n"+contentSHA+"\n"+policySHA+"\n"), encoded...)
	digest := sha256.Sum256(seed)
	return hex.EncodeToString(digest[:]), string(encoded), nil
}

func (c *Coordinator) createDraft(ctx context.Context, kind, date string, versions []SourceVersion, contentSHA string, now time.Time) (UniverseBatch, bool, error) {
	if !validSHA256(contentSHA) {
		return UniverseBatch{}, false, errors.New("batch content SHA256 is required")
	}
	binding, err := c.effectivePolicyBinding(ctx)
	if err != nil {
		return UniverseBatch{}, false, err
	}
	id, encoded, err := batchIdentityWithPolicy(kind, date, versions, contentSHA, binding.PolicyContentSHA256)
	if err != nil {
		return UniverseBatch{}, false, err
	}
	batch := UniverseBatch{BatchID: id, Kind: kind, Status: BatchStatusDraft, EffectiveDate: date, SourceVersionsJSON: encoded, ContentSHA256: contentSHA, StartedAt: now,
		PolicyVersionID: binding.PolicyVersionID, PolicyVersion: binding.PolicyVersion, PolicyContentSHA256: binding.PolicyContentSHA256, PolicySnapshotJSON: binding.PolicySnapshotJSON}
	for _, version := range versions {
		switch {
		case version.Source == BatchKindSecurity || strings.Contains(version.Source, "metadata") || strings.Contains(version.Source, "nasdaq") || strings.Contains(version.Source, "sec-bulk"):
			batch.UniverseSourceVersion = version.Version
		case strings.HasPrefix(version.Source, "price:"):
			batch.PriceSourceVersion = version.Version
		case strings.Contains(version.Source, "share"):
			batch.ShareSourceVersion = version.Version
		}
	}
	result := c.DB.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&batch)
	if result.Error != nil {
		return UniverseBatch{}, false, result.Error
	}
	if result.RowsAffected == 1 {
		return batch, false, nil
	}
	var existing UniverseBatch
	if err := c.DB.WithContext(ctx).First(&existing, "batch_id = ?", id).Error; err != nil {
		return UniverseBatch{}, false, err
	}
	if existing.Kind != kind || existing.EffectiveDate != date || existing.SourceVersionsJSON != encoded || existing.ContentSHA256 != contentSHA {
		return UniverseBatch{}, false, errors.New("deterministic batch ID content conflict")
	}
	if existing.Status == BatchStatusPublished {
		return existing, true, nil
	}
	if existing.Status == BatchStatusDraft || existing.Status == BatchStatusFailed || existing.Status == BatchStatusPartial {
		var resumeErr error
		if kind == BatchKindSecurity {
			resumeErr = c.resumeRetryableBatch(ctx, batch)
		} else {
			resumeErr = c.resetRetryableBatch(ctx, batch)
		}
		if resumeErr != nil {
			return UniverseBatch{}, false, resumeErr
		}
		retry, err := currentBatchByID(ctx, c.DB, id)
		return retry, false, err
	}
	return existing, true, fmt.Errorf("batch %s already exists with status %s", id, existing.Status)
}

// resetRetryableBatch keeps the original market-batch semantics. Market
// staging is not checkpointed yet and provider diagnostics must describe only
// the latest attempt, so its retry still rebuilds batch-owned rows.
func (c *Coordinator) resetRetryableBatch(ctx context.Context, batch UniverseBatch) error {
	return c.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, model := range []any{
			&UniverseSnapshot{},
			&CandidateScoreSnapshot{},
			&ProviderRun{},
			&SocialHeatSnapshot{},
			&CapitalRiskSnapshot{},
			&FinancialMetricSnapshot{},
			&InsiderCoverageSnapshot{},
			&BatchShareSelection{},
			&ClassificationSnapshot{},
			&SecurityBatchIdentity{},
			&ListingIdentitySnapshot{},
		} {
			if err := cleanupBatchRows(tx, model, batch.BatchID); err != nil {
				return err
			}
		}
		result := tx.Model(&UniverseBatch{}).Where("batch_id = ?", batch.BatchID).Updates(map[string]any{
			"kind": batch.Kind, "status": BatchStatusDraft, "effective_date": batch.EffectiveDate,
			"source_versions_json": batch.SourceVersionsJSON, "content_sha256": batch.ContentSHA256,
			"record_count": 0, "universe_source_version": batch.UniverseSourceVersion,
			"price_source_version": batch.PriceSourceVersion, "share_source_version": batch.ShareSourceVersion,
			"policy_version_id": batch.PolicyVersionID, "policy_version": batch.PolicyVersion,
			"policy_content_sha256": batch.PolicyContentSHA256, "policy_snapshot_json": batch.PolicySnapshotJSON,
			"started_at": batch.StartedAt, "completed_at": nil, "error_message": "",
		})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return fmt.Errorf("reset retryable batch affected %d rows", result.RowsAffected)
		}
		return nil
	})
}

func (c *Coordinator) resumeRetryableBatch(ctx context.Context, batch UniverseBatch) error {
	return c.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// The batch identity includes every normalized input hash and the frozen
		// policy hash. Therefore an identical ID means committed chunks are safe
		// to resume; changed inputs naturally create a different batch. Never
		// delete successful work merely to turn a failed batch back into a draft.
		result := tx.Model(&UniverseBatch{}).Where("batch_id = ?", batch.BatchID).Updates(map[string]any{
			"kind":                    batch.Kind,
			"status":                  BatchStatusDraft,
			"effective_date":          batch.EffectiveDate,
			"source_versions_json":    batch.SourceVersionsJSON,
			"content_sha256":          batch.ContentSHA256,
			"record_count":            0,
			"universe_source_version": batch.UniverseSourceVersion,
			"price_source_version":    batch.PriceSourceVersion,
			"share_source_version":    batch.ShareSourceVersion,
			"policy_version_id":       batch.PolicyVersionID,
			"policy_version":          batch.PolicyVersion,
			"policy_content_sha256":   batch.PolicyContentSHA256,
			"policy_snapshot_json":    batch.PolicySnapshotJSON,
			// Preserve the first attempt timestamp so the batch audit trail covers
			// its complete lifetime across resumptions.
			"completed_at":  nil,
			"error_message": "",
		})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return fmt.Errorf("resume retryable batch affected %d rows", result.RowsAffected)
		}
		return nil
	})
}

func hashSecurityInputs(records []SecuritySourceRecord, facts []ShareFact, financials []FinancialFact, insiders []InsiderTransaction, insiderCoverage []InsiderCoverage, events []CapitalEvent, overrides []ManualSecurityOverride) (string, error) {
	recordCopy := append([]SecuritySourceRecord(nil), records...)
	sort.Slice(recordCopy, func(i, j int) bool { return canonicalLess(recordCopy[i], recordCopy[j]) })
	factCopy := append([]ShareFact(nil), facts...)
	sort.Slice(factCopy, func(i, j int) bool { return canonicalLess(factCopy[i], factCopy[j]) })
	financialCopy := append([]FinancialFact(nil), financials...)
	sort.Slice(financialCopy, func(i, j int) bool { return canonicalLess(financialCopy[i], financialCopy[j]) })
	insiderCopy := append([]InsiderTransaction(nil), insiders...)
	sort.Slice(insiderCopy, func(i, j int) bool { return canonicalLess(insiderCopy[i], insiderCopy[j]) })
	coverageCopy := append([]InsiderCoverage(nil), insiderCoverage...)
	sort.Slice(coverageCopy, func(i, j int) bool { return canonicalLess(coverageCopy[i], coverageCopy[j]) })
	eventCopy := append([]CapitalEvent(nil), events...)
	sort.Slice(eventCopy, func(i, j int) bool { return canonicalLess(eventCopy[i], eventCopy[j]) })
	overrideCopy := canonicalManualOverrides(overrides)
	return hashCanonicalContent(struct {
		Records    []SecuritySourceRecord    `json:"records"`
		Facts      []ShareFact               `json:"facts"`
		Financials []FinancialFact           `json:"financials"`
		Insiders   []InsiderTransaction      `json:"insiders"`
		Coverage   []InsiderCoverage         `json:"insider_coverage"`
		Events     []CapitalEvent            `json:"events"`
		Overrides  []canonicalManualOverride `json:"overrides"`
	}{recordCopy, factCopy, financialCopy, insiderCopy, coverageCopy, eventCopy, overrideCopy})
}

func sourceVersionForOverrides(rows []ManualSecurityOverride, at time.Time) (SourceVersion, error) {
	digest, err := hashCanonicalContent(canonicalManualOverrides(rows))
	if err != nil {
		return SourceVersion{}, err
	}
	return SourceVersion{Source: "classification:manual-overrides", Version: digest, SHA256: digest, EffectiveAt: at}, nil
}

type canonicalManualOverride struct {
	SecurityID                                   uint `json:"security_id"`
	EffectiveStatus, Reason, SourceURL, Operator string
}

func canonicalManualOverrides(rows []ManualSecurityOverride) []canonicalManualOverride {
	result := make([]canonicalManualOverride, 0, len(rows))
	for _, row := range rows {
		result = append(result, canonicalManualOverride{row.SecurityID, strings.TrimSpace(row.EffectiveStatus), strings.TrimSpace(row.Reason), strings.TrimSpace(row.SourceURL), strings.TrimSpace(row.Operator)})
	}
	sort.Slice(result, func(i, j int) bool { return canonicalLess(result[i], result[j]) })
	return result
}

func canonicalLess(a, b any) bool {
	ab, _ := json.Marshal(a)
	bb, _ := json.Marshal(b)
	return string(ab) < string(bb)
}

func normalizeMetadataRecords(input []SecuritySourceRecord) ([]SecuritySourceRecord, error) {
	seen := map[string]SecuritySourceRecord{}
	for _, row := range input {
		row.Ticker = strings.ToUpper(strings.TrimSpace(row.Ticker))
		key := row.CIK + "\x00" + row.Ticker
		if prior, ok := seen[key]; ok {
			if !reflect.DeepEqual(prior, row) {
				return nil, fmt.Errorf("metadata identity %s/%s has conflicting duplicates", row.CIK, row.Ticker)
			}
			continue
		}
		seen[key] = row
	}
	result := make([]SecuritySourceRecord, 0, len(seen))
	for _, row := range seen {
		result = append(result, row)
	}
	sort.Slice(result, func(i, j int) bool { return canonicalLess(result[i], result[j]) })
	return result, nil
}

func normalizeShareFacts(input []ShareFact) ([]ShareFact, error) {
	seen := map[string]ShareFact{}
	for _, row := range input {
		key := strings.Join([]string{row.CIK, row.Concept, row.Unit, row.Form, row.Accession, row.Instant.UTC().Format(time.RFC3339Nano)}, "\x00")
		if prior, ok := seen[key]; ok {
			if !reflect.DeepEqual(prior, row) {
				return nil, fmt.Errorf("share fact identity has conflicting duplicates: %s", row.Accession)
			}
			continue
		}
		seen[key] = row
	}
	result := make([]ShareFact, 0, len(seen))
	for _, row := range seen {
		result = append(result, row)
	}
	sort.Slice(result, func(i, j int) bool { return canonicalLess(result[i], result[j]) })
	return result, nil
}

func normalizeFinancialFacts(input []FinancialFact) ([]FinancialFact, error) {
	return normalizeFinancialFactsWithProgress(input, nil)
}

func normalizeFinancialFactsWithProgress(input []FinancialFact, progress func(processed, total int, message string)) ([]FinancialFact, error) {
	seen := map[string]FinancialFact{}
	for index, row := range input {
		if progress != nil && ((index+1)%50000 == 0 || index+1 == len(input)) {
			progress(index+1, len(input), fmt.Sprintf("已校验 %d/%d 条财务事实", index+1, len(input)))
		}
		key := strings.Join([]string{row.CIK, row.Metric, row.Concept, row.Unit, row.Form, row.Accession, row.PeriodStart.UTC().Format(time.RFC3339Nano), row.PeriodEnd.UTC().Format(time.RFC3339Nano)}, "\x00")
		if prior, ok := seen[key]; ok {
			if !reflect.DeepEqual(prior, row) {
				return nil, fmt.Errorf("financial fact identity has conflicting duplicates: %s", row.Accession)
			}
			continue
		}
		seen[key] = row
	}
	result := make([]FinancialFact, 0, len(seen))
	for _, row := range seen {
		result = append(result, row)
	}
	if progress != nil {
		progress(len(input), len(input), fmt.Sprintf("已去重为 %d 条财务事实，正在稳定排序", len(result)))
	}
	sort.Slice(result, func(i, j int) bool { return canonicalLess(result[i], result[j]) })
	return result, nil
}

func normalizeInsiderTransactions(input []InsiderTransaction) ([]InsiderTransaction, error) {
	seen := map[string]InsiderTransaction{}
	for _, row := range input {
		// One Form 4 can contain multiple same-day rows with identical share
		// count and price but different post-transaction ownership (for example
		// sequential sales from separate lots). Keep the ownership balances in
		// the identity so legitimate rows are not treated as corrupt duplicates.
		key := strings.Join([]string{row.CIK, row.Accession, row.OwnerName, row.TransactionDate.UTC().Format(time.RFC3339Nano), row.TransactionCode, row.AcquiredDisposedCode, fmt.Sprintf("%f", row.Shares), fmt.Sprintf("%f", row.PricePerShareUSD), fmt.Sprintf("%f", row.SharesOwnedAfter), fmt.Sprintf("%f", row.SharesOwnedBefore), fmt.Sprintf("%t", row.Derivative)}, "\x00")
		if prior, ok := seen[key]; ok {
			if !equivalentInsiderTransactions(prior, row) {
				return nil, fmt.Errorf("insider transaction identity has conflicting duplicates: %s", row.Accession)
			}
			prior.SourceURL = preferredInsiderSourceURL(row.Accession, prior.SourceURL, row.SourceURL)
			seen[key] = prior
			continue
		}
		seen[key] = row
	}
	result := make([]InsiderTransaction, 0, len(seen))
	for _, row := range seen {
		result = append(result, row)
	}
	sort.Slice(result, func(i, j int) bool { return canonicalLess(result[i], result[j]) })
	return result, nil
}

func equivalentInsiderTransactions(left, right InsiderTransaction) bool {
	left.SourceURL = ""
	right.SourceURL = ""
	return reflect.DeepEqual(left, right)
}

func preferredInsiderSourceURL(accession, left, right string) string {
	archiveCIK, ok := form4AccessionCIK(accession)
	if ok {
		archivePath := "/" + strings.TrimLeft(archiveCIK, "0") + "/" + strings.ReplaceAll(accession, "-", "") + "/"
		leftCanonical := strings.Contains(left, archivePath)
		rightCanonical := strings.Contains(right, archivePath)
		if leftCanonical != rightCanonical {
			if leftCanonical {
				return left
			}
			return right
		}
	}
	if left == "" || (right != "" && right < left) {
		return right
	}
	return left
}

func normalizeCapitalEvents(input []CapitalEvent) ([]CapitalEvent, error) {
	seen := map[string]CapitalEvent{}
	for _, row := range input {
		key := strings.Join([]string{row.CIK, normalizeCapitalEventKind(row.Kind), row.Accession, row.EffectiveAt.UTC().Format(time.RFC3339Nano)}, "\x00")
		if prior, ok := seen[key]; ok {
			if !reflect.DeepEqual(prior, row) {
				return nil, fmt.Errorf("capital event identity has conflicting duplicates: %s", row.Accession)
			}
			continue
		}
		seen[key] = row
	}
	result := make([]CapitalEvent, 0, len(seen))
	for _, row := range seen {
		result = append(result, row)
	}
	sort.Slice(result, func(i, j int) bool { return canonicalLess(result[i], result[j]) })
	return result, nil
}

func hashPriceInputs(securityBatchID string, records []PriceRecord) (string, error) {
	copyRecords := append([]PriceRecord(nil), records...)
	sort.Slice(copyRecords, func(i, j int) bool { return canonicalLess(copyRecords[i], copyRecords[j]) })
	return hashCanonicalContent(struct {
		SecurityBatchID string        `json:"security_batch_id"`
		Records         []PriceRecord `json:"records"`
	}{securityBatchID, copyRecords})
}

func hashCanonicalContent(value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}

func (c *Coordinator) stageSecurity(ctx context.Context, batch UniverseBatch, records []SecuritySourceRecord, facts []ShareFact, financialFacts []FinancialFact, insiderTransactions []InsiderTransaction, insiderCoverage []InsiderCoverage, events []CapitalEvent, overrides []ManualSecurityOverride, now time.Time) (int, int, error) {
	listingRows := make([]ListingIdentitySnapshot, 0, len(records))
	mapped := make([]SecuritySourceRecord, 0, len(records))
	for _, record := range records {
		key := record.SourceKey
		if key == "" {
			key = strings.ToUpper(strings.TrimSpace(record.Ticker))
		}
		status, reason := EffectiveStatusDataInsufficient, ReasonMappingConflict
		if validCIK(record.CIK) && record.MappingStatus == MappingStatusCurrent {
			status, reason = "", ""
			mapped = append(mapped, record)
		}
		listingRows = append(listingRows, ListingIdentitySnapshot{BatchID: batch.BatchID, SourceKey: key, CIK: record.CIK, Ticker: record.Ticker, ProviderTicker: record.ProviderTicker, Exchange: record.Exchange, CompanyName: record.CompanyName, MappingStatus: record.MappingStatus, Included: false, Status: status, ReasonCode: reason, EvidenceJSON: record.EvidenceJSON, CreatedAt: now})
	}
	for start := 0; start < len(listingRows); start += universeChunkSize {
		end := start + universeChunkSize
		if end > len(listingRows) {
			end = len(listingRows)
		}
		chunk := append([]ListingIdentitySnapshot(nil), listingRows[start:end]...)
		if err := c.runSecurityStageChunk(ctx, batch.BatchID, "security-listings", start/universeChunkSize, len(chunk), func(tx *gorm.DB) error {
			return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&chunk).Error
		}); err != nil {
			return 0, 0, err
		}
	}
	groups := []metadataGroup{}
	if len(mapped) > 0 {
		var err error
		groups, err = groupMetadata(mapped)
		if err != nil {
			return 0, 0, err
		}
	}
	factsByCIK := make(map[string][]ShareFact)
	for _, fact := range facts {
		factsByCIK[fact.CIK] = append(factsByCIK[fact.CIK], fact)
	}
	financialsByCIK := make(map[string][]FinancialFact)
	for _, fact := range financialFacts {
		financialsByCIK[fact.CIK] = append(financialsByCIK[fact.CIK], fact)
	}
	insidersByCIK := make(map[string][]InsiderTransaction)
	for _, tx := range insiderTransactions {
		insidersByCIK[tx.CIK] = append(insidersByCIK[tx.CIK], tx)
	}
	eventsByCIK := make(map[string][]CapitalEvent)
	for _, event := range events {
		eventsByCIK[event.CIK] = append(eventsByCIK[event.CIK], event)
	}
	type listingClassification struct {
		included       bool
		status, reason string
	}
	classificationBySourceKey := make(map[string]listingClassification, len(records))
	for _, group := range groups {
		classification := ClassifySecurity(group.Primary, overrides)
		for _, listing := range group.Listings {
			listingResult := classification
			if isAttachedNonCommonListing(listing) {
				listingResult = excluded(ReasonNonCommonSecurity, "security_name", listing.SecurityName)
			}
			classificationBySourceKey[listingIdentitySourceKey(listing)] = listingClassification{listingResult.Included, listingResult.Status, listingResult.ReasonCode}
		}
	}
	// Each group can write up to five rows (security, identity,
	// classification, share evidence, selection). Keep transactions below the
	// hard 1,000-row budget even for an all-new fixture.
	const groupsPerTransaction = 190
	groupChunks := (len(groups) + groupsPerTransaction - 1) / groupsPerTransaction
	if groupChunks == 0 {
		groupChunks = 1
	}
	for chunkIndex := 0; chunkIndex < groupChunks; chunkIndex++ {
		start := chunkIndex * groupsPerTransaction
		end := start + groupsPerTransaction
		if end > len(groups) {
			end = len(groups)
		}
		chunkGroups := append([]metadataGroup(nil), groups[start:end]...)
		err := c.runSecurityStageChunk(ctx, batch.BatchID, BatchKindSecurity, chunkIndex, len(chunkGroups), func(tx *gorm.DB) error {
			for _, group := range chunkGroups {
				source := group.Primary
				if err := ctx.Err(); err != nil {
					return err
				}
				security := Security{CIK: source.CIK}
				if err := tx.Where("cik = ?", source.CIK).Attrs(Security{CatalogStatus: SecurityCatalogStaged, CreatedBatchID: batch.BatchID}).FirstOrCreate(&security).Error; err != nil {
					return err
				}
				source.SecurityID = security.ID
				if source.MappingStatus == "" {
					source.MappingStatus = MappingStatusCurrent
				}
				providerTicker := strings.ToUpper(strings.TrimSpace(source.ProviderTicker))
				if providerTicker == "" {
					providerTicker = strings.ToUpper(strings.TrimSpace(source.Ticker))
				}
				identity := SecurityBatchIdentity{BatchID: batch.BatchID, SecurityID: security.ID, CIK: source.CIK, Ticker: strings.ToUpper(strings.TrimSpace(source.Ticker)), ProviderTicker: providerTicker, Exchange: source.Exchange, MappingStatus: source.MappingStatus, CompanyName: source.CompanyName, SIC: source.SIC, SICDescription: source.SICDescription, StateOfIncorporation: source.StateOfIncorporation, LatestAnnualForm: source.LatestAnnualForm, CreatedAt: now}
				if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&identity).Error; err != nil {
					return err
				}
				classification := ClassifySecurity(source, overrides)
				evidence, _ := json.Marshal(classification.Evidence)
				row := ClassificationSnapshot{BatchID: batch.BatchID, SecurityID: security.ID, Included: classification.Included, Status: classification.Status, Confidence: classification.Confidence, ReasonCode: classification.ReasonCode, RuleVersion: ClassificationRuleVersion, EvidenceJSON: string(evidence), CreatedAt: now}
				if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error; err != nil {
					return err
				}
				selection := SelectShareSnapshot(factsByCIK[source.CIK], eventsByCIK[source.CIK], now)
				var shareID *uint
				if selection.Fact != nil {
					f := selection.Fact
					snapshot := ShareSnapshot{SecurityID: security.ID, Instant: f.Instant, Accession: f.Accession, Concept: f.Concept, Form: f.Form, SourceURL: f.SourceURL, QualityStatus: selection.QualityStatus, Shares: f.Shares, FiledAt: f.FiledAt, AcceptedAt: f.AcceptedAt, CreatedAt: now}
					if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&snapshot).Error; err != nil {
						return err
					}
					if snapshot.ID == 0 {
						if err := tx.Where("security_id = ? AND instant = ? AND accession = ?", security.ID, f.Instant, f.Accession).First(&snapshot).Error; err != nil {
							return err
						}
					}
					shareID = &snapshot.ID
				}
				binding := BatchShareSelection{BatchID: batch.BatchID, SecurityID: security.ID, ShareSnapshotID: shareID, QualityStatus: selection.QualityStatus, ReasonCode: selection.ReasonCode, CreatedAt: now}
				if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&binding).Error; err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			return len(groups), len(groups), err
		}
	}
	securityIDByCIK := make(map[string]uint, len(groups))
	var stagedIdentities []SecurityBatchIdentity
	if err := c.DB.WithContext(ctx).Where("batch_id = ?", batch.BatchID).Find(&stagedIdentities).Error; err != nil {
		return len(groups), len(groups), err
	}
	for _, identity := range stagedIdentities {
		securityIDByCIK[identity.CIK] = identity.SecurityID
	}
	for start := 0; start < len(listingRows); start += universeChunkSize {
		end := start + universeChunkSize
		if end > len(listingRows) {
			end = len(listingRows)
		}
		chunk := append([]ListingIdentitySnapshot(nil), listingRows[start:end]...)
		if err := c.runSecurityStageChunk(ctx, batch.BatchID, "security-listing-classification", start/universeChunkSize, len(chunk), func(tx *gorm.DB) error {
			for _, row := range chunk {
				classification, ok := classificationBySourceKey[strings.ToUpper(strings.TrimSpace(row.SourceKey))]
				if !ok {
					continue
				}
				if err := tx.Model(&ListingIdentitySnapshot{}).Where("batch_id = ? AND source_key = ?", batch.BatchID, row.SourceKey).Updates(map[string]any{"included": classification.included, "status": classification.status, "reason_code": classification.reason}).Error; err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			return len(groups), len(groups), err
		}
	}
	if len(financialsByCIK) > 0 {
		if err := c.persistFinancialSnapshots(ctx, batch.BatchID, securityIDByCIK, financialsByCIK, now); err != nil {
			return len(groups), len(groups), err
		}
	} else if err := c.runSecurityStageChunk(ctx, batch.BatchID, "financial-facts", 0, 0, func(*gorm.DB) error { return nil }); err != nil {
		return len(groups), len(groups), err
	}
	// Company facts includes prior 10-Q/10-K cover-page share counts. Persist
	// those immutable facts as well as the current batch selection so dilution
	// research can start immediately instead of waiting a year of daily runs.
	if err := c.persistHistoricalShareSnapshotsForBatch(ctx, batch.BatchID, securityIDByCIK, factsByCIK, now); err != nil {
		return len(groups), len(groups), err
	}
	if len(insidersByCIK) > 0 {
		if err := c.persistInsiderSnapshots(ctx, batch.BatchID, securityIDByCIK, insidersByCIK, now); err != nil {
			return len(groups), len(groups), err
		}
	} else if err := c.runSecurityStageChunk(ctx, batch.BatchID, "insider-transactions", 0, 0, func(*gorm.DB) error { return nil }); err != nil {
		return len(groups), len(groups), err
	}
	if len(insiderCoverage) > 0 {
		if err := c.persistInsiderCoverageSnapshots(ctx, batch.BatchID, securityIDByCIK, insiderCoverage, now); err != nil {
			return len(groups), len(groups), err
		}
	} else if err := c.runSecurityStageChunk(ctx, batch.BatchID, "insider-coverage", 0, 0, func(*gorm.DB) error { return nil }); err != nil {
		return len(groups), len(groups), err
	}
	if err := c.persistRecentSECFilingSnapshotsForBatch(ctx, batch.BatchID, securityIDByCIK, groups, now); err != nil {
		return len(groups), len(groups), err
	}
	if len(eventsByCIK) > 0 {
		if err := c.persistCapitalRiskSnapshots(ctx, batch.BatchID, securityIDByCIK, eventsByCIK, now); err != nil {
			return len(groups), len(groups), err
		}
	} else if err := c.runSecurityStageChunk(ctx, batch.BatchID, "capital-risks", 0, 0, func(*gorm.DB) error { return nil }); err != nil {
		return len(groups), len(groups), err
	}
	return len(groups), len(groups), nil
}

func (c *Coordinator) persistHistoricalShareSnapshots(ctx context.Context, securityIDByCIK map[string]uint, factsByCIK map[string][]ShareFact, now time.Time) error {
	return c.persistHistoricalShareSnapshotsForBatch(ctx, "adhoc-historical-shares", securityIDByCIK, factsByCIK, now)
}

func (c *Coordinator) persistHistoricalShareSnapshotsForBatch(ctx context.Context, batchID string, securityIDByCIK map[string]uint, factsByCIK map[string][]ShareFact, now time.Time) error {
	rows := make([]ShareSnapshot, 0)
	for cik, facts := range factsByCIK {
		securityID, ok := securityIDByCIK[cik]
		if !ok {
			continue
		}
		for _, fact := range facts {
			if !eligibleHistoricalShareFact(fact, now) {
				continue
			}
			rows = append(rows, ShareSnapshot{SecurityID: securityID, Instant: fact.Instant, Accession: fact.Accession, Concept: fact.Concept, Form: fact.Form, SourceURL: fact.SourceURL, QualityStatus: QualityStatusValid, Shares: fact.Shares, FiledAt: fact.FiledAt, AcceptedAt: fact.AcceptedAt, CreatedAt: now})
		}
	}
	sort.Slice(rows, func(i, j int) bool { return canonicalLess(rows[i], rows[j]) })
	chunks := (len(rows) + universeChunkSize - 1) / universeChunkSize
	if chunks == 0 {
		chunks = 1
	}
	for chunkIndex := 0; chunkIndex < chunks; chunkIndex++ {
		start := chunkIndex * universeChunkSize
		end := start + universeChunkSize
		if end > len(rows) {
			end = len(rows)
		}
		chunk := append([]ShareSnapshot(nil), rows[start:end]...)
		if err := c.runSecurityStageChunk(ctx, batchID, "historical-shares", chunkIndex, len(chunk), func(tx *gorm.DB) error {
			if len(chunk) == 0 {
				return nil
			}
			return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&chunk).Error
		}); err != nil {
			return err
		}
	}
	return nil
}

func eligibleHistoricalShareFact(fact ShareFact, asOf time.Time) bool {
	if !eligibleShareFact(fact, asOf) {
		return false
	}
	// eligibleShareFact permits a zero acceptance timestamp for later
	// conflict handling, but an unaccepted historical fact cannot be used in
	// point-in-time dilution evidence.
	return !fact.AcceptedAt.IsZero() && !fact.AcceptedAt.After(asOf)
}

func (c *Coordinator) persistRecentSECFilingSnapshots(ctx context.Context, securityIDByCIK map[string]uint, groups []metadataGroup, now time.Time) error {
	return c.persistRecentSECFilingSnapshotsForBatch(ctx, "adhoc-sec-filing-index", securityIDByCIK, groups, now)
}

func (c *Coordinator) persistRecentSECFilingSnapshotsForBatch(ctx context.Context, batchID string, securityIDByCIK map[string]uint, groups []metadataGroup, now time.Time) error {
	rows := make([]SECFilingSnapshot, 0)
	for _, group := range groups {
		securityID, ok := securityIDByCIK[group.Primary.CIK]
		if !ok {
			continue
		}
		filings := append([]FilingMetadata(nil), group.Primary.FilingMetadata...)
		sort.Slice(filings, func(i, j int) bool {
			left, right := filingMetadataTimestamp(filings[i]), filingMetadataTimestamp(filings[j])
			if left.Equal(right) {
				return filings[i].Accession > filings[j].Accession
			}
			return left.After(right)
		})
		seen := map[string]struct{}{}
		for _, filing := range filings {
			accession := strings.TrimSpace(filing.Accession)
			if accession == "" {
				continue
			}
			if _, duplicate := seen[accession]; duplicate {
				continue
			}
			seen[accession] = struct{}{}
			rows = append(rows, secFilingSnapshotFromMetadata(securityID, filing, now))
			if len(seen) >= recentSECFilingLimit {
				break
			}
		}
	}
	sort.Slice(rows, func(i, j int) bool { return canonicalLess(rows[i], rows[j]) })
	chunks := (len(rows) + universeChunkSize - 1) / universeChunkSize
	if chunks == 0 {
		chunks = 1
	}
	for chunkIndex := 0; chunkIndex < chunks; chunkIndex++ {
		start := chunkIndex * universeChunkSize
		end := start + universeChunkSize
		if end > len(rows) {
			end = len(rows)
		}
		chunk := append([]SECFilingSnapshot(nil), rows[start:end]...)
		if err := c.runSecurityStageChunk(ctx, batchID, "sec-filing-index", chunkIndex, len(chunk), func(tx *gorm.DB) error {
			if len(chunk) == 0 {
				return nil
			}
			return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&chunk).Error
		}); err != nil {
			return err
		}
	}
	return nil
}

func filingMetadataTimestamp(filing FilingMetadata) time.Time {
	if !filing.AcceptedAt.IsZero() {
		return filing.AcceptedAt
	}
	return filing.FiledAt
}

func secFilingSnapshotFromMetadata(securityID uint, filing FilingMetadata, now time.Time) SECFilingSnapshot {
	var reportDate, acceptedAt *time.Time
	if !filing.ReportAt.IsZero() {
		value := filing.ReportAt
		reportDate = &value
	}
	if !filing.AcceptedAt.IsZero() {
		value := filing.AcceptedAt
		acceptedAt = &value
	}
	return SECFilingSnapshot{
		SecurityID: securityID, AccessionNumber: strings.TrimSpace(filing.Accession), FilingType: strings.TrimSpace(filing.Form),
		FilingDate: filing.FiledAt, ReportDate: reportDate, AcceptedAt: acceptedAt,
		PrimaryDocument: strings.TrimSpace(filing.PrimaryDocument), Items: strings.TrimSpace(filing.Items),
		FilingURL: secFilingURL(filing), CreatedAt: now,
	}
}

func secFilingURL(filing FilingMetadata) string {
	cik := strings.TrimLeft(strings.TrimSpace(filing.CIK), "0")
	accession := strings.ReplaceAll(strings.TrimSpace(filing.Accession), "-", "")
	if cik == "" || accession == "" {
		return ""
	}
	base := "https://www.sec.gov/Archives/edgar/data/" + cik + "/" + accession + "/"
	if document := strings.TrimSpace(filing.PrimaryDocument); document != "" {
		return base + document
	}
	return base
}

func (c *Coordinator) persistFinancialSnapshots(ctx context.Context, batchID string, securityIDByCIK map[string]uint, factsByCIK map[string][]FinancialFact, now time.Time) error {
	type financialRowSet struct {
		Facts   []FinancialFactSnapshot
		Metrics []FinancialMetricSnapshot
	}
	rows := financialRowSet{}
	for cik, facts := range factsByCIK {
		securityID, ok := securityIDByCIK[cik]
		if !ok {
			continue
		}
		for _, fact := range facts {
			rows.Facts = append(rows.Facts, financialFactSnapshot(securityID, fact, now))
		}
		metric, err := FinancialMetricFromFacts(batchID, securityID, facts, now)
		if err != nil {
			return err
		}
		rows.Metrics = append(rows.Metrics, metric)
	}
	sort.Slice(rows.Facts, func(i, j int) bool { return canonicalLess(rows.Facts[i], rows.Facts[j]) })
	sort.Slice(rows.Metrics, func(i, j int) bool { return canonicalLess(rows.Metrics[i], rows.Metrics[j]) })
	factChunks := (len(rows.Facts) + universeChunkSize - 1) / universeChunkSize
	if factChunks == 0 {
		factChunks = 1
	}
	c.emitSecurityStageProgress("security-financial-persistence", securityCheckpointRunning, 0, len(rows.Facts), fmt.Sprintf("开始分块入库 %d 条财务事实", len(rows.Facts)))
	for chunkIndex := 0; chunkIndex < factChunks; chunkIndex++ {
		start := chunkIndex * universeChunkSize
		end := start + universeChunkSize
		if end > len(rows.Facts) {
			end = len(rows.Facts)
		}
		chunk := append([]FinancialFactSnapshot(nil), rows.Facts[start:end]...)
		if err := c.runSecurityStageChunk(ctx, batchID, "financial-facts", chunkIndex, len(chunk), func(tx *gorm.DB) error {
			if len(chunk) == 0 {
				return nil
			}
			return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&chunk).Error
		}); err != nil {
			c.emitSecurityStageProgress("security-financial-persistence", securityCheckpointFailed, start, len(rows.Facts), err.Error())
			return err
		}
		c.emitSecurityStageProgress("security-financial-persistence", securityCheckpointRunning, end, len(rows.Facts), fmt.Sprintf("已入库 %d/%d 条财务事实", end, len(rows.Facts)))
	}
	c.emitSecurityStageProgress("security-financial-persistence", securityCheckpointCompleted, len(rows.Facts), len(rows.Facts), fmt.Sprintf("已入库 %d 条财务事实", len(rows.Facts)))
	metricChunks := (len(rows.Metrics) + universeChunkSize - 1) / universeChunkSize
	if metricChunks == 0 {
		metricChunks = 1
	}
	for chunkIndex := 0; chunkIndex < metricChunks; chunkIndex++ {
		start := chunkIndex * universeChunkSize
		end := start + universeChunkSize
		if end > len(rows.Metrics) {
			end = len(rows.Metrics)
		}
		chunk := append([]FinancialMetricSnapshot(nil), rows.Metrics[start:end]...)
		if err := c.runSecurityStageChunk(ctx, batchID, "financial-metrics", chunkIndex, len(chunk), func(tx *gorm.DB) error {
			if len(chunk) == 0 {
				return nil
			}
			return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&chunk).Error
		}); err != nil {
			return err
		}
	}
	return nil
}

func financialFactSnapshot(securityID uint, fact FinancialFact, now time.Time) FinancialFactSnapshot {
	return FinancialFactSnapshot{
		SecurityID: securityID, Metric: fact.Metric, Concept: fact.Concept, PeriodStart: fact.PeriodStart,
		PeriodEnd: fact.PeriodEnd, Accession: fact.Accession, Unit: fact.Unit, AmountMicros: fact.AmountMicros,
		Form: fact.Form, SourceURL: fact.SourceURL, QualityStatus: QualityStatusValid, FiledAt: fact.FiledAt,
		AcceptedAt: fact.AcceptedAt, CreatedAt: now,
	}
}

func financialMetricSnapshot(batchID string, securityID uint, summary FinancialSummary, flags string, now time.Time) FinancialMetricSnapshot {
	return FinancialMetricSnapshot{
		BatchID: batchID, SecurityID: securityID, ParserVersion: FinancialParserVersion,
		RevenueGrowthAvailable: summary.RevenueGrowthAvailable, RunwayAvailable: summary.RunwayAvailable, GrossMarginAvailable: summary.GrossMarginAvailable,
		LatestQuarterRevenueUSD: summary.LatestQuarterRevenueUSD, PriorYearQuarterRevenueUSD: summary.PriorYearQuarterRevenueUSD,
		PreviousQuarterRevenueUSD: summary.PreviousQuarterRevenueUSD, QuarterlyRevenueYoYPct: summary.QuarterlyRevenueYoYPct,
		QuarterlyRevenueQoQPct: summary.QuarterlyRevenueQoQPct, LatestAnnualRevenueUSD: summary.LatestAnnualRevenueUSD,
		PriorAnnualRevenueUSD: summary.PriorAnnualRevenueUSD, AnnualRevenueYoYPct: summary.AnnualRevenueYoYPct,
		AnnualRevenueQoQPct: summary.AnnualRevenueQoQPct,
		AvailableCashUSD:    summary.AvailableCashUSD, TTMOperatingCashFlowUSD: summary.TTMOperatingCashFlowUSD,
		TTMCapitalExpenditureUSD: summary.TTMCapitalExpenditureUSD, CFOBurnMonthlyUSD: summary.CFOBurnMonthlyUSD,
		FCFBurnMonthlyUSD: summary.FCFBurnMonthlyUSD, CashRunwayMonths: summary.CashRunwayMonths, GrossMarginPct: summary.GrossMarginPct,
		QualityFlagsJSON: flags, CreatedAt: now,
	}
}

func (c *Coordinator) persistInsiderSnapshots(ctx context.Context, batchID string, securityIDByCIK map[string]uint, insidersByCIK map[string][]InsiderTransaction, now time.Time) error {
	rows := make([]InsiderTransactionSnapshot, 0)
	for cik, transactions := range insidersByCIK {
		securityID, ok := securityIDByCIK[cik]
		if !ok {
			continue
		}
		for _, tx := range transactions {
			rows = append(rows, InsiderTransactionToSnapshot(securityID, tx, now))
		}
	}
	sort.Slice(rows, func(i, j int) bool { return canonicalLess(rows[i], rows[j]) })
	chunks := (len(rows) + universeChunkSize - 1) / universeChunkSize
	if chunks == 0 {
		chunks = 1
	}
	for chunkIndex := 0; chunkIndex < chunks; chunkIndex++ {
		start := chunkIndex * universeChunkSize
		end := start + universeChunkSize
		if end > len(rows) {
			end = len(rows)
		}
		chunk := append([]InsiderTransactionSnapshot(nil), rows[start:end]...)
		if err := c.runSecurityStageChunk(ctx, batchID, "insider-transactions", chunkIndex, len(chunk), func(tx *gorm.DB) error {
			if len(chunk) == 0 {
				return nil
			}
			return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&chunk).Error
		}); err != nil {
			return err
		}
	}
	return nil
}

func (c *Coordinator) persistInsiderCoverageSnapshots(ctx context.Context, batchID string, securityIDByCIK map[string]uint, coverage []InsiderCoverage, now time.Time) error {
	rows := make([]InsiderCoverageSnapshot, 0, len(coverage))
	for _, item := range coverage {
		securityID, ok := securityIDByCIK[item.CIK]
		if !ok {
			continue
		}
		checkedAt := item.CheckedAt
		if checkedAt.IsZero() {
			checkedAt = now
		}
		rows = append(rows, InsiderCoverageSnapshot{
			BatchID: batchID, SecurityID: securityID, CIK: item.CIK, Status: item.Status,
			EligibleFilings: item.EligibleFilings, DownloadedDocuments: item.DownloadedDocuments,
			ParsedDocuments: item.ParsedDocuments, TransactionCount: item.TransactionCount,
			PermanentDocumentFailures: item.PermanentDocumentFailures,
			TransientDocumentFailures: item.TransientDocumentFailures,
			MalformedDocuments:        item.MalformedDocuments, CheckedAt: checkedAt, CreatedAt: now,
		})
	}
	sort.Slice(rows, func(i, j int) bool { return canonicalLess(rows[i], rows[j]) })
	chunks := (len(rows) + universeChunkSize - 1) / universeChunkSize
	if chunks == 0 {
		chunks = 1
	}
	for chunkIndex := 0; chunkIndex < chunks; chunkIndex++ {
		start := chunkIndex * universeChunkSize
		end := start + universeChunkSize
		if end > len(rows) {
			end = len(rows)
		}
		chunk := append([]InsiderCoverageSnapshot(nil), rows[start:end]...)
		if err := c.runSecurityStageChunk(ctx, batchID, "insider-coverage", chunkIndex, len(chunk), func(tx *gorm.DB) error {
			if len(chunk) == 0 {
				return nil
			}
			return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&chunk).Error
		}); err != nil {
			return err
		}
	}
	return nil
}

func (c *Coordinator) persistCapitalRiskSnapshots(ctx context.Context, batchID string, securityIDByCIK map[string]uint, eventsByCIK map[string][]CapitalEvent, now time.Time) error {
	rows := make([]CapitalRiskSnapshot, 0)
	for cik, events := range eventsByCIK {
		securityID, ok := securityIDByCIK[cik]
		if !ok {
			continue
		}
		for _, risk := range AssessCapitalRisks(events, now) {
			rows = append(rows, CapitalRiskToSnapshot(batchID, securityID, risk, now))
		}
	}
	sort.Slice(rows, func(i, j int) bool { return canonicalLess(rows[i], rows[j]) })
	chunks := (len(rows) + universeChunkSize - 1) / universeChunkSize
	if chunks == 0 {
		chunks = 1
	}
	for chunkIndex := 0; chunkIndex < chunks; chunkIndex++ {
		start := chunkIndex * universeChunkSize
		end := start + universeChunkSize
		if end > len(rows) {
			end = len(rows)
		}
		chunk := append([]CapitalRiskSnapshot(nil), rows[start:end]...)
		if err := c.runSecurityStageChunk(ctx, batchID, "capital-risks", chunkIndex, len(chunk), func(tx *gorm.DB) error {
			if len(chunk) == 0 {
				return nil
			}
			return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&chunk).Error
		}); err != nil {
			return err
		}
	}
	return nil
}

func groupMetadata(records []SecuritySourceRecord) ([]metadataGroup, error) {
	if len(records) == 0 {
		return nil, errors.New("security metadata is empty")
	}
	byCIK := map[string][]SecuritySourceRecord{}
	identities := map[string]SecuritySourceRecord{}
	tickerCIKs := map[string]map[string]struct{}{}
	for _, record := range records {
		record.Ticker = strings.ToUpper(strings.TrimSpace(record.Ticker))
		if !validCIK(record.CIK) || record.Ticker == "" {
			return nil, errors.New("metadata contains invalid identity")
		}
		key := record.CIK + "\x00" + record.Ticker
		if prior, ok := identities[key]; ok {
			if !reflect.DeepEqual(prior, record) {
				return nil, fmt.Errorf("metadata identity %s/%s has conflicting duplicates", record.CIK, record.Ticker)
			}
			continue
		}
		identities[key] = record
		byCIK[record.CIK] = append(byCIK[record.CIK], record)
		if tickerCIKs[record.Ticker] == nil {
			tickerCIKs[record.Ticker] = map[string]struct{}{}
		}
		tickerCIKs[record.Ticker][record.CIK] = struct{}{}
	}
	ciks := make([]string, 0, len(byCIK))
	for cik := range byCIK {
		ciks = append(ciks, cik)
	}
	sort.Strings(ciks)
	groups := make([]metadataGroup, 0, len(ciks))
	for _, cik := range ciks {
		rows := byCIK[cik]
		sort.Slice(rows, func(i, j int) bool { return rows[i].Ticker < rows[j].Ticker })
		primary, attachedNonCommon := selectPrimaryCommonListing(rows)
		if len(rows) > 1 && !attachedNonCommon || len(tickerCIKs[primary.Ticker]) > 1 {
			primary.MappingStatus = MappingStatusConflict
		}
		for i := range rows {
			if len(tickerCIKs[rows[i].Ticker]) > 1 {
				primary.MappingStatus = MappingStatusConflict
			}
		}
		groups = append(groups, metadataGroup{Primary: primary, Listings: rows})
	}
	return groups, nil
}

// selectPrimaryCommonListing treats a company common share and its attached
// warrants/rights/units as one issuer, not conflicting issuer identities.
// The exchange directory provides the security name, which is safer than
// inferring a warrant solely from ticker suffixes (for example, W is itself a
// valid common-stock ticker). Any second listing that is not explicitly a
// non-common security remains a mapping conflict and is never guessed.
func selectPrimaryCommonListing(rows []SecuritySourceRecord) (SecuritySourceRecord, bool) {
	if len(rows) == 0 {
		return SecuritySourceRecord{}, false
	}
	common := make([]SecuritySourceRecord, 0, len(rows))
	for _, row := range rows {
		if !isAttachedNonCommonListing(row) {
			common = append(common, row)
		}
	}
	if len(rows) > 1 && len(common) == 1 {
		return common[0], true
	}
	return rows[0], len(rows) == 1
}

func isAttachedNonCommonListing(record SecuritySourceRecord) bool {
	return containsWholeTerm(record.SecurityName, nonCommonSecurityTerms)
}

func listingIdentitySourceKey(record SecuritySourceRecord) string {
	key := strings.ToUpper(strings.TrimSpace(record.SourceKey))
	if key != "" {
		return key
	}
	return strings.ToUpper(strings.TrimSpace(record.Ticker))
}

func validateSecurityStage(db *gorm.DB, batchID string, classifications, selections int) error {
	var c, s, i, l int64
	if err := db.Model(&ClassificationSnapshot{}).Where("batch_id = ?", batchID).Count(&c).Error; err != nil {
		return err
	}
	if err := db.Model(&BatchShareSelection{}).Where("batch_id = ?", batchID).Count(&s).Error; err != nil {
		return err
	}
	if err := db.Model(&SecurityBatchIdentity{}).Where("batch_id = ?", batchID).Count(&i).Error; err != nil {
		return err
	}
	if err := db.Model(&ListingIdentitySnapshot{}).Where("batch_id = ?", batchID).Count(&l).Error; err != nil {
		return err
	}
	if int(c) != classifications || int(s) != selections || l == 0 || c != s || c != i || c > l {
		return errors.New("security stage count validation failed")
	}
	return nil
}

func (c *Coordinator) publish(ctx context.Context, batch UniverseBatch, count int) (UniverseBatch, error) {
	now := c.Clock()
	err := c.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&UniverseBatch{}).Where("batch_id = ? AND status = ?", batch.BatchID, BatchStatusDraft).Updates(map[string]any{"status": BatchStatusPublished, "record_count": count, "completed_at": now})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return errors.New("draft batch changed before publish")
		}
		if err := persistCandidateSignalEvents(ctx, tx, batch, now); err != nil {
			return fmt.Errorf("persist candidate signal events: %w", err)
		}
		pointer := CurrentBatchPointer{Kind: batch.Kind, BatchID: batch.BatchID, UpdatedAt: now}
		return tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "kind"}}, DoUpdates: clause.AssignmentColumns([]string{"batch_id", "updated_at"})}).Create(&pointer).Error
	})
	if err != nil {
		return c.failBatch(ctx, batch, err)
	}
	return currentBatchByID(ctx, c.DB, batch.BatchID)
}

func (c *Coordinator) failBatch(ctx context.Context, batch UniverseBatch, cause error) (UniverseBatch, error) {
	if batch.BatchID != "" {
		now := c.Clock()
		backgroundDB := c.DB.WithContext(context.WithoutCancel(ctx))
		if err := backgroundDB.Model(&UniverseBatch{}).Where("batch_id = ? AND status = ?", batch.BatchID, BatchStatusDraft).Updates(map[string]any{"status": BatchStatusFailed, "completed_at": now, "error_message": cause.Error()}).Error; err != nil {
			cause = errors.Join(cause, fmt.Errorf("mark batch failed: %w", err))
		}
		if loaded, err := currentBatchByID(context.WithoutCancel(ctx), c.DB, batch.BatchID); err != nil {
			cause = errors.Join(cause, fmt.Errorf("reload failed batch: %w", err))
		} else {
			batch = loaded
		}
	}
	return batch, cause
}

func cleanupBatchRows(db *gorm.DB, model any, batchID string) error {
	for {
		var ids []uint
		if err := db.Model(model).Where("batch_id = ?", batchID).Limit(900).Pluck("id", &ids).Error; err != nil {
			return err
		}
		if len(ids) == 0 {
			return nil
		}
		if err := db.Transaction(func(tx *gorm.DB) error { return tx.Where("id IN ?", ids).Delete(model).Error }); err != nil {
			return err
		}
	}
}

func currentBatchByID(ctx context.Context, db *gorm.DB, id string) (UniverseBatch, error) {
	var b UniverseBatch
	err := db.WithContext(ctx).First(&b, "batch_id = ?", id).Error
	return b, err
}

func currentPublishedBatch(ctx context.Context, db *gorm.DB, kind string) (UniverseBatch, error) {
	var pointer CurrentBatchPointer
	if err := db.WithContext(ctx).First(&pointer, "kind = ?", kind).Error; err != nil {
		return UniverseBatch{}, fmt.Errorf("load current %s batch: %w", kind, err)
	}
	b, err := currentBatchByID(ctx, db, pointer.BatchID)
	if err != nil {
		return b, err
	}
	if b.Status != BatchStatusPublished || b.Kind != kind {
		return UniverseBatch{}, errors.New("current batch pointer is not published")
	}
	return b, nil
}

func (c *Coordinator) currentIncludedListings(ctx context.Context, batchID string) ([]Listing, error) {
	var identities []SecurityBatchIdentity
	err := c.DB.WithContext(ctx).Table("security_batch_identities i").Select("i.*").Joins("JOIN classification_snapshots c ON c.security_id = i.security_id AND c.batch_id = i.batch_id").Where("i.batch_id = ? AND c.included = ? AND c.status = ? AND i.mapping_status = ?", batchID, true, EffectiveStatusIncluded, MappingStatusCurrent).Order("i.ticker").Find(&identities).Error
	rows := make([]Listing, len(identities))
	for i, identity := range identities {
		rows[i] = Listing{SecurityID: identity.SecurityID, Ticker: identity.Ticker, ProviderTicker: identity.ProviderTicker, Exchange: identity.Exchange, MappingStatus: identity.MappingStatus}
	}
	if err != nil {
		return nil, err
	}
	binding, err := c.effectivePolicyBinding(ctx)
	if err != nil {
		return nil, err
	}
	return filterListingsForPolicy(rows, binding.Policy), nil
}

func (c *Coordinator) currentPriceRequestListings(ctx context.Context, batchID string) ([]Listing, error) {
	if !c.ResearchMode {
		return c.currentIncludedListings(ctx, batchID)
	}
	rows, err := c.currentResearchPriceListings(ctx, batchID)
	if err != nil {
		return nil, err
	}
	if len(rows) > 0 {
		return rows, nil
	}
	var metricCount int64
	if err := c.DB.WithContext(ctx).Model(&FinancialMetricSnapshot{}).Where("batch_id = ?", batchID).Count(&metricCount).Error; err != nil {
		return nil, err
	}
	if metricCount == 0 {
		return c.currentIncludedListings(ctx, batchID)
	}
	return rows, nil
}

func (c *Coordinator) currentResearchPriceListings(ctx context.Context, batchID string) ([]Listing, error) {
	binding, err := c.effectivePolicyBinding(ctx)
	if err != nil {
		return nil, err
	}
	var identities []SecurityBatchIdentity
	err = c.DB.WithContext(ctx).
		Table("security_batch_identities i").
		Select("i.*").
		Joins("JOIN classification_snapshots c ON c.security_id = i.security_id AND c.batch_id = i.batch_id").
		Joins("JOIN financial_metric_snapshots f ON f.security_id = i.security_id AND f.batch_id = i.batch_id").
		Where("i.batch_id = ? AND c.included = ? AND c.status = ? AND i.mapping_status = ?", batchID, true, EffectiveStatusIncluded, MappingStatusCurrent).
		Where("f.revenue_growth_available = ? AND (f.quarterly_revenue_yo_y_pct > ? OR f.annual_revenue_yo_y_pct > ?)", true, binding.Policy.BRevenueGrowthMinPct, binding.Policy.BRevenueGrowthMinPct).
		Where("NOT EXISTS (SELECT 1 FROM capital_risk_snapshots r WHERE r.batch_id = i.batch_id AND r.security_id = i.security_id AND r.active = ? AND r.blocks_b = ?)", true, true).
		Order("i.ticker").
		Find(&identities).Error
	rows := make([]Listing, len(identities))
	for i, identity := range identities {
		rows[i] = Listing{SecurityID: identity.SecurityID, Ticker: identity.Ticker, ProviderTicker: identity.ProviderTicker, Exchange: identity.Exchange, MappingStatus: identity.MappingStatus}
	}
	if err != nil {
		return nil, err
	}
	return filterListingsForPolicy(rows, binding.Policy), nil
}

func filterListingsForPolicy(rows []Listing, policy SmallCapPolicy) []Listing {
	filtered := make([]Listing, 0, len(rows))
	for _, row := range rows {
		if policyAllowsExchange(policy, row.Exchange) {
			filtered = append(filtered, row)
		}
	}
	return filtered
}

func (c *Coordinator) persistPrices(ctx context.Context, records []PriceRecord, version string) error {
	binding, err := c.effectivePolicyBinding(ctx)
	if err != nil {
		return err
	}
	type key struct{ source, symbol, date string }
	grouped := map[key][]PriceRecord{}
	for _, record := range records {
		grouped[key{record.Source, strings.ToUpper(record.Symbol), record.TradeDate.Format(time.RFC3339Nano)}] = append(grouped[key{record.Source, strings.ToUpper(record.Symbol), record.TradeDate.Format(time.RFC3339Nano)}], record)
	}
	snapshots := make([]PriceSnapshot, 0, len(grouped))
	for _, group := range grouped {
		sort.Slice(group, func(i, j int) bool { return canonicalLess(group[i], group[j]) })
		record, quality := group[0], QualityStatusValid
		for _, other := range group[1:] {
			if !reflect.DeepEqual(other, record) {
				quality = QualityStatusConflict
			}
		}
		if quality == QualityStatusValid {
			if _, err := ValidateMarketCapPriceWithPolicy(ctx, c.Calendar, record, c.Clock(), binding.Policy); err != nil {
				if errors.Is(err, ErrCalendarYearMissing) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
					return err
				}
				quality = qualityForPriceError(err)
			}
		}
		snapshots = append(snapshots, PriceSnapshot{Source: record.Source, SourceVersion: version, Symbol: record.Symbol, TradeDate: record.TradeDate, OpenMicros: record.OpenMicros, HighMicros: record.HighMicros, LowMicros: record.LowMicros, CloseMicros: record.CloseMicros, Volume: record.Volume, Currency: record.Currency, Adjusted: record.Adjusted, QualityStatus: quality, CreatedAt: c.Clock()})
	}
	sort.Slice(snapshots, func(i, j int) bool { return canonicalLess(snapshots[i], snapshots[j]) })
	for start := 0; start < len(snapshots); start += universeChunkSize {
		end := start + universeChunkSize
		if end > len(snapshots) {
			end = len(snapshots)
		}
		if err := c.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			_, persistErr := persistPriceSnapshotsWithQuarantine(tx, snapshots[start:end], c.Clock())
			return persistErr
		}); err != nil {
			return err
		}
		if c.AfterStageChunk != nil {
			if err := c.AfterStageChunk("price-snapshots", start/universeChunkSize); err != nil {
				return err
			}
		}
	}
	return nil
}

func (c *Coordinator) buildUniverseSnapshots(ctx context.Context, securityBatchID, marketBatchID string, prices []PriceRecord, result ProviderResult, now time.Time) ([]UniverseSnapshot, error) {
	binding, err := c.effectivePolicyBinding(ctx)
	if err != nil {
		return nil, err
	}
	var classifications []ClassificationSnapshot
	if err := c.DB.WithContext(ctx).Where("batch_id = ?", securityBatchID).Order("security_id").Find(&classifications).Error; err != nil {
		return nil, err
	}
	var selections []BatchShareSelection
	if err := c.DB.WithContext(ctx).Where("batch_id = ?", securityBatchID).Find(&selections).Error; err != nil {
		return nil, err
	}
	selectionBySecurity := map[uint]BatchShareSelection{}
	for _, s := range selections {
		selectionBySecurity[s.SecurityID] = s
	}
	var identities []SecurityBatchIdentity
	if err := c.DB.WithContext(ctx).Where("batch_id = ?", securityBatchID).Order("ticker").Find(&identities).Error; err != nil {
		return nil, err
	}
	listingBySecurity := map[uint]SecurityBatchIdentity{}
	for _, l := range identities {
		if _, ok := listingBySecurity[l.SecurityID]; !ok {
			listingBySecurity[l.SecurityID] = l
		}
	}
	priceBySymbol := map[string][]PriceRecord{}
	for _, p := range prices {
		priceBySymbol[strings.ToUpper(p.Symbol)] = append(priceBySymbol[strings.ToUpper(p.Symbol)], p)
	}
	output := make([]UniverseSnapshot, 0, len(classifications))
	for _, classification := range classifications {
		listing := listingBySecurity[classification.SecurityID]
		snapshot := UniverseSnapshot{BatchID: marketBatchID, SecurityID: classification.SecurityID, Ticker: listing.Ticker, Included: false, Status: EffectiveStatusDataInsufficient, QualityStatus: QualityStatusMissing, ReasonCode: ReasonClassificationData, CreatedAt: now}
		if !classification.Included || classification.Status != EffectiveStatusIncluded {
			snapshot.Status = classification.Status
			snapshot.ReasonCode = classification.ReasonCode
			output = append(output, snapshot)
			continue
		}
		if listing.ID == 0 || listing.MappingStatus != MappingStatusCurrent {
			snapshot.QualityStatus = QualityStatusConflict
			snapshot.ReasonCode = ReasonMappingConflict
			output = append(output, snapshot)
			continue
		}
		if !policyAllowsExchange(binding.Policy, listing.Exchange) {
			snapshot.Status = EffectiveStatusExcluded
			snapshot.QualityStatus = QualityStatusValid
			snapshot.ReasonCode = ReasonExchangeNotAllowed
			output = append(output, snapshot)
			continue
		}
		selection := selectionBySecurity[classification.SecurityID]
		snapshot.ShareSnapshotID = selection.ShareSnapshotID
		if selection.QualityStatus != QualityStatusValid || selection.ShareSnapshotID == nil {
			snapshot.QualityStatus = selection.QualityStatus
			if snapshot.QualityStatus == "" {
				snapshot.QualityStatus = QualityStatusMissing
			}
			snapshot.ReasonCode = selection.ReasonCode
			if snapshot.ReasonCode == "" {
				snapshot.ReasonCode = ReasonShareFactMissing
			}
			output = append(output, snapshot)
			continue
		}
		candidates := priceBySymbol[strings.ToUpper(listing.ProviderTicker)]
		if len(candidates) == 0 {
			candidates = priceBySymbol[strings.ToUpper(listing.Ticker)]
		}
		if len(candidates) == 0 {
			snapshot.ReasonCode = ReasonPriceMissing
			output = append(output, snapshot)
			continue
		}
		sort.Slice(candidates, func(i, j int) bool {
			if !candidates[i].TradeDate.Equal(candidates[j].TradeDate) {
				return candidates[i].TradeDate.After(candidates[j].TradeDate)
			}
			return canonicalLess(candidates[i], candidates[j])
		})
		price := candidates[0]
		var priceSnapshot PriceSnapshot
		if err := c.DB.WithContext(ctx).Where("source = ? AND source_version = ? AND symbol = ? AND trade_date = ?", price.Source, result.SourceVersion, price.Symbol, price.TradeDate).First(&priceSnapshot).Error; err != nil {
			return nil, err
		}
		snapshot.PriceSnapshotID = &priceSnapshot.ID
		conflict := false
		for _, candidate := range candidates[1:] {
			if candidate.TradeDate.Equal(price.TradeDate) && !reflect.DeepEqual(candidate, price) {
				conflict = true
				break
			}
		}
		if conflict {
			snapshot.QualityStatus = QualityStatusConflict
			snapshot.ReasonCode = ReasonPriceConflict
			output = append(output, snapshot)
			continue
		}
		if _, err := ValidateMarketCapPriceWithPolicy(ctx, c.Calendar, price, now, binding.Policy); err != nil {
			if errors.Is(err, ErrCalendarYearMissing) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return nil, err
			}
			snapshot.QualityStatus = qualityForPriceError(err)
			snapshot.ReasonCode = reasonForPriceError(err)
			output = append(output, snapshot)
			continue
		}
		var share ShareSnapshot
		if err := c.DB.WithContext(ctx).First(&share, *selection.ShareSnapshotID).Error; err != nil {
			return nil, err
		}
		capUSD, qualified, err := ComputeSmallCapQualificationWithPolicy(price.CloseMicros, share.Shares, binding.Policy)
		if err != nil {
			snapshot.QualityStatus = QualityStatusConflict
			snapshot.ReasonCode = ReasonMarketCapOverflow
			output = append(output, snapshot)
			continue
		}
		snapshot.MarketCapUSD = capUSD
		snapshot.QualityStatus = QualityStatusValid
		if qualified {
			snapshot.Included = true
			snapshot.Status = EffectiveStatusPrescreen
			snapshot.ReasonCode = ReasonQualifiedSmallCap
		} else {
			snapshot.Status = EffectiveStatusExcluded
			snapshot.ReasonCode = ReasonOutsideMarketCap
		}
		output = append(output, snapshot)
	}
	return output, nil
}

func qualityForPriceError(err error) string {
	if errors.Is(err, ErrPriceStale) {
		return QualityStatusStale
	}
	return QualityStatusMissing
}

func reasonForPriceError(err error) string {
	switch {
	case errors.Is(err, ErrPriceStale):
		return ReasonPriceStale
	case errors.Is(err, ErrPriceFuture):
		return ReasonPriceFuture
	case errors.Is(err, ErrPriceNotTradingDay):
		return ReasonPriceNonTrading
	case errors.Is(err, ErrPriceAdjusted):
		return ReasonPriceAdjusted
	case errors.Is(err, ErrPriceCurrency):
		return ReasonPriceCurrency
	case errors.Is(err, ErrPriceZero):
		return ReasonPriceZero
	case errors.Is(err, ErrPriceNegative):
		return ReasonPriceNegative
	default:
		return ReasonPriceMissing
	}
}

func (c *Coordinator) persistUniverseSnapshots(ctx context.Context, rows []UniverseSnapshot) error {
	for start := 0; start < len(rows); start += universeChunkSize {
		end := start + universeChunkSize
		if end > len(rows) {
			end = len(rows)
		}
		if err := c.DB.WithContext(ctx).CreateInBatches(rows[start:end], universeChunkSize).Error; err != nil {
			return err
		}
		if c.AfterStageChunk != nil {
			if err := c.AfterStageChunk(BatchKindPrescreen, start/universeChunkSize); err != nil {
				return err
			}
		}
	}
	return nil
}

func (c *Coordinator) persistCandidateScoreSnapshots(ctx context.Context, securityBatchID, marketBatchID string, universeRows []UniverseSnapshot, now time.Time) error {
	binding, err := c.effectivePolicyBinding(ctx)
	if err != nil {
		return err
	}
	securityIDs := make([]uint, 0, len(universeRows))
	seenSecurity := map[uint]struct{}{}
	for _, row := range universeRows {
		if row.SecurityID == 0 {
			continue
		}
		if _, ok := seenSecurity[row.SecurityID]; ok {
			continue
		}
		seenSecurity[row.SecurityID] = struct{}{}
		securityIDs = append(securityIDs, row.SecurityID)
	}
	if len(securityIDs) == 0 {
		return nil
	}
	var metrics []FinancialMetricSnapshot
	if err := c.DB.WithContext(ctx).Where("batch_id = ? AND security_id IN ?", securityBatchID, securityIDs).Find(&metrics).Error; err != nil {
		return err
	}
	metricBySecurity := map[uint]FinancialMetricSnapshot{}
	for _, metric := range metrics {
		metricBySecurity[metric.SecurityID] = metric
	}
	var insiders []InsiderTransactionSnapshot
	if err := c.DB.WithContext(ctx).Where("security_id IN ?", securityIDs).Find(&insiders).Error; err != nil {
		return err
	}
	insidersBySecurity := map[uint][]InsiderTransactionSnapshot{}
	for _, insider := range insiders {
		insidersBySecurity[insider.SecurityID] = append(insidersBySecurity[insider.SecurityID], insider)
	}
	var risks []CapitalRiskSnapshot
	if err := c.DB.WithContext(ctx).Where("batch_id = ? AND security_id IN ?", securityBatchID, securityIDs).Find(&risks).Error; err != nil {
		return err
	}
	risksBySecurity := map[uint][]CapitalRiskSnapshot{}
	for _, risk := range risks {
		risksBySecurity[risk.SecurityID] = append(risksBySecurity[risk.SecurityID], risk)
	}
	var identities []SecurityBatchIdentity
	if err := c.DB.WithContext(ctx).Where("batch_id = ? AND security_id IN ?", securityBatchID, securityIDs).Find(&identities).Error; err != nil {
		return err
	}
	identityBySecurity := map[uint]SecurityBatchIdentity{}
	for _, identity := range identities {
		identityBySecurity[identity.SecurityID] = identity
	}
	businessModels, err := activeCandidateBusinessModels(ctx, c.DB, securityIDs)
	if err != nil {
		return err
	}
	rows := make([]CandidateScoreSnapshot, 0, len(universeRows))
	for _, snapshot := range universeRows {
		if snapshot.SecurityID == 0 || snapshot.QualityStatus != QualityStatusValid || snapshot.MarketCapUSD <= 0 {
			continue
		}
		sectorRating := SectorRatingForSIC(identityBySecurity[snapshot.SecurityID].SIC)
		var override *CandidateBusinessModelOverride
		if row, ok := businessModels[snapshot.SecurityID]; ok {
			override = &row
		}
		score := ScoreDiscoveryCandidateWithPolicy(DiscoveryScoreInput{
			SecurityID: snapshot.SecurityID, Ticker: snapshot.Ticker, MarketCapUSD: snapshot.MarketCapUSD,
			Financial: metricBySecurity[snapshot.SecurityID], Insiders: insidersBySecurity[snapshot.SecurityID],
			Risks: risksBySecurity[snapshot.SecurityID], GrossMarginPct: metricBySecurity[snapshot.SecurityID].GrossMarginPct, SectorScore: sectorRating.Score,
			BusinessModel: candidateBusinessModelEvidence(override, sectorRating.Category == "生物医药"), AsOf: now,
		}, binding.Policy)
		rows = append(rows, CandidateScoreToSnapshot(marketBatchID, score, now))
	}
	sort.Slice(rows, func(i, j int) bool { return canonicalLess(rows[i], rows[j]) })
	for start := 0; start < len(rows); start += universeChunkSize {
		end := start + universeChunkSize
		if end > len(rows) {
			end = len(rows)
		}
		chunk := rows[start:end]
		if err := c.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			return tx.Create(&chunk).Error
		}); err != nil {
			return err
		}
		if c.AfterStageChunk != nil {
			if err := c.AfterStageChunk("candidate-scores", start/universeChunkSize); err != nil {
				return err
			}
		}
	}
	return nil
}

func (c *Coordinator) persistProviderRunDB(db *gorm.DB, batchID string, result ProviderResult, day ProviderDayResult, status string) error {
	attempts := providerAttemptsForResult(result)
	attemptsJSON, err := encodeProviderAttempts(attempts)
	if err != nil {
		return err
	}
	run := ProviderRun{BatchID: batchID, Provider: result.Provider, Status: status, SourceVersion: result.SourceVersion, SHA256: result.SHA256, EffectiveDate: result.EffectiveDate, RecordCount: result.Records, ExpectedCount: result.Expected, CoveragePct: result.CoveragePct, ValidationErrorPct: result.ValidationErrorPct, Timely: result.Timely, GoldSHA256: day.goldSHA256, AttemptsJSON: attemptsJSON, Attempts: attempts, FallbackUsed: result.FallbackUsed, CreatedAt: c.Clock()}
	return db.Create(&run).Error
}

func validateMarketStage(db *gorm.DB, batchID string, want int) error {
	var count int64
	if err := db.Model(&UniverseSnapshot{}).Where("batch_id = ?", batchID).Count(&count).Error; err != nil {
		return err
	}
	if int(count) != want || count == 0 {
		return errors.New("market stage count validation failed")
	}
	return nil
}
