package discovery

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"
)

// SupersededMarketRepairCleanupPreview describes the full-snapshot rows that
// can be removed from old targeted price-repair batches. Price snapshots and
// candidate signal events are intentionally not included: both are durable
// audit evidence and remain useful after a replacement market batch exists.
type SupersededMarketRepairCleanupPreview struct {
	Batches            int64 `json:"batches"`
	UniverseSnapshots  int64 `json:"universe_snapshots"`
	CandidateScoreRows int64 `json:"candidate_score_rows"`
}

func (p SupersededMarketRepairCleanupPreview) Total() int64 {
	return p.Batches + p.UniverseSnapshots + p.CandidateScoreRows
}

// PreviewSupersededMarketRepairCleanup finds targeted price-repair batches
// older than cutoff that have already been superseded by the current market
// pointer. A current repair batch is never eligible, even if it is old, so a
// stalled normal workflow cannot make the live research view disappear.
func PreviewSupersededMarketRepairCleanup(ctx context.Context, db *gorm.DB, cutoff time.Time) (SupersededMarketRepairCleanupPreview, error) {
	preview := SupersededMarketRepairCleanupPreview{}
	batchIDs, err := supersededMarketRepairBatchIDs(ctx, db, cutoff)
	if err != nil {
		return preview, err
	}
	if len(batchIDs) == 0 {
		return preview, nil
	}
	preview.Batches = int64(len(batchIDs))
	if err := db.WithContext(ctx).Model(&UniverseSnapshot{}).Where("batch_id IN ?", batchIDs).Count(&preview.UniverseSnapshots).Error; err != nil {
		return SupersededMarketRepairCleanupPreview{}, err
	}
	if err := db.WithContext(ctx).Model(&CandidateScoreSnapshot{}).Where("batch_id IN ?", batchIDs).Count(&preview.CandidateScoreRows).Error; err != nil {
		return SupersededMarketRepairCleanupPreview{}, err
	}
	return preview, nil
}

// CleanupSupersededMarketRepairBatches removes only the full-snapshot copies
// created for old one-symbol price repairs. It intentionally leaves the
// underlying local price history and immutable signal events intact.
func CleanupSupersededMarketRepairBatches(ctx context.Context, db *gorm.DB, cutoff time.Time) (SupersededMarketRepairCleanupPreview, error) {
	if db == nil {
		return SupersededMarketRepairCleanupPreview{}, errors.New("database is required")
	}
	batchIDs, err := supersededMarketRepairBatchIDs(ctx, db, cutoff)
	if err != nil {
		return SupersededMarketRepairCleanupPreview{}, err
	}
	if len(batchIDs) == 0 {
		return SupersededMarketRepairCleanupPreview{}, nil
	}
	deleted := SupersededMarketRepairCleanupPreview{Batches: int64(len(batchIDs))}
	if err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Where("batch_id IN ?", batchIDs).Delete(&UniverseSnapshot{})
		if result.Error != nil {
			return result.Error
		}
		deleted.UniverseSnapshots = result.RowsAffected
		result = tx.Where("batch_id IN ?", batchIDs).Delete(&CandidateScoreSnapshot{})
		if result.Error != nil {
			return result.Error
		}
		deleted.CandidateScoreRows = result.RowsAffected
		result = tx.Where("batch_id IN ?", batchIDs).Delete(&UniverseBatch{})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != deleted.Batches {
			return errors.New("market repair batches changed before cleanup")
		}
		return nil
	}); err != nil {
		return SupersededMarketRepairCleanupPreview{}, err
	}
	return deleted, nil
}

func supersededMarketRepairBatchIDs(ctx context.Context, db *gorm.DB, cutoff time.Time) ([]string, error) {
	if db == nil {
		return nil, errors.New("database is required")
	}
	if cutoff.IsZero() {
		return nil, errors.New("cleanup cutoff is required")
	}
	var pointer CurrentBatchPointer
	if err := db.WithContext(ctx).First(&pointer, "kind = ?", BatchKindPrescreen).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			// Without a published current view we cannot prove that the repair
			// is superseded, so preserve every historical batch.
			return []string{}, nil
		}
		return nil, err
	}
	var batches []UniverseBatch
	if err := db.WithContext(ctx).
		Where("kind = ? AND status = ? AND completed_at IS NOT NULL AND completed_at < ? AND batch_id <> ?", BatchKindPrescreen, BatchStatusPublished, cutoff, pointer.BatchID).
		Order("completed_at ASC, batch_id ASC").
		Find(&batches).Error; err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(batches))
	for _, batch := range batches {
		if isTargetedMarketRepairBatch(batch) {
			ids = append(ids, batch.BatchID)
		}
	}
	return ids, nil
}

func isTargetedMarketRepairBatch(batch UniverseBatch) bool {
	var versions []SourceVersion
	if err := json.Unmarshal([]byte(batch.SourceVersionsJSON), &versions); err != nil {
		return false
	}
	for _, version := range versions {
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(version.Source)), "price-repair:") {
			return true
		}
	}
	return false
}
