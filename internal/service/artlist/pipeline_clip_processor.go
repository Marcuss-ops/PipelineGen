package artlist

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"go.uber.org/zap"
	"velox/go-master/internal/core/lifecycle"
	"velox/go-master/internal/core/processor"
	"velox/go-master/pkg/models"
	"velox/go-master/pkg/security"
)

// processClip processes a single clip
func (s *Service) processClip(ctx context.Context, clip *models.Clip, tagFolderID, tagFolderName string, resp *RunTagResponse, req *RunTagRequest) {
	if clip == nil {
		return
	}

	// Add search term to clip's SearchTerms
	if req != nil && req.Term != "" {
		if clip.SearchTerms == nil {
			clip.SearchTerms = []string{}
		}
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
			DriveFileID:  clip.DriveFileID,
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
				Filename:     clip.Filename,
				Kind:         lifecycle.AssetKindVideo,
				Source:       "artlist",
				Group:        "",
				Subfolder:    tagFolderName,
				LocalPath:    result.LocalPath,
				FolderID:     tagFolderID,
				FolderPath:   tagFolderName,
				DriveLink:    clip.DriveLink,
				DriveFileID:  clip.DriveFileID,
				DownloadLink: result.DownloadLink,
				FileHash:     result.FileHash,
				Metadata:     metadata,
				RequireLocal: true,
				RequireHash:  true,
				RequireDrive: clip.DriveLink != "",
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
		// Update result and clip with lifecycle results
		result.DriveLink = lifecycleResult.DriveLink
		result.DriveFileID = lifecycleResult.DriveFileID
		result.DownloadLink = lifecycleResult.DownloadLink
		clip.DriveLink = lifecycleResult.DriveLink
		clip.DriveFileID = lifecycleResult.DriveFileID
		clip.DownloadLink = lifecycleResult.DownloadLink
		clip.LocalPath = result.LocalPath
		clip.FileHash = result.FileHash
	}

	// Add to response
	resp.Processed++
	item := RunTagItem{
		ClipID:       clip.ID,
		Name:         clip.Name,
		Filename:     result.Filename,
		Status:       "processed",
		DriveLink:    result.DriveLink,
		DriveFileID:  result.DriveFileID,
		DownloadLink: result.DownloadLink,
		LocalPath:    result.LocalPath,
		FileHash:     result.FileHash,
	}
	s.log.Info("response item prepared",
		zap.String("clip_id", clip.ID),
		zap.String("item.DriveFileID", item.DriveFileID),
		zap.String("item.DriveLink", item.DriveLink),
		zap.String("item.LocalPath", item.LocalPath),
	)
	resp.Items = append(resp.Items, item)

	// Auto-index clip after successful processing
	if s.clipIndexer != nil && s.clipIndexer.IsEnabled() {
		if err := s.clipIndexer.IndexClip(ctx, clip.ID); err != nil {
			s.log.Warn("failed to index clip after processing",
				zap.String("clip_id", clip.ID),
				zap.Error(err),
			)
		} else {
			s.log.Info("clip indexed successfully after processing",
				zap.String("clip_id", clip.ID),
			)
		}
	}

	// Save updated clip to DB (including DriveFileID from lifecycle)
	if err := s.UpsertClip(ctx, clip); err != nil {
		s.log.Warn("failed to save clip after processing",
			zap.String("clip_id", clip.ID),
			zap.Error(err),
		)
	}
}
