// Package usecase — process_segment_step10_metrics_test.go: hermetic
// TDD test for the Step 10 partial-state metrics wiring in
// ProcessYouTubeSegmentUseCase (PR-PY-STEP10-FAIL-LOG-OBSEVE-PARITY,
// July 2026).
//
// godlike/06 SSOT (one canonical owner per fact): the Step 10
// metrics port is the SOLE canonical application-layer surface for
// the transcript_metadata_step10_fail_after_clip_total counter
// increment. The use case MUST NOT import the observability package
// directly; it consumes the typed port and the composition root
// wires the concrete adapter.
//
// godlike/07 NO-FAKE-AVAILABILITY contract pinned in this test:
//  1. On a Step 10 metadata-enrichment failure, the use case MUST
//     call Step10Metrics.IncStep10FailAfterClip exactly ONCE with
//     the stringified FailureCode constant ("metadata_failed").
//  2. On a successful Step 10 path (MetadataService nil → no
//     invocation), the recorder MUST NOT be called (no
//     false-positive counter increments).
//  3. On a Step 10 path where Step10Metrics is nil (composition
//     root omitted the wiring), the use case MUST NOT panic
//     (the optional-port nil-tolerance pattern).
//
// The test uses a mock recorder (struct in the test file) that
// captures every call. The mock satisfies the Step10MetricsRecorder
// port shape via a structural Go type assertion — no observability
// import in this file (godlike/06).
package usecase

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/dto"
	ytmetadata "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/metadata"
	youtubeports "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/ports"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// step10MetricsMock satisfies the Step10MetricsRecorder port shape
// (structural Go interface satisfaction — no import needed). The
// mock records every failure_code parameter it sees, in order.
type step10MetricsMock struct {
	calls []string
}

func (m *step10MetricsMock) IncStep10FailAfterClip(failureCode string) {
	m.calls = append(m.calls, failureCode)
}

// Compile-time structural assertion: the mock satisfies the
// canonical application-layer port (locks the signature).
var _ youtubeports.Step10MetricsRecorder = (*step10MetricsMock)(nil)

// errMetadataBuilder is a ClipMetadataBuilder fake that always
// returns an error from Build. Used to drive the Step 10
// metadata-enrichment failure path. Mirrors the pattern from
// process_segment_correttezza_test.go::errBuilder (same package).
type errMetadataBuilder struct{}

func (errMetadataBuilder) Build(_ context.Context, _ youtubetypes.ClipMetadataInput) (youtubetypes.CanonicalClipMetadata, error) {
	return youtubetypes.CanonicalClipMetadata{}, errors.New("simulated metadata builder failure (PR-PY-STEP10-FAIL-LOG-OBSEVE-PARITY test)")
}

// Compile-time assertion: errMetadataBuilder satisfies ClipMetadataBuilder.
var _ ytmetadata.ClipMetadataBuilder = errMetadataBuilder{}

// noopMetadataWriter satisfies ClipMetadataWriter. Not exercised in
// the failure path (errMetadataBuilder errors before the writer is
// called) but satisfies NewMetadataService's required-arg contract.
type noopMetadataWriter struct{}

func (noopMetadataWriter) UpdateClipMetadataAndRequestIndex(_ context.Context, _ string, _ youtubetypes.CanonicalClipMetadata) error {
	return nil
}

func (noopMetadataWriter) UpdateClipMetadataTextsAndRequestIndex(_ context.Context, _ string, _ youtubetypes.CanonicalClipMetadata, _ []asset.TextTrack) error {
	return nil
}

// Compile-time assertion: noopMetadataWriter satisfies ClipMetadataWriter.
var _ youtubeports.ClipMetadataWriter = noopMetadataWriter{}

// newAlwaysFailingMetadataService constructs a real
// *ytmetadata.MetadataService that always errors on EnrichClip
// (driven by the errMetadataBuilder). Returns the service so the
// test can inject it into ProcessSegmentDeps.
func newAlwaysFailingMetadataService(t *testing.T) *ytmetadata.MetadataService {
	t.Helper()
	svc, err := ytmetadata.NewMetadataService(ytmetadata.MetadataDeps{
		Builder:  errMetadataBuilder{},
		Writer:   noopMetadataWriter{},
		Logger:   zap.NewNop(),
		JobID:    "test-job",
		JobGroup: "general",
	})
	require.NoError(t, err, "NewMetadataService must succeed with errMetadataBuilder + noopMetadataWriter")
	require.NotNil(t, svc)
	return svc
}

// TestProcessSegment_MetadataAnalysisFailure_FailsBeforeCommit_NoMetric is the
// canonical hermetic probe for the PR-ASSET-COMMITTER-ENRICHMENT contract:
// a metadata-analysis failure now happens INSIDE step6to9 BEFORE the canonical
// commit, so (a) the run fails closed with FailureCodeMetadataFailed and (b)
// the obsolete Step 10 "fail-after-clip" metrics recorder is NOT invoked —
// that partial-state class no longer exists.
func TestProcessSegment_MetadataAnalysisFailure_FailsBeforeCommit_NoMetric(t *testing.T) {
	t.Parallel()

	mockMetrics := &step10MetricsMock{}

	// Reuse the validProcessSegmentDeps helper (in-package) for the
	// canonical 9-step happy-path stubs (Steps 1..9). Override the
	// metadata + file-path requirements:
	//   - VideoPipeline:  stubVideoPipelineWithPath returns a real
	//     file so os.Stat passes (Execute MUST reach step6to9)
	//   - Hash:           testStubHash returns non-empty (Step 5's
	//     fail-closed gate)
	//   - MetadataService: real service that ALWAYS errors on
	//     AnalyzeClip (forces the pre-commit analysis failure path)
	//   - Step10Metrics:  the mock recorder (asserted NOT called)
	realPath := mustWriteFakeFile(t, "step10-metrics-probe.mp4")
	core, media, metadata, observability := validProcessSegmentDeps()
	core.VideoPipeline = stubVideoPipelineWithPath{path: realPath}
	core.Hash = testStubHash{}
	metadata.MetadataService = newAlwaysFailingMetadataService(t)
	observability.Step10Metrics = mockMetrics
	uc := NewProcessYouTubeSegmentFromSubBundles(core, media, metadata, observability)

	cmd := youtubetypes.ProcessSegmentCommand{
		VideoURL: "https://www.youtube.com/watch?v=yt_step10_metrics_001",
		VideoID:  "yt_step10_metrics_001",
		OutDir:   t.TempDir(),
		Segment: youtubetypes.Segment{
			Start: "0:00:00.000",
			End:   "0:00:30.000",
			Name:  "step10-metrics-probe",
		},
	}

	// Execute must return the typed *ExtractionError with
	// FailureCodeMetadataFailed (the canonical job outcome).
	_, execErr := uc.Execute(context.Background(), cmd)
	require.Error(t, execErr, "Execute succeeded; expected typed ExtractionError(FailureCodeMetadataFailed)")
	ee, ok := execErr.(*ExtractionError)
	require.True(t, ok, "Execute error is not *ExtractionError: %T %v", execErr, execErr)
	require.Equal(t, FailureCodeMetadataFailed, ee.Code, "FailureCode mismatch (metadata analysis failure must fail closed)")

	// godlike/07 contract: the obsolete "Step 10 fail AFTER clip write"
	// recorder is NOT invoked — the partial-state class is eliminated.
	require.Equal(t, 0, len(mockMetrics.calls), "Step10Metrics.IncStep10FailAfterClip call count = want 0 (the 'fail after clip' class no longer exists)")
}

// TestProcessSegment_Step10Success_DoesNotInvokeMetricsRecorder
// pins the godlike/07 NO-FAKE-AVAILABILITY inverse contract: when
// MetadataService is nil (the default — Step 10 is silently
// skipped, no error path is exercised), the recorder MUST NOT be
// invoked (no false-positive events).
func TestProcessSegment_Step10Success_DoesNotInvokeMetricsRecorder(t *testing.T) {
	t.Parallel()

	mockMetrics := &step10MetricsMock{}

	// validProcessSegmentDeps has MetadataService=nil by default;
	// Step 10 short-circuits silently (the optional-port pattern).
	// We wire the recorder so we can verify it is NOT called.
	realPath := mustWriteFakeFile(t, "step10-success-probe.mp4")
	core, media, metadata, observability := validProcessSegmentDeps()
	core.VideoPipeline = stubVideoPipelineWithPath{path: realPath}
	core.Hash = testStubHash{}
	observability.Step10Metrics = mockMetrics
	// metadata.MetadataService is nil by default — leave as-is.
	uc := NewProcessYouTubeSegmentFromSubBundles(core, media, metadata, observability)

	cmd := youtubetypes.ProcessSegmentCommand{
		VideoURL: "https://www.youtube.com/watch?v=yt_step10_metrics_002",
		VideoID:  "yt_step10_metrics_002",
		OutDir:   t.TempDir(),
		Segment: youtubetypes.Segment{
			Start: "0:00:00.000",
			End:   "0:00:30.000",
			Name:  "step10-success-probe",
		},
	}

	out, execErr := uc.Execute(context.Background(), cmd)
	require.NoError(t, execErr, "Execute failed on the success path: %v", execErr)
	require.Equal(t, "processed", out.Status, "Execute must surface as processed on the success path")
	require.Equal(t, 0, len(mockMetrics.calls), "Step10Metrics.IncStep10FailAfterClip call count on success path: want 0 (godlike/07 no-fake-availability)")
}

// TestProcessSegment_Step10Failure_NilRecorder_NoPanic pins the
// nil-tolerance contract: the use case MUST NOT panic when the
// composition root omits the Step10Metrics wiring (the optional-
// port pattern matches Subtitles/Transcriber/DriveFolderMgr/FFProbe).
func TestProcessSegment_Step10Failure_NilRecorder_NoPanic(t *testing.T) {
	t.Parallel()

	// Same as the failure-path test, but Step10Metrics is nil
	// (composition root omitted the wiring). Execute must still
	// surface the typed ExtractionError; the missing recorder
	// must not cause a panic.
	realPath := mustWriteFakeFile(t, "step10-nil-metrics-probe.mp4")
	core, media, metadata, observability := validProcessSegmentDeps()
	core.VideoPipeline = stubVideoPipelineWithPath{path: realPath}
	core.Hash = testStubHash{}
	metadata.MetadataService = newAlwaysFailingMetadataService(t)
	// observability.Step10Metrics is nil by default — do NOT set it.
	uc := NewProcessYouTubeSegmentFromSubBundles(core, media, metadata, observability)

	cmd := youtubetypes.ProcessSegmentCommand{
		VideoURL: "https://www.youtube.com/watch?v=yt_step10_metrics_003",
		VideoID:  "yt_step10_metrics_003",
		OutDir:   t.TempDir(),
		Segment: youtubetypes.Segment{
			Start: "0:00:00.000",
			End:   "0:00:30.000",
			Name:  "step10-nil-metrics-probe",
		},
	}

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Execute panicked with nil Step10Metrics: %v", r)
		}
	}()

	_, execErr := uc.Execute(context.Background(), cmd)
	require.Error(t, execErr, "Execute succeeded; expected typed ExtractionError(FailureCodeMetadataFailed)")
	ee, ok := execErr.(*ExtractionError)
	require.True(t, ok && ee.Code == FailureCodeMetadataFailed,
		"expected *ExtractionError{FailureCodeMetadataFailed}, got %T %v", execErr, execErr)
}

// ── Test helpers ──────────────────────────────────────────────────────

// mustWriteFakeFile writes a non-empty file at t.TempDir()/name and
// returns the absolute path. Used to satisfy the use case's Step 5
// os.Stat check (the file MUST exist and be non-empty for Execute
// to reach Step 10).
func mustWriteFakeFile(t *testing.T, name string) string {
	t.Helper()
	p := t.TempDir() + "/" + name
	if err := os.WriteFile(p, []byte("fake-clip-bytes-for-step10-test"), 0o644); err != nil {
		t.Fatalf("os.WriteFile: %v", err)
	}
	return p
}
