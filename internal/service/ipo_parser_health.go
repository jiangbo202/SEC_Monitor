package service

import (
	"context"
	"sec_monitor/internal/model"
	"strings"
)

type IPOParserIssue struct {
	Reason     string             `json:"reason"`
	Label      string             `json:"label"`
	Count      int                `json:"count"`
	IndexPages int                `json:"index_pages"`
	Examples   []IPOParserExample `json:"examples"`
}
type IPOParserExample struct {
	CompanyName string `json:"company_name"`
	FilingURL   string `json:"filing_url"`
}
type IPOMappingIssue struct {
	CIK         string `json:"cik"`
	CompanyName string `json:"company_name"`
	Reason      string `json:"reason"`
}

func (s *IPORadarService) annotateIPOParserIssues(ctx context.Context, health *IPORadarHealth) error {
	health.ParserIssues = []IPOParserIssue{}
	var rows []model.IPOOfferingEvent
	if err := s.db.WithContext(ctx).Select("company_name, filing_url, parse_message").Where("parse_status = ?", "unsupported").Order("filing_date DESC, id DESC").Find(&rows).Error; err != nil {
		return err
	}
	labels := map[string]string{"fetch_failed": "SEC 正文读取失败", "offer_price_not_found": "未识别最终每股发行价（不采用价格区间）", "shares_offered_not_found": "未识别发行股数（ADS / 单位等需单独口径）", "shares_offered_invalid": "发行股数无效", "offer_price_invalid": "发行价无效"}
	byReason := map[string]int{}
	for _, row := range rows {
		index, ok := byReason[row.ParseMessage]
		if !ok {
			index = len(health.ParserIssues)
			byReason[row.ParseMessage] = index
			label := labels[row.ParseMessage]
			if label == "" {
				label = "文档格式待人工核验"
			}
			health.ParserIssues = append(health.ParserIssues, IPOParserIssue{Reason: row.ParseMessage, Label: label, Examples: []IPOParserExample{}})
		}
		issue := &health.ParserIssues[index]
		issue.Count++
		if strings.Contains(row.FilingURL, "-index.htm") {
			issue.IndexPages++
		}
		if len(issue.Examples) < 3 {
			issue.Examples = append(issue.Examples, IPOParserExample{CompanyName: row.CompanyName, FilingURL: row.FilingURL})
		}
	}
	return nil
}
