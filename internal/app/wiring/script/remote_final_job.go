package script

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

	"github.com/Marcuss-ops/PipelineGen/internal/app/knobs"
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

// deprecatedFinalJobWorker51ID is the pin this build used to apply when no
// operator override was configured. Worker 51 was quarantined on 2026-11-02, so
// a hardcoded default silently routed every handoff to an unhealthy worker: r9
// sat PENDING for 43 minutes against it. The pin is now opt-in
// (VELOX_FINAL_JOB_WORKER_ID); with no override, the Master places the job.
const deprecatedFinalJobWorker51ID = "velox-worker-523925eb"

// finalJobWorkerPin is the placement pin for a handoff, or "" when the Master
// must place the job itself. There is deliberately no default: an operator pin
// is a deployment fact (which worker is healthy), not something this binary can
// know. The retired worker-51 default is rejected loudly instead of silently
// resurrected by a stale environment file.
func finalJobWorkerPin() (string, error) {
	pin := strings.TrimSpace(knobs.String("VELOX_FINAL_JOB_WORKER_ID"))
	if pin == "" {
		return "", nil
	}
	if pin == deprecatedFinalJobWorker51ID {
		return "", fmt.Errorf("VELOX_FINAL_JOB_WORKER_ID pins the retired worker %s; unset it to let the Master place the job", pin)
	}
	return pin, nil
}

// finalJobQueueBudget reads VELOX_FINAL_JOB_QUEUE_SECONDS: how long a handoff
// may wait for the Master to lease and START the job before the wait is handed
// back. It bounds the failure mode r9 exposed (46 minutes in PENDING, then a
// cancellation): a queued job cannot finish, so holding the local worker for
// the whole render budget buys nothing. 0 disables the queue budget. A
// malformed value is a configuration error, not a silent default.
func finalJobQueueBudget() (time.Duration, error) {
	raw := strings.TrimSpace(knobs.String("VELOX_FINAL_JOB_QUEUE_SECONDS"))
	if raw == "" {
		return defaultFinalJobQueueBudget, nil
	}
	seconds, err := strconv.Atoi(raw)
	if err != nil || seconds < 0 {
		return 0, fmt.Errorf("VELOX_FINAL_JOB_QUEUE_SECONDS must be a non-negative integer number of seconds, got %q", raw)
	}
	return time.Duration(seconds) * time.Second, nil
}

// defaultFinalJobQueueBudget is generous for a healthy Master (r101 leased in
// 0 s, r103 in 58 s) and far below the 46-minute stall that motivated it.
const defaultFinalJobQueueBudget = 3 * time.Minute

// finalJobAttachBudget reads VELOX_FINAL_JOB_ATTACH_SECONDS. 0 disables the
// hand-off (every wait runs to completion: the pre-split blocking contract).
// A malformed value is a configuration error, not a silent default.
func finalJobAttachBudget() (time.Duration, error) {
	raw := strings.TrimSpace(knobs.String("VELOX_FINAL_JOB_ATTACH_SECONDS"))
	if raw == "" {
		return defaultFinalJobAttachBudget, nil
	}
	seconds, err := strconv.Atoi(raw)
	if err != nil || seconds < 0 {
		return 0, fmt.Errorf("VELOX_FINAL_JOB_ATTACH_SECONDS must be a non-negative integer number of seconds, got %q", raw)
	}
	return time.Duration(seconds) * time.Second, nil
}

type RemoteFinalJobAdapter struct {
	media  *pgmedia.MediaSearcher
	stock  drive.Reader
	client *remotejob.Client
	// attachBudget bounds a single attempt's wait on the Master render; see
	// defaultFinalJobAttachBudget and finalJobAttachBudget.
	attachBudget time.Duration
	// queueBudget bounds the NOT-STARTED portion of that wait; see
	// defaultFinalJobQueueBudget and finalJobQueueBudget. It is carried on the
	// transport client so the resume path shares the same rule.
	queueBudget time.Duration
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
	// resolvedRunID scopes the cache to ONE run. A prefetch for the SAME run
	// keeps what was already resolved; a different run (or a retry reusing
	// this adapter) must never inherit another run's identities.
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

// RemoteFinalJobConfig contains the resolved service dependencies. The app
// composition root owns ComposeRoot traversal; this package owns the handoff.
type RemoteFinalJobConfig struct {
	Media        *pgmedia.MediaSearcher
	Stock        drive.Reader
	Client       *remotejob.Client
	AttachBudget time.Duration
	QueueBudget  time.Duration
}

func NewRemoteFinalJobAdapter(cfg RemoteFinalJobConfig) (*RemoteFinalJobAdapter, error) {
	if cfg.Media == nil || cfg.Stock == nil || cfg.Client == nil {
		return nil, fmt.Errorf("remote final-job adapter requires media search, Drive stock reader, and client")
	}
	if cfg.AttachBudget <= 0 {
		cfg.AttachBudget = defaultFinalJobAttachBudget
	}
	if cfg.QueueBudget < 0 {
		return nil, fmt.Errorf("remote final-job queue budget cannot be negative")
	}
	cfg.Client.QueueTimeout = cfg.QueueBudget
	return &RemoteFinalJobAdapter{media: cfg.Media, stock: cfg.Stock, client: cfg.Client, attachBudget: cfg.AttachBudget, queueBudget: cfg.QueueBudget}, nil
}

func (a *RemoteFinalJobAdapter) BaseURL() string {
	if a == nil || a.client == nil {
		return ""
	}
	return a.client.BaseURL
}

func NewRemoteFinalJobAdapterFromEnvironment(media *pgmedia.MediaSearcher, stock drive.Reader) (*RemoteFinalJobAdapter, error) {
	if media == nil || stock == nil {
		return nil, fmt.Errorf("remote final-job handoff requires the media SSOT and Drive stock reader")
	}
	if strings.TrimSpace(os.Getenv("OBJECTSTORE_PUBLIC_URL")) == "" {
		return nil, fmt.Errorf("remote final-job HTTPS handoff requires OBJECTSTORE_PUBLIC_URL; only stock assets may use Drive")
	}
	token := strings.TrimSpace(knobs.String("VELOX_FINAL_JOB_M2M_SECRET"))
	if token == "" {
		path := strings.TrimSpace(knobs.String("VELOX_FINAL_JOB_CREDENTIALS_FILE"))
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
	base := strings.TrimSpace(knobs.String("VELOX_FINAL_JOB_MASTER_URL"))
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
	queueBudget, err := finalJobQueueBudget()
	if err != nil {
		return nil, err
	}
	return NewRemoteFinalJobAdapter(RemoteFinalJobConfig{
		Media: media, Stock: stock, Client: remotejob.New(base, token),
		AttachBudget: budget, QueueBudget: queueBudget,
	})
}

func (a *RemoteFinalJobAdapter) SubmitFinalJob(ctx context.Context, runID string, req scriptgen.GenerateRequest, result *scriptgen.GenerateResult) (scriptgen.RemoteFinalJobResult, error) {
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
func (a *RemoteFinalJobAdapter) StartFinalJob(ctx context.Context, runID string, req scriptgen.GenerateRequest, result *scriptgen.GenerateResult) (scriptgen.RemoteFinalJobResult, error) {
	if a == nil || a.client == nil {
		return scriptgen.RemoteFinalJobResult{}, fmt.Errorf("remote final-job adapter is not configured")
	}
	handoffStarted := time.Now()
	var resolveMS, prepareMS int64
	var payloadBuiltAt time.Time
	handoffTiming := func() *scriptgen.RemoteFinalJobTiming {
		timing := &scriptgen.RemoteFinalJobTiming{
			HandoffMS:        time.Since(handoffStarted).Milliseconds(),
			HandoffResolveMS: resolveMS,
			PrepareMS:        prepareMS,
		}
		if !payloadBuiltAt.IsZero() {
			// The payload build is a SUBSET of handoff_ms; measuring its end makes
			// the remainder attributable to FINALIZE instead of unknown.
			timing.PayloadMS = payloadBuiltAt.Sub(handoffStarted).Milliseconds()
		}
		return timing
	}
	a.beginHandoffCache(runID)
	a.resolverNS.Store(0)
	// Producer sends source assets and immutable windows directly. The Master
	// owns source materialization; the Producer must not cut or re-encode video
	// segments before FINALIZE.
	envelope, err := scriptgen.BuildFinalJobEnvelopeV1(ctx, runID, req, result, a)
	if err != nil {
		return scriptgen.RemoteFinalJobResult{}, err
	}
	payloadBuiltAt = time.Now()
	if err := attachFinalJobDriveSourcesV1(&envelope.Finalize); err != nil {
		return scriptgen.RemoteFinalJobResult{}, err
	}
	resolveMS = a.resolverNS.Load() / int64(time.Millisecond)
	a.resolvedMu.Lock()
	uniqueResolved, dedupHits := len(a.resolved), a.hits
	a.resolvedMu.Unlock()
	log.Printf("final_job handoff resolve_ms=%d unique_resolved=%d dedup_hits=%d",
		resolveMS, uniqueResolved, dedupHits)
	workerID, err := finalJobWorkerPin()
	if err != nil {
		return scriptgen.RemoteFinalJobResult{}, err
	}
	if workerID != "" {
		envelope.Prepare.PlacementPinWorkerID = workerID
		log.Printf("final_job placement pin=%s", workerID)
	} else {
		log.Printf("final_job placement pin=<master> (VELOX_FINAL_JOB_WORKER_ID unset)")
	}
	pre, finalize, err := envelope.MasterPayloads()
	if err != nil {
		return scriptgen.RemoteFinalJobResult{}, fmt.Errorf("serialize typed final-job envelope: %w", err)
	}
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
			return scriptgen.RemoteFinalJobResult{JobID: prepared.JobID, Status: staleReceiptStatus(prepared.Status), ReportOwner: scriptgen.FinalJobReportOwner, Timing: handoffTiming()},
				fmt.Errorf("remote preparation %s is stale and must be replaced: %w: %w", prepared.JobID, scriptgen.ErrFinalJobStaleReceipt, err)
		}
		return scriptgen.RemoteFinalJobResult{}, err
	}
	if remoteJobInFlight(prepared.Status) {
		// The idempotent PREPARE replayed a job that is already leased and
		// running. Finalizing it is forbidden (and would fail with
		// job_already_leased); handing the handle back lets the next attempt
		// attach to the render that already exists.
		return scriptgen.RemoteFinalJobResult{JobID: prepared.JobID, Status: prepared.Status, ReportOwner: scriptgen.FinalJobReportOwner, Timing: handoffTiming()},
			fmt.Errorf("remote job %s is already leased and %s: %w", prepared.JobID, prepared.Status, scriptgen.ErrFinalJobPending)
	}
	if err := a.client.Finalize(ctx, prepared.JobID, finalize); err != nil {
		receipt := scriptgen.RemoteFinalJobResult{JobID: prepared.JobID, ReportOwner: scriptgen.FinalJobReportOwner, Timing: handoffTiming()}
		if errors.Is(err, remotejob.ErrRemoteStaleJob) {
			receipt.Status = staleReceiptStatus("")
			return receipt, fmt.Errorf("remote finalize of %s is stale and must be replaced: %w: %w", prepared.JobID, scriptgen.ErrFinalJobStaleReceipt, err)
		}
		return receipt, err
	}
	return scriptgen.RemoteFinalJobResult{JobID: prepared.JobID, Status: "RUNNING", ReportOwner: scriptgen.FinalJobReportOwner, Timing: handoffTiming()}, nil
}

// attachFinalJobDriveSourcesV1 applies the transport locator policy to typed
// immutable asset references before the single Master serialization adapter.
func attachFinalJobDriveSourcesV1(finalize *scriptgen.FinalJobFinalizeV1) error {
	if finalize == nil {
		return fmt.Errorf("remote final-job FINALIZE DTO is nil")
	}
	for i := range finalize.RuntimeAssets {
		asset := &finalize.RuntimeAssets[i]
		if !strings.EqualFold(strings.TrimSpace(asset.Kind), "video") || !strings.EqualFold(strings.TrimSpace(asset.Role), "stock") {
			continue
		}
		driveID := strings.TrimSpace(asset.DriveFileID)
		if driveID == "" {
			return fmt.Errorf("remote final-job stock source %q has no Drive identity", asset.AssetID)
		}
		asset.SourceURI = driveStockDownloadURL(driveID)
		asset.URL = "velox-drive://" + driveID
	}
	for i := range finalize.RuntimeAssets {
		asset := &finalize.RuntimeAssets[i]
		source := strings.TrimSpace(asset.SourceURI)
		if source == "" {
			source = strings.TrimSpace(asset.URL)
		}
		if !strings.HasPrefix(strings.ToLower(source), "https://") {
			return fmt.Errorf("final_job runtime asset %q has no HTTPS source_uri", asset.AssetID)
		}
		if strings.TrimSpace(asset.AssetID) == "" {
			return fmt.Errorf("final_job runtime asset %d has no immutable asset_id", i)
		}
		asset.SourceURI = source
		if strings.EqualFold(strings.TrimSpace(asset.Role), "stock") {
			if strings.TrimSpace(asset.DriveFileID) == "" {
				return fmt.Errorf("final_job stock runtime asset %q has no Drive identity", asset.AssetID)
			}
			asset.URL = "velox-drive://" + strings.TrimSpace(asset.DriveFileID)
		} else {
			asset.URL = "velox-asset://" + strings.TrimSpace(asset.AssetID)
			asset.DriveFileID = ""
		}
	}
	return nil
}

// attachFinalJobDriveSources adds the Master's Drive fetch URI to stock
// references. Producer-owned video stays on its existing HTTPS source URI;
// FINALIZE never publishes or materializes a local video fragment.
func (a *RemoteFinalJobAdapter) attachFinalJobDriveSources(finalize map[string]any) error {
	assets, ok := finalize["runtime_assets"].([]any)
	if !ok {
		return fmt.Errorf("remote final-job runtime_assets has an invalid shape")
	}
	// Existing stock: leave it on Drive, exactly as the stockpipeline wrote it.
	for _, raw := range assets {
		ref, ok := raw.(map[string]any)
		if !ok || !strings.EqualFold(strings.TrimSpace(finalJobAdapterString(ref["kind"])), "video") {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(finalJobAdapterString(ref["role"])), "stock") {
			continue
		}
		driveID := strings.TrimSpace(finalJobAdapterString(ref["drive_file_id"]))
		if driveID == "" {
			return fmt.Errorf("remote final-job stock source %q has no Drive identity", finalJobAdapterString(ref["asset_id"]))
		}
		ref["source_uri"] = driveStockDownloadURL(driveID)
		ref["url"] = "velox-drive://" + driveID
	}
	if err := scriptgen.NormalizeFinalJobHTTPSRuntimeAssets(assets); err != nil {
		return fmt.Errorf("normalize remote final-job video sources: %w", err)
	}
	return nil
}

func finalJobAdapterString(value any) string {
	if value == nil {
		return ""
	}
	valueString := strings.TrimSpace(fmt.Sprint(value))
	if valueString == "<nil>" {
		return ""
	}
	return valueString
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
func (a *RemoteFinalJobAdapter) AttachFinalJob(ctx context.Context, jobID string, waitToCompletion bool) (scriptgen.RemoteFinalJobResult, error) {
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
	return scriptgen.RemoteFinalJobResult{JobID: id, Status: remote.Status, WorkerID: remote.WorkerID, ArtifactURL: remote.ArtifactURL, SHA256: remote.SHA256, ReportOwner: scriptgen.FinalJobReportOwner, Timing: finalJobTiming(remote.Timing)}
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
	if errors.Is(err, remotejob.ErrRemoteInvalidPlan) {
		// A rejected plan is not a stale identity: report it as its own terminal
		// class so the run fails on the plan instead of rotating the attempt.
		return fmt.Errorf("%w: %w", scriptgen.ErrFinalJobInvalidPlan, err)
	}
	if errors.Is(err, remotejob.ErrRemoteStaleJob) {
		return fmt.Errorf("%w: %w", scriptgen.ErrFinalJobStaleReceipt, err)
	}
	if errors.Is(err, remotejob.ErrRemotePending) {
		return fmt.Errorf("%w: %w", scriptgen.ErrFinalJobPending, err)
	}
	return err
}
