package discovery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"sec_monitor/internal/config"

	lbconfig "github.com/longbridge/openapi-go/config"
	lbfundamental "github.com/longbridge/openapi-go/fundamental"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const longbridgeAnalystRatingProvider = "longbridge"

const (
	AnalystRatingStatusAvailable  = "available"
	AnalystRatingStatusNoCoverage = "no_coverage"
)

// AnalystRatingView is the local/read-only shape used by both candidate and
// watch-target detail pages. Opening a detail page never calls a provider.
type AnalystRatingView struct {
	Latest  *AnalystRatingSnapshot  `json:"latest,omitempty"`
	History []AnalystRatingSnapshot `json:"history"`
	Message string                  `json:"message"`
	Quality DataQualityMetadata     `json:"quality"`
}

type AnalystRatingRefreshResult struct {
	Ticker        string                `json:"ticker"`
	Fetched       bool                  `json:"fetched"`
	Cached        bool                  `json:"cached"`
	Changed       bool                  `json:"changed"`
	ChangeSummary string                `json:"change_summary,omitempty"`
	Snapshot      AnalystRatingSnapshot `json:"snapshot"`
	Message       string                `json:"message"`
}

type AnalystRatingSyncResult struct {
	CandidateCount   int                     `json:"candidate_count"`
	WatchTargetCount int                     `json:"watch_target_count"`
	Attempted        int                     `json:"attempted"`
	Fetched          int                     `json:"fetched"`
	Cached           int                     `json:"cached"`
	Changed          int                     `json:"changed"`
	Failed           int                     `json:"failed"`
	Skipped          bool                    `json:"skipped"`
	Message          string                  `json:"message"`
	Changes          []AnalystRatingSnapshot `json:"changes"`
}

// PendingAnalystRatingNotifications returns semantic provider changes that
// were safely persisted but have not yet entered the normal notification
// delivery workflow. Keeping this query local makes notification recovery
// independent from Longbridge availability.
func PendingAnalystRatingNotifications(ctx context.Context, db *gorm.DB, limit int) ([]AnalystRatingSnapshot, error) {
	if db == nil {
		return nil, errors.New("database is required")
	}
	if limit <= 0 {
		limit = 100
	}
	var rows []AnalystRatingSnapshot
	if err := db.WithContext(ctx).
		Where("notification_status = ? AND change_summary <> ?", "pending", "").
		Order("fetched_at ASC, id ASC").
		Limit(limit).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

type longbridgeAnalystRatingClient interface {
	InstitutionRating(context.Context, string) (*lbfundamental.InstitutionRating, error)
}

type LongbridgeAnalystRatingOptions struct {
	AppKey          string
	AppSecret       string
	AccessToken     string
	TargetChangePct float64
	RequestInterval time.Duration
	Now             func() time.Time
	NewClient       func(string, string, string) (longbridgeAnalystRatingClient, error)
}

func NewLongbridgeAnalystRatingOptions(cfg config.DiscoveryConfig) LongbridgeAnalystRatingOptions {
	return LongbridgeAnalystRatingOptions{
		AppKey: cfg.LongbridgeAppKey, AppSecret: cfg.LongbridgeAppSecret, AccessToken: cfg.LongbridgeAccessToken,
		TargetChangePct: cfg.LongbridgeAnalystRatingTargetChangePct,
		RequestInterval: time.Duration(cfg.LongbridgeFundamentalRequestIntervalMS) * time.Millisecond,
	}
}

// GetAnalystRating returns only persisted observations. Missing coverage is
// represented as a normal local state so users can distinguish it from an
// unavailable provider.
func GetAnalystRating(ctx context.Context, db *gorm.DB, ticker string) (AnalystRatingView, error) {
	result := AnalystRatingView{History: []AnalystRatingSnapshot{}}
	if db == nil {
		return result, errors.New("database is required")
	}
	symbol := normalizeAnalystRatingTicker(ticker)
	if symbol == "" {
		return result, errors.New("ticker is required")
	}
	if err := db.WithContext(ctx).Where("provider = ? AND ticker = ?", longbridgeAnalystRatingProvider, symbol).Order("fetched_at DESC, id DESC").Limit(24).Find(&result.History).Error; err != nil {
		return result, err
	}
	if len(result.History) == 0 {
		result.Message = "尚未同步分析师共识；可点击“刷新分析师评级”仅获取该标的最新公开数据。"
		result.Quality = researchQualityMetadata(DataLayerFact, longbridgeAnalystRatingProvider, "", time.Time{}, 14*24*time.Hour, 45*24*time.Hour)
		return result, nil
	}
	result.Latest = &result.History[0]
	result.Quality = researchQualityMetadata(DataLayerFact, longbridgeAnalystRatingProvider, result.Latest.SnapshotHash, result.Latest.FetchedAt, 14*24*time.Hour, 45*24*time.Hour)
	if result.Latest.Status == AnalystRatingStatusNoCoverage {
		result.Message = "数据提供方当前暂无分析师覆盖；这在小盘和微盘股中较常见，不代表同步失败。"
	} else {
		result.Message = "数据来源：Longbridge 机构评级聚合共识。"
	}
	return result, nil
}

// RefreshLongbridgeAnalystRating fetches exactly one issuer's rating
// aggregate. It has no relationship to SEC or price-universe sync and is
// designed for an explicit detail-page button.
func RefreshLongbridgeAnalystRating(ctx context.Context, db *gorm.DB, cfg config.DiscoveryConfig, ticker, cik string) (AnalystRatingRefreshResult, error) {
	return refreshLongbridgeAnalystRating(ctx, db, ticker, cik, NewLongbridgeAnalystRatingOptions(cfg))
}

func refreshLongbridgeAnalystRating(ctx context.Context, db *gorm.DB, ticker, cik string, options LongbridgeAnalystRatingOptions) (AnalystRatingRefreshResult, error) {
	result := AnalystRatingRefreshResult{Ticker: normalizeAnalystRatingTicker(ticker)}
	if db == nil {
		return result, errors.New("database is required")
	}
	if result.Ticker == "" {
		return result, errors.New("ticker is required")
	}
	if strings.TrimSpace(options.AppKey) == "" || strings.TrimSpace(options.AppSecret) == "" || strings.TrimSpace(options.AccessToken) == "" {
		return result, errors.New("Longbridge app key, app secret, and access token are required")
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.NewClient == nil {
		options.NewClient = newLongbridgeAnalystRatingSDKClient
	}
	securityID := analystRatingSecurityID(ctx, db, result.Ticker, cik)
	client, err := options.NewClient(options.AppKey, options.AppSecret, options.AccessToken)
	if err != nil {
		return result, fmt.Errorf("create Longbridge analyst rating client: %w", err)
	}
	requestCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 25*time.Second)
	defer cancel()
	rating, err := longbridgeFundamentalCall(requestCtx, options.RequestInterval, func(callCtx context.Context) (*lbfundamental.InstitutionRating, error) {
		return client.InstitutionRating(callCtx, result.Ticker+".US")
	})
	if err != nil {
		return result, fmt.Errorf("load Longbridge institution rating: %w", err)
	}
	snapshot := analystRatingSnapshotFromLongbridge(result.Ticker, securityID, rating, options.Now().UTC())
	if snapshot.Status == AnalystRatingStatusAvailable && snapshot.AnalystCount == 0 {
		snapshot.Status = AnalystRatingStatusNoCoverage
	}
	previous, err := latestAnalystRatingSnapshot(ctx, db, snapshot.Provider, snapshot.Ticker)
	if err != nil {
		return result, err
	}
	if previous != nil && previous.SnapshotHash == snapshot.SnapshotHash {
		result.Cached = true
		result.Snapshot = *previous
		result.Message = "分析师共识与本地最新快照一致。"
		return result, nil
	}
	if previous != nil && previous.Status == AnalystRatingStatusAvailable && snapshot.Status == AnalystRatingStatusAvailable {
		snapshot.ChangeSummary = analystRatingChangeSummary(*previous, snapshot, options.TargetChangePct)
		if snapshot.ChangeSummary != "" {
			snapshot.NotificationStatus = "pending"
		}
	}
	if snapshot.NotificationStatus == "" {
		snapshot.NotificationStatus = "not_applicable"
	}
	if err := db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&snapshot).Error; err != nil {
		return result, fmt.Errorf("save analyst rating snapshot: %w", err)
	}
	if snapshot.ID == 0 {
		var saved AnalystRatingSnapshot
		if err := db.WithContext(ctx).Where("provider = ? AND ticker = ? AND snapshot_hash = ?", snapshot.Provider, snapshot.Ticker, snapshot.SnapshotHash).First(&saved).Error; err != nil {
			return result, err
		}
		snapshot = saved
	}
	result.Fetched = true
	result.Changed = snapshot.ChangeSummary != ""
	result.ChangeSummary = snapshot.ChangeSummary
	result.Snapshot = snapshot
	if snapshot.Status == AnalystRatingStatusNoCoverage {
		result.Message = "Longbridge 当前暂无该标的的分析师覆盖。"
	} else if result.Changed {
		result.Message = "已保存新的分析师共识快照，并标记为待通知变化。"
	} else {
		result.Message = "已保存最新分析师共识快照。"
	}
	return result, nil
}

// SyncCurrentCandidateLongbridgeAnalystRatings is deliberately budgeted. It
// scans the current score universe in descending score order and never blocks
// publishing a market batch if a provider has no coverage or is unavailable.
func SyncCurrentCandidateLongbridgeAnalystRatings(ctx context.Context, db *gorm.DB, cfg config.DiscoveryConfig) (AnalystRatingSyncResult, error) {
	result := AnalystRatingSyncResult{Changes: []AnalystRatingSnapshot{}}
	if db == nil {
		return result, errors.New("database is required")
	}
	if !cfg.LongbridgeAnalystRatingEnabled {
		result.Skipped, result.Message = true, "Longbridge 分析师评级同步已关闭"
		return result, nil
	}
	if cfg.LongbridgeAnalystRatingRequestBudget <= 0 {
		result.Skipped, result.Message = true, "Longbridge 分析师评级请求预算为 0，已跳过"
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
		return result, fmt.Errorf("load analyst rating candidate universe: %w", err)
	}
	result.CandidateCount = len(scores)
	latestByTicker := map[string]time.Time{}
	if len(scores) > 0 {
		tickers := make([]string, 0, len(scores))
		for _, score := range scores {
			if ticker := normalizeAnalystRatingTicker(score.Ticker); ticker != "" {
				tickers = append(tickers, ticker)
			}
		}
		var prior []AnalystRatingSnapshot
		if err := db.WithContext(ctx).Where("provider = ? AND ticker IN ?", longbridgeAnalystRatingProvider, tickers).Order("fetched_at DESC, id DESC").Find(&prior).Error; err != nil {
			return result, err
		}
		for _, item := range prior {
			if _, exists := latestByTicker[item.Ticker]; !exists {
				latestByTicker[item.Ticker] = item.FetchedAt
			}
		}
	}
	// Rotate the bounded budget through the oldest observations first. Without
	// this, low-scoring but still-active candidates would never receive a
	// refresh when the universe is larger than the provider request budget.
	sort.SliceStable(scores, func(left, right int) bool {
		leftAt, leftOK := latestByTicker[normalizeAnalystRatingTicker(scores[left].Ticker)]
		rightAt, rightOK := latestByTicker[normalizeAnalystRatingTicker(scores[right].Ticker)]
		if leftOK != rightOK {
			return !leftOK
		}
		if !leftAt.Equal(rightAt) {
			return leftAt.Before(rightAt)
		}
		if scores[left].TotalScore != scores[right].TotalScore {
			return scores[left].TotalScore > scores[right].TotalScore
		}
		return scores[left].Ticker < scores[right].Ticker
	})
	seen := map[string]struct{}{}
	for _, score := range scores {
		if result.Attempted >= cfg.LongbridgeAnalystRatingRequestBudget {
			break
		}
		ticker := normalizeAnalystRatingTicker(score.Ticker)
		if ticker == "" {
			continue
		}
		if _, duplicate := seen[ticker]; duplicate {
			continue
		}
		seen[ticker] = struct{}{}
		result.Attempted++
		refreshed, refreshErr := RefreshLongbridgeAnalystRating(ctx, db, cfg, ticker, "")
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
	result.Message = fmt.Sprintf("已同步 %d 个标的的分析师共识，变化 %d 个，失败 %d 个", result.Fetched, result.Changed, result.Failed)
	return result, nil
}

func latestAnalystRatingSnapshot(ctx context.Context, db *gorm.DB, provider, ticker string) (*AnalystRatingSnapshot, error) {
	var snapshot AnalystRatingSnapshot
	err := db.WithContext(ctx).Where("provider = ? AND ticker = ?", provider, ticker).Order("fetched_at DESC, id DESC").First(&snapshot).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &snapshot, nil
}

func analystRatingSecurityID(ctx context.Context, db *gorm.DB, ticker, cik string) uint {
	var security Security
	query := db.WithContext(ctx)
	if normalizedCIK := strings.TrimSpace(cik); normalizedCIK != "" {
		if err := query.Where("cik = ?", normalizedCIK).First(&security).Error; err == nil {
			return security.ID
		}
	} else if err := query.Joins("JOIN listings ON listings.security_id = securities.id").Where("listings.ticker = ?", ticker).First(&security).Error; err == nil {
		return security.ID
	}
	return 0
}

func analystRatingSnapshotFromLongbridge(ticker string, securityID uint, rating *lbfundamental.InstitutionRating, now time.Time) AnalystRatingSnapshot {
	snapshot := AnalystRatingSnapshot{SecurityID: securityID, Provider: longbridgeAnalystRatingProvider, Ticker: ticker, Status: AnalystRatingStatusNoCoverage, FetchedAt: now}
	if rating == nil {
		snapshot.SnapshotHash = analystRatingHash(snapshot)
		return snapshot
	}
	snapshot.Recommendation = analystRecommendationLabel(rating.Summary.Recommend)
	snapshot.StrongBuyCount = int(rating.Summary.Evaluate.StrongBuy)
	snapshot.BuyCount = int(rating.Summary.Evaluate.Buy)
	snapshot.HoldCount = int(rating.Summary.Evaluate.Hold)
	snapshot.UnderperformCount = int(rating.Summary.Evaluate.Under)
	snapshot.SellCount = int(rating.Summary.Evaluate.Sell)
	snapshot.NoOpinionCount = int(rating.Latest.Evaluate.NoOpinion)
	snapshot.AnalystCount = int(rating.Latest.Evaluate.Total)
	if snapshot.AnalystCount == 0 {
		snapshot.AnalystCount = snapshot.StrongBuyCount + snapshot.BuyCount + snapshot.HoldCount + snapshot.UnderperformCount + snapshot.SellCount + snapshot.NoOpinionCount
	}
	snapshot.TargetAverageMicros = decimalToMicros(rating.Summary.Target)
	snapshot.TargetHighMicros = decimalToMicros(rating.Latest.Target.HighestPrice)
	snapshot.TargetLowMicros = decimalToMicros(rating.Latest.Target.LowestPrice)
	snapshot.ReferencePriceMicros = decimalToMicros(rating.Latest.Target.PrevClose)
	snapshot.Currency = strings.TrimSpace(rating.Summary.CcySymbol)
	snapshot.ProviderUpdatedAtText = strings.TrimSpace(rating.Summary.UpdatedAt)
	if snapshot.AnalystCount > 0 || snapshot.TargetAverageMicros > 0 {
		snapshot.Status = AnalystRatingStatusAvailable
	}
	snapshot.SnapshotHash = analystRatingHash(snapshot)
	return snapshot
}

func decimalToMicros(value *decimal.Decimal) int64 {
	if value == nil {
		return 0
	}
	return value.Shift(6).IntPart()
}

func analystRecommendationLabel(value lbfundamental.InstitutionRecommend) string {
	switch value {
	case lbfundamental.InstitutionRecommendStrongBuy:
		return "strong_buy"
	case lbfundamental.InstitutionRecommendBuy:
		return "buy"
	case lbfundamental.InstitutionRecommendHold:
		return "hold"
	case lbfundamental.InstitutionRecommendSell:
		return "sell"
	case lbfundamental.InstitutionRecommendStrongSell:
		return "strong_sell"
	case lbfundamental.InstitutionRecommendUnderperform:
		return "underperform"
	case lbfundamental.InstitutionRecommendNoOpinion:
		return "no_opinion"
	default:
		return "unknown"
	}
}

func analystRatingHash(value AnalystRatingSnapshot) string {
	parts := []string{value.Provider, value.Ticker, value.Status, value.Recommendation, fmt.Sprintf("%d", value.StrongBuyCount), fmt.Sprintf("%d", value.BuyCount), fmt.Sprintf("%d", value.HoldCount), fmt.Sprintf("%d", value.UnderperformCount), fmt.Sprintf("%d", value.SellCount), fmt.Sprintf("%d", value.NoOpinionCount), fmt.Sprintf("%d", value.AnalystCount), fmt.Sprintf("%d", value.TargetAverageMicros), fmt.Sprintf("%d", value.TargetHighMicros), fmt.Sprintf("%d", value.TargetLowMicros), fmt.Sprintf("%d", value.ReferencePriceMicros), value.Currency}
	sum := sha256.Sum256([]byte(strings.Join(parts, "|")))
	return hex.EncodeToString(sum[:])
}

func analystRatingChangeSummary(previous, current AnalystRatingSnapshot, threshold float64) string {
	changes := []string{}
	if previous.Recommendation != current.Recommendation {
		changes = append(changes, fmt.Sprintf("共识评级 %s → %s", previous.Recommendation, current.Recommendation))
	}
	if previous.StrongBuyCount != current.StrongBuyCount || previous.BuyCount != current.BuyCount || previous.HoldCount != current.HoldCount || previous.UnderperformCount != current.UnderperformCount || previous.SellCount != current.SellCount || previous.AnalystCount != current.AnalystCount {
		changes = append(changes, fmt.Sprintf("评级覆盖 %d → %d", previous.AnalystCount, current.AnalystCount))
	}
	if previous.TargetAverageMicros > 0 && current.TargetAverageMicros > 0 {
		changePct := float64(current.TargetAverageMicros-previous.TargetAverageMicros) / float64(previous.TargetAverageMicros) * 100
		if absAnalystRating(changePct) >= threshold {
			changes = append(changes, fmt.Sprintf("平均目标价变动 %.1f%%", changePct))
		}
	}
	return strings.Join(changes, "；")
}

func absAnalystRating(value float64) float64 {
	if value < 0 {
		return -value
	}
	return value
}

func normalizeAnalystRatingTicker(value string) string {
	value = strings.ToUpper(strings.TrimSpace(value))
	value = strings.TrimSuffix(value, ".US")
	return value
}

type longbridgeAnalystRatingSDKClient struct {
	fundamental *lbfundamental.FundamentalContext
}

func newLongbridgeAnalystRatingSDKClient(appKey, appSecret, accessToken string) (longbridgeAnalystRatingClient, error) {
	cfg, err := lbconfig.New(lbconfig.WithConfigKey(appKey, appSecret, accessToken))
	if err != nil {
		return nil, err
	}
	client, err := lbfundamental.NewFromCfg(cfg)
	if err != nil {
		return nil, err
	}
	return &longbridgeAnalystRatingSDKClient{fundamental: client}, nil
}

func (c *longbridgeAnalystRatingSDKClient) InstitutionRating(ctx context.Context, symbol string) (*lbfundamental.InstitutionRating, error) {
	return c.fundamental.InstitutionRating(ctx, symbol)
}

// sortAnalystRatingSnapshotsNewest first is kept explicit for tests and API
// callers that compose rows from multiple local sources.
func sortAnalystRatingSnapshotsNewest(rows []AnalystRatingSnapshot) {
	sort.SliceStable(rows, func(left, right int) bool { return rows[left].FetchedAt.After(rows[right].FetchedAt) })
}
