package scriptgeneration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/observability"
	"go.uber.org/zap"
)

// ── Internal helpers ────────────────────────────────────────────────

// updateStage persists the stage transition.
func (r *Runner) updateStage(ctx context.Context, runID string, status RunStatus, stage Stage) error {
	if err := r.repo.UpdateStage(ctx, runID, status, stage); err != nil {
		return err
	}
	r.relayJobStage(ctx, runID, stage)
	return nil
}

// checkpoint saves the partial result to the repository.
// Errors are logged but not propagated (best-effort checkpoint).
//
// It is the SINGLE owner of the script checkpoint write-amplification
// instrumentation: every call counts as an attempt, a successful save counts as
// a write and contributes its serialized bytes, and the save wall time is
// observed. The byte counter is recorded where the payload is actually produced
// (the repository marshal), so it measures what SQLite received rather than an
// estimate.
func (r *Runner) checkpoint(ctx context.Context, runID string, result *GenerateResult) {
	observability.ScriptCheckpointAttemptTotal.Inc()
	started := time.Now()
	saveErr := r.repo.SavePartialResult(ctx, runID, result)
	if saveErr != nil {
		r.log.Warn("checkpoint save failed",
			zap.String("run_id", runID),
			zap.Error(saveErr),
		)
	} else {
		observability.ScriptCheckpointWriteTotal.Inc()
	}
	if result != nil && result.SemanticRenderBundle != nil {
		if err := persistSemanticBundleSidecar(runID, result.SemanticRenderBundle); err != nil {
			r.log.Warn("semantic bundle sidecar save failed", zap.String("run_id", runID), zap.Error(err))
		}
	}
	observability.ScriptCheckpointSeconds.Observe(time.Since(started).Seconds())
	kernobs.RecordStage(ctx, kernobs.StageInfo{Stage: "checkpoint"}, started, time.Now(), nil)
}

const partialCheckpointDebounce = 500 * time.Millisecond

// checkpointGate keeps per-unit progress durable without turning every
// scene×language completion into a full SQLite write. The phase still flushes
// one final snapshot after the fan-out, so the only intentional crash window
// is the debounce interval.
type checkpointGate struct {
	mu       sync.Mutex
	last     time.Time
	inFlight bool
}

func (g *checkpointGate) due(now time.Time) bool {
	if g == nil {
		return false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.inFlight {
		observability.ScriptCheckpointDebouncedTotal.Inc()
		return false
	}
	if g.last.IsZero() || now.Sub(g.last) >= partialCheckpointDebounce {
		g.last = now
		g.inFlight = true
		return true
	}
	observability.ScriptCheckpointDebouncedTotal.Inc()
	return false
}

func (g *checkpointGate) complete() {
	if g == nil {
		return
	}
	g.mu.Lock()
	g.inFlight = false
	g.mu.Unlock()
}

func recordCheckpointFlush() {
	observability.ScriptCheckpointFlushTotal.Inc()
}

// snapshotBufferPool reuses marshal buffers across per-unit checkpoints so
// the full-result copy does not allocate a fresh byte slice per debounced
// unit (P1-D: less allocation amplification; the copy itself is retained
// for race safety, see below).
var snapshotBufferPool = sync.Pool{New: func() any { return new(bytes.Buffer) }}

// snapshotGenerateResult creates an immutable checkpoint value. JSON is the
// durable result contract, so round-tripping it also deep-copies every map and
// slice that concurrent scene workers may otherwise mutate after the apply
// lock is released. Callers must create this snapshot while holding the
// phase's result-apply mutex; the actual repository write happens later.
//
// P1-D note: per-unit callers are already debounced (500ms + inFlight) and
// final flushes are the durable DAG boundaries, so this keeps crash-resume
// while the pool avoids a fresh allocation per debounced unit. The remaining
// full-copy cost is the race-safety price; payload bytes stay measurable via
// ScriptCheckpointBytesTotal at the repository write.
func snapshotGenerateResult(result *GenerateResult) (*GenerateResult, error) {
	if result == nil {
		return nil, nil
	}
	buf := snapshotBufferPool.Get().(*bytes.Buffer)
	buf.Reset()
	defer snapshotBufferPool.Put(buf)
	enc := json.NewEncoder(buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(result); err != nil {
		return nil, fmt.Errorf("marshal checkpoint snapshot: %w", err)
	}
	var snapshot GenerateResult
	if err := json.Unmarshal(buf.Bytes(), &snapshot); err != nil {
		return nil, fmt.Errorf("unmarshal checkpoint snapshot: %w", err)
	}
	// LocalPath is deliberately producer-only (`json:"-"`): it must never
	// enter the durable result, but the parallel render branch still needs the
	// verified local files while validating and staging the semantic plan.
	// Restore those transient locations onto the detached JSON snapshot.
	restoreOverlayPlanRuntimeAssetLocations(result.OverlayPlan, snapshot.OverlayPlan)
	for language, sourcePlan := range result.LocalizedOverlayPlans {
		restoreOverlayPlanRuntimeAssetLocations(sourcePlan, snapshot.LocalizedOverlayPlans[language])
	}
	return &snapshot, nil
}

func restoreOverlayPlanRuntimeAssetLocations(source, target *capabilityoverlay.OverlayPlan) {
	if source == nil || target == nil {
		return
	}
	// These application-only fields are excluded from the RenderingGen JSON
	// contract, but the detached PipelineGen render snapshot still needs them
	// to publish the finished artifact under its owning broker job.
	target.DriveJobID = source.DriveJobID
	target.ResultJobID = source.ResultJobID
	target.RequireDriveBeforeReturn = source.RequireDriveBeforeReturn
	if source.Source != nil && target.Source != nil {
		target.Source.LocalPath = source.Source.LocalPath
	}
	if source.Background != nil && target.Background != nil {
		for i := range source.Background.AssetRefs {
			if i < len(target.Background.AssetRefs) && source.Background.AssetRefs[i].AssetID == target.Background.AssetRefs[i].AssetID {
				target.Background.AssetRefs[i].LocalPath = source.Background.AssetRefs[i].LocalPath
			}
		}
	}
	for i := range source.Items {
		if i >= len(target.Items) || source.Items[i].ID != target.Items[i].ID {
			continue
		}
		for j := range source.Items[i].AssetRefs {
			if j < len(target.Items[i].AssetRefs) && source.Items[i].AssetRefs[j].AssetID == target.Items[i].AssetRefs[j].AssetID {
				target.Items[i].AssetRefs[j].LocalPath = source.Items[i].AssetRefs[j].LocalPath
			}
		}
		if source.Items[i].SoundEffect != nil && target.Items[i].SoundEffect != nil &&
			source.Items[i].SoundEffect.AssetRef.AssetID == target.Items[i].SoundEffect.AssetRef.AssetID {
			target.Items[i].SoundEffect.AssetRef.LocalPath = source.Items[i].SoundEffect.AssetRef.LocalPath
		}
	}
}

// observeCheckpointWait records how long a worker waited for the per-unit apply
// lock before checkpointing. The stage timer never covered this wait, so
// contention between the scene×language workers was invisible in every report;
// it is measured at the call site that owns the lock.
func observeCheckpointWait(started time.Time) {
	observability.ScriptCheckpointWaitSeconds.Observe(time.Since(started).Seconds())
}

// persistSemanticBundleSidecar keeps the audit bundle beside the job's other
// generated artifacts. The write is atomic so a reader never observes a
// partially serialized semantic contract during retries/restarts.
func persistSemanticBundleSidecar(runID string, bundle any) error {
	if runID == "" || bundle == nil {
		return nil
	}
	outDir := filepath.Join(os.TempDir(), "pipelinegen", "jobs", runID, "output")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return fmt.Errorf("mkdir semantic bundle output: %w", err)
	}
	raw, err := json.MarshalIndent(bundle, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal semantic bundle: %w", err)
	}
	raw = append(raw, '\n')
	tmp, err := os.CreateTemp(outDir, ".semantic_bundle-*.tmp")
	if err != nil {
		return fmt.Errorf("create semantic bundle temp: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o644); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write semantic bundle: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close semantic bundle: %w", err)
	}
	if err := os.Rename(tmpName, filepath.Join(outDir, "semantic_bundle.json")); err != nil {
		return fmt.Errorf("commit semantic bundle: %w", err)
	}
	return nil
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
	if pending, ok := ctx.Value(deferredAudioFailureKey{}).(*deferredAudioFailure); ok {
		pending.cause = err
		return
	}
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
	if nextAttempt <= MaxRetries && !isPermanentMediaPreflightFailure(err) {
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

// stageSkipped reports whether a stage is already behind the resume point.
//
// It lives with the other resume/lifecycle semantics (ResumeFrom, StageIndex,
// StageIsTerminal) rather than in a file of its own: it is the single predicate
// every phase consults before re-running work on a retry-from-checkpoint, so
// the resume rule has one home.
func stageSkipped(resumeIdx int, stage Stage) bool {
	return resumeIdx >= 0 && StageIndex(stage) < resumeIdx
}

// ── Job stage relay (merged from runner_job_stage.go) ──
//
// Merged here to keep the registered scripts hotspot at its committed
// baseline: stage relay is runner lifecycle. No behaviour change.

// jobStageState tracks which job-level stage is currently open for a durable
// run so the next transition can close it.
type jobStageState struct {
	jobID string
	open  job.StageName
}

// SetJobStageSink wires the durable job stage-row store. Optional: nil keeps
// the run ledger as the only progress record.
func (r *Runner) SetJobStageSink(sink job.JobStageStatusStore) {
	if r != nil {
		r.jobStageSink = sink
	}
}

// bindJobStage associates a durable run with the job whose stage rows it feeds.
func (r *Runner) bindJobStage(runID, jobID string) {
	if r == nil || r.jobStageSink == nil || runID == "" || jobID == "" {
		return
	}
	r.jobStages.Store(runID, &jobStageState{jobID: jobID})
}

// closeJobStage closes the job stage still open when the execution returns.
// The run's terminal status decides the outcome: COMPLETED closes it as
// completed, anything else as failed. Best-effort, like every relay write.
func (r *Runner) closeJobStage(ctx context.Context, runID string) {
	if r == nil || r.jobStageSink == nil {
		return
	}
	raw, ok := r.jobStages.Load(runID)
	if !ok {
		return
	}
	state := raw.(*jobStageState)
	if state.open == "" {
		return
	}
	status := job.StageFailed
	if run, err := r.repo.Get(ctx, runID); err == nil && run != nil && run.Status == RunStatusCompleted {
		status = job.StageCompleted
	}
	r.writeJobStage(ctx, state.jobID, state.open, status, time.Now().UTC())
	state.open = ""
}

// unbindJobStage removes the association once the execution has returned.
func (r *Runner) unbindJobStage(runID string) {
	if r == nil {
		return
	}
	r.jobStages.Delete(runID)
}

// jobStageForRunStage maps a run-ledger stage onto the job stage vocabulary.
// Stages without a job-level equivalent return false and are not relayed.
func jobStageForRunStage(stage Stage) (job.StageName, bool) {
	switch stage {
	case StageNormalizing, StagePreflight, StageGeneratingSceneText:
		return job.StageScript, true
	case StageTranslatingScenes:
		return job.StageTranslation, true
	case StageGeneratingVoiceovers:
		return job.StageVoiceover, true
	}
	return "", false
}

// relayJobStage writes the job stage row for a run-ledger transition. It is
// best-effort: a failed row write is logged and never fails the run.
func (r *Runner) relayJobStage(ctx context.Context, runID string, stage Stage) {
	if r == nil || r.jobStageSink == nil {
		return
	}
	raw, ok := r.jobStages.Load(runID)
	if !ok {
		return
	}
	state := raw.(*jobStageState)
	closing := stage == StageCoreReady || stage == StageCompleted
	next, opens := jobStageForRunStage(stage)
	if !opens && !closing {
		return
	}
	now := time.Now().UTC()
	if state.open != "" && (closing || (opens && next != state.open)) {
		r.writeJobStage(ctx, state.jobID, state.open, job.StageCompleted, now)
		state.open = ""
	}
	if opens {
		r.writeJobStage(ctx, state.jobID, next, job.StageRunning, now)
		state.open = next
	}
}

func (r *Runner) writeJobStage(ctx context.Context, jobID string, stage job.StageName, status job.StageStatus, at time.Time) {
	rec := job.JobStageStatus{JobID: jobID, Stage: stage, Status: status, UpdatedAt: at}
	if err := r.jobStageSink.UpsertJobStageStatus(ctx, rec); err != nil && r.log != nil {
		r.log.Warn("job stage status write failed",
			zap.String("job_id", jobID),
			zap.String("stage", string(stage)),
			zap.Error(err))
	}
}
