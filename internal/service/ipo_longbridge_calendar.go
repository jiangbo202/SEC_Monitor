package service

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode"

	"sec_monitor/internal/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	lbcalendar "github.com/longbridge/openapi-go/calendar"
	lbconfig "github.com/longbridge/openapi-go/config"
)

const longbridgeIPOCalendarSource = "longbridge_ipo_calendar"

// longbridgeIPOCalendarEvent keeps the calendar integration independent from
// the SDK. The IPO calendar may contain a symbol before SEC exposes a CIK, so
// callers must treat it as discovery data rather than filing evidence.
type longbridgeIPOCalendarEvent struct {
	ID          string
	Symbol      string
	Market      string
	CompanyName string
	Date        string
	Session     string
	Content     string
	Currency    string
}

type longbridgeIPOCalendarPage struct {
	NextDate string
	Events   []longbridgeIPOCalendarEvent
}

type longbridgeIPOCalendarClient interface {
	FinanceCalendar(context.Context, string, string, string) (longbridgeIPOCalendarPage, error)
}

type longbridgeIPOCalendarSDKClient struct {
	calendar *lbcalendar.CalendarContext
}

func newLongbridgeIPOCalendarClient(appKey, appSecret, accessToken string) (longbridgeIPOCalendarClient, error) {
	cfg, err := lbconfig.New(lbconfig.WithConfigKey(appKey, appSecret, accessToken))
	if err != nil {
		return nil, err
	}
	client, err := lbcalendar.NewFromCfg(cfg)
	if err != nil {
		return nil, err
	}
	return &longbridgeIPOCalendarSDKClient{calendar: client}, nil
}

func (c *longbridgeIPOCalendarSDKClient) FinanceCalendar(ctx context.Context, start, end, market string) (longbridgeIPOCalendarPage, error) {
	response, err := c.calendar.FinanceCalendar(ctx, lbcalendar.CalendarCategoryIpo, start, end, &market)
	if err != nil {
		return longbridgeIPOCalendarPage{}, err
	}
	page := longbridgeIPOCalendarPage{NextDate: response.NextDate}
	for _, group := range response.List {
		for _, event := range group.Infos {
			date := strings.TrimSpace(event.Date)
			if date == "" {
				date = group.Date
			}
			page.Events = append(page.Events, longbridgeIPOCalendarEvent{
				ID: event.ID, Symbol: event.Symbol, Market: event.Market, CompanyName: event.CounterName,
				Date: date, Session: event.DateType, Content: event.Content, Currency: event.Currency,
			})
		}
	}
	return page, nil
}

type IPOCalendarEventFilter struct {
	CompanyName string
	Ticker      string
	Page        int
	PageSize    int
}

// ListCalendarEvents returns locally cached data only; reading the IPO radar
// must not consume Longbridge quota. A refresh performs the external query.
func (s *IPORadarService) ListCalendarEvents(ctx context.Context, filter IPOCalendarEventFilter) (PageResult[model.IPOCalendarEvent], error) {
	page, pageSize := normalizePage(filter.Page, filter.PageSize)
	query := s.db.WithContext(ctx).Model(&model.IPOCalendarEvent{})
	if companyName := strings.TrimSpace(filter.CompanyName); companyName != "" {
		query = query.Where("company_name LIKE ?", "%"+companyName+"%")
	}
	var events []model.IPOCalendarEvent
	if err := query.Order("event_date ASC, company_name ASC, id ASC").Find(&events).Error; err != nil {
		return PageResult[model.IPOCalendarEvent]{}, err
	}
	if ticker := normalizeIPOCompanyTicker(filter.Ticker); ticker != "" {
		filtered := events[:0]
		for _, event := range events {
			if normalizeIPOCompanyTicker(event.Symbol) == ticker {
				filtered = append(filtered, event)
			}
		}
		events = filtered
	}
	total := int64(len(events))
	start := (page - 1) * pageSize
	if start >= len(events) {
		return newPageResult([]model.IPOCalendarEvent{}, total, page, pageSize), nil
	}
	end := start + pageSize
	if end > len(events) {
		end = len(events)
	}
	return newPageResult(events[start:end], total, page, pageSize), nil
}

// syncLongbridgeIPOCalendar imports a bounded US IPO calendar window. An
// absent event is never used as evidence that a filing was withdrawn or that
// an issuer is already listed.
func (s *IPORadarService) syncLongbridgeIPOCalendar(ctx context.Context, settings IPORadarSettings) string {
	if !settings.LongbridgeIPOCalendarEnabled {
		return ""
	}
	cfg, err := s.longbridgeListingConfig(ctx)
	if err != nil {
		return "Longbridge IPO calendar configuration: " + err.Error()
	}
	if strings.TrimSpace(cfg.LongbridgeAppKey) == "" || strings.TrimSpace(cfg.LongbridgeAppSecret) == "" || strings.TrimSpace(cfg.LongbridgeAccessToken) == "" {
		return ""
	}
	client, err := s.newLongbridgeIPOCalendarClient(cfg.LongbridgeAppKey, cfg.LongbridgeAppSecret, cfg.LongbridgeAccessToken)
	if err != nil {
		return "create Longbridge IPO calendar client: " + err.Error()
	}
	now := time.Now().UTC()
	start := now.AddDate(0, 0, -settings.LongbridgeIPOCalendarLookbackDays).Format(time.DateOnly)
	end := now.AddDate(0, 0, settings.LongbridgeIPOCalendarLookaheadDays).Format(time.DateOnly)
	requestStart := start
	allEvents := make([]longbridgeIPOCalendarEvent, 0)
	for page := 0; page < settings.LongbridgeIPOCalendarMaxPages; page++ {
		result, fetchErr := client.FinanceCalendar(ctx, requestStart, end, "US")
		if fetchErr != nil {
			return "Longbridge IPO calendar: " + fetchErr.Error()
		}
		allEvents = append(allEvents, result.Events...)
		next := strings.TrimSpace(result.NextDate)
		if next == "" || next == requestStart {
			break
		}
		requestStart = next
	}
	if err := s.storeLongbridgeIPOCalendarEvents(ctx, allEvents, now); err != nil {
		return "store Longbridge IPO calendar: " + err.Error()
	}
	if err := s.attachLongbridgeCalendarToSECCandidates(ctx, allEvents); err != nil {
		return "match Longbridge IPO calendar: " + err.Error()
	}
	return ""
}

func (s *IPORadarService) storeLongbridgeIPOCalendarEvents(ctx context.Context, events []longbridgeIPOCalendarEvent, now time.Time) error {
	for _, event := range events {
		eventDate, ok := parseLongbridgeIPOListingDate(event.Date)
		if !ok {
			continue
		}
		symbol := strings.ToUpper(strings.TrimSpace(event.Symbol))
		companyName := strings.TrimSpace(event.CompanyName)
		key := strings.TrimSpace(event.ID)
		if key == "" {
			key = fmt.Sprintf("%s|%s|%s|%s", symbol, strings.ToUpper(strings.TrimSpace(event.Market)), companyName, eventDate.Format(time.DateOnly))
		}
		row := model.IPOCalendarEvent{
			EventKey: key, Symbol: symbol, Market: strings.ToUpper(strings.TrimSpace(event.Market)), CompanyName: companyName,
			EventDate: eventDate, Session: strings.TrimSpace(event.Session), Content: strings.TrimSpace(event.Content), Currency: strings.TrimSpace(event.Currency),
			Source: longbridgeIPOCalendarSource, LastSeenAt: now,
		}
		if err := s.db.WithContext(ctx).Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "event_key"}},
			DoUpdates: clause.AssignmentColumns([]string{"symbol", "market", "company_name", "event_date", "session", "content", "currency", "source", "last_seen_at", "updated_at"}),
		}).Create(&row).Error; err != nil {
			return err
		}
	}
	return nil
}

func (s *IPORadarService) attachLongbridgeCalendarToSECCandidates(ctx context.Context, events []longbridgeIPOCalendarEvent) error {
	if len(events) == 0 {
		return nil
	}
	var filings []model.IPOFiling
	if err := scopeIPOCandidateFilings(s.db.WithContext(ctx).Model(&model.IPOFiling{}), s.db.WithContext(ctx)).Find(&filings).Error; err != nil {
		return err
	}
	nameToCIKs := map[string]map[string]bool{}
	coreNameToCIKs := map[string]map[string]bool{}
	for _, filing := range filings {
		name := normalizedIPOCalendarCompanyName(filing.CompanyName)
		if name == "" || strings.TrimSpace(filing.CIK) == "" {
			continue
		}
		if nameToCIKs[name] == nil {
			nameToCIKs[name] = map[string]bool{}
		}
		nameToCIKs[name][filing.CIK] = true
		if core := normalizedIPOCalendarCompanyCore(filing.CompanyName); core != "" {
			if coreNameToCIKs[core] == nil {
				coreNameToCIKs[core] = map[string]bool{}
			}
			coreNameToCIKs[core][filing.CIK] = true
		}
	}
	for _, event := range events {
		name := normalizedIPOCalendarCompanyName(event.CompanyName)
		ciks := nameToCIKs[name]
		// Calendar publishers often omit a legal suffix (Inc., Ltd., PLC).
		// Accept that normalization only when it still identifies exactly one
		// SEC CIK; an ambiguous core name remains intentionally unmapped.
		if len(ciks) != 1 {
			ciks = coreNameToCIKs[normalizedIPOCalendarCompanyCore(event.CompanyName)]
		}
		if len(ciks) != 1 {
			continue // Never attach an ambiguous calendar name to an SEC CIK.
		}
		var cik string
		for cik = range ciks {
		}
		if err := s.upsertLongbridgeCalendarMarketData(ctx, cik, event); err != nil {
			return err
		}
	}
	return nil
}

func (s *IPORadarService) upsertLongbridgeCalendarMarketData(ctx context.Context, cik string, event longbridgeIPOCalendarEvent) error {
	eventDate, ok := parseLongbridgeIPOListingDate(event.Date)
	if !ok || strings.TrimSpace(cik) == "" {
		return nil
	}
	var row model.IPOCompanyMarketData
	err := s.db.WithContext(ctx).Where("cik = ?", cik).First(&row).Error
	if err == gorm.ErrRecordNotFound {
		row = model.IPOCompanyMarketData{CIK: cik}
	} else if err != nil {
		return err
	}
	if row.TickerSource == "https://www.sec.gov/files/company_tickers_exchange.json" || row.ListedVerifiedAt != nil {
		return nil // SEC's CIK-bound mapping and a confirmed listing take priority.
	}
	if symbol := normalizeIPOCompanyTicker(event.Symbol); symbol != "" {
		row.Ticker = symbol
		row.TickerSource = longbridgeIPOCalendarSource
		row.TickerConfidence = "medium"
	}
	row.ListingDate = &eventDate
	row.ListingSource = longbridgeIPOCalendarSource
	row.ListingConfidence = "medium"
	return s.db.WithContext(ctx).Save(&row).Error
}

func normalizedIPOCalendarCompanyName(value string) string {
	var builder strings.Builder
	for _, char := range strings.ToUpper(strings.TrimSpace(value)) {
		if unicode.IsLetter(char) || unicode.IsDigit(char) {
			builder.WriteRune(char)
		}
	}
	return builder.String()
}

var ipoCalendarLegalSuffixes = map[string]bool{
	"INC": true, "INCORPORATED": true, "CORP": true, "CORPORATION": true,
	"LTD": true, "LIMITED": true, "LLC": true, "PLC": true, "LP": true,
	"LLP": true, "CO": true, "COMPANY": true,
}

func normalizedIPOCalendarCompanyCore(value string) string {
	words := strings.FieldsFunc(strings.ToUpper(strings.TrimSpace(value)), func(char rune) bool {
		return !unicode.IsLetter(char) && !unicode.IsDigit(char)
	})
	for len(words) > 1 && ipoCalendarLegalSuffixes[words[len(words)-1]] {
		words = words[:len(words)-1]
	}
	return strings.Join(words, "")
}
