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

// FinalJobSourcePrewarmer is the OPTIONAL capability that resolves the stock
// sources a final job will consume BEFORE the handoff asks for them.
//
// A handoff resolves its stock serially by contract (each source publishes its
// own bytes), so on a 10-scene run it pays ~10 s inside `remote_final_job`
// while the overlay render holds 13 s of the critical path with nothing queued
// behind it. Warming the same sources during that render moves the cost into a
// window that was already paid for; it does not make the resolution faster.
//
// It is optional and best-effort by construction: a submitter that cannot
// prewarm keeps the serial handoff byte for byte, and the returned error is
// logged by the runner — the handoff still walks and still reports its own
// failure, so a prewarm miss can only cost the time it tried to save.
type FinalJobSourcePrewarmer interface {
	PrewarmFinalJobStockSources(ctx context.Context, runID string, req GenerateRequest, result *GenerateResult) error
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
	SHA256      string `json:"sha256,omitempty"`
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
	// SegmentPrep splits handoff_ms into the phases of the local fragment
	// preparation that must finish before PREPARE can exist. nil means no
	// fragment was prepared on this attempt (a resumed attach).
	SegmentPrep *RemoteFinalJobSegmentPrepTiming `json:"segment_prep,omitempty"`
	// HandoffResolveMS is the runtime-asset resolution time inside the handoff:
	// Drive folder listings, media-SSOT lookups and Objectstore publication of
	// the sources. It is the part of handoff_ms that is neither fragment
	// preparation nor the two Master calls, and it is measured, never inferred.
	HandoffResolveMS int64 `json:"handoff_resolve_ms,omitempty"`
	// PayloadMS is the local payload build, measured from the start of the
	// handoff until the PREPARE body exists. It CONTAINS SegmentPrep and
	// HandoffResolveMS (they run inside the build), so it is not a sibling of
	// them; it is the phase that used to be the unexplained remainder of
	// handoff_ms.
	PayloadMS int64 `json:"payload_ms,omitempty"`
	// PrepareMS is the PREPARE round-trip to the Master. Together with
	// PayloadMS it closes the handoff budget: FINALIZE is the remainder
	// (handoff_ms − payload_ms − prepare_ms), so a phase can no longer hide in
	// a single opaque number.
	PrepareMS int64 `json:"prepare_ms,omitempty"`
}

// RemoteFinalJobSegmentPrepWindow attributes the preparation cost of ONE
// fragment. The aggregate block above says how much the stage cost; this says
// where: which windows were copied and which were encoded, and what each one
// paid. Path is the producing lane, so `copy` with a zero EncodeMS is the copy
// lever working and `encode` is the encoder being asked for bytes — the
// distinction the aggregate `encode_ms` cannot make, because it sums both.
// Phase durations are milliseconds and are measured, never estimated: a phase
// that did not run stays 0.
type RemoteFinalJobSegmentPrepWindow struct {
	// SegmentID is the plan's segment identity (empty when the caller did not
	// provide one; the other fields still describe the work).
	SegmentID string `json:"segment_id,omitempty"`
	// Path is how the bytes were produced: reuse|cache|copy|encode|derived.
	Path string `json:"path"`
	// Frames is the requested window length in frames.
	Frames int64 `json:"frames,omitempty"`
	// DownloadMS is the source transfer this window paid (0 when the source was
	// already staged by an earlier window of the same bytes).
	DownloadMS int64 `json:"download_ms,omitempty"`
	// CopyMS is the packet-copy time. EncodeMS is the encoder time. They are
	// mutually exclusive by construction: a copied window never reaches the
	// encoder, and CopyMS stays 0 on an encoded window.
	CopyMS   int64 `json:"copy_ms,omitempty"`
	EncodeMS int64 `json:"encode_ms,omitempty"`
	// VerifyMS is the census/probe work this window paid.
	VerifyMS  int64 `json:"verify_ms,omitempty"`
	PublishMS int64 `json:"publish_ms,omitempty"`
	Bytes     int64 `json:"bytes,omitempty"`
	// PaddedFrames is how many tail frames tpad had to clone because the
	// source ended before the plan's window did. Zero is the normal case: a
	// window fully inside the source is encoded without tpad at all, so a
	// frozen tail is a measured shortfall, never a side effect.
	PaddedFrames int64 `json:"padded_frames,omitempty"`
	// SourceSHA256 identifies the bytes this window was cut from, so a slow
	// window can be tied to its source after the temporary file is gone.
	SourceSHA256 string `json:"source_sha256,omitempty"`
	// Normalized reports that the encoder rebuilt the delivery geometry
	// (resolution, frame rate, chroma) for this window instead of only cutting
	// it. It separates a scale cost from a cut cost without a second probe.
	Normalized bool `json:"normalized,omitempty"`
	// CopyFailure explains why this window used the encoder instead of packet
	// copy: for example, an unaligned start, an uncertified source, or a copy
	// that failed verification. Every encode path names its reason so CPU work
	// stays visible in the receipt.
	CopyFailure string `json:"copy_failure,omitempty"`
}

// RemoteFinalJobSegmentPrepTiming is the locally measured split of the one
// stage the remote render does not explain: preparing assembly-ready fragments
// (download the source, encode the window, verify the frame cap, publish to the
// HTTPS Objectstore). It is measured, never estimated: a phase that did not run
// stays 0.
type RemoteFinalJobSegmentPrepTiming struct {
	Segments int `json:"segments,omitempty"`
	// Reused counts fragments whose SOURCE already was the exact certified
	// fragment (nothing was produced). Cached counts fragments served from the
	// local fragment cache: the same window of the same source bytes, encoded
	// and certified by an earlier run.
	Reused int `json:"reused,omitempty"`
	Cached int `json:"cached,omitempty"`
	// Derived counts duplicate placements certified by copying an already
	// certified bitstream into a distinct container. They cost a remux, not a
	// second encode, and their bytes are distinct by construction.
	Derived int `json:"derived,omitempty"`
	// Copied counts verified GOP-aligned windows copied without re-encoding.
	Copied     int   `json:"copied,omitempty"`
	DownloadMS int64 `json:"download_ms,omitempty"`
	EncodeMS   int64 `json:"encode_ms,omitempty"`
	VerifyMS   int64 `json:"verify_ms,omitempty"`
	PublishMS  int64 `json:"publish_ms,omitempty"`
	Bytes      int64 `json:"bytes,omitempty"`
	// PaddedFrames is the sum of cloned tail frames across this handoff. It is
	// exposed separately so a run can prove that reaching the exact frame cap
	// did not silently freeze pictures.
	PaddedFrames int64 `json:"padded_frames,omitempty"`
	// EncodeFallbacks counts windows that were ELIGIBLE for a packet copy (GOP
	// aligned, certified source) but reached the encoder anyway. It is the
	// count the lenient mode used to hide: the run reported success and paid a
	// full re-encode for a window the copy lever should have covered. Each one
	// is also named in its window record through CopyFailure.
	EncodeFallbacks int `json:"encode_fallbacks,omitempty"`
	// Windows is the per-fragment attribution of this handoff, sorted by
	// segment identity for determinism. It is scoped to the handoff that built
	// this payload, so a concurrent or resumed run never contributes its
	// windows to this receipt.
	Windows []RemoteFinalJobSegmentPrepWindow `json:"windows,omitempty"`
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
	// The local preparation block is also one atomic observation: it describes
	// a single payload build, so it is replaced whole, never field by field.
	if other.SegmentPrep != nil {
		t.SegmentPrep = other.SegmentPrep
	}
	return t
}
