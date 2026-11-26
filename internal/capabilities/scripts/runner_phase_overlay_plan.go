package scriptgeneration

import (
	"context"
	"errors"
	"fmt"
	"time"

	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/contract"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"

	"go.uber.org/zap"
)

// compileAudioOverlayPlan builds the timing-frozen overlay plan from the
// canonical audio timeline. Keeping this before the independent audio render
// lets the overlay worker begin as soon as both plans exist.
// compileAudioOverlayPlan runs the SSOT plan projection with the
// runner's fail-closed audio-compile semantics on the caller's goroutine.
// The overlap path (runner_overlay_plan_branch.go) computes the exact same
// projection on a detached snapshot and applies it at the join.
func (r *Runner) compileAudioOverlayPlan(ctx context.Context, runID string, req contract.GenerateRequest, exec ExecutionContext, step ExecutionStep, result *contract.GenerateResult) bool {
	if err := r.computeOverlayPlanProjection(ctx, runID, req, exec.JobID, result); err != nil {
		return r.failAudioCompileStep(ctx, runID, exec, step, err)
	}
	return true
}

type deferredAudioFailureKey struct{}

type deferredAudioFailure struct {
	cause error
	step  ExecutionStep
}

type audioOverlayOutcome struct {
	outcomes []overlayRenderOutcome
	err      error
}

// audioCompile overlaps snapshot-only overlays with encoding. All exits join
// both siblings; one caller owns result writes and exactly-once failures.
func (r *Runner) audioCompilePhase(ctx context.Context, runID string, req contract.GenerateRequest, routing scriptpkg.ArtifactRoutingContext, exec ExecutionContext, resumeIdx int, result *contract.GenerateResult) bool {
	baseCtx := ctx
	var state audioCompileState
	failure := &deferredAudioFailure{}
	ctx, cancel := context.WithCancel(context.WithValue(baseCtx, deferredAudioFailureKey{}, failure))
	defer cancel()
	failJoined := func(cause error) bool {
		failCtx, finish := context.WithTimeout(context.WithoutCancel(baseCtx), 5*time.Second)
		defer finish()
		step := failure.step
		if step.StepID == "" {
			step = state.Step
		}
		r.failExecutionStepBestEffort(failCtx, exec, step, cause)
		r.failRunWithRetry(failCtx, runID, StageCompilingAudio, cause)
		return false
	}
	var done chan audioOverlayOutcome
	state.OnPlanReady = func(result *contract.GenerateResult) bool {
		// Freeze the plan ONCE on the durable result; the renderer then receives
		// a typed immutable input copied from it. Freezing before the copy is
		// what makes the persisted plan and the rendered plan the same document
		// (and therefore the same digest).
		if err := FreezeFinalJobOverlayPlans(result, req.FinalJob); err != nil {
			failure.cause = fmt.Errorf("freeze overlay plan: %w", err)
			return false
		}
		renderInput := newOverlayRenderInput(result, req, resumeIdx)
		done = make(chan audioOverlayOutcome, 1)
		// The render overlaps audio compilation on this branch, so it does not
		// pass through runOverlayRenderPhase (the serial path that normally adds
		// the map subprocess observer). Attach the same observer here before the
		// goroutine starts; otherwise map heartbeats reach the process log but
		// `/api/jobs/{id}/full` stays at the generic "render running" detail.
		mapProgressCtx := r.withMapRenderProgress(ctx, runID)
		go func() {
			var outcome audioOverlayOutcome
			// Explicit sibling ownership survives temporal containment.
			_, outcome.err = kernobs.MeasureIndependentStageReport(mapProgressCtx, StageOverlayRender, func(c context.Context) error {
				// The final-job handoff consumes the certified object-store locators,
				// so the Drive publication is NOT on this critical path: it belongs to
				// the outbox, which projects drive_file_id onto the /full view when the
				// upload lands. Waiting here made "rendered" and "delivered" one
				// stage, so a slow Drive upload stretched the overlay stage even though
				// the master never read the Drive copy.
				var err error
				outcome.outcomes, err = r.renderOverlayInput(c, req, renderInput)
				return err
			})
			if outcome.err != nil {
				cancel()
			}
			done <- outcome
		}()
		return true
	}
	compiled := r.measurePhase(ctx, kernobs.StageName(audioCompileStage), func(c context.Context) bool {
		return r.runAudioCompilePhase(c, runID, req, exec, resumeIdx, result, &state)
	})
	if !compiled {
		cancel()
	}
	// The certified master is ready when audio_compile returns. Publish it
	// while the already-started overlay branch is still rendering; the editing
	// timeline itself remains in audio_finalize below, after both siblings join.
	audioPublished := false
	if compiled {
		if !publishFinalAudioNeeded(req, r) {
			// The final-job handoff publishes certified master-audio bytes to
			// Objectstore over HTTPS; no run needs a Drive copy.
			if r.log != nil {
				r.log.Info("final audio Drive publish skipped: final handoff uses HTTPS Objectstore",
					zap.String("run_id", runID))
			}
			audioPublished = true
		} else {
			audioPublished = r.measurePhase(ctx, StageAudioPublish, func(c context.Context) bool {
				return r.publishFinalAudio(c, runID, req, routing, exec, result)
			})
			if !audioPublished {
				cancel()
			}
		}
	}
	var splitDone chan finalJobSplitOutcome
	if compiled && audioPublished && done != nil {
		// PREPARE the base timeline while the overlay sibling is still
		// rendering. The join below waits for whichever sibling finishes last,
		// so the handoff is overlapped instead of appended to the critical path.
		splitDone = r.startFinalJobSplitPrepare(ctx, runID, req, result)
	}
	if done != nil {
		// The join is QUEUE WAIT on the overlay sibling, not audio work: before
		// this instrumentation the tail where audio had finished and the run was
		// simply parked on the overlay branch landed in the run's unattributed
		// bucket (the TICKET-OVERLAY-THROUGHPUT "which inputs wait on the
		// others" question). Recorded as a completion wait on the render-queue
		// component — the same vocabulary waitForCompletion uses — so overlap
		// with the sibling's own render-queue waits is dissolved by the kernel's
		// blocked-interval UNION (overlapping waits count once).
		joinStarted := time.Now()
		outcome := <-done
		overlayFinishedAt := time.Now()
		if splitDone != nil {
			// Join the early PREPARE before inspecting the render outcome: even
			// a failed render must leave a remote handle durable, otherwise the
			// retry would ask the Master for a second render.
			r.applyFinalJobSplitPrepare(ctx, runID, result, <-splitDone, overlayFinishedAt)
		}
		// Record only the overlay sibling's own wait. If the early PREPARE is
		// slower, the extra join time is handoff work, not render-queue waiting;
		// attributing it to render_queue would inflate the queue metric and hide
		// the overlap the split is meant to create.
		if overlayFinishedAt.After(joinStarted) {
			kernobs.RecordWait(baseCtx, kernobs.WaitInfo{
				Kind:       kernobs.WaitCompletion,
				Component:  kernobs.ComponentRenderQueue,
				StartedAt:  joinStarted,
				FinishedAt: overlayFinishedAt,
			})
		}
		if outcome.err != nil && (compiled || (!errors.Is(outcome.err, context.Canceled) && !errors.Is(outcome.err, context.DeadlineExceeded))) {
			return failJoined(outcome.err)
		}
		if !compiled {
			return failJoined(failure.cause)
		}
		if !audioPublished {
			return false
		}
		r.applyOverlayRenderOutcomes(runID, result, outcome.outcomes)
	} else {
		if !compiled {
			return failJoined(failure.cause)
		}
		if !audioPublished {
			return false
		}
		if !r.measurePhase(ctx, StageOverlayRender, func(c context.Context) bool {
			return r.runOverlayRenderPhase(c, runID, req, exec, resumeIdx, state, result)
		}) {
			return false
		}
	}
	if err := baseCtx.Err(); err != nil {
		return failJoined(err)
	}
	if !audioPublished {
		return false
	}
	if !r.measurePhase(ctx, StageAudioFinalize, func(c context.Context) bool {
		return r.runAudioFinalizePhase(c, runID, exec, state, result)
	}) {
		return false
	}
	return true
}

// ResolveOverlayBackground preserves the scripts capability API while
// delegating background policy to the dedicated overlay package.
func ResolveOverlayBackground(ctx context.Context, source OverlayBackgroundSource, src *scriptpkg.OverlayBackgroundSpec) (*scriptpkg.OverlayBackgroundSpec, error) {
	if source == nil {
		return capabilityoverlay.ResolveBackground(ctx, nil, src)
	}
	return capabilityoverlay.ResolveBackground(ctx, func(ctx context.Context, assetID string) (string, string, string, string, string, error) {
		asset, err := source.ResolveOverlayBackground(ctx, assetID)
		return asset.AssetID, asset.LocalPath, asset.URL, asset.SHA256, asset.MediaType, err
	}, src)
}

func (r *Runner) resolveOverlayBackground(ctx context.Context, src *scriptpkg.OverlayBackgroundSpec) (*scriptpkg.OverlayBackgroundSpec, error) {
	if r == nil {
		return nil, fmt.Errorf("visual background asset cannot be resolved: runner is nil")
	}
	return ResolveOverlayBackground(ctx, r.overlayBackgroundSource, src)
}
