package discovery

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// IncrementalListingInput contains the bounded SEC payload for issuers that
// appeared in the latest exchange/SEC ticker directories but not in the
// current immutable security universe.
type IncrementalListingInput struct {
	Records        []SecuritySourceRecord
	Shares         []ShareFact
	FinancialFacts []FinancialFact
	SourceVersions []SourceVersion
}

type incrementalListingPlan struct {
	Records            []SecuritySourceRecord
	ReplaceSourceKeys  []string
	ReplaceSecurityIDs []uint
	Skipped            int
	Previous           UniverseBatch
}

// IncrementalListingResult records whether a daily run advanced the frozen
// security universe.  Existing listings are left untouched; ticker changes
// and identity conflicts remain the responsibility of weekly calibration.
type IncrementalListingResult struct {
	PreviousBatch UniverseBatch
	Batch         UniverseBatch
	Discovered    int
	Added         int
	Skipped       int
}

// FindNewListings compares current directory records with the published
// security universe. It accepts wholly new CIK/ticker pairs, plus the safe
// upgrade of a previous unresolved listing row to a verified SEC identity.
// Ticker reuse and existing-issuer changes remain weekly-calibration work.
func (c *Coordinator) FindNewListings(ctx context.Context, records []SecuritySourceRecord) (incrementalListingPlan, error) {
	if c == nil || c.DB == nil {
		return incrementalListingPlan{}, errors.New("discovery database is required")
	}
	current, err := currentPublishedBatch(ctx, c.DB, BatchKindSecurity)
	if err != nil {
		return incrementalListingPlan{}, err
	}
	normalized, err := normalizeMetadataRecords(records)
	if err != nil {
		return incrementalListingPlan{}, err
	}
	var identities []SecurityBatchIdentity
	if err := c.DB.WithContext(ctx).Where("batch_id = ?", current.BatchID).Find(&identities).Error; err != nil {
		return incrementalListingPlan{}, err
	}
	var listingRows []ListingIdentitySnapshot
	if err := c.DB.WithContext(ctx).Where("batch_id = ?", current.BatchID).Find(&listingRows).Error; err != nil {
		return incrementalListingPlan{}, err
	}
	byCIK, byTicker, byPair := make(map[string]struct{}, len(identities)), make(map[string]struct{}, len(identities)), make(map[string]struct{}, len(identities))
	identityByCIK := make(map[string]SecurityBatchIdentity, len(identities))
	for _, identity := range identities {
		cik := strings.TrimSpace(identity.CIK)
		ticker := strings.ToUpper(strings.TrimSpace(identity.Ticker))
		byCIK[cik] = struct{}{}
		byTicker[ticker] = struct{}{}
		byPair[cik+"\x00"+ticker] = struct{}{}
		identityByCIK[cik] = identity
	}
	listingsBySourceKey := make(map[string]ListingIdentitySnapshot, len(listingRows))
	for _, row := range listingRows {
		listingsBySourceKey[row.SourceKey] = row
	}
	result := make([]SecuritySourceRecord, 0)
	replacements := make([]string, 0)
	replaceSecurityIDs := make([]uint, 0)
	skipped := 0
	for _, record := range normalized {
		record.Ticker = strings.ToUpper(strings.TrimSpace(record.Ticker))
		if !validCIK(record.CIK) || record.Ticker == "" || record.MappingStatus != MappingStatusCurrent {
			skipped++
			continue
		}
		pair := record.CIK + "\x00" + record.Ticker
		if _, exists := byPair[pair]; exists {
			continue
		}
		if _, exists := byCIK[record.CIK]; exists {
			skipped++
			continue
		}
		if _, exists := byTicker[record.Ticker]; exists {
			skipped++
			continue
		}
		sourceKey := strings.TrimSpace(record.SourceKey)
		if sourceKey == "" {
			sourceKey = record.Ticker
		}
		if existing, exists := listingsBySourceKey[sourceKey]; exists {
			if validCIK(existing.CIK) && existing.MappingStatus == MappingStatusCurrent {
				skipped++
				continue
			}
			replacements = append(replacements, sourceKey)
		}
		result = append(result, record)
	}
	// A current, previously unresolved issuer identity is safe to repair in the
	// daily path when the exchange directory now distinguishes exactly one
	// common share from attached warrants/rights/units. This is not a ticker
	// reuse guess: the CIK already exists, the incoming mapping is current, and
	// the primary listing is selected solely from its explicit security name.
	// It prevents a normal common-share + warrant pair (for example SLDP/SLDPW)
	// from remaining frozen as a mapping conflict until weekly calibration.
	validCurrent := make([]SecuritySourceRecord, 0, len(normalized))
	for _, record := range normalized {
		if validCIK(record.CIK) && strings.TrimSpace(record.Ticker) != "" && record.MappingStatus == MappingStatusCurrent {
			validCurrent = append(validCurrent, record)
		}
	}
	groups, groupErr := groupMetadata(validCurrent)
	if groupErr != nil {
		return incrementalListingPlan{}, groupErr
	}
	for _, group := range groups {
		existing, exists := identityByCIK[group.Primary.CIK]
		if !exists || existing.MappingStatus == MappingStatusCurrent || group.Primary.MappingStatus != MappingStatusCurrent {
			continue
		}
		// Keep the complete local listing set so the audit trail records the
		// attached warrant as excluded while the common share is selected as
		// the issuer's primary market identity.
		result = append(result, group.Listings...)
		replaceSecurityIDs = append(replaceSecurityIDs, existing.SecurityID)
		for _, listing := range group.Listings {
			replacements = append(replacements, listingIdentitySourceKey(listing))
		}
	}
	result = dedupeMetadataRecords(result)
	replacements = dedupeSortedStrings(replacements)
	replaceSecurityIDs = dedupeSortedUintIDs(replaceSecurityIDs)
	sort.Slice(result, func(i, j int) bool { return canonicalLess(result[i], result[j]) })
	return incrementalListingPlan{Records: result, ReplaceSourceKeys: replacements, ReplaceSecurityIDs: replaceSecurityIDs, Skipped: skipped, Previous: current}, nil
}

// SyncIncrementalListings advances the immutable security universe by copying
// its already-validated evidence and staging only newly discovered issuers.
// It never fetches or parses the full SEC archives.
func (c *Coordinator) SyncIncrementalListings(ctx context.Context, input IncrementalListingInput) (IncrementalListingResult, error) {
	if err := c.validateBase(ctx); err != nil {
		return IncrementalListingResult{}, err
	}
	release, err := acquireCoordinatorRun(ctx)
	if err != nil {
		return IncrementalListingResult{}, err
	}
	defer release()
	now := c.Clock()
	date, err := nyCivilDate(now)
	if err != nil {
		return IncrementalListingResult{}, err
	}
	plan, err := c.FindNewListings(ctx, input.Records)
	if err != nil {
		return IncrementalListingResult{}, err
	}
	newRecords, previous := plan.Records, plan.Previous
	result := IncrementalListingResult{PreviousBatch: previous, Batch: previous, Discovered: len(input.Records), Skipped: plan.Skipped}
	if len(newRecords) == 0 {
		return result, nil
	}
	allowed := make(map[string]struct{}, len(newRecords))
	for _, record := range newRecords {
		allowed[record.CIK] = struct{}{}
	}
	shares := filterShareFacts(input.Shares, allowed)
	financials := filterFinancialFacts(input.FinancialFacts, allowed)
	events := capitalEventsForRecords(newRecords, now)
	shares, err = normalizeShareFacts(shares)
	if err != nil {
		return result, err
	}
	financials, err = normalizeFinancialFacts(financials)
	if err != nil {
		return result, err
	}
	events, err = normalizeCapitalEvents(events)
	if err != nil {
		return result, err
	}
	var overrides []ManualSecurityOverride
	if err := c.DB.WithContext(ctx).Where("active = ?", true).Order("security_id, id").Find(&overrides).Error; err != nil {
		return result, err
	}
	overrideVersion, err := sourceVersionForOverrides(overrides, now)
	if err != nil {
		return result, err
	}
	baseHash, err := hashCanonicalContent(struct {
		Previous string                 `json:"previous_batch"`
		Records  []SecuritySourceRecord `json:"records"`
		Shares   []ShareFact            `json:"shares"`
		Facts    []FinancialFact        `json:"financial_facts"`
	}{previous.BatchID, newRecords, shares, financials})
	if err != nil {
		return result, err
	}
	baseVersion := SourceVersion{Source: "security-universe:incremental-base", Version: previous.BatchID, SHA256: previous.ContentSHA256, EffectiveAt: now}
	versions := append([]SourceVersion{baseVersion}, input.SourceVersions...)
	versions = append(versions, SourceVersion{Source: "capital-events:incremental-submissions", Version: baseHash + "+" + CapitalRiskPolicyVersion, SHA256: baseHash, EffectiveAt: now}, overrideVersion)
	versions, err = alignSourceVersionsToBatchDate(date, versions)
	if err != nil {
		return result, err
	}
	versions, err = normalizeSourceVersions(date, versions...)
	if err != nil {
		return result, err
	}
	contentHash, err := hashCanonicalContent(struct {
		Previous           string                    `json:"previous_batch"`
		Inputs             string                    `json:"input_hash"`
		Replacements       []string                  `json:"replacement_source_keys"`
		ReplaceSecurityIDs []uint                    `json:"replace_security_ids"`
		Overrides          []canonicalManualOverride `json:"overrides"`
	}{previous.BatchID, baseHash, plan.ReplaceSourceKeys, plan.ReplaceSecurityIDs, canonicalManualOverrides(overrides)})
	if err != nil {
		return result, err
	}
	batch, existed, err := c.createDraft(ctx, BatchKindSecurity, date, versions, contentHash, now)
	if err != nil || existed {
		result.Batch = batch
		if existed && batch.Status == BatchStatusPublished {
			result.Added = len(newRecords)
		}
		return result, err
	}
	existingClassifications, existingSelections, err := c.cloneSecurityBatchEvidence(ctx, previous.BatchID, batch.BatchID, plan.ReplaceSourceKeys, plan.ReplaceSecurityIDs, now)
	if err == nil {
		addedClassifications, addedSelections, stageErr := c.stageSecurity(ctx, batch, newRecords, shares, financials, nil, nil, events, overrides, now)
		if stageErr != nil {
			err = stageErr
		} else {
			err = validateSecurityStage(c.DB.WithContext(ctx), batch.BatchID, existingClassifications+addedClassifications, existingSelections+addedSelections)
		}
	}
	if err != nil {
		failed, failure := c.failBatch(ctx, batch, err)
		result.Batch = failed
		return result, failure
	}
	published, err := c.publish(ctx, batch, existingClassifications+len(newRecords))
	result.Batch = published
	if err == nil {
		result.Added = len(newRecords)
	}
	return result, err
}

func dedupeMetadataRecords(records []SecuritySourceRecord) []SecuritySourceRecord {
	seen := make(map[string]struct{}, len(records))
	result := make([]SecuritySourceRecord, 0, len(records))
	for _, record := range records {
		key := record.CIK + "\x00" + listingIdentitySourceKey(record)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, record)
	}
	return result
}

func dedupeSortedStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.ToUpper(strings.TrimSpace(value))
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func dedupeSortedUintIDs(values []uint) []uint {
	seen := make(map[uint]struct{}, len(values))
	result := make([]uint, 0, len(values))
	for _, value := range values {
		if value == 0 {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}

func filterShareFacts(rows []ShareFact, allowed map[string]struct{}) []ShareFact {
	result := make([]ShareFact, 0, len(rows))
	for _, row := range rows {
		if _, ok := allowed[row.CIK]; ok {
			result = append(result, row)
		}
	}
	return result
}

func filterFinancialFacts(rows []FinancialFact, allowed map[string]struct{}) []FinancialFact {
	result := make([]FinancialFact, 0, len(rows))
	for _, row := range rows {
		if _, ok := allowed[row.CIK]; ok {
			result = append(result, row)
		}
	}
	return result
}

func capitalEventsForRecords(records []SecuritySourceRecord, asOf time.Time) []CapitalEvent {
	result := make([]CapitalEvent, 0)
	for _, record := range records {
		for _, filing := range record.FilingMetadata {
			if filing.CIK != record.CIK || (!filing.AcceptedAt.IsZero() && filing.AcceptedAt.After(asOf)) {
				continue
			}
			kind, changes, relevant := capitalEventForFiling(filing)
			if !relevant {
				continue
			}
			effective := filing.FiledAt
			if effective.IsZero() {
				effective = filing.ReportAt
			}
			if effective.IsZero() {
				effective = filing.AcceptedAt
			}
			if effective.IsZero() {
				effective = asOf
			}
			result = append(result, CapitalEvent{CIK: record.CIK, Kind: kind, Accession: filing.Accession, EffectiveAt: effective, AcceptedAt: filing.AcceptedAt, ChangesShares: changes, Reason: "potential share-count change; filing is a conservative risk signal, not confirmation of issuance"})
		}
	}
	return result
}

// cloneSecurityBatchEvidence carries forward immutable evidence that is not
// re-downloaded by the light daily path. Raw SEC facts and transactions remain
// issuer-scoped tables and therefore do not need copying.
func (c *Coordinator) cloneSecurityBatchEvidence(ctx context.Context, fromBatchID, toBatchID string, replaceSourceKeys []string, replaceSecurityIDs []uint, now time.Time) (int, int, error) {
	var classifications []ClassificationSnapshot
	if err := c.DB.WithContext(ctx).Where("batch_id = ?", fromBatchID).Find(&classifications).Error; err != nil {
		return 0, 0, err
	}
	var selections []BatchShareSelection
	if err := c.DB.WithContext(ctx).Where("batch_id = ?", fromBatchID).Find(&selections).Error; err != nil {
		return 0, 0, err
	}
	var identities []SecurityBatchIdentity
	if err := c.DB.WithContext(ctx).Where("batch_id = ?", fromBatchID).Find(&identities).Error; err != nil {
		return 0, 0, err
	}
	var listings []ListingIdentitySnapshot
	if err := c.DB.WithContext(ctx).Where("batch_id = ?", fromBatchID).Find(&listings).Error; err != nil {
		return 0, 0, err
	}
	var metrics []FinancialMetricSnapshot
	if err := c.DB.WithContext(ctx).Where("batch_id = ?", fromBatchID).Find(&metrics).Error; err != nil {
		return 0, 0, err
	}
	var coverage []InsiderCoverageSnapshot
	if err := c.DB.WithContext(ctx).Where("batch_id = ?", fromBatchID).Find(&coverage).Error; err != nil {
		return 0, 0, err
	}
	var risks []CapitalRiskSnapshot
	if err := c.DB.WithContext(ctx).Where("batch_id = ?", fromBatchID).Find(&risks).Error; err != nil {
		return 0, 0, err
	}
	replacements := make(map[string]struct{}, len(replaceSourceKeys))
	for _, key := range replaceSourceKeys {
		replacements[strings.TrimSpace(key)] = struct{}{}
	}
	if len(replacements) > 0 {
		filtered := listings[:0]
		for _, row := range listings {
			if _, replace := replacements[row.SourceKey]; !replace {
				filtered = append(filtered, row)
			}
		}
		listings = filtered
	}
	replaceSecurities := make(map[uint]struct{}, len(replaceSecurityIDs))
	for _, securityID := range replaceSecurityIDs {
		replaceSecurities[securityID] = struct{}{}
	}
	if len(replaceSecurities) > 0 {
		classifications = filterBatchClassifications(classifications, replaceSecurities)
		selections = filterBatchShareSelections(selections, replaceSecurities)
		identities = filterBatchIdentities(identities, replaceSecurities)
		metrics = filterBatchFinancialMetrics(metrics, replaceSecurities)
		// stageSecurity re-persists capital-risk rows for every repaired issuer.
		// Keeping old copies would collide on their immutable batch/security
		// identities before the replacement can be published. Insider coverage is
		// intentionally retained: the lightweight listing path does not download
		// Form 4 data or recreate that batch evidence.
		risks = filterBatchCapitalRisks(risks, replaceSecurities)
	}
	for i := range classifications {
		classifications[i].ID, classifications[i].BatchID, classifications[i].CreatedAt = 0, toBatchID, now
	}
	for i := range selections {
		selections[i].ID, selections[i].BatchID, selections[i].CreatedAt = 0, toBatchID, now
	}
	for i := range identities {
		identities[i].ID, identities[i].BatchID, identities[i].CreatedAt = 0, toBatchID, now
	}
	for i := range listings {
		listings[i].ID, listings[i].BatchID, listings[i].CreatedAt = 0, toBatchID, now
	}
	for i := range metrics {
		metrics[i].ID, metrics[i].BatchID, metrics[i].CreatedAt = 0, toBatchID, now
	}
	for i := range coverage {
		coverage[i].ID, coverage[i].BatchID, coverage[i].CreatedAt = 0, toBatchID, now
	}
	for i := range risks {
		risks[i].ID, risks[i].BatchID, risks[i].CreatedAt = 0, toBatchID, now
	}
	// SQLite's default variable limit is commonly 999. Some snapshots have
	// more than twenty persisted columns, so use a deliberately small batch
	// instead of the 1,000-row universe import chunk.
	const evidenceCloneBatchSize = 25
	if err := c.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		rows := []struct {
			value any
			count int
		}{
			{&classifications, len(classifications)}, {&selections, len(selections)}, {&identities, len(identities)}, {&listings, len(listings)},
			{&metrics, len(metrics)}, {&coverage, len(coverage)}, {&risks, len(risks)},
		}
		for _, item := range rows {
			if item.count == 0 {
				continue
			}
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).CreateInBatches(item.value, evidenceCloneBatchSize).Error; err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return 0, 0, err
	}
	// Listing rows may intentionally be one fewer than identities while an
	// unresolved source key is being replaced in this batch. stageSecurity
	// inserts the upgraded listing immediately afterwards, and the final
	// validateSecurityStage check verifies the completed set.
	if len(classifications) != len(selections) || len(classifications) != len(identities) {
		return 0, 0, fmt.Errorf("previous security batch evidence is incomplete")
	}
	return len(classifications), len(selections), nil
}

func filterBatchClassifications(rows []ClassificationSnapshot, excluded map[uint]struct{}) []ClassificationSnapshot {
	result := rows[:0]
	for _, row := range rows {
		if _, skip := excluded[row.SecurityID]; !skip {
			result = append(result, row)
		}
	}
	return result
}

func filterBatchShareSelections(rows []BatchShareSelection, excluded map[uint]struct{}) []BatchShareSelection {
	result := rows[:0]
	for _, row := range rows {
		if _, skip := excluded[row.SecurityID]; !skip {
			result = append(result, row)
		}
	}
	return result
}

func filterBatchIdentities(rows []SecurityBatchIdentity, excluded map[uint]struct{}) []SecurityBatchIdentity {
	result := rows[:0]
	for _, row := range rows {
		if _, skip := excluded[row.SecurityID]; !skip {
			result = append(result, row)
		}
	}
	return result
}

func filterBatchFinancialMetrics(rows []FinancialMetricSnapshot, excluded map[uint]struct{}) []FinancialMetricSnapshot {
	result := rows[:0]
	for _, row := range rows {
		if _, skip := excluded[row.SecurityID]; !skip {
			result = append(result, row)
		}
	}
	return result
}

func filterBatchCapitalRisks(rows []CapitalRiskSnapshot, excluded map[uint]struct{}) []CapitalRiskSnapshot {
	result := rows[:0]
	for _, row := range rows {
		if _, skip := excluded[row.SecurityID]; !skip {
			result = append(result, row)
		}
	}
	return result
}
