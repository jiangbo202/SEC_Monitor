package discovery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const CandidateReportSchemaV1 = "candidate-report-v1"

// CandidateReportSnapshot is the immutable research artifact for one
// published market batch. Notification delivery deliberately lives elsewhere:
// a failed Telegram attempt cannot delete or rewrite this report.
type CandidateReportSnapshot struct {
	ID            uint      `json:"id"`
	BatchID       string    `json:"batch_id" gorm:"size:64;uniqueIndex"`
	EffectiveDate string    `json:"effective_date" gorm:"size:10;index"`
	SchemaVersion string    `json:"schema_version" gorm:"size:32"`
	ContentSHA256 string    `json:"content_sha256" gorm:"size:64;index"`
	ReportJSON    string    `json:"-" gorm:"type:text"`
	GeneratedAt   time.Time `json:"generated_at"`
	CreatedAt     time.Time `json:"created_at"`
}

type CandidateReport struct {
	Status        string           `json:"status"`
	Available     bool             `json:"available"`
	Message       string           `json:"message,omitempty"`
	Date          string           `json:"date"`
	Batch         UniverseBatch    `json:"batch"`
	Summary       CandidateSummary `json:"summary"`
	Health        CandidateHealth  `json:"health"`
	SnapshotID    uint             `json:"snapshot_id,omitempty"`
	SchemaVersion string           `json:"schema_version,omitempty"`
	ContentSHA256 string           `json:"content_sha256,omitempty"`
	Generated     time.Time        `json:"generated_at"`
}

func BuildCandidateReport(ctx context.Context, db *gorm.DB, date string) (CandidateReport, error) {
	result := CandidateReport{Status: "ready", Date: strings.TrimSpace(date), Generated: time.Now().UTC()}
	if db == nil {
		return result, errors.New("database is required")
	}
	if result.Date == "" {
		result.Date = time.Now().UTC().Format("2006-01-02")
	}
	batch, ok, err := candidateReportBatch(ctx, db, result.Date)
	if err != nil {
		return result, err
	}
	if !ok {
		// A fresh installation has no published prescreen batch yet. This is a
		// normal bootstrap state, not an API failure. Return health evidence so
		// the UI can explain what must run first.
		result.Status = "empty"
		result.Message = "尚无已发布的小盘股候选批次；请先完成全量校准"
		result.Summary, err = BuildCandidateSummary(ctx, db, 10)
		if err != nil {
			return result, err
		}
		result.Health, err = BuildCandidateHealth(ctx, db)
		return result, err
	}
	return BuildAndPersistCandidateReportForBatch(ctx, db, batch)
}

// BuildAndPersistCandidateReport generates (or returns) the immutable daily
// research artifact for an exact published batch. It performs no notification
// delivery and is safe to retry.
func BuildAndPersistCandidateReport(ctx context.Context, db *gorm.DB, batchID string) (CandidateReport, error) {
	result := CandidateReport{Status: "ready", Generated: time.Now().UTC()}
	if db == nil {
		return result, errors.New("database is required")
	}
	if ctx == nil {
		return result, errors.New("context is required")
	}
	batchID = strings.TrimSpace(batchID)
	if batchID == "" {
		return result, errors.New("batch id is required")
	}
	var batch UniverseBatch
	err := db.WithContext(ctx).First(&batch, "batch_id = ? AND kind = ? AND status = ?", batchID, BatchKindPrescreen, BatchStatusPublished).Error
	if err != nil {
		return result, err
	}
	return BuildAndPersistCandidateReportForBatch(ctx, db, batch)
}

// BuildAndPersistCandidateReportForBatch is the orchestration-friendly form:
// coordinators can pass the published batch they just produced without a
// second lookup. The snapshot itself remains uniquely keyed by batch ID.
func BuildAndPersistCandidateReportForBatch(ctx context.Context, db *gorm.DB, batch UniverseBatch) (CandidateReport, error) {
	if db == nil {
		return CandidateReport{}, errors.New("database is required")
	}
	if ctx == nil {
		return CandidateReport{}, errors.New("context is required")
	}
	if strings.TrimSpace(batch.BatchID) == "" {
		return CandidateReport{}, errors.New("batch id is required")
	}
	if archived, ok, err := loadCandidateReportSnapshot(ctx, db, batch.BatchID); err != nil {
		return CandidateReport{}, err
	} else if ok {
		return archived, nil
	}

	generatedAt := time.Now().UTC()
	date := strings.TrimSpace(batch.EffectiveDate)
	if date == "" {
		date = batch.StartedAt.UTC().Format("2006-01-02")
	}
	report := CandidateReport{
		Status: "ready", Available: true, Date: date, Batch: batch,
		SchemaVersion: CandidateReportSchemaV1, Generated: generatedAt,
	}
	var err error
	report.Summary, err = BuildCandidateSummaryForBatch(ctx, db, batch, CandidateSummaryOptions{LimitPerGrade: 10, IncludeA: true, IncludeB: true})
	if err != nil {
		return report, err
	}
	report.Health, err = BuildCandidateHealthForBatch(ctx, db, batch)
	if err != nil {
		return report, err
	}
	payload, err := json.Marshal(report)
	if err != nil {
		return report, fmt.Errorf("marshal candidate report: %w", err)
	}
	digest := sha256.Sum256(payload)
	snapshot := CandidateReportSnapshot{
		BatchID: batch.BatchID, EffectiveDate: date, SchemaVersion: CandidateReportSchemaV1,
		ContentSHA256: hex.EncodeToString(digest[:]), ReportJSON: string(payload), GeneratedAt: generatedAt,
	}
	if err := db.WithContext(ctx).Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "batch_id"}}, DoNothing: true}).Create(&snapshot).Error; err != nil {
		return report, fmt.Errorf("persist candidate report snapshot: %w", err)
	}
	// A concurrent retry may have won the unique-key race. Always reload the
	// authoritative row so every caller observes the same immutable artifact.
	archived, ok, err := loadCandidateReportSnapshot(ctx, db, batch.BatchID)
	if err != nil {
		return report, err
	}
	if !ok {
		return report, errors.New("candidate report snapshot was not persisted")
	}
	return archived, nil
}

func loadCandidateReportSnapshot(ctx context.Context, db *gorm.DB, batchID string) (CandidateReport, bool, error) {
	var snapshot CandidateReportSnapshot
	err := db.WithContext(ctx).First(&snapshot, "batch_id = ?", batchID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return CandidateReport{}, false, nil
	}
	if err != nil {
		return CandidateReport{}, false, err
	}
	var report CandidateReport
	if err := json.Unmarshal([]byte(snapshot.ReportJSON), &report); err != nil {
		return CandidateReport{}, false, fmt.Errorf("decode candidate report snapshot %d: %w", snapshot.ID, err)
	}
	report.SnapshotID = snapshot.ID
	report.SchemaVersion = snapshot.SchemaVersion
	report.ContentSHA256 = snapshot.ContentSHA256
	report.Generated = snapshot.GeneratedAt
	return report, true, nil
}

func candidateReportBatch(ctx context.Context, db *gorm.DB, date string) (UniverseBatch, bool, error) {
	var batch UniverseBatch
	query := db.WithContext(ctx).Where("kind = ? AND status = ?", BatchKindPrescreen, BatchStatusPublished)
	if date != "" {
		query = query.Where("effective_date = ? OR date(started_at) = ?", date, date)
	}
	err := query.Order("started_at DESC").First(&batch).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		current, ok, currentErr := currentPublishedPrescreenBatch(ctx, db)
		return current, ok, currentErr
	}
	if err != nil {
		return UniverseBatch{}, false, err
	}
	return batch, true, nil
}
