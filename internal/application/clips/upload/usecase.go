// Package upload — UseCase.Execute is the canonical clip-upload
// orchestration (CUTOVER, June 2026 P1.5).
//
// Wave 14 EXPAND produced command.go, ports.go, and result.go. The
// CUTOVER moves the 13-step orchestration previously inlined in
// internal/api/assets/clips/ingest.go::IngestHandler.UploadVideoClip
// into a typed use case behind the ports declared in this package.
// The handler is now thin transport only (AGENTS.md Pattern 8).
package upload

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	appassets "github.com/Marcuss-ops/PipelineGen/internal/application/assets"
	appclips "github.com/Marcuss-ops/PipelineGen/internal/application/clips"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	jobservice "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	"go.uber.org/zap"
)

// UseCase orchestrates the clip-upload pipeline behind the typed ports
// declared in ports.go. Zero infrastructure imports — every dependency
// is a typed port. The concrete wiring lives in
// internal/app/clips_adapters_*.go.
type UseCase struct {
	artifact       ArtifactServicePort
	driveUploader  DriveUploader
	dispatcher     IndexDispatcher
	cfg            Config
	treeBuilder    TreeBuilder
	jobsSvc        jobservice.Service
	processRunner  appassets.ProcessRunner
	log            *zap.Logger
}

// UseCaseDeps carries every dependency the upload use case needs.
// Wired once at composition time by the handler constructor.
type UseCaseDeps struct {
	Artifact      ArtifactServicePort
	DriveUploader DriveUploader
	Dispatcher    IndexDispatcher
	Config        Config
	TreeBuilder   TreeBuilder
	JobsSvc       jobservice.Service
	ProcessRunner appassets.ProcessRunner
	Log           *zap.Logger
}

// NewUseCase constructs the canonical upload use case. All ports are
// required (checked lazily at Execute time for test-friendliness).
func NewUseCase(d UseCaseDeps) *UseCase {
	if d.Log == nil {
		d.Log = zap.NewNop()
	}
	return &UseCase{
		artifact:      d.Artifact,
		driveUploader: d.DriveUploader,
		dispatcher:    d.Dispatcher,
		cfg:           d.Config,
		treeBuilder:   d.TreeBuilder,
		jobsSvc:       d.JobsSvc,
		processRunner: d.ProcessRunner,
		log:           d.Log,
	}
}

// Execute runs the full clip-upload pipeline.
//
// Steps absorbed from the legacy IngestHandler.UploadVideoClip:
//  1. Stream through artifact service (CreateAndVerify)
//  2. Resolve local path for Drive upload + duration probing
//  3. Resolve Drive target folder (root → group → GetOrCreateFolder)
//  4. Upload file to Google Drive (with description)
//  5. Upload cumulative metadata.json (best effort)
//  6. Build the MediaAsset record
//  7. Probe video duration (ffprobe / mediainfo)
//  8. Save via dispatcher (atomic UPSERT + outbox)
//  9. Upsert Asset Tree node
// 10. Enqueue async media.enrich job
//
// Duration is in milliseconds on the result (matches legacy contract).
func (uc *UseCase) Execute(ctx context.Context, cmd UploadClipCommand) (*UploadClipResult, error) {
	log := uc.log.With(
		zap.String("handler", "upload-use-case"),
		zap.String("filename", cmd.Filename),
		zap.String("name", cmd.Name),
	)

	// ── 1. Stream through artifact service ────────────────────────
	if uc.artifact == nil {
		return nil, ErrArtifactServiceUnavailable
	}
	ext := filepath.Ext(cmd.Filename)
	if ext == "" {
		ext = ".mp4"
	}
	mimeType := cmd.MimeType
	if mimeType == "" {
		mimeType = "video/mp4"
	}
	clipID := "manual_" + fmt.Sprintf("%d", time.Now().UnixNano())[:12]

	artifactRef, err := uc.artifact.CreateAndVerify(ctx, ArtifactCreateInput{
		ID:       clipID,
		Kind:     "video",
		MimeType: mimeType,
		Reader:   cmd.File,
	})
	if err != nil {
		log.Error("failed to store artifact", zap.Error(err))
		return nil, fmt.Errorf("upload: artifact CreateAndVerify: %w", err)
	}
	log.Info("artifact stored",
		zap.String("id", artifactRef.ID),
		zap.String("sha256", artifactRef.SHA256),
		zap.Int64("bytes", artifactRef.SizeBytes))

	fileHash := artifactRef.SHA256
	clipID = "manual_" + fileHash[:12]

	localPath, err := uc.artifact.LocalPath(ctx, artifactRef.ID)
	if err != nil {
		log.Warn("could not resolve local path for artifact",
			zap.String("id", artifactRef.ID), zap.Error(err))
		localPath = ""
	}

	// ── 2-3. Resolve Drive target folder ─────────────────────────
	targetFolderID := appclips.ExtractDriveFolderID(cmd.FolderID)
	if targetFolderID == "" {
		targetFolderID = uc.cfg.RootFolder()
		if cmd.Group != "" && targetFolderID != "" {
			dirID, ferr := uc.driveUploader.GetOrCreateFolder(ctx, cmd.Group, targetFolderID)
			if ferr != nil {
				log.Warn("failed to create group folder on Drive, using root",
					zap.String("group", cmd.Group), zap.Error(ferr))
			} else {
				targetFolderID = dirID
			}
		}
	} else if cmd.Group != "" {
		if existingName, ferr := uc.driveUploader.GetFolderName(ctx, targetFolderID); ferr == nil &&
			appclips.CleanFolderName(existingName) == appclips.CleanFolderName(cmd.Group) {
			log.Info("folder_id already points to group folder, reusing it",
				zap.String("folder_id", targetFolderID),
				zap.String("name", existingName))
		} else {
			dirID, ferr := uc.driveUploader.GetOrCreateFolder(ctx, cmd.Group, targetFolderID)
			if ferr != nil {
				log.Warn("failed to create group folder on Drive, using root",
					zap.String("group", cmd.Group), zap.Error(ferr))
			} else {
				targetFolderID = dirID
			}
		}
	}

	// ── 4. Upload to Drive ────────────────────────────────────────
	driveFilename := fmt.Sprintf("%s%s", cmd.Name, ext)
	var driveFileID, driveLink, downloadLink string
	if uc.driveUploader != nil && localPath != "" {
		driveDesc := appclips.BuildDriveDescription(cmd.Name, cmd.Description, "", cmd.Tags, cmd.Category, cmd.Source, "", "")
		result, uerr := uc.driveUploader.UploadFileWithDescription(ctx, localPath, targetFolderID, driveFilename, driveDesc)
		if uerr != nil {
			log.Warn("Drive upload failed, continuing with local file only", zap.Error(uerr))
		} else {
			driveFileID = result.FileID
			driveLink = result.WebViewLink
			downloadLink = result.DownloadLink
			log.Info("uploaded to Drive",
				zap.String("file_id", result.FileID),
				zap.String("drive_link", result.WebViewLink))
		}
	}

	// ── 5. Upload cumulative metadata.json (best effort) ──────────
	if uc.driveUploader != nil && targetFolderID != "" {
		clipEntry := map[string]interface{}{
			"clip_id":     clipID,
			"name":        cmd.Name,
			"description": cmd.Description,
			"category":    cmd.Category,
			"source":      cmd.Source,
			"tags":        cmd.Tags,
			"created_at":  time.Now().UTC().Format(time.RFC3339),
		}
		if driveFileID != "" {
			clipEntry["drive_file_id"] = driveFileID
			clipEntry["drive_link"] = driveLink
		}
		uc.updateCumulativeMetadataJSON(ctx, uc.cfg.TempPath(), targetFolderID, clipID, clipEntry, log)
	}

	// ── 6. Build the MediaAsset record ────────────────────────────
	now := time.Now().UTC()
	clip := &asset.Asset{
		ID:         clipID,
		Name:       cmd.Name,
		Filename:   driveFilename,
		Source:     asset.Source(cmd.Source),
		Category:   cmd.Category,
		Group:      cmd.Group,
		MediaType:  asset.MediaType("video"),
		Tags:       cmd.Tags,
		SearchText: cmd.Description,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	clip.SetLocalPath(localPath)
	clip.SetFileHash(fileHash)
	clip.SetFolderID(targetFolderID)
	clip.SetFolderPath(cmd.Group)
	if driveFileID != "" {
		clip.SetDriveLink(driveLink)
		clip.SetDownloadLink(downloadLink)
		clip.SetDriveFileID(driveFileID)
	}

	// ── 7. Probe video duration ───────────────────────────────────
	if localPath != "" {
		probeDuration(ctx, localPath, clip, log, uc.processRunner)
	}

	// ── 8. Save via dispatcher (atomic UPSERT + outbox) ───────────
	if uc.dispatcher == nil {
		log.Error("Execute: dispatcher not wired — atomic UPSERT + outbox enqueue refused",
			zap.String("clip_id", clip.ID))
		return nil, ErrDispatcherUnavailable
	}
	contentHash := clip.FileHash()
	if contentHash == "" {
		contentHash = fileHash
	}
	if err := uc.dispatcher.EnqueueAndIndex(ctx, clip, contentHash); err != nil {
		log.Error("Execute: dispatcher.EnqueueAndIndex failed",
			zap.String("clip_id", clip.ID), zap.Error(err))
		return nil, fmt.Errorf("upload: dispatcher.EnqueueAndIndex: %w", err)
	}
	log.Info("saved clip via dispatcher (atomic UPSERT + outbox event)",
		zap.String("clip_id", clip.ID), zap.String("content_hash", contentHash))

	// ── 9. Upsert Asset Tree ──────────────────────────────────────
	if uc.treeBuilder != nil {
		if err := uc.treeBuilder.UpsertFromAsset(ctx, clip); err != nil {
			log.Warn("failed to upsert to asset tree",
				zap.String("clip_id", clip.ID), zap.Error(err))
		}
	}

	// ── 10. Enqueue async media.enrich job ────────────────────────
	indexed := false
	if uc.jobsSvc != nil {
		_, err := uc.jobsSvc.Enqueue(ctx, &jobservice.EnqueueRequest{
			Type: jobservice.TypeMediaEnrich,
			Payload: map[string]any{
				"asset_id": clip.ID,
				"source":   cmd.Source,
			},
			ActiveKey: "enrich_clip_" + clip.ID,
		})
		if err != nil {
			log.Warn("failed to enqueue media.enrich job (clip is saved; reactive re-index required)",
				zap.String("clip_id", clip.ID), zap.Error(err))
		} else {
			indexed = true
		}
	}
	if indexed {
		log.Info("triggered async enrichment + Qdrant indexing", zap.String("clip_id", clip.ID))
	}

	return &UploadClipResult{
		OK:          true,
		ClipID:      clip.ID,
		Name:        clip.Name,
		Filename:    driveFilename,
		DriveLink:   clip.DriveLink(),
		DriveFileID: clip.DriveFileID(),
		FileHash:    fileHash,
		Source:      cmd.Source,
		Category:    cmd.Category,
		Tags:        cmd.Tags,
		LocalPath:   localPath,
		Indexed:     indexed,
		Duration:    int(clip.Duration.Milliseconds()),
	}, nil
}

// ── Moved helpers (from ingest.go) ──────────────────────────────────

// updateCumulativeMetadataJSON is a best-effort helper. Originally a
// no-op shim; preserved as-is for zero behaviour change.
func (uc *UseCase) updateCumulativeMetadataJSON(_ context.Context, _ string, _ string, _ string, _ map[string]interface{}, log *zap.Logger) {
	if log != nil {
		log.Debug("updateCumulativeMetadataJSON called")
	}
}

// probeDuration probes the video file for duration using ffprobe.
// Falls back to 0 if unavailable.
func probeDuration(ctx context.Context, localPath string, clip *asset.Asset, log *zap.Logger, runner appassets.ProcessRunner) {
	if clip == nil {
		return
	}
	probe := probeFFprobe(ctx, localPath, runner)
	if probe != nil && probe.Duration > 0 {
		clip.Duration = time.Duration(probe.Duration * float64(time.Second))
		return
	}
	dur := probeMediaInfo(ctx, localPath, runner)
	if dur > 0 {
		clip.Duration = time.Duration(dur) * time.Second
		return
	}
	log.Debug("could not probe video duration, leaving at 0",
		zap.String("path", localPath))
}

type ffprobeResult struct {
	Duration float64
}

func probeFFprobe(ctx context.Context, localPath string, runner appassets.ProcessRunner) *ffprobeResult {
	ffprobePath := "ffprobe"
	args := []string{
		"-v", "error",
		"-show_entries", "format=duration",
		"-of", "csv=p=0",
		localPath,
	}
	result, err := execCmd(ctx, ffprobePath, args, runner)
	if err != nil {
		return nil
	}
	output := strings.TrimSpace(result)
	if output == "" {
		return nil
	}
	var duration float64
	if _, err := fmt.Sscanf(output, "%f", &duration); err != nil {
		return nil
	}
	return &ffprobeResult{Duration: duration}
}

func probeMediaInfo(ctx context.Context, localPath string, runner appassets.ProcessRunner) int {
	result, err := execCmd(ctx, "mediainfo", []string{
		"--Inform=General;%Duration%",
		localPath,
	}, runner)
	if err != nil {
		return 0
	}
	output := strings.TrimSpace(result)
	if output == "" {
		return 0
	}
	var durationMs int
	if _, err := fmt.Sscanf(output, "%d", &durationMs); err != nil {
		return 0
	}
	return durationMs / 1000
}

func execCmd(ctx context.Context, name string, args []string, runner appassets.ProcessRunner) (string, error) {
	if runner == nil {
		return "", fmt.Errorf("process runner not configured")
	}
	result, err := runner.RunSimple(ctx, name, args...)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(result.Output), nil
}
