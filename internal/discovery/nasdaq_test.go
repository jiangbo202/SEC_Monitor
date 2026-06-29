package discovery

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestParseNasdaqListed(t *testing.T) {
	in := "\r\nSymbol|Security Name|Market Category|Test Issue|Financial Status|Round Lot Size|ETF|NextShares\r\n brk.b | Berkshire |Q|N|N|100|Y|N\r\nFile Creation Time: 0621202618:00|||||||\r\n"
	records, version, err := ParseNasdaqListed(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Ticker != "BRK.B" || records[0].Exchange != "Nasdaq" || !records[0].ETF || records[0].TestIssue {
		t.Fatalf("records = %#v", records)
	}
	if version != "0621202618:00" {
		t.Fatalf("version = %q", version)
	}
}

func TestParseNasdaqOtherExchangeMappings(t *testing.T) {
	header := "ACT Symbol|Security Name|Exchange|CQS Symbol|ETF|Round Lot Size|Test Issue|NASDAQ Symbol\n"
	var rows strings.Builder
	rows.WriteString(header)
	for _, r := range []string{"n|N|N|n|N|100|N|n", "a|A|A|a|N|100|N|a", "p|P|P|p|N|100|N|p", "z|Z|Z|z|N|100|N|z", "v|V|V|v|N|100|Y|v", "m|M|M|m|N|100|Y|m"} {
		rows.WriteString(r + "\n")
	}
	rows.WriteString("File Creation Time: 06212026|||||||\n")
	recs, _, err := ParseNasdaqOther(strings.NewReader(rows.String()))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"N": "NYSE", "A": "NYSE American", "P": "NYSE Arca", "Z": "Cboe BZX", "V": "IEX", "M": "NYSE Texas"}
	for _, rec := range recs {
		if rec.Exchange != want[rec.Ticker] {
			t.Fatalf("exchange[%s] = %q", rec.Ticker, rec.Exchange)
		}
	}
}

func TestParseNasdaqErrors(t *testing.T) {
	header := "Symbol|Security Name|Market Category|Test Issue|Financial Status|Round Lot Size|ETF|NextShares\n"
	for _, tc := range []struct{ name, body, contains string }{
		{"header", "bad|header\nFile Creation Time: x\n", "header"},
		{"row", header + "A|name\nFile Creation Time: x\n", "line 2"},
		{"bool", header + "A|name|Q|X|N|100|N|N\nFile Creation Time: x\n", "line 2"},
		{"footer", header + "A|name|Q|N|N|100|N|N\n", "footer"},
		{"conflict", header + "A|one|Q|N|N|100|N|N\nA|two|Q|N|N|100|N|N\nFile Creation Time: x\n", "duplicate ticker"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := ParseNasdaqListed(strings.NewReader(tc.body))
			if err == nil || !strings.Contains(err.Error(), tc.contains) {
				t.Fatalf("error = %v", err)
			}
		})
	}
	other := "ACT Symbol|Security Name|Exchange|CQS Symbol|ETF|Round Lot Size|Test Issue|NASDAQ Symbol\nA|name|X|A|N|100|N|A\nFile Creation Time: x\n"
	if _, _, err := ParseNasdaqOther(strings.NewReader(other)); err == nil || !strings.Contains(err.Error(), "unknown exchange") {
		t.Fatalf("error = %v", err)
	}
}

func TestNasdaqDirectorySource(t *testing.T) {
	listed := "Symbol|Security Name|Market Category|Test Issue|Financial Status|Round Lot Size|ETF|NextShares\nA|Alpha|Q|N|N|100|N|N\nFile Creation Time: 0621202618:00|||||||\n"
	other := "ACT Symbol|Security Name|Exchange|CQS Symbol|ETF|Round Lot Size|Test Issue|NASDAQ Symbol\nB|Beta|N|B|N|100|N|B\nFile Creation Time: 0621202619:00|||||||\n"
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		body := listed
		if strings.Contains(r.URL.Path, "other") {
			body = other
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header), Request: r}, nil
	})}
	s := NasdaqDirectorySource{Downloader: &Downloader{Client: client, CacheDir: t.TempDir(), MaxBytes: 1 << 20}, ListedURL: "https://example.test/listed", OtherURL: "https://example.test/other"}
	recs, version, err := s.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 2 || version.Source != "nasdaq-directory" || version.SHA256 == "" || version.Version != "0621202618:00+0621202619:00" || version.EffectiveAt.IsZero() {
		t.Fatalf("records/version = %#v %#v", recs, version)
	}
}
