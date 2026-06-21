package artlist

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"

	artcache "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/artlist/cache"

	"go.uber.org/zap"
)

// liveSearchCacheEntry holds a cached live search result for a term.
//
// Kept as a public-ish shape so future tooling can introspect the
// entry. The actual payload (Clips) is still the application
// ScraperClip type because this file is the marshalling boundary
// between byte-level infra storage and app-typed search hits.
type liveSearchCacheEntry struct {
	Clips    []ScraperClip
	CachedAt time.Time
}

// liveSearchCache is a TWO-LEVEL cache for Artlist live search
// results.
//
//	PR2.4 refactor: Level 1+2 logic has moved to
//	internal/infrastructure/artlist/cache.Cache.
//
// This wrapper:
//   - owns the byte <-> ScraperClip marshalling
//   - exposes the legacy in-memory TTL semantics the
//     CachedScraperProvider chain still relies on
//   - delegates Get/Put/Cleanup/WarmFromDB to the infra package
//
// The wrapper MUST stay thin: every byte-level decision (cache key
// casing, TTL horizon, L2 retention) is owned by the infra cache.
type liveSearchCache struct {
	// inner is the byte-level cache. Always non-nil after
	// newLiveSearchCache().
	inner *artcache.Cache
	log   *zap.Logger
}

func newLiveSearchCache() *liveSearchCache {
	return &liveSearchCache{
		inner: artcache.New(nil, nil, artcache.Config{}),
	}
}

// newPersistentLiveSearchCache creates a cache with SQLite backing.
// The optional parentCtx is used for tracing in the background warm-up
// goroutine. Pass nil to use context.Background().
func newPersistentLiveSearchCache(db *sql.DB, log *zap.Logger, parentCtx ...context.Context) *liveSearchCache {
	c := newLiveSearchCache()
	c.inner = artcache.New(db, log, artcache.Config{
		TTLDuration: 24 * time.Hour,
		WarmTimeout: 15 * time.Second,
	})
	c.log = log
	var warmCtx context.Context = context.Background()
	if len(parentCtx) > 0 && parentCtx[0] != nil {
		warmCtx = parentCtx[0]
	}
	// warmFromDB is honoured inside the infra cache - we just
	// trigger it with a no-op callback because the on-Load flood
	// rehydration is unnecessary: Get() rehydrates on demand.
	c.inner.WarmFromDB(warmCtx, func(term string, raw []byte) {})
	return c
}

// get returns cached clips and whether the entry exists. Keys are
// lower-cased by the infra cache for case-insensitive matching.
func (c *liveSearchCache) get(term string) ([]ScraperClip, bool) {
	raw, err := c.inner.Get(context.Background(), strings.TrimSpace(strings.ToLower(term)))
	if err != nil || len(raw) == 0 {
		return nil, false
	}
	var clips []ScraperClip
	if err := json.Unmarshal(raw, &clips); err != nil {
		return nil, false
	}
	return clips, true
}

// age returns how old the cached entry is. Returns -1 if not cached.
func (c *liveSearchCache) age(term string) time.Duration {
	_, age, ok := c.inner.GetFresh(context.Background(), term)
	if !ok {
		return -1
	}
	return age
}

// isFresh returns true if the cache entry exists and is within the
// TTL. The wrapper computes the comparison itself because the
// legacy callers pass their own TTL (different tiers in the chain
// ask for different freshness windows).
func (c *liveSearchCache) isFresh(term string, ttl time.Duration) bool {
	_, age, ok := c.inner.GetFresh(context.Background(), term)
	return ok && age >= 0 && age < ttl
}

// isGettingStale returns true if cache is past 75% of TTL - time to
// schedule a background refresh. Same self-computed check so the
// wrapper can match the legacy pre-emptive refresh policy.
func (c *liveSearchCache) isGettingStale(term string, ttl time.Duration) bool {
	_, age, ok := c.inner.GetFresh(context.Background(), term)
	return ok && age >= 0 && age >= ttl*3/4
}

// set stores a fresh live search result in both in-memory and
// SQLite cache (L2 is delegated to the infra cache).
func (c *liveSearchCache) set(term string, clips []ScraperClip) {
	data, err := json.Marshal(clips)
	if err != nil {
		if c.log != nil {
			c.log.Debug("live cache: marshal failed", zap.String("term", term))
		}
		return
	}
	c.inner.Put(context.Background(), strings.TrimSpace(strings.ToLower(term)), data)
}

// Cleanup removes expired entries from both in-memory and SQLite.
func (c *liveSearchCache) Cleanup(ttl time.Duration) {
	c.inner.Cleanup(context.Background())
}

// getFromDB / deleteFromDB / persistToDB have been removed. The
// infra cache owns all SQLite operations; the application layer
// only marshals bytes. Callers that previously relied on these
// internals now go through get/set.
