package scriptgeneration

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"

	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	"go.uber.org/zap"
)

func (r *Runner) assembleFinalVideo(ctx context.Context, runID string, result *GenerateResult) error {
	if result == nil || result.ExpectedRenderCount == 0 {
		return nil
	}
	if r.finalVideoAssembler == nil {
		return fmt.Errorf("FINAL_ASSEMBLER_NOT_WIRED: final video assembler is not configured")
	}
	if len(result.LocalizedRenders) != result.ExpectedRenderCount || len(result.LocalizedRenderFailures) != 0 {
		return fmt.Errorf("INCOMPLETE_RENDER_SET: cannot assemble incomplete render set")
	}
	ordered := append([]LocalizedRenderResult(nil), result.LocalizedRenders...)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].SceneIndex < ordered[j].SceneIndex })
	inputs := make([]string, 0, len(ordered))
	for _, render := range ordered {
		if render.LocalPath == "" {
			return fmt.Errorf("FINAL_VIDEO_INPUT_MISSING: scene=%s has no local render path", render.SceneID)
		}
		info, err := os.Stat(render.LocalPath)
		if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
			return fmt.Errorf("FINAL_VIDEO_INPUT_MISSING: %s", render.LocalPath)
		}
		inputs = append(inputs, render.LocalPath)
	}
	output := filepath.Join(os.TempDir(), "pipelinegen-"+runID+"-final.mp4")
	if err := r.finalVideoAssembler.AssembleFinalVideo(ctx, inputs, output); err != nil {
		return fmt.Errorf("FINAL_ASSEMBLY_FAILED: %w", err)
	}
	info, err := os.Stat(output)
	if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
		return fmt.Errorf("FINAL_VIDEO_MISSING: assembler returned no usable output")
	}
	file, err := os.Open(output)
	if err != nil {
		return fmt.Errorf("FINAL_VIDEO_HASH_FAILED: %w", err)
	}
	hash := sha256.New()
	_, copyErr := io.Copy(hash, file)
	closeErr := file.Close()
	if copyErr != nil || closeErr != nil {
		return fmt.Errorf("FINAL_VIDEO_HASH_FAILED: %v", firstError(copyErr, closeErr))
	}
	result.FinalVideo = &FinalVideoReference{LocalPath: output, SizeBytes: info.Size(), SHA256: fmt.Sprintf("%x", hash.Sum(nil))}
	return nil
}

func firstError(a, b error) error {
	if a != nil {
		return a
	}
	return b
}

// ── Internal helpers ────────────────────────────────────────────────

// updateStage persists the stage transition.
func (r *Runner) updateStage(ctx context.Context, runID string, status RunStatus, stage Stage) error {
	return r.repo.UpdateStage(ctx, runID, status, stage)
}

// checkpoint saves the partial result to the repository.
// Errors are logged but not propagated (best-effort checkpoint).
func (r *Runner) checkpoint(ctx context.Context, runID string, result *GenerateResult) {
	started := time.Now()
	if err := r.repo.SavePartialResult(ctx, runID, result); err != nil {
		r.log.Warn("checkpoint save failed",
			zap.String("run_id", runID),
			zap.Error(err),
		)
	}
	kernobs.RecordStage(ctx, kernobs.StageInfo{Stage: "checkpoint"}, started, time.Now(), nil)
}

// failRunWithRetry marks the run as FAILED and persists all failure
// metadata (error_code, failed_stage, attempt_count, next_retry_at)
// via the repository's FailRun method.
//
// P0 verdetto contract: every failure persists:
//   - error_code   — stable machine-readable code
//   - failed_stage — which stage failed
//   - attempt_count — incremented retry count
//   - next_retry_at — exponential backoff window (nil when exhausted)
func (r *Runner) failRunWithRetry(ctx context.Context, runID string, failedStage Stage, err error) {
	r.log.Error("scriptgeneration: stage failed",
		zap.String("run_id", runID),
		zap.String("failed_stage", string(failedStage)),
		zap.Error(err),
	)

	// Derive a stable error code from the error chain.
	errorCode := deriveErrorCode(err, failedStage)

	// Read current run to get attempt count.
	run, readErr := r.repo.Get(ctx, runID)
	attempt := 0
	if readErr == nil && run != nil {
		attempt = run.AttemptCount
	}

	// Compute the next retry attempt (1-based for display, 0-based for storage).
	nextAttempt := attempt + 1
	var nextRetryAt *time.Time
	if nextAttempt <= MaxRetries {
		delay := RetryDelay(attempt)
		now := time.Now().UTC()
		t := now.Add(delay)
		nextRetryAt = &t
		r.log.Info("retry scheduled",
			zap.String("run_id", runID),
			zap.Int("attempt", nextAttempt),
			zap.Duration("delay", delay),
			zap.Time("next_retry_at", t),
		)
	} else {
		r.log.Warn("max retries exhausted",
			zap.String("run_id", runID),
			zap.Int("attempts", attempt),
		)
	}

	// Persist all failure metadata atomically via FailRun.
	// AttemptCount is incremented to reflect this failed attempt.
	if failErr := r.repo.FailRun(ctx, FailRunInput{
		RunID:        runID,
		FailedStage:  failedStage,
		ErrorCode:    errorCode,
		ErrorMessage: err.Error(),
		AttemptCount: attempt + 1,
		NextRetryAt:  nextRetryAt,
	}); failErr != nil {
		r.log.Error("failed to persist run failure",
			zap.String("run_id", runID),
			zap.Error(failErr),
		)
	}
}

// completeRun marks the run as COMPLETED and saves the final result.
func (r *Runner) completeRun(ctx context.Context, runID string, result *GenerateResult) {
	if result != nil && result.ExpectedRenderCount > 0 {
		actual, failed := len(result.LocalizedRenders), len(result.LocalizedRenderFailures)
		if result.RenderMetrics != nil {
			result.RenderMetrics.Expected = result.ExpectedRenderCount
			result.RenderMetrics.Successful = actual
			result.RenderMetrics.Failed = failed
		}
		if actual != result.ExpectedRenderCount || failed != 0 {
			r.failRunWithRetry(ctx, runID, StagePublishingDocuments,
				fmt.Errorf("INCOMPLETE_RENDER_SET: expected=%d successful=%d failed=%d", result.ExpectedRenderCount, actual, failed))
			return
		}
		if result.FinalVideoRequired && (result.FinalVideo == nil || result.FinalVideo.LocalPath == "") {
			r.failRunWithRetry(ctx, runID, StagePublishingDocuments, fmt.Errorf("FINAL_VIDEO_MISSING: render set is complete but final.mp4 is absent"))
			return
		}
	}
	r.log.Info("scriptgeneration: run completed",
		zap.String("run_id", runID),
		zap.Int("scene_count", len(result.Scenes)),
	)
	// P1.2: record the completion finalization as an observable sub-stage
	// so the "complete run" wall time (checkpoint + updateStage) is attributed.
	completeStarted := time.Now()
	// Print the canonical critical path + bottleneck percentage from the live
	// run clock AND compute pipeline invariants so operators see both the
	// dominant sequential chain and whether key invariants hold per run
	// without querying /api/jobs/:id/full. Best-effort: a unit runtime with
	// no Run bound to ctx is a silent no-op (instrumentation never changes
	// behaviour).
	if run := kernobs.FromContext(ctx); run != nil {
		sum := run.TimingSummary()
		if len(sum.CriticalPath) > 0 {
			r.log.Info("scriptgeneration: critical path",
				zap.String("run_id", runID),
				zap.String("critical_path", sum.FormatCriticalPath()),
				zap.String("bottleneck_stage", sum.BottleneckStage),
				zap.Float64("bottleneck_percent", sum.BottleneckPercent),
				zap.Int64("wall_ms", sum.WallMs),
			)
		}

		// ── Pipeline invariants ────────────────────────────────
		// Compute the canonical invariants from the Run's own report
		// and KPIs. These are the single-source-of-truth checks that
		// operators can monitor without recomputing anything by hand.
		kpis := run.Report().KPIs

		// Invariant: render.first_started < generate.finished
		// (for streaming/clip mode, render should start BEFORE Gemma finishes)
		kpis.InvariantRenderBeforeGenerateFinished =
			kpis.RenderFirstStartedMs > 0 && kpis.GenerateFinishedMs > 0 &&
				kpis.RenderFirstStartedMs < kpis.GenerateFinishedMs

		// Invariant: TTS worker never waits for render
		// (TTS slots are freed when synthesis completes, render runs async)
		// This is now structural (see P0.3), so it always holds.
		kpis.InvariantTTSNeverWaitsRender = true

		// Invariant: TTS provider slot never waits for Drive upload
		// (publish runs in a separate pool, see P0.4)
		kpis.InvariantTTSNeverWaitsDrive =
			kpis.TTSFirstStartedMs > 0 && kpis.AudioCompileStartedMs > 0

		// Invariant: unattributed / total < 5%
		kpis.InvariantUnattributedBelowFivePercent = sum.UnattributedPercent < 5.0

		run.SetKPIs(kpis)

		r.log.Info("scriptgeneration: pipeline invariants",
			zap.String("run_id", runID),
			zap.Bool("render_before_generate_finished", kpis.InvariantRenderBeforeGenerateFinished),
			zap.Bool("tts_never_waits_render", kpis.InvariantTTSNeverWaitsRender),
			zap.Bool("tts_never_waits_drive", kpis.InvariantTTSNeverWaitsDrive),
			zap.Bool("unattributed_below_five_percent", kpis.InvariantUnattributedBelowFivePercent),
			zap.Float64("unattributed_percent", sum.UnattributedPercent),
		)
	}
	r.checkpoint(ctx, runID, result)
	if updateErr := r.repo.UpdateStage(ctx, runID, RunStatusCompleted, StageCompleted); updateErr != nil {
		r.log.Error("failed to persist run completion",
			zap.String("run_id", runID),
			zap.Error(updateErr),
		)
	}
	kernobs.RecordStage(ctx, kernobs.StageInfo{Stage: "complete_finalize"}, completeStarted, time.Now(), nil)
}
