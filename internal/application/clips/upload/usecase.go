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
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	appclips "github.com/Marcuss-ops/PipelineGen/internal/application/clips"
	jobmedia "github.com/Marcuss-ops/PipelineGen/internal/domain/media"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"go.uber.org/zap"
)

// UseCase orchestrates the clip-upload pipeline behind the typed ports
// declared in ports.go. Zero infrastructure imports — every dependency
// is a typed port. The concrete wiring lives in
// internal/app/clips_adapters_*.go.
//
// F2.9 (June 2026): the legacy `driveUploader DriveUploader` field is
// REMOVED. Publisher is the single canonical Drive upload canal; the
// pre-F2.9 `else if uc.driveUploader != nil` fallback path is dropped
// entirely. Composition-time fail-fast (no panic on nil Publisher —
// the legacy field was nil-tolerant by design; FASE 9 hardening
// surfaces elsewhere if Publisher is nil).
type UseCase struct {
	artifact      ArtifactServicePort
	publisher     Publisher
	dispatcher    IndexDispatcher
	cfg           Config
	treeBuilder   TreeBuilder
	jobsSvc       job.Service
	processRunner appassets.ProcessRunner
	log           *zap.Logger
}

// UseCaseDeps carries every dependency the upload use case needs.
// Wired once at composition time by the handler constructor.
//
// F2.9 (June 2026): the `DriveUploader` deps field is REMOVED.
// Publisher is the only Drive-write canal. The pre-F2.9 wiring site
// passed `newClipsDriveAdapter(driveUploader, driveUploader)`; that
// line at internal/app/module_media.go is now deleted.
type UseCaseDeps struct {
	Artifact      ArtifactServicePort
	Publisher     Publisher
	Dispatcher    IndexDispatcher
	Config        Config
	TreeBuilder   TreeBuilder
	JobsSvc       job.Service
	ProcessRunner appassets.ProcessRunner
	Log           *zap.Logger
}

// NewUseCase constructs the canonical upload use case. Artifact and
// Dispatcher are mandatory — a nil value fails at composition time
// instead of returning HTTP 500 at request time (P0.1 fix, June 2026).
func NewUseCase(d UseCaseDeps) (*UseCase, error) {
	if d.Artifact == nil {
		return nil, fmt.Errorf("upload.NewUseCase: Artifact is required (composition root must wire *artifacts.Service via ArtifactServicePort adapter)")
	}
	if d.Dispatcher == nil {
		return nil, fmt.Errorf("upload.NewUseCase: Dispatcher is required (composition root must wire ClipIndexDispatcherPort)")
	}
	if d.Log == nil {
		d.Log = zap.NewNop()
	}
	return &UseCase{
		artifact:      d.Artifact,
		publisher:     d.Publisher,
		dispatcher:    d.Dispatcher,
		cfg:           d.Config,
		treeBuilder:   d.TreeBuilder,
		jobsSvc:       d.JobsSvc,
		processRunner: d.ProcessRunner,
		log:           d.Log,
	}, nil
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
//
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

	// ── 2-4. Upload to Drive via Publisher (F2.9: canonical only) ─
	// F2.9 (June 2026): the pre-F2.9 `else if uc.driveUploader != nil`
	// legacy fallback is DROPPED. Publisher is the single canonical
	// Drive upload canal; the destination folder is resolved
	// internally by Publisher via DestinationRegistry + PathBuilder.
	driveFilename := fmt.Sprintf("%s%s", cmd.Name, ext)
	var driveFileID, driveLink, downloadLink, targetFolderID string
	// F2.9 (June 2026): hoist PublishResult.MD5Checksum + PublishResult.Action
	// outside the publisher.Publish block so they can propagate to the
	// constructed clip below (parity with reupload_usecase.go::Execute per
	// user F2.9 spec "DB has drive_file_id/drive_link/download_link/md5/
	// publish_action populated"). Pre-F2.9 asymmetry: the upload path left
	// FileHash(pubRes.MD5Checksum) and publish_action unrecorded on the clip.
	var publishMD5, publishAction string

	if uc.publisher != nil && localPath != "" {
		pubReq := delivery.PublishRequest{
			Destination: delivery.DestinationYouTubeClip,
			LocalPath:   localPath,
			Filename:    driveFilename,
			Description: appclips.BuildDriveDescription(cmd.Name, cmd.Description, "", cmd.Tags, cmd.Category, cmd.Source, "", ""),
			ProjectID:   strings.TrimSpace(cmd.Source), // auto-derive Project from cmd.Source (godlike/06 SSOT, PR-P12-CLIPS-AND-BOOKS, July 2026)
			Group:       strings.TrimSpace(cmd.Group),  // explicit caller-provided group
			Subject:     strings.TrimSpace(cmd.Name),   // auto-derive Subject from clip.Name (godlike/06 SSOT)
			// RootFolderOverride RETIRED per PR-P12-CLIPS-AND-BOOKS (July 2026, deadline 2026-08-08).
			// The canonical Publisher resolves the target folder via
			// DestinationRegistry + DestinationPolicy.RootFolderID
			// (single source of truth for root folders per
			// architecture/current.yaml#DRIVE-AS-CENTRAL-CAPABILITY).
		}
		pubResult, uerr := uc.publisher.Publish(ctx, pubReq)
		if uerr != nil {
			log.Warn("Drive publish failed, continuing with local file only", zap.Error(uerr))
		} else {
			driveFileID = pubResult.FileID
			driveLink = pubResult.WebViewLink
			// F1.5 (P0 #9): read DownloadLink from the canonical
			// PublishResult instead of reconstructing it. Reconstructing
			// risks format drift (e.g. ?export=download vs plain uc?id=)
			// and produces different URLs for the same underlying
			// FileID depending on call site.
			downloadLink = pubResult.DownloadLink
			targetFolderID = pubResult.FolderID
			// F2.9: hoist MD5 + action for clip metadata propagation below.
			publishMD5 = pubResult.MD5Checksum
			publishAction = string(pubResult.Action)
			log.Info("published to Drive",
				zap.String("file_id", pubResult.FileID),
				zap.String("drive_link", pubResult.WebViewLink),
				zap.String("publish_action", string(pubResult.Action)))
		}
	}

	// ── 5. Upload cumulative metadata.json (F2.9: REMOVED) ────────
	// F2.9 (June 2026): the metadata.json sidecar maintenance that
	// pre-F2.9 lived here (via the no-op updateCumulativeMetadataJSON
	// method shim + an `if uc.driveUploader != nil` guard) is REMOVED.
	// The canonical metadata ledger is the artifacts.Registry +
	// dispatcher.EnqueueAndIndex pair (Step 8 below) — the Wave C
	// SSOT. The legacy metadata.json sidecar in upload_helpers.go
	// stays available as a package-private helper for callers that
	// still need it (no production caller today), but the upload
	// use case no longer threads the sidecar from here.

	// ── 6. Build the MediaAsset record ────────────────────────────
	now := time.Now().UTC()
	// New rows use the shared lifecycle factory. The canonical
	// media_assets.index_state column supplies its DISCOVERED default;
	// it must not be duplicated in metadata_json.
	lifecycleState, _ := asset.NewIndexableAssetState()
	clip := &asset.Asset{
		ID:             clipID,
		Name:           cmd.Name,
		Filename:       driveFilename,
		Source:         asset.Source(cmd.Source),
		Category:       cmd.Category,
		Group:          cmd.Group,
		MediaType:      asset.MediaType("video"),
		LifecycleState: lifecycleState,
		Tags:           cmd.Tags,
		SearchText:     cmd.Description,
		CreatedAt:      now,
		UpdatedAt:      now,
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
	// F2.9 (June 2026): propagate Publisher-returned MD5 + action onto
	// the constructed clip for upload-path symmetry with reupload.
	// Empty values are skipped (preserve pre-F2.9 behaviour when the
	// Publisher surfaces no MD5/action). Per user F2.9 spec, upload +
	// reupload must BOTH populate MD5 + publish_action so the dispatched
	// clip + DB row carries the 5-field audit contract.
	if publishMD5 != "" {
		clip.SetFileHash(publishMD5)
	}
	if publishAction != "" {
		clip.SetMetadataString("publish_action", publishAction)
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
		_, err := uc.jobsSvc.Enqueue(ctx, &job.EnqueueRequest{
			Type: jobmedia.TypeEnrich,
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

// F2.9 (June 2026): updateCumulativeMetadataJSON (no-op shim)
// REMOVED. Its single caller at Step 5 above is gone; the
// metadata.json sidecar maintenance is no longer threaded from
// the upload UseCase. The canonical helper still lives at
// appclips.UpdateCumulativeMetadataJSON (upload_helpers.go) for
// legacy callers, but the upload UseCase no longer calls it.
// CONTRACTS this closes:
//   - the duplicate stub (canonical lives in upload_helpers.go)
//   - the DriveUploader field dependency that drove the duplicate

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
