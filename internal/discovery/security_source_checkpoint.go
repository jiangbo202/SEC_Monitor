package discovery

import (
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const securitySourceArtifactFormat = "security-source-artifact-v1"

func securitySourceArtifactKey(effectiveDate, phase, scopeSHA, policySHA string) (string, error) {
	if strings.TrimSpace(effectiveDate) == "" || strings.TrimSpace(phase) == "" || !validSHA256(scopeSHA) || !validSHA256(policySHA) {
		return "", errors.New("security source artifact identity is invalid")
	}
	sum := sha256.Sum256([]byte(securitySourceArtifactFormat + "\n" + effectiveDate + "\n" + phase + "\n" + scopeSHA + "\n" + policySHA))
	return hex.EncodeToString(sum[:]), nil
}

func securitySourceScopeSHA(value any) (string, error) {
	return hashCanonicalContent(struct {
		Format string
		Scope  any
	}{securitySourceArtifactFormat, value})
}

func securitySourceArtifactPath(cacheDir, artifactKey string) (string, error) {
	if strings.TrimSpace(cacheDir) == "" || !validSHA256(artifactKey) {
		return "", errors.New("security source artifact path is invalid")
	}
	dir, err := filepath.Abs(filepath.Join(cacheDir, "source-checkpoints"))
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "security-source-"+artifactKey+".json.gz"), nil
}

func loadSecuritySourceArtifact[T any](ctx context.Context, db *gorm.DB, cacheDir, artifactKey string, ttl time.Duration, dst *T) (SecuritySourceCheckpoint, bool, error) {
	if ctx == nil || db == nil || dst == nil {
		return SecuritySourceCheckpoint{}, false, errors.New("security source artifact load is invalid")
	}
	var checkpoint SecuritySourceCheckpoint
	err := db.WithContext(ctx).First(&checkpoint, "artifact_key = ? AND status = ?", artifactKey, securityCheckpointCompleted).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return SecuritySourceCheckpoint{}, false, nil
	}
	if err != nil {
		return SecuritySourceCheckpoint{}, false, err
	}
	if checkpoint.CompletedAt == nil || ttl > 0 && time.Since(*checkpoint.CompletedAt) > ttl {
		return checkpoint, false, nil
	}
	expectedPath, err := securitySourceArtifactPath(cacheDir, artifactKey)
	if err != nil {
		return SecuritySourceCheckpoint{}, false, err
	}
	if checkpoint.PayloadPath != expectedPath || !validSHA256(checkpoint.PayloadSHA256) {
		return checkpoint, false, nil
	}
	file, err := os.Open(expectedPath)
	if errors.Is(err, os.ErrNotExist) {
		return checkpoint, false, nil
	}
	if err != nil {
		return checkpoint, false, err
	}
	defer file.Close()
	hash := sha256.New()
	gzipReader, err := gzip.NewReader(io.TeeReader(file, hash))
	if err != nil {
		return checkpoint, false, nil
	}
	decoder := json.NewDecoder(gzipReader)
	decodeErr := decoder.Decode(dst)
	_, drainErr := io.Copy(io.Discard, gzipReader)
	closeErr := gzipReader.Close()
	if decodeErr != nil || drainErr != nil || closeErr != nil {
		return checkpoint, false, nil
	}
	if hex.EncodeToString(hash.Sum(nil)) != checkpoint.PayloadSHA256 {
		return checkpoint, false, nil
	}
	return checkpoint, true, nil
}

func beginSecuritySourceCheckpoint(ctx context.Context, db *gorm.DB, checkpoint SecuritySourceCheckpoint) error {
	if ctx == nil || db == nil || !validSHA256(checkpoint.ArtifactKey) {
		return errors.New("security source checkpoint is invalid")
	}
	now := time.Now().UTC()
	checkpoint.Status = securityCheckpointRunning
	checkpoint.AttemptCount = 1
	checkpoint.StartedAt = now
	checkpoint.CompletedAt = nil
	result := db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&checkpoint)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 1 {
		return nil
	}
	if err := db.WithContext(ctx).Model(&SecuritySourceCheckpoint{}).Where("artifact_key = ?", checkpoint.ArtifactKey).Updates(map[string]any{
		"phase": checkpoint.Phase, "effective_date": checkpoint.EffectiveDate, "scope_sha256": checkpoint.ScopeSHA256,
		"policy_content_sha256": checkpoint.PolicyContentSHA256, "status": securityCheckpointRunning,
		"attempt_count": gorm.Expr("attempt_count + ?", 1), "record_count": 0, "payload_path": "", "payload_sha256": "",
		"error_message": "", "started_at": now, "completed_at": nil,
	}).Error; err != nil {
		return err
	}
	return nil
}

func saveSecuritySourceArtifact[T any](ctx context.Context, db *gorm.DB, cacheDir string, checkpoint SecuritySourceCheckpoint, payload T, recordCount int) error {
	path, err := securitySourceArtifactPath(cacheDir, checkpoint.ArtifactKey)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".security-source-*.tmp")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	hash := sha256.New()
	compressed := gzip.NewWriter(io.MultiWriter(temp, hash))
	encodeErr := json.NewEncoder(compressed).Encode(payload)
	closeGzipErr := compressed.Close()
	syncErr := temp.Sync()
	closeFileErr := temp.Close()
	for _, candidate := range []error{encodeErr, closeGzipErr, syncErr, closeFileErr} {
		if candidate != nil {
			return candidate
		}
	}
	if err := os.Rename(tempPath, path); err != nil {
		return err
	}
	now := time.Now().UTC()
	result := db.WithContext(context.WithoutCancel(ctx)).Model(&SecuritySourceCheckpoint{}).Where("artifact_key = ?", checkpoint.ArtifactKey).Updates(map[string]any{
		"status": securityCheckpointCompleted, "record_count": recordCount, "payload_path": path,
		"payload_sha256": hex.EncodeToString(hash.Sum(nil)), "error_message": "", "completed_at": now,
	})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return fmt.Errorf("complete security source checkpoint affected %d rows", result.RowsAffected)
	}
	return nil
}

func failSecuritySourceCheckpoint(ctx context.Context, db *gorm.DB, artifactKey string, cause error) {
	if db == nil || !validSHA256(artifactKey) || cause == nil {
		return
	}
	now := time.Now().UTC()
	_ = db.WithContext(context.WithoutCancel(ctx)).Model(&SecuritySourceCheckpoint{}).Where("artifact_key = ?", artifactKey).Updates(map[string]any{
		"status": securityCheckpointFailed, "error_message": cause.Error(), "completed_at": now,
	}).Error
}
