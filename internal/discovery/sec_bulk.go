package discovery

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"os"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

type ShareFact struct {
	CIK, Concept, Unit, Form, Accession string
	Instant, FiledAt, AcceptedAt        time.Time
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
	CacheTTL                                   time.Duration
}

// SECTickerMappingSource is the lightweight SEC side of the daily listing
// discovery pass.  Unlike SECBulkSource.Load, it deliberately downloads only
// company_tickers_exchange.json and never touches submissions.zip.
type SECTickerMappingSource struct{ Bulk SECBulkSource }

func (s SECTickerMappingSource) Load(ctx context.Context) ([]SecuritySourceRecord, SourceVersion, error) {
	return s.Bulk.LoadTickerMappings(ctx)
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
	if err := requireJSONEOF(dec); err != nil {
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
		rawCIK := bytes.TrimSpace(values[idx["cik"]])
		if len(rawCIK) == 0 || rawCIK[0] < '0' || rawCIK[0] > '9' {
			return nil, fmt.Errorf("row %d invalid cik type", row+1)
		}
		var cik json.Number
		if err := json.Unmarshal(rawCIK, &cik); err != nil {
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
		name = strings.TrimSpace(name)
		ticker = strings.ToUpper(strings.TrimSpace(ticker))
		exchange = strings.TrimSpace(exchange)
		if ticker == "" {
			return nil, fmt.Errorf("row %d empty ticker", row+1)
		}
		if name == "" {
			return nil, fmt.Errorf("row %d empty name", row+1)
		}
		rec := SecuritySourceRecord{CIK: fmt.Sprintf("%010d", n), Ticker: ticker, CompanyName: name, SecurityName: name, Exchange: exchange}
		identityKey := rec.CIK + "\x00" + ticker
		if old, ok := seen[identityKey]; ok {
			if old.Exchange == "" && rec.Exchange != "" {
				seen[identityKey] = rec
				continue
			}
			if old.Exchange != "" && rec.Exchange == "" {
				continue
			}
			if !reflect.DeepEqual(old, rec) {
				return nil, fmt.Errorf("row %d conflicting duplicate ticker/CIK %q/%s", row+1, ticker, rec.CIK)
			}
			continue
		}
		seen[identityKey] = rec
	}
	out := make([]SecuritySourceRecord, 0, len(seen))
	for _, x := range seen {
		out = append(out, x)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Ticker < out[j].Ticker })
	return out, nil
}

var secZIPName = regexp.MustCompile(`^CIK([0-9]{10})\.json$`)
var secSubmissionZIPName = regexp.MustCompile(`^CIK([0-9]{10})(?:-submissions-[0-9]+)?\.json$`)

type submission struct {
	Name           string          `json:"name"`
	CIK            json.RawMessage `json:"cik"`
	EntityType     string          `json:"entityType"`
	SIC            string          `json:"sic"`
	SICDescription string          `json:"sicDescription"`
	State          string          `json:"stateOfIncorporation"`
	Tickers        []string        `json:"tickers"`
	Exchanges      []string        `json:"exchanges"`
	Filings        struct {
		Recent recentFilings `json:"recent"`
	} `json:"filings"`
}
type recentFilings struct {
	Form, AccessionNumber, FilingDate, ReportDate, AcceptanceDate, PrimaryDocument, Items []string
	present                                                                               map[string]bool
}

func (r *recentFilings) UnmarshalJSON(data []byte) error {
	type wire struct {
		Form       []string `json:"form"`
		Accession  []string `json:"accessionNumber"`
		Filing     []string `json:"filingDate"`
		Report     []string `json:"reportDate"`
		Acceptance []string `json:"acceptanceDateTime"`
		Primary    []string `json:"primaryDocument"`
		Items      []string `json:"items"`
	}
	var w wire
	if err := json.Unmarshal(data, &w); err != nil {
		return err
	}
	r.Form = w.Form
	r.AccessionNumber = w.Accession
	r.FilingDate = w.Filing
	r.ReportDate = w.Report
	r.AcceptanceDate = w.Acceptance
	r.PrimaryDocument = w.Primary
	r.Items = w.Items
	r.present = make(map[string]bool)
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	for _, name := range []string{"form", "accessionNumber", "filingDate", "reportDate", "acceptanceDateTime", "primaryDocument", "items"} {
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
		if f.Name == "placeholder.txt" {
			continue
		}
		m := secSubmissionZIPName.FindStringSubmatch(f.Name)
		if m == nil {
			return nil, fmt.Errorf("invalid SEC submissions ZIP entry %q", f.Name)
		}
		if secZIPName.FindStringSubmatch(f.Name) == nil {
			continue
		}
		if m[1] == "0000000000" {
			return nil, fmt.Errorf("invalid SEC submissions CIK in entry %q", f.Name)
		}
		if err := checkDeclaredZIPSize(f, limits.MaxEntryBytes, limits.MaxTotalBytes-total); err != nil {
			return nil, err
		}
		var s submission
		n, err := decodeZIPJSON(f, limits.MaxEntryBytes, &s, false)
		if err != nil {
			return nil, fmt.Errorf("submission CIK %s: %w", m[1], err)
		}
		if n > limits.MaxTotalBytes-total {
			return nil, fmt.Errorf("ZIP aggregate decoded bytes exceed limit")
		}
		total += n
		cik, err := normalizeCIK(s.CIK)
		if err != nil {
			return nil, fmt.Errorf("submission CIK %s: invalid cik", m[1])
		}
		if cik != m[1] {
			return nil, fmt.Errorf("submission CIK %s does not match filename", cik)
		}
		nforms := len(s.Filings.Recent.Form)
		for name, col := range map[string][]string{"accessionNumber": s.Filings.Recent.AccessionNumber, "filingDate": s.Filings.Recent.FilingDate, "reportDate": s.Filings.Recent.ReportDate, "acceptanceDateTime": s.Filings.Recent.AcceptanceDate, "primaryDocument": s.Filings.Recent.PrimaryDocument, "items": s.Filings.Recent.Items} {
			if s.Filings.Recent.present[name] && len(col) != nforms {
				return nil, fmt.Errorf("submission CIK %s recent %s length mismatch", cik, name)
			}
		}
		name := strings.TrimSpace(s.Name)
		rec := SecuritySourceRecord{CIK: cik, CompanyName: name, SecurityName: name, SICDescription: strings.TrimSpace(s.SICDescription), StateOfIncorporation: strings.TrimSpace(s.State)}
		if s.SIC != "" {
			if strings.Trim(s.SIC, "0123456789") != "" {
				return nil, fmt.Errorf("submission CIK %s invalid SIC", cik)
			}
			rec.SIC, err = strconv.Atoi(s.SIC)
			if err != nil || rec.SIC < 0 || rec.SIC > 9999 {
				return nil, fmt.Errorf("submission CIK %s invalid SIC", cik)
			}
		}
		forms := map[string]bool{}
		var annualDate time.Time
		var businessCombinationDate time.Time
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
			if s.Filings.Recent.present["accessionNumber"] {
				accession := strings.TrimSpace(s.Filings.Recent.AccessionNumber[j])
				if accession == "" {
					return nil, fmt.Errorf("submission CIK %s empty accession number", cik)
				}
				metadata := FilingMetadata{CIK: cik, Accession: accession, Form: form, FiledAt: d}
				if s.Filings.Recent.present["items"] {
					metadata.Items = strings.TrimSpace(s.Filings.Recent.Items[j])
				}
				if s.Filings.Recent.present["primaryDocument"] {
					metadata.PrimaryDocument = strings.TrimSpace(s.Filings.Recent.PrimaryDocument[j])
				}
				if s.Filings.Recent.present["reportDate"] && s.Filings.Recent.ReportDate[j] != "" {
					metadata.ReportAt, err = time.Parse("2006-01-02", s.Filings.Recent.ReportDate[j])
					if err != nil {
						return nil, fmt.Errorf("submission CIK %s invalid report date", cik)
					}
				}
				if s.Filings.Recent.present["acceptanceDateTime"] {
					if !secAccessionNumber.MatchString(accession) {
						return nil, fmt.Errorf("submission CIK %s invalid accession number", cik)
					}
					metadata.AcceptedAt, err = time.Parse(time.RFC3339Nano, s.Filings.Recent.AcceptanceDate[j])
					if err != nil {
						return nil, fmt.Errorf("submission CIK %s invalid acceptance date time", cik)
					}
				}
				rec.FilingMetadata = append(rec.FilingMetadata, metadata)
			} else if s.Filings.Recent.present["acceptanceDateTime"] {
				return nil, fmt.Errorf("submission CIK %s acceptance date time requires accession number", cik)
			}
			if (form == "10-K" || form == "10-K/A" || form == "20-F" || form == "40-F") && (rec.LatestAnnualForm == "" || d.After(annualDate)) {
				rec.LatestAnnualForm = form
				annualDate = d
			}
			if (form == "8-K" || form == "8-K/A") && len(s.Filings.Recent.Items) > 0 && containsItem201(s.Filings.Recent.Items[j]) {
				rec.HasBusinessCombinationItem201 = true
				if !d.IsZero() && (businessCombinationDate.IsZero() || d.After(businessCombinationDate)) {
					businessCombinationDate = d
				}
			}
		}
		if !businessCombinationDate.IsZero() {
			rec.BusinessCombinationCompletedAt = &businessCombinationDate
		}
		if old, ok := out[cik]; ok {
			if !reflect.DeepEqual(old, rec) {
				return nil, fmt.Errorf("submission CIK %s conflicting duplicate archive entry", cik)
			}
			continue
		}
		out[cik] = rec
	}
	return out, nil
}

var secAccessionNumber = regexp.MustCompile(`^[0-9]{10}-[0-9]{2}-[0-9]{6}$`)

// EnrichShareFactsWithAcceptance returns a copy of facts with SEC acceptance
// timestamps joined by CIK and accession number after validating filing identity.
func EnrichShareFactsWithAcceptance(facts []ShareFact, metadata []FilingMetadata) ([]ShareFact, error) {
	byAccession := make(map[string]FilingMetadata, len(metadata))
	for _, filing := range metadata {
		filing.CIK = strings.TrimSpace(filing.CIK)
		filing.Accession = strings.TrimSpace(filing.Accession)
		if filing.CIK == "" || filing.Accession == "" {
			return nil, fmt.Errorf("acceptance metadata requires CIK and accession")
		}
		if old, ok := byAccession[filing.Accession]; ok {
			if !sameFilingMetadata(old, filing) {
				continue
			}
			continue
		}
		byAccession[filing.Accession] = filing
	}

	out := append([]ShareFact(nil), facts...)
	for i := range out {
		filing, ok := byAccession[strings.TrimSpace(out[i].Accession)]
		if !ok {
			continue
		}
		if filing.CIK != strings.TrimSpace(out[i].CIK) {
			continue
		}
		if out[i].Form == "" || out[i].FiledAt.IsZero() || filing.Form == "" || filing.FiledAt.IsZero() {
			continue
		}
		if out[i].Form != filing.Form {
			continue
		}
		if !sameCivilDate(out[i].FiledAt, filing.FiledAt) {
			continue
		}
		if filing.AcceptedAt.IsZero() {
			continue
		}
		if !out[i].AcceptedAt.IsZero() && !out[i].AcceptedAt.Equal(filing.AcceptedAt) {
			continue
		}
		out[i].AcceptedAt = filing.AcceptedAt
	}
	return out, nil
}

func sameCivilDate(a, b time.Time) bool {
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	return ay == by && am == bm && ad == bd
}

func sameFilingMetadata(a, b FilingMetadata) bool {
	return a.CIK == b.CIK && a.Accession == b.Accession && a.Form == b.Form && a.Items == b.Items &&
		a.FiledAt.Equal(b.FiledAt) && a.ReportAt.Equal(b.ReportAt) && a.AcceptedAt.Equal(b.AcceptedAt)
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

// ParseSECSubmissionJSON parses the per-issuer submissions endpoint used by
// the daily listing discovery pass.  The validation mirrors the archive
// parser: an issuer document must identify the CIK requested by the caller.
func ParseSECSubmissionJSON(r io.Reader, expectedCIK string) (SecuritySourceRecord, error) {
	dec := json.NewDecoder(r)
	dec.UseNumber()
	var s submission
	if err := dec.Decode(&s); err != nil {
		return SecuritySourceRecord{}, err
	}
	if err := requireJSONEOF(dec); err != nil {
		return SecuritySourceRecord{}, err
	}
	cik, err := normalizeCIK(s.CIK)
	if err != nil || cik != expectedCIK {
		return SecuritySourceRecord{}, fmt.Errorf("submission CIK %s does not match requested issuer", cik)
	}
	nforms := len(s.Filings.Recent.Form)
	for name, col := range map[string][]string{"accessionNumber": s.Filings.Recent.AccessionNumber, "filingDate": s.Filings.Recent.FilingDate, "reportDate": s.Filings.Recent.ReportDate, "acceptanceDateTime": s.Filings.Recent.AcceptanceDate, "primaryDocument": s.Filings.Recent.PrimaryDocument, "items": s.Filings.Recent.Items} {
		if s.Filings.Recent.present[name] && len(col) != nforms {
			return SecuritySourceRecord{}, fmt.Errorf("submission CIK %s recent %s length mismatch", cik, name)
		}
	}
	record := SecuritySourceRecord{CIK: cik, CompanyName: strings.TrimSpace(s.Name), SecurityName: strings.TrimSpace(s.Name), SICDescription: strings.TrimSpace(s.SICDescription), StateOfIncorporation: strings.TrimSpace(s.State)}
	if s.SIC != "" {
		if strings.Trim(s.SIC, "0123456789") != "" {
			return SecuritySourceRecord{}, fmt.Errorf("submission CIK %s invalid SIC", cik)
		}
		record.SIC, err = strconv.Atoi(s.SIC)
		if err != nil || record.SIC < 0 || record.SIC > 9999 {
			return SecuritySourceRecord{}, fmt.Errorf("submission CIK %s invalid SIC", cik)
		}
	}
	forms := map[string]bool{}
	var annualDate, businessCombinationDate time.Time
	for i, form := range s.Filings.Recent.Form {
		if !forms[form] {
			forms[form] = true
			record.RecentForms = append(record.RecentForms, form)
		}
		var filed time.Time
		if len(s.Filings.Recent.FilingDate) > 0 {
			filed, err = time.Parse("2006-01-02", s.Filings.Recent.FilingDate[i])
			if err != nil {
				return SecuritySourceRecord{}, fmt.Errorf("submission CIK %s invalid filing date", cik)
			}
		}
		if s.Filings.Recent.present["accessionNumber"] {
			accession := strings.TrimSpace(s.Filings.Recent.AccessionNumber[i])
			if accession == "" {
				return SecuritySourceRecord{}, fmt.Errorf("submission CIK %s empty accession number", cik)
			}
			metadata := FilingMetadata{CIK: cik, Accession: accession, Form: form, FiledAt: filed}
			if s.Filings.Recent.present["items"] {
				metadata.Items = strings.TrimSpace(s.Filings.Recent.Items[i])
			}
			if s.Filings.Recent.present["primaryDocument"] {
				metadata.PrimaryDocument = strings.TrimSpace(s.Filings.Recent.PrimaryDocument[i])
			}
			if s.Filings.Recent.present["reportDate"] && s.Filings.Recent.ReportDate[i] != "" {
				metadata.ReportAt, err = time.Parse("2006-01-02", s.Filings.Recent.ReportDate[i])
				if err != nil {
					return SecuritySourceRecord{}, fmt.Errorf("submission CIK %s invalid report date", cik)
				}
			}
			if s.Filings.Recent.present["acceptanceDateTime"] {
				if !secAccessionNumber.MatchString(accession) {
					return SecuritySourceRecord{}, fmt.Errorf("submission CIK %s invalid accession number", cik)
				}
				metadata.AcceptedAt, err = time.Parse(time.RFC3339Nano, s.Filings.Recent.AcceptanceDate[i])
				if err != nil {
					return SecuritySourceRecord{}, fmt.Errorf("submission CIK %s invalid acceptance date time", cik)
				}
			}
			record.FilingMetadata = append(record.FilingMetadata, metadata)
		} else if s.Filings.Recent.present["acceptanceDateTime"] {
			return SecuritySourceRecord{}, fmt.Errorf("submission CIK %s acceptance date time requires accession number", cik)
		}
		if (form == "10-K" || form == "10-K/A" || form == "20-F" || form == "40-F") && (record.LatestAnnualForm == "" || filed.After(annualDate)) {
			record.LatestAnnualForm, annualDate = form, filed
		}
		if (form == "8-K" || form == "8-K/A") && len(s.Filings.Recent.Items) > 0 && containsItem201(s.Filings.Recent.Items[i]) {
			record.HasBusinessCombinationItem201 = true
			if !filed.IsZero() && (businessCombinationDate.IsZero() || filed.After(businessCombinationDate)) {
				businessCombinationDate = filed
			}
		}
	}
	if !businessCombinationDate.IsZero() {
		record.BusinessCombinationCompletedAt = &businessCombinationDate
	}
	return record, nil
}

func ParseSECCompanyFactsZIP(z *zip.Reader, allowed map[string]struct{}, limits ZIPParseLimits) ([]ShareFact, error) {
	if err := validateParseLimits(limits); err != nil {
		return nil, err
	}
	var out []ShareFact
	entriesByCIK := map[string][]ShareFact{}
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
		if err := checkDeclaredZIPSize(f, limits.MaxEntryBytes, limits.MaxTotalBytes-total); err != nil {
			return nil, err
		}
		var doc companyFactsDocument
		n, err := decodeZIPJSON(f, limits.MaxEntryBytes, &doc, true)
		if err != nil {
			return nil, fmt.Errorf("companyfacts CIK %s: %w", m[1], err)
		}
		if n > limits.MaxTotalBytes-total {
			return nil, fmt.Errorf("ZIP aggregate decoded bytes exceed limit")
		}
		total += n
		cik, err := normalizeCIK(doc.CIK)
		if err != nil || cik != m[1] {
			continue
		}
		var entryFacts []ShareFact
		entrySeen := map[string]ShareFact{}
		for _, spec := range []struct {
			ns, concept string
			data        companyFactsConcept
		}{{"dei", "EntityCommonStockSharesOutstanding", doc.Facts.DEI.EntityCommonStockSharesOutstanding}, {"us-gaap", "CommonStockSharesOutstanding", doc.Facts.USGAAP.CommonStockSharesOutstanding}} {
			concept := spec.data
			for unit, facts := range concept.Units {
				if unit != "shares" {
					return nil, fmt.Errorf("companyfacts %s/%s:%s: invalid unit %q", cik, spec.ns, spec.concept, unit)
				}
				for _, x := range facts {
					context := cik + "/" + spec.ns + ":" + spec.concept
					shares, err := parseShares(x.Val)
					if err != nil {
						continue
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
					if old, ok := entrySeen[key]; ok {
						if old != fact {
							return nil, fmt.Errorf("companyfacts %s conflicting duplicate fact", context)
						}
						continue
					}
					entrySeen[key] = fact
					entryFacts = append(entryFacts, fact)
				}
			}
		}
		sortShareFacts(entryFacts)
		if old, ok := entriesByCIK[cik]; ok {
			if !reflect.DeepEqual(old, entryFacts) {
				return nil, fmt.Errorf("companyfacts CIK %s conflicting duplicate archive entry", cik)
			}
			continue
		}
		entriesByCIK[cik] = entryFacts
		out = append(out, entryFacts...)
	}
	sortShareFacts(out)
	return out, nil
}

type companyFactsFact struct {
	Val                           json.Number `json:"val"`
	Start, End, Filed, Form, Accn string
}

type companyFactsConcept struct {
	Units map[string][]companyFactsFact `json:"units"`
}

type companyFactsDocument struct {
	CIK   json.RawMessage `json:"cik"`
	Facts struct {
		DEI struct {
			EntityCommonStockSharesOutstanding companyFactsConcept `json:"EntityCommonStockSharesOutstanding"`
		} `json:"dei"`
		USGAAP struct {
			CommonStockSharesOutstanding companyFactsConcept `json:"CommonStockSharesOutstanding"`
		} `json:"us-gaap"`
	} `json:"facts"`
}

// ParseSECCompanyFactsSharesJSON parses just the share-count concepts from an
// issuer's compact Company Facts endpoint.  It is the small, on-demand
// counterpart of ParseSECCompanyFactsZIP.
func ParseSECCompanyFactsSharesJSON(r io.Reader, expectedCIK string) ([]ShareFact, error) {
	dec := json.NewDecoder(r)
	dec.UseNumber()
	var doc companyFactsDocument
	if err := dec.Decode(&doc); err != nil {
		return nil, err
	}
	if err := requireJSONEOF(dec); err != nil {
		return nil, err
	}
	cik, err := normalizeCIK(doc.CIK)
	if err != nil || cik != expectedCIK {
		return nil, fmt.Errorf("companyfacts CIK %s does not match requested issuer", cik)
	}
	seen := map[string]ShareFact{}
	for _, spec := range []struct {
		ns, concept string
		data        companyFactsConcept
	}{{"dei", "EntityCommonStockSharesOutstanding", doc.Facts.DEI.EntityCommonStockSharesOutstanding}, {"us-gaap", "CommonStockSharesOutstanding", doc.Facts.USGAAP.CommonStockSharesOutstanding}} {
		for unit, facts := range spec.data.Units {
			if unit != "shares" {
				return nil, fmt.Errorf("companyfacts %s/%s:%s: invalid unit %q", cik, spec.ns, spec.concept, unit)
			}
			for _, item := range facts {
				shares, err := parseShares(item.Val)
				if err != nil {
					continue
				}
				instant, err := time.Parse("2006-01-02", item.End)
				if err != nil {
					return nil, fmt.Errorf("companyfacts %s: invalid end date", cik)
				}
				filed, err := time.Parse("2006-01-02", item.Filed)
				if err != nil {
					return nil, fmt.Errorf("companyfacts %s: invalid filed date", cik)
				}
				key := cik + "|" + spec.ns + ":" + spec.concept + "|" + item.End + "|" + item.Accn
				source := "https://data.sec.gov/api/xbrl/companyfacts/CIK" + cik + ".json"
				if item.Accn != "" {
					source = "https://www.sec.gov/Archives/edgar/data/" + strings.TrimLeft(cik, "0") + "/" + strings.ReplaceAll(item.Accn, "-", "") + "/"
				}
				fact := ShareFact{CIK: cik, Concept: spec.ns + ":" + spec.concept, Unit: "shares", Form: item.Form, Accession: item.Accn, Instant: instant, FiledAt: filed, Shares: shares, SourceURL: source}
				if old, ok := seen[key]; ok && old != fact {
					return nil, fmt.Errorf("companyfacts %s conflicting duplicate fact", cik)
				}
				seen[key] = fact
			}
		}
	}
	result := make([]ShareFact, 0, len(seen))
	for _, item := range seen {
		result = append(result, item)
	}
	sortShareFacts(result)
	return result, nil
}

func sortShareFacts(facts []ShareFact) {
	sort.Slice(facts, func(i, j int) bool {
		a, b := facts[i], facts[j]
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

type countingLimitReader struct {
	r         io.Reader
	remaining int64
	read      int64
	name      string
}

func (r *countingLimitReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if r.remaining == 0 {
		var probe [1]byte
		n, err := r.r.Read(probe[:])
		if n > 0 {
			return 0, fmt.Errorf("ZIP entry %q exceeds decoded byte limit", r.name)
		}
		return 0, err
	}
	if int64(len(p)) > r.remaining {
		p = p[:r.remaining]
	}
	n, err := r.r.Read(p)
	r.remaining -= int64(n)
	r.read += int64(n)
	return n, err
}

func decodeZIPJSON(f *zip.File, max int64, dst any, useNumber bool) (int64, error) {
	r, err := f.Open()
	if err != nil {
		return 0, err
	}
	defer r.Close()
	lr := &countingLimitReader{r: r, remaining: max, name: f.Name}
	dec := json.NewDecoder(lr)
	if useNumber {
		dec.UseNumber()
	}
	if err := dec.Decode(dst); err != nil {
		return lr.read, err
	}
	if err := requireJSONEOF(dec); err != nil {
		return lr.read, err
	}
	return lr.read, nil
}

func requireJSONEOF(dec *json.Decoder) error {
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("trailing JSON value")
		}
		return fmt.Errorf("trailing data: %w", err)
	}
	return nil
}

func checkDeclaredZIPSize(f *zip.File, entryLimit, remainingTotal int64) error {
	if f.UncompressedSize64 > uint64(entryLimit) {
		return fmt.Errorf("ZIP entry %q exceeds decoded byte limit", f.Name)
	}
	if f.UncompressedSize64 > uint64(remainingTotal) {
		return fmt.Errorf("ZIP aggregate decoded bytes exceed limit")
	}
	return nil
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
	a, err := s.Downloader.DownloadWithCacheTTL(ctx, s.TickerURL, "company_tickers_exchange.json", nil, s.CacheTTL)
	if err != nil {
		return nil, SourceVersion{}, err
	}
	b, err := s.Downloader.DownloadWithCacheTTL(ctx, s.SubmissionsURL, "submissions.zip", nil, s.CacheTTL)
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
			ticker, exchange, companyName, securityName := mappings[i].Ticker, mappings[i].Exchange, mappings[i].CompanyName, mappings[i].SecurityName
			mappings[i] = x
			mappings[i].Ticker = ticker
			mappings[i].Exchange = exchange
			if mappings[i].CompanyName == "" {
				mappings[i].CompanyName = companyName
			}
			if mappings[i].SecurityName == "" {
				mappings[i].SecurityName = securityName
			}
		}
	}
	return mappings, bulkVersion("sec-bulk", a, b), nil
}

// LoadTickerMappings loads SEC's compact ticker/CIK directory.  It is used by
// the daily discovery workflow to find newly listed issuers without pulling
// the multi-gigabyte SEC archives used by weekly calibration.
func (s SECBulkSource) LoadTickerMappings(ctx context.Context) ([]SecuritySourceRecord, SourceVersion, error) {
	if err := s.validateDownloader(); err != nil {
		return nil, SourceVersion{}, err
	}
	if strings.TrimSpace(s.TickerURL) == "" {
		return nil, SourceVersion{}, fmt.Errorf("SEC ticker URL is required")
	}
	download, err := s.Downloader.DownloadWithCacheTTL(ctx, s.TickerURL, "company_tickers_exchange.json", nil, s.CacheTTL)
	if err != nil {
		return nil, SourceVersion{}, err
	}
	file, err := os.Open(download.Path)
	if err != nil {
		return nil, SourceVersion{}, err
	}
	records, parseErr := ParseSECTickerExchange(file)
	closeErr := file.Close()
	if parseErr != nil {
		return nil, SourceVersion{}, parseErr
	}
	if closeErr != nil {
		return nil, SourceVersion{}, closeErr
	}
	return records, bulkVersion("sec-ticker-exchange", download), nil
}

// IncrementalIssuerData is the bounded SEC enrichment payload for newly
// discovered issuers.  Per-issuer failures are warnings: a directory entry is
// still useful for later retries and must not abort the whole daily run.
type IncrementalIssuerData struct {
	Records        []SecuritySourceRecord
	Shares         []ShareFact
	FinancialFacts []FinancialFact
	Version        SourceVersion
	Warnings       []string
}

// LoadIncrementalIssuerData fetches only the submissions and companyfacts JSON
// documents for the supplied issuers.  This is intentionally independent of
// submissions.zip/companyfacts.zip so a new IPO or listing can appear in the
// daily candidate universe before the next weekly archive calibration.
func (s SECBulkSource) LoadIncrementalIssuerData(ctx context.Context, records []SecuritySourceRecord) (IncrementalIssuerData, error) {
	if err := s.validateDownloader(); err != nil {
		return IncrementalIssuerData{}, err
	}
	result := IncrementalIssuerData{Records: make([]SecuritySourceRecord, 0, len(records))}
	rows := append([]SecuritySourceRecord(nil), records...)
	sort.Slice(rows, func(i, j int) bool { return canonicalLess(rows[i], rows[j]) })
	hash := sha256.New()
	// A common share and its attached warrants/rights legitimately share one
	// issuer CIK. Fetch issuer-scoped SEC JSON once, then apply the parsed
	// metadata to every listing record. Apart from reducing EDGAR traffic, this
	// keeps an identity-repair run from downloading the same companyfacts file
	// once for the common share and again for each attached security.
	issuerByCIK := make(map[string]SecuritySourceRecord, len(rows))
	for _, base := range rows {
		if !validCIK(base.CIK) {
			continue
		}
		cik := base.CIK
		if issuer, exists := issuerByCIK[cik]; exists {
			result.Records = append(result.Records, mergeIncrementalIssuerRecord(base, issuer))
			continue
		}
		merged := base
		issuerByCIK[cik] = merged
		submissionsURL := "https://data.sec.gov/submissions/CIK" + cik + ".json"
		// Daily listing discovery must remain cache-first. New issuer files are
		// immutable enough within the configured SEC cache window, and retrying
		// identity repairs should not re-fetch every issuer document again.
		submissionDownload, err := s.Downloader.DownloadWithCacheTTL(ctx, submissionsURL, "submissions-incremental-"+cik+".json", nil, s.CacheTTL)
		if err != nil {
			result.Warnings = append(result.Warnings, fmt.Sprintf("%s submissions: %v", base.Ticker, err))
			result.Records = append(result.Records, merged)
			continue
		}
		file, err := os.Open(submissionDownload.Path)
		if err != nil {
			return IncrementalIssuerData{}, err
		}
		submission, parseErr := ParseSECSubmissionJSON(file, cik)
		closeErr := file.Close()
		if parseErr != nil {
			result.Warnings = append(result.Warnings, fmt.Sprintf("%s submissions parse: %v", base.Ticker, parseErr))
		} else if closeErr != nil {
			return IncrementalIssuerData{}, closeErr
		} else {
			issuerByCIK[cik] = submission
			merged = mergeIncrementalIssuerRecord(base, submission)
			io.WriteString(hash, cik+" submissions "+submissionDownload.SHA256+"\n")
		}

		factsURL := "https://data.sec.gov/api/xbrl/companyfacts/CIK" + cik + ".json"
		factsDownload, err := s.Downloader.DownloadWithCacheTTL(ctx, factsURL, "companyfacts-incremental-"+cik+".json", nil, s.CacheTTL)
		if err != nil {
			result.Warnings = append(result.Warnings, fmt.Sprintf("%s companyfacts: %v", base.Ticker, err))
			result.Records = append(result.Records, merged)
			continue
		}
		file, err = os.Open(factsDownload.Path)
		if err != nil {
			return IncrementalIssuerData{}, err
		}
		financials, parseErr := ParseSECFinancialFactsJSON(file, cik)
		closeErr = file.Close()
		if parseErr != nil {
			result.Warnings = append(result.Warnings, fmt.Sprintf("%s financial facts parse: %v", base.Ticker, parseErr))
		} else if closeErr != nil {
			return IncrementalIssuerData{}, closeErr
		} else {
			result.FinancialFacts = append(result.FinancialFacts, financials...)
		}
		file, err = os.Open(factsDownload.Path)
		if err != nil {
			return IncrementalIssuerData{}, err
		}
		shares, shareErr := ParseSECCompanyFactsSharesJSON(file, cik)
		closeErr = file.Close()
		if shareErr != nil {
			result.Warnings = append(result.Warnings, fmt.Sprintf("%s share facts parse: %v", base.Ticker, shareErr))
		} else if closeErr != nil {
			return IncrementalIssuerData{}, closeErr
		} else {
			shares, enrichErr := EnrichShareFactsWithAcceptance(shares, merged.FilingMetadata)
			if enrichErr != nil {
				return IncrementalIssuerData{}, enrichErr
			}
			result.Shares = append(result.Shares, shares...)
		}
		io.WriteString(hash, cik+" companyfacts "+factsDownload.SHA256+"\n")
		result.Records = append(result.Records, merged)
	}
	normalized, err := normalizeMetadataRecords(result.Records)
	if err != nil {
		return IncrementalIssuerData{}, err
	}
	result.Records = normalized
	result.Shares, err = normalizeShareFacts(result.Shares)
	if err != nil {
		return IncrementalIssuerData{}, err
	}
	result.FinancialFacts, err = normalizeFinancialFacts(result.FinancialFacts)
	if err != nil {
		return IncrementalIssuerData{}, err
	}
	digest := hex.EncodeToString(hash.Sum(nil))
	if digest == hex.EncodeToString(sha256.New().Sum(nil)) {
		fallback, hashErr := hashCanonicalContent(result.Records)
		if hashErr != nil {
			return IncrementalIssuerData{}, hashErr
		}
		digest = fallback
	}
	result.Version = SourceVersion{Source: "sec-individual-new-listings", Version: digest + "+" + FinancialParserVersion, SHA256: digest, EffectiveAt: time.Now().UTC()}
	return result, nil
}

func mergeIncrementalIssuerRecord(base, sec SecuritySourceRecord) SecuritySourceRecord {
	result := sec
	result.SourceKey = base.SourceKey
	result.Ticker = base.Ticker
	result.ProviderTicker = base.ProviderTicker
	result.Exchange = base.Exchange
	result.SecurityName = base.SecurityName
	result.TestIssue = base.TestIssue
	result.ETF = base.ETF
	result.MappingStatus = base.MappingStatus
	result.EvidenceJSON = base.EvidenceJSON
	if result.CompanyName == "" {
		result.CompanyName = base.CompanyName
	}
	if result.SecurityName == "" {
		result.SecurityName = base.SecurityName
	}
	return result
}

// LoadLatestShares returns company facts enriched with SEC acceptance times.
// Facts absent from submissions recent retain a zero acceptance time so the
// point-in-time selector can fail closed only when such a fact is preferred.
func (s SECBulkSource) LoadLatestShares(ctx context.Context, allowed map[string]struct{}) ([]ShareFact, SourceVersion, error) {
	if err := s.validateDownloader(); err != nil {
		return nil, SourceVersion{}, err
	}
	if s.CompanyFactsURL == "" || s.SubmissionsURL == "" {
		return nil, SourceVersion{}, fmt.Errorf("SEC companyfacts and submissions URLs are required")
	}
	c, err := s.Downloader.DownloadWithCacheTTL(ctx, s.CompanyFactsURL, "companyfacts.zip", nil, s.CacheTTL)
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
	submissions, err := s.Downloader.DownloadWithCacheTTL(ctx, s.SubmissionsURL, "submissions.zip", nil, s.CacheTTL)
	if err != nil {
		return nil, SourceVersion{}, err
	}
	metadataZIP, err := OpenSafeZIP(submissions.Path, limitEntries(s.Limits), s.Limits.MaxTotalBytes)
	if err != nil {
		return nil, SourceVersion{}, err
	}
	records, err := ParseSECSubmissionsZIP(&metadataZIP.Reader, s.Limits)
	metadataZIP.Close()
	if err != nil {
		return nil, SourceVersion{}, err
	}
	metadata := make([]FilingMetadata, 0)
	for cik := range allowed {
		metadata = append(metadata, records[cik].FilingMetadata...)
	}
	facts, err = EnrichShareFactsWithAcceptance(facts, metadata)
	if err != nil {
		return nil, SourceVersion{}, err
	}
	return facts, bulkVersion("sec-companyfacts-submissions", c, submissions), nil
}

func (s SECBulkSource) LoadFinancialFacts(ctx context.Context, allowed map[string]struct{}) ([]FinancialFact, SourceVersion, error) {
	if err := s.validateDownloader(); err != nil {
		return nil, SourceVersion{}, err
	}
	if s.CompanyFactsURL == "" {
		return nil, SourceVersion{}, fmt.Errorf("SEC companyfacts URL is required")
	}
	c, err := s.Downloader.DownloadWithCacheTTL(ctx, s.CompanyFactsURL, "companyfacts.zip", nil, s.CacheTTL)
	if err != nil {
		return nil, SourceVersion{}, err
	}
	z, err := OpenSafeZIP(c.Path, limitEntries(s.Limits), s.Limits.MaxTotalBytes)
	if err != nil {
		return nil, SourceVersion{}, err
	}
	facts, err := ParseSECFinancialFactsZIP(&z.Reader, allowed, s.Limits)
	z.Close()
	if err != nil {
		return nil, SourceVersion{}, err
	}
	version := bulkVersion("sec-financialfacts", c)
	version.Version = version.Version + "+" + FinancialParserVersion
	return facts, version, nil
}

// LoadIncrementalFinancialFacts fetches only the affected issuers from the
// SEC Company Facts API.  It deliberately bypasses the archive cache: a dirty
// event represents a newly observed report and must not wait for the weekly
// full-calibration archive refresh.
func (s SECBulkSource) LoadIncrementalFinancialFacts(ctx context.Context, allowed map[string]struct{}) ([]FinancialFact, SourceVersion, error) {
	if err := s.validateDownloader(); err != nil {
		return nil, SourceVersion{}, err
	}
	ciks := make([]string, 0, len(allowed))
	for cik := range allowed {
		if validCIK(cik) {
			ciks = append(ciks, cik)
		}
	}
	sort.Strings(ciks)
	all := make([]FinancialFact, 0)
	h := sha256.New()
	for _, cik := range ciks {
		url := "https://data.sec.gov/api/xbrl/companyfacts/CIK" + cik + ".json"
		download, err := s.Downloader.Download(ctx, url, "companyfacts-incremental-"+cik+".json", nil)
		if err != nil {
			return nil, SourceVersion{}, fmt.Errorf("download companyfacts %s: %w", cik, err)
		}
		file, err := os.Open(download.Path)
		if err != nil {
			return nil, SourceVersion{}, err
		}
		facts, parseErr := ParseSECFinancialFactsJSON(file, cik)
		closeErr := file.Close()
		if parseErr != nil {
			return nil, SourceVersion{}, fmt.Errorf("parse companyfacts %s: %w", cik, parseErr)
		}
		if closeErr != nil {
			return nil, SourceVersion{}, closeErr
		}
		all = append(all, facts...)
		io.WriteString(h, cik+"\n"+download.SHA256+"\n")
	}
	sortFinancialFacts(all)
	digest := hex.EncodeToString(h.Sum(nil))
	return all, SourceVersion{Source: "sec-companyfacts-incremental", Version: digest + "+" + FinancialParserVersion, SHA256: digest, EffectiveAt: time.Now().UTC()}, nil
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
