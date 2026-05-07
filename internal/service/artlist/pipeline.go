package artlist

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.uber.org/zap"
	driveapi "google.golang.org/api/drive/v3"

	"velox/go-master/internal/core/destination"
	"velox/go-master/internal/core/lifecycle"
	"velox/go-master/internal/core/processor"
	"velox/go-master/internal/upload/drive"
	"velox/go-master/pkg/models"
	"velox/go-master/pkg/pathutil"
	"velox/go-master/pkg/security"
)

type artlistChecksumChecker struct {
	driveClient *driveapi.Service
}

func (c *artlistChecksumChecker) GetMD5Checksum(ctx context.Context, driveLink string) (string, error) {
	fileID := drive.FileIDFromLink(driveLink)
	if fileID == "" {
		return "", fmt.Errorf("could not extract file ID from link: %s", driveLink)
	}
	file, err := c.driveClient.Files.Get(fileID).Fields("id,md5Checksum").Context(ctx).Do()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(file.Md5Checksum), nil
}

// RunTag executes the full Artlist pipeline for one search term
func (s *Service) RunTag(ctx context.Context, req *RunTagRequest) (*RunTagResponse, error) {
	resp := &RunTagResponse{
		OK:        true,
		Term:      strings.TrimSpace(req.Term),
		StartedAt: func() *string { t := time.Now().UTC().Format(time.RFC3339); return &t }(),
	}

	if resp.Term == "" {
		resp.OK = false
		resp.Error = "term is required"
		return resp, fmt.Errorf("term is required")
	}

	// Assume request is already normalized (handler or worker normalized before enqueue/execution)
	rootFolderID := req.RootFolderID
	strategy := req.Strategy
	resp.Requested = req.Limit
	resp.DryRun = req.DryRun
	resp.Strategy = req.Strategy
	resp.RootFolderID = req.RootFolderID

	if s.assetDestResolver == nil && !req.DryRun {
		s.log.Warn("drive service not configured, proceeding with local harvesting only")
	}

	tagFolderName := pathutil.SafeFolderName(resp.Term)
	s.log.Info("artlist pipeline start",
		zap.String("term", resp.Term),
		zap.Int("limit", req.Limit),
		zap.String("root_folder_id", rootFolderID),
		zap.String("strategy", strategy),
		zap.Bool("dry_run", req.DryRun),
		zap.String("tag_folder_name", tagFolderName),
	)

	// Step 0: Ensure we have clips in the DB via live search if none found
	clipsList, err := s.ensureClips(ctx, resp.Term, req.Limit, resp)
	if err != nil {
		resp.OK = false
		resp.Status = "failed"
		return resp, err
	}

	s.log.Info("clips available for processing", zap.String("term", resp.Term), zap.Int("count", len(clipsList)))

	if len(clipsList) == 0 {
		s.log.Warn("no clips found even after live search, terminating pipeline", zap.String("term", resp.Term))
		resp.Status = "completed"
		resp.OK = true
		return resp, nil
	}

	resp.Found = len(clipsList)
	resp.EstimatedSize = resp.Found
	if lastProcessedAt, err := s.lastProcessedAtForTerm(ctx, resp.Term); err == nil {
		resp.LastProcessedAt = lastProcessedAt
	}

	// Step 1: Resolve Drive destination
	tagFolderID := s.resolveDestination(ctx, rootFolderID, resp.Term, tagFolderName, resp)
	resp.TagFolderID = tagFolderID

	// Step 2: Select candidate clips up to limit
	candidateClips := s.selectCandidates(clipsList, req.Limit)

	// Step 3: Process candidates
	s.processCandidates(ctx, candidateClips, tagFolderID, tagFolderName, resp, req)

	resp.Status = "completed"
	resp.EndedAt = func() *string { t := time.Now().UTC().Format(time.RFC3339); return &t }()
	s.log.Info("artlist pipeline complete",
		zap.String("term", resp.Term),
		zap.Int("found", resp.Found),
		zap.Int("processed", resp.Processed),
		zap.Int("skipped", resp.Skipped),
		zap.Int("failed", resp.Failed),
		zap.String("tag_folder_id", resp.TagFolderID),
	)

	return resp, nil
}

// ensureClips retrieves clips for term, performing live search if needed
func (s *Service) ensureClips(ctx context.Context, term string, limit int, resp *RunTagResponse) ([]*models.Clip, error) {
	clipsList, err := s.artlistRepo.SearchClips(ctx, term)
	if err != nil {
		s.log.Error("failed to search clips in DB", zap.String("term", term), zap.Error(err))
		resp.Error = "db_search_error: " + err.Error()
	}

	// Force live search if clips have invalid URLs (e.g., Drive links instead of Artlist HLS)
	hasValidURLs := false
	hasValidDriveLinks := false
	for _, clip := range clipsList {
		if clip != nil && strings.Contains(clip.ExternalURL, "artlist") && strings.Contains(clip.ExternalURL, ".m3u8") {
			hasValidURLs = true
			break
		}
		if clip != nil && clip.DriveLink != "" && strings.Contains(clip.DriveLink, "drive.google.com") {
			hasValidDriveLinks = true
		}
	}

	// If we have clips with valid Drive links, skip live search
	if hasValidDriveLinks && len(clipsList) > 0 {
		s.log.Info("clips have valid Drive links, skipping live search", zap.String("term", term), zap.Int("count", len(clipsList)))
		return clipsList, nil
	}

	if len(clipsList) == 0 || !hasValidURLs {
		if resp.Error != "" {
			s.log.Warn("DB error occurred, attempting live search fallback", zap.String("term", term))
		} else {
			if !hasValidURLs && len(clipsList) > 0 {
				s.log.Info("found clips but with invalid URLs, forcing live search", zap.String("term", term))
			} else {
				s.log.Info("no clips found in DB for term, performing live search discovery", zap.String("term", term))
			}
		}
		searchResp, err := s.SearchLiveAndSave(ctx, term, limit*2)
		if err != nil {
			s.log.Error("live search discovery failed", zap.String("term", term), zap.Error(err))
			if resp.Error != "" {
				resp.Error = "db_error_and_live_search_failed: " + err.Error()
			}
			// If DB failed and live search failed, return error
			if strings.HasPrefix(resp.Error, "db_search_error") {
				resp.OK = false
				resp.Status = "failed"
				return nil, fmt.Errorf("failed to get clips: %s", resp.Error)
			}
		} else if searchResp != nil {
			s.log.Info("live search discovery completed", zap.String("term", term), zap.Int("found", len(searchResp.Clips)))
			resp.Error = "" // Clear DB error if live search succeeded
		}
		// Reload from DB after search
		clipsList, err = s.artlistRepo.SearchClips(ctx, term)
		if err != nil {
			s.log.Error("failed to reload clips from DB after discovery", zap.String("term", term), zap.Error(err))
			resp.OK = false
			resp.Status = "failed"
			resp.Error = "failed to reload clips after discovery: " + err.Error()
			return nil, err
		}
	}
	return clipsList, nil
}

// resolveDestination resolves the Drive folder for the tag
func (s *Service) resolveDestination(ctx context.Context, rootFolderID, term, tagFolderName string, resp *RunTagResponse) string {
	tagFolderID := rootFolderID
	if s.assetDestResolver != nil && rootFolderID != "" {
		resolved, err := s.assetDestResolver.Resolve(ctx, &destination.ResolveRequest{
			Source:          "artlist",
			Group:           term,
			FolderID:        rootFolderID,
			SubfolderName:   tagFolderName,
			CreateSubfolder: true,
		})
		if err != nil {
			s.log.Warn("failed to resolve drive destination, using root folder ID",
				zap.String("root_folder_id", rootFolderID),
				zap.Error(err),
			)
		} else {
			tagFolderID = resolved.FolderID
		}
	}
	if !resp.DryRun && s.assetDestResolver != nil && tagFolderID != "" {
		s.log.Info("using artlist folder for uploads",
			zap.String("folder_id", tagFolderID),
			zap.String("folder_link", "https://drive.google.com/drive/folders/"+tagFolderID),
		)
	}
	return tagFolderID
}

// selectCandidates selects up to limit clips from the list
func (s *Service) selectCandidates(clipsList []*models.Clip, limit int) []*models.Clip {
	candidateClips := clipsList
	if len(candidateClips) > limit {
		candidateClips = candidateClips[:limit]
	}
	return candidateClips
}

// processCandidates processes the candidate clips, handling dry-run and normal processing
func (s *Service) processCandidates(ctx context.Context, candidates []*models.Clip, tagFolderID, tagFolderName string, resp *RunTagResponse, req *RunTagRequest) {
	if req.DryRun {
		s.processDryRun(ctx, candidates, resp)
		return
	}
	for _, clip := range candidates {
		s.processClip(ctx, clip, tagFolderID, tagFolderName, resp, req)
	}
}

// processDryRun simulates processing without real downloads
func (s *Service) processDryRun(ctx context.Context, candidates []*models.Clip, resp *RunTagResponse) {
	s.log.Info("dry-run mode, simulating pipeline", zap.String("term", resp.Term), zap.Int("candidates", len(candidates)))
	for _, clip := range candidates {
		if clip == nil {
			continue
		}
		status := "would_process"
		if s.lifecycleService != nil {
			input := &lifecycle.FinalizeInput{
				ID:           clip.ID,
				Name:         clip.Name,
				Filename:     clip.Filename,
				Kind:         lifecycle.AssetKindVideo,
				Source:       "artlist",
				LocalPath:    clip.LocalPath,
				DriveLink:    clip.DriveLink,
				FileHash:     clip.FileHash,
				RequireLocal: true,
				RequireHash:  true,
				RequireDrive: clip.DriveLink != "",
				VerifyDB:     true,
			}
			result, err := s.lifecycleService.CheckDuplicate(ctx, input, clip.FileHash)
			if err != nil {
				status = "would_skip"
				resp.WouldSkip++
				s.log.Info("would skip clip", zap.String("clip_id", clip.ID), zap.Error(err))
			} else if result.Status == "would_skip_duplicate" {
				status = "would_skip_duplicate"
				resp.WouldSkip++
			} else {
				resp.WouldProcess++
			}
		} else {
			resp.WouldProcess++
		}
		resp.Items = append(resp.Items, RunTagItem{
			ClipID: clip.ID,
			Name:   clip.Name,
			Status: status,
		})
	}
	resp.Status = "completed_dry_run"
	resp.OK = true
}

// processClip processes a single clip
func (s *Service) processClip(ctx context.Context, clip *models.Clip, tagFolderID, tagFolderName string, resp *RunTagResponse, req *RunTagRequest) {
	if clip == nil {
		return
	}

	// Aggiungi il termine di ricerca ai SearchTerms del clip
	if req != nil && req.Term != "" {
		// Inizializza SearchTerms se nil
		if clip.SearchTerms == nil {
			clip.SearchTerms = []string{}
		}
		// Aggiungi il termine se non presente
		termExists := false
		for _, t := range clip.SearchTerms {
			if t == req.Term {
				termExists = true
				break
			}
		}
		if !termExists {
			clip.SearchTerms = append(clip.SearchTerms, req.Term)
		}
	}

	// Skip if clip already has Drive link
	if strings.TrimSpace(clip.DriveLink) != "" {
		s.log.Info("skipping clip with existing drive link",
			zap.String("clip_id", clip.ID),
			zap.String("drive_link", clip.DriveLink))
		resp.Skipped++
		resp.Items = append(resp.Items, RunTagItem{
			ClipID:       clip.ID,
			Name:         clip.Name,
			Filename:     clip.Filename,
			Status:       "skipped_existing",
			DriveLink:    clip.DriveLink,
			DownloadLink: clip.DownloadLink,
			LocalPath:    clip.LocalPath,
		})
		return
	}

	// Get download URL
	url := strings.TrimSpace(clip.DownloadLink)
	if url == "" || !strings.Contains(url, ".m3u8") {
		url = strings.TrimSpace(clip.ExternalURL)
	}
	if strings.Contains(url, "drive.google") {
		s.log.Warn("clip has Drive URL in source fields, skipping",
			zap.String("clip_id", clip.ID),
			zap.String("url", url))
		resp.Skipped++
		resp.Items = append(resp.Items, RunTagItem{
			ClipID:   clip.ID,
			Name:     clip.Name,
			Filename: clip.Filename,
			Status:   "skipped_invalid_url",
			Error:    "source URL is a Drive link, not a valid download URL",
		})
		return
	}
	if url == "" {
		resp.Failed++
		resp.Items = append(resp.Items, RunTagItem{
			ClipID:   clip.ID,
			Name:     clip.Name,
			Filename: clip.Filename,
			Status:   "failed",
			Error:    "missing source url",
		})
		return
	}
	if err := security.ValidateDownloadURL(url); err != nil {
		s.log.Warn("artlist pipeline skipping unsupported url", zap.String("clip_id", clip.ID), zap.String("url", url))
		resp.Skipped++
		resp.Items = append(resp.Items, RunTagItem{
			ClipID:   clip.ID,
			Name:     clip.Name,
			Filename: clip.Filename,
			Status:   "skipped_invalid_url",
			Error:    err.Error(),
		})
		return
	}

	s.log.Info("processing clip",
		zap.String("clip_id", clip.ID),
		zap.String("name", clip.Name),
		zap.String("url", url),
		zap.String("folder_id", tagFolderID),
	)

	processInput := &processor.ProcessInput{
		ID:        clip.ID,
		Name:      clip.Name,
		SourceURL: url,
		Term:      resp.Term,
		OutputDir: filepath.Join(s.cfg.Storage.DataDir, "artlist", tagFolderName),
		FolderID:  tagFolderID,
	}
	// Apply preset config if available from req
	if req != nil {
		if req.ClipDuration > 0 {
			processInput.Duration = req.ClipDuration
		} else if s.cfg.Video.Duration > 0 {
			processInput.Duration = s.cfg.Video.Duration
		}
		if req.Width > 0 {
			processInput.Width = req.Width
		}
		if req.Height > 0 {
			processInput.Height = req.Height
		}
		if req.FPS > 0 {
			// Note: ProcessInput doesn't have FPS field directly
			// This would need to be passed via Metadata or a custom field
			processInput.Metadata = map[string]interface{}{
				"fps": req.FPS,
			}
		}
	} else {
		processInput.Duration = s.cfg.Video.Duration
	}
	result, err := s.mediaProcessor.Process(ctx, processInput)
	if err != nil {
		s.log.Error("media processing failed", zap.String("clip_id", clip.ID), zap.Error(err))
		resp.Failed++
		resp.Items = append(resp.Items, RunTagItem{
			ClipID:   clip.ID,
			Name:     clip.Name,
			Filename: clip.Filename,
			Status:   "media_process_failed",
			Error:    err.Error(),
		})
		return
	}

	_ = os.Remove(filepath.Join(os.TempDir(), fmt.Sprintf("raw_%s.mp4", clip.ID)))

	// Use LifecycleService for dedupe + upload + persist
	metadata := composeArtlistMetadata(clip.Metadata, result.FileHash, result.FileHash)
	if s.lifecycleService != nil {
		input := &lifecycle.FinalizeInput{
			ID:           clip.ID,
			Name:         clip.Name,
			Filename:     result.Filename,
			Kind:         lifecycle.AssetKindVideo,
			Source:       "artlist",
			Group:        "",
			Subfolder:    tagFolderName,
			LocalPath:    result.LocalPath,
			FolderID:     tagFolderID,
			FolderPath:   tagFolderName,
			DriveLink:    result.DriveLink,
			DriveFileID:  result.DriveFileID,
			DownloadLink: result.DownloadLink,
			FileHash:     result.FileHash,
			Metadata:     metadata,
			RequireLocal: true,
			RequireHash:  true,
			RequireDrive: result.DriveLink != "",
			VerifyDB:     true,
		}
		lifecycleResult, err := s.lifecycleService.ProcessAsset(ctx, input, result.FileHash)
		if err != nil {
			s.log.Error("lifecycle failed", zap.String("clip_id", clip.ID), zap.Error(err))
			resp.Failed++
			resp.Items = append(resp.Items, RunTagItem{
				ClipID:       clip.ID,
				Name:         clip.Name,
				Filename:     result.Filename,
				Status:       "lifecycle_failed",
				DriveLink:    result.DriveLink,
				DownloadLink: result.DownloadLink,
				LocalPath:    result.LocalPath,
				FileHash:     result.FileHash,
				Error:        err.Error(),
			})
			return
		}
		if !lifecycleResult.OK {
			resp.Skipped++
			resp.Items = append(resp.Items, RunTagItem{
				ClipID:       clip.ID,
				Name:          clip.Name,
				Filename:     result.Filename,
				Status:       lifecycleResult.Status,
				DriveLink:    lifecycleResult.DriveLink,
				DownloadLink: lifecycleResult.DownloadLink,
				LocalPath:    result.LocalPath,
				FileHash:     result.FileHash,
				Error:        lifecycleResult.Error,
			})
			return
		}
		// Update result with lifecycle results
		result.DriveLink = lifecycleResult.DriveLink
		result.DriveFileID = lifecycleResult.DriveFileID
		result.DownloadLink = lifecycleResult.DownloadLink
	}

	// Add to response
	resp.Processed++
	resp.Items = append(resp.Items, RunTagItem{
		ClipID:       clip.ID,
		Name:         clip.Name,
		Filename:     result.Filename,
		Status:       "processed",
		DriveLink:    result.DriveLink,
		DownloadLink: result.DownloadLink,
		LocalPath:    result.LocalPath,
		FileHash:     result.FileHash,
	})
}
