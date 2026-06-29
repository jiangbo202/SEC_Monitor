package discovery

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestParseNasdaqHeaderAndDuplicateContracts(t *testing.T) {
	header := "Symbol|Security Name|Market Category|Test Issue|Financial Status|Round Lot Size|ETF|NextShares\n"
	for _, tc := range []struct{ name, body, want string }{
		{"header whitespace", "\n Symbol|Security Name|Market Category|Test Issue|Financial Status|Round Lot Size|ETF|NextShares\n", "line 2"},
		{"leading blank row error", "\n\n" + header + "A|bad\nFile Creation Time: x\n", "line 4"},
		{"footer fields", header + "A|Alpha|Q|N|N|100|N|N\nFile Creation Time: x|||||||extra\n", "line 3"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := ParseNasdaqListed(strings.NewReader(tc.body))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v", err)
			}
		})
	}
	body := header + "A|Alpha|Q|N|N|100|N|N\nA|Alpha|Q|N|N|100|N|N\nFile Creation Time: x|||||||\n"
	if records, _, err := ParseNasdaqListed(strings.NewReader(body)); err != nil || len(records) != 1 {
		t.Fatalf("records/error = %#v %v", records, err)
	}
}

func TestParseNasdaqAcceptsOnlyEmptyPaddedFooterFields(t *testing.T) {
	listed := "Symbol|Security Name|Market Category|Test Issue|Financial Status|Round Lot Size|ETF|NextShares\n" +
		"A|Alpha|Q|N|N|100|N|N\nFile Creation Time: 0621202618:00|||||||\n"
	if records, stamp, err := ParseNasdaqListed(strings.NewReader(listed)); err != nil || len(records) != 1 || stamp != "0621202618:00" {
		t.Fatalf("listed records/stamp/error = %#v %q %v", records, stamp, err)
	}
	other := "ACT Symbol|Security Name|Exchange|CQS Symbol|ETF|Round Lot Size|Test Issue|NASDAQ Symbol\n" +
		"B|Beta|N|B|N|100|N|B\nFile Creation Time: 0621202618:00|||||||\n"
	if records, stamp, err := ParseNasdaqOther(strings.NewReader(other)); err != nil || len(records) != 1 || stamp != "0621202618:00" {
		t.Fatalf("other records/stamp/error = %#v %q %v", records, stamp, err)
	}
}

func TestParseNasdaqOtherPreservesTickerPunctuation(t *testing.T) {
	body := "ACT Symbol|Security Name|Exchange|CQS Symbol|ETF|Round Lot Size|Test Issue|NASDAQ Symbol\n" +
		" brk.b | Berkshire Class B |N|BRK.B|N|100|N|BRK.B\nFile Creation Time: x\n"
	records, _, err := ParseNasdaqOther(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Ticker != "BRK.B" {
		t.Fatalf("records = %#v", records)
	}
}

func TestNasdaqDirectorySourceRejectsCrossFeedTickerConflict(t *testing.T) {
	listed := "Symbol|Security Name|Market Category|Test Issue|Financial Status|Round Lot Size|ETF|NextShares\nA|Alpha|Q|N|N|100|N|N\nFile Creation Time: 0621202618:00\n"
	other := "ACT Symbol|Security Name|Exchange|CQS Symbol|ETF|Round Lot Size|Test Issue|NASDAQ Symbol\nA|Alpha|N|A|N|100|N|A\nFile Creation Time: 0621202619:00\n"
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		body := listed
		if strings.Contains(r.URL.Path, "other") {
			body = other
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header), Request: r}, nil
	})}
	s := NasdaqDirectorySource{Downloader: &Downloader{Client: client, CacheDir: t.TempDir(), MaxBytes: 1 << 20}, ListedURL: "https://x.test/listed", OtherURL: "https://x.test/other"}
	if _, _, err := s.Load(context.Background()); err == nil || !strings.Contains(err.Error(), "duplicate ticker") {
		t.Fatalf("error = %v", err)
	}
}

func TestParseNasdaqMergeDedupesExactAndRejectsConflict(t *testing.T) {
	record := SecuritySourceRecord{Ticker: "A", CompanyName: "Alpha", Exchange: "Nasdaq"}
	merged, err := mergeNasdaqRecords([]SecuritySourceRecord{record}, []SecuritySourceRecord{record})
	if err != nil || len(merged) != 1 {
		t.Fatalf("exact duplicate = %#v %v", merged, err)
	}
	for _, field := range []string{"name", "exchange"} {
		changed := record
		if field == "name" {
			changed.CompanyName = "Other"
		} else {
			changed.Exchange = "NYSE"
		}
		for _, pair := range [][2]SecuritySourceRecord{{record, changed}, {changed, record}} {
			if _, err := mergeNasdaqRecords([]SecuritySourceRecord{pair[0]}, []SecuritySourceRecord{pair[1]}); err == nil {
				t.Fatalf("conflicting duplicate %s accepted in order %#v", field, pair)
			}
		}
	}
}

func TestParseSECRejectsZeroCIK(t *testing.T) {
	if _, err := ParseSECTickerExchange(strings.NewReader(`{"fields":["cik","name","ticker","exchange"],"data":[[0,"Zero","ZERO","Nasdaq"]]}`)); err == nil {
		t.Fatal("ticker zero CIK accepted")
	}
	for _, parseFacts := range []bool{false, true} {
		p := zipFile(t, map[string]string{"CIK0000000000.json": `{"cik":0,"facts":{}}`})
		z, err := zip.OpenReader(p)
		if err != nil {
			t.Fatal(err)
		}
		if parseFacts {
			_, err = ParseSECCompanyFactsZIP(&z.Reader, map[string]struct{}{"0000000000": {}}, ZIPParseLimits{MaxEntryBytes: 100, MaxTotalBytes: 100})
		} else {
			_, err = ParseSECSubmissionsZIP(&z.Reader, ZIPParseLimits{MaxEntryBytes: 100, MaxTotalBytes: 100})
		}
		z.Close()
		if err == nil {
			t.Fatalf("zero CIK accepted (facts=%v)", parseFacts)
		}
	}
}

func TestParseSECTickerRowContracts(t *testing.T) {
	for _, in := range []string{
		`{"fields":["cik","name","ticker","exchange"],"data":[[1,"A","A"]]}`,
		`{"fields":["cik","name","ticker","exchange"],"data":[[1,"A","A","Nasdaq","extra"]]}`,
		`{"fields":["cik","name","ticker","exchange"],"data":[[1,"A",7,"Nasdaq"]]}`,
	} {
		if _, err := ParseSECTickerExchange(strings.NewReader(in)); err == nil {
			t.Fatalf("accepted %s", in)
		}
	}
	in := `{"fields":["cik","name","ticker","exchange"],"data":[[1,"A","A","Nasdaq"],[1,"A","A.B","NYSE"]]}`
	if r, err := ParseSECTickerExchange(strings.NewReader(in)); err != nil || len(r) != 2 {
		t.Fatalf("same-CIK tickers = %#v, %v", r, err)
	}
	exact := `{"fields":["cik","name","ticker","exchange"],"data":[[1,"A","A","Nasdaq"],[1,"A","A","Nasdaq"]]}`
	if r, err := ParseSECTickerExchange(strings.NewReader(exact)); err != nil || len(r) != 1 {
		t.Fatalf("exact duplicate = %#v, %v", r, err)
	}
}

func TestParseSECTickerRejectsTrailingJSONEmptyFieldsAndConflictsInEitherOrder(t *testing.T) {
	for _, in := range []string{
		`{"fields":["cik","name","ticker","exchange"],"data":[[1,"A","A","Nasdaq"]]} garbage`,
		`{"fields":["cik","name","ticker","exchange"],"data":[[1," ","A","Nasdaq"]]}`,
		`{"fields":["cik","name","ticker","exchange"],"data":[[1,"A","A"," "]]}`,
	} {
		if _, err := ParseSECTickerExchange(strings.NewReader(in)); err == nil {
			t.Fatalf("accepted invalid ticker payload %q", in)
		}
	}
	for _, changed := range []string{`[1,"Other","A","Nasdaq"]`, `[1,"Alpha","A","NYSE"]`} {
		base := `[1,"Alpha","A","Nasdaq"]`
		for _, pair := range [][2]string{{base, changed}, {changed, base}} {
			in := `{"fields":["cik","name","ticker","exchange"],"data":[` + pair[0] + `,` + pair[1] + `]}`
			if _, err := ParseSECTickerExchange(strings.NewReader(in)); err == nil || !strings.Contains(err.Error(), "duplicate ticker") {
				t.Fatalf("conflict order %#v error = %v", pair, err)
			}
		}
	}
}

func TestParseNasdaqRejectsEmptyTickerAndSecurityName(t *testing.T) {
	header := "Symbol|Security Name|Market Category|Test Issue|Financial Status|Round Lot Size|ETF|NextShares\n"
	for _, row := range []string{"|Alpha|Q|N|N|100|N|N", "A| |Q|N|N|100|N|N"} {
		if _, _, err := ParseNasdaqListed(strings.NewReader(header + row + "\nFile Creation Time: x|||||||\n")); err == nil || !strings.Contains(err.Error(), "line 2") {
			t.Fatalf("accepted row %q: %v", row, err)
		}
	}
}

func TestParseSECTickerRequiredFieldsAndCIKTypes(t *testing.T) {
	for _, missing := range []string{"cik", "name", "ticker", "exchange"} {
		t.Run("missing-"+missing, func(t *testing.T) {
			all := []string{"cik", "name", "ticker", "exchange"}
			fields := make([]string, 0, 3)
			values := make([]string, 0, 3)
			for i, field := range all {
				if field != missing {
					fields = append(fields, `"`+field+`"`)
					values = append(values, []string{"1", `"A"`, `"A"`, `"Nasdaq"`}[i])
				}
			}
			input := `{"fields":[` + strings.Join(fields, ",") + `],"data":[[` + strings.Join(values, ",") + `]]}`
			if _, err := ParseSECTickerExchange(strings.NewReader(input)); err == nil || !strings.Contains(err.Error(), "missing required field") {
				t.Fatalf("error = %v", err)
			}
		})
	}
	for name, cik := range map[string]string{"string": `"1"`, "null": "null", "fractional": "1.5"} {
		t.Run("cik-"+name, func(t *testing.T) {
			input := `{"fields":["cik","name","ticker","exchange"],"data":[[` + cik + `,"A","A","Nasdaq"]]}`
			if _, err := ParseSECTickerExchange(strings.NewReader(input)); err == nil {
				t.Fatal("invalid CIK accepted")
			}
		})
	}
}

func TestParseSECSubmissionsZIPDirectoryAndItemForms(t *testing.T) {
	p := zipFileWithDirectory(t, "safe/", map[string]string{
		"CIK0000001234.json": `{"name":"Acme","cik":1234,"filings":{"recent":{"form":["8-K/A","8-K-A"],"filingDate":["2026-06-01","2026-06-02"],"items":["2.01","2.01"]}}}`,
	})
	z, err := zip.OpenReader(p)
	if err != nil {
		t.Fatal(err)
	}
	m, err := ParseSECSubmissionsZIP(&z.Reader, ZIPParseLimits{MaxEntries: 3, MaxEntryBytes: 1000, MaxTotalBytes: 1000})
	z.Close()
	if err != nil {
		t.Fatal(err)
	}
	if !m["0000001234"].HasBusinessCombinationItem201 {
		t.Fatal("8-K/A item 2.01 not detected")
	}
	wantCompleted := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	if got := m["0000001234"].BusinessCombinationCompletedAt; got == nil || !got.Equal(wantCompleted) {
		t.Fatalf("completion date = %v, want %v", got, wantCompleted)
	}
	p = zipFile(t, map[string]string{"CIK0000001234.json": `{"name":"Acme","cik":1234,"filings":{"recent":{"form":["8-K-A"],"items":["2.01"]}}}`})
	z, err = zip.OpenReader(p)
	if err != nil {
		t.Fatal(err)
	}
	m, err = ParseSECSubmissionsZIP(&z.Reader, ZIPParseLimits{MaxEntryBytes: 1000, MaxTotalBytes: 1000})
	z.Close()
	if err != nil {
		t.Fatal(err)
	}
	if m["0000001234"].HasBusinessCombinationItem201 {
		t.Fatal("malformed 8-K-A detected")
	}
}

func TestParseSECSubmissionsRejectsTrailingJSONNameSICAndDuplicateCIKConflicts(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"trailing", `{"name":"Acme","cik":1234} garbage`},
		{"missing-name", `{"cik":1234}`},
		{"empty-name", `{"name":" ","cik":1234}`},
		{"negative-sic", `{"name":"Acme","cik":1234,"sic":"-1"}`},
		{"large-sic", `{"name":"Acme","cik":1234,"sic":"10000"}`},
		{"nondigit-sic", `{"name":"Acme","cik":1234,"sic":"1x"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := zipFile(t, map[string]string{"CIK0000001234.json": tc.body})
			z, err := zip.OpenReader(p)
			if err != nil {
				t.Fatal(err)
			}
			_, err = ParseSECSubmissionsZIP(&z.Reader, ZIPParseLimits{MaxEntryBytes: 2000, MaxTotalBytes: 2000})
			z.Close()
			if err == nil || !strings.Contains(err.Error(), "1234") {
				t.Fatalf("error = %v", err)
			}
		})
	}

	base := `{"name":"Acme","cik":1234,"sic":"","stateOfIncorporation":"DE"}`
	if records, err := parseSubmissionEntries(t, []task4ZIPEntry{{"CIK0000001234.json", base}, {"CIK0000001234.json", base}}); err != nil || len(records) != 1 {
		t.Fatalf("exact duplicate = %#v %v", records, err)
	}
	changed := `{"name":"Other","cik":1234,"sic":"","stateOfIncorporation":"DE"}`
	for _, entries := range [][]task4ZIPEntry{
		{{"CIK0000001234.json", base}, {"CIK0000001234.json", changed}},
		{{"CIK0000001234.json", changed}, {"CIK0000001234.json", base}},
	} {
		if _, err := parseSubmissionEntries(t, entries); err == nil || !strings.Contains(err.Error(), "conflicting duplicate") {
			t.Fatalf("conflict entries %#v error = %v", entries, err)
		}
	}
	for _, sic := range []string{"", "0", "9999"} {
		body := `{"name":"Acme","cik":1234,"sic":"` + sic + `"}`
		records, err := parseSubmissionEntries(t, []task4ZIPEntry{{"CIK0000001234.json", body}})
		if err != nil {
			t.Fatalf("valid SIC %q: %v", sic, err)
		}
		want := 0
		if sic == "9999" {
			want = 9999
		}
		if records["0000001234"].SIC != want {
			t.Fatalf("SIC %q = %d", sic, records["0000001234"].SIC)
		}
	}
}

func TestParseSECSubmissionsUniqueFormsAndForeignAnnualOrdering(t *testing.T) {
	body := `{"name":"Acme","cik":1234,"filings":{"recent":{"form":["20-F","20-F","40-F","20-F/A"],"filingDate":["2025-01-01","2025-01-01","2026-01-01","2027-01-01"]}}}`
	p := zipFile(t, map[string]string{"CIK0000001234.json": body})
	z, e := zip.OpenReader(p)
	if e != nil {
		t.Fatal(e)
	}
	m, e := ParseSECSubmissionsZIP(&z.Reader, ZIPParseLimits{MaxEntryBytes: 2000, MaxTotalBytes: 2000})
	z.Close()
	if e != nil {
		t.Fatal(e)
	}
	record := m["0000001234"]
	if !reflect.DeepEqual(record.RecentForms, []string{"20-F", "40-F", "20-F/A"}) {
		t.Fatalf("forms = %v", record.RecentForms)
	}
	if record.LatestAnnualForm != "40-F" {
		t.Fatalf("latest annual = %q", record.LatestAnnualForm)
	}
	body = `{"name":"Acme","cik":1234,"filings":{"recent":{"form":["20-F","40-F"],"filingDate":["2026-02-01","2026-01-01"]}}}`
	p = zipFile(t, map[string]string{"CIK0000001234.json": body})
	z, e = zip.OpenReader(p)
	if e != nil {
		t.Fatal(e)
	}
	m, e = ParseSECSubmissionsZIP(&z.Reader, ZIPParseLimits{MaxEntryBytes: 2000, MaxTotalBytes: 2000})
	z.Close()
	if e != nil {
		t.Fatal(e)
	}
	if m["0000001234"].LatestAnnualForm != "20-F" {
		t.Fatalf("20-F branch = %q", m["0000001234"].LatestAnnualForm)
	}
}

func TestParseSECCompanyFactsDuplicateAllFields(t *testing.T) {
	base := `{"val":1,"end":"2026-01-01","filed":"2026-01-02","form":"10-Q","accn":"x"}`
	for _, tc := range []struct {
		name, second string
		wantErr      bool
	}{
		{"exact", base, false},
		{"form", `{"val":1,"end":"2026-01-01","filed":"2026-01-02","form":"8-K","accn":"x"}`, true},
		{"filed", `{"val":1,"end":"2026-01-01","filed":"2026-01-03","form":"10-Q","accn":"x"}`, true},
		{"shares", `{"val":2,"end":"2026-01-01","filed":"2026-01-02","form":"10-Q","accn":"x"}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := `{"cik":1234,"facts":{"dei":{"EntityCommonStockSharesOutstanding":{"units":{"shares":[` + base + `,` + tc.second + `]}}}}}`
			p := zipFile(t, map[string]string{"CIK0000001234.json": body})
			z, e := zip.OpenReader(p)
			if e != nil {
				t.Fatal(e)
			}
			facts, e := ParseSECCompanyFactsZIP(&z.Reader, map[string]struct{}{"0000001234": {}}, ZIPParseLimits{MaxEntryBytes: 2000, MaxTotalBytes: 2000})
			z.Close()
			if tc.wantErr != (e != nil) {
				t.Fatalf("facts/error = %#v %v", facts, e)
			}
			if !tc.wantErr && len(facts) != 1 {
				t.Fatalf("facts = %#v", facts)
			}
		})
	}
}

func TestParseSECCompanyFactsBothConceptsAllowedAndAllFacts(t *testing.T) {
	body := `{"cik":1234,"facts":{"dei":{"EntityCommonStockSharesOutstanding":{"units":{"shares":[{"val":1,"end":"2026-01-01","filed":"2026-01-02","accn":"a"},{"val":2,"end":"2026-02-01","filed":"2026-02-02","accn":"b"}]}}},"us-gaap":{"CommonStockSharesOutstanding":{"units":{"shares":[{"val":3,"end":"2026-03-01","filed":"2026-03-02","accn":"c"}]}}}}}`
	p := zipFile(t, map[string]string{"CIK0000001234.json": body, "CIK0000001235.json": `{"cik":1235,"facts":{}}`})
	z, e := zip.OpenReader(p)
	if e != nil {
		t.Fatal(e)
	}
	facts, e := ParseSECCompanyFactsZIP(&z.Reader, map[string]struct{}{"0000001234": {}}, ZIPParseLimits{MaxEntryBytes: 5000, MaxTotalBytes: 5000})
	z.Close()
	if e != nil || len(facts) != 3 {
		t.Fatalf("facts/error = %#v %v", facts, e)
	}
}

func TestParseSECCompanyFactsSelectiveDecodeTrailingJSONAndDuplicateCIKIdentity(t *testing.T) {
	largeUnrelated := strings.Repeat(`{"val":9,"end":"2026-01-01","filed":"2026-01-02"},`, 2000) + `{"val":9,"end":"2026-01-01","filed":"2026-01-02"}`
	body := `{"cik":1234,"facts":{"us-gaap":{"Assets":{"units":{"USD":[` + largeUnrelated + `]}},"CommonStockSharesOutstanding":{"units":{"shares":[{"val":3,"end":"2026-03-01","filed":"2026-03-02","accn":"c"}]}}}}}`
	entries := []task4ZIPEntry{{"CIK0000001234.json", body}}
	facts, err := parseCompanyFactEntries(t, entries, int64(len(body)))
	if err != nil || len(facts) != 1 || facts[0].Shares != 3 {
		t.Fatalf("selective facts/error = %#v %v", facts, err)
	}
	if _, err := parseCompanyFactEntries(t, []task4ZIPEntry{{"CIK0000001234.json", body + " garbage"}}, int64(len(body)+8)); err == nil {
		t.Fatal("companyfacts trailing JSON accepted")
	}

	a := `{"cik":1234,"facts":{"dei":{"EntityCommonStockSharesOutstanding":{"units":{"shares":[{"val":1,"end":"2026-01-01","filed":"2026-01-02","accn":"a"}]}}}}}`
	b := `{"cik":1234,"facts":{"dei":{"EntityCommonStockSharesOutstanding":{"units":{"shares":[{"val":2,"end":"2026-02-01","filed":"2026-02-02","accn":"b"}]}}}}}`
	for _, pair := range [][]task4ZIPEntry{
		{{"CIK0000001234.json", a}, {"CIK0000001234.json", b}},
		{{"CIK0000001234.json", b}, {"CIK0000001234.json", a}},
	} {
		if _, err := parseCompanyFactEntries(t, pair, 5000); err == nil || !strings.Contains(err.Error(), "conflicting duplicate") {
			t.Fatalf("nonidentical duplicate entries %#v error = %v", pair, err)
		}
	}
	if facts, err := parseCompanyFactEntries(t, []task4ZIPEntry{{"CIK0000001234.json", a}, {"CIK0000001234.json", a}}, 5000); err != nil || len(facts) != 1 {
		t.Fatalf("identical duplicate entries = %#v %v", facts, err)
	}
}

func TestParseSECZIPEntryLimitBoundaries(t *testing.T) {
	body := `{"name":"Acme","cik":1234}`
	entries := []task4ZIPEntry{{"CIK0000001234.json", body}}
	if _, err := parseSubmissionEntriesWithLimit(t, entries, int64(len(body))); err != nil {
		t.Fatalf("exact boundary: %v", err)
	}
	if _, err := parseSubmissionEntriesWithLimit(t, entries, int64(len(body)-1)); err == nil || !strings.Contains(err.Error(), "limit") {
		t.Fatalf("one over error = %v", err)
	}
	if _, err := parseSubmissionEntriesWithLimit(t, entries, math.MaxInt64); err != nil {
		t.Fatalf("max int limit: %v", err)
	}
}

func TestParseSECZIPAggregateMaxIntDoesNotOverflow(t *testing.T) {
	p := zipFile(t, map[string]string{"CIK0000001234.json": `{"name":"A","cik":1234}`, "CIK0000001235.json": `{"name":"B","cik":1235}`})
	z, e := zip.OpenReader(p)
	if e != nil {
		t.Fatal(e)
	}
	defer z.Close()
	if _, e := ParseSECSubmissionsZIP(&z.Reader, ZIPParseLimits{MaxEntryBytes: math.MaxInt64, MaxTotalBytes: math.MaxInt64}); e != nil {
		t.Fatalf("max aggregate: %v", e)
	}
}

func TestParseSECZIPRejectsUnsafeDirectory(t *testing.T) {
	p := zipFileWithDirectory(t, "../", map[string]string{"CIK0000001234.json": `{"cik":1234}`})
	z, e := zip.OpenReader(p)
	if e != nil {
		t.Fatal(e)
	}
	defer z.Close()
	if _, e := ParseSECSubmissionsZIP(&z.Reader, ZIPParseLimits{MaxEntries: 2, MaxEntryBytes: 100, MaxTotalBytes: 100}); e == nil {
		t.Fatal("unsafe directory accepted")
	}
}

func TestParseSECCompanyFactsZIPRejectsEntryShapesAndCount(t *testing.T) {
	for _, name := range []string{"nested/CIK0000001234.json", "README.txt"} {
		t.Run(name, func(t *testing.T) {
			p := zipFile(t, map[string]string{name: `{"cik":1234}`})
			z, e := zip.OpenReader(p)
			if e != nil {
				t.Fatal(e)
			}
			_, e = ParseSECCompanyFactsZIP(&z.Reader, map[string]struct{}{"0000001234": {}}, ZIPParseLimits{MaxEntries: 2, MaxEntryBytes: 100, MaxTotalBytes: 100})
			z.Close()
			if e == nil {
				t.Fatal("non-root/nonmatching entry accepted")
			}
		})
	}
	p := zipFile(t, map[string]string{"CIK0000001234.json": `{"cik":1234}`, "CIK0000001235.json": `{"cik":1235}`})
	z, e := zip.OpenReader(p)
	if e != nil {
		t.Fatal(e)
	}
	_, e = ParseSECCompanyFactsZIP(&z.Reader, map[string]struct{}{"0000001234": {}, "0000001235": {}}, ZIPParseLimits{MaxEntries: 1, MaxEntryBytes: 100, MaxTotalBytes: 200})
	z.Close()
	if e == nil || !strings.Contains(e.Error(), "entry count") {
		t.Fatalf("error = %v", e)
	}
}

func TestSECBulkSourceConsumesOnlyRequiredInputs(t *testing.T) {
	tickers := `{"fields":["cik","name","ticker","exchange"],"data":[[1234,"Mapped","MAP","Nasdaq"],[9999,"Missing","MISS","NYSE"]]}`
	sub := makeZIPBytes(t, map[string]string{"CIK0000001234.json": `{"name":"Metadata","cik":1234,"sic":"1"}`, "CIK0000007777.json": `{"name":"Unmapped","cik":7777}`})
	facts := makeZIPBytes(t, map[string]string{"CIK0000001234.json": `{"cik":1234,"facts":{}}`})
	var calls []string
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls = append(calls, r.URL.Path)
		var b []byte
		switch r.URL.Path {
		case "/ticker":
			b = []byte(tickers)
		case "/sub":
			b = sub
		case "/facts":
			b = facts
		default:
			t.Fatalf("unexpected URL %s", r.URL)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(b)), Header: make(http.Header), Request: r}, nil
	})}
	s := SECBulkSource{Downloader: &Downloader{Client: client, CacheDir: t.TempDir(), MaxBytes: 1 << 20}, TickerURL: "https://x.test/ticker", SubmissionsURL: "https://x.test/sub", CompanyFactsURL: "https://x.test/facts", Limits: ZIPParseLimits{MaxEntries: 10, MaxEntryBytes: 5000, MaxTotalBytes: 5000}}
	recs, v, e := s.Load(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	if !reflect.DeepEqual(calls, []string{"/ticker", "/sub"}) {
		t.Fatalf("metadata calls = %v", calls)
	}
	if len(recs) != 2 || recs[0].CompanyName != "Metadata" || recs[1].SIC != 0 {
		t.Fatalf("merged = %#v", recs)
	}
	for _, rec := range recs {
		if rec.CIK == "0000007777" || rec.Ticker == "UNMAPPED" {
			t.Fatalf("submissions-only security emitted: %#v", rec)
		}
	}
	metadataVersion := v.SHA256
	calls = nil
	s.TickerURL = ""
	_, fv, e := s.LoadLatestShares(context.Background(), map[string]struct{}{"0000001234": {}})
	if e != nil {
		t.Fatal(e)
	}
	if !reflect.DeepEqual(calls, []string{"/facts", "/sub"}) {
		t.Fatalf("fact calls = %v", calls)
	}
	if fv.SHA256 == metadataVersion {
		t.Fatal("versions unexpectedly equal")
	}
}

func zipFileWithDirectory(t *testing.T, dir string, entries map[string]string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "dirs.zip")
	f, e := os.Create(p)
	if e != nil {
		t.Fatal(e)
	}
	w := zip.NewWriter(f)
	h := &zip.FileHeader{Name: dir, Method: zip.Store}
	h.SetMode(os.ModeDir | 0755)
	if _, e = w.CreateHeader(h); e != nil {
		t.Fatal(e)
	}
	for n, b := range entries {
		x, e := w.Create(n)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = x.Write([]byte(b)); e != nil {
			t.Fatal(e)
		}
	}
	if e = w.Close(); e != nil {
		t.Fatal(e)
	}
	if e = f.Close(); e != nil {
		t.Fatal(e)
	}
	return p
}

type task4ZIPEntry struct {
	name string
	body string
}

func orderedZIPFile(t *testing.T, entries []task4ZIPEntry) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "ordered.zip")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	w := zip.NewWriter(f)
	for _, entry := range entries {
		x, err := w.Create(entry.name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := x.Write([]byte(entry.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return p
}

func parseSubmissionEntries(t *testing.T, entries []task4ZIPEntry) (map[string]SecuritySourceRecord, error) {
	t.Helper()
	return parseSubmissionEntriesWithLimit(t, entries, 1<<20)
}

func parseSubmissionEntriesWithLimit(t *testing.T, entries []task4ZIPEntry, limit int64) (map[string]SecuritySourceRecord, error) {
	t.Helper()
	z, err := zip.OpenReader(orderedZIPFile(t, entries))
	if err != nil {
		t.Fatal(err)
	}
	defer z.Close()
	return ParseSECSubmissionsZIP(&z.Reader, ZIPParseLimits{MaxEntries: 100, MaxEntryBytes: limit, MaxTotalBytes: math.MaxInt64})
}

func parseCompanyFactEntries(t *testing.T, entries []task4ZIPEntry, limit int64) ([]ShareFact, error) {
	t.Helper()
	z, err := zip.OpenReader(orderedZIPFile(t, entries))
	if err != nil {
		t.Fatal(err)
	}
	defer z.Close()
	return ParseSECCompanyFactsZIP(&z.Reader, map[string]struct{}{"0000001234": {}}, ZIPParseLimits{MaxEntries: 100, MaxEntryBytes: limit, MaxTotalBytes: math.MaxInt64})
}
