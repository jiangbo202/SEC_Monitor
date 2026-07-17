package discovery

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestDownloaderSuccessfulDownload(t *testing.T) {
	payload := []byte("bounded discovery payload")
	client := clientForHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"v1"`)
		w.Header().Set("Last-Modified", "Mon, 22 Jun 2026 12:00:00 GMT")
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		_, _ = w.Write(payload)
	}))

	cacheDir := filepath.Join(t.TempDir(), "nested", "cache")
	d := Downloader{Client: client, CacheDir: cacheDir, MaxBytes: 1024}
	result, err := d.Download(context.Background(), "https://example.test/source", "nasdaq-symbols", nil)
	if err != nil {
		t.Fatalf("Download() error = %v", err)
	}

	wantHash := sha256.Sum256(payload)
	if result.Path != filepath.Join(cacheDir, "nasdaq-symbols") {
		t.Fatalf("Path = %q, want deterministic cache path", result.Path)
	}
	if result.SourceURL != "https://example.test/source" || result.CacheKey != "nasdaq-symbols" {
		t.Errorf("source binding = (%q, %q)", result.SourceURL, result.CacheKey)
	}
	if result.FinalURL != "https://example.test/source" {
		t.Errorf("FinalURL = %q, want source URL", result.FinalURL)
	}
	if result.ETag != `"v1"` || result.LastModified != "Mon, 22 Jun 2026 12:00:00 GMT" {
		t.Errorf("conditional metadata = (%q, %q)", result.ETag, result.LastModified)
	}
	if result.ContentType != "text/csv; charset=utf-8" {
		t.Errorf("ContentType = %q", result.ContentType)
	}
	if result.Size != int64(len(payload)) {
		t.Errorf("Size = %d, want %d", result.Size, len(payload))
	}
	if result.SHA256 != hex.EncodeToString(wantHash[:]) {
		t.Errorf("SHA256 = %q, want %q", result.SHA256, hex.EncodeToString(wantHash[:]))
	}
	got, err := os.ReadFile(result.Path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(got) != string(payload) {
		t.Errorf("cached content = %q, want %q", got, payload)
	}
}

func TestDownloaderSetsUserAgent(t *testing.T) {
	client := clientForHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("User-Agent"); got != "sec-monitor-test contact@example.com" {
			t.Fatalf("User-Agent = %q", got)
		}
		_, _ = w.Write([]byte("ok"))
	}))
	d := Downloader{Client: client, CacheDir: t.TempDir(), MaxBytes: 1024, UserAgent: "sec-monitor-test contact@example.com"}
	if _, err := d.Download(context.Background(), "https://example.test/source", "ua-test", nil); err != nil {
		t.Fatal(err)
	}
}

func TestDownloaderFallsBackToCachedFileOnRequestFailure(t *testing.T) {
	cacheDir := t.TempDir()
	cachePath := filepath.Join(cacheDir, "cached-source")
	if err := os.WriteFile(cachePath, []byte("cached payload"), 0o644); err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("temporary network failure")
	})}
	result, err := (&Downloader{Client: client, CacheDir: cacheDir, MaxBytes: 1024}).Download(context.Background(), "https://example.test/source", "cached-source", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !result.NotModified || result.Path != cachePath || result.Size != int64(len("cached payload")) {
		t.Fatalf("cached result = %#v", result)
	}
}

func TestDownloaderReusesFreshCacheWithoutRequest(t *testing.T) {
	var calls atomic.Int32
	client := &http.Client{Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		calls.Add(1)
		return responseForRequest(request, http.StatusOK, "fresh payload"), nil
	})}
	d := &Downloader{Client: client, CacheDir: t.TempDir(), MaxBytes: 1024}
	if _, err := d.Download(context.Background(), "https://example.test/source", "fresh-cache", nil); err != nil {
		t.Fatal(err)
	}
	result, err := d.DownloadWithCacheTTL(context.Background(), "https://example.test/source", "fresh-cache", nil, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if !result.NotModified {
		t.Fatal("fresh cache result should be marked not modified")
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("transport calls = %d, want 1", got)
	}
}

func TestDownloaderStopsStalledTransferAfterIdleTimeout(t *testing.T) {
	body := &blockingReadCloser{closed: make(chan struct{})}
	client := &http.Client{Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: body, Request: request}, nil
	})}
	d := &Downloader{Client: client, CacheDir: t.TempDir(), MaxBytes: 1024, ReadIdleTimeout: 20 * time.Millisecond}
	_, err := d.Download(context.Background(), "https://example.test/source", "stalled", nil)
	if err == nil || !strings.Contains(err.Error(), "made no progress") {
		t.Fatalf("Download() error = %v, want idle timeout", err)
	}
	select {
	case <-body.closed:
	case <-time.After(time.Second):
		t.Fatal("stalled response body was not closed")
	}
}

type blockingReadCloser struct {
	closed chan struct{}
	once   sync.Once
}

func (r *blockingReadCloser) Read([]byte) (int, error) {
	<-r.closed
	return 0, io.ErrUnexpectedEOF
}

func (r *blockingReadCloser) Close() error {
	r.once.Do(func() { close(r.closed) })
	return nil
}

func TestDownloaderReturnsRedirectFinalURL(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/start", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/final", http.StatusFound)
	})
	mux.HandleFunc("/final", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "ok")
	})

	d := Downloader{Client: clientForHandler(mux), CacheDir: t.TempDir(), MaxBytes: 10}
	result, err := d.Download(context.Background(), "https://example.test/start", "redirect", nil)
	if err != nil {
		t.Fatalf("Download() error = %v", err)
	}
	if result.FinalURL != "https://example.test/final" {
		t.Errorf("FinalURL = %q, want redirected URL", result.FinalURL)
	}
}

func TestDownloaderConditionalNotModified(t *testing.T) {
	cacheDir := t.TempDir()
	cachePath := filepath.Join(cacheDir, "symbols")
	content := []byte("old")
	if err := os.WriteFile(cachePath, content, 0o600); err != nil {
		t.Fatal(err)
	}
	wantHash := sha256.Sum256(content)
	sourceURL := "https://example.test/symbols"
	prior := &CacheMetadata{
		Path: cachePath, SourceURL: sourceURL, CacheKey: "symbols", FinalURL: "https://cached.example/final", ETag: `"old"`,
		LastModified: "Sun, 21 Jun 2026 12:00:00 GMT", SHA256: hex.EncodeToString(wantHash[:]), ContentType: "text/plain", Size: 3,
	}
	client := clientForHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("If-None-Match"); got != prior.ETag {
			t.Errorf("If-None-Match = %q, want %q", got, prior.ETag)
		}
		if got := r.Header.Get("If-Modified-Since"); got != prior.LastModified {
			t.Errorf("If-Modified-Since = %q, want %q", got, prior.LastModified)
		}
		w.Header().Set("ETag", `"new"`)
		w.Header().Set("Last-Modified", "Mon, 22 Jun 2026 12:00:00 GMT")
		w.WriteHeader(http.StatusNotModified)
	}))

	d := Downloader{Client: client, CacheDir: cacheDir, MaxBytes: 10}
	result, err := d.Download(context.Background(), sourceURL, "symbols", prior)
	if err != nil {
		t.Fatalf("Download() error = %v", err)
	}
	if !result.NotModified {
		t.Fatal("NotModified = false, want true")
	}
	if result.Path != prior.Path || result.SourceURL != sourceURL || result.CacheKey != "symbols" || result.FinalURL != sourceURL || result.ETag != `"new"` ||
		result.LastModified != "Mon, 22 Jun 2026 12:00:00 GMT" || result.SHA256 != prior.SHA256 ||
		result.ContentType != prior.ContentType || result.Size != prior.Size {
		t.Errorf("result = %+v, want prior metadata %+v", result, prior)
	}
}

func TestDownloaderNotModifiedRequiresCachedFile(t *testing.T) {
	client := clientForHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotModified)
	}))

	cacheDir := t.TempDir()
	sourceURL := "https://example.test/symbols"
	prior := &CacheMetadata{Path: filepath.Join(cacheDir, "symbols"), SourceURL: sourceURL, CacheKey: "symbols", ETag: `"old"`}
	d := Downloader{Client: client, CacheDir: cacheDir, MaxBytes: 10}
	_, err := d.Download(context.Background(), sourceURL, "symbols", prior)
	if err == nil || !strings.Contains(err.Error(), "cached file") {
		t.Fatalf("Download() error = %v, want missing cached file error", err)
	}
}

func TestDownloaderNotModifiedRejectsUntrustedCacheMetadata(t *testing.T) {
	const sourceURL = "https://user-secret:password@example.test/symbols?token=query-secret"
	client := clientForHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotModified)
	}))

	tests := []struct {
		name   string
		mutate func(*CacheMetadata, string)
	}{
		{"wrong path", func(prior *CacheMetadata, cacheDir string) {
			prior.Path = filepath.Join(cacheDir, "other")
			if err := os.WriteFile(prior.Path, []byte("trusted"), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{"wrong key", func(prior *CacheMetadata, _ string) { prior.CacheKey = "other" }},
		{"wrong source", func(prior *CacheMetadata, _ string) { prior.SourceURL = "https://other-secret@example.test/source" }},
		{"wrong size", func(prior *CacheMetadata, _ string) { prior.Size++ }},
		{"wrong hash", func(prior *CacheMetadata, _ string) { prior.SHA256 = strings.Repeat("0", 64) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cacheDir := t.TempDir()
			prior := writeCacheMetadata(t, cacheDir, "symbols", sourceURL, []byte("trusted"))
			tt.mutate(prior, cacheDir)
			d := Downloader{Client: client, CacheDir: cacheDir, MaxBytes: 100}
			_, err := d.Download(context.Background(), sourceURL, "symbols", prior)
			if err == nil || !strings.Contains(err.Error(), "cached file") {
				t.Fatalf("Download() error = %v, want cached file validation error", err)
			}
			assertErrorDoesNotContain(t, err, "user-secret", "password", "query-secret", "other-secret", sourceURL)
		})
	}
}

func TestDownloaderNotModifiedRejectsSymlink(t *testing.T) {
	cacheDir := t.TempDir()
	const sourceURL = "https://example.test/symbols"
	target := filepath.Join(cacheDir, "target")
	if err := os.WriteFile(target, []byte("trusted"), 0o600); err != nil {
		t.Fatal(err)
	}
	cachePath := filepath.Join(cacheDir, "symbols")
	if err := os.Symlink(target, cachePath); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	prior := metadataForContent(cachePath, "symbols", sourceURL, []byte("trusted"))
	client := clientForHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotModified)
	}))
	d := Downloader{Client: client, CacheDir: cacheDir, MaxBytes: 100}
	if _, err := d.Download(context.Background(), sourceURL, "symbols", prior); err == nil {
		t.Fatal("Download() error = nil, want symlink rejection")
	}
}

func TestDownloaderRejectsFailuresAndClosesBodies(t *testing.T) {
	t.Run("non-2xx", func(t *testing.T) {
		client := clientForHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadGateway)
			_, _ = io.WriteString(w, "secret response body")
		}))
		d := Downloader{Client: client, CacheDir: t.TempDir(), MaxBytes: 10}
		_, err := d.Download(context.Background(), "https://example.test/status", "status", nil)
		if err == nil || !strings.Contains(err.Error(), "502") || strings.Contains(err.Error(), "secret response body") {
			t.Fatalf("Download() error = %v", err)
		}
		if !IsDownloadHTTPStatus(err, http.StatusBadGateway) {
			t.Fatalf("Download() error = %v, want typed HTTP status", err)
		}
	})

	t.Run("network error", func(t *testing.T) {
		cause := errors.New("transport failed")
		client := &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
			return nil, cause
		})}
		d := Downloader{Client: client, CacheDir: t.TempDir(), MaxBytes: 10}
		_, err := d.Download(context.Background(), "https://user-secret:password@example.test/file?token=query-secret", "network", nil)
		if err == nil {
			t.Fatal("Download() error = nil")
		}
		if !errors.Is(err, cause) {
			t.Fatalf("errors.Is(%v, cause) = false", err)
		}
		assertErrorDoesNotContain(t, err, "user-secret", "password", "query-secret", "/file", "https://")
	})

	t.Run("stream error closes response body", func(t *testing.T) {
		body := &trackingBody{reader: errorReader{}}
		client := &http.Client{Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Body: body, Header: make(http.Header), Request: request}, nil
		})}
		d := Downloader{Client: client, CacheDir: t.TempDir(), MaxBytes: 10}
		_, err := d.Download(context.Background(), "https://example.test/file", "stream", nil)
		if err == nil {
			t.Fatal("Download() error = nil")
		}
		if !body.closed {
			t.Fatal("response body was not closed")
		}
	})

	t.Run("invalid URL", func(t *testing.T) {
		d := Downloader{Client: http.DefaultClient, CacheDir: t.TempDir(), MaxBytes: 10}
		_, err := d.Download(context.Background(), "://user-secret?token=query-secret", "invalid", nil)
		if err == nil {
			t.Fatal("Download() error = nil")
		}
		assertErrorDoesNotContain(t, err, "user-secret", "query-secret", "://")
	})

	t.Run("invalid maximum", func(t *testing.T) {
		d := Downloader{Client: http.DefaultClient, CacheDir: t.TempDir(), MaxBytes: 0}
		if _, err := d.Download(context.Background(), "https://example.test", "invalid", nil); err == nil {
			t.Fatal("Download() error = nil")
		}
	})
}

func TestDownloaderHonorsContextTimeoutAndCancel(t *testing.T) {
	tests := []struct {
		name string
		ctx  func() (context.Context, context.CancelFunc)
	}{
		{"timeout", func() (context.Context, context.CancelFunc) {
			return context.WithTimeout(context.Background(), 20*time.Millisecond)
		}},
		{"cancel", func() (context.Context, context.CancelFunc) {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			return ctx, func() {}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := tt.ctx()
			defer cancel()
			client := &http.Client{Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
				<-request.Context().Done()
				return nil, request.Context().Err()
			})}
			d := Downloader{Client: client, CacheDir: t.TempDir(), MaxBytes: 10}
			_, err := d.Download(ctx, "https://user-secret:password@example.test/context?token=query-secret", "context", nil)
			if err == nil {
				t.Fatal("Download() error = nil")
			}
			if !errors.Is(err, ctx.Err()) {
				t.Fatalf("errors.Is(%v, %v) = false", err, ctx.Err())
			}
			assertErrorDoesNotContain(t, err, "user-secret", "password", "query-secret", "/context", "https://")
		})
	}
}

func TestDownloaderMaxInt64LimitDoesNotOverflow(t *testing.T) {
	payload := []byte("nonempty")
	client := clientForHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(payload)
	}))
	d := Downloader{Client: client, CacheDir: t.TempDir(), MaxBytes: math.MaxInt64}

	result, err := d.Download(context.Background(), "https://example.test/max", "max-int64", nil)
	if err != nil {
		t.Fatalf("Download() error = %v", err)
	}
	wantHash := sha256.Sum256(payload)
	if result.Size != int64(len(payload)) {
		t.Errorf("Size = %d, want %d", result.Size, len(payload))
	}
	if result.SHA256 != hex.EncodeToString(wantHash[:]) {
		t.Errorf("SHA256 = %q, want %q", result.SHA256, hex.EncodeToString(wantHash[:]))
	}
	assertFileContent(t, result.Path, string(payload))
}

func TestDownloaderMaxBytesAndAtomicReplacement(t *testing.T) {
	cacheDir := t.TempDir()
	cachePath := filepath.Join(cacheDir, "atomic")
	if err := os.WriteFile(cachePath, []byte("old-good"), 0o600); err != nil {
		t.Fatal(err)
	}

	oversized := clientForHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "too-large")
	}))
	d := Downloader{Client: oversized, CacheDir: cacheDir, MaxBytes: 4}
	if _, err := d.Download(context.Background(), "https://example.test/oversized", "atomic", nil); err == nil {
		t.Fatal("oversized Download() error = nil")
	}
	assertFileContent(t, cachePath, "old-good")
	assertNoTempFiles(t, cacheDir)

	success := clientForHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "new")
	}))
	d.Client = success
	if _, err := d.Download(context.Background(), "https://example.test/success", "atomic", nil); err != nil {
		t.Fatalf("successful Download() error = %v", err)
	}
	assertFileContent(t, cachePath, "new")
	assertNoTempFiles(t, cacheDir)
}

func TestDownloaderRejectsNon200ContentResponses(t *testing.T) {
	for _, status := range []int{http.StatusNoContent, http.StatusPartialContent} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			cacheDir := t.TempDir()
			cachePath := filepath.Join(cacheDir, "status")
			if err := os.WriteFile(cachePath, []byte("old-good"), 0o600); err != nil {
				t.Fatal(err)
			}
			client := clientForHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(status)
				_, _ = io.WriteString(w, "replacement")
			}))
			d := Downloader{Client: client, CacheDir: cacheDir, MaxBytes: 100}
			if _, err := d.Download(context.Background(), "https://example.test/status", "status", nil); err == nil {
				t.Fatal("Download() error = nil")
			}
			assertFileContent(t, cachePath, "old-good")
		})
	}
}

func TestDownloaderSerializesSameCacheKey(t *testing.T) {
	var calls atomic.Int32
	var active atomic.Int32
	var overlapped atomic.Bool
	firstEntered := make(chan struct{})
	releaseFirst := make(chan struct{})
	secondEntered := make(chan struct{})
	releaseSecond := make(chan struct{})
	client := &http.Client{Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		call := calls.Add(1)
		if active.Add(1) > 1 {
			overlapped.Store(true)
		}
		defer active.Add(-1)
		if call == 1 {
			close(firstEntered)
			<-releaseFirst
		} else {
			close(secondEntered)
			<-releaseSecond
		}
		body := fmt.Sprintf("content-%d", call)
		return responseForRequest(request, http.StatusOK, body), nil
	})}
	d := &Downloader{Client: client, CacheDir: t.TempDir(), MaxBytes: 100}

	type outcome struct {
		result       DownloadResult
		contentMatch bool
		err          error
	}
	results := make(chan outcome, 2)
	download := func() {
		result, err := d.Download(context.Background(), "https://example.test/source", "same", nil)
		match := false
		if err == nil {
			content, readErr := os.ReadFile(result.Path)
			if readErr != nil {
				err = readErr
			} else {
				hash := sha256.Sum256(content)
				match = result.SHA256 == hex.EncodeToString(hash[:])
			}
		}
		results <- outcome{result, match, err}
	}
	go func() {
		download()
	}()
	<-firstEntered
	secondStarted := make(chan struct{})
	go func() {
		close(secondStarted)
		download()
	}()
	<-secondStarted
	time.Sleep(20 * time.Millisecond)
	close(releaseFirst)

	seenHashes := map[string]bool{}
	first := <-results
	select {
	case <-secondEntered:
	case <-time.After(time.Second):
		t.Fatal("second same-key request did not start after first completed")
	}
	close(releaseSecond)
	for _, outcome := range []outcome{first, <-results} {
		if outcome.err != nil {
			t.Fatalf("Download() error = %v", outcome.err)
		}
		if !outcome.contentMatch {
			t.Fatal("completed result hash did not match cached content at return")
		}
		seenHashes[outcome.result.SHA256] = true
	}
	if overlapped.Load() {
		t.Fatal("same-key transports overlapped")
	}
	for _, content := range []string{"content-1", "content-2"} {
		hash := sha256.Sum256([]byte(content))
		if !seenHashes[hex.EncodeToString(hash[:])] {
			t.Errorf("missing completed result for %q", content)
		}
	}
}

func TestDownloaderDoesNotSerializeDifferentCacheKeys(t *testing.T) {
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	client := &http.Client{Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		entered <- struct{}{}
		<-release
		return responseForRequest(request, http.StatusOK, request.URL.Path), nil
	})}
	d := &Downloader{Client: client, CacheDir: t.TempDir(), MaxBytes: 100}
	errs := make(chan error, 2)
	for _, key := range []string{"one", "two"} {
		go func(key string) {
			_, err := d.Download(context.Background(), "https://example.test/"+key, key, nil)
			errs <- err
		}(key)
	}
	for range 2 {
		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatal("different cache keys were serialized")
		}
	}
	close(release)
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatalf("Download() error = %v", err)
		}
	}
}

func TestDownloaderCanceledWhileWaitingForSameCacheKey(t *testing.T) {
	firstEntered := make(chan struct{})
	releaseFirst := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseFirst) }) }
	defer release()
	var transportCalls atomic.Int32
	client := &http.Client{Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		if transportCalls.Add(1) == 1 {
			close(firstEntered)
			<-releaseFirst
		}
		return responseForRequest(request, http.StatusOK, "content"), nil
	})}
	d := &Downloader{Client: client, CacheDir: t.TempDir(), MaxBytes: 100}

	firstResult := make(chan error, 1)
	go func() {
		_, err := d.Download(context.Background(), "https://example.test/source", "same", nil)
		firstResult <- err
	}()
	<-firstEntered

	ctx, cancel := context.WithCancel(context.Background())
	secondStarted := make(chan struct{})
	secondResult := make(chan error, 1)
	go func() {
		close(secondStarted)
		_, err := d.Download(ctx, "https://example.test/source", "same", nil)
		secondResult <- err
	}()
	<-secondStarted
	cancel()

	select {
	case err := <-secondResult:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Download() error = %v, want context.Canceled", err)
		}
	case <-time.After(200 * time.Millisecond):
		release()
		err := <-secondResult
		t.Fatalf("canceled waiter returned only after active download released: %v", err)
	}
	if got := transportCalls.Load(); got != 1 {
		t.Fatalf("transport calls = %d, want 1 before active release", got)
	}

	release()
	if err := <-firstResult; err != nil {
		t.Fatalf("first Download() error = %v", err)
	}
	for i := range 12 {
		key := fmt.Sprintf("repeat-%d", i%3)
		if _, err := d.Download(context.Background(), "https://example.test/source", key, nil); err != nil {
			t.Fatalf("repeated Download(%q) error = %v", key, err)
		}
	}
	d.locksMu.Lock()
	lockCount := len(d.locks)
	d.locksMu.Unlock()
	if lockCount != 0 {
		t.Fatalf("keyed lock entries = %d, want 0", lockCount)
	}
}

func TestDownloaderRejectsUnsafeCacheKeys(t *testing.T) {
	tests := []string{
		"", ".", "..", "../escape", "a/b", `a\b`, "/absolute", "two words", "line\nbreak", "unicode-é", "trailing.",
		"CON", "con.txt", "PrN.data", "AUX", "NUL.log", "COM1", "com9.zip", "LPT1", "lpt9.csv",
	}
	for _, key := range tests {
		t.Run(key, func(t *testing.T) {
			d := Downloader{Client: http.DefaultClient, CacheDir: t.TempDir(), MaxBytes: 10}
			if _, err := d.Download(context.Background(), "https://example.test", key, nil); err == nil {
				t.Fatalf("Download(cacheKey=%q) error = nil", key)
			}
		})
	}
}

func TestSafeZIPAcceptsNestedEntries(t *testing.T) {
	path := makeZIP(t, []zipEntry{{"nested/", true, 0}, {"nested/file.txt", false, 4}})
	zr, err := OpenSafeZIP(path, 2, 4)
	if err != nil {
		t.Fatalf("OpenSafeZIP() error = %v", err)
	}
	if len(zr.File) != 2 {
		t.Errorf("len(File) = %d, want 2", len(zr.File))
	}
	if err := zr.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

func TestSafeZIPRejectsUnsafeNames(t *testing.T) {
	tests := []string{
		".",
		"dir/..",
		"/absolute.txt",
		"../escape.txt",
		"nested/../../escape.txt",
		`..\escape.txt`,
		`nested\..\..\escape.txt`,
		`C:\absolute.txt`,
		`C:drive-relative.txt`,
	}
	for _, name := range tests {
		t.Run(name, func(t *testing.T) {
			path := makeZIP(t, []zipEntry{{name, false, 1}})
			if zr, err := OpenSafeZIP(path, 2, 10); err == nil {
				_ = zr.Close()
				t.Fatalf("OpenSafeZIP(%q) error = nil", name)
			}
		})
	}
}

func TestSafeZIPRejectsSpecialModes(t *testing.T) {
	tests := []struct {
		name string
		mode os.FileMode
	}{
		{"symlink", os.ModeSymlink | 0o777},
		{"named pipe", os.ModeNamedPipe | 0o600},
		{"device", os.ModeDevice | 0o600},
		{"socket", os.ModeSocket | 0o600},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			archivePath := makeZIPWithMode(t, "special", tt.mode)
			if zr, err := OpenSafeZIP(archivePath, 1, 10); err == nil {
				_ = zr.Close()
				t.Fatal("OpenSafeZIP() error = nil")
			}
		})
	}
}

func TestSafeZIPRejectsLimits(t *testing.T) {
	t.Run("invalid arguments", func(t *testing.T) {
		path := makeZIP(t, []zipEntry{{"file", false, 1}})
		for _, tc := range []struct {
			entries int
			bytes   int64
		}{{0, 1}, {1, 0}, {-1, 1}, {1, -1}} {
			if zr, err := OpenSafeZIP(path, tc.entries, tc.bytes); err == nil {
				_ = zr.Close()
				t.Fatalf("OpenSafeZIP(%d, %d) error = nil", tc.entries, tc.bytes)
			}
		}
	})

	t.Run("entry count", func(t *testing.T) {
		path := makeZIP(t, []zipEntry{{"one", false, 1}, {"two", false, 1}})
		if zr, err := OpenSafeZIP(path, 1, 10); err == nil {
			_ = zr.Close()
			t.Fatal("OpenSafeZIP() error = nil")
		}
	})

	t.Run("single entry uncompressed size", func(t *testing.T) {
		path := makeZIP(t, []zipEntry{{"large", false, 11}})
		if zr, err := OpenSafeZIP(path, 1, 10); err == nil {
			_ = zr.Close()
			t.Fatal("OpenSafeZIP() error = nil")
		}
	})

	t.Run("aggregate uncompressed size", func(t *testing.T) {
		path := makeZIP(t, []zipEntry{{"one", false, 6}, {"two", false, 5}})
		if zr, err := OpenSafeZIP(path, 2, 10); err == nil {
			_ = zr.Close()
			t.Fatal("OpenSafeZIP() error = nil")
		}
	})
}

func TestSafeZIPRejectsMalformedArchive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.zip")
	if err := os.WriteFile(path, []byte("not a zip"), 0o600); err != nil {
		t.Fatal(err)
	}
	if zr, err := OpenSafeZIP(path, 1, 10); err == nil {
		_ = zr.Close()
		t.Fatal("OpenSafeZIP() error = nil")
	}
}

type trackingBody struct {
	reader io.Reader
	closed bool
}

func (b *trackingBody) Read(p []byte) (int, error) { return b.reader.Read(p) }
func (b *trackingBody) Close() error               { b.closed = true; return nil }

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func clientForHandler(handler http.Handler) *http.Client {
	return &http.Client{Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		response := recorder.Result()
		response.Request = request
		return response, nil
	})}
}

type errorReader struct{}

func (errorReader) Read([]byte) (int, error) { return 0, errors.New("read failed") }

type zipEntry struct {
	name string
	dir  bool
	size int
}

func makeZIP(t *testing.T, entries []zipEntry) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.zip")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(file)
	for _, entry := range entries {
		header := &zip.FileHeader{Name: entry.name, Method: zip.Deflate}
		if entry.dir {
			header.SetMode(os.ModeDir | 0o755)
		}
		writer, err := zw.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		if !entry.dir {
			if _, err := writer.Write([]byte(strings.Repeat("x", entry.size))); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func makeZIPWithMode(t *testing.T, name string, mode os.FileMode) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "mode.zip")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(file)
	header := &zip.FileHeader{Name: name, Method: zip.Store}
	header.SetMode(mode)
	if _, err := zw.CreateHeader(header); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeCacheMetadata(t *testing.T, cacheDir, cacheKey, sourceURL string, content []byte) *CacheMetadata {
	t.Helper()
	path := filepath.Join(cacheDir, cacheKey)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	return metadataForContent(path, cacheKey, sourceURL, content)
}

func metadataForContent(path, cacheKey, sourceURL string, content []byte) *CacheMetadata {
	hash := sha256.Sum256(content)
	return &CacheMetadata{
		Path: path, SourceURL: sourceURL, CacheKey: cacheKey, FinalURL: sourceURL,
		SHA256: hex.EncodeToString(hash[:]), Size: int64(len(content)), ContentType: "text/plain",
	}
}

func responseForRequest(request *http.Request, status int, body string) *http.Response {
	response := httptest.NewRecorder()
	response.WriteHeader(status)
	_, _ = io.WriteString(response, body)
	result := response.Result()
	result.Request = request
	return result
}

func assertFileContent(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Fatalf("file content = %q, want %q", got, want)
	}
}

func assertNoTempFiles(t *testing.T, cacheDir string) {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(cacheDir, ".download-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("temporary files remain: %v", matches)
	}
}

func assertErrorDoesNotContain(t *testing.T, err error, secrets ...string) {
	t.Helper()
	for _, secret := range secrets {
		if strings.Contains(err.Error(), secret) {
			t.Errorf("error %q exposes sensitive URL component %q", err, secret)
		}
	}
}
