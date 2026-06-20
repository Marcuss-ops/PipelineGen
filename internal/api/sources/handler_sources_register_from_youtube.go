package sources

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	apiutil "github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
	clipsources "github.com/Marcuss-ops/PipelineGen/internal/api/sources/clips"
	"github.com/Marcuss-ops/PipelineGen/internal/assets"
	concurrent "github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"
	downloader "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/downloader"
	hashutil "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/files"
	metrics "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/observability"
)

// RegisterFromYouTubeRequest is the JSON body for registering a clip from a YouTube URL.
type RegisterFromYouTubeRequest struct {
	URL         string   `json:"url" binding:"required"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Tags        []string `json:"tags"`
	Source      string   `json:"source"`
	Category    string   `json:"category"`
	Group       string   `json:"group"`
	FolderID    string   `json:"folder_id"`
	Start       float64  `json:"start"`
	End         float64  `json:"end"`
	Force       bool     `json:"force"`
}

// RegisterFromYouTube handles POST /api/media/register-from-youtube
func (h *Handler) RegisterFromYouTube(c *gin.Context) {
	req, ok := bindJSON[RegisterFromYouTubeRequest](c)
	if !ok {
		return
	}

	ctx := c.Request.Context()

	// Sanitize URL: extract video ID, start/end from raw URL and build a
	// clean URL. Handles both standard (&) and non-standard (?) separators
	// e.g. watch?v=ID?start=X&end=Y or watch?v=ID&start=X&end=Y.
	// The clean URL (no &) passes security.ValidateDownloadURL.
	{
		rawURL := req.URL
		// Extract video ID: everything between ?v= and the next separator
		videoID := ""
		if idx := strings.Index(rawURL, "v="); idx != -1 {
			rest := rawURL[idx+2:]
			for i, c := range rest {
				if c == '&' || c == '?' || c == '#' {
					videoID = rest[:i]
					break
				}
			}
			if videoID == "" {
				videoID = rest
			}
		}
		// Extract start/end from the raw URL by scanning for key=value pairs
		extractParam := func(key string) string {
			prefixes := []string{"&" + key + "=", "?" + key + "="}
			for _, pfx := range prefixes {
				if idx := strings.Index(rawURL, pfx); idx != -1 {
					rest := rawURL[idx+len(pfx):]
					for i, c := range rest {
						if c == '&' || c == '?' || c == '#' {
							return rest[:i]
						}
					}
					return rest
				}
			}
			return ""
		}
		if req.Start == 0 {
			if s := extractParam("start"); s != "" {
				if v, err := strconv.ParseFloat(s, 64); err == nil {
					req.Start = v
				}
			}
		}
		if req.End == 0 {
			if s := extractParam("end"); s != "" {
				if v, err := strconv.ParseFloat(s, 64); err == nil {
					req.End = v
				}
			}
		}
		if videoID != "" {
			req.URL = "https://www.youtube.com/watch?v=" + videoID
		}
	}

	log := h.log.With(
		zap.String("handler", "register-from-youtube"),
		zap.String("url", req.URL),
	)

	// 1. Fetch YouTube metadata first
	ytdlp := downloader.NewYTDLP(h.cfg)
	meta, metaErr := ytdlp.GetVideoMetadata(ctx, req.URL)
	if metaErr != nil {
		log.Warn("failed to fetch YouTube metadata, continuing without it",
			zap.Error(metaErr))
	}

	// Extract video ID from URL
	videoID := ""
	if meta != nil && meta.ID != "" {
		videoID = meta.ID
	} else {
		for _, part := range strings.Split(req.URL, "&") {
			if strings.HasPrefix(part, "v=") || strings.Contains(part, "?v=") {
				if idx := strings.Index(part, "v="); idx != -1 {
					id := part[idx+2:]
					if len(id) > 11 {
						id = id[:11]
					}
					videoID = id
					break
				}
			}
		}
		if videoID == "" && strings.Contains(req.URL, "youtu.be/") {
			videoID = req.URL[strings.LastIndex(req.URL, "/")+1:]
		}
	}
	if videoID == "" {
		videoID = fmt.Sprintf("%d", time.Now().UnixNano())
	}

	// Fill name from YouTube title if not provided
	name := strings.TrimSpace(req.Name)
	if name == "" && meta != nil && meta.Title != "" {
		name = meta.Title
	}
	if name == "" {
		name = videoID
	}

	// Resolve source label early
	source := strings.TrimSpace(req.Source)
	if source == "" {
		source = "youtube-manual"
	}

	// Dedup pre-check
	if !req.Force && h.clipsRepo != nil {
		if existing, dedupErr := h.findExistingYouTubeClip(ctx, videoID, req.URL, req.Start, req.End); dedupErr == nil && existing != "" {
			metrics.DedupHits.WithLabelValues(source, "precheck").Inc()
			log.Info("dedup hit: returning existing clip",
				zap.String("existing_clip_id", existing),
				zap.String("video_id", videoID))
			if existingClip, gerr := h.clipsRepo.GetClip(ctx, existing); gerr == nil && existingClip != nil {
				apiutil.OK(c, gin.H{
					"ok":            true,
					"duplicate":     true,
					"clip_id":       existingClip.ID,
					"video_id":      videoID,
					"name":          existingClip.Name,
					"filename":      existingClip.Filename,
					"duration_sec":  int64(existingClip.Duration.Seconds()),
					"drive_link":    existingClip.DriveLink(),
					"drive_file_id": existingClip.DriveFileID(),
					"file_hash":     existingClip.FileHash(),
					"source":        string(existingClip.Source),
					"category":      existingClip.Category,
					"tags":          existingClip.Tags,
					"local_path":    existingClip.LocalPath(),
					"indexed":       h.clipIndexer != nil || h.vectorStore != nil,
					"message":       "clip already registered for this YouTube video",
				})
				return
			}
		} else if dedupErr != nil {
			metrics.DedupMisses.WithLabelValues(source, "precheck_error").Inc()
			log.Warn("dedup pre-check failed, proceeding with registration",
				zap.String("video_id", videoID), zap.Error(dedupErr))
		} else {
			metrics.DedupMisses.WithLabelValues(source, "precheck").Inc()
		}
	}

	// Validate start/end
	if req.End > 0 && req.Start >= req.End {
		apiutil.BadRequest(c, fmt.Sprintf("invalid segment: start (%.1f) must be less than end (%.1f)", req.Start, req.End))
		return
	}
	if req.Start < 0 || req.End < 0 {
		apiutil.BadRequest(c, "start and end must be non-negative")
		return
	}
	if meta != nil && meta.Duration > 0 {
		if req.Start > 0 && req.Start >= meta.Duration {
			apiutil.BadRequest(c, fmt.Sprintf("start (%.1f) exceeds video duration (%.1f)", req.Start, meta.Duration))
			return
		}
		if req.End > meta.Duration {
			log.Warn("end exceeds video duration, yt-dlp will clip to end",
				zap.Float64("end", req.End), zap.Float64("duration", meta.Duration))
		}
	}

	// Name collision warning
	if h.clipsRepo != nil {
		if existingNameID, _ := h.clipsRepo.FindByName(ctx, name); existingNameID != "" {
			log.Warn("name collision: another clip with same name exists",
				zap.String("existing_id", existingNameID), zap.String("name", name))
		}
	}

	// Fill description from YouTube description if not provided
	description := strings.TrimSpace(req.Description)
	if description == "" && meta != nil && meta.Description != "" {
		description = meta.Description
		if len(description) > 1000 {
			description = description[:1000]
		}
	}

	log.Info("registering YouTube video",
		zap.String("video_id", videoID),
		zap.String("name", name))

	// 2. Resolve Drive target folder
	targetFolderID := clipsources.ExtractDriveFolderID(strings.TrimSpace(req.FolderID))
	if targetFolderID == "" {
		targetFolderID = h.cfg.Drive.ClipsFolder()
		if targetFolderID == "" {
			targetFolderID = h.cfg.Drive.RootFolder()
		}
	}
	group := strings.TrimSpace(req.Group)
	if group != "" && targetFolderID != "" {
		if existingName, err := h.driveUploader.GetFolderName(ctx, targetFolderID); err == nil && clipsources.CleanFolderName(existingName) == clipsources.CleanFolderName(group) {
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

	// Create per-video subfolder inside the channel/group folder
	if targetFolderID != "" && videoID != "" {
		videoSlug := videoID
		if req.Name != "" {
			titleSlug := textutil.SlugifyWithMax(req.Name, 60)
			if titleSlug != "" {
				videoSlug = videoID + "-" + titleSlug
			}
		}
		videoFolderID, err := h.driveUploader.GetOrCreateFolder(ctx, videoSlug, targetFolderID)
		if err != nil {
			log.Warn("failed to create video subfolder, using parent",
				zap.String("slug", videoSlug), zap.Error(err))
		} else {
			targetFolderID = videoFolderID
			log.Info("using per-video subfolder",
				zap.String("slug", videoSlug),
				zap.String("folder_id", videoFolderID))
		}
	}

	// 3. Download the video
	var downloadedPath string
	var fileHash string
	hasSegment := req.End > req.Start

	if hasSegment {
		cacheKey := "full:" + videoID
		if cached, ok := h.downloadCache.Load(cacheKey); ok {
			cachedPath := cached.(string)
			if _, err := os.Stat(cachedPath); err == nil {
				log.Info("reusing cached full video for segment extraction",
					zap.String("cached_path", cachedPath))
				downloadedPath = cachedPath
			}
		}

		if downloadedPath == "" {
			tempFilename := fmt.Sprintf("yt_%s_full_%d", videoID, time.Now().UnixNano())
			tempPath := filepath.Join(h.cfg.Storage.TempPath(), tempFilename)

			log.Info("downloading full YouTube video for segment extraction",
				zap.String("url", req.URL), zap.String("output", tempPath))

			downloadReq := &downloader.DownloadRequest{
				URL:         req.URL,
				OutputPath:  tempPath,
				MergeFormat: "mp4",
				Timeout:     10 * time.Minute,
			}
			downloadStart := time.Now()
			if err := ytdlp.Download(ctx, downloadReq); err != nil {
				// Retry with cookies for age-restricted videos
				downloadReq.UseCookies = true
				if err2 := ytdlp.Download(ctx, downloadReq); err2 != nil {
					log.Error("failed to download YouTube video (with and without cookies)", zap.Error(err2))
					apiutil.InternalError(c, fmt.Errorf("failed to download video: %w", err2))
					return
				}
			}
			log.Info("full video download complete", zap.Duration("elapsed", time.Since(downloadStart)))

			downloadedPath = resolveDownloadedPath(tempPath)
			if downloadedPath == "" {
				log.Error("downloaded file not found", zap.String("expected", tempPath))
				apiutil.InternalError(c, fmt.Errorf("downloaded file not found"))
				return
			}
			h.downloadCache.Store(cacheKey, downloadedPath)
		}

		segmentFilename := fmt.Sprintf("yt_%s_%d_%d.mp4", videoID, int(req.Start), int(req.End))
		segmentPath := filepath.Join(h.cfg.Storage.TempPath(), segmentFilename)

		log.Info("extracting segment from cached video",
			zap.Float64("start", req.Start), zap.Float64("end", req.End),
			zap.String("output", segmentPath))

		if err := cutVideoSegment(downloadedPath, segmentPath, req.Start, req.End); err != nil {
			log.Error("failed to cut video segment", zap.Error(err))
			apiutil.InternalError(c, fmt.Errorf("failed to cut video segment: %w", err))
			return
		}
		downloadedPath = segmentPath
	} else {
		cacheKey := "full:" + videoID
		if cached, ok := h.downloadCache.Load(cacheKey); ok {
			cachedPath := cached.(string)
			if _, err := os.Stat(cachedPath); err == nil {
				log.Info("reusing cached full video", zap.String("cached_path", cachedPath))
				downloadedPath = cachedPath
			}
		}

		if downloadedPath == "" {
			tempFilename := fmt.Sprintf("yt_%s_%d", videoID, time.Now().UnixNano())
			tempPath := filepath.Join(h.cfg.Storage.TempPath(), tempFilename)

			log.Info("downloading YouTube video",
				zap.String("url", req.URL), zap.String("output", tempPath))

			downloadReq := &downloader.DownloadRequest{
				URL:         req.URL,
				OutputPath:  tempPath,
				MergeFormat: "mp4",
				Timeout:     10 * time.Minute,
			}
			downloadStart := time.Now()
			if err := ytdlp.Download(ctx, downloadReq); err != nil {
				// Retry with cookies for age-restricted videos
				downloadReq.UseCookies = true
				if err2 := ytdlp.Download(ctx, downloadReq); err2 != nil {
					log.Error("failed to download YouTube video (with and without cookies)", zap.Error(err2))
					apiutil.InternalError(c, fmt.Errorf("failed to download video: %w", err2))
					return
				}
			}
			log.Info("download complete", zap.Duration("elapsed", time.Since(downloadStart)))

			downloadedPath = resolveDownloadedPath(tempPath)
			if downloadedPath == "" {
				log.Error("downloaded file not found", zap.String("expected", tempPath))
				apiutil.InternalError(c, fmt.Errorf("downloaded file not found"))
				return
			}
			h.downloadCache.Store(cacheKey, downloadedPath)
		}
	}

	log.Info("downloaded file resolved", zap.String("path", downloadedPath))

	// 4. Compute MD5 hash
	fileHash, err := hashutil.MD5File(downloadedPath)
	if err != nil {
		if !hasSegment {
			os.Remove(downloadedPath)
		}
		log.Error("failed to hash file", zap.Error(err))
		apiutil.InternalError(c, fmt.Errorf("failed to hash file: %w", err))
		return
	}

	clipID := fmt.Sprintf("yt_%s_%s", videoID, fileHash[:8])

	// 4b. Transcribe audio with Whisper (best-effort, non-fatal)
	transcript, detectedLang := h.transcribeAudio(ctx, downloadedPath, log)
	if transcript != "" {
		h.saveTranscriptAndStage(downloadedPath, transcript, group, log)
	}

	// 5. Upload to Google Drive
	ext := ".mp4"
	driveFilename := fmt.Sprintf("%s - %s%s", videoID, name, ext)
	var uploadResult *clipsources.DriveUploadResult
	if h.driveUploader != nil {
		driveDescription := clipsources.BuildDriveDescription(name, req.Description, description, req.Tags, req.Category, req.Source, req.URL, videoID)
		result, err := h.driveUploader.UploadFileWithDescription(ctx, downloadedPath, targetFolderID, driveFilename, driveDescription)
		if err != nil {
			log.Warn("Drive upload failed, continuing with local file only",
				zap.Error(err))
		} else {
			uploadResult = &clipsources.DriveUploadResult{
				FileID:       result.FileID,
				WebViewLink:  result.WebViewLink,
				DownloadLink: result.DownloadLink,
			}
			log.Info("uploaded to Drive",
				zap.String("file_id", result.FileID),
				zap.String("drive_link", result.WebViewLink))
		}
	}

	// 5b. Upload metadata.json to Drive alongside the video
	if h.driveUploader != nil && targetFolderID != "" {
		clipEntry := map[string]interface{}{
			"clip_id":       clipID,
			"name":          name,
			"description":   description,
			"category":      req.Category,
			"source":        source,
			"group":         group,
			"tags":          req.Tags,
			"youtube_url":   req.URL,
			"youtube_id":    videoID,
			"filename":      driveFilename,
			"file_hash":     fileHash,
			"duration_sec":  0,
			"created_at":    time.Now().UTC().Format(time.RFC3339),
			"drive_file_id": "",
			"drive_link":    "",
		}
		if meta != nil {
			if meta.Title != "" {
				clipEntry["youtube_title"] = meta.Title
			}
			if meta.Uploader != "" {
				clipEntry["youtube_uploader"] = meta.Uploader
			}
			if meta.UploadDate != "" {
				clipEntry["youtube_upload_date"] = meta.UploadDate
			}
			if meta.Duration > 0 {
				clipEntry["duration_sec"] = int(meta.Duration)
			}
		}
		if transcript != "" {
			clipEntry["clean_transcript"] = transcript
		}
		if detectedLang != "" {
			clipEntry["language"] = detectedLang
		}
		if req.Start > 0 {
			clipEntry["start_sec"] = req.Start
		}
		if req.End > 0 {
			clipEntry["end_sec"] = req.End
		}
		if uploadResult != nil {
			clipEntry["drive_file_id"] = uploadResult.FileID
			clipEntry["drive_link"] = uploadResult.WebViewLink
		}

		clipsources.UpdateCumulativeMetadataJSON(ctx, h.driveUploader, h.cfg.Storage.TempPath(), targetFolderID, clipID, clipEntry, log)
	}

	// 6. Compute duration
	duration := 0
	if meta != nil && meta.Duration > 0 {
		duration = int(meta.Duration)
	} else if req.End > req.Start {
		duration = int(req.End - req.Start)
	}

	// 7. Create MediaAsset record
	now := time.Now().UTC()
	clip := &assets.Asset{
		ID:         clipID,
		Name:       name,
		Filename:   driveFilename,
		Source:     assets.Source(source),
		Category:   req.Category,
		Group:      group,
		MediaType:  assets.MediaType("video"),
		Tags:       req.Tags,
		SearchText: description,
		SourceURL:  req.URL,
		Duration:   time.Duration(duration) * time.Second,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	clip.SetLocalPath(downloadedPath)
	clip.SetFileHash(fileHash)
	clip.SetFolderID(targetFolderID)
	clip.SetFolderPath(group)

	clip.SetMetadataString("youtube_video_id", videoID)
	clip.SetMetadataString("youtube_url", req.URL)
	clip.SetMetadataString("source_url", req.URL)
	// Whisper transcript (auto-generated, best-effort)
	if transcript != "" {
		clip.SetMetadataString("clean_transcript", transcript)
	}
	// Language: prefer Whisper detection > YouTube metadata > empty
	if detectedLang != "" {
		clip.SetMetadataString("language", detectedLang)
	} else if meta != nil && meta.Language != "" {
		clip.SetMetadataString("language", meta.Language)
	}
	if meta != nil {
		if meta.Uploader != "" {
			clip.SetMetadataString("youtube_uploader", meta.Uploader)
		}
		if meta.UploadDate != "" {
			clip.SetMetadataString("youtube_upload_date", meta.UploadDate)
		}
		if meta.ViewCount > 0 {
			clip.SetMetadataString("view_count", fmt.Sprintf("%d", meta.ViewCount))
		}
		if len(meta.Tags) > 0 {
			clip.SetMetadataString("youtube_tags", strings.Join(meta.Tags, ","))
		}
	}
	if uploadResult != nil {
		clip.SetDriveLink(uploadResult.WebViewLink)
		clip.SetDownloadLink(uploadResult.DownloadLink)
		clip.SetDriveFileID(uploadResult.FileID)
	}
	if req.Start > 0 {
		clip.SetMetadataString("start", fmt.Sprintf("%.1f", req.Start))
	}
	if req.End > 0 {
		clip.SetMetadataString("end", fmt.Sprintf("%.1f", req.End))
	}

	// 8. Save to database
	if h.clipsRepo != nil {
		if err := h.clipsRepo.UpsertClip(ctx, clip); err != nil {
			log.Error("failed to save clip to DB", zap.Error(err))
			apiutil.InternalError(c, fmt.Errorf("failed to save clip: %w", err))
			return
		}
		log.Info("saved clip to DB", zap.String("clip_id", clip.ID))
	}

	// 9. Update Asset Tree
	if h.assetTreeSvc != nil {
		node := clipsources.ClipToAssetNode(clip)
		if err := h.assetTreeSvc.UpsertNode(ctx, node); err != nil {
			log.Warn("failed to upsert to asset tree", zap.String("clip_id", clip.ID), zap.Error(err))
		}
	}

	// 10. Trigger async enrichment + Qdrant indexing
	hasIndexer := h.clipIndexer != nil || h.vectorStore != nil || h.metaWriter != nil
	if hasIndexer {
		concurrent.SafeGo("yt-register-enrich", func() {
			if h.clips != nil {
				h.clips.EnrichAndIndexClip(context.WithoutCancel(ctx), clip, source)
			}
		})
		log.Info("triggered async Qdrant indexing and enrichment",
			zap.String("clip_id", clip.ID))
	}

	// 11. Return success
	indexingStatus := "not_configured"
	if hasIndexer {
		indexingStatus = "enqueued"
	}
	apiutil.OK(c, gin.H{
		"ok":              true,
		"clip_id":         clip.ID,
		"video_id":        videoID,
		"name":            clip.Name,
		"filename":        driveFilename,
		"duration_sec":    duration,
		"drive_link":      clip.DriveLink(),
		"drive_file_id":   clip.DriveFileID(),
		"file_hash":       fileHash,
		"source":          source,
		"category":        req.Category,
		"tags":            req.Tags,
		"local_path":      downloadedPath,
		"indexed":         hasIndexer,
		"indexing_status": indexingStatus,
		"transcribed":     transcript != "",
		"language":        detectedLang,
		"youtube_meta":    meta != nil,
	})
}
