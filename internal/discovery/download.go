package discovery

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"unicode"
)

type CacheMetadata struct {
	Path         string
	FinalURL     string
	ETag         string
	LastModified string
	SHA256       string
	ContentType  string
	Size         int64
}

type DownloadResult struct {
	Path         string
	FinalURL     string
	ETag         string
	LastModified string
	SHA256       string
	ContentType  string
	Size         int64
	NotModified  bool
}

type Downloader struct {
	Client   *http.Client
	CacheDir string
	MaxBytes int64
}

func (d *Downloader) Download(ctx context.Context, sourceURL, cacheKey string, prior *CacheMetadata) (DownloadResult, error) {
	if d.MaxBytes <= 0 {
		return DownloadResult{}, fmt.Errorf("download maximum bytes must be positive")
	}
	if d.CacheDir == "" {
		return DownloadResult{}, fmt.Errorf("download cache directory is required")
	}
	if !safeCacheKey(cacheKey) {
		return DownloadResult{}, fmt.Errorf("unsafe download cache key %q", cacheKey)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, sourceURL, nil)
	if err != nil {
		return DownloadResult{}, fmt.Errorf("invalid download URL")
	}
	if req.URL.Scheme != "http" && req.URL.Scheme != "https" || req.URL.Host == "" {
		return DownloadResult{}, fmt.Errorf("invalid download URL")
	}
	if prior != nil {
		if prior.ETag != "" {
			req.Header.Set("If-None-Match", prior.ETag)
		}
		if prior.LastModified != "" {
			req.Header.Set("If-Modified-Since", prior.LastModified)
		}
	}

	client := d.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
		return DownloadResult{}, newDownloadRequestError(req.URL.Hostname(), err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotModified {
		if prior == nil || prior.Path == "" {
			return DownloadResult{}, fmt.Errorf("download returned 304 without cached file metadata")
		}
		if info, statErr := os.Stat(prior.Path); statErr != nil || info.IsDir() {
			if statErr == nil {
				statErr = fmt.Errorf("path is a directory")
			}
			return DownloadResult{}, fmt.Errorf("cached file unavailable for 304 response: %w", statErr)
		}
		return resultFromMetadata(*prior, true), nil
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return DownloadResult{}, fmt.Errorf("download from host %q returned HTTP %d", req.URL.Host, resp.StatusCode)
	}
	if resp.ContentLength > d.MaxBytes {
		return DownloadResult{}, fmt.Errorf("download from host %q exceeds maximum size of %d bytes", req.URL.Host, d.MaxBytes)
	}

	if err := os.MkdirAll(d.CacheDir, 0o755); err != nil {
		return DownloadResult{}, fmt.Errorf("create download cache directory: %w", err)
	}
	temp, err := os.CreateTemp(d.CacheDir, ".download-*")
	if err != nil {
		return DownloadResult{}, fmt.Errorf("create temporary download file: %w", err)
	}
	tempPath := temp.Name()
	keepTemp := false
	defer func() {
		_ = temp.Close()
		if !keepTemp {
			_ = os.Remove(tempPath)
		}
	}()

	hash := sha256.New()
	written, exceeded, err := copyWithHardLimit(io.MultiWriter(temp, hash), resp.Body, d.MaxBytes)
	if err != nil {
		return DownloadResult{}, fmt.Errorf("stream download from host %q: %w", req.URL.Host, err)
	}
	if exceeded {
		return DownloadResult{}, fmt.Errorf("download from host %q exceeds maximum size of %d bytes", req.URL.Host, d.MaxBytes)
	}
	if err := temp.Sync(); err != nil {
		return DownloadResult{}, fmt.Errorf("sync temporary download file: %w", err)
	}
	if err := temp.Close(); err != nil {
		return DownloadResult{}, fmt.Errorf("close temporary download file: %w", err)
	}

	cachePath := filepath.Join(d.CacheDir, cacheKey)
	if err := os.Rename(tempPath, cachePath); err != nil {
		return DownloadResult{}, fmt.Errorf("replace cached download: %w", err)
	}
	keepTemp = true

	finalURL := sourceURL
	if resp.Request != nil && resp.Request.URL != nil {
		finalURL = resp.Request.URL.String()
	}
	return DownloadResult{
		Path:         cachePath,
		FinalURL:     finalURL,
		ETag:         resp.Header.Get("ETag"),
		LastModified: resp.Header.Get("Last-Modified"),
		SHA256:       hex.EncodeToString(hash.Sum(nil)),
		ContentType:  resp.Header.Get("Content-Type"),
		Size:         written,
	}, nil
}

type downloadRequestError struct {
	host  string
	cause error
}

func (e *downloadRequestError) Error() string {
	if e.host == "" {
		return "download request failed"
	}
	return fmt.Sprintf("download from host %q failed", e.host)
}

func (e *downloadRequestError) Unwrap() error { return e.cause }

func newDownloadRequestError(host string, err error) error {
	cause := err
	for {
		urlErr, ok := cause.(*url.Error)
		if !ok || urlErr.Err == nil {
			break
		}
		cause = urlErr.Err
	}
	return &downloadRequestError{host: host, cause: cause}
}

func copyWithHardLimit(dst io.Writer, src io.Reader, maxBytes int64) (written int64, exceeded bool, err error) {
	written, err = io.Copy(dst, io.LimitReader(src, maxBytes))
	if err != nil || written < maxBytes {
		return written, false, err
	}

	var extra [1]byte
	extraBytes, extraErr := io.ReadFull(src, extra[:])
	if extraBytes > 0 {
		return written, true, nil
	}
	if errors.Is(extraErr, io.EOF) {
		return written, false, nil
	}
	return written, false, extraErr
}

func resultFromMetadata(metadata CacheMetadata, notModified bool) DownloadResult {
	return DownloadResult{
		Path:         metadata.Path,
		FinalURL:     metadata.FinalURL,
		ETag:         metadata.ETag,
		LastModified: metadata.LastModified,
		SHA256:       metadata.SHA256,
		ContentType:  metadata.ContentType,
		Size:         metadata.Size,
		NotModified:  notModified,
	}
}

func safeCacheKey(key string) bool {
	if key == "" || key == "." || key == ".." {
		return false
	}
	for _, r := range key {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_' || r == '.' {
			continue
		}
		return false
	}
	return true
}

// OpenSafeZIP opens a ZIP archive and validates its declared entries before
// returning it. The caller owns the returned ReadCloser and must close it.
func OpenSafeZIP(filename string, maxEntries int, maxUncompressedBytes int64) (*zip.ReadCloser, error) {
	if maxEntries <= 0 {
		return nil, fmt.Errorf("ZIP maximum entries must be positive")
	}
	if maxUncompressedBytes <= 0 {
		return nil, fmt.Errorf("ZIP maximum uncompressed bytes must be positive")
	}

	zr, err := zip.OpenReader(filename)
	if err != nil {
		return nil, fmt.Errorf("open ZIP archive: %w", err)
	}
	valid := false
	defer func() {
		if !valid {
			_ = zr.Close()
		}
	}()

	if len(zr.File) > maxEntries {
		return nil, fmt.Errorf("ZIP archive contains %d entries, maximum is %d", len(zr.File), maxEntries)
	}
	limit := uint64(maxUncompressedBytes)
	var total uint64
	for _, file := range zr.File {
		if !safeZIPName(file.Name) {
			return nil, fmt.Errorf("ZIP archive contains unsafe entry name %q", file.Name)
		}
		size := file.UncompressedSize64
		if size > limit {
			return nil, fmt.Errorf("ZIP entry %q exceeds uncompressed size limit", file.Name)
		}
		if total > limit-size {
			return nil, fmt.Errorf("ZIP archive exceeds aggregate uncompressed size limit")
		}
		total += size
	}

	valid = true
	return zr, nil
}

func safeZIPName(name string) bool {
	if name == "" || strings.IndexByte(name, 0) >= 0 {
		return false
	}
	normalized := strings.ReplaceAll(name, `\`, "/")
	if path.IsAbs(normalized) || filepath.IsAbs(name) || hasWindowsDrivePrefix(normalized) {
		return false
	}
	cleaned := path.Clean(normalized)
	return cleaned != ".." && !strings.HasPrefix(cleaned, "../")
}

func hasWindowsDrivePrefix(name string) bool {
	return len(name) >= 2 && ((name[0] >= 'a' && name[0] <= 'z') || (name[0] >= 'A' && name[0] <= 'Z')) && name[1] == ':'
}
