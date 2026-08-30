package service

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"sec_monitor/internal/discovery"
	"sec_monitor/internal/model"
)

var ErrThesisConflict = errors.New("研究观点已有新版本，请重新加载后合并修改")
var thesisTickerPattern = regexp.MustCompile(`^[A-Z0-9][A-Z0-9.\-]{0,31}$`)

type ResearchThesisService struct{ db, research *gorm.DB }

func NewResearchThesisService(db, research *gorm.DB) *ResearchThesisService {
	return &ResearchThesisService{db: db, research: research}
}

type ThesisWrite struct {
	Version      uint                   `json:"version"`
	Status       string                 `json:"status"`
	Rationale    string                 `json:"rationale"`
	Invalidation string                 `json:"invalidation"`
	NextCheck    string                 `json:"next_check"`
	NextReviewAt *time.Time             `json:"next_review_at"`
	ReviewNote   string                 `json:"review_note"`
	Evidence     []model.ThesisEvidence `json:"evidence"`
}
type ThesisDetail struct {
	Thesis    *model.ResearchThesis          `json:"thesis"`
	Revisions []model.ResearchThesisRevision `json:"revisions"`
	ReviewDue bool                           `json:"review_due"`
}

func thesisTicker(ticker string) (string, error) {
	ticker = strings.ToUpper(strings.TrimSpace(ticker))
	if !thesisTickerPattern.MatchString(ticker) {
		return "", fmt.Errorf("%w: 标的代码无效", ErrValidation)
	}
	return ticker, nil
}
func (s *ResearchThesisService) Get(ctx context.Context, ticker string) (ThesisDetail, error) {
	ticker, err := thesisTicker(ticker)
	if err != nil {
		return ThesisDetail{}, err
	}
	out := ThesisDetail{Revisions: []model.ResearchThesisRevision{}}
	var row model.ResearchThesis
	err = s.db.WithContext(ctx).First(&row, "ticker = ?", ticker).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	out.Thesis = &row
	out.ReviewDue = row.Status == "active" && row.NextReviewAt != nil && !row.NextReviewAt.After(time.Now())
	err = s.db.WithContext(ctx).Where("ticker = ?", ticker).Order("version DESC").Limit(50).Find(&out.Revisions).Error
	return out, err
}
func (s *ResearchThesisService) Due(ctx context.Context) ([]model.ResearchThesis, error) {
	rows := []model.ResearchThesis{}
	err := thesisTimeFilter(s.db.WithContext(ctx).Where("status = ?", "active"), "next_review_at", "<=", time.Now().UTC()).Order("next_review_at ASC, ticker ASC").Limit(100).Find(&rows).Error
	return rows, err
}

func (s *ResearchThesisService) Save(ctx context.Context, ticker string, in ThesisWrite) (model.ResearchThesis, error) {
	ticker, err := thesisTicker(ticker)
	if err != nil {
		return model.ResearchThesis{}, err
	}
	in.Rationale = strings.TrimSpace(in.Rationale)
	in.Invalidation = strings.TrimSpace(in.Invalidation)
	in.NextCheck = strings.TrimSpace(in.NextCheck)
	in.ReviewNote = strings.TrimSpace(in.ReviewNote)
	if in.Status != "draft" && in.Status != "active" && in.Status != "invalidated" && in.Status != "archived" {
		return model.ResearchThesis{}, fmt.Errorf("%w: 观点状态无效", ErrValidation)
	}
	if len(in.Rationale) > 10000 || len(in.Invalidation) > 10000 || len(in.NextCheck) > 10000 || len(in.ReviewNote) > 10000 || len(in.Evidence) > 30 {
		return model.ResearchThesis{}, fmt.Errorf("%w: 内容过长或证据超过 30 条", ErrValidation)
	}
	if in.Status != "draft" && (in.Rationale == "" || in.ReviewNote == "") {
		return model.ResearchThesis{}, fmt.Errorf("%w: 请填写观点及本次复核结论", ErrValidation)
	}
	if in.Status == "active" && (in.Invalidation == "" || in.NextCheck == "" || in.NextReviewAt == nil || len(in.Evidence) == 0) {
		return model.ResearchThesis{}, fmt.Errorf("%w: 跟踪中观点需要证据、失效条件和下次验证事项/时间", ErrValidation)
	}
	if in.Status == "active" && !in.NextReviewAt.After(time.Now()) {
		return model.ResearchThesis{}, fmt.Errorf("%w: 下次复核时间应晚于当前时间", ErrValidation)
	}
	var saved model.ResearchThesis
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var before model.ResearchThesis
		err := tx.First(&before, "ticker = ?", ticker).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if before.Version != in.Version {
			return ErrThesisConflict
		}
		// Closed conclusions cannot silently become active again; create an explicit
		// draft revision first, leaving the previous outcome in the immutable trail.
		if (before.Status == "invalidated" || before.Status == "archived") && in.Status == "active" {
			return fmt.Errorf("%w: 已结束观点请先修订为草稿，再重新跟踪", ErrValidation)
		}
		evidence := []model.ThesisEvidence{}
		seen := map[string]bool{}
		for _, ref := range in.Evidence {
			key := fmt.Sprintf("%s:%d", ref.Kind, ref.ID)
			if seen[key] {
				continue
			}
			seen[key] = true
			var item model.ThesisEvidence
			found := false
			for _, old := range before.Evidence {
				if old.Kind == ref.Kind && old.ID == ref.ID {
					item = old
					found = true
					break
				}
			}
			if !found {
				item, err = s.resolveEvidence(ctx, tx, ticker, ref.Kind, ref.ID)
				if err != nil {
					return err
				}
			}
			evidence = append(evidence, item)
		}
		now := time.Now().UTC()
		saved = model.ResearchThesis{Ticker: ticker, Version: before.Version + 1, Status: in.Status, Rationale: in.Rationale, Invalidation: in.Invalidation, NextCheck: in.NextCheck, NextReviewAt: in.NextReviewAt, ReviewNote: in.ReviewNote, Evidence: evidence, ReviewedAt: before.ReviewedAt, CreatedAt: before.CreatedAt, UpdatedAt: now}
		if in.Status != "draft" {
			saved.ReviewedAt = &now
		}
		if saved.Status == "invalidated" || saved.Status == "archived" {
			saved.NextReviewAt = nil
		}
		if before.Version == 0 {
			saved.CreatedAt = now
			res := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&saved)
			if res.Error != nil {
				return res.Error
			}
			if res.RowsAffected != 1 {
				return ErrThesisConflict
			}
		} else {
			res := tx.Model(&model.ResearchThesis{}).Where("ticker = ? AND version = ?", ticker, in.Version).Select("*").Updates(&saved)
			if res.Error != nil {
				return res.Error
			}
			if res.RowsAffected != 1 {
				return ErrThesisConflict
			}
		}
		if err := tx.Create(&model.ResearchThesisRevision{Ticker: ticker, Version: saved.Version, Snapshot: saved, CreatedAt: now}).Error; err != nil {
			return err
		}
		return NewAuditService(tx).Record(ctx, "web", "research_thesis.revise", "research_thesis", ticker, before, saved)
	})
	return saved, err
}

// Evidence is resolved server-side and bound to the symbol. Client labels,
// summaries and URLs are never trusted. No provider is called by this service.
func (s *ResearchThesisService) resolveEvidence(ctx context.Context, db *gorm.DB, ticker, kind string, id uint) (model.ThesisEvidence, error) {
	out := model.ThesisEvidence{Kind: kind, ID: id}
	var err error
	if id == 0 {
		return out, fmt.Errorf("%w: 证据 ID 无效", ErrValidation)
	}
	switch kind {
	case "filing":
		var row model.Filing
		err = db.WithContext(ctx).First(&row, "id = ? AND ticker = ?", id, ticker).Error
		out.Label = "SEC " + row.FilingType
		out.URL = row.FilingURL
		out.Summary = row.Title
		out.RecordedAt = row.CreatedAt
	case "ai":
		var row model.AIAnalysis
		err = db.WithContext(ctx).First(&row, "id = ? AND ticker = ? AND status = ?", id, ticker, "success").Error
		out.Label = "AI 研判（非原始事实） · " + row.ProviderName
		out.URL = "/ai-analyses?ticker=" + url.QueryEscape(ticker)
		out.RecordedAt = row.CreatedAt
		var result model.AIAnalysisStructuredResult
		_ = json.Unmarshal([]byte(row.ResultJSON), &result)
		out.Summary = result.Conclusion
	case "notification":
		var row model.InAppNotification
		err = db.WithContext(ctx).First(&row, "id = ? AND ticker = ?", id, ticker).Error
		out.Label = "站内通知 · " + row.Title
		out.Summary = row.Body
		out.URL = row.Link
		out.RecordedAt = row.CreatedAt
	case "insider":
		if s.research == nil {
			return out, fmt.Errorf("%w: 研究数据库不可用", ErrValidation)
		}
		var row discovery.InsiderTransactionSnapshot
		err = s.insiderEvidenceQuery(ctx, ticker).First(&row, "insider_transaction_snapshots.id = ?", id).Error
		out.Label = "Form 4 · " + row.OwnerName
		out.URL = row.SourceURL
		out.RecordedAt = row.CreatedAt
		out.Summary = fmt.Sprintf("%s · %s · 股数 %.6f · 10b5-1 %s", row.TransactionDate.Format("2006-01-02"), row.TransactionCode, float64(row.SharesMicros)/1e6, row.TenB5OneStatus)
	default:
		return out, fmt.Errorf("%w: 不支持的证据类型", ErrValidation)
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return out, fmt.Errorf("%w: 证据不存在或不属于该标的", ErrValidation)
	}
	if err != nil {
		return out, err
	}
	parsed, e := url.Parse(out.URL)
	if e != nil || parsed.User != nil || (parsed.Scheme != "https" && parsed.Scheme != "http" && !(parsed.Scheme == "" && strings.HasPrefix(out.URL, "/") && !strings.HasPrefix(out.URL, "//"))) {
		out.URL = ""
	}
	data, _ := json.Marshal(out)
	out.SHA256 = fmt.Sprintf("%x", sha256.Sum256(data))
	return out, nil
}
func (s *ResearchThesisService) insiderEvidenceQuery(ctx context.Context, ticker string) *gorm.DB {
	// Some monitored issuers only have a published batch identity, not a mutable
	// listing. Prefer an active listing; otherwise use the latest batch identity
	// for that security, never every historical ticker (symbols can be reused).
	return s.research.WithContext(ctx).Model(&discovery.InsiderTransactionSnapshot{}).Where(`security_id IN (
		SELECT security_id FROM listings WHERE ticker = ? AND valid_to IS NULL
		UNION
		SELECT b.security_id FROM security_batch_identities b WHERE b.ticker = ?
		AND NOT EXISTS (SELECT 1 FROM listings l WHERE l.security_id = b.security_id AND l.valid_to IS NULL)
		AND b.id = (SELECT latest.id FROM security_batch_identities latest WHERE latest.security_id = b.security_id ORDER BY latest.created_at DESC, latest.id DESC LIMIT 1)
	)`, ticker, ticker)
}

type ThesisSources struct {
	Items       []model.ThesisEvidence `json:"items"`
	NewCount    int64                  `json:"new_count"`
	Unavailable []string               `json:"unavailable"`
}

func (s *ResearchThesisService) Sources(ctx context.Context, ticker string) (ThesisSources, error) {
	out := ThesisSources{Items: []model.ThesisEvidence{}, Unavailable: []string{}}
	ticker, err := thesisTicker(ticker)
	if err != nil {
		return out, err
	}
	detail, err := s.Get(ctx, ticker)
	if err != nil {
		return out, err
	}
	queries := []struct {
		kind string
		q    *gorm.DB
	}{
		{"filing", s.db.WithContext(ctx).Model(&model.Filing{}).Where("ticker = ?", ticker)},
		{"ai", s.db.WithContext(ctx).Model(&model.AIAnalysis{}).Where("ticker = ? AND status = ?", ticker, "success")},
		{"notification", s.db.WithContext(ctx).Model(&model.InAppNotification{}).Where("ticker = ?", ticker)},
	}
	if s.research != nil {
		queries = append(queries, struct {
			kind string
			q    *gorm.DB
		}{"insider", s.insiderEvidenceQuery(ctx, ticker)})
	} else {
		out.Unavailable = append(out.Unavailable, "insider")
	}
	for _, source := range queries {
		var ids []uint
		if err := source.q.Session(&gorm.Session{}).Order("created_at DESC, id DESC").Limit(20).Pluck("id", &ids).Error; err != nil {
			out.Unavailable = append(out.Unavailable, source.kind)
			continue
		}
		for _, id := range ids {
			item, err := s.resolveEvidence(ctx, s.db, ticker, source.kind, id)
			if err != nil {
				out.Unavailable = append(out.Unavailable, source.kind)
				break
			}
			out.Items = append(out.Items, item)
		}
		if detail.Thesis != nil && detail.Thesis.Status == "active" && detail.Thesis.ReviewedAt != nil {
			var count int64
			if err := thesisTimeFilter(source.q.Session(&gorm.Session{}), "created_at", ">", *detail.Thesis.ReviewedAt).Count(&count).Error; err != nil {
				out.Unavailable = append(out.Unavailable, source.kind)
			} else {
				out.NewCount += count
			}
		}
	}
	sort.SliceStable(out.Items, func(i, j int) bool { return out.Items[i].RecordedAt.After(out.Items[j].RecordedAt) })
	return out, nil
}

// SQLite stores offsets in datetime text; lexical comparisons mix UTC and local
// time incorrectly. Column/operator are internal constants, never user input.
func thesisTimeFilter(db *gorm.DB, column, operator string, at time.Time) *gorm.DB {
	if db.Dialector.Name() == "sqlite" {
		return db.Where("julianday("+column+") "+operator+" julianday(?)", at.UTC().Format(time.RFC3339Nano))
	}
	return db.Where(column+" "+operator+" ?", at.UTC())
}
