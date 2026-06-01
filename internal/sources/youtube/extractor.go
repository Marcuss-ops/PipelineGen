package youtube

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.uber.org/zap"
	"velox/go-master/internal/core/destination"
	"velox/go-master/internal/media/models"
	"velox/go-master/internal/pkg/hashutil"
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

	// ── Validation ────────────────────────────────────────────────
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

	// ── MonitoredSource upsert ────────────────────────────────────
	now := time.Now().UTC().Format(time.RFC3339)
	monitoredSource := &models.MonitoredSource{
		ID:           "youtube_" + videoID,
		Source:       "youtube",
		ExternalID:   videoID,
		ExternalURL:  req.URL,
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

	// ── Folder setup ──────────────────────────────────────────────
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

	// ── Drive destination resolution ──────────────────────────────
	driveFolderID, resolvedPath := s.resolveDriveDestination(ctx, req, videoID)

	// ── Folder info on response ───────────────────────────────────
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

	// ── Clip folder loading ───────────────────────────────────────
	clipFolder := s.loadClipFolder(ctx, videoID, outDir, driveFolderID, resolvedPath, resp, req)

	// ── Manifest loading ──────────────────────────────────────────
	manifest := s.loadManifest(clipFolder, videoID, outDir, driveFolderID, resolvedPath)

	// ── Segment processing loop ───────────────────────────────────
	for i, seg := range req.Segments {
		item := s.processSegment(ctx, seg, req, resp, videoID, driveFolderID, resolvedPath, folderSlug, manifest, i)

		// Enrich response with segment result
		resp.Items = append(resp.Items, item)

		switch item.Status {
		case "failed":
			resp.Stats.Failed++
			resp.OK = false
		case "skipped":
			resp.Stats.Skipped++
		default:
			resp.Stats.Processed++
			// Trigger auto-indexing for successfully processed clips
			s.triggerAutoIndexing(ctx, item.ID)
		}
	}

	// ── Manifest save ─────────────────────────────────────────────
	s.saveManifest(ctx, clipFolder, manifest, req, outDir)

	// ── MonitoredSource status update ─────────────────────────────
	s.updateMonitoredSourceStatus(ctx, monitoredSource, resp)

	return resp, nil
}

// resolveDriveDestination resolves the Google Drive folder for the extraction output.
func (s *Service) resolveDriveDestination(ctx context.Context, req *ExtractRequest, videoID string) (string, string) {
	if s.assetDestResolver == nil || req.Destination == nil {
		return "", ""
	}

	destReq := &destination.ResolveRequest{
		Source:          "youtube",
		Group:           req.Destination.Group,
		FolderID:        req.Destination.FolderID,
		FolderPath:      req.Destination.FolderPath,
		SubfolderName:   req.Destination.SubfolderName,
		CreateSubfolder: req.Destination.CreateSubfolder,
	}

	if destReq.SubfolderName == "" {
		destReq.SubfolderName = "yt_" + videoID
		destReq.CreateSubfolder = true
		s.log.Info("auto-assigning video subfolder", zap.String("subfolder", destReq.SubfolderName))
	}

	resolved, err := s.assetDestResolver.Resolve(ctx, destReq)
	if err != nil {
		s.log.Warn("failed to resolve drive destination", zap.Error(err))
		return "", ""
	}
	return resolved.FolderID, resolved.FolderPath
}

// loadClipFolder loads an existing clip folder from DB or creates a new one.
func (s *Service) loadClipFolder(ctx context.Context, videoID, outDir, driveFolderID, resolvedPath string,
	resp *ExtractResponse, req *ExtractRequest) *models.ClipFolder {
	if s.clipsRepo == nil {
		return nil
	}

	folderID := fmt.Sprintf("clipfolder_youtube_%s", videoID)
	existingFolder, err := s.clipsRepo.GetClipFolder(ctx, folderID)
	if err == nil && existingFolder != nil {
		s.log.Info("loaded existing clip folder", zap.String("folder_id", folderID))

		if existingFolder.FolderID == "" && driveFolderID != "" {
			existingFolder.FolderID = driveFolderID
			existingFolder.FolderPath = resolvedPath
			existingFolder.Group = getGroupFromDestination(req.Destination)
		}
		if existingFolder.LocalFolderPath != outDir {
			existingFolder.LocalFolderPath = outDir
			existingFolder.ManifestTXTPath = filepath.Join(outDir, "clip_manifest.txt")
			existingFolder.ManifestJSONPath = filepath.Join(outDir, "clip_manifest.json")
		}
		return existingFolder
	}

	clipFolder := &models.ClipFolder{
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
	return clipFolder
}

// loadManifest loads an existing clip manifest from disk or creates a new one.
func (s *Service) loadManifest(clipFolder *models.ClipFolder, videoID, outDir, driveFolderID, resolvedPath string) *models.ClipManifest {
	folderID := fmt.Sprintf("clipfolder_youtube_%s", videoID)
	manifest := &models.ClipManifest{
		ID:              folderID,
		FolderID:        driveFolderID,
		FolderPath:      resolvedPath,
		Source:          "youtube",
		SourceURL:       "",
		VideoID:         videoID,
		LocalFolderPath: outDir,
		Clips:           []models.ClipManifestItem{},
	}

	if clipFolder == nil || clipFolder.ManifestJSONPath == "" {
		return manifest
	}

	loadedManifest, err := s.folderMemory.LoadManifest(clipFolder.ManifestJSONPath)
	if err != nil || loadedManifest == nil {
		return manifest
	}

	if loadedManifest.FolderID == "" && driveFolderID != "" {
		loadedManifest.FolderID = driveFolderID
		loadedManifest.FolderPath = resolvedPath
	}
	if loadedManifest.ID == "" {
		loadedManifest.ID = folderID
	}
	s.log.Info("loaded existing manifest", zap.Int("clip_count", len(loadedManifest.Clips)))
	return loadedManifest
}

// saveManifest writes the manifest JSON and TXT files and updates the clip folder in DB.
func (s *Service) saveManifest(ctx context.Context, clipFolder *models.ClipFolder, manifest *models.ClipManifest,
	req *ExtractRequest, outDir string) {
	if clipFolder == nil {
		return
	}

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

// updateMonitoredSourceStatus sets the final status on the monitored source record.
func (s *Service) updateMonitoredSourceStatus(ctx context.Context, ms *models.MonitoredSource, resp *ExtractResponse) {
	if s.monitoredRepo == nil {
		return
	}

	if resp.Stats.Failed == resp.Stats.Requested {
		ms.Status = "failed"
	} else {
		ms.Status = "processed"
	}

	if err := s.monitoredRepo.UpsertSource(ctx, ms); err != nil {
		s.log.Error("Failed to update monitored source status", zap.Error(err))
	}
	if resp.Stats.Failed != resp.Stats.Requested {
		if err := s.monitoredRepo.IncrementProcessed(ctx, ms.ID); err != nil {
			s.log.Error("Failed to increment processed count", zap.Error(err))
		}
	}
}
