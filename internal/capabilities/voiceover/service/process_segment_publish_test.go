// Package voiceover — process_segment_publish_test.go
//
// Publish-stage tests for the canonical timing bundle: publishTimingBundle
// must apply the required/best-effort/disabled policy after the audio
// upload, bind the artifact to the FINAL audio bytes via SHA-256, use
// verified Publisher file IDs for every link, and clean up its local
// staging files. required failures fail the segment with typed
// PipelineErrors; best-effort failures degrade the timing status while
// the audio stays completed; disabled preserves the legacy behavior.
package voiceover

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	files "github.com/Marcuss-ops/PipelineGen/internal/platform/filesystem"
)

// newPublishTimingUseCase builds a ProcessSegmentUseCase whose only live
// dependency in these tests is the Publisher; the other ports are inert
// stubs that satisfy the constructor's fail-fast gate.
func newPublishTimingUseCase(t *testing.T, pub VoiceoverPublisher) *ProcessSegmentUseCase {
	t.Helper()
	db := openProcessTestDB(t)
	return NewProcessSegmentUseCase(ProcessSegmentDeps{
		TTSProvider:         &stubProcessTTS{},
		Publisher:           pub,
		VoiceoverRepository: &stubProcessVoRepo{db: db},
		Finalizer:           &stubProcessFinalizer{cannedRes: &FinalizeResult{ID: "vo-timing-test"}},
		Logger:              zap.NewNop(),
	})
}

// timingTestFixture builds a hermetic publish context: a real audio file
// in a temp dir (so the artifact can bind via SHA-256) and a TTS output
// carrying three valid word boundaries within a 2.5s duration.
func timingTestFixture(t *testing.T) (dir, audioPath string, tts *TTSOutput, cmd *ProcessSegmentCommand) {
	t.Helper()
	dir = t.TempDir()
	audioPath = filepath.Join(dir, "scene-0-it.mp3")
	require.NoError(t, os.WriteFile(audioPath, []byte("fake-mp3-bytes"), 0o644))

	tts = &TTSOutput{
		LocalPath:    audioPath,
		Voice:        "it-IT-DiegoNeural",
		Provider:     "edge_tts",
		BoundaryMode: audio.BoundaryWord,
		Duration:     2500 * time.Millisecond,
		WordBoundaries: []RawSpeechBoundary{
			{Text: "Il", StartUS: 0, EndUS: 125_000},
			{Text: "celebre", StartUS: 125_000, EndUS: 487_000},
			{Text: "incontro.", StartUS: 487_000, EndUS: 900_000},
		},
	}
	cmd = &ProcessSegmentCommand{
		ID:        "vo-timing-test",
		RequestID: "req-timing-test",
		Text:      "Il celebre incontro.",
		TextHash:  "hash-timing-001",
		Language:  "it",
		Voice:     "it-IT-DiegoNeural",
		Filename:  "scene-0-it.mp3",
		Project:   "progetto-storia",
		Dest:      &ResolvedDestination{FolderID: "folder-timing", FolderPath: dir},
	}
	return dir, audioPath, tts, cmd
}

func TestPublishTimingBundle_Disabled_PreservesLegacyBehavior(t *testing.T) {
	_, _, tts, cmd := timingTestFixture(t)
	cmd.Timing = &audio.TimingRequest{Mode: audio.TimingDisabled}
	pub := &stubProcessPublisher{fileID: "drive-timing"}
	uc := newPublishTimingUseCase(t, pub)

	res, err := uc.publishTimingBundle(context.Background(), cmd, &VoiceoverItemResult{}, tts, nil, tts.LocalPath, zap.NewNop())

	require.NoError(t, err)
	assert.Nil(t, res, "disabled policy must produce no timing result (legacy behavior)")
	assert.Empty(t, pub.published, "disabled policy must publish nothing beyond the audio")
}

func TestPublishTimingBundle_Required_NoBoundaries_FailsClosed(t *testing.T) {
	_, _, tts, cmd := timingTestFixture(t)
	cmd.Timing = &audio.TimingRequest{Mode: audio.TimingRequired, BoundaryMode: audio.BoundaryWord, Formats: []audio.TimingFormat{audio.TimingJSON}}
	tts.WordBoundaries = nil // provider produced no timing
	pub := &stubProcessPublisher{fileID: "drive-timing"}
	uc := newPublishTimingUseCase(t, pub)

	res, err := uc.publishTimingBundle(context.Background(), cmd, &VoiceoverItemResult{}, tts, nil, tts.LocalPath, zap.NewNop())

	require.Error(t, err)
	assert.Nil(t, res)
	var pipelineErr *PipelineError
	require.ErrorAs(t, err, &pipelineErr)
	assert.Equal(t, FailureTimingUnavailable, pipelineErr.FailureCode())
	assert.Equal(t, StageTiming, pipelineErr.Stage)
	assert.False(t, pipelineErr.Retryable, "missing boundaries under required timing is permanent")
	assert.Contains(t, err.Error(), "VOICEOVER_TIMING_UNAVAILABLE")
	assert.Empty(t, pub.published, "no timing files may be published without boundaries")
}

func TestPublishTimingBundle_BestEffort_NoBoundaries_Unavailable(t *testing.T) {
	_, _, tts, cmd := timingTestFixture(t)
	cmd.Timing = &audio.TimingRequest{Mode: audio.TimingBestEffort, BoundaryMode: audio.BoundaryWord, Formats: []audio.TimingFormat{audio.TimingJSON}}
	tts.WordBoundaries = nil
	pub := &stubProcessPublisher{fileID: "drive-timing"}
	uc := newPublishTimingUseCase(t, pub)

	res, err := uc.publishTimingBundle(context.Background(), cmd, &VoiceoverItemResult{}, tts, nil, tts.LocalPath, zap.NewNop())

	require.NoError(t, err, "best-effort must not fail the segment when boundaries are absent")
	require.NotNil(t, res)
	assert.Equal(t, TimingStatusUnavailable, res.Status, "no boundaries under best-effort must be explicitly unavailable, not completed")
	assert.Empty(t, pub.published)
}

func TestPublishTimingBundle_Required_RemoveSilence_Rejected(t *testing.T) {
	_, _, tts, cmd := timingTestFixture(t)
	cmd.Timing = &audio.TimingRequest{Mode: audio.TimingRequired, BoundaryMode: audio.BoundaryWord, Formats: []audio.TimingFormat{audio.TimingJSON}}
	cmd.RemoveSilence = true
	pub := &stubProcessPublisher{fileID: "drive-timing"}
	uc := newPublishTimingUseCase(t, pub)

	res, err := uc.publishTimingBundle(context.Background(), cmd, &VoiceoverItemResult{}, tts, nil, tts.LocalPath, zap.NewNop())

	require.Error(t, err)
	assert.Nil(t, res)
	var pipelineErr *PipelineError
	require.ErrorAs(t, err, &pipelineErr)
	assert.Equal(t, FailureTimingIncompatible, pipelineErr.FailureCode())
	assert.False(t, pipelineErr.Retryable)
	assert.Empty(t, pub.published, "no fake timestamps may be published after silence removal")
}

func TestPublishTimingBundle_BestEffort_RemoveSilence_Unavailable(t *testing.T) {
	_, _, tts, cmd := timingTestFixture(t)
	cmd.Timing = &audio.TimingRequest{Mode: audio.TimingBestEffort, BoundaryMode: audio.BoundaryWord, Formats: []audio.TimingFormat{audio.TimingJSON}}
	cmd.RemoveSilence = true
	pub := &stubProcessPublisher{fileID: "drive-timing"}
	uc := newPublishTimingUseCase(t, pub)

	res, err := uc.publishTimingBundle(context.Background(), cmd, &VoiceoverItemResult{}, tts, nil, tts.LocalPath, zap.NewNop())

	require.NoError(t, err)
	require.NotNil(t, res)
	assert.Equal(t, TimingStatusUnavailable, res.Status, "best-effort + silence removal must not fabricate timestamps")
	assert.Empty(t, pub.published)
}

func TestPublishTimingBundle_HappyPath_PublishesJSONAndSRT(t *testing.T) {
	dir, audioPath, tts, cmd := timingTestFixture(t)
	cmd.Timing = &audio.TimingRequest{
		Mode:         audio.TimingRequired,
		BoundaryMode: audio.BoundaryWord,
		Formats:      []audio.TimingFormat{audio.TimingJSON, audio.TimingSRT},
	}
	pub := &stubProcessPublisher{fileID: "drive-timing-file"}
	uc := newPublishTimingUseCase(t, pub)
	out := &VoiceoverItemResult{Voice: "it-IT-DiegoNeural", LocalPath: audioPath}

	res, err := uc.publishTimingBundle(context.Background(), cmd, out, tts, nil, audioPath, zap.NewNop())

	require.NoError(t, err)
	require.NotNil(t, res)
	assert.Equal(t, TimingStatusCompleted, res.Status)

	// Verified storage links: every link derives from the Publisher's
	// real file IDs (stub returns drive-timing-file).
	assert.Equal(t, CanonicalDriveWebURL("drive-timing-file"), res.JSONLink)
	assert.Equal(t, CanonicalDriveWebURL("drive-timing-file"), res.SRTLink)
	assert.Empty(t, res.VTTLink, "VTT must not be published when the policy only requests json+srt")

	// Artifact surface bound to the exact text + final audio bytes.
	assert.Equal(t, "word", res.BoundaryMode)
	assert.Equal(t, 3, res.WordCount)
	assert.Equal(t, int64(2_500_000), res.DurationUS)
	assert.Equal(t, files.SHA256String(cmd.Text), res.TextSHA256)
	assert.Equal(t, files.SHA256String("fake-mp3-bytes"), res.AudioSHA256)

	// Two projection uploads with canonical filenames + distinct idem keys.
	// The projections are published in non-deterministic order, so assert
	// the filename set and per-projection metadata independently.
	require.Len(t, pub.published, 2)
	require.ElementsMatch(t,
		[]string{"scene-0-it-timing.json", "scene-0-it.srt"},
		[]string{pub.published[0].Filename, pub.published[1].Filename})
	for _, p := range pub.published {
		assert.Equal(t, "it", p.Language)
		assert.Equal(t, "progetto-storia", p.Project)
	}
	assert.NotEqual(t, pub.published[0].IdempotencyKey, pub.published[1].IdempotencyKey,
		"each timing projection must have its own idempotency key")

	// Local staging files must be cleaned up after publish.
	_, err = os.Stat(filepath.Join(dir, "scene-0-it-timing.json"))
	assert.Error(t, err, "timing staging file must be removed after publish")
	_, err = os.Stat(filepath.Join(dir, "scene-0-it.srt"))
	assert.Error(t, err, "SRT staging file must be removed after publish")
}

func TestPublishTimingBundle_Required_PublishFailure_Fails(t *testing.T) {
	_, audioPath, tts, cmd := timingTestFixture(t)
	cmd.Timing = &audio.TimingRequest{Mode: audio.TimingRequired, BoundaryMode: audio.BoundaryWord, Formats: []audio.TimingFormat{audio.TimingJSON}}
	pub := &stubFailingPublisher{err: errors.New("drive upload failed")}
	uc := newPublishTimingUseCase(t, pub)
	out := &VoiceoverItemResult{Voice: "it-IT-DiegoNeural", LocalPath: audioPath}

	res, err := uc.publishTimingBundle(context.Background(), cmd, out, tts, nil, audioPath, zap.NewNop())

	require.Error(t, err)
	assert.Nil(t, res)
	var pipelineErr *PipelineError
	require.ErrorAs(t, err, &pipelineErr)
	assert.Equal(t, FailureTimingPublish, pipelineErr.FailureCode())
	assert.Equal(t, StageTiming, pipelineErr.Stage)
	assert.False(t, pipelineErr.Retryable)
}

func TestPublishTimingBundle_BestEffort_PublishFailure_VisibleButNotFatal(t *testing.T) {
	_, audioPath, tts, cmd := timingTestFixture(t)
	cmd.Timing = &audio.TimingRequest{Mode: audio.TimingBestEffort, BoundaryMode: audio.BoundaryWord, Formats: []audio.TimingFormat{audio.TimingJSON}}
	pub := &stubFailingPublisher{err: errors.New("drive upload failed")}
	uc := newPublishTimingUseCase(t, pub)
	out := &VoiceoverItemResult{Voice: "it-IT-DiegoNeural", LocalPath: audioPath}

	res, err := uc.publishTimingBundle(context.Background(), cmd, out, tts, nil, audioPath, zap.NewNop())

	require.NoError(t, err, "best-effort timing publish failure must not fail the segment")
	require.NotNil(t, res)
	assert.Equal(t, TimingStatusFailed, res.Status, "best-effort timing failure must be explicitly visible")
	assert.Empty(t, res.JSONLink)
}

// TestPublishTimingBundle_Required_RemoveSilence_WithEditMap_Completes
// pins the DEFINITIVE remap path (design step 6): when the post-
// processor reports an edit map + final duration, timing required +
// remove_silence produces a completed bundle whose word timestamps
// refer to the CLEANED audio and whose duration matches the final file.
func TestPublishTimingBundle_Required_RemoveSilence_WithEditMap_Completes(t *testing.T) {
	dir, audioPath, tts, cmd := timingTestFixture(t)
	cleanedPath := filepath.Join(dir, "scene-0-it.cleaned.mp3")
	require.NoError(t, os.WriteFile(cleanedPath, []byte("cleaned-fake-mp3"), 0o644))

	cmd.Timing = &audio.TimingRequest{Mode: audio.TimingRequired, BoundaryMode: audio.BoundaryWord, Formats: []audio.TimingFormat{audio.TimingJSON}}
	cmd.RemoveSilence = true
	// 3s original audio; a 1s silence at [1s,2s) is removed → 2s final.
	tts.Duration = 3 * time.Second
	tts.WordBoundaries = []RawSpeechBoundary{
		{Text: "Il", StartUS: 0, EndUS: 500_000},
		{Text: "celebre", StartUS: 500_000, EndUS: 900_000},
		{Text: "incontro.", StartUS: 2_100_000, EndUS: 2_500_000},
	}
	post := &AudioPostOutput{
		CleanedPath: cleanedPath,
		DurationUS:  2_000_000,
		EditMap: []audio.AudioEdit{
			{SourceStartUS: 1_000_000, SourceEndUS: 2_000_000, OutputStartUS: 1_000_000, OutputEndUS: 1_000_000},
		},
	}
	pub := &stubProcessPublisher{fileID: "drive-remap"}
	uc := newPublishTimingUseCase(t, pub)
	out := &VoiceoverItemResult{Voice: "it-IT-DiegoNeural", LocalPath: audioPath, CleanedPath: cleanedPath}

	res, err := uc.publishTimingBundle(context.Background(), cmd, out, tts, post, cleanedPath, zap.NewNop())

	require.NoError(t, err, "required timing + remove_silence with an edit map must succeed")
	require.NotNil(t, res)
	assert.Equal(t, TimingStatusCompleted, res.Status)
	assert.NotEmpty(t, res.JSONLink)
	// The artifact must describe the CLEANED audio: final duration 2s and
	// the post-silence word shifted left by the removed 1s.
	assert.Equal(t, int64(2_000_000), res.DurationUS)
	assert.Equal(t, 3, res.WordCount)
	assert.Equal(t, files.SHA256String("cleaned-fake-mp3"), res.AudioSHA256,
		"the artifact must bind to the CLEANED audio bytes")

	// The timing.json projection is published with the canonical filename
	// and the cleaned audio's duration (2s) baked into the artifact.
	require.Len(t, pub.published, 1)
	assert.Equal(t, "scene-0-it-timing.json", pub.published[0].Filename)
	assert.Equal(t, "it", pub.published[0].Language)
	assert.Equal(t, "progetto-storia", pub.published[0].Project)
}

// TestProcessSegmentUseCase_Execute_RequiredTimingNoBoundariesFailsJob
// pins the E2E negative contract at the Execute boundary: Edge returns
// audio but zero word boundaries with timing.mode=required → JOB FAILED
// with the typed VOICEOVER_TIMING_UNAVAILABLE code, and the finalizer is
// never reached.
func TestProcessSegmentUseCase_Execute_RequiredTimingNoBoundariesFailsJob(t *testing.T) {
	db := openProcessTestDB(t)
	tts := &stubProcessTTS{
		cannedOut: TTSOutput{
			LocalPath:     "/tmp/vo/required-no-boundaries.mp3",
			Voice:         "en-US-RogerNeural",
			LegacyFileMD5: "required-nb-hash",
		},
	}
	pub := &stubProcessPublisher{fileID: "drive-required-nb"}
	finalizer := &stubProcessFinalizer{cannedRes: &FinalizeResult{ID: "must-not-finalize"}}
	uc := NewProcessSegmentUseCase(ProcessSegmentDeps{
		TTSProvider:         tts,
		Publisher:           pub,
		VoiceoverRepository: &stubProcessVoRepo{db: db},
		Finalizer:           finalizer,
		Logger:              zap.NewNop(),
	})

	out, err := uc.Execute(context.Background(), &ProcessSegmentCommand{
		ID:       "vo-required-nb",
		Language: "en",
		Text:     "required timing text",
		Filename: "required-nb.mp3",
		Timing: &audio.TimingRequest{
			Mode:         audio.TimingRequired,
			BoundaryMode: audio.BoundaryWord,
			Formats:      []audio.TimingFormat{audio.TimingJSON},
		},
		Dest: &ResolvedDestination{FolderID: "folder", FolderPath: t.TempDir()},
	})

	require.Error(t, err)
	require.NotNil(t, out)
	assert.Equal(t, StatusFailed, out.Status)
	assert.Equal(t, string(FailureTimingUnavailable), out.ErrorCode,
		"required timing with no boundaries must surface VOICEOVER_TIMING_UNAVAILABLE")
	var pipelineErr *PipelineError
	require.ErrorAs(t, err, &pipelineErr)
	assert.Equal(t, FailureTimingUnavailable, pipelineErr.FailureCode())
	assert.Equal(t, StageTiming, pipelineErr.Stage)
	assert.False(t, pipelineErr.Retryable)
	assert.Empty(t, finalizer.calls, "timing failure must fail before DB finalization")

	// The audio upload already happened (Stage 3 uploads audio first);
	// the failure is confined to the timing bundle.
	require.Len(t, pub.published, 1)
	assert.Equal(t, "required-nb.mp3", pub.published[0].Filename)
}

// TestPublishTimingBundle_Moments_AnchorsAnnotationsDeterministically pins
// the extraction → PhraseLocator → moments connection: the LLM contributes
// only (kind, value) strings; the timing stage derives exact timestamps
// from the canonical word timing. Not-found values are skipped, never
// fabricated, and the audio still completes.
func TestPublishTimingBundle_Moments_AnchorsAnnotationsDeterministically(t *testing.T) {
	_, audioPath, tts, cmd := timingTestFixture(t)
	cmd.Timing = &audio.TimingRequest{Mode: audio.TimingRequired, BoundaryMode: audio.BoundaryWord, Formats: []audio.TimingFormat{audio.TimingJSON}}
	cmd.Moments = []audio.MomentQuery{
		{Kind: audio.MomentEntity, Value: "celebre"},
		{Kind: audio.MomentKeyword, Value: "Mussolini"}, // not present — skipped
	}
	pub := &stubProcessPublisher{fileID: "drive-moments"}
	uc := newPublishTimingUseCase(t, pub)
	out := &VoiceoverItemResult{Voice: "it-IT-DiegoNeural", LocalPath: audioPath}

	res, err := uc.publishTimingBundle(context.Background(), cmd, out, tts, nil, audioPath, zap.NewNop())

	require.NoError(t, err)
	require.NotNil(t, res)
	require.Len(t, res.Moments, 1)
	got := res.Moments[0]
	assert.Equal(t, audio.MomentEntity, got.Kind)
	assert.Equal(t, "celebre", got.Value)
	assert.Equal(t, 1, got.WordStart)
	assert.Equal(t, int64(125_000), got.StartUS)
	assert.Equal(t, int64(487_000), got.EndUS)
}
