// Package scriptgeneration — runner_stage_instrumentation_test.go pins the
// canonical wall timings for the three boundaries added to the durable
// runner: scene_analysis (per-scene nlp.extract), overlay.prepare (enqueue),
// and document.prepare (docs render, distinct from document.publish).
package scriptgeneration

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// TestSceneAnalysisRecordsNLPExtract pins that a per-scene VidRush enrichment
// is recorded as an nlp.extract operation under the scene_analysis stage, so
// fan-out reports expose calls / accumulated work / max call separate from
// the run wall time.
func TestSceneAnalysisRecordsNLPExtract(t *testing.T) {
	run := kernobs.NewRunObserver(nil).StartRun(context.Background(), kernobs.RunInfo{JobID: "job-1", AttemptID: "attempt-1"})
	ctx := kernobs.WithRun(context.Background(), run)

	enricher := &fakeSegmentEnricher{errs: map[string]error{}}
	coordinator := NewVidRushIncrementalCoordinator(enricher, &scriptpkg.ResolvedGenerationPlan{Language: "en"}, 1)

	_, err := coordinator.enrichSegment(ctx, scriptpkg.SpecScene{ID: "scene-0", Index: 0, Text: "scene text"})
	require.NoError(t, err)
	run.Finish()

	var found bool
	for _, op := range run.Report().Operations {
		if op.Stage == string(StageSceneAnalysis) && op.Component == string(kernobs.ComponentNLP) && op.Operation == string(kernobs.OperationExtract) {
			found = true
			break
		}
	}
	require.True(t, found, "scene_analysis nlp.extract operation must be recorded, got %+v", run.Report().Operations)
}

// TestSceneAnalysisNoRunIsNoop pins the nil-run degradation: without a Run
// bound to ctx, the coordinator still enriches (behaviour is unchanged) and
// records nothing.
func TestSceneAnalysisNoRunIsNoop(t *testing.T) {
	coordinator := NewVidRushIncrementalCoordinator(&fakeSegmentEnricher{errs: map[string]error{}}, &scriptpkg.ResolvedGenerationPlan{Language: "en"}, 1)
	_, err := coordinator.enrichSegment(context.Background(), scriptpkg.SpecScene{ID: "scene-0", Index: 0, Text: "text"})
	require.NoError(t, err)
}

// TestOverlayPrepareEnqueueRecordsStage pins that submitting overlay.prepare
// is measured as a stage on the canonical Run clock (the enqueue wall time),
// so the critical path shows the prepare submit separate from the generate
// phase.
func TestOverlayPrepareEnqueueRecordsStage(t *testing.T) {
	run := kernobs.NewRunObserver(nil).StartRun(context.Background(), kernobs.RunInfo{JobID: "job-1", AttemptID: "attempt-1"})
	ctx := kernobs.WithRun(context.Background(), run)

	enqueuer := &fakeOverlayPrepareEnqueuer{}
	runner := &Runner{overlayPrepareEnqueuer: enqueuer}
	intents := []capabilityoverlay.OverlayIntent{{
		Version: capabilityoverlay.OverlayIntentVersion, IntentID: "intent-scene-0",
		SceneID: "scene-0", SceneIndex: 0, Source: capabilityoverlay.IntentSourceEntity,
		Kind: string(capabilityoverlay.KindEntityCard), TemplateID: "person_default",
		TimingState: capabilityoverlay.TimingStatePending,
	}}

	require.NoError(t, runner.enqueueOverlayPrepare(ctx, "run-001", defaultTestRequest(), intents))
	require.Len(t, enqueuer.reqs, 1, "overlay.prepare must be enqueued once")
	run.Finish()

	var found bool
	for _, st := range run.Report().Stages {
		if st.Name == string(StageOverlayPrepare) {
			found = true
			break
		}
	}
	require.True(t, found, "overlay.prepare stage must be recorded, got %+v", run.Report().Stages)
}

// TestOverlayPrepareEnqueueNoIntentsNoStage pins the nil/no-intent no-op:
// with no enqueuer or no intents, no stage is recorded and no error surfaces.
func TestOverlayPrepareEnqueueNoIntentsNoStage(t *testing.T) {
	run := kernobs.NewRunObserver(nil).StartRun(context.Background(), kernobs.RunInfo{JobID: "job-1", AttemptID: "attempt-1"})
	ctx := kernobs.WithRun(context.Background(), run)
	runner := &Runner{overlayPrepareEnqueuer: &fakeOverlayPrepareEnqueuer{}}

	require.NoError(t, runner.enqueueOverlayPrepare(ctx, "run-001", defaultTestRequest(), nil))
	run.Finish()
	require.Empty(t, run.Report().Stages, "no intents must record no overlay.prepare stage")
}

// TestDocumentPrepareRecordsStage runs the full durable runner with docs
// enabled under a bound Run and pins that the document HTML render is
// recorded as a document.prepare stage, distinct from document.publish.
func TestDocumentPrepareRecordsStage(t *testing.T) {
	runner, repo, _, _, _, _, _ := newTestRunner()

	run := kernobs.NewRunObserver(nil).StartRun(context.Background(), kernobs.RunInfo{JobID: "job-docs-1", AttemptID: "attempt-1"})
	runCtx := kernobs.WithRun(context.Background(), run)

	req := defaultTestRequest()
	runID := "run-docs-prepare-001"
	require.NoError(t, repo.Create(context.Background(), &GenerationRun{
		ID: runID, Request: req, Status: RunStatusPending, CurrentStage: StageNormalizing,
	}))

	runner.Execute(runCtx, runID, req)
	require.Equal(t, RunStatusCompleted, awaitCompletion(t, repo, runID, 5*time.Second).Status)
	run.Finish()

	var prepare, publish bool
	for _, st := range run.Report().Stages {
		switch st.Name {
		case string(StageDocumentPrepare):
			prepare = true
		case string(StageDocumentPublish):
			publish = true
		}
	}
	require.True(t, prepare, "document.prepare stage must be recorded, got %+v", run.Report().Stages)
	require.True(t, publish, "document.publish stage must be recorded, got %+v", run.Report().Stages)
}

// TestSceneAnalysisFanoutWallIsReported pins that the scene_analysis fan-out
// wall is recorded as a stage on the canonical Run clock after the barrier
// completes, so the fan-out report exposes a nonzero wall_ms separate from the
// accumulated nlp.extract work instead of leaking the NLP wall into
// unattributed time.
func TestSceneAnalysisFanoutWallIsReported(t *testing.T) {
	run := kernobs.NewRunObserver(nil).StartRun(context.Background(), kernobs.RunInfo{JobID: "job-1", AttemptID: "attempt-1"})
	ctx := kernobs.WithRun(context.Background(), run)

	enricher := newBlockingSegmentEnricher()
	coordinator := NewVidRushIncrementalCoordinator(enricher, &scriptpkg.ResolvedGenerationPlan{Language: "en"}, 4)

	commitWithCtx := func(sceneID string, idx int, text string) {
		require.NoError(t, coordinator.OnSceneCommitted(ctx, SceneCommitted{
			RunID: "run-1", SceneID: sceneID, SceneIndex: idx,
			Text: text, TextHash: SceneTextHash(text), Revision: 1, Language: "en",
		}))
	}
	commitWithCtx("scene-0", 0, "First scene text")
	commitWithCtx("scene-1", 1, "Second scene text")

	done := make(chan error, 1)
	go func() {
		_, err := coordinator.WaitForVidRush(ctx, "run-1")
		done <- err
	}()

	// Hold the barrier open while enrichments are still pending so the
	// first-enrichment → barrier-end wall window is measurably non-zero.
	time.Sleep(10 * time.Millisecond)
	close(enricher.release)

	require.NoError(t, <-done)
	run.Finish()

	var stage *kernobs.StageReport
	for i := range run.Report().Stages {
		if run.Report().Stages[i].Name == string(StageSceneAnalysis) {
			stage = &run.Report().Stages[i]
			break
		}
	}
	require.NotNil(t, stage, "scene_analysis stage must be recorded, got %+v", run.Report().Stages)
	require.Greater(t, stage.DurationMs, int64(0), "scene_analysis wall must be non-zero")

	var fanout *kernobs.FanoutReport
	for _, f := range run.Report().FanoutReports() {
		if f.Stage == string(StageSceneAnalysis) {
			fanout = &f
			break
		}
	}
	require.NotNil(t, fanout, "scene_analysis fan-out report must be present")
	require.Greater(t, fanout.WallMs, int64(0), "scene_analysis fan-out wall_ms must be non-zero, got %+v", fanout)
	require.Equal(t, int64(2), fanout.Calls, "two enrichments must be counted")
}
