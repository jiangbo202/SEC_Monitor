package discovery

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"
)

const CandidateWatchStatusActive = "active"
const CandidateWatchStatusArchived = "archived"

const (
	CandidateResearchStatusInbox       = "inbox"
	CandidateResearchStatusResearching = "researching"
	CandidateResearchStatusConviction  = "conviction"
	CandidateResearchStatusRejected    = "rejected"
)

type CandidateWatchInput struct {
	Ticker              string     `json:"ticker"`
	Note                string     `json:"note"`
	Status              string     `json:"status"`
	ResearchStatus      *string    `json:"research_status"`
	Thesis              *string    `json:"thesis"`
	RiskNotes           *string    `json:"risk_notes"`
	Invalidation        *string    `json:"invalidation"`
	MarketConcern       *string    `json:"market_concern"`
	FalsifiableJudgment *string    `json:"falsifiable_judgment"`
	Catalyst            *string    `json:"catalyst"`
	CatalystSource      *string    `json:"catalyst_source"`
	CatalystDate        *time.Time `json:"catalyst_date"`
	ClearCatalystDate   bool       `json:"clear_catalyst_date"`
	NextReviewAt        *time.Time `json:"next_review_at"`
	ClearNextReviewAt   bool       `json:"clear_next_review_at"`
	// CaptureBaseline is an explicit recovery action for watches created before
	// baseline snapshots were introduced. It never replaces an existing
	// baseline, so the original research decision remains auditable.
	CaptureBaseline bool   `json:"capture_baseline"`
	MemoAuthor      string `json:"memo_author"`
}

type CandidateWatchQuery struct {
	Page, PageSize int
	Ticker         string
	Status         string
}

type CandidateWatchPage struct {
	Page     int                    `json:"page"`
	PageSize int                    `json:"page_size"`
	Total    int64                  `json:"total"`
	Items    []CandidateWatchResult `json:"items"`
}

type CandidateWatchResult struct {
	CandidateWatch
	LatestScore   *CandidateScoreResult         `json:"latest_score,omitempty"`
	Baseline      *CandidateWatchMetricSnapshot `json:"baseline,omitempty"`
	Current       *CandidateWatchMetricSnapshot `json:"current,omitempty"`
	MetricChanges CandidateWatchMetricChanges   `json:"metric_changes"`
}

// CandidateWatchMetricSnapshot is the compact, immutable research baseline
// captured when a candidate is first added to the follow list. Values are
// sourced from the same published small-cap batch shown in the candidate
// table, rather than from a separate live quote request.
type CandidateWatchMetricSnapshot struct {
	BatchID            string     `json:"batch_id"`
	ScoreEffectiveDate string     `json:"score_effective_date"`
	CapturedAt         time.Time  `json:"captured_at"`
	PriceCloseUSD      float64    `json:"price_close_usd"`
	PriceVolume        int64      `json:"price_volume"`
	PriceTradeDate     *time.Time `json:"price_trade_date,omitempty"`
	PriceSource        string     `json:"price_source,omitempty"`
	MarketCapUSD       int64      `json:"market_cap_usd"`
	TotalScore         int        `json:"total_score"`
	Grade              string     `json:"grade"`
	RevenueGrowthPct   float64    `json:"revenue_growth_pct"`
	CashRunwayMonths   float64    `json:"cash_runway_months"`
	QualityTier        string     `json:"quality_tier"`
	ResearchReadiness  string     `json:"research_readiness"`
	SectorCategory     string     `json:"sector_category"`
}

// CandidateWatchMetricChanges keeps percentage changes only where a valid
// non-zero baseline exists. Score, growth and runway are absolute changes so
// the UI never presents percentage-point metrics as misleading percentages.
type CandidateWatchMetricChanges struct {
	PriceChangePct         *float64 `json:"price_change_pct,omitempty"`
	MarketCapChangePct     *float64 `json:"market_cap_change_pct,omitempty"`
	VolumeChangePct        *float64 `json:"volume_change_pct,omitempty"`
	ScoreChange            *int     `json:"score_change,omitempty"`
	RevenueGrowthChangePct *float64 `json:"revenue_growth_change_pct,omitempty"`
	CashRunwayChangeMonths *float64 `json:"cash_runway_change_months,omitempty"`
}

// CandidateReviewQueue is a local, user-maintained research to-do list.  It is
// deliberately independent from the recommendation workflow: an exited or
// currently unscored watch still needs a review when the user set a due date.
type CandidateReviewQueue struct {
	AsOf          string                     `json:"as_of"`
	OverdueCount  int                        `json:"overdue_count"`
	DueTodayCount int                        `json:"due_today_count"`
	UpcomingCount int                        `json:"upcoming_count"`
	Items         []CandidateReviewQueueItem `json:"items"`
}

type CandidateReviewQueueItem struct {
	CandidateWatch
	LatestScore      *CandidateScoreResult `json:"latest_score,omitempty"`
	ReviewState      string                `json:"review_state"`
	DaysUntilReview  int                   `json:"days_until_review"`
	CurrentCandidate bool                  `json:"current_candidate"`
}

func ListCandidateWatches(ctx context.Context, db *gorm.DB, filter CandidateWatchQuery) (CandidateWatchPage, error) {
	page, size, err := normalizePage(filter.Page, filter.PageSize)
	if err != nil {
		return CandidateWatchPage{}, err
	}
	result := CandidateWatchPage{Page: page, PageSize: size, Items: []CandidateWatchResult{}}
	if db == nil {
		return result, errors.New("database is required")
	}
	query := db.WithContext(ctx).Model(&CandidateWatch{})
	if ticker := normalizeTicker(filter.Ticker); ticker != "" {
		query = query.Where("ticker = ?", ticker)
	}
	status := normalizeCandidateWatchStatus(filter.Status)
	if status == "" {
		status = CandidateWatchStatusActive
	}
	if status != "all" {
		query = query.Where("status = ?", status)
	}
	if err := query.Count(&result.Total).Error; err != nil {
		return result, err
	}
	var watches []CandidateWatch
	if err := query.Order("updated_at DESC").Order("ticker ASC").Offset((page - 1) * size).Limit(size).Find(&watches).Error; err != nil {
		return result, err
	}
	result.Items = make([]CandidateWatchResult, 0, len(watches))
	for _, watch := range watches {
		result.Items = append(result.Items, CandidateWatchResult{CandidateWatch: watch})
	}
	if err := attachLatestCandidateScores(ctx, db, result.Items); err != nil {
		return result, err
	}
	attachCandidateWatchMetricComparisons(result.Items)
	return result, nil
}

// ListCandidateReviewQueue returns active watches due today or in the next
// seven calendar days. The caller supplies now in the configured scheduler
// timezone so a review date retains the same meaning in the UI and scheduler.
func ListCandidateReviewQueue(ctx context.Context, db *gorm.DB, now time.Time) (CandidateReviewQueue, error) {
	location := now.Location()
	if location == nil {
		location = time.UTC
	}
	localNow := now.In(location)
	// Review dates come from a date picker and are persisted at UTC midnight.
	// Keep them date-only rather than converting that timestamp into the
	// scheduler timezone (which would move a US review date to the prior day).
	today := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 0, 0, 0, 0, time.UTC)
	result := CandidateReviewQueue{AsOf: today.Format(time.DateOnly), Items: []CandidateReviewQueueItem{}}
	if db == nil {
		return result, errors.New("database is required")
	}

	// Include today and the following seven calendar days. The exclusive upper
	// bound makes this safe for a time-valued database field while the UI only
	// exposes a date picker.
	until := today.AddDate(0, 0, 8)
	var watches []CandidateWatch
	if err := db.WithContext(ctx).
		Where("status = ? AND next_review_at IS NOT NULL AND next_review_at < ?", CandidateWatchStatusActive, until).
		Order("next_review_at ASC").Order("ticker ASC").
		Find(&watches).Error; err != nil {
		return result, err
	}
	if len(watches) == 0 {
		return result, nil
	}

	watchResults := make([]CandidateWatchResult, 0, len(watches))
	for _, watch := range watches {
		watchResults = append(watchResults, CandidateWatchResult{CandidateWatch: watch})
	}
	if err := attachLatestCandidateScores(ctx, db, watchResults); err != nil {
		return result, err
	}
	for _, watch := range watchResults {
		if watch.NextReviewAt == nil {
			continue
		}
		reviewDay := dateOnlyUTC(*watch.NextReviewAt)
		days := int(reviewDay.Sub(today).Hours() / 24)
		state := "upcoming"
		switch {
		case days < 0:
			state = "overdue"
			result.OverdueCount++
		case days == 0:
			state = "due_today"
			result.DueTodayCount++
		default:
			result.UpcomingCount++
		}
		result.Items = append(result.Items, CandidateReviewQueueItem{
			CandidateWatch: watch.CandidateWatch, LatestScore: watch.LatestScore,
			ReviewState: state, DaysUntilReview: days, CurrentCandidate: watch.LatestScore != nil,
		})
	}
	return result, nil
}

func dateOnlyUTC(value time.Time) time.Time {
	return time.Date(value.UTC().Year(), value.UTC().Month(), value.UTC().Day(), 0, 0, 0, 0, time.UTC)
}

func attachLatestCandidateScores(ctx context.Context, db *gorm.DB, items []CandidateWatchResult) error {
	if len(items) == 0 {
		return nil
	}
	batch, ok, err := currentPublishedPrescreenBatch(ctx, db)
	if err != nil || !ok {
		return err
	}
	tickers := make([]string, 0, len(items))
	for _, item := range items {
		tickers = append(tickers, item.Ticker)
	}
	var scores []CandidateScoreSnapshot
	if err := db.WithContext(ctx).Where("batch_id = ? AND ticker IN ?", batch.BatchID, tickers).Find(&scores).Error; err != nil {
		return err
	}
	if len(scores) == 0 {
		return nil
	}
	scoreItems, err := hydrateCandidateSectorEvidence(ctx, db, batch.UniverseSourceVersion, scores)
	if err != nil {
		return err
	}
	if scoreItems, err = hydrateCandidateRevenueGrowthEvidence(ctx, db, batch.UniverseSourceVersion, scoreItems); err != nil {
		return err
	}
	if scoreItems, err = hydrateCandidatePriceEvidence(ctx, db, batch, scoreItems); err != nil {
		return err
	}
	riskBatchID := strings.TrimSpace(batch.UniverseSourceVersion)
	if riskBatchID == "" {
		riskBatchID = batch.BatchID
	}
	if scoreItems, err = hydrateCandidateCapitalRiskSummaries(ctx, db, riskBatchID, scoreItems); err != nil {
		return err
	}
	if scoreItems, err = annotateCandidateChanges(ctx, db, batch, scoreItems); err != nil {
		return err
	}
	annotateCandidateQuality(scoreItems)
	if err = hydrateCandidatePerformance(ctx, db, scoreItems); err != nil {
		return err
	}
	byTicker := map[string]CandidateScoreResult{}
	for _, score := range scoreItems {
		byTicker[score.Ticker] = score
	}
	for i := range items {
		if score, ok := byTicker[items[i].Ticker]; ok {
			scoreCopy := score
			items[i].LatestScore = &scoreCopy
		}
	}
	return nil
}

func UpsertCandidateWatch(ctx context.Context, db *gorm.DB, input CandidateWatchInput) (CandidateWatch, error) {
	if db == nil {
		return CandidateWatch{}, errors.New("database is required")
	}
	ticker := normalizeTicker(input.Ticker)
	if ticker == "" {
		return CandidateWatch{}, errors.New("ticker is required")
	}
	status, err := validatedCandidateWatchStatus(input.Status, CandidateWatchStatusActive)
	if err != nil {
		return CandidateWatch{}, err
	}
	researchStatus, err := validatedCandidateResearchStatus(input.ResearchStatus, CandidateResearchStatusInbox)
	if err != nil {
		return CandidateWatch{}, err
	}
	watch := CandidateWatch{Ticker: ticker, Status: status, Note: strings.TrimSpace(input.Note), ResearchStatus: researchStatus, UpdatedAt: time.Now().UTC()}
	applyCandidateWatchResearchInput(&watch, input)
	var baselineScore *CandidateScoreResult
	if score, ok, err := currentCandidateScoreByTicker(ctx, db, ticker); err != nil {
		return CandidateWatch{}, err
	} else if ok {
		watch.SecurityID = score.SecurityID
		watch.SourceBatchID = score.BatchID
		var security Security
		if err := db.WithContext(ctx).First(&security, score.SecurityID).Error; err == nil {
			watch.CIK = security.CIK
			watch.CompanyName = security.CompanyName
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return CandidateWatch{}, err
		}
		baselineScore, err = currentCandidateWatchScoreResult(ctx, db, ticker)
		if err != nil {
			return CandidateWatch{}, err
		}
	}
	var existing CandidateWatch
	err = db.WithContext(ctx).First(&existing, "ticker = ?", ticker).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		if baselineScore != nil {
			capturedAt := time.Now().UTC()
			baseline := candidateWatchMetricSnapshot(*baselineScore, capturedAt)
			encoded, marshalErr := json.Marshal(baseline)
			if marshalErr != nil {
				return CandidateWatch{}, marshalErr
			}
			watch.BaselineBatchID = baseline.BatchID
			watch.BaselineCapturedAt = &capturedAt
			watch.BaselineJSON = string(encoded)
		}
		if err := db.WithContext(ctx).Create(&watch).Error; err != nil {
			return CandidateWatch{}, err
		}
		if hasCandidateResearchMemoChanges(input) {
			if err := appendCandidateResearchMemoVersion(ctx, db, watch, input.MemoAuthor); err != nil {
				return CandidateWatch{}, err
			}
		}
		return watch, nil
	}
	if err != nil {
		return CandidateWatch{}, err
	}
	updates := map[string]any{
		"security_id": watch.SecurityID, "cik": watch.CIK, "company_name": watch.CompanyName,
		"note": watch.Note, "source_batch_id": watch.SourceBatchID, "updated_at": watch.UpdatedAt,
	}
	if strings.TrimSpace(input.Status) != "" {
		updates["status"] = watch.Status
	}
	if input.ResearchStatus != nil {
		updates["research_status"] = watch.ResearchStatus
	}
	if input.Thesis != nil {
		updates["thesis"] = watch.Thesis
	}
	if input.RiskNotes != nil {
		updates["risk_notes"] = watch.RiskNotes
	}
	if input.Invalidation != nil {
		updates["invalidation"] = watch.Invalidation
	}
	if input.MarketConcern != nil {
		updates["market_concern"] = watch.MarketConcern
	}
	if input.FalsifiableJudgment != nil {
		updates["falsifiable_judgment"] = watch.FalsifiableJudgment
	}
	if input.Catalyst != nil {
		updates["catalyst"] = watch.Catalyst
	}
	if input.CatalystSource != nil {
		updates["catalyst_source"] = watch.CatalystSource
	}
	if input.ClearCatalystDate {
		updates["catalyst_date"] = nil
	} else if input.CatalystDate != nil {
		updates["catalyst_date"] = watch.CatalystDate
	}
	if input.ClearNextReviewAt {
		updates["next_review_at"] = nil
	} else if input.NextReviewAt != nil {
		updates["next_review_at"] = watch.NextReviewAt
	}
	if input.CaptureBaseline && strings.TrimSpace(existing.BaselineJSON) == "" {
		if baselineScore == nil {
			return CandidateWatch{}, errors.New("current candidate data is unavailable; cannot capture follow baseline")
		}
		capturedAt := time.Now().UTC()
		baseline := candidateWatchMetricSnapshot(*baselineScore, capturedAt)
		encoded, marshalErr := json.Marshal(baseline)
		if marshalErr != nil {
			return CandidateWatch{}, marshalErr
		}
		updates["baseline_batch_id"] = baseline.BatchID
		updates["baseline_captured_at"] = &capturedAt
		updates["baseline_json"] = string(encoded)
	}
	if err := db.WithContext(ctx).Model(&existing).Updates(updates).Error; err != nil {
		return CandidateWatch{}, err
	}
	if err := db.WithContext(ctx).First(&existing, existing.ID).Error; err != nil {
		return CandidateWatch{}, err
	}
	if hasCandidateResearchMemoChanges(input) {
		if err := appendCandidateResearchMemoVersion(ctx, db, existing, input.MemoAuthor); err != nil {
			return CandidateWatch{}, err
		}
	}
	return existing, nil
}

func hasCandidateResearchMemoChanges(input CandidateWatchInput) bool {
	return input.Thesis != nil || input.RiskNotes != nil || input.Invalidation != nil ||
		input.MarketConcern != nil || input.FalsifiableJudgment != nil || input.Catalyst != nil ||
		input.CatalystSource != nil || input.CatalystDate != nil || input.ClearCatalystDate ||
		input.NextReviewAt != nil || input.ClearNextReviewAt
}

func appendCandidateResearchMemoVersion(ctx context.Context, db *gorm.DB, watch CandidateWatch, author string) error {
	if db == nil {
		return errors.New("database is required")
	}
	author = strings.TrimSpace(author)
	if author == "" {
		author = "local_user"
	}
	var count int64
	if err := db.WithContext(ctx).Model(&CandidateResearchMemoVersion{}).Where("ticker = ?", watch.Ticker).Count(&count).Error; err != nil {
		return err
	}
	version := CandidateResearchMemoVersion{
		Ticker: watch.Ticker, Version: int(count) + 1, SecurityID: watch.SecurityID, Author: author,
		Thesis: watch.Thesis, MarketConcern: watch.MarketConcern, FalsifiableJudgment: watch.FalsifiableJudgment,
		Catalyst: watch.Catalyst, CatalystSource: watch.CatalystSource, CatalystDate: watch.CatalystDate,
		RiskNotes: watch.RiskNotes, Invalidation: watch.Invalidation, NextReviewAt: watch.NextReviewAt,
	}
	return db.WithContext(ctx).Create(&version).Error
}

func DeleteCandidateWatch(ctx context.Context, db *gorm.DB, id uint) error {
	if db == nil {
		return errors.New("database is required")
	}
	if id == 0 {
		return errors.New("id is required")
	}
	return db.WithContext(ctx).Delete(&CandidateWatch{}, id).Error
}

func currentCandidateScoreByTicker(ctx context.Context, db *gorm.DB, ticker string) (CandidateScoreSnapshot, bool, error) {
	batch, ok, err := currentPublishedPrescreenBatch(ctx, db)
	if err != nil || !ok {
		return CandidateScoreSnapshot{}, false, err
	}
	var score CandidateScoreSnapshot
	err = db.WithContext(ctx).First(&score, "batch_id = ? AND ticker = ?", batch.BatchID, ticker).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return CandidateScoreSnapshot{}, false, nil
	}
	if err != nil {
		return CandidateScoreSnapshot{}, false, err
	}
	return score, true, nil
}

func currentCandidateWatchScoreResult(ctx context.Context, db *gorm.DB, ticker string) (*CandidateScoreResult, error) {
	items := []CandidateWatchResult{{CandidateWatch: CandidateWatch{Ticker: ticker}}}
	if err := attachLatestCandidateScores(ctx, db, items); err != nil {
		return nil, err
	}
	return items[0].LatestScore, nil
}

func candidateWatchMetricSnapshot(score CandidateScoreResult, capturedAt time.Time) CandidateWatchMetricSnapshot {
	return CandidateWatchMetricSnapshot{
		BatchID:            score.BatchID,
		ScoreEffectiveDate: score.ScoreEffectiveDate,
		CapturedAt:         capturedAt.UTC(),
		PriceCloseUSD:      score.PriceCloseUSD,
		PriceVolume:        score.PriceVolume,
		PriceTradeDate:     score.PriceTradeDate,
		PriceSource:        score.PriceSource,
		MarketCapUSD:       score.MarketCapUSD,
		TotalScore:         score.TotalScore,
		Grade:              score.Grade,
		RevenueGrowthPct:   score.RevenueGrowthPct,
		CashRunwayMonths:   score.CashRunwayMonths,
		QualityTier:        score.QualityTier,
		ResearchReadiness:  score.ResearchReadiness.Status,
		SectorCategory:     score.SectorCategory,
	}
}

func attachCandidateWatchMetricComparisons(items []CandidateWatchResult) {
	for i := range items {
		watch := &items[i]
		if strings.TrimSpace(watch.BaselineJSON) != "" {
			var baseline CandidateWatchMetricSnapshot
			if json.Unmarshal([]byte(watch.BaselineJSON), &baseline) == nil {
				watch.Baseline = &baseline
			}
		}
		if watch.LatestScore == nil {
			continue
		}
		current := candidateWatchMetricSnapshot(*watch.LatestScore, time.Now().UTC())
		watch.Current = &current
		if watch.Baseline == nil {
			continue
		}
		baseline := watch.Baseline
		watch.MetricChanges.PriceChangePct = candidateWatchPercentChange(baseline.PriceCloseUSD, current.PriceCloseUSD)
		watch.MetricChanges.MarketCapChangePct = candidateWatchPercentChange(float64(baseline.MarketCapUSD), float64(current.MarketCapUSD))
		watch.MetricChanges.VolumeChangePct = candidateWatchPercentChange(float64(baseline.PriceVolume), float64(current.PriceVolume))
		scoreChange := current.TotalScore - baseline.TotalScore
		watch.MetricChanges.ScoreChange = &scoreChange
		revenueChange := current.RevenueGrowthPct - baseline.RevenueGrowthPct
		watch.MetricChanges.RevenueGrowthChangePct = &revenueChange
		runwayChange := current.CashRunwayMonths - baseline.CashRunwayMonths
		watch.MetricChanges.CashRunwayChangeMonths = &runwayChange
	}
}

func candidateWatchPercentChange(baseline, current float64) *float64 {
	if baseline <= 0 || current <= 0 {
		return nil
	}
	change := (current - baseline) / baseline * 100
	return &change
}

func normalizeTicker(ticker string) string {
	return strings.ToUpper(strings.TrimSpace(ticker))
}

func normalizeCandidateWatchStatus(status string) string {
	return strings.ToLower(strings.TrimSpace(status))
}

func validatedCandidateWatchStatus(input, fallback string) (string, error) {
	status := normalizeCandidateWatchStatus(input)
	if status == "" {
		return fallback, nil
	}
	if status != CandidateWatchStatusActive && status != CandidateWatchStatusArchived {
		return "", errors.New("invalid watch status")
	}
	return status, nil
}

func validatedCandidateResearchStatus(input *string, fallback string) (string, error) {
	if input == nil {
		return fallback, nil
	}
	status := strings.ToLower(strings.TrimSpace(*input))
	if status == "" {
		return fallback, nil
	}
	switch status {
	case CandidateResearchStatusInbox, CandidateResearchStatusResearching, CandidateResearchStatusConviction, CandidateResearchStatusRejected:
		return status, nil
	default:
		return "", errors.New("invalid research status")
	}
}

func applyCandidateWatchResearchInput(watch *CandidateWatch, input CandidateWatchInput) {
	if input.Thesis != nil {
		watch.Thesis = strings.TrimSpace(*input.Thesis)
	}
	if input.RiskNotes != nil {
		watch.RiskNotes = strings.TrimSpace(*input.RiskNotes)
	}
	if input.Invalidation != nil {
		watch.Invalidation = strings.TrimSpace(*input.Invalidation)
	}
	if input.MarketConcern != nil {
		watch.MarketConcern = strings.TrimSpace(*input.MarketConcern)
	}
	if input.FalsifiableJudgment != nil {
		watch.FalsifiableJudgment = strings.TrimSpace(*input.FalsifiableJudgment)
	}
	if input.Catalyst != nil {
		watch.Catalyst = strings.TrimSpace(*input.Catalyst)
	}
	if input.CatalystSource != nil {
		watch.CatalystSource = strings.TrimSpace(*input.CatalystSource)
	}
	if input.ClearCatalystDate {
		watch.CatalystDate = nil
	} else if input.CatalystDate != nil {
		catalystDate := input.CatalystDate.UTC()
		watch.CatalystDate = &catalystDate
	}
	if input.ClearNextReviewAt {
		watch.NextReviewAt = nil
	} else if input.NextReviewAt != nil {
		nextReview := input.NextReviewAt.UTC()
		watch.NextReviewAt = &nextReview
	}
}
