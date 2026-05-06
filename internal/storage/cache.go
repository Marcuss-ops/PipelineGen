package storage

import (
	"sync"
	"time"
)

// CacheEntry holds a cached value with expiration.
type CacheEntry struct {
	Value     interface{}
	ExpiresAt time.Time
}

// MemoryCache provides a simple in-memory cache with TTL support.
type MemoryCache struct {
	mu       sync.RWMutex
	entries  map[string]CacheEntry
	cleanups int // number of cleanup runs
}

// NewMemoryCache creates a new in-memory cache.
// Optionally, pass a cleanup interval to periodically remove expired entries.
func NewMemoryCache(cleanupInterval ...time.Duration) *MemoryCache {
	c := &MemoryCache{
		entries: make(map[string]CacheEntry),
	}

	if len(cleanupInterval) > 0 && cleanupInterval[0] > 0 {
		go c.cleanupLoop(cleanupInterval[0])
	}

	return c
}

// Set stores a value in the cache with the given TTL.
func (c *MemoryCache) Set(key string, value interface{}, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.entries[key] = CacheEntry{
		Value:     value,
		ExpiresAt: time.Now().Add(ttl),
	}
}

// Get retrieves a value from the cache.
// Returns (value, true) if found and not expired, (nil, false) otherwise.
func (c *MemoryCache) Get(key string) (interface{}, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	entry, ok := c.entries[key]
	if !ok {
		return nil, false
	}

	if time.Now().After(entry.ExpiresAt) {
		// Expired - delete asynchronously
		go c.Delete(key)
		return nil, false
	}

	return entry.Value, true
}

// Delete removes a key from the cache.
func (c *MemoryCache) Delete(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, key)
}

// Clear removes all entries from the cache.
func (c *MemoryCache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = make(map[string]CacheEntry)
}

// Len returns the number of entries in the cache (including expired ones).
func (c *MemoryCache) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.entries)
}

// cleanupLoop periodically removes expired entries.
func (c *MemoryCache) cleanupLoop(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for range ticker.C {
		c.cleanup()
	}
}

// cleanup removes all expired entries.
func (c *MemoryCache) cleanup() {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	for key, entry := range c.entries {
		if now.After(entry.ExpiresAt) {
			delete(c.entries, key)
		}
	}
	c.cleanups++
}

// Stats returns cache statistics.
func (c *MemoryCache) Stats() (total int, expired int) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	now := time.Now()
	total = len(c.entries)
	for _, entry := range c.entries {
		if now.After(entry.ExpiresAt) {
			expired++
		}
	}
	return total, expired
}
