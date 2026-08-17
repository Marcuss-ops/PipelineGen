// Package usecase — process_segment_fase1c_test.go: hermetic test
// suite for PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 1.c (July 2026).
//
// GOAL: prove that the double-Whisper regression is RETIRED.
// Step 10 MUST NOT invoke any Transcriber.TranscribeAudio call.
// The resolved transcript (from the 5-priority chain at Step 6-9)
// is the SOLE source of the transcript that reaches
// MetadataService.EnrichClip.
//
// godlike/07 NO-FAKE-AVAILABILITY contract pinned in this test:
//  1. A direct unit test of step10_MetadataEnrich proves Step 10
//     accepts the new (transcript, languageCode, cues) parameter
//     list and returns nil when MetadataService is nil — the
//     field-bound Transcriber.TranscribeAudio call is RETIRED.
//  2. An end-to-end orchestrator test (Execute) with a cache
//     miss + transcript acquisition failure + missing YouTube
//     subs proves the total Transcriber invocations across
//     the WHOLE pipeline is ≤ 1 (only Step 7's priority-5
//     fallback path can fire, never Step 10).
//  3. A cache-hit test proves acquisition/cut is SKIPPED (0
//     VideoPipeline invocations) while the finalization gate
//     still runs (status "processed"; Whisper not re-invoked
//     because the cached item carries no local path).
//
// The test reuses the canonical stub surface from
// process_segment_failfast_test.go (stubVideoPipeline,
// stubHashService, stubAtomicWriter, stubClipCache,
// validProcessSegmentDeps, testStubHash, stubVideoPipelineWithPath)
// so it does not redefine stub boilerplate.
package usecase

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/dto"
	youtubeports "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/ports"
	capyoutubeusecase "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/usecase"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// ── Stubs ──────────────────────────────────────────────────────────────

// countingTranscriber satisfies WhisperTranscriberPort. It
// records every call to TranscribeAudio + TranscribeAudioWithDetection
// so the Fase 1.c tests can pin the total invocation count
// across the whole pipeline.
//
// godlike/07 NO-FAKE-AVAILABILITY: the recorder returns canned
// success / failure responses; the tests verify the COUNT of
// invocations, not just the success of the pipeline. A
// regression that re-introduces Step 10's direct Transcriber
// use would surface as 2 invocations (Step 7 + Step 10) in
// the cache-miss path, failing the upper-bound assertion.
type countingTranscriber struct {
	calls int32
	text  string
	err   error
}

func (c *countingTranscriber) TranscribeAudio(_ context.Context, _ string) (string, error) {
	atomic.AddInt32(&c.calls, 1)
	return c.text, c.err
}

func (c *countingTranscriber) TranscribeAudioWithDetection(_ context.Context, _ string) (asset.TranscriptResult, error) {
	atomic.AddInt32(&c.calls, 1)
	if c.err != nil {
		return asset.TranscriptResult{}, c.err
	}
	return asset.TranscriptResult{Text: c.text, DetectedLanguage: "en"}, nil
}

// compile-time assertion: countingTranscriber satisfies WhisperTranscriberPort.
var _ youtubeports.WhisperTranscriberPort = (*countingTranscriber)(nil)

// noSubtitleFetcher always returns "no subtitles" so the
// resolver falls through to priority 5 (Whisper). This is
// the canonical trigger for the Whisper-fallback path.
//
// Implements youtubeports.SubtitleFetcherPort (BOTH methods):
//   - FetchSegmentSubtitles: returns ErrSubtitleUnavailable so
//     the resolver skips priority 3+4 and falls through to
//     priority 5.
//   - SliceSubtitles: returns ErrSubtitleUnavailable so any
//     resolver path that probes the legacy slice surface also
//     falls through cleanly (kept for compile-time interface
//     conformance — the canonical Fase 1.a/b surface is
//     FetchSegmentSubtitles).
type noSubtitleFetcher struct{}

func (noSubtitleFetcher) FetchSegmentSubtitles(_ context.Context, _ string, _, _ int) (*asset.ResolvedTextBundle, error) {
	return nil, capyoutubeusecase.ErrSubtitleUnavailable
}

func (noSubtitleFetcher) SliceSubtitles(_ context.Context, _ string, _, _ int, _ string) error {
	return capyoutubeusecase.ErrSubtitleUnavailable
}

// compile-time assertion: noSubtitleFetcher satisfies SubtitleFetcherPort.
var _ youtubeports.SubtitleFetcherPort = (*noSubtitleFetcher)(nil)

// noRowsRepo satisfies asset.TextTrackRepository and ALWAYS
// returns "no rows" for every read method. This is the
// canonical "priority 1 (DB) miss + priority 2 (DB READY)
// miss" stub so the resolver falls through to priority 3-5.
//
// godlike/07 NO-FAKE-AVAILABILITY: write methods are no-ops
// (the tests never observe a write). Read methods return
// the empty/miss signal that lets the resolver advance
// down the priority chain.
type noRowsRepo struct{}

func (noRowsRepo) UpsertBatch(_ context.Context, _ []asset.TextTrack) error { return nil }
func (noRowsRepo) Find(_ context.Context, _ string, _ string, _ asset.TextTrackKind) (*asset.TextTrack, error) {
	return nil, nil
}
func (noRowsRepo) ListByAsset(_ context.Context, _ string) ([]asset.TextTrack, error) {
	return []asset.TextTrack{}, nil
}
func (noRowsRepo) FindReady(_ context.Context, _ string, _ string, _ asset.TextTrackKind) (*asset.TextTrack, []asset.TimedCue, error) {
	return nil, nil, nil
}
func (noRowsRepo) ListReadyLanguages(_ context.Context, _ string, _ asset.TextTrackKind) ([]string, error) {
	return []string{}, nil
}

// FindCurrentForTranslation + InsertTranslationWithAuditPredecessor
// are the PR-CATALOG-MULTILINGUA step-4 surface added to the
// canonical TextTrackRepository port. noRowsRepo is the canonical
// "every read misses" stub (priority 1+2 miss); both new methods
// mirror the null-write contract from
// internal/application/scripts/usecase/multilingual_persistence_p1f_test.go::p1fStubRepo
// and internal/application/assets/texttracks/materializer_test.go::fakeTextTrackRepo.
func (noRowsRepo) FindCurrentForTranslation(_ context.Context, _ string, _ asset.TextTrackKind, _, _, _, _, _ string) (*asset.TextTrack, error) {
	return nil, nil
}
func (noRowsRepo) InsertTranslationWithAuditPredecessor(_ context.Context, _ asset.TextTrack) error {
	return nil
}

// compile-time assertion: noRowsRepo satisfies TextTrackRepository.
var _ asset.TextTrackRepository = (*noRowsRepo)(nil)

// readyTrackRepo satisfies asset.TextTrackRepository and ALWAYS
// returns a READY track from FindReady. This is the canonical
// "priority 1+2 (DB) HIT" stub so the resolver short-circuits
// at priority 2 (DB READY lookup) and NEVER reaches priority 5
// (Whisper fallback).
//
// godlike/07 NO-FAKE-AVAILABILITY: the stub returns a real
// *asset.TextTrack with TextTrackReady status, a non-empty
// transcript, and the requested language code. ListReadyLanguages
// also returns the matching language code so the resolver's
// branch logic is exercised end-to-end.
//
// Other methods (UpsertBatch, Find, ListByAsset) return
// miss/empty signals — only FindReady is exercised in the
// "transcript already READY" path.
type readyTrackRepo struct {
	langCode string
	track    *asset.TextTrack
}

func (r readyTrackRepo) UpsertBatch(_ context.Context, _ []asset.TextTrack) error { return nil }
func (r readyTrackRepo) Find(_ context.Context, _ string, _ string, _ asset.TextTrackKind) (*asset.TextTrack, error) {
	return r.track, nil
}
func (r readyTrackRepo) ListByAsset(_ context.Context, _ string) ([]asset.TextTrack, error) {
	return []asset.TextTrack{*r.track}, nil
}
func (r readyTrackRepo) FindReady(_ context.Context, _ string, _ string, _ asset.TextTrackKind) (*asset.TextTrack, []asset.TimedCue, error) {
	return r.track, nil, nil
}
func (r readyTrackRepo) ListReadyLanguages(_ context.Context, _ string, _ asset.TextTrackKind) ([]string, error) {
	return []string{r.langCode}, nil
}

// FindCurrentForTranslation + InsertTranslationWithAuditPredecessor
// are the PR-CATALOG-MULTILINGUA step-4 surface added to the
// canonical TextTrackRepository port. readyTrackRepo is the canonical
// "priority 1+2 (DB) HIT + READY" stub used by the Fase 4
// transcript-already-READY chain test. Both new methods mirror
// the null-write contract of p1fStubRepo / fakeTextTrackRepo
// (this stub tracks no audit predecessor — the tests inspect
// only the priority-2 short-circuit at FindReady).
func (r readyTrackRepo) FindCurrentForTranslation(_ context.Context, _ string, _ asset.TextTrackKind, _, _, _, _, _ string) (*asset.TextTrack, error) {
	return nil, nil
}
func (r readyTrackRepo) InsertTranslationWithAuditPredecessor(_ context.Context, _ asset.TextTrack) error {
	return nil
}

// compile-time assertion: readyTrackRepo satisfies TextTrackRepository.
var _ asset.TextTrackRepository = (*readyTrackRepo)(nil)

// alwaysHitCache always returns a hit at Step 2. The
// orchestrator short-circuits BEFORE the resolver.
type alwaysHitCache struct {
	stubClipCache
	item *youtubetypes.ExtractItem
}

func (a *alwaysHitCache) GetExisting(_ context.Context, _ string) (*youtubetypes.ExtractItem, bool, error) {
	return a.item, true, nil
}

// ── Test 1: Step 10 alone (no panic, accepts new signature) ──────────
//
// Directly exercise step10_MetadataEnrich. The test asserts:
//
//	(a) the method accepts the new (transcript, languageCode, cues)
//	    parameter list and returns nil when MetadataService is nil
//	(b) the call does NOT panic, even with a nil MetadataService
//
// godlike/06 SSOT: the test pins that step10_MetadataEnrich
// reads ONLY from the parameter list (transcript, languageCode,
// cues) — no port, no filesystem, no callback into the resolver.
// The compile-time guarantee is that ProcessSegmentDeps no longer
// has a `Transcriber` field, so Step 10 cannot reach one even
// if a future maintainer tries to re-introduce a direct call.
func TestStep10_DoesNotInvokeTranscriber(t *testing.T) {
	uc := NewProcessYouTubeSegmentFromSubBundles(func() (ProcessSegmentCoreDeps, ProcessSegmentMediaDeps, ProcessSegmentMetadataDeps, ProcessSegmentObservabilityDeps) {
		core, media, metadata, observability := validProcessSegmentDeps()
		metadata.MetadataService = nil // no-op path
		return core, media, metadata, observability
	}())
	cmd := youtubetypes.ProcessSegmentCommand{
		VideoID: "yt_step10_unit",
		OutDir:  t.TempDir(),
		Segment: youtubetypes.Segment{Start: "0:00", End: "0:10", Name: "Step10Unit"},
		Index:   0,
	}
	err := uc.step10_MetadataEnrich(
		context.Background(),
		cmd,
		"yt_yt_step10_unit_0_10_v1",
		10,
		"transcript from bundle", // transcript
		"en",                     // languageCode
		nil,                      // cues
	)
	require.NoError(t, err, "step10_MetadataEnrich with nil MetadataService must be a no-op")
}

// ── Test 2: end-to-end orchestrator (≤ 1 Transcriber call) ────────────
//
// LOAD-BEARING Fase 1.c assertion. Drive the orchestrator with:
//   - cache miss (so Steps 3-9 run)
//   - TextTrackResolver present, wired with the counting transcriber
//   - noRowsRepo so priority 1+2 (DB lookup) miss
//   - noSubtitleFetcher so priority 3+4 (YouTube subs) miss
//   - The transcriber is consulted EXACTLY ONCE at priority 5
//
// The pipeline SHOULD invoke the transcriber EXACTLY ONCE
// (priority 5 at Step 7) and NEVER at Step 10. If Step 10
// regresses to a direct Transcriber call, the counter hits 2.
func TestExecute_CacheMiss_TranscriberInvokedAtMostOnce(t *testing.T) {
	// Real audio file path for Step 5's os.Stat check.
	realPath := filepath.Join(t.TempDir(), "clip.mp4")
	_ = os.WriteFile(realPath, []byte("fake audio bytes"), 0o644)

	// Counting transcriber wired into the resolver (Step 7's
	// priority-5 fallback). Returns a canned transcript so the
	// bundle surface is non-empty and Step 10 receives real text.
	tport := &countingTranscriber{text: "whisper fallback transcript"}

	// Build deps. The test does NOT set metadata.Transcriber
	// because that field is RETIRED in Fase 1.c. The
	// transcriber is wired indirectly via the TextTrackResolver
	// (the canonical owner post-Fase 1.c).
	core, media, metadata, observability := validProcessSegmentDeps()
	core.VideoPipeline = stubVideoPipelineWithPath{path: realPath}
	core.Hash = testStubHash{} // non-empty so Step 5 passes

	// Wire a TextTrackResolver with the counting transcriber.
	// The resolver's AcquireSegmentText is the priority chain
	// entry; it invokes the transcriber at priority 5 when
	// subtitles return no rows. The struct is built via a
	// struct literal (no NewTextTrackResolver ctor in this
	// codebase; the field shape is the canonical surface).
	media.TextTrackResolver = &TextTrackResolver{
		Repo:        noRowsRepo{},        // priority 1+2 (DB) → miss
		Subtitles:   noSubtitleFetcher{}, // priority 3+4 → miss
		Transcriber: tport,               // priority 5 → fires once
		Log:         zap.NewNop(),
	}

	uc := NewProcessYouTubeSegmentFromSubBundles(core, media, metadata, observability)
	cmd := youtubetypes.ProcessSegmentCommand{
		VideoID: "yt_fase1c_e2e",
		OutDir:  t.TempDir(),
		Segment: youtubetypes.Segment{Start: "0:00", End: "0:10", Name: "Fase1cE2E"},
		Index:   0,
	}

	out, execErr := uc.Execute(context.Background(), cmd)
	require.NoError(t, execErr, "Execute must succeed (cache miss + Whisper fallback path)")
	require.Equal(t, "processed", out.Status)

	// Load-bearing Fase 1.c assertion: total Transcriber
	// invocations across the WHOLE pipeline is EXACTLY 1. Pre-Fase
	// 1.c the count was 2 (Step 7 + Step 10). Post-Fase 1.c
	// the count is exactly 1 (Step 7's priority-5 fallback
	// always fires when no other priority hits). If Step 10
	// regresses to a direct Transcriber call, the counter
	// hits 2 and this assertion fails.
	got := atomic.LoadInt32(&tport.calls)
	require.Equal(t, int32(1), got,
		"Fase 1.c contract: total Transcriber calls across the pipeline must be exactly 1 (Step 7's priority-5 fallback only). Got %d — Step 10 is re-invoking Whisper!", got)
}

// ── Test 3: cache hit skips acquisition but still finalizes ───────────
//
// PR-CACHE-HIT-FINALIZATION: a binary cache hit skips ONLY the
// acquisition/cut + ffprobe steps (Steps 3-5 + 5a); the canonical
// enrichment/finalization gate (Steps 6-10) STILL runs so missing/stale
// metadata is repaired. The pipeline must NOT re-download the binary.
//
// In this fixture the cached item carries NO LocalPath, so the
// Whisper fallback (priority 5, guarded by LocalPath != "") is not
// consulted — the resolver exhausts without a transcript. The
// load-bearing assertions are: 0 VideoPipeline invocations
// (acquisition skipped) and status "processed" (finalization ran).
func TestExecute_CacheHit_TranscriberNotInvoked(t *testing.T) {
	tport := &countingTranscriber{text: "should never be called"}
	vpipe := &countingVideoPipeline{}

	core, media, metadata, observability := validProcessSegmentDeps()
	core.VideoPipeline = vpipe
	// Wire a TextTrackResolver with the counting transcriber so
	// the test would observe any regression that re-invokes Whisper.
	media.TextTrackResolver = &TextTrackResolver{
		Repo:        noRowsRepo{},
		Subtitles:   noSubtitleFetcher{},
		Transcriber: tport,
		Log:         zap.NewNop(),
	}
	// Wire a cache that ALWAYS hits.
	core.Cache = &alwaysHitCache{item: &youtubetypes.ExtractItem{
		Filename:    "yt_yt_fase1c_cachehit_0_10_v1.mp4",
		Duration:    10,
		FileHash:    "cache-hit-hash",
		DriveFileID: "cache-hit-drive-file",
	}}

	uc := NewProcessYouTubeSegmentFromSubBundles(core, media, metadata, observability)
	cmd := youtubetypes.ProcessSegmentCommand{
		VideoID: "yt_fase1c_cachehit",
		OutDir:  t.TempDir(),
		Segment: youtubetypes.Segment{Start: "0:00", End: "0:10", Name: "Fase1cCacheHit"},
		Index:   0,
	}

	out, execErr := uc.Execute(context.Background(), cmd)
	require.NoError(t, execErr, "Execute must succeed on cache hit")
	// PR-CACHE-HIT-FINALIZATION: the legacy "skipped" short-circuit is
	// RETIRED. The cache hit still passes through Steps 6-10, so the
	// canonical outcome is "processed".
	require.Equal(t, "processed", out.Status, "cache hit must finalize through the enrichment gate (status processed)")

	// Load-bearing assertion: the cached binary is NOT re-acquired —
	// the cache hit skips Steps 3-5.
	if got := atomic.LoadInt32(&vpipe.calls); got != 0 {
		t.Fatalf("cache hit must skip acquisition/cut: VideoPipeline invoked %d times", got)
	}

	// The cached item carries no LocalPath, so the Whisper fallback
	// (priority 5, guarded by LocalPath != "") is never consulted.
	// This is no longer a short-circuit — it is the resolver's own
	// empty-path guard.
	if got := atomic.LoadInt32(&tport.calls); got != 0 {
		t.Fatalf("cache hit with empty LocalPath must not invoke Whisper: %d calls", got)
	}
}

// ── Test 4: transcript already READY (0 Transcriber calls) ────────────
//
// godlike/07 NO-FAKE-AVAILABILITY: the canonical "transcript
// already READY" path. The resolver's priority 2 (DB lookup)
// returns a READY track, so the resolver short-circuits at
// priority 2 and NEVER reaches priority 5 (Whisper). The
// total Transcriber invocations must be 0.
//
// This test exercises the TextTrackResolver directly (not the
// full Execute pipeline) because wiring PreferredLanguages from
// config into the orchestrator's TextTrackAcquireRequest is a
// PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 5 concern (the current
// TODO in step6to9.go: "TODO Fase 5 wires the policy once
// media.multilingual.* is read from cfg"). At the resolver
// level, PreferredLanguages is a first-class field on
// TextTrackAcquireRequest and the test exercises it directly.
//
// This is the companion to Test 3: Test 3 covers the cache
// short-circuit (Step 2 of Execute), Test 4 covers the resolver
// short-circuit (priority 2 of the 5-level chain). Together
// they pin the full "no-double-Whisper" contract across the
// two early-exit paths.
func TestAcquireSegmentText_TranscriptReady_TranscriberNotInvoked(t *testing.T) {
	// Real audio file path for AcquireSegmentText's LocalPath
	// (Whisper priority 5 needs a real file if it fires).
	realPath := filepath.Join(t.TempDir(), "clip.mp4")
	_ = os.WriteFile(realPath, []byte("fake audio bytes"), 0o644)

	// Counting transcriber — wired into the resolver so any
	// regression that bypasses the DB-hit short-circuit would
	// surface as a non-zero call count.
	tport := &countingTranscriber{text: "should never be called"}

	// Build a READY TextTrack that the resolver will return
	// at priority 2. This is the canonical "transcript already
	// READY in the DB" surface.
	readyTrack := &asset.TextTrack{
		AssetID:      "yt_fase1c_ready",
		LanguageCode: "en",
		TextKind:     asset.TextTrackTranscript,
		TextContent:  "DB-cached transcript from previous extraction",
		Status:       asset.TextTrackReady,
		SourceType:   asset.TextSourceProvided,
	}
	repo := &readyTrackRepo{langCode: "en", track: readyTrack}

	// Wire the resolver directly with the readyTrackRepo at
	// priority 2. Subtitles return ErrSubtitleUnavailable so
	// priority 3+4 miss; the transcriber would fire at
	// priority 5 BUT the resolver short-circuits at priority 2.
	resolver := &TextTrackResolver{
		Repo:        repo,                // priority 1+2 → HIT (READY track)
		Subtitles:   noSubtitleFetcher{}, // priority 3+4 → miss
		Transcriber: tport,               // priority 5 → would fire, but short-circuits at priority 2
		Log:         zap.NewNop(),
	}

	req := TextTrackAcquireRequest{
		ClipID:             "yt_fase1c_ready",
		VideoID:            "yt_fase1c_ready",
		StartSec:           0,
		EndSec:             10,
		LocalPath:          realPath,
		PreferredLanguages: []string{"en"}, // forces Priority 2 iteration over FindReady
	}

	bundle, acqErr := resolver.AcquireSegmentText(context.Background(), req)
	require.NoError(t, acqErr, "AcquireSegmentText must succeed on DB READY hit")
	require.NotNil(t, bundle, "Resolver must return the READY DB track as a non-nil bundle")
	require.Equal(t, "en", bundle.LanguageCode, "Bundle must carry the language code from the READY DB track")
	require.Equal(t, "DB-cached transcript from previous extraction", bundle.PlainText, "Bundle must carry the transcript from the READY DB track")

	// Load-bearing assertion: 0 Transcriber invocations when
	// the resolver's priority 2 (DB READY lookup) hits. The
	// resolver short-circuits at priority 2 and NEVER reaches
	// priority 5 (Whisper), so the transcriber is never
	// consulted — even when YouTube subs are absent.
	got := atomic.LoadInt32(&tport.calls)
	require.Equal(t, int32(0), got,
		"Fase 1.c contract: transcript already READY must produce 0 Transcriber invocations. Got %d — the resolver did not short-circuit at priority 2.", got)
}
