package scriptgeneration

// final_job_split.go is the runner half of the split remote handoff. The base
// PREPARE runs as a sibling of the overlay render, so the Master can queue and
// prefetch the source job while the GPU is still composing overlays. The
// overlay FINALIZE stays on the final-job phase, after the render is certified.

import (
	"context"
	"errors"
	"strings"
	"time"

	"go.uber.org/zap"
)

// finalJobSplitOutcome is the early PREPARE result joined at the overlay render
// boundary. started/finished are the interval used to attribute the overlap.
type finalJobSplitOutcome struct {
	receipt  RemoteFinalJobResult
	err      error
	started  time.Time
	finished time.Time
}

// startFinalJobSplitPrepare launches the early base PREPARE. It returns nil
// whenever the serial handoff must stay authoritative: final job disabled,
// adapter without the split contract, no overlay sibling to overlap, a
// non-replayable receipt, or a result that cannot be snapshotted.
func (r *Runner) startFinalJobSplitPrepare(ctx context.Context, runID string, req GenerateRequest, result *GenerateResult) chan finalJobSplitOutcome {
	if r == nil || !req.FinalJob || result == nil || result.FinalAudio == nil {
		return nil
	}
	preparer, ok := r.finalJobSubmitter.(FinalJobSplitPreparer)
	if !ok || !preparer.FinalJobSplitEnabled() {
		return nil
	}
	// A PREPARED job already exists: FINALIZE is the only legal next step.
	// A terminal receipt is handled by the serial attempt rotation, and a
	// plain PREPARE_INTENT (no split marker) is the serial reconciliation path.
	if receipt := result.RemoteFinalJob; receipt != nil {
		if finalJobPreparedID(receipt) != "" || finalJobReceiptSucceeded(receipt) || finalJobReceiptTerminal(receipt) {
			return nil
		}
		if !(strings.EqualFold(strings.TrimSpace(receipt.Status), "PREPARE_INTENT") && receipt.Split) {
			return nil
		}
	}
	snapshot, err := snapshotGenerateResult(result)
	if err != nil {
		if r.log != nil {
			r.log.Warn("final_job split PREPARE skipped: result snapshot failed",
				zap.String("run_id", runID), zap.Error(err))
		}
		return nil
	}
	if result.RemoteFinalJob == nil {
		// Durable intent before the network: a process restart can replay the
		// SAME base plan (and therefore the same idempotency key) instead of
		// guessing whether a PREPARE ever left this host.
		intent := RemoteFinalJobResult{Status: "PREPARE_INTENT", Attempt: 1, Split: true}
		result.RemoteFinalJob = &intent
		r.checkpoint(ctx, runID, result)
	}
	outcome := make(chan finalJobSplitOutcome, 1)
	started := time.Now()
	go func() {
		receipt, err := preparer.PrepareFinalJobSource(ctx, runID, req, snapshot)
		outcome <- finalJobSplitOutcome{receipt: receipt, err: err, started: started, finished: time.Now()}
	}()
	if r.log != nil {
		r.log.Info("final_job split PREPARE started alongside the overlay render",
			zap.String("run_id", runID),
			zap.Int("attempt", finalJobSplitAttempt(result)))
	}
	return outcome
}

// applyFinalJobSplitPrepare records the early PREPARE before the final-job
// phase inspects the result. A failure is never fatal here: no job exists, so
// the serial path can still build and submit the full handoff. A success is
// durable immediately, because the next attempt must FINALIZE this job instead
// of asking the Master for another render.
func (r *Runner) applyFinalJobSplitPrepare(ctx context.Context, runID string, result *GenerateResult, outcome finalJobSplitOutcome, overlayFinishedAt time.Time) {
	if result == nil {
		return
	}
	if outcome.err != nil {
		if r.log != nil {
			r.log.Warn("final_job split PREPARE failed; falling back to the serial handoff",
				zap.String("run_id", runID),
				zap.String("remote_job_id", outcome.receipt.JobID),
				zap.Error(outcome.err))
		}
		if strings.TrimSpace(outcome.receipt.JobID) == "" {
			if errors.Is(outcome.err, ErrFinalJobSplitPrepareNotSent) {
				// The request never reached the Master: discard the split intent
				// so the serial handoff owns this attempt without replaying it.
				if receipt := result.RemoteFinalJob; receipt != nil && receipt.Split && strings.EqualFold(strings.TrimSpace(receipt.Status), "PREPARE_INTENT") {
					result.RemoteFinalJob = nil
					r.checkpoint(ctx, runID, result)
				}
				return
			}
			// The response was lost after a possible send. Keep the durable
			// intent: submitFinalJob replays the SAME base plan/key before it
			// decides to fall back, so the Master can reconcile instead of
			// starting a second render.
			if r.log != nil {
				r.log.Warn("final_job split PREPARE outcome is ambiguous; keeping the base intent for same-key replay",
					zap.String("run_id", runID), zap.Error(outcome.err))
			}
			return
		}
	}
	receipt := outcome.receipt
	if strings.TrimSpace(receipt.JobID) == "" {
		return
	}
	receipt.Split = true
	if receipt.Attempt < 1 {
		receipt.Attempt = finalJobSplitAttempt(result)
	}
	overlap := time.Duration(0)
	if outcome.finished.After(outcome.started) {
		end := outcome.finished
		if !overlayFinishedAt.IsZero() && overlayFinishedAt.Before(end) {
			end = overlayFinishedAt
		}
		if end.After(outcome.started) {
			overlap = end.Sub(outcome.started)
		}
	}
	if receipt.Timing == nil {
		receipt.Timing = &RemoteFinalJobTiming{}
	}
	receipt.Timing.SplitPrepareMS = outcome.finished.Sub(outcome.started).Milliseconds()
	receipt.Timing.SplitOverlapMS = overlap.Milliseconds()
	result.RemoteFinalJob = &receipt
	recordFinalJobReport(ctx, &receipt)
	r.checkpoint(ctx, runID, result)
	if r.log != nil {
		r.log.Info("final_job split PREPARE accepted",
			zap.String("run_id", runID),
			zap.String("remote_job_id", receipt.JobID),
			zap.String("remote_status", receipt.Status),
			zap.Int64("split_prepare_ms", receipt.Timing.SplitPrepareMS),
			zap.Int64("split_overlap_ms", receipt.Timing.SplitOverlapMS))
	}
}

func finalJobSplitAttempt(result *GenerateResult) int {
	if result == nil || result.RemoteFinalJob == nil || result.RemoteFinalJob.Attempt < 1 {
		return 1
	}
	return result.RemoteFinalJob.Attempt
}
