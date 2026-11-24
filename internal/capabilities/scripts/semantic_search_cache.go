package scriptgeneration

import (
	"context"

	semanticscript "github.com/Marcuss-ops/PipelineGen/internal/kernel/script/semantic"

	"github.com/Marcuss-ops/PipelineGen/pkg/cacheutil"
)

type SemanticSearchCacheEntry = semanticscript.SemanticSearchCacheEntry

type SemanticSearchCache = semanticscript.SemanticSearchCache

type SemanticSearchCacheInvalidator = semanticscript.SemanticSearchCacheInvalidator

// SemanticSearchPlanCache is the process-local bounded cache. It stores only
// non-empty provider answers: a negative observation is not a stable fact (a
// provider index changes and a challenged lane recovers), so caching it would
// turn one bad run into a durable empty visual.
type SemanticSearchPlanCache struct {
	lru *cacheutil.LRU
}

// NewSemanticSearchPlanCache builds a cache holding at most capacity entries.
func NewSemanticSearchPlanCache(capacity int) *SemanticSearchPlanCache {
	return &SemanticSearchPlanCache{lru: cacheutil.NewLRU(capacity)}
}

func (c *SemanticSearchPlanCache) Get(_ context.Context, key string) (SemanticSearchCacheEntry, bool) {
	if c == nil || c.lru == nil || key == "" {
		return SemanticSearchCacheEntry{}, false
	}
	value, ok := c.lru.Get(key)
	if !ok {
		return SemanticSearchCacheEntry{}, false
	}
	entry, ok := value.(SemanticSearchCacheEntry)
	return entry, ok
}

func (c *SemanticSearchPlanCache) Put(_ context.Context, key string, entry SemanticSearchCacheEntry) {
	if c == nil || c.lru == nil || key == "" || len(entry.Response.Hits) == 0 {
		return
	}
	c.lru.Put(key, entry)
}
