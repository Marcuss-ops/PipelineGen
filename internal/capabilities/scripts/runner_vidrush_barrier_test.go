// Package scriptgeneration — runner_vidrush_barrier_test.go certifies the
// runner's final VidRush barrier contract: when a VidRushBarrier is wired, the
// runner awaits it after scene-text generation and fails the run closed when
// the barrier errors; when no barrier is wired the run still completes.
package scriptgeneration

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// recordingVidRushBarrier records the run IDs awaited and returns a canned
// result set or error.
type recordingVidRushBarrier struct {
	mu        sync.Mutex
	runIDs    []string
	block     chan struct{}
	resultErr error
}

func newRecordingVidRushBarrier() *recordingVidRushBarrier {
	return &recordingVidRushBarrier{}
}

func (b *recordingVidRushBarrier) WaitForVidRush(_ context.Context, runID string) ([]scriptpkg.VidRushSegmentResult, error) {
	b.mu.Lock()
	b.runIDs = append(b.runIDs, runID)
	block := b.block
	resultErr := b.resultErr
	b.mu.Unlock()
	if block != nil {
		<-block
	}
	if resultErr != nil {
		return nil, resultErr
	}
	return []scriptpkg.VidRushSegmentResult{{SceneID: "scene-0", Position: 0}}, nil
}

func (b *recordingVidRushBarrier) awaitedRunIDs() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.runIDs...)
}

func TestRunner_AwaitsVidRushBarrierAfterSceneText(t *testing.T) {
	runner, repo, _, _, _, _, _ := newTestRunner()
	barrier := newRecordingVidRushBarrier()
	runner.SetVidRushBarrier(barrier)

	req := defaultTestRequest()
	runID := "run-barrier-001"
	require.NoError(t, repo.Create(context.Background(), &GenerationRun{
		ID: runID, Request: req, Status: RunStatusPending, CurrentStage: StageNormalizing,
	}))

	runner.Execute(context.Background(), runID, req)
	final := awaitCompletion(t, repo, runID, 5*time.Second)
	require.NotNil(t, final)

	assert.Equal(t, RunStatusCompleted, final.Status, "run should complete when the barrier succeeds")
	assert.Equal(t, []string{runID}, barrier.awaitedRunIDs(), "the barrier must be awaited exactly once for the run")
}

func TestRunner_BarrierErrorFailsRunClosed(t *testing.T) {
	runner, repo, _, _, _, _, _ := newTestRunner()
	barrier := newRecordingVidRushBarrier()
	barrier.resultErr = assert.AnError
	runner.SetVidRushBarrier(barrier)

	req := defaultTestRequest()
	runID := "run-barrier-fail-001"
	require.NoError(t, repo.Create(context.Background(), &GenerationRun{
		ID: runID, Request: req, Status: RunStatusPending, CurrentStage: StageNormalizing,
	}))

	runner.Execute(context.Background(), runID, req)
	final := awaitCompletion(t, repo, runID, 5*time.Second)
	require.NotNil(t, final)

	assert.Equal(t, RunStatusFailed, final.Status, "a barrier error must fail the run closed")
	assert.Equal(t, StageGeneratingSceneText, final.FailedStage, "barrier failure is attributed to the scene-text stage")
}

func TestRunner_BarrierBlocksUntilEnrichmentDone(t *testing.T) {
	runner, repo, _, _, _, _, _ := newTestRunner()
	barrier := newRecordingVidRushBarrier()
	barrier.block = make(chan struct{})
	runner.SetVidRushBarrier(barrier)

	req := defaultTestRequest()
	runID := "run-barrier-block-001"
	require.NoError(t, repo.Create(context.Background(), &GenerationRun{
		ID: runID, Request: req, Status: RunStatusPending, CurrentStage: StageNormalizing,
	}))

	// Execute in a goroutine so the test can observe the blocked barrier.
	go runner.Execute(context.Background(), runID, req)

	// The run must not complete while the barrier is still waiting.
	time.Sleep(50 * time.Millisecond)
	run, err := repo.Get(context.Background(), runID)
	require.NoError(t, err)
	require.NotEqual(t, RunStatusCompleted, run.Status, "run must not complete while the barrier blocks")

	close(barrier.block)
	final := awaitCompletion(t, repo, runID, 5*time.Second)
	require.NotNil(t, final)
	assert.Equal(t, RunStatusCompleted, final.Status)
}

func TestRunner_NoBarrierIsSafeNoOp(t *testing.T) {
	runner, repo, _, _, _, _, _ := newTestRunner()
	// No SetVidRushBarrier call.

	req := defaultTestRequest()
	runID := "run-no-barrier-001"
	require.NoError(t, repo.Create(context.Background(), &GenerationRun{
		ID: runID, Request: req, Status: RunStatusPending, CurrentStage: StageNormalizing,
	}))

	runner.Execute(context.Background(), runID, req)
	final := awaitCompletion(t, repo, runID, 5*time.Second)
	require.NotNil(t, final)
	assert.Equal(t, RunStatusCompleted, final.Status, "a nil barrier must be a safe no-op")
}
