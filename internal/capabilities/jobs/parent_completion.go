package jobs

// parent_completion.go — event-driven aggregate-parent finalisation.
//
// An aggregate parent is a job that fans work out to children and then waits:
// it completes with parent_state=waiting_children and is finalised later, once
// its children are terminal. clip.render's submit phase is the canonical case
// (submit → settle child → parent flips), and the scripts/voiceover fan-outs
// use the same contract.
//
// The child's terminal commit is the only moment that can make the parent
// eligible, and it happens in-process, right here. Polling for it therefore
// pays the poll interval as user-visible completion latency on every fan-out
// and runs a continuous query for a case that is normally already satisfied.
//
// ParentCompletionNotifier is the seam that removes that wait: the worker
// reports the just-committed child, and the parent aggregator finalises the
// specific parent instead of scanning for it. The durability net stays — the
// aggregator's sweep still runs, because a process that dies between the child
// commit and the notification must not strand a parent. The sweep is now a
// recovery path with a recovery cadence rather than the primary mechanism.

import (
	"context"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// parentCompletionTimeout bounds the notification. It runs on the worker's
// finalisation path, so it must never be able to stall the worker: the
// parent finalisation is one row read plus one idempotent CAS.
const parentCompletionTimeout = 5 * time.Second

// ParentCompletionNotifier finalises the parent of a child that has just
// committed terminal.
//
// Implementations MUST be idempotent and MUST tolerate a parent that is
// already terminal or has no child link: a duplicate call, a retry, or a race
// with the aggregator's sweep is normal, not an error. The implementation is
// also the place that decides which parent types it owns — the worker reports
// every terminal child that carries a parent link.
type ParentCompletionNotifier interface {
	NotifyChildTerminal(ctx context.Context, child *job.Job) error
}

// notifyParentCompletion hands a just-committed terminal child to the notifier,
// when one is wired and the child carries a parent link.
//
// Best-effort by construction: by the time this is called the child's terminal
// state is durable, so a notification failure cannot be allowed to fail the
// job — it is logged and the aggregator's recovery sweep remains the durability
// net. The call is detached from the job context (the job may already be
// cancelled or shutting down) but keeps its values, so the run-scoped
// observability identity survives into the finalisation.
func (w *Worker) notifyParentCompletion(ctx context.Context, j *job.Job) {
	if w == nil || w.parentNotifier == nil || j == nil {
		return
	}
	parentID := j.ParentJobID
	if parentID == "" {
		// The payload is the transport-neutral carrier of the link (it is what
		// a remote claimer reads), so a child whose row predates the column
		// still notifies.
		parentID = job.ParentLinkFromPayload(j.Payload).ParentJobID
	}
	if parentID == "" {
		return
	}
	child := *j
	child.ParentJobID = parentID

	notifyCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), parentCompletionTimeout)
	defer cancel()
	if err := w.parentNotifier.NotifyChildTerminal(notifyCtx, &child); err != nil {
		w.log.Warn("parent completion notification failed; the aggregation recovery sweep will finalise the parent",
			zap.String("job_id", j.ID),
			zap.String("job_type", j.Type),
			zap.String("parent_job_id", parentID),
			zap.Error(err),
		)
	}
}
