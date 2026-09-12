package scriptgeneration

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"go.uber.org/zap"

	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
)

// sleepingRenderEnqueuer stands in for the RenderingGen queue: the blocking
// render is what this phase measures, so the stub must actually take time.
type sleepingRenderEnqueuer struct {
	delay time.Duration
	calls int
}

func (e *sleepingRenderEnqueuer) EnqueueChrononPlan(ctx context.Context, plan capabilityoverlay.OverlayPlan) (RenderReference, error) {
	e.calls++
	if e.delay > 0 {
		select {
		case <-time.After(e.delay):
		case <-ctx.Done():
			return RenderReference{}, ctx.Err()
		}
	}
	return RenderReference{
		JobID:  plan.PlanID,
		Status: "COMPLETED",
		Artifact: &RenderArtifact{
			ID: "overlay-artifact", Kind: "overlay", SHA256: "overlay-sha",
			SizeBytes: 1024, DriveFileID: "drive-overlay", DriveLink: "https://drive.example/overlay",
		},
	}, nil
}

// TestOverlayRenderPhaseOwnsItsStageAndNotTheAudioStage pins the attribution
// contract the phase split exists for.
//
// Before the split the blocking render ran inside the audio compile phase, so
// audio_compile's wall time was dominated by a video render it does not own and
// the render never appeared on the critical path. The kernel attributes a nested
// stage to its enclosing stage, so "the render must be a sibling" is a
// structural requirement, not a naming preference — hence the assertion that the
// render's stage wall is real AND that nothing is recorded under audio_compile.
//
// The end-to-end geometry (audio_compile ending where the render starts) is
// asserted by TestCertification_ThreeSceneVerticalSlice, which runs the real
// pipeline; this test pins the boundary's own contract.
func TestOverlayRenderPhaseOwnsItsStageAndNotTheAudioStage(t *testing.T) {
	run := kernobs.NewRunObserver(nil).StartRun(context.Background(), kernobs.RunInfo{JobID: "job-render", AttemptID: "attempt-1"})
	ctx := kernobs.WithRun(context.Background(), run)

	const renderDelay = 120 * time.Millisecond
	enqueuer := &sleepingRenderEnqueuer{delay: renderDelay}
	runner := &Runner{overlayRenderEnqueuer: enqueuer, log: zap.NewNop()}

	req := defaultTestRequest()
	req.Render.Enabled = true
	result := &GenerateResult{
		OverlayPlan: &capabilityoverlay.OverlayPlan{SchemaVersion: capabilityoverlay.SchemaVersionPlan, PlanID: "plan-1", VideoID: "video-1"},
	}

	// Exercised exactly as the pipeline does: audioCompile() runs this phase
	// under its own stage wrapper, which is what makes the render a SIBLING of
	// the audio stage rather than a nested part of it.
	require.True(t, runner.measurePhase(ctx, StageOverlayRender, func(c context.Context) bool {
		return runner.runOverlayRenderPhase(c, "run-1", req, ExecutionContext{}, 0, audioCompileState{}, result)
	}), "the render phase must succeed")
	run.Finish()

	report := run.Report()
	var renderStage *kernobs.StageReport
	for i := range report.Stages {
		st := &report.Stages[i]
		if st.Name == string(StageOverlayRender) {
			renderStage = st
		}
		require.NotEqual(t, string(audioCompileStage), st.Name,
			"the render phase must not record anything under the audio compile stage")
	}
	require.NotNil(t, renderStage, "overlay_render must be recorded as its own stage, got %+v", report.Stages)
	require.GreaterOrEqual(t, renderStage.DurationMs, renderDelay.Milliseconds(),
		"overlay_render must carry the real render wall time, not a zero-width marker")
	require.False(t, renderStage.StartedAt.IsZero(), "overlay_render must be anchored so it can join the critical path")
	require.False(t, renderStage.FinishedAt.IsZero(), "overlay_render must be anchored so it can join the critical path")

	require.NotNil(t, result.OverlayRender, "the certified render reference must be persisted on the result")
	require.Equal(t, 1, enqueuer.calls)
}

// TestOverlayRenderPhaseSkipsWithoutWork pins that a run which did not ask for a
// render (or has no frozen plan) does not enqueue anything and does not fail.
// The skip conditions are the same ones the in-phase block used to check.
func TestOverlayRenderPhaseSkipsWithoutWork(t *testing.T) {
	enqueuer := &sleepingRenderEnqueuer{}
	runner := &Runner{overlayRenderEnqueuer: enqueuer, log: zap.NewNop()}

	req := defaultTestRequest()
	req.Render.Enabled = true

	// No frozen overlay plan.
	require.True(t, runner.runOverlayRenderPhase(context.Background(), "run-1", req, ExecutionContext{}, 0, audioCompileState{}, &GenerateResult{}))

	// Frozen plan but the request did not enable the render.
	req.Render.Enabled = false
	result := &GenerateResult{OverlayPlan: &capabilityoverlay.OverlayPlan{PlanID: "plan-1"}}
	require.True(t, runner.runOverlayRenderPhase(context.Background(), "run-1", req, ExecutionContext{}, 0, audioCompileState{}, result))

	// Frozen plan but the run already resumed past the audio stage: the render
	// was part of that stage's work, so it must not be re-waited on.
	req.Render.Enabled = true
	require.True(t, runner.runOverlayRenderPhase(context.Background(), "run-1", req, ExecutionContext{},
		StageIndex(StageCompilingAudio)+1, audioCompileState{}, result))

	require.Zero(t, enqueuer.calls, "no skip path may submit a render")
}

// TestAudioStageNamesAreDistinct pins that the four boundaries the audio pipeline
// was split into keep distinct stage names. Two boundaries sharing a name would
// make the breakdown report the same stage twice and make the dominant-operation
// join ambiguous.
func TestAudioStageNamesAreDistinct(t *testing.T) {
	stages := []kernobs.StageName{StageOverlayRender, StageAudioFinalize, StageAudioPublish, kernobs.StageName(audioCompileStage)}
	seen := map[kernobs.StageName]bool{}
	for _, st := range stages {
		require.NotEmpty(t, string(st), "every audio boundary stage needs a name")
		require.False(t, seen[st], "stage %q is declared twice", st)
		seen[st] = true
	}
}
