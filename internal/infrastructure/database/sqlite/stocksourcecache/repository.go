// Package stocksourcecache — SQLite-backed source download cache for
// the stock pipeline.
//
// The cache avoids downloading the same YouTube/Drive video N times
// when multiple rounds reference the same source URL. Entries are
// keyed by a deterministic SHA-256 of (provider + canonical URL +
// download section + merge format + force keyframes).
//
// godlike/06 SSOT: this file is the SOLE concrete implementation of
// the stockpipeline.SourceCacheReader + SourceCacheWriter ports.
// Composition root (wire_stock_pipeline.go) injects the repository
// into the StockStager via WithSourceCache.
//
// Table DDL: migrations/sqlite/160_stock_source_cache.sql
package stocksourcecache

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/stock/stockpipeline"
)

// Repository is the SQLite-backed source cache store.
// Table is created by migration 160_stock_source_cache.sql.
type Repository struct {
	db *sql.DB
}

// Compile-time structural conformance pins: *Repository satisfies
// both stockpipeline.SourceCacheReader and SourceCacheWriter. Drift
// in either interface or the adapter is caught at compile time so a
// future refactor cannot silently break the typed-port contract.
//
// Inverse-direction rationale (godelike/06 SSO): repository.go
// lives in infrastructure and imports stockpipeline only because
// the typed ports are declared in the application package. The pin
// has no cycle risk because stockpipeline reaches the Repository
// only via the structural interface fields (Reader/Writer) — there
// is no concrete reference back to the SQLite adapter.
var (
	_ stockpipeline.SourceCacheReader = (*Repository)(nil)
	_ stockpipeline.SourceCacheWriter = (*Repository)(nil)
)

// NewRepository constructs the cache repository. db must be non-nil.
// Panics on nil db (fail-fast wiring guard, per godlike/07).
func NewRepository(db *sql.DB) *Repository {
	if db == nil {
		panic("stocksourcecache.NewRepository: nil *sql.DB (composition root must wire root.DB.DB)")
	}
	return &Repository{db: db}
}

// GetByCacheKey returns the active cache entry for the given key.
// Returns (nil, nil) when no active entry exists (cache miss).
func (r *Repository) GetByCacheKey(ctx context.Context, cacheKey string) (*stockpipeline.SourceCacheEntry, error) {
	const q = `SELECT cache_key, provider, external_id, source_url, local_path,
	           file_size, legacy_file_md5, download_section, merge_format, force_keyframes
	           FROM stock_source_cache
	           WHERE cache_key = ? AND state = 'active'`

	var e stockpipeline.SourceCacheEntry
	var forceKeyframes int
	err := r.db.QueryRowContext(ctx, q, cacheKey).Scan(
		&e.CacheKey, &e.Provider, &e.ExternalID, &e.SourceURL,
		&e.LocalPath, &e.FileSize, &e.LegacyFileMD5, &e.DownloadSection,
		&e.MergeFormat, &forceKeyframes,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("stocksourcecache.GetByCacheKey: %w", err)
	}
	e.ForceKeyframes = forceKeyframes != 0
	return &e, nil
}

// Upsert inserts or replaces a cache entry. When an entry with the
// same cache_key exists, it is updated with the new values and state
// is reset to 'active'.
func (r *Repository) Upsert(ctx context.Context, entry *stockpipeline.SourceCacheEntry) error {
	const q = `INSERT INTO stock_source_cache
	           (cache_key, provider, external_id, source_url, local_path,
	            file_size, legacy_file_md5, download_section, merge_format,
	            force_keyframes, state, created_at, updated_at)
	           VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'active', datetime('now'), datetime('now'))
	           ON CONFLICT(cache_key) DO UPDATE SET
	            provider        = excluded.provider,
	            external_id     = excluded.external_id,
	            source_url      = excluded.source_url,
	            local_path      = excluded.local_path,
	            file_size       = excluded.file_size,
	            legacy_file_md5       = excluded.legacy_file_md5,
	            download_section = excluded.download_section,
	            merge_format    = excluded.merge_format,
	            force_keyframes = excluded.force_keyframes,
	            state           = 'active',
	            updated_at      = datetime('now')`

	forceKeyframes := 0
	if entry.ForceKeyframes {
		forceKeyframes = 1
	}

	_, err := r.db.ExecContext(ctx, q,
		entry.CacheKey, entry.Provider, entry.ExternalID, entry.SourceURL,
		entry.LocalPath, entry.FileSize, entry.LegacyFileMD5, entry.DownloadSection,
		entry.MergeFormat, forceKeyframes,
	)
	if err != nil {
		return fmt.Errorf("stocksourcecache.Upsert: %w", err)
	}
	return nil
}

// Invalidate marks a cache entry as invalidated (e.g., file missing
// on disk or corrupted). The entry is not deleted so audit trails
// are preserved.
func (r *Repository) Invalidate(ctx context.Context, cacheKey string) error {
	const q = `UPDATE stock_source_cache
	           SET state = 'invalidated', updated_at = datetime('now')
	           WHERE cache_key = ? AND state = 'active'`

	_, err := r.db.ExecContext(ctx, q, cacheKey)
	if err != nil {
		return fmt.Errorf("stocksourcecache.Invalidate: %w", err)
	}
	return nil
}

// ActiveCount returns the number of active cache entries.
// Useful for diagnostics and test assertions.
func (r *Repository) ActiveCount(ctx context.Context) (int, error) {
	const q = `SELECT COUNT(*) FROM stock_source_cache WHERE state = 'active'`
	var count int
	if err := r.db.QueryRowContext(ctx, q).Scan(&count); err != nil {
		return 0, fmt.Errorf("stocksourcecache.ActiveCount: %w", err)
	}
	return count, nil
}

// PurgeExpired removes cache entries older than the given age.
// Returns the number of rows deleted.
func (r *Repository) PurgeExpired(ctx context.Context, maxAge time.Duration) (int64, error) {
	cutoff := time.Now().Add(-maxAge).UTC().Format("2006-01-02 15:04:05")
	const q = `DELETE FROM stock_source_cache
	           WHERE state IN ('active', 'invalidated') AND created_at < ?`
	result, err := r.db.ExecContext(ctx, q, cutoff)
	if err != nil {
		return 0, fmt.Errorf("stocksourcecache.PurgeExpired: %w", err)
	}
	return result.RowsAffected()
}
