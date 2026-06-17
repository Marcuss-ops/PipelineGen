package artlist

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"sync"
	"time"

	"velox/go-master/pkg/concurrent"

	"go.uber.org/zap"
)

// liveSearchCacheEntry holds a cached live search result for a term.
type liveSearchCacheEntry struct {
	Clips    []ScraperClip
	CachedAt time.Time
}

// liveSearchCache is a TWO-LEVEL cache for Artlist live search results.
// Level 1 (fast): in-memory map — eliminates redundant DB reads for same term.
// Level 2 (persistent): SQLite-backed table — survives server restarts.
// Level 3 (background refresh): refreshes stale entries before expiry.
type liveSearchCache struct {
	mu    sync.RWMutex
	items map[string]liveSearchCacheEntry
	db    *sql.DB // SQLite-backed persistent cache (optional)
	log   *zap.Logger
}

func newLiveSearchCache() *liveSearchCache {
	return &liveSearchCache{
		items: make(map[string]liveSearchCacheEntry),
	}
}

// newPersistentLiveSearchCache creates a cache with SQLite backing.
// The optional parentCtx is used for tracing in the background warm-up
// goroutine. Pass nil to use context.Background().
func newPersistentLiveSearchCache(db *sql.DB, log *zap.Logger, parentCtx ...context.Context) *liveSearchCache {
	c := newLiveSearchCache()
	c.db = db
	c.log = log
	var warmCtx context.Context = context.Background()
	if len(parentCtx) > 0 && parentCtx[0] != nil {
		warmCtx = parentCtx[0]
	}
	// Warm up in-memory cache from DB (load recent entries asynchronously)
	c.warmFromDB(warmCtx)
	return c
}

// warmFromDB loads cached entries that are still fresh into the in-memory map.
// Accepts a parent context for tracing; the actual work runs in a background goroutine
// with its own timeout to avoid blocking startup.
func (c *liveSearchCache) warmFromDB(parentCtx context.Context) {
	if c.db == nil {
		return
	}
	// Don't block startup — load in background
	concurrent.SafeGo("artlist-cache-warm", func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(parentCtx), 15*time.Second)
		defer cancel()
		rows, err := c.db.QueryContext(ctx, `SELECT term, clips_json, cached_at FROM artlist_search_cache`)
		if err != nil {
			if c.log != nil {
				c.log.Debug("persistent cache warm: no table yet", zap.Error(err))
			}
			return
		}
		defer rows.Close()

		c.mu.Lock()
		defer c.mu.Unlock()

		for rows.Next() {
			var term, clipsJSON, cachedAtStr string
			if err := rows.Scan(&term, &clipsJSON, &cachedAtStr); err != nil {
				continue
			}
			cachedAt, err := time.Parse("2006-01-02 15:04:05", cachedAtStr)
			if err != nil {
				continue
			}
			// Only load entries < 48h old (twice default TTL)
			if time.Since(cachedAt) > 48*time.Hour {
				continue
			}
			var clips []ScraperClip
			if err := json.Unmarshal([]byte(clipsJSON), &clips); err != nil {
				continue
			}
			if _, exists := c.items[term]; !exists {
				c.items[term] = liveSearchCacheEntry{
					Clips:    clips,
					CachedAt: cachedAt,
				}
			}
		}
		if c.log != nil {
			c.log.Info("persistent cache warmed from SQLite",
				zap.Int("entries", len(c.items)),
			)
		}
	})
}

// get returns cached clips and whether the entry exists.
// Keys are lowercased for case-insensitive matching.
func (c *liveSearchCache) get(term string) ([]ScraperClip, bool) {
	key := strings.ToLower(term)
	c.mu.RLock()
	entry, ok := c.items[key]
	c.mu.RUnlock()
	if ok {
		return entry.Clips, true
	}
	// L2: fallback to SQLite
	if c.db != nil {
		clips, ok := c.getFromDB(key)
		if ok {
			// Promote to in-memory cache
			c.mu.Lock()
			c.items[key] = liveSearchCacheEntry{Clips: clips, CachedAt: time.Now()}
			c.mu.Unlock()
			return clips, true
		}
	}
	return nil, false
}

// getFromDB fetches a cache entry from SQLite.
func (c *liveSearchCache) getFromDB(term string) ([]ScraperClip, bool) {
	if c.db == nil {
		return nil, false
	}
	var clipsJSON, cachedAtStr string
	err := c.db.QueryRow(
		`SELECT clips_json, cached_at FROM artlist_search_cache WHERE term = ?`,
		term,
	).Scan(&clipsJSON, &cachedAtStr)
	if err != nil {
		return nil, false
	}
	cachedAt, err := time.Parse("2006-01-02 15:04:05", cachedAtStr)
	if err != nil {
		return nil, false
	}
	// Check if expired (48h hard limit for persisted cache)
	if time.Since(cachedAt) > 48*time.Hour {
		c.deleteFromDB(term)
		return nil, false
	}
	var clips []ScraperClip
	if err := json.Unmarshal([]byte(clipsJSON), &clips); err != nil {
		return nil, false
	}
	return clips, true
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

// set stores a fresh live search result in both in-memory and SQLite cache.
// Keys are lowercased for case-insensitive matching.
func (c *liveSearchCache) set(term string, clips []ScraperClip) {
	key := strings.ToLower(term)
	c.mu.Lock()
	c.items[key] = liveSearchCacheEntry{
		Clips:    clips,
		CachedAt: time.Now(),
	}
	c.mu.Unlock()

	// Persist to SQLite asynchronously
	if c.db != nil {
		c.persistToDB(key, clips)
	}
}

// persistToDB writes the cache entry to SQLite.
func (c *liveSearchCache) persistToDB(term string, clips []ScraperClip) {
	data, err := json.Marshal(clips)
	if err != nil {
		return
	}
	_, err = c.db.Exec(
		`INSERT INTO artlist_search_cache (term, clips_json, cached_at) VALUES (?, ?, datetime('now'))
		 ON CONFLICT(term) DO UPDATE SET clips_json = excluded.clips_json, cached_at = excluded.cached_at`,
		term, string(data),
	)
	if err != nil && c.log != nil {
		c.log.Debug("persistent cache write failed", zap.String("term", term), zap.Error(err))
	}
}

// deleteFromDB removes a cache entry from SQLite.
func (c *liveSearchCache) deleteFromDB(term string) {
	if c.db == nil {
		return
	}
	_, _ = c.db.Exec(`DELETE FROM artlist_search_cache WHERE term = ?`, term)
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

// Cleanup removes expired entries from both in-memory and SQLite.
func (c *liveSearchCache) Cleanup(ttl time.Duration) {
	c.mu.Lock()
	for term, entry := range c.items {
		if time.Since(entry.CachedAt) > ttl*2 {
			delete(c.items, term)
		}
	}
	c.mu.Unlock()

	// Remove expired from DB (48h hard limit for persisted entries)
	if c.db != nil {
		expiryHours := int(48 * time.Hour / time.Hour)
		if c.log != nil {
			c.log.Debug("cleaning up expired search cache entries",
				zap.Int("expiry_hours", expiryHours),
			)
		}
		_, _ = c.db.Exec(`DELETE FROM artlist_search_cache WHERE cached_at < datetime('now', '-' || ? || ' hours')`, expiryHours)
	}
}
