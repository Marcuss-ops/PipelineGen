package adapters

import (
	"context"
	"errors"
	"testing"
	"time"

	mediadomain "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/Marcuss-ops/PipelineGen/pkg/cacheutil"
)

type scriptedImageSearcher struct {
	calls int
	err   error
	delay time.Duration
}

func (s *scriptedImageSearcher) SearchImages(context.Context, InternetImageSearchRequest) ([]scriptpkg.SegmentAssetCandidate, error) {
	s.calls++
	if s.delay > 0 {
		time.Sleep(s.delay)
	}
	if s.err != nil {
		return nil, s.err
	}
	return nil, nil
}

func entitySurfacePlan(forceRefresh bool) *scriptpkg.ResolvedGenerationPlan {
	return &scriptpkg.ResolvedGenerationPlan{
		ForceRefresh: forceRefresh,
		MediaPlan: mediadomain.MediaPlanSpec{
			ForceRefreshAssets: forceRefresh,
			Extraction: mediadomain.MediaExtractionPolicy{
				EntityImages: mediadomain.EntityImagePolicy{Enabled: true},
			},
		},
	}
}

// resetImageCaches isolates this file's global-L1 tests from the rest of the
// package (the fan-out caches are package-scoped by design).
func resetImageCaches(t *testing.T) {
	t.Helper()
	vidrushImageCache = cacheutil.NewLRU(vidrushImageL1Capacity)
	entityImageCache = cacheutil.NewLRU(entityImageL1Capacity)
	t.Cleanup(func() {
		vidrushImageCache = cacheutil.NewLRU(vidrushImageL1Capacity)
		entityImageCache = cacheutil.NewLRU(entityImageL1Capacity)
	})
}

func runImageDiscovery(t *testing.T, fanout *VidRushProviderFanout, plan *scriptpkg.ResolvedGenerationPlan) vidRushProviderOutcome {
	t.Helper()
	outcomes := make(chan vidRushProviderOutcome, 1)
	fanout.resolveInternetImageCandidates(context.Background(), plan, vidRushFanoutPlan{
		segmentID: "negative-segment", textHash: "negative-hash",
		imageQueries: []string{"Garlasco"}, perQueryLimit: 1,
	}, scriptpkg.VidRushSegmentResult{SegmentID: "negative-segment", TextHash: "negative-hash"}, outcomes)
	return <-outcomes
}

// TestInternetImageZeroResultIsReusedWithoutAProviderCall is ELIM-01's core:
// the same entity searched twice must pay the provider once, and the second
// outcome must be the same empty candidate set (not an error, not a hit that
// fabricates a candidate).
func TestInternetImageZeroResultIsReusedWithoutAProviderCall(t *testing.T) {
	resetImageCaches(t)
	searcher := &scriptedImageSearcher{delay: 150 * time.Millisecond}
	fanout := &VidRushProviderFanout{images: searcher}

	first := runImageDiscovery(t, fanout, entitySurfacePlan(false))
	if first.err != nil {
		t.Fatalf("first discovery: %v", first.err)
	}
	if searcher.calls != 1 || len(first.candidates) != 0 {
		t.Fatalf("first discovery calls=%d candidates=%d, want 1/0", searcher.calls, len(first.candidates))
	}

	started := time.Now()
	second := runImageDiscovery(t, fanout, entitySurfacePlan(false))
	if elapsed := time.Since(started); elapsed > 50*time.Millisecond {
		t.Fatalf("second discovery took %s, want the negative entry to skip the 150ms provider wait", elapsed)
	}
	if second.err != nil {
		t.Fatalf("second discovery: %v", second.err)
	}
	if searcher.calls != 1 {
		t.Fatalf("second discovery called the provider (calls=%d): a completed zero result must be reused", searcher.calls)
	}
	if len(second.candidates) != 0 || !second.allCacheHits {
		t.Fatalf("second outcome = %d candidates, allCacheHits=%v; want 0/true", len(second.candidates), second.allCacheHits)
	}
}

// TestInternetImageProviderErrorIsNeverCachedAsZero is the fail-closed guard:
// an error is an unknown state, not an empty observation.
func TestInternetImageProviderErrorIsNeverCachedAsZero(t *testing.T) {
	resetImageCaches(t)
	searcher := &scriptedImageSearcher{err: errors.New("provider exploded")}
	fanout := &VidRushProviderFanout{images: searcher}

	if outcome := runImageDiscovery(t, fanout, entitySurfacePlan(false)); outcome.err == nil {
		t.Fatal("the first discovery must surface the provider error")
	}
	if outcome := runImageDiscovery(t, fanout, entitySurfacePlan(false)); outcome.err == nil {
		t.Fatal("a provider error must be retried, not served from a negative entry")
	}
	if searcher.calls != 2 {
		t.Fatalf("provider calls = %d, want 2 (errors are never cached)", searcher.calls)
	}
}

// TestInternetImageForceRefreshBypassesNegativeEntry keeps the manual refresh
// possible: an operator must not be locked out for 15 minutes.
func TestInternetImageForceRefreshBypassesNegativeEntry(t *testing.T) {
	resetImageCaches(t)
	searcher := &scriptedImageSearcher{}
	fanout := &VidRushProviderFanout{images: searcher}

	runImageDiscovery(t, fanout, entitySurfacePlan(false))
	runImageDiscovery(t, fanout, entitySurfacePlan(false))
	if searcher.calls != 1 {
		t.Fatalf("provider calls before refresh = %d, want 1", searcher.calls)
	}

	runImageDiscovery(t, fanout, entitySurfacePlan(true))
	if searcher.calls != 2 {
		t.Fatalf("provider calls after force refresh = %d, want 2", searcher.calls)
	}
}

// TestInternetImageNegativeEntryExpires pins the TTL: an old observation is
// not a reason to skip the provider, so a stale entry cannot become permanent.
func TestInternetImageNegativeEntryExpires(t *testing.T) {
	fresh := internetImageCachePayload{Negative: true, ObservedAt: time.Now()}
	if !internetImageNegativeFresh(fresh, time.Now()) {
		t.Fatal("a just-observed negative entry must be fresh")
	}
	stale := internetImageCachePayload{Negative: true, ObservedAt: time.Now().Add(-internetImageNegativeTTL - time.Minute)}
	if internetImageNegativeFresh(stale, time.Now()) {
		t.Fatal("a negative entry older than the TTL must be stale")
	}
	legacy := internetImageCachePayload{Negative: true}
	if internetImageNegativeFresh(legacy, time.Now()) {
		t.Fatal("a negative entry without an observation time must be stale")
	}
	positive := internetImageCachePayload{ObservedAt: time.Now()}
	if internetImageNegativeFresh(positive, time.Now()) {
		t.Fatal("a positive entry is not a negative observation")
	}
}

// TestSanitizePreservesNegativeFields keeps the durable round-trip honest.
func TestSanitizePreservesNegativeFields(t *testing.T) {
	observed := time.Now().UTC().Truncate(time.Second)
	payload := sanitizeInternetImageCachePayload(internetImageCachePayload{
		Negative: true, NegativeReason: "no_results", ObservedAt: observed,
	})
	if !payload.Negative || payload.NegativeReason != "no_results" || !payload.ObservedAt.Equal(observed) {
		t.Fatalf("sanitize dropped negative metadata: %+v", payload)
	}
}
