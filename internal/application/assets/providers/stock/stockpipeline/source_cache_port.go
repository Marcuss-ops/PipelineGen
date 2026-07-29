// Package stockpipeline — source_cache_port.go
//
// Defines the application-layer ports for the cross-run source download
// cache. The StockStager uses these ports to avoid downloading the same
// YouTube/Drive video multiple times across pipeline runs.
//
// godlike/06 SSOT: the concrete implementation lives in
// internal/infrastructure/database/sqlite/stocksourcecache.
// Composition root (wire_stock_pipeline.go) injects the concrete
// repository into the StockStager via WithSourceCache.
package stockpipeline

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"strings"

	"go.uber.org/zap"
)

// LocalFSPort is the Pattern 0 typed port for local filesystem I/O.
//
// godlike/07 PR-REFACTOR-P0-IO-BINDER (July 2026): the application
// layer MUST NOT import "os" or call os.* directly. Every file
// read/write/stat call goes through this port; the concrete
// implementation lives in internal/infrastructure/filesystem and
// is injected via the SourceCacheDeps.LocalFS dependency at
// composition time.
//
// SSOT: this interface lives only in this package (the only
// consumer across the stock pipeline). Concrete implementations
// (filesystem.LocalAdapter) implement it structurally.
type LocalFSPort interface {
	// Stat returns the FileInfo for the named file.
	Stat(name string) (os.FileInfo, error)
	// Open opens the named file for reading.
	Open(name string) (io.ReadCloser, error)
	// Create creates or truncates the named file for writing.
	Create(name string) (io.WriteCloser, error)
	// MkdirTemp creates a new temporary directory and returns its path.
	MkdirTemp(dir, pattern string) (string, error)
	// Remove removes the named file or (empty) directory.
	Remove(name string) error
	// RemoveAll removes path and any children it contains.
	RemoveAll(path string) error
	// MkdirAll creates a directory along with any necessary parents.
	MkdirAll(path string, perm os.FileMode) error
	// CreateTemp creates a new temporary file, returning its path
	// and a WriteCloser for writing. The caller must close the
	// returned WriteCloser before using the path for hashing.
	CreateTemp(dir, pattern string) (string, io.WriteCloser, error)
	// TempDir returns the default directory to use for temporary files.
	TempDir() string
}

// SourceCacheReader abstracts the read side of the source download cache.
// Concrete: stocksourcecache.Repository.
type SourceCacheReader interface {
	// GetByCacheKey returns the active cache entry for the given key.
	// Returns (nil, nil) on cache miss.
	GetByCacheKey(ctx context.Context, cacheKey string) (*SourceCacheEntry, error)
}

// SourceCacheWriter abstracts the write side of the source download cache.
// Concrete: stocksourcecache.Repository.
type SourceCacheWriter interface {
	// Upsert inserts or replaces a cache entry.
	Upsert(ctx context.Context, entry *SourceCacheEntry) error
	// Invalidate marks a cache entry as invalid (e.g., file missing).
	Invalidate(ctx context.Context, cacheKey string) error
}

// SourceCacheEntry is the application-layer DTO for a cached source
// download. It mirrors stocksourcecache.CacheEntry but lives in the
// application package so the stockpipeline package does not import
// infrastructure.
type SourceCacheEntry struct {
	CacheKey        string
	Provider        string
	ExternalID      string
	SourceURL       string
	LocalPath       string
	FileSize        int64
	FileHash        string
	DownloadSection string
	MergeFormat     string
	ForceKeyframes  bool
}

// DeriveSourceCacheKey computes a deterministic cache key from a source
// URL and download parameters. The key is a hex-encoded SHA-256 of the
// canonical input so the same logical source always hits the same cache
// entry regardless of download order.
//
// The input is: "stock-source:" + canonicalURL + "|" + downloadSection +
// "|" + mergeFormat + "|" + forceKeyframes. YouTube URLs are normalized
// to the canonical watch?v= form before hashing.
func DeriveSourceCacheKey(url, downloadSection, mergeFormat string, forceKeyframes bool) string {
	canon := normalizeSourceURL(url)
	fk := "false"
	if forceKeyframes {
		fk = "true"
	}
	input := fmt.Sprintf("stock-source:%s|%s|%s|%s", canon, downloadSection, mergeFormat, fk)
	h := sha256.Sum256([]byte(input))
	return fmt.Sprintf("%x", h)
}

// normalizeSourceURL canonicalizes YouTube URLs to the form
// https://www.youtube.com/watch?v=<id> by stripping query parameters
// and normalizing the host. Non-YouTube URLs are returned unchanged.
func normalizeSourceURL(raw string) string {
	raw = strings.TrimSpace(raw)
	lower := strings.ToLower(raw)
	if !strings.Contains(lower, "youtube.com") && !strings.Contains(lower, "youtu.be") {
		return raw
	}
	if id := extractVideoIDFromURL(raw); id != "" {
		return "https://www.youtube.com/watch?v=" + id
	}
	return raw
}

// extractVideoIDFromURL extracts the YouTube video ID from a URL.
// Supports youtube.com/watch?v=<id>, youtu.be/<id>, and bare IDs.
func extractVideoIDFromURL(raw string) string {
	raw = strings.TrimSpace(raw)
	lower := strings.ToLower(raw)

	// youtu.be/<id>
	if strings.Contains(lower, "youtu.be/") {
		if idx := strings.LastIndex(raw, "/"); idx >= 0 {
			id := strings.TrimSpace(raw[idx+1:])
			if id != "" {
				return id
			}
		}
	}

	// youtube.com/watch?v=<id>
	if strings.Contains(lower, "youtube.com") {
		if idx := strings.Index(lower, "v="); idx >= 0 {
			rest := raw[idx+2:]
			ampIdx := strings.IndexAny(rest, "&# \t\n")
			if ampIdx > 0 {
				return strings.TrimSpace(rest[:ampIdx])
			}
			return strings.TrimSpace(rest)
		}
	}

	return ""
}

// validateCacheHit checks that a cached file still exists on disk and
// has a non-zero size. Returns nil on valid hit, error on invalid.
//
// The LocalFSPort is the Pattern 0 typed port (PR-REFACTOR-P0-IO-BINDER);
// nil is fail-closed — without an FS port the cache cannot validate
// any entry and the caller falls through to the download path.
func validateCacheHit(entry *SourceCacheEntry, fs LocalFSPort, log *zap.Logger) error {
	if entry == nil {
		return fmt.Errorf("cache entry is nil")
	}
	if entry.LocalPath == "" {
		return fmt.Errorf("cache entry has empty local_path")
	}
	if fs == nil {
		return fmt.Errorf("cache validation: LocalFSPort not wired (composition root must inject filesystem.NewLocal())")
	}
	fi, err := fs.Stat(entry.LocalPath)
	if err != nil {
		if log != nil {
			log.Warn("stock source cache: file missing on disk",
				zap.String("cache_key", entry.CacheKey),
				zap.String("local_path", entry.LocalPath),
				zap.Error(err))
		}
		return fmt.Errorf("cached file missing: %w", err)
	}
	if fi.Size() == 0 {
		if log != nil {
			log.Warn("stock source cache: file is zero bytes",
				zap.String("cache_key", entry.CacheKey),
				zap.String("local_path", entry.LocalPath))
		}
		return fmt.Errorf("cached file is zero bytes")
	}
	if fi.Size() != entry.FileSize {
		if log != nil {
			log.Warn("stock source cache: file size mismatch",
				zap.String("cache_key", entry.CacheKey),
				zap.Int64("expected", entry.FileSize),
				zap.Int64("actual", fi.Size()))
		}
		return fmt.Errorf("cached file size mismatch: expected %d, got %d", entry.FileSize, fi.Size())
	}
	return nil
}

// copyFileToPath copies srcPath to dstPath. Used by the cache to
// stage a cached file into a new temp directory without holding the
// original locked.
//
// The LocalFSPort is the Pattern 0 typed port (PR-REFACTOR-P0-IO-BINDER);
// nil is fail-closed so callers surface the wiring gap instead of
// silently returning a partial file.
//
// io.Copy handles the 32KB buffer + EOF semantics internally; the
// close-then-Close-error return pattern preserves the explicit
// flush the previous implementation provided.
func copyFileToPath(srcPath, dstPath string, fs LocalFSPort) error {
	if fs == nil {
		return fmt.Errorf("cache copy: LocalFSPort not wired (composition root must inject filesystem.NewLocal())")
	}
	src, err := fs.Open(srcPath)
	if err != nil {
		return err
	}
	defer src.Close()

	dst, err := fs.Create(dstPath)
	if err != nil {
		return err
	}

	if _, err := io.Copy(dst, src); err != nil {
		_ = dst.Close()
		return err
	}
	return dst.Close()
}
