package wiring

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaexec"
	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
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
	return finalJobResult(started.JobID, remote), finalJobWaitError(waitErr)
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
	pre, finalize, err := scriptgen.BuildFinalJobPayloads(ctx, runID, req, result, a, a.segments)
	if err != nil {
		return scriptgen.RemoteFinalJobResult{}, err
	}
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
	prepared, err := a.client.PrepareJob(ctx, pre)
	if err != nil {
		if errors.Is(err, remotejob.ErrRemoteStaleJob) {
			return scriptgen.RemoteFinalJobResult{JobID: prepared.JobID, Status: staleReceiptStatus(prepared.Status)},
				fmt.Errorf("remote preparation %s is stale and must be replaced: %w: %w", prepared.JobID, scriptgen.ErrFinalJobStaleReceipt, err)
		}
		return scriptgen.RemoteFinalJobResult{}, err
	}
	if remoteJobInFlight(prepared.Status) {
		// The idempotent PREPARE replayed a job that is already leased and
		// running. Finalizing it is forbidden (and would fail with
		// job_already_leased); handing the handle back lets the next attempt
		// attach to the render that already exists.
		return scriptgen.RemoteFinalJobResult{JobID: prepared.JobID, Status: prepared.Status},
			fmt.Errorf("remote job %s is already leased and %s: %w", prepared.JobID, prepared.Status, scriptgen.ErrFinalJobPending)
	}
	if err := a.client.Finalize(ctx, prepared.JobID, finalize); err != nil {
		receipt := scriptgen.RemoteFinalJobResult{JobID: prepared.JobID}
		if errors.Is(err, remotejob.ErrRemoteStaleJob) {
			receipt.Status = staleReceiptStatus("")
			return receipt, fmt.Errorf("remote finalize of %s is stale and must be replaced: %w: %w", prepared.JobID, scriptgen.ErrFinalJobStaleReceipt, err)
		}
		return receipt, err
	}
	return scriptgen.RemoteFinalJobResult{JobID: prepared.JobID, Status: "RUNNING"}, nil
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
	return scriptgen.RemoteFinalJobResult{JobID: id, Status: remote.Status, WorkerID: remote.WorkerID, ArtifactURL: remote.ArtifactURL, SHA256: remote.SHA256}
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

func (a *remoteFinalJobAdapter) ResolveFinalJobAsset(ctx context.Context, id string) (map[string]any, error) {
	return a.ResolveFinalJobObjectStoreAsset(ctx, id)
}

// ResolveFinalJobStockAsset retains the explicitly allowed Drive path for
// selected stock-folder videos. Every other final-job asset goes through the
// HTTPS Objectstore resolver above.
func (a *remoteFinalJobAdapter) ResolveFinalJobStockAsset(ctx context.Context, id string) (map[string]any, error) {
	if a == nil || a.stock == nil {
		return nil, fmt.Errorf("final-job stock Drive reader is not configured")
	}
	id = strings.TrimSpace(id)
	if id == "" || strings.ContainsAny(id, "/\\\x00\r\n") {
		return nil, fmt.Errorf("final-job stock Drive id is invalid")
	}
	meta, err := a.stock.GetFileMeta(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("read stock Drive metadata %q: %w", id, err)
	}
	if meta == nil || meta.Size <= 0 {
		return nil, fmt.Errorf("stock Drive file %q has no positive byte size", id)
	}

	tmpDir, err := os.MkdirTemp("", "velox-final-job-stock-")
	if err != nil {
		return nil, fmt.Errorf("create temporary stock staging directory: %w", err)
	}
	defer os.RemoveAll(tmpDir)
	path := filepath.Join(tmpDir, "stock-video")
	reader, _, err := a.stock.DownloadFile(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("download stock Drive file %q: %w", id, err)
	}
	file, err := os.Create(path)
	if err != nil {
		_ = reader.Close()
		return nil, fmt.Errorf("create staged stock file: %w", err)
	}
	written, copyErr := io.Copy(file, reader)
	readerErr := reader.Close()
	fileErr := file.Close()
	if copyErr != nil {
		return nil, fmt.Errorf("copy stock Drive file %q: %w", id, copyErr)
	}
	if readerErr != nil {
		return nil, fmt.Errorf("close stock Drive response %q: %w", id, readerErr)
	}
	if fileErr != nil {
		return nil, fmt.Errorf("close staged stock file %q: %w", id, fileErr)
	}
	if written != meta.Size {
		return nil, fmt.Errorf("stock Drive file %q downloaded %d bytes, expected %d", id, written, meta.Size)
	}
	hash, size, err := digest.SHA256File(path)
	if err != nil {
		return nil, fmt.Errorf("hash stock Drive file %q: %w", id, err)
	}
	if a.probe == nil {
		return nil, fmt.Errorf("canonical media probe is not configured")
	}
	info, probeErr := a.probe.Probe(ctx, path)
	if info == nil || !info.HasVideo {
		return nil, fmt.Errorf("probe stock Drive video %q: video stream is missing", id)
	}
	resolved := asset.ResolveAssetDuration(info.Duration.Microseconds(), probeErr, 0, false)
	if !resolved.Known() {
		return nil, fmt.Errorf("probe stock Drive video %q: duration is unknown: %w", id, probeErr)
	}
	durationMS := resolved.DurationUS / 1000
	assetID := "stock-sha256-" + hash[:24]
	ref, err := scriptgen.PublishFinalJobLocalObject(ctx, assetID, path, hash, size)
	if err != nil {
		return nil, fmt.Errorf("publish stock Drive file %q to HTTPS Objectstore: %w", id, err)
	}
	ref["duration_ms"] = durationMS
	return ref, nil
}

// FinalJobPublishedFileSize is retained for the capability port's legacy
// shape, but direct HTTPS delivery never resolves sizes through Drive.
func (a *remoteFinalJobAdapter) FinalJobPublishedFileSize(context.Context, string) (int64, error) {
	return 0, fmt.Errorf("Drive file metadata is disabled for final-job delivery; use Objectstore HTTPS references")
}

func (a *remoteFinalJobAdapter) ListFinalJobStockFolder(ctx context.Context, folderID string) ([]scriptgen.FinalJobStockFile, error) {
	if a == nil || a.stock == nil {
		return nil, fmt.Errorf("remote final-job stock Drive reader is not configured")
	}
	listed, err := a.stock.ListFiles(ctx, folderID)
	if err != nil {
		return nil, err
	}
	files := make([]scriptgen.FinalJobStockFile, 0, len(listed))
	for _, f := range listed {
		if strings.HasPrefix(strings.ToLower(f.MimeType), "video/") || strings.HasSuffix(strings.ToLower(f.Name), ".mp4") {
			files = append(files, scriptgen.FinalJobStockFile{ID: f.ID, Name: f.Name})
		}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Name < files[j].Name })
	return files, nil
}

var (
	_ scriptgen.FinalJobSubmitter = (*remoteFinalJobAdapter)(nil)
	_ scriptgen.FinalJobAttacher  = (*remoteFinalJobAdapter)(nil)
)
