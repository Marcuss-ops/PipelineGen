package adapters

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	"github.com/Marcuss-ops/PipelineGen/pkg/cacheutil"
)

func loadVidRushPersistentJSON(ctx context.Context, cache scriptports.VidRushCachePort, namespace, key string, dst any) (bool, error) {
	if cache == nil {
		return false, nil
	}
	raw, hit, err := cache.Get(ctx, namespace, key)
	if err != nil || !hit {
		return false, err
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		return false, fmt.Errorf("vidrush cache %s/%s: decode: %w", namespace, key, err)
	}
	return true, nil
}

func storeVidRushPersistentJSON(ctx context.Context, cache scriptports.VidRushCachePort, namespace, key string, value any) error {
	if cache == nil {
		return nil
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("vidrush cache %s/%s: encode: %w", namespace, key, err)
	}
	if err := scriptports.ValidateVidRushCachePayload(raw); err != nil {
		return fmt.Errorf("vidrush cache %s/%s: validate: %w", namespace, key, err)
	}
	return cache.Put(ctx, namespace, key, raw)
}

// ── L1 caches (bounded LRU) ────────────────────────────────────────────
//
// The VidRush L1 caches are no-TTL by contract (a warm replay of the same
// query must hit without re-calling the provider), so they must be BOUNDED
// rather than swept. They are canonical pkg/cacheutil.LRU instances:
// capacity is fixed at construction, eviction is amortized O(1), and there
// is NO background goroutine to leak or to destroy every warm entry at once.
//
// This replaces the historical unbounded sync.Map + 10-minute full-sweep
// janitor (the canonical anti-pattern already retired in cliprender). The
// durable L2 cache (VidRushCachePort, TTL 48h) re-warms L1 on the next
// replay with identical HIT_EXACT semantics, so bounding memory never
// changes results.
const (
	vidrushImageL1Capacity        = 4096
	vidrushBindingL1Capacity      = 4096
	vidrushMaterializedL1Capacity = 1024
)

var (
	vidrushImageCache        = cacheutil.NewLRU(vidrushImageL1Capacity)
	vidrushBindingCache      = cacheutil.NewLRU(vidrushBindingL1Capacity)
	vidrushMaterializedCache = cacheutil.NewLRU(vidrushMaterializedL1Capacity)
)

// weightedKeywordValues projects a profile's weighted keyword stream onto the
// legacy string surface consumed by SegmentInsights and the ad-hoc query
// builders. It is the only legal way to read Keywords/VisualTerms back as a
// plain list; values keep the profile's deterministic order.

func versionedSegmentCacheKey(stage string, version scriptports.CacheVersion, parts ...string) string {
	return segmentCacheKey(append([]string{scriptports.VersionedCacheNamespace(stage, version)}, parts...)...)
}

func segmentCacheKey(parts ...string) string {
	h := sha256.New()
	for _, p := range parts {
		_, _ = h.Write([]byte(p))
		_, _ = h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

func cacheLoad(cache *cacheutil.LRU, key string) (any, bool) {
	if cache == nil || key == "" {
		return nil, false
	}
	return cache.Get(key)
}

func cacheStore(cache *cacheutil.LRU, key string, value any) {
	if cache == nil || key == "" {
		return
	}
	cache.Put(key, value)
}
