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
	"time"

	"gorm.io/gorm"
)

// MarketPriceRecoveryQueue identifies current A/B candidates whose displayed
// market evidence needs attention. It is intentionally derived entirely from
// persisted snapshots so opening the discovery-log page never consumes a
// market-data-provider request.
type MarketPriceRecoveryQueue struct {
	BatchID       string                    `json:"batch_id"`
	EffectiveDate string                    `json:"effective_date"`
	Items         []MarketPriceRecoveryItem `json:"items"`
}

type MarketPriceRecoveryItem struct {
	Ticker               string     `json:"ticker"`
	SecurityID           uint       `json:"security_id"`
	Grade                string     `json:"grade"`
	MarketCapUSD         int64      `json:"market_cap_usd"`
	Issue                string     `json:"issue"`
	IssueLabel           string     `json:"issue_label"`
	PriceTradeDate       *time.Time `json:"price_trade_date,omitempty"`
	PriceFreshnessStatus string     `json:"price_freshness_status"`
	PriceAgeCalendarDays int        `json:"price_age_calendar_days"`
	PriceSource          string     `json:"price_source"`
}

// ListCurrentCandidateMarketPriceRecoveryQueue returns only candidates whose
// market quote is missing, stale, future-dated, or sourced from a local
// fallback. A normal previous-trading-day close is intentionally not an
// anomaly: before the US close it is the current valid daily evidence.
func ListCurrentCandidateMarketPriceRecoveryQueue(ctx context.Context, db *gorm.DB) (MarketPriceRecoveryQueue, error) {
	result := MarketPriceRecoveryQueue{Items: []MarketPriceRecoveryItem{}}
	if db == nil {
		return result, errors.New("database is required")
	}
	batch, ok, err := currentPublishedPrescreenBatch(ctx, db)
	if err != nil {
		return result, err
	}
	if !ok {
		return result, nil
	}
	result.BatchID = batch.BatchID
	result.EffectiveDate = batch.EffectiveDate
	var scores []CandidateScoreSnapshot
	if err := db.WithContext(ctx).
		Where("batch_id = ? AND grade IN ?", batch.BatchID, []string{CandidateGradeA, CandidateGradeB}).
		Order("ticker ASC").
		Find(&scores).Error; err != nil {
		return result, err
	}
	items := make([]CandidateScoreResult, len(scores))
	for index, score := range scores {
		items[index] = CandidateScoreResult{CandidateScoreSnapshot: score}
	}
	items, err = hydrateCandidatePriceEvidence(ctx, db, batch, items)
	if err != nil {
		return result, err
	}
	for _, item := range items {
		issue, label := marketPriceRecoveryIssue(item)
		if issue == "" {
			continue
		}
		result.Items = append(result.Items, MarketPriceRecoveryItem{
			Ticker:               item.Ticker,
			SecurityID:           item.SecurityID,
			Grade:                item.Grade,
			MarketCapUSD:         item.MarketCapUSD,
			Issue:                issue,
			IssueLabel:           label,
			PriceTradeDate:       item.PriceTradeDate,
			PriceFreshnessStatus: item.PriceFreshnessStatus,
			PriceAgeCalendarDays: item.PriceAgeCalendarDays,
			PriceSource:          item.PriceSource,
		})
	}
	sort.Slice(result.Items, func(i, j int) bool {
		left, right := marketPriceRecoveryPriority(result.Items[i].Issue), marketPriceRecoveryPriority(result.Items[j].Issue)
		if left != right {
			return left < right
		}
		return result.Items[i].Ticker < result.Items[j].Ticker
	})
	return result, nil
}

func marketPriceRecoveryIssue(item CandidateScoreResult) (string, string) {
	switch item.PriceFreshnessStatus {
	case PriceFreshnessMissing:
		return "missing", "缺少可用收盘价"
	case PriceFreshnessStale:
		return "stale", "收盘价已过期"
	case PriceFreshnessFuture:
		return "future", "收盘价日期异常"
	}
	if strings.EqualFold(strings.TrimSpace(item.PriceSource), PriceSourceLocalCache) {
		return "local_fallback", "使用本地回退价"
	}
	return "", ""
}

func marketPriceRecoveryPriority(issue string) int {
	switch issue {
	case "missing":
		return 0
	case "stale", "future":
		return 1
	case "local_fallback":
		return 2
	default:
		return 3
	}
}

// MarketPriceRepriceResult is an immutable, targeted market correction. The
// new batch copies the prior universe and score evidence, replacing only the
// requested security's price, market cap and score. This keeps historical
// batches auditable while avoiding a full provider scan for one repaired
// symbol.
type MarketPriceRepriceResult struct {
	PreviousBatchID string `json:"previous_batch_id"`
	BatchID         string `json:"batch_id"`
	Ticker          string `json:"ticker"`
	PriceSnapshotID uint   `json:"price_snapshot_id"`
	MarketCapUSD    int64  `json:"market_cap_usd"`
	Grade           string `json:"grade"`
}

// RepriceCurrentCandidateFromLocalHistory publishes a small, immutable market
// correction after a one-symbol history repair. It deliberately requires a
// quote no newer than the current batch's effective date: a genuinely newer
// trading day must be handled by the regular market workflow so every symbol
// shares the same as-of date.
func RepriceCurrentCandidateFromLocalHistory(ctx context.Context, db *gorm.DB, ticker string, now time.Time) (MarketPriceRepriceResult, error) {
	result := MarketPriceRepriceResult{Ticker: strings.ToUpper(strings.TrimSpace(ticker))}
	if db == nil {
		return result, errors.New("database is required")
	}
	release, err := acquireCoordinatorRun(ctx)
	if err != nil {
		return result, err
	}
	defer release()
	if result.Ticker == "" {
		return result, errors.New("ticker is required")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	base, ok, err := currentPublishedPrescreenBatch(ctx, db)
	if err != nil {
		return result, err
	}
	if !ok {
		return result, errors.New("no current published prescreen batch")
	}
	result.PreviousBatchID = base.BatchID
	if strings.TrimSpace(base.UniverseSourceVersion) == "" {
		return result, errors.New("current market batch has no security source version")
	}
	var target UniverseSnapshot
	if err := db.WithContext(ctx).Where("batch_id = ? AND ticker = ?", base.BatchID, result.Ticker).First(&target).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return result, errors.New("ticker is not in the current market batch")
		}
		return result, err
	}
	if target.ShareSnapshotID == nil {
		return result, errors.New("ticker has no valid share evidence for market-cap reprice")
	}
	var price PriceSnapshot
	if err := db.WithContext(ctx).
		Where("symbol = ? AND quality_status = ?", result.Ticker, QualityStatusValid).
		Order("trade_date DESC, created_at DESC, id DESC").
		First(&price).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return result, errors.New("ticker has no usable local closing price")
		}
		return result, err
	}
	effectiveAt, err := parseNYCivilDate(base.EffectiveDate)
	if err != nil {
		return result, err
	}
	priceDate, err := time.Parse(time.DateOnly, price.TradeDate.Format(time.DateOnly))
	if err != nil {
		return result, err
	}
	batchDate, err := time.Parse(time.DateOnly, base.EffectiveDate)
	if err != nil {
		return result, err
	}
	if priceDate.After(batchDate) {
		return result, fmt.Errorf("local price date %s is newer than current batch date %s; run the normal market sync", price.TradeDate.Format(time.DateOnly), base.EffectiveDate)
	}
	if freshness, _ := candidatePriceFreshnessAt(base.EffectiveDate, &price.TradeDate, now); freshness != PriceFreshnessCurrent && freshness != PriceFreshnessPreviousTradingDay {
		return result, fmt.Errorf("local price for %s is not current enough to reprice (%s)", result.Ticker, freshness)
	}
	var share ShareSnapshot
	if err := db.WithContext(ctx).First(&share, *target.ShareSnapshotID).Error; err != nil {
		return result, err
	}
	marketCap, qualified, err := ComputeSmallCapQualification(price.CloseMicros, share.Shares)
	if err != nil {
		return result, err
	}
	var versions []SourceVersion
	if err := json.Unmarshal([]byte(base.SourceVersionsJSON), &versions); err != nil {
		return result, fmt.Errorf("decode current market source versions: %w", err)
	}
	digest := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%s\x00%d\x00%d", base.BatchID, result.Ticker, price.ID, marketCap)))
	versions = append(versions, SourceVersion{Source: "price-repair:" + strings.ToLower(result.Ticker), Version: fmt.Sprintf("%s:%d", price.Source, price.ID), SHA256: hex.EncodeToString(digest[:]), EffectiveAt: effectiveAt})
	versions, err = normalizeSourceVersions(base.EffectiveDate, versions...)
	if err != nil {
		return result, err
	}
	content := sha256.Sum256([]byte(base.ContentSHA256 + "\x00" + result.Ticker + "\x00" + fmt.Sprint(price.ID) + "\x00" + fmt.Sprint(marketCap)))
	coordinator := &Coordinator{DB: db, Clock: func() time.Time { return now }}
	batch, existed, err := coordinator.createDraft(ctx, BatchKindPrescreen, base.EffectiveDate, versions, hex.EncodeToString(content[:]), now)
	if err != nil {
		return result, err
	}
	result.BatchID = batch.BatchID
	if existed && batch.Status == BatchStatusPublished {
		var score CandidateScoreSnapshot
		if err := db.WithContext(ctx).Where("batch_id = ? AND security_id = ?", batch.BatchID, target.SecurityID).First(&score).Error; err != nil {
			return result, err
		}
		result.PriceSnapshotID, result.MarketCapUSD, result.Grade = price.ID, score.MarketCapUSD, score.Grade
		return result, nil
	}

	var universeRows []UniverseSnapshot
	var scoreRows []CandidateScoreSnapshot
	if err := db.WithContext(ctx).Where("batch_id = ?", base.BatchID).Order("security_id ASC").Find(&universeRows).Error; err != nil {
		return result, err
	}
	if err := db.WithContext(ctx).Where("batch_id = ?", base.BatchID).Order("security_id ASC").Find(&scoreRows).Error; err != nil {
		return result, err
	}
	if len(universeRows) == 0 {
		return result, errors.New("current market batch has no universe snapshots")
	}
	for index := range universeRows {
		universeRows[index].ID = 0
		universeRows[index].BatchID = batch.BatchID
		universeRows[index].CreatedAt = now
		if universeRows[index].SecurityID != target.SecurityID {
			continue
		}
		universeRows[index].PriceSnapshotID = &price.ID
		universeRows[index].MarketCapUSD = marketCap
		universeRows[index].QualityStatus = QualityStatusValid
		if qualified {
			universeRows[index].Included, universeRows[index].Status, universeRows[index].ReasonCode = true, EffectiveStatusPrescreen, ReasonQualifiedSmallCap
		} else {
			universeRows[index].Included, universeRows[index].Status, universeRows[index].ReasonCode = false, EffectiveStatusExcluded, ReasonOutsideMarketCap
		}
	}
	replacement, err := scoreCandidateForMarketCap(ctx, db, base.UniverseSourceVersion, batch.BatchID, target, marketCap, now)
	if err != nil {
		return result, err
	}
	for index := range scoreRows {
		scoreRows[index].ID = 0
		scoreRows[index].BatchID = batch.BatchID
		scoreRows[index].CreatedAt = now
		if scoreRows[index].SecurityID == target.SecurityID {
			scoreRows[index] = replacement
		}
	}
	if err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.CreateInBatches(universeRows, universeChunkSize).Error; err != nil {
			return err
		}
		if len(scoreRows) > 0 {
			return tx.CreateInBatches(scoreRows, universeChunkSize).Error
		}
		return nil
	}); err != nil {
		return result, err
	}
	published, err := coordinator.publish(ctx, batch, len(universeRows))
	if err != nil {
		return result, err
	}
	result.BatchID, result.PriceSnapshotID, result.MarketCapUSD, result.Grade = published.BatchID, price.ID, replacement.MarketCapUSD, replacement.Grade
	return result, nil
}

func scoreCandidateForMarketCap(ctx context.Context, db *gorm.DB, securityBatchID, marketBatchID string, row UniverseSnapshot, marketCap int64, now time.Time) (CandidateScoreSnapshot, error) {
	var metric FinancialMetricSnapshot
	if err := db.WithContext(ctx).Where("batch_id = ? AND security_id = ?", securityBatchID, row.SecurityID).First(&metric).Error; err != nil {
		return CandidateScoreSnapshot{}, err
	}
	var insiders []InsiderTransactionSnapshot
	if err := db.WithContext(ctx).Where("security_id = ?", row.SecurityID).Find(&insiders).Error; err != nil {
		return CandidateScoreSnapshot{}, err
	}
	var risks []CapitalRiskSnapshot
	if err := db.WithContext(ctx).Where("batch_id = ? AND security_id = ?", securityBatchID, row.SecurityID).Find(&risks).Error; err != nil {
		return CandidateScoreSnapshot{}, err
	}
	var identity SecurityBatchIdentity
	if err := db.WithContext(ctx).Where("batch_id = ? AND security_id = ?", securityBatchID, row.SecurityID).First(&identity).Error; err != nil {
		return CandidateScoreSnapshot{}, err
	}
	businessModels, err := activeCandidateBusinessModels(ctx, db, []uint{row.SecurityID})
	if err != nil {
		return CandidateScoreSnapshot{}, err
	}
	sector := SectorRatingForSIC(identity.SIC)
	var override *CandidateBusinessModelOverride
	if value, ok := businessModels[row.SecurityID]; ok {
		override = &value
	}
	score := ScoreDiscoveryCandidate(DiscoveryScoreInput{
		SecurityID: row.SecurityID, Ticker: row.Ticker, MarketCapUSD: marketCap, Financial: metric,
		Insiders: insiders, Risks: risks, GrossMarginPct: metric.GrossMarginPct, SectorScore: sector.Score,
		BusinessModel: candidateBusinessModelEvidence(override, sector.Category == "生物医药"), AsOf: now,
	})
	return CandidateScoreToSnapshot(marketBatchID, score, now), nil
}
