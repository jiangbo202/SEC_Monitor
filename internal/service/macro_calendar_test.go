package service

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"sec_monitor/internal/model"
)

type macroRoundTripper map[string]string

func (r macroRoundTripper) Do(request *http.Request) (*http.Response, error) {
	body, ok := r[request.URL.String()]
	if !ok {
		return &http.Response{StatusCode: http.StatusNotFound, Body: io.NopCloser(strings.NewReader("not found")), Header: make(http.Header)}, nil
	}
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
}

func TestParseBEAScheduleAndObservations(t *testing.T) {
	now := time.Date(2026, time.July, 29, 12, 0, 0, 0, time.UTC)
	schedule := `<table>
<tr><td>July 30 8:30 AM</td><td><a href="/news/2026/gdp-advance-estimate-2nd-quarter-2026.htm">Gross Domestic Product, 2nd Quarter 2026 (Advance Estimate)</a></td></tr>
<tr><td>July 30 8:30 AM</td><td><a href="/news/2026/personal-income-and-outlays-june-2026.htm">Personal Income and Outlays, June 2026</a></td></tr>
</table>`
	events, err := parseBEASchedule(schedule, "https://www.bea.gov/news/schedule/", now)
	if err != nil {
		t.Fatalf("parseBEASchedule: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("events = %d, want 2: %#v", len(events), events)
	}
	if events[0].ScheduledAt.Format(time.RFC3339) != "2026-07-30T12:30:00Z" {
		t.Fatalf("scheduled at = %s", events[0].ScheduledAt)
	}

	byCategory := map[string]beaScheduleEvent{}
	for _, event := range events {
		byCategory[event.Category] = event
	}
	pce := parseBEAReleaseObservations(byCategory["personal_income_outlays"], `<p>Personal consumption expenditures increased 0.3 percent. From the same month one year ago, the PCE price index increased 2.6 percent. Excluding food and energy, the PCE price index increased 2.8 percent. From the preceding month, the PCE price index, excluding food and energy, increased 0.2 percent.</p>`)
	assertMacroValues(t, pce, map[string]float64{"pce_mom": 0.3, "core_pce_mom": 0.2, "core_pce_yoy": 2.8})
	gdp := parseBEAReleaseObservations(byCategory["gdp"], `<p>Real gross domestic product (GDP) increased at an annual rate of 1.5 percent. Real personal consumption expenditures increased 3.2 percent. The PCE price index excluding food and energy increased 3.4 percent.</p>`)
	assertMacroValues(t, gdp, map[string]float64{"real_gdp_qoq_annualized": 1.5, "real_pce_qoq_annualized": 3.2, "core_pce_qoq_annualized": 3.4})
}

func TestMacroCalendarSyncStoresOfficialReleaseOnce(t *testing.T) {
	db := testDB(t)
	now := time.Date(2026, time.July, 31, 12, 0, 0, 0, time.UTC)
	const scheduleURL = "https://bea.test/schedule/"
	const releaseURL = "https://bea.test/news/pio.htm"
	service := NewMacroCalendarService(db)
	service.scheduleURL = scheduleURL
	service.now = func() time.Time { return now }
	service.client = macroRoundTripper{
		scheduleURL: `<table><tr><td>July 30 8:30 AM</td><td><a href="/news/pio.htm">Personal Income and Outlays, June 2026</a></td></tr></table>`,
		releaseURL:  `<p>Personal consumption expenditures increased 0.3 percent. From the same month one year ago, the PCE price index increased 2.6 percent. Excluding food and energy, the PCE price index increased 2.8 percent. From the preceding month, the PCE price index, excluding food and energy, increased 0.2 percent.</p>`,
	}
	result, err := service.SyncOfficialBEA(context.Background())
	if err != nil {
		t.Fatalf("SyncOfficialBEA: %v", err)
	}
	if result.ReleasesSaved != 1 || result.Observations != 3 || result.Published != 1 {
		t.Fatalf("sync result = %+v", result)
	}
	second, err := service.SyncOfficialBEA(context.Background())
	if err != nil {
		t.Fatalf("second SyncOfficialBEA: %v", err)
	}
	if second.Observations != 0 || second.Published != 0 {
		t.Fatalf("published release should be skipped on refresh: %+v", second)
	}
	page, err := service.List(context.Background(), MacroReleaseFilter{Page: 1, PageSize: 10})
	if err != nil || page.Total != 1 || len(page.Items) != 1 || len(page.Items[0].Observations) != 3 {
		t.Fatalf("List page=%+v err=%v", page, err)
	}
}

func TestMacroCalendarListFiltersFrequencyAndTimeOrder(t *testing.T) {
	db := testDB(t)
	first := time.Date(2026, time.June, 25, 12, 30, 0, 0, time.UTC)
	second := first.AddDate(0, 1, 0)
	third := second.AddDate(0, 1, 0)
	if err := db.Create(&[]model.MacroRelease{
		{Provider: MacroProviderBEA, Category: "personal_income_outlays", Title: "May PIO", Status: MacroReleasePublished, ScheduledAt: &first, SourceURL: "https://bea.test/pio-may", FetchedAt: first},
		{Provider: MacroProviderBEA, Category: "gdp", Title: "GDP Q2", Status: MacroReleasePublished, ScheduledAt: &second, SourceURL: "https://bea.test/gdp-q2", FetchedAt: second},
		{Provider: MacroProviderBEA, Category: "personal_income_outlays", Title: "June PIO", Status: MacroReleaseScheduled, ScheduledAt: &third, SourceURL: "https://bea.test/pio-june", FetchedAt: third},
	}).Error; err != nil {
		t.Fatalf("seed macro releases: %v", err)
	}
	service := NewMacroCalendarService(db)
	page, err := service.List(context.Background(), MacroReleaseFilter{Frequency: "monthly", SortOrder: "desc", Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("List monthly: %v", err)
	}
	if page.Total != 2 || len(page.Items) != 2 || page.Items[0].Title != "June PIO" || page.Items[1].Title != "May PIO" {
		t.Fatalf("monthly descending page = %+v", page)
	}
	page, err = service.List(context.Background(), MacroReleaseFilter{Category: "gdp", SortOrder: "asc", Page: 1, PageSize: 10})
	if err != nil || page.Total != 1 || len(page.Items) != 1 || page.Items[0].Title != "GDP Q2" {
		t.Fatalf("GDP page=%+v err=%v", page, err)
	}
}

func TestMacroCalendarListAssociatesOfficialAndLongbridgeEvent(t *testing.T) {
	db := testDB(t)
	at := time.Date(2026, time.August, 12, 12, 30, 0, 0, time.UTC)
	if err := db.Create(&[]model.MacroRelease{
		{Provider: MacroProviderBLS, Category: "cpi", Title: "Consumer Price Index for July 2026", Status: MacroReleaseScheduled, ScheduledAt: &at, SourceURL: "https://bls.test/cpi", FetchedAt: at},
		{Provider: macroProviderLongbridge, Category: "market_calendar", Title: "United States CPI", Status: MacroReleaseScheduled, ScheduledAt: &at, SourceURL: "https://longbridge.test/cpi", FetchedAt: at},
	}).Error; err != nil {
		t.Fatalf("seed macro releases: %v", err)
	}
	page, err := NewMacroCalendarService(db).List(context.Background(), MacroReleaseFilter{Page: 1, PageSize: 10})
	if err != nil || len(page.Items) != 2 {
		t.Fatalf("List page=%+v err=%v", page, err)
	}
	for _, item := range page.Items {
		if item.CanonicalEventKey != "cpi:2026-08-12" || len(item.RelatedSources) != 2 {
			t.Fatalf("association item=%+v", item)
		}
		if !item.RelatedSources[0].Official || item.RelatedSources[1].Official {
			t.Fatalf("sources should be official-first: %+v", item.RelatedSources)
		}
	}
}

func TestCanonicalMacroEventKeyKeepsGDPSeparateFromPersonalIncome(t *testing.T) {
	at := time.Date(2026, time.April, 9, 12, 30, 0, 0, time.UTC)
	key := canonicalMacroEventKey("gdp", "GDP (Third Estimate), Industries, Corporate Profits, State GDP, and State Personal Income", &at)
	if key != "gdp:2026-04-09" {
		t.Fatalf("GDP canonical key=%q", key)
	}
}

func TestParseBLSScheduleAndReleaseObservations(t *testing.T) {
	schedule := `BEGIN:VCALENDAR
BEGIN:VEVENT
DTSTART;TZID=America/New_York:20260710T083000
SUMMARY:Employment Situation for June 2026
END:VEVENT
BEGIN:VEVENT
DTSTART:20260715T123000Z
SUMMARY:Consumer Price Index for June 2026
END:VEVENT
BEGIN:VEVENT
DTSTART;TZID=America/New_York:20260716T083000
SUMMARY:Producer Price Index for June 2026
END:VEVENT
END:VCALENDAR`
	events, err := parseBLSSchedule(schedule, "https://bls.test/calendar.ics")
	if err != nil || len(events) != 3 {
		t.Fatalf("parseBLSSchedule events=%+v err=%v", events, err)
	}
	if events[0].Provider != MacroProviderBLS || events[0].Category != "employment" || events[0].ScheduledAt.Format(time.RFC3339) != "2026-07-10T12:30:00Z" {
		t.Fatalf("employment event=%+v", events[0])
	}
	employment := parseBLSReleaseObservations("employment", `<p>Total nonfarm payroll employment increased by 147,000 in June, and the unemployment rate changed little at 4.1 percent. Average hourly earnings for all employees rose 0.3 percent in June.</p>`)
	assertMacroValues(t, employment, map[string]float64{"nonfarm_payrolls_change_k": 147, "unemployment_rate": 4.1, "average_hourly_earnings_mom": 0.3})
	cpi := parseBLSReleaseObservations("cpi", `<p>The all items index rose 0.3 percent in June. The index for all items less food and energy increased 0.2 percent in June.</p>`)
	assertMacroValues(t, cpi, map[string]float64{"cpi_mom": 0.3, "core_cpi_mom": 0.2})
}

func TestParseFOMCScheduleStoresFutureStatementDate(t *testing.T) {
	now := time.Date(2026, time.July, 31, 12, 0, 0, 0, time.UTC)
	events, err := parseFOMCSchedule(`<h2>2026 FOMC Meetings</h2><p>January 27-28 March 17-18 September 15-16 October 27-28</p><h2>2027 FOMC Meetings</h2><p>January 26-27</p>`, "https://fed.test/fomc", now)
	if err != nil || len(events) != 2 {
		t.Fatalf("parseFOMCSchedule events=%+v err=%v", events, err)
	}
	if events[0].Provider != MacroProviderFederalReserve || events[0].Category != "fomc" || events[0].ScheduledAt.Format(time.RFC3339) != "2026-09-16T18:00:00Z" {
		t.Fatalf("first FOMC event=%+v", events[0])
	}
}

func TestMacroCalendarSyncStoresBLSRelease(t *testing.T) {
	db := testDB(t)
	now := time.Date(2026, time.July, 31, 12, 0, 0, 0, time.UTC)
	service := NewMacroCalendarService(db)
	service.now = func() time.Time { return now }
	service.blsScheduleURL = "https://bls.test/calendar.ics"
	service.client = macroRoundTripper{
		service.blsScheduleURL: `BEGIN:VCALENDAR
BEGIN:VEVENT
DTSTART;TZID=America/New_York:20260710T083000
SUMMARY:Employment Situation for June 2026
END:VEVENT
END:VCALENDAR`,
		defaultBLSEmploymentURL: `<p>Total nonfarm payroll employment increased by 147,000 in June, and the unemployment rate was 4.1 percent. Average hourly earnings rose 0.3 percent.</p>`,
	}
	result := MacroCalendarSyncResult{Warnings: []string{}}
	if err := service.syncOfficialBLS(context.Background(), &result); err != nil {
		t.Fatalf("syncOfficialBLS: %v", err)
	}
	if result.ReleasesSaved != 1 || result.Published != 1 || result.Observations != 3 {
		t.Fatalf("BLS sync result=%+v", result)
	}
	page, err := service.List(context.Background(), MacroReleaseFilter{Category: "employment", Page: 1, PageSize: 10})
	if err != nil || page.Total != 1 || page.Items[0].Provider != MacroProviderBLS {
		t.Fatalf("BLS page=%+v err=%v", page, err)
	}
}

func TestSyncFREDCPIBackfillsAndSkipsExistingReferencePeriods(t *testing.T) {
	db := testDB(t)
	now := time.Date(2026, time.July, 31, 12, 0, 0, 0, time.UTC)
	const sourceURL = "https://fred.test/cpi.csv?id=CPIAUCSL,CPILFESL"
	body := `observation_date,CPIAUCSL,CPILFESL
2025-05-01,100.0,100.0
2025-06-01,101.0,100.5
2025-07-01,102.0,101.0
2025-08-01,103.0,101.5
2025-09-01,104.0,102.0
2025-10-01,105.0,102.5
2025-11-01,106.0,103.0
2025-12-01,107.0,103.5
2026-01-01,108.0,104.0
2026-02-01,109.0,104.5
2026-03-01,110.0,105.0
2026-04-01,111.0,105.5
2026-05-01,112.0,106.0
2026-06-01,113.0,106.5
`
	service := NewMacroCalendarService(db)
	service.now = func() time.Time { return now }
	service.fredCPIURL = sourceURL
	service.client = macroRoundTripper{sourceURL: body}
	result := MacroCalendarSyncResult{Warnings: []string{}}
	if err := service.syncFREDCPI(context.Background(), &result); err != nil {
		t.Fatalf("syncFREDCPI: %v", err)
	}
	if result.Published != 13 || result.ReleasesSaved != 13 {
		t.Fatalf("FRED CPI result=%+v, want 13 published releases", result)
	}
	page, err := service.List(context.Background(), MacroReleaseFilter{Category: "cpi", SortOrder: "desc", Page: 1, PageSize: 20})
	if err != nil || page.Total != 13 || page.Items[0].Provider != MacroProviderFRED || page.Items[0].ReferencePeriod != "June 2026" {
		t.Fatalf("FRED CPI page=%+v err=%v", page, err)
	}
	assertMacroValues(t, page.Items[0].Observations, map[string]float64{"cpi_mom": 0.9, "core_cpi_mom": 0.5, "cpi_yoy": 11.9, "core_cpi_yoy": 6.0})
	second := MacroCalendarSyncResult{Warnings: []string{}}
	if err := service.syncFREDCPI(context.Background(), &second); err != nil {
		t.Fatalf("second syncFREDCPI: %v", err)
	}
	if second.Published != 0 || second.ReleasesSaved != 0 {
		t.Fatalf("existing FRED CPI releases should be skipped: %+v", second)
	}
}

func TestParseCensusRetailScheduleAndHeadline(t *testing.T) {
	schedule := `<table><tr><td>June 2026</td><td>July 16, 2026</td><td>Advance Monthly Retail Trade Report</td></tr><tr><td>July 2026</td><td>August 14, 2026</td><td>Advance Monthly Retail Trade Report</td></tr></table>`
	events, err := parseCensusRetailSchedule(schedule, "https://census.test/retail")
	if err != nil || len(events) != 2 {
		t.Fatalf("parseCensusRetailSchedule events=%+v err=%v", events, err)
	}
	if events[0].Provider != MacroProviderCensus || events[0].ReferencePeriod != "June 2026" || events[0].ScheduledAt.Format(time.RFC3339) != "2026-07-16T12:30:00Z" {
		t.Fatalf("Census event=%+v", events[0])
	}
	observations := parseCensusRetailObservations(`<section>Advance Retail and Food Services Sales Current May 2026 $763.7B Difference April 2026 +0.9%</section>`)
	assertMacroValues(t, observations, map[string]float64{"retail_sales_mom": 0.9})
}

func TestParseJOLTSAndCensusEconomicSchedule(t *testing.T) {
	jolts := parseBLSReleaseObservations("jolts", `<p>The number of job openings was little changed at 7.4 million. Hires were little changed at 5.5 million. Total separations were little changed at 5.1 million.</p>`)
	assertMacroValues(t, jolts, map[string]float64{"job_openings_m": 7.4, "hires_m": 5.5, "separations_m": 5.1})
	calendar := `<table>
<tr><td>Advance Report on Durable Goods--Manufacturers' Shipments, Inventories, and Orders</td><td>August 25, 2026</td><td>8:30 AM</td><td>July 2026</td></tr>
<tr><td>New Residential Construction (Building Permits, Housing Starts, and Housing Completions)</td><td>August 18, 2026</td><td>8:30 AM</td><td>July 2026</td></tr>
<tr><td>U.S. International Trade in Goods and Services</td><td>September 4, 2026</td><td>8:30 AM</td><td>July 2026</td></tr>
</table>`
	events, err := parseCensusEconomicSchedule(calendar, "https://census.test/calendar")
	if err != nil || len(events) != 3 {
		t.Fatalf("parseCensusEconomicSchedule events=%+v err=%v", events, err)
	}
	if events[0].Category != "housing_starts" || events[0].ScheduledAt.Format(time.RFC3339) != "2026-08-18T12:30:00Z" {
		t.Fatalf("housing event=%+v", events[0])
	}
}

func TestParseDOLClaimsRelease(t *testing.T) {
	event, observations, ok := parseDOLClaimsRelease(`<article><h2>Unemployment Insurance Weekly Claims Report</h2><p>July 30, 2026</p><p>In the week ending July 25, the advance figure for seasonally adjusted initial claims was 218,000. The 4-week moving average was 221,500.</p></article>`, "https://dol.test/releases")
	if !ok || event.Provider != MacroProviderDOL || event.Category != "initial_claims" || event.ScheduledAt.Format(time.RFC3339) != "2026-07-30T12:30:00Z" {
		t.Fatalf("DOL event=%+v ok=%t", event, ok)
	}
	assertMacroValues(t, observations, map[string]float64{"initial_claims_k": 218, "initial_claims_4w_avg_k": 221.5})
}

func TestParseCensusEconomicObservations(t *testing.T) {
	durable := parseCensusEconomicObservations("durable_goods", `<section>Advance New Orders for Manufactured Durable Goods Latest June 25th, 2026 Current May 2026 $332.1 B Difference April 2026 − 4.5 %</section>`)
	assertMacroValues(t, durable, map[string]float64{"durable_goods_mom": -4.5})
	housing := parseCensusEconomicObservations("housing_starts", `<section>Housing Starts Latest June 16th, 2026 Current May 2026 1,177,000 units Difference April 2026 -15.4%</section>`)
	assertMacroValues(t, housing, map[string]float64{"housing_starts_mom": -15.4})
	trade := parseCensusEconomicObservations("international_trade", `<section>International Trade Deficit in Goods & Services Latest July 7th, 2026 Current May 2026 $77.6 B Difference April 2026 +42.2%</section>`)
	assertMacroValues(t, trade, map[string]float64{"trade_deficit_b": 77.6, "trade_deficit_b_mom": 42.2})
}

func TestParseTreasuryYieldCurveAndStoreLatestBusinessDay(t *testing.T) {
	const sourceURL = "https://treasury.test/yields"
	body := `<table><tr><th>Date</th><th>1 Yr</th><th>2 Yr</th><th>5 Yr</th><th>10 Yr</th></tr>
<tr><td>07/28/2026</td><td>3.80</td><td>3.70</td><td>3.88</td><td>4.18</td></tr>
<tr><td>07/29/2026</td><td>3.79</td><td>3.69</td><td>3.86</td><td>4.16</td></tr></table>`
	event, observations, ok := parseTreasuryYieldCurve(body, sourceURL)
	if !ok || event.Provider != MacroProviderTreasury || event.Category != "treasury_yields" || event.ReferencePeriod != "2026-07-29" {
		t.Fatalf("Treasury event=%+v ok=%t", event, ok)
	}
	assertMacroValues(t, observations, map[string]float64{"treasury_2y_yield": 3.69, "treasury_5y_yield": 3.86, "treasury_10y_yield": 4.16, "treasury_10y_2y_spread_bp": 47})

	db := testDB(t)
	service := NewMacroCalendarService(db)
	service.now = func() time.Time { return time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC) }
	service.treasuryYieldURL = sourceURL
	service.client = macroRoundTripper{sourceURL: body}
	result := MacroCalendarSyncResult{Warnings: []string{}}
	if err := service.syncOfficialTreasuryYields(context.Background(), &result); err != nil {
		t.Fatalf("syncOfficialTreasuryYields: %v", err)
	}
	if result.Published != 2 || result.Observations != 8 {
		t.Fatalf("Treasury result=%+v", result)
	}
	page, err := service.List(context.Background(), MacroReleaseFilter{Frequency: "daily", Page: 1, PageSize: 10})
	if err != nil || page.Total != 2 || len(page.Items) != 2 || page.Items[0].Provider != MacroProviderTreasury || page.Items[0].ReferencePeriod != "2026-07-29" || page.Items[1].ReferencePeriod != "2026-07-28" {
		t.Fatalf("Treasury page=%+v err=%v", page, err)
	}
}

func TestParseTreasuryRealYieldCurve(t *testing.T) {
	const sourceURL = "https://treasury.test/real-yields"
	body := `<table><tr><th>Date</th><th>5 Yr</th><th>10 Yr</th><th>30 Yr</th></tr>
<tr><td>07/28/2026</td><td>1.42</td><td>1.88</td><td>2.21</td></tr>
<tr><td>07/29/2026</td><td>1.40</td><td>1.86</td><td>2.19</td></tr></table>`
	event, observations, ok := parseTreasuryRealYieldCurve(body, sourceURL)
	if !ok || event.Provider != MacroProviderTreasury || event.Category != "treasury_real_yields" || event.ReferencePeriod != "2026-07-29" {
		t.Fatalf("Treasury real event=%+v ok=%t", event, ok)
	}
	assertMacroValues(t, observations, map[string]float64{"treasury_5y_real_yield": 1.40, "treasury_10y_real_yield": 1.86, "treasury_30y_real_yield": 2.19})
}

func TestTreasuryCurveRequestURLUsesNewYorkMonth(t *testing.T) {
	publicURL := treasuryCurveRequestURL(defaultTreasuryYieldURL, time.Date(2026, time.August, 1, 2, 0, 0, 0, time.UTC))
	if !strings.Contains(publicURL, "field_tdr_date_value=202607") {
		t.Fatalf("Treasury URL=%q, want New York July parameter", publicURL)
	}
	customURL := "https://treasury.test/yields"
	if got := treasuryCurveRequestURL(customURL, time.Now()); got != customURL {
		t.Fatalf("custom URL=%q, want unchanged %q", got, customURL)
	}
}

func TestParseEIAWeeklyPetroleumAndStoreOfficialCSV(t *testing.T) {
	const pageURL = "https://eia.test/wpsr"
	const tableURL = "https://eia.test/table4.csv"
	page := `<h1>Weekly Petroleum Status Report</h1><p>Data for week ending July 24, 2026 Release Date: July 29, 2026 Next Release Date: August 5, 2026</p>`
	table := `"STUB_1","7/24/26","7/17/26","Difference"
"Crude Oil","712.158","723.122","-10.964"
"Commercial (Excluding SPR)","404.508","411.675","-7.167"
"Total Motor Gasoline","211.301","211.294","0.007"
"Distillate Fuel Oil","110.632","109.570","1.061"`
	event, observations, ok := parseEIAWeeklyPetroleum(page, table, pageURL)
	if !ok || event.Provider != MacroProviderEIA || event.Category != "petroleum_inventories" || event.ReferencePeriod != "week ending 2026-07-24" {
		t.Fatalf("EIA event=%+v ok=%t", event, ok)
	}
	assertMacroValues(t, observations, map[string]float64{
		"commercial_crude_oil_inventory_mmbbl": 404.508, "commercial_crude_oil_inventory_mmbbl_wow": -7.167,
		"motor_gasoline_inventory_mmbbl": 211.301, "motor_gasoline_inventory_mmbbl_wow": 0.007,
		"distillate_inventory_mmbbl": 110.632, "distillate_inventory_mmbbl_wow": 1.062,
	})
	db := testDB(t)
	service := NewMacroCalendarService(db)
	service.now = func() time.Time { return time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC) }
	service.eiaWeeklyPetroleumURL, service.eiaWeeklyPetroleumTable4 = pageURL, tableURL
	service.client = macroRoundTripper{pageURL: page, tableURL: table}
	result := MacroCalendarSyncResult{Warnings: []string{}}
	if err := service.syncOfficialEIAWeeklyPetroleum(context.Background(), &result); err != nil {
		t.Fatalf("syncOfficialEIAWeeklyPetroleum: %v", err)
	}
	if result.Published != 1 || result.Observations != 6 {
		t.Fatalf("EIA result=%+v", result)
	}
}

func assertMacroValues(t *testing.T, observations []model.MacroObservation, want map[string]float64) {
	t.Helper()
	got := map[string]float64{}
	for _, observation := range observations {
		if observation.ActualValue != nil {
			got[observation.IndicatorCode] = *observation.ActualValue
		}
	}
	if len(got) != len(want) {
		t.Fatalf("values = %#v, want %#v", got, want)
	}
	for code, expected := range want {
		if got[code] != expected {
			t.Fatalf("%s = %v, want %v (all=%#v)", code, got[code], expected, got)
		}
	}
}
