package artlist

import (
	"sync"
	"time"
)

// liveSearchCacheEntry holds a cached live search result for a term.
type liveSearchCacheEntry struct {
	Clips    []ScraperClip
	CachedAt time.Time
}

// liveSearchCache is a thread-safe in-memory cache for Artlist live search results.
// Level 1: Eliminates redundant Playwright launches for recently searched terms.
// Level 2: Triggers background refresh when cache is > 75% of its TTL age.
type liveSearchCache struct {
	mu    sync.RWMutex
	items map[string]liveSearchCacheEntry
}

func newLiveSearchCache() *liveSearchCache {
	return &liveSearchCache{
		items: make(map[string]liveSearchCacheEntry),
	}
}

// get returns cached clips and whether the entry exists.
func (c *liveSearchCache) get(term string) ([]ScraperClip, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	entry, ok := c.items[term]
	if !ok {
		return nil, false
	}
	return entry.Clips, true
}

// age returns how old the cached entry is. Returns -1 if not cached.
func (c *liveSearchCache) age(term string) time.Duration {
	c.mu.RLock()
	defer c.mu.RUnlock()
	entry, ok := c.items[term]
	if !ok {
		return -1
	}
	return time.Since(entry.CachedAt)
}

// set stores a fresh live search result in the cache.
func (c *liveSearchCache) set(term string, clips []ScraperClip) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.items[term] = liveSearchCacheEntry{
		Clips:    clips,
		CachedAt: time.Now(),
	}
}

// isFresh returns true if the cache entry exists and is within the TTL.
func (c *liveSearchCache) isFresh(term string, ttl time.Duration) bool {
	age := c.age(term)
	return age >= 0 && age < ttl
}

// isGettingStale returns true if cache is past 75% of TTL — time to schedule a background refresh.
func (c *liveSearchCache) isGettingStale(term string, ttl time.Duration) bool {
	age := c.age(term)
	return age >= 0 && age >= (ttl*3/4)
}
