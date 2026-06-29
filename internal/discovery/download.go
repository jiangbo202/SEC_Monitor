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
	"sync"
)

type CacheMetadata struct {
	Path         string
	SourceURL    string
	CacheKey     string
	FinalURL     string
	ETag         string
	LastModified string
	SHA256       string
	ContentType  string
	Size         int64
}

type DownloadResult struct {
	Path         string
	SourceURL    string
	CacheKey     string
	FinalURL     string
	ETag         string
	LastModified string
	SHA256       string
	ContentType  string
	Size         int64
	NotModified  bool
}

// Downloader is stateful. Use it through a pointer and do not copy it after
// the first call to Download.
type Downloader struct {
	Client    *http.Client
	CacheDir  string
	MaxBytes  int64
	UserAgent string

	locksMu sync.Mutex
	locks   map[string]*cachePathLock
}

type cachePathLock struct {
	semaphore  chan struct{}
	references int
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
	cachePath, err := filepath.Abs(filepath.Join(d.CacheDir, cacheKey))
	if err != nil {
		return DownloadResult{}, fmt.Errorf("resolve download cache path: %w", err)
	}
	// This prevents in-process writers from racing for one cache path. A
	// cross-process lock is deferred until discovery has a worker process.
	unlock, err := d.lockCachePath(ctx, cachePath)
	if err != nil {
		return DownloadResult{}, fmt.Errorf("wait for download cache lock: %w", err)
	}
	defer unlock()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, sourceURL, nil)
	if err != nil {
		return DownloadResult{}, fmt.Errorf("invalid download URL")
	}
	if req.URL.Scheme != "http" && req.URL.Scheme != "https" || req.URL.Host == "" {
		return DownloadResult{}, fmt.Errorf("invalid download URL")
	}
	if d.UserAgent != "" {
		req.Header.Set("User-Agent", d.UserAgent)
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
		if cached, ok := d.cachedDownloadResult(cachePath, sourceURL, cacheKey); ok {
			return cached, nil
		}
		return DownloadResult{}, newDownloadRequestError(req.URL.Hostname(), err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotModified {
		if prior == nil {
			return DownloadResult{}, fmt.Errorf("download returned 304 without cached file metadata")
		}
		if err := verifyCachedFile(prior, cachePath, sourceURL, cacheKey); err != nil {
			return DownloadResult{}, err
		}
		return notModifiedResult(*prior, resp, sourceURL, cacheKey), nil
	}
	if resp.StatusCode != http.StatusOK {
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
		if cached, ok := d.cachedDownloadResult(cachePath, sourceURL, cacheKey); ok {
			return cached, nil
		}
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

	if err := os.Rename(tempPath, cachePath); err != nil {
		return DownloadResult{}, fmt.Errorf("replace cached download: %w", err)
	}
	keepTemp = true
	if err := syncDirectory(filepath.Dir(cachePath)); err != nil {
		return DownloadResult{}, fmt.Errorf("sync download cache directory: %w", err)
	}

	finalURL := sourceURL
	if resp.Request != nil && resp.Request.URL != nil {
		finalURL = resp.Request.URL.String()
	}
	return DownloadResult{
		Path:         cachePath,
		SourceURL:    sourceURL,
		CacheKey:     cacheKey,
		FinalURL:     finalURL,
		ETag:         resp.Header.Get("ETag"),
		LastModified: resp.Header.Get("Last-Modified"),
		SHA256:       hex.EncodeToString(hash.Sum(nil)),
		ContentType:  resp.Header.Get("Content-Type"),
		Size:         written,
	}, nil
}

func (d *Downloader) cachedDownloadResult(cachePath, sourceURL, cacheKey string) (DownloadResult, bool) {
	f, err := os.Open(cachePath)
	if err != nil {
		return DownloadResult{}, false
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || info.IsDir() {
		return DownloadResult{}, false
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, f); err != nil {
		return DownloadResult{}, false
	}
	return DownloadResult{
		Path:        cachePath,
		SourceURL:   sourceURL,
		CacheKey:    cacheKey,
		FinalURL:    sourceURL,
		SHA256:      hex.EncodeToString(hash.Sum(nil)),
		Size:        info.Size(),
		NotModified: true,
	}, true
}

func (d *Downloader) lockCachePath(ctx context.Context, cachePath string) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	d.locksMu.Lock()
	if d.locks == nil {
		d.locks = make(map[string]*cachePathLock)
	}
	lock := d.locks[cachePath]
	if lock == nil {
		lock = &cachePathLock{semaphore: make(chan struct{}, 1)}
		lock.semaphore <- struct{}{}
		d.locks[cachePath] = lock
	}
	lock.references++
	d.locksMu.Unlock()

	select {
	case <-ctx.Done():
		d.releaseCachePathLockReference(cachePath, lock)
		return nil, ctx.Err()
	case <-lock.semaphore:
	}
	if err := ctx.Err(); err != nil {
		lock.semaphore <- struct{}{}
		d.releaseCachePathLockReference(cachePath, lock)
		return nil, err
	}

	return func() {
		lock.semaphore <- struct{}{}
		d.releaseCachePathLockReference(cachePath, lock)
	}, nil
}

func (d *Downloader) releaseCachePathLockReference(cachePath string, lock *cachePathLock) {
	d.locksMu.Lock()
	lock.references--
	if lock.references == 0 && d.locks[cachePath] == lock {
		delete(d.locks, cachePath)
	}
	d.locksMu.Unlock()
}

func verifyCachedFile(prior *CacheMetadata, expectedPath, sourceURL, cacheKey string) error {
	if prior.Path != expectedPath || prior.SourceURL != sourceURL || prior.CacheKey != cacheKey {
		return fmt.Errorf("cached file metadata does not match this download")
	}
	info, err := os.Lstat(expectedPath)
	if err != nil {
		return fmt.Errorf("cached file unavailable")
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("cached file is not a regular file")
	}
	if info.Size() != prior.Size {
		return fmt.Errorf("cached file size does not match metadata")
	}

	file, err := os.Open(expectedPath)
	if err != nil {
		return fmt.Errorf("cached file unavailable")
	}
	defer file.Close()
	openedInfo, err := file.Stat()
	if err != nil || !openedInfo.Mode().IsRegular() || !os.SameFile(info, openedInfo) {
		return fmt.Errorf("cached file changed during validation")
	}
	hash := sha256.New()
	size, err := io.Copy(hash, file)
	if err != nil {
		return fmt.Errorf("cached file validation failed")
	}
	if size != prior.Size || hex.EncodeToString(hash.Sum(nil)) != prior.SHA256 {
		return fmt.Errorf("cached file size or SHA256 does not match metadata")
	}
	return nil
}

func notModifiedResult(prior CacheMetadata, resp *http.Response, sourceURL, cacheKey string) DownloadResult {
	result := resultFromMetadata(prior, true)
	result.SourceURL = sourceURL
	result.CacheKey = cacheKey
	if resp.Request != nil && resp.Request.URL != nil {
		result.FinalURL = resp.Request.URL.String()
	}
	if etag := resp.Header.Get("ETag"); etag != "" {
		result.ETag = etag
	}
	if lastModified := resp.Header.Get("Last-Modified"); lastModified != "" {
		result.LastModified = lastModified
	}
	return result
}

func syncDirectory(directory string) error {
	dir, err := os.Open(directory)
	if err != nil {
		return err
	}
	syncErr := dir.Sync()
	closeErr := dir.Close()
	if syncErr != nil {
		return syncErr
	}
	return closeErr
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
		SourceURL:    metadata.SourceURL,
		CacheKey:     metadata.CacheKey,
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
	if key == "" || key == "." || key == ".." || strings.HasSuffix(key, ".") {
		return false
	}
	for i := 0; i < len(key); i++ {
		character := key[i]
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' || character == '-' || character == '_' || character == '.' {
			continue
		}
		return false
	}
	base := key
	if dot := strings.IndexByte(base, '.'); dot >= 0 {
		base = base[:dot]
	}
	base = strings.ToUpper(base)
	if base == "CON" || base == "PRN" || base == "AUX" || base == "NUL" {
		return false
	}
	if len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) && base[3] >= '1' && base[3] <= '9' {
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
		mode := file.Mode()
		if !mode.IsRegular() && !mode.IsDir() {
			return nil, fmt.Errorf("ZIP archive contains unsupported entry mode for %q", file.Name)
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
	return cleaned != "." && cleaned != ".." && !strings.HasPrefix(cleaned, "../")
}

func hasWindowsDrivePrefix(name string) bool {
	return len(name) >= 2 && ((name[0] >= 'a' && name[0] <= 'z') || (name[0] >= 'A' && name[0] <= 'Z')) && name[1] == ':'
}
