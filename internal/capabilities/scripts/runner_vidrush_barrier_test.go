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

// TestRunner_BarrierEntitiesProjectedOntoResult certifies the durable entity
// surfacing fix: the runner must NOT discard the barrier's fenced per-scene
// enrichment results. When the barrier returns segments with extracted
// entities, the completed run's result exposes the typed aggregate
// (persons/places/concepts) on the durable surface.
func TestRunner_BarrierEntitiesProjectedOntoResult(t *testing.T) {
	runner, repo, _, _, _, _, _ := newTestRunner()
	barrier := newRecordingVidRushBarrier()
	barrier.resultErr = nil
	runner.SetVidRushBarrier(barrier)
	// Override the canned barrier result with a real entity-bearing segment.
	runner.vidRushBarrier = barrierFunc(func(_ context.Context, runID string) ([]scriptpkg.VidRushSegmentResult, error) {
		return []scriptpkg.VidRushSegmentResult{
			{SceneID: "scene-0", Position: 0, Insights: scriptpkg.SegmentInsights{Entities: []scriptpkg.ExtractedEntity{
				{Value: "Jackie Chan", Type: "PERSON", Confidence: 0.95},
				{Value: "Hong Kong", Type: "LOCATION", Confidence: 0.9},
				{Value: "martial arts", Type: "CONCEPT", Confidence: 0.85},
			}}},
		}, nil
	})

	req := defaultTestRequest()
	runID := "run-barrier-entities-001"
	require.NoError(t, repo.Create(context.Background(), &GenerationRun{
		ID: runID, Request: req, Status: RunStatusPending, CurrentStage: StageNormalizing,
	}))

	runner.Execute(context.Background(), runID, req)
	final := awaitCompletion(t, repo, runID, 5*time.Second)
	require.NotNil(t, final)
	require.Equal(t, RunStatusCompleted, final.Status)
	require.NotNil(t, final.Result, "completed run must carry its durable result")
	require.NotNil(t, final.Result.Entities, "barrier entities must be projected onto the result")
	assert.Equal(t, []scriptpkg.Entity{{Value: "Jackie Chan", Type: "PERSON", Score: 0.95}}, final.Result.Entities.Persons)
	assert.Equal(t, []scriptpkg.Entity{{Value: "Hong Kong", Type: "LOCATION", Score: 0.9}}, final.Result.Entities.Places)
	assert.Equal(t, []scriptpkg.Entity{{Value: "martial arts", Type: "CONCEPT", Score: 0.85}}, final.Result.Entities.Concepts)
}

// TestRunner_BarrierProjectsPerSceneEntitiesOntoScenes certifies the durable
// per-scene entity surface: after the VidRush barrier, each scene carries its
// own typed EntityResult (the same model as the document aggregate — no second
// entity model) plus entity_overlay_required derived from it. A scene with a
// matching segment keeps its entities; the empty case is explicit
// (entities=[] + entity_overlay_required=false), never a fabricated entity.
func TestRunner_BarrierProjectsPerSceneEntitiesOntoScenes(t *testing.T) {
	runner, repo, _, _, _, _, _ := newTestRunner()
	runner.vidRushBarrier = barrierFunc(func(_ context.Context, runID string) ([]scriptpkg.VidRushSegmentResult, error) {
		return []scriptpkg.VidRushSegmentResult{
			{SceneID: "scene-0", Position: 0, Insights: scriptpkg.SegmentInsights{Entities: []scriptpkg.ExtractedEntity{
				{Value: "Jackie Chan", Type: "PERSON", Confidence: 0.95},
				{Value: "Hong Kong", Type: "LOCATION", Confidence: 0.9},
			}}},
			{SceneID: "scene-1", Position: 1, Insights: scriptpkg.SegmentInsights{}},
		}, nil
	})

	req := defaultTestRequest()
	runID := "run-barrier-per-scene-001"
	require.NoError(t, repo.Create(context.Background(), &GenerationRun{
		ID: runID, Request: req, Status: RunStatusPending, CurrentStage: StageNormalizing,
	}))

	runner.Execute(context.Background(), runID, req)
	final := awaitCompletion(t, repo, runID, 5*time.Second)
	require.NotNil(t, final)
	require.Equal(t, RunStatusCompleted, final.Status)
	require.NotNil(t, final.Result)

	// scene-0 carries its own typed entities + overlay required.
	scene0 := final.Result.Scenes[0]
	require.NotNil(t, scene0.Entities, "scene-0 must carry its per-scene EntityResult")
	assert.Equal(t, []scriptpkg.Entity{{Value: "Jackie Chan", Type: "PERSON", Score: 0.95}}, scene0.Entities.Persons)
	assert.Equal(t, []scriptpkg.Entity{{Value: "Hong Kong", Type: "LOCATION", Score: 0.9}}, scene0.Entities.Places)
	assert.True(t, scene0.EntityOverlayRequired)

	// scene-1 has no entities → explicit empty result + entity_overlay_required=false.
	scene1 := final.Result.Scenes[1]
	require.NotNil(t, scene1.Entities, "empty scene must keep an explicit empty EntityResult")
	assert.Empty(t, scene1.Entities.Persons)
	assert.Empty(t, scene1.Entities.Places)
	assert.Empty(t, scene1.Entities.Concepts)
	assert.False(t, scene1.EntityOverlayRequired)

	// The aggregate remains projected on the result surface.
	require.NotNil(t, final.Result.Entities)
	assert.Equal(t, []scriptpkg.Entity{{Value: "Jackie Chan", Type: "PERSON", Score: 0.95}}, final.Result.Entities.Persons)
}

// barrierFunc adapts a plain function to the VidRushBarrier seam.
type barrierFunc func(ctx context.Context, runID string) ([]scriptpkg.VidRushSegmentResult, error)

func (f barrierFunc) WaitForVidRush(ctx context.Context, runID string) ([]scriptpkg.VidRushSegmentResult, error) {
	return f(ctx, runID)
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
