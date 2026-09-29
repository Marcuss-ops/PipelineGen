package wiring

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaregistry"
	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/checksum"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/drive"
	pgmedia "github.com/Marcuss-ops/PipelineGen/internal/platform/postgres/media"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/remotejob"
)

const defaultFinalJobMasterURL = "http://51.91.11.36:8000"

// defaultFinalJobAttachBudget keeps script.generate attached through the usual
// remote composite render. The old 60s default marked the local job FAILED while
// the Master was still rendering for 6-14 minutes; those renders continued, but
// their completed result was never joined back to the parent job. Leave enough
// room for the observed render times while remaining below the 60m script job
// timeout. Operators can still override this with
// VELOX_FINAL_JOB_ATTACH_SECONDS.
const defaultFinalJobAttachBudget = 50 * time.Minute

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
	drive  drive.Reader
	media  *pgmedia.MediaSearcher
	client *remotejob.Client
	// attachBudget bounds a single attempt's wait on the Master render; see
	// defaultFinalJobAttachBudget and finalJobAttachBudget.
	attachBudget time.Duration
}

func newRemoteFinalJobAdapter(root *ComposeRoot) (*remoteFinalJobAdapter, error) {
	if root == nil || root.Drive == nil || root.Drive.Reader == nil || root.MediaPostgres == nil {
		return nil, fmt.Errorf("remote final-job handoff requires Drive read access and the PostgreSQL media SSOT")
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
	budget, err := finalJobAttachBudget()
	if err != nil {
		return nil, err
	}
	client := remotejob.New(base, token)
	return &remoteFinalJobAdapter{drive: root.Drive.Reader, media: pgmedia.NewMediaSearcher(root.MediaPostgres), client: client, attachBudget: budget}, nil
}

func (a *remoteFinalJobAdapter) SubmitFinalJob(ctx context.Context, runID string, req scriptgen.GenerateRequest, result *scriptgen.GenerateResult) (scriptgen.RemoteFinalJobResult, error) {
	if a == nil || a.client == nil {
		return scriptgen.RemoteFinalJobResult{}, fmt.Errorf("remote final-job adapter is not configured")
	}
	pre, finalize, err := scriptgen.BuildFinalJobPayloads(ctx, runID, req, result, a)
	if err != nil {
		return scriptgen.RemoteFinalJobResult{}, err
	}
	// final_job=true on the local script request triggers this Master handoff.
	// PREPARE carries the scene plan; FINALIZE attaches runtime assets and
	// overlays so the Master refreshes prefetch before the worker starts. The id
	// is kept between the two phases and through the wait, so a bounded wait that
	// expires still reports a handle the next attempt can resume from — the job
	// exists on the Master either way.
	jobID, err := a.client.Prepare(ctx, pre)
	if err != nil {
		return scriptgen.RemoteFinalJobResult{}, err
	}
	if err := a.client.Finalize(ctx, jobID, finalize); err != nil {
		return scriptgen.RemoteFinalJobResult{JobID: jobID}, err
	}
	remote, waitErr := a.client.Attach(ctx, jobID, a.attachBudget)
	return finalJobResult(jobID, remote), finalJobWaitError(waitErr)
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
	if asset, ok, err := a.resolveEditorialMusicAsset(ctx, id); ok || err != nil {
		return asset, err
	}
	ref, _, err := a.assetRef(ctx, id)
	return ref, err
}

// resolveEditorialMusicAsset projects a curated BGM alias directly to its
// canonical Drive file. Editorial audio aliases intentionally live outside
// media_assets; the local BGM file is already required by audio compilation,
// so hash it here and verify it matches the Drive object's MD5 before asking
// the Master worker to prefetch it.
func (a *remoteFinalJobAdapter) resolveEditorialMusicAsset(ctx context.Context, alias string) (map[string]any, bool, error) {
	for _, editorial := range mediaregistry.EditorialAudioAssets() {
		if editorial.Alias != strings.TrimSpace(alias) || editorial.Family != "music" {
			continue
		}
		if a == nil || a.drive == nil {
			return nil, true, fmt.Errorf("remote final-job adapter has no Drive reader")
		}
		path := filepath.Join("data", "media", "sound_effects", editorial.Filename)
		file, err := os.Open(path)
		if err != nil {
			return nil, true, fmt.Errorf("open curated BGM %s: %w", editorial.Alias, err)
		}
		defer file.Close()
		info, err := file.Stat()
		if err != nil {
			return nil, true, fmt.Errorf("stat curated BGM %s: %w", editorial.Alias, err)
		}
		// Content identity streams through the digest SSOT (kernel/digest
		// SHA-256); the MD5 exists ONLY to equal the Drive object's
		// md5Checksum provider token and therefore streams through the md5
		// SSOT (platform/checksum) — the only package allowed to hash MD5.
		sha256Hex, err := digest.SHA256Reader(file)
		if err != nil {
			return nil, true, fmt.Errorf("hash curated BGM %s: %w", editorial.Alias, err)
		}
		if _, err := file.Seek(0, io.SeekStart); err != nil {
			return nil, true, fmt.Errorf("rewind curated BGM %s: %w", editorial.Alias, err)
		}
		md5Hex, err := checksum.LegacyMD5Reader(file)
		if err != nil {
			return nil, true, fmt.Errorf("md5 curated BGM %s: %w", editorial.Alias, err)
		}
		meta, err := a.drive.GetFileMeta(ctx, editorial.DriveFileID)
		if err != nil {
			return nil, true, err
		}
		remoteMD5, err := a.drive.GetFileMD5(ctx, editorial.DriveFileID)
		if err != nil {
			return nil, true, err
		}
		if meta == nil || meta.Size <= 0 || meta.Size != info.Size() || !strings.EqualFold(remoteMD5, md5Hex) {
			return nil, true, fmt.Errorf("curated BGM %s local bytes do not match its Drive file", editorial.Alias)
		}
		return map[string]any{
			"asset_id": editorial.Alias, "drive_file_id": editorial.DriveFileID,
			"url":    "velox-drive://" + editorial.DriveFileID,
			"sha256": sha256Hex, "size_bytes": info.Size(),
		}, true, nil
	}
	return nil, false, nil
}

// FinalJobPublishedFileSize reports the published size of a certified localized
// render, so a clip-only scene can be handed to the runtime as a complete asset
// reference (id + sha256 + size + duration) exactly like a library asset.
func (a *remoteFinalJobAdapter) FinalJobPublishedFileSize(ctx context.Context, driveFileID string) (int64, error) {
	if a == nil || a.drive == nil {
		return 0, fmt.Errorf("remote final-job adapter is not configured")
	}
	id := strings.TrimSpace(driveFileID)
	if id == "" {
		return 0, fmt.Errorf("drive file id is required")
	}
	meta, err := a.drive.GetFileMeta(ctx, id)
	if err != nil {
		return 0, err
	}
	return meta.Size, nil
}

func (a *remoteFinalJobAdapter) ListFinalJobStockFolder(ctx context.Context, folderID string) ([]scriptgen.FinalJobStockFile, error) {
	listed, err := a.drive.ListFiles(ctx, folderID)
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

func (a *remoteFinalJobAdapter) assetRef(ctx context.Context, id string) (map[string]any, int64, error) {
	asset, err := a.media.ResolveByMediaAssetID(ctx, id)
	if err != nil {
		return nil, 0, err
	}
	if asset == nil {
		assets, resolveErr := a.media.ResolveByDriveFileID(ctx, id)
		err = resolveErr
		if err != nil {
			return nil, 0, err
		}
		if len(assets) > 0 {
			asset = assets[0]
		}
	}
	if asset == nil {
		return nil, 0, fmt.Errorf("asset is absent from media SSOT")
	}
	driveID := strings.TrimSpace(asset.DriveFileID())
	if driveID == "" {
		driveID = strings.TrimSpace(id)
	}
	hash := strings.TrimSpace(asset.BinarySHA256())
	if len(hash) != 64 {
		return nil, 0, fmt.Errorf("asset %q has no canonical SHA-256", id)
	}
	meta, err := a.drive.GetFileMeta(ctx, driveID)
	if err != nil {
		return nil, 0, err
	}
	durationMS := asset.Duration.Milliseconds()
	if durationMS <= 0 {
		durationMS = int64(asset.LegacyDurationMSMirror())
	}
	if durationMS <= 0 {
		return nil, 0, fmt.Errorf("asset %q has no known duration", id)
	}
	ref := map[string]any{
		"asset_id": asset.ID, "drive_file_id": driveID, "url": "velox-drive://" + driveID,
		"sha256": hash, "size_bytes": meta.Size, "duration_ms": durationMS,
	}
	return ref, durationMS, nil
}

var (
	_ scriptgen.FinalJobSubmitter = (*remoteFinalJobAdapter)(nil)
	_ scriptgen.FinalJobAttacher  = (*remoteFinalJobAdapter)(nil)
)
