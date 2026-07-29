// Package usecase — process_segment_fase2c_test.go: audit &
// hardening hermetic test suite for PR-PY-CLIPS-CORRETTE-TRADOTTE
// Fase 2.c (July 2026).
//
// SCOPE:
//
//	Fase 1.c proved the negative side: Step 10 NEVER invokes a
//	Transcriber via invocation counters (≤ 1 across the whole
//	pipeline, 0 on cache hit, 0 on transcript-already-READY). The
//	structural fix is in (process_segment_step10.go no longer
//	references u.deps.Transcriber, and ProcessSegmentDeps has no
//	Transcriber field to begin with).
//
//	Fase 2.c adds POSITIVE DATA-FLOW validation: when
//	step10_MetadataEnrich(ctx, cmd, clipID, duration, transcript,
//	languageCode, cues) is called, the `transcript` and
//	`languageCode` parameter values MUST flow verbatim into the
//	downstream ClipMetadataInput.Transcript / LanguageCode. A
//	future regression that swaps the parameter source (e.g.
//	silently drops the bundle.PlainText and re-invokes a
//	hardcoded fallback path) would surface as a builder.input
//	mismatch even though the counter stays flat.
//
// godlike/07 NO-FAKE-AVAILABILITY: the audit wires a real
// *ytmetadata.MetadataService backed by a recording Builder stub
// + a noop Writer stub. We do NOT mock the use case's
// MetadataService field (it is concrete *MetadataService, not an
// interface — concretely stubbing it would require a refactor
// beyond the structural-removal scope). The Builder stub
// captures the ClipMetadataInput the use case passed; the Writer
// stub satisfies NewMetadataService's required-arg contract and
// is present-but-passive here.
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

	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/dto"
	ytmetadata "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/metadata"
	youtubeports "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/ports"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// ── Stubs (Fase 2.c-only; sibling stubs reused from fase1c) ────────

// recordingBuilder satisfies ytmetadata.ClipMetadataBuilder and
// captures every ClipMetadataInput pass. Build returns a canned
// non-zero CanonicalClipMetadata so the writer side is happy
// and Build never errors (this is the "happy Step 10" probe —
// the failure path is already covered by
// process_segment_step10_metrics_test.go).
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
// Distinct from the sibling noopMetadataWriter in
// process_segment_step10_metrics_test.go so the two test files
// don't redeclare (same package). Required by NewMetadataService
// (P1 #15 fail-closed); the Builder stub already captures the
// data flow, so the Writer is present-but-passive here.
type noopFase2cMetadataWriter struct{}

func (noopFase2cMetadataWriter) UpdateClipMetadataAndRequestIndex(_ context.Context, _ string, _ youtubetypes.CanonicalClipMetadata) error {
	return nil
}
func (noopFase2cMetadataWriter) UpdateClipMetadataTextsAndRequestIndex(_ context.Context, _ string, _ youtubetypes.CanonicalClipMetadata, _ []asset.TextTrack) error {
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

// ── Test 1: data-flow (bundle.PlainText → ClipMetadataInput.Transcript) ──// TestStep10_Fase2c_BundlePlainText_ReachesEnrichClip is the
// canonical Fase 2.c audit.
//
// The thread from Step 6-9's ResolvedTextBundle.PlainText ->
// orchestrator.step10Transcript -> step10_MetadataEnrich's
// `transcript` parameter -> EnrichClip(ctx,
// ClipMetadataInput{Transcript: ...}) MUST be byte-identical.
// A regression that swaps the source (e.g. drops the parameter
// and sources a Whisper fallback) would surface as
// builder.last.Transcript != expected.
//
// godlike/06 SSOT: this test is the canonical regression
// guard for the Fase 1.c structural removal (the parameter
// chain that makes the double-Whisper bug architecturally
// impossible).
//
// The languageCode parameter is passed through the seam for
// compile-time parity but is NOT asserted on
// ClipMetadataInput — the dto.ClipMetadataInput type does
// not currently expose a LanguageCode field, and threading
// one in belongs to a separate field-coverage audit (the
// out-of-scope for Fase 2.c data-flow guarantee).
func TestStep10_Fase2c_BundlePlainText_ReachesEnrichClip(t *testing.T) {
	const expectedTranscript = "Fase 2.c hardening: explicit bundle-to-metadata data-flow"
	const bundleLanguageCode = "en"

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

	err := uc.step10_MetadataEnrich(
		context.Background(),
		cmd,
		"yt_fase2c_dataflow_0_10_v1", // clipID
		10,                           // duration
		expectedTranscript,           // transcript (bundle.PlainText analog)
		bundleLanguageCode,           // languageCode (bundle.LanguageCode analog; passed-but-currently-unused)
		nil,                          // cues (accepted but unused; see _ = cues in step10)
	)
	require.NoError(t, err, "step10_MetadataEnrich with a recording MetadataService must succeed")

	// Load-bearing Fase 2.c assertions:
	require.Equal(t, int32(1), atomic.LoadInt32(&builder.calls),
		"Builder.Build call count = want 1 (step10_MetadataEnrich must invoke EnrichClip exactly once)")
	require.Equal(t, expectedTranscript, builder.last.Transcript,
		"Fase 2.c data-flow: ClipMetadataInput.Transcript MUST equal the step10 transcript parameter byte-for-byte. The bundle.PlainText thread is regression-proof at this seam.")
	require.Equal(t, cmd.Segment.Name, builder.last.Title,
		"Fase 2.c data-flow: ClipMetadataInput.Title MUST equal cmd.Segment.Name (no silent rename)")
	require.Equal(t, []string{"alpha", "beta"}, builder.last.Topics,
		"Fase 2.c data-flow: ClipMetadataInput.Topics MUST equal cmd.Segment.Topics (free win — a future regression that silently drops fields would surface here)")
}

// ── Test 2: empty-bundle (fail-closed grace) ──────────────────────────

// TestStep10_Fase2c_EmptyTranscript_ReachesEnrichClip is the
// empty-bundle companion.
//
// When the entire 5-priority chain at Step 6-9 fails (no DB
// track, no YouTube subs, no Whisper output) the orchestrator
// passes (transcript="", languageCode="") to step10_MetadataEnrich.
// Step 10 MUST NOT silently source a Whisper fallback here —
// the empty fields MUST thread through verbatim and the
// downstream metadata service handles empty transcripts
// gracefully (low QualityScore, no crash) per godlike/07
// fail-closed.
//
// godlike/07 NO-FAKE-AVAILABILITY: the contract is "empty
// fields flow through" — not "Step 10 secretly re-runs Whisper
// to fill the gap" (that would reintroduce the double-Whisper
// regression in a different shape).
func TestStep10_Fase2c_EmptyTranscript_ReachesEnrichClip(t *testing.T) {
	metaSvc, builder := newRecordingMetadataService(t)
	core, media, metadata, observability := validProcessSegmentDeps()
	metadata.MetadataService = metaSvc
	uc := NewProcessYouTubeSegmentFromSubBundles(core, media, metadata, observability)

	cmd := youtubetypes.ProcessSegmentCommand{
		VideoID: "yt_fase2c_empty",
		Segment: youtubetypes.Segment{Name: "Fase2cEmpty", Start: "0:00", End: "0:10"},
	}

	err := uc.step10_MetadataEnrich(
		context.Background(),
		cmd,
		"yt_fase2c_empty_0_10_v1",
		10,
		"", // transcript = "" (Step 6-9 returned nil/empty)
		"", // languageCode = ""
		nil,
	)
	require.NoError(t, err, "step10_MetadataEnrich with empty paragraph fields must succeed (metadata service degrades gracefully per godlike/07)")

	require.Equal(t, int32(1), atomic.LoadInt32(&builder.calls),
		"Builder.Build call count = want 1 (step10_MetadataEnrich must invoke EnrichClip once even with empty transcript)")
	require.Equal(t, "", builder.last.Transcript,
		"Fase 2.c empty-bundle audit: ClipMetadataInput.Transcript MUST equal the empty string passed in, NOT a Whisper fallback. Re-introducing a direct Whisper call here would silently resurrect the double-Whisper regression.")
	// Note: languageCode and other ClipMetadataInput fields are not asserted
	// in the empty-bundle variant — the data-flow audit only needs to pin
	// the Transcript thread; the field-coverage audit lives in Test 1.
}

// ── Test 3: zero Transcriber calls on direct Step 10 (resolver + MetadataService both wired) ──

// TestStep10_Fase2c_ZeroTranscriberCalls_WithResolverAndMetadataWired
// pins the counter-level guarantee strictly at the Step 10
// boundary, even when EVERYTHING is wired. Step 10's signature
// takes (transcript, languageCode, cues) as parameters — it
// MUST NOT reach back into the resolver or the Transcriber
// port, ever.
//
// godlike/06 SSOT: step10_MetadataEnrich reads ONLY from its
// parameter list. The compile-time guarantee is that
// ProcessSegmentDeps no longer has a Transcriber field
// (Fase 1.c retirement), so Step 10 cannot reach one even if
// a future maintainer tried to reintroduce a direct call.
// This test pins that guarantee with a runtime observation
// AND a positive data-flow assertion (the transcript
// parameter, NOT a Whisper fallback, reaches the builder).
func TestStep10_Fase2c_ZeroTranscriberCalls_WithResolverAndMetadataWired(t *testing.T) {
	tport := &countingTranscriber{text: "MUST NOT be called from Step 10"}
	metaSvc, builder := newRecordingMetadataService(t)

	core, media, metadata, observability := validProcessSegmentDeps()
	metadata.MetadataService = metaSvc
	// Wire the canonical 5-priority chain so the use case's
	// resolver port is non-nil + WOULD fire Whisper if Step 10
	// ever reached back. The noRowsRepo + noSubtitleFetcher
	// force the chain to fall through to priority 5, so ANY
	// leak from Step 10 to the resolver would surface as a
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

	err := uc.step10_MetadataEnrich(
		context.Background(),
		cmd,
		"yt_fase2c_zerocounts_0_10_v1",
		10,
		"transcript parameter (Step 10 only sees this)",
		"en",
		nil,
	)
	require.NoError(t, err)

	// Load-bearing Fase 2.c assertions:
	require.Equal(t, int32(0), atomic.LoadInt32(&tport.calls),
		"Fase 2.c contract: step10_MetadataEnrich must produce 0 Transcriber invocations even when TextTrackResolver + a live Transcriber are wired. Step 10 reads ONLY from its parameter list; it has no Whisper fallback path. Got %d calls — Step 10 is reaching into the resolver/Whisper.", tport.calls)
	require.Equal(t, int32(1), atomic.LoadInt32(&builder.calls),
		"Builder.Build call count = want 1 (positive sanity check: with MetadataService wired, EnrichClip must fire exactly once)")
	require.Equal(t, "transcript parameter (Step 10 only sees this)", builder.last.Transcript,
		"Fase 2.c data-flow: even with the resolver wired, the parameter (not a Whisper fallback) reaches the builder")
}
