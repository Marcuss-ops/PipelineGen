// Package scriptgeneration — runner_vidrush_pipeline_test.go certifies the
// composition-time VidRushPipeline seam: when wired, the Runner builds a
// run-scoped incremental coordinator per run, so scene generation and VidRush
// enrichment run together (each committed scene is enriched exactly once and
// the final barrier completes the run).
package scriptgeneration

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func TestRunner_VidRushPipeline_EnrichesScenesIncrementally(t *testing.T) {
	runner, repo, _, _, _, _, _ := newTestRunner()

	enricher := &fakeSegmentEnricher{errs: map[string]error{}}
	metrics := &recordingVidRushMetrics{}
	runner.SetVidRushPipeline(&VidRushPipeline{
		Enricher: enricher,
		Metrics:  metrics,
		PlanResolver: VidRushPlanResolverFunc(func(_ context.Context, _ GenerateRequest) (*scriptpkg.ResolvedGenerationPlan, error) {
			return &scriptpkg.ResolvedGenerationPlan{Language: "en", Title: "test"}, nil
		}),
		Backpressure: DefaultVidRushBackpressure(),
	})

	req := defaultTestRequest()
	runID := "run-vidrush-001"
	require.NoError(t, repo.Create(context.Background(), &GenerationRun{
		ID:           runID,
		Request:      req,
		Status:       RunStatusPending,
		CurrentStage: StageNormalizing,
	}))

	runner.Execute(context.Background(), runID, req)
	final := awaitCompletion(t, repo, runID, 5*time.Second)
	require.NotNil(t, final)
	assert.Equal(t, RunStatusCompleted, final.Status, "run must complete with the VidRush pipeline wired")

	assert.Equal(t, 3, enricher.callCount(), "each generated scene must be enriched exactly once")

	committed, started, completed, _, _, _ := metrics.snapshot()
	assert.Equal(t, 3, committed, "one committed metric per scene")
	assert.Equal(t, 3, started, "one enrichment-started metric per scene")
	assert.Equal(t, 3, completed, "one enrichment-completed metric per scene")
}

func TestRunner_VidRushPipeline_WithoutPipelineSkipsVidRush(t *testing.T) {
	runner, repo, _, _, _, _, _ := newTestRunner()
	// No SetVidRushPipeline call: the runner must complete normally without
	// any incremental VidRush wiring.

	req := defaultTestRequest()
	runID := "run-no-vidrush-001"
	require.NoError(t, repo.Create(context.Background(), &GenerationRun{
		ID:           runID,
		Request:      req,
		Status:       RunStatusPending,
		CurrentStage: StageNormalizing,
	}))

	runner.Execute(context.Background(), runID, req)
	final := awaitCompletion(t, repo, runID, 5*time.Second)
	require.NotNil(t, final)
	assert.Equal(t, RunStatusCompleted, final.Status, "run must complete without VidRush wiring")
}

func TestRunner_VidRushPipeline_PlanResolverErrorFailsRun(t *testing.T) {
	runner, repo, _, _, _, _, _ := newTestRunner()

	runner.SetVidRushPipeline(&VidRushPipeline{
		Enricher: &fakeSegmentEnricher{errs: map[string]error{}},
		PlanResolver: VidRushPlanResolverFunc(func(_ context.Context, _ GenerateRequest) (*scriptpkg.ResolvedGenerationPlan, error) {
			return nil, assert.AnError
		}),
	})

	req := defaultTestRequest()
	runID := "run-vidrush-plan-err-001"
	require.NoError(t, repo.Create(context.Background(), &GenerationRun{
		ID:           runID,
		Request:      req,
		Status:       RunStatusPending,
		CurrentStage: StageNormalizing,
	}))

	runner.Execute(context.Background(), runID, req)
	final := awaitCompletion(t, repo, runID, 5*time.Second)
	require.NotNil(t, final)
	assert.Equal(t, RunStatusFailed, final.Status, "a plan resolver error must fail the run closed")
}
