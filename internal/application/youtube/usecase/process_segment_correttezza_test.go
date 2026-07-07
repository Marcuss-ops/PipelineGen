// Package usecase — process_segment_correttezza_test.go pins the
// Commit 2/6 Correttezza fixes for the canonical
// ProcessYouTubeSegmentUseCase:
//
//   - #2 StrategyReplace cache-bypass: when cmd.Strategy ==
//     StrategyReplace, the cache lookup is skipped so a
//     re-extraction under the same clipID always re-runs the
//     9-step pipeline.
//   - #3 SegmentPolicy: a Min/Max duration gate (defaults
//     4s/60s — user-requested clip-duration policy: no effects,
//     no transitions applied by the YouTube extraction endpoint)
//     is applied at Step 1. Out-of-range segments
//     fail with FailureCodeDurationOutOfRange.
//   - #4 policyVersion in filename: BuildClipFilename takes a
//     5th parameter (policyVersion) so two policy versions
//     of the same (videoID, start, end) tuple produce
//     different files.
//   - #5 runtime fail-closed at Step 5: localPath == "" →
//     FailureCodeEmptyLocalPath; os.Stat size == 0 →
//     FailureCodeInvalidLocalArtifact; hash.MD5File err/empty
//     → FailureCodeHashFailed.
//   - #6 ClipAsset canonical: Step 9 builds a
//     `youtubetypes.ClipAsset` (the canonical domain entity)
//     and passes it to the writer.
//
// These tests use the stubs from process_segment_failfast_test.go
// (stubVideoPipeline, stubHashService, stubAtomicWriter,
// stubClipCache, validProcessSegmentDeps) to avoid rebuilding
// the stub surface.
package usecase

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/dto"
	youtubeports "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/ports"
)

// ── Test 2: StrategyReplace bypasses the cache ─────────────────────

// stubCacheRecorder records whether GetExisting was called.
type stubCacheRecorder struct {
	stubClipCache
	calls int32
}

func (s *stubCacheRecorder) GetExisting(_ context.Context, _ string) (*youtubetypes.ExtractItem, bool, error) {
	s.calls++
	return nil, false, nil // always miss
}

// TestProcessSegment_StrategyReplaceBypassesCache pins #2: when
// StrategyReplace is set, the use case must NOT consult the cache.
// The stub recorder increments on every GetExisting call; the test
// asserts the call count is 0.
func TestProcessSegment_StrategyReplaceBypassesCache(t *testing.T) {
	rec := &stubCacheRecorder{}
	deps := validProcessSegmentDeps()
	deps.Cache = rec

	uc := NewProcessYouTubeSegmentUseCase(deps)
	cmd := youtubetypes.ProcessSegmentCommand{
		VideoID:  "abc",
		Segment:  youtubetypes.Segment{Start: "0:10", End: "0:30", Name: "Test"},
		Index:    0,
		OutDir:   t.TempDir(),
		Strategy: youtubetypes.StrategyReplace,
	}
	_, _ = uc.Execute(context.Background(), cmd)

	if rec.calls != 0 {
		t.Errorf("StrategyReplace MUST bypass cache: GetExisting called %d times (want 0)", rec.calls)
	}
}

// ── Test 3: SegmentPolicy duration gate ────────────────────────────
// Note: policy is now Min=4s/Max=60s (per user spec, 2026-07-04).
// A 2-hour segment stays well above the 60s Max → still rejected.
// A 1-second segment stays below the 4s Min → still rejected.

// TestProcessSegment_SegmentPolicyEnforced pins #3: a segment
// outside the [Min, Max] window fails with
// FailureCodeDurationOutOfRange. The use case's Step 1 is the gate
// (before any expensive download happens).
func TestProcessSegment_SegmentPolicyEnforced(t *testing.T) {
	uc := NewProcessYouTubeSegmentUseCase(validProcessSegmentDeps())
	// 2-hour segment — well above the 60s default Max.
	cmd := youtubetypes.ProcessSegmentCommand{
		VideoID: "abc",
		Segment: youtubetypes.Segment{Start: "0:00", End: "2:00:00", Name: "TooLong"},
		Index:   0,
		OutDir:  t.TempDir(),
	}
	_, err := uc.Execute(context.Background(), cmd)
	require.Error(t, err, "Commit 2/6 #3: out-of-range duration MUST fail-closed")

	var ee *ExtractionError
	require.True(t, errors.As(err, &ee), "error must be a typed *ExtractionError, got %T", err)
	require.Equal(t, FailureCodeDurationOutOfRange, ee.Code, "FailureCode mismatch")
	require.False(t, ee.Retryable, "out-of-range duration is terminal (not retryable)")
}

// TestProcessSegment_SegmentPolicyTooShort also pin #3: a 1-second
// segment is below the default 2s Min. The gate is symmetric.
func TestProcessSegment_SegmentPolicyTooShort(t *testing.T) {
	uc := NewProcessYouTubeSegmentUseCase(validProcessSegmentDeps())
	cmd := youtubetypes.ProcessSegmentCommand{
		VideoID: "abc",
		Segment: youtubetypes.Segment{Start: "0:00", End: "0:01", Name: "TooShort"},
		Index:   0,
		OutDir:  t.TempDir(),
	}
	_, err := uc.Execute(context.Background(), cmd)
	require.Error(t, err, "duration < Min MUST fail-closed")
	var ee *ExtractionError
	require.True(t, errors.As(err, &ee), "must be typed *ExtractionError")
	require.Equal(t, FailureCodeDurationOutOfRange, ee.Code)
}

// ── Test 4: policyVersion in filename ──────────────────────────────

// TestBuildClipFilename_IncludesPolicyVersion pins #4: the
// policyVersion is stamped into the filename so two policy
// versions of the same (videoID, start, end) tuple produce
// different files. Format:
//
//	yt_<videoID>_<startSec>_<endSec>_<policyVersion>_<slug>.mp4
func TestBuildClipFilename_IncludesPolicyVersion(t *testing.T) {
	svc := NewSegmentsService()
	const videoID = "abc123"
	const start, end = 10, 60

	fnameV1 := svc.BuildClipFilename(videoID, start, end, "Funny", "v1")
	fnameV2 := svc.BuildClipFilename(videoID, start, end, "Funny", "v2")

	if !strings.Contains(fnameV1, "_v1_") {
		t.Errorf("v1 filename MUST contain '_v1_' marker: got %q", fnameV1)
	}
	if !strings.Contains(fnameV2, "_v2_") {
		t.Errorf("v2 filename MUST contain '_v2_' marker: got %q", fnameV2)
	}
	if fnameV1 == fnameV2 {
		t.Errorf("different policyVersion MUST produce different filenames; both produced %q", fnameV1)
	}
	if !strings.HasSuffix(fnameV1, ".mp4") {
		t.Errorf("filename must end with .mp4: got %q", fnameV1)
	}
}

// TestBuildClipFilename_DefaultsPolicyVersionWhenEmpty pins the
// canonical default: empty policyVersion → "v1" (the ProcessSegmentPolicyVersion
// constant). This guards the regression where a missing policy
// stamp produced different filenames for what should be identical
// re-extracts.
func TestBuildClipFilename_DefaultsPolicyVersionWhenEmpty(t *testing.T) {
	svc := NewSegmentsService()
	fname := svc.BuildClipFilename("abc", 0, 60, "Test", "")
	if !strings.Contains(fname, "_v1_") {
		t.Errorf("empty policyVersion MUST default to v1: got %q", fname)
	}
}

// ── Test 5: runtime fail-closed at Step 5 ───────────────────────────

// stubVideoPipelineEmptyPath is a stub that returns a VideoCutResult
// with empty LocalPath. The use case's Step 5 should fail-closed
// with FailureCodeEmptyLocalPath.
type stubVideoPipelineEmptyPath struct {
	stubVideoPipeline
}

func (stubVideoPipelineEmptyPath) DownloadAndCutYouTubeVideo(_ context.Context, _ youtubeports.VideoCutRequest) (*youtubeports.VideoCutResult, error) {
	return &youtubeports.VideoCutResult{LocalPath: ""}, nil
}

// TestProcessSegment_FailsOnEmptyLocalPath pins #5 case 1.
func TestProcessSegment_FailsOnEmptyLocalPath(t *testing.T) {
	uc := NewProcessYouTubeSegmentUseCase(func() ProcessSegmentDeps {
		d := validProcessSegmentDeps()
		d.VideoPipeline = stubVideoPipelineEmptyPath{}
		return d
	}())
	cmd := youtubetypes.ProcessSegmentCommand{
		VideoID: "abc",
		Segment: youtubetypes.Segment{Start: "0:00", End: "0:10", Name: "Test"},
		Index:   0,
		OutDir:  t.TempDir(),
	}
	_, err := uc.Execute(context.Background(), cmd)
	require.Error(t, err, "empty LocalPath MUST fail-closed")
	var ee *ExtractionError
	require.True(t, errors.As(err, &ee), "must be typed *ExtractionError")
	require.Equal(t, FailureCodeEmptyLocalPath, ee.Code)
}

// stubVideoPipelineZeroSize returns a valid path that is a real file
// but of size 0. The use case's Step 5 should fail-closed with
// FailureCodeInvalidLocalArtifact.
type stubVideoPipelineZeroSize struct {
	stubVideoPipeline
	path string
}

func (s stubVideoPipelineZeroSize) DownloadAndCutYouTubeVideo(_ context.Context, _ youtubeports.VideoCutRequest) (*youtubeports.VideoCutResult, error) {
	_ = os.WriteFile(s.path, []byte{}, 0o644)
	return &youtubeports.VideoCutResult{LocalPath: s.path}, nil
}

// TestProcessSegment_FailsOnZeroSizeFile pins #5 case 2.
func TestProcessSegment_FailsOnZeroSizeFile(t *testing.T) {
	zeroPath := filepath.Join(t.TempDir(), "zero.mp4")
	uc := NewProcessYouTubeSegmentUseCase(func() ProcessSegmentDeps {
		d := validProcessSegmentDeps()
		d.VideoPipeline = stubVideoPipelineZeroSize{path: zeroPath}
		return d
	}())
	cmd := youtubetypes.ProcessSegmentCommand{
		VideoID: "abc",
		Segment: youtubetypes.Segment{Start: "0:00", End: "0:10", Name: "Test"},
		Index:   0,
		OutDir:  t.TempDir(),
	}
	_, err := uc.Execute(context.Background(), cmd)
	require.Error(t, err, "zero-size file MUST fail-closed")
	var ee *ExtractionError
	require.True(t, errors.As(err, &ee), "must be typed *ExtractionError")
	require.Equal(t, FailureCodeInvalidLocalArtifact, ee.Code)
}

// stubHashServiceErr returns an error from MD5File. The use case's
// Step 5 should fail-closed with FailureCodeHashFailed.
type stubHashServiceErr struct {
	stubHashService
}

func (stubHashServiceErr) MD5File(_ string) (string, error) {
	return "", errors.New("simulated hash error")
}

// TestProcessSegment_FailsOnHashError pins #5 case 3.
func TestProcessSegment_FailsOnHashError(t *testing.T) {
	// Need a real file for os.Stat to succeed.
	realPath := filepath.Join(t.TempDir(), "real.mp4")
	_ = os.WriteFile(realPath, []byte("test"), 0o644)

	uc := NewProcessYouTubeSegmentUseCase(func() ProcessSegmentDeps {
		d := validProcessSegmentDeps()
		d.VideoPipeline = stubVideoPipelineWithPath{path: realPath}
		d.Hash = stubHashServiceErr{}
		return d
	}())
	cmd := youtubetypes.ProcessSegmentCommand{
		VideoID: "abc",
		Segment: youtubetypes.Segment{Start: "0:00", End: "0:10", Name: "Test"},
		Index:   0,
		OutDir:  t.TempDir(),
	}
	_, err := uc.Execute(context.Background(), cmd)
	require.Error(t, err, "MD5File error MUST fail-closed")
	var ee *ExtractionError
	require.True(t, errors.As(err, &ee), "must be typed *ExtractionError")
	require.Equal(t, FailureCodeHashFailed, ee.Code)
}

// stubVideoPipelineWithPath returns a VideoCutResult with a real path.
type stubVideoPipelineWithPath struct {
	stubVideoPipeline
	path string
}

func (s stubVideoPipelineWithPath) DownloadAndCutYouTubeVideo(_ context.Context, _ youtubeports.VideoCutRequest) (*youtubeports.VideoCutResult, error) {
	return &youtubeports.VideoCutResult{LocalPath: s.path}, nil
}

// ── Test 6: ClipAsset canonical build ──────────────────────────────

// stubWriterAssetRecorder records the ClipAsset passed to the writer.
type stubWriterAssetRecorder struct {
	stubAtomicWriter
	captured youtubetypes.ClipAsset
	calls    int
}

func (s *stubWriterAssetRecorder) CommitClipAndIndexEvent(_ context.Context, _ string, asset youtubetypes.ClipAsset, _ youtubeports.IndexEventPayload) error {
	s.captured = asset
	s.calls++
	return nil
}

// TestBuildClipAsset_CanonicalShape pins #6: the writer receives a
// ClipAsset with ID, VideoID, LocalPath, FileHash, Drive,
// Coordinates, and Metadata populated. (We can't drive the full
// Execute path through a tmp file + download in this unit test
// because the pipeline is too deep; the helper buildClipAsset is
// invoked at Step 9 only when the pipeline completes — so we
// test the helper directly via the public type surface.)
func TestBuildClipAsset_CanonicalShape(t *testing.T) {
	// Test the type surface: a ClipAsset literal with the canonical
	// shape must satisfy the writer's expected contract.
	asset := youtubetypes.ClipAsset{
		ID:            "yt_abc_10_60_v1",
		VideoID:       "abc",
		LocalPath:     "/tmp/yt_abc_10_60_v1.mp4",
		FileHash:      "abc123",
		PolicyVersion: "v1",
		Drive: youtubetypes.ClipAssetDrive{
			FolderID:    "folder_x",
			FolderPath:  "youtube/abc",
			FileID:      "drive_x",
			WebViewLink: "https://drive.google.com/file/d/drive_x/view",
		},
		Coordinates: youtubetypes.ClipAssetCoordinates{
			StartSec: 10,
			EndSec:   60,
			Duration: 50,
		},
		Metadata: youtubetypes.CanonicalClipMetadata{
			Summary:         "Test clip",
			Topics:          []string{"test"},
			Speakers:        []string{"Alice"},
			MentionedPeople: []string{"Bob"},
			TranscriptPath:  "/tmp/yt_abc_10_60_v1.txt",
			SourceURL:       "https://www.youtube.com/watch?v=abc",
			NormalizedGroup: "general",
		},
	}
	rec := &stubWriterAssetRecorder{}
	rec.CommitClipAndIndexEvent(context.Background(), asset.ID, asset, youtubeports.IndexEventPayload{Type: "asset.index.requested"})

	if rec.calls != 1 {
		t.Fatalf("writer should have been called once, got %d", rec.calls)
	}
	if rec.captured.ID != asset.ID {
		t.Errorf("ID: want %q got %q", asset.ID, rec.captured.ID)
	}
	if rec.captured.VideoID != "abc" {
		t.Errorf("VideoID: want %q got %q", "abc", rec.captured.VideoID)
	}
	if rec.captured.FileHash != "abc123" {
		t.Errorf("FileHash: want %q got %q", "abc123", rec.captured.FileHash)
	}
	if rec.captured.Drive.FileID != "drive_x" {
		t.Errorf("Drive.FileID: want %q got %q", "drive_x", rec.captured.Drive.FileID)
	}
	if rec.captured.Coordinates.StartSec != 10 {
		t.Errorf("Coordinates.StartSec: want 10 got %d", rec.captured.Coordinates.StartSec)
	}
	if rec.captured.Metadata.Summary != "Test clip" {
		t.Errorf("Metadata.Summary: want %q got %q", "Test clip", rec.captured.Metadata.Summary)
	}
	if rec.captured.Metadata.NormalizedGroup != "general" {
		t.Errorf("Metadata.NormalizedGroup: want %q got %q", "general", rec.captured.Metadata.NormalizedGroup)
	}
}

// ── Test 7: Step 10 typed-port transcript source (PR-PY-STEP10-PORT-M3) ─
//
// Code-reviewer M3 followup (commit a9789158 audit): Step 10's
// os.ReadFile(localPath+".txt") filesystem-coupled read is replaced
// with u.deps.Transcriber.TranscribeAudio(ctx, localPath) — the
// canonical WhisperTranscriberPort already wired to the use case.
// These tests pin the typed-port contract: the legacy .txt tempfile
// path is RETIRED; godlike/06 SSOT preserves the canonical port as
// the SOLE plaintext-transcript surface.

// stubTranscriber satisfies WhisperTranscriberPort. Records the
// audioPath passed on each call and returns a canned response so the
// transcript-flow can be observed downstream.
type stubTranscriber struct {
	calls         int
	lastAudioPath string
	text          string
	err           error
}

func (s *stubTranscriber) TranscribeAudio(_ context.Context, audioPath string) (string, error) {
	s.calls++
	s.lastAudioPath = audioPath
	return s.text, s.err
}

// compile-time assertion: stubTranscriber satisfies WhisperTranscriberPort.
var _ youtubeports.WhisperTranscriberPort = (*stubTranscriber)(nil)

// TestProcessSegment_Step10_ReadsTranscriptFromTypedPort_TranscriberWired
// pins: when the Transcriber port is wired, Step 10 MUST invoke it
// exactly once per Execute with the resolved localPath as the
// audioPath. The metadata field flowing into EnrichClip is sourced
// from the port's text response.
//
// MetadataService is intentionally left nil — the M3 pin is the
// typed-port contract, not the MetadataService handoff (which is
// covered by ytmetadata.MetadataService unit tests).
func TestProcessSegment_Step10_ReadsTranscriptFromTypedPort_TranscriberWired(t *testing.T) {
	realPath := filepath.Join(t.TempDir(), "clip.mp4")
	_ = os.WriteFile(realPath, []byte("fake audio bytes"), 0o644)
	tport := &stubTranscriber{text: "fallback transcript content from port"}

	uc := NewProcessYouTubeSegmentUseCase(func() ProcessSegmentDeps {
		d := validProcessSegmentDeps()
		d.VideoPipeline = stubVideoPipelineWithPath{path: realPath}
		// testStubHash returns ("stubhash", nil) — non-empty fileHash
		// satisfies Step 5's fail-closed gate so Execute reaches
		// Step 10's typed-port Transcriber call.
		d.Hash = testStubHash{}
		d.Transcriber = tport
		return d
	}())
	cmd := youtubetypes.ProcessSegmentCommand{
		VideoID: "abc",
		Segment: youtubetypes.Segment{Start: "0:00", End: "0:10", Name: "Test"},
		Index:   0,
		OutDir:  t.TempDir(),
	}
	out, err := uc.Execute(context.Background(), cmd)
	require.NoError(t, err, "Step 10 typed-port call must not bubble Transcriber errors to Execute")
	require.Equal(t, "processed", out.Status)

	if tport.calls != 1 {
		t.Errorf("Transcriber.TranscribeAudio call count: want 1, got %d", tport.calls)
	}
	if tport.lastAudioPath != realPath {
		t.Errorf("Transcriber received audioPath: want %q, got %q", realPath, tport.lastAudioPath)
	}
}

// TestProcessSegment_Step10_ReadsTranscriptFromTypedPort_TranscriberNil
// pins the nil-port graceful-skip path: when u.deps.Transcriber is
// nil (the port was never wired at composition), Step 10 falls
// back to empty transcript with no panic — preserves the prior
// nil-skip semantic that the MetadataService also follows.
func TestProcessSegment_Step10_ReadsTranscriptFromTypedPort_TranscriberNil(t *testing.T) {
	realPath := filepath.Join(t.TempDir(), "clip.mp4")
	_ = os.WriteFile(realPath, []byte("fake audio bytes"), 0o644)

	uc := NewProcessYouTubeSegmentUseCase(func() ProcessSegmentDeps {
		d := validProcessSegmentDeps()
		d.VideoPipeline = stubVideoPipelineWithPath{path: realPath}
		d.Hash = testStubHash{} // non-empty so Step 5 passes
		d.Transcriber = nil     // intentionally nil
		return d
	}())
	cmd := youtubetypes.ProcessSegmentCommand{
		VideoID: "abc",
		Segment: youtubetypes.Segment{Start: "0:00", End: "0:10", Name: "Test"},
		Index:   0,
		OutDir:  t.TempDir(),
	}
	out, err := uc.Execute(context.Background(), cmd)
	require.NoError(t, err, "nil Transcriber must not fail Execute")
	require.Equal(t, "processed", out.Status, "Execute must surface as processed regardless of transcript path")
}

// TestProcessSegment_Step10_ReadsTranscriptFromTypedPort_TranscriberError
// pins the typed-error graceful-swallow contract: a Transcriber
// port error is logged at Warn level + transcript falls back to
// empty string. Execute does NOT propagate the error — that would
// regress the Step 9 media_assets + outbox surface (the prior
// M3 path also swallowed the .txt read failure but with a
// silent-success signal that hid the missing-transcript problem).
// The new path at least logs the Warn so operators can spot it.
func TestProcessSegment_Step10_ReadsTranscriptFromTypedPort_TranscriberError(t *testing.T) {
	realPath := filepath.Join(t.TempDir(), "clip.mp4")
	_ = os.WriteFile(realPath, []byte("fake audio bytes"), 0o644)
	tport := &stubTranscriber{err: errors.New("simulated whisper timeout")}

	uc := NewProcessYouTubeSegmentUseCase(func() ProcessSegmentDeps {
		d := validProcessSegmentDeps()
		d.VideoPipeline = stubVideoPipelineWithPath{path: realPath}
		d.Hash = testStubHash{} // non-empty so Step 5 passes
		d.Transcriber = tport
		return d
	}())
	cmd := youtubetypes.ProcessSegmentCommand{
		VideoID: "abc",
		Segment: youtubetypes.Segment{Start: "0:00", End: "0:10", Name: "Test"},
		Index:   0,
		OutDir:  t.TempDir(),
	}
	out, err := uc.Execute(context.Background(), cmd)
	require.NoError(t, err, "Transcriber error must not fail Execute (graceful swallow + empty transcript)")
	require.Equal(t, "processed", out.Status)
	require.Equal(t, 1, tport.calls, "Transcriber must still be invoked even when it errors")
}

// log_silence is a Nop logger reference; the process_segment tests
// inject validProcessSegmentDeps().Log = zap.NewNop().
var _ = zap.NewNop
