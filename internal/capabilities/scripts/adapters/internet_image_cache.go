// internet_image_cache.go — the ONE segment-level internet-image cache.
//
// Two paths read the same durable rows: the VidRush provider fan-out
// (vidrush_internet_image_candidates.go) and the media-resolver postprocessor
// (media_resolver_image_segments.go). Before this file each one hand-rolled its
// own L1 lookup, L2 load, negative-entry check and write policy; the rows were
// the same but the rules were not, so one path could replay a negative
// observation as a materialized hit while the other re-queried the provider.
//
// The key and the hit policy now live here. The two callers differ on purpose,
// and the difference is expressed as a named policy instead of a copy:
//
//   - replayPolicy is the media-resolver stage: a completed empty search is a
//     durable replay (the warm run must be deterministic without a provider
//     call), and cached candidates are already the post-verification set the
//     stage re-scores in its own loop.
//   - observationPolicy is the provider fan-out: a completed empty search is a
//     15-minute observation, never a fact, and cached candidates are a
//     sanitized discovery projection that must be re-verified on replay.
//
// Both policies share one key function, one L1/L2 shape and one negative-TTL
// rule; adding a cache site means choosing a policy, not writing a third copy.
package adapters

import (
	"context"
	"fmt"
	"time"

	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
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
