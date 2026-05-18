package visualquery

import (
	"fmt"
	"strings"
	"sync"
)

// queryCache provides in-memory caching for LLM-generated queries
var (
	queryCache   = make(map[string]VisualQueryResult)
	queryCacheMu sync.RWMutex
)

// FirstNonEmpty returns the first non-empty string from the input list
func FirstNonEmpty(values ...string) string {
	for _, v := range values {
		if v := strings.TrimSpace(v); v != "" {
			return v
		}
	}
	return ""
}

// buildCacheKey creates a unique cache key for query generation
func buildCacheKey(topic, subject, narrative string, maxQueries int) string {
	// Simple hash of inputs
	hashInput := fmt.Sprintf("%s|%s|%s|%d|%s", topic, subject, narrative, maxQueries, cacheVersion)
	return fmt.Sprintf("%x", hash(hashInput))
}

// hash creates a simple hash of the input string
func hash(s string) uint32 {
	h := uint32(2166136261)
	for i := 0; i < len(s); i++ {
		h ^= uint32(s[i])
		h *= 16777619
	}
	return h
}

// getFromCache retrieves a cached result
func getFromCache(key string) (VisualQueryResult, bool) {
	queryCacheMu.RLock()
	defer queryCacheMu.RUnlock()

	result, ok := queryCache[key]
	return result, ok
}

// saveToCache stores a result in cache
func saveToCache(key string, result VisualQueryResult) {
	queryCacheMu.Lock()
	defer queryCacheMu.Unlock()

	queryCache[key] = result
}
