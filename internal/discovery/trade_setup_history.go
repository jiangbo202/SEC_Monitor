package discovery

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"
)

// RecordTradeSetupStatusTransitions captures the latest daily-close plan for
// each supplied symbol. Repeated syncs in the same status do not add rows:
// the most recent event remains the start of the ongoing state.
func RecordTradeSetupStatusTransitions(ctx context.Context, db *gorm.DB, tickers []string, recordedAt time.Time) (int, error) {
	if db == nil || ctx == nil {
		return 0, errors.New("database and context are required")
	}
	symbols := normalizeTickerFilter(tickers)
	if len(symbols) == 0 {
		return 0, nil
	}
	if recordedAt.IsZero() {
		recordedAt = time.Now().UTC()
	}
	recordedAt = recordedAt.UTC()

	var latest []TradeSetupStatusEvent
	if err := db.WithContext(ctx).Where("ticker IN ?", symbols).Order("ticker ASC, started_at DESC, id DESC").Find(&latest).Error; err != nil {
		return 0, err
	}
	latestByTicker := make(map[string]TradeSetupStatusEvent, len(symbols))
	for _, item := range latest {
		if _, exists := latestByTicker[item.Ticker]; !exists {
			latestByTicker[item.Ticker] = item
		}
	}

	securityIDs, err := tradeSetupSecurityIDs(ctx, db, symbols)
	if err != nil {
		return 0, err
	}
	events := make([]TradeSetupStatusEvent, 0, len(symbols))
	for _, ticker := range symbols {
		rows, err := technicalPriceHistoryForSymbol(ctx, db, ticker, "", technicalDetailHistoryDays, nil)
		if err != nil {
			return 0, err
		}
		technical := buildCandidateTechnicalAnalysis(rows)
		setup := technical.TradeSetup
		if previous, exists := latestByTicker[ticker]; exists && previous.Status == setup.Status {
			continue
		}
		reasons, err := json.Marshal(setup.Reasons)
		if err != nil {
			return 0, err
		}
		startedAt := tradeSetupStartedAt(technical.TradeDate, recordedAt)
		previousStatus := ""
		if previous, exists := latestByTicker[ticker]; exists {
			previousStatus = previous.Status
		}
		events = append(events, TradeSetupStatusEvent{
			SecurityID: securityIDs[ticker], Ticker: ticker, TradeDate: technical.TradeDate,
			Status: setup.Status, PreviousStatus: previousStatus, Bias: setup.Bias,
			EntryTrigger: setup.EntryTrigger, ExitReason: setup.ExitReason, ReasonsJSON: string(reasons),
			CloseUSD: technical.CloseUSD, StopLossUSD: setup.StopLossUSD, RiskPct: setup.RiskPct,
			TakeProfitZoneLowUSD: setup.TakeProfitZoneLowUSD, TakeProfitZoneHighUSD: setup.TakeProfitZoneHighUSD,
			StartedAt: startedAt, RecordedAt: recordedAt,
		})
	}
	if len(events) == 0 {
		return 0, nil
	}
	if err := db.WithContext(ctx).Create(&events).Error; err != nil {
		return 0, err
	}
	return len(events), nil
}

func tradeSetupSecurityIDs(ctx context.Context, db *gorm.DB, tickers []string) (map[string]uint, error) {
	var rows []Listing
	if err := db.WithContext(ctx).Where("ticker IN ?", tickers).Order("ticker ASC, valid_from DESC, id DESC").Find(&rows).Error; err != nil {
		return nil, err
	}
	result := make(map[string]uint, len(rows))
	for _, row := range rows {
		ticker := strings.ToUpper(strings.TrimSpace(row.Ticker))
		if _, exists := result[ticker]; !exists {
			result[ticker] = row.SecurityID
		}
	}
	return result, nil
}

func tradeSetupStartedAt(tradeDate string, fallback time.Time) time.Time {
	if parsed, err := time.Parse(time.DateOnly, strings.TrimSpace(tradeDate)); err == nil {
		return parsed.UTC()
	}
	return fallback.UTC()
}

// GetTradeSetupStatusHistory is local/read-only and returns newest first.
func GetTradeSetupStatusHistory(ctx context.Context, db *gorm.DB, ticker string, limit int) ([]TradeSetupStatusEvent, error) {
	if db == nil || ctx == nil {
		return nil, errors.New("database and context are required")
	}
	ticker = strings.ToUpper(strings.TrimSpace(ticker))
	if ticker == "" {
		return nil, errors.New("ticker is required")
	}
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	result := []TradeSetupStatusEvent{}
	if err := db.WithContext(ctx).Where("ticker = ?", ticker).Order("started_at DESC, id DESC").Limit(limit).Find(&result).Error; err != nil {
		return nil, err
	}
	for index := range result {
		result[index].Reasons = TradeSetupReasons(result[index])
	}
	return result, nil
}

func hydrateTradeSetupStatusSince(ctx context.Context, db *gorm.DB, items []CandidateScoreResult) error {
	tickers := make([]string, 0, len(items))
	for _, item := range items {
		if ticker := strings.ToUpper(strings.TrimSpace(item.Ticker)); ticker != "" {
			tickers = append(tickers, ticker)
		}
	}
	tickers = normalizeTickerFilter(tickers)
	if len(tickers) == 0 {
		return nil
	}
	var rows []TradeSetupStatusEvent
	if err := db.WithContext(ctx).Where("ticker IN ?", tickers).Order("ticker ASC, started_at DESC, id DESC").Find(&rows).Error; err != nil {
		return err
	}
	latest := make(map[string]TradeSetupStatusEvent, len(tickers))
	for _, row := range rows {
		if _, exists := latest[row.Ticker]; !exists {
			latest[row.Ticker] = row
		}
	}
	for index := range items {
		ticker := strings.ToUpper(strings.TrimSpace(items[index].Ticker))
		if row, exists := latest[ticker]; exists && row.Status == items[index].Technical.TradeSetup.Status {
			startedAt := row.StartedAt
			items[index].Technical.TradeSetup.StatusSince = &startedAt
		}
	}
	return nil
}

func hydrateTickerTradeSetupStatusSince(ctx context.Context, db *gorm.DB, ticker string, technical *CandidateTechnicalAnalysis) error {
	if technical == nil {
		return nil
	}
	history, err := GetTradeSetupStatusHistory(ctx, db, ticker, 1)
	if err != nil || len(history) == 0 || history[0].Status != technical.TradeSetup.Status {
		return err
	}
	startedAt := history[0].StartedAt
	technical.TradeSetup.StatusSince = &startedAt
	return nil
}

// TradeSetupReasons keeps the API payload easy to consume while preserving
// compact JSON in SQLite.
func TradeSetupReasons(event TradeSetupStatusEvent) []string {
	result := []string{}
	_ = json.Unmarshal([]byte(event.ReasonsJSON), &result)
	return result
}
