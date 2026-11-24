package contract

import (
	segmentcontract "github.com/Marcuss-ops/PipelineGen/internal/kernel/media/finaljob"
)

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
	// SegmentPrep is the Producer's own split of payload_ms for this handoff:
	// download, encode/copy, verify and publish per fragment. It is the only
	// attribution that can tell a slow transfer from a slow encode; nil means
	// the installed preparer did not report it, never zero work.
	SegmentPrep *RemoteFinalJobSegmentPrepTiming `json:"segment_prep,omitempty"`
	// ArtifactDownloadMS is the Producer-side transfer of the Master's
	// published MP4: the GET plus the SHA-256 verification, measured by the
	// adapter that owns the bytes. This is the phase the final-job stage used
	// to hide: before it existed, a 891 MB artifact download of ~257 s was
	// inside `remote_final_job` but in none of queue/handoff/execution, so the
	// stage looked unexplained instead of transfer-bound.
	ArtifactDownloadMS int64 `json:"artifact_download_ms,omitempty"`
	// ArtifactDownloadBytes is the transferred size, or the verified local
	// size on a reuse.
	ArtifactDownloadBytes int64 `json:"artifact_download_bytes,omitempty"`
	// ArtifactDownloadReused marks a retry that found the deterministic temp
	// file already verified: no transfer, only the local hash check.
	ArtifactDownloadReused bool `json:"artifact_download_reused,omitempty"`
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

// The receipt types are owned by the kernel/media/finaljob contract; the
// capability aliases keep the public JSON shape and call sites unchanged.
type RemoteFinalJobSegmentPrepTiming = segmentcontract.SegmentPrepTiming

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
	}{{&t.QueueMS, other.QueueMS}, {&t.ExecutionMS, other.ExecutionMS}, {&t.HandoffMS, other.HandoffMS}, {&t.HandoffResolveMS, other.HandoffResolveMS}, {&t.PayloadMS, other.PayloadMS}, {&t.PrepareMS, other.PrepareMS}, {&t.WaitMS, other.WaitMS}, {&t.PollLagMS, other.PollLagMS}, {&t.ArtifactDownloadMS, other.ArtifactDownloadMS}, {&t.ArtifactDownloadBytes, other.ArtifactDownloadBytes}} {
		if phase.src > 0 {
			*phase.dst = phase.src
		}
	}
	if other.Polls > t.Polls {
		t.Polls = other.Polls
	}
	// Reuse is monotone: the deterministic temp path for a job is transferred
	// once, so a later attach can only observe it already verified.
	t.ArtifactDownloadReused = t.ArtifactDownloadReused || other.ArtifactDownloadReused
	// The worker block is one atomic observation: a later report replaces the
	// previous one instead of being merged field by field, so a resumed attach
	// cannot mix phases from two different renders.
	if other.Worker != nil {
		t.Worker = other.Worker
	}
	// The segment-preparation block is atomic for the same reason: it belongs
	// to the handoff that produced it, and an attach that prepared no fragments
	// must not erase the split the handoff measured.
	if other.SegmentPrep != nil {
		t.SegmentPrep = other.SegmentPrep
	}
	return t
}
