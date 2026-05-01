package artlist

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"go.uber.org/zap"
	"google.golang.org/api/drive/v3"

	"velox/go-master/pkg/security"
)

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

	strategy := normalizeRunStrategy(req)
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

	tagFolderName := sanitizeDriveFolderName(resp.Term)
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

	// Use main folder directly (no subfolder per tag)
	tagFolderID := rootFolderID
	if !req.DryRun && s.driveClient != nil && rootFolderID != "" {
		s.log.Info("using main artlist folder for uploads",
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
			skip, _ := s.shouldSkipClip(ctx, strategy, clip)
			status := "would_process"
			if skip {
				status = "would_skip"
				resp.WouldSkip++
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

		skip, _ := s.shouldSkipClip(ctx, strategy, clip)
		if skip {
			s.log.Info("skipping existing clip", zap.String("clip_id", clip.ID), zap.String("drive_link", clip.DriveLink))
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

		tmpDir := filepath.Join(s.cfg.Storage.DataDir, s.cfg.Storage.TempDir)
		if err := os.MkdirAll(tmpDir, 0755); err != nil {
			s.log.Error("failed to create temp directory", zap.String("dir", tmpDir), zap.Error(err))
			// Fallback to os.TempDir if configured one fails
			tmpDir = os.TempDir()
		}

		saveDir := filepath.Join(s.cfg.Storage.DataDir, "artlist", tagFolderName)
		if err := os.MkdirAll(saveDir, 0755); err != nil {
			s.log.Error("failed to create save directory", zap.String("dir", saveDir), zap.Error(err))
			saveDir = tmpDir
		}

		rawPath := filepath.Join(tmpDir, fmt.Sprintf("raw_%s.mp4", clip.ID))
		safeName := sanitizeDriveFolderName(clip.Name)
		finalFilename := fmt.Sprintf("%s_%ds_%s.mp4", safeName, s.cfg.Video.Duration, clip.ID)
		processedPath := filepath.Join(saveDir, finalFilename)

		s.log.Info("downloading clip", zap.String("clip_id", clip.ID), zap.String("url", url))
		if err := s.downloadClip(url, rawPath); err != nil {
			s.log.Error("download failed", zap.String("clip_id", clip.ID), zap.Error(err))
			resp.Failed++
			resp.Items = append(resp.Items, RunTagItem{
				ClipID:   clip.ID,
				Name:     clip.Name,
				Filename: clip.Filename,
				Status:   "download_failed",
				Error:    err.Error(),
			})
			continue
		}

		s.log.Info("processing video (ffmpeg)", zap.String("clip_id", clip.ID), zap.String("output", processedPath))
		if err := s.processVideo(rawPath, processedPath); err != nil {
			s.log.Error("ffmpeg processing failed", zap.String("clip_id", clip.ID), zap.Error(err))
			_ = os.Remove(rawPath)
			resp.Failed++
			resp.Items = append(resp.Items, RunTagItem{
				ClipID:   clip.ID,
				Name:     clip.Name,
				Filename: clip.Filename,
				Status:   "process_failed",
				Error:    err.Error(),
			})
			continue
		}

		s.log.Info("calculating file hash", zap.String("clip_id", clip.ID), zap.String("path", processedPath))
		fileHash, err := calculateFileHash(processedPath)
		if err != nil {
			s.log.Error("hashing failed", zap.String("clip_id", clip.ID), zap.Error(err))
			_ = os.Remove(rawPath)
			_ = os.Remove(processedPath)
			resp.Failed++
			resp.Items = append(resp.Items, RunTagItem{
				ClipID:   clip.ID,
				Name:     clip.Name,
				Filename: clip.Filename,
				Status:   "hash_failed",
				Error:    err.Error(),
			})
			continue
		}

		var driveFile *drive.File
		if s.driveClient != nil {
			s.log.Info("uploading to Google Drive", zap.String("clip_id", clip.ID), zap.String("filename", finalFilename))
			f, err := os.Open(processedPath)
			if err == nil {
				driveFileReq := &drive.File{Name: finalFilename}
				if tagFolderID != "" {
					driveFileReq.Parents = []string{tagFolderID}
				}
				driveFile, err = s.driveClient.Files.Create(driveFileReq).Fields("id,webViewLink,md5Checksum").Media(f).Do()
				_ = f.Close()
				if err != nil {
					s.log.Error("drive upload failed", zap.String("clip_id", clip.ID), zap.Error(err))
				} else {
					s.log.Info("drive upload success", zap.String("clip_id", clip.ID), zap.String("file_id", driveFile.Id))
				}
			} else {
				s.log.Error("failed to open processed file for upload", zap.String("clip_id", clip.ID), zap.Error(err))
			}
		} else {
			s.log.Warn("driveClient is nil, skipping upload for clip", zap.String("clip_id", clip.ID))
		}

		_ = os.Remove(rawPath)
		// We DO NOT remove processedPath so it stays on disk

		if driveFile != nil {
			clip.DriveLink = driveFile.WebViewLink
			clip.DownloadLink = "https://drive.google.com/uc?id=" + driveFile.Id
		}
		clip.FileHash = fileHash
		if driveFile != nil {
			clip.Metadata = composeArtlistMetadata(clip.Metadata, fileHash, driveFile.Md5Checksum)
		} else {
			clip.Metadata = composeArtlistMetadata(clip.Metadata, fileHash, "")
		}
		clip.UpdatedAt = time.Now().UTC()
		clip.LocalPath = processedPath
		
		s.log.Info("updating database record", zap.String("clip_id", clip.ID), zap.String("local_path", processedPath))
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
				LocalPath:    processedPath,
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
			LocalPath:    processedPath,
			FileHash:     clip.FileHash,
		})

		s.log.Info("clip pipeline item completed",
			zap.String("clip_id", clip.ID),
			zap.String("drive_link", clip.DriveLink),
			zap.String("local_path", processedPath),
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

func getOrCreateFolder(svc *drive.Service, name, parentID string) (string, error) {
	query := fmt.Sprintf("name = '%s' and '%s' in parents and mimeType = 'application/vnd.google-apps.folder' and trashed = false", name, parentID)
	list, err := svc.Files.List().Q(query).Do()
	if err != nil {
		return "", err
	}

	if len(list.Files) > 0 {
		return list.Files[0].Id, nil
	}

	f, err := svc.Files.Create(&drive.File{
		Name:     name,
		MimeType: "application/vnd.google-apps.folder",
		Parents:  []string{parentID},
	}).Do()
	if err != nil {
		return "", err
	}
	return f.Id, nil
}

func (s *Service) downloadClip(sourceURL, rawPath string) error {
	ytdlp := s.cfg.External.YtdlpPath
	if ytdlp == "" {
		ytdlp = "yt-dlp"
	}
	cmdDl := exec.Command(ytdlp, "-o", rawPath, sourceURL)
	if out, err := cmdDl.CombinedOutput(); err != nil {
		return fmt.Errorf("yt-dlp failed: %v (output: %s)", err, string(out))
	}
	return nil
}

func (s *Service) processVideo(input, output string) error {
	ffmpeg := s.cfg.External.FfmpegPath
	if ffmpeg == "" {
		ffmpeg = "ffmpeg"
	}

	video := s.cfg.Video.WithDefaults()

	args := []string{
		"-y",
		"-t", fmt.Sprintf("%d", video.Duration),
		"-i", input,
		"-vf", fmt.Sprintf("scale=%d:%d:force_original_aspect_ratio=increase,crop=%d:%d,fps=%d",
			video.Width, video.Height, video.Width, video.Height, video.FPS),
		"-an",
		"-c:v", video.Codec,
		"-preset", video.Preset,
		"-crf", fmt.Sprintf("%d", video.CRF),
		output,
	}

	cmd := exec.Command(ffmpeg, args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("ffmpeg failed: %v (output: %s)", err, string(out))
	}
	return nil
}

func calculateFileHash(filePath string) (string, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := md5.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}

	return hex.EncodeToString(h.Sum(nil)), nil
}
