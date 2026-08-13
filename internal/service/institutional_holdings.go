package service

import (
	"archive/zip"
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"sec_monitor/internal/model"
)

const institutionalDatasetIndexURL = "https://www.sec.gov/data-research/sec-markets-data/form-13f-data-sets"

type InstitutionalHoldingsService struct {
	db     *gorm.DB
	client *http.Client
}

func NewInstitutionalHoldingsService(db *gorm.DB) *InstitutionalHoldingsService {
	return &InstitutionalHoldingsService{db: db, client: &http.Client{Timeout: 5 * time.Minute}}
}

type InstitutionalHoldingsSyncResult struct {
	DatasetURL     string `json:"dataset_url"`
	InvestorsSaved int    `json:"investors_saved"`
	HoldingsSaved  int    `json:"holdings_saved"`
	Skipped        bool   `json:"skipped"`
	Message        string `json:"message"`
}

func (s *InstitutionalHoldingsService) Sync(ctx context.Context) (InstitutionalHoldingsSyncResult, error) {
	var r InstitutionalHoldingsSyncResult
	if s == nil || s.db == nil {
		return r, errors.New("institutional holdings service is not configured")
	}
	url, err := s.latestDatasetURL(ctx)
	if err != nil {
		return r, err
	}
	r.DatasetURL = url
	var existing int64
	if err := s.db.WithContext(ctx).Model(&model.InstitutionalFiling{}).Where("dataset_url = ?", url).Count(&existing).Error; err != nil {
		return r, err
	}
	if existing > 0 {
		r.Skipped = true
		r.Message = "SEC 尚未发布新的 13F 数据集，保留现有逐标的持仓与披露记录。"
		return r, nil
	}
	resp, err := s.request(ctx, url)
	if err != nil {
		return r, err
	}
	defer resp.Body.Close()
	f, err := os.CreateTemp("", "sec-monitor-13f-*.zip")
	if err != nil {
		return r, err
	}
	name := f.Name()
	defer os.Remove(name)
	if _, err = io.Copy(f, io.LimitReader(resp.Body, 160<<20)); err != nil {
		f.Close()
		return r, err
	}
	f.Close()
	z, err := zip.OpenReader(name)
	if err != nil {
		return r, err
	}
	defer z.Close()
	rows := func(file string) ([][]string, error) {
		for _, x := range z.File {
			if x.Name == file {
				h, e := x.Open()
				if e != nil {
					return nil, e
				}
				defer h.Close()
				rd := csv.NewReader(h)
				rd.Comma = '\t'
				rd.FieldsPerRecord = -1
				return rd.ReadAll()
			}
		}
		return nil, fmt.Errorf("%s not found", file)
	}
	cover, err := rows("COVERPAGE.tsv")
	if err != nil {
		return r, err
	}
	summary, err := rows("SUMMARYPAGE.tsv")
	if err != nil {
		return r, err
	}
	submission, err := rows("SUBMISSION.tsv")
	if err != nil {
		return r, err
	}
	index := func(h []string) map[string]int {
		m := map[string]int{}
		for i, v := range h {
			m[v] = i
		}
		return m
	}
	value := func(row []string, m map[string]int, k string) string {
		i := m[k]
		if i < len(row) {
			return strings.TrimSpace(row[i])
		}
		return ""
	}
	ci, si, ui := index(cover[0]), index(summary[0]), index(submission[0])
	meta := map[string]model.InstitutionalFiling{}
	filing := map[string]string{}
	ciks := map[string]string{}
	for _, x := range submission[1:] {
		if value(x, ui, "SUBMISSIONTYPE") == "13F-HR" {
			a := value(x, ui, "ACCESSION_NUMBER")
			filing[a] = value(x, ui, "FILING_DATE")
			ciks[a] = value(x, ui, "CIK")
		}
	}
	for _, x := range cover[1:] {
		a := value(x, ci, "ACCESSION_NUMBER")
		if value(x, ci, "REPORTTYPE") != "13F HOLDINGS REPORT" || filing[a] == "" {
			continue
		}
		meta[a] = model.InstitutionalFiling{AccessionNumber: a, CIK: ciks[a], Firm: value(x, ci, "FILINGMANAGER_NAME"), ReportDate: value(x, ci, "REPORTCALENDARORQUARTER"), FilingDate: filing[a], DatasetURL: url, FetchedAt: time.Now().UTC()}
	}
	for _, x := range summary[1:] {
		a := value(x, si, "ACCESSION_NUMBER")
		v, _ := strconv.ParseInt(value(x, si, "TABLEVALUETOTAL"), 10, 64)
		if f, ok := meta[a]; ok {
			f.TotalHoldings, _ = strconv.Atoi(value(x, si, "TABLEENTRYTOTAL"))
			f.TotalValueUSD = v * 1000
			f.SourceURL = sec13FURL(f.CIK, a)
			meta[a] = f
		}
	}
	list := make([]model.InstitutionalFiling, 0, len(meta))
	for _, f := range meta {
		if f.TotalValueUSD > 0 {
			list = append(list, f)
		}
	}
	sort.Slice(list, func(i, j int) bool { return list[i].TotalValueUSD > list[j].TotalValueUSD })
	if len(list) > 50 {
		list = list[:50]
	}
	pick := map[string]model.InstitutionalFiling{}
	for _, f := range list {
		pick[f.AccessionNumber] = f
	}
	if err = s.db.WithContext(ctx).Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "accession_number"}}, DoUpdates: clause.AssignmentColumns([]string{"firm", "report_date", "filing_date", "total_holdings", "total_value_usd", "source_url", "dataset_url", "fetched_at", "updated_at"})}).Create(&list).Error; err != nil {
		return r, err
	}
	r.InvestorsSaved = len(list)
	info, err := rows("INFOTABLE.tsv")
	if err != nil {
		return r, err
	}
	ii := index(info[0])
	hs := []model.InstitutionalPortfolioHolding{}
	for _, x := range info[1:] {
		a := value(x, ii, "ACCESSION_NUMBER")
		f, ok := pick[a]
		if !ok {
			continue
		}
		val, _ := strconv.ParseInt(value(x, ii, "VALUE"), 10, 64)
		sh, _ := strconv.ParseInt(value(x, ii, "SSHPRNAMT"), 10, 64)
		w := float64(val*1000) / float64(f.TotalValueUSD) * 100
		hs = append(hs, model.InstitutionalPortfolioHolding{AccessionNumber: a, CUSIP: value(x, ii, "CUSIP"), Issuer: value(x, ii, "NAMEOFISSUER"), TitleOfClass: value(x, ii, "TITLEOFCLASS"), Shares: sh, ValueUSD: val * 1000, WeightPct: w})
	}
	if len(hs) > 0 {
		if err = s.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).CreateInBatches(&hs, 1000).Error; err != nil {
			return r, err
		}
	}
	r.HoldingsSaved = len(hs)
	r.Message = fmt.Sprintf("已同步 %d 家机构的最新 13F 持仓与披露公告", r.InvestorsSaved)
	return r, nil
}
func (s *InstitutionalHoldingsService) latestDatasetURL(ctx context.Context) (string, error) {
	r, e := s.request(ctx, institutionalDatasetIndexURL)
	if e != nil {
		return "", e
	}
	defer r.Body.Close()
	b, e := io.ReadAll(io.LimitReader(r.Body, 2<<20))
	if e != nil {
		return "", e
	}
	m := regexp.MustCompile(`https?[^"']+form13f\.zip|/files/structureddata[^"']+form13f\.zip`).FindString(string(b))
	if m == "" {
		return "", errors.New("latest SEC 13F dataset link not found")
	}
	if strings.HasPrefix(m, "/") {
		m = "https://www.sec.gov" + m
	}
	return strings.ReplaceAll(m, "&amp;", "&"), nil
}
func (s *InstitutionalHoldingsService) request(ctx context.Context, url string) (*http.Response, error) {
	r, e := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if e != nil {
		return nil, e
	}
	r.Header.Set("User-Agent", "sec-monitor/0.1 contact@example.com")
	x, e := s.client.Do(r)
	if e != nil {
		return nil, e
	}
	if x.StatusCode < 200 || x.StatusCode >= 300 {
		x.Body.Close()
		return nil, fmt.Errorf("SEC request failed: %s", x.Status)
	}
	return x, nil
}
func sec13FURL(cik, accession string) string {
	return "https://www.sec.gov/Archives/edgar/data/" + strings.TrimLeft(cik, "0") + "/" + strings.ReplaceAll(accession, "-", "") + "/"
}
