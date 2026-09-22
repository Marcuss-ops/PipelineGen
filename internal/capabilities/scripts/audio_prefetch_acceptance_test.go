package scriptgeneration

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
)

// This file is the ACCEPTANCE assertion the prefetch was missing: the unit tests
// pinned each half (the prefetch caches, the adapters serve hits, a miss falls
// through), but nothing asserted the property the whole change exists for —
//
//	with a COLD cache, the prefetch delivers every planned asset, and the
//	synchronous prepare behind it pays ~0
//
// "~0" is asserted as two independent facts rather than a wall-clock threshold
// alone, because a timing assertion on its own can pass for the wrong reason:
//
//  1. the real sources receive ZERO additional calls during the synchronous
//     prepare (deterministic: the cache served every asset), and
//  2. the synchronous prepare completes in less than ONE asset's resolution
//     latency (so it demonstrably did not wait for even a single download).
//
// The same test measures the cold path first, so the contrast is measured in the
// same run rather than quoted from a benchmark document.

// assetSourceStub is a real-ish source: every resolution costs a fixed latency
// (that is the I/O the prefetch is supposed to move off the critical path) and
// materializes an actual file, because prepareClipAudioAssets verifies the
// resolved path with os.Stat and clamps intents against the resolved duration.
type assetSourceStub struct {
	mu      sync.Mutex
	calls   int
	latency time.Duration
	dir     string
}

func newAssetSourceStub(t *testing.T, latency time.Duration) *assetSourceStub {
	t.Helper()
	return &assetSourceStub{latency: latency, dir: t.TempDir()}
}

func (s *assetSourceStub) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

// resolve implements the shared body of both stub methods. It honours the
// caller's context, which is what makes the deadline test below meaningful: a
// source that resolved regardless of the context could never demonstrate the
// fall-through path.
func (s *assetSourceStub) resolve(ctx context.Context, id string) (capabilityaudio.ResolvedAudioAsset, error) {
	s.mu.Lock()
	s.calls++
	s.mu.Unlock()
	select {
	case <-time.After(s.latency):
	case <-ctx.Done():
		return capabilityaudio.ResolvedAudioAsset{}, ctx.Err()
	}
	path := filepath.Join(s.dir, id+".mp4")
	if err := os.WriteFile(path, []byte("audio-bytes"), 0o600); err != nil {
		return capabilityaudio.ResolvedAudioAsset{}, err
	}
	// 4 s of source: longer than every planned source window, so the duration
	// clamp is a no-op and cannot mask a resolution failure.
	return capabilityaudio.ResolvedAudioAsset{AssetID: id, Path: path, DurationUS: 4_000_000}, nil
}

func (s *assetSourceStub) ResolveAudioAsset(ctx context.Context, id string) (capabilityaudio.ResolvedAudioAsset, error) {
	return s.resolve(ctx, id)
}

func (s *assetSourceStub) ResolveClipAudioAsset(ctx context.Context, id string) (capabilityaudio.ResolvedAudioAsset, error) {
	return s.resolve(ctx, id)
}

// clipAudioPlan builds the GenerateResult prepareClipAudioAssets walks: one
// scene per clip, each declaring an AudioClip intent for its own clip, which is
// the shape VOICEOVER_DUCKED_CLIP produces.
func clipAudioPlan(clipIDs []string) *GenerateResult {
	result := &GenerateResult{}
	for _, id := range clipIDs {
		result.Scenes = append(result.Scenes, Scene{
			ID:    "scene-" + id,
			Clips: []*ClipReference{{ID: id}},
			AudioIntents: []capabilityaudio.AudioIntent{{
				Mode:             capabilityaudio.AudioClip,
				ClipAssetID:      id,
				SourceDurationUS: 3_000_000,
			}},
		})
	}
	return result
}

// TestColdCachePrefetchDeliversEveryAssetAndLeavesNothingForTheSyncPrepare is the
// end-to-end acceptance assertion for the P1.1 audio prefetch.
func TestColdCachePrefetchDeliversEveryAssetAndLeavesNothingForTheSyncPrepare(t *testing.T) {
	const perAssetLatency = 20 * time.Millisecond
	bgmIDs := []string{"bgm1"}
	sfxIDs := []string{"whoosh1", "impact1"}
	clipIDs := []string{"clip-a", "clip-b", "clip-c"}
	const planned = 6 // 1 BGM + 2 SFX + 3 clip tracks

	ctx := context.Background()
	policy := capabilityaudio.MixVoiceoverWithDuckedClip

	// ── 1. Baseline: NO prefetch. The synchronous prepare pays every clip
	// resolution itself. This is the cost the prefetch has to remove, measured
	// here instead of quoted.
	coldSource := newAssetSourceStub(t, perAssetLatency)
	coldStart := time.Now()
	if ms, err := prepareClipAudioAssets(ctx, clipAudioPlan(clipIDs), coldSource, policy); err != nil {
		t.Fatalf("cold prepare: %v", err)
	} else if ms < perAssetLatency.Milliseconds() {
		t.Fatalf("cold prepare reported %dms for %d clip resolutions; the measurement is not real", ms, len(clipIDs))
	}
	coldElapsed := time.Since(coldStart)
	if got := coldSource.count(); got != len(clipIDs) {
		t.Fatalf("cold prepare resolved %d of %d clips", got, len(clipIDs))
	}

	// ── 2. The prefetch, over a COLD cache: fresh sources, and every planned
	// asset requested (BGM alias + SFX + clip tracks).
	audioSource := newAssetSourceStub(t, perAssetLatency)
	clipSource := newAssetSourceStub(t, perAssetLatency)
	prefetch, err := PrefetchAudioAssets(ctx, bgmIDs, sfxIDs, audioSource, clipIDs, clipSource, policy)
	if err != nil {
		t.Fatalf("prefetch: %v", err)
	}
	if prefetch == nil {
		t.Fatal("prefetch returned a nil result")
	}
	if prefetch.RequestedCount() != planned {
		t.Fatalf("prefetch was asked for %d assets, want %d", prefetch.RequestedCount(), planned)
	}
	if prefetch.CachedCount() != planned {
		t.Fatalf("cold-cache prefetch delivered %d of %d assets (failures: %v)",
			prefetch.CachedCount(), planned, prefetch.Failures)
	}
	if prefetch.Degraded {
		t.Fatalf("a fully delivered prefetch reported degraded: %+v", prefetch.Failures)
	}
	if prefetch.ClipAudioReady != len(clipIDs) || prefetch.BGMSFXResolved != len(bgmIDs)+len(sfxIDs) {
		t.Fatalf("delivery split: clip_audio_ready=%d bgm_sfx_resolved=%d",
			prefetch.ClipAudioReady, prefetch.BGMSFXResolved)
	}
	// The prefetch, not the sync path, is what talked to the real sources.
	if got := clipSource.count(); got != len(clipIDs) {
		t.Fatalf("prefetch resolved %d of %d clip tracks", got, len(clipIDs))
	}
	if got := audioSource.count(); got != len(bgmIDs)+len(sfxIDs) {
		t.Fatalf("prefetch resolved %d of %d BGM/SFX assets", got, len(bgmIDs)+len(sfxIDs))
	}

	// ── 3. The synchronous prepare, through the prefetch's caching adapters.
	// It must find every clip already materialized.
	prepared := clipAudioPlan(clipIDs)
	clipCallsBefore := clipSource.count()
	syncStart := time.Now()
	syncMS, err := prepareClipAudioAssets(ctx, prepared, prefetch.ClipAudioSource, policy)
	syncElapsed := time.Since(syncStart)
	if err != nil {
		t.Fatalf("synchronous prepare after a delivering prefetch: %v", err)
	}

	// Fact 1: zero asset I/O on the synchronous path.
	if got := clipSource.count(); got != clipCallsBefore {
		t.Fatalf("synchronous prepare re-resolved %d clip track(s); the prefetch delivered nothing usable",
			got-clipCallsBefore)
	}
	// Fact 2: it did not wait for even one asset's resolution.
	if syncElapsed >= perAssetLatency {
		t.Fatalf("synchronous prepare took %v, i.e. it waited for at least one resolution (latency=%v, reported=%dms)",
			syncElapsed, perAssetLatency, syncMS)
	}
	// And the contrast with the cold path is measured in the same run.
	if syncElapsed >= coldElapsed {
		t.Fatalf("prefetched prepare (%v) was not faster than the cold prepare (%v)", syncElapsed, coldElapsed)
	}

	// The prepare's outcome must still be complete: every clip carries the
	// path the compile boundary needs, so "~0" is not "did nothing".
	for i := range prepared.Scenes {
		clip := prepared.Scenes[i].Clips[0]
		if clip.AudioPath == "" {
			t.Fatalf("scene %s clip %s was left without a materialized audio path", prepared.Scenes[i].ID, clip.ID)
		}
		if _, statErr := os.Stat(clip.AudioPath); statErr != nil {
			t.Fatalf("scene %s clip %s points at an unreadable path %q: %v",
				prepared.Scenes[i].ID, clip.ID, clip.AudioPath, statErr)
		}
	}
}

// TestPrefetchKeepsTheSyncPathCorrectWhenTheCacheIsCold is the fail-soft half:
// when the prefetch could NOT deliver (deadline expired before any clip was
// resolved), the synchronous prepare must still do the work — with the cache
// adapter falling through to the real source — instead of failing the run.
// Without this, "the prefetch delivered everything" could be satisfied by a
// prefetch that makes the sync path unable to work at all.
func TestPrefetchKeepsTheSyncPathCorrectWhenTheCacheIsCold(t *testing.T) {
	const perAssetLatency = 5 * time.Millisecond
	clipIDs := []string{"clip-a", "clip-b"}
	policy := capabilityaudio.MixVoiceoverWithDuckedClip

	clipSource := newAssetSourceStub(t, perAssetLatency)
	timeoutCtx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()
	// The deadline is already spent: nothing can be cached, which is the worst
	// case the sync path must survive.
	prefetch, err := PrefetchAudioAssets(timeoutCtx, nil, nil, nil, clipIDs, clipSource, policy)
	if err != nil {
		t.Fatalf("a spent deadline must degrade, not fail: %v", err)
	}
	if prefetch.ClipAudioSource == nil {
		t.Fatal("the caching adapter was dropped, so the sync path has no source at all")
	}
	if prefetch.CachedCount() != 0 {
		t.Fatalf("the spent deadline cached %d asset(s); the fall-through path is untested", prefetch.CachedCount())
	}

	prepared := clipAudioPlan(clipIDs)
	before := clipSource.count()
	if _, err := prepareClipAudioAssets(context.Background(), prepared, prefetch.ClipAudioSource, policy); err != nil {
		t.Fatalf("synchronous prepare must fall through to the real source: %v", err)
	}
	if got := clipSource.count(); got != before+len(clipIDs) {
		t.Fatalf("fall-through resolved %d of %d clips", got-before, len(clipIDs))
	}
	for i := range prepared.Scenes {
		if prepared.Scenes[i].Clips[0].AudioPath == "" {
			t.Fatalf("scene %s was left without audio after the fall-through", prepared.Scenes[i].ID)
		}
	}
}
