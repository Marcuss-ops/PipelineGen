package cliprender

// bench_transcripts_test.go owns scenario 6 of the canonical clip.render
// benchmark: ASR/transcript reuse.
//
// The transcript policy is the single biggest per-clip lever that is NOT a
// renderer change: transcript.mode=generate re-runs speech recognition for
// EVERY clip even when all clips come from one source, while
// reuse_or_generate + persist runs it ONCE per source and serves the rest from
// the canonical text track. This is measured here with a counting ASR
// resolver that persists like the real one, driving the real Preparer.

import (
	"context"
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

// runTranscriptBatch prepares the same source `clips` times under one
// transcript policy and returns the wall time plus the ASR resolution state.
func runTranscriptBatch(t *testing.T, mode string, persist bool, clips int, asrMS time.Duration) (time.Duration, int, int, int) {
	t.Helper()
	cache := newBenchTranscriptCache(asrMS)

	assets := newFakeAssetResolver(map[string]AssetRef{"asset-source": {AssetID: "asset-source"}})
	preparer, err := NewPreparer(assets, &fakeMaterializer{}, cache, NewContractResolver(), zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}

	started := time.Now()
	for i := 0; i < clips; i++ {
		req := baseRenderRequest()
		req.Transcript = &TranscriptSpec{Mode: mode, Language: "en", Persist: persist}
		prepared, err := preparer.Prepare(context.Background(), req, fmt.Sprintf("run-%d", i))
		if err != nil {
			t.Fatalf("mode=%s clip %d: Prepare: %v", mode, i, err)
		}
		if prepared.Transcript == nil || !prepared.Transcript.HasText() {
			t.Fatalf("mode=%s clip %d: prepared transcript missing text", mode, i)
		}
	}
	elapsed := time.Since(started)
	gen, lookups, hits := cache.counts()
	return elapsed, gen, lookups, hits
}

// TestScenario6_TranscriptReuse proves the ASR call count and the wall-time
// saving: generate = one ASR pass per clip; reuse_or_generate + persist = one
// ASR pass per SOURCE.
func TestScenario6_TranscriptReuse(t *testing.T) {
	const (
		clips = 10
		asrMS = 20 * time.Millisecond
	)

	generateWall, generateCalls, _, _ := runTranscriptBatch(t, TranscriptModeGenerate, true, clips, asrMS)
	reuseWall, reuseCalls, reuseLookups, reuseHits := runTranscriptBatch(t, TranscriptModeReuseOrGenerate, true, clips, asrMS)

	generateReport := benchReport{
		Scenario:    "scenario-06-transcript-generate",
		Mode:        "transcript",
		Clips:       clips,
		WallMS:      generateWall.Milliseconds(),
		ClipsPerMin: benchClipsPerMin(clips, generateWall),
	}
	// The ASR invocation count is the metric that matters; it rides in the
	// download counter slot of the shared schema so a single report shape
	// still carries it.
	generateReport.SourceDownloads = int64(generateCalls)
	writeBenchReport(t, generateReport)

	reuseReport := benchReport{
		Scenario:        "scenario-06-transcript-reuse-or-generate",
		Mode:            "transcript",
		Clips:           clips,
		WallMS:          reuseWall.Milliseconds(),
		ClipsPerMin:     benchClipsPerMin(clips, reuseWall),
		SourceDownloads: int64(reuseCalls),
	}
	writeBenchReport(t, reuseReport)

	if generateCalls != clips {
		t.Errorf("mode=generate: ASR calls = %d, want %d (one per clip)", generateCalls, clips)
	}
	if reuseCalls != 1 {
		t.Errorf("mode=reuse_or_generate + persist: ASR calls = %d, want 1 (one per source)", reuseCalls)
	}
	if reuseLookups != clips {
		t.Errorf("mode=reuse_or_generate: lookups = %d, want %d", reuseLookups, clips)
	}
	if reuseHits != clips-1 {
		t.Errorf("mode=reuse_or_generate: cache hits = %d, want %d", reuseHits, clips-1)
	}
	if reuseWall >= generateWall {
		t.Errorf("reuse wall %v is not faster than generate wall %v", reuseWall, generateWall)
	}
	t.Logf("transcript reuse (%d clips, %v ASR each): generate = %d ASR call(s) in %v | reuse_or_generate+persist = %d ASR call(s) in %v (%d cache hit(s), saved %v)",
		clips, asrMS, generateCalls, generateWall, reuseCalls, reuseWall, reuseHits, generateWall-reuseWall)
}

// TestScenario6_ReuseWithoutPersistDoesNotCache pins the trap the spec warns
// about: reuse_or_generate WITHOUT persist cannot reuse anything, because the
// generated track is never written back — so every clip pays ASR anyway. A
// caller that forgets persist gets the expensive mode while believing it asked
// for the cheap one.
func TestScenario6_ReuseWithoutPersistDoesNotCache(t *testing.T) {
	const (
		clips = 4
		asrMS = 10 * time.Millisecond
	)
	_, calls, _, hits := runTranscriptBatch(t, TranscriptModeReuseOrGenerate, false, clips, asrMS)
	if calls != clips {
		t.Errorf("reuse_or_generate without persist: ASR calls = %d, want %d (nothing was persisted to reuse)", calls, clips)
	}
	if hits != 0 {
		t.Errorf("reuse_or_generate without persist: cache hits = %d, want 0", hits)
	}
	t.Logf("reuse_or_generate without persist: %d ASR call(s) for %d clips — persist=true is what makes reuse real", calls, clips)
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
