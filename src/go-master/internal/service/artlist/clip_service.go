package artlist

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.uber.org/zap"
	driveapi "google.golang.org/api/drive/v3"
	"velox/go-master/pkg/hashutil"
	"velox/go-master/pkg/media/downloader"
	"velox/go-master/pkg/models"
	"velox/go-master/pkg/pathutil"
)

func (s *Service) GetClipStatus(ctx context.Context, clipID string) (*ClipStatusResponse, error) {
	clip, err := s.clipsRepo.GetClip(ctx, clipID)
	if err != nil {
		return nil, err
	}

	resp := &ClipStatusResponse{
		ClipID:      clip.ID,
		Name:        clip.Name,
		HasDriveLink: clip.DriveLink != "",
		DriveLink:   clip.DriveLink,
		FileHash:    clip.FileHash,
		Source:      clip.Source,
		ExternalURL: clip.ExternalURL,
	}

	// Check if local file exists
	localPath := strings.TrimSpace(clip.LocalPath)
	if localPath == "" {
		// Try to construct local path from clip metadata
		saveDir := filepath.Join(s.cfg.Storage.DataDir, "artlist", pathutil.SafeFolderName(clip.Name))
		safeName := pathutil.SafeFolderName(clip.Name)
		localPath = filepath.Join(saveDir, fmt.Sprintf("%s_%ds_%s.mp4", safeName, s.cfg.Video.Duration, clip.ID))
	}

	if localPath != "" {
		if _, err := os.Stat(localPath); err == nil {
			resp.HasLocalFile = true
			resp.LocalPath = localPath
		} else {
			resp.HasLocalFile = false
			resp.LocalPath = localPath
		}
	}

	return resp, nil
}

func (s *Service) DownloadClip(ctx context.Context, clipID string, req *DownloadClipRequest) (*DownloadClipResponse, error) {
	clip, err := s.clipsRepo.GetClip(ctx, clipID)
	if err != nil {
		return nil, err
	}

	outputDir := req.OutputDir
	if outputDir == "" {
		outputDir = filepath.Join(s.cfg.Storage.DataDir, "artlist", pathutil.SafeFolderName(clip.Name))
		if err := os.MkdirAll(outputDir, 0755); err != nil {
			outputDir = s.nodeScraperDir
		}
	}

	localPath := filepath.Join(outputDir, fmt.Sprintf("%s_%s.mp4", pathutil.SafeFolderName(clip.Name), clip.ID))
	resp := &DownloadClipResponse{OK: true, ClipID: clipID, LocalPath: localPath}

	// Try downloading from Drive if DriveLink exists
	if clip.DriveLink != "" && s.driveClient != nil {
		fileID := driveFileIDFromClip(clip)
		if fileID != "" {
			file, err := s.driveClient.Files.Get(fileID).Context(ctx).Download()
			if err == nil {
				defer file.Body.Close()

				out, err := os.Create(localPath)
				if err != nil {
					return nil, err
				}
				defer out.Close()

				if _, err = io.Copy(out, file.Body); err != nil {
					return nil, err
				}

			if hash, err := hashutil.MD5File(localPath); err == nil {
				resp.FileHash = hash
			}

				// Update clip LocalPath
				clip.LocalPath = localPath
				clip.UpdatedAt = time.Now().UTC()
				_ = s.clipsRepo.UpsertClip(ctx, clip)

				return resp, nil
			}
			s.log.Warn("failed to download from drive, trying artlist source", zap.String("clip_id", clipID), zap.Error(err))
		}
	}

	// Fallback: download from Artlist source URL
	url := strings.TrimSpace(clip.ExternalURL)
	if url == "" {
		url = strings.TrimSpace(clip.DownloadLink)
	}
	if url == "" {
		resp.Error = "no download source available"
		return resp, fmt.Errorf("no download source available")
	}

	dl := downloader.NewYTDLP(s.cfg)
	if err := dl.Download(ctx, &downloader.DownloadRequest{URL: url, OutputPath: localPath}); err != nil {
		resp.Error = err.Error()
		return resp, err
	}

	actualPath := resolveDownloadedFile(localPath)
	if actualPath != localPath {
		s.log.Info("resolved actual download path", zap.String("expected", localPath), zap.String("actual", actualPath))
		localPath = actualPath
	}

	if hash, err := hashutil.MD5File(localPath); err == nil {
		resp.FileHash = hash
	}

	// Update clip LocalPath
	clip.LocalPath = localPath
	clip.UpdatedAt = time.Now().UTC()
	_ = s.clipsRepo.UpsertClip(ctx, clip)

	return resp, nil
}

func (s *Service) UploadClipToDrive(ctx context.Context, clipID string, req *UploadClipToDriveRequest) (*UploadClipToDriveResponse, error) {
	clip, err := s.clipsRepo.GetClip(ctx, clipID)
	if err != nil {
		return nil, err
	}

	resp := &UploadClipToDriveResponse{OK: true, ClipID: clipID}

	if s.driveClient == nil {
		resp.Error = "drive client not configured"
		return resp, fmt.Errorf("drive client not configured")
	}

	folderID := req.FolderID
	if folderID == "" {
		folderID = s.driveFolderID
	}

	// Determine file path: use LocalPath if available, otherwise construct from name
	localPath := strings.TrimSpace(clip.LocalPath)
	if localPath == "" {
		saveDir := filepath.Join(s.cfg.Storage.DataDir, "artlist", pathutil.SafeFolderName(clip.Name))
		safeName := pathutil.SafeFolderName(clip.Name)
		localPath = filepath.Join(saveDir, fmt.Sprintf("%s_%ds_%s.mp4", safeName, s.cfg.Video.Duration, clip.ID))
	}

	// Open the local file for upload
	f, err := os.Open(localPath)
	if err != nil {
		resp.Error = fmt.Sprintf("failed to open local file: %v", err)
		s.log.Error("failed to open file for drive upload", zap.String("clip_id", clipID), zap.String("path", localPath), zap.Error(err))
		return resp, err
	}
	defer f.Close()

	file := &driveapi.File{Name: clip.Filename}
	if folderID != "" {
		file.Parents = []string{folderID}
	}

	created, err := s.driveClient.Files.Create(file).Context(ctx).Media(f).Fields("id,webViewLink,md5Checksum").Do()
	if err != nil {
		resp.Error = err.Error()
		return resp, err
	}

	if created != nil {
		resp.DriveLink = created.WebViewLink
		resp.DownloadLink = "https://drive.google.com/uc?id=" + created.Id

		// Update clip with drive info
		clip.DriveLink = created.WebViewLink
		clip.DownloadLink = resp.DownloadLink
		if created.Md5Checksum != "" {
			clip.FileHash = created.Md5Checksum
		}
		clip.UpdatedAt = time.Now().UTC()
		if err := s.clipsRepo.UpsertClip(ctx, clip); err != nil {
			s.log.Warn("failed to update clip after drive upload", zap.String("clip_id", clipID), zap.Error(err))
		}
	}

	return resp, nil
}

func (s *Service) ProcessClip(ctx context.Context, req *ProcessClipRequest) (*ProcessClipResponse, error) {
	resp := &ProcessClipResponse{OK: true, ClipID: req.ClipID, Status: "processed"}

	clip, err := s.clipsRepo.GetClip(ctx, req.ClipID)
	if err != nil {
		resp.Status = "clip_not_found"
		resp.Error = err.Error()
		return resp, err
	}
	updated := false

	if req.AutoDownload {
		downloadResp, err := s.DownloadClip(ctx, req.ClipID, &DownloadClipRequest{})
		if err != nil {
			resp.Status = "download_failed"
			resp.Error = err.Error()
			return resp, err
		}
		if downloadResp != nil {
			if downloadResp.FileHash != "" {
				clip.FileHash = downloadResp.FileHash
			}
			updated = true
		}
	}

	if req.AutoUpload {
		uploadResp, err := s.UploadClipToDrive(ctx, req.ClipID, &UploadClipToDriveRequest{})
		if err != nil {
			resp.Status = "upload_failed"
			resp.Error = err.Error()
			return resp, err
		}
		if uploadResp != nil {
			clip.DriveLink = uploadResp.DriveLink
			clip.DownloadLink = uploadResp.DownloadLink
			updated = true
		}
	}

	if updated {
		clip.UpdatedAt = time.Now().UTC()
		clip.LocalPath = ""
		if err := s.clipsRepo.UpsertClip(ctx, clip); err != nil {
			resp.Status = "db_update_failed"
			resp.Error = err.Error()
			return resp, err
		}
	}

	return resp, nil
}

func (s *Service) SyncDriveFolder(ctx context.Context, folderID, mediaType string) (*SyncResponse, error) {
	resp := &SyncResponse{OK: true}
	if strings.TrimSpace(mediaType) == "" {
		mediaType = "clip"
	}

	if s.driveClient == nil {
		resp.Error = "drive client not configured"
		return resp, fmt.Errorf("drive client not configured")
	}

	folderID = strings.TrimSpace(folderID)
	if folderID == "" {
		resp.Error = "folder_id is required"
		return resp, fmt.Errorf("folder_id is required")
	}

	folderMeta, err := s.driveClient.Files.Get(folderID).Fields("id, name, webViewLink").Context(ctx).Do()
	if err != nil {
		resp.Error = err.Error()
		return resp, err
	}

	now := time.Now().UTC()
	folderName := folderID
	folderLink := ""
	if folderMeta != nil {
		if strings.TrimSpace(folderMeta.Name) != "" {
			folderName = folderMeta.Name
		}
		folderLink = strings.TrimSpace(folderMeta.WebViewLink)
	}
	if folderLink == "" {
		folderLink = "https://drive.google.com/drive/folders/" + folderID
	}

	folderClip := &models.Clip{
		ID:           folderID,
		Name:         folderName,
		Filename:     folderName,
		FolderID:     folderID,
		FolderPath:   folderName,
		Group:        "Clips",
		MediaType:    mediaType,
		DriveLink:    folderLink,
		DownloadLink: folderLink,
		Source:       "drive",
		Category:     "folder",
		ExternalURL:  folderLink,
		Tags:         []string{},
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := s.clipsRepo.UpsertClip(ctx, folderClip); err == nil {
		resp.Synced++
	} else {
		resp.Failed++
	}

	query := fmt.Sprintf("'%s' in parents and trashed=false", folderID)
	fileList, err := s.driveClient.Files.List().Q(query).Fields("files(id, name, mimeType, webViewLink, webContentLink)").Context(ctx).Do()
	if err != nil {
		resp.Error = err.Error()
		return resp, err
	}

	resp.Requested = len(fileList.Files)
	for _, file := range fileList.Files {
		if file == nil {
			continue
		}

		clipLink := strings.TrimSpace(file.WebViewLink)
		if clipLink == "" {
			clipLink = strings.TrimSpace(file.WebContentLink)
		}
		if clipLink == "" {
			clipLink = "https://drive.google.com/file/d/" + file.Id
		}

		clip := &models.Clip{
			ID:           file.Id,
			Name:         file.Name,
			Filename:     file.Name,
			FolderID:     folderID,
			FolderPath:   folderName,
			Group:        "Clips",
			MediaType:    mediaType,
			DriveLink:    clipLink,
			DownloadLink: clipLink,
			Source:       "drive",
			Category:     "file",
			ExternalURL:  clipLink,
			Tags:         []string{},
			CreatedAt:    now,
			UpdatedAt:    now,
		}

		if err := s.clipsRepo.UpsertClip(ctx, clip); err != nil {
			resp.Failed++
			continue
		}
		resp.Synced++
	}

	return resp, nil
}

func mapToModelClip(data map[string]interface{}, term string) *models.Clip {
	clipID, _ := data["clip_id"].(string)
	title, _ := data["title"].(string)
	primaryURL, _ := data["primary_url"].(string)
	clipPageURL, _ := data["clip_page_url"].(string)

	if clipID == "" {
		return nil
	}

	tags := []string{}
	if term != "" {
		tags = append(tags, term)
	}

	return &models.Clip{
		ID:           clipID,
		Name:         title,
		ExternalURL:  primaryURL,
		DownloadLink: clipPageURL,
		Source:       "artlist",
		Category:     "live",
		Tags:         tags,
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}
}

func (s *Service) ImportScraperDB(ctx context.Context, dbPath string) (int, error) {
	if dbPath == "" {
		dbPath = filepath.Join(s.nodeScraperDir, "artlist_videos.db")
	}

	artlistDB, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return 0, fmt.Errorf("failed to open scraper db: %w", err)
	}
	defer artlistDB.Close()

	rows, err := artlistDB.QueryContext(ctx, `
		SELECT v.video_id, v.file_name, v.url, s.term
		FROM video_links v
		JOIN search_terms s ON v.search_term_id = s.id
	`)
	if err != nil {
		return 0, fmt.Errorf("failed to query scraper db: %w", err)
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		var vid, name, url, term sql.NullString
		if err := rows.Scan(&vid, &name, &url, &term); err != nil {
			continue
		}

		tags := []string{}
		if term.Valid && term.String != "" {
			tags = []string{term.String}
		}

		clip := &models.Clip{
			ID:           vid.String,
			Name:         name.String,
			ExternalURL:  url.String,
			DownloadLink: url.String,
			Source:       "artlist",
			Category:     "dynamic",
			Tags:         tags,
			CreatedAt:    time.Now(),
			UpdatedAt:    time.Now(),
		}

		if existing, err := s.clipsRepo.GetClip(ctx, clip.ID); err == nil && existing != nil {
			clip = preserveExistingClipFields(clip, existing)
		}

		if err := s.clipsRepo.UpsertClip(ctx, clip); err == nil {
			count++
		}
	}

	return count, nil
}

func preserveExistingClipFields(incoming, existing *models.Clip) *models.Clip {
	if incoming == nil {
		return existing
	}
	if existing == nil {
		return incoming
	}

	if strings.TrimSpace(existing.FolderID) != "" && strings.TrimSpace(incoming.FolderID) == "" {
		incoming.FolderID = existing.FolderID
	}
	if strings.TrimSpace(existing.FolderPath) != "" && strings.TrimSpace(incoming.FolderPath) == "" {
		incoming.FolderPath = existing.FolderPath
	}
	if strings.TrimSpace(existing.Group) != "" && strings.TrimSpace(incoming.Group) == "" {
		incoming.Group = existing.Group
	}
	if strings.TrimSpace(existing.MediaType) != "" && strings.TrimSpace(incoming.MediaType) == "" {
		incoming.MediaType = existing.MediaType
	}
	if strings.TrimSpace(existing.DriveLink) != "" {
		incoming.DriveLink = existing.DriveLink
	}
	if strings.TrimSpace(existing.DownloadLink) != "" {
		incoming.DownloadLink = existing.DownloadLink
	}
	if strings.TrimSpace(existing.FileHash) != "" {
		incoming.FileHash = existing.FileHash
	}
	if strings.TrimSpace(existing.LocalPath) != "" {
		incoming.LocalPath = existing.LocalPath
	}
	if strings.TrimSpace(existing.Metadata) != "" {
		incoming.Metadata = existing.Metadata
	}
	if len(existing.Tags) > 0 {
		merged := append([]string{}, incoming.Tags...)
		merged = append(merged, existing.Tags...)
		incoming.Tags = dedupeStrings(merged)
	}
	if !existing.CreatedAt.IsZero() {
		incoming.CreatedAt = existing.CreatedAt
	}
	return incoming
}

func dedupeStrings(input []string) []string {
	if len(input) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(input))
	out := make([]string, 0, len(input))
	for _, item := range input {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		key := strings.ToLower(item)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, item)
	}
	return out
}

// resolveDownloadedFile attempts to find the actual downloaded file.
// Handles cases where yt-dlp saves with different extensions (e.g. m3u8 streams).
func resolveDownloadedFile(expectedPath string) string {
	if _, err := os.Stat(expectedPath); err == nil {
		return expectedPath
	}

	pattern := strings.TrimSuffix(expectedPath, filepath.Ext(expectedPath)) + "*"
	matches, err := filepath.Glob(pattern)
	if err == nil && len(matches) > 0 {
		for _, m := range matches {
			if info, err := os.Stat(m); err == nil && !info.IsDir() {
				return m
			}
		}
	}

	return expectedPath
}
