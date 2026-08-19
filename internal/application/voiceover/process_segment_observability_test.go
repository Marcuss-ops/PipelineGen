package voiceover

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
)

// TestProcessSegmentUseCase_RecordsCanonicalStages pins the convergence of
// the per-item voiceover stage timers onto the canonical kernel Run: each of
// tts / audio_post / publish / finalize now flows through MeasureStage, so it
// produces exactly one StageReport on the run bound to ctx — the SSOT that
// SQLiteRecorder persists to run_stage_observations. stageLog remains a
// structured lifecycle log only and never measures on its own.
func TestProcessSegmentUseCase_RecordsCanonicalStages(t *testing.T) {
	db := openProcessTestDB(t)

	run := kernobs.NewRunObserver(nil).StartRun(context.Background(), kernobs.RunInfo{
		AttemptID: "attempt-records-stages",
	})
	ctx := kernobs.WithRun(context.Background(), run)

	uc := NewProcessSegmentUseCase(ProcessSegmentDeps{
		TTSProvider: &stubProcessTTS{cannedOut: TTSOutput{
			LocalPath: "/tmp/vo/stages.mp3",
			Voice:     "en-US-RogerNeural",
		}},
		AudioPostProcessor: &stubProcessAudioPost{cannedOut: AudioPostOutput{
			CleanedPath: "/tmp/vo/stages-clean.mp3",
			DurationUS:  1_000_000,
		}},
		Publisher:           &stubProcessPublisher{fileID: "drive-stages"},
		VoiceoverRepository: &stubProcessVoRepo{db: db},
		Finalizer:           &stubProcessFinalizer{cannedRes: &FinalizeResult{}},
		Logger:              zap.NewNop(),
	})

	cmd := &ProcessSegmentCommand{
		ID:            "vo-id-stages",
		RequestID:     "req-stages",
		TextHash:      "hash-stages",
		Text:          "canonical stage recording",
		Language:      "en",
		Voice:         "en-US-RogerNeural",
		Filename:      "stages.mp3",
		RemoveSilence: true, // exercise the audio_post stage too
		Dest:          &ResolvedDestination{FolderID: "folder-stages", FolderPath: "/tmp/vo"},
	}

	out, err := uc.Execute(ctx, cmd)
	require.NoError(t, err, "Execute must succeed in the happy path")
	require.NotNil(t, out)
	assert.Equal(t, StatusCompleted, out.Status)

	report := run.Report()
	require.NotNil(t, report)

	stageNames := map[string]kernobs.StageReport{}
	for _, st := range report.Stages {
		stageNames[st.Name] = st
	}
	for _, want := range []string{"tts", "audio_post", "publish", "finalize"} {
		st, ok := stageNames[want]
		require.True(t, ok, "canonical stage %q must be recorded on the run", want)
		assert.Equal(t, kernobs.StageStatusCompleted, st.Status,
			"canonical stage %q must record completed status", want)
	}
}
