package discovery

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Immutable identities and assessment versions are shared by every batch.
// Memberships retain the original snapshot ID and capture time, so the public
// snapshot view and historical research keep their original meaning.
type CapitalRiskIdentity struct {
	ID            uint      `gorm:"primaryKey"`
	ContentSHA256 string    `gorm:"size:64;not null;uniqueIndex"`
	SecurityID    uint      `gorm:"not null;index:idx_risk_identity_security_kind_time,priority:1"`
	Kind          string    `gorm:"size:64;not null;index:idx_risk_identity_security_kind_time,priority:2"`
	Accession     string    `gorm:"size:32;not null"`
	EffectiveAt   time.Time `gorm:"index:idx_risk_identity_security_kind_time,priority:3"`
	Security      Security  `gorm:"foreignKey:SecurityID;constraint:OnDelete:RESTRICT,OnUpdate:RESTRICT"`
}

type CapitalRiskVersion struct {
	ID            uint   `gorm:"primaryKey"`
	ContentSHA256 string `gorm:"size:64;not null;uniqueIndex"`
	IdentityID    uint   `gorm:"not null;index"`
	AcceptedAt    time.Time
	ActiveUntil   time.Time
	Active        bool
	BlocksA       bool
	BlocksB       bool
	Severity      string `gorm:"size:16"`
	ChangesShares bool
	Reason        string              `gorm:"type:text"`
	Identity      CapitalRiskIdentity `gorm:"foreignKey:IdentityID;constraint:OnDelete:RESTRICT,OnUpdate:RESTRICT"`
}

type CapitalRiskBatch struct {
	ID      uint   `gorm:"primaryKey"`
	BatchID string `gorm:"size:64;not null;uniqueIndex"`
}

type CapitalRiskMembership struct {
	ID            uint                `gorm:"primaryKey"`
	BatchNumberID uint                `gorm:"not null;uniqueIndex:idx_risk_membership_batch_identity,priority:1"`
	IdentityID    uint                `gorm:"not null;index;uniqueIndex:idx_risk_membership_batch_identity,priority:2"`
	VersionID     uint                `gorm:"not null"`
	CreatedAt     time.Time           `gorm:"autoCreateTime:false"`
	Batch         CapitalRiskBatch    `gorm:"foreignKey:BatchNumberID;constraint:OnDelete:RESTRICT,OnUpdate:RESTRICT"`
	Identity      CapitalRiskIdentity `gorm:"foreignKey:IdentityID;constraint:OnDelete:RESTRICT,OnUpdate:RESTRICT"`
	Version       CapitalRiskVersion  `gorm:"foreignKey:VersionID;constraint:OnDelete:RESTRICT,OnUpdate:RESTRICT"`
}

type CapitalRiskStorageMigration struct {
	ID           uint `gorm:"primaryKey"`
	LastID       uint
	TotalRows    int64
	MigratedRows int64
	Status       string
	LegacySHA256 string
	CompletedAt  *time.Time
}

const legacyCapitalRiskTable = "capital_risk_snapshots_legacy"

const normalizedCapitalRiskSelect = `SELECT m.id, b.batch_id, i.security_id, i.kind, i.accession, i.effective_at,
 v.accepted_at, v.active_until, v.active, v.blocks_a, v.blocks_b, v.severity,
 v.changes_shares, v.reason, m.created_at
 FROM capital_risk_memberships m
 JOIN capital_risk_batches b ON b.id = m.batch_number_id
 JOIN capital_risk_identities i ON i.id = m.identity_id
 JOIN capital_risk_versions v ON v.id = m.version_id AND v.identity_id = i.id`

func EnsureCapitalRiskStorage(db *gorm.DB) error {
	return db.Transaction(func(tx *gorm.DB) error {
		var kind string
		if err := tx.Raw("SELECT type FROM sqlite_master WHERE name = 'capital_risk_snapshots'").Scan(&kind).Error; err != nil {
			return err
		}
		if kind == "table" {
			if tx.Migrator().HasTable(legacyCapitalRiskTable) {
				return errors.New("both legacy capital risk tables exist")
			}
			if err := tx.Exec("ALTER TABLE capital_risk_snapshots RENAME TO " + legacyCapitalRiskTable).Error; err != nil {
				return err
			}
		}
		if err := tx.AutoMigrate(&CapitalRiskIdentity{}, &CapitalRiskVersion{}, &CapitalRiskBatch{}, &CapitalRiskMembership{}, &CapitalRiskStorageMigration{}); err != nil {
			return err
		}
		if tx.Migrator().HasTable(legacyCapitalRiskTable) {
			// New live snapshots must not reuse IDs awaiting offline consolidation.
			var maximum uint
			if err := tx.Raw("SELECT COALESCE(MAX(id),0) FROM " + legacyCapitalRiskTable).Scan(&maximum).Error; err != nil {
				return err
			}
			if err := tx.Exec("INSERT INTO sqlite_sequence(name,seq) SELECT 'capital_risk_memberships', ? WHERE NOT EXISTS (SELECT 1 FROM sqlite_sequence WHERE name='capital_risk_memberships')", maximum).Error; err != nil {
				return err
			}
			if err := tx.Exec("UPDATE sqlite_sequence SET seq=MAX(seq,?) WHERE name='capital_risk_memberships'", maximum).Error; err != nil {
				return err
			}
		}
		return createCapitalRiskView(tx)
	})
}

func createCapitalRiskView(db *gorm.DB) error {
	if err := db.Exec("DROP VIEW IF EXISTS capital_risk_snapshots").Error; err != nil {
		return err
	}
	query := normalizedCapitalRiskSelect
	if db.Migrator().HasTable(legacyCapitalRiskTable) {
		// During resumable migration, readers see each historical row exactly once.
		query += ` UNION ALL SELECT l.id,l.batch_id,l.security_id,l.kind,l.accession,l.effective_at,
   l.accepted_at,l.active_until,l.active,l.blocks_a,l.blocks_b,l.severity,l.changes_shares,l.reason,l.created_at FROM capital_risk_snapshots_legacy l
   WHERE NOT EXISTS (SELECT 1 FROM capital_risk_memberships m WHERE m.id=l.id)`
	}
	return db.Exec("CREATE VIEW capital_risk_snapshots AS " + query).Error
}

func riskContentHash(value any) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}

func riskTime(value time.Time) string { return value.UTC().Format(time.RFC3339Nano) }

// PersistCapitalRiskSnapshots is the only write path for risk snapshots. The
// unique numeric batch/identity key retains the previous first-write semantics.
func PersistCapitalRiskSnapshots(ctx context.Context, db *gorm.DB, rows []CapitalRiskSnapshot) error {
	if len(rows) == 0 {
		return nil
	}
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for start := 0; start < len(rows); start += 500 {
			end := start + 500
			if end > len(rows) {
				end = len(rows)
			}
			if err := persistCapitalRiskChunk(tx, rows[start:end]); err != nil {
				return err
			}
		}
		return nil
	})
}

func deleteCapitalRiskBatchSnapshots(ctx context.Context, db *gorm.DB, batchID string) error {
	if err := db.WithContext(ctx).Where("batch_number_id IN (SELECT id FROM capital_risk_batches WHERE batch_id = ?)", batchID).Delete(&CapitalRiskMembership{}).Error; err != nil {
		return err
	}
	if db.Migrator().HasTable(legacyCapitalRiskTable) {
		return db.WithContext(ctx).Table(legacyCapitalRiskTable).Where("batch_id = ?", batchID).Delete(&CapitalRiskSnapshot{}).Error
	}
	return nil
}

func persistCapitalRiskChunk(db *gorm.DB, rows []CapitalRiskSnapshot) error {
	identities := map[string]CapitalRiskIdentity{}
	identityHashes := make([]string, len(rows))
	batchKeys := map[string]CapitalRiskBatch{}
	for n, row := range rows {
		if strings.TrimSpace(row.BatchID) == "" || row.SecurityID == 0 {
			return errors.New("capital risk requires a batch and security identity")
		}
		identity := CapitalRiskIdentity{SecurityID: row.SecurityID, Kind: row.Kind, Accession: row.Accession, EffectiveAt: row.EffectiveAt}
		key, err := riskContentHash([]any{identity.SecurityID, identity.Kind, identity.Accession, riskTime(identity.EffectiveAt)})
		if err != nil {
			return err
		}
		identity.ContentSHA256 = key
		identities[key] = identity
		identityHashes[n] = key
		batchKeys[row.BatchID] = CapitalRiskBatch{BatchID: row.BatchID}
	}
	keys := make([]string, 0, len(identities))
	for key := range identities {
		keys = append(keys, key)
	}
	var foundIdentities []CapitalRiskIdentity
	if err := db.Where("content_sha256 IN ?", keys).Find(&foundIdentities).Error; err != nil {
		return err
	}
	identityIDs := map[string]uint{}
	for _, row := range foundIdentities {
		identityIDs[row.ContentSHA256] = row.ID
	}
	missingIdentities := []CapitalRiskIdentity{}
	for key, row := range identities {
		if identityIDs[key] == 0 {
			missingIdentities = append(missingIdentities, row)
		}
	}
	if len(missingIdentities) > 0 {
		if err := db.Clauses(clause.OnConflict{DoNothing: true}).CreateInBatches(&missingIdentities, 50).Error; err != nil {
			return err
		}
		if err := db.Where("content_sha256 IN ?", keys).Find(&foundIdentities).Error; err != nil {
			return err
		}
		for _, row := range foundIdentities {
			identityIDs[row.ContentSHA256] = row.ID
		}
	}
	batchIDs := map[string]uint{}
	for key, row := range batchKeys {
		if err := db.Where("batch_id = ?", key).FirstOrCreate(&row).Error; err != nil {
			return err
		}
		batchIDs[key] = row.ID
	}
	versions := map[string]CapitalRiskVersion{}
	versionHashes := make([]string, len(rows))
	for n, row := range rows {
		version := CapitalRiskVersion{IdentityID: identityIDs[identityHashes[n]], AcceptedAt: row.AcceptedAt, ActiveUntil: row.ActiveUntil, Active: row.Active, BlocksA: row.BlocksA, BlocksB: row.BlocksB, Severity: row.Severity, ChangesShares: row.ChangesShares, Reason: row.Reason}
		if version.IdentityID == 0 {
			return errors.New("capital risk identity was not persisted")
		}
		key, err := riskContentHash([]any{identityHashes[n], riskTime(version.AcceptedAt), riskTime(version.ActiveUntil), version.Active, version.BlocksA, version.BlocksB, version.Severity, version.ChangesShares, version.Reason})
		if err != nil {
			return err
		}
		version.ContentSHA256 = key
		versions[key] = version
		versionHashes[n] = key
	}
	keys = keys[:0]
	for key := range versions {
		keys = append(keys, key)
	}
	var foundVersions []CapitalRiskVersion
	if err := db.Where("content_sha256 IN ?", keys).Find(&foundVersions).Error; err != nil {
		return err
	}
	versionIDs := map[string]uint{}
	for _, row := range foundVersions {
		versionIDs[row.ContentSHA256] = row.ID
	}
	missingVersions := []CapitalRiskVersion{}
	for key, row := range versions {
		if versionIDs[key] == 0 {
			missingVersions = append(missingVersions, row)
		}
	}
	if len(missingVersions) > 0 {
		if err := db.Clauses(clause.OnConflict{DoNothing: true}).CreateInBatches(&missingVersions, 50).Error; err != nil {
			return err
		}
		if err := db.Where("content_sha256 IN ?", keys).Find(&foundVersions).Error; err != nil {
			return err
		}
		for _, row := range foundVersions {
			versionIDs[row.ContentSHA256] = row.ID
		}
	}
	memberships := make([]CapitalRiskMembership, 0, len(rows))
	for n, row := range rows {
		if versionIDs[versionHashes[n]] == 0 {
			return errors.New("capital risk version was not persisted")
		}
		memberships = append(memberships, CapitalRiskMembership{ID: row.ID, BatchNumberID: batchIDs[row.BatchID], IdentityID: identityIDs[identityHashes[n]], VersionID: versionIDs[versionHashes[n]], CreatedAt: row.CreatedAt})
	}
	return db.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "batch_number_id"}, {Name: "identity_id"}}, DoNothing: true}).CreateInBatches(&memberships, 50).Error
}

type CapitalRiskConsolidationResult struct {
	TotalRows      int64  `json:"total_rows"`
	MigratedRows   int64  `json:"migrated_rows"`
	IdentityCount  int64  `json:"identity_count"`
	VersionCount   int64  `json:"version_count"`
	SnapshotSHA256 string `json:"snapshot_sha256"`
	LegacyRemoved  bool   `json:"legacy_removed"`
}

// ConsolidateCapitalRiskStorage is an offline, restartable maintenance action.
// The legacy table is dropped only after every original ID is represented and
// complete before/after record digests agree, including timestamps and flags.
func ConsolidateCapitalRiskStorage(ctx context.Context, db *gorm.DB, progress func(int64, int64)) (CapitalRiskConsolidationResult, error) {
	result := CapitalRiskConsolidationResult{}
	if err := EnsureCapitalRiskStorage(db); err != nil {
		return result, err
	}
	if !db.Migrator().HasTable(legacyCapitalRiskTable) {
		return capitalRiskConsolidationStats(db, result)
	}
	var state CapitalRiskStorageMigration
	if err := db.FirstOrCreate(&state, CapitalRiskStorageMigration{ID: 1}).Error; err != nil {
		return result, err
	}
	if err := db.Table(legacyCapitalRiskTable).Count(&state.TotalRows).Error; err != nil {
		return result, err
	}
	result.TotalRows = state.TotalRows
	for {
		var rows []CapitalRiskSnapshot
		if err := db.WithContext(ctx).Table(legacyCapitalRiskTable).Where("id > ?", state.LastID).Order("id ASC").Limit(2000).Find(&rows).Error; err != nil {
			return result, err
		}
		if len(rows) == 0 {
			break
		}
		if err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if err := PersistCapitalRiskSnapshots(ctx, tx, rows); err != nil {
				return err
			}
			state.LastID = rows[len(rows)-1].ID
			state.MigratedRows += int64(len(rows))
			state.Status = "migrating"
			return tx.Save(&state).Error
		}); err != nil {
			return result, err
		}
		result.MigratedRows = state.MigratedRows
		if progress != nil {
			progress(state.MigratedRows, state.TotalRows)
		}
	}
	var missing int64
	if err := db.Raw(`SELECT COUNT(*) FROM capital_risk_snapshots_legacy l LEFT JOIN capital_risk_memberships m ON m.id=l.id WHERE m.id IS NULL`).Scan(&missing).Error; err != nil {
		return result, err
	}
	if missing != 0 {
		return result, fmt.Errorf("capital risk migration is missing %d original IDs", missing)
	}
	before, err := capitalRiskSnapshotDigest(ctx, db, legacyCapitalRiskTable, false)
	if err != nil {
		return result, err
	}
	after, err := capitalRiskSnapshotDigest(ctx, db, "capital_risk_snapshots", true)
	if err != nil {
		return result, err
	}
	if before != after {
		return result, errors.New("capital risk snapshot digest differs; legacy evidence has been retained")
	}
	result.SnapshotSHA256 = before
	if err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("DROP VIEW capital_risk_snapshots").Error; err != nil {
			return err
		}
		if err := tx.Exec("DROP TABLE " + legacyCapitalRiskTable).Error; err != nil {
			return err
		}
		if err := createCapitalRiskView(tx); err != nil {
			return err
		}
		completed := time.Now().UTC()
		state.Status = "completed"
		state.CompletedAt = &completed
		state.LegacySHA256 = before
		return tx.Save(&state).Error
	}); err != nil {
		return result, err
	}
	result.LegacyRemoved = true
	return capitalRiskConsolidationStats(db, result)
}

func capitalRiskConsolidationStats(db *gorm.DB, result CapitalRiskConsolidationResult) (CapitalRiskConsolidationResult, error) {
	if err := db.Model(&CapitalRiskIdentity{}).Count(&result.IdentityCount).Error; err != nil {
		return result, err
	}
	if err := db.Model(&CapitalRiskVersion{}).Count(&result.VersionCount).Error; err != nil {
		return result, err
	}
	if !db.Migrator().HasTable(legacyCapitalRiskTable) {
		result.LegacyRemoved = true
	}
	return result, nil
}

func capitalRiskSnapshotDigest(ctx context.Context, db *gorm.DB, table string, legacyOnly bool) (string, error) {
	digest := sha256.New()
	query := db.WithContext(ctx).Table(table).Select("id,batch_id,security_id,kind,accession,effective_at,accepted_at,active_until,active,blocks_a,blocks_b,severity,changes_shares,reason,created_at").Order("id ASC")
	if legacyOnly {
		query = query.Where("id IN (SELECT id FROM capital_risk_snapshots_legacy)")
	}
	rows, err := query.Rows()
	if err != nil {
		return "", err
	}
	defer rows.Close()
	for rows.Next() {
		var snapshot CapitalRiskSnapshot
		var effective, accepted, until, created sql.NullTime
		var kind, accession, severity, reason sql.NullString
		var active, blocksA, blocksB, changesShares sql.NullBool
		if err := rows.Scan(&snapshot.ID, &snapshot.BatchID, &snapshot.SecurityID, &kind, &accession, &effective, &accepted, &until, &active, &blocksA, &blocksB, &severity, &changesShares, &reason, &created); err != nil {
			return "", err
		}
		snapshot.Kind = kind.String
		snapshot.Accession = accession.String
		snapshot.Severity = severity.String
		snapshot.Reason = reason.String
		snapshot.EffectiveAt = effective.Time
		snapshot.AcceptedAt = accepted.Time
		snapshot.ActiveUntil = until.Time
		snapshot.CreatedAt = created.Time
		snapshot.Active = active.Bool
		snapshot.BlocksA = blocksA.Bool
		snapshot.BlocksB = blocksB.Bool
		snapshot.ChangesShares = changesShares.Bool
		if err := writeCapitalRiskSnapshotDigest(digest, snapshot); err != nil {
			return "", err
		}
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

func writeCapitalRiskSnapshotDigest(digest hash.Hash, snapshot CapitalRiskSnapshot) error {
	snapshot.EffectiveAt = snapshot.EffectiveAt.UTC()
	snapshot.AcceptedAt = snapshot.AcceptedAt.UTC()
	snapshot.ActiveUntil = snapshot.ActiveUntil.UTC()
	snapshot.CreatedAt = snapshot.CreatedAt.UTC()
	data, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	_, err = digest.Write(append(data, '\n'))
	return err
}
