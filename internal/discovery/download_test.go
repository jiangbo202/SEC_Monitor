package discovery

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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
	if err := os.WriteFile(cachePath, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	prior := &CacheMetadata{
		Path: cachePath, FinalURL: "https://cached.example/final", ETag: `"old"`,
		LastModified: "Sun, 21 Jun 2026 12:00:00 GMT", SHA256: "abc", ContentType: "text/plain", Size: 3,
	}
	client := clientForHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("If-None-Match"); got != prior.ETag {
			t.Errorf("If-None-Match = %q, want %q", got, prior.ETag)
		}
		if got := r.Header.Get("If-Modified-Since"); got != prior.LastModified {
			t.Errorf("If-Modified-Since = %q, want %q", got, prior.LastModified)
		}
		w.WriteHeader(http.StatusNotModified)
	}))

	d := Downloader{Client: client, CacheDir: cacheDir, MaxBytes: 10}
	result, err := d.Download(context.Background(), "https://example.test/symbols", "symbols", prior)
	if err != nil {
		t.Fatalf("Download() error = %v", err)
	}
	if !result.NotModified {
		t.Fatal("NotModified = false, want true")
	}
	if result.Path != prior.Path || result.FinalURL != prior.FinalURL || result.ETag != prior.ETag ||
		result.LastModified != prior.LastModified || result.SHA256 != prior.SHA256 ||
		result.ContentType != prior.ContentType || result.Size != prior.Size {
		t.Errorf("result = %+v, want prior metadata %+v", result, prior)
	}
}

func TestDownloaderNotModifiedRequiresCachedFile(t *testing.T) {
	client := clientForHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotModified)
	}))

	prior := &CacheMetadata{Path: filepath.Join(t.TempDir(), "missing"), ETag: `"old"`}
	d := Downloader{Client: client, CacheDir: t.TempDir(), MaxBytes: 10}
	_, err := d.Download(context.Background(), "https://example.test/symbols", "symbols", prior)
	if err == nil || !strings.Contains(err.Error(), "cached file") {
		t.Fatalf("Download() error = %v, want missing cached file error", err)
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
	})

	t.Run("network error", func(t *testing.T) {
		client := &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
			return nil, errors.New("transport failed")
		})}
		d := Downloader{Client: client, CacheDir: t.TempDir(), MaxBytes: 10}
		_, err := d.Download(context.Background(), "https://example.test/file", "network", nil)
		if err == nil {
			t.Fatal("Download() error = nil")
		}
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
		if _, err := d.Download(context.Background(), "://bad", "invalid", nil); err == nil {
			t.Fatal("Download() error = nil")
		}
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
			_, err := d.Download(ctx, "https://example.test/context", "context", nil)
			if err == nil {
				t.Fatal("Download() error = nil")
			}
		})
	}
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

func TestDownloaderRejectsUnsafeCacheKeys(t *testing.T) {
	tests := []string{"", ".", "..", "../escape", "a/b", `a\b`, "/absolute", "two words", "line\nbreak"}
	for _, key := range tests {
		t.Run(key, func(t *testing.T) {
			d := Downloader{Client: http.DefaultClient, CacheDir: t.TempDir(), MaxBytes: 10}
			if _, err := d.Download(context.Background(), "https://example.test", key, nil); err == nil {
				t.Fatalf("Download(cacheKey=%q) error = nil", key)
			}
		})
	}
}

func TestOpenSafeZIPAcceptsNestedEntries(t *testing.T) {
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

func TestOpenSafeZIPRejectsUnsafeNames(t *testing.T) {
	tests := []string{
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

func TestOpenSafeZIPRejectsLimits(t *testing.T) {
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

func TestOpenSafeZIPRejectsMalformedArchive(t *testing.T) {
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
