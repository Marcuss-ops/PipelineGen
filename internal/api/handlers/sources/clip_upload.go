package sources

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/artifacts"
	"github.com/Marcuss-ops/PipelineGen/internal/media/models"
	"github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
	"github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
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
	targetFolderID := extractDriveFolderID(folderID)
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
		if existingName, err := h.driveUploader.GetFolderName(ctx, targetFolderID); err == nil && cleanFoldName(existingName) == cleanFoldName(group) {
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
	var uploadResult *driveUploadResult
	if h.driveUploader != nil && localPath != "" {
		driveDescription := buildDriveDescription(name, description, "", tags, category, source, "", "")
		result, err := h.driveUploader.UploadFileWithDescription(ctx, localPath, targetFolderID, driveFilename, driveDescription)
		if err != nil {
			log.Warn("Drive upload failed, continuing with local file only",
				zap.Error(err))
		} else {
			uploadResult = &driveUploadResult{
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
		h.updateCumulativeMetadataJSON(ctx, targetFolderID, clipID, clipEntry, log)
	}

	// 8. Build the MediaAsset record
	now := time.Now().UTC()
	clip := &models.MediaAsset{
		ID:         clipID,
		Name:       name,
		Filename:   driveFilename,
		Source:     source,
		Category:   category,
		Group:      group,
		MediaType:  "video",
		Tags:       tags,
		SearchText: description,
		LocalPath:  localPath,
		FileHash:   fileHash,
		FolderID:   targetFolderID,
		FolderPath: group,
		Status:     "uploaded",
		CreatedAt:  now,
		UpdatedAt:  now,
	}

	if uploadResult != nil {
		clip.DriveLink = uploadResult.WebViewLink
		clip.DownloadLink = uploadResult.DownloadLink
		clip.DriveFileID = uploadResult.FileID
	}

	// 9. Probe video duration from local file
	if localPath != "" {
		probeDuration(ctx, localPath, clip, log)
	}

	// 10. Save to database
	if h.clipsRepo != nil {
		if err := h.clipsRepo.UpsertClip(ctx, clip); err != nil {
			log.Error("failed to save clip to DB", zap.Error(err))
			apiutil.InternalError(c, fmt.Errorf("failed to save clip: %w", err))
			return
		}
		log.Info("saved clip to DB", zap.String("clip_id", clip.ID))
	}

	// 11. Update Asset Tree
	if h.assetTreeSvc != nil {
		node := clipToAssetNode(clip)
		if err := h.assetTreeSvc.UpsertNode(ctx, node); err != nil {
			log.Warn("failed to upsert to asset tree", zap.String("clip_id", clip.ID), zap.Error(err))
		}
	}

	// 12. Trigger async enrichment + Qdrant indexing (reuses the existing pipeline)
	hasIndexer := h.clipIndexer != nil || h.vectorStore != nil || h.metaWriter != nil
	if hasIndexer {
		concurrent.SafeGo("upload-video-enrich", func() {
			h.enrichAndIndexClip(context.WithoutCancel(ctx), clip, source)
		})
		log.Info("triggered async enrichment + Qdrant indexing", zap.String("clip_id", clip.ID))
	}

	// 13. Return success response
	apiutil.OK(c, UploadVideoClipResponse{
		OK:          true,
		ClipID:      clip.ID,
		Name:        clip.Name,
		Filename:    driveFilename,
		DriveLink:   clip.DriveLink,
		DriveFileID: clip.DriveFileID,
		FileHash:    fileHash,
		Source:      source,
		Category:    category,
		Tags:        tags,
		LocalPath:   localPath,
		Indexed:     hasIndexer,
		Duration:    clip.Duration,
	})
}

// driveUploadResult is a simplified drive upload result for internal use.
type driveUploadResult struct {
	FileID       string
	WebViewLink  string
	DownloadLink string
}

// probeDuration probes the video file for duration using ffprobe.
// Falls back to 0 if unavailable.
func probeDuration(ctx context.Context, localPath string, clip *models.MediaAsset, log *zap.Logger) {
	if clip == nil {
		return
	}

	// Try ffprobe
	probe := probeFFprobe(ctx, localPath)
	if probe != nil && probe.Duration > 0 {
		clip.Duration = int(probe.Duration)
		return
	}

	// Fallback: try mediainfo if available
	dur := probeMediaInfo(ctx, localPath)
	if dur > 0 {
		clip.Duration = dur
		return
	}

	log.Debug("could not probe video duration, leaving at 0",
		zap.String("path", localPath))
}

// probeFFprobe runs ffprobe on the file and returns duration.
func probeFFprobe(ctx context.Context, localPath string) *ffprobeResult {
	ffprobePath := "ffprobe"
	args := []string{
		"-v", "error",
		"-show_entries", "format=duration",
		"-of", "csv=p=0",
		localPath,
	}

	result, err := execCmd(ctx, ffprobePath, args)
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
func probeMediaInfo(ctx context.Context, localPath string) int {
	result, err := execCmd(ctx, "mediainfo", []string{
		"--Inform=General;%Duration%",
		localPath,
	})
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
func execCmd(ctx context.Context, name string, args []string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(output), nil
}
