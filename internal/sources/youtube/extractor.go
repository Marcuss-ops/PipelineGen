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
	"velox/go-master/internal/core/destination"
	"velox/go-master/internal/core/lifecycle"
	jobservice "velox/go-master/internal/jobs"
	"velox/go-master/internal/media/models"
	"velox/go-master/internal/media/videomuscles"
	"velox/go-master/internal/pkg/fileutil"
	"velox/go-master/internal/pkg/hashutil"
	"velox/go-master/internal/pkg/pathutil"
	"velox/go-master/internal/pkg/ptrutil"
	"velox/go-master/internal/pkg/urlutil"
	"velox/go-master/internal/security"
)

// MaxSegmentDuration is the maximum allowed duration for a single clip segment (120 seconds)
const MaxSegmentDuration = 120

// Extract processes a YouTube clip extraction request.
func (s *Service) Extract(ctx context.Context, req *ExtractRequest) (*ExtractResponse, error) {
	s.log.Info("YouTube Extract service called", zap.String("url", req.URL))

	// Apply configurable timeout if no deadline is set
	if _, hasDeadline := ctx.Deadline(); !hasDeadline && s.cfg.Jobs.YouTubeExtractTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(s.cfg.Jobs.YouTubeExtractTimeout)*time.Second)
		defer cancel()
	}

	videoID, err := urlutil.ExtractVideoID(req.URL)
	if err != nil || videoID == "" {
		videoID = hashutil.MD5String(req.URL)[:12]
	}
	if canonical := canonicalYouTubeURL(req.URL, videoID); canonical != "" {
		req.URL = canonical
	}

	resp := &ExtractResponse{
		OK:        true,
		SourceURL: strings.TrimSpace(req.URL),
		VideoID:   videoID,
		Stats: &ExtractStats{
			Requested: len(req.Segments),
		},
	}

	if resp.SourceURL == "" {
		resp.OK = false
		resp.Error = "url is required"
		return resp, fmt.Errorf("url is required")
	}

	if err := security.ValidateDownloadURL(resp.SourceURL); err != nil {
		resp.OK = false
		resp.Error = err.Error()
		return resp, err
	}

	if len(req.Segments) == 0 {
		resp.OK = false
		resp.Error = "segments are required"
		return resp, fmt.Errorf("segments are required")
	}

	if len(req.Segments) > 20 {
		resp.OK = false
		resp.Error = "too many segments, max 20"
		return resp, fmt.Errorf("too many segments")
	}

	// Upsert MonitoredSource (after validation to avoid stale "processing" entries)
	now := time.Now().UTC().Format(time.RFC3339)
	monitoredSource := &models.MonitoredSource{
		ID:           "youtube_" + videoID,
		Source:       "youtube",
		ExternalID:   videoID,
		ExternalURL:  req.URL,
		GroupName:    "",
		Category:     "manual_extract",
		Status:       "processing",
		LastSeenAt:   now,
		CreatedAt:    now,
		UpdatedAt:    now,
		MetadataJSON: "{}",
	}
	if req.Destination != nil {
		monitoredSource.GroupName = req.Destination.Group
	}
	if s.monitoredRepo != nil {
		if err := s.monitoredRepo.UpsertSource(ctx, monitoredSource); err != nil {
			s.log.Error("Failed to upsert monitored source", zap.Error(err))
		}
	}

	// Create stable folder path using video ID instead of timestamp
	folderSlug := videoID
	group := "general"
	if req.Destination != nil && req.Destination.Group != "" {
		group = req.Destination.Group
	}

	outDir := filepath.Join(s.cfg.Storage.DataDir, "media", "youtube", group, folderSlug)
	if err := os.MkdirAll(outDir, 0755); err != nil {
		resp.OK = false
		resp.Error = err.Error()
		return resp, err
	}
	s.log.Info("using stable folder for video", zap.String("folder", outDir), zap.String("video_id", videoID))

	// Resolve Drive destination using unified asset destination resolver
	var driveFolderID string
	var resolvedPath string
	if s.assetDestResolver != nil && req.Destination != nil {
		destReq := &destination.ResolveRequest{
			Source:          "youtube",
			Group:           req.Destination.Group,
			FolderID:        req.Destination.FolderID,
			FolderPath:      req.Destination.FolderPath,
			SubfolderName:   req.Destination.SubfolderName,
			CreateSubfolder: req.Destination.CreateSubfolder,
		}

		// If no subfolder provided, automatically create one based on the video ID
		if destReq.SubfolderName == "" {
			destReq.SubfolderName = "yt_" + videoID
			destReq.CreateSubfolder = true
			s.log.Info("auto-assigning video subfolder", zap.String("subfolder", destReq.SubfolderName))
		}

		resolved, err := s.assetDestResolver.Resolve(ctx, destReq)
		if err != nil {
			s.log.Warn("failed to resolve drive destination", zap.Error(err))
		} else {
			driveFolderID = resolved.FolderID
			resolvedPath = resolved.FolderPath
		}
	}

	// Set folder info on response
	resp.Folder = &FolderInfo{
		ID:               fmt.Sprintf("clipfolder_youtube_%s", videoID),
		LocalFolderPath:  outDir,
		DriveFolderID:    driveFolderID,
		DriveFolderPath:  resolvedPath,
		ManifestTXTPath:  filepath.Join(outDir, "clip_manifest.txt"),
		ManifestJSONPath: filepath.Join(outDir, "clip_manifest.json"),
	}
	resp.DriveFolderID = driveFolderID
	resp.DriveFolderPath = resolvedPath

	// Initialize or load clip folder from DB
	folderID := fmt.Sprintf("clipfolder_youtube_%s", videoID)
	var clipFolder *models.ClipFolder
	if s.clipsRepo != nil {
		existingFolder, err := s.clipsRepo.GetClipFolder(ctx, folderID)
		if err == nil && existingFolder != nil {
			clipFolder = existingFolder
			s.log.Info("loaded existing clip folder", zap.String("folder_id", folderID))

			// Update drive info if it was missing but we have it now
			if clipFolder.FolderID == "" && driveFolderID != "" {
				clipFolder.FolderID = driveFolderID
				clipFolder.FolderPath = resolvedPath
				clipFolder.Group = getGroupFromDestination(req.Destination)
			}

			// Update local path if it changed (e.g. user provided a specific subfolder_name)
			if clipFolder.LocalFolderPath != outDir {
				clipFolder.LocalFolderPath = outDir
				clipFolder.ManifestTXTPath = filepath.Join(outDir, "clip_manifest.txt")
				clipFolder.ManifestJSONPath = filepath.Join(outDir, "clip_manifest.json")
			}
		} else {
			clipFolder = &models.ClipFolder{
				ID:               folderID,
				Source:           "youtube",
				SourceURL:        resp.SourceURL,
				VideoID:          videoID,
				FolderID:         driveFolderID,
				FolderPath:       resolvedPath,
				LocalFolderPath:  outDir,
				Group:            getGroupFromDestination(req.Destination),
				ManifestTXTPath:  filepath.Join(outDir, "clip_manifest.txt"),
				ManifestJSONPath: filepath.Join(outDir, "clip_manifest.json"),
				CreatedAt:        time.Now().UTC(),
				UpdatedAt:        time.Now().UTC(),
			}
			s.log.Info("created new clip folder", zap.String("folder_id", folderID))
		}
	}

	// Load existing manifest if available
	manifest := &models.ClipManifest{
		ID:              folderID,
		FolderID:        driveFolderID,
		FolderPath:      resolvedPath,
		Source:          "youtube",
		SourceURL:       resp.SourceURL,
		VideoID:         videoID,
		LocalFolderPath: outDir,
		Clips:           []models.ClipManifestItem{},
	}
	if clipFolder != nil && clipFolder.ManifestJSONPath != "" {
		loadedManifest, err := s.folderMemory.LoadManifest(clipFolder.ManifestJSONPath)
		if err == nil && loadedManifest != nil {
			manifest = loadedManifest
			s.log.Info("loaded existing manifest", zap.Int("clip_count", len(manifest.Clips)))

			// Restore/Update drive info if missing in file but present in current request
			if manifest.FolderID == "" && driveFolderID != "" {
				manifest.FolderID = driveFolderID
				manifest.FolderPath = resolvedPath
			}
			if manifest.ID == "" {
				manifest.ID = folderID
			}
		}
	}

	for i, seg := range req.Segments {
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

		if err := security.SanitizeTimestamp(item.Start); err != nil {
			item.Status = "failed"
			item.Error = "invalid start timestamp: " + err.Error()
			resp.Items = append(resp.Items, item)
			resp.Stats.Failed++
			resp.OK = false
			continue
		}

		if err := security.SanitizeTimestamp(item.End); err != nil {
			item.Status = "failed"
			item.Error = "invalid end timestamp: " + err.Error()
			resp.Items = append(resp.Items, item)
			resp.Stats.Failed++
			resp.OK = false
			continue
		}

		// Validate start < end and check max duration
		startSec, err := parseTimestamp(item.Start)
		if err != nil {
			item.Status = "failed"
			item.Error = "invalid start timestamp format: " + err.Error()
			resp.Items = append(resp.Items, item)
			resp.Stats.Failed++
			resp.OK = false
			continue
		}
		endSec, err := parseTimestamp(item.End)
		if err != nil {
			item.Status = "failed"
			item.Error = "invalid end timestamp format: " + err.Error()
			resp.Items = append(resp.Items, item)
			resp.Stats.Failed++
			resp.OK = false
			continue
		}
		if startSec >= endSec {
			item.Status = "failed"
			item.Error = fmt.Sprintf("start time (%s) must be before end time (%s)", item.Start, item.End)
			resp.Items = append(resp.Items, item)
			resp.Stats.Failed++
			resp.OK = false
			continue
		}
		duration := endSec - startSec
		if duration > MaxSegmentDuration {
			item.Status = "failed"
			item.Error = fmt.Sprintf("segment duration (%d seconds) exceeds maximum allowed (%d seconds)", duration, MaxSegmentDuration)
			resp.Items = append(resp.Items, item)
			resp.Stats.Failed++
			resp.OK = false
			continue
		}

		// Create stable ID: yt_videoID_startSec_endSec
		clipID := fmt.Sprintf("yt_%s_%d_%d", videoID, startSec, endSec)
		item.ID = clipID

		// Strategy-aware fast path:
		//   replace → skip cache entirely
		//   skip    → reuse if DB record exists (no file check needed)
		//   verify  → reuse only if DB + file are valid (default)
		if req.Strategy != "replace" && s.clipsRepo != nil {
			existingClip, err := s.clipsRepo.GetClip(ctx, clipID)
			if err == nil && existingClip != nil {
				if req.Strategy == "skip" {
					item.LocalPath = existingClip.LocalPath
					item.DriveLink = existingClip.DriveLink
					item.DriveFileID = existingClip.DriveFileID
					item.DownloadLink = existingClip.DownloadLink
					item.Status = "skipped"
					resp.Items = append(resp.Items, item)
					resp.Stats.Skipped++
					continue
				}

				if ok, clipErr := fileutil.UsableCachedClip(existingClip.LocalPath); clipErr == nil && ok {
					item.LocalPath = existingClip.LocalPath
					item.DriveLink = existingClip.DriveLink
					item.DriveFileID = existingClip.DriveFileID
					item.DownloadLink = existingClip.DownloadLink
					item.Status = "skipped"
					resp.Items = append(resp.Items, item)
					resp.Stats.Skipped++
					continue
				}

				s.log.Warn("stale youtube clip record detected, removing it before reprocessing",
					zap.String("clip_id", clipID),
					zap.String("local_path", existingClip.LocalPath))
				if existingClip.LocalPath != "" {
					_ = os.Remove(existingClip.LocalPath)
				}
				_ = s.clipsRepo.DeleteClip(ctx, clipID)
			}
		}

		// Determine normalize flag
		shouldNormalize := req.Normalize == nil || *req.Normalize

		// Download and cut using FFmpeg
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
			resp.Items = append(resp.Items, item)
			resp.Stats.Failed++
			resp.OK = false
			continue
		}

		fileHash, _ := hashutil.MD5File(localPath)

		// Use LifecycleService for dedupe + upload + persist
		metadataMap := map[string]interface{}{
			"video_id":         videoID,
			"start":            item.Start,
			"end":              item.End,
			"start_seconds":    startSec,
			"end_seconds":      endSec,
			"duration_seconds": duration,
			"folder_slug":      folderSlug,
			"normalized":       shouldNormalize,
			"keep_audio":       req.KeepAudio,
		}
		metadataBytes, _ := json.Marshal(metadataMap)
		metadata := string(metadataBytes)

		folderPath := resolvedPath
		if folderPath == "" && req.Destination != nil {
			folderPath = req.Destination.FolderPath
		}

		input := &lifecycle.FinalizeInput{
			ID:           clipID,
			Name:         item.Name,
			Filename:     filepath.Base(localPath),
			Kind:         lifecycle.AssetKindVideo,
			Source:       "youtube",
			Group:        getGroupFromDestination(req.Destination),
			Subfolder:    "",
			LocalPath:    localPath,
			FolderID:     driveFolderID,
			FolderPath:   folderPath,
			DriveLink:    "",
			DriveFileID:  "",
			DownloadLink: "",
			FileHash:     fileHash,
			Metadata:     metadata,
			RequireLocal: true,
			RequireHash:  true,
			RequireDrive: driveFolderID != "",
			VerifyDB:     true,
		}

		// Use LifecycleService for dedupe + upload + persist if available
		if s.lifecycleService != nil {
			lifecycleResult, err := s.lifecycleService.ProcessAsset(ctx, input, fileHash)
			if err != nil {
				item.Status = "failed"
				item.Error = fmt.Sprintf("lifecycle failed: %v", err)
				resp.Items = append(resp.Items, item)
				resp.Stats.Failed++
				resp.OK = false
				continue
			}
			if !lifecycleResult.OK {
				item.Status = "failed"
				item.Error = lifecycleResult.Error
				resp.Items = append(resp.Items, item)
				resp.Stats.Failed++
				resp.OK = false
				continue
			}

			item.LocalPath = localPath
			item.DriveLink = lifecycleResult.DriveLink
			item.DriveFileID = lifecycleResult.DriveFileID
			item.DownloadLink = lifecycleResult.DownloadLink
			item.Status = "processed"
		} else {
			// Fallback if LifecycleService not available (tests)
			item.LocalPath = localPath
			item.DriveLink = ""
			item.DriveFileID = ""
			item.DownloadLink = ""
			item.Status = "processed"
		}

		// Update manifest with this clip
		if manifest != nil {
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
			found := false
			for j, mItem := range manifest.Clips {
				if mItem.ID == clipID {
					manifest.Clips[j] = newMItem
					found = true
					break
				}
			}
			if !found {
				manifest.Clips = append(manifest.Clips, newMItem)
			}
		}

		resp.Items = append(resp.Items, item)
		resp.Stats.Processed++

		// Trigger automatic embedding/indexing if indexer is available
		if s.indexer != nil && s.indexer.IsEnabled() {
			go func(id string, parentCtx context.Context) {
				indexCtx, cancel := context.WithTimeout(parentCtx, 2*time.Minute)
				defer cancel()

				s.log.Info("triggering automatic indexing for YouTube clip", zap.String("clip_id", id))
				if err := s.indexer.IndexClip(indexCtx, id); err != nil {
					s.log.Error("failed to automatically index YouTube clip", zap.String("clip_id", id), zap.Error(err))
				}
			}(clipID, ctx)
		}
	}

	// Update folder manifest (TXT + JSON)
	if clipFolder != nil {
		// Compute manifest stats using foldermemory
		stats := s.folderMemory.ComputeManifestStats(manifest)
		manifest.Stats = stats

		clipFolder.ClipCount = stats.ClipCount
		clipFolder.ProcessedCount = stats.ProcessedCount
		clipFolder.FailedCount = stats.FailedCount
		clipFolder.SkippedCount = stats.SkippedCount
		clipFolder.UpdatedAt = time.Now().UTC()

		// Save manifest JSON
		if manifest != nil {
			if err := s.folderMemory.SaveManifest(clipFolder.ManifestJSONPath, manifest); err != nil {
				s.log.Warn("failed to write manifest JSON", zap.Error(err))
			} else {
				s.log.Info("manifest JSON updated", zap.String("path", clipFolder.ManifestJSONPath))
			}
		}

		// Save manifest TXT (respect WriteSummary flag)
		writeSummary := ptrutil.BoolDefault(req.WriteSummary, true)
		if writeSummary && clipFolder.ManifestTXTPath != "" {
			if err := s.folderMemory.UpdateManifestTXT(clipFolder, manifest); err != nil {
				s.log.Warn("failed to write manifest TXT", zap.Error(err))
			} else {
				s.log.Info("manifest TXT updated", zap.String("path", clipFolder.ManifestTXTPath))
			}
		}

		// Upsert clip folder to DB
		if err := s.folderMemory.UpsertClipFolder(ctx, clipFolder); err != nil {
			s.log.Warn("failed to upsert clip folder", zap.Error(err))
		}
	}

	// Update MonitoredSource status
	if resp.Stats.Failed == resp.Stats.Requested {
		monitoredSource.Status = "failed"
	} else {
		monitoredSource.Status = "processed"
	}
	if s.monitoredRepo != nil {
		if err := s.monitoredRepo.UpsertSource(ctx, monitoredSource); err != nil {
			s.log.Error("Failed to update monitored source status", zap.Error(err))
		}
		// Only increment processed count if not all segments failed
		if resp.Stats.Failed != resp.Stats.Requested {
			if err := s.monitoredRepo.IncrementProcessed(ctx, monitoredSource.ID); err != nil {
				s.log.Error("Failed to increment processed count", zap.Error(err))
			}
		}
	}

	return resp, nil
}

// HandleJob processes a youtube_clip.extract job
func (s *Service) HandleJob(ctx context.Context, job *models.Job, tools *jobservice.JobTools) (map[string]any, error) {
	s.log.Info("handling youtube_clip.extract job",
		zap.String("job_id", job.ID),
	)

	var req ExtractRequest
	if len(job.Payload) > 0 {
		if err := json.Unmarshal(job.Payload, &req); err != nil {
			return nil, fmt.Errorf("failed to unmarshal payload: %w", err)
		}
	}

	if tools.Progress != nil {
		tools.Progress(10, "Starting YouTube clip extraction")
	}

	resp, err := s.Extract(ctx, &req)
	if err != nil {
		return nil, fmt.Errorf("extraction failed: %w", err)
	}

	if tools.Progress != nil {
		tools.Progress(100, "YouTube clip extraction completed")
	}

	result := map[string]any{
		"ok":              resp.OK,
		"source_url":      resp.SourceURL,
		"video_id":        resp.VideoID,
		"folder":          resp.Folder,
		"stats":           resp.Stats,
		"items":           resp.Items,
		"drive_folder_id": resp.DriveFolderID,
		"message":         "YouTube clip extraction completed",
	}
	if !resp.OK && resp.Error != "" {
		result["error"] = resp.Error
	}
	return result, nil
}
