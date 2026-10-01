package scriptgeneration

import (
	"context"
	"fmt"
	"strings"

	mediadomain "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
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
	canvas.MaxImageOverlays = req.MaxImageOverlays
	if canvas.Style == nil && background != nil {
		canvas.Style = background.Style
	}
	driveFolderID := strings.TrimSpace(req.Render.DriveFolderID)
	plates := r.mapPlates
	if req.MediaPlan.ProviderPolicy.Geocoding != mediadomain.MediaToggleEnabled {
		plates = nil
	}
	if err := compileResultOverlayPlan(result, req.SourceLanguage, runID, req.Project, driveFolderID, canvas, plates, req.MediaPlan.Extraction.EntityImages.PerScene()); err != nil {
		cause := fmt.Errorf("overlay plan compilation failed: %w", err)
		r.failExecutionStep(ctx, exec, step, cause)
		r.failRunWithRetry(ctx, runID, StageCompilingAudio, cause)
		return false
	}
	r.logPhraseMotionSelections(runID, result.OverlayPlan)
	r.logPhraseAnchoringDiagnostics(runID, result, req.SourceLanguage)
	setOverlayDriveJobID(result, exec.JobID)
	if _, err := BuildEditingTimeline(result); err != nil {
		cause := fmt.Errorf("editing timeline preflight failed: %w", err)
		r.failExecutionStep(ctx, exec, step, cause)
		r.failRunWithRetry(ctx, runID, StageCompilingAudio, cause)
		return false
	}
	return true
}
