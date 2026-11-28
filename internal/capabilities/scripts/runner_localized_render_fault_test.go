package scriptgeneration

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/stretchr/testify/require"

	retry "github.com/Marcuss-ops/PipelineGen/pkg/retry"
)

// TestLocalizedRenderIPCFailureIsAttributedAndRetried is the controlled
// fault-injection AUGRENDER-03 was missing. It injects the same shape the
// voiceover render fan-out produces (ErrLocalizedRenderFailure joined with a
// transient IPC timeout) and asserts the durable run records the render stage
// and its own error code, then schedules the next attempt instead of
// attributing the failure to TTS.
func TestLocalizedRenderIPCFailureIsAttributedAndRetried(t *testing.T) {
	runner, repo, _, _, _, _ := newTestRunner()
	ctx := context.Background()
	runID := "run-augrender-03-render-ipc"
	require.NoError(t, repo.Create(ctx, &GenerationRun{
		ID:           runID,
		Status:       RunStatusRunning,
		CurrentStage: StageGeneratingVoiceovers,
	}))

	ipcCause := &retry.TransientInfrastructureError{Err: fmt.Errorf("localized render IPC timeout: %w", context.DeadlineExceeded)}
	runner.SetLocalizedRenderEnqueuer(&recordingLocalizedRenderEnqueuer{err: ipcCause})
	req := defaultTestRequest()
	req.Languages = []Language{"en"}
	req.Render = scriptpkg.VideoRenderSpec{Enabled: true}
	newResult := func() *GenerateResult {
		return &GenerateResult{
			Scenes: []Scene{{
				ID: "scene-render", Index: 0, DurationMS: 1000,
				Text:      map[Language]string{"en": "scene narration"},
				Voiceover: map[Language]AudioReference{"en": {ID: "vo-scene-render", Duration: 1}},
				Clip:      &ClipReference{ID: "clip-scene-render", Duration: 1},
			}},
			AudioOutput: AudioOutput{AudioMetrics: &AudioPipelineMetrics{}},
		}
	}
	exec := NewExecutionContext("job-augrender-03-render-ipc", "corr-augrender-03-render-ipc")
	failThroughRenderFanout := func() {
		var renderWg sync.WaitGroup
		renderErrors := make(chan error, 1)
		continued := runner.projectVoiceoverMetrics(
			ctx, runID, req, scriptpkg.ArtifactRoutingContext{}, exec,
			capabilityaudio.AudioModeChunkedVoiceover, newResult(), nil, 0,
			&renderWg, renderErrors, ExecutionStep{StepID: "voiceover-step"},
		)
		require.False(t, continued, "the real render fan-out error consumer must fail the run")
	}
	failThroughRenderFanout()

	failed, err := repo.Get(ctx, runID)
	require.NoError(t, err)
	require.Equal(t, RunStatusFailed, failed.Status)
	require.Equal(t, StageRenderingVideo, failed.FailedStage,
		"a localized render failure must not be attributed to GENERATING_VOICEOVERS")
	require.Equal(t, "LOCALIZED_RENDER_FAILED", failed.ErrorCode)
	require.Equal(t, 1, failed.AttemptCount)
	require.NotNil(t, failed.NextRetryAt, "a transient render IPC failure must schedule the next attempt")
	firstRetry := *failed.NextRetryAt

	// A second attempt fails the same way: the ledger increments the attempt
	// and moves the retry window forward; it never rewrites the stage to TTS.
	// Re-enter the phase as the durable worker does after the retry delay,
	// keeping the attempt counter from the first failure.
	require.NoError(t, repo.UpdateStage(ctx, runID, RunStatusRunning, StageGeneratingVoiceovers))
	failThroughRenderFanout()
	retried, err := repo.Get(ctx, runID)
	require.NoError(t, err)
	require.Equal(t, StageRenderingVideo, retried.FailedStage)
	require.Equal(t, "LOCALIZED_RENDER_FAILED", retried.ErrorCode)
	require.Equal(t, 2, retried.AttemptCount)
	require.NotNil(t, retried.NextRetryAt)
	require.True(t, retried.NextRetryAt.After(firstRetry),
		"retry window must move forward: first=%s second=%s", firstRetry, retried.NextRetryAt)
	require.WithinDuration(t, time.Now().Add(RetryDelay(1)), *retried.NextRetryAt, 2*time.Second)
}
