// Package adapters — stock_resolve_cache.go is the canonical infrastructure
// implementation of stockintelligence.ResolveCache: a bounded, no-TTL L1 LRU
// in front of the durable VidRushCachePort L2 (the same L1+L2 pair the
// VidRush image caches use, with the same contracts).
//
// Contracts mirrored from vidrush_helpers.go:
//   - the L1 is BOUNDED rather than swept (a warm replay must hit without
//     re-calling the provider), so memory never changes results;
//   - the durable L2 (TTL 48h) re-warms L1 after a process restart;
//   - payloads are validated on write so a malformed value can never become
//     a durable successful hit, and an unreadable/malformed row degrades to
//     a MISS (the cache is a replay projection, never truth).
package adapters

import (
	"context"
	"fmt"

	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	stockintelligence "github.com/Marcuss-ops/PipelineGen/internal/capabilities/stockintelligence"
	"github.com/Marcuss-ops/PipelineGen/pkg/cacheutil"
)

const (
	// stockResolveCacheNamespace is the durable L2 namespace for the
	// content-addressed stock resolve replay entries.
	stockResolveCacheNamespace = "stock_resolve"
	// stockResolveL1Capacity bounds the in-process dedupe layer. The
	// durable L2 remains the replay authority; this cache only keeps warm
	// replays from re-reading the durable store.
	stockResolveL1Capacity = 2048
)

// stockResolveCacheL1 is the process-wide bounded L1 (see the contract in
// vidrush_helpers.go: bounded LRU, no TTL sweep, re-warmed from L2).
var stockResolveCacheL1 = cacheutil.NewLRU(stockResolveL1Capacity)

type stockResolveCache struct {
	l1 *cacheutil.LRU
	l2 scriptports.VidRushCachePort
}

// NewStockResolveCache wires the canonical resolve replay cache over the
// shared durable VidRush cache. A nil L2 degrades to an L1-only cache; the
// resolver treats cache errors as misses, so either way resolution keeps
// working.
func NewStockResolveCache(l2 scriptports.VidRushCachePort) stockintelligence.ResolveCache {
	return &stockResolveCache{l1: stockResolveCacheL1, l2: l2}
}

func (c *stockResolveCache) Get(ctx context.Context, key string) ([]byte, bool, error) {
	if c == nil || key == "" {
		return nil, false, nil
	}
	if raw, ok := cacheLoad(c.l1, key); ok {
		if payload, ok := raw.([]byte); ok && len(payload) > 0 {
			return payload, true, nil
		}
	}
	if c.l2 == nil {
		return nil, false, nil
	}
	raw, hit, err := c.l2.Get(ctx, stockResolveCacheNamespace, key)
	if err != nil || !hit {
		return nil, false, err
	}
	if err := scriptports.ValidateVidRushCachePayload(raw); err != nil {
		// A malformed durable row must never become a successful hit.
		return nil, false, nil
	}
	cacheStore(c.l1, key, raw)
	return raw, true, nil
}

func (c *stockResolveCache) Put(ctx context.Context, key string, payload []byte) error {
	if c == nil || key == "" {
		return nil
	}
	if err := scriptports.ValidateVidRushCachePayload(payload); err != nil {
		return fmt.Errorf("stock resolve cache %s: validate: %w", key, err)
	}
	cacheStore(c.l1, key, payload)
	if c.l2 == nil {
		return nil
	}
	return c.l2.Put(ctx, stockResolveCacheNamespace, key, payload)
}
