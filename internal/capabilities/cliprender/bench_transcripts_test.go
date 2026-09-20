package cliprender

// bench_transcripts_test.go owns scenario 6 of the canonical clip.render
// benchmark: transcript reuse.
//
// clip.render is a RENDER step: the default policy is `reuse`, so a batch over
// one source runs ZERO ASR passes when the canonical READY text track exists,
// and FAILS CLOSED when it does not. `generate` remains only as an explicit
// manual repair and re-runs ASR per clip. This is measured here with a
// counting ASR resolver that persists like the real one, driving the real
// Preparer.
//
// The legacy `reuse_or_generate` implicit-ASR mode was DELETED in the
// 2026-09-13 audit, so the scenarios that measured "one ASR pass per source on
// a miss" no longer describe the system and were removed with it.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"
)

// benchTranscriptCache is the transcript plane with a persisted, content-keyed
// cache and a configurable ASR cost. Persist=true makes a generated transcript
// reusable, exactly like the canonical resolver writing a READY text track.
type benchTranscriptCache struct {
	asrMS time.Duration

	mu          sync.Mutex
	byKey       map[string]*TranscriptResult
	genCalls    int
	lookupCalls int
	lookupHits  int
}

func newBenchTranscriptCache(asrMS time.Duration) *benchTranscriptCache {
	return &benchTranscriptCache{asrMS: asrMS, byKey: map[string]*TranscriptResult{}}
}

func benchTranscriptKey(in TranscriptInput) string {
	return fmt.Sprintf("%s|%s", in.AssetID, in.Language)
}

func (c *benchTranscriptCache) Lookup(_ context.Context, in TranscriptInput) (*TranscriptResult, bool, error) {
	c.mu.Lock()
	c.lookupCalls++
	found, ok := c.byKey[benchTranscriptKey(in)]
	if ok {
		c.lookupHits++
	}
	c.mu.Unlock()
	if !ok {
		return nil, false, nil
	}
	clone := *found
	clone.Reused = true
	return &clone, true, nil
}

func (c *benchTranscriptCache) Generate(_ context.Context, in TranscriptInput, _ *MaterializedAsset) (*TranscriptResult, error) {
	c.mu.Lock()
	c.genCalls++
	c.mu.Unlock()
	if err := benchSleep(context.Background(), c.asrMS); err != nil {
		return nil, err
	}
	result := &TranscriptResult{
		AssetID:  in.AssetID,
		Language: in.Language,
		Text:     "generated once for this source",
		Cues:     []Cue{{StartMs: 0, EndMs: 1000, Text: "generated"}},
	}
	if in.Persist {
		c.mu.Lock()
		c.byKey[benchTranscriptKey(in)] = result
		c.mu.Unlock()
	}
	return result, nil
}

func (c *benchTranscriptCache) counts() (gen, lookups, hits int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.genCalls, c.lookupCalls, c.lookupHits
}

// seed installs an already-READY canonical track, which is what the clip
// producers are required to supply before a render can run in `reuse` mode.
func (c *benchTranscriptCache) seed(in TranscriptInput) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.byKey[benchTranscriptKey(in)] = &TranscriptResult{
		AssetID: in.AssetID, Language: in.Language,
		Text: "ready canonical track",
		Cues: []Cue{{StartMs: 0, EndMs: 1000, Text: "ready"}},
	}
}

// runTranscriptBatch prepares the same source `clips` times under one
// transcript policy and returns the wall time plus the ASR resolution state.

// runSeededReuseBatch prepares the same source `clips` times under the
// production default (`reuse`) against a pre-existing READY canonical track,
// exactly like a certified producer supplies it.
func runSeededReuseBatch(t *testing.T, clips int, asrMS time.Duration) (time.Duration, int, int, int) {
	t.Helper()
	cache := newBenchTranscriptCache(asrMS)
	cache.seed(TranscriptInput{AssetID: "asset-source", Language: "en"})

	assets := newFakeAssetResolver(map[string]AssetRef{"asset-source": {AssetID: "asset-source"}})
	preparer, err := NewPreparer(assets, &fakeMaterializer{}, cache, NewContractResolver(), zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}

	started := time.Now()
	for i := 0; i < clips; i++ {
		req := baseRenderRequest()
		if _, err := preparer.Prepare(context.Background(), req, fmt.Sprintf("run-reuse-%d", i)); err != nil {
			t.Fatalf("reuse clip %d: Prepare: %v", i, err)
		}
	}
	elapsed := time.Since(started)
	gen, lookups, hits := cache.counts()
	return elapsed, gen, lookups, hits
}

// TestScenario6_ReuseServesWholeBatchFromCanonicalTrack proves the ASR call
// count of the production default: with a READY canonical track, a batch of
// clips over one source runs ZERO ASR passes and every clip is served from the
// canonical text track.
func TestScenario6_ReuseServesWholeBatchFromCanonicalTrack(t *testing.T) {
	const (
		clips = 10
		asrMS = 20 * time.Millisecond
	)

	reuseWall, reuseCalls, reuseLookups, reuseHits := runSeededReuseBatch(t, clips, asrMS)

	reuseReport := benchReport{
		Scenario:        "scenario-06-transcript-reuse",
		Mode:            "transcript",
		Clips:           clips,
		WallMS:          reuseWall.Milliseconds(),
		ClipsPerMin:     benchClipsPerMin(clips, reuseWall),
		SourceDownloads: int64(reuseCalls),
	}
	writeBenchReport(t, reuseReport)

	if reuseCalls != 0 {
		t.Errorf("mode=reuse with a READY track: ASR calls = %d, want 0 (the render must not run ASR)", reuseCalls)
	}
	if reuseLookups != clips {
		t.Errorf("mode=reuse: lookups = %d, want %d", reuseLookups, clips)
	}
	if reuseHits != clips {
		t.Errorf("mode=reuse: cache hits = %d, want %d", reuseHits, clips)
	}
	t.Logf("transcript reuse (%d clips, %v ASR each): %d ASR call(s) in %v (%d lookup(s), %d hit(s))—the render path never transcribes",
		clips, asrMS, reuseCalls, reuseWall, reuseLookups, reuseHits)
}

// TestScenario6_ReuseMissingTrackFailsClosed pins the fail-closed half: a
// render that asks for `reuse` against a source with no READY track must fail
// with a typed error and NEVER silently run ASR inside the render.
func TestScenario6_ReuseMissingTrackFailsClosed(t *testing.T) {
	cache := newBenchTranscriptCache(10 * time.Millisecond)
	assets := newFakeAssetResolver(map[string]AssetRef{"asset-source": {AssetID: "asset-source"}})
	preparer, err := NewPreparer(assets, &fakeMaterializer{}, cache, NewContractResolver(), zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	req := baseRenderRequest() // mode=reuse default
	if req.Transcript.Mode != TranscriptModeReuse {
		t.Fatalf("default transcript mode = %q, want reuse", req.Transcript.Mode)
	}
	if _, err := preparer.Prepare(context.Background(), req, "run-miss"); !errors.Is(err, ErrTranscriptUnavailable) {
		t.Fatalf("missing READY track must fail closed with ErrTranscriptUnavailable, got %v", err)
	}
	if gen, _, _ := cache.counts(); gen != 0 {
		t.Fatalf("mode=reuse must never generate, got %d ASR call(s)", gen)
	}
}

// TestTranscriptDefault_ReuseIsAppliedWithoutAnExplicitMode pins the default
// the endpoint actually applies: a payload that declares NO transcript policy
// (exactly what POST /api/clips/render carries when the caller does not care)
// normalizes to `reuse`, and a render against a source with no READY track
// therefore fails closed instead of silently transcribing.
func TestTranscriptDefault_ReuseIsAppliedWithoutAnExplicitMode(t *testing.T) {
	// The payload is fetched straight from the wire: a request WITHOUT the mode
	// key is what the endpoint decodes and what the job broker stores.
	raw, err := json.Marshal(map[string]any{"source_asset_id": "asset-source"})
	if err != nil {
		t.Fatal(err)
	}
	var req RenderRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	req.Normalize()
	if req.Transcript.Mode != TranscriptModeReuse {
		t.Fatalf("default transcript mode = %q, want reuse", req.Transcript.Mode)
	}
	if err := req.Validate(); err != nil {
		t.Fatalf("defaulted request must validate: %v", err)
	}

	cache := newBenchTranscriptCache(15 * time.Millisecond)
	assets := newFakeAssetResolver(map[string]AssetRef{"asset-source": {AssetID: "asset-source"}})
	preparer, err := NewPreparer(assets, &fakeMaterializer{}, cache, NewContractResolver(), zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := preparer.Prepare(context.Background(), &req, "run-default"); !errors.Is(err, ErrTranscriptUnavailable) {
		t.Fatalf("default policy must fail closed on a missing READY track, got %v", err)
	}
	if gen, _, _ := cache.counts(); gen != 0 {
		t.Fatalf("default policy must never run ASR inside a render, got %d call(s)", gen)
	}
}

// TestScenario6_PreparerWiringIsUsed guards the harness itself: the ASR
// resolver must be the one the Preparer actually calls (a silent fallback to a
// different transcript source would make scenario 6 measure nothing).
func TestScenario6_PreparerWiringIsUsed(t *testing.T) {
	cache := newBenchTranscriptCache(0)
	assets := newFakeAssetResolver(map[string]AssetRef{"asset-source": {AssetID: "asset-source"}})
	preparer, err := NewPreparer(assets, &fakeMaterializer{}, cache, NewContractResolver(), zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	req := baseRenderRequest()
	req.Transcript = &TranscriptSpec{Mode: TranscriptModeGenerate, Language: "en", Persist: true}
	if _, err := preparer.Prepare(context.Background(), req, "run-wiring"); err != nil {
		t.Fatal(err)
	}
	if gen, _, _ := cache.counts(); gen != 1 {
		t.Fatalf("generate calls = %d, want 1 (the Preparer must call the injected resolver)", gen)
	}
}
