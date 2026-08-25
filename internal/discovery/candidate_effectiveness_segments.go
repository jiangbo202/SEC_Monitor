package discovery

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"
)

func hydrateCandidateEffectivenessDimensions(ctx context.Context, db *gorm.DB, seeds []candidateCohortSeed) error {
	if len(seeds) == 0 {
		return nil
	}
	batchIDs, securityIDs, tickers := []string{}, []uint{}, []string{"IWM"}
	seenBatch, seenSecurity, seenTicker := map[string]bool{}, map[uint]bool{}, map[string]bool{"IWM": true}
	for _, seed := range seeds {
		if !seenBatch[seed.BatchID] {
			seenBatch[seed.BatchID] = true
			batchIDs = append(batchIDs, seed.BatchID)
		}
		if !seenSecurity[seed.SecurityID] {
			seenSecurity[seed.SecurityID] = true
			securityIDs = append(securityIDs, seed.SecurityID)
		}
		ticker := strings.ToUpper(strings.TrimSpace(seed.Ticker))
		if ticker != "" && !seenTicker[ticker] {
			seenTicker[ticker] = true
			tickers = append(tickers, ticker)
		}
	}
	var snapshots []CandidateScoreSnapshot
	if err := db.WithContext(ctx).Where("batch_id IN ? AND security_id IN ?", batchIDs, securityIDs).Find(&snapshots).Error; err != nil {
		return err
	}
	scoreByKey := map[string]CandidateScoreSnapshot{}
	for _, score := range snapshots {
		scoreByKey[effectivenessSeedKey(score.BatchID, score.SecurityID)] = score
	}
	var batches []UniverseBatch
	if err := db.WithContext(ctx).Where("batch_id IN ?", batchIDs).Find(&batches).Error; err != nil {
		return err
	}
	securityBatchByMarketBatch := map[string]string{}
	securityBatchIDs := []string{}
	seenIdentityBatch := map[string]bool{}
	for _, batch := range batches {
		securityBatchID := strings.TrimSpace(batch.UniverseSourceVersion)
		securityBatchByMarketBatch[batch.BatchID] = securityBatchID
		if securityBatchID != "" && !seenIdentityBatch[securityBatchID] {
			seenIdentityBatch[securityBatchID] = true
			securityBatchIDs = append(securityBatchIDs, securityBatchID)
		}
	}
	identityByKey := map[string]SecurityBatchIdentity{}
	if len(securityBatchIDs) > 0 {
		var identities []SecurityBatchIdentity
		if err := db.WithContext(ctx).Where("batch_id IN ? AND security_id IN ?", securityBatchIDs, securityIDs).Find(&identities).Error; err != nil {
			return err
		}
		for _, identity := range identities {
			identityByKey[effectivenessSeedKey(identity.BatchID, identity.SecurityID)] = identity
		}
	}
	var securities []Security
	if err := db.WithContext(ctx).Where("id IN ?", securityIDs).Find(&securities).Error; err != nil {
		return err
	}
	securityByID := map[uint]Security{}
	for _, security := range securities {
		securityByID[security.ID] = security
	}
	var prices []PriceSnapshot
	if err := db.WithContext(ctx).Where("symbol IN ? AND quality_status = ? AND close_micros > 0", tickers, QualityStatusValid).Order("trade_date ASC, id ASC").Find(&prices).Error; err != nil {
		return err
	}
	pricesByTicker := map[string][]PriceSnapshot{}
	for _, price := range prices {
		ticker := strings.ToUpper(strings.TrimSpace(price.Symbol))
		pricesByTicker[ticker] = append(pricesByTicker[ticker], price)
	}
	for index := range seeds {
		seed := &seeds[index]
		if full, ok := scoreByKey[effectivenessSeedKey(seed.BatchID, seed.SecurityID)]; ok {
			seed.CandidateScoreSnapshot = full
		}
		seed.MarketCapBucket = candidateMarketCapBucket(seed.MarketCapUSD)
		seed.SectorCategory = "未分类"
		securityBatchID := securityBatchByMarketBatch[seed.BatchID]
		if identity, ok := identityByKey[effectivenessSeedKey(securityBatchID, seed.SecurityID)]; ok {
			seed.SectorCategory = stringOrDefault(sectorCategory(identity.SIC), "未分类")
		} else if security, ok := securityByID[seed.SecurityID]; ok {
			seed.SectorCategory = stringOrDefault(sectorCategory(security.SIC), "未分类")
		}
		prior := pointInTimePriceWindow(pricesByTicker[strings.ToUpper(seed.Ticker)], seed.BaselineDate, 20)
		seed.LiquidityBucket = candidateLiquidityBucket(prior)
		seed.MarketRegime = candidateMarketRegime(pointInTimePriceWindow(pricesByTicker["IWM"], seed.BaselineDate, 21))
	}
	return nil
}

func effectivenessSeedKey(batchID string, securityID uint) string {
	return fmt.Sprintf("%s\x00%d", batchID, securityID)
}

func pointInTimePriceWindow(rows []PriceSnapshot, asOf time.Time, limit int) []PriceSnapshot {
	selected := make([]PriceSnapshot, 0, limit)
	for index := len(rows) - 1; index >= 0 && len(selected) < limit; index-- {
		if rows[index].TradeDate.After(asOf) {
			continue
		}
		selected = append(selected, rows[index])
	}
	for left, right := 0, len(selected)-1; left < right; left, right = left+1, right-1 {
		selected[left], selected[right] = selected[right], selected[left]
	}
	return selected
}

func candidateMarketCapBucket(value int64) string {
	switch {
	case value <= 0:
		return "未知"
	case value < 100_000_000:
		return "<$100M"
	case value < 300_000_000:
		return "$100M–$300M"
	case value < 500_000_000:
		return "$300M–$500M"
	default:
		return "$500M–$1B"
	}
}

func candidateLiquidityBucket(rows []PriceSnapshot) string {
	if len(rows) == 0 {
		return "未知"
	}
	total := 0.0
	for _, row := range rows {
		total += float64(row.CloseMicros) / 1_000_000 * float64(row.Volume)
	}
	average := total / float64(len(rows))
	switch {
	case average < 100_000:
		return "<$100K ADV"
	case average < 500_000:
		return "$100K–$500K ADV"
	case average < 5_000_000:
		return "$500K–$5M ADV"
	default:
		return "≥$5M ADV"
	}
}

func candidateMarketRegime(rows []PriceSnapshot) string {
	if len(rows) < 21 {
		return "样本不足"
	}
	first, last := rows[0].CloseMicros, rows[len(rows)-1].CloseMicros
	if first <= 0 || last <= 0 {
		return "样本不足"
	}
	change := float64(last-first) / float64(first) * 100
	switch {
	case change >= 3:
		return "IWM 上行"
	case change <= -3:
		return "IWM 下行"
	default:
		return "IWM 震荡"
	}
}

func buildCandidateEffectivenessSegments(ctx context.Context, db *gorm.DB, seeds []candidateCohortSeed, benchmark string) ([]CandidateEffectivenessSegment, error) {
	dimensions := []struct {
		name   string
		bucket func(candidateCohortSeed) string
	}{
		{"market_cap", func(seed candidateCohortSeed) string { return seed.MarketCapBucket }},
		{"sector", func(seed candidateCohortSeed) string { return seed.SectorCategory }},
		{"liquidity", func(seed candidateCohortSeed) string { return seed.LiquidityBucket }},
		{"market_regime", func(seed candidateCohortSeed) string { return seed.MarketRegime }},
		{"signal_type", func(seed candidateCohortSeed) string { return seed.EventType }},
	}
	result := []CandidateEffectivenessSegment{}
	for _, dimension := range dimensions {
		groups := map[string][]candidateCohortSeed{}
		for _, seed := range seeds {
			bucket := stringOrDefault(dimension.bucket(seed), "未知")
			groups[bucket] = append(groups[bucket], seed)
		}
		buckets := make([]string, 0, len(groups))
		for bucket := range groups {
			buckets = append(buckets, bucket)
		}
		sort.Strings(buckets)
		for _, bucket := range buckets {
			windows, _, err := buildCandidateCohortWindows(ctx, db, groups[bucket], benchmark)
			if err != nil {
				return nil, err
			}
			window := CandidateEffectivenessWindow{HorizonDays: 20, VerificationStatus: "unverified"}
			for _, item := range windows {
				if item.HorizonDays == 20 {
					window = item
					break
				}
			}
			result = append(result, CandidateEffectivenessSegment{Dimension: dimension.name, Bucket: bucket, CandidateCount: len(groups[bucket]), Window20: window})
		}
	}
	return result, nil
}
