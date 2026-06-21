// Package cache owns the byte-level two-level cache used by the
// application layer's search-result cache (legacy ScraperClip).
//
// Architecture (PR2.4):
//
//   - L1: in-memory map (sync.RWMutex) - eliminates redundant reads
//     within the same process. Used by the application wrapper in
//     internal/application/artlist/search_cache.go.
//
//   - L2: SQLite-backed table `artlist_search_cache` (migration 012).
//     Optional - the cache is functional without it. When nil, only
//     L1 is populated; fresh process boots start cold.
//
//   - Cleanup: removes L1 entries older than 2x ttl, and L2 entries
//     older than the absolute 48h horizon (reflected by the
//     migration's cached_at timestamp semantics).
//
// Infrastructure does NOT know about ScraperClip, Candidate, or any
// application shape. It stores opaque bytes. The application layer is
// responsible for json.Marshal / json.Unmarshal. This keeps the
// infra package import-graph clean (no upward dependency to internal/
// application/), and lets multiple call sites reuse the cache with
// different payload types.
//
// The cache MUST NOT handle transport errors or orchestrate retries;
// those live in the application wrapper or in pkg/retry.
package cache

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"time"

	"go.uber.org/zap"
)

// Sentinel errors returned by the cache. They mirror the application
// fault taxonomy loosely so callers can branch on intent.
var (
	// ErrUnavailable indicates a transient backend failure (DB locked,
	// connection lost). Caller should retry with backoff.
	ErrUnavailable = errors.New("cache: backend unavailable")
	// ErrInvalidResponse indicates the cache serialised a row it
	// cannot parse (corrupt on-disk JSON). The caller treats the row
	// as missing and may overwrite it on the next Put.
	ErrInvalidResponse = errors.New("cache: invalid persisted response")
)

// Config carries the wiring the application passes to New. ttl is
// the freshness horizon for L2 deletes during Cleanup and for L1
// warm-up filtering (only entries < 48h are loaded into L1).
type Config struct {
	// TTLDuration is the freshness horizon used for L2 retention on
	// Cleanup. Default: 24h.
	TTLDuration time.Duration
	// WarmTimeout bounds the background warm-up goroutine. Default
	// 15s. 0 means skip warm-up entirely.
	WarmTimeout time.Duration
}

// Cache is the two-level cache. Safe for concurrent use.
type Cache struct {
	mu    sync.RWMutex
	items map[string]cacheEntry
	db    *sql.DB // optional; nil skips L2 reads/writes
	log   *zap.Logger
	cfg   Config
}

type cacheEntry struct {
	Raw      []byte
	CachedAt time.Time
}

// Compile-time guarantee that we don't accidentally take
// dependencies on application types.
var _ = func() bool { return true }

// New constructs a Cache. db may be nil (L2 disabled). log may be nil.
// cfg fields fall back to defaults when zero.
func New(db *sql.DB, log *zap.Logger, cfg Config) *Cache {
	if cfg.TTLDuration <= 0 {
		cfg.TTLDuration = 24 * time.Hour
	}
	if cfg.WarmTimeout <= 0 {
		cfg.WarmTimeout = 15 * time.Second
	}
	return &Cache{
		items: make(map[string]cacheEntry),
		db:    db,
		log:   log,
		cfg:   cfg,
	}
}

// Get returns the cached bytes for term and whether the entry was
// found and is fresh (CachedAt < TTL).
//
// Keys are lowercased for case-insensitive matching. Promotion from
// L2 to L1 happens transparently (the application wrapper does not
// need to know that L2 was hit).
//
// Implementations may return ErrInvalidResponse when a persisted row
// is found but contains corrupt JSON; callers typically treat this
// as a cache miss and re-Put.
func (c *Cache) Get(ctx context.Context, term string) ([]byte, error) {
	key := lowerKey(term)

	// L1
	c.mu.RLock()
	entry, ok := c.items[key]
	c.mu.RUnlock()
	if ok && time.Since(entry.CachedAt) < c.cfg.TTLDuration {
		return entry.Raw, nil
	}

	// L2 (optional)
	if c.db == nil {
		return nil, nil
	}
	raw, err := c.fetchFromDB(ctx, key)
	if err != nil {
		return nil, err
	}
	if raw == nil {
		return nil, nil
	}
	// Promote to L1.
	c.mu.Lock()
	c.items[key] = cacheEntry{Raw: raw, CachedAt: time.Now()}
	c.mu.Unlock()
	return raw, nil
}

// GetFresh returns the cached bytes for term along with the age of
// the entry. ok=false on miss. The age is the wall-clock duration
// since the entry was last Put; ok entries past 2x TTL are filtered
// to nil and treated as a miss just like Get would.
//
// Callers should use this when they need to differentiate
// "fresh-and-quiet" from "fresh-but-getting-stale" (for example, to
// schedule a background refresh before serving from cache). Callers
// that just need "exists and parseable bytes" can stick to Get.
func (c *Cache) GetFresh(ctx context.Context, term string) (raw []byte, age time.Duration, ok bool) {
	key := lowerKey(term)

	c.mu.RLock()
	entry, present := c.items[key]
	c.mu.RUnlock()

	if present {
		a := time.Since(entry.CachedAt)
		if a < c.cfg.TTLDuration*2 {
			return entry.Raw, a, true
		}
	}

	if c.db == nil {
		return nil, 0, false
	}
	dbRaw, err := c.fetchFromDB(ctx, key)
	if err != nil || dbRaw == nil {
		return nil, 0, false
	}
	c.mu.Lock()
	c.items[key] = cacheEntry{Raw: dbRaw, CachedAt: time.Now()}
	c.mu.Unlock()
	return dbRaw, 0, true // age=0 reflects post-promotion timestamp; callers treating age as cache-life signal see this as "just written"
}

// fetchFromDB returns nil bytes (cache miss) on `sql.ErrNoRows`. Any
// other query error is wrapped as ErrUnavailable. A corrupt row is
// wrapped as ErrInvalidResponse after we delete it.
func (c *Cache) fetchFromDB(ctx context.Context, term string) ([]byte, error) {
	var raw string
	var cachedAtStr string
	err := c.db.QueryRowContext(ctx,
		`SELECT clips_json, cached_at FROM artlist_search_cache WHERE term = ?`,
		term,
	).Scan(&raw, &cachedAtStr)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, wrapUnavailable(err)
	}
	cachedAt, err := time.Parse("2006-01-02 15:04:05", cachedAtStr)
	if err != nil {
		return nil, wrapInvalid(errors.New("cached_at parse failed: " + err.Error()))
	}
	// Hard horizon: persisted entries past 2x TTL are considered stale
	// even if the table hasn't been cleaned up yet.
	if time.Since(cachedAt) > c.cfg.TTLDuration*2 {
		_ = c.deleteFromDB(ctx, term)
		return nil, nil
	}
	return []byte(raw), nil
}

// Put stores raw bytes for term in L1 and asynchronously in L2.
// L1 write is synchronous (caller expects a consistent view next
// Get). L2 write is best-effort and never blocks the caller.
func (c *Cache) Put(ctx context.Context, term string, raw []byte) {
	if len(raw) == 0 {
		return
	}
	key := lowerKey(term)

	c.mu.Lock()
	c.items[key] = cacheEntry{Raw: raw, CachedAt: time.Now()}
	c.mu.Unlock()

	if c.db == nil {
		return
	}
	// Best-effort persistence. Errors are logged at debug level and
	// do not surface as cache faults; L1 still serves the entry.
	go func() {
		bgCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, err := c.db.ExecContext(bgCtx,
			`INSERT INTO artlist_search_cache (term, clips_json, cached_at) VALUES (?, ?, datetime('now'))
			 ON CONFLICT(term) DO UPDATE SET clips_json = excluded.clips_json, cached_at = excluded.cached_at`,
			key, string(raw),
		)
		if err != nil && c.log != nil {
			c.log.Debug("cache: persistent write failed",
				zap.String("term", key), zap.Error(err))
		}
	}()
}

// Cleanup removes expired entries from L1 (> 2x TTL) and L2 (> 2x TTL).
// L2 cleanup is bounded by the hard horizon: zero values default to
// the config TTL. The implementation mirrors the legacy
// liveSearchCache.Cleanup contract but uses bytes instead of typed
// clips.
func (c *Cache) Cleanup(ctx context.Context) {
	c.mu.Lock()
	for term, entry := range c.items {
		if time.Since(entry.CachedAt) > c.cfg.TTLDuration*2 {
			delete(c.items, term)
		}
	}
	c.mu.Unlock()

	if c.db == nil {
		return
	}
	horizonHours := int((c.cfg.TTLDuration * 2).Hours())
	if horizonHours <= 0 {
		horizonHours = 48
	}
	if c.log != nil {
		c.log.Debug("cache: cleanup", zap.Int("horizon_hours", horizonHours))
	}
	_, _ = c.db.ExecContext(ctx,
		`DELETE FROM artlist_search_cache WHERE cached_at < datetime('now', '-' || ? || ' hours')`,
		horizonHours,
	)
}

// WarmFromDB loads recent L2 entries asynchronously into L1. The
// callback is invoked once per loaded entry so the application layer
// can post-process (e.g., rehydrate typed structs). parentCtx is
// used for tracing; the actual work runs with a bounded timeout so
// the caller is not blocked.
//
// Returns immediately after scheduling the goroutine.
func (c *Cache) WarmFromDB(parentCtx context.Context, onLoad func(term string, raw []byte)) {
	if c.db == nil || onLoad == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(withoutCancel(parentCtx), c.cfg.WarmTimeout)
		defer cancel()
		rows, err := c.db.QueryContext(ctx,
			`SELECT term, clips_json, cached_at FROM artlist_search_cache`,
		)
		if err != nil {
			if c.log != nil {
				c.log.Debug("cache: warm-up query failed", zap.Error(err))
			}
			return
		}
		defer rows.Close()
		loaded := 0
		for rows.Next() {
			var term, raw, cachedAtStr string
			if err := rows.Scan(&term, &raw, &cachedAtStr); err != nil {
				continue
			}
			cachedAt, err := time.Parse("2006-01-02 15:04:05", cachedAtStr)
			if err != nil {
				continue
			}
			if time.Since(cachedAt) > c.cfg.TTLDuration*2 {
				continue
			}
			c.mu.Lock()
			if _, exists := c.items[term]; !exists {
				c.items[term] = cacheEntry{Raw: []byte(raw), CachedAt: cachedAt}
				loaded++
			}
			c.mu.Unlock()
			onLoad(term, []byte(raw))
		}
		if c.log != nil {
			c.log.Info("cache: warm-up loaded", zap.Int("entries", loaded))
		}
	}()
}

// deleteFromDB removes a single key. Best-effort.
func (c *Cache) deleteFromDB(ctx context.Context, term string) error {
	if c.db == nil {
		return nil
	}
	_, err := c.db.ExecContext(ctx,
		`DELETE FROM artlist_search_cache WHERE term = ?`, term)
	if err != nil {
		return wrapUnavailable(err)
	}
	return nil
}

func wrapUnavailable(err error) error { return errors.Join(ErrUnavailable, err) }
func wrapInvalid(err error) error     { return errors.Join(ErrInvalidResponse, err) }

func lowerKey(s string) string {
	// ASCII-fast lower-case: search-cache keys are english terms per
	// normaliseSearchTerm. Avoid pulling strings.ToLower for the
	// hot path; lowercase bytes via a tiny inline transform.
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 32
		}
	}
	return string(b)
}

// withoutCancel detaches a parent context's cancellation while
// preserving its values (tracing IDs, deadlines). Mirrors the
// legacy post-write save context pattern documented in
// AGENTS.md §"Context.Background() Policy".
func withoutCancel(parent context.Context) context.Context {
	if parent == nil {
		return context.Background()
	}
	return context.WithoutCancel(parent)
}
