package extraction

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/lifecycle"
	youtubeports "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/ports"
	segments "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/segments"
	tagutil "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/tagutil"
	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/types"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	retry "github.com/Marcuss-ops/PipelineGen/pkg/retry"
	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

// MaxSegmentDuration is the maximum allowed duration for a single clip segment (60 seconds)
const MaxSegmentDuration = 60

// processSegment processes a single segment: validates timestamps, checks cache,
// downloads via video pipeline, runs lifecycle, and enriches with YouTube metadata.
func (s *Service) processSegment(
	ctx context.Context,
	seg youtubetypes.Segment,
	req *youtubetypes.ExtractRequest,
	resp *youtubetypes.ExtractResponse,
	videoID string,
	driveFolderID string,
	resolvedPath string,
	folderSlug string,
	i int,
	preDownloadedPath string,
) youtubetypes.ExtractItem {
	item := youtubetypes.ExtractItem{
		Name:            tagutil.CleanClipName(textutil.SafeName(seg.Name)),
		Start:           strings.TrimSpace(seg.Start),
		End:             strings.TrimSpace(seg.End),
		DriveFolderID:   driveFolderID,
		DriveFolderPath: resolvedPath,
		Status:          "failed",
	}

	if item.Name == "" {
		item.Name = fmt.Sprintf("segment_%03d", i+1)
	}
	item.Filename = item.Name + ".mp4"

	if err := s.segmentsSvc.SanitizeTimestamp(item.Start); err != nil {
		item.Error = "invalid start timestamp: " + err.Error()
		return item
	}
	if err := s.segmentsSvc.SanitizeTimestamp(item.End); err != nil {
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

	item.Filename = s.segmentsSvc.BuildClipFilename(videoID, startSec, endSec, item.Name)
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

	if s.callbacks.CheckExistingClip(ctx, req, clipID, &item, outDir) {
		s.callbacks.EnrichSkippedClip(ctx, clipID, req.URL, videoID)
		return item
	}

	s.log.Info("calling video pipeline for segment",
		zap.String("clip_id", clipID),
		zap.Int("start_sec", startSec),
		zap.Int("duration_sec", duration))

	shouldNormalize := req.Normalize == nil || *req.Normalize
	keepAudio := req.KeepAudio

	cutReq := youtubeports.VideoCutRequest{
		URL:               resp.SourceURL,
		VideoID:           videoID,
		Start:             float64(startSec),
		Duration:          float64(duration),
		OutputName:        strings.TrimSuffix(item.Filename, ".mp4"),
		ForceKeyframes:    req.ForceKeyframes,
		KeepAudio:         keepAudio,
		Normalize:         shouldNormalize,
		Strategy:          req.Strategy,
		OutputDir:         outDir,
		PreDownloadedPath: preDownloadedPath,
	}

	_ = s.callbacks.AssetProcessingStart(ctx, clipID, "download_and_cut")

	var result *youtubeports.VideoCutResult
	err = retry.Do(ctx, func() error {
		candidatePath := filepath.Join(outDir, item.Filename)
		os.Remove(candidatePath)

		var dlErr error
		result, dlErr = s.videoPipeline.DownloadAndCutYouTubeVideo(ctx, cutReq)
		return dlErr
	}, retry.RetryOptions{
		MaxAttempts: 3,
		IsRetryable: tagutil.IsTransientDownloadError,
	})
	if err != nil {
		_ = s.callbacks.AssetProcessingFail(ctx, clipID, "download_and_cut", err.Error())
		s.log.Warn("segment video pipeline failed after retries",
			zap.String("clip_id", clipID),
			zap.Error(err))
		item.Status = "failed"
		item.Error = fmt.Sprintf("video processing failed: %v", err)
		return item
	}

	_ = s.callbacks.AssetProcessingComplete(ctx, clipID, "download_and_cut")

	// Track asset version
	if result.LocalPath != "" {
		versionHash := s.callbacks.MD5File(result.LocalPath)
		fileSize := s.segmentsSvc.FileSizeFromPath(result.LocalPath)
		if versionHash != "" {
			v := &asset.Version{
				AssetID:       clipID,
				FileHash:      versionHash,
				FileSizeBytes: fileSize,
				MimeType:      "video/mp4",
				MetadataJSON:  `{"pipeline":"youtube","source":"download_and_cut","createdBy":"youtube-pipeline"}`,
			}
			_ = s.callbacks.AssetVersionsAppend(ctx, v)
		}
	}

	localPath := result.LocalPath
	s.log.Info("segment video pipeline completed",
		zap.String("clip_id", clipID),
		zap.String("local_path", localPath))

	fileHash := s.callbacks.MD5File(localPath)
	item.FileHash = fileHash
	item.LocalPath = localPath
	if localPath != "" {
		item.Filename = filepath.Base(localPath)
	}

	// Try to slice official VTT subtitles first
	if err := s.callbacks.SliceSubtitles(ctx, videoID, startSec, endSec, localPath); err != nil {
		s.log.Warn("Failed to slice official subtitles, falling back to Whisper", zap.String("clip_id", clipID), zap.Error(err))

		// Whisper fallback
		transcript, wErr := s.callbacks.TranscribeAudio(ctx, localPath)
		if wErr == nil && transcript != "" {
			txtPath := strings.TrimSuffix(localPath, filepath.Ext(localPath)) + ".txt"
			_ = os.WriteFile(txtPath, []byte(transcript), 0644)
			s.log.Info("Successfully transcribed clip via Whisper fallback", zap.String("path", txtPath))
		} else {
			s.log.Warn("Whisper fallback transcription failed", zap.Error(wErr))
		}
	}

	// Build lifecycle metadata
	folderPath := resolvedPath
	if folderPath == "" && req.Destination != nil {
		folderPath = req.Destination.FolderPath
	}
	metadata := s.segmentsSvc.BuildClipMetadata(segments.BuildClipMetadataInput{
		ClipID: clipID, Name: item.Name, LocalPath: localPath, VideoID: videoID,
		Start: item.Start, End: item.End,
		StartSec: startSec, EndSec: endSec, Duration: duration,
		FolderSlug:      folderSlug,
		ShouldNormalize: shouldNormalize, KeepAudio: req.KeepAudio,
		DriveFolderID: driveFolderID, FolderPath: folderPath,
		FileHash: fileHash, Group: group,
		YouTubeMeta: result.Metadata, Segment: &seg,
	})

	s.log.Info("starting lifecycle processing for segment",
		zap.String("clip_id", clipID),
		zap.Bool("has_drive", driveFolderID != ""))
	s.callbacks.ProcessLifecycle(ctx, metadata, localPath, fileHash, &item)
	s.log.Info("segment lifecycle completed",
		zap.String("clip_id", clipID),
		zap.String("status", item.Status),
		zap.String("drive_link", item.DriveLink))

	s.log.Info("starting metadata enrichment for segment",
		zap.String("clip_id", clipID))
	s.callbacks.EnrichClip(ctx, clipID, result.Metadata, false)
	s.log.Info("segment metadata enrichment completed",
		zap.String("clip_id", clipID))

	if err := s.callbacks.IndexClip(ctx, clipID); err != nil {
		s.log.Warn("failed to auto-index clip embeddings (non-fatal)",
			zap.String("clip_id", clipID),
			zap.Error(err))
	} else {
		s.log.Info("auto-indexed clip embeddings (semantic + transcript)",
			zap.String("clip_id", clipID))
	}

	s.log.Info("segment processing complete",
		zap.String("clip_id", clipID),
		zap.String("status", item.Status),
		zap.Int("duration_sec", duration),
		zap.String("filename", item.Filename))

	return item
}

// ProcessLifecycle runs the asset lifecycle on a processed clip.
// Exported for use by the root service's callback implementation.
func ProcessLifecycle(ctx context.Context, lifecycleSvc *lifecycle.Service, localPath, fileHash string, item *youtubetypes.ExtractItem, metadata *lifecycle.FinalizeInput) {
	if lifecycleSvc == nil {
		item.LocalPath = localPath
		item.DriveLink = ""
		item.DriveFileID = ""
		item.DownloadLink = ""
		item.Status = "processed"
		return
	}

	lifecycleResult, err := lifecycleSvc.ProcessAsset(ctx, metadata, fileHash)
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
