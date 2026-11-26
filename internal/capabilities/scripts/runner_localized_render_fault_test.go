package scriptgeneration

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

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
	renderErr := errors.Join(ErrLocalizedRenderFailure, ipcCause)
	runner.failRunWithRetry(ctx, runID, StageRenderingVideo, renderErr)

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
	runner.failRunWithRetry(ctx, runID, StageRenderingVideo, renderErr)
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
