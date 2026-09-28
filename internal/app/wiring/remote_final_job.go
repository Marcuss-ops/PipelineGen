package wiring

import (
	"context"
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaregistry"
	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/drive"
	pgmedia "github.com/Marcuss-ops/PipelineGen/internal/platform/postgres/media"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/remotejob"
)

const defaultFinalJobMasterURL = "http://51.91.11.36:8000"

type remoteFinalJobAdapter struct {
	drive  drive.Reader
	media  *pgmedia.MediaSearcher
	client *remotejob.Client
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
	client := remotejob.New(base, token)
	return &remoteFinalJobAdapter{drive: root.Drive.Reader, media: pgmedia.NewMediaSearcher(root.MediaPostgres), client: client}, nil
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
	// overlays so the Master refreshes prefetch before the worker starts.
	remote, err := a.client.Submit(ctx, pre, finalize)
	return scriptgen.RemoteFinalJobResult{JobID: remote.JobID, Status: remote.Status, WorkerID: remote.WorkerID, ArtifactURL: remote.ArtifactURL, SHA256: remote.SHA256}, err
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
		sha := sha256.New()
		md5sum := md5.New()
		if _, err := io.Copy(io.MultiWriter(sha, md5sum), file); err != nil {
			return nil, true, fmt.Errorf("hash curated BGM %s: %w", editorial.Alias, err)
		}
		meta, err := a.drive.GetFileMeta(ctx, editorial.DriveFileID)
		if err != nil {
			return nil, true, err
		}
		remoteMD5, err := a.drive.GetFileMD5(ctx, editorial.DriveFileID)
		if err != nil {
			return nil, true, err
		}
		if meta == nil || meta.Size <= 0 || meta.Size != info.Size() || !strings.EqualFold(remoteMD5, hex.EncodeToString(md5sum.Sum(nil))) {
			return nil, true, fmt.Errorf("curated BGM %s local bytes do not match its Drive file", editorial.Alias)
		}
		return map[string]any{
			"asset_id": editorial.Alias, "drive_file_id": editorial.DriveFileID,
			"url":    "velox-drive://" + editorial.DriveFileID,
			"sha256": hex.EncodeToString(sha.Sum(nil)), "size_bytes": info.Size(),
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

var _ scriptgen.FinalJobSubmitter = (*remoteFinalJobAdapter)(nil)
