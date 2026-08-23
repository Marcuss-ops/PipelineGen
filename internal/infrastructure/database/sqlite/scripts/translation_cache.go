// Package scripts provides SQLite-backed persistence and caching for the
// script subsystem: the ScriptRepository (scripts, sections, versions, logs,
// research sources/cache) plus the translation cache and gemma memory store.
//
// translation_cache.go provides a two-tier caching layer for LLM-based
// translations.
// Uses a two-tier approach:
//
//	L1: in-memory map with TTL for hot translations (fastest path)
//	L2: SQLite table for persistence across restarts
//
// The cache key is sha256(source_text_normalized + target_language),
// so the same text translated to the same language always hits.
package scripts

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	"strings"
	"sync"
	"time"

	hashutil "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/files"
	concurrent "github.com/Marcuss-ops/PipelineGen/pkg/concurrent"

	_ "github.com/mattn/go-sqlite3"
)

// Default cache TTL for in-memory entries (1 hour).
const defaultMemoryTTL = 1 * time.Hour

// cacheEntry holds a translated text with its expiration time.
type cacheEntry struct {
	translated string
	expiresAt  time.Time
}

// Cache provides a two-tier translation cache.
// Automatically starts a background goroutine for L1 eviction and L2 sweep.
type Cache struct {
	db *sql.DB

	mu        sync.RWMutex
	memory    map[string]cacheEntry // L1: in-memory hot cache
	memoryTTL time.Duration

	ctx    context.Context
	cancel context.CancelFunc
}

// NewCache creates a new translation cache backed by the given SQLite DB.
// The DB should already have the translation_cache table (migration 023).
// Starts background cleanup goroutines for L1 eviction and L2 sweep.
//
// The cache's internal context is derived from parentCtx so it is
// cancellable when the parent (typically the app lifecycle context)
// terminates. When parentCtx is nil, context.Background() is used.
func NewCache(db *sql.DB, parentCtx ...context.Context) *Cache {
	var parent context.Context = context.Background()
	if len(parentCtx) > 0 && parentCtx[0] != nil {
		parent = parentCtx[0]
	}
	ctx, cancel := context.WithCancel(parent)
	c := &Cache{
		db:        db,
		memory:    make(map[string]cacheEntry),
		memoryTTL: defaultMemoryTTL,
		ctx:       ctx,
		cancel:    cancel,
	}
	// Start periodic cleanup: evict expired L1 entries every 10 minutes
	// and sweep stale L2 rows older than 30 days every hour.
	go c.periodicCleanup(ctx)
	return c
}

// Close stops the background cleanup goroutines.
func (c *Cache) Close() {
	c.cancel()
}

// periodicCleanup runs every 10 minutes to evict expired L1 entries,
// and every hour to sweep stale L2 rows from SQLite.
func (c *Cache) periodicCleanup(ctx context.Context) {
	l1Ticker := time.NewTicker(10 * time.Minute)
	l2Ticker := time.NewTicker(1 * time.Hour)
	defer l1Ticker.Stop()
	defer l2Ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-l1Ticker.C:
			c.evictExpiredMemory()
		case <-l2Ticker.C:
			c.sweepStaleIfReady()
		}
	}
}

// evictExpiredMemory removes expired entries from the in-memory cache.
func (c *Cache) evictExpiredMemory() {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	for key, entry := range c.memory {
		if now.After(entry.expiresAt) {
			delete(c.memory, key)
		}
	}
}

// sweepStaleIfReady runs SweepStale with a 30-day cutoff.
func (c *Cache) sweepStaleIfReady() {
	if c.db == nil {
		return
	}
	// Use the cache's own context (derived from the app lifecycle) so the
	// sweep is cancelled when the server shuts down. 30s timeout prevents
	// blocking if the DB is busy.
	sweepCtx, cancel := context.WithTimeout(c.ctx, 30*time.Second)
	defer cancel()
	deleted, err := c.SweepStale(sweepCtx, 30)
	if err == nil && deleted > 0 {
		// Log is not available here since we don't have a logger
		_ = deleted
	}
}

// Get retrieves a translation from cache.
// Checks L1 (in-memory) first, then L2 (SQLite).
// Returns ("", false) on cache miss.
func (c *Cache) Get(ctx context.Context, sourceText, targetLanguage string) (string, bool) {
	key := cacheKey(sourceText, targetLanguage)

	// L1: in-memory check (fast, no DB call)
	c.mu.RLock()
	entry, ok := c.memory[key]
	c.mu.RUnlock()
	if ok && time.Now().Before(entry.expiresAt) {
		return entry.translated, true
	}

	// L2: SQLite check
	if c.db == nil {
		return "", false
	}

	var translated string
	err := c.db.QueryRowContext(ctx, `
		SELECT translated_text FROM translation_cache
		WHERE cache_key = ? AND last_used > datetime('now', '-30 days')
	`, key).Scan(&translated)
	if err == sql.ErrNoRows {
		return "", false
	}
	if err != nil {
		return "", false
	}

	// Promote to L1 cache on hit
	c.mu.Lock()
	c.memory[key] = cacheEntry{
		translated: translated,
		expiresAt:  time.Now().Add(c.memoryTTL),
	}
	c.mu.Unlock()

	// Update last_used timestamp asynchronously (fire-and-forget).
	// SafeGo wraps with recover() so a panic in the DB driver or SQL
	// path cannot crash the request-serving goroutine that called Get.
	concurrent.SafeGo("translations.cache.touchLastUsed", func() {
		updCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer cancel()
		_, _ = c.db.ExecContext(updCtx, "UPDATE translation_cache SET last_used = datetime('now') WHERE cache_key = ?", key)
	})

	return translated, true
}

// Set stores a translation in both L1 and L2 caches.
func (c *Cache) Set(ctx context.Context, sourceText, targetLanguage, translatedText string) error {
	if strings.TrimSpace(translatedText) == "" {
		return nil // don't cache empty translations
	}

	key := cacheKey(sourceText, targetLanguage)
	sourceHash := hashutil.SHA256String(sourceText)

	// Store in L1 (in-memory) with TTL
	c.mu.Lock()
	c.memory[key] = cacheEntry{
		translated: translatedText,
		expiresAt:  time.Now().Add(c.memoryTTL),
	}
	c.mu.Unlock()

	// Store in L2 (SQLite)
	if c.db == nil {
		return nil
	}

	_, err := c.db.ExecContext(ctx, `
		INSERT OR REPLACE INTO translation_cache (cache_key, source_text_hash, target_language, translated_text, created_at, last_used)
		VALUES (?, ?, ?, ?, datetime('now'), datetime('now'))
	`, key, sourceHash, targetLanguage, translatedText)
	return err
}

// Invalidate removes a specific translation from cache.
// Useful when a user explicitly requests a fresh translation.
func (c *Cache) Invalidate(ctx context.Context, sourceText, targetLanguage string) error {
	key := cacheKey(sourceText, targetLanguage)

	c.mu.Lock()
	delete(c.memory, key)
	c.mu.Unlock()

	if c.db != nil {
		_, err := c.db.ExecContext(ctx, "DELETE FROM translation_cache WHERE cache_key = ?", key)
		return err
	}
	return nil
}

// SweepStale removes old cache entries from SQLite.
// Safe to run periodically (e.g., daily maintenance).
func (c *Cache) SweepStale(ctx context.Context, maxAgeDays int) (int64, error) {
	if c.db == nil {
		return 0, nil
	}
	if maxAgeDays <= 0 {
		maxAgeDays = 30
	}
	result, err := c.db.ExecContext(ctx,
		"DELETE FROM translation_cache WHERE last_used < datetime('now', ?)",
		fmt.Sprintf("-%d days", maxAgeDays),
	)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// cacheKey generates a deterministic key for a source text + target language pair.
func cacheKey(sourceText, targetLanguage string) string {
	normalized := strings.ToLower(strings.TrimSpace(sourceText))
	payload := normalized + "|" + strings.TrimSpace(targetLanguage)
	hash := digest.SHA256Bytes([]byte(payload))
	return hash
}
