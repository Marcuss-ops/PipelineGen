package scriptgeneration

import (
	"context"
	"errors"
	"fmt"
	"time"

	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
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
func (r *Runner) compileAudioOverlayPlan(ctx context.Context, runID string, req GenerateRequest, exec ExecutionContext, step ExecutionStep, result *GenerateResult) bool {
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
func (e *executionRun) audioCompile() bool {
	var state audioCompileState
	failure := &deferredAudioFailure{}
	ctx, cancel := context.WithCancel(context.WithValue(e.ctx, deferredAudioFailureKey{}, failure))
	defer cancel()
	failJoined := func(cause error) bool {
		failCtx, finish := context.WithTimeout(context.WithoutCancel(e.ctx), 5*time.Second)
		defer finish()
		step := failure.step
		if step.StepID == "" {
			step = state.Step
		}
		e.r.failExecutionStepBestEffort(failCtx, e.exec, step, cause)
		e.r.failRunWithRetry(failCtx, e.runID, StageCompilingAudio, cause)
		return false
	}
	var done chan audioOverlayOutcome
	state.OnPlanReady = func(result *GenerateResult) bool {
		// Freeze the plan on the DURABLE result before the snapshot exists. The
		// snapshot is what the renderer receives; freezing first is what makes
		// the persisted plan and the rendered plan the same document (and
		// therefore the same digest). Doing it on the snapshot instead was the
		// divergence this hand-off used to create.
		if err := FreezeFinalJobOverlayPlans(result, e.req.FinalJob); err != nil {
			failure.cause = fmt.Errorf("freeze overlay plan: %w", err)
			return false
		}
		snapshot, err := snapshotGenerateResult(result)
		if err != nil {
			failure.cause = fmt.Errorf("snapshot overlay plan: %w", err)
			return false
		}
		// The overlay branch runs from a detached snapshot. Reassert the
		// broker job identity at this hand-off so publication cannot fall back
		// to the internal run/plan ID after JSON checkpointing.
		setOverlayDriveJobID(snapshot, e.exec.JobID)
		done = make(chan audioOverlayOutcome, 1)
		// The P2 barrier (spun in runAudioCompilePhase off the P1 plan branch)
		// may already have certified some languages while the master compiled.
		// The fork merges its outcomes before its pending capture, so a language
		// the barrier composed is never re-submitted; only the fork writes.
		barrier := state.Barrier
		go func() {
			var outcome audioOverlayOutcome
			var barrierOutcomes []overlayRenderOutcome
			// Explicit sibling ownership survives temporal containment.
			_, outcome.err = kernobs.MeasureIndependentStageReport(ctx, StageOverlayRender, func(c context.Context) error {
				if barrier != nil {
					// Carry the barrier's certified outcomes into the fork's join:
					// they must reach the durable result exactly like the fork's
					// own submissions do, and no language composed by the barrier
					// is re-submitted because applyToSnapshot claimed it first.
					barrierOutcomes = barrier.applyToSnapshot(snapshot)
				}
				var err error
				outcome.outcomes, err = e.r.renderOverlayPlans(c, e.req, e.resumeIdx, snapshot)
				if err == nil && e.req.FinalJob && e.r.overlayPublicationDrainer != nil {
					// All child renders are now submitted; join their bounded Drive
					// publications before closing the measured overlay stage. The
					// final master consumes these certified Drive IDs.
					err = e.r.overlayPublicationDrainer.Wait(c)
				}
				return err
			})
			outcome.outcomes = append(barrierOutcomes, outcome.outcomes...)
			if outcome.err != nil {
				cancel()
			}
			done <- outcome
		}()
		return true
	}
	compiled := e.r.measurePhase(ctx, kernobs.StageName(audioCompileStage), func(c context.Context) bool {
		return e.r.runAudioCompilePhase(c, e.runID, e.req, e.exec, e.resumeIdx, e.result, &state)
	})
	if !compiled {
		cancel()
	}
	// The certified master is ready when audio_compile returns. Publish it
	// while the already-started overlay branch is still rendering; the editing
	// timeline itself remains in audio_finalize below, after both siblings join.
	audioPublished := false
	if compiled {
		if !publishFinalAudioNeeded(e.req, e.r) {
			// The final-job handoff publishes certified master-audio bytes to
			// Objectstore over HTTPS; no run needs a Drive copy.
			if e.r.log != nil {
				e.r.log.Info("final audio Drive publish skipped: final handoff uses HTTPS Objectstore",
					zap.String("run_id", e.runID))
			}
			audioPublished = true
		} else {
			audioPublished = e.measure(StageAudioPublish, func(c context.Context) bool {
				return e.r.publishFinalAudio(c, e.runID, e.req, e.routing, e.exec, e.result)
			})
			if !audioPublished {
				cancel()
			}
		}
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
		if joinFinished := time.Now(); joinFinished.After(joinStarted) {
			kernobs.RecordWait(e.ctx, kernobs.WaitInfo{
				Kind:       kernobs.WaitCompletion,
				Component:  kernobs.ComponentRenderQueue,
				StartedAt:  joinStarted,
				FinishedAt: joinFinished,
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
		e.r.applyOverlayRenderOutcomes(e.runID, e.result, outcome.outcomes)
	} else {
		if !compiled {
			return failJoined(failure.cause)
		}
		if !audioPublished {
			return false
		}
		if !e.measure(StageOverlayRender, func(c context.Context) bool {
			return e.r.runOverlayRenderPhase(c, e.runID, e.req, e.exec, e.resumeIdx, state, e.result)
		}) {
			return false
		}
	}
	if err := e.ctx.Err(); err != nil {
		return failJoined(err)
	}
	if !audioPublished {
		return false
	}
	if !e.measure(StageAudioFinalize, func(c context.Context) bool {
		return e.r.runAudioFinalizePhase(c, e.runID, e.exec, state, e.result)
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
