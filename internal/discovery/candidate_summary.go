package discovery

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"gorm.io/gorm"
)

const defaultCandidateSummaryLimit = 5
const maxCandidateSummaryLimit = 20

type CandidateSummary struct {
	BatchID    string                   `json:"batch_id"`
	TotalA     int                      `json:"total_a"`
	TotalB     int                      `json:"total_b"`
	ItemsA     []CandidateScoreSnapshot `json:"items_a"`
	ItemsB     []CandidateScoreSnapshot `json:"items_b"`
	EventNotes map[string]string        `json:"event_notes"`
	Message    string                   `json:"message"`
}

type CandidateSummaryOptions struct {
	LimitPerGrade          int
	IncludeA               bool
	IncludeB               bool
	ActionableOnly         bool
	MinReviewPriorityScore int
}

func BuildCandidateSummary(ctx context.Context, db *gorm.DB, limitPerGrade int) (CandidateSummary, error) {
	return BuildCandidateSummaryWithOptions(ctx, db, CandidateSummaryOptions{LimitPerGrade: limitPerGrade, IncludeA: true, IncludeB: true})
}

func BuildCandidateSummaryWithOptions(ctx context.Context, db *gorm.DB, options CandidateSummaryOptions) (CandidateSummary, error) {
	result := CandidateSummary{ItemsA: []CandidateScoreSnapshot{}, ItemsB: []CandidateScoreSnapshot{}, EventNotes: map[string]string{}}
	if db == nil {
		return result, errors.New("database is required")
	}
	if ctx == nil {
		return result, errors.New("context is required")
	}
	limit := normalizeCandidateSummaryLimit(options.LimitPerGrade)
	batch, ok, err := currentPublishedPrescreenBatch(ctx, db)
	if err != nil {
		return result, err
	}
	if !ok {
		result.Message = "暂无小盘候选批次。"
		return result, nil
	}
	result.BatchID = batch.BatchID
	if options.IncludeA {
		if result.TotalA, result.ItemsA, err = listCandidateSummaryItems(ctx, db, batch.BatchID, CandidateGradeA, "eligible_a", limit, options); err != nil {
			return result, err
		}
	}
	if options.IncludeB {
		if result.TotalB, result.ItemsB, err = listCandidateSummaryItems(ctx, db, batch.BatchID, CandidateGradeB, "eligible_b", limit, options); err != nil {
			return result, err
		}
	}
	if err := hydrateCandidateSummaryEventNotes(ctx, db, &result); err != nil {
		return result, err
	}
	result.Message = renderCandidateSummaryMessage(result)
	return result, nil
}

func hydrateCandidateSummaryEventNotes(ctx context.Context, db *gorm.DB, summary *CandidateSummary) error {
	if summary == nil || summary.BatchID == "" || (len(summary.ItemsA) == 0 && len(summary.ItemsB) == 0) {
		return nil
	}
	page, err := ListCandidateScores(ctx, db, CandidateScoreQuery{Page: 1, PageSize: maxDiscoveryPageSize})
	if err != nil {
		return err
	}
	for _, item := range page.Items {
		if item.BatchID != summary.BatchID {
			continue
		}
		summary.EventNotes[item.Ticker] = candidateSummaryEventNote(item)
	}
	return nil
}

func candidateSummaryEventNote(item CandidateScoreResult) string {
	switch item.ChangeStatus {
	case "new":
		return "新增入选"
	case "improved":
		return "评分或等级改善"
	case "weakened":
		return "评分或风险转弱"
	default:
		return "持续跟踪"
	}
}

func normalizeCandidateSummaryLimit(limit int) int {
	if limit <= 0 {
		return defaultCandidateSummaryLimit
	}
	if limit > maxCandidateSummaryLimit {
		return maxCandidateSummaryLimit
	}
	return limit
}

func currentPublishedPrescreenBatch(ctx context.Context, db *gorm.DB) (UniverseBatch, bool, error) {
	var pointer CurrentBatchPointer
	err := db.WithContext(ctx).First(&pointer, "kind = ?", BatchKindPrescreen).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return UniverseBatch{}, false, nil
	}
	if err != nil {
		return UniverseBatch{}, false, err
	}
	var batch UniverseBatch
	err = db.WithContext(ctx).First(&batch, "batch_id = ? AND kind = ? AND status = ?", pointer.BatchID, BatchKindPrescreen, BatchStatusPublished).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return UniverseBatch{}, false, nil
	}
	if err != nil {
		return UniverseBatch{}, false, err
	}
	return batch, true, nil
}

func listCandidateSummaryItems(ctx context.Context, db *gorm.DB, batchID, grade, eligibilityColumn string, limit int, options CandidateSummaryOptions) (int, []CandidateScoreSnapshot, error) {
	items := []CandidateScoreSnapshot{}
	query := db.WithContext(ctx).Model(&CandidateScoreSnapshot{}).Where("batch_id = ? AND grade = ? AND "+eligibilityColumn+" = ?", batchID, grade, true)
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return 0, items, err
	}
	if options.ActionableOnly || options.MinReviewPriorityScore > 0 {
		page, err := ListCandidateScores(ctx, db, CandidateScoreQuery{Grade: grade, Page: 1, PageSize: maxDiscoveryPageSize})
		if err != nil {
			return 0, items, err
		}
		for _, item := range page.Items {
			if !candidateSummaryMatchesEligibility(item, grade) {
				continue
			}
			if options.ActionableOnly && grade == CandidateGradeB && !candidateSummaryActionableB(item) {
				continue
			}
			if options.MinReviewPriorityScore > 0 && item.ReviewPriorityScore < options.MinReviewPriorityScore {
				continue
			}
			items = append(items, item.CandidateScoreSnapshot)
			if len(items) >= limit {
				break
			}
		}
		return int(total), items, nil
	}
	err := query.Order("total_score DESC").Order("market_cap_usd ASC").Order("ticker ASC").Limit(limit).Find(&items).Error
	return int(total), items, err
}

func candidateSummaryMatchesEligibility(item CandidateScoreResult, grade string) bool {
	if grade == CandidateGradeA {
		return item.EligibleA
	}
	if grade == CandidateGradeB {
		return item.EligibleB
	}
	return false
}

func candidateSummaryActionableB(item CandidateScoreResult) bool {
	if item.QualityTier == "strong_b" {
		return true
	}
	return item.QualityTier == "standard_b" && (item.ChangeStatus == "new" || item.ChangeStatus == "improved")
}

func renderCandidateSummaryMessage(summary CandidateSummary) string {
	lines := []string{
		"小盘股研究候选摘要（仅研究与通知，不构成投资建议）",
		fmt.Sprintf("批次：%s", summary.BatchID),
		fmt.Sprintf("A级候选 %d 只，B级候选 %d 只。", summary.TotalA, summary.TotalB),
	}
	if summary.TotalA == 0 && summary.TotalB == 0 {
		lines = append(lines, "今日暂无 A/B 候选。")
		return strings.Join(lines, "\n")
	}
	if len(summary.ItemsA) > 0 {
		lines = append(lines, "", "A级候选：")
		for _, item := range summary.ItemsA {
			lines = append(lines, formatCandidateSummaryLine(item, summary.EventNotes[item.Ticker]))
		}
	}
	if len(summary.ItemsB) > 0 {
		lines = append(lines, "", "B级候选：")
		for _, item := range summary.ItemsB {
			lines = append(lines, formatCandidateSummaryLine(item, summary.EventNotes[item.Ticker]))
		}
	}
	return strings.Join(lines, "\n")
}

func formatCandidateSummaryLine(item CandidateScoreSnapshot, eventNote string) string {
	flags := []string{}
	if item.RecentQualifiedInsider {
		flags = append(flags, "Form 4增持")
	}
	if item.ActiveBlocksA || item.ActiveBlocksB {
		flags = append(flags, "存在融资/稀释风险")
	}
	if len(flags) == 0 {
		flags = append(flags, "无关键风险标记")
	}
	if eventNote != "" {
		flags = append(flags, eventNote)
	}
	return fmt.Sprintf("- %s｜%d分｜市值%s｜收入增长%s｜现金%s｜%s",
		item.Ticker,
		item.TotalScore,
		formatMarketCapUSD(item.MarketCapUSD),
		formatPercent(item.RevenueGrowthPct),
		formatMonths(item.CashRunwayMonths),
		strings.Join(flags, "，"),
	)
}

func formatMarketCapUSD(value int64) string {
	switch {
	case value >= 1_000_000_000:
		return fmt.Sprintf("$%.1fB", float64(value)/1_000_000_000)
	case value > 0:
		return fmt.Sprintf("$%.0fM", float64(value)/1_000_000)
	default:
		return "未知"
	}
}

func formatPercent(value float64) string {
	if value == 0 {
		return "未知"
	}
	return fmt.Sprintf("%.1f%%", value)
}

func formatMonths(value float64) string {
	if value == 0 {
		return "未知"
	}
	return fmt.Sprintf("%.1f个月", value)
}
