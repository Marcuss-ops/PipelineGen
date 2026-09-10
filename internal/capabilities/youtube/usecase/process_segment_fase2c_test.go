// Package usecase — process_segment_fase2c_test.go: audit &
// hardening hermetic test suite for PR-PY-CLIPS-CORRETTE-TRADOTTE
// Fase 2.c (July 2026), retargeted Sept 2026 onto the canonical
// enrichment seam.
//
// SCOPE:
//
//	Fase 1.c proved the negative side: the metadata enrichment step
//	NEVER invokes a Transcriber (0 calls when the transcript is
//	already READY; exactly 1 when Whisper is the only source). The
//	structural fix is that enrichment consumes a resolved
//	*detail.ResolvedTextBundle and has no Whisper path at all.
//
//	Fase 2.c adds POSITIVE DATA-FLOW validation: the resolved
//	bundle's PlainText MUST flow verbatim into
//	ClipMetadataInput.Transcript (and cmd.Segment fields into the
//	rest of the analyzer input). A future regression that swaps the
//	source (e.g. silently drops the bundle.PlainText and re-invokes a
//	hardcoded fallback path) would surface as a builder.input
//	mismatch even though the transcriber counter stays flat.
//
//	  - The synchronous path (analyzeClipForCommit) and the
//	    asynchronous path (the serialized metadata.enrich.requested
//	    payload) both build their input through the SINGLE canonical
//	    constructor buildClipMetadataInput, so the two enrichment
//	    modes cannot drift (godlike/06 SSOT).
//
// godlike/07 NO-FAKE-AVAILABILITY: the audit wires a real
// *ytmetadata.MetadataService backed by a recording Builder stub
// + a noop Writer stub. The Builder stub captures the
// ClipMetadataInput the use case passed; the Writer stub satisfies
// NewMetadataService's required-arg contract and is present-but-
// passive here.
//
// The test reuses the canonical stub surface from sibling test
// files (validProcessSegmentDeps from process_segment_failfast_test.go;
// countingTranscriber + noRowsRepo + noSubtitleFetcher from
// process_segment_fase1c_test.go) so it does NOT redefine stub
// boilerplate. The TextTrackResolver struct is constructed via
// struct literal (no ctor in this codebase).
package usecase

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/dto"
	ytmetadata "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/metadata"
	youtubeports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/ports"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
)

// ── Stubs (Fase 2.c-only; sibling stubs reused from fase1c) ────────

// recordingBuilder satisfies ytmetadata.ClipMetadataBuilder and
// captures every ClipMetadataInput pass. Build returns a canned
// non-zero CanonicalClipMetadata so the writer side is happy
// and Build never errors.
type recordingBuilder struct {
	calls int32
	last  youtubetypes.ClipMetadataInput
}

func (r *recordingBuilder) Build(_ context.Context, in youtubetypes.ClipMetadataInput) (youtubetypes.CanonicalClipMetadata, error) {
	atomic.AddInt32(&r.calls, 1)
	r.last = in
	// Non-zero canned metadata: SourceVersion non-empty so
	// the writer isn't fed an empty fingerprint (which would
	// fail-closed downstream).
	return youtubetypes.CanonicalClipMetadata{
		ClipID:         in.ClipID,
		Title:          in.Title,
		SourceVersion:  "v1",
		QualityScore:   0.5,
		SponsorSegment: false,
	}, nil
}

// Compile-time assert: recordingBuilder satisfies the
// canonical application-layer builder port.
var _ ytmetadata.ClipMetadataBuilder = (*recordingBuilder)(nil)

// noopFase2cMetadataWriter satisfies youtubeports.ClipMetadataWriter.
// Distinct from noopMetadataWriter in
// process_segment_metadata_threading_test.go so the two test files
// don't redeclare (same package). Required by NewMetadataService
// (P1 #15 fail-closed); the Builder stub already captures the
// data flow, so the Writer is present-but-passive here.
type noopFase2cMetadataWriter struct{}

func (noopFase2cMetadataWriter) UpdateClipMetadataAndRequestIndex(_ context.Context, _ string, _ youtubetypes.CanonicalClipMetadata) error {
	return nil
}
func (noopFase2cMetadataWriter) UpdateClipMetadataTextsAndRequestIndex(_ context.Context, _ string, _ youtubetypes.CanonicalClipMetadata, _ []detail.TextTrack) error {
	return nil
}

// Compile-time assert: noopFase2cMetadataWriter satisfies the
// canonical ClipMetadataWriter port.
var _ youtubeports.ClipMetadataWriter = noopFase2cMetadataWriter{}

// newRecordingMetadataService constructs a real
// *ytmetadata.MetadataService with the recording Builder + a
// noop Writer. Returns (service, builder) so each test can read
// the captured input via `builder.last` after the use case call.
func newRecordingMetadataService(t *testing.T) (*ytmetadata.MetadataService, *recordingBuilder) {
	t.Helper()
	b := &recordingBuilder{}
	svc, err := ytmetadata.NewMetadataService(ytmetadata.MetadataDeps{
		Builder:  b,
		Writer:   noopFase2cMetadataWriter{},
		Logger:   zap.NewNop(),
		JobID:    "test-job-fase2c",
		JobGroup: "general",
	})
	require.NoError(t, err, "NewMetadataService must succeed with the recording Builder + noop Writer")
	require.NotNil(t, svc)
	return svc, b
}

// ── Test 1: data-flow (bundle.PlainText → ClipMetadataInput.Transcript) ──

// TestAnalyzeClipForCommit_BundlePlainText_ReachesEnrichClip is the
// canonical Fase 2.c audit.
//
// The thread ResolvedTextBundle.PlainText → buildClipMetadataInput →
// ClipMetadataInput.Transcript → EnrichClip must be byte-identical.
// A regression that swaps the source (e.g. drops the bundle and
// sources a Whisper fallback) would surface as
// builder.last.Transcript != expected.
func TestAnalyzeClipForCommit_BundlePlainText_ReachesEnrichClip(t *testing.T) {
	const expectedTranscript = "Fase 2.c hardening: explicit bundle-to-metadata data-flow"

	metaSvc, builder := newRecordingMetadataService(t)
	core, media, metadata, observability := validProcessSegmentDeps()
	metadata.MetadataService = metaSvc
	uc := NewProcessYouTubeSegmentFromSubBundles(core, media, metadata, observability)

	cmd := youtubetypes.ProcessSegmentCommand{
		VideoID: "yt_fase2c_dataflow",
		Segment: youtubetypes.Segment{
			Name:   "Fase2cDataFlow",
			Start:  "0:00",
			End:    "0:10",
			Topics: []string{"alpha", "beta"},
		},
	}
	bundle := &detail.ResolvedTextBundle{
		LanguageCode: "en",
		PlainText:    expectedTranscript,
	}

	_, err := uc.analyzeClipForCommit(context.Background(), cmd, "yt_fase2c_dataflow_0_10_v1", 0, 10, bundle)
	require.NoError(t, err, "analyzeClipForCommit with a recording MetadataService must succeed")

	// Load-bearing Fase 2.c assertions:
	require.Equal(t, int32(1), atomic.LoadInt32(&builder.calls),
		"Builder.Build call count = want 1 (analyzeClipForCommit must run the analyzer exactly once)")
	require.Equal(t, expectedTranscript, builder.last.Transcript,
		"Fase 2.c data-flow: ClipMetadataInput.Transcript MUST equal the resolved bundle PlainText byte-for-byte. The bundle thread is regression-proof at this seam.")
	require.Equal(t, cmd.Segment.Name, builder.last.Title,
		"Fase 2.c data-flow: ClipMetadataInput.Title MUST equal cmd.Segment.Name (no silent rename)")
	require.Equal(t, []string{"alpha", "beta"}, builder.last.Topics,
		"Fase 2.c data-flow: ClipMetadataInput.Topics MUST equal cmd.Segment.Topics (a regression that silently drops fields would surface here)")
	require.Equal(t, 10, builder.last.ClipDuration,
		"Fase 2.c data-flow: ClipDuration MUST be endSec-startSec from the same call")
}

// ── Test 2: empty-bundle (fail-closed grace) ──────────────────────────

// TestAnalyzeClipForCommit_EmptyBundle_ReachesEnrichClip is the
// empty-bundle companion.
//
// When the 5-priority chain fails (no DB track, no YouTube subs, no
// Whisper output) the orchestrator passes a nil/empty bundle.
// Enrichment MUST NOT silently source a Whisper fallback here — the
// empty transcript MUST thread through verbatim and the downstream
// metadata service handles empty transcripts gracefully (low
// QualityScore, no crash) per godlike/07 fail-closed.
func TestAnalyzeClipForCommit_EmptyBundle_ReachesEnrichClip(t *testing.T) {
	metaSvc, builder := newRecordingMetadataService(t)
	core, media, metadata, observability := validProcessSegmentDeps()
	metadata.MetadataService = metaSvc
	uc := NewProcessYouTubeSegmentFromSubBundles(core, media, metadata, observability)

	cmd := youtubetypes.ProcessSegmentCommand{
		VideoID: "yt_fase2c_empty",
		Segment: youtubetypes.Segment{Name: "Fase2cEmpty", Start: "0:00", End: "0:10"},
	}

	_, err := uc.analyzeClipForCommit(context.Background(), cmd, "yt_fase2c_empty_0_10_v1", 0, 10, nil)
	require.NoError(t, err, "analyzeClipForCommit with an empty bundle must succeed (metadata service degrades gracefully per godlike/07)")

	require.Equal(t, int32(1), atomic.LoadInt32(&builder.calls),
		"Builder.Build call count = want 1 (analyzeClipForCommit must run the analyzer once even with an empty bundle)")
	require.Equal(t, "", builder.last.Transcript,
		"Fase 2.c empty-bundle audit: ClipMetadataInput.Transcript MUST be empty, NOT a Whisper fallback. Re-introducing a direct Whisper call here would silently resurrect the double-Whisper regression.")
}

// ── Test 3: zero Transcriber calls on direct analysis ──

// TestAnalyzeClipForCommit_ZeroTranscriberCalls_WithResolverAndMetadataWired
// pins the counter-level guarantee at the analysis boundary, even when
// EVERYTHING is wired. analyzeClipForCommit consumes a resolved bundle;
// it MUST NOT reach back into the resolver or the Transcriber port.
func TestAnalyzeClipForCommit_ZeroTranscriberCalls_WithResolverAndMetadataWired(t *testing.T) {
	tport := &countingTranscriber{text: "MUST NOT be called from analysis"}
	metaSvc, builder := newRecordingMetadataService(t)

	core, media, metadata, observability := validProcessSegmentDeps()
	metadata.MetadataService = metaSvc
	// Wire the canonical 5-priority chain so the use case's
	// resolver port is non-nil + WOULD fire Whisper if analysis
	// ever reached back. The noRowsRepo + noSubtitleFetcher
	// force the chain to fall through to priority 5, so ANY
	// leak from analysis to the resolver would surface as a
	// countingTranscriber.call > 0.
	media.TextTrackResolver = &TextTrackResolver{
		Repo:        noRowsRepo{},
		Subtitles:   noSubtitleFetcher{},
		Transcriber: tport,
		Log:         zap.NewNop(),
	}
	uc := NewProcessYouTubeSegmentFromSubBundles(core, media, metadata, observability)

	cmd := youtubetypes.ProcessSegmentCommand{
		VideoID: "yt_fase2c_zerocounts",
		Segment: youtubetypes.Segment{Name: "Fase2cZero", Start: "0:00", End: "0:10"},
	}
	bundle := &detail.ResolvedTextBundle{
		LanguageCode: "en",
		PlainText:    "transcript parameter (analysis only sees this)",
	}

	_, err := uc.analyzeClipForCommit(context.Background(), cmd, "yt_fase2c_zerocounts_0_10_v1", 0, 10, bundle)
	require.NoError(t, err)

	// Load-bearing Fase 2.c assertions:
	require.Equal(t, int32(0), atomic.LoadInt32(&tport.calls),
		"Fase 2.c contract: analyzeClipForCommit must produce 0 Transcriber invocations even when TextTrackResolver + a live Transcriber are wired. Got %d calls — analysis is reaching into the resolver/Whisper.", atomic.LoadInt32(&tport.calls))
	require.Equal(t, int32(1), atomic.LoadInt32(&builder.calls),
		"Builder.Build call count = want 1 (positive sanity check: with MetadataService wired, the analyzer must run exactly once)")
	require.Equal(t, "transcript parameter (analysis only sees this)", builder.last.Transcript,
		"Fase 2.c data-flow: even with the resolver wired, the resolved bundle (not a Whisper fallback) reaches the builder")
}

// ── Test 4: nil MetadataService is a no-op ────────────────────────────

// TestAnalyzeClipForCommit_NilMetadataService_IsNoOp pins the optional-
// service contract: without a wired analyzer the enrichment returns a
// zero value and never panics (the caller commits the raw segment
// metadata verbatim).
func TestAnalyzeClipForCommit_NilMetadataService_IsNoOp(t *testing.T) {
	core, media, metadata, observability := validProcessSegmentDeps()
	metadata.MetadataService = nil
	uc := NewProcessYouTubeSegmentFromSubBundles(core, media, metadata, observability)

	cmd := youtubetypes.ProcessSegmentCommand{
		VideoID: "yt_fase2c_nil",
		Segment: youtubetypes.Segment{Name: "Fase2cNil", Start: "0:00", End: "0:10"},
	}
	enrichment, err := uc.analyzeClipForCommit(context.Background(), cmd, "yt_fase2c_nil_0_10_v1", 0, 10, nil)
	require.NoError(t, err, "analyzeClipForCommit with a nil MetadataService must be a no-op")
	require.Equal(t, ytmetadata.CanonicalClipEnrichment{}, enrichment,
		"a nil analyzer must yield the zero enrichment (caller commits raw metadata)")
}
