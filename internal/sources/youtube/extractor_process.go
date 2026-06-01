package youtube

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.uber.org/zap"
	"velox/go-master/internal/core/lifecycle"
	"velox/go-master/internal/media/models"
	"velox/go-master/internal/media/videomuscles"
	"velox/go-master/internal/pkg/fileutil"
	"velox/go-master/internal/pkg/hashutil"
	"velox/go-master/internal/pkg/pathutil"
	"velox/go-master/internal/security"
)

// processSegment processes a single segment: validates timestamps, checks cache,
// downloads via video pipeline, runs lifecycle, updates manifest.
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
	manifest *models.ClipManifest,
	i int,
) ExtractItem {
	item := ExtractItem{
		Name:            pathutil.SafeFolderName(seg.Name),
		Start:           strings.TrimSpace(seg.Start),
		End:             strings.TrimSpace(seg.End),
		DriveFolderID:   driveFolderID,
		DriveFolderPath: resolvedPath,
	}

	if item.Name == "" {
		item.Name = fmt.Sprintf("segment_%03d", i+1)
	}

	// Validate timestamps
	if err := security.SanitizeTimestamp(item.Start); err != nil {
		item.Status = "failed"
		item.Error = "invalid start timestamp: " + err.Error()
		return item
	}

	if err := security.SanitizeTimestamp(item.End); err != nil {
		item.Status = "failed"
		item.Error = "invalid end timestamp: " + err.Error()
		return item
	}

	startSec, err := parseTimestamp(item.Start)
	if err != nil {
		item.Status = "failed"
		item.Error = "invalid start timestamp format: " + err.Error()
		return item
	}
	endSec, err := parseTimestamp(item.End)
	if err != nil {
		item.Status = "failed"
		item.Error = "invalid end timestamp format: " + err.Error()
		return item
	}
	if startSec >= endSec {
		item.Status = "failed"
		item.Error = fmt.Sprintf("start time (%s) must be before end time (%s)", item.Start, item.End)
		return item
	}
	duration := endSec - startSec
	if duration > MaxSegmentDuration {
		item.Status = "failed"
		item.Error = fmt.Sprintf("segment duration (%d seconds) exceeds maximum allowed (%d seconds)", duration, MaxSegmentDuration)
		return item
	}

	// Create stable ID: yt_videoID_startSec_endSec
	clipID := fmt.Sprintf("yt_%s_%d_%d", videoID, startSec, endSec)
	item.ID = clipID

	// Strategy-aware fast path: check cache for existing clips (skip/verify)
	if s.checkExistingClip(ctx, req, clipID, &item) {
		return item
	}

	// Download and cut using FFmpeg
	shouldNormalize := req.Normalize == nil || *req.Normalize
	localPath, err := s.videoPipeline.DownloadAndCutYouTubeVideo(ctx, videomuscles.YouTubeCutRequest{
		URL:            resp.SourceURL,
		VideoID:        videoID,
		Start:          float64(startSec),
		Duration:       float64(duration),
		OutputName:     item.Name,
		ForceKeyframes: req.ForceKeyframes,
		KeepAudio:      req.KeepAudio,
		Normalize:      shouldNormalize,
		Strategy:       req.Strategy,
	})
	if err != nil {
		item.Status = "failed"
		item.Error = fmt.Sprintf("video processing failed: %v", err)
		return item
	}

	fileHash, _ := hashutil.MD5File(localPath)

	// Build lifecycle metadata
	metadata := buildClipMetadata(clipID, item.Name, localPath, videoID, item.Start, item.End,
		startSec, endSec, duration, folderSlug, shouldNormalize, req.KeepAudio,
		driveFolderID, resolvedPath, fileHash, req.Destination)

	// Process via LifecycleService (dedupe + upload + persist) or fallback
	s.processLifecycle(ctx, metadata, localPath, fileHash, &item)

	// Update manifest with this clip
	s.updateManifest(manifest, seg, clipID, item, startSec, endSec, duration, localPath, fileHash)

	return item
}

// checkExistingClip checks if a clip already exists in DB and handles cache strategies.
// Returns true if the item was resolved from cache (caller should skip further processing).
func (s *Service) checkExistingClip(ctx context.Context, req *ExtractRequest, clipID string, item *ExtractItem) bool {
	if req.Strategy == "replace" || s.clipsRepo == nil {
		return false
	}

	existingClip, err := s.clipsRepo.GetClip(ctx, clipID)
	if err != nil || existingClip == nil {
		return false
	}

	if req.Strategy == "skip" {
		item.LocalPath = existingClip.LocalPath
		item.DriveLink = existingClip.DriveLink
		item.DriveFileID = existingClip.DriveFileID
		item.DownloadLink = existingClip.DownloadLink
		item.Status = "skipped"
		return true
	}

	// Default strategy: verify file exists
	if ok, clipErr := fileutil.UsableCachedClip(existingClip.LocalPath); clipErr == nil && ok {
		item.LocalPath = existingClip.LocalPath
		item.DriveLink = existingClip.DriveLink
		item.DriveFileID = existingClip.DriveFileID
		item.DownloadLink = existingClip.DownloadLink
		item.Status = "skipped"
		return true
	}

	// Stale record — clean up before reprocessing
	s.log.Warn("stale youtube clip record detected, removing it before reprocessing",
		zap.String("clip_id", clipID),
		zap.String("local_path", existingClip.LocalPath))
	if existingClip.LocalPath != "" {
		_ = os.Remove(existingClip.LocalPath)
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

// buildClipMetadata creates the lifecycle.FinalizeInput for a processed clip.
func buildClipMetadata(clipID, name, localPath, videoID, start, end string,
	startSec, endSec, duration int, folderSlug string,
	shouldNormalize, keepAudio bool,
	driveFolderID, resolvedPath, fileHash string,
	dest *DestinationRequest) *lifecycle.FinalizeInput {

	metadataMap := map[string]interface{}{
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

// updateManifest updates the clip manifest with the processed segment.
func (s *Service) updateManifest(manifest *models.ClipManifest, seg Segment, clipID string, item ExtractItem,
	startSec, endSec, duration int, localPath, fileHash string) {
	if manifest == nil {
		return
	}

	segTagsJSON, _ := json.Marshal(seg.Tags)
	newMItem := models.ClipManifestItem{
		ID:              clipID,
		Name:            item.Name,
		Start:           item.Start,
		End:             item.End,
		StartSeconds:    startSec,
		EndSeconds:      endSec,
		DurationSeconds: duration,
		Filename:        filepath.Base(localPath),
		LocalPath:       item.LocalPath,
		DriveLink:       item.DriveLink,
		FileHash:        fileHash,
		Status:          item.Status,
		Tags:            string(segTagsJSON),
	}

	// Replace existing or append new
	for j, mItem := range manifest.Clips {
		if mItem.ID == clipID {
			manifest.Clips[j] = newMItem
			return
		}
	}
	manifest.Clips = append(manifest.Clips, newMItem)
}

// triggerAutoIndexing fires a background goroutine to index the clip if the indexer is enabled.
func (s *Service) triggerAutoIndexing(ctx context.Context, clipID string) {
	if s.indexer == nil || !s.indexer.IsEnabled() {
		return
	}

	go func(id string, parentCtx context.Context) {
		indexCtx, cancel := context.WithTimeout(parentCtx, 2*time.Minute)
		defer cancel()

		s.log.Info("triggering automatic indexing for YouTube clip", zap.String("clip_id", id))
		if err := s.indexer.IndexClip(indexCtx, id); err != nil {
			s.log.Error("failed to automatically index YouTube clip", zap.String("clip_id", id), zap.Error(err))
		}
	}(clipID, ctx)
}
