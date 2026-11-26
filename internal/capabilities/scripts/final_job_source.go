package scriptgeneration

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/finaljob"
	"go.uber.org/zap"
)

// Source preparation for the final job: the local preflight and the durable
// receipt the runner reads back. Stock bytes never enter this path; the Master
// downloads and processes the Drive source.

// enforceFinalJobPreflight is the deterministic pre-enqueue gate: predictable
// remote scene rejection classes fail locally before the Master PREPARE exists.
func (r *Runner) finalJobDurableReceipt(ctx context.Context, runID string) *RemoteFinalJobResult {
	if r.repo == nil {
		return nil
	}
	run, err := r.repo.Get(ctx, runID)
	if err != nil || run == nil || run.Result == nil {
		return nil
	}
	return run.Result.RemoteFinalJob
}

// submitFinalJob drives the optional remote render handoff. It is attach-first
// on purpose: an attempt that finds a pending receipt resumes THAT job, because
// a second PREPARE asks the Master for a second render of work it is already
// doing. Only a run with no receipt submits, and a run whose receipt is already
// SUCCEEDED is skipped entirely (a later phase failed the run after the render
// finished).
func (r *Runner) submitFinalJob(ctx context.Context, runID string, req GenerateRequest, result *GenerateResult) bool {
	if r.finalJobSubmitter == nil {
		r.failRunWithRetry(ctx, runID, StagePublishingDocuments, fmt.Errorf("final_job=true but remote final-job submitter is not configured"))
		return false
	}
	r.auditFinalJobArtifactReuse(runID, req, result)
	receipt := (*RemoteFinalJobResult)(nil)
	if result != nil {
		receipt = result.RemoteFinalJob
	}
	if receipt == nil {
		receipt = r.finalJobDurableReceipt(ctx, runID)
		if receipt != nil && result != nil {
			result.RemoteFinalJob = receipt
		}
	}
	if finalJobReceiptSucceeded(receipt) {
		recordFinalJobReport(ctx, receipt)
		if r.log != nil {
			r.log.Info("remote final job already completed by an earlier attempt; reusing its receipt",
				zap.String("run_id", runID), zap.String("remote_job_id", receipt.JobID))
		}
		return true
	}
	attempt := 1
	if receipt != nil {
		attempt = receipt.Attempt
		if attempt < 1 {
			attempt = 1
		}
		if finalJobReceiptTerminal(receipt) {
			if result != nil {
				result.RemoteFinalJobHistory = append(result.RemoteFinalJobHistory, *receipt)
			}
			attempt++
		}
		// A receipt without a job id is a durable PREPARE intent. Reuse its
		// attempt key so a lost PREPARE response reconciles to the same lease.
	}
	if receipt != nil && strings.EqualFold(strings.TrimSpace(receipt.Status), "PREPARE_INTENT") && receipt.Split && strings.TrimSpace(receipt.JobID) == "" {
		// The early PREPARE failed after a possible send. Replay the SAME base
		// plan and attempt key once before falling back: if the Master accepted
		// the first request, this reconciles to that job instead of creating a
		// second remote render. A pre-send failure is safe to discard and lets
		// the serial handoff own the attempt.
		if preparer, ok := r.finalJobSubmitter.(FinalJobSplitPreparer); ok && result != nil {
			remote, err := preparer.PrepareFinalJobSource(ctx, runID, req, result)
			remote.Attempt = attempt
			remote.Timing = receipt.Timing.Merge(remote.Timing)
			if err != nil {
				if errors.Is(err, ErrFinalJobSplitPrepareNotSent) {
					if r.log != nil {
						r.log.Warn("final_job split PREPARE replay was not sent; using the serial handoff",
							zap.String("run_id", runID), zap.Error(err))
					}
					result.RemoteFinalJob = nil
					if saveErr := r.repo.SavePartialResult(ctx, runID, result); saveErr != nil {
						return r.settleFinalJob(ctx, runID, result, RemoteFinalJobResult{}, 0, fmt.Errorf("%w: clear unsent split intent: %w", ErrFinalJobReceiptCheckpoint, saveErr))
					}
					receipt = nil
				} else {
					// Still ambiguous: fail the attempt with the intent durable so
					// the retry replays the same base plan/key again.
					return r.settleFinalJob(ctx, runID, result, remote, 0, err)
				}
			} else if strings.TrimSpace(remote.JobID) == "" {
				return r.settleFinalJob(ctx, runID, result, remote, 0, fmt.Errorf("remote final job split replay returned no job id"))
			} else {
				receipt = &remote
				result.RemoteFinalJob = receipt
				if err := r.repo.SavePartialResult(ctx, runID, result); err != nil {
					return r.settleFinalJob(ctx, runID, result, remote, 0, fmt.Errorf("%w: checkpoint replayed split PREPARE: %w", ErrFinalJobReceiptCheckpoint, err))
				}
				if finalJobReceiptSucceeded(receipt) {
					recordFinalJobReport(ctx, receipt)
					return true
				}
				if finalJobReceiptTerminal(receipt) {
					result.RemoteFinalJobHistory = append(result.RemoteFinalJobHistory, *receipt)
					result.RemoteFinalJob = nil
					receipt = nil
					attempt++
				}
			}
		}
	}
	if jobID := finalJobPreparedID(receipt); jobID != "" {
		finalizer, ok := r.finalJobSubmitter.(FinalJobSplitFinalizer)
		if !ok {
			r.failRunWithRetry(ctx, runID, StagePublishingDocuments, fmt.Errorf(
				"remote final job %s was PREPARED with a split handoff but the wired submitter cannot FINALIZE it (%T does not implement FinalJobSplitFinalizer)", jobID, r.finalJobSubmitter))
			return false
		}
		if result == nil {
			r.failRunWithRetry(ctx, runID, StagePublishingDocuments, fmt.Errorf("remote final job %s was PREPARED but the run has no result to finalize it with", jobID))
			return false
		}
		remote, err := finalizer.FinalizeFinalJobSource(ctx, jobID, runID, req, result)
		remote.Attempt = attempt
		if strings.TrimSpace(remote.JobID) == "" {
			remote.JobID = jobID
		}
		// The PREPARE receipt owns the base payload/prepare timing; the
		// FINALIZE receipt owns only its own overlay supplement. Merge keeps
		// both sides instead of replacing the early handoff measurement.
		remote.Timing = receipt.Timing.Merge(remote.Timing)
		if err != nil {
			return r.settleFinalJob(ctx, runID, result, remote, 0, err)
		}
		receipt = &remote
		result.RemoteFinalJob = receipt
		if err := r.repo.SavePartialResult(ctx, runID, result); err != nil {
			return r.settleFinalJob(ctx, runID, result, remote, 0, fmt.Errorf("%w: checkpoint split FINALIZE %s: %w", ErrFinalJobReceiptCheckpoint, jobID, err))
		}
	}
	if jobID := finalJobPendingID(receipt); jobID != "" {
		attacher, ok := r.finalJobSubmitter.(FinalJobAttacher)
		if !ok {
			// Fail closed: the wired submitter cannot wait on the job this run
			// already started, and submitting again would duplicate a render the
			// Master is already performing.
			r.failRunWithRetry(ctx, runID, StagePublishingDocuments, fmt.Errorf(
				"remote final job %s is still in flight but the wired submitter cannot resume it (%T does not implement FinalJobAttacher)", jobID, r.finalJobSubmitter))
			return false
		}
		if r.log != nil {
			r.log.Info("resuming remote final job",
				zap.String("run_id", runID), zap.String("remote_job_id", jobID),
				zap.Int("yields", receipt.Yields), zap.Bool("wait_to_completion", receipt.Yields > 0))
		}
		remote, err := attacher.AttachFinalJob(ctx, jobID, receipt.Yields > 0)
		if err != nil && ctx.Err() != nil {
			// A local cancel interrupted the wait. The Master may have finished
			// the render: ask before turning the run into CANCELLED.
			remote, err = reconcileInterruptedFinalJob(ctx, attacher, jobID)
		}
		remote.Attempt = receipt.Attempt
		// The durable receipt already carries the locally measured handoff (and,
		// on a resumed job, possibly earlier Master phases); keep them when the
		// attach result only reports its own side. Keep the split marker too: the
		// post-FINALIZE attach receipt is the same late-overlay job.
		remote.Split = remote.Split || receipt.Split
		remote.Timing = receipt.Timing.Merge(remote.Timing)
		if err == nil && ctx.Err() != nil {
			settleCtx, settleCancel := finalJobSettleContext(ctx)
			defer settleCancel()
			return r.settleFinalJob(settleCtx, runID, result, remote, receipt.Yields, nil)
		}
		return r.settleFinalJob(ctx, runID, result, remote, receipt.Yields, err)
	}
	// Burn translated captions into each protected intro/mid/outro clip before
	// handing the assembled timeline to the remote Master. The fixed-media
	// render lane works per clip and language; stock never enters that lane.
	if err := r.ensureFinalJobFixedMediaSubtitles(ctx, runID, req, result); err != nil {
		r.failRunWithRetry(ctx, runID, StagePublishingDocuments, err)
		return false
	}
	if starter, ok := r.finalJobSubmitter.(FinalJobStarter); ok {
		if result == nil {
			return r.settleFinalJob(ctx, runID, result, RemoteFinalJobResult{}, 0, fmt.Errorf("remote final job has no result to checkpoint before PREPARE"))
		}
		intent := RemoteFinalJobResult{Status: "PREPARE_INTENT", Attempt: attempt}
		result.RemoteFinalJob = &intent
		if err := r.repo.SavePartialResult(ctx, runID, result); err != nil {
			return r.settleFinalJob(ctx, runID, result, intent, 0, fmt.Errorf("%w: checkpoint PREPARE intent: %w", ErrFinalJobReceiptCheckpoint, err))
		}
		remote, err := starter.StartFinalJob(ctx, runID, req, result)
		remote.Attempt = attempt
		if err != nil {
			return r.settleFinalJob(ctx, runID, result, remote, 0, err)
		}
		if strings.TrimSpace(remote.JobID) == "" {
			return r.settleFinalJob(ctx, runID, result, remote, 0, fmt.Errorf("remote final job starter returned no job id"))
		}
		remote.Yields = 0
		result.RemoteFinalJob = &remote
		if err := r.repo.SavePartialResult(ctx, runID, result); err != nil {
			return r.settleFinalJob(ctx, runID, result, remote, 0, fmt.Errorf("checkpoint remote final job %s before attach: %w", remote.JobID, err))
		}
		attacher, ok := r.finalJobSubmitter.(FinalJobAttacher)
		if !ok {
			return r.settleFinalJob(ctx, runID, result, remote, 0, fmt.Errorf("remote final job starter cannot attach to job %s", remote.JobID))
		}
		attached, err := attacher.AttachFinalJob(ctx, remote.JobID, false)
		if err != nil && ctx.Err() != nil {
			attached, err = reconcileInterruptedFinalJob(ctx, attacher, remote.JobID)
		}
		attached.Attempt = attempt
		// StartFinalJob measured the local handoff; the attach result knows the
		// Master phases. The persisted receipt must carry both.
		attached.Timing = remote.Timing.Merge(attached.Timing)
		if err == nil && ctx.Err() != nil {
			settleCtx, settleCancel := finalJobSettleContext(ctx)
			defer settleCancel()
			return r.settleFinalJob(settleCtx, runID, result, attached, 0, nil)
		}
		return r.settleFinalJob(ctx, runID, result, attached, 0, err)
	}
	remote, err := r.finalJobSubmitter.SubmitFinalJob(ctx, runID, req, result)
	return r.settleFinalJob(ctx, runID, result, remote, 0, err)
}

// A clip-only run has no stock folder to chunk: its video IS the certified
// localized render of each scene. These helpers select that render and project
// it into the runtime asset reference. The source clip in the media library is
// never sent: it still carries the unmodified picture that this pipeline
// replaces (background, watermark, burnt subtitles).
//
// Moved verbatim from final_job_payload.go (2026-09-28, 731 → 581) to satisfy
// the strict 600-LOC forward-prevention gate (godlike/08) without changing
// behaviour. The payload BUILDER stays in final_job_payload.go; the certified
// render-selection family is one cohesive unit and lives next to the remote
// final-job lifecycle that consumes it.

// finalJobRenderLanguage resolves the single language whose certified renders
// feed the remote video. An empty language means the run produced no certified
// render at all (a stock-only run), which is not an error by itself. A run with
// renders in several languages and none in the source language is ambiguous and
// reports an error instead of picking one.

// --- stale remote receipt classification ---

// ErrFinalJobStaleReceipt marks a remote final-job identity the Master will
// never accept another FINALIZE for: a canceled preparation, or a lease held
// elsewhere (`job_already_leased`). The identity lives on the Master, so
// clearing the local receipt does not make it reusable. The receipt carrying
// it is archived as TERMINAL (see settleFinalJob), and the next attempt rotates
// the idempotency key (see finalJobAttemptIdempotencyKey) to prepare a fresh
// remote job instead of replaying the dead one.
func (r *Runner) auditFinalJobArtifactReuse(runID string, req GenerateRequest, result *GenerateResult) {
	if r == nil || r.log == nil || result == nil {
		return
	}
	receipt := finaljob.RenderReceiptFor(req, result)
	scenes := len(result.Scenes)
	localizedRenders := len(result.LocalizedRenders)
	overlayItems, overlayRenders := 0, 0
	if result.OverlayPlan != nil {
		overlayItems = len(result.OverlayPlan.Items)
	}
	overlayRenders = len(receipt.Items) + len(receipt.Batches)
	finalAudioMS := int64(0)
	finalAudioID := ""
	if result.FinalAudio != nil {
		finalAudioMS = result.FinalAudio.DurationMS
		finalAudioID = strings.TrimSpace(result.FinalAudio.AssetID)
	}
	r.log.Info("final_job artifact reuse",
		zap.String("run_id", runID),
		zap.Int("scenes", scenes),
		zap.String("final_audio_asset_id", finalAudioID),
		zap.Int64("final_audio_ms", finalAudioMS),
		zap.Int("overlay_items", overlayItems),
		zap.Int("overlay_renders", overlayRenders),
		zap.Bool("subtitles_enabled", receipt.Subtitles != nil && receipt.Subtitles.Enabled),
		zap.Int("localized_renders", localizedRenders))
}

// finalJobAudioInput projects the canonical mix onto the remote final-job
// handoff: TTS, BGM and SFX pass through, and the original clip audio is
// restored at FinalJobRestoredClipGainDB instead of being dropped. History:
// this projection used to remove every AudioClip intent, which delivered
// videos with silent clip audio (2026-09-30 Milton incident) — the source
// speech was audible only in the local lane while the remote master mixed
// narration + music alone. The restored track ducks like any clip track:
// applyMixPolicy only deepens non-protected events toward the active duck
// gain while speech plays.
