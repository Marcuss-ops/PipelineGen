package clip

import (
	"fmt"
	"sync"
	"time"
)

// SuggestionCache is an LRU-style cache for semantic suggestions
type SuggestionCache struct {
	mu       sync.RWMutex
	items    map[string]cacheItem
	maxSize  int
	ttl      time.Duration
}

type cacheItem struct {
	value     interface{}
	createdAt time.Time
}

// NewSuggestionCache creates a new cache with maxSize items and TTL duration
func NewSuggestionCache(maxSize int, ttl time.Duration) *SuggestionCache {
	return &SuggestionCache{
		items:   make(map[string]cacheItem),
		maxSize: maxSize,
		ttl:     ttl,
	}
}

// Get retrieves a cached value if it exists and is not expired
func (c *SuggestionCache) Get(key string) (interface{}, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	item, ok := c.items[key]
	if !ok {
		return nil, false
	}

	if time.Since(item.createdAt) > c.ttl {
		return nil, false
	}

	return item.value, true
}

// Set stores a value in the cache
func (c *SuggestionCache) Set(key string, value interface{}) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Evict oldest if at capacity
	if len(c.items) >= c.maxSize {
		oldestKey := ""
		oldestTime := time.Now()
		for k, v := range c.items {
			if v.createdAt.Before(oldestTime) {
				oldestTime = v.createdAt
				oldestKey = k
			}
		}
		if oldestKey != "" {
			delete(c.items, oldestKey)
		}
	}

	c.items[key] = cacheItem{
		value:     value,
		createdAt: time.Now(),
	}
}

// Clear removes all cached items
func (c *SuggestionCache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.items = make(map[string]cacheItem)
}

// Size returns the number of cached items
func (c *SuggestionCache) Size() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.items)
}

// Global cache instance for backward compatibility with other handlers
var (
	globalCache     *SuggestionCache
	globalCacheOnce sync.Once
)

// GetGlobalCache returns the global cache instance
func GetGlobalCache() *SuggestionCache {
	globalCacheOnce.Do(func() {
		globalCache = NewSuggestionCache(1000, 15*time.Minute)
	})
	return globalCache
}

// SearchCacheKey generates a cache key for search queries
func SearchCacheKey(query, group, parentID string) string {
	return fmt.Sprintf("search:%s:%s:%s", query, group, parentID)
}

// FolderCacheKey generates a cache key for folder queries
func FolderCacheKey(query, group, parentID string) string {
	return fmt.Sprintf("folder:%s:%s:%s", query, group, parentID)
}

// InvalidateSearchCache invalidates search cache entries
func InvalidateSearchCache() {
	GetGlobalCache().Clear()
}

// Stats returns cache statistics
func (c *SuggestionCache) Stats() map[string]interface{} {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return map[string]interface{}{
		"items":   len(c.items),
		"max_size": c.maxSize,
		"ttl":     c.ttl.String(),
	}
}
