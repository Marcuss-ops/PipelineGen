// Package scriptgeneration — runner_vidrush_concurrency_test.go certifies
// that VidRush wiring is isolated per run: when two runs execute concurrently
// on the same shared Runner, each run must resolve its own coordinator and
// never observe the other run's. This is the regression guard for the
// production race where run B's beginVidRush overwrote the shared observer
// fields and run A's scene commits landed in run B's coordinator, failing
// closed with "scene commit run X does not match coordinator run Y".
package scriptgeneration

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// vidrushPipelineFixture wires a shared VidRushPipeline (enricher + plan
// resolver + metrics) onto the runner, mirroring production wiring.
func vidrushPipelineFixture(runner *Runner) (*fakeSegmentEnricher, *recordingVidRushMetrics) {
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
	return enricher, metrics
}

// TestRunner_VidRushWiring_IsPerRun is the deterministic seam-level
// regression: registering two runs must yield two distinct coordinators, and
// each run's scene commits must route to its own coordinator even after the
// other run registers its wiring. Pre-registry this failed because run B's
// beginVidRush overwrote the shared observer, so run A's commit hit run B's
// coordinator and errored with the run-mismatch guard.
func TestRunner_VidRushWiring_IsPerRun(t *testing.T) {
	runner, _, _, _, _, _, _ := newTestRunner()
	vidrushPipelineFixture(runner)

	req := defaultTestRequest()
	runA, runB := "run-a", "run-b"

	coordA, err := runner.beginVidRush(context.Background(), runA, req)
	require.NoError(t, err)
	require.NotNil(t, coordA)
	coordB, err := runner.beginVidRush(context.Background(), runB, req)
	require.NoError(t, err)
	require.NotNil(t, coordB)

	// Distinct coordinators per run — the single-field design returned the
	// same (last-registered) coordinator for both.
	require.NotSame(t, coordA, coordB, "each run must own its coordinator")
	assert.Same(t, coordA, runner.sceneCommitObserverFor(runA))
	assert.Same(t, coordB, runner.sceneCommitObserverFor(runB))
	assert.Same(t, coordA, runner.vidRushBarrierFor(runA))
	assert.Same(t, coordA, runner.vidRushTimingFor(runA))

	// Seed run B's coordinator with its own commit so its runID is pinned.
	require.NoError(t, coordB.OnSceneCommitted(context.Background(), NewSceneCommitted(runB, Scene{ID: "b-0", Index: 0}, "en", 1)))

	// Run A's commits must route to A's coordinator, not B's — pre-registry
	// this hit coordB and failed the run-mismatch guard.
	require.NoError(t, coordA.OnSceneCommitted(context.Background(), NewSceneCommitted(runA, Scene{ID: "a-0", Index: 0}, "en", 1)))

	// Unregistering run A must not disturb run B's wiring.
	runner.endVidRush(runA)
	assert.Same(t, coordB, runner.sceneCommitObserverFor(runB), "run B wiring survives run A teardown")

	// Drain B's enrichment goroutine so the test exits cleanly.
	_, err = coordB.WaitForVidRush(context.Background(), runB)
	require.NoError(t, err)
}

// TestRunner_ConcurrentRuns_IsolateVidRushWiring is the end-to-end regression:
// two full runs execute concurrently on the same Runner with the VidRush
// pipeline wired, and both must complete with every scene enriched exactly
// once. Pre-registry, at least one run failed closed on the coordinator run
// mismatch under this interleaving.
func TestRunner_ConcurrentRuns_IsolateVidRushWiring(t *testing.T) {
	runner, repo, _, _, _, _, _ := newTestRunner()
	enricher, _ := vidrushPipelineFixture(runner)

	req := defaultTestRequest()
	runIDs := []string{"run-concurrent-a", "run-concurrent-b"}
	for _, runID := range runIDs {
		require.NoError(t, repo.Create(context.Background(), &GenerationRun{
			ID:           runID,
			Request:      req,
			Status:       RunStatusPending,
			CurrentStage: StageNormalizing,
		}))
	}

	var wg sync.WaitGroup
	for _, runID := range runIDs {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			runner.Execute(context.Background(), id, req)
		}(runID)
	}
	wg.Wait()

	for _, runID := range runIDs {
		final := awaitCompletion(t, repo, runID, 5*time.Second)
		require.NotNil(t, final)
		assert.Equal(t, RunStatusCompleted, final.Status,
			"run %s must complete with isolated VidRush wiring (failure: %s)", runID, final.ErrorMessage)
	}
	assert.Equal(t, 6, enricher.callCount(), "3 scenes per run, each enriched exactly once across both runs")
}
