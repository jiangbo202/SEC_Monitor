package discovery

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestInspectStorageIncludesSQLiteSidecarsAndCache(t *testing.T) {
	dir := t.TempDir()
	databasePath := filepath.Join(dir, "small_cap.db")
	cacheDir := filepath.Join(dir, "cache")
	if err := os.WriteFile(databasePath, make([]byte, 10), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(databasePath+"-wal", make([]byte, 4), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cacheDir, "form4.xml"), make([]byte, 7), 0o600); err != nil {
		t.Fatal(err)
	}
	health, err := InspectStorage(databasePath, cacheDir)
	if err != nil {
		t.Fatal(err)
	}
	if health.DatabaseBytes != 14 || health.CacheBytes != 7 || health.CacheFiles != 1 || health.Status != "ok" {
		t.Fatalf("storage health = %#v", health)
	}
}

func TestCacheCleanupOnlyDeletesExpiredFiles(t *testing.T) {
	dir := t.TempDir()
	oldPath := filepath.Join(dir, "old", "form4.xml")
	newPath := filepath.Join(dir, "new.xml")
	if err := os.MkdirAll(filepath.Dir(oldPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(oldPath, make([]byte, 8), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(newPath, make([]byte, 5), 0o600); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 19, 0, 0, 0, 0, time.UTC)
	oldTime := now.AddDate(0, 0, -15)
	if err := os.Chtimes(oldPath, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	preview, err := PreviewCacheCleanup(dir, 14, now)
	if err != nil {
		t.Fatal(err)
	}
	if preview.FileCount != 1 || preview.Bytes != 8 {
		t.Fatalf("preview = %#v", preview)
	}
	result, err := CleanupExpiredCache(dir, 14, now)
	if err != nil {
		t.Fatal(err)
	}
	if result.FileCount != 1 {
		t.Fatalf("cleanup result = %#v", result)
	}
	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Fatalf("old file stat error = %v, want not exist", err)
	}
	if _, err := os.Stat(newPath); err != nil {
		t.Fatalf("new file stat error = %v", err)
	}
}
