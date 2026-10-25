package scriptgeneration

import (
	"context"
	"time"

	"github.com/Marcuss-ops/PipelineGen/pkg/cacheutil"
)

// SemanticSearchCacheEntry is one remembered provider answer. StoredAt is
// stamped by the planner clock so a deterministic test can expire entries
// without sleeping.
type SemanticSearchCacheEntry struct {
	Response SemanticAssetSearchResponse
	StoredAt time.Time
	// Provider is the provider that produced the answer (for example
	// `wikimedia_commons` or `duckduckgo`). A durable implementation keeps it
	// as a column so an answer can be invalidated without dropping every
	// entry; the in-memory cache ignores it.
	Provider string
	// ProviderLane is the budget lane the query charged. It is recorded
	// separately from Provider because a routed query may be answered by a
	// fallback provider while still belonging to the class lane.
	ProviderLane string
}

// SemanticSearchCache is the narrow cache contract the batch planner needs.
// Implementations must be safe for concurrent use: independent ladders share
// one planner cache.
//
// The context is passed through because a durable implementation performs I/O.
// A cache failure is never fatal: Get reports a miss and Put is best effort,
// so an unavailable cache degrades to the uncached provider path instead of
// failing the run.
type SemanticSearchCache interface {
	Get(ctx context.Context, key string) (SemanticSearchCacheEntry, bool)
	Put(ctx context.Context, key string, entry SemanticSearchCacheEntry)
}

// SemanticSearchCacheInvalidator is the optional capability of a durable
// cache: drop every answer produced by one provider (or lane) while keeping
// the answers of the others. It is used when a provider's index or routing
// changes and its cached answers can no longer be trusted.
type SemanticSearchCacheInvalidator interface {
	InvalidateProvider(ctx context.Context, provider string) (int64, error)
}

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
