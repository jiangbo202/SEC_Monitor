package discovery

import (
	"compress/gzip"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const (
	form4CheckpointFormat = "form4-issuer-checkpoint-v1"
	// One issuer per artifact makes transient SEC failures independently
	// retryable. The candidate-scoped allowlist keeps the file count bounded.
	form4CheckpointChunkSize = 1
)

type form4DocumentEvidence struct {
	Accession string
	SHA256    string
}

type form4IssuerChunkArtifact struct {
	ArtifactKey  string
	CIKs         []string
	Transactions []InsiderTransaction
	Coverage     []InsiderCoverage
	Documents    []form4DocumentEvidence
}

func form4IssuerChunkKey(metadataSHA, effectiveDate string, lookbackDays int, baseURL string, ciks []string) (string, error) {
	if !validSHA256(metadataSHA) || strings.TrimSpace(effectiveDate) == "" || lookbackDays <= 0 || len(ciks) == 0 {
		return "", errors.New("Form 4 checkpoint identity is invalid")
	}
	digest, err := hashCanonicalContent(struct {
		Format, MetadataSHA, EffectiveDate, BaseURL, ParserVersion, CoverageVersion string
		LookbackDays                                                                int
		CIKs                                                                        []string
	}{form4CheckpointFormat, metadataSHA, effectiveDate, baseURL, InsiderParserVersion, InsiderCoverageVersion, lookbackDays, ciks})
	if err != nil {
		return "", err
	}
	return digest, nil
}

func form4IssuerChunkPath(cacheDir, artifactKey string) (string, error) {
	if strings.TrimSpace(cacheDir) == "" || !validSHA256(artifactKey) {
		return "", errors.New("Form 4 checkpoint path is invalid")
	}
	dir, err := filepath.Abs(filepath.Join(cacheDir, "source-checkpoints", "form4"))
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "form4-chunk-"+artifactKey+".json.gz"), nil
}

func loadForm4IssuerChunk(cacheDir, artifactKey string, ciks []string) (form4IssuerChunkArtifact, bool, error) {
	path, err := form4IssuerChunkPath(cacheDir, artifactKey)
	if err != nil {
		return form4IssuerChunkArtifact{}, false, err
	}
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return form4IssuerChunkArtifact{}, false, nil
	}
	if err != nil {
		return form4IssuerChunkArtifact{}, false, err
	}
	defer file.Close()
	reader, err := gzip.NewReader(file)
	if err != nil {
		return form4IssuerChunkArtifact{}, false, nil
	}
	var artifact form4IssuerChunkArtifact
	decodeErr := json.NewDecoder(reader).Decode(&artifact)
	_, drainErr := io.Copy(io.Discard, reader)
	closeErr := reader.Close()
	if decodeErr != nil || drainErr != nil || closeErr != nil || artifact.ArtifactKey != artifactKey || len(artifact.CIKs) != len(ciks) {
		return form4IssuerChunkArtifact{}, false, nil
	}
	for i := range ciks {
		if artifact.CIKs[i] != ciks[i] {
			return form4IssuerChunkArtifact{}, false, nil
		}
	}
	return artifact, true, nil
}

func saveForm4IssuerChunk(cacheDir string, artifact form4IssuerChunkArtifact) error {
	path, err := form4IssuerChunkPath(cacheDir, artifact.ArtifactKey)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".form4-chunk-*.tmp")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	writer := gzip.NewWriter(temp)
	encodeErr := json.NewEncoder(writer).Encode(artifact)
	closeGzipErr := writer.Close()
	syncErr := temp.Sync()
	closeFileErr := temp.Close()
	for _, candidate := range []error{encodeErr, closeGzipErr, syncErr, closeFileErr} {
		if candidate != nil {
			return candidate
		}
	}
	return os.Rename(tempPath, path)
}
