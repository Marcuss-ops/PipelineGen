package youtube

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/core/lifecycle"
	"github.com/Marcuss-ops/PipelineGen/internal/media/videomuscles"
	"github.com/Marcuss-ops/PipelineGen/internal/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/security"
	fileutil "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/files"
	hashutil "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/files"
	downloader "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/downloader"
	retry "github.com/Marcuss-ops/PipelineGen/internal/infrastructure"
	textutil "github.com/Marcuss-ops/PipelineGen/internal/infrastructure"

	"go.uber.org/zap"
)

// processSegment processes a single segment: validates timestamps, checks cache,
// downloads via video pipeline (or cuts from pre-downloaded file), runs lifecycle,
// and enriches with YouTube metadata.
// preDownloadedPath is optional — when set, skips yt-dlp and cuts locally (instant).
// Returns the processed item. Caller is responsible for updating resp.Items, resp.Stats,
// and manifest.Clips based on the returned item.
func (s *Service) processSegment(
	ctx context.Context,
	seg Segment,
	req *ExtractRequest,
	resp *ExtractResponse,
	videoID string,
	driveFolderID string,
	resolvedPath string,
	folderSlug string,
	i int,
	preDownloadedPath string,
) ExtractItem {
	// Validate timestamps
	item := ExtractItem{
		Name:            cleanClipName(textutil.SafeName(seg.Name)),
		Start:           strings.TrimSpace(seg.Start),
		End:             strings.TrimSpace(seg.End),
		DriveFolderID:   driveFolderID,
		DriveFolderPath: resolvedPath,
		Status:          "failed",
	}

	if item.Name == "" {
		item.Name = fmt.Sprintf("segment_%03d", i+1)
	}
	// Temporary placeholder — overwritten with unique name after timestamp parsing
	item.Filename = item.Name + ".mp4"

	if err := security.SanitizeTimestamp(item.Start); err != nil {
		item.Error = "invalid start timestamp: " + err.Error()
		return item
	}

	if err := security.SanitizeTimestamp(item.End); err != nil {
		item.Error = "invalid end timestamp: " + err.Error()
		return item
	}

	startSec, err := textutil.ParseTimestamp(item.Start)
	if err != nil {
		item.Error = "invalid start timestamp format: " + err.Error()
		return item
	}
	endSec, err := textutil.ParseTimestamp(item.End)
	if err != nil {
		item.Error = "invalid end timestamp format: " + err.Error()
		return item
	}
	if startSec >= endSec {
		item.Error = fmt.Sprintf("start time (%s) must be before end time (%s)", item.Start, item.End)
		return item
	}
	duration := endSec - startSec
	item.ID = fmt.Sprintf("yt_%s_%d_%d", videoID, startSec, endSec)
	item.StartSeconds = startSec
	item.EndSeconds = endSec
	item.Duration = duration
	if duration > MaxSegmentDuration {
		item.Error = fmt.Sprintf("segment duration (%d seconds) exceeds maximum allowed (%d seconds)", duration, MaxSegmentDuration)
		return item
	}

	// Build deterministic unique filename AFTER timestamps are parsed
	item.Filename = buildClipFilename(videoID, startSec, endSec, item.Name)

	item.Status = "running"
	clipID := item.ID

	s.log.Info("processing segment",
		zap.String("clip_id", clipID),
		zap.String("name", item.Name),
		zap.String("start", item.Start),
		zap.String("end", item.End),
		zap.Int("duration_sec", duration))

	group := "general"
	if req.Destination != nil && req.Destination.Group != "" {
		group = req.Destination.Group
	}
	outDir := filepath.Join(s.cfg.Storage.DataDir, "media", "clips", group, folderSlug)

	// Strategy-aware fast path: check cache for existing clips (skip/verify)
	if s.checkExistingClip(ctx, req, clipID, &item, outDir) {
		// Try to enrich skipped clips with YouTube metadata if missing
		s.enrichSkippedClip(ctx, clipID, req.URL, videoID)
		return item
	}

	// Download and cut using FFmpeg
	s.log.Info("calling video pipeline for segment",
		zap.String("clip_id", clipID),
		zap.Int("start_sec", startSec),
		zap.Int("duration_sec", duration))

	shouldNormalize := req.Normalize == nil || *req.Normalize
	// Default to keeping audio — ffmpeg strips it when KeepAudio=false
	keepAudio := true
	if req.KeepAudio {
		keepAudio = true
	}

	cutReq := videomuscles.YouTubeCutRequest{
		URL:               resp.SourceURL,
		VideoID:           videoID,
		Start:             float64(startSec),
		Duration:          float64(duration),
		OutputName:        strings.TrimSuffix(item.Filename, ".mp4"), // strip .mp4 for output
		ForceKeyframes:    req.ForceKeyframes,
		KeepAudio:         keepAudio,
		Normalize:         shouldNormalize,
		Strategy:          req.Strategy,
		OutputDir:         outDir,
		PreDownloadedPath: preDownloadedPath,
	}

	// Track asset lifecycle: mark download_and_cut step as running.
	if s.assetProcessing != nil {
		if err := s.assetProcessing.Start(ctx, clipID, "download_and_cut"); err != nil {
			s.log.Warn("asset_processing.Start failed",
				zap.String("clip_id", clipID),
				zap.Error(err))
		}
	}

	var result *videomuscles.YouTubeCutResult
	err = retry.Do(ctx, func() error {
		// Clean partial files before retry
		candidatePath := filepath.Join(outDir, item.Filename)
		os.Remove(candidatePath)

		var dlErr error
		result, dlErr = s.videoPipeline.DownloadAndCutYouTubeVideo(ctx, cutReq)
		return dlErr
	}, retry.RetryOptions{
		MaxAttempts: 3,
		IsRetryable: isTransientDownloadError,
	})
	if err != nil {
		// Track asset lifecycle: mark download_and_cut step as failed.
		if s.assetProcessing != nil {
			if fErr := s.assetProcessing.Fail(ctx, clipID, "download_and_cut", err.Error()); fErr != nil {
				s.log.Warn("asset_processing.Fail failed",
					zap.String("clip_id", clipID),
					zap.Error(fErr))
			}
		}
		s.log.Warn("segment video pipeline failed after retries",
			zap.String("clip_id", clipID),
			zap.Error(err))
		item.Status = "failed"
		item.Error = fmt.Sprintf("video processing failed: %v", err)
		return item
	}

	// Track asset lifecycle: mark download_and_cut step as completed.
	if s.assetProcessing != nil {
		if err := s.assetProcessing.Complete(ctx, clipID, "download_and_cut"); err != nil {
			s.log.Warn("asset_processing.Complete failed",
				zap.String("clip_id", clipID),
				zap.Error(err))
		}
	}

	// Track asset version: create version record for the processed file.
	if s.assetVersions != nil && result.LocalPath != "" {
		versionHash, _ := hashutil.MD5File(result.LocalPath)
		fileSize := fileSizeFromPath(result.LocalPath)
		if versionHash != "" {
			v := &assets.Version{
				AssetID:       clipID,
				FileHash:      versionHash,
				FileSizeBytes: fileSize,
				MimeType:      "video/mp4",
				MetadataJSON:  `{"pipeline":"youtube","source":"download_and_cut","createdBy":"youtube-pipeline"}`,
			}
			if verErr := s.assetVersions.Append(ctx, v); verErr != nil {
				s.log.Warn("asset_versions.Append failed",
					zap.String("clip_id", clipID),
					zap.Error(verErr))
			}
		}
	}

	localPath := result.LocalPath
	s.log.Info("segment video pipeline completed",
		zap.String("clip_id", clipID),
		zap.String("local_path", localPath))

	fileHash, _ := hashutil.MD5File(localPath)
	item.FileHash = fileHash
	item.LocalPath = localPath
	if localPath != "" {
		item.Filename = filepath.Base(localPath)
	}

	// Try to slice official VTT subtitles first (absolute precision)
	if err := s.sliceSubtitles(ctx, videoID, startSec, endSec, localPath); err != nil {
		s.log.Warn("Failed to slice official subtitles, falling back to Whisper", zap.String("clip_id", clipID), zap.Error(err))

		// Whisper fallback
		s.log.Info("Running Whisper transcription fallback", zap.String("clip_id", clipID))
		scriptPath := "scripts/tools/transcribe_detect_lang.py"
		cmd := exec.CommandContext(ctx, "python3", scriptPath, localPath, "--model", "base", "--transcribe", "--json-only")
		out, err := cmd.Output()
		if err == nil {
			var whisperResult struct {
				Transcript string `json:"transcript"`
			}
			if json.Unmarshal(out, &whisperResult) == nil && whisperResult.Transcript != "" {
				txtPath := strings.TrimSuffix(localPath, filepath.Ext(localPath)) + ".txt"
				_ = os.WriteFile(txtPath, []byte(whisperResult.Transcript), 0644)
				s.log.Info("Successfully transcribed clip via Whisper fallback", zap.String("path", txtPath))
			}
		} else {
			s.log.Warn("Whisper fallback transcription failed", zap.Error(err))
		}
	}

	// Build lifecycle metadata (now enriched with YouTube video info if available)
	metadata := buildClipMetadata(clipID, item.Name, localPath, videoID, item.Start, item.End,
		startSec, endSec, duration, folderSlug, shouldNormalize, req.KeepAudio,
		driveFolderID, resolvedPath, fileHash, req.Destination, result.Metadata, &seg)

	// Process via LifecycleService (dedupe + upload + persist) or fallback
	s.log.Info("starting lifecycle processing for segment",
		zap.String("clip_id", clipID),
		zap.Bool("has_drive", driveFolderID != ""))
	s.processLifecycle(ctx, metadata, localPath, fileHash, &item)
	s.log.Info("segment lifecycle completed",
		zap.String("clip_id", clipID),
		zap.String("status", item.Status),
		zap.String("drive_link", item.DriveLink))

	// Enrich clip with YouTube metadata (title, description, tags, language) AFTER lifecycle
	// so the clip is already in DB. This provides rich search_text for semantic search.
	s.log.Info("starting metadata enrichment for segment",
		zap.String("clip_id", clipID))
	s.enrichYouTubeClipWithMetadata(ctx, clipID, result, false)
	s.log.Info("segment metadata enrichment completed",
		zap.String("clip_id", clipID))

	// Generate embeddings (semantic + transcript) immediately after the clip is in DB.
	// This ensures every new clip has dual vectors for hybrid search.
	// The transcript .txt file (from VTT or Whisper fallback) must already exist on disk.
	//
	// On the dispatcher path, dispatchOrIndex (called by enrichment above)
	// already wrote media_assets + outbox atomically — the worker will
	// re-index asynchronously. The synchronous inline IndexClip here is
	// only needed as a best-effort fallback for the legacy path (no
	// dispatcher); it would otherwise re-index redundantly and slow the
	// hot path.
	if s.dispatcher == nil && s.indexer != nil {
		s.log.Info("starting embedding indexing for segment",
			zap.String("clip_id", clipID))
		indexCtx, indexCancel := context.WithTimeout(ctx, 30*time.Second)
		defer indexCancel()
		if err := s.indexer.IndexClip(indexCtx, clipID); err != nil {
			s.log.Warn("failed to auto-index clip embeddings (non-fatal)",
				zap.String("clip_id", clipID),
				zap.Error(err))
		} else {
			s.log.Info("auto-indexed clip embeddings (semantic + transcript)",
				zap.String("clip_id", clipID))
		}
	}

	s.log.Info("segment processing complete",
		zap.String("clip_id", clipID),
		zap.String("status", item.Status),
		zap.Int("duration_sec", duration),
		zap.String("filename", item.Filename))

	// Note: manifest update is done by the caller (Extract) to handle parallel processing safely
	return item
}

// checkExistingClip checks if a clip already exists in DB and handles cache strategies.
// Returns true if the item was resolved from cache (caller should skip further processing).
func (s *Service) checkExistingClip(ctx context.Context, req *ExtractRequest, clipID string, item *ExtractItem, outDir string) bool {
	if req.Strategy == "replace" || s.clipsRepo == nil {
		return false
	}

	existingClip, err := s.clipsRepo.GetClip(ctx, clipID)
	if err != nil || existingClip == nil {
		return false
	}

	expectedPath := filepath.Join(outDir, item.Filename)
	if existingClip.LocalPath() != expectedPath {
		s.log.Info("cached clip local path mismatch, forcing re-processing for new folder",
			zap.String("clip_id", clipID),
			zap.String("existing_path", existingClip.LocalPath()),
			zap.String("expected_path", expectedPath))
		return false
	}

	if req.Strategy == "skip" {
		item.LocalPath = existingClip.LocalPath()
		item.DriveLink = existingClip.DriveLink()
		item.DriveFileID = existingClip.DriveFileID()
		item.DownloadLink = existingClip.DownloadLink()
		item.Status = "skipped"
		return true
	}

	// Default strategy: verify file exists
	if ok, clipErr := fileutil.UsableCachedClip(existingClip.LocalPath()); clipErr == nil && ok {
		item.LocalPath = existingClip.LocalPath()
		item.DriveLink = existingClip.DriveLink()
		item.DriveFileID = existingClip.DriveFileID()
		item.DownloadLink = existingClip.DownloadLink()
		item.Status = "skipped"
		return true
	}

	// Stale record — clean up before reprocessing
	s.log.Warn("stale youtube clip record detected, removing it before reprocessing",
		zap.String("clip_id", clipID),
		zap.String("local_path", existingClip.LocalPath()))
	if existingClip.LocalPath() != "" {
		_ = os.Remove(existingClip.LocalPath())
	}
	_ = s.clipsRepo.DeleteClip(ctx, clipID)
	return false
}

// processLifecycle handles the lifecycle processing (dedupe + upload + persist) or falls back.
func (s *Service) processLifecycle(ctx context.Context, metadata *lifecycle.FinalizeInput, localPath, fileHash string, item *ExtractItem) {
	if s.lifecycleService == nil {
		item.LocalPath = localPath
		item.DriveLink = ""
		item.DriveFileID = ""
		item.DownloadLink = ""
		item.Status = "processed"
		return
	}

	lifecycleResult, err := s.lifecycleService.ProcessAsset(ctx, metadata, fileHash)
	if err != nil {
		item.Status = "failed"
		item.Error = fmt.Sprintf("lifecycle failed: %v", err)
		return
	}
	if !lifecycleResult.OK {
		item.Status = "failed"
		item.Error = lifecycleResult.Error
		return
	}

	item.LocalPath = localPath
	item.DriveLink = lifecycleResult.DriveLink
	item.DriveFileID = lifecycleResult.DriveFileID
	item.DownloadLink = lifecycleResult.DownloadLink
	item.Status = "processed"
}

// fileSizeFromPath returns the file size in bytes, or 0 if the file cannot be stat'd.
func fileSizeFromPath(path string) int64 {
	if path == "" {
		return 0
	}
	fi, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return fi.Size()
}

// buildClipFilename creates a deterministic, unique filename for a YouTube clip.
// Format: yt_{videoID}_{startSec}_{endSec}_{slug}.mp4
// This ensures every clip has a unique file on Drive regardless of segment name collisions.
func buildClipFilename(videoID string, startSec, endSec int, name string) string {
	slug := textutil.SlugifyWithMax(name, 40)
	if slug == "" {
		slug = "clip"
	}
	// Ensure the slug doesn't start with a number (Drive naming convention)
	if slug[0] >= '0' && slug[0] <= '9' {
		slug = "c_" + slug
	}
	return fmt.Sprintf("yt_%s_%d_%d_%s.mp4", videoID, startSec, endSec, slug)
}

// buildClipMetadata creates the lifecycle.FinalizeInput for a processed clip.
// If youtubeMeta is provided, includes YouTube video metadata (title, description, tags, language).
func buildClipMetadata(clipID, name, localPath, videoID, start, end string,
	startSec, endSec, duration int, folderSlug string,
	shouldNormalize, keepAudio bool,
	driveFolderID, resolvedPath, fileHash string,
	dest *DestinationRequest,
	youtubeMeta *downloader.YouTubeMetadata,
	seg *Segment) *lifecycle.FinalizeInput {

	metadataMap := map[string]any{
		"video_id":         videoID,
		"start":            start,
		"end":              end,
		"start_seconds":    startSec,
		"end_seconds":      endSec,
		"duration_seconds": duration,
		"folder_slug":      folderSlug,
		"normalized":       shouldNormalize,
		"keep_audio":       keepAudio,
	}

	// Include custom metadata from request if provided
	if seg != nil {
		if seg.Summary != "" {
			metadataMap["clip_summary"] = seg.Summary
		}
		if len(seg.Topics) > 0 {
			metadataMap["topics"] = seg.Topics
		}
		if len(seg.Speakers) > 0 {
			metadataMap["speakers"] = seg.Speakers
		}
		if len(seg.MentionedPeople) > 0 {
			metadataMap["mentioned_people"] = seg.MentionedPeople
		}
		if seg.Hook != "" {
			metadataMap["hook"] = seg.Hook
		}
		if seg.QualityScore > 0 {
			metadataMap["quality_score"] = seg.QualityScore
		}
		if seg.SearchVisibility != "" {
			metadataMap["search_visibility"] = seg.SearchVisibility
		}
		if len(seg.Tags) > 0 {
			metadataMap["segment_tags"] = seg.Tags
		}
	}

	// Include YouTube video metadata if available (from yt-dlp --dump-json)
	if youtubeMeta != nil {
		metadataMap["youtube_title"] = youtubeMeta.Title
		metadataMap["youtube_description"] = youtubeMeta.Description
		metadataMap["youtube_language"] = youtubeMeta.Language
		metadataMap["youtube_uploader"] = youtubeMeta.Uploader
		metadataMap["youtube_upload_date"] = youtubeMeta.UploadDate
		metadataMap["youtube_view_count"] = youtubeMeta.ViewCount
		metadataMap["youtube_duration"] = youtubeMeta.Duration
		metadataMap["youtube_video_id"] = youtubeMeta.ID
		metadataMap["youtube_url"] = fmt.Sprintf("https://www.youtube.com/watch?v=%s", youtubeMeta.ID)
		if len(youtubeMeta.Tags) > 0 {
			metadataMap["youtube_tags"] = youtubeMeta.Tags
		}
		if len(youtubeMeta.Chapters) > 0 {
			metadataMap["youtube_chapters"] = youtubeMeta.Chapters
		}
	}
	metadataBytes, _ := json.Marshal(metadataMap)

	folderPath := resolvedPath
	if folderPath == "" && dest != nil {
		folderPath = dest.FolderPath
	}

	return &lifecycle.FinalizeInput{
		ID:           clipID,
		Name:         name,
		Filename:     filepath.Base(localPath),
		Kind:         lifecycle.AssetKindVideo,
		Source:       "youtube",
		Group:        getGroupFromDestination(dest),
		Subfolder:    "",
		LocalPath:    localPath,
		FolderID:     driveFolderID,
		FolderPath:   folderPath,
		DriveLink:    "",
		DriveFileID:  "",
		DownloadLink: "",
		FileHash:     fileHash,
		Metadata:     string(metadataBytes),
		RequireLocal: true,
		RequireHash:  true,
		RequireDrive: driveFolderID != "",
		VerifyDB:     true,
	}
}
