package script

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/app/knobs"
	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/remotejob"
)

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
	receipt, downloadErr := a.withDownloadedArtifact(ctx, receipt)
	if downloadErr != nil {
		return receipt, errors.Join(finalJobWaitError(waitErr), downloadErr)
	}
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
	if a.segments == nil {
		return scriptgen.RemoteFinalJobResult{}, fmt.Errorf("remote final-job non-stock segment certifier is not configured; refusing to send uncertified copy segments")
	}
	handoffStarted := time.Now()
	var resolveMS, prepareMS int64
	var payloadBuiltAt time.Time
	tracer, _ := a.segments.(finalJobSegmentHandoffTracer)
	var handoffScope uint64
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
		if tracer != nil && handoffScope != 0 {
			if prep := tracer.Timing(handoffScope); prep.Segments > 0 || prep.Bytes > 0 {
				timing.SegmentPrep = &prep
			}
		}
		return timing
	}
	a.beginHandoffCache(runID)
	a.resolverNS.Store(0)
	if tracer != nil {
		// The scope must be on the context BEFORE BuildFinalJobEnvelopeV1 asks
		// the preparer for fragments; otherwise the work is recorded under scope
		// 0 and no receipt can attribute it.
		ctx, handoffScope = tracer.BeginHandoff(ctx)
	}
	// Producer prepares only its own clip/overlay timeline fragments. Explicit
	// stock-pool slots bypass this path entirely and remain Master-owned.
	envelope, err := scriptgen.BuildFinalJobEnvelopeV1(ctx, runID, req, result, a, a.segments)
	if err != nil {
		return scriptgen.RemoteFinalJobResult{}, err
	}
	if a.preflight != nil {
		if err := a.preflightFinalJob(ctx, envelope.Plan); err != nil {
			return scriptgen.RemoteFinalJobResult{}, err
		}
	}
	payloadBuiltAt = time.Now()
	if err := attachFinalJobDriveSourcesV1(&envelope.Finalize); err != nil {
		return scriptgen.RemoteFinalJobResult{}, err
	}
	for index, asset := range envelope.Finalize.RuntimeAssets {
		if strings.EqualFold(asset.Role, "stock") {
			continue
		}
		log.Printf("final_job producer asset index=%d id=%s sha256=%s kind=%s role=%s", index, asset.AssetID, asset.SHA256, asset.Kind, asset.Role)
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
	keepFinalJobEnvelopes(runID, pre, finalize)
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
	receipt, downloadErr := a.withDownloadedArtifact(ctx, finalJobResult(id, remote))
	if downloadErr != nil {
		return receipt, errors.Join(finalJobWaitError(err), downloadErr)
	}
	return receipt, finalJobWaitError(err)
}

// FinalJobStatus reads the Master state without waiting: the local-cancel
// reconciler needs one answer, not another attach budget. A pending job is
// projected onto ErrFinalJobPending so the caller can tell "still working"
// from "terminal failure" without importing the transport package.
func (a *RemoteFinalJobAdapter) FinalJobStatus(ctx context.Context, jobID string) (scriptgen.RemoteFinalJobResult, error) {
	if a == nil || a.client == nil {
		return scriptgen.RemoteFinalJobResult{JobID: strings.TrimSpace(jobID)}, fmt.Errorf("remote final-job adapter is not configured")
	}
	id := strings.TrimSpace(jobID)
	remote, err := a.client.Poll(ctx, id)
	if errors.Is(err, remotejob.ErrRemotePending) {
		err = fmt.Errorf("%w: %s", scriptgen.ErrFinalJobPending, remote.Status)
	}
	return finalJobResult(id, remote), err
}

// CancelFinalJob stops a job the local run will no longer adopt. A 409 is not
// a failure: it means the Master finished first, which is exactly the race the
// reconciler resolves by re-reading the status.
func (a *RemoteFinalJobAdapter) CancelFinalJob(ctx context.Context, jobID string) error {
	if a == nil || a.client == nil {
		return fmt.Errorf("remote final-job adapter is not configured")
	}
	err := a.client.Cancel(ctx, jobID)
	if errors.Is(err, remotejob.ErrJobNotCancellable) {
		return fmt.Errorf("%w: %v", scriptgen.ErrFinalJobCancelRejected, err)
	}
	return err
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

// keepFinalJobEnvelopes writes the exact prepare/finalize envelopes to
// VELOX_FINAL_JOB_DUMP_DIR when it is set. A copy-only rejection from the
// worker names a cache path on a machine this Producer cannot read, so the only
// way to answer "which segment was that" is the plan that produced it. Off by
// default: the envelopes carry script text and asset locators.
func keepFinalJobEnvelopes(runID string, prepare, finalize map[string]any) {
	dir := strings.TrimSpace(knobs.String("VELOX_FINAL_JOB_DUMP_DIR"))
	if dir == "" {
		return
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		log.Printf("final_job envelope dump dir %s: %v", dir, err)
		return
	}
	for name, body := range map[string]map[string]any{"prepare": prepare, "finalize": finalize} {
		raw, err := json.MarshalIndent(body, "", "  ")
		if err != nil {
			log.Printf("final_job envelope dump %s: %v", name, err)
			continue
		}
		path := filepath.Join(dir, runID+"-"+name+".json")
		if err := os.WriteFile(path, raw, 0o600); err != nil {
			log.Printf("final_job envelope dump %s: %v", path, err)
		}
	}
}
