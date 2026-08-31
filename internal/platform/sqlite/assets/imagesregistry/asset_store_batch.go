// Package imagesregistry — asset_store_batch.go: BatchGetByIDs is the
// canonical batch reader for search-result hydration (data-layer
// unification, August 2026).
//
// CONTRACT: the search path (Qdrant) returns candidate asset IDs +
// score; the canonical asset content (name, source, drive_link,
// duration, lifecycle, metadata, transcripts) is hydrated from SQLite
// via this method so stale Qdrant payloads never propagate to the
// runtime API.
//
// The method issues a single SELECT ... WHERE id IN (?, ?, ...) against
// media_assets and returns a map keyed by asset ID. An LRU-style cache
// (TTL 60s) avoids repeating the query for the same asset IDs across
// repeated search requests within the TTL window.
package imagesregistry

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	asset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// batchCacheTTL is how long a cached BatchGetByIDs entry stays valid.
// 60s balances freshness (asset metadata changes are rare and routed
// through the canonical committer) against throughput (repeated search
// queries for the same assets hit the cache instead of SQLite).
const batchCacheTTL = 60 * time.Second

// batchCacheMaxSize is the soft cap on the number of cached entries.
// When exceeded, the cache is cleared (poor-man's eviction; a true LRU
// is overkill for this read-heavy, write-rare path).
const batchCacheMaxSize = 5000

// batchCacheEntry is one cached asset Details + the time it was fetched.
type batchCacheEntry struct {
	details   *asset.Details
	fetchedAt time.Time
}

// batchCache is the per-AssetStoreSQLite LRU-style cache. Protected by
// batchCacheMu. Never held across the SQL call (only for cache read/write).
type batchCache struct {
	mu      sync.RWMutex
	entries map[string]batchCacheEntry
}

// newBatchCache constructs an empty batch cache.
func newBatchCache() *batchCache {
	return &batchCache{entries: make(map[string]batchCacheEntry)}
}

// get returns the cached entry for id if present and not expired.
func (c *batchCache) get(id string) (*asset.Details, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	e, ok := c.entries[id]
	if !ok || time.Since(e.fetchedAt) >= batchCacheTTL {
		return nil, false
	}
	return e.details, true
}

// put stores a details entry, with a soft-eviction clear when the cap
// is exceeded.
func (c *batchCache) put(id string, details *asset.Details) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.entries) >= batchCacheMaxSize {
		// Soft eviction: clear the cache rather than evicting
		// individual entries. The cache is best-effort; a full
		// clear is acceptable because the next search will
		// re-populate the hot assets.
		c.entries = make(map[string]batchCacheEntry)
	}
	c.entries[id] = batchCacheEntry{details: details, fetchedAt: time.Now()}
}

// BatchGetByIDs retrieves multiple non-tombstoned assets by ID in a
// single SQL query, returning a map keyed by asset ID. Missing IDs
// are simply absent from the map (callers tolerate partial hydration).
// Uses the canonical MediaAssetColumns projection + ScanMediaAsset so
// the returned *asset.Asset is identical in shape to Get().
//
// The LRU cache (TTL 60s) is consulted first; only uncached IDs are
// queried from SQLite. This keeps repeated search requests for the
// same assets off the SQLite hot path.
func (s *AssetStoreSQLite) BatchGetByIDs(ctx context.Context, ids []string) (map[string]*asset.Details, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("assets.BatchGetByIDs: store or db is nil")
	}
	if len(ids) == 0 {
		return map[string]*asset.Details{}, nil
	}

	// Initialise the cache lazily (the field is zero-value nil for
	// stores constructed without the cache; NewAssetStoreSQLite wires
	// it via SetBatchCache if the caller opts in).
	if s.batchCache == nil {
		s.batchCache = newBatchCache()
	}

	out := make(map[string]*asset.Details, len(ids))
	var missed []string
	for _, id := range ids {
		if d, ok := s.batchCache.get(id); ok {
			out[id] = d
		} else {
			missed = append(missed, id)
		}
	}
	if len(missed) == 0 {
		return out, nil
	}

	// Build a single SELECT ... WHERE id IN (?, ?, ...) for the
	// uncached IDs. SQLite has a hard limit on bound parameters
	// (SQLITE_MAX_VARIABLE_NUMBER, default 999); we batch in chunks
	// of 500 to stay well under the limit.
	const chunkSize = 500
	for start := 0; start < len(missed); start += chunkSize {
		end := start + chunkSize
		if end > len(missed) {
			end = len(missed)
		}
		chunk := missed[start:end]

		placeholders := make([]string, len(chunk))
		args := make([]any, len(chunk))
		for i, id := range chunk {
			placeholders[i] = "?"
			args[i] = id
		}
		query := "SELECT " + MediaAssetColumns + " FROM media_assets WHERE " + SoftDeleteFilter() +
			" AND id IN (" + strings.Join(placeholders, ", ") + ")"

		rows, err := s.db.QueryContext(ctx, query, args...)
		if err != nil {
			return nil, fmt.Errorf("assets.BatchGetByIDs: query: %w", err)
		}
		for rows.Next() {
			a, scanErr := ScanMediaAsset(rows)
			if scanErr != nil {
				_ = rows.Close()
				return nil, fmt.Errorf("assets.BatchGetByIDs: scan: %w", scanErr)
			}
			d := &asset.Details{Asset: a}
			out[a.ID] = d
			s.batchCache.put(a.ID, d)
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("assets.BatchGetByIDs: rows iteration: %w", err)
		}
		_ = rows.Close()
	}
	return out, nil
}

// SetBatchCache allows the caller to inject a pre-populated cache
// (e.g. a shared cache across multiple stores). When not called, the
// cache is initialised lazily on first BatchGetByIDs invocation.
func (s *AssetStoreSQLite) SetBatchCache(c *batchCache) {
	if s != nil {
		s.batchCache = c
	}
}
