package discovery

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	DiscoveryStorageWarnBytes  int64 = 5 << 30
	DiscoveryStorageErrorBytes int64 = 15 << 30
)

// StorageHealth makes local SQLite and download-cache growth observable. It
// intentionally reports physical files rather than database page statistics,
// because WAL and the SEC cache consume the same user-managed disk volume.
type StorageHealth struct {
	DatabasePath  string   `json:"database_path"`
	DatabaseBytes int64    `json:"database_bytes"`
	CachePath     string   `json:"cache_path"`
	CacheBytes    int64    `json:"cache_bytes"`
	CacheFiles    int64    `json:"cache_files"`
	Status        string   `json:"status"`
	Issues        []string `json:"issues"`
}

type CacheCleanupPreview struct {
	RetentionDays int       `json:"retention_days"`
	Cutoff        time.Time `json:"cutoff"`
	FileCount     int64     `json:"file_count"`
	Bytes         int64     `json:"bytes"`
}

func InspectStorage(databaseDSN, cacheDir string) (StorageHealth, error) {
	databasePath := sqliteDSNPath(databaseDSN)
	result := StorageHealth{DatabasePath: databasePath, CachePath: cacheDir, Status: "ok", Issues: []string{}}
	if databasePath != "" {
		bytes, err := sqlitePhysicalSize(databasePath)
		if err != nil {
			return result, err
		}
		result.DatabaseBytes = bytes
	}
	bytes, files, err := directorySize(cacheDir)
	if err != nil {
		return result, err
	}
	result.CacheBytes = bytes
	result.CacheFiles = files
	if result.DatabaseBytes >= DiscoveryStorageErrorBytes {
		result.Status = "error"
		result.Issues = append(result.Issues, "研究库超过 15GB，应尽快执行快照保留治理并评估迁移 PostgreSQL")
	} else if result.DatabaseBytes >= DiscoveryStorageWarnBytes {
		result.Status = "warning"
		result.Issues = append(result.Issues, "研究库超过 5GB 容量告警线；建议检查历史批次保留策略")
	}
	if result.CacheBytes >= DiscoveryStorageWarnBytes {
		if result.Status == "ok" {
			result.Status = "warning"
		}
		result.Issues = append(result.Issues, "SEC 下载缓存超过 5GB，可预览并清理过期缓存；不会删除研究结论")
	}
	return result, nil
}

func PreviewCacheCleanup(cacheDir string, retentionDays int, now time.Time) (CacheCleanupPreview, error) {
	if retentionDays <= 0 {
		return CacheCleanupPreview{}, errors.New("cache retention days must be greater than 0")
	}
	result := CacheCleanupPreview{RetentionDays: retentionDays, Cutoff: now.UTC().AddDate(0, 0, -retentionDays)}
	err := walkCacheFiles(cacheDir, func(path string, info fs.FileInfo) error {
		if info.ModTime().UTC().Before(result.Cutoff) {
			result.FileCount++
			result.Bytes += info.Size()
		}
		return nil
	})
	return result, err
}

// CleanupExpiredCache deletes only cache files older than the configured
// retention horizon. It never touches the SQLite database or active files.
func CleanupExpiredCache(cacheDir string, retentionDays int, now time.Time) (CacheCleanupPreview, error) {
	preview, err := PreviewCacheCleanup(cacheDir, retentionDays, now)
	if err != nil || preview.FileCount == 0 {
		return preview, err
	}
	if err := walkCacheFiles(cacheDir, func(path string, info fs.FileInfo) error {
		if !info.ModTime().UTC().Before(preview.Cutoff) {
			return nil
		}
		return os.Remove(path)
	}); err != nil {
		return CacheCleanupPreview{}, err
	}
	_ = removeEmptyDirectories(cacheDir)
	return preview, nil
}

func sqliteDSNPath(dsn string) string {
	path := strings.TrimSpace(strings.SplitN(dsn, "?", 2)[0])
	if strings.HasPrefix(path, "file:") {
		path = strings.TrimPrefix(path, "file:")
	}
	return path
}

func sqlitePhysicalSize(path string) (int64, error) {
	var total int64
	for _, suffix := range []string{"", "-wal", "-shm"} {
		info, err := os.Stat(path + suffix)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return 0, err
		}
		total += info.Size()
	}
	return total, nil
}

func directorySize(path string) (int64, int64, error) {
	var bytes, files int64
	err := walkCacheFiles(path, func(_ string, info fs.FileInfo) error {
		bytes += info.Size()
		files++
		return nil
	})
	return bytes, files, err
}

func walkCacheFiles(root string, visit func(string, fs.FileInfo) error) error {
	if strings.TrimSpace(root) == "" {
		return nil
	}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		return visit(path, info)
	})
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func removeEmptyDirectories(root string) error {
	var directories []string
	if err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || !entry.IsDir() || path == root {
			return err
		}
		directories = append(directories, path)
		return nil
	}); err != nil {
		return err
	}
	for index := len(directories) - 1; index >= 0; index-- {
		_ = os.Remove(directories[index])
	}
	return nil
}
