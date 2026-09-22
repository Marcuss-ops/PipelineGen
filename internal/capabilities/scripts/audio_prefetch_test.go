package scriptgeneration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
)

// ── stubs ─────────────────────────────────────────────────────────────

type prefetchAudioStub struct {
	mu      sync.Mutex
	calls   int
	fail    map[string]error
	blockOn map[string]chan struct{}
}

func (s *prefetchAudioStub) ResolveAudioAsset(ctx context.Context, assetID string) (capabilityaudio.ResolvedAudioAsset, error) {
	s.mu.Lock()
	s.calls++
	fail := s.fail[assetID]
	block := s.blockOn[assetID]
	s.mu.Unlock()
	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
			return capabilityaudio.ResolvedAudioAsset{}, ctx.Err()
		}
	}
	if fail != nil {
		return capabilityaudio.ResolvedAudioAsset{}, fail
	}
	return capabilityaudio.ResolvedAudioAsset{AssetID: assetID, Path: "/audio/" + assetID + ".mp3", DurationUS: 1_000_000}, nil
}

func (s *prefetchAudioStub) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

type prefetchClipStub struct {
	mu      sync.Mutex
	calls   int
	fail    map[string]error
	blockOn map[string]chan struct{}
}

func (s *prefetchClipStub) ResolveClipAudioAsset(ctx context.Context, clipID string) (capabilityaudio.ResolvedAudioAsset, error) {
	s.mu.Lock()
	s.calls++
	fail := s.fail[clipID]
	block := s.blockOn[clipID]
	s.mu.Unlock()
	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
			return capabilityaudio.ResolvedAudioAsset{}, ctx.Err()
		}
	}
	if fail != nil {
		return capabilityaudio.ResolvedAudioAsset{}, fail
	}
	return capabilityaudio.ResolvedAudioAsset{AssetID: clipID, Path: "/clips/" + clipID + ".mp4", DurationUS: 2_000_000}, nil
}

func (s *prefetchClipStub) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

// ── tests ─────────────────────────────────────────────────────────────

func TestPrefetchAudioAssetsResolvesEverythingAndServesCacheHits(t *testing.T) {
	bgm := &prefetchAudioStub{}
	clips := &prefetchClipStub{}
	res, err := PrefetchAudioAssets(context.Background(),
		[]string{"bgm1"}, []string{"whoosh1"}, bgm,
		[]string{"clip-a", "clip-b"}, clips, capabilityaudio.MixVoiceoverWithDuckedClip)
	if err != nil {
		t.Fatalf("prefetch: %v", err)
	}
	if res == nil {
		t.Fatal("prefetch returned a nil result")
	}
	if res.RequestedCount() != 4 || res.CachedCount() != 4 {
		t.Fatalf("counts: requested=%d cached=%d", res.RequestedCount(), res.CachedCount())
	}
	if res.BGMSFXResolved != 2 || res.ClipAudioReady != 2 {
		t.Fatalf("split counts: bgm/sfx=%d clip=%d", res.BGMSFXResolved, res.ClipAudioReady)
	}
	if res.Degraded || len(res.Failures) != 0 {
		t.Fatalf("healthy prefetch reported degraded: %+v", res)
	}
	if res.DurationMS < 0 {
		t.Fatalf("negative duration: %d", res.DurationMS)
	}
	if res.AudioSource == nil || res.ClipAudioSource == nil {
		t.Fatal("caching adapters were not wired")
	}

	// Cache hits must not reach the real source again.
	got, err := res.ClipAudioSource.ResolveClipAudioAsset(context.Background(), "clip-a")
	if err != nil || got.Path != "/clips/clip-a.mp4" {
		t.Fatalf("cache hit: %v %q", err, got.Path)
	}
	if clips.callCount() != 2 {
		t.Fatalf("cache hit re-resolved through the real source: calls=%d", clips.callCount())
	}
	// A miss falls THROUGH to the real source, so a partial prefetch is
	// never worse than no prefetch at all.
	miss, err := res.ClipAudioSource.ResolveClipAudioAsset(context.Background(), "clip-missing")
	if err != nil || miss.Path != "/clips/clip-missing.mp4" {
		t.Fatalf("cache miss fall-through: %v %q", err, miss.Path)
	}
	if clips.callCount() != 3 {
		t.Fatalf("fall-through did not call the real source: calls=%d", clips.callCount())
	}
}

func TestPrefetchAudioAssetsKeepsResolvedAssetsWhenOneAssetFails(t *testing.T) {
	clips := &prefetchClipStub{fail: map[string]error{"clip-bad": errors.New("drive download failed")}}
	res, err := PrefetchAudioAssets(context.Background(), nil, nil, nil,
		[]string{"clip-ok", "clip-bad", "clip-also-ok"}, clips, capabilityaudio.MixVoiceoverWithDuckedClip)
	if err != nil {
		t.Fatalf("one failing asset must not fail the prefetch: %v", err)
	}
	if res == nil {
		t.Fatal("prefetch returned a nil result despite partial success")
	}
	if !res.Degraded {
		t.Fatal("partial failure was not reported as degraded")
	}
	if res.ClipAudioReady != 2 || res.CachedCount() != 2 {
		t.Fatalf("resolved assets were discarded: cached=%d clip=%d", res.CachedCount(), res.ClipAudioReady)
	}
	if res.ClipAudioSource == nil {
		t.Fatal("caching adapter was dropped because of a partial failure")
	}
	if len(res.Failures) != 1 || !strings.Contains(res.Failures[0], "clip-bad") {
		t.Fatalf("failure evidence missing: %+v", res.Failures)
	}
	if _, err := res.ClipAudioSource.ResolveClipAudioAsset(context.Background(), "clip-ok"); err != nil {
		t.Fatalf("healthy asset not served from cache: %v", err)
	}
}

func TestPrefetchAudioAssetsNilRequiredSourceFailsClosed(t *testing.T) {
	if _, err := PrefetchAudioAssets(context.Background(), []string{"bgm1"}, nil, nil, nil, nil, capabilityaudio.MixVoiceoverOnly); err == nil {
		t.Fatal("nil audio source with BGM intents must fail closed")
	}
	if _, err := PrefetchAudioAssets(context.Background(), nil, nil, nil, []string{"clip-a"}, nil, capabilityaudio.MixVoiceoverWithDuckedClip); err == nil {
		t.Fatal("nil clip audio source with VOICEOVER_DUCKED_CLIP must fail closed")
	}
}

func TestPrefetchAudioAssetsDeadlineKeepsPartialResultAndReturns(t *testing.T) {
	blocked := make(chan struct{})
	defer close(blocked)
	clips := &prefetchClipStub{blockOn: map[string]chan struct{}{"clip-slow": blocked}}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel()

	done := make(chan struct{})
	var res *AudioPrefetchResult
	var err error
	go func() {
		defer close(done)
		res, err = PrefetchAudioAssets(ctx, nil, nil, nil,
			[]string{"clip-fast", "clip-slow"}, clips, capabilityaudio.MixVoiceoverWithDuckedClip)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("prefetch ignored its deadline and blocked the caller")
	}
	if err != nil {
		t.Fatalf("deadline must degrade, not fail: %v", err)
	}
	if res == nil || !res.Degraded {
		t.Fatalf("deadline not reported: %+v", res)
	}
	if res.ClipAudioReady != 1 {
		t.Fatalf("asset resolved before the deadline was discarded: ready=%d", res.ClipAudioReady)
	}
}

func TestPrefetchAudioAssetsWithoutIntentsIsNoop(t *testing.T) {
	res, err := PrefetchAudioAssets(context.Background(), nil, nil, nil, nil, nil, capabilityaudio.MixVoiceoverOnly)
	if err != nil {
		t.Fatalf("no-op prefetch: %v", err)
	}
	if res == nil || res.RequestedCount() != 0 || res.CachedCount() != 0 || res.Degraded {
		t.Fatalf("unexpected no-op result: %+v", res)
	}
}

// TestAudioPrefetchResultExposesSummaryButNotRuntimeCarriers pins the polling
// contract: the counts/outcomes are serialized, the runtime carriers (paths and
// adapters) are not.
func TestAudioPrefetchResultExposesSummaryButNotRuntimeCarriers(t *testing.T) {
	clips := &prefetchClipStub{}
	res, err := PrefetchAudioAssets(context.Background(), []string{"bgm1"}, nil, &prefetchAudioStub{},
		[]string{"clip-a"}, clips, capabilityaudio.MixVoiceoverWithDuckedClip)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	payload := string(raw)
	for _, want := range []string{`"bgm_requested":1`, `"bgm_sfx_resolved":1`, `"clip_audio_ready":1`, `"assets"`} {
		if !strings.Contains(payload, want) {
			t.Fatalf("summary missing %s in %s", want, payload)
		}
	}
	for _, forbidden := range []string{"/clips/clip-a.mp4", "ClipAudioSource", "resolved_audio"} {
		if strings.Contains(payload, forbidden) {
			t.Fatalf("runtime carrier leaked into the payload: %s in %s", forbidden, payload)
		}
	}
}

// TestPrefetchAudioAssetsResolvesBGMSFXThroughCanonicalID pins the id contract:
// the payload addresses BGM/SFX by public alias, the media registry by Drive
// identity, and the COMPILE boundary maps the alias through
// audio.CanonicalAssetID. The prefetch must use the same id, otherwise it
// reports an unresolvable asset for an id the compile resolves happily.
func TestPrefetchAudioAssetsResolvesBGMSFXThroughCanonicalID(t *testing.T) {
	canonical := capabilityaudio.CanonicalAssetID("bgm1")
	audio := &prefetchAudioStub{}
	res, err := PrefetchAudioAssets(context.Background(), []string{"bgm1"}, nil, audio, nil, nil, capabilityaudio.MixVoiceoverOnly)
	if err != nil {
		t.Fatalf("prefetch: %v", err)
	}
	if res.Degraded || res.BGMSFXResolved != 1 {
		t.Fatalf("alias resolution: degraded=%v resolved=%d failures=%v", res.Degraded, res.BGMSFXResolved, res.Failures)
	}
	if len(res.Assets) != 1 || res.Assets[0].AssetID != "bgm1" || res.Assets[0].ResolvedID != canonical {
		t.Fatalf("report must keep the public alias and record the registry id: %+v", res.Assets)
	}
	// Both spellings must hit the cache: the compile asks for the canonical id.
	if _, err := res.AudioSource.ResolveAudioAsset(context.Background(), canonical); err != nil {
		t.Fatalf("canonical lookup missed: %v", err)
	}
	if _, err := res.AudioSource.ResolveAudioAsset(context.Background(), "bgm1"); err != nil {
		t.Fatalf("alias lookup missed: %v", err)
	}
	if audio.callCount() != 1 {
		t.Fatalf("cache lookups re-resolved through the real source: calls=%d", audio.callCount())
	}
}

func TestPrefetchAudioAssetsDedupesRequestedAssets(t *testing.T) {
	clips := &prefetchClipStub{}
	res, err := PrefetchAudioAssets(context.Background(), nil, nil, nil,
		[]string{"clip-a", "clip-a", "clip-b", "clip-a"}, clips, capabilityaudio.MixVoiceoverWithDuckedClip)
	if err != nil {
		t.Fatal(err)
	}
	if res.ClipAudioRequested != 2 || res.ClipAudioReady != 2 {
		t.Fatalf("duplicate clip ids were not collapsed: requested=%d ready=%d", res.ClipAudioRequested, res.ClipAudioReady)
	}
	if clips.callCount() != 2 {
		t.Fatalf("duplicate clip ids resolved twice: calls=%d", clips.callCount())
	}
}

func TestMergeAudioCompileTimingsKeepsMeasuredClipPrepare(t *testing.T) {
	compiled := AudioCompileTimings{AudioAssetResolveMS: 12, TimelineCompileMS: 34, ClipAudioPrepareMS: 0, AudioPlanCompileMS: 56}
	merged := mergeAudioCompileTimings(compiled, 31_700)
	if merged.ClipAudioPrepareMS != 31_700 {
		t.Fatalf("measured clip prepare lost: %d", merged.ClipAudioPrepareMS)
	}
	if merged.AudioAssetResolveMS != 12 || merged.TimelineCompileMS != 34 || merged.AudioPlanCompileMS != 56 {
		t.Fatalf("compile-owned subtimings were clobbered: %+v", merged)
	}
	if fmt.Sprint(compiled.ClipAudioPrepareMS) != "0" {
		t.Fatal("merge must not mutate its input")
	}
}
