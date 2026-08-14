package service

import (
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"sec_monitor/internal/config"
	"sec_monitor/internal/model"

	"golang.org/x/net/html"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	MacroProviderBEA            = "bea"
	MacroProviderBLS            = "bls"
	MacroProviderCensus         = "census"
	MacroProviderDOL            = "dol"
	MacroProviderEIA            = "eia"
	MacroProviderTreasury       = "treasury"
	MacroProviderFederalReserve = "federal_reserve"
	// FRED is operated by the Federal Reserve Bank of St. Louis.  CPI values
	// retained here carry BLS as their original source, but use FRED's public
	// CSV mirror when the BLS web calendar is unavailable to this installation.
	MacroProviderFRED     = "fred"
	MacroReleaseScheduled = "scheduled"
	MacroReleasePublished = "published"
	// The full schedule retains the current year's already-released events,
	// which makes an initial sync useful immediately instead of showing only
	// future appointments from the abbreviated schedule page.
	defaultBEAScheduleURL            = "https://www.bea.gov/news/schedule/full"
	defaultBLSScheduleURL            = "https://www.bls.gov/schedule/news_release/bls.ics"
	defaultFOMCScheduleURL           = "https://www.federalreserve.gov/monetarypolicy/fomccalendars.htm"
	defaultBLSEmploymentURL          = "https://www.bls.gov/news.release/empsit.nr0.htm"
	defaultBLSCPIURL                 = "https://www.bls.gov/news.release/cpi.nr0.htm"
	defaultBLSPPIURL                 = "https://www.bls.gov/news.release/ppi.nr0.htm"
	defaultBLSJOLTSURL               = "https://www.bls.gov/news.release/jolts.nr0.htm"
	defaultCensusRetailScheduleURL   = "https://www.census.gov/retail/release_schedule.html"
	defaultCensusRetailSalesURL      = "https://www.census.gov/retail/sales.html"
	defaultCensusEconomicScheduleURL = "https://www.census.gov/economic-indicators/calendar-listview.html"
	defaultDOLClaimsURL              = "https://www.dol.gov/newsroom/releases/eta?lang=en"
	defaultTreasuryYieldURL          = "https://home.treasury.gov/resource-center/data-chart-center/interest-rates/TextView?type=daily_treasury_yield_curve&field_tdr_date_value="
	defaultTreasuryRealYieldURL      = "https://home.treasury.gov/resource-center/data-chart-center/interest-rates/TextView?type=daily_treasury_real_yield_curve&field_tdr_date_value="
	defaultEIAWeeklyPetroleumURL     = "https://www.eia.gov/petroleum/supply/weekly/"
	defaultEIAWeeklyPetroleumTable4  = "https://ir.eia.gov/wpsr/table4.csv"
	defaultFREDCPIURL                = "https://fred.stlouisfed.org/graph/fredgraph.csv?id=CPIAUCSL,CPILFESL"
	// BLS occasionally rejects automated requests to its public iCalendar and
	// release pages. These public FRED mirrors retain BLS as their original
	// source and give the calendar a durable, auditable fallback for the two
	// most market-sensitive BLS reports.
	defaultFREDEmploymentURL = "https://fred.stlouisfed.org/graph/fredgraph.csv?id=PAYEMS,UNRATE,CES0500000003"
	defaultFREDPPIURL        = "https://fred.stlouisfed.org/graph/fredgraph.csv?id=PPIFIS,WPSFD49116"
)

type macroHTTPClient interface {
	Do(*http.Request) (*http.Response, error)
}

type MacroReleaseFilter struct {
	Status    string
	Category  string
	View      string
	Frequency string
	SortOrder string
	From      *time.Time
	To        *time.Time
	Page      int
	PageSize  int
}

type MacroReleaseItem struct {
	model.MacroRelease
	Observations   []model.MacroObservation `json:"observations"`
	RelatedSources []MacroReleaseSource     `json:"related_sources"`
}

// MacroReleaseSource is an auditable cross-source association for one
// calendar event. Official sources remain primary; a Longbridge record only
// contributes its separately-labelled consensus and market-calendar fields.
type MacroReleaseSource struct {
	Provider    string     `json:"provider"`
	Category    string     `json:"category"`
	Title       string     `json:"title"`
	Status      string     `json:"status"`
	ScheduledAt *time.Time `json:"scheduled_at,omitempty"`
	PublishedAt *time.Time `json:"published_at,omitempty"`
	SourceURL   string     `json:"source_url"`
	Official    bool       `json:"official"`
}

type MacroReleasePage struct {
	Items    []MacroReleaseItem `json:"items"`
	Total    int64              `json:"total"`
	Page     int                `json:"page"`
	PageSize int                `json:"page_size"`
	Pages    int                `json:"pages"`
}

type MacroCalendarSyncResult struct {
	ScheduledFound int      `json:"scheduled_found"`
	ReleasesSaved  int      `json:"releases_saved"`
	Published      int      `json:"published"`
	Observations   int      `json:"observations"`
	Warnings       []string `json:"warnings"`
}

// MacroCalendarService uses official agency pages as its primary source. It
// may add explicitly-labelled Longbridge market-calendar events, but never
// infers a forecast or a "bullish/bearish" interpretation itself.
type MacroCalendarService struct {
	db                        *gorm.DB
	client                    macroHTTPClient
	scheduleURL               string
	blsScheduleURL            string
	censusRetailScheduleURL   string
	censusEconomicScheduleURL string
	dolClaimsURL              string
	treasuryYieldURL          string
	treasuryRealYieldURL      string
	eiaWeeklyPetroleumURL     string
	eiaWeeklyPetroleumTable4  string
	fomcScheduleURL           string
	fredCPIURL                string
	fredEmploymentURL         string
	fredPPIURL                string
	now                       func() time.Time
	configs                   *ConfigService
	runtime                   config.DiscoveryConfig
}

// WithLongbridge adds the commercial calendar as an explicitly labelled
// supplement. Official agency releases remain the primary evidence source.
func (s *MacroCalendarService) WithLongbridge(configs *ConfigService, runtime config.DiscoveryConfig) *MacroCalendarService {
	if s != nil {
		s.configs, s.runtime = configs, runtime
	}
	return s
}

func NewMacroCalendarService(db *gorm.DB) *MacroCalendarService {
	return &MacroCalendarService{
		db: db, client: &http.Client{Timeout: 20 * time.Second}, scheduleURL: defaultBEAScheduleURL,
		blsScheduleURL: defaultBLSScheduleURL, fomcScheduleURL: defaultFOMCScheduleURL, now: time.Now,
		censusRetailScheduleURL:   defaultCensusRetailScheduleURL,
		censusEconomicScheduleURL: defaultCensusEconomicScheduleURL,
		dolClaimsURL:              defaultDOLClaimsURL,
		treasuryYieldURL:          defaultTreasuryYieldURL,
		treasuryRealYieldURL:      defaultTreasuryRealYieldURL,
		eiaWeeklyPetroleumURL:     defaultEIAWeeklyPetroleumURL,
		eiaWeeklyPetroleumTable4:  defaultEIAWeeklyPetroleumTable4,
		fredCPIURL:                defaultFREDCPIURL,
		fredEmploymentURL:         defaultFREDEmploymentURL,
		fredPPIURL:                defaultFREDPPIURL,
	}
}

func (s *MacroCalendarService) List(ctx context.Context, filter MacroReleaseFilter) (MacroReleasePage, error) {
	result := MacroReleasePage{Items: []MacroReleaseItem{}}
	if s == nil || s.db == nil {
		return result, errors.New("macro calendar service is not configured")
	}
	if filter.Page < 1 {
		filter.Page = 1
	}
	if filter.PageSize < 1 || filter.PageSize > 200 {
		filter.PageSize = 100
	}
	query := s.db.WithContext(ctx).Model(&model.MacroRelease{})
	if status := strings.TrimSpace(filter.Status); status != "" {
		query = query.Where("status = ?", status)
	}
	if category := strings.TrimSpace(filter.Category); category != "" {
		query = query.Where("category = ?", category)
	}
	// Keep economic releases and the rate/liquidity backdrop distinct in the
	// default calendar.  The same ledger backs both views so a date range and
	// source link remain comparable without duplicating data.
	switch strings.TrimSpace(filter.View) {
	case "economic":
		query = query.Where("category NOT IN ?", []string{"treasury_yields", "treasury_real_yields"})
	case "rates":
		query = query.Where("category IN ?", []string{"treasury_yields", "treasury_real_yields"})
	}
	// Release frequency is a presentation-level grouping for the current
	// current coverage. It intentionally includes scheduled releases that do not
	// have parsed observations yet.
	switch strings.TrimSpace(filter.Frequency) {
	case "monthly":
		query = query.Where("category IN ?", []string{"personal_income_outlays", "employment", "cpi", "ppi", "jolts", "retail_sales", "durable_goods", "housing_starts", "new_home_sales", "international_trade", "advance_trade"})
	case "daily":
		query = query.Where("category IN ?", []string{"treasury_yields", "treasury_real_yields"})
	case "quarterly":
		query = query.Where("category = ?", "gdp")
	case "meeting":
		query = query.Where("category = ?", "fomc")
	case "weekly":
		query = query.Where("category IN ?", []string{"initial_claims", "petroleum_inventories"})
	}
	if filter.From != nil {
		query = query.Where("scheduled_at >= ?", filter.From.UTC())
	}
	if filter.To != nil {
		query = query.Where("scheduled_at <= ?", filter.To.UTC())
	}
	if err := query.Count(&result.Total).Error; err != nil {
		return result, err
	}
	order := "scheduled_at DESC, id DESC"
	if strings.EqualFold(strings.TrimSpace(filter.SortOrder), "asc") {
		order = "scheduled_at ASC, id ASC"
	}
	var releases []model.MacroRelease
	if err := query.Order(order).Offset((filter.Page - 1) * filter.PageSize).Limit(filter.PageSize).Find(&releases).Error; err != nil {
		return result, err
	}
	result.Items = make([]MacroReleaseItem, 0, len(releases))
	for _, release := range releases {
		if release.CanonicalEventKey == "" {
			release.CanonicalEventKey = canonicalMacroEventKey(release.Category, release.Title, release.ScheduledAt)
		}
		item := MacroReleaseItem{MacroRelease: release, Observations: []model.MacroObservation{}, RelatedSources: []MacroReleaseSource{}}
		if err := s.db.WithContext(ctx).Where("release_id = ?", release.ID).Order("indicator_code ASC").Find(&item.Observations).Error; err != nil {
			return result, err
		}
		result.Items = append(result.Items, item)
	}
	if err := s.attachMacroReleaseSources(ctx, result.Items); err != nil {
		return result, err
	}
	result.Page, result.PageSize = filter.Page, filter.PageSize
	result.Pages = int((result.Total + int64(filter.PageSize) - 1) / int64(filter.PageSize))
	return result, nil
}

func (s *MacroCalendarService) SyncOfficialBEA(ctx context.Context) (MacroCalendarSyncResult, error) {
	result := MacroCalendarSyncResult{Warnings: []string{}}
	if s == nil || s.db == nil || s.client == nil {
		return result, errors.New("macro calendar service is not configured")
	}
	// The individual official sources are independent.  Do not let a BEA
	// outage prevent the BLS/FRED, Fed, Treasury, Census, DOL, EIA, or
	// Longbridge portions of the calendar from refreshing.
	body, err := s.fetch(ctx, s.scheduleURL)
	if err != nil {
		result.Warnings = append(result.Warnings, "BEA 官方日历同步提示："+sanitizeMacroError(err))
	} else if events, parseErr := parseBEASchedule(body, s.scheduleURL, s.now().UTC()); parseErr != nil {
		result.Warnings = append(result.Warnings, "BEA 官方日历解析提示："+sanitizeMacroError(parseErr))
	} else {
		result.ScheduledFound = len(events)
		for _, event := range events {
			release, saved, saveErr := s.upsertRelease(ctx, event)
			if saveErr != nil {
				result.Warnings = append(result.Warnings, "保存官方日历事件失败："+sanitizeMacroError(saveErr))
				continue
			}
			if saved {
				result.ReleasesSaved++
			}
			// Published releases are immutable release-time records. Re-fetch only
			// entries we have not successfully captured yet; this keeps the daily
			// calendar job small and avoids repeatedly hitting the public site for
			// historical data that is already locally auditable.
			if release.Status == MacroReleasePublished && strings.TrimSpace(release.SourceHash) != "" {
				continue
			}
			if event.ReleaseURL == "" || event.ScheduledAt.After(s.now().UTC().Add(5*time.Minute)) {
				continue
			}
			releaseBody, fetchErr := s.fetch(ctx, event.ReleaseURL)
			if fetchErr != nil {
				result.Warnings = append(result.Warnings, event.Title+"：官方公告暂不可读取（"+sanitizeMacroError(fetchErr)+"）")
				continue
			}
			observations := parseBEAReleaseObservations(event, releaseBody)
			if len(observations) == 0 {
				// A scheduled entry can legally point to an explanatory page rather
				// than a release. Preserve the calendar item without fabricating data.
				continue
			}
			publishedAt := event.ScheduledAt
			if err := s.db.WithContext(ctx).Model(&model.MacroRelease{}).Where("id = ?", release.ID).Updates(map[string]any{
				"status": MacroReleasePublished, "published_at": &publishedAt, "source_hash": hashMacroBody(releaseBody), "fetched_at": s.now().UTC(), "last_error": "",
			}).Error; err != nil {
				return result, err
			}
			result.Published++
			for _, observation := range observations {
				observation.ReleaseID = release.ID
				observation.SourceURL = event.ReleaseURL
				observation.ProviderUpdatedAt = &publishedAt
				observation.FetchedAt = s.now().UTC()
				if prior, priorErr := s.previousOfficialValue(ctx, release.Provider, observation.IndicatorCode, release.ID, event.ScheduledAt); priorErr == nil {
					observation.PreviousValue = prior
				}
				if err := s.db.WithContext(ctx).Clauses(clause.OnConflict{
					Columns:   []clause.Column{{Name: "release_id"}, {Name: "indicator_code"}},
					DoUpdates: clause.AssignmentColumns([]string{"indicator_name", "frequency", "unit", "actual_value", "previous_value", "previous_revised", "source_field", "source_url", "provider_updated_at", "fetched_at", "updated_at"}),
				}).Create(&observation).Error; err != nil {
					return result, err
				}
				result.Observations++
			}
		}
	}
	// BLS and the Federal Reserve are independent official sources. A transient
	// failure in either must not discard a successful BEA calendar refresh.
	if err := s.syncOfficialBLS(ctx, &result); err != nil {
		result.Warnings = append(result.Warnings, "BLS 官方日历同步提示："+sanitizeMacroError(err))
	}
	// The BLS public calendar is occasionally protected by a network-edge
	// policy (HTTP 403/timeout).  FRED publishes the BLS CPI and core-CPI
	// series as a public, auditable mirror, so it keeps the calendar useful
	// without fabricating a release from an unofficial estimate.
	if err := s.syncFREDCPI(ctx, &result); err != nil {
		result.Warnings = append(result.Warnings, "FRED CPI 回填提示："+sanitizeMacroError(err))
	}
	if err := s.syncFREDEmployment(ctx, &result); err != nil {
		result.Warnings = append(result.Warnings, "FRED 就业报告回填提示："+sanitizeMacroError(err))
	}
	if err := s.syncFREDPPI(ctx, &result); err != nil {
		result.Warnings = append(result.Warnings, "FRED PPI 回填提示："+sanitizeMacroError(err))
	}
	if err := s.syncOfficialFOMC(ctx, &result); err != nil {
		result.Warnings = append(result.Warnings, "美联储 FOMC 日历同步提示："+sanitizeMacroError(err))
	}
	if err := s.syncOfficialCensusRetail(ctx, &result); err != nil {
		result.Warnings = append(result.Warnings, "Census 零售销售日历同步提示："+sanitizeMacroError(err))
	}
	if err := s.syncOfficialDOLClaims(ctx, &result); err != nil {
		result.Warnings = append(result.Warnings, "DOL 初请失业金同步提示："+sanitizeMacroError(err))
	}
	if err := s.syncOfficialTreasuryYields(ctx, &result); err != nil {
		result.Warnings = append(result.Warnings, "美国财政部收益率曲线同步提示："+sanitizeMacroError(err))
	}
	if err := s.syncOfficialTreasuryRealYields(ctx, &result); err != nil {
		result.Warnings = append(result.Warnings, "美国财政部实际收益率曲线同步提示："+sanitizeMacroError(err))
	}
	if err := s.syncOfficialEIAWeeklyPetroleum(ctx, &result); err != nil {
		result.Warnings = append(result.Warnings, "EIA 周度石油库存同步提示："+sanitizeMacroError(err))
	}
	if err := s.syncLongbridgeMacroCalendar(ctx, &result); err != nil {
		result.Warnings = append(result.Warnings, "Longbridge 市场日历同步提示："+sanitizeMacroError(err))
	}
	return result, nil
}

func (s *MacroCalendarService) previousOfficialValue(ctx context.Context, provider, indicator string, releaseID uint, before time.Time) (*float64, error) {
	var row model.MacroObservation
	err := s.db.WithContext(ctx).
		Joins("JOIN macro_releases ON macro_releases.id = macro_observations.release_id").
		Where("macro_releases.provider = ? AND macro_observations.indicator_code = ? AND macro_observations.release_id <> ? AND macro_releases.scheduled_at < ? AND macro_observations.actual_value IS NOT NULL", provider, indicator, releaseID, before).
		Order("macro_releases.scheduled_at DESC, macro_observations.id DESC").First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return row.ActualValue, nil
}

func (s *MacroCalendarService) upsertRelease(ctx context.Context, event beaScheduleEvent) (model.MacroRelease, bool, error) {
	provider := strings.TrimSpace(event.Provider)
	if provider == "" {
		provider = MacroProviderBEA
	}
	var existing model.MacroRelease
	err := s.db.WithContext(ctx).Where("provider = ? AND source_url = ?", provider, event.SourceURL).First(&existing).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return existing, false, err
	}
	now := s.now().UTC()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		row := model.MacroRelease{Provider: provider, Category: event.Category, CanonicalEventKey: canonicalMacroEventKey(event.Category, event.Title, &event.ScheduledAt), Title: event.Title, ReferencePeriod: event.ReferencePeriod, ReleaseStage: event.ReleaseStage, Status: MacroReleaseScheduled, ScheduledAt: &event.ScheduledAt, SourceURL: event.SourceURL, FetchedAt: now}
		if err := s.db.WithContext(ctx).Create(&row).Error; err != nil {
			return row, false, err
		}
		return row, true, nil
	}
	if err := s.db.WithContext(ctx).Model(&existing).Updates(map[string]any{"category": event.Category, "canonical_event_key": canonicalMacroEventKey(event.Category, event.Title, &event.ScheduledAt), "title": event.Title, "reference_period": event.ReferencePeriod, "release_stage": event.ReleaseStage, "scheduled_at": &event.ScheduledAt, "fetched_at": now}).Error; err != nil {
		return existing, false, err
	}
	if err := s.db.WithContext(ctx).First(&existing, existing.ID).Error; err != nil {
		return existing, false, err
	}
	return existing, false, nil
}

// syncOfficialBLS stores BLS's public schedule first, then enriches the last
// eligible event for each supported report using the official release page.
// This avoids treating a page's publication timestamp as the event identity.
func (s *MacroCalendarService) syncOfficialBLS(ctx context.Context, result *MacroCalendarSyncResult) error {
	body, err := s.fetch(ctx, s.blsScheduleURL)
	if err != nil {
		return fmt.Errorf("load BLS release schedule: %w", err)
	}
	events, err := parseBLSSchedule(body, s.blsScheduleURL)
	if err != nil {
		return fmt.Errorf("parse BLS release schedule: %w", err)
	}
	result.ScheduledFound += len(events)
	for _, event := range events {
		_, saved, err := s.upsertRelease(ctx, event)
		if err != nil {
			result.Warnings = append(result.Warnings, "保存 BLS 日历事件失败："+sanitizeMacroError(err))
			continue
		}
		if saved {
			result.ReleasesSaved++
		}
	}
	for category, releaseURL := range map[string]string{
		"employment": defaultBLSEmploymentURL,
		"cpi":        defaultBLSCPIURL,
		"ppi":        defaultBLSPPIURL,
		"jolts":      defaultBLSJOLTSURL,
	} {
		if err := s.syncLatestBLSRelease(ctx, category, releaseURL, result); err != nil {
			result.Warnings = append(result.Warnings, "BLS "+category+"："+sanitizeMacroError(err))
		}
	}
	return nil
}

func (s *MacroCalendarService) syncLatestBLSRelease(ctx context.Context, category, releaseURL string, result *MacroCalendarSyncResult) error {
	var release model.MacroRelease
	now := s.now().UTC()
	err := s.db.WithContext(ctx).
		Where("provider = ? AND category = ? AND scheduled_at <= ?", MacroProviderBLS, category, now.Add(5*time.Minute)).
		Order("scheduled_at DESC, id DESC").First(&release).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil // The official calendar has not reached this report yet.
	}
	if err != nil {
		return err
	}
	if release.Status == MacroReleasePublished && strings.TrimSpace(release.SourceHash) != "" {
		return nil
	}
	body, err := s.fetch(ctx, releaseURL)
	if err != nil {
		return err
	}
	observations := parseBLSReleaseObservations(category, body)
	if len(observations) == 0 {
		return errors.New("official release did not contain supported indicators")
	}
	return s.publishMacroRelease(ctx, &release, releaseURL, body, observations, result)
}

type fredCPIRecord struct {
	Period time.Time
	CPI    *float64
	Core   *float64
}

// syncFREDCPI fills the CPI ledger from FRED's public mirror of the BLS CPI
// release. It is intentionally limited to CPI for now: the series definitions
// are stable, both headline and core values are available in one response, and
// the derived monthly/year-over-year changes map exactly to this page's CPI
// fields. The first successful run keeps three years; later runs inspect only
// the newest three periods.
func (s *MacroCalendarService) syncFREDCPI(ctx context.Context, result *MacroCalendarSyncResult) error {
	if strings.TrimSpace(s.fredCPIURL) == "" {
		return nil
	}
	body, err := s.fetchCSV(ctx, s.fredCPIURL)
	if err != nil {
		return fmt.Errorf("load FRED CPI series: %w", err)
	}
	records, err := parseFREDCPIRecords(body)
	if err != nil {
		return fmt.Errorf("parse FRED CPI series: %w", err)
	}
	if len(records) < 2 {
		return errors.New("FRED CPI series did not contain enough monthly observations")
	}
	var existing int64
	if err := s.db.WithContext(ctx).Model(&model.MacroRelease{}).Where("provider = ? AND category = ?", MacroProviderFRED, "cpi").Count(&existing).Error; err != nil {
		return err
	}
	limit := 3
	if existing == 0 {
		limit = 36
	}
	start := len(records) - limit
	if start < 1 { // The first record has no preceding month for a rate of change.
		start = 1
	}
	for index := start; index < len(records); index++ {
		record := records[index]
		if record.CPI == nil && record.Core == nil {
			continue
		}
		referencePeriod := record.Period.Format("January 2006")
		var duplicate int64
		if err := s.db.WithContext(ctx).Model(&model.MacroRelease{}).
			Where("category = ? AND reference_period = ? AND status = ?", "cpi", referencePeriod, MacroReleasePublished).
			Count(&duplicate).Error; err != nil {
			return err
		}
		// Prefer an already captured BLS release for the same reference period;
		// FRED should fill a gap, never create a second calendar entry.
		if duplicate > 0 {
			continue
		}
		event := beaScheduleEvent{
			Provider:        MacroProviderFRED,
			Category:        "cpi",
			Title:           "美国 CPI / 核心 CPI（FRED 镜像，原始来源：BLS）",
			ReferencePeriod: referencePeriod,
			ReleaseStage:    "fred_mirror",
			// FRED's CSV is indexed by the BLS reference month, not an exact
			// release timestamp. Keep the period date stable for sorting/audit and
			// expose the provider label so it cannot be mistaken for a BLS calendar time.
			ScheduledAt: record.Period.UTC(),
			SourceURL:   fredCPIObservationURL(s.fredCPIURL, record.Period),
		}
		observations := buildFREDCPIObservations(records, index)
		if len(observations) == 0 {
			continue
		}
		release, saved, err := s.upsertRelease(ctx, event)
		if err != nil {
			return err
		}
		if saved {
			result.ReleasesSaved++
		}
		result.ScheduledFound++
		if release.Status == MacroReleasePublished && strings.TrimSpace(release.SourceHash) != "" {
			continue
		}
		if err := s.publishMacroRelease(ctx, &release, event.SourceURL, body, observations, result); err != nil {
			return err
		}
	}
	return nil
}

func parseFREDCPIRecords(raw string) ([]fredCPIRecord, error) {
	reader := csv.NewReader(strings.NewReader(raw))
	records, err := reader.ReadAll()
	if err != nil {
		return nil, err
	}
	if len(records) < 2 {
		return nil, errors.New("CSV did not contain CPI observations")
	}
	columns := map[string]int{}
	for index, name := range records[0] {
		columns[strings.ToUpper(strings.TrimSpace(strings.TrimPrefix(name, "\ufeff")))] = index
	}
	dateColumn, hasDate := columns["OBSERVATION_DATE"]
	cpiColumn, hasCPI := columns["CPIAUCSL"]
	coreColumn, hasCore := columns["CPILFESL"]
	if !hasDate || !hasCPI || !hasCore {
		return nil, errors.New("CSV did not contain observation_date, CPIAUCSL, and CPILFESL columns")
	}
	result := make([]fredCPIRecord, 0, len(records)-1)
	for _, row := range records[1:] {
		if len(row) <= maxMacroColumn(dateColumn, cpiColumn, coreColumn) {
			continue
		}
		period, err := time.Parse("2006-01-02", strings.TrimSpace(row[dateColumn]))
		if err != nil {
			continue
		}
		result = append(result, fredCPIRecord{Period: period.UTC(), CPI: macroFloat(strings.TrimSpace(row[cpiColumn])), Core: macroFloat(strings.TrimSpace(row[coreColumn]))})
	}
	sort.Slice(result, func(left, right int) bool { return result[left].Period.Before(result[right].Period) })
	return result, nil
}

func buildFREDCPIObservations(records []fredCPIRecord, index int) []model.MacroObservation {
	if index < 1 || index >= len(records) {
		return nil
	}
	current, prior := records[index], records[index-1]
	yearPrior := (*fredCPIRecord)(nil)
	if index >= 12 {
		yearPrior = &records[index-12]
	}
	observations := make([]model.MacroObservation, 0, 4)
	appendChange := func(code, name, field string, value, base *float64) {
		if change := macroPercentChange(value, base); change != nil {
			observations = append(observations, model.MacroObservation{IndicatorCode: code, IndicatorName: name, Frequency: "monthly", Unit: "%", ActualValue: change, SourceField: field})
		}
	}
	appendChange("cpi_mom", "CPI 月率", "FRED CPIAUCSL（原始来源：BLS；按季调指数计算）", current.CPI, prior.CPI)
	appendChange("core_cpi_mom", "核心 CPI 月率", "FRED CPILFESL（原始来源：BLS；按季调指数计算）", current.Core, prior.Core)
	if yearPrior != nil {
		appendChange("cpi_yoy", "CPI 年率", "FRED CPIAUCSL（原始来源：BLS；按季调指数计算）", current.CPI, yearPrior.CPI)
		appendChange("core_cpi_yoy", "核心 CPI 年率", "FRED CPILFESL（原始来源：BLS；按季调指数计算）", current.Core, yearPrior.Core)
	}
	return observations
}

func macroPercentChange(value, base *float64) *float64 {
	if value == nil || base == nil || *base == 0 {
		return nil
	}
	changed := math.Round(((*value / *base)-1)*1000) / 10
	return &changed
}

func fredCPIObservationURL(rawURL string, period time.Time) string {
	return fredObservationURL(rawURL, period)
}

func fredObservationURL(rawURL string, period time.Time) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return rawURL + "#" + period.Format("2006-01-02")
	}
	query := parsed.Query()
	query.Set("observation_date", period.Format("2006-01-02"))
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

type fredEmploymentRecord struct {
	Period         time.Time
	PayrollsK      *float64
	Unemployment   *float64
	HourlyEarnings *float64
}

type fredPPIRecord struct {
	Period time.Time
	PPI    *float64
	Core   *float64
}

// syncFREDEmployment stores a BLS-backed fallback when BLS's public calendar
// is unavailable. FRED's CSV is indexed by reference month, not release time;
// the release stage deliberately makes that distinction visible to the UI.
func (s *MacroCalendarService) syncFREDEmployment(ctx context.Context, result *MacroCalendarSyncResult) error {
	if strings.TrimSpace(s.fredEmploymentURL) == "" {
		return nil
	}
	body, err := s.fetchCSV(ctx, s.fredEmploymentURL)
	if err != nil {
		return fmt.Errorf("load FRED employment series: %w", err)
	}
	records, err := parseFREDEmploymentRecords(body)
	if err != nil {
		return err
	}
	return s.syncFREDMonthlyFallback(ctx, "employment", "美国就业报告 / 非农（FRED 镜像，原始来源：BLS）", s.fredEmploymentURL, body, len(records), func(index int) (time.Time, []model.MacroObservation) {
		return records[index].Period, buildFREDEmploymentObservations(records, index)
	}, result)
}

func parseFREDEmploymentRecords(raw string) ([]fredEmploymentRecord, error) {
	reader := csv.NewReader(strings.NewReader(raw))
	rows, err := reader.ReadAll()
	if err != nil {
		return nil, err
	}
	columns := macroCSVColumns(rows)
	dateColumn, hasDate := columns["OBSERVATION_DATE"]
	payrollColumn, hasPayroll := columns["PAYEMS"]
	unemploymentColumn, hasUnemployment := columns["UNRATE"]
	hourlyColumn, hasHourly := columns["CES0500000003"]
	if !hasDate || !hasPayroll || !hasUnemployment || !hasHourly {
		return nil, errors.New("CSV did not contain PAYEMS, UNRATE, and CES0500000003")
	}
	result := make([]fredEmploymentRecord, 0, len(rows)-1)
	for _, row := range rows[1:] {
		if len(row) <= maxMacroColumn(dateColumn, payrollColumn, unemploymentColumn, hourlyColumn) {
			continue
		}
		period, parseErr := time.Parse("2006-01-02", strings.TrimSpace(row[dateColumn]))
		if parseErr != nil {
			continue
		}
		result = append(result, fredEmploymentRecord{Period: period.UTC(), PayrollsK: macroFloat(strings.TrimSpace(row[payrollColumn])), Unemployment: macroFloat(strings.TrimSpace(row[unemploymentColumn])), HourlyEarnings: macroFloat(strings.TrimSpace(row[hourlyColumn]))})
	}
	sort.Slice(result, func(left, right int) bool { return result[left].Period.Before(result[right].Period) })
	if len(result) < 2 {
		return nil, errors.New("FRED employment series did not contain enough monthly observations")
	}
	return result, nil
}

func buildFREDEmploymentObservations(records []fredEmploymentRecord, index int) []model.MacroObservation {
	if index < 1 || index >= len(records) {
		return nil
	}
	current, prior := records[index], records[index-1]
	observations := make([]model.MacroObservation, 0, 3)
	if current.PayrollsK != nil && prior.PayrollsK != nil {
		change := math.Round((*current.PayrollsK-*prior.PayrollsK)*10) / 10
		observations = append(observations, model.MacroObservation{IndicatorCode: "nonfarm_payrolls_change_k", IndicatorName: "非农就业人数变动", Frequency: "monthly", Unit: "K", ActualValue: &change, SourceField: "FRED PAYEMS（原始来源：BLS；季调总非农就业）"})
	}
	if current.Unemployment != nil {
		value := *current.Unemployment
		observations = append(observations, model.MacroObservation{IndicatorCode: "unemployment_rate", IndicatorName: "失业率", Frequency: "monthly", Unit: "%", ActualValue: &value, SourceField: "FRED UNRATE（原始来源：BLS）"})
	}
	if change := macroPercentChange(current.HourlyEarnings, prior.HourlyEarnings); change != nil {
		observations = append(observations, model.MacroObservation{IndicatorCode: "average_hourly_earnings_mom", IndicatorName: "平均时薪月率", Frequency: "monthly", Unit: "%", ActualValue: change, SourceField: "FRED CES0500000003（原始来源：BLS；季调）"})
	}
	return observations
}

func (s *MacroCalendarService) syncFREDPPI(ctx context.Context, result *MacroCalendarSyncResult) error {
	if strings.TrimSpace(s.fredPPIURL) == "" {
		return nil
	}
	body, err := s.fetchCSV(ctx, s.fredPPIURL)
	if err != nil {
		return fmt.Errorf("load FRED PPI series: %w", err)
	}
	records, err := parseFREDPPIRecords(body)
	if err != nil {
		return err
	}
	return s.syncFREDMonthlyFallback(ctx, "ppi", "美国 PPI / 核心 PPI（FRED 镜像，原始来源：BLS）", s.fredPPIURL, body, len(records), func(index int) (time.Time, []model.MacroObservation) {
		return records[index].Period, buildFREDPPIObservations(records, index)
	}, result)
}

func parseFREDPPIRecords(raw string) ([]fredPPIRecord, error) {
	reader := csv.NewReader(strings.NewReader(raw))
	rows, err := reader.ReadAll()
	if err != nil {
		return nil, err
	}
	columns := macroCSVColumns(rows)
	dateColumn, hasDate := columns["OBSERVATION_DATE"]
	ppiColumn, hasPPI := columns["PPIFIS"]
	coreColumn, hasCore := columns["WPSFD49116"]
	if !hasDate || !hasPPI || !hasCore {
		return nil, errors.New("CSV did not contain PPIFIS and WPSFD49116")
	}
	result := make([]fredPPIRecord, 0, len(rows)-1)
	for _, row := range rows[1:] {
		if len(row) <= maxMacroColumn(dateColumn, ppiColumn, coreColumn) {
			continue
		}
		period, parseErr := time.Parse("2006-01-02", strings.TrimSpace(row[dateColumn]))
		if parseErr != nil {
			continue
		}
		result = append(result, fredPPIRecord{Period: period.UTC(), PPI: macroFloat(strings.TrimSpace(row[ppiColumn])), Core: macroFloat(strings.TrimSpace(row[coreColumn]))})
	}
	sort.Slice(result, func(left, right int) bool { return result[left].Period.Before(result[right].Period) })
	if len(result) < 2 {
		return nil, errors.New("FRED PPI series did not contain enough monthly observations")
	}
	return result, nil
}

func buildFREDPPIObservations(records []fredPPIRecord, index int) []model.MacroObservation {
	if index < 1 || index >= len(records) {
		return nil
	}
	current, prior := records[index], records[index-1]
	observations := make([]model.MacroObservation, 0, 2)
	if change := macroPercentChange(current.PPI, prior.PPI); change != nil {
		observations = append(observations, model.MacroObservation{IndicatorCode: "ppi_mom", IndicatorName: "PPI 月率", Frequency: "monthly", Unit: "%", ActualValue: change, SourceField: "FRED PPIFIS（原始来源：BLS；季调最终需求）"})
	}
	if change := macroPercentChange(current.Core, prior.Core); change != nil {
		observations = append(observations, model.MacroObservation{IndicatorCode: "core_ppi_mom", IndicatorName: "核心 PPI 月率", Frequency: "monthly", Unit: "%", ActualValue: change, SourceField: "FRED WPSFD49116（原始来源：BLS；季调，不含食品、能源和贸易服务）"})
	}
	return observations
}

func (s *MacroCalendarService) syncFREDMonthlyFallback(ctx context.Context, category, title, sourceURL, body string, recordCount int, observationAt func(int) (time.Time, []model.MacroObservation), result *MacroCalendarSyncResult) error {
	if recordCount < 2 {
		return nil
	}
	var existing int64
	if err := s.db.WithContext(ctx).Model(&model.MacroRelease{}).Where("provider = ? AND category = ?", MacroProviderFRED, category).Count(&existing).Error; err != nil {
		return err
	}
	limit := 3
	if existing == 0 {
		limit = 36
	}
	start := recordCount - limit
	if start < 1 {
		start = 1
	}
	for index := start; index < recordCount; index++ {
		period, observations := observationAt(index)
		if len(observations) == 0 {
			continue
		}
		referencePeriod := period.Format("January 2006")
		var duplicate int64
		if err := s.db.WithContext(ctx).Model(&model.MacroRelease{}).Where("category = ? AND reference_period = ? AND status = ?", category, referencePeriod, MacroReleasePublished).Count(&duplicate).Error; err != nil {
			return err
		}
		if duplicate > 0 {
			continue
		}
		event := beaScheduleEvent{Provider: MacroProviderFRED, Category: category, Title: title, ReferencePeriod: referencePeriod, ReleaseStage: "fred_mirror", ScheduledAt: period.UTC(), SourceURL: fredObservationURL(sourceURL, period)}
		release, saved, err := s.upsertRelease(ctx, event)
		if err != nil {
			return err
		}
		if saved {
			result.ReleasesSaved++
		}
		result.ScheduledFound++
		if release.Status == MacroReleasePublished && strings.TrimSpace(release.SourceHash) != "" {
			continue
		}
		if err := s.publishMacroRelease(ctx, &release, event.SourceURL, body, observations, result); err != nil {
			return err
		}
	}
	return nil
}

func macroCSVColumns(rows [][]string) map[string]int {
	columns := map[string]int{}
	if len(rows) == 0 {
		return columns
	}
	for index, name := range rows[0] {
		columns[strings.ToUpper(strings.TrimSpace(strings.TrimPrefix(name, "\ufeff")))] = index
	}
	return columns
}

func maxMacroColumn(values ...int) int {
	maximum := 0
	for _, value := range values {
		if value > maximum {
			maximum = value
		}
	}
	return maximum
}

func (s *MacroCalendarService) publishMacroRelease(ctx context.Context, release *model.MacroRelease, sourceURL, body string, observations []model.MacroObservation, result *MacroCalendarSyncResult) error {
	if release == nil {
		return errors.New("macro release is required")
	}
	publishedAt := s.now().UTC()
	if release.ScheduledAt != nil {
		publishedAt = *release.ScheduledAt
	}
	if err := s.db.WithContext(ctx).Model(&model.MacroRelease{}).Where("id = ?", release.ID).Updates(map[string]any{
		"status": MacroReleasePublished, "published_at": &publishedAt, "source_hash": hashMacroBody(body), "fetched_at": s.now().UTC(), "last_error": "",
	}).Error; err != nil {
		return err
	}
	result.Published++
	for _, observation := range observations {
		observation.ReleaseID = release.ID
		observation.SourceURL = sourceURL
		observation.ProviderUpdatedAt = &publishedAt
		observation.FetchedAt = s.now().UTC()
		if prior, err := s.previousOfficialValue(ctx, release.Provider, observation.IndicatorCode, release.ID, publishedAt); err == nil {
			observation.PreviousValue = prior
		}
		if err := s.db.WithContext(ctx).Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "release_id"}, {Name: "indicator_code"}},
			DoUpdates: clause.AssignmentColumns([]string{"indicator_name", "frequency", "unit", "actual_value", "previous_value", "previous_revised", "source_field", "source_url", "provider_updated_at", "fetched_at", "updated_at"}),
		}).Create(&observation).Error; err != nil {
			return err
		}
		result.Observations++
	}
	return nil
}

func (s *MacroCalendarService) syncOfficialFOMC(ctx context.Context, result *MacroCalendarSyncResult) error {
	body, err := s.fetch(ctx, s.fomcScheduleURL)
	if err != nil {
		return fmt.Errorf("load FOMC calendar: %w", err)
	}
	events, err := parseFOMCSchedule(body, s.fomcScheduleURL, s.now().UTC())
	if err != nil {
		return fmt.Errorf("parse FOMC calendar: %w", err)
	}
	result.ScheduledFound += len(events)
	for _, event := range events {
		_, saved, err := s.upsertRelease(ctx, event)
		if err != nil {
			result.Warnings = append(result.Warnings, "保存 FOMC 日历事件失败："+sanitizeMacroError(err))
			continue
		}
		if saved {
			result.ReleasesSaved++
		}
	}
	return nil
}

// syncOfficialCensusRetail records Census's Advance Monthly Retail Trade
// schedule. The current sales page provides the published headline month-over-
// month change; the separately defined "control group" is intentionally not
// guessed from the headline table and will be added only with its exact
// official table definition.
func (s *MacroCalendarService) syncOfficialCensusRetail(ctx context.Context, result *MacroCalendarSyncResult) error {
	body, err := s.fetch(ctx, s.censusRetailScheduleURL)
	if err != nil {
		return fmt.Errorf("load Census retail schedule: %w", err)
	}
	events, err := parseCensusRetailSchedule(body, s.censusRetailScheduleURL)
	if err != nil {
		return fmt.Errorf("parse Census retail schedule: %w", err)
	}
	if strings.TrimSpace(s.censusEconomicScheduleURL) != "" {
		economicBody, fetchErr := s.fetch(ctx, s.censusEconomicScheduleURL)
		if fetchErr != nil {
			result.Warnings = append(result.Warnings, "Census 经济指标总日历暂不可读取："+sanitizeMacroError(fetchErr))
		} else if economicEvents, parseErr := parseCensusEconomicSchedule(economicBody, s.censusEconomicScheduleURL); parseErr != nil {
			result.Warnings = append(result.Warnings, "Census 经济指标总日历解析提示："+sanitizeMacroError(parseErr))
		} else {
			events = append(events, economicEvents...)
		}
	}
	result.ScheduledFound += len(events)
	for _, event := range events {
		_, saved, err := s.upsertRelease(ctx, event)
		if err != nil {
			result.Warnings = append(result.Warnings, "保存 Census 零售销售日历事件失败："+sanitizeMacroError(err))
			continue
		}
		if saved {
			result.ReleasesSaved++
		}
	}
	if err := s.syncLatestCensusRelease(ctx, "retail_sales", defaultCensusRetailSalesURL, "", result, parseCensusRetailObservations); err != nil {
		result.Warnings = append(result.Warnings, "Census 零售销售："+sanitizeMacroError(err))
	}
	if strings.TrimSpace(s.censusEconomicScheduleURL) == "" {
		return nil
	}
	currentBody, fetchErr := s.fetch(ctx, s.censusEconomicScheduleURL)
	if fetchErr != nil {
		result.Warnings = append(result.Warnings, "Census 最新经济指标暂不可读取："+sanitizeMacroError(fetchErr))
		return nil
	}
	for _, category := range []string{"durable_goods", "housing_starts", "new_home_sales", "international_trade", "advance_trade"} {
		if err := s.syncLatestCensusRelease(ctx, category, s.censusEconomicScheduleURL, currentBody, result, func(raw string) []model.MacroObservation {
			return parseCensusEconomicObservations(category, raw)
		}); err != nil {
			result.Warnings = append(result.Warnings, "Census "+category+"："+sanitizeMacroError(err))
		}
	}
	return nil
}

func (s *MacroCalendarService) syncLatestCensusRelease(ctx context.Context, category, sourceURL, body string, result *MacroCalendarSyncResult, parser func(string) []model.MacroObservation) error {
	var release model.MacroRelease
	now := s.now().UTC()
	err := s.db.WithContext(ctx).Where("provider = ? AND category = ? AND scheduled_at <= ?", MacroProviderCensus, category, now.Add(5*time.Minute)).Order("scheduled_at DESC, id DESC").First(&release).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil || (release.Status == MacroReleasePublished && strings.TrimSpace(release.SourceHash) != "") {
		return err
	}
	if body == "" {
		body, err = s.fetch(ctx, sourceURL)
		if err != nil {
			return err
		}
	}
	observations := parser(body)
	if len(observations) == 0 {
		return errors.New("official current page did not contain supported indicators")
	}
	return s.publishMacroRelease(ctx, &release, sourceURL, body, observations, result)
}

// syncOfficialDOLClaims captures the latest public weekly claims release from
// the Department of Labor's ETA newsroom. DOL does not expose an equivalent
// future iCalendar feed, so this source intentionally creates published
// releases only rather than inventing a Thursday schedule around holidays.
func (s *MacroCalendarService) syncOfficialDOLClaims(ctx context.Context, result *MacroCalendarSyncResult) error {
	body, err := s.fetch(ctx, s.dolClaimsURL)
	if err != nil {
		return fmt.Errorf("load DOL claims release list: %w", err)
	}
	event, observations, ok := parseDOLClaimsRelease(body, s.dolClaimsURL)
	if !ok {
		return errors.New("DOL release list did not contain a current weekly claims report")
	}
	release, saved, err := s.upsertRelease(ctx, event)
	if err != nil {
		return err
	}
	if saved {
		result.ReleasesSaved++
	}
	result.ScheduledFound++
	if release.Status == MacroReleasePublished && strings.TrimSpace(release.SourceHash) != "" {
		return nil
	}
	return s.publishMacroRelease(ctx, &release, s.dolClaimsURL, body, observations, result)
}

// syncOfficialTreasuryYields stores the latest official daily par-yield curve.
// These are reference yields, not tradable prices or a market-data-provider
// estimate. Keeping the source in the macro ledger lets growth-stock research
// explain changes in the discount-rate backdrop without mixing it into the
// equity price-provider chain.
func (s *MacroCalendarService) syncOfficialTreasuryYields(ctx context.Context, result *MacroCalendarSyncResult) error {
	if strings.TrimSpace(s.treasuryYieldURL) == "" {
		return nil
	}
	sourceURL := treasuryCurveRequestURL(s.treasuryYieldURL, s.now())
	body, err := s.fetch(ctx, sourceURL)
	if err != nil {
		return fmt.Errorf("load Treasury yield curve: %w", err)
	}
	_, _, ok := parseTreasuryYieldCurve(body, sourceURL)
	if !ok && treasuryCurveHasOfficialHost(s.treasuryYieldURL) {
		// The new month can have no business-day row yet (for example on a
		// weekend or public holiday). Fall back to the most recent completed
		// month instead of marking the whole rate view unavailable.
		sourceURL = treasuryCurveRequestURL(s.treasuryYieldURL, s.now().AddDate(0, -1, 0))
		body, err = s.fetch(ctx, sourceURL)
		if err != nil {
			return fmt.Errorf("load Treasury yield curve fallback: %w", err)
		}
		_, _, ok = parseTreasuryYieldCurve(body, sourceURL)
	}
	if !ok {
		return errors.New("Treasury yield curve did not contain a usable 2-year and 10-year row")
	}
	return s.syncRecentTreasuryCurve(ctx, result, sourceURL, body, parseTreasuryYieldCurveBefore)
}

// syncOfficialTreasuryRealYields complements the nominal curve with Treasury's
// own inflation-indexed (TIPS) reference curve.  This is deliberately kept as
// a separate release: nominal and real curves can have different publication
// dates, and we must not manufacture a breakeven value from mismatched days.
func (s *MacroCalendarService) syncOfficialTreasuryRealYields(ctx context.Context, result *MacroCalendarSyncResult) error {
	if strings.TrimSpace(s.treasuryRealYieldURL) == "" {
		return nil
	}
	sourceURL := treasuryCurveRequestURL(s.treasuryRealYieldURL, s.now())
	body, err := s.fetch(ctx, sourceURL)
	if err != nil {
		return fmt.Errorf("load Treasury real yield curve: %w", err)
	}
	_, _, ok := parseTreasuryRealYieldCurve(body, sourceURL)
	if !ok && treasuryCurveHasOfficialHost(s.treasuryRealYieldURL) {
		sourceURL = treasuryCurveRequestURL(s.treasuryRealYieldURL, s.now().AddDate(0, -1, 0))
		body, err = s.fetch(ctx, sourceURL)
		if err != nil {
			return fmt.Errorf("load Treasury real yield curve fallback: %w", err)
		}
		_, _, ok = parseTreasuryRealYieldCurve(body, sourceURL)
	}
	if !ok {
		return errors.New("Treasury real yield curve did not contain a usable 10-year row")
	}
	return s.syncRecentTreasuryCurve(ctx, result, sourceURL, body, parseTreasuryRealYieldCurveBefore)
}

// syncRecentTreasuryCurve persists the most recent three business-day rows in
// a single official monthly table response. This makes a new installation
// useful immediately: daily curves no longer need three separate scheduler
// runs before the trend chart has enough points. Repeated syncs are cheap,
// because already-published rows are skipped by their stable source URL.
type treasuryCurveParser func(string, string, time.Time) (beaScheduleEvent, []model.MacroObservation, bool)

func (s *MacroCalendarService) syncRecentTreasuryCurve(ctx context.Context, result *MacroCalendarSyncResult, sourceURL, body string, parser treasuryCurveParser) error {
	var before time.Time
	found := 0
	for range 3 {
		event, observations, ok := parser(body, sourceURL, before)
		if !ok {
			break
		}
		found++
		date, err := time.Parse("2006-01-02", event.ReferencePeriod)
		if err != nil {
			return fmt.Errorf("parse Treasury reference date %q: %w", event.ReferencePeriod, err)
		}
		before = date

		release, saved, err := s.upsertRelease(ctx, event)
		if err != nil {
			return err
		}
		if saved {
			result.ReleasesSaved++
		}
		result.ScheduledFound++
		if release.Status == MacroReleasePublished && strings.TrimSpace(release.SourceHash) != "" {
			continue
		}
		if err := s.publishMacroRelease(ctx, &release, event.SourceURL, body, observations, result); err != nil {
			return err
		}
	}
	if found == 0 {
		return errors.New("Treasury curve did not contain usable business-day rows")
	}
	return nil
}

// Treasury's current Drupal page requires an explicit YYYYMM time-period
// parameter. Older versions accepted an empty value, but now return a 200
// response containing "No Results Found", which is easy to mistake for a
// parser failure. Keep test/injected URLs untouched and only normalize the
// public Treasury endpoint.
func treasuryCurveRequestURL(rawURL string, now time.Time) string {
	parsed, err := url.Parse(rawURL)
	if err != nil || !treasuryCurveHasOfficialHost(rawURL) {
		return rawURL
	}
	location, loadErr := time.LoadLocation("America/New_York")
	if loadErr != nil {
		return rawURL
	}
	query := parsed.Query()
	query.Set("field_tdr_date_value", now.In(location).Format("200601"))
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

func treasuryCurveHasOfficialHost(rawURL string) bool {
	parsed, err := url.Parse(rawURL)
	return err == nil && strings.EqualFold(parsed.Hostname(), "home.treasury.gov")
}

// syncOfficialEIAWeeklyPetroleum uses EIA's public WPSR landing page plus its
// Table 4 CSV. The CSV is a first-party downloadable table, so we do not need
// an EIA API key or a third-party inventory estimate.
func (s *MacroCalendarService) syncOfficialEIAWeeklyPetroleum(ctx context.Context, result *MacroCalendarSyncResult) error {
	if strings.TrimSpace(s.eiaWeeklyPetroleumURL) == "" || strings.TrimSpace(s.eiaWeeklyPetroleumTable4) == "" {
		return nil
	}
	pageBody, err := s.fetch(ctx, s.eiaWeeklyPetroleumURL)
	if err != nil {
		return fmt.Errorf("load EIA weekly petroleum report: %w", err)
	}
	tableBody, err := s.fetch(ctx, s.eiaWeeklyPetroleumTable4)
	if err != nil {
		return fmt.Errorf("load EIA weekly petroleum table 4: %w", err)
	}
	event, observations, ok := parseEIAWeeklyPetroleum(pageBody, tableBody, s.eiaWeeklyPetroleumURL)
	if !ok {
		return errors.New("EIA weekly petroleum report did not contain supported inventory values")
	}
	release, saved, err := s.upsertRelease(ctx, event)
	if err != nil {
		return err
	}
	if saved {
		result.ReleasesSaved++
	}
	result.ScheduledFound++
	if release.Status == MacroReleasePublished && strings.TrimSpace(release.SourceHash) != "" {
		return nil
	}
	return s.publishMacroRelease(ctx, &release, s.eiaWeeklyPetroleumTable4, tableBody, observations, result)
}

func (s *MacroCalendarService) fetch(ctx context.Context, rawURL string) (string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", err
	}
	request.Header.Set("User-Agent", "sec-monitor/0.1 macro-calendar")
	request.Header.Set("Accept", "text/html,application/xhtml+xml,text/calendar;q=0.9")
	response, err := s.client.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", fmt.Errorf("HTTP %d", response.StatusCode)
	}
	limited := io.LimitReader(response.Body, 4<<20)
	body, err := io.ReadAll(limited)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

func (s *MacroCalendarService) fetchCSV(ctx context.Context, rawURL string) (string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", err
	}
	request.Header.Set("User-Agent", "sec-monitor/0.1 macro-calendar")
	request.Header.Set("Accept", "text/csv,application/csv,text/plain;q=0.9,*/*;q=0.1")
	response, err := s.client.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", fmt.Errorf("HTTP %d", response.StatusCode)
	}
	limited := io.LimitReader(response.Body, 4<<20)
	body, err := io.ReadAll(limited)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

type beaScheduleEvent struct {
	Provider        string
	Category        string
	Title           string
	ReferencePeriod string
	ReleaseStage    string
	ScheduledAt     time.Time
	SourceURL       string
	ReleaseURL      string
}

func parseBLSSchedule(raw, baseURL string) ([]beaScheduleEvent, error) {
	// RFC 5545 permits folded lines. Unfold before extracting VEVENT fields so
	// a long BLS summary never silently loses its date or URL.
	raw = strings.ReplaceAll(raw, "\r\n ", "")
	raw = strings.ReplaceAll(raw, "\r\n\t", "")
	raw = strings.ReplaceAll(raw, "\n ", "")
	raw = strings.ReplaceAll(raw, "\n\t", "")
	blocks := strings.Split(raw, "BEGIN:VEVENT")
	result := make([]beaScheduleEvent, 0)
	seen := make(map[string]struct{})
	for _, block := range blocks[1:] {
		fields := map[string]string{}
		for _, line := range strings.Split(block, "\n") {
			line = strings.TrimSpace(line)
			if index := strings.IndexByte(line, ':'); index > 0 {
				key := strings.ToUpper(strings.SplitN(line[:index], ";", 2)[0])
				fields[key] = strings.TrimSpace(line[index+1:])
			}
		}
		title := normalizeMacroText(strings.ReplaceAll(fields["SUMMARY"], `\,`, ","))
		category, ok := macroBLSCategory(title)
		if !ok {
			continue
		}
		scheduledAt, ok := parseICSDateTime(fields["DTSTART"])
		if !ok {
			continue
		}
		sourceURL := baseURL + "#" + url.QueryEscape(category+"-"+scheduledAt.Format("200601021504"))
		if _, exists := seen[sourceURL]; exists {
			continue
		}
		seen[sourceURL] = struct{}{}
		result = append(result, beaScheduleEvent{
			Provider: MacroProviderBLS, Category: category, Title: title, ReferencePeriod: macroReferencePeriod(title),
			ReleaseStage: "monthly", ScheduledAt: scheduledAt, SourceURL: sourceURL,
		})
	}
	sort.Slice(result, func(left, right int) bool { return result[left].ScheduledAt.Before(result[right].ScheduledAt) })
	return result, nil
}

func macroBLSCategory(title string) (string, bool) {
	lower := strings.ToLower(title)
	switch {
	case strings.Contains(lower, "employment situation"):
		return "employment", true
	case strings.Contains(lower, "consumer price index"):
		return "cpi", true
	case strings.Contains(lower, "producer price index"):
		return "ppi", true
	case strings.Contains(lower, "job openings and labor turnover"):
		return "jolts", true
	default:
		return "", false
	}
}

func parseICSDateTime(value string) (time.Time, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, false
	}
	if strings.HasSuffix(value, "Z") {
		parsed, err := time.Parse("20060102T150405Z", value)
		return parsed, err == nil
	}
	location, err := time.LoadLocation("America/New_York")
	if err != nil {
		return time.Time{}, false
	}
	for _, layout := range []string{"20060102T150405", "20060102T1504"} {
		if parsed, err := time.ParseInLocation(layout, value, location); err == nil {
			return parsed.UTC(), true
		}
	}
	return time.Time{}, false
}

func parseBLSReleaseObservations(category, raw string) []model.MacroObservation {
	text := normalizeMacroText(htmlText(raw))
	observations := make([]model.MacroObservation, 0, 4)
	addPercent := func(code, name, field, keyword string) {
		if value := firstPercentAfter(text, keyword); value != nil {
			observations = append(observations, model.MacroObservation{IndicatorCode: code, IndicatorName: name, Frequency: "monthly", Unit: "%", ActualValue: value, SourceField: field})
		}
	}
	switch category {
	case "employment":
		if match := macroBLSNonfarm.FindStringSubmatch(text); len(match) == 3 {
			value := macroFloat(strings.ReplaceAll(match[2], ",", ""))
			if value != nil {
				*value /= 1000 // BLS prose is persons; the UI presents the conventional thousands (K).
				if strings.EqualFold(match[1], "decreased") || strings.EqualFold(match[1], "declined") {
					*value = -*value
				}
				observations = append(observations, model.MacroObservation{IndicatorCode: "nonfarm_payrolls_change_k", IndicatorName: "非农就业人数变动", Frequency: "monthly", Unit: "K", ActualValue: value, SourceField: "BLS Employment Situation / Total nonfarm payroll employment"})
			}
		}
		if match := macroBLSUnemployment.FindStringSubmatch(text); len(match) == 2 {
			observations = append(observations, model.MacroObservation{IndicatorCode: "unemployment_rate", IndicatorName: "失业率", Frequency: "monthly", Unit: "%", ActualValue: macroFloat(match[1]), SourceField: "BLS Employment Situation / Unemployment rate"})
		}
		addPercent("average_hourly_earnings_mom", "平均时薪月率", "BLS Employment Situation / Average hourly earnings", `average hourly earnings`)
	case "cpi":
		addPercent("cpi_mom", "CPI 月率", "BLS Consumer Price Index / All items index", `all items index`)
		addPercent("core_cpi_mom", "核心 CPI 月率", "BLS Consumer Price Index / All items less food and energy", `index for all items less food and energy`)
		if value := firstPercentInSentence(text, `over the last 12 months`, `all items index`); value != nil {
			observations = append(observations, model.MacroObservation{IndicatorCode: "cpi_yoy", IndicatorName: "CPI 年率", Frequency: "monthly", Unit: "%", ActualValue: value, SourceField: "BLS Consumer Price Index / All items over 12 months"})
		}
	case "ppi":
		addPercent("ppi_mom", "PPI 月率", "BLS Producer Price Index / Final demand", `final demand`)
		addPercent("core_ppi_mom", "核心 PPI 月率", "BLS Producer Price Index / Final demand less foods, energy, and trade services", `final demand less foods, energy, and trade services`)
	case "jolts":
		addMillion := func(code, name, field, keyword string) {
			if match := regexp.MustCompile(`(?i)` + keyword + `[^.]{0,220}?\b(?:at|to)\s+([0-9]+(?:\.[0-9]+)?)\s+million`).FindStringSubmatch(text); len(match) == 2 {
				observations = append(observations, model.MacroObservation{IndicatorCode: code, IndicatorName: name, Frequency: "monthly", Unit: "M", ActualValue: macroFloat(match[1]), SourceField: field})
			}
		}
		addMillion("job_openings_m", "JOLTS 职位空缺", "BLS JOLTS / Job openings", `job openings`)
		addMillion("hires_m", "JOLTS 招聘人数", "BLS JOLTS / Hires", `\bhires`)
		addMillion("separations_m", "JOLTS 离职人数", "BLS JOLTS / Total separations", `total separations`)
	}
	return observations
}

func parseFOMCSchedule(raw, baseURL string, now time.Time) ([]beaScheduleEvent, error) {
	text := normalizeMacroText(htmlText(raw))
	location, err := time.LoadLocation("America/New_York")
	if err != nil {
		return nil, err
	}
	// Limit parsing to the current calendar-year section. The Fed publishes
	// adjacent years on one page, and keeping only future meetings avoids
	// presenting past decisions without their official statement values.
	year := now.In(location).Year()
	start := strings.Index(text, strconv.Itoa(year)+" FOMC Meetings")
	if start < 0 {
		start = strings.Index(text, strconv.Itoa(year)+" Meeting")
	}
	if start < 0 {
		return nil, nil
	}
	section := text[start:]
	if next := strings.Index(section[4:], strconv.Itoa(year+1)+" FOMC Meetings"); next >= 0 {
		section = section[:next+4]
	}
	matches := macroFOMCDate.FindAllStringSubmatch(section, -1)
	result := make([]beaScheduleEvent, 0, len(matches))
	for _, match := range matches {
		month, ok := macroMonthNumber(match[1])
		if !ok {
			continue
		}
		day, _ := strconv.Atoi(match[3]) // Statement is issued on the final meeting day.
		scheduledAt := time.Date(year, month, day, 14, 0, 0, 0, location).UTC()
		if !scheduledAt.After(now.UTC()) {
			continue
		}
		sourceURL := baseURL + "#" + url.QueryEscape("fomc-"+scheduledAt.Format("20060102"))
		result = append(result, beaScheduleEvent{Provider: MacroProviderFederalReserve, Category: "fomc", Title: "FOMC 会议与政策声明", ReferencePeriod: scheduledAt.In(location).Format("2006-01-02"), ReleaseStage: "meeting", ScheduledAt: scheduledAt, SourceURL: sourceURL})
	}
	sort.Slice(result, func(left, right int) bool { return result[left].ScheduledAt.Before(result[right].ScheduledAt) })
	return result, nil
}

func parseCensusRetailSchedule(raw, baseURL string) ([]beaScheduleEvent, error) {
	doc, err := html.Parse(strings.NewReader(raw))
	if err != nil {
		return nil, err
	}
	location, err := time.LoadLocation("America/New_York")
	if err != nil {
		return nil, err
	}
	result := []beaScheduleEvent{}
	seen := map[string]struct{}{}
	for _, row := range findHTMLNodes(doc, "tr") {
		text := normalizeMacroText(htmlNodeText(row))
		lower := strings.ToLower(text)
		if !strings.Contains(lower, "advance monthly retail") || strings.Contains(lower, "to be announced") {
			continue
		}
		dates := macroCensusMonthDate.FindAllStringSubmatch(text, -1)
		referencePeriod := macroReference.FindString(text)
		if len(dates) == 0 || referencePeriod == "" {
			continue
		}
		date, parseErr := time.ParseInLocation("January 2, 2006", dates[len(dates)-1][0], location)
		if parseErr != nil {
			continue
		}
		scheduledAt := time.Date(date.Year(), date.Month(), date.Day(), 8, 30, 0, 0, location).UTC()
		sourceURL := baseURL + "#" + url.QueryEscape("retail-sales-"+scheduledAt.Format("200601021504"))
		if _, exists := seen[sourceURL]; exists {
			continue
		}
		seen[sourceURL] = struct{}{}
		result = append(result, beaScheduleEvent{Provider: MacroProviderCensus, Category: "retail_sales", Title: "Advance Monthly Retail Trade Report", ReferencePeriod: referencePeriod, ReleaseStage: "advance", ScheduledAt: scheduledAt, SourceURL: sourceURL})
	}
	sort.Slice(result, func(left, right int) bool { return result[left].ScheduledAt.Before(result[right].ScheduledAt) })
	return result, nil
}

func parseCensusEconomicSchedule(raw, baseURL string) ([]beaScheduleEvent, error) {
	doc, err := html.Parse(strings.NewReader(raw))
	if err != nil {
		return nil, err
	}
	result := []beaScheduleEvent{}
	seen := map[string]struct{}{}
	for _, row := range findHTMLNodes(doc, "tr") {
		text := normalizeMacroText(htmlNodeText(row))
		category, title, frequency, ok := macroCensusEconomicCategory(text)
		if !ok || category == "retail_sales" {
			continue // The dedicated retail schedule is more complete and authoritative for this series.
		}
		scheduledAt, ok := parseCensusCalendarTime(text)
		if !ok {
			continue
		}
		referencePeriod := macroReference.FindString(text)
		if referencePeriod == "" {
			referencePeriod = strings.TrimSpace(macroCensusPeriod.FindString(text))
		}
		sourceURL := baseURL + "#" + url.QueryEscape(category+"-"+scheduledAt.Format("200601021504"))
		if _, exists := seen[sourceURL]; exists {
			continue
		}
		seen[sourceURL] = struct{}{}
		result = append(result, beaScheduleEvent{Provider: MacroProviderCensus, Category: category, Title: title, ReferencePeriod: referencePeriod, ReleaseStage: "scheduled", ScheduledAt: scheduledAt, SourceURL: sourceURL})
		_ = frequency // retained in the event title/category and used by the UI classification.
	}
	sort.Slice(result, func(left, right int) bool { return result[left].ScheduledAt.Before(result[right].ScheduledAt) })
	return result, nil
}

func macroCensusEconomicCategory(text string) (category, title, frequency string, ok bool) {
	lower := strings.ToLower(text)
	switch {
	case strings.Contains(lower, "advance monthly sales for retail and food services"):
		return "retail_sales", "Advance Monthly Retail Trade Report", "monthly", true
	case strings.Contains(lower, "advance report on durable goods"):
		return "durable_goods", "耐用品订单", "monthly", true
	case strings.Contains(lower, "new residential construction"):
		return "housing_starts", "新屋开工与营建许可", "monthly", true
	case strings.Contains(lower, "new residential sales"):
		return "new_home_sales", "新屋销售", "monthly", true
	case strings.Contains(lower, "u.s. international trade in goods and services"):
		return "international_trade", "美国国际贸易", "monthly", true
	case strings.Contains(lower, "advance economic indicators report"):
		return "advance_trade", "预先经济指标（贸易）", "monthly", true
	default:
		return "", "", "", false
	}
}

func parseCensusCalendarTime(text string) (time.Time, bool) {
	match := macroCensusUSDateTime.FindStringSubmatch(text)
	if len(match) != 7 {
		return time.Time{}, false
	}
	location, err := time.LoadLocation("America/New_York")
	if err != nil {
		return time.Time{}, false
	}
	parsed, err := time.ParseInLocation("January 2, 2006 3:04 PM", fmt.Sprintf("%s %s, %s %s:%s %s", match[1], match[2], match[3], match[4], match[5], strings.ToUpper(match[6])), location)
	if err != nil {
		return time.Time{}, false
	}
	return parsed.UTC(), true
}

func parseCensusRetailObservations(raw string) []model.MacroObservation {
	text := normalizeMacroText(htmlText(raw))
	// The official overview presents the latest total-sales change as
	// "Difference ... +0.9%". It is deliberately parsed only when the same
	// nearby text identifies Advance Retail and Food Services Sales.
	location := regexp.MustCompile(`(?i)advance retail and food services sales`).FindStringIndex(text)
	if location == nil {
		return nil
	}
	fragment := text[location[1]:]
	if len(fragment) > 900 {
		fragment = fragment[:900]
	}
	match := macroSignedPercent.FindStringSubmatch(fragment)
	if len(match) != 2 {
		return nil
	}
	value := macroFloat(match[1])
	if value == nil {
		return nil
	}
	return []model.MacroObservation{{IndicatorCode: "retail_sales_mom", IndicatorName: "零售与餐饮服务销售月率", Frequency: "monthly", Unit: "%", ActualValue: value, SourceField: "U.S. Census Bureau / Advance Retail and Food Services Sales"}}
}

func parseCensusEconomicObservations(category, raw string) []model.MacroObservation {
	text := normalizeMacroText(htmlText(raw))
	cardTitle := map[string]string{
		"durable_goods":       "Advance New Orders for Manufactured Durable Goods",
		"housing_starts":      "Housing Starts",
		"new_home_sales":      "New Single-Family Houses Sold",
		"international_trade": "International Trade Deficit in Goods & Services",
		"advance_trade":       "Advance International Trade Deficit in Goods",
	}[category]
	card, ok := censusIndicatorCard(text, cardTitle)
	if !ok {
		return nil
	}
	addPercent := func(code, name, field string) []model.MacroObservation {
		if value := censusCardPercentChange(card); value != nil {
			return []model.MacroObservation{{IndicatorCode: code, IndicatorName: name, Frequency: "monthly", Unit: "%", ActualValue: value, SourceField: field}}
		}
		return nil
	}
	switch category {
	case "durable_goods":
		return addPercent("durable_goods_mom", "耐用品订单月率", "U.S. Census Bureau / Advance New Orders for Manufactured Durable Goods")
	case "housing_starts":
		return addPercent("housing_starts_mom", "新屋开工月率", "U.S. Census Bureau / Housing Starts")
	case "new_home_sales":
		return addPercent("new_home_sales_mom", "新屋销售月率", "U.S. Census Bureau / New Single-Family Houses Sold")
	case "international_trade", "advance_trade":
		code, name, field := "trade_deficit_b", "国际贸易逆差", "U.S. Census Bureau / International Trade Deficit in Goods & Services"
		if category == "advance_trade" {
			code, name, field = "advance_goods_trade_deficit_b", "预先商品贸易逆差", "U.S. Census Bureau / Advance International Trade Deficit in Goods"
		}
		observations := []model.MacroObservation{}
		if value := censusCardBillions(card); value != nil {
			observations = append(observations, model.MacroObservation{IndicatorCode: code, IndicatorName: name, Frequency: "monthly", Unit: "B", ActualValue: value, SourceField: field})
		}
		if value := censusCardPercentChange(card); value != nil {
			observations = append(observations, model.MacroObservation{IndicatorCode: code + "_mom", IndicatorName: name + "月变动", Frequency: "monthly", Unit: "%", ActualValue: value, SourceField: field})
		}
		return observations
	default:
		return nil
	}
}

func censusIndicatorCard(text, title string) (string, bool) {
	index := strings.Index(strings.ToLower(text), strings.ToLower(title))
	if index < 0 {
		return "", false
	}
	card := text[index:]
	if len(card) > 1300 {
		card = card[:1300]
	}
	return card, true
}

func censusCardPercentChange(card string) *float64 {
	index := strings.Index(strings.ToLower(card), "difference")
	if index < 0 {
		return nil
	}
	fragment := cleanCensusCardText(card[index:])
	match := macroCensusCardPercent.FindStringSubmatch(fragment)
	if len(match) != 3 {
		return nil
	}
	value := macroFloat(match[2])
	if value != nil && match[1] == "-" {
		*value = -*value
	}
	return value
}

func censusCardBillions(card string) *float64 {
	match := macroCensusCardBillions.FindStringSubmatch(cleanCensusCardText(card))
	if len(match) != 2 {
		return nil
	}
	return macroFloat(strings.ReplaceAll(match[1], ",", ""))
}

func cleanCensusCardText(value string) string {
	replacer := strings.NewReplacer("−", "-", "^", " ", "_", " ", "{", " ", "}", " ", "$", " ")
	return normalizeMacroText(replacer.Replace(value))
}

func parseDOLClaimsRelease(raw, baseURL string) (beaScheduleEvent, []model.MacroObservation, bool) {
	text := normalizeMacroText(htmlText(raw))
	index := strings.Index(strings.ToLower(text), "unemployment insurance weekly claims report")
	if index < 0 {
		return beaScheduleEvent{}, nil, false
	}
	fragment := text[index:]
	if len(fragment) > 6000 {
		fragment = fragment[:6000]
	}
	dateMatch := macroUSReleaseDate.FindStringSubmatch(fragment)
	claimsMatch := macroDOLInitialClaims.FindStringSubmatch(fragment)
	if len(dateMatch) != 2 || len(claimsMatch) != 2 {
		return beaScheduleEvent{}, nil, false
	}
	location, err := time.LoadLocation("America/New_York")
	if err != nil {
		return beaScheduleEvent{}, nil, false
	}
	date, err := time.ParseInLocation("January 2, 2006", dateMatch[1], location)
	if err != nil {
		return beaScheduleEvent{}, nil, false
	}
	scheduledAt := time.Date(date.Year(), date.Month(), date.Day(), 8, 30, 0, 0, location).UTC()
	initialClaims := macroFloat(strings.ReplaceAll(claimsMatch[1], ",", ""))
	if initialClaims == nil {
		return beaScheduleEvent{}, nil, false
	}
	*initialClaims /= 1000
	observations := []model.MacroObservation{{IndicatorCode: "initial_claims_k", IndicatorName: "初请失业金人数", Frequency: "weekly", Unit: "K", ActualValue: initialClaims, SourceField: "U.S. Department of Labor ETA / advance seasonally adjusted initial claims"}}
	if match := macroDOLFourWeekAverage.FindStringSubmatch(fragment); len(match) == 2 {
		if average := macroFloat(strings.ReplaceAll(match[1], ",", "")); average != nil {
			*average /= 1000
			observations = append(observations, model.MacroObservation{IndicatorCode: "initial_claims_4w_avg_k", IndicatorName: "初请失业金四周均值", Frequency: "weekly", Unit: "K", ActualValue: average, SourceField: "U.S. Department of Labor ETA / 4-week moving average"})
		}
	}
	sourceURL := baseURL + "#" + url.QueryEscape("initial-claims-"+scheduledAt.Format("20060102"))
	event := beaScheduleEvent{Provider: MacroProviderDOL, Category: "initial_claims", Title: "Unemployment Insurance Weekly Claims Report", ReferencePeriod: strings.TrimSpace(macroDOLWeekEnding.FindString(fragment)), ReleaseStage: "weekly", ScheduledAt: scheduledAt, SourceURL: sourceURL}
	return event, observations, true
}

// parseTreasuryYieldCurve extracts the newest business-day row from Treasury's
// own yield-curve table. We retain the short end, benchmark notes and long
// bond so the calendar can show both the level and the shape of the curve.
func parseTreasuryYieldCurve(raw, baseURL string) (beaScheduleEvent, []model.MacroObservation, bool) {
	return parseTreasuryYieldCurveBefore(raw, baseURL, time.Time{})
}

// parseTreasuryYieldCurveBefore returns the newest row strictly before the
// given calendar date. A zero date means "newest row".
func parseTreasuryYieldCurveBefore(raw, baseURL string, before time.Time) (beaScheduleEvent, []model.MacroObservation, bool) {
	doc, err := html.Parse(strings.NewReader(raw))
	if err != nil {
		return beaScheduleEvent{}, nil, false
	}
	var latestDate time.Time
	var latestValues map[string]float64
	for _, table := range findHTMLNodes(doc, "table") {
		rows := findHTMLNodes(table, "tr")
		if len(rows) < 2 {
			continue
		}
		headings := macroHTMLRowCells(rows[0])
		dateIndex, twoYearIndex, tenYearIndex := macroTreasuryColumnIndex(headings, "date"), macroTreasuryColumnIndex(headings, "2 yr"), macroTreasuryColumnIndex(headings, "10 yr")
		if dateIndex < 0 || twoYearIndex < 0 || tenYearIndex < 0 {
			continue
		}
		for _, row := range rows[1:] {
			cells := macroHTMLRowCells(row)
			if len(cells) <= tenYearIndex {
				continue
			}
			date, dateErr := time.Parse("01/02/2006", strings.TrimSpace(cells[dateIndex]))
			twoYear, twoOK := macroTreasuryRate(cells[twoYearIndex])
			tenYear, tenOK := macroTreasuryRate(cells[tenYearIndex])
			if dateErr != nil || !twoOK || !tenOK || (!before.IsZero() && !date.Before(before)) || !date.After(latestDate) {
				continue
			}
			latestDate = date
			latestValues = map[string]float64{"2y": twoYear, "10y": tenYear}
			for key, heading := range map[string]string{"3m": "3 mo", "5y": "5 yr", "30y": "30 yr"} {
				index := macroTreasuryColumnIndex(headings, heading)
				if index >= 0 && len(cells) > index {
					if value, valueOK := macroTreasuryRate(cells[index]); valueOK {
						latestValues[key] = value
					}
				}
			}
		}
	}
	if latestDate.IsZero() || latestValues == nil {
		return beaScheduleEvent{}, nil, false
	}
	location, err := time.LoadLocation("America/New_York")
	if err != nil {
		return beaScheduleEvent{}, nil, false
	}
	scheduledAt := time.Date(latestDate.Year(), latestDate.Month(), latestDate.Day(), 16, 0, 0, 0, location).UTC()
	twoYear, tenYear := latestValues["2y"], latestValues["10y"]
	// Treasury publishes the inputs to two decimals. Round the derived basis
	// point spread too, so a binary float artifact never leaks into storage or
	// into a published value comparison.
	spreadBP := math.Round((tenYear-twoYear)*100*100) / 100
	event := beaScheduleEvent{
		Provider: MacroProviderTreasury, Category: "treasury_yields", Title: "Daily Treasury Par Yield Curve Rates",
		ReferencePeriod: latestDate.Format("2006-01-02"), ReleaseStage: "daily", ScheduledAt: scheduledAt,
		SourceURL: baseURL + "#" + url.QueryEscape("yield-curve-"+latestDate.Format("20060102")),
	}
	observations := []model.MacroObservation{
		{IndicatorCode: "treasury_3m_yield", IndicatorName: "美国国债 3 个月期收益率", Frequency: "daily", Unit: "%", ActualValue: treasuryValuePointer(latestValues, "3m"), SourceField: "U.S. Treasury / Daily Treasury Par Yield Curve / 3 Mo"},
		{IndicatorCode: "treasury_2y_yield", IndicatorName: "美国国债 2 年期收益率", Frequency: "daily", Unit: "%", ActualValue: &twoYear, SourceField: "U.S. Treasury / Daily Treasury Par Yield Curve / 2 Yr"},
		{IndicatorCode: "treasury_5y_yield", IndicatorName: "美国国债 5 年期收益率", Frequency: "daily", Unit: "%", ActualValue: treasuryValuePointer(latestValues, "5y"), SourceField: "U.S. Treasury / Daily Treasury Par Yield Curve / 5 Yr"},
		{IndicatorCode: "treasury_10y_yield", IndicatorName: "美国国债 10 年期收益率", Frequency: "daily", Unit: "%", ActualValue: &tenYear, SourceField: "U.S. Treasury / Daily Treasury Par Yield Curve / 10 Yr"},
		{IndicatorCode: "treasury_30y_yield", IndicatorName: "美国国债 30 年期收益率", Frequency: "daily", Unit: "%", ActualValue: treasuryValuePointer(latestValues, "30y"), SourceField: "U.S. Treasury / Daily Treasury Par Yield Curve / 30 Yr"},
		{IndicatorCode: "treasury_10y_2y_spread_bp", IndicatorName: "美债 10Y-2Y 利差", Frequency: "daily", Unit: "bp", ActualValue: float64Ptr(spreadBP), SourceField: "U.S. Treasury / Daily Treasury Par Yield Curve / 10 Yr minus 2 Yr"},
	}
	if threeMonth, exists := latestValues["3m"]; exists {
		spread := math.Round((tenYear-threeMonth)*100*100) / 100
		observations = append(observations, model.MacroObservation{IndicatorCode: "treasury_10y_3m_spread_bp", IndicatorName: "美债 10Y-3M 利差", Frequency: "daily", Unit: "bp", ActualValue: float64Ptr(spread), SourceField: "U.S. Treasury / Daily Treasury Par Yield Curve / 10 Yr minus 3 Mo"})
	}
	return event, compactMacroObservations(observations), true
}

// parseTreasuryRealYieldCurve stores the official TIPS real-yield curve. It
// intentionally does not calculate an implied inflation expectation here:
// that requires nominal and real data from the same business day.
func parseTreasuryRealYieldCurve(raw, baseURL string) (beaScheduleEvent, []model.MacroObservation, bool) {
	return parseTreasuryRealYieldCurveBefore(raw, baseURL, time.Time{})
}

// parseTreasuryRealYieldCurveBefore mirrors the nominal curve helper and lets
// one official response seed a compact three-business-day history.
func parseTreasuryRealYieldCurveBefore(raw, baseURL string, before time.Time) (beaScheduleEvent, []model.MacroObservation, bool) {
	doc, err := html.Parse(strings.NewReader(raw))
	if err != nil {
		return beaScheduleEvent{}, nil, false
	}
	var latestDate time.Time
	var latestValues map[string]float64
	for _, table := range findHTMLNodes(doc, "table") {
		rows := findHTMLNodes(table, "tr")
		if len(rows) < 2 {
			continue
		}
		headings := macroHTMLRowCells(rows[0])
		dateIndex, tenYearIndex := macroTreasuryColumnIndex(headings, "date"), macroTreasuryColumnIndex(headings, "10 yr")
		if dateIndex < 0 || tenYearIndex < 0 {
			continue
		}
		for _, row := range rows[1:] {
			cells := macroHTMLRowCells(row)
			if len(cells) <= tenYearIndex {
				continue
			}
			date, dateErr := time.Parse("01/02/2006", strings.TrimSpace(cells[dateIndex]))
			tenYear, tenOK := macroTreasuryRate(cells[tenYearIndex])
			if dateErr != nil || !tenOK || (!before.IsZero() && !date.Before(before)) || !date.After(latestDate) {
				continue
			}
			latestDate = date
			latestValues = map[string]float64{"10y": tenYear}
			for key, heading := range map[string]string{"5y": "5 yr", "30y": "30 yr"} {
				index := macroTreasuryColumnIndex(headings, heading)
				if index >= 0 && len(cells) > index {
					if value, valueOK := macroTreasuryRate(cells[index]); valueOK {
						latestValues[key] = value
					}
				}
			}
		}
	}
	if latestDate.IsZero() || latestValues == nil {
		return beaScheduleEvent{}, nil, false
	}
	location, err := time.LoadLocation("America/New_York")
	if err != nil {
		return beaScheduleEvent{}, nil, false
	}
	scheduledAt := time.Date(latestDate.Year(), latestDate.Month(), latestDate.Day(), 16, 0, 0, 0, location).UTC()
	event := beaScheduleEvent{Provider: MacroProviderTreasury, Category: "treasury_real_yields", Title: "Daily Treasury Real Yield Curve Rates (TIPS)", ReferencePeriod: latestDate.Format("2006-01-02"), ReleaseStage: "daily", ScheduledAt: scheduledAt, SourceURL: baseURL + "#" + url.QueryEscape("real-yield-curve-"+latestDate.Format("20060102"))}
	observations := []model.MacroObservation{
		{IndicatorCode: "treasury_5y_real_yield", IndicatorName: "美国国债 5 年期实际收益率（TIPS）", Frequency: "daily", Unit: "%", ActualValue: treasuryValuePointer(latestValues, "5y"), SourceField: "U.S. Treasury / Daily Treasury Real Yield Curve / 5 Yr"},
		{IndicatorCode: "treasury_10y_real_yield", IndicatorName: "美国国债 10 年期实际收益率（TIPS）", Frequency: "daily", Unit: "%", ActualValue: treasuryValuePointer(latestValues, "10y"), SourceField: "U.S. Treasury / Daily Treasury Real Yield Curve / 10 Yr"},
		{IndicatorCode: "treasury_30y_real_yield", IndicatorName: "美国国债 30 年期实际收益率（TIPS）", Frequency: "daily", Unit: "%", ActualValue: treasuryValuePointer(latestValues, "30y"), SourceField: "U.S. Treasury / Daily Treasury Real Yield Curve / 30 Yr"},
	}
	return event, compactMacroObservations(observations), true
}

func treasuryValuePointer(values map[string]float64, key string) *float64 {
	value, exists := values[key]
	if !exists {
		return nil
	}
	return float64Ptr(value)
}

func compactMacroObservations(observations []model.MacroObservation) []model.MacroObservation {
	result := make([]model.MacroObservation, 0, len(observations))
	for _, observation := range observations {
		if observation.ActualValue != nil {
			result = append(result, observation)
		}
	}
	return result
}

func macroHTMLRowCells(row *html.Node) []string {
	cells := []string{}
	for child := row.FirstChild; child != nil; child = child.NextSibling {
		if child.Type != html.ElementNode || (child.Data != "td" && child.Data != "th") {
			continue
		}
		cells = append(cells, normalizeMacroText(htmlNodeText(child)))
	}
	return cells
}

func macroTreasuryColumnIndex(headings []string, want string) int {
	for index, heading := range headings {
		if strings.EqualFold(strings.TrimSpace(heading), want) {
			return index
		}
	}
	return -1
}

func macroTreasuryRate(value string) (float64, bool) {
	value = strings.TrimSpace(strings.ToUpper(value))
	if value == "" || value == "N/A" {
		return 0, false
	}
	parsed, err := strconv.ParseFloat(value, 64)
	return parsed, err == nil
}

func float64Ptr(value float64) *float64 { return &value }

func roundMacroValue(value float64, places int) float64 {
	scale := math.Pow10(places)
	return math.Round(value*scale) / scale
}

// parseEIAWeeklyPetroleum combines release timing from WPSR's landing page
// with current and prior-week stock levels from Table 4. Values are in million
// barrels, exactly as the EIA CSV publishes them.
func parseEIAWeeklyPetroleum(pageRaw, tableRaw, baseURL string) (beaScheduleEvent, []model.MacroObservation, bool) {
	text := normalizeMacroText(htmlText(pageRaw))
	match := macroEIAWeeklyRelease.FindStringSubmatch(text)
	if len(match) != 3 {
		return beaScheduleEvent{}, nil, false
	}
	location, err := time.LoadLocation("America/New_York")
	if err != nil {
		return beaScheduleEvent{}, nil, false
	}
	weekEnding, err := time.ParseInLocation("January 2, 2006", match[1], location)
	if err != nil {
		return beaScheduleEvent{}, nil, false
	}
	releaseDate, err := time.ParseInLocation("January 2, 2006", match[2], location)
	if err != nil {
		return beaScheduleEvent{}, nil, false
	}
	records, err := csv.NewReader(strings.NewReader(tableRaw)).ReadAll()
	if err != nil || len(records) < 2 {
		return beaScheduleEvent{}, nil, false
	}
	type inventoryRow struct{ current, prior float64 }
	values := map[string]inventoryRow{}
	for _, row := range records[1:] {
		if len(row) < 3 {
			continue
		}
		current, currentErr := strconv.ParseFloat(strings.ReplaceAll(strings.TrimSpace(row[1]), ",", ""), 64)
		prior, priorErr := strconv.ParseFloat(strings.ReplaceAll(strings.TrimSpace(row[2]), ",", ""), 64)
		if currentErr != nil || priorErr != nil {
			continue
		}
		switch strings.TrimSpace(row[0]) {
		case "Commercial (Excluding SPR)":
			values["crude"] = inventoryRow{current: current, prior: prior}
		case "Total Motor Gasoline":
			values["gasoline"] = inventoryRow{current: current, prior: prior}
		case "Distillate Fuel Oil":
			values["distillate"] = inventoryRow{current: current, prior: prior}
		}
	}
	if len(values) == 0 {
		return beaScheduleEvent{}, nil, false
	}
	scheduledAt := time.Date(releaseDate.Year(), releaseDate.Month(), releaseDate.Day(), 10, 30, 0, 0, location).UTC()
	event := beaScheduleEvent{Provider: MacroProviderEIA, Category: "petroleum_inventories", Title: "Weekly Petroleum Status Report", ReferencePeriod: "week ending " + weekEnding.Format("2006-01-02"), ReleaseStage: "weekly", ScheduledAt: scheduledAt, SourceURL: baseURL + "#" + url.QueryEscape("weekly-petroleum-"+releaseDate.Format("20060102"))}
	definitions := []struct {
		key, code, name, field string
	}{
		{"crude", "commercial_crude_oil_inventory_mmbbl", "美国商业原油库存（不含 SPR）", "EIA WPSR Table 4 / Commercial (Excluding SPR)"},
		{"gasoline", "motor_gasoline_inventory_mmbbl", "美国汽油库存", "EIA WPSR Table 4 / Total Motor Gasoline"},
		{"distillate", "distillate_inventory_mmbbl", "美国馏分油库存", "EIA WPSR Table 4 / Distillate Fuel Oil"},
	}
	observations := make([]model.MacroObservation, 0, len(definitions)*2)
	for _, definition := range definitions {
		row, exists := values[definition.key]
		if !exists {
			continue
		}
		current, change := row.current, roundMacroValue(row.current-row.prior, 3)
		observations = append(observations,
			model.MacroObservation{IndicatorCode: definition.code, IndicatorName: definition.name, Frequency: "weekly", Unit: "MMbbl", ActualValue: &current, SourceField: definition.field},
			model.MacroObservation{IndicatorCode: definition.code + "_wow", IndicatorName: definition.name + "周变动", Frequency: "weekly", Unit: "MMbbl", ActualValue: float64Ptr(change), SourceField: definition.field + " / current week minus prior week"},
		)
	}
	return event, observations, len(observations) > 0
}

func macroMonthNumber(value string) (time.Month, bool) {
	for month := time.January; month <= time.December; month++ {
		if strings.EqualFold(month.String(), value) {
			return month, true
		}
	}
	return 0, false
}

func parseBEASchedule(raw, baseURL string, now time.Time) ([]beaScheduleEvent, error) {
	doc, err := html.Parse(strings.NewReader(raw))
	if err != nil {
		return nil, err
	}
	rows := findHTMLNodes(doc, "tr")
	seen := map[string]struct{}{}
	result := []beaScheduleEvent{}
	for _, row := range rows {
		text := normalizeMacroText(htmlNodeText(row))
		category, stage, ok := macroBEACategory(text)
		if !ok {
			continue
		}
		scheduledAt, ok := parseBEAScheduleTime(text, now)
		if !ok {
			continue
		}
		title := macroReleaseTitle(text)
		if title == "" {
			continue
		}
		links := htmlNodeLinks(row, baseURL)
		releaseURL := ""
		for _, link := range links {
			if strings.Contains(link, "/news/") {
				releaseURL = link
				break
			}
		}
		// A scheduled event's eventual release URL is often absent until it is
		// published. Keep the persisted identity tied to the official schedule
		// and time, otherwise the same event would be duplicated when BEA later
		// adds the news-release link. The parsed observation retains that exact
		// release URL as its source.
		sourceURL := baseURL + "#" + url.QueryEscape(category+"-"+scheduledAt.Format("200601021504"))
		key := fmt.Sprintf("%s|%s|%s", category, scheduledAt.Format(time.RFC3339), title)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, beaScheduleEvent{Category: category, Title: title, ReferencePeriod: macroReferencePeriod(title), ReleaseStage: stage, ScheduledAt: scheduledAt, SourceURL: sourceURL, ReleaseURL: releaseURL})
	}
	sort.Slice(result, func(left, right int) bool { return result[left].ScheduledAt.Before(result[right].ScheduledAt) })
	return result, nil
}

func parseBEAReleaseObservations(event beaScheduleEvent, raw string) []model.MacroObservation {
	text := normalizeMacroText(htmlText(raw))
	observations := []model.MacroObservation{}
	add := func(code, name, frequency, field string, value *float64) {
		if value == nil {
			return
		}
		observations = append(observations, model.MacroObservation{IndicatorCode: code, IndicatorName: name, Frequency: frequency, Unit: "%", ActualValue: value, SourceField: field})
	}
	if event.Category == "personal_income_outlays" {
		add("pce_mom", "个人消费支出月率", "monthly", "BEA Personal Income and Outlays / Personal consumption expenditures", firstPercentAfter(text, `personal consumption expenditures`))
		add("core_pce_mom", "核心 PCE 物价指数月率", "monthly", "BEA Personal Income and Outlays / PCE price index excluding food and energy", firstPercentAfter(text, `pce price index, excluding food and energy`))
		add("core_pce_yoy", "核心 PCE 物价指数年率", "monthly", "BEA Personal Income and Outlays / From same month one year ago", firstPercentInSentence(text, `from the same month one year ago`, `excluding food and energy`))
	}
	if event.Category == "gdp" {
		add("real_gdp_qoq_annualized", "实际 GDP 年化季率", "quarterly", "BEA GDP release / Real gross domestic product", firstPercentAfter(text, `real gross domestic product \(gdp\)`))
		add("real_pce_qoq_annualized", "实际个人消费支出年化季率", "quarterly", "BEA GDP release / Real personal consumption expenditures", firstPercentAfter(text, `real personal consumption expenditures`))
		add("core_pce_qoq_annualized", "核心 PCE 物价指数年化季率", "quarterly", "BEA GDP release / PCE price index excluding food and energy", firstPercentAfter(text, `pce price index excluding food and energy`))
	}
	return observations
}

func macroBEACategory(text string) (string, string, bool) {
	lower := strings.ToLower(text)
	if strings.Contains(lower, "personal income and outlays") {
		return "personal_income_outlays", "monthly", true
	}
	if (strings.Contains(lower, "gdp") || strings.Contains(lower, "gross domestic product")) && (strings.Contains(lower, "estimate") || strings.Contains(lower, "gross domestic product")) {
		stage := ""
		for _, candidate := range []string{"advance", "second", "third"} {
			if strings.Contains(lower, candidate+" estimate") {
				stage = candidate
				break
			}
		}
		return "gdp", stage, true
	}
	return "", "", false
}

var (
	macroWhitespace         = regexp.MustCompile(`\s+`)
	macroDateTime           = regexp.MustCompile(`(?i)(January|February|March|April|May|June|July|August|September|October|November|December)\s+(\d{1,2})\s+(\d{1,2}):(\d{2})\s*(a\.m\.|p\.m\.|am|pm)`)
	macroReference          = regexp.MustCompile(`(?i)(January|February|March|April|May|June|July|August|September|October|November|December)\s+20\d{2}|([1-4](?:st|nd|rd|th)?\s+Quarter\s+20\d{2})`)
	macroPercentAfter       = regexp.MustCompile(`(?i)(?:increased|decreased|rose|fell)\s+(?:\$[\d,.]+\s+\([^)]*?\)\s+)?(?:at an annual rate of\s+)?([0-9]+(?:\.[0-9]+)?)\s+percent`)
	macroBLSNonfarm         = regexp.MustCompile(`(?i)nonfarm payroll employment\s+(increased|decreased|declined)\s+by\s+([\d,]+)`)
	macroBLSUnemployment    = regexp.MustCompile(`(?i)unemployment rate[^.]{0,180}?\b(?:at|was)\s+([0-9]+(?:\.[0-9]+)?)\s+percent`)
	macroFOMCDate           = regexp.MustCompile(`(?i)(January|February|March|April|May|June|July|August|September|October|November|December)\s+(\d{1,2})\s*(?:[-–]\s*(\d{1,2}))`)
	macroCensusMonthDate    = regexp.MustCompile(`(?i)(January|February|March|April|May|June|July|August|September|October|November|December)\s+\d{1,2},\s+20\d{2}`)
	macroSignedPercent      = regexp.MustCompile(`([+-]?[0-9]+(?:\.[0-9]+)?)\s*%`)
	macroCensusUSDateTime   = regexp.MustCompile(`(?i)(January|February|March|April|May|June|July|August|September|October|November|December)\s+(\d{1,2}),\s+(20\d{2})\s+(\d{1,2}):(\d{2})\s*(AM|PM)`)
	macroCensusPeriod       = regexp.MustCompile(`(?i)(January|February|March|April|May|June|July|August|September|October|November|December)\s+20\d{2}|(?:First|Second|Third|Fourth)\s+Quarter\s+20\d{2}`)
	macroUSReleaseDate      = regexp.MustCompile(`(?i)\b((?:January|February|March|April|May|June|July|August|September|October|November|December)\s+\d{1,2},\s+20\d{2})\b`)
	macroDOLInitialClaims   = regexp.MustCompile(`(?i)advance figure for seasonally adjusted initial claims was\s+([\d,]+)`)
	macroDOLFourWeekAverage = regexp.MustCompile(`(?i)4-week moving average was\s+([\d,]+)`)
	macroDOLWeekEnding      = regexp.MustCompile(`(?i)week ending\s+(?:January|February|March|April|May|June|July|August|September|October|November|December)\s+\d{1,2}`)
	macroCensusCardPercent  = regexp.MustCompile(`([+-]?)\s*([0-9]+(?:\.[0-9]+)?)\s*%`)
	macroCensusCardBillions = regexp.MustCompile(`(?i)([0-9][0-9,]*(?:\.[0-9]+)?)\s*(?:B|billion)\b`)
	macroEIAWeeklyRelease   = regexp.MustCompile(`(?i)data for week ending\s+([A-Za-z]+\s+\d{1,2},\s+20\d{2})\s+release date:\s+([A-Za-z]+\s+\d{1,2},\s+20\d{2})`)
)

func parseBEAScheduleTime(text string, now time.Time) (time.Time, bool) {
	match := macroDateTime.FindStringSubmatch(text)
	if len(match) == 0 {
		return time.Time{}, false
	}
	location, err := time.LoadLocation("America/New_York")
	if err != nil {
		return time.Time{}, false
	}
	parsed, err := time.ParseInLocation("January 2 3:04 PM", fmt.Sprintf("%s %s %s:%s %s", match[1], match[2], match[3], match[4], strings.ToUpper(strings.ReplaceAll(match[5], ".", ""))), location)
	if err != nil {
		return time.Time{}, false
	}
	year := now.In(location).Year()
	candidate := time.Date(year, parsed.Month(), parsed.Day(), parsed.Hour(), parsed.Minute(), 0, 0, location)
	if candidate.Before(now.In(location).AddDate(0, -9, 0)) {
		candidate = candidate.AddDate(1, 0, 0)
	}
	return candidate.UTC(), true
}

func macroReleaseTitle(text string) string {
	text = strings.TrimSpace(text)
	if match := macroDateTime.FindStringIndex(text); match != nil {
		text = strings.TrimSpace(text[match[1]:])
	}
	return text
}

func macroReferencePeriod(title string) string {
	return strings.TrimSpace(macroReference.FindString(title))
}

func firstPercentAfter(text, keyword string) *float64 {
	location := regexp.MustCompile(`(?i)` + keyword).FindStringIndex(text)
	if location == nil {
		return nil
	}
	fragment := text[location[1]:]
	if len(fragment) > 700 {
		fragment = fragment[:700]
	}
	match := macroPercentAfter.FindStringSubmatch(fragment)
	if len(match) < 2 {
		return nil
	}
	return macroFloat(match[1])
}

func firstPercentInSentence(text, firstKeyword, secondKeyword string) *float64 {
	start := regexp.MustCompile(`(?i)` + firstKeyword).FindStringIndex(text)
	if start == nil {
		return nil
	}
	fragment := text[start[1]:]
	if len(fragment) > 1000 {
		fragment = fragment[:1000]
	}
	second := regexp.MustCompile(`(?i)` + secondKeyword).FindStringIndex(fragment)
	if second == nil {
		return nil
	}
	return firstPercentAfter(fragment[second[1]:], `^`)
}

func macroFloat(value string) *float64 {
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return nil
	}
	return &parsed
}

func htmlText(raw string) string {
	doc, err := html.Parse(strings.NewReader(raw))
	if err != nil {
		return raw
	}
	return htmlNodeText(doc)
}

func htmlNodeText(node *html.Node) string {
	parts := []string{}
	var walk func(*html.Node)
	walk = func(current *html.Node) {
		if current.Type == html.TextNode {
			parts = append(parts, current.Data)
		}
		for child := current.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(node)
	return strings.Join(parts, " ")
}

func findHTMLNodes(node *html.Node, name string) []*html.Node {
	items := []*html.Node{}
	var walk func(*html.Node)
	walk = func(current *html.Node) {
		if current.Type == html.ElementNode && current.Data == name {
			items = append(items, current)
		}
		for child := current.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(node)
	return items
}

func htmlNodeLinks(node *html.Node, base string) []string {
	baseURL, err := url.Parse(base)
	if err != nil {
		return nil
	}
	links := []string{}
	var walk func(*html.Node)
	walk = func(current *html.Node) {
		if current.Type == html.ElementNode && current.Data == "a" {
			for _, attribute := range current.Attr {
				if attribute.Key == "href" && strings.TrimSpace(attribute.Val) != "" {
					if parsed, parseErr := url.Parse(attribute.Val); parseErr == nil {
						links = append(links, baseURL.ResolveReference(parsed).String())
					}
				}
			}
		}
		for child := current.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(node)
	return links
}

func normalizeMacroText(value string) string {
	return strings.TrimSpace(macroWhitespace.ReplaceAllString(value, " "))
}

func hashMacroBody(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func sanitizeMacroError(err error) string {
	if err == nil {
		return ""
	}
	return strings.TrimSpace(strings.ReplaceAll(err.Error(), "\n", " "))
}
