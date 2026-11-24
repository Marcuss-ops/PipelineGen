package vidrush

import (
	"context"
	"strings"
	"testing"
	"time"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/Marcuss-ops/PipelineGen/pkg/cacheutil"
	"github.com/stretchr/testify/require"
)

// TestInternetImageSegmentCacheKeyHasOneSpelling is the anti-drift guard: the
// fan-out key builder and the media-resolver key must be the same function.
func TestInternetImageSegmentCacheKeyHasOneSpelling(t *testing.T) {
	plan := &scriptpkg.ResolvedGenerationPlan{Language: "it", Model: "model-x", PromptVersion: "prompt-v7"}
	fanout := &vidRushFanoutPlan{segmentID: "segment-1", textHash: "hash-1", perQueryLimit: 3}
	want := internetImageSegmentCacheKey("segment-1", "hash-1", "it", "model-x", "prompt-v7", 3)
	require.Equal(t, want, vidRushFanoutImageCacheKey(fanout, plan))
}

// TestInternetImageHitPoliciesAreExplicit pins the one difference between the
// two callers as a named policy instead of a copied block.
func TestInternetImageHitPoliciesAreExplicit(t *testing.T) {
	now := time.Now()
	positive := internetImageCachePayload{Candidates: []scriptpkg.SegmentAssetCandidate{{AssetID: "asset-1"}}}
	freshNegative := internetImageCachePayload{Negative: true, ObservedAt: now}
	staleNegative := internetImageCachePayload{Negative: true, ObservedAt: now.Add(-time.Hour)}
	empty := internetImageCachePayload{}

	cases := []struct {
		name    string
		policy  internetImageHitPolicy
		payload internetImageCachePayload
		want    bool
	}{
		{"replay accepts a positive payload", internetImageReplayPolicy, positive, true},
		{"replay accepts a durable empty result", internetImageReplayPolicy, empty, true},
		{"replay rejects a fan-out negative observation", internetImageReplayPolicy, freshNegative, false},
		{"observation accepts a positive payload", internetImageObservationPolicy, positive, true},
		{"observation accepts a fresh negative", internetImageObservationPolicy, freshNegative, true},
		{"observation rejects a stale negative", internetImageObservationPolicy, staleNegative, false},
		{"observation rejects an empty non-negative", internetImageObservationPolicy, empty, false},
	}
	for _, tc := range cases {
		hit, _ := tc.policy.accept(tc.payload, now)
		require.Equal(t, tc.want, hit, tc.name)
	}
	require.True(t, internetImageObservationPolicy.VerifyCachedCandidates,
		"the fan-out replays a sanitized discovery projection and must re-verify it")
	require.False(t, internetImageReplayPolicy.VerifyCachedCandidates,
		"the media resolver re-scores cached candidates in its own loop")
}

// TestInternetImageSegmentCacheReplayKeepsEmptyDurable pins the media-resolver
// contract: an empty completed search is a durable L2 replay, never an L1 entry.
func TestInternetImageSegmentCacheReplayKeepsEmptyDurable(t *testing.T) {
	l1 := cacheutil.NewLRU(8)
	l2 := newMemoryVidRushCache()
	cache := newInternetImageSegmentCache(l1, l2)
	key := internetImageSegmentCacheKey("segment-empty", "hash-empty", "it", "model", "prompt", 2)

	require.NoError(t, cache.Store(context.Background(), key, internetImageCachePayload{}, internetImageReplayPolicy))
	if _, ok := cacheLoad(l1, key); ok {
		t.Fatal("an empty replay payload must stay out of the no-TTL L1")
	}
	payload, hit, err := cache.Load(context.Background(), key, internetImageReplayPolicy, time.Now())
	require.NoError(t, err)
	require.True(t, hit, "the durable empty result must replay")
	require.Empty(t, payload.Candidates)
}

// TestInternetImageSegmentCacheObservationExpires pins the fan-out contract:
// a fresh negative skips the provider, a stale one does not, and the same row
// is a miss for the media resolver (cross-policy read).
func TestInternetImageSegmentCacheObservationExpires(t *testing.T) {
	l1 := cacheutil.NewLRU(8)
	l2 := newMemoryVidRushCache()
	cache := newInternetImageSegmentCache(l1, l2)
	key := internetImageSegmentCacheKey("segment-negative", "hash-negative", "it", "model", "prompt", 2)
	payload := internetImageCachePayload{Negative: true, NegativeReason: "no_results", ObservedAt: time.Now()}

	require.NoError(t, cache.Store(context.Background(), key, payload, internetImageObservationPolicy))

	got, hit, err := cache.Load(context.Background(), key, internetImageObservationPolicy, time.Now())
	require.NoError(t, err)
	require.True(t, hit)
	require.True(t, got.Negative)
	require.Equal(t, "no_results", got.NegativeReason)

	_, hit, err = cache.Load(context.Background(), key, internetImageReplayPolicy, time.Now())
	require.NoError(t, err)
	require.False(t, hit, "a fan-out negative observation is not a media-resolver replay")

	_, hit, err = cache.Load(context.Background(), key, internetImageObservationPolicy, time.Now().Add(internetImageNegativeTTL+time.Minute))
	require.NoError(t, err)
	require.False(t, hit, "an expired observation must not skip the provider")
}

// TestInternetImageSegmentCacheStoreStripsMaterializationFromL2 pins the L1/L2
// split: L1 keeps the run-scoped candidates, L2 only the discovery projection.
func TestInternetImageSegmentCacheStoreStripsMaterializationFromL2(t *testing.T) {
	l1 := cacheutil.NewLRU(8)
	l2 := newMemoryVidRushCache()
	cache := newInternetImageSegmentCache(l1, l2)
	key := internetImageSegmentCacheKey("segment-positive", "hash-positive", "it", "model", "prompt", 2)
	candidate := scriptpkg.SegmentAssetCandidate{
		AssetID: "asset-1", Provider: scriptpkg.VidRushProviderInternetImages,
		DriveLink: "https://drive.google.com/file/d/abc/view", ContentSHA256: strings.Repeat("a", 64),
	}
	require.NoError(t, cache.Store(context.Background(), key, internetImageCachePayload{Candidates: []scriptpkg.SegmentAssetCandidate{candidate}}, internetImageObservationPolicy))

	rawL1, ok := cacheLoad(l1, key)
	require.True(t, ok)
	require.Equal(t, candidate.DriveLink, rawL1.(internetImageCachePayload).Candidates[0].DriveLink,
		"L1 must keep the run-scoped materialization state")

	rawL2, ok, err := l2.Get(context.Background(), internetImageSegmentCacheNamespace, key)
	require.NoError(t, err)
	require.True(t, ok)
	require.NotContains(t, string(rawL2), candidate.DriveLink, "L2 must never persist a Drive link")
	require.NotContains(t, string(rawL2), candidate.ContentSHA256, "L2 must never persist a content address")
}
