package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestOfficialJSONCalendarContentNegotiation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.Header.Get("Accept"), "application/json") {
			w.WriteHeader(http.StatusNotAcceptable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"data_for_date":"2026-09-04","release_date":"2026-09-10","release_time":"12:00"}]`))
	}))
	defer server.Close()
	s := NewMacroCalendarService(testDB(t))
	body, err := s.fetch(context.Background(), server.URL+"/wpsr-calendar.json")
	if err != nil {
		t.Fatal(err)
	}
	if rows, parseErr := parseEIAReleaseCalendar(body); parseErr != nil || len(rows) != 1 {
		t.Fatalf("rows=%d err=%v", len(rows), parseErr)
	}
}

func TestOfficialScheduleFallbacksAndDST(t *testing.T) {
	events, err := parseBLSHTMLSchedule(`<table><tr><th>Reference Month</th><th>Release Date</th><th>Time</th></tr><tr><td>October 2026</td><td>Nov. 06, 2026</td><td>08:30 AM</td></tr><tr><td>January 2026</td><td>Feb. 06, 2026</td><td>08:30 AM</td></tr><tr><td>December 2026</td><td>To be announced</td><td>08:30 AM</td></tr></table>`, blsHTMLSchedules[0].url, "employment", "Employment Situation")
	if err != nil || len(events) != 2 || events[0].ScheduledAt.Format(time.RFC3339) != "2026-11-06T13:30:00Z" {
		t.Fatalf("events=%+v err=%v", events, err)
	}
	db := testDB(t)
	s := NewMacroCalendarService(db)
	s.client = macroRoundTripper{blsHTMLSchedules[0].url: `<table><tr><td>October 2026</td><td>Nov. 06, 2026</td><td>08:30 AM</td></tr></table>`, s.censusEconomicScheduleURL: `<table><tr><td>Advance Monthly Sales for Retail and Food Services</td><td>November 17, 2026 8:30 AM</td><td>October 2026</td></tr></table>`}
	s.now = func() time.Time { return time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC) }
	result := MacroCalendarSyncResult{}
	if err := s.syncOfficialBLS(context.Background(), &result); err != nil {
		t.Fatal(err)
	}
	if err := s.syncOfficialCensusRetail(context.Background(), &result); err != nil {
		t.Fatal(err)
	}
	page, err := s.List(context.Background(), MacroReleaseFilter{Page: 1, PageSize: 10})
	if err != nil || page.Total != 2 || len(result.Warnings) == 0 {
		t.Fatalf("page=%+v result=%+v err=%v", page, result, err)
	}
}

func TestEIAOfficialCalendarPairsCSVAndHolidayTime(t *testing.T) {
	events, err := parseEIAReleaseCalendar(`[{"data_for_date":"2026-09-04","release_date":"2026-09-10","release_time":"12:00"}]`)
	if err != nil || len(events) != 1 || events[0].ScheduledAt.Format(time.RFC3339) != "2026-09-10T16:00:00Z" {
		t.Fatalf("events=%+v err=%v", events, err)
	}
	_, observations, ok := parseEIAWeeklyWithCalendar(events, "STUB_1,9/4/26,8/28/26\nCommercial (Excluding SPR),400,399")
	if !ok || len(observations) != 2 {
		t.Fatalf("observations=%+v ok=%v", observations, ok)
	}
	if observations[0].PreviousValue == nil || *observations[0].PreviousValue != 399 || observations[1].ActualValue == nil || *observations[1].ActualValue != 1 {
		t.Fatal("inventory level and change must use the same official prior-week column")
	}
	if _, _, ok := parseEIAWeeklyWithCalendar(events, "STUB_1,9/11/26,9/4/26\nCommercial (Excluding SPR),400,399"); ok {
		t.Fatal("must reject an unverified report week")
	}
}
