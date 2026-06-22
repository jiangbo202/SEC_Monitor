package discovery

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"reflect"
	"sort"
	"strings"
)

type SecuritySourceRecord struct {
	CIK                           string
	Ticker                        string
	CompanyName                   string
	Exchange                      string
	SecurityName                  string
	TestIssue                     bool
	ETF                           bool
	SIC                           int
	StateOfIncorporation          string
	LatestAnnualForm              string
	RecentForms                   []string
	HasBusinessCombinationItem201 bool
}

type SecurityMetadataSource interface {
	Load(context.Context) ([]SecuritySourceRecord, SourceVersion, error)
}

type NasdaqDirectorySource struct {
	Downloader *Downloader
	ListedURL  string
	OtherURL   string
}

var listedHeader = []string{"Symbol", "Security Name", "Market Category", "Test Issue", "Financial Status", "Round Lot Size", "ETF", "NextShares"}
var otherHeader = []string{"ACT Symbol", "Security Name", "Exchange", "CQS Symbol", "ETF", "Round Lot Size", "Test Issue", "NASDAQ Symbol"}

func ParseNasdaqListed(r io.Reader) ([]SecuritySourceRecord, string, error) {
	return parseNasdaq(r, listedHeader, func(fields []string, line int) (SecuritySourceRecord, error) {
		test, err := parseYN(fields[3])
		if err != nil {
			return SecuritySourceRecord{}, fmt.Errorf("line %d: Test Issue: %w", line, err)
		}
		etf, err := parseYN(fields[6])
		if err != nil {
			return SecuritySourceRecord{}, fmt.Errorf("line %d: ETF: %w", line, err)
		}
		return SecuritySourceRecord{Ticker: strings.ToUpper(fields[0]), SecurityName: fields[1], CompanyName: fields[1], Exchange: "Nasdaq", TestIssue: test, ETF: etf}, nil
	})
}

func ParseNasdaqOther(r io.Reader) ([]SecuritySourceRecord, string, error) {
	return parseNasdaq(r, otherHeader, func(fields []string, line int) (SecuritySourceRecord, error) {
		exchanges := map[string]string{"N": "NYSE", "A": "NYSE American", "P": "NYSE Arca", "Z": "Cboe BZX", "V": "IEX"}
		exchange, ok := exchanges[fields[2]]
		if !ok {
			return SecuritySourceRecord{}, fmt.Errorf("line %d: unknown exchange code %q", line, fields[2])
		}
		etf, err := parseYN(fields[4])
		if err != nil {
			return SecuritySourceRecord{}, fmt.Errorf("line %d: ETF: %w", line, err)
		}
		test, err := parseYN(fields[6])
		if err != nil {
			return SecuritySourceRecord{}, fmt.Errorf("line %d: Test Issue: %w", line, err)
		}
		return SecuritySourceRecord{Ticker: strings.ToUpper(fields[0]), SecurityName: fields[1], CompanyName: fields[1], Exchange: exchange, TestIssue: test, ETF: etf}, nil
	})
}

func parseNasdaq(r io.Reader, expected []string, makeRecord func([]string, int) (SecuritySourceRecord, error)) ([]SecuritySourceRecord, string, error) {
	s := bufio.NewScanner(r)
	s.Buffer(make([]byte, 4096), 1024*1024)
	line := 0
	headerSeen := false
	footer := ""
	byTicker := make(map[string]SecuritySourceRecord)
	for s.Scan() {
		line++
		raw := strings.TrimSuffix(s.Text(), "\r")
		text := strings.TrimSpace(raw)
		if text == "" {
			continue
		}
		if !headerSeen {
			if !reflect.DeepEqual(strings.Split(raw, "|"), expected) {
				return nil, "", fmt.Errorf("line %d: invalid Nasdaq header", line)
			}
			headerSeen = true
			continue
		}
		if strings.HasPrefix(text, "File Creation Time:") {
			if strings.Contains(text, "|") {
				return nil, "", fmt.Errorf("line %d: malformed File Creation Time footer", line)
			}
			if footer != "" {
				return nil, "", fmt.Errorf("duplicate footer at line %d", line)
			}
			footer = strings.TrimSpace(strings.TrimPrefix(text, "File Creation Time:"))
			if footer == "" {
				return nil, "", fmt.Errorf("empty footer at line %d", line)
			}
			continue
		}
		if footer != "" {
			return nil, "", fmt.Errorf("line %d appears after footer", line)
		}
		fields := splitTrim(text)
		if len(fields) != len(expected) {
			return nil, "", fmt.Errorf("line %d: expected %d fields, got %d", line, len(expected), len(fields))
		}
		record, err := makeRecord(fields, line)
		if err != nil {
			return nil, "", err
		}
		if old, ok := byTicker[record.Ticker]; ok {
			if !reflect.DeepEqual(old, record) {
				return nil, "", fmt.Errorf("line %d: conflicting duplicate ticker %q", line, record.Ticker)
			}
			continue
		}
		byTicker[record.Ticker] = record
	}
	if err := s.Err(); err != nil {
		return nil, "", fmt.Errorf("read Nasdaq file: %w", err)
	}
	if !headerSeen {
		return nil, "", fmt.Errorf("missing header")
	}
	if footer == "" {
		return nil, "", fmt.Errorf("missing File Creation Time footer")
	}
	result := make([]SecuritySourceRecord, 0, len(byTicker))
	for _, r := range byTicker {
		result = append(result, r)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Ticker < result[j].Ticker })
	return result, footer, nil
}

func splitTrim(s string) []string {
	p := strings.Split(s, "|")
	for i := range p {
		p[i] = strings.TrimSpace(p[i])
	}
	return p
}
func parseYN(s string) (bool, error) {
	switch s {
	case "Y":
		return true, nil
	case "N":
		return false, nil
	default:
		return false, fmt.Errorf("expected Y or N")
	}
}

func (s NasdaqDirectorySource) Load(ctx context.Context) ([]SecuritySourceRecord, SourceVersion, error) {
	if s.Downloader == nil {
		return nil, SourceVersion{}, fmt.Errorf("Nasdaq downloader is required")
	}
	if s.ListedURL == "" || s.OtherURL == "" {
		return nil, SourceVersion{}, fmt.Errorf("Nasdaq source URLs are required")
	}
	a, err := s.Downloader.Download(ctx, s.ListedURL, "nasdaqlisted.txt", nil)
	if err != nil {
		return nil, SourceVersion{}, err
	}
	b, err := s.Downloader.Download(ctx, s.OtherURL, "otherlisted.txt", nil)
	if err != nil {
		return nil, SourceVersion{}, err
	}
	fa, err := os.Open(a.Path)
	if err != nil {
		return nil, SourceVersion{}, err
	}
	ra, va, err := ParseNasdaqListed(fa)
	fa.Close()
	if err != nil {
		return nil, SourceVersion{}, err
	}
	fb, err := os.Open(b.Path)
	if err != nil {
		return nil, SourceVersion{}, err
	}
	rb, vb, err := ParseNasdaqOther(fb)
	fb.Close()
	if err != nil {
		return nil, SourceVersion{}, err
	}
	records, err := mergeNasdaqRecords(ra, rb)
	if err != nil {
		return nil, SourceVersion{}, err
	}
	sort.Slice(records, func(i, j int) bool {
		if records[i].Ticker == records[j].Ticker {
			return records[i].Exchange < records[j].Exchange
		}
		return records[i].Ticker < records[j].Ticker
	})
	h := sha256.Sum256([]byte(a.SHA256 + "\n" + b.SHA256))
	return records, SourceVersion{Source: "nasdaq-directory", Version: va + "+" + vb, SHA256: hex.EncodeToString(h[:])}, nil
}

func mergeNasdaqRecords(feeds ...[]SecuritySourceRecord) ([]SecuritySourceRecord, error) {
	byTicker := make(map[string]SecuritySourceRecord)
	for _, feed := range feeds {
		for _, record := range feed {
			if old, ok := byTicker[record.Ticker]; ok {
				if !reflect.DeepEqual(old, record) {
					return nil, fmt.Errorf("conflicting duplicate ticker %q across Nasdaq feeds", record.Ticker)
				}
				continue
			}
			byTicker[record.Ticker] = record
		}
	}
	result := make([]SecuritySourceRecord, 0, len(byTicker))
	for _, record := range byTicker {
		result = append(result, record)
	}
	return result, nil
}
