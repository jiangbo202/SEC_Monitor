package service

import (
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/html"
	"sec_monitor/internal/model"
)

var blsHTMLSchedules = []struct{ category, title, url string }{
	{"employment", "Employment Situation", "https://www.bls.gov/schedule/news_release/empsit.htm"},
	{"cpi", "Consumer Price Index", "https://www.bls.gov/schedule/news_release/cpi.htm"},
	{"ppi", "Producer Price Index", "https://www.bls.gov/schedule/news_release/ppi.htm"},
	{"jolts", "Job Openings and Labor Turnover Survey", "https://www.bls.gov/schedule/news_release/jolts.htm"},
}

func parseBLSHTMLSchedule(raw, sourceURL, category, title string) ([]beaScheduleEvent, error) {
	doc, err := html.Parse(strings.NewReader(raw))
	if err != nil {
		return nil, err
	}
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		return nil, err
	}
	var events []beaScheduleEvent
	for _, row := range findHTMLNodes(doc, "tr") {
		cells := findHTMLNodes(row, "td")
		if len(cells) < 3 {
			continue
		}
		period := normalizeMacroText(htmlNodeText(cells[0]))
		if _, err := time.Parse("January 2006", period); err != nil {
			continue
		}
		date := strings.ReplaceAll(normalizeMacroText(htmlNodeText(cells[1])), ".", "")
		clock := normalizeMacroText(htmlNodeText(cells[2]))
		var scheduled time.Time
		for _, layout := range []string{"Jan 02, 2006 03:04 PM", "Jan 2, 2006 3:04 PM", "January 2, 2006 3:04 PM"} {
			scheduled, err = time.ParseInLocation(layout, date+" "+clock, loc)
			if err == nil {
				break
			}
		}
		if err != nil {
			continue
		}
		events = append(events, beaScheduleEvent{Provider: MacroProviderBLS, Category: category, Title: title, ReferencePeriod: period, ReleaseStage: "scheduled", ScheduledAt: scheduled.UTC(), SourceURL: sourceURL + "#" + url.QueryEscape(category+"-"+scheduled.Format("200601021504"))})
	}
	if len(events) == 0 {
		return nil, errors.New("official HTML calendar contained no supported dated rows")
	}
	return events, nil
}

const defaultEIAWeeklyCalendarURL = "https://www.eia.gov/petroleum/supply/weekly/includes/wpsr-calendar.json"

func parseEIAReleaseCalendar(raw string) ([]beaScheduleEvent, error) {
	var rows []struct {
		Week  string `json:"data_for_date"`
		Date  string `json:"release_date"`
		Clock string `json:"release_time"`
	}
	if err := json.Unmarshal([]byte(raw), &rows); err != nil {
		return nil, err
	}
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		return nil, err
	}
	var events []beaScheduleEvent
	for _, row := range rows {
		week, weekErr := time.Parse("2006-01-02", row.Week)
		at, dateErr := time.ParseInLocation("2006-01-02 15:04", row.Date+" "+row.Clock, loc)
		if weekErr != nil || dateErr != nil || !at.After(week) {
			continue
		}
		events = append(events, beaScheduleEvent{Provider: MacroProviderEIA, Category: "petroleum_inventories", Title: "Weekly Petroleum Status Report", ReferencePeriod: "week ending " + row.Week, ReleaseStage: "weekly", ScheduledAt: at.UTC(), SourceURL: defaultEIAWeeklyCalendarURL + "#" + row.Date})
	}
	if len(events) == 0 {
		return nil, errors.New("official EIA calendar contained no verified dates")
	}
	return events, nil
}

func parseEIAWeeklyWithCalendar(events []beaScheduleEvent, raw string) (beaScheduleEvent, []model.MacroObservation, bool) {
	records, err := csv.NewReader(strings.NewReader(raw)).ReadAll()
	if err != nil || len(records) < 2 || len(records[0]) < 3 {
		return beaScheduleEvent{}, nil, false
	}
	week, err := time.Parse("1/2/06", records[0][1])
	if err != nil {
		return beaScheduleEvent{}, nil, false
	}
	for _, event := range events {
		if event.ReferencePeriod != "week ending "+week.Format("2006-01-02") {
			continue
		}
		// Reuse inventory-value parsing after pairing the CSV with an explicit
		// official week/date. Never infer a release date from a weekday rule.
		loc, _ := time.LoadLocation("America/New_York")
		page := fmt.Sprintf("Data for week ending %s Release Date: %s", week.Format("January 2, 2006"), event.ScheduledAt.In(loc).Format("January 2, 2006"))
		_, observations, ok := parseEIAWeeklyPetroleum(page, raw, defaultEIAWeeklyPetroleumURL)
		return event, observations, ok
	}
	return beaScheduleEvent{}, nil, false
}
