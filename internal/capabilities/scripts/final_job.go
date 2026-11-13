package scriptgeneration

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
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
	SHA256      string `json:"sha256,omitempty"`
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

// The final-job stock planner owns WHICH stock source a final job consumes and
// which Drive reference/window goes into the compiled plan. The planner reads
// saved metadata only; the Master downloads and processes the selected stock
// window outside the Producer. The old dedicated file was folded into this one
// by the 2026-11-11 boundary cleanup, so do not look for it by filename.
// finalJobStockClip is one stock window the remote Master will place: the
// resolved Drive reference and the exact duration the scene consumes from it.
type finalJobStockClip struct {
	Asset   map[string]any
	ChunkMS int64
}

// finalJobStockPlanner carries the per-handoff stock bookkeeping: the shuffled
// file list per folder, the resolved asset cache, and the rotation cursor. Its
// order is seeded by runID, making new runs different while keeping retries of
// one run stable.
type finalJobStockPlanner struct {
	runID   string
	files   map[string][]FinalJobStockFile
	assets  map[string]map[string]any
	cursors map[string]int
}

func newFinalJobStockPlanner(runID string) *finalJobStockPlanner {
	return &finalJobStockPlanner{
		runID:   strings.TrimSpace(runID),
		files:   make(map[string][]FinalJobStockFile),
		assets:  make(map[string]map[string]any),
		cursors: make(map[string]int),
	}
}

// folder returns the video files of one stock folder, listing it once per
// handoff and warming the metadata of the sources it will offer. The Producer
// never normalizes or uploads stock: the compiled plan carries the Drive
// reference and the remote Master processes the selected window.
func (p *finalJobStockPlanner) folder(ctx context.Context, resolver FinalJobAssetResolver, folderID string) ([]FinalJobStockFile, error) {
	if files, ok := p.files[folderID]; ok {
		return files, nil
	}
	listed, err := resolver.ListFinalJobStockFolder(ctx, folderID)
	if err != nil {
		return nil, fmt.Errorf("list selected stock folder %s: %w", folderID, err)
	}
	if len(listed) == 0 {
		return nil, fmt.Errorf("selected stock folder %s contains no video files", folderID)
	}
	listed = append([]FinalJobStockFile(nil), listed...)
	sort.SliceStable(listed, func(i, j int) bool {
		if listed[i].ID != listed[j].ID {
			return listed[i].ID < listed[j].ID
		}
		return listed[i].Name < listed[j].Name
	})
	seed := digest.SHA256Sum([]byte(p.runID + ":" + folderID))
	rng := rand.New(rand.NewSource(int64(binary.LittleEndian.Uint64(seed[:8]))))
	rng.Shuffle(len(listed), func(i, j int) { listed[i], listed[j] = listed[j], listed[i] })
	p.files[folderID] = listed
	return listed, nil
}

// finalJobStockScanWindow bounds how many folder entries one selection step
// inspects. The prefetch lane warms the same head of the folder, so this is
// also the window in which the next source is already cached. A folder that
// cannot fill a scene inside this window fails closed instead of sending the
// Master a stock window it cannot packet-copy.
const finalJobStockScanWindow = 64

// sceneClips consumes one scene's canonical duration from its stock folder
// using WHOLE files only. The remote Master's stock lane is packet-copy: it
// accepts source_in_us == 0 and source_duration_us == file duration, and
// rejects a sub-window (r56: stock_window_not_packet_copyable). The Producer
// therefore never trims a stock file. It prefers a file whose duration exactly
// matches the remaining window; when none exists it takes the first whole file
// that fits, so the scene is composed of whole files summing exactly to the
// canonical duration. If no such composition exists, the handoff fails here
// instead of sending a doomed plan.
//
// Selection follows a per-run shuffled order: the cursor advances past the
// claimed file and the next scene continues through that permutation. Retries
// keep the same order; new runs get a fresh order.
func (p *finalJobStockPlanner) sceneClips(ctx context.Context, resolver FinalJobAssetResolver, folderID string, durationUS int64) ([]finalJobStockClip, error) {
	files, err := p.folder(ctx, resolver, folderID)
	if err != nil {
		return nil, err
	}
	remainingMS := (durationUS + 999) / 1000
	if remainingMS <= 0 {
		return nil, nil
	}
	clips := make([]finalJobStockClip, 0, 1)
	folderIndex := p.cursors[folderID]
	for remainingMS > 0 {
		limit := len(files)
		if limit > finalJobStockScanWindow {
			limit = finalJobStockScanWindow
		}
		chosen := -1
		chosenDurationMS := int64(0)
		var chosenAsset map[string]any
		var firstResolveErr error
		for offset := 0; offset < limit; offset++ {
			index := (folderIndex + offset) % len(files)
			file := files[index]
			asset, ok := p.assets[file.ID]
			if !ok {
				asset, err = resolveFinalJobStockAsset(ctx, resolver, file.ID)
				if err != nil {
					if firstResolveErr == nil {
						firstResolveErr = fmt.Errorf("resolve stock file %s (%s): %w", file.Name, file.ID, err)
					}
					continue
				}
				p.assets[file.ID] = asset
			}
			durationMS, _ := asset["duration_ms"].(int64)
			if durationMS <= 0 {
				if firstResolveErr == nil {
					firstResolveErr = fmt.Errorf("stock file %s has no canonical duration", file.Name)
				}
				continue
			}
			// Exact match wins immediately. Otherwise retain the first whole
			// file that fits; do not pick a larger file and leave a tail the
			// next step cannot fill.
			if durationMS == remainingMS {
				chosen, chosenDurationMS, chosenAsset = index, durationMS, asset
				break
			}
			if durationMS < remainingMS && chosen < 0 {
				chosen, chosenDurationMS, chosenAsset = index, durationMS, asset
			}
		}
		if chosen < 0 {
			if firstResolveErr != nil {
				return nil, fmt.Errorf("stock folder %s cannot fill the remaining %dms with whole files (no partial stock windows allowed): %w", folderID, remainingMS, firstResolveErr)
			}
			return nil, fmt.Errorf("stock folder %s cannot fill the remaining %dms with whole files; no partial stock windows are allowed", folderID, remainingMS)
		}
		clips = append(clips, finalJobStockClip{Asset: chosenAsset, ChunkMS: chosenDurationMS})
		remainingMS -= chosenDurationMS
		folderIndex = (chosen + 1) % len(files)
		p.cursors[folderID] = folderIndex
	}
	return clips, nil
}
