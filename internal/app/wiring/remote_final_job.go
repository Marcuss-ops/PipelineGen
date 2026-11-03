package wiring

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaexec"
	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/drive"
	pgmedia "github.com/Marcuss-ops/PipelineGen/internal/platform/postgres/media"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/remotejob"
)

const defaultFinalJobMasterURL = "https://veloxmanager.duckdns.org"

// defaultFinalJobAttachBudget keeps script.generate attached through the usual
// remote composite render. The old 60s default marked the local job FAILED while
// the Master was still rendering for 6-14 minutes; those renders continued, but
// their completed result was never joined back to the parent job. Leave enough
// room for the observed render times while remaining below the 60m script job
// timeout. Operators can still override this with
// VELOX_FINAL_JOB_ATTACH_SECONDS.
const defaultFinalJobAttachBudget = 50 * time.Minute

// finalJobWorker51ID is Worker 51's registered Master ID. The human-readable
// worker name is velox_worker_51_222_204_158; placement pins use the distinct
// registered ID. Operators can override it when the worker is reprovisioned.
const finalJobWorker51ID = "velox-worker-523925eb"

// finalJobAttachBudget reads VELOX_FINAL_JOB_ATTACH_SECONDS. 0 disables the
// hand-off (every wait runs to completion: the pre-split blocking contract).
// A malformed value is a configuration error, not a silent default.
func finalJobAttachBudget() (time.Duration, error) {
	raw := strings.TrimSpace(os.Getenv("VELOX_FINAL_JOB_ATTACH_SECONDS"))
	if raw == "" {
		return defaultFinalJobAttachBudget, nil
	}
	seconds, err := strconv.Atoi(raw)
	if err != nil || seconds < 0 {
		return 0, fmt.Errorf("VELOX_FINAL_JOB_ATTACH_SECONDS must be a non-negative integer number of seconds, got %q", raw)
	}
	return time.Duration(seconds) * time.Second, nil
}

type remoteFinalJobAdapter struct {
	media    *pgmedia.MediaSearcher
	stock    drive.Reader
	client   *remotejob.Client
	segments *finalJobSegmentPreparer
	probe    interface {
		Probe(context.Context, string) (*mediaexec.MediaInfo, error)
	}
	// attachBudget bounds a single attempt's wait on the Master render; see
	// defaultFinalJobAttachBudget and finalJobAttachBudget.
	attachBudget time.Duration
	// resolverNS is the accumulated runtime-asset resolution time reported in
	// the receipt as handoff_resolve_ms (diagnostics only).
	resolverNS atomic.Int64
	// resolved caches one handoff's runtime-asset resolution by identity. The
	// payload builder asks for the SAME source once per timeline window (85
	// windows over ~13 sources in r50, 65,1 s of resolution); publishing and
	// signing those bytes again per window answers a question the identity has
	// already answered. Cleared when a new handoff starts, never shared across
	// runs, and only ever stores a reference to immutable content.
	resolvedMu sync.Mutex
	resolved   map[string]map[string]any
	hits       int
	// resolvedRunID scopes the cache to ONE run. The stock prewarm fills it
	// during the overlay render, so StartFinalJob of the SAME run must keep
	// what was already warmed; a different run (or a retry reusing this
	// adapter) must never inherit another run's identities.
	resolvedRunID string
	// inflight is the single-flight index over identities currently being
	// resolved. A folder prefetch and the serial walk ask for the same source,
	// and two folders may offer the same file; the second caller waits for the
	// first instead of downloading and publishing those bytes again. Entries
	// hold immutable content, so a waiter always receives the same answer the
	// builder produced.
	inflightMu sync.Mutex
	inflight   map[string]*finalJobResolveCall
}

// finalJobResolveCall is one in-progress resolution. `done` is closed exactly
// once, after ref/err are written, so a waiter needs no further lock.
type finalJobResolveCall struct {
	done chan struct{}
	ref  map[string]any
	err  error
}

func newRemoteFinalJobAdapter(root *ComposeRoot) (*remoteFinalJobAdapter, error) {
	if root == nil || root.MediaPostgres == nil || root.Drive == nil || root.Drive.Reader == nil {
		return nil, fmt.Errorf("remote final-job handoff requires the media SSOT and Drive stock reader")
	}
	if strings.TrimSpace(os.Getenv("OBJECTSTORE_PUBLIC_URL")) == "" {
		return nil, fmt.Errorf("remote final-job HTTPS handoff requires OBJECTSTORE_PUBLIC_URL; only stock assets may use Drive")
	}
	token := strings.TrimSpace(os.Getenv("VELOX_FINAL_JOB_M2M_SECRET"))
	if token == "" {
		path := strings.TrimSpace(os.Getenv("VELOX_FINAL_JOB_CREDENTIALS_FILE"))
		if path == "" {
			home, _ := os.UserHomeDir()
			path = filepath.Join(home, "creator-77-master.env")
		}
		var err error
		token, err = remotejob.ReadTokenFile(path)
		if err != nil {
			return nil, err
		}
	}
	base := ""
	if v := strings.TrimSpace(os.Getenv("VELOX_FINAL_JOB_MASTER_URL")); v != "" {
		base = v
	}
	if base == "" {
		base = defaultFinalJobMasterURL
	}
	masterURL, err := url.Parse(base)
	if err != nil || masterURL == nil || masterURL.Scheme != "https" || masterURL.Host == "" {
		return nil, fmt.Errorf("remote final-job master URL must use HTTPS")
	}
	budget, err := finalJobAttachBudget()
	if err != nil {
		return nil, err
	}
	client := remotejob.New(base, token)
	return &remoteFinalJobAdapter{media: pgmedia.NewMediaSearcher(root.MediaPostgres), stock: root.Drive.Reader, client: client, attachBudget: budget}, nil
}

func (a *remoteFinalJobAdapter) SubmitFinalJob(ctx context.Context, runID string, req scriptgen.GenerateRequest, result *scriptgen.GenerateResult) (scriptgen.RemoteFinalJobResult, error) {
	started, err := a.StartFinalJob(ctx, runID, req, result)
	if err != nil {
		return started, err
	}
	remote, waitErr := a.client.Attach(ctx, started.JobID, a.attachBudget)
	receipt := finalJobResult(started.JobID, remote)
	// The attach result only knows Master-clock phases; the locally measured
	// handoff belongs to the receipt the submission produced.
	receipt.Timing = mergeFinalJobTiming(started.Timing, receipt.Timing)
	return receipt, finalJobWaitError(waitErr)
}

// StartFinalJob creates the Master job and returns its durable handle without
// polling. The scripts runner checkpoints this receipt before it attaches.
//
// It never sends FINALIZE to an identity the Master reports as already leased
// or canceled: a leased job is handed back as ErrFinalJobPending so the next
// attempt ATTACHES to it, and a canceled one is handed back as a stale receipt
// so the next attempt prepares a fresh identity instead of replaying a dead id.
func (a *remoteFinalJobAdapter) StartFinalJob(ctx context.Context, runID string, req scriptgen.GenerateRequest, result *scriptgen.GenerateResult) (scriptgen.RemoteFinalJobResult, error) {
	if a == nil || a.client == nil {
		return scriptgen.RemoteFinalJobResult{}, fmt.Errorf("remote final-job adapter is not configured")
	}
	if a.segments == nil {
		return scriptgen.RemoteFinalJobResult{}, fmt.Errorf("remote final-job local segment preparation is not configured; refusing to dispatch raw video sources")
	}
	handoffStarted := time.Now()
	// The segment preparation measured by THIS build, never the preparer's
	// process-wide total: a resumed or concurrent run must not inherit it.
	var segmentPrep *scriptgen.RemoteFinalJobSegmentPrepTiming
	var resolveMS, prepareMS int64
	var payloadBuiltAt time.Time
	handoffTiming := func() *scriptgen.RemoteFinalJobTiming {
		timing := &scriptgen.RemoteFinalJobTiming{
			HandoffMS:        time.Since(handoffStarted).Milliseconds(),
			HandoffResolveMS: resolveMS,
			PrepareMS:        prepareMS,
		}
		if !payloadBuiltAt.IsZero() {
			// The payload build is a SUBSET of handoff_ms that contains segment
			// preparation and resolve; measuring its end is what makes the
			// remainder attributable to FINALIZE instead of unknown.
			timing.PayloadMS = payloadBuiltAt.Sub(handoffStarted).Milliseconds()
		}
		if segmentPrep != nil && segmentPrep.Segments > 0 {
			timing.SegmentPrep = segmentPrep
		}
		return timing
	}
	a.beginHandoffCache(runID)
	a.resolverNS.Store(0)
	// One scope per handoff: the per-window attribution must describe THIS
	// payload, not whatever the preparer accumulated before it or beside it.
	scope := a.segments.NewHandoffScope()
	pre, finalize, err := scriptgen.BuildFinalJobPayloads(withFinalJobHandoffScope(ctx, scope), runID, req, result, a, a.segments)
	if err != nil {
		return scriptgen.RemoteFinalJobResult{}, err
	}
	if prep := a.segments.Timing(scope); prep.Segments > 0 {
		segmentPrep = &prep
	}
	payloadBuiltAt = time.Now()
	resolveMS = a.resolverNS.Load() / int64(time.Millisecond)
	a.resolvedMu.Lock()
	uniqueResolved, dedupHits := len(a.resolved), a.hits
	a.resolvedMu.Unlock()
	var encodeMS, verifyMS int64
	if segmentPrep != nil {
		encodeMS, verifyMS = segmentPrep.EncodeMS, segmentPrep.VerifyMS
	}
	log.Printf("final_job handoff resolve_ms=%d unique_resolved=%d dedup_hits=%d encode_ms=%d verify_ms=%d",
		resolveMS, uniqueResolved, dedupHits, encodeMS, verifyMS)
	workerID := strings.TrimSpace(os.Getenv("VELOX_FINAL_JOB_WORKER_ID"))
	if workerID == "" {
		workerID = finalJobWorker51ID
	}
	pre["placement_pin_worker_id"] = workerID
	// final_job=true on the local script request triggers this Master handoff.
	// PREPARE carries the scene plan; FINALIZE attaches runtime assets and
	// overlays so the Master refreshes prefetch before the worker starts. The id
	// is kept between the two phases and through the wait, so a bounded wait that
	// expires still reports a handle the next attempt can resume from — the job
	// exists on the Master either way.
	prepareStarted := time.Now()
	prepared, err := a.client.PrepareJob(ctx, pre)
	prepareMS = time.Since(prepareStarted).Milliseconds()
	if err != nil {
		if errors.Is(err, remotejob.ErrRemoteStaleJob) {
			return scriptgen.RemoteFinalJobResult{JobID: prepared.JobID, Status: staleReceiptStatus(prepared.Status), Timing: handoffTiming()},
				fmt.Errorf("remote preparation %s is stale and must be replaced: %w: %w", prepared.JobID, scriptgen.ErrFinalJobStaleReceipt, err)
		}
		return scriptgen.RemoteFinalJobResult{}, err
	}
	if remoteJobInFlight(prepared.Status) {
		// The idempotent PREPARE replayed a job that is already leased and
		// running. Finalizing it is forbidden (and would fail with
		// job_already_leased); handing the handle back lets the next attempt
		// attach to the render that already exists.
		return scriptgen.RemoteFinalJobResult{JobID: prepared.JobID, Status: prepared.Status, Timing: handoffTiming()},
			fmt.Errorf("remote job %s is already leased and %s: %w", prepared.JobID, prepared.Status, scriptgen.ErrFinalJobPending)
	}
	if err := a.client.Finalize(ctx, prepared.JobID, finalize); err != nil {
		receipt := scriptgen.RemoteFinalJobResult{JobID: prepared.JobID, Timing: handoffTiming()}
		if errors.Is(err, remotejob.ErrRemoteStaleJob) {
			receipt.Status = staleReceiptStatus("")
			return receipt, fmt.Errorf("remote finalize of %s is stale and must be replaced: %w: %w", prepared.JobID, scriptgen.ErrFinalJobStaleReceipt, err)
		}
		return receipt, err
	}
	return scriptgen.RemoteFinalJobResult{JobID: prepared.JobID, Status: "RUNNING", Timing: handoffTiming()}, nil
}

// staleReceiptStatus projects a stale transport outcome onto the capability's
// terminal receipt vocabulary. A Master-reported terminal status is kept as-is
// (it archives the real outcome); any other stale cause — a lease conflict — is
// archived as REJECTED so the receipt is terminal and the next attempt rotates
// the idempotency key instead of replaying the dead identity.
func staleReceiptStatus(remoteStatus string) string {
	switch strings.ToUpper(strings.TrimSpace(remoteStatus)) {
	case "FAILED", "CANCELLED", "CANCELED", "REJECTED":
		return strings.ToUpper(strings.TrimSpace(remoteStatus))
	default:
		return "REJECTED"
	}
}

// remoteJobInFlight reports a Master status that means the job is already
// leased and progressing — a status that must be resumed, never re-finalized.
func remoteJobInFlight(status string) bool {
	switch strings.ToUpper(strings.TrimSpace(status)) {
	case "RUNNING", "QUEUED", "RENDERING":
		return true
	default:
		return false
	}
}

// AttachFinalJob waits on a job a previous attempt already submitted: the poll
// phase of the split, with no PREPARE and no FINALIZE, so resuming cannot ask
// the Master for a second render.
func (a *remoteFinalJobAdapter) AttachFinalJob(ctx context.Context, jobID string, waitToCompletion bool) (scriptgen.RemoteFinalJobResult, error) {
	if a == nil || a.client == nil {
		return scriptgen.RemoteFinalJobResult{}, fmt.Errorf("remote final-job adapter is not configured")
	}
	id := strings.TrimSpace(jobID)
	if id == "" {
		return scriptgen.RemoteFinalJobResult{}, fmt.Errorf("remote final-job attach requires a job id")
	}
	// waitToCompletion is the capability's decision ("the wait was already
	// handed back once; run it to completion now"); the DURATION stays here,
	// where deployment config lives. Budget 0 means the client's full poll
	// timeout, i.e. exactly the pre-split blocking contract.
	budget := a.attachBudget
	if waitToCompletion {
		budget = 0
	}
	remote, err := a.client.Attach(ctx, id, budget)
	return finalJobResult(id, remote), finalJobWaitError(err)
}

// finalJobResult projects the client's Result onto the capability's durable
// receipt. The id is carried even on a failed wait: the Master job exists, and
// losing its address is what turns a resumable render into a duplicate one.
func finalJobResult(jobID string, remote remotejob.Result) scriptgen.RemoteFinalJobResult {
	id := strings.TrimSpace(remote.JobID)
	if id == "" {
		id = strings.TrimSpace(jobID)
	}
	return scriptgen.RemoteFinalJobResult{JobID: id, Status: remote.Status, WorkerID: remote.WorkerID, ArtifactURL: remote.ArtifactURL, SHA256: remote.SHA256, Timing: finalJobTiming(remote.Timing)}
}

// finalJobTiming projects the transport's Master-clock timing onto the durable
// receipt. A wait with no reported boundaries yields nil so an unmeasured
// receipt never claims zero queue/execution time; the poll count alone is still
// useful while a job is pending, so it is kept when there was at least one poll.
func finalJobTiming(timing remotejob.Timing) *scriptgen.RemoteFinalJobTiming {
	worker := finalJobWorkerTiming(timing.Worker)
	if worker == nil && timing.CreatedAt.IsZero() && timing.StartedAt.IsZero() && timing.CompletedAt.IsZero() &&
		timing.QueueWait == 0 && timing.Execution == 0 && timing.Wait == 0 && timing.Polls == 0 {
		return nil
	}
	projected := &scriptgen.RemoteFinalJobTiming{
		QueueMS:     timing.QueueWait.Milliseconds(),
		ExecutionMS: timing.Execution.Milliseconds(),
		WaitMS:      timing.Wait.Milliseconds(),
		PollLagMS:   timing.PollLag.Milliseconds(),
		Polls:       timing.Polls,
		Worker:      worker,
	}
	if !timing.CreatedAt.IsZero() {
		projected.MasterCreatedAt = timing.CreatedAt.UTC().Format(time.RFC3339Nano)
	}
	if !timing.StartedAt.IsZero() {
		projected.MasterStartedAt = timing.StartedAt.UTC().Format(time.RFC3339Nano)
	}
	if !timing.CompletedAt.IsZero() {
		projected.MasterCompletedAt = timing.CompletedAt.UTC().Format(time.RFC3339Nano)
	}
	return projected
}

// finalJobWorkerTiming projects the worker's own phase report. nil means the
// Master relayed no worker block, which must stay distinguishable from a worker
// that reported zeroes (a render with no download, e.g. a cached source).
func finalJobWorkerTiming(worker remotejob.WorkerTiming) *scriptgen.RemoteFinalJobWorkerTiming {
	if worker.LeasedAt.IsZero() && worker.StartedAt.IsZero() && worker.FinishedAt.IsZero() &&
		worker.Download == 0 && worker.Decode == 0 && worker.Compose == 0 && worker.Encode == 0 &&
		worker.Mux == 0 && worker.Upload == 0 && worker.Frames == 0 && worker.EncodeFPS == 0 {
		return nil
	}
	projected := &scriptgen.RemoteFinalJobWorkerTiming{
		DownloadMS: worker.Download.Milliseconds(),
		DecodeMS:   worker.Decode.Milliseconds(),
		ComposeMS:  worker.Compose.Milliseconds(),
		EncodeMS:   worker.Encode.Milliseconds(),
		MuxMS:      worker.Mux.Milliseconds(),
		UploadMS:   worker.Upload.Milliseconds(),
		Frames:     worker.Frames,
		EncodeFPS:  worker.EncodeFPS,
	}
	if !worker.LeasedAt.IsZero() {
		projected.LeasedAt = worker.LeasedAt.UTC().Format(time.RFC3339Nano)
	}
	if !worker.StartedAt.IsZero() {
		projected.StartedAt = worker.StartedAt.UTC().Format(time.RFC3339Nano)
	}
	if !worker.FinishedAt.IsZero() {
		projected.FinishedAt = worker.FinishedAt.UTC().Format(time.RFC3339Nano)
	}
	return projected
}

// mergeFinalJobTiming adds the phases only one side observed: the handoff is
// measured locally, the queue/execution/poll phases come from the attach. The
// capability owns the merge (see RemoteFinalJobTiming.Merge) so the runner's
// resume path cannot drop the handoff.
func mergeFinalJobTiming(base, extra *scriptgen.RemoteFinalJobTiming) *scriptgen.RemoteFinalJobTiming {
	return base.Merge(extra)
}

// finalJobWaitError maps the transport's "not terminal yet" sentinel onto the
// capability-owned one, so the runner branches on a typed error it owns without
// importing the transport package. Both sentinels stay in the chain (`%w`
// twice): the capability matches the one it owns, the wiring keeps the
// transport's for logs. A terminal Master failure or a transport fault passes
// through unchanged.
func finalJobWaitError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, remotejob.ErrRemotePending) {
		return fmt.Errorf("%w: %w", scriptgen.ErrFinalJobPending, err)
	}
	return err
}
