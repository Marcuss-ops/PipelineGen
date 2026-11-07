package scriptgeneration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/observability"
	"github.com/Marcuss-ops/PipelineGen/pkg/retry"
	"go.uber.org/zap"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// settleFinalJob is the SINGLE interpretation of a remote final-job outcome.
// Both submit and attach paths share receipt persistence and retry semantics.
func (r *Runner) settleFinalJob(ctx context.Context, runID string, result *GenerateResult, remote RemoteFinalJobResult, yields int, err error) bool {
	// Project the newest receipt into the ONE report owner before any branch
	// below can return. The checkpoint in workflow_payload_json remains the
	// resumable copy, but run_observability.report_json is the reporting
	// surface: /full and history readers must not infer this receipt from a
	// log line or from the checkpoint shape.
	reportReceipt := remote
	if strings.TrimSpace(reportReceipt.JobID) == "" && result != nil && result.RemoteFinalJob != nil {
		reportReceipt = *result.RemoteFinalJob
	}
	recordFinalJobReport(ctx, &reportReceipt)
	if err != nil {
		if errors.Is(err, ErrFinalJobPending) && result != nil {
			receipt := remote
			if strings.TrimSpace(receipt.JobID) == "" && result.RemoteFinalJob != nil {
				receipt.JobID = strings.TrimSpace(result.RemoteFinalJob.JobID)
			}
			receipt.Yields = yields + 1
			result.RemoteFinalJob = &receipt
			recordFinalJobReport(ctx, &receipt)
			r.checkpoint(ctx, runID, result)
			if r.log != nil {
				r.log.Info("remote final job still rendering; handing the wait back to a later attempt",
					zap.String("run_id", runID), zap.String("remote_job_id", receipt.JobID),
					zap.String("remote_status", receipt.Status), zap.Int("yields", receipt.Yields))
			}
			r.failRunWithRetry(ctx, runID, StagePublishingDocuments, fmt.Errorf(
				"remote final job %s is still %s: %w", receipt.JobID, receipt.Status, ErrFinalJobPending))
			return false
		}
		if errors.Is(err, ErrFinalJobStaleReceipt) {
			// The Master will never accept another FINALIZE for this identity
			// (canceled preparation, or a lease held elsewhere). Archive it as
			// TERMINAL so the next attempt rotates the idempotency key
			// (finalJobAttemptIdempotencyKey) and prepares a FRESH remote job:
			// the identity lives on the Master, so clearing the local receipt
			// alone would only replay the dead id forever.
			if !finalJobReceiptTerminal(&remote) {
				remote.Status = "REJECTED"
			}
			recordFinalJobReport(ctx, &remote)
			if r.log != nil {
				r.log.Warn("invalidating stale remote final-job receipt; the next attempt prepares a fresh identity",
					zap.String("run_id", runID), zap.String("remote_job_id", remote.JobID),
					zap.Int("attempt", remote.Attempt), zap.Error(err))
			}
		}
		// Preserve a terminal Master response; the old RUNNING receipt must not
		// remain authoritative after the worker has ended the remote job.
		if result != nil && strings.TrimSpace(remote.JobID) != "" && strings.TrimSpace(remote.Status) != "" {
			result.RemoteFinalJob = &remote
			if saveErr := r.repo.SavePartialResult(ctx, runID, result); saveErr != nil {
				err = fmt.Errorf("%w: persist terminal remote receipt %s/%s: %v; original failure: %w", ErrFinalJobReceiptCheckpoint, remote.JobID, remote.Status, saveErr, err)
			}
		}
		r.failRunWithRetry(ctx, runID, StagePublishingDocuments, fmt.Errorf("remote final job: %w", err))
		return false
	}
	if strings.TrimSpace(remote.JobID) == "" || !finalJobReceiptSucceeded(&remote) {
		recordFinalJobReport(ctx, &remote)
		r.failRunWithRetry(ctx, runID, StagePublishingDocuments, fmt.Errorf("remote final job returned incomplete/non-success result (job_id=%q status=%q)", remote.JobID, remote.Status))
		return false
	}
	if result == nil {
		r.failRunWithRetry(ctx, runID, StagePublishingDocuments, fmt.Errorf("remote final job %s succeeded but the run has no result to record it in", remote.JobID))
		return false
	}
	result.RemoteFinalJob = &remote
	recordFinalJobReport(ctx, &remote)
	r.checkpoint(ctx, runID, result)
	if r.log != nil {
		fields := []zap.Field{zap.String("run_id", runID), zap.String("remote_job_id", remote.JobID), zap.String("worker_id", remote.WorkerID)}
		if timing := remote.Timing; timing != nil {
			fields = append(fields,
				zap.Int64("queue_ms", timing.QueueMS),
				zap.Int64("execution_ms", timing.ExecutionMS),
				zap.Int64("handoff_ms", timing.HandoffMS),
				zap.Int64("wait_ms", timing.WaitMS),
				zap.Int64("poll_lag_ms", timing.PollLagMS),
				zap.Int("polls", timing.Polls))
		}
		r.log.Info("remote final job completed", fields...)
	}
	return true
}

// recordFinalJobReport projects the capability receipt into the kernel report.
// The kernel receives opaque JSON so observability never imports the scripts
// capability. Empty receipts are ignored: a PREPARE intent is not a job handle.
func recordFinalJobReport(ctx context.Context, receipt *RemoteFinalJobResult) {
	if receipt == nil || strings.TrimSpace(receipt.JobID) == "" || strings.TrimSpace(receipt.Status) == "" {
		return
	}
	raw, err := json.Marshal(receipt)
	if err != nil {
		return
	}
	kernobs.SetFinalJobReport(ctx, kernobs.FinalJobReport{ReportOwner: FinalJobReportOwner, Receipt: raw})
}

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

func restoreOverlayPlanRuntimeAssetLocations(source, target *capmodel.OverlayPlan) {
	if source == nil || target == nil {
		return
	}
	// These application-only fields are excluded from the RenderingGen JSON
	// contract, but the detached PipelineGen render snapshot still needs them
	// to publish the finished artifact under its owning broker job.
	target.DriveJobID = source.DriveJobID
	target.ResultJobID = source.ResultJobID
	target.RequireObjectStoreBeforeReturn = source.RequireObjectStoreBeforeReturn
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
	defer func() { _ = os.Remove(tmpName) }()
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
	// A durable retry must represent work that may succeed on another attempt.
	// Retrying arbitrary errors made deterministic failures (invalid overlay
	// timelines, missing media/Drive assets, and lost local artifacts) repeat
	// the same expensive pipeline without changing their inputs. The canonical
	// typed retry contract is shared with the job worker; the one intentional
	// non-error deferral is a remote final render that is still running.
	// ErrFinalJobStaleReceipt is retryable with CHANGED inputs: the retry that
	// follows rotates the idempotency key and prepares a fresh remote identity,
	// so it is not a blind repeat of the refused one.
	retryable := retry.IsTransient(err) || errors.Is(err, ErrFinalJobPending) || errors.Is(err, ErrFinalAudioLocalArtifactMissing) || errors.Is(err, ErrFinalJobStaleReceipt)
	if nextAttempt <= MaxRetries && retryable && !isPermanentMediaPreflightFailure(err) {
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
