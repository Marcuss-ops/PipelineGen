// Package voiceover — process_segment_publish_test.go
//
// Publish-stage tests for the canonical timing bundle: publishTimingBundle
// must apply the required/best-effort/disabled policy, bind the artifact to
// the FINAL audio bytes via SHA-256, persist the certified local projections
// (they are the durable SSOT), and use verified Publisher file IDs for every
// remote link when a publisher is wired. required failures fail the segment
// with typed
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
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
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
		Enrichment: ProcessSegmentEnrichmentDeps{
			PhraseAligner: stubPublishPhraseAligner{},
		},
		Logger: zap.NewNop(),
	})
}

// stubPublishPhraseAligner mirrors the one alignment the moments fixture
// needs: "celebre" is present in the word timing, "Mussolini" is absent and
// must be skipped (allow_missing), never fabricated.
type stubPublishPhraseAligner struct{}

func (stubPublishPhraseAligner) AlignPhrases(_ context.Context, request audio.PhraseAlignmentRequest) ([]audio.LocatedPhrase, error) {
	var out []audio.LocatedPhrase
	for _, phrase := range request.Phrases {
		if phrase == "celebre" {
			out = append(out, audio.LocatedPhrase{
				Text: phrase, Occurrence: 1, WordStart: 1, WordEnd: 1, StartUS: 125_000, EndUS: 487_000,
			})
		}
	}
	return out, nil
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
	assert.Equal(t, digest.SHA256String(cmd.Text), res.TextSHA256)
	assert.Equal(t, digest.SHA256String("fake-mp3-bytes"), res.AudioSHA256)

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

	// Certified local projections are durable: the local path is the SSOT
	// that retries and the cross-run cache rehydrate from.
	jsonPath := filepath.Join(dir, "scene-0-it-timing.json")
	srtPath := filepath.Join(dir, "scene-0-it.srt")
	assert.FileExists(t, jsonPath, "timing JSON SSOT must persist locally")
	assert.FileExists(t, srtPath, "timing SRT projection must persist locally")
	assert.Equal(t, jsonPath, res.JSONPath)
	assert.Equal(t, srtPath, res.SRTPath)
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
	assert.Equal(t, digest.SHA256String("cleaned-fake-mp3"), res.AudioSHA256,
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
			ContentSHA256: "required-nb-hash",
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

func TestTimingInputBlocker(t *testing.T) {
	withWords := func() *TTSOutput {
		return &TTSOutput{WordBoundaries: []RawSpeechBoundary{{Text: "x", StartUS: 0, EndUS: 100}}}
	}
	const incompatibleMessage = "timing required + remove_silence: the audio post-processor reported no edit map, so accurate timestamps are impossible"

	tests := []struct {
		name          string
		tts           *TTSOutput
		post          *AudioPostOutput
		removeSilence bool
		wantNil       bool
		wantCode      FailureCode
		wantMessage   string
	}{
		{name: "usable without silence removal", tts: withWords(), wantNil: true},
		{name: "usable with edit map duration", tts: withWords(), post: &AudioPostOutput{DurationUS: 1000}, removeSilence: true, wantNil: true},
		{name: "post output ignored when silence removal is off", tts: withWords(), post: &AudioPostOutput{}, removeSilence: false, wantNil: true},
		{
			name:        "no word boundaries",
			tts:         &TTSOutput{},
			wantCode:    FailureTimingUnavailable,
			wantMessage: "VOICEOVER_TIMING_UNAVAILABLE: TTS produced no word boundaries for required timing",
		},
		{
			name: "silence removal without post output", tts: withWords(), removeSilence: true,
			wantCode: FailureTimingIncompatible, wantMessage: incompatibleMessage,
		},
		{
			name: "silence removal with zero duration", tts: withWords(), post: &AudioPostOutput{}, removeSilence: true,
			wantCode: FailureTimingIncompatible, wantMessage: incompatibleMessage,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := timingInputBlocker(tt.tts, tt.post, tt.removeSilence)
			if tt.wantNil {
				require.Nil(t, got)
				return
			}
			require.NotNil(t, got)
			assert.Equal(t, tt.wantCode, got.code)
			assert.Equal(t, tt.wantMessage, got.message)
		})
	}
}

func TestBuildTimingWordTimeline_RemapsOntoCleanedTimeline(t *testing.T) {
	tts := &TTSOutput{
		Duration: 3 * time.Second,
		WordBoundaries: []RawSpeechBoundary{
			{Text: "Il", StartUS: 0, EndUS: 500_000},
			{Text: "celebre", StartUS: 500_000, EndUS: 900_000},
			{Text: "incontro.", StartUS: 2_100_000, EndUS: 2_500_000},
		},
	}
	post := &AudioPostOutput{
		DurationUS: 2_000_000,
		EditMap: []audio.AudioEdit{
			{SourceStartUS: 1_000_000, SourceEndUS: 2_000_000, OutputStartUS: 1_000_000, OutputEndUS: 1_000_000},
		},
	}

	timeline, err := buildTimingWordTimeline(tts, post, true)

	require.NoError(t, err)
	assert.Equal(t, int64(2_000_000), timeline.DurationUS, "the cleaned duration is authoritative")
	require.Len(t, timeline.Words, 3)
	assert.Equal(t, 2, timeline.Words[2].Index, "remap reindexes the surviving words contiguously")
	assert.Equal(t, int64(1_100_000), timeline.Words[2].StartUS, "the removed second must shift the trailing word left")
	assert.Zero(t, timeline.DroppedWords)
	assert.Zero(t, timeline.ClampedOverrunUS)
}

func TestBuildTimingWordTimeline_IgnoresEditMapWithoutSilenceRemoval(t *testing.T) {
	tts := &TTSOutput{
		Duration:       3 * time.Second,
		WordBoundaries: []RawSpeechBoundary{{Text: "x", StartUS: 2_100_000, EndUS: 2_500_000}},
	}
	post := &AudioPostOutput{
		DurationUS: 2_000_000,
		EditMap: []audio.AudioEdit{
			{SourceStartUS: 1_000_000, SourceEndUS: 2_000_000, OutputStartUS: 1_000_000, OutputEndUS: 1_000_000},
		},
	}

	timeline, err := buildTimingWordTimeline(tts, post, false)

	require.NoError(t, err)
	assert.Equal(t, int64(3_000_000), timeline.DurationUS)
	assert.Equal(t, int64(2_100_000), timeline.Words[0].StartUS, "the provider timeline stays authoritative without silence removal")
}

func TestPersistTimingProjections_WritesCertifiedLocalSSOT(t *testing.T) {
	dir := t.TempDir()
	artifactJSON := []byte(`{"version":1}`)
	projections := map[audio.TimingFormat][]byte{
		audio.TimingJSON: artifactJSON,
		audio.TimingSRT:  []byte("1\n00:00:00,000 --> 00:00:01,000\nx\n"),
	}

	local, err := persistTimingProjections(dir, "scene-0-it", artifactJSON, projections)

	require.NoError(t, err)
	assert.Equal(t, filepath.Join(dir, "scene-0-it-timing.json"), local.JSONPath)
	assert.Equal(t, filepath.Join(dir, "scene-0-it.srt"), local.SRTPath)
	assert.Empty(t, local.VTTPath)
	assert.FileExists(t, local.JSONPath)
	assert.FileExists(t, local.SRTPath)
	assert.NoFileExists(t, filepath.Join(dir, "scene-0-it.vtt"))
	got, err := os.ReadFile(local.JSONPath)
	require.NoError(t, err)
	assert.JSONEq(t, string(artifactJSON), string(got))
}

func TestInjectTimingMetadata(t *testing.T) {
	meta := map[string]any{}
	injectTimingMetadata(meta, nil)
	assert.Empty(t, meta, "nil timing result must not touch the envelope")

	meta["timing_status"] = "stale"
	injectTimingMetadata(meta, &VoiceoverTimingResult{Status: TimingStatusUnavailable})
	assert.Equal(t, TimingStatusUnavailable, meta["timing_status"])
	assert.Equal(t, "", meta["timing_boundary_mode"])
	assert.Equal(t, 0, meta["timing_word_count"])
	assert.Equal(t, int64(0), meta["timing_duration_us"])
	assert.NotContains(t, meta, "timing_json_link")
	assert.NotContains(t, meta, "audio_sha256")

	injectTimingMetadata(meta, &VoiceoverTimingResult{
		Status: TimingStatusCompleted, JSONLink: "https://json.example", SRTPath: "/tmp/a.srt",
		AudioSHA256: "sha", BoundaryMode: "word", WordCount: 3, DurationUS: 2500,
	})
	assert.Equal(t, "https://json.example", meta["timing_json_link"])
	assert.Equal(t, "/tmp/a.srt", meta["timing_srt_path"])
	assert.Equal(t, "sha", meta["audio_sha256"])
	assert.Equal(t, 3, meta["timing_word_count"])
	assert.Equal(t, int64(2500), meta["timing_duration_us"])
}

func TestMarshalVoiceoverMetadata_TypedSerializationFailure(t *testing.T) {
	_, err := marshalVoiceoverMetadata(map[string]any{"unsupported": func() {}})

	require.Error(t, err)
	var pipelineErr *PipelineError
	require.ErrorAs(t, err, &pipelineErr)
	assert.Equal(t, StageMetadata, pipelineErr.Stage)
	assert.Equal(t, FailureMetadataSerialization, pipelineErr.FailureCode())
	assert.False(t, pipelineErr.Retryable)
}

func TestPersistTimingProjections_KeepsUnrequestedJSONLocalOnly(t *testing.T) {
	dir := t.TempDir()
	artifactJSON := []byte(`{"version":1}`)
	projections := map[audio.TimingFormat][]byte{audio.TimingSRT: []byte("srt")}

	local, err := persistTimingProjections(dir, "scene-0-it", artifactJSON, projections)

	require.NoError(t, err)
	assert.FileExists(t, local.JSONPath, "the JSON SSOT is always persisted locally")
	assert.NotContains(t, local.Paths, audio.TimingJSON, "an unrequested JSON format must not be uploaded")
	assert.Len(t, local.Paths, 1)
	assert.Equal(t, filepath.Join(dir, "scene-0-it.srt"), local.Paths[audio.TimingSRT])
}
