// Package artlist — search_cache.go: in-memory L1 + persistent
// L2 cache for Artlist live search results (PR-ARTLIST-CACHE-L1,
// July 2026).
//
// P0-3 (godlike/07 zero-legacy, July 2026): the persistent cache
// no longer relies on `database/sql` directly; it consumes a
// typed ArtlistSearchCachePort (declared in cache_ports.go) whose
// concrete SQLite adapter lives at
// internal/infrastructure/database/sqlite/artlist_search_cache_adapter.go.
// This file is now application-layer-only; SQL is owned by the
// infrastructure layer per AGENTS.md Pattern 0.
//
// The two-level cache contract is preserved verbatim from the
// pre-migration version — in-memory L1 for the same-term hit
// fast-path, persistent L2 (via the port) for cross-restart
// durability, async persist, async warm-up.
//
// Behavior preserved:
//
//   - L1 (`c.items`) is the source of truth for served reads;
//     cache mutations are atomic w.r.t. concurrent readers
//     via sync.RWMutex.
//   - L2 (`c.cache`) failures are surfaced via the port's
//     typed-error contract; persistent-layer failures never
//     panic the L1 path (the legacy fail-soft logic is
//     retained verbatim).
//   - In-memory-only mode (`c.cache == nil`) is preserved for
//     tests + single-process pipelines; tests in
//     search_cache_test.go bypass the constructor and construct
//     `&liveSearchCache{items: ...}` directly to exercise L1
//     semantics in isolation.
package artlist

import (
	"context"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
)

// liveSearchCacheEntry holds a cached live search result.
type liveSearchCacheEntry struct {
	Clips    []Candidate
	CachedAt time.Time
}

// liveSearchCache is a TWO-LEVEL cache for Artlist live search
// results. Level 1 (fast): in-memory map. Level 2 (persistent):
// typed ArtlistSearchCachePort (typically SQLite-backed in
// production, nil in tests).
type liveSearchCache struct {
	mu    sync.RWMutex
	items map[string]liveSearchCacheEntry
	cache ArtlistSearchCachePort // optional: nil → in-memory only
	log   *zap.Logger
}

func newLiveSearchCache() *liveSearchCache {
	return &liveSearchCache{
		items: make(map[string]liveSearchCacheEntry),
	}
}

// get returns cached clips and whether the entry exists.
// L1 fast-path first; L2 fallback only on L1 miss.
func (c *liveSearchCache) get(term string) ([]Candidate, bool) {
	key := strings.ToLower(term)
	c.mu.RLock()
	entry, ok := c.items[key]
	c.mu.RUnlock()
	if ok {
		return entry.Clips, true
	}
	// L2 fallback through the typed port.
	if c.cache != nil {
		clips, cachedAt, ok, err := c.cache.Get(context.Background(), key)
		if err != nil && c.log != nil {
			c.log.Warn("persistent cache get failed", zap.String("term", key), zap.Error(err))
		}
		if ok {
			c.mu.Lock()
			c.items[key] = liveSearchCacheEntry{Clips: clips, CachedAt: cachedAt}
			c.mu.Unlock()
			return clips, true
		}
	}
	return nil, false
}

// age returns how old the cached entry is. Returns -1 if not cached.
func (c *liveSearchCache) age(term string) time.Duration {
	key := strings.ToLower(term)
	c.mu.RLock()
	entry, ok := c.items[key]
	c.mu.RUnlock()
	if !ok {
		return -1
	}
	return time.Since(entry.CachedAt)
}

// set stores a fresh live search result in both in-memory and
// the persistent port. Ordinary request-path cache fills remain
// fail-soft for backward compatibility; durable worker refreshes use
// setWithContext so a persistent write failure is retryable.
func (c *liveSearchCache) set(term string, clips []Candidate) {
	if err := c.setWithContext(context.Background(), term, clips); err != nil && c.log != nil {
		c.log.Debug("persistent cache write failed", zap.String("term", strings.ToLower(term)), zap.Error(err))
	}
}

// setWithContext is the worker-safe cache mutation path. The L1 update is
// applied first, while an L2 failure is returned so durable refresh jobs do
// not report success when the persistent cache could not be updated.
func (c *liveSearchCache) setWithContext(ctx context.Context, term string, clips []Candidate) error {
	key := strings.ToLower(term)
	writtenAt := time.Now()
	c.mu.Lock()
	previous, existed := c.items[key]
	c.items[key] = liveSearchCacheEntry{Clips: clips, CachedAt: writtenAt}
	c.mu.Unlock()
	if c.cache == nil {
		return nil
	}
	if err := c.cache.Set(ctx, key, clips); err != nil {
		// Do not leave an unpersisted L1 value looking fresh. Restore only
		// if no concurrent writer superseded this refresh in the meantime.
		c.mu.Lock()
		if current, ok := c.items[key]; ok && current.CachedAt.Equal(writtenAt) {
			if existed {
				c.items[key] = previous
			} else {
				delete(c.items, key)
			}
		}
		c.mu.Unlock()
		return err
	}
	return nil
}

// isFresh returns true if the cache entry exists and is within the TTL.
func (c *liveSearchCache) isFresh(term string, ttl time.Duration) bool {
	age := c.age(term)
	return age >= 0 && age < ttl
}

// isGettingStale returns true if cache is past 75% of TTL.
func (c *liveSearchCache) isGettingStale(term string, ttl time.Duration) bool {
	age := c.age(term)
	return age >= 0 && age >= (ttl*3/4)
}

// Cleanup removes expired entries from both in-memory and the
// persistent port. The in-memory cleanup is unconditional; the
// L2 delegated through the port's typed cleanup contract.
func (c *liveSearchCache) Cleanup(ttl time.Duration) {
	c.mu.Lock()
	for term, entry := range c.items {
		if time.Since(entry.CachedAt) > ttl*2 {
			delete(c.items, term)
		}
	}
	c.mu.Unlock()

	if c.cache != nil {
		if err := c.cache.CleanupExpired(context.Background(), ttl); err != nil && c.log != nil {
			c.log.Warn("persistent cache cleanup failed", zap.Error(err))
		}
	}
}
