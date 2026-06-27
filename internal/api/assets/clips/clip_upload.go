package clips

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	appassets "github.com/Marcuss-ops/PipelineGen/internal/application/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/artifacts"
	appclips "github.com/Marcuss-ops/PipelineGen/internal/application/clips"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	jobservice "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	"github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// UploadVideoClipResponse is returned after a successful video upload.
type UploadVideoClipResponse struct {
	OK          bool     `json:"ok"`
	ClipID      string   `json:"clip_id"`
	Name        string   `json:"name"`
	Filename    string   `json:"filename"`
	DriveLink   string   `json:"drive_link,omitempty"`
	DriveFileID string   `json:"drive_file_id,omitempty"`
	FileHash    string   `json:"file_hash"`
	Source      string   `json:"source"`
	Category    string   `json:"category,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	LocalPath   string   `json:"local_path"`
	Indexed     bool     `json:"indexed"`
	Duration    int      `json:"duration,omitempty"`
}

// UploadVideoClip handles POST /api/media/upload-video
// Accepts multipart form data with a video file and metadata fields.
//
// Form fields:
//   - file:       (required) the video file
//   - name:       clip name (defaults to filename without extension)
//   - description: description / search text for Qdrant indexing
//   - tags:       JSON array of tags, e.g. ["funny","interview"]
//   - source:     source identifier (default "manual")
//   - category:   category
//   - group:      Drive subfolder group name
//   - folder_id:  Drive folder ID (if omitted, uses configured default root)
func (h *Handler) UploadVideoClip(c *gin.Context) {
	// 1. Parse multipart form (max 500MB)
	if err := c.Request.ParseMultipartForm(500 << 20); err != nil {
		apiutil.BadRequest(c, fmt.Sprintf("failed to parse multipart form: %v", err))
		return
	}

	// 2. Get the video file
	file, header, err := c.Request.FormFile("file")
	if err != nil {
		apiutil.BadRequest(c, "file field is required: "+err.Error())
		return
	}
	defer file.Close()

	// 3. Parse metadata from form fields
	name := strings.TrimSpace(c.PostForm("name"))
	description := strings.TrimSpace(c.PostForm("description"))
	source := strings.TrimSpace(c.PostForm("source"))
	category := strings.TrimSpace(c.PostForm("category"))
	group := strings.TrimSpace(c.PostForm("group"))
	folderID := strings.TrimSpace(c.PostForm("folder_id"))

	// Parse tags as JSON array (fallback: comma-separated)
	var tags []string
	if tagsStr := c.PostForm("tags"); tagsStr != "" {
		if err := json.Unmarshal([]byte(tagsStr), &tags); err != nil {
			for _, t := range strings.Split(tagsStr, ",") {
				if trimmed := strings.TrimSpace(t); trimmed != "" {
					tags = append(tags, trimmed)
				}
			}
		}
	}

	if source == "" {
		source = "manual"
	}
	if name == "" {
		name = strings.TrimSuffix(header.Filename, filepath.Ext(header.Filename))
	}

	ctx := c.Request.Context()
	log := h.log.With(
		zap.String("handler", "upload-video"),
		zap.String("filename", header.Filename),
		zap.String("name", name),
	)

	// 4. Stream uploaded file through artifact service (content-addressed storage)
	// This replaces os.Create + io.Copy + hashutil.MD5File with a single
	// Stage→Verify→Promote flow that computes SHA-256 and stores the blob
	// at a canonical content-addressed path.
	if h.artifactSvc == nil {
		apiutil.InternalError(c, fmt.Errorf("artifact service not available"))
		return
	}

	ext := filepath.Ext(header.Filename)
	if ext == "" {
		ext = ".mp4"
	}
	clipID := "manual_" + fmt.Sprintf("%d", time.Now().UnixNano())[:12]

	mimeType := header.Header.Get("Content-Type")
	if mimeType == "" {
		mimeType = "video/mp4"
	}

	artifact, err := h.artifactSvc.CreateAndVerify(ctx, artifacts.CreateInput{
		ID:       clipID,
		Kind:     "video",
		MimeType: mimeType,
		Reader:   file,
	})
	if err != nil {
		log.Error("failed to store artifact", zap.Error(err))
		apiutil.InternalError(c, fmt.Errorf("failed to store file: %w", err))
		return
	}
	log.Info("artifact stored",
		zap.String("id", artifact.ID),
		zap.String("sha256", artifact.SHA256),
		zap.Int64("bytes", artifact.SizeBytes))

	// 5. Resolve local path for Drive upload and duration probing
	fileHash := artifact.SHA256
	// Re-derive clipID from content hash to preserve dedup-by-content behavior:
	// uploading the same file twice gets the same clip ID → upsert instead of insert.
	clipID = "manual_" + fileHash[:12]
	localPath, err := h.artifactSvc.LocalPath(ctx, artifact.ID)
	if err != nil {
		log.Warn("could not resolve local path for artifact",
			zap.String("id", artifact.ID),
			zap.Error(err))
		// Fallback: use the artifact ID for Drive-less flows
		localPath = ""
	}

	// 6. Resolve Drive target folder
	targetFolderID := appclips.ExtractDriveFolderID(folderID)
	if targetFolderID == "" {
		// Use the MediaRootFolder as default root
		targetFolderID = h.cfg.Drive.RootFolder()
		if group != "" && targetFolderID != "" {
			dirID, err := h.driveUploader.GetOrCreateFolder(ctx, group, targetFolderID)
			if err != nil {
				log.Warn("failed to create group folder on Drive, using root",
					zap.String("group", group), zap.Error(err))
			} else {
				targetFolderID = dirID
			}
		}
	} else if group != "" {
		// Check if the target folder already IS the group folder (avoid nested duplicates)
		if existingName, err := h.driveUploader.GetFolderName(ctx, targetFolderID); err == nil && appclips.CleanFolderName(existingName) == appclips.CleanFolderName(group) {
			log.Info("folder_id already points to group folder, reusing it",
				zap.String("folder_id", targetFolderID),
				zap.String("name", existingName))
		} else {
			dirID, err := h.driveUploader.GetOrCreateFolder(ctx, group, targetFolderID)
			if err != nil {
				log.Warn("failed to create group folder on Drive, using root",
					zap.String("group", group), zap.Error(err))
			} else {
				targetFolderID = dirID
			}
		}
	}

	// 7. Upload file to Google Drive
	driveFilename := fmt.Sprintf("%s%s", name, ext)
	var uploadResult *DriveUploadResult
	if h.driveUploader != nil && localPath != "" {
		driveDescription := appclips.BuildDriveDescription(name, description, "", tags, category, source, "", "")
		result, err := h.driveUploader.UploadFileWithDescription(ctx, localPath, targetFolderID, driveFilename, driveDescription)
		if err != nil {
			log.Warn("Drive upload failed, continuing with local file only",
				zap.Error(err))
		} else {
			uploadResult = &DriveUploadResult{
				FileID:       result.FileID,
				WebViewLink:  result.WebViewLink,
				DownloadLink: result.DownloadLink,
			}
			log.Info("uploaded to Drive",
				zap.String("file_id", result.FileID),
				zap.String("drive_link", result.WebViewLink))
		}
	}

	// 7b. Upload cumulative metadata.json to Drive alongside the video
	if h.driveUploader != nil && targetFolderID != "" {
		clipEntry := map[string]interface{}{
			"clip_id":     clipID,
			"name":        name,
			"description": description,
			"category":    category,
			"source":      source,
			"tags":        tags,
			"created_at":  time.Now().UTC().Format(time.RFC3339),
		}
		if uploadResult != nil {
			clipEntry["drive_file_id"] = uploadResult.FileID
			clipEntry["drive_link"] = uploadResult.WebViewLink
		}
		h.updateCumulativeMetadataJSON(ctx, h.cfg.Storage.TempPath(), targetFolderID, clipID, clipEntry, log)
	}

	// 8. Build the MediaAsset record
	now := time.Now().UTC()
	clip := &asset.Asset{
		ID:         clipID,
		Name:       name,
		Filename:   driveFilename,
		Source:     asset.Source(source),
		Category:   category,
		Group:      group,
		MediaType:  asset.MediaType("video"),
		Tags:       tags,
		SearchText: description,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	clip.SetLocalPath(localPath)
	clip.SetFileHash(fileHash)
	clip.SetFolderID(targetFolderID)
	clip.SetFolderPath(group)

	if uploadResult != nil {
		clip.SetDriveLink(uploadResult.WebViewLink)
		clip.SetDownloadLink(uploadResult.DownloadLink)
		clip.SetDriveFileID(uploadResult.FileID)
	}

	// 9. Probe video duration from local file
	if localPath != "" {
		probeDuration(ctx, localPath, clip, log, h.processRunner)
	}

	// 10. Save to database. PR 2 (Wave 22 PR-2 — clip thin transport):
	//     route through appclips.ClipIndexDispatcherPort when wired;
	//     pass fileHash as the contentHash dedup-supersede gate key.
	//     Falls back to h.assetRepo.Upsert when nil — same documented
	//     semantics as CreateClip.
	if h.dispatcher != nil {
		if err := h.dispatcher.EnqueueAndIndex(ctx, clip, fileHash); err != nil {
			log.Error("clip dispatcher enqueue failed (upload)", zap.Error(err))
			apiutil.InternalError(c, fmt.Errorf("failed to dispatch clip: %w", err))
			return
		}
		log.Info("dispatched clip via index dispatcher", zap.String("clip_id", clip.ID))
	} else if h.assetRepo != nil {
		if err := h.assetRepo.Upsert(ctx, clip); err != nil {
			log.Error("failed to save clip to DB", zap.Error(err))
			apiutil.InternalError(c, fmt.Errorf("failed to save clip: %w", err))
			return
		}
		log.Info("saved clip to DB", zap.String("clip_id", clip.ID))
	}

	// 11. Update Asset Tree
	if h.assetTreeSvc != nil {
		node := appclips.ClipToAssetNode(clip)
		if err := h.assetTreeSvc.UpsertNode(ctx, node); err != nil {
			log.Warn("failed to upsert to asset tree", zap.String("clip_id", clip.ID), zap.Error(err))
		}
	}

	// 12. Trigger async enrichment + Qdrant indexing via canonical jobs
	// system (S1a, June 2026). The previous implementation spawned a
	// goroutine via `concurrent.SafeGo` + detached the ctx via
	// `context.WithoutCancel` to simulate a background job — forbidden
	// by AGENTS.md §7 + Pattern 8 (handler goroutines must not
	// orchestrate business work). Canonical path: enqueue a
	// `media.enrich` job whose worker is registered in
	// `internal/application/clips/media_enrich_worker.go` and runs in
	// the local broker pool (or a remote worker via VELOX_BROKER_URL),
	// with the same 3-minute hard cap that the registry records.
	indexed := false
	if hasIndexer := h.clipIndexer != nil || h.enrichUC != nil || h.metaWriter != nil; hasIndexer && h.jobsSvc != nil {
		_, err := h.jobsSvc.Enqueue(ctx, &jobservice.EnqueueRequest{
			Type: jobservice.TypeMediaEnrich,
			Payload: map[string]any{
				"asset_id": clip.ID,
				"source":   source,
			},
			ActiveKey: "enrich_clip_" + clip.ID,
		})
		if err != nil {
			log.Warn("failed to enqueue media.enrich job (clip is saved; reactive re-index required)",
				zap.String("clip_id", clip.ID), zap.Error(err))
		} else {
			indexed = true
		}
	} else if h.clipIndexer != nil || h.enrichUC != nil || h.metaWriter != nil {
		// S1a (June 2026): same misleading-fallback fix as CreateClip —
		// jobs service not wired but enrichment deps are. Stay silent
		// (indexed stays false). Production always wires jobsSvc;
		// a missing jobsSvc in test fixtures is the test author's
		// responsibility. A WARN log surfaces the drift.
		log.Warn("UploadVideoClip: enrichment deps wired but jobsSvc nil — clip saved; index will lag until reactive re-index",
			zap.String("clip_id", clip.ID), zap.String("source", source))
	}
	if indexed {
		log.Info("triggered async enrichment + Qdrant indexing", zap.String("clip_id", clip.ID))
	}

	// 13. Return success response
	apiutil.OK(c, UploadVideoClipResponse{
		OK:          true,
		ClipID:      clip.ID,
		Name:        clip.Name,
		Filename:    driveFilename,
		DriveLink:   clip.DriveLink(),
		DriveFileID: clip.DriveFileID(),
		FileHash:    fileHash,
		Source:      source,
		Category:    category,
		Tags:        tags,
		LocalPath:   localPath,
		Indexed:     indexed,
		Duration:    int(clip.Duration.Milliseconds()),
	})
}

// DriveUploadResult is a simplified drive upload result, exported so
// the sibling sources package (handler_sources_register_from_youtube.go)
// can construct one without depending on clips package internals.
type DriveUploadResult struct {
	FileID       string
	WebViewLink  string
	DownloadLink string
}

// probeDuration probes the video file for duration using ffprobe.
// Falls back to 0 if unavailable.
func probeDuration(ctx context.Context, localPath string, clip *asset.Asset, log *zap.Logger, runner appassets.ProcessRunner) {
	if clip == nil {
		return
	}

	// Try ffprobe
	probe := probeFFprobe(ctx, localPath, runner)
	if probe != nil && probe.Duration > 0 {
		clip.Duration = time.Duration(probe.Duration * float64(time.Second))
		return
	}

	// Fallback: try mediainfo if available
	dur := probeMediaInfo(ctx, localPath, runner)
	if dur > 0 {
		clip.Duration = time.Duration(dur) * time.Second
		return
	}

	log.Debug("could not probe video duration, leaving at 0",
		zap.String("path", localPath))
}

// probeFFprobe runs ffprobe on the file and returns duration.
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

type ffprobeResult struct {
	Duration float64
}

// probeMediaInfo runs mediainfo as a fallback probe.
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

// execCmd runs a command and returns stdout as a string.
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

