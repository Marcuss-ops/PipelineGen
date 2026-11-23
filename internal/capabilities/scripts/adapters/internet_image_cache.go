package adapters

import (
	"context"
	"fmt"
	"time"

	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	stockintelligence "github.com/Marcuss-ops/PipelineGen/internal/capabilities/stockintelligence"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/Marcuss-ops/PipelineGen/pkg/cacheutil"
)

// internetImageSegmentCacheSchema versions the key shape. A change that makes
// an older row mean something different must bump it.
const internetImageSegmentCacheSchema = "internet-images-assets-v5"

// internetImageSegmentCacheNamespace is the durable L2 namespace shared by both
// callers. It is part of the contract: a row written by one path is readable by
// the other, which is exactly why the policies have to be explicit.
const internetImageSegmentCacheNamespace = "internet_images"

// internetImageHitPolicy states how a stored payload may satisfy a lookup.
type internetImageHitPolicy struct {
	// NegativeTTL > 0 treats a Negative payload as a short-lived observation
	// worth this duration. 0 means negative payloads are never replayable
	// (they can only come from a writer with a different policy).
	NegativeTTL time.Duration
	// EmptyReplay accepts a completed empty (non-negative) payload as a hit.
	EmptyReplay bool
	// VerifyCachedCandidates documents that the payload is a sanitized
	// discovery projection which the caller must re-verify on replay.
	VerifyCachedCandidates bool
}

var (
	// internetImageReplayPolicy: deterministic warm replay for the
	// media-resolver stage. Empty is a stored result; negative observations
	// written by the fan-out are a miss (they are not this stage's fact).
	internetImageReplayPolicy = internetImageHitPolicy{
		NegativeTTL: 0,
		EmptyReplay: true,
	}
	// internetImageObservationPolicy: the provider fan-out. Empty is a
	// 15-minute observation, and cached candidates are re-verified.
	internetImageObservationPolicy = internetImageHitPolicy{
		NegativeTTL:            internetImageNegativeTTL,
		EmptyReplay:            false,
		VerifyCachedCandidates: true,
	}
)

// internetImageSegmentCache is the bounded L1 + durable L2 pair behind one
// lookup/store API. L1 is no-TTL and run-scoped; L2 holds discovery only
// (materialization state is stripped on write by sanitize).
type internetImageSegmentCache struct {
	l1 *cacheutil.LRU
	l2 scriptports.VidRushCachePort
}

func newInternetImageSegmentCache(l1 *cacheutil.LRU, l2 scriptports.VidRushCachePort) internetImageSegmentCache {
	return internetImageSegmentCache{l1: l1, l2: l2}
}

// internetImageSegmentCacheKey is the ONE identity of a segment-level image
// result: schema + segment identity + the plan knobs that change the provider
// answer. Query text is deliberately absent (an LLM wording change must not
// invalidate a warm replay).
func internetImageSegmentCacheKey(segmentID, textHash, language, model, promptVersion string, perQueryLimit int) string {
	return segmentCacheKey(
		internetImageSegmentCacheSchema,
		segmentID,
		textHash,
		language,
		model,
		promptVersion,
		fmt.Sprintf("%d", perQueryLimit),
	)
}

// Load applies the policy to L1 first, then to the durable row, and re-warms
// L1 from L2 with the same rule the pre-refactor readers used: only a payload
// with candidates or a negative observation is promoted.
func (c internetImageSegmentCache) Load(ctx context.Context, key string, policy internetImageHitPolicy, now time.Time) (internetImageCachePayload, bool, error) {
	if key == "" {
		return internetImageCachePayload{}, false, nil
	}
	if raw, ok := cacheLoad(c.l1, key); ok {
		if payload, ok := raw.(internetImageCachePayload); ok {
			if hit, accepted := policy.accept(payload, now); hit {
				return accepted, true, nil
			}
		}
	}
	var persisted internetImageCachePayload
	hit, err := loadVidRushPersistentJSON(ctx, c.l2, internetImageSegmentCacheNamespace, key, &persisted)
	if err != nil || !hit {
		return internetImageCachePayload{}, false, err
	}
	persisted = sanitizeInternetImageCachePayload(persisted)
	if len(persisted.Candidates) > 0 || persisted.Negative {
		cacheStore(c.l1, key, persisted)
	}
	if hit, accepted := policy.accept(persisted, now); hit {
		return accepted, true, nil
	}
	return internetImageCachePayload{}, false, nil
}

// Store writes the payload with the policy's rules: L1 keeps the run-scoped
// value (it may carry verdicts the L2 projection strips), L2 always receives
// the sanitized discovery projection when the policy can replay it.
func (c internetImageSegmentCache) Store(ctx context.Context, key string, payload internetImageCachePayload, policy internetImageHitPolicy) error {
	if key == "" {
		return nil
	}
	payload.Candidates = append([]scriptpkg.SegmentAssetCandidate(nil), payload.Candidates...)
	if len(payload.Candidates) > 0 || payload.Negative {
		cacheStore(c.l1, key, payload)
	}
	if !policy.EmptyReplay && len(payload.Candidates) == 0 && !payload.Negative {
		return nil
	}
	return storeVidRushPersistentJSON(ctx, c.l2, internetImageSegmentCacheNamespace, key, sanitizeInternetImageCachePayload(payload))
}

// accept decides whether a stored payload satisfies the policy. A negative
// payload is accepted only while fresh under the policy TTL; an empty positive
// payload is accepted only under EmptyReplay.
func (p internetImageHitPolicy) accept(payload internetImageCachePayload, now time.Time) (bool, internetImageCachePayload) {
	if payload.Negative {
		return internetImageNegativeFreshWithin(payload, now, p.NegativeTTL), payload
	}
	if len(payload.Candidates) == 0 {
		return p.EmptyReplay, payload
	}
	return true, payload
}

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
