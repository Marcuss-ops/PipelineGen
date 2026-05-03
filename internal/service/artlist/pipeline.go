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

	"velox/go-master/internal/service/assetstore"
	"velox/go-master/internal/service/drivedestination"
	"velox/go-master/internal/service/mediaasset"
	"velox/go-master/internal/service/pipeline"
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
	if req.Limit <= 0 {
		req.Limit = 1
	}
	if req.Limit > 500 {
		req.Limit = 500
	}
	resp.Requested = req.Limit
	resp.DryRun = req.DryRun

	strategy := string(pipeline.NormalizeStrategy(req.Strategy, req.ForceReupload))
	resp.Strategy = strategy

	rootFolderID := strings.TrimSpace(req.RootFolderID)
	if rootFolderID == "" {
		rootFolderID = strings.TrimSpace(s.driveFolderID)
	}
	if rootFolderID == "" {
		rootFolderID = "root"
	}
	resp.RootFolderID = rootFolderID

	if s.driveClient == nil && !req.DryRun {
		s.log.Warn("drive client not configured, proceeding with local harvesting only")
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
	clipsList, err := s.clipsRepo.SearchClips(ctx, resp.Term)
	if err != nil {
		s.log.Error("failed to search clips in DB", zap.String("term", resp.Term), zap.Error(err))
		// DB error - try live search as fallback, but track the error
		resp.Error = "db_search_error: " + err.Error()
	}

	if len(clipsList) == 0 {
		if resp.Error != "" {
			s.log.Warn("DB error occurred, attempting live search fallback", zap.String("term", resp.Term))
		} else {
			s.log.Info("no clips found in DB for term, performing live search discovery", zap.String("term", resp.Term))
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
		clipsList, err = s.clipsRepo.SearchClips(ctx, resp.Term)
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

	// Resolve Drive destination using drivedestination service
	tagFolderID := rootFolderID
	if s.driveDestination != nil && rootFolderID != "" {
		resolved, err := s.driveDestination.Resolve(ctx, &drivedestination.Request{
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
	if !req.DryRun && s.driveClient != nil && tagFolderID != "" {
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
			asset := assetstore.ExistingAsset{
				ID:        clip.ID,
				DriveLink: clip.DriveLink,
				FileHash:  clip.FileHash,
				Metadata:  clip.Metadata,
				LocalPath: clip.LocalPath,
			}
			skip, reason, _ := assetstore.ShouldSkipExisting(ctx, asset, assetstore.ExistencePolicy(strategy), nil)
			status := "would_process"
			if skip {
				status = "would_skip"
				resp.WouldSkip++
				s.log.Info("would skip clip", zap.String("clip_id", clip.ID), zap.String("reason", reason))
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

	processed := 0
	for _, clip := range candidateClips {
		if clip == nil {
			continue
		}
		
		url := strings.TrimSpace(clip.ExternalURL)
		if url == "" {
			url = strings.TrimSpace(clip.DownloadLink)
		}

		s.log.Info("processing clip", 
			zap.String("clip_id", clip.ID), 
			zap.String("name", clip.Name),
			zap.String("url", url),
		)

		// Check if clip should be skipped using assetstore
		asset := assetstore.ExistingAsset{
			ID:        clip.ID,
			DriveLink: clip.DriveLink,
			FileHash:  clip.FileHash,
			Metadata:  clip.Metadata,
			LocalPath: clip.LocalPath,
		}
		var checker assetstore.ChecksumChecker
		if s.driveClient != nil {
			checker = &artlistChecksumChecker{driveClient: s.driveClient}
		}
		skip, reason, _ := assetstore.ShouldSkipExisting(ctx, asset, assetstore.ExistencePolicy(strategy), checker)
		
		if skip {
			s.log.Info("skipping existing clip", zap.String("clip_id", clip.ID), zap.String("reason", reason))
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
		assetInput := mediaasset.AssetInput{
			ID:        clip.ID,
			Name:      clip.Name,
			SourceURL: url,
			Term:      resp.Term,
			OutputDir: filepath.Join(s.cfg.Storage.DataDir, "artlist", tagFolderName),
			FolderID:  tagFolderID,
			Duration:  s.cfg.Video.Duration,
		}

		result, err := s.mediaProcessor.DownloadProcessUpload(ctx, assetInput)
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

		// Update clip with results from media processor
		if result.DriveLink != "" {
			clip.DriveLink = result.DriveLink
			clip.DownloadLink = result.DownloadLink
		}
		clip.FileHash = result.FileHash
		clip.Metadata = composeArtlistMetadata(clip.Metadata, result.FileHash, result.FileHash)
		clip.UpdatedAt = time.Now().UTC()
		clip.LocalPath = result.LocalPath
		
		s.log.Info("updating database record", zap.String("clip_id", clip.ID), zap.String("local_path", result.LocalPath))
		if err := s.clipsRepo.UpsertClip(ctx, clip); err != nil {
			s.log.Error("db update failed", zap.String("clip_id", clip.ID), zap.Error(err))
			resp.Failed++
		resp.Items = append(resp.Items, RunTagItem{
			ClipID:       clip.ID,
			Name:         clip.Name,
			Filename:     clip.Filename,
			Status:       "db_update_failed",
			DriveLink:    clip.DriveLink,
			DownloadLink: clip.DownloadLink,
			LocalPath:    result.LocalPath,
			FileHash:     clip.FileHash,
			Error:        err.Error(),
		})
			continue
		}

		processed++
		resp.Processed = processed
		resp.Items = append(resp.Items, RunTagItem{
			ClipID:       clip.ID,
			Name:         clip.Name,
			Filename:     clip.Filename,
			Status:       "processed",
			DownloadURL:  url,
			DriveLink:    clip.DriveLink,
			DownloadLink: clip.DownloadLink,
			LocalPath:    result.LocalPath,
			FileHash:     clip.FileHash,
		})

		s.log.Info("clip pipeline item completed",
			zap.String("clip_id", clip.ID),
			zap.String("drive_link", clip.DriveLink),
			zap.String("local_path", result.LocalPath),
		)
	}

	resp.Processed = processed
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

func getOrCreateFolder(svc *driveapi.Service, name, parentID string) (string, error) {
	query := fmt.Sprintf("name = '%s' and '%s' in parents and mimeType = 'application/vnd.google-apps.folder' and trashed = false", name, parentID)
	list, err := svc.Files.List().Q(query).Do()
	if err != nil {
		return "", err
	}

	if len(list.Files) > 0 {
		return list.Files[0].Id, nil
	}

	f, err := svc.Files.Create(&driveapi.File{
		Name:     name,
		MimeType: "application/vnd.google-apps.folder",
		Parents:  []string{parentID},
	}).Do()
	if err != nil {
		return "", err
	}
	return f.Id, nil
}






