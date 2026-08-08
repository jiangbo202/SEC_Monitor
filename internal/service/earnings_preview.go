package service

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"sec_monitor/internal/config"
	"sec_monitor/internal/discovery"
	"sec_monitor/internal/model"
	"sec_monitor/internal/telegram"

	lbcalendar "github.com/longbridge/openapi-go/calendar"
	lbconfig "github.com/longbridge/openapi-go/config"
	lbfundamental "github.com/longbridge/openapi-go/fundamental"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	earningsPreviewProvider          = "longbridge"
	earningsPreviewStatusScheduled   = "scheduled"
	earningsPreviewStatusNoCoverage  = "no_coverage"
	earningsPreviewStatusUnavailable = "unavailable"
)

// EarningsPreviewSettings is deliberately small and stored in system config so
// an operator can tune provider load and reminders without a restart.
type EarningsPreviewSettings struct {
	Enabled          bool
	LookaheadDays    int
	MaxCalendarPages int
	NotifyEnabled    bool
	ReminderDays     []int
}

type EarningsPreviewView struct {
	Preview *model.EarningsPreview `json:"preview,omitempty"`
	Message string                 `json:"message"`
}

type EarningsPreviewRefreshResult struct {
	TargetID uint                  `json:"target_id"`
	Preview  model.EarningsPreview `json:"preview"`
	Fetched  bool                  `json:"fetched"`
	Changed  bool                  `json:"changed"`
	Message  string                `json:"message"`
}

type EarningsPreviewSyncResult struct {
	TargetCount int      `json:"target_count"`
	Matched     int      `json:"matched"`
	Fetched     int      `json:"fetched"`
	Changed     int      `json:"changed"`
	NoCoverage  int      `json:"no_coverage"`
	Failed      int      `json:"failed"`
	Notified    int      `json:"notified"`
	Warnings    []string `json:"warnings"`
	Skipped     bool     `json:"skipped"`
	Message     string   `json:"message"`
}

type longbridgeEarningsClient interface {
	FinanceCalendar(context.Context, lbcalendar.CalendarCategory, string, string, *string) (*lbcalendar.CalendarEventsResponse, error)
	Consensus(context.Context, string) (*lbfundamental.FinancialConsensus, error)
}

type EarningsPreviewService struct {
	db          *gorm.DB
	discoveryDB *gorm.DB
	runtime     config.DiscoveryConfig
	configs     *ConfigService
	notifier    telegram.Notifier
	now         func() time.Time
	newClient   func(string, string, string) (longbridgeEarningsClient, error)
}

func NewEarningsPreviewService(db *gorm.DB, runtime config.DiscoveryConfig, configs *ConfigService, notifier telegram.Notifier) *EarningsPreviewService {
	return &EarningsPreviewService{db: db, runtime: runtime, configs: configs, notifier: notifier, now: time.Now, newClient: newLongbridgeEarningsClient}
}

func (s *EarningsPreviewService) WithDiscoveryDB(db *gorm.DB) *EarningsPreviewService {
	s.discoveryDB = db
	return s
}

func (s *EarningsPreviewService) UpcomingCandidateTickers(ctx context.Context) ([]string, error) {
	var tickers []string
	if s == nil || s.db == nil {
		return tickers, errors.New("earnings preview service is not configured")
	}
	if err := s.db.WithContext(ctx).Model(&model.CandidateEarningsPreview{}).Where("status = ? AND report_at >= ?", earningsPreviewStatusScheduled, dateAtUTC(s.now())).Order("report_at ASC, ticker ASC").Pluck("ticker", &tickers).Error; err != nil {
		return nil, err
	}
	return tickers, nil
}

// SyncCurrentCandidates uses one Longbridge calendar scan for the active A/B
// universe. It intentionally does not request per-ticker consensus data: the
// candidate filter only needs a confirmed future reporting date.
func (s *EarningsPreviewService) SyncCurrentCandidates(ctx context.Context) (int, error) {
	if s == nil || s.db == nil || s.discoveryDB == nil {
		return 0, errors.New("candidate earnings sync service is not configured")
	}
	tickers, err := discovery.CurrentCandidateTickers(ctx, s.discoveryDB)
	if err != nil {
		return 0, err
	}
	if len(tickers) == 0 {
		return 0, nil
	}
	settings, err := s.Settings(ctx)
	if err != nil {
		return 0, err
	}
	if !settings.Enabled {
		return 0, nil
	}
	cfg, err := s.discoveryConfig(ctx)
	if err != nil {
		return 0, err
	}
	if strings.TrimSpace(cfg.LongbridgeAppKey) == "" || strings.TrimSpace(cfg.LongbridgeAppSecret) == "" || strings.TrimSpace(cfg.LongbridgeAccessToken) == "" {
		return 0, nil
	}
	client, err := s.newClient(cfg.LongbridgeAppKey, cfg.LongbridgeAppSecret, cfg.LongbridgeAccessToken)
	if err != nil {
		return 0, err
	}
	now := s.now().UTC()
	events, _, err := loadLongbridgeEarningsEvents(ctx, client, now, settings.LookaheadDays, settings.MaxCalendarPages)
	if err != nil {
		return 0, err
	}
	matched := nearestEarningsEvents(events, now)
	tickerSet := map[string]bool{}
	for _, ticker := range tickers {
		tickerSet[normalizeEarningsTicker(ticker)] = true
	}
	count := 0
	for ticker, event := range matched {
		if !tickerSet[ticker] {
			continue
		}
		entry := model.CandidateEarningsPreview{Ticker: ticker, Provider: earningsPreviewProvider, Status: earningsPreviewStatusScheduled, EventKey: event.ID, ReportAt: &event.ReportAt, Session: event.Session, FetchedAt: &now}
		if entry.EventKey == "" {
			entry.EventKey = ticker + ":" + event.ReportAt.Format(time.DateOnly)
		}
		if err := s.db.WithContext(ctx).Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "ticker"}}, DoUpdates: clause.AssignmentColumns([]string{"provider", "status", "event_key", "report_at", "session", "fetched_at", "last_error", "updated_at"})}).Create(&entry).Error; err != nil {
			return count, err
		}
		count++
	}
	return count, nil
}

func (s *EarningsPreviewService) Settings(ctx context.Context) (EarningsPreviewSettings, error) {
	settings := EarningsPreviewSettings{Enabled: true, LookaheadDays: 90, MaxCalendarPages: 20, NotifyEnabled: false, ReminderDays: []int{7, 3, 1, 0}}
	if s == nil || s.configs == nil {
		return settings, nil
	}
	values := []struct {
		key string
		set func(string)
	}{
		{"earnings_preview.enabled", func(value string) { settings.Enabled = configBool(value, settings.Enabled) }},
		{"earnings_preview.lookahead_days", func(value string) { settings.LookaheadDays = configBoundedInt(value, settings.LookaheadDays, 7, 180) }},
		{"earnings_preview.max_calendar_pages", func(value string) {
			settings.MaxCalendarPages = configBoundedInt(value, settings.MaxCalendarPages, 1, 100)
		}},
		{"earnings_preview.notify_enabled", func(value string) { settings.NotifyEnabled = configBool(value, settings.NotifyEnabled) }},
		{"earnings_preview.reminder_days", func(value string) { settings.ReminderDays = parseReminderDays(value, settings.ReminderDays) }},
	}
	for _, item := range values {
		value, found, err := s.configs.GetValue(ctx, item.key)
		if err != nil {
			return settings, err
		}
		if found {
			item.set(value)
		}
	}
	return settings, nil
}

func configBool(value string, fallback bool) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "true", "1", "yes", "on":
		return true
	case "false", "0", "no", "off":
		return false
	default:
		return fallback
	}
}

func configBoundedInt(value string, fallback, min, max int) int {
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || parsed < min || parsed > max {
		return fallback
	}
	return parsed
}

func parseReminderDays(value string, fallback []int) []int {
	seen := map[int]bool{}
	result := make([]int, 0, 4)
	for _, item := range strings.Split(value, ",") {
		day, err := strconv.Atoi(strings.TrimSpace(item))
		if err != nil || day < 0 || day > 60 || seen[day] {
			continue
		}
		seen[day] = true
		result = append(result, day)
	}
	if len(result) == 0 {
		return append([]int(nil), fallback...)
	}
	sort.Sort(sort.Reverse(sort.IntSlice(result)))
	return result
}

func (s *EarningsPreviewService) Get(ctx context.Context, targetID uint) (EarningsPreviewView, error) {
	if s == nil || s.db == nil {
		return EarningsPreviewView{}, errors.New("earnings preview service is not configured")
	}
	var preview model.EarningsPreview
	err := s.db.WithContext(ctx).Where("target_id = ?", targetID).First(&preview).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return EarningsPreviewView{Message: "尚未同步财报预告；可手动刷新当前标的。"}, nil
	}
	if err != nil {
		return EarningsPreviewView{}, err
	}
	return EarningsPreviewView{Preview: &preview, Message: earningsPreviewMessage(preview)}, nil
}

func (s *EarningsPreviewService) List(ctx context.Context) ([]model.EarningsPreview, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("earnings preview service is not configured")
	}
	var previews []model.EarningsPreview
	if err := s.db.WithContext(ctx).Order("report_at ASC, target_id ASC").Find(&previews).Error; err != nil {
		return nil, err
	}
	return previews, nil
}

// RefreshTarget refreshes exactly one enabled stock. It still reads the
// provider calendar once for the configured market/date window, then requests
// consensus only for the selected target. It never starts SEC or price sync.
func (s *EarningsPreviewService) RefreshTarget(ctx context.Context, targetID uint) (EarningsPreviewRefreshResult, error) {
	if s == nil || s.db == nil {
		return EarningsPreviewRefreshResult{}, errors.New("earnings preview service is not configured")
	}
	var target model.WatchTarget
	if err := s.db.WithContext(ctx).First(&target, targetID).Error; err != nil {
		return EarningsPreviewRefreshResult{}, mapNotFound(err)
	}
	if !strings.EqualFold(target.TargetType, "stock") {
		return EarningsPreviewRefreshResult{}, fmt.Errorf("%w: ETF/fund targets do not have company earnings previews", ErrValidation)
	}
	result, err := s.syncTargets(ctx, []model.WatchTarget{target}, true)
	if err != nil {
		return EarningsPreviewRefreshResult{}, err
	}
	view, err := s.Get(ctx, target.ID)
	if err != nil {
		return EarningsPreviewRefreshResult{}, err
	}
	if view.Preview == nil {
		return EarningsPreviewRefreshResult{TargetID: target.ID, Message: result.Message}, nil
	}
	return EarningsPreviewRefreshResult{TargetID: target.ID, Preview: *view.Preview, Fetched: result.Fetched > 0, Changed: result.Changed > 0, Message: view.Message}, nil
}

// SyncEnabled updates enabled equity watch targets. A provider outage is
// retained as local error state; existing good previews remain readable.
func (s *EarningsPreviewService) SyncEnabled(ctx context.Context) (EarningsPreviewSyncResult, error) {
	if s == nil || s.db == nil {
		return EarningsPreviewSyncResult{}, errors.New("earnings preview service is not configured")
	}
	var targets []model.WatchTarget
	if err := s.db.WithContext(ctx).Where("status = ? AND target_type = ?", "enabled", "stock").Order("ticker ASC").Find(&targets).Error; err != nil {
		return EarningsPreviewSyncResult{}, err
	}
	return s.syncTargets(ctx, targets, false)
}

func (s *EarningsPreviewService) syncTargets(ctx context.Context, targets []model.WatchTarget, force bool) (EarningsPreviewSyncResult, error) {
	result := EarningsPreviewSyncResult{TargetCount: len(targets), Warnings: []string{}}
	settings, err := s.Settings(ctx)
	if err != nil {
		return result, err
	}
	if !settings.Enabled {
		result.Skipped, result.Message = true, "财报预告同步已关闭"
		return result, nil
	}
	if len(targets) == 0 {
		result.Message = "没有已启用的股票监控标的"
		return result, nil
	}
	cfg, err := s.discoveryConfig(ctx)
	if err != nil {
		return result, err
	}
	if strings.TrimSpace(cfg.LongbridgeAppKey) == "" || strings.TrimSpace(cfg.LongbridgeAppSecret) == "" || strings.TrimSpace(cfg.LongbridgeAccessToken) == "" {
		result.Skipped, result.Message = true, "Longbridge 凭据未配置，已跳过财报预告同步"
		return result, nil
	}
	client, err := s.newClient(cfg.LongbridgeAppKey, cfg.LongbridgeAppSecret, cfg.LongbridgeAccessToken)
	if err != nil {
		return result, fmt.Errorf("create Longbridge earnings client: %w", err)
	}
	now := s.now().UTC()
	events, complete, err := loadLongbridgeEarningsEvents(ctx, client, now, settings.LookaheadDays, settings.MaxCalendarPages)
	if err != nil {
		for _, target := range targets {
			_ = s.recordProviderError(ctx, target, err, now)
		}
		return result, fmt.Errorf("load Longbridge earnings calendar: %w", err)
	}
	if !complete {
		result.Warnings = append(result.Warnings, "Longbridge 财报日历分页达到本次上限；未匹配标的保留原有缓存，下一次同步会继续刷新。")
	}
	byTicker := nearestEarningsEvents(events, now)
	changedPreviews := make([]model.EarningsPreview, 0)
	for _, target := range targets {
		event, found := byTicker[normalizeEarningsTicker(target.Ticker)]
		if !found {
			if complete {
				preview, changed, saveErr := s.saveNoCoverage(ctx, target, now)
				if saveErr != nil {
					result.Failed++
					result.Warnings = append(result.Warnings, fmt.Sprintf("%s：保存无覆盖状态失败：%v", target.Ticker, saveErr))
					continue
				}
				result.NoCoverage++
				if changed {
					result.Changed++
					changedPreviews = append(changedPreviews, preview)
				}
			}
			continue
		}
		result.Matched++
		consensus, consensusErr := client.Consensus(ctx, normalizeEarningsTicker(target.Ticker)+".US")
		if consensusErr != nil {
			result.Warnings = append(result.Warnings, fmt.Sprintf("%s：财务共识暂不可用：%v", target.Ticker, SanitizeSensitiveError(consensusErr.Error())))
		}
		preview := previewFromLongbridgeEvent(target, event, consensus, now)
		saved, changed, saveErr := s.savePreview(ctx, preview, force)
		if saveErr != nil {
			result.Failed++
			result.Warnings = append(result.Warnings, fmt.Sprintf("%s：保存财报预告失败：%v", target.Ticker, saveErr))
			continue
		}
		result.Fetched++
		if changed {
			result.Changed++
			changedPreviews = append(changedPreviews, saved)
		}
	}
	if sent, notifyErr := s.deliverNotifications(ctx, settings, changedPreviews); notifyErr != nil {
		result.Warnings = append(result.Warnings, "财报预告通知暂未发送："+SanitizeSensitiveError(notifyErr.Error()))
	} else {
		result.Notified = sent
	}
	if result.Message == "" {
		result.Message = fmt.Sprintf("已更新 %d/%d 个股票监控标的的财报预告", result.Fetched, result.TargetCount)
	}
	return result, nil
}

func (s *EarningsPreviewService) discoveryConfig(ctx context.Context) (config.DiscoveryConfig, error) {
	if s.configs == nil {
		return s.runtime, nil
	}
	return s.configs.ApplyDiscoveryConfig(ctx, s.runtime)
}

func (s *EarningsPreviewService) recordProviderError(ctx context.Context, target model.WatchTarget, sourceErr error, now time.Time) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var existing model.EarningsPreview
		err := tx.Where("target_id = ?", target.ID).First(&existing).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return tx.Create(&model.EarningsPreview{TargetID: target.ID, Ticker: normalizeEarningsTicker(target.Ticker), CompanyName: target.CompanyName, Provider: earningsPreviewProvider, Status: earningsPreviewStatusUnavailable, LastError: SanitizeSensitiveError(sourceErr.Error()), FetchedAt: &now}).Error
		}
		if err != nil {
			return err
		}
		return tx.Model(&existing).Updates(map[string]any{"last_error": SanitizeSensitiveError(sourceErr.Error()), "fetched_at": &now, "updated_at": now}).Error
	})
}

func (s *EarningsPreviewService) saveNoCoverage(ctx context.Context, target model.WatchTarget, now time.Time) (model.EarningsPreview, bool, error) {
	preview := model.EarningsPreview{TargetID: target.ID, Ticker: normalizeEarningsTicker(target.Ticker), CompanyName: target.CompanyName, Provider: earningsPreviewProvider, Status: earningsPreviewStatusNoCoverage, FetchedAt: &now}
	return s.savePreview(ctx, preview, false)
}

func (s *EarningsPreviewService) savePreview(ctx context.Context, next model.EarningsPreview, force bool) (model.EarningsPreview, bool, error) {
	var saved model.EarningsPreview
	changed := false
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var previous model.EarningsPreview
		err := tx.Where("target_id = ?", next.TargetID).First(&previous).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			next.ChangeSummary = ""
			if err := tx.Create(&next).Error; err != nil {
				return err
			}
			saved = next
			return nil
		}
		if err != nil {
			return err
		}
		next.ID, next.CreatedAt = previous.ID, previous.CreatedAt
		summary := earningsPreviewChangeSummary(previous, next)
		changed = summary != ""
		if changed {
			at := s.now().UTC()
			next.ChangedAt, next.ChangeSummary = &at, summary
		} else {
			next.ChangedAt, next.ChangeSummary = previous.ChangedAt, previous.ChangeSummary
		}
		if !force && previous.Status == earningsPreviewStatusScheduled && next.Status == earningsPreviewStatusNoCoverage {
			// A complete calendar scan may still lag a provider correction. Retain
			// the last scheduled event until it has passed instead of erasing a
			// useful, recently confirmed date.
			if previous.ReportAt != nil && previous.ReportAt.After(s.now().UTC().AddDate(0, 0, -2)) {
				previous.FetchedAt = next.FetchedAt
				previous.LastError = "Longbridge 本次未返回该标的；保留最近财报预告缓存，等待下一次确认。"
				if err := tx.Save(&previous).Error; err != nil {
					return err
				}
				saved, changed = previous, false
				return nil
			}
		}
		// Save the value carrying the original primary key instead of updating a
		// selected struct through the previous model.  Besides being clearer, this
		// preserves pointer fields (for example a cleared estimate) correctly on
		// SQLite and avoids accidentally trying to update the primary key.
		if err := tx.Save(&next).Error; err != nil {
			return err
		}
		if err := tx.First(&saved, previous.ID).Error; err != nil {
			return err
		}
		return nil
	})
	return saved, changed, err
}

func earningsPreviewChangeSummary(before, after model.EarningsPreview) string {
	changes := make([]string, 0, 4)
	if !samePreviewDate(before.ReportAt, after.ReportAt) {
		changes = append(changes, fmt.Sprintf("财报日期：%s → %s", previewDateText(before.ReportAt), previewDateText(after.ReportAt)))
	}
	if strings.TrimSpace(before.Session) != strings.TrimSpace(after.Session) {
		changes = append(changes, fmt.Sprintf("发布时段：%s → %s", displayOrDash(before.Session), displayOrDash(after.Session)))
	}
	if !samePreviewNumber(before.EPSEstimate, after.EPSEstimate) {
		changes = append(changes, fmt.Sprintf("EPS 预期：%s → %s", previewNumberText(before.EPSEstimate), previewNumberText(after.EPSEstimate)))
	}
	if !samePreviewNumber(before.RevenueEstimate, after.RevenueEstimate) {
		changes = append(changes, fmt.Sprintf("收入预期：%s → %s", previewNumberText(before.RevenueEstimate), previewNumberText(after.RevenueEstimate)))
	}
	return strings.Join(changes, "；")
}

func samePreviewDate(left, right *time.Time) bool {
	if left == nil || right == nil {
		return left == right
	}
	return left.UTC().Format(time.DateOnly) == right.UTC().Format(time.DateOnly)
}

func samePreviewNumber(left, right *float64) bool {
	if left == nil || right == nil {
		return left == right
	}
	return math.Abs(*left-*right) < 0.0000001
}

func previewDateText(value *time.Time) string {
	if value == nil {
		return "未提供"
	}
	return value.UTC().Format("2006-01-02")
}

func previewNumberText(value *float64) string {
	if value == nil {
		return "未提供"
	}
	return strconv.FormatFloat(*value, 'f', 4, 64)
}

func displayOrDash(value string) string {
	if strings.TrimSpace(value) == "" {
		return "未提供"
	}
	return value
}

func earningsPreviewMessage(preview model.EarningsPreview) string {
	switch preview.Status {
	case earningsPreviewStatusScheduled:
		return "数据来源：Longbridge 财报日历与财务共识（本地缓存）。"
	case earningsPreviewStatusNoCoverage:
		return "Longbridge 当前未返回该标的的未来财报日；这不表示公司不会发布财报。"
	case earningsPreviewStatusUnavailable:
		return "最近一次提供方请求未完成；已保留此前成功缓存（如有）。"
	default:
		return "尚未获得财报预告。"
	}
}

type earningsCalendarEvent struct {
	ID       string
	Ticker   string
	ReportAt time.Time
	Session  string
	Content  string
	Currency string
}

func loadLongbridgeEarningsEvents(ctx context.Context, client longbridgeEarningsClient, now time.Time, lookaheadDays, maxPages int) ([]earningsCalendarEvent, bool, error) {
	start, end := now.Format(time.DateOnly), now.AddDate(0, 0, lookaheadDays).Format(time.DateOnly)
	market := "US"
	events := make([]earningsCalendarEvent, 0)
	seenPages := map[string]bool{}
	for page := 0; page < maxPages; page++ {
		response, err := client.FinanceCalendar(ctx, lbcalendar.CalendarCategoryReport, start, end, &market)
		if err != nil {
			return nil, false, err
		}
		for _, group := range response.List {
			for _, item := range group.Infos {
				if ticker := normalizeEarningsTicker(item.Symbol); ticker != "" {
					reportAt, ok := parseLongbridgeEventDate(item.Date, item.Datetime, group.Date)
					if !ok {
						continue
					}
					events = append(events, earningsCalendarEvent{ID: strings.TrimSpace(item.ID), Ticker: ticker, ReportAt: reportAt, Session: firstNonEmpty(item.FinancialMarketTime, item.DateType), Content: item.Content, Currency: item.Currency})
				}
			}
		}
		next := strings.TrimSpace(response.NextDate)
		if next == "" || next > end || seenPages[next] {
			return events, true, nil
		}
		seenPages[next] = true
		start = next
	}
	return events, false, nil
}

func normalizeEarningsTicker(value string) string {
	value = strings.ToUpper(strings.TrimSpace(value))
	return strings.TrimSuffix(value, ".US")
}

func parseLongbridgeEventDate(values ...string) (time.Time, bool) {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		for _, layout := range []string{time.DateOnly, "2006.01.02", time.RFC3339} {
			if parsed, err := time.Parse(layout, value); err == nil {
				return parsed.UTC(), true
			}
		}
		if seconds, err := strconv.ParseInt(value, 10, 64); err == nil && seconds > 1_000_000_000 {
			return time.Unix(seconds, 0).UTC(), true
		}
	}
	return time.Time{}, false
}

func nearestEarningsEvents(events []earningsCalendarEvent, now time.Time) map[string]earningsCalendarEvent {
	result := map[string]earningsCalendarEvent{}
	for _, event := range events {
		if event.ReportAt.Before(time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)) {
			continue
		}
		current, exists := result[event.Ticker]
		if !exists || event.ReportAt.Before(current.ReportAt) {
			result[event.Ticker] = event
		}
	}
	return result
}

func previewFromLongbridgeEvent(target model.WatchTarget, event earningsCalendarEvent, consensus *lbfundamental.FinancialConsensus, now time.Time) model.EarningsPreview {
	reportAt := event.ReportAt.UTC()
	preview := model.EarningsPreview{TargetID: target.ID, Ticker: normalizeEarningsTicker(target.Ticker), CompanyName: target.CompanyName, Provider: earningsPreviewProvider, Status: earningsPreviewStatusScheduled, EventKey: event.ID, ReportAt: &reportAt, Session: event.Session, EventContent: event.Content, Currency: event.Currency, ProviderUpdatedAt: &now, FetchedAt: &now}
	if preview.EventKey == "" {
		preview.EventKey = fmt.Sprintf("%s:%s", preview.Ticker, reportAt.Format(time.DateOnly))
	}
	if consensus == nil {
		return preview
	}
	if strings.TrimSpace(preview.Currency) == "" {
		preview.Currency = consensus.Currency
	}
	if report, ok := nextConsensusReport(consensus); ok {
		preview.FiscalYear, preview.FiscalPeriod = int(report.FiscalYear), report.FiscalPeriod
		for _, detail := range report.Details {
			key := strings.ToLower(strings.TrimSpace(detail.Key))
			switch {
			case strings.Contains(key, "eps"):
				preview.EPSEstimate, preview.EPSActual = decimalToFloat(detail.Estimate), decimalToFloat(detail.Actual)
				preview.EPSSurprise = decimalToFloat(detail.CompValue)
			case strings.Contains(key, "revenue") || strings.Contains(key, "sales"):
				preview.RevenueEstimate, preview.RevenueActual = decimalToFloat(detail.Estimate), decimalToFloat(detail.Actual)
				preview.RevenueSurprise = decimalToFloat(detail.CompValue)
			}
		}
	}
	return preview
}

func nextConsensusReport(consensus *lbfundamental.FinancialConsensus) (lbfundamental.ConsensusReport, bool) {
	if consensus == nil {
		return lbfundamental.ConsensusReport{}, false
	}
	for _, report := range consensus.List {
		for _, detail := range report.Details {
			if !detail.IsReleased && detail.Estimate != nil {
				return report, true
			}
		}
	}
	return lbfundamental.ConsensusReport{}, false
}

func decimalToFloat(value *decimal.Decimal) *float64 {
	if value == nil {
		return nil
	}
	converted := value.InexactFloat64()
	return &converted
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func (s *EarningsPreviewService) deliverNotifications(ctx context.Context, settings EarningsPreviewSettings, changed []model.EarningsPreview) (int, error) {
	if !settings.NotifyEnabled || s.notifier == nil || s.configs == nil {
		return 0, nil
	}
	telegramConfig, err := s.configs.Telegram(ctx)
	if err != nil {
		return 0, err
	}
	if !telegramConfig.Enabled || telegramConfig.BotToken == "" || telegramConfig.ChatID == "" {
		return 0, nil
	}
	var previews []model.EarningsPreview
	if err := s.db.WithContext(ctx).Where("status = ? AND report_at IS NOT NULL", earningsPreviewStatusScheduled).Find(&previews).Error; err != nil {
		return 0, err
	}
	changedByID := make(map[uint]model.EarningsPreview, len(changed))
	for _, preview := range changed {
		if strings.TrimSpace(preview.ChangeSummary) != "" {
			changedByID[preview.TargetID] = preview
		}
	}
	notices := make([]pendingEarningsNotice, 0)
	now := s.now().UTC()
	for _, preview := range previews {
		if preview.ReportAt == nil || strings.TrimSpace(preview.EventKey) == "" {
			continue
		}
		if changedPreview, ok := changedByID[preview.TargetID]; ok {
			notices = append(notices, pendingEarningsNotice{Preview: changedPreview, Key: "changed", Text: "财报预告已更新：" + changedPreview.ChangeSummary})
		}
		days := int(preview.ReportAt.UTC().Sub(dateAtUTC(now)).Hours() / 24)
		for _, reminder := range settings.ReminderDays {
			if days == reminder {
				notices = append(notices, pendingEarningsNotice{Preview: preview, Key: fmt.Sprintf("reminder_%d", reminder), Text: earningsReminderText(preview, reminder)})
			}
		}
	}
	if len(notices) == 0 {
		return 0, nil
	}
	candidates := make([]NotificationCandidate, 0, len(notices))
	created := make([]model.EarningsPreviewNotice, 0, len(notices))
	for _, notice := range notices {
		stored := model.EarningsPreviewNotice{TargetID: notice.Preview.TargetID, EventKey: notice.Preview.EventKey, NoticeKey: notice.Key, Status: "pending"}
		result := s.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&stored)
		if result.Error != nil {
			return 0, result.Error
		}
		if result.RowsAffected == 0 {
			continue
		}
		created = append(created, stored)
		candidates = append(candidates, NotificationCandidate{EntityKind: "earnings_preview", FilingID: fmt.Sprintf("earnings-preview:%d", stored.ID), TargetID: notice.Preview.TargetID, Ticker: notice.Preview.Ticker, CompanyName: notice.Preview.CompanyName, FilingType: "earnings_preview", Title: notice.Text, Reason: "eligible", EventAt: *notice.Preview.ReportAt})
	}
	if len(candidates) == 0 {
		return 0, nil
	}
	batch, err := NewNotificationBatchService(s.db, s.notifier, s.configs).Deliver(ctx, NotificationBatchInput{Source: "earnings_preview", Trigger: "scheduler", Candidates: candidates, SummaryText: renderEarningsPreviewNotification(candidates)})
	if err != nil {
		return 0, err
	}
	if batch.Status == "sent" || batch.Status == "failed" {
		status := batch.Status
		updates := map[string]any{"status": status}
		if status == "sent" {
			sentAt := s.now().UTC()
			updates["sent_at"] = &sentAt
		}
		ids := make([]uint, 0, len(created))
		for _, item := range created {
			ids = append(ids, item.ID)
		}
		if err := s.db.WithContext(ctx).Model(&model.EarningsPreviewNotice{}).Where("id IN ?", ids).Updates(updates).Error; err != nil {
			return 0, err
		}
	}
	if batch.Status == "sent" {
		return len(candidates), nil
	}
	return 0, nil
}

type pendingEarningsNotice struct {
	Preview model.EarningsPreview
	Key     string
	Text    string
}

func dateAtUTC(value time.Time) time.Time {
	return time.Date(value.UTC().Year(), value.UTC().Month(), value.UTC().Day(), 0, 0, 0, 0, time.UTC)
}

func earningsReminderText(preview model.EarningsPreview, days int) string {
	when := previewDateText(preview.ReportAt)
	if days == 0 {
		return fmt.Sprintf("今日预计披露财报（%s%s）", when, earningsSessionSuffix(preview.Session))
	}
	return fmt.Sprintf("距预计财报还有 %d 天（%s%s）", days, when, earningsSessionSuffix(preview.Session))
}

func earningsSessionSuffix(session string) string {
	if strings.TrimSpace(session) == "" {
		return ""
	}
	return "，" + session
}

func renderEarningsPreviewNotification(candidates []NotificationCandidate) string {
	lines := []string{"财报预告提醒（Longbridge）："}
	for _, candidate := range candidates {
		lines = append(lines, fmt.Sprintf("- %s｜%s", candidate.Ticker, candidate.Title))
	}
	return strings.Join(lines, "\n")
}

type longbridgeEarningsSDKClient struct {
	calendar    *lbcalendar.CalendarContext
	fundamental *lbfundamental.FundamentalContext
}

func newLongbridgeEarningsClient(appKey, appSecret, accessToken string) (longbridgeEarningsClient, error) {
	cfg, err := lbconfig.New(lbconfig.WithConfigKey(appKey, appSecret, accessToken))
	if err != nil {
		return nil, err
	}
	calendarClient, err := lbcalendar.NewFromCfg(cfg)
	if err != nil {
		return nil, err
	}
	fundamentalClient, err := lbfundamental.NewFromCfg(cfg)
	if err != nil {
		return nil, err
	}
	return &longbridgeEarningsSDKClient{calendar: calendarClient, fundamental: fundamentalClient}, nil
}

func (c *longbridgeEarningsSDKClient) FinanceCalendar(ctx context.Context, category lbcalendar.CalendarCategory, start, end string, market *string) (*lbcalendar.CalendarEventsResponse, error) {
	return c.calendar.FinanceCalendar(ctx, category, start, end, market)
}

func (c *longbridgeEarningsSDKClient) Consensus(ctx context.Context, symbol string) (*lbfundamental.FinancialConsensus, error) {
	return c.fundamental.Consensus(ctx, symbol)
}
