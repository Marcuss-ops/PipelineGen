package scriptgeneration

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/observability"
	"go.uber.org/zap"
)

// compileAudioOverlayPlan builds the timing-frozen overlay plan from the
// canonical audio timeline. Keeping this before the independent audio render
// lets the overlay worker begin as soon as both plans exist.
func (r *Runner) compileAudioOverlayPlan(ctx context.Context, runID string, req GenerateRequest, exec ExecutionContext, step ExecutionStep, result *GenerateResult) bool {
	reportTimingSkip := func(skip timingProjectionSkip) {
		observability.ScriptTimingProjectionSkipTotal.WithLabelValues(skip.SceneID, skip.Surface).Inc()
		r.log.Warn("voiceover timing projection skipped an unanchored surface",
			zap.String("scene_id", skip.SceneID), zap.String("surface", skip.Surface), zap.Error(skip.Cause))
	}
	if err := compileResultPhraseTimings(result, req.SourceLanguage, reportTimingSkip); err != nil {
		cause := fmt.Errorf("phrase timing compilation failed: %w", err)
		r.failExecutionStep(ctx, exec, step, cause)
		r.failRunWithRetry(ctx, runID, StageCompilingAudio, cause)
		return false
	}
	if err := compileResultEntityTimeline(result, req.SourceLanguage, reportTimingSkip); err != nil {
		cause := fmt.Errorf("entity timeline compilation failed: %w", err)
		r.failExecutionStep(ctx, exec, step, cause)
		r.failRunWithRetry(ctx, runID, StageCompilingAudio, cause)
		return false
	}
	background, bgErr := r.resolveOverlayBackground(ctx, req.OverlayBackground)
	if bgErr != nil {
		cause := fmt.Errorf("resolve overlay background failed: %w", bgErr)
		r.failExecutionStep(ctx, exec, step, cause)
		r.failRunWithRetry(ctx, runID, StageCompilingAudio, cause)
		return false
	}
	canvas := r.overlayCanvas
	canvas.ForegroundScalePercent = req.Render.ForegroundScalePercent
	canvas.Background = overlayBackgroundFromPayload(background)
	canvas.Style = req.OverlayStyle
	canvas.PhraseMotions = req.PhraseMotions
	canvas.PhraseMotionFamily = req.PhraseMotionFamily
	canvas.ImageMotions = req.ImageMotions
	canvas.MaxPhraseOverlays = req.MaxPhraseOverlays
	canvas.MapsOnly = req.MapsOnly
	canvas.MaxImageOverlays = req.MaxImageOverlays
	canvas.DisableNumberOverlays = req.DisableNumberOverlays
	if canvas.Style == nil && background != nil {
		canvas.Style = background.Style
	}
	driveFolderID := strings.TrimSpace(req.Render.DriveFolderID)
	plates := r.mapPlates
	if !r.shouldGeocodeScriptLocations(req) {
		plates = nil
	}
	if err := compileResultOverlayPlan(result, req.SourceLanguage, runID, req.Project, driveFolderID, canvas, plates, req.MediaPlan.Extraction.EntityImages.PerScene(), req.ScriptParams.ImagesPerScene > 0); err != nil {
		cause := fmt.Errorf("overlay plan compilation failed: %w", err)
		r.failExecutionStep(ctx, exec, step, cause)
		r.failRunWithRetry(ctx, runID, StageCompilingAudio, cause)
		return false
	}
	r.logPhraseMotionSelections(runID, result.OverlayPlan)
	r.logPhraseAnchoringDiagnostics(runID, result, req.SourceLanguage)
	setOverlayDriveJobID(result, exec.JobID)
	if err := validatePlannedEditingSpans(result); err != nil {
		return r.failAudioCompileStep(ctx, runID, exec, step, fmt.Errorf("editing timeline preflight failed: %w", err))
	}
	if _, err := BuildEditingTimeline(result); err != nil {
		cause := fmt.Errorf("editing timeline preflight failed: %w", err)
		r.failExecutionStep(ctx, exec, step, cause)
		r.failRunWithRetry(ctx, runID, StageCompilingAudio, cause)
		return false
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
		e.r.failExecutionStep(failCtx, e.exec, step, cause)
		e.r.failRunWithRetry(failCtx, e.runID, StageCompilingAudio, cause)
		return false
	}
	var done chan audioOverlayOutcome
	state.OnPlanReady = func(result *GenerateResult) bool {
		snapshot, err := snapshotGenerateResult(result)
		if err != nil {
			failure.cause = fmt.Errorf("snapshot overlay plan: %w", err)
			return false
		}
		done = make(chan audioOverlayOutcome, 1)
		go func() {
			var outcome audioOverlayOutcome
			// Explicit sibling ownership survives temporal containment.
			_, outcome.err = kernobs.MeasureIndependentStageReport(ctx, StageOverlayRender, func(c context.Context) error {
				var err error
				outcome.outcomes, err = e.r.renderOverlayPlans(c, e.req, e.resumeIdx, snapshot)
				return err
			})
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
	if done != nil {
		outcome := <-done
		if outcome.err != nil && (compiled || (!errors.Is(outcome.err, context.Canceled) && !errors.Is(outcome.err, context.DeadlineExceeded))) {
			return failJoined(outcome.err)
		}
		if !compiled {
			return failJoined(failure.cause)
		}
		e.r.applyOverlayRenderOutcomes(e.runID, e.result, outcome.outcomes)
	} else {
		if !compiled {
			return failJoined(failure.cause)
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
	if !e.measure(StageAudioFinalize, func(c context.Context) bool {
		return e.r.runAudioFinalizePhase(c, e.runID, e.exec, state, e.result)
	}) {
		return false
	}
	return e.measure(StageAudioPublish, func(c context.Context) bool {
		return e.r.publishFinalAudio(c, e.runID, e.req, e.routing, e.exec, e.result)
	})
}
