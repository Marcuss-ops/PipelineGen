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

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/lifecycle"
	hashutil "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/files"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/security"
	"github.com/Marcuss-ops/PipelineGen/internal/media/videomuscles"
	retry "github.com/Marcuss-ops/PipelineGen/pkg/retry"
	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

// Compile-time check: checkExistingClip and buildClipMetadata moved to
// segment_cache.go and segment_lifecycle.go respectively. Keep the
// package boundary minimal.
var _ = (&Service{}).checkExistingClip
var _ = buildClipMetadata
var _ = fileSizeFromPath

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
			v := &asset.Version{
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
