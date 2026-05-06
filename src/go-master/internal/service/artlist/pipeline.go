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
	"velox/go-master/internal/core/processor"
	"velox/go-master/internal/service/assetpipeline"
	"velox/go-master/internal/upload/drive"
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

// RunTag executes the full Artlist pipeline for one search term:
// search locally, download the source asset, process it, upload it to Drive,
// and persist the updated clip metadata back to the database.
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

	if s.driveService == nil && !req.DryRun {
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
	clipsList, err := s.artlistRepo.SearchClips(ctx, resp.Term)
	if err != nil {
		s.log.Error("failed to search clips in DB", zap.String("term", resp.Term), zap.Error(err))
		// DB error - try live search as fallback, but track the error
		resp.Error = "db_search_error: " + err.Error()
	}

	// Force live search if clips have invalid URLs (e.g., Drive links instead of Artlist HLS)
	hasValidURLs := false
	for _, clip := range clipsList {
		if clip != nil && strings.Contains(clip.ExternalURL, "artlist") && strings.Contains(clip.ExternalURL, ".m3u8") {
			hasValidURLs = true
			break
		}
	}

	if len(clipsList) == 0 || !hasValidURLs {
		if resp.Error != "" {
			s.log.Warn("DB error occurred, attempting live search fallback", zap.String("term", resp.Term))
		} else {
			if !hasValidURLs && len(clipsList) > 0 {
				s.log.Info("found clips but with invalid URLs, forcing live search", zap.String("term", resp.Term))
			} else {
				s.log.Info("no clips found in DB for term, performing live search discovery", zap.String("term", resp.Term))
			}
		}
		searchResp, err := s.SearchLiveAndSave(ctx, resp.Term, req.Limit*2)
		if err != nil {
			s.log.Error("live search discovery failed", zap.String("term", resp.Term), zap.Error(err))
			if resp.Error != "" {
				resp.Error = "db_error_and_live_search_failed: " + err.Error()
			}
			// If DB failed and live search failed, return error
			if strings.HasPrefix(resp.Error, "db_search_error") {
				resp.OK = false
				resp.Status = "failed"
				return resp, fmt.Errorf("failed to get clips: %s", resp.Error)
			}
		} else if searchResp != nil {
			s.log.Info("live search discovery completed", zap.String("term", resp.Term), zap.Int("found", len(searchResp.Clips)))
			resp.Error = "" // Clear DB error if live search succeeded
		}
		// Reload from DB after search
		clipsList, err = s.artlistRepo.SearchClips(ctx, resp.Term)
		if err != nil {
			s.log.Error("failed to reload clips from DB after discovery", zap.String("term", resp.Term), zap.Error(err))
			resp.OK = false
			resp.Status = "failed"
			resp.Error = "failed to reload clips after discovery: " + err.Error()
			return resp, err
		}
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

	// Resolve Drive destination using unified asset destination resolver
	tagFolderID := rootFolderID
	if s.assetDestResolver != nil && rootFolderID != "" {
		resolved, err := s.assetDestResolver.Resolve(ctx, &destination.ResolveRequest{
			Source:          "artlist",
			Group:           req.Term,
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
	if !req.DryRun && s.driveService != nil && tagFolderID != "" {
		s.log.Info("using artlist folder for uploads",
			zap.String("folder_id", tagFolderID),
			zap.String("folder_link", "https://drive.google.com/drive/folders/"+tagFolderID),
		)
	}
	resp.TagFolderID = tagFolderID

	candidateClips := clipsList
	if len(candidateClips) > req.Limit {
		candidateClips = candidateClips[:req.Limit]
	}

	// Dry-run: simulate without real processing
	if req.DryRun {
		s.log.Info("dry-run mode, simulating pipeline", zap.String("term", resp.Term), zap.Int("candidates", len(candidateClips)))
		for _, clip := range candidateClips {
			if clip == nil {
				continue
			}
			// Use LifecycleService to check if asset would be skipped
			status := "would_process"
			if s.lifecycleService != nil {
				metadata := composeArtlistMetadata(clip.Metadata, clip.FileHash, "")
				input := &assetpipeline.FinalizeInput{
					ID:           clip.ID,
					Name:         clip.Name,
					Filename:     clip.Filename,
					Kind:         assetpipeline.AssetKindVideo,
					Source:       "artlist",
					LocalPath:    clip.LocalPath,
					DriveLink:    clip.DriveLink,
					FileHash:     clip.FileHash,
					Metadata:     metadata,
					RequireLocal: true,
					RequireHash:  true,
					RequireDrive: clip.DriveLink != "",
					VerifyDB:     true,
				}
				_, err := s.lifecycleService.ProcessAsset(ctx, input, clip.FileHash)
				if err != nil {
					status = "would_skip"
					resp.WouldSkip++
					s.log.Info("would skip clip", zap.String("clip_id", clip.ID), zap.Error(err))
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
		return resp, nil
	}

	for _, clip := range candidateClips {
		if clip == nil {
			continue
		}

		// Skip if clip already has Drive link (already processed and uploaded)
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
			continue
		}

		// Get download URL - use HLS URL from DownloadLink (set by live search)
		url := strings.TrimSpace(clip.DownloadLink)

		// If DownloadLink is empty or not HLS, try ExternalURL
		if url == "" || !strings.Contains(url, ".m3u8") {
			url = strings.TrimSpace(clip.ExternalURL)
		}

		// Skip Drive URLs - they're not valid source URLs for download
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
			continue
		}

		s.log.Info("processing clip",
			zap.String("clip_id", clip.ID),
			zap.String("name", clip.Name),
			zap.String("url", url),
		)

		if url == "" {
			resp.Failed++
			resp.Items = append(resp.Items, RunTagItem{
				ClipID:   clip.ID,
				Name:     clip.Name,
				Filename: clip.Filename,
				Status:   "failed",
				Error:    "missing source url",
			})
			continue
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
			continue
		}

		s.log.Info("artlist pipeline processing clip",
			zap.String("clip_id", clip.ID),
			zap.String("name", clip.Name),
			zap.String("url", url),
			zap.String("folder_id", tagFolderID),
		)

		// Use mediaasset processor for download/process/hash/upload
		processInput := &processor.ProcessInput{
			ID:        clip.ID,
			Name:      clip.Name,
			SourceURL: url,
			Term:      resp.Term,
			OutputDir: filepath.Join(s.cfg.Storage.DataDir, "artlist", tagFolderName),
			FolderID:  tagFolderID,
			Duration:  s.cfg.Video.Duration,
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
			continue
		}

		_ = os.Remove(filepath.Join(os.TempDir(), fmt.Sprintf("raw_%s.mp4", clip.ID)))

		// Use LifecycleService for dedupe + upload + persist
		metadata := composeArtlistMetadata(clip.Metadata, result.FileHash, result.FileHash)
		if s.lifecycleService != nil {
			input := &assetpipeline.FinalizeInput{
				ID:           clip.ID,
				Name:         clip.Name,
				Filename:     result.Filename,
				Kind:         assetpipeline.AssetKindVideo,
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
				continue
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
				continue
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
