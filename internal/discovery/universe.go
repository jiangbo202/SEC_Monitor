package discovery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
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
	ReasonPriceInvalid       = "price_invalid"
	ReasonProviderInactive   = "provider_inactive"
	ReasonOutsideMarketCap   = "outside_market_cap"
	ReasonQualifiedSmallCap  = "qualified_small_cap"
	ReasonClassificationData = "classification_not_valid"
)

var coordinatorRunMu sync.Mutex

type Coordinator struct {
	DB       *gorm.DB
	Metadata SecurityMetadataSource
	Shares   ShareFactSource
	Prices   PriceProvider
	Calendar MarketCalendar
	Clock    func() time.Time
	// AfterStageChunk is a test/operations fault-injection hook. It runs only
	// after a chunk transaction commits and before the next chunk begins.
	AfterStageChunk func(kind string, chunk int) error
}

type metadataGroup struct {
	Primary  SecuritySourceRecord
	Listings []SecuritySourceRecord
}

func (c *Coordinator) SyncSecurityUniverse(ctx context.Context) (UniverseBatch, error) {
	coordinatorRunMu.Lock()
	defer coordinatorRunMu.Unlock()
	if err := c.validateBase(ctx); err != nil {
		return UniverseBatch{}, err
	}
	if c.Metadata == nil || c.Shares == nil {
		return UniverseBatch{}, errors.New("metadata and share sources are required")
	}
	now := c.Clock()
	date, err := nyCivilDate(now)
	if err != nil {
		return UniverseBatch{}, err
	}
	records, metadataVersion, err := c.Metadata.Load(ctx)
	if err != nil {
		return UniverseBatch{}, fmt.Errorf("load security metadata: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return UniverseBatch{}, err
	}
	if len(records) == 0 {
		return UniverseBatch{}, errors.New("security metadata is empty")
	}
	allowed := make(map[string]struct{}, len(records))
	for _, record := range records {
		if validCIK(record.CIK) {
			allowed[record.CIK] = struct{}{}
		}
	}
	facts, shareVersion, err := c.Shares.LoadLatestShares(ctx, allowed)
	if err != nil {
		return UniverseBatch{}, fmt.Errorf("load share facts: %w", err)
	}
	versions, err := normalizeSourceVersions(date, metadataVersion, shareVersion)
	if err != nil {
		return UniverseBatch{}, err
	}
	contentSHA, err := hashSecurityInputs(records, facts)
	if err != nil {
		return UniverseBatch{}, err
	}
	batch, existed, err := c.createDraft(ctx, BatchKindSecurity, date, versions, contentSHA, now)
	if err != nil || existed {
		return batch, err
	}

	classifications, selections, stageErr := c.stageSecurity(ctx, batch, records, facts, now)
	if stageErr == nil {
		stageErr = validateSecurityStage(c.DB.WithContext(ctx), batch.BatchID, classifications, selections)
	}
	if stageErr != nil {
		return c.failBatch(ctx, batch, stageErr)
	}
	return c.publish(ctx, batch, classifications)
}

func (c *Coordinator) SyncMarketPrices(ctx context.Context) (UniverseBatch, error) {
	coordinatorRunMu.Lock()
	defer coordinatorRunMu.Unlock()
	if err := c.validateBase(ctx); err != nil {
		return UniverseBatch{}, err
	}
	if c.Prices == nil || c.Calendar == nil {
		return UniverseBatch{}, errors.New("price provider and market calendar are required")
	}
	named, ok := c.Prices.(interface{ ProviderName() string })
	if !ok || strings.TrimSpace(named.ProviderName()) == "" {
		return UniverseBatch{}, errors.New("price provider must expose its provider name")
	}
	providerName := strings.TrimSpace(named.ProviderName())
	securityBatch, err := currentPublishedBatch(ctx, c.DB, BatchKindSecurity)
	if err != nil {
		return UniverseBatch{}, err
	}
	now := c.Clock()
	date, err := nyCivilDate(now)
	if err != nil {
		return UniverseBatch{}, err
	}
	if securityBatch.EffectiveDate != date {
		return UniverseBatch{}, fmt.Errorf("security batch date %s does not match price run date %s", securityBatch.EffectiveDate, date)
	}
	var health ProviderHealth
	if err := c.DB.WithContext(ctx).First(&health, "provider = ?", providerName).Error; err != nil {
		cause := fmt.Errorf("load provider health: %w", err)
		return c.recordPreflightFailure(ctx, BatchKindPrescreen, date, securityBatch, providerName, "health-missing", now, cause)
	}
	if health.Status != ProviderStatusActive {
		cause := fmt.Errorf("%s: %s", ReasonProviderInactive, health.Status)
		return c.recordPreflightFailure(ctx, BatchKindPrescreen, date, securityBatch, providerName, health.Status, now, cause)
	}
	var window []providerWindowDay
	if err := json.Unmarshal([]byte(health.WindowJSON), &window); err != nil {
		cause := fmt.Errorf("decode active provider health: %w", err)
		return c.recordPreflightFailure(ctx, BatchKindPrescreen, date, securityBatch, providerName, "health-invalid", now, cause)
	}
	if err := validateProviderHealthWindow(ctx, c.Calendar, health, window); err != nil {
		cause := fmt.Errorf("validate active provider health: %w", err)
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

	expected, err := c.currentIncludedListings(ctx, securityBatch.BatchID)
	if err != nil {
		return UniverseBatch{}, err
	}
	records, result, err := c.Prices.Load(ctx, expected)
	if err != nil {
		cause := fmt.Errorf("load market prices: %w", err)
		return c.recordPreflightFailure(ctx, BatchKindPrescreen, date, securityBatch, providerName, "load-failed", now, cause)
	}
	if result.Provider != providerName {
		return UniverseBatch{}, errors.New("price provider identity changed during load")
	}
	for _, record := range records {
		if record.Source != providerName || strings.TrimSpace(record.Symbol) == "" {
			return UniverseBatch{}, errors.New("price records do not match provider identity")
		}
	}
	priceVersion := SourceVersion{Source: "price:" + result.Provider, Version: result.SourceVersion, SHA256: result.SHA256, EffectiveAt: result.EffectiveDate}
	securityVersion := SourceVersion{Source: BatchKindSecurity, Version: securityBatch.BatchID, SHA256: securityBatch.BatchID, EffectiveAt: now}
	var inherited []SourceVersion
	if err := json.Unmarshal([]byte(securityBatch.SourceVersionsJSON), &inherited); err != nil {
		return UniverseBatch{}, fmt.Errorf("decode security batch source versions: %w", err)
	}
	versions, err := normalizeSourceVersions(date, append(inherited, securityVersion, priceVersion)...)
	if err != nil {
		return UniverseBatch{}, err
	}
	day, err := EvaluateProviderDay(result, records, now)
	if err != nil {
		return UniverseBatch{}, err
	}
	if day.goldSHA256 != health.GoldSHA256 {
		return UniverseBatch{}, errors.New("active provider health uses different frozen gold evidence")
	}
	contentSHA, err := hashPriceInputs(securityBatch.BatchID, records)
	if err != nil {
		return UniverseBatch{}, err
	}
	batch, existed, err := c.createDraft(ctx, BatchKindPrescreen, date, versions, contentSHA, now)
	if err != nil || existed {
		return batch, err
	}
	if err := c.persistPrices(ctx, records, result.SourceVersion); err != nil {
		return c.failBatch(ctx, batch, err)
	}
	snapshots, stageErr := c.buildUniverseSnapshots(ctx, securityBatch.BatchID, batch.BatchID, records, result, now)
	if stageErr == nil {
		stageErr = c.persistUniverseSnapshots(ctx, snapshots)
	}
	if stageErr == nil {
		stageErr = c.persistProviderRun(ctx, batch.BatchID, result, day)
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
	versions, versionErr := normalizeSourceVersions(date,
		SourceVersion{Source: BatchKindSecurity, Version: securityBatch.BatchID, SHA256: securityBatch.BatchID, EffectiveAt: now},
		SourceVersion{Source: "provider-preflight:" + provider, Version: state, SHA256: hex.EncodeToString(sha[:]), EffectiveAt: now},
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
	return existing, true, fmt.Errorf("batch %s already exists with status %s", id, existing.Status)
}

func hashSecurityInputs(records []SecuritySourceRecord, facts []ShareFact) (string, error) {
	recordCopy := append([]SecuritySourceRecord(nil), records...)
	sort.Slice(recordCopy, func(i, j int) bool {
		if recordCopy[i].CIK == recordCopy[j].CIK {
			return recordCopy[i].Ticker < recordCopy[j].Ticker
		}
		return recordCopy[i].CIK < recordCopy[j].CIK
	})
	factCopy := append([]ShareFact(nil), facts...)
	sort.Slice(factCopy, func(i, j int) bool { return shareFactIdentity(factCopy[i]) < shareFactIdentity(factCopy[j]) })
	return hashCanonicalContent(struct {
		Records []SecuritySourceRecord `json:"records"`
		Facts   []ShareFact            `json:"facts"`
	}{recordCopy, factCopy})
}

func hashPriceInputs(securityBatchID string, records []PriceRecord) (string, error) {
	copyRecords := append([]PriceRecord(nil), records...)
	sort.Slice(copyRecords, func(i, j int) bool {
		if copyRecords[i].Symbol == copyRecords[j].Symbol {
			if copyRecords[i].TradeDate.Equal(copyRecords[j].TradeDate) {
				return copyRecords[i].Source < copyRecords[j].Source
			}
			return copyRecords[i].TradeDate.Before(copyRecords[j].TradeDate)
		}
		return copyRecords[i].Symbol < copyRecords[j].Symbol
	})
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

func (c *Coordinator) stageSecurity(ctx context.Context, batch UniverseBatch, records []SecuritySourceRecord, facts []ShareFact, now time.Time) (int, int, error) {
	groups, err := groupMetadata(records)
	if err != nil {
		return 0, 0, err
	}
	var overrides []ManualSecurityOverride
	if err := c.DB.WithContext(ctx).Where("active = ?", true).Find(&overrides).Error; err != nil {
		return 0, 0, err
	}
	factsByCIK := make(map[string][]ShareFact)
	for _, fact := range facts {
		factsByCIK[fact.CIK] = append(factsByCIK[fact.CIK], fact)
	}
	classifications, selections := 0, 0
	for start := 0; start < len(groups); start += universeChunkSize {
		end := start + universeChunkSize
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
				if err := tx.Where("cik = ?", source.CIK).FirstOrCreate(&security).Error; err != nil {
					return err
				}
				updates := map[string]any{"company_name": source.CompanyName, "sic": source.SIC, "state_of_incorporation": source.StateOfIncorporation, "latest_annual_form": source.LatestAnnualForm}
				if err := tx.Model(&security).Updates(updates).Error; err != nil {
					return err
				}
				source.SecurityID = security.ID
				if source.MappingStatus == "" {
					source.MappingStatus = MappingStatusCurrent
				}
				validFrom, _ := time.Parse(time.DateOnly, batch.EffectiveDate)
				currentTickers := make([]string, 0, len(group.Listings))
				for _, listingSource := range group.Listings {
					ticker := strings.ToUpper(strings.TrimSpace(listingSource.Ticker))
					currentTickers = append(currentTickers, ticker)
					var listing Listing
					lookup := tx.Where("security_id = ? AND ticker = ? AND valid_to IS NULL", security.ID, ticker).Order("valid_from DESC").First(&listing)
					if errors.Is(lookup.Error, gorm.ErrRecordNotFound) {
						listing = Listing{SecurityID: security.ID, Ticker: ticker, ProviderTicker: ticker, Exchange: listingSource.Exchange, ValidFrom: validFrom, Source: "coordinator-metadata", MappingStatus: source.MappingStatus}
						if err := tx.Create(&listing).Error; err != nil {
							return err
						}
					} else if lookup.Error != nil {
						return lookup.Error
					} else if err := tx.Model(&listing).Updates(map[string]any{"provider_ticker": ticker, "exchange": listingSource.Exchange, "mapping_status": source.MappingStatus}).Error; err != nil {
						return err
					}
				}
				if err := tx.Model(&Listing{}).Where("security_id = ? AND valid_to IS NULL AND ticker NOT IN ?", security.ID, currentTickers).Update("valid_to", validFrom).Error; err != nil {
					return err
				}
				classification := ClassifySecurity(source, overrides)
				evidence, _ := json.Marshal(classification.Evidence)
				row := ClassificationSnapshot{BatchID: batch.BatchID, SecurityID: security.ID, Included: classification.Included, Status: classification.Status, Confidence: classification.Confidence, ReasonCode: classification.ReasonCode, RuleVersion: ClassificationRuleVersion, EvidenceJSON: string(evidence), CreatedAt: now}
				if err := tx.Create(&row).Error; err != nil {
					return err
				}
				selection := SelectShareSnapshot(factsByCIK[source.CIK], nil, now)
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
			if err := c.AfterStageChunk(BatchKindSecurity, start/universeChunkSize); err != nil {
				return classifications, selections, err
			}
		}
	}
	return classifications, selections, nil
}

func groupMetadata(records []SecuritySourceRecord) ([]metadataGroup, error) {
	if len(records) == 0 {
		return nil, errors.New("security metadata is empty")
	}
	byCIK := map[string][]SecuritySourceRecord{}
	tickerCIKs := map[string]map[string]struct{}{}
	for _, record := range records {
		record.Ticker = strings.ToUpper(strings.TrimSpace(record.Ticker))
		if !validCIK(record.CIK) || record.Ticker == "" {
			return nil, errors.New("metadata contains invalid identity")
		}
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
	var c, s int64
	if err := db.Model(&ClassificationSnapshot{}).Where("batch_id = ?", batchID).Count(&c).Error; err != nil {
		return err
	}
	if err := db.Model(&BatchShareSelection{}).Where("batch_id = ?", batchID).Count(&s).Error; err != nil {
		return err
	}
	if int(c) != classifications || int(s) != selections || c == 0 || c != s {
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
		_ = c.DB.WithContext(context.WithoutCancel(ctx)).Model(&UniverseBatch{}).Where("batch_id = ? AND status = ?", batch.BatchID, BatchStatusDraft).Updates(map[string]any{"status": BatchStatusPartial, "completed_at": now, "error_message": cause.Error()}).Error
		batch, _ = currentBatchByID(context.WithoutCancel(ctx), c.DB, batch.BatchID)
	}
	return batch, cause
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
	var rows []Listing
	err := c.DB.WithContext(ctx).Table("listings").Select("listings.*").Joins("JOIN classification_snapshots c ON c.security_id = listings.security_id").Where("c.batch_id = ? AND c.included = ? AND c.status = ? AND listings.valid_to IS NULL AND listings.mapping_status = ?", batchID, true, EffectiveStatusIncluded, MappingStatusCurrent).Order("listings.ticker").Find(&rows).Error
	return rows, err
}

func (c *Coordinator) persistPrices(ctx context.Context, records []PriceRecord, version string) error {
	snapshots := make([]PriceSnapshot, len(records))
	for i, record := range records {
		snapshots[i] = PriceSnapshot{Source: record.Source, SourceVersion: version, Symbol: record.Symbol, TradeDate: record.TradeDate, CloseMicros: record.CloseMicros, Volume: record.Volume, Currency: record.Currency, Adjusted: record.Adjusted, QualityStatus: QualityStatusValid, CreatedAt: c.Clock()}
	}
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
	var listings []Listing
	if err := c.DB.WithContext(ctx).Where("valid_to IS NULL").Order("ticker").Find(&listings).Error; err != nil {
		return nil, err
	}
	listingBySecurity := map[uint]Listing{}
	for _, l := range listings {
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
		sort.Slice(candidates, func(i, j int) bool { return candidates[i].TradeDate.After(candidates[j].TradeDate) })
		price := candidates[0]
		if len(candidates) > 1 && candidates[1].TradeDate.Equal(price.TradeDate) && candidates[1].CloseMicros != price.CloseMicros {
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
			snapshot.ReasonCode = ReasonPriceInvalid
			output = append(output, snapshot)
			continue
		}
		var priceSnapshot PriceSnapshot
		if err := c.DB.WithContext(ctx).Where("source = ? AND source_version = ? AND symbol = ? AND trade_date = ?", price.Source, result.SourceVersion, price.Symbol, price.TradeDate).First(&priceSnapshot).Error; err != nil {
			return nil, err
		}
		snapshot.PriceSnapshotID = &priceSnapshot.ID
		var share ShareSnapshot
		if err := c.DB.WithContext(ctx).First(&share, *selection.ShareSnapshotID).Error; err != nil {
			return nil, err
		}
		capUSD, qualified, err := ComputeSmallCapQualification(price.CloseMicros, share.Shares)
		if err != nil {
			snapshot.QualityStatus = QualityStatusConflict
			snapshot.ReasonCode = ReasonPriceInvalid
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

func (c *Coordinator) persistProviderRun(ctx context.Context, batchID string, result ProviderResult, day ProviderDayResult) error {
	run := ProviderRun{BatchID: batchID, Provider: result.Provider, Status: ProviderStatusActive, SourceVersion: result.SourceVersion, SHA256: result.SHA256, EffectiveDate: result.EffectiveDate, RecordCount: result.Records, ExpectedCount: result.Expected, CoveragePct: result.CoveragePct, ValidationErrorPct: result.ValidationErrorPct, Timely: result.Timely, GoldSHA256: day.goldSHA256, CreatedAt: c.Clock()}
	return c.DB.WithContext(ctx).Create(&run).Error
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
