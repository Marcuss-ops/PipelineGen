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
		resp.OK = false
		resp.Error = "drive client not configured"
		return resp, fmt.Errorf("drive client not configured")
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

	clipsList, err := s.clipsRepo.SearchClips(ctx, resp.Term)
	if err != nil {
		resp.OK = false
		resp.Error = err.Error()
		return resp, err
	}

	resp.Found = len(clipsList)
	resp.EstimatedSize = resp.Found
	if lastProcessedAt, err := s.lastProcessedAtForTerm(ctx, resp.Term); err == nil {
		resp.LastProcessedAt = lastProcessedAt
	}

	var tagFolderID string
	if !req.DryRun && rootFolderID != "" {
		tagFolderID, err = getOrCreateFolder(s.driveClient, tagFolderName, rootFolderID)
		if err != nil {
			resp.OK = false
			resp.Error = err.Error()
			return resp, err
		}
	}
	resp.TagFolderID = tagFolderID

	candidateClips := clipsList
	if len(candidateClips) > req.Limit {
		candidateClips = candidateClips[:req.Limit]
	}

	if req.DryRun {
		for _, clip := range candidateClips {
			if clip == nil || clip.Source != "artlist" {
				resp.Skipped++
				continue
			}
			skip, _ := s.shouldSkipClip(ctx, strategy, clip)
			if skip {
				resp.WouldSkip++
			} else {
				resp.WouldProcess++
			}
		}
		s.log.Info("artlist dry run complete",
			zap.String("term", resp.Term),
			zap.Int("would_process", resp.WouldProcess),
			zap.Int("would_skip", resp.WouldSkip),
			zap.String("tag_folder_id", tagFolderID),
		)
		return resp, nil
	}

	processed := 0
	for _, clip := range candidateClips {
		if clip == nil {
			continue
		}
		if clip.Source != "artlist" {
			resp.Skipped++
			continue
		}

		skip, _ := s.shouldSkipClip(ctx, strategy, clip)
		if skip {
			s.log.Info("artlist pipeline skip existing drive clip",
				zap.String("clip_id", clip.ID),
				zap.String("name", clip.Name),
				zap.String("drive_link", clip.DriveLink),
			)
			resp.Skipped++
			resp.Items = append(resp.Items, RunTagItem{
				ClipID:       clip.ID,
				Name:         clip.Name,
				Filename:     clip.Filename,
				Status:       "skipped_existing_drive_link",
				DriveLink:    clip.DriveLink,
				DownloadLink: clip.DownloadLink,
				FileHash:     clip.FileHash,
				LocalPath:    clip.LocalPath,
			})
			continue
		}

		url := strings.TrimSpace(clip.ExternalURL)
		if url == "" {
			url = strings.TrimSpace(clip.DownloadLink)
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

		rawPath := filepath.Join(os.TempDir(), fmt.Sprintf("raw_%s.mp4", clip.ID))
		processedPath := filepath.Join(os.TempDir(), fmt.Sprintf("proc_%s.mp4", clip.ID))

		if err := downloadClip(url, rawPath); err != nil {
			s.log.Error("artlist download failed", zap.String("clip_id", clip.ID), zap.Error(err))
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

		if err := processVideo(rawPath, processedPath); err != nil {
			s.log.Error("artlist processing failed", zap.String("clip_id", clip.ID), zap.Error(err))
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

		fileHash, err := calculateFileHash(processedPath)
		if err != nil {
			s.log.Error("artlist hash failed", zap.String("clip_id", clip.ID), zap.Error(err))
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

		f, err := os.Open(processedPath)
		if err != nil {
			_ = os.Remove(rawPath)
			_ = os.Remove(processedPath)
			resp.Failed++
			resp.Items = append(resp.Items, RunTagItem{
				ClipID:   clip.ID,
				Name:     clip.Name,
				Filename: clip.Filename,
				Status:   "open_failed",
				Error:    err.Error(),
			})
			continue
		}

		driveFileReq := &drive.File{Name: fmt.Sprintf("%s_7s.mp4", clip.Name)}
		if tagFolderID != "" {
			driveFileReq.Parents = []string{tagFolderID}
		}
		driveFile, err := s.driveClient.Files.Create(driveFileReq).Fields("id,webViewLink,md5Checksum").Media(f).Do()
		_ = f.Close()
		_ = os.Remove(rawPath)
		_ = os.Remove(processedPath)
		if err != nil {
			s.log.Error("artlist upload failed", zap.String("clip_id", clip.ID), zap.Error(err))
			resp.Failed++
			resp.Items = append(resp.Items, RunTagItem{
				ClipID:   clip.ID,
				Name:     clip.Name,
				Filename: clip.Filename,
				Status:   "upload_failed",
				Error:    err.Error(),
			})
			continue
		}

		clip.DriveLink = driveFile.WebViewLink
		clip.DownloadLink = "https://drive.google.com/uc?id=" + driveFile.Id
		clip.FileHash = fileHash
		clip.Metadata = composeArtlistMetadata(clip.Metadata, fileHash, driveFile.Md5Checksum)
		clip.UpdatedAt = time.Now().UTC()
		clip.LocalPath = ""
		if err := s.clipsRepo.UpsertClip(ctx, clip); err != nil {
			s.log.Error("artlist db update failed", zap.String("clip_id", clip.ID), zap.Error(err))
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

		s.log.Info("artlist pipeline success",
			zap.String("clip_id", clip.ID),
			zap.String("name", clip.Name),
			zap.String("drive_link", clip.DriveLink),
			zap.String("download_link", clip.DownloadLink),
			zap.String("file_hash", clip.FileHash),
			zap.String("drive_md5_checksum", driveFile.Md5Checksum),
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

func downloadClip(sourceURL, rawPath string) error {
	cmdDl := exec.Command("yt-dlp", "-o", rawPath, sourceURL)
	if out, err := cmdDl.CombinedOutput(); err != nil {
		return fmt.Errorf("yt-dlp failed: %v (output: %s)", err, string(out))
	}
	return nil
}

func processVideo(input, output string) error {
	args := []string{
		"-y",
		"-t", "7",
		"-i", input,
		"-vf", "scale=1920:1080:force_original_aspect_ratio=increase,crop=1920:1080,fps=30",
		"-an",
		"-c:v", "libx264",
		"-preset", "fast",
		"-crf", "23",
		output,
	}

	cmd := exec.Command("ffmpeg", args...)
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
