package scriptgeneration

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// finalJobCancelReconcileBudget bounds the detached work done after a local
// cancel. It is deliberately short: the run is already cancelled, so the only
// purposes are one status read, at most one cancel, and a re-read when the
// Master reports the job finished first.
const finalJobCancelReconcileBudget = 30 * time.Second

// reconcileInterruptedFinalJob runs when a local cancel interrupts the attach.
// The render outlives the local job, so the decision cannot be "CANCELLED"
// before asking the Master:
//
//   - SUCCEEDED: download and adopt the artifact as the run's result;
//   - still active: stop it remotely so the two sides cannot diverge;
//   - 409 on cancel (finished in the meantime): re-read once and adopt.
//
// A nil error means the returned receipt is SUCCEEDED and the caller must
// settle it as the result, not as a cancellation.
func reconcileInterruptedFinalJob(ctx context.Context, attacher FinalJobAttacher, jobID string) (RemoteFinalJobResult, error) {
	id := strings.TrimSpace(jobID)
	if id == "" {
		return RemoteFinalJobResult{}, fmt.Errorf("reconcile final job after local cancel: empty job id")
	}
	stater, ok := attacher.(FinalJobStater)
	if !ok {
		return RemoteFinalJobResult{JobID: id}, fmt.Errorf(
			"reconcile final job %s after local cancel: the wired submitter cannot read Master state", id)
	}
	// The run context is already cancelled: detach from it or the status read
	// would fail before reaching the Master.
	reconcileCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), finalJobCancelReconcileBudget)
	defer cancel()

	remote, err := stater.FinalJobStatus(reconcileCtx, id)
	if err == nil && strings.EqualFold(strings.TrimSpace(remote.Status), "SUCCEEDED") {
		return adoptInterruptedFinalJob(reconcileCtx, attacher, remote)
	}
	if err != nil && !errors.Is(err, ErrFinalJobPending) {
		// FAILED/CANCELLED/REJECTED: nothing to adopt and no cancel to send.
		return remote, err
	}
	canceller, ok := attacher.(FinalJobCanceller)
	if !ok {
		return remote, fmt.Errorf(
			"reconcile final job %s after local cancel: still %s and the submitter cannot cancel it", id, remote.Status)
	}
	if cancelErr := canceller.CancelFinalJob(reconcileCtx, id); cancelErr != nil {
		if !errors.Is(cancelErr, ErrFinalJobCancelRejected) {
			return remote, cancelErr
		}
		// The job became terminal between the poll and the cancel. A SUCCEEDED
		// artifact must be adopted, not reported as a cancellation.
		remote, err = stater.FinalJobStatus(reconcileCtx, id)
		if err == nil && strings.EqualFold(strings.TrimSpace(remote.Status), "SUCCEEDED") {
			return adoptInterruptedFinalJob(reconcileCtx, attacher, remote)
		}
		if err != nil {
			return remote, err
		}
	}
	return remote, fmt.Errorf("remote final job %s stopped after local cancel (last status %s)", id, remote.Status)
}

// finalJobSettleContext survives a local cancel only for the adopted branch:
// persisting a verified artifact and its SUCCEEDED receipt must not be aborted
// by the cancellation that lost the race. When the run is not cancelled the
// original context is returned unchanged.
func finalJobSettleContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx.Err() == nil {
		return ctx, func() {}
	}
	return context.WithTimeout(context.WithoutCancel(ctx), finalJobCancelReconcileBudget)
}

// adoptInterruptedFinalJob downloads and binds the artifact of a render the
// Master already completed, through the same attach path a resume uses so the
// artifact is verified and recorded exactly once.
func adoptInterruptedFinalJob(ctx context.Context, attacher FinalJobAttacher, remote RemoteFinalJobResult) (RemoteFinalJobResult, error) {
	adopted, err := attacher.AttachFinalJob(ctx, remote.JobID, false)
	if err != nil {
		return remote, fmt.Errorf("adopt completed final job %s after local cancel: %w", remote.JobID, err)
	}
	return adopted, nil
}
