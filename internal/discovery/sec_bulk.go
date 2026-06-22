package discovery

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

type ShareFact struct {
	CIK, Concept, Unit, Form, Accession string
	Instant, FiledAt                    time.Time
	Shares                              int64
	SourceURL                           string
}
type ShareFactSource interface {
	LoadLatestShares(context.Context, map[string]struct{}) ([]ShareFact, SourceVersion, error)
}
type ZIPParseLimits struct {
	MaxEntries                   int
	MaxEntryBytes, MaxTotalBytes int64
}
type SECBulkSource struct {
	Downloader                                 *Downloader
	TickerURL, SubmissionsURL, CompanyFactsURL string
	Limits                                     ZIPParseLimits
}

type tickerFile struct {
	Fields []string            `json:"fields"`
	Data   [][]json.RawMessage `json:"data"`
}

func ParseSECTickerExchange(r io.Reader) ([]SecuritySourceRecord, error) {
	dec := json.NewDecoder(r)
	dec.UseNumber()
	var f tickerFile
	if err := dec.Decode(&f); err != nil {
		return nil, fmt.Errorf("decode SEC ticker exchange: %w", err)
	}
	idx := map[string]int{}
	for i, n := range f.Fields {
		idx[n] = i
	}
	for _, n := range []string{"cik", "name", "ticker", "exchange"} {
		if _, ok := idx[n]; !ok {
			return nil, fmt.Errorf("missing required field %q", n)
		}
	}
	seen := map[string]SecuritySourceRecord{}
	for row, values := range f.Data {
		if len(values) != len(f.Fields) {
			return nil, fmt.Errorf("row %d has %d fields, expected %d", row+1, len(values), len(f.Fields))
		}
		for _, i := range idx {
			if i >= len(values) {
				return nil, fmt.Errorf("row %d has too few fields", row+1)
			}
		}
		var cik json.Number
		if err := json.Unmarshal(values[idx["cik"]], &cik); err != nil {
			return nil, fmt.Errorf("row %d invalid cik type", row+1)
		}
		n, err := strconv.ParseInt(string(cik), 10, 64)
		if err != nil || n <= 0 || n > 9999999999 {
			return nil, fmt.Errorf("row %d invalid cik", row+1)
		}
		var name, ticker, exchange string
		if json.Unmarshal(values[idx["name"]], &name) != nil || json.Unmarshal(values[idx["ticker"]], &ticker) != nil || json.Unmarshal(values[idx["exchange"]], &exchange) != nil {
			return nil, fmt.Errorf("row %d invalid string field", row+1)
		}
		ticker = strings.ToUpper(strings.TrimSpace(ticker))
		if ticker == "" {
			return nil, fmt.Errorf("row %d empty ticker", row+1)
		}
		rec := SecuritySourceRecord{CIK: fmt.Sprintf("%010d", n), Ticker: ticker, CompanyName: name, SecurityName: name, Exchange: exchange}
		if old, ok := seen[ticker]; ok && old.CIK != rec.CIK {
			return nil, fmt.Errorf("row %d duplicate ticker %q maps to different CIK", row+1, ticker)
		}
		seen[ticker] = rec
	}
	out := make([]SecuritySourceRecord, 0, len(seen))
	for _, x := range seen {
		out = append(out, x)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Ticker < out[j].Ticker })
	return out, nil
}

var secZIPName = regexp.MustCompile(`^CIK([0-9]{10})\.json$`)

type submission struct {
	Name       string          `json:"name"`
	CIK        json.RawMessage `json:"cik"`
	EntityType string          `json:"entityType"`
	SIC        string          `json:"sic"`
	State      string          `json:"stateOfIncorporation"`
	Tickers    []string        `json:"tickers"`
	Exchanges  []string        `json:"exchanges"`
	Filings    struct {
		Recent recentFilings `json:"recent"`
	} `json:"filings"`
}
type recentFilings struct {
	Form, AccessionNumber, FilingDate, ReportDate, PrimaryDocument, Items []string
	present                                                               map[string]bool
}

func (r *recentFilings) UnmarshalJSON(data []byte) error {
	type wire struct {
		Form      []string `json:"form"`
		Accession []string `json:"accessionNumber"`
		Filing    []string `json:"filingDate"`
		Report    []string `json:"reportDate"`
		Primary   []string `json:"primaryDocument"`
		Items     []string `json:"items"`
	}
	var w wire
	if err := json.Unmarshal(data, &w); err != nil {
		return err
	}
	r.Form = w.Form
	r.AccessionNumber = w.Accession
	r.FilingDate = w.Filing
	r.ReportDate = w.Report
	r.PrimaryDocument = w.Primary
	r.Items = w.Items
	r.present = make(map[string]bool)
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	for _, name := range []string{"form", "accessionNumber", "filingDate", "reportDate", "primaryDocument", "items"} {
		_, r.present[name] = fields[name]
	}
	return nil
}

func ParseSECSubmissionsZIP(z *zip.Reader, limits ZIPParseLimits) (map[string]SecuritySourceRecord, error) {
	if err := validateParseLimits(limits); err != nil {
		return nil, err
	}
	out := map[string]SecuritySourceRecord{}
	var total int64
	for i, f := range z.File {
		if f.FileInfo().IsDir() {
			if !safeZIPName(f.Name) {
				return nil, fmt.Errorf("invalid SEC submissions ZIP directory %q", f.Name)
			}
			continue
		}
		if limits.MaxEntries > 0 && i >= limits.MaxEntries {
			return nil, fmt.Errorf("ZIP entry count exceeds limit")
		}
		m := secZIPName.FindStringSubmatch(f.Name)
		if m == nil {
			return nil, fmt.Errorf("invalid SEC submissions ZIP entry %q", f.Name)
		}
		if m[1] == "0000000000" {
			return nil, fmt.Errorf("invalid SEC submissions CIK in entry %q", f.Name)
		}
		data, n, err := readZIPEntry(f, limits.MaxEntryBytes)
		if err != nil {
			return nil, err
		}
		if n > limits.MaxTotalBytes-total {
			return nil, fmt.Errorf("ZIP aggregate decoded bytes exceed limit")
		}
		total += n
		var s submission
		if err = json.Unmarshal(data, &s); err != nil {
			return nil, fmt.Errorf("submission CIK %s: %w", m[1], err)
		}
		cik, err := normalizeCIK(s.CIK)
		if err != nil {
			return nil, fmt.Errorf("submission CIK %s: invalid cik", m[1])
		}
		if cik != m[1] {
			return nil, fmt.Errorf("submission CIK %s does not match filename", cik)
		}
		nforms := len(s.Filings.Recent.Form)
		for name, col := range map[string][]string{"accessionNumber": s.Filings.Recent.AccessionNumber, "filingDate": s.Filings.Recent.FilingDate, "reportDate": s.Filings.Recent.ReportDate, "primaryDocument": s.Filings.Recent.PrimaryDocument, "items": s.Filings.Recent.Items} {
			if s.Filings.Recent.present[name] && len(col) != nforms {
				return nil, fmt.Errorf("submission CIK %s recent %s length mismatch", cik, name)
			}
		}
		rec := SecuritySourceRecord{CIK: cik, CompanyName: s.Name, SecurityName: s.Name, StateOfIncorporation: s.State}
		if s.SIC != "" {
			rec.SIC, err = strconv.Atoi(s.SIC)
			if err != nil {
				return nil, fmt.Errorf("submission CIK %s invalid SIC", cik)
			}
		}
		forms := map[string]bool{}
		var annualDate time.Time
		for j, form := range s.Filings.Recent.Form {
			if !forms[form] {
				forms[form] = true
				rec.RecentForms = append(rec.RecentForms, form)
			}
			var d time.Time
			if len(s.Filings.Recent.FilingDate) > 0 {
				d, err = time.Parse("2006-01-02", s.Filings.Recent.FilingDate[j])
				if err != nil {
					return nil, fmt.Errorf("submission CIK %s invalid filing date", cik)
				}
			}
			if (form == "10-K" || form == "10-K/A" || form == "20-F" || form == "40-F") && (rec.LatestAnnualForm == "" || d.After(annualDate)) {
				rec.LatestAnnualForm = form
				annualDate = d
			}
			if (form == "8-K" || form == "8-K/A") && len(s.Filings.Recent.Items) > 0 && containsItem201(s.Filings.Recent.Items[j]) {
				rec.HasBusinessCombinationItem201 = true
			}
		}
		out[cik] = rec
	}
	return out, nil
}

func containsItem201(s string) bool {
	for _, x := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ';' || r == ' ' }) {
		if x == "2.01" {
			return true
		}
	}
	return false
}
func normalizeCIK(raw json.RawMessage) (string, error) {
	var n json.Number
	if len(raw) == 0 {
		return "", fmt.Errorf("missing")
	}
	if raw[0] == '"' {
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return "", err
		}
		n = json.Number(s)
	} else {
		if err := json.Unmarshal(raw, &n); err != nil {
			return "", err
		}
	}
	v, err := strconv.ParseInt(string(n), 10, 64)
	if err != nil || v <= 0 || v > 9999999999 {
		return "", fmt.Errorf("invalid")
	}
	return fmt.Sprintf("%010d", v), nil
}

func ParseSECCompanyFactsZIP(z *zip.Reader, allowed map[string]struct{}, limits ZIPParseLimits) ([]ShareFact, error) {
	if err := validateParseLimits(limits); err != nil {
		return nil, err
	}
	var out []ShareFact
	seen := map[string]ShareFact{}
	var total int64
	for i, f := range z.File {
		if f.FileInfo().IsDir() {
			if !safeZIPName(f.Name) {
				return nil, fmt.Errorf("invalid SEC companyfacts ZIP directory %q", f.Name)
			}
			continue
		}
		if limits.MaxEntries > 0 && i >= limits.MaxEntries {
			return nil, fmt.Errorf("ZIP entry count exceeds limit")
		}
		m := secZIPName.FindStringSubmatch(f.Name)
		if m == nil {
			return nil, fmt.Errorf("invalid SEC companyfacts ZIP entry %q", f.Name)
		}
		if m[1] == "0000000000" {
			return nil, fmt.Errorf("invalid SEC companyfacts CIK in entry %q", f.Name)
		}
		if _, ok := allowed[m[1]]; !ok {
			continue
		}
		data, n, err := readZIPEntry(f, limits.MaxEntryBytes)
		if err != nil {
			return nil, err
		}
		if n > limits.MaxTotalBytes-total {
			return nil, fmt.Errorf("ZIP aggregate decoded bytes exceed limit")
		}
		total += n
		var doc struct {
			CIK   json.RawMessage `json:"cik"`
			Facts map[string]map[string]struct {
				Units map[string][]struct {
					Val                    json.Number `json:"val"`
					End, Filed, Form, Accn string
				} `json:"units"`
			} `json:"facts"`
		}
		dec := json.NewDecoder(strings.NewReader(string(data)))
		dec.UseNumber()
		if err = dec.Decode(&doc); err != nil {
			return nil, fmt.Errorf("companyfacts CIK %s: %w", m[1], err)
		}
		cik, err := normalizeCIK(doc.CIK)
		if err != nil || cik != m[1] {
			return nil, fmt.Errorf("companyfacts CIK %s invalid cik", m[1])
		}
		for _, spec := range []struct{ ns, concept string }{{"dei", "EntityCommonStockSharesOutstanding"}, {"us-gaap", "CommonStockSharesOutstanding"}} {
			concept, ok := doc.Facts[spec.ns][spec.concept]
			if !ok {
				continue
			}
			for unit, facts := range concept.Units {
				if unit != "shares" {
					return nil, fmt.Errorf("companyfacts %s/%s:%s: invalid unit %q", cik, spec.ns, spec.concept, unit)
				}
				for _, x := range facts {
					context := cik + "/" + spec.ns + ":" + spec.concept
					shares, err := parseShares(x.Val)
					if err != nil {
						return nil, fmt.Errorf("companyfacts %s: %w", context, err)
					}
					instant, err := time.Parse("2006-01-02", x.End)
					if err != nil {
						return nil, fmt.Errorf("companyfacts %s: invalid end date", context)
					}
					filed, err := time.Parse("2006-01-02", x.Filed)
					if err != nil {
						return nil, fmt.Errorf("companyfacts %s: invalid filed date", context)
					}
					key := cik + "|" + spec.ns + ":" + spec.concept + "|" + x.End + "|" + x.Accn
					source := "https://data.sec.gov/api/xbrl/companyfacts/CIK" + cik + ".json"
					if x.Accn != "" {
						source = "https://www.sec.gov/Archives/edgar/data/" + strings.TrimLeft(cik, "0") + "/" + strings.ReplaceAll(x.Accn, "-", "") + "/"
					}
					fact := ShareFact{CIK: cik, Concept: spec.ns + ":" + spec.concept, Unit: "shares", Form: x.Form, Accession: x.Accn, Instant: instant, FiledAt: filed, Shares: shares, SourceURL: source}
					if old, ok := seen[key]; ok {
						if old != fact {
							return nil, fmt.Errorf("companyfacts %s conflicting duplicate fact", context)
						}
						continue
					}
					seen[key] = fact
					out = append(out, fact)
				}
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.CIK != b.CIK {
			return a.CIK < b.CIK
		}
		if a.Concept != b.Concept {
			return a.Concept < b.Concept
		}
		if !a.Instant.Equal(b.Instant) {
			return a.Instant.Before(b.Instant)
		}
		return a.Accession < b.Accession
	})
	return out, nil
}

func parseShares(n json.Number) (int64, error) {
	s := string(n)
	if s == "" {
		return 0, fmt.Errorf("missing value")
	}
	r, ok := new(big.Rat).SetString(s)
	if !ok || !r.IsInt() || r.Sign() < 0 || !r.Num().IsInt64() {
		return 0, fmt.Errorf("value must be an integral nonnegative int64")
	}
	return r.Num().Int64(), nil
}
func validateParseLimits(l ZIPParseLimits) error {
	if l.MaxEntryBytes <= 0 || l.MaxTotalBytes <= 0 {
		return fmt.Errorf("ZIP decoded byte limits must be positive")
	}
	return nil
}
func readZIPEntry(f *zip.File, max int64) ([]byte, int64, error) {
	r, err := f.Open()
	if err != nil {
		return nil, 0, err
	}
	defer r.Close()
	lr := io.LimitReader(r, max)
	data, err := io.ReadAll(lr)
	if err != nil {
		return nil, 0, err
	}
	var extra [1]byte
	n, extraErr := r.Read(extra[:])
	if n > 0 {
		return nil, 0, fmt.Errorf("ZIP entry %q exceeds decoded byte limit", f.Name)
	}
	if extraErr != nil && extraErr != io.EOF {
		return nil, 0, extraErr
	}
	return data, int64(len(data)), nil
}

func (s SECBulkSource) validateDownloader() error {
	if s.Downloader == nil {
		return fmt.Errorf("SEC downloader is required")
	}
	return nil
}
func (s SECBulkSource) Load(ctx context.Context) ([]SecuritySourceRecord, SourceVersion, error) {
	if err := s.validateDownloader(); err != nil {
		return nil, SourceVersion{}, err
	}
	if s.TickerURL == "" || s.SubmissionsURL == "" {
		return nil, SourceVersion{}, fmt.Errorf("SEC metadata URLs are required")
	}
	a, err := s.Downloader.Download(ctx, s.TickerURL, "company_tickers_exchange.json", nil)
	if err != nil {
		return nil, SourceVersion{}, err
	}
	b, err := s.Downloader.Download(ctx, s.SubmissionsURL, "submissions.zip", nil)
	if err != nil {
		return nil, SourceVersion{}, err
	}
	f, err := os.Open(a.Path)
	if err != nil {
		return nil, SourceVersion{}, err
	}
	mappings, err := ParseSECTickerExchange(f)
	f.Close()
	if err != nil {
		return nil, SourceVersion{}, err
	}
	z, err := OpenSafeZIP(b.Path, limitEntries(s.Limits), s.Limits.MaxTotalBytes)
	if err != nil {
		return nil, SourceVersion{}, err
	}
	meta, err := ParseSECSubmissionsZIP(&z.Reader, s.Limits)
	z.Close()
	if err != nil {
		return nil, SourceVersion{}, err
	}
	for i := range mappings {
		if x, ok := meta[mappings[i].CIK]; ok {
			ticker, exchange := mappings[i].Ticker, mappings[i].Exchange
			mappings[i] = x
			mappings[i].Ticker = ticker
			mappings[i].Exchange = exchange
		}
	}
	return mappings, bulkVersion("sec-bulk", a, b), nil
}

// LoadLatestShares returns all eligible facts. Task 8 applies the latest-fact selection policy.
func (s SECBulkSource) LoadLatestShares(ctx context.Context, allowed map[string]struct{}) ([]ShareFact, SourceVersion, error) {
	if err := s.validateDownloader(); err != nil {
		return nil, SourceVersion{}, err
	}
	if s.CompanyFactsURL == "" {
		return nil, SourceVersion{}, fmt.Errorf("SEC companyfacts URL is required")
	}
	c, err := s.Downloader.Download(ctx, s.CompanyFactsURL, "companyfacts.zip", nil)
	if err != nil {
		return nil, SourceVersion{}, err
	}
	z, err := OpenSafeZIP(c.Path, limitEntries(s.Limits), s.Limits.MaxTotalBytes)
	if err != nil {
		return nil, SourceVersion{}, err
	}
	facts, err := ParseSECCompanyFactsZIP(&z.Reader, allowed, s.Limits)
	z.Close()
	if err != nil {
		return nil, SourceVersion{}, err
	}
	return facts, bulkVersion("sec-companyfacts", c), nil
}
func limitEntries(l ZIPParseLimits) int {
	if l.MaxEntries > 0 {
		return l.MaxEntries
	}
	return 100000
}
func bulkVersion(source string, rs ...DownloadResult) SourceVersion {
	h := sha256.New()
	for _, r := range rs {
		io.WriteString(h, r.SHA256+"\n")
	}
	digest := hex.EncodeToString(h.Sum(nil))
	return SourceVersion{Source: source, Version: digest, SHA256: digest}
}
