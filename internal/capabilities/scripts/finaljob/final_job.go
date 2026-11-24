package finaljob

import (
	"context"
	"errors"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/contract"
	segmentcontract "github.com/Marcuss-ops/PipelineGen/internal/kernel/media/finaljob"
)

// FinalJobSubmitter hands a completed local media plan to the configured
// remote render master. It is intentionally absent from runs with
// final_job=false.
//
// The handoff is a three-phase job (PREPARE → FINALIZE → poll). The submitter
// owns the first two phases and the FIRST bounded wait; a wait that outlives
// that budget is reported with ErrFinalJobPending, and the durable receipt it
// returns carries the Master's job id so a later attempt resumes from it (see
// FinalJobAttacher) instead of asking the Master for a second render.
type FinalJobSubmitter interface {
	SubmitFinalJob(context.Context, string, contract.GenerateRequest, *contract.GenerateResult) (contract.RemoteFinalJobResult, error)
}

// FinalJobStarter separates creating the remote render from waiting for it.
// The runner checkpoints the returned job ID before it starts polling, so a
// process restart can always reattach to the same render.
type FinalJobStarter interface {
	StartFinalJob(context.Context, string, contract.GenerateRequest, *contract.GenerateResult) (contract.RemoteFinalJobResult, error)
}

// ErrFinalJobPending is the capability-owned sentinel for a remote render that
// EXISTS on the Master and is still running. It is a sentinel so the runner can
// branch on it without importing the transport package, and it always travels
// with the receipt that carries the job id — the handle an attempt must persist
// before handing the wait back.
var ErrFinalJobPending = errors.New("remote final job is still rendering")

// ErrFinalJobReceiptCheckpoint prevents an automatic retry when durable
// receipt state could not be saved safely.
var ErrFinalJobReceiptCheckpoint = errors.New("remote final job receipt checkpoint failed")

// FinalJobReportOwner names the ONE durable report that owns the final-job
// projection: receipt, timing and boundary verdict. It is persisted in
// run_observability.report_json (RunReport.FinalJob). The resumable copy in
// run_observability.workflow_payload_json is a checkpoint, not a second report.
const FinalJobReportOwner = "run_observability.report_json"

// FinalJobAttacher is the OPTIONAL half of the final-job contract: waiting on a
// job that a PREVIOUS attempt already submitted, with no second PREPARE. A
// submitter that cannot resume one is still valid (it is what every non-final
// run wires), but a run that already has a pending receipt fails closed rather
// than submit a duplicate render.
type FinalJobAttacher interface {
	// waitToCompletion=false waits only up to the submitter's attach budget and
	// reports ErrFinalJobPending when it expires; true waits with the
	// submitter's full poll timeout, which is the pre-split blocking contract.
	AttachFinalJob(ctx context.Context, jobID string, waitToCompletion bool) (contract.RemoteFinalJobResult, error)
}

// FinalJobStater is the OPTIONAL read-only half used when a local cancel
// interrupts an attach: one status read decides whether the Master finished a
// render that must be adopted instead of discarded.
type FinalJobStater interface {
	FinalJobStatus(ctx context.Context, jobID string) (contract.RemoteFinalJobResult, error)
}

// FinalJobCanceller is the OPTIONAL stop half: a job that is still active when
// the local run is cancelled is stopped remotely, so the two sides cannot
// diverge by a render that completes after the local CANCELLED.
type FinalJobCanceller interface {
	CancelFinalJob(ctx context.Context, jobID string) error
}

// ErrFinalJobCancelRejected reports that the Master reached a terminal state
// before the remote cancel: the reconciler re-reads the status and adopts a
// SUCCEEDED artifact instead of reporting the race as a failure.
var ErrFinalJobCancelRejected = errors.New("remote final job finished before cancel")

type RemoteFinalJobSegmentPrepWindow = segmentcontract.SegmentPrepWindow
