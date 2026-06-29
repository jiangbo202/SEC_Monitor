package discovery

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func zipFile(t *testing.T, entries map[string]string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "x.zip")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	w := zip.NewWriter(f)
	for name, body := range entries {
		x, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := x.Write([]byte(body)); err != nil {
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

func TestParseSECTickerExchange(t *testing.T) {
	in := `{"fields":["exchange","ticker","name","cik"],"data":[["Nasdaq","brk.b","Berkshire",1067983],["NYSE","BRK.A","Berkshire",1067983]]}`
	recs, err := ParseSECTickerExchange(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 2 || recs[0].CIK != "0001067983" || recs[1].Ticker != "BRK.B" {
		t.Fatalf("records = %#v", recs)
	}
	bad := `{"fields":["cik","name","ticker","exchange"],"data":[[1,"a","X","N"],[2,"b","X","N"]]}`
	if rows, err := ParseSECTickerExchange(strings.NewReader(bad)); err != nil || len(rows) != 2 {
		t.Fatalf("rows=%#v error=%v", rows, err)
	}
}

func TestParseSECSubmissions(t *testing.T) {
	body := `{"name":"Acme","cik":"1234","entityType":"operating","sic":"3571","stateOfIncorporation":"DE","tickers":["ACME"],"exchanges":["Nasdaq"],"filings":{"recent":{"form":["10-Q","10-K/A","10-K","8-K"],"accessionNumber":["1","2","3","4"],"filingDate":["2026-05-01","2026-04-01","2025-03-01","2026-06-01"],"reportDate":["","","",""],"primaryDocument":["a","b","c","d"],"items":["","","","2.01,9.01"]}}}`
	p := zipFile(t, map[string]string{"CIK0000001234.json": body})
	z, err := OpenSafeZIP(p, 10, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer z.Close()
	m, err := ParseSECSubmissionsZIP(&z.Reader, ZIPParseLimits{MaxEntryBytes: 1 << 20, MaxTotalBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	x := m["0000001234"]
	wantCompleted := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	if x.CompanyName != "Acme" || x.SIC != 3571 || x.LatestAnnualForm != "10-K/A" || !x.HasBusinessCombinationItem201 || x.BusinessCombinationCompletedAt == nil || !x.BusinessCombinationCompletedAt.Equal(wantCompleted) || len(x.RecentForms) != 4 {
		t.Fatalf("metadata = %#v", x)
	}
}

func TestParseSECSubmissionsCapturesLatestBusinessCombinationCompletionDate(t *testing.T) {
	body := `{"name":"Acme","cik":"1234","filings":{"recent":{"form":["8-K","8-K/A","8-K","10-Q"],"filingDate":["2026-05-01","2026-06-02","2026-06-01","2026-06-03"],"items":["2.01","2.01","1.01","2.01"]}}}`
	p := zipFile(t, map[string]string{"CIK0000001234.json": body})
	z, err := OpenSafeZIP(p, 10, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer z.Close()
	m, err := ParseSECSubmissionsZIP(&z.Reader, ZIPParseLimits{MaxEntryBytes: 1 << 20, MaxTotalBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 6, 2, 0, 0, 0, 0, time.UTC)
	got := m["0000001234"]
	if !got.HasBusinessCombinationItem201 || got.BusinessCombinationCompletedAt == nil || !got.BusinessCombinationCompletedAt.Equal(want) {
		t.Fatalf("metadata = %#v", got)
	}
}

func TestParseSECSubmissionsRejectsInvalid(t *testing.T) {
	for _, tc := range []struct{ name, entry, body, contains string }{
		{"name", "nested/CIK0000001234.json", `{}`, "entry"},
		{"columns", "CIK0000001234.json", `{"cik":1234,"filings":{"recent":{"form":["10-K"],"accessionNumber":[],"filingDate":["2026-01-01","2026-01-02"]}}}`, "length"},
		{"empty-present-column", "CIK0000001234.json", `{"cik":1234,"filings":{"recent":{"form":["10-K"],"accessionNumber":[]}}}`, "length"},
		{"limit", "CIK0000001234.json", `{"name":"too long"}`, "limit"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := zipFile(t, map[string]string{tc.entry: tc.body})
			z, err := zip.OpenReader(p)
			if err != nil {
				t.Fatal(err)
			}
			defer z.Close()
			lim := int64(1 << 20)
			if tc.name == "limit" {
				lim = 2
			}
			_, err = ParseSECSubmissionsZIP(&z.Reader, ZIPParseLimits{MaxEntryBytes: lim, MaxTotalBytes: lim})
			if err == nil || !strings.Contains(err.Error(), tc.contains) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestParseSECCompanyFacts(t *testing.T) {
	body := `{"cik":1234,"facts":{"dei":{"EntityCommonStockSharesOutstanding":{"units":{"shares":[{"val":12,"end":"2026-05-31","filed":"2026-06-01","form":"10-Q","accn":"0001-26-1"},{"val":12,"end":"2026-05-31","filed":"2026-06-01","form":"10-Q","accn":"0001-26-1"}]} }},"us-gaap":{"Assets":{"units":{"USD":[{"val":9,"end":"2026-01-01","filed":"2026-01-02"}]}}}}}`
	p := zipFile(t, map[string]string{"CIK0000001234.json": body, "CIK0000009999.json": `{"cik":9999}`})
	z, err := zip.OpenReader(p)
	if err != nil {
		t.Fatal(err)
	}
	defer z.Close()
	facts, err := ParseSECCompanyFactsZIP(&z.Reader, map[string]struct{}{"0000001234": {}}, ZIPParseLimits{MaxEntryBytes: 1 << 20, MaxTotalBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	if len(facts) != 1 || facts[0].Shares != 12 || facts[0].Concept != "dei:EntityCommonStockSharesOutstanding" || facts[0].SourceURL == "" {
		t.Fatalf("facts = %#v", facts)
	}
}

func TestParseSECCompanyFactsConflictAndMalformed(t *testing.T) {
	for _, body := range []string{
		`{"cik":1234,"facts":{"dei":{"EntityCommonStockSharesOutstanding":{"units":{"shares":[{"val":1,"end":"bad","filed":"2026-01-01","accn":"x"}]}}}}}`,
		`{"cik":1234,"facts":{"dei":{"EntityCommonStockSharesOutstanding":{"units":{"shares":[{"val":1,"end":"2026-01-01","filed":"bad","accn":"x"}]}}}}}`,
		`{"cik":1234,"facts":{"dei":{"EntityCommonStockSharesOutstanding":{"units":{"shares":[{"val":-1,"end":"2026-01-01","filed":"2026-01-02","accn":"x"}]}}}}}`,
		`{"cik":1234,"facts":{"dei":{"EntityCommonStockSharesOutstanding":{"units":{"shares":[{"val":1.5,"end":"2026-01-01","filed":"2026-01-02","accn":"x"}]}}}}}`,
		`{"cik":1234,"facts":{"dei":{"EntityCommonStockSharesOutstanding":{"units":{"shares":[{"val":1,"end":"2026-01-01","filed":"2026-01-02","accn":"x"},{"val":2,"end":"2026-01-01","filed":"2026-01-02","accn":"x"}]}}}}}`,
		`{"cik":1234,"facts":{"dei":{"EntityCommonStockSharesOutstanding":{"units":{"USD":[{"val":1,"end":"2026-01-01","filed":"2026-01-02","accn":"x"}]}}}}}`,
		`{"cik":1234,"facts":{"dei":{"EntityCommonStockSharesOutstanding":{"units":{"shares":[{"val":9223372036854775808.0,"end":"2026-01-01","filed":"2026-01-02","accn":"x"}]}}}}}`,
	} {
		p := zipFile(t, map[string]string{"CIK0000001234.json": body})
		z, err := zip.OpenReader(p)
		if err != nil {
			t.Fatal(err)
		}
		_, err = ParseSECCompanyFactsZIP(&z.Reader, map[string]struct{}{"0000001234": {}}, ZIPParseLimits{MaxEntryBytes: 1 << 20, MaxTotalBytes: 1 << 20})
		z.Close()
		if err == nil {
			t.Fatalf("expected error for %s", body)
		}
	}
}

func TestParseSECCompanyFactsAggregateLimit(t *testing.T) {
	p := zipFile(t, map[string]string{
		"CIK0000001234.json": `{"cik":1234,"facts":{}}`,
		"CIK0000001235.json": `{"cik":1235,"facts":{}}`,
	})
	z, err := zip.OpenReader(p)
	if err != nil {
		t.Fatal(err)
	}
	defer z.Close()
	_, err = ParseSECCompanyFactsZIP(&z.Reader, map[string]struct{}{"0000001234": {}, "0000001235": {}}, ZIPParseLimits{MaxEntryBytes: 100, MaxTotalBytes: 30})
	if err == nil || !strings.Contains(err.Error(), "aggregate") {
		t.Fatalf("error = %v", err)
	}
}

func TestSECBulkSource(t *testing.T) {
	tickers := `{"fields":["cik","name","ticker","exchange"],"data":[[1234,"Acme","ACME","Nasdaq"]]}`
	sub := makeZIPBytes(t, map[string]string{"CIK0000001234.json": `{"name":"Acme","cik":1234,"sic":"3571","stateOfIncorporation":"DE","filings":{"recent":{"form":["10-Q"],"accessionNumber":["0000001234-26-000001"],"filingDate":["2026-05-01"],"acceptanceDateTime":["2026-05-01T12:34:56Z"]}}}`})
	cf := makeZIPBytes(t, map[string]string{"CIK0000001234.json": `{"cik":1234,"facts":{"dei":{"EntityCommonStockSharesOutstanding":{"units":{"shares":[{"val":40000000,"end":"2026-03-31","filed":"2026-05-01","form":"10-Q","accn":"0000001234-26-000001"}]}}},"us-gaap":{"RevenueFromContractWithCustomerExcludingAssessedTax":{"units":{"USD":[{"val":15000000,"start":"2026-01-01","end":"2026-03-31","filed":"2026-05-01","form":"10-Q","accn":"0000001234-26-000001"}]}}}}}`})
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		var b []byte
		switch {
		case strings.Contains(r.URL.Path, "ticker"):
			b = []byte(tickers)
		case strings.Contains(r.URL.Path, "sub"):
			b = sub
		default:
			b = cf
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(b)), Header: make(http.Header), Request: r}, nil
	})}
	s := SECBulkSource{Downloader: &Downloader{Client: client, CacheDir: t.TempDir(), MaxBytes: 1 << 20}, TickerURL: "https://x.test/ticker", SubmissionsURL: "https://x.test/sub", CompanyFactsURL: "https://x.test/facts", Limits: ZIPParseLimits{MaxEntries: 10, MaxEntryBytes: 1 << 20, MaxTotalBytes: 1 << 20}}
	recs, v, err := s.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 || recs[0].SIC != 3571 || v.SHA256 == "" {
		t.Fatalf("recs/version=%#v %#v", recs, v)
	}
	facts, fv, err := s.LoadLatestShares(context.Background(), map[string]struct{}{"0000001234": {}})
	if err != nil {
		t.Fatal(err)
	}
	if len(facts) != 1 || facts[0].AcceptedAt.IsZero() || fv.Source != "sec-companyfacts-submissions" {
		t.Fatalf("facts/version=%#v %#v", facts, fv)
	}
	selection := SelectShareSnapshot(facts, nil, time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC))
	if selection.QualityStatus != QualityStatusValid {
		t.Fatalf("integrated source selection = %#v, want valid", selection)
	}
	financials, financialVersion, err := s.LoadFinancialFacts(context.Background(), map[string]struct{}{"0000001234": {}})
	if err != nil {
		t.Fatal(err)
	}
	if len(financials) != 1 || financials[0].Metric != FinancialMetricRevenue || financialVersion.Source != "sec-financialfacts" {
		t.Fatalf("financials/version=%#v %#v", financials, financialVersion)
	}
}

func TestSECBulkSourceSharesRequiresBothArchives(t *testing.T) {
	base := SECBulkSource{Downloader: &Downloader{}, CompanyFactsURL: "https://x.test/facts"}
	if _, _, err := base.LoadLatestShares(context.Background(), map[string]struct{}{"0000001234": {}}); err == nil || !strings.Contains(err.Error(), "submissions URL") {
		t.Fatalf("missing submissions URL error = %v", err)
	}
}

func TestSECBulkSourceSharesDefersMissingAcceptanceToLatestSelection(t *testing.T) {
	const (
		oldAccession    = "0000001234-25-000001"
		latestAccession = "0000001234-26-000001"
	)
	companyFacts := makeZIPBytes(t, map[string]string{"CIK0000001234.json": `{"cik":1234,"facts":{"dei":{"EntityCommonStockSharesOutstanding":{"units":{"shares":[{"val":10,"end":"2025-12-31","filed":"2026-01-02","form":"10-K","accn":"` + oldAccession + `"},{"val":20,"end":"2026-03-31","filed":"2026-05-01","form":"10-Q","accn":"` + latestAccession + `"}]}}}}}`})
	tests := []struct {
		name, matchedAccession, form, filed, acceptance string
		wantStatus, wantReason                          string
		wantShares                                      int64
	}{
		{name: "irrelevant historical fact is unmatched", matchedAccession: latestAccession, form: "10-Q", filed: "2026-05-01", acceptance: "2026-05-01T12:34:56Z", wantStatus: QualityStatusValid, wantReason: ReasonShareSelected, wantShares: 20},
		{name: "latest fact is unmatched", matchedAccession: oldAccession, form: "10-K", filed: "2026-01-02", acceptance: "2026-01-02T12:34:56Z", wantStatus: QualityStatusMissing, wantReason: ReasonShareAcceptedAtMissing},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			submissions := makeZIPBytes(t, map[string]string{"CIK0000001234.json": `{"name":"Acme","cik":1234,"filings":{"recent":{"form":["` + test.form + `"],"accessionNumber":["` + test.matchedAccession + `"],"filingDate":["` + test.filed + `"],"acceptanceDateTime":["` + test.acceptance + `"]}}}`})
			client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				body := companyFacts
				if strings.Contains(r.URL.Path, "sub") {
					body = submissions
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(body)), Header: make(http.Header), Request: r}, nil
			})}
			source := SECBulkSource{Downloader: &Downloader{Client: client, CacheDir: t.TempDir(), MaxBytes: 1 << 20}, CompanyFactsURL: "https://x.test/facts", SubmissionsURL: "https://x.test/sub", Limits: ZIPParseLimits{MaxEntries: 10, MaxEntryBytes: 1 << 20, MaxTotalBytes: 1 << 20}}

			facts, version, err := source.LoadLatestShares(context.Background(), map[string]struct{}{"0000001234": {}})
			if err != nil {
				t.Fatal(err)
			}
			if len(facts) != 2 {
				t.Fatalf("facts = %#v, want two partially enriched facts", facts)
			}
			if want := testBulkVersionHash(companyFacts, submissions); version.Source != "sec-companyfacts-submissions" || version.Version != want || version.SHA256 != want {
				t.Fatalf("version = %#v, want double-archive hash %q", version, want)
			}
			selection := SelectShareSnapshot(facts, nil, time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC))
			if selection.QualityStatus != test.wantStatus || selection.ReasonCode != test.wantReason {
				t.Fatalf("selection = %#v, want status=%q reason=%q", selection, test.wantStatus, test.wantReason)
			}
			if test.wantShares > 0 && (selection.Fact == nil || selection.Fact.Shares != test.wantShares) {
				t.Fatalf("selection fact = %#v, want shares=%d", selection.Fact, test.wantShares)
			}
		})
	}
}

func testBulkVersionHash(archives ...[]byte) string {
	combined := sha256.New()
	for _, archive := range archives {
		digest := sha256.Sum256(archive)
		combined.Write([]byte(hex.EncodeToString(digest[:]) + "\n"))
	}
	return hex.EncodeToString(combined.Sum(nil))
}

func makeZIPBytes(t *testing.T, entries map[string]string) []byte {
	t.Helper()
	var b bytes.Buffer
	w := zip.NewWriter(&b)
	for n, s := range entries {
		x, e := w.Create(n)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = x.Write([]byte(s)); e != nil {
			t.Fatal(e)
		}
	}
	if e := w.Close(); e != nil {
		t.Fatal(e)
	}
	return b.Bytes()
}
