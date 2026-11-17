package scriptgeneration

import (
	"context"
	"errors"
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
	SubmitFinalJob(context.Context, string, GenerateRequest, *GenerateResult) (RemoteFinalJobResult, error)
}

// FinalJobStarter separates creating the remote render from waiting for it.
// The runner checkpoints the returned job ID before it starts polling, so a
// process restart can always reattach to the same render.
type FinalJobStarter interface {
	StartFinalJob(context.Context, string, GenerateRequest, *GenerateResult) (RemoteFinalJobResult, error)
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
	AttachFinalJob(ctx context.Context, jobID string, waitToCompletion bool) (RemoteFinalJobResult, error)
}

// RemoteFinalJobResult is the durable receipt for the two-stage remote job.
type RemoteFinalJobResult struct {
	JobID  string `json:"job_id"`
	Status string `json:"status"`
	// Attempt distinguishes a new Master lease from a replay of PREPARE for
	// the same logical render. Retries of a terminal receipt increment it;
	// retries of PREPARE_INTENT keep it stable for idempotent reconciliation.
	Attempt     int    `json:"attempt,omitempty"`
	WorkerID    string `json:"worker_id,omitempty"`
	ArtifactURL string `json:"artifact_url,omitempty"`
	// ArtifactPath is the local copy the adapter downloads from ArtifactURL
	// after a SUCCEEDED receipt. It is deliberately persisted with the durable
	// receipt (the worker and the artifact persister are separate boundaries)
	// and stripped from the public script.json projection.
	ArtifactPath      string `json:"artifact_path,omitempty"`
	ArtifactSizeBytes int64  `json:"artifact_size_bytes,omitempty"`
	SHA256            string `json:"sha256,omitempty"`
	// ReportOwner is the single durable owner of this receipt. It is stamped on
	// every receipt (submit, attach, stale) so a reader never has to guess
	// whether timing came from the Master, a log line or a sidecar.
	ReportOwner string `json:"report_owner,omitempty"`
	// Simulated marks a receipt produced by the local-only fake submitter
	// (VELOX_FAKE_FINAL_JOB): the handoff completed, but no remote render was
	// dispatched and no artifact exists. It exists so an operator can tell a
	// simulated completion from a certified one when reading the run result.
	Simulated bool `json:"simulated,omitempty"`
	// Yields counts how many attempts have already handed this job's wait back
	// to a later attempt. It is persisted with the receipt because it is what
	// bounds the churn: the FIRST wait is bounded (the worker is freed), and
	// every wait after it runs to completion, so a render longer than the whole
	// retry window still finishes instead of dying on an exhausted attempt
	// budget. Only meaningful while the job is pending.
	Yields int `json:"yields,omitempty"`
	// Timing decomposes the wait that `remote_final_job` reports as one stage.
	// It is persisted with the receipt so the split survives the run and is
	// readable from /full without re-running the render.
	Timing *RemoteFinalJobTiming `json:"timing,omitempty"`
}

// RemoteFinalJobTiming separates the Master-side phases (queue vs execution)
// from what the pipeline adds (handoff + attach wait + poll lag). Durations are
// milliseconds and stay 0 when the Master did not report that boundary; a
// missing phase is never estimated. The worker-internal split (download,
// decode, composition, encode, mux, upload) is not exposed by the Master poll
// API and is therefore NOT represented here — adding it requires worker
// telemetry, not a local clock.
type RemoteFinalJobTiming struct {
	MasterCreatedAt   string `json:"master_created_at,omitempty"`
	MasterStartedAt   string `json:"master_started_at,omitempty"`
	MasterCompletedAt string `json:"master_completed_at,omitempty"`
	QueueMS           int64  `json:"queue_ms,omitempty"`
	ExecutionMS       int64  `json:"execution_ms,omitempty"`
	// HandoffMS is the locally measured PREPARE+FINALIZE (payload build, upload,
	// Master acceptance) — the part of the stage that is our own work.
	HandoffMS int64 `json:"handoff_ms,omitempty"`
	// WaitMS is the attach wall: polling, resumptions, transport.
	WaitMS int64 `json:"wait_ms,omitempty"`
	// PollLagMS is terminal observation minus the Master's completed_at.
	PollLagMS int64 `json:"poll_lag_ms,omitempty"`
	Polls     int   `json:"polls,omitempty"`
	// Worker is the worker-side split of execution_ms, relayed by the Master.
	// nil means the Master did not report it; it is never estimated here.
	Worker *RemoteFinalJobWorkerTiming `json:"worker,omitempty"`
	// HandoffResolveMS is the runtime-asset resolution time inside the handoff:
	// Drive folder listings, media-SSOT lookups and source URL resolution. It is
	// measured, never inferred.
	HandoffResolveMS int64 `json:"handoff_resolve_ms,omitempty"`
	// PayloadMS is the local payload build, measured from the start of the
	// handoff until the PREPARE body exists. It contains HandoffResolveMS.
	PayloadMS int64 `json:"payload_ms,omitempty"`
	// PrepareMS is the PREPARE round-trip to the Master. Together with
	// PayloadMS it closes the handoff budget: FINALIZE is the remainder
	// (handoff_ms − payload_ms − prepare_ms), so a phase can no longer hide in
	// a single opaque number.
	PrepareMS int64 `json:"prepare_ms,omitempty"`
}

// RemoteFinalJobWorkerTiming is what the remote worker reports about its own
// phases. Queue and execution are Master-clock intervals; these are the only
// numbers that say why the remote render took as long as it did.
type RemoteFinalJobWorkerTiming struct {
	LeasedAt   string  `json:"leased_at,omitempty"`
	StartedAt  string  `json:"started_at,omitempty"`
	FinishedAt string  `json:"finished_at,omitempty"`
	DownloadMS int64   `json:"download_ms,omitempty"`
	DecodeMS   int64   `json:"decode_ms,omitempty"`
	ComposeMS  int64   `json:"compose_ms,omitempty"`
	EncodeMS   int64   `json:"encode_ms,omitempty"`
	MuxMS      int64   `json:"mux_ms,omitempty"`
	UploadMS   int64   `json:"upload_ms,omitempty"`
	Frames     int64   `json:"frames,omitempty"`
	EncodeFPS  float64 `json:"encode_fps,omitempty"`
}

// RemoteFinalJobSegmentPrepTiming records Producer-side preparation for the
// non-stock clip and overlay windows included in one final-job handoff.
type RemoteFinalJobSegmentPrepTiming struct {
	Segments     int                               `json:"segments,omitempty"`
	Reused       int                               `json:"reused,omitempty"`
	Cached       int                               `json:"cached,omitempty"`
	Derived      int                               `json:"derived,omitempty"`
	Copied       int                               `json:"copied,omitempty"`
	DownloadMS   int64                             `json:"download_ms,omitempty"`
	EncodeMS     int64                             `json:"encode_ms,omitempty"`
	VerifyMS     int64                             `json:"verify_ms,omitempty"`
	PublishMS    int64                             `json:"publish_ms,omitempty"`
	Bytes        int64                             `json:"bytes,omitempty"`
	PaddedFrames int64                             `json:"padded_frames,omitempty"`
	Windows      []RemoteFinalJobSegmentPrepWindow `json:"windows,omitempty"`
}

// RemoteFinalJobSegmentPrepWindow identifies the work for one non-stock
// timeline placement. Paths are local diagnostics and never source stock.
type RemoteFinalJobSegmentPrepWindow struct {
	SegmentID    string `json:"segment_id,omitempty"`
	SourceSHA256 string `json:"source_sha256,omitempty"`
	Normalized   bool   `json:"normalized,omitempty"`
	Path         string `json:"path,omitempty"`
	Frames       int64  `json:"frames,omitempty"`
	DownloadMS   int64  `json:"download_ms,omitempty"`
	CopyMS       int64  `json:"copy_ms,omitempty"`
	EncodeMS     int64  `json:"encode_ms,omitempty"`
	VerifyMS     int64  `json:"verify_ms,omitempty"`
	PublishMS    int64  `json:"publish_ms,omitempty"`
	Bytes        int64  `json:"bytes,omitempty"`
	PaddedFrames int64  `json:"padded_frames,omitempty"`
}

// Merge combines the phases observed by two sides of the handoff. The handoff
// is measured before PREPARE/FINALIZE; queue/execution/wait/poll arrive from a
// later attach. Neither may erase the other, and a phase a side did not report
// (0) never overwrites a measured one.
func (t *RemoteFinalJobTiming) Merge(other *RemoteFinalJobTiming) *RemoteFinalJobTiming {
	if t == nil {
		t = &RemoteFinalJobTiming{}
	}
	if other == nil {
		return t
	}
	if other.MasterCreatedAt != "" {
		t.MasterCreatedAt = other.MasterCreatedAt
	}
	if other.MasterStartedAt != "" {
		t.MasterStartedAt = other.MasterStartedAt
	}
	if other.MasterCompletedAt != "" {
		t.MasterCompletedAt = other.MasterCompletedAt
	}
	for _, phase := range []struct {
		dst *int64
		src int64
	}{{&t.QueueMS, other.QueueMS}, {&t.ExecutionMS, other.ExecutionMS}, {&t.HandoffMS, other.HandoffMS}, {&t.HandoffResolveMS, other.HandoffResolveMS}, {&t.PayloadMS, other.PayloadMS}, {&t.PrepareMS, other.PrepareMS}, {&t.WaitMS, other.WaitMS}, {&t.PollLagMS, other.PollLagMS}} {
		if phase.src > 0 {
			*phase.dst = phase.src
		}
	}
	if other.Polls > t.Polls {
		t.Polls = other.Polls
	}
	// The worker block is one atomic observation: a later report replaces the
	// previous one instead of being merged field by field, so a resumed attach
	// cannot mix phases from two different renders.
	if other.Worker != nil {
		t.Worker = other.Worker
	}
	return t
}
