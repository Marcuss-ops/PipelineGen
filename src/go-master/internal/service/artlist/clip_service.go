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

	"google.golang.org/api/drive/v3"
	"velox/go-master/pkg/models"
)

func (s *Service) GetClipStatus(ctx context.Context, clipID string) (*ClipStatusResponse, error) {
	clip, err := s.clipsRepo.GetClip(ctx, clipID)
	if err != nil {
		return nil, err
	}

	return &ClipStatusResponse{
		ClipID:       clip.ID,
		Name:         clip.Name,
		HasLocalFile: false,
		HasDriveLink: clip.DriveLink != "",
		DriveLink:    clip.DriveLink,
		FileHash:     clip.FileHash,
		Source:       clip.Source,
		ExternalURL:  clip.ExternalURL,
	}, nil
}

func (s *Service) DownloadClip(ctx context.Context, clipID string, req *DownloadClipRequest) (*DownloadClipResponse, error) {
	clip, err := s.clipsRepo.GetClip(ctx, clipID)
	if err != nil {
		return nil, err
	}

	outputDir := req.OutputDir
	if outputDir == "" {
		outputDir = s.nodeScraperDir
	}

	localPath := filepath.Join(outputDir, clip.Filename)
	resp := &DownloadClipResponse{OK: true, ClipID: clipID, LocalPath: localPath}

	if clip.DriveLink != "" && s.driveClient != nil {
		file, err := s.driveClient.Files.Get(clip.DriveLink).Context(ctx).Download()
		if err != nil {
			resp.Error = err.Error()
			return resp, err
		}
		defer file.Body.Close()

		out, err := os.Create(localPath)
		if err != nil {
			return nil, err
		}
		defer out.Close()

		if _, err = io.Copy(out, file.Body); err != nil {
			return nil, err
		}

		if hash, err := calculateFileHash(localPath); err == nil {
			resp.FileHash = hash
		}
	}

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

	file := &drive.File{Name: clip.Filename}
	if folderID != "" {
		file.Parents = []string{folderID}
	}

	created, err := s.driveClient.Files.Create(file).Context(ctx).Do()
	if err != nil {
		resp.Error = err.Error()
		return resp, err
	}

	if created != nil {
		resp.DriveLink = created.WebViewLink
		resp.DownloadLink = "https://drive.google.com/uc?id=" + created.Id
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

	if s.driveClient == nil {
		resp.Error = "drive client not configured"
		return resp, fmt.Errorf("drive client not configured")
	}

	query := fmt.Sprintf("'%s' in parents and trashed=false", folderID)
	fileList, err := s.driveClient.Files.List().Q(query).Fields("files(id, name)").Context(ctx).Do()
	if err != nil {
		resp.Error = err.Error()
		return resp, err
	}

	resp.Requested = len(fileList.Files)
	for _, file := range fileList.Files {
		if _, err := s.clipsRepo.GetClipByFolderAndFilename(ctx, folderID, file.Name); err != nil {
			resp.Synced++
		}
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
