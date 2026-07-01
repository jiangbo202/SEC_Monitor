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
	BatchKindSecurity  = "security-universe"
	BatchKindPrescreen = "market-prescreen"
	universeChunkSize  = 1000
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
	// ResearchMode allows publishing auditable research batches while a price
	// provider is still in validation/degraded state. It is intentionally scoped
	// to discovery output and does not promote the provider health state.
	ResearchMode bool
	// AfterStageChunk is a test/operations fault-injection hook. It runs only
	// after a chunk transaction commits and before the next chunk begins.
	AfterStageChunk      func(kind string, chunk int) error
	providerDayEvaluator func(ProviderResult, []PriceRecord, time.Time) (ProviderDayResult, error)
}

type metadataGroup struct {
	Primary  SecuritySourceRecord
	Listings []SecuritySourceRecord
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
	records, metadataVersion, err := c.Metadata.Load(ctx)
	if err != nil {
		return c.recordEarlyFailure(ctx, BatchKindSecurity, now, "metadata", fmt.Errorf("load security metadata: %w", err))
	}
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
	facts, shareVersion, err := c.Shares.LoadLatestShares(ctx, allowed)
	if err != nil {
		return c.recordEarlyFailure(ctx, BatchKindSecurity, now, "shares", fmt.Errorf("load share facts: %w", err))
	}
	var financialFacts []FinancialFact
	var financialVersion SourceVersion
	if c.Financials != nil {
		financialFacts, financialVersion, err = c.Financials.LoadFinancialFacts(ctx, allowed)
		if err != nil {
			return c.recordEarlyFailure(ctx, BatchKindSecurity, now, "financials", fmt.Errorf("load financial facts: %w", err))
		}
	}
	var insiderTransactions []InsiderTransaction
	var insiderVersion SourceVersion
	if c.Insiders != nil {
		insiderTransactions, insiderVersion, err = c.Insiders.LoadInsiderTransactions(ctx, allowed, now)
		if err != nil {
			return c.recordEarlyFailure(ctx, BatchKindSecurity, now, "insiders", fmt.Errorf("load insider transactions: %w", err))
		}
	}
	events, eventVersion, err := c.Events.Load(ctx, allowed, now)
	if err != nil {
		return c.recordEarlyFailure(ctx, BatchKindSecurity, now, "capital-events", fmt.Errorf("load capital events: %w", err))
	}
	facts, err = normalizeShareFacts(facts)
	if err != nil {
		return c.recordEarlyFailure(ctx, BatchKindSecurity, now, "share-normalization", err)
	}
	financialFacts, err = normalizeFinancialFacts(financialFacts)
	if err != nil {
		return c.recordEarlyFailure(ctx, BatchKindSecurity, now, "financial-normalization", err)
	}
	insiderTransactions, err = normalizeInsiderTransactions(insiderTransactions)
	if err != nil {
		return c.recordEarlyFailure(ctx, BatchKindSecurity, now, "insider-normalization", err)
	}
	events, err = normalizeCapitalEvents(events)
	if err != nil {
		return c.recordEarlyFailure(ctx, BatchKindSecurity, now, "event-normalization", err)
	}
	sourceVersions := []SourceVersion{metadataVersion, shareVersion, eventVersion}
	if c.Financials != nil {
		sourceVersions = append(sourceVersions, financialVersion)
	}
	if c.Insiders != nil {
		sourceVersions = append(sourceVersions, insiderVersion)
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
	versions, err = normalizeSourceVersions(date, sourceVersions...)
	if err != nil {
		return c.recordEarlyFailure(ctx, BatchKindSecurity, now, "source-versions", err)
	}
	contentSHA, err := hashSecurityInputs(records, facts, financialFacts, insiderTransactions, events, overrides)
	if err != nil {
		return UniverseBatch{}, err
	}
	batch, existed, err := c.createDraft(ctx, BatchKindSecurity, date, versions, contentSHA, now)
	if err != nil || existed {
		return batch, err
	}

	classifications, selections, stageErr := c.stageSecurity(ctx, batch, records, facts, financialFacts, insiderTransactions, events, overrides, now)
	if stageErr == nil {
		stageErr = validateSecurityStage(c.DB.WithContext(ctx), batch.BatchID, classifications, selections)
	}
	if stageErr != nil {
		return c.failBatch(ctx, batch, stageErr)
	}
	return c.publish(ctx, batch, classifications)
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
	date, err := nyCivilDate(now)
	if err != nil {
		return UniverseBatch{}, err
	}
	if c.ResearchMode {
		date = securityBatch.EffectiveDate
	} else if securityBatch.EffectiveDate != date {
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
	trading, calendarErr := c.Calendar.IsTradingDate(ctx, date)
	if calendarErr != nil || !trading {
		if calendarErr == nil {
			calendarErr = fmt.Errorf("%s is not a trading date", date)
		}
		cause := fmt.Errorf("validate price run calendar: %w", calendarErr)
		return c.recordPreflightFailure(ctx, BatchKindPrescreen, date, securityBatch, providerName, "calendar-invalid", now, cause)
	}

	expected, err := c.currentPriceRequestListings(ctx, securityBatch.BatchID)
	if err != nil {
		return c.recordPreflightFailure(ctx, BatchKindPrescreen, date, securityBatch, providerName, "security-stage-invalid", now, err)
	}
	var records []PriceRecord
	var result ProviderResult
	if c.ResearchMode && len(expected) == 0 {
		result, err = emptyResearchProviderResult(providerName, securityBatch, now)
	} else if dated, ok := c.Prices.(DatedPriceProvider); ok {
		records, result, err = dated.LoadForDate(ctx, expected, securityBatch.EffectiveDate)
	} else {
		records, result, err = c.Prices.Load(ctx, expected)
	}
	if err != nil {
		cause := fmt.Errorf("load market prices: %w", err)
		return c.recordPreflightFailure(ctx, BatchKindPrescreen, date, securityBatch, providerName, "load-failed", now, cause)
	}
	if result.Provider != providerName {
		cause := errors.New("price provider identity changed during load")
		return c.recordPreflightFailure(ctx, BatchKindPrescreen, date, securityBatch, providerName, "identity-invalid", now, cause)
	}
	for _, record := range records {
		if record.Source != providerName || strings.TrimSpace(record.Symbol) == "" {
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

func emptyResearchProviderResult(provider string, securityBatch UniverseBatch, now time.Time) (ProviderResult, error) {
	date := strings.TrimSpace(securityBatch.EffectiveDate)
	if date == "" {
		var err error
		date, err = nyCivilDate(now)
		if err != nil {
			return ProviderResult{}, err
		}
	}
	effectiveDate, err := parseNYCivilDate(date)
	if err != nil {
		return ProviderResult{}, err
	}
	seed := provider + "\x00research-empty\x00" + securityBatch.BatchID + "\x00" + date
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

func batchIdentity(kind, date string, versions []SourceVersion) (string, string, error) {
	encoded, err := json.Marshal(versions)
	if err != nil {
		return "", "", err
	}
	digest := sha256.Sum256(append([]byte(kind+"\n"+date+"\n"), encoded...))
	return hex.EncodeToString(digest[:]), string(encoded), nil
}

func (c *Coordinator) createDraft(ctx context.Context, kind, date string, versions []SourceVersion, contentSHA string, now time.Time) (UniverseBatch, bool, error) {
	if !validSHA256(contentSHA) {
		return UniverseBatch{}, false, errors.New("batch content SHA256 is required")
	}
	id, encoded, err := batchIdentity(kind, date, versions)
	if err != nil {
		return UniverseBatch{}, false, err
	}
	batch := UniverseBatch{BatchID: id, Kind: kind, Status: BatchStatusDraft, EffectiveDate: date, SourceVersionsJSON: encoded, ContentSHA256: contentSHA, StartedAt: now}
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
		if err := c.resetRetryableBatch(ctx, batch); err != nil {
			return UniverseBatch{}, false, err
		}
		retry, err := currentBatchByID(ctx, c.DB, id)
		return retry, false, err
	}
	return existing, true, fmt.Errorf("batch %s already exists with status %s", id, existing.Status)
}

func (c *Coordinator) resetRetryableBatch(ctx context.Context, batch UniverseBatch) error {
	return c.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, model := range []any{
			&UniverseSnapshot{},
			&CandidateScoreSnapshot{},
			&ProviderRun{},
			&SocialHeatSnapshot{},
			&CapitalRiskSnapshot{},
			&FinancialMetricSnapshot{},
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
			"kind":                    batch.Kind,
			"status":                  BatchStatusDraft,
			"effective_date":          batch.EffectiveDate,
			"source_versions_json":    batch.SourceVersionsJSON,
			"content_sha256":          batch.ContentSHA256,
			"record_count":            0,
			"universe_source_version": batch.UniverseSourceVersion,
			"price_source_version":    batch.PriceSourceVersion,
			"share_source_version":    batch.ShareSourceVersion,
			"started_at":              batch.StartedAt,
			"completed_at":            nil,
			"error_message":           "",
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

func hashSecurityInputs(records []SecuritySourceRecord, facts []ShareFact, financials []FinancialFact, insiders []InsiderTransaction, events []CapitalEvent, overrides []ManualSecurityOverride) (string, error) {
	recordCopy := append([]SecuritySourceRecord(nil), records...)
	sort.Slice(recordCopy, func(i, j int) bool { return canonicalLess(recordCopy[i], recordCopy[j]) })
	factCopy := append([]ShareFact(nil), facts...)
	sort.Slice(factCopy, func(i, j int) bool { return canonicalLess(factCopy[i], factCopy[j]) })
	financialCopy := append([]FinancialFact(nil), financials...)
	sort.Slice(financialCopy, func(i, j int) bool { return canonicalLess(financialCopy[i], financialCopy[j]) })
	insiderCopy := append([]InsiderTransaction(nil), insiders...)
	sort.Slice(insiderCopy, func(i, j int) bool { return canonicalLess(insiderCopy[i], insiderCopy[j]) })
	eventCopy := append([]CapitalEvent(nil), events...)
	sort.Slice(eventCopy, func(i, j int) bool { return canonicalLess(eventCopy[i], eventCopy[j]) })
	overrideCopy := canonicalManualOverrides(overrides)
	return hashCanonicalContent(struct {
		Records    []SecuritySourceRecord    `json:"records"`
		Facts      []ShareFact               `json:"facts"`
		Financials []FinancialFact           `json:"financials"`
		Insiders   []InsiderTransaction      `json:"insiders"`
		Events     []CapitalEvent            `json:"events"`
		Overrides  []canonicalManualOverride `json:"overrides"`
	}{recordCopy, factCopy, financialCopy, insiderCopy, eventCopy, overrideCopy})
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
	seen := map[string]FinancialFact{}
	for _, row := range input {
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
	sort.Slice(result, func(i, j int) bool { return canonicalLess(result[i], result[j]) })
	return result, nil
}

func normalizeInsiderTransactions(input []InsiderTransaction) ([]InsiderTransaction, error) {
	seen := map[string]InsiderTransaction{}
	for _, row := range input {
		key := strings.Join([]string{row.CIK, row.Accession, row.OwnerName, row.TransactionDate.UTC().Format(time.RFC3339Nano), row.TransactionCode, row.AcquiredDisposedCode, fmt.Sprintf("%f", row.Shares), fmt.Sprintf("%f", row.PricePerShareUSD), fmt.Sprintf("%t", row.Derivative)}, "\x00")
		if prior, ok := seen[key]; ok {
			if !reflect.DeepEqual(prior, row) {
				return nil, fmt.Errorf("insider transaction identity has conflicting duplicates: %s", row.Accession)
			}
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

func (c *Coordinator) stageSecurity(ctx context.Context, batch UniverseBatch, records []SecuritySourceRecord, facts []ShareFact, financialFacts []FinancialFact, insiderTransactions []InsiderTransaction, events []CapitalEvent, overrides []ManualSecurityOverride, now time.Time) (int, int, error) {
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
		if err := c.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			for i := start; i < end; i++ {
				if err := tx.Create(&listingRows[i]).Error; err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			return 0, 0, err
		}
		if c.AfterStageChunk != nil {
			if err := c.AfterStageChunk("security-listings", start/universeChunkSize); err != nil {
				return 0, 0, err
			}
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
	classifications, selections := 0, 0
	type listingClassification struct {
		included       bool
		status, reason string
	}
	classificationByCIK := make(map[string]listingClassification, len(groups))
	securityIDByCIK := make(map[string]uint, len(groups))
	// Each group can write up to five rows (security, identity,
	// classification, share evidence, selection). Keep transactions below the
	// hard 1,000-row budget even for an all-new fixture.
	const groupsPerTransaction = 190
	for start := 0; start < len(groups); start += groupsPerTransaction {
		end := start + groupsPerTransaction
		if end > len(groups) {
			end = len(groups)
		}
		err := c.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			for _, group := range groups[start:end] {
				source := group.Primary
				if err := ctx.Err(); err != nil {
					return err
				}
				security := Security{CIK: source.CIK}
				if err := tx.Where("cik = ?", source.CIK).Attrs(Security{CatalogStatus: SecurityCatalogStaged, CreatedBatchID: batch.BatchID}).FirstOrCreate(&security).Error; err != nil {
					return err
				}
				securityIDByCIK[source.CIK] = security.ID
				source.SecurityID = security.ID
				if source.MappingStatus == "" {
					source.MappingStatus = MappingStatusCurrent
				}
				providerTicker := strings.ToUpper(strings.TrimSpace(source.ProviderTicker))
				if providerTicker == "" {
					providerTicker = strings.ToUpper(strings.TrimSpace(source.Ticker))
				}
				identity := SecurityBatchIdentity{BatchID: batch.BatchID, SecurityID: security.ID, CIK: source.CIK, Ticker: strings.ToUpper(strings.TrimSpace(source.Ticker)), ProviderTicker: providerTicker, Exchange: source.Exchange, MappingStatus: source.MappingStatus, CompanyName: source.CompanyName, SIC: source.SIC, StateOfIncorporation: source.StateOfIncorporation, LatestAnnualForm: source.LatestAnnualForm, CreatedAt: now}
				if err := tx.Create(&identity).Error; err != nil {
					return err
				}
				classification := ClassifySecurity(source, overrides)
				classificationByCIK[source.CIK] = listingClassification{classification.Included, classification.Status, classification.ReasonCode}
				evidence, _ := json.Marshal(classification.Evidence)
				row := ClassificationSnapshot{BatchID: batch.BatchID, SecurityID: security.ID, Included: classification.Included, Status: classification.Status, Confidence: classification.Confidence, ReasonCode: classification.ReasonCode, RuleVersion: ClassificationRuleVersion, EvidenceJSON: string(evidence), CreatedAt: now}
				if err := tx.Create(&row).Error; err != nil {
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
				if err := tx.Create(&binding).Error; err != nil {
					return err
				}
				classifications++
				selections++
			}
			return nil
		})
		if err != nil {
			return classifications, selections, err
		}
		if c.AfterStageChunk != nil {
			if err := c.AfterStageChunk(BatchKindSecurity, start/groupsPerTransaction); err != nil {
				return classifications, selections, err
			}
		}
	}
	for start := 0; start < len(listingRows); start += universeChunkSize {
		end := start + universeChunkSize
		if end > len(listingRows) {
			end = len(listingRows)
		}
		if err := c.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			for _, row := range listingRows[start:end] {
				classification, ok := classificationByCIK[row.CIK]
				if !ok {
					continue
				}
				if err := tx.Model(&ListingIdentitySnapshot{}).Where("batch_id = ? AND source_key = ?", batch.BatchID, row.SourceKey).Updates(map[string]any{"included": classification.included, "status": classification.status, "reason_code": classification.reason}).Error; err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			return classifications, selections, err
		}
	}
	if len(financialsByCIK) > 0 {
		if err := c.persistFinancialSnapshots(ctx, batch.BatchID, securityIDByCIK, financialsByCIK, now); err != nil {
			return classifications, selections, err
		}
	}
	if len(insidersByCIK) > 0 {
		if err := c.persistInsiderSnapshots(ctx, securityIDByCIK, insidersByCIK, now); err != nil {
			return classifications, selections, err
		}
	}
	if len(eventsByCIK) > 0 {
		if err := c.persistCapitalRiskSnapshots(ctx, batch.BatchID, securityIDByCIK, eventsByCIK, now); err != nil {
			return classifications, selections, err
		}
	}
	return classifications, selections, nil
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
		summary := BuildFinancialSummary(facts, now)
		flags, err := json.Marshal(summary.QualityFlags)
		if err != nil {
			return err
		}
		rows.Metrics = append(rows.Metrics, financialMetricSnapshot(batchID, securityID, summary, string(flags), now))
	}
	sort.Slice(rows.Facts, func(i, j int) bool { return canonicalLess(rows.Facts[i], rows.Facts[j]) })
	sort.Slice(rows.Metrics, func(i, j int) bool { return canonicalLess(rows.Metrics[i], rows.Metrics[j]) })
	for start := 0; start < len(rows.Facts); start += universeChunkSize {
		end := start + universeChunkSize
		if end > len(rows.Facts) {
			end = len(rows.Facts)
		}
		chunk := rows.Facts[start:end]
		if err := c.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&chunk).Error
		}); err != nil {
			return err
		}
		if c.AfterStageChunk != nil {
			if err := c.AfterStageChunk("financial-facts", start/universeChunkSize); err != nil {
				return err
			}
		}
	}
	for start := 0; start < len(rows.Metrics); start += universeChunkSize {
		end := start + universeChunkSize
		if end > len(rows.Metrics) {
			end = len(rows.Metrics)
		}
		chunk := rows.Metrics[start:end]
		if err := c.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			return tx.Create(&chunk).Error
		}); err != nil {
			return err
		}
		if c.AfterStageChunk != nil {
			if err := c.AfterStageChunk("financial-metrics", start/universeChunkSize); err != nil {
				return err
			}
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
		RevenueGrowthAvailable: summary.RevenueGrowthAvailable, RunwayAvailable: summary.RunwayAvailable,
		LatestQuarterRevenueUSD: summary.LatestQuarterRevenueUSD, PriorYearQuarterRevenueUSD: summary.PriorYearQuarterRevenueUSD,
		QuarterlyRevenueYoYPct: summary.QuarterlyRevenueYoYPct, LatestAnnualRevenueUSD: summary.LatestAnnualRevenueUSD,
		PriorAnnualRevenueUSD: summary.PriorAnnualRevenueUSD, AnnualRevenueYoYPct: summary.AnnualRevenueYoYPct,
		AvailableCashUSD: summary.AvailableCashUSD, TTMOperatingCashFlowUSD: summary.TTMOperatingCashFlowUSD,
		TTMCapitalExpenditureUSD: summary.TTMCapitalExpenditureUSD, CFOBurnMonthlyUSD: summary.CFOBurnMonthlyUSD,
		FCFBurnMonthlyUSD: summary.FCFBurnMonthlyUSD, CashRunwayMonths: summary.CashRunwayMonths,
		QualityFlagsJSON: flags, CreatedAt: now,
	}
}

func (c *Coordinator) persistInsiderSnapshots(ctx context.Context, securityIDByCIK map[string]uint, insidersByCIK map[string][]InsiderTransaction, now time.Time) error {
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
	for start := 0; start < len(rows); start += universeChunkSize {
		end := start + universeChunkSize
		if end > len(rows) {
			end = len(rows)
		}
		chunk := rows[start:end]
		if err := c.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&chunk).Error
		}); err != nil {
			return err
		}
		if c.AfterStageChunk != nil {
			if err := c.AfterStageChunk("insider-transactions", start/universeChunkSize); err != nil {
				return err
			}
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
			if err := c.AfterStageChunk("capital-risks", start/universeChunkSize); err != nil {
				return err
			}
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
		primary := rows[0]
		if len(rows) > 1 || len(tickerCIKs[primary.Ticker]) > 1 {
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
	return rows, err
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
	var identities []SecurityBatchIdentity
	err := c.DB.WithContext(ctx).
		Table("security_batch_identities i").
		Select("i.*").
		Joins("JOIN classification_snapshots c ON c.security_id = i.security_id AND c.batch_id = i.batch_id").
		Joins("JOIN financial_metric_snapshots f ON f.security_id = i.security_id AND f.batch_id = i.batch_id").
		Where("i.batch_id = ? AND c.included = ? AND c.status = ? AND i.mapping_status = ?", batchID, true, EffectiveStatusIncluded, MappingStatusCurrent).
		Where("f.revenue_growth_available = ? AND (f.quarterly_revenue_yo_y_pct > ? OR f.annual_revenue_yo_y_pct > ?)", true, 20.0, 20.0).
		Where("NOT EXISTS (SELECT 1 FROM capital_risk_snapshots r WHERE r.batch_id = i.batch_id AND r.security_id = i.security_id AND r.active = ? AND r.blocks_b = ?)", true, true).
		Order("i.ticker").
		Find(&identities).Error
	rows := make([]Listing, len(identities))
	for i, identity := range identities {
		rows[i] = Listing{SecurityID: identity.SecurityID, Ticker: identity.Ticker, ProviderTicker: identity.ProviderTicker, Exchange: identity.Exchange, MappingStatus: identity.MappingStatus}
	}
	return rows, err
}

func (c *Coordinator) persistPrices(ctx context.Context, records []PriceRecord, version string) error {
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
			if _, err := ValidateMarketCapPrice(ctx, c.Calendar, record, c.Clock()); err != nil {
				if errors.Is(err, ErrCalendarYearMissing) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
					return err
				}
				quality = qualityForPriceError(err)
			}
		}
		snapshots = append(snapshots, PriceSnapshot{Source: record.Source, SourceVersion: version, Symbol: record.Symbol, TradeDate: record.TradeDate, CloseMicros: record.CloseMicros, Volume: record.Volume, Currency: record.Currency, Adjusted: record.Adjusted, QualityStatus: quality, CreatedAt: c.Clock()})
	}
	sort.Slice(snapshots, func(i, j int) bool { return canonicalLess(snapshots[i], snapshots[j]) })
	for start := 0; start < len(snapshots); start += universeChunkSize {
		end := start + universeChunkSize
		if end > len(snapshots) {
			end = len(snapshots)
		}
		if err := c.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error { return persistPriceSnapshotsInBatches(tx, snapshots[start:end]) }); err != nil {
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
		if _, err := ValidateMarketCapPrice(ctx, c.Calendar, price, now); err != nil {
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
		capUSD, qualified, err := ComputeSmallCapQualification(price.CloseMicros, share.Shares)
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
	rows := make([]CandidateScoreSnapshot, 0, len(universeRows))
	for _, snapshot := range universeRows {
		if snapshot.SecurityID == 0 || snapshot.QualityStatus != QualityStatusValid || snapshot.MarketCapUSD <= 0 {
			continue
		}
		score := ScoreDiscoveryCandidate(DiscoveryScoreInput{
			SecurityID: snapshot.SecurityID, Ticker: snapshot.Ticker, MarketCapUSD: snapshot.MarketCapUSD,
			Financial: metricBySecurity[snapshot.SecurityID], Insiders: insidersBySecurity[snapshot.SecurityID],
			Risks: risksBySecurity[snapshot.SecurityID], AsOf: now,
		})
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
	run := ProviderRun{BatchID: batchID, Provider: result.Provider, Status: status, SourceVersion: result.SourceVersion, SHA256: result.SHA256, EffectiveDate: result.EffectiveDate, RecordCount: result.Records, ExpectedCount: result.Expected, CoveragePct: result.CoveragePct, ValidationErrorPct: result.ValidationErrorPct, Timely: result.Timely, GoldSHA256: day.goldSHA256, CreatedAt: c.Clock()}
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
