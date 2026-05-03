package artlist

import (
	"context"
	"time"

	"go.uber.org/zap"

	"velox/go-master/pkg/models"
)

// mapToModelClip converts a map from the Artlist API to a Clip model
func mapToModelClip(data map[string]interface{}, term string) *models.Clip {
	clip := &models.Clip{
		Source:    "artlist",
		Group:     term,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	if id, ok := data["clip_id"].(string); ok && id != "" {
		clip.ID = id
	} else if id, ok := data["id"].(string); ok && id != "" {
		clip.ID = id
	}

	if title, ok := data["title"].(string); ok {
		clip.Name = title
		clip.Filename = title
	}

	if url, ok := data["clip_page_url"].(string); ok {
		clip.ExternalURL = url
	} else if url, ok := data["primary_url"].(string); ok {
		clip.ExternalURL = url
	}

	if duration, ok := data["duration"]; ok {
		switch v := duration.(type) {
		case float64:
			clip.Duration = int(v)
		case int:
			clip.Duration = v
		}
	}

	if mediaType, ok := data["media_type"].(string); ok {
		clip.MediaType = mediaType
	}

	if category, ok := data["category"].(string); ok {
		clip.Category = category
	}

	if tags, ok := data["tags"]; ok {
		switch t := tags.(type) {
		case []string:
			clip.Tags = t
		case []interface{}:
			for _, tag := range t {
				if tagStr, ok := tag.(string); ok {
					clip.Tags = append(clip.Tags, tagStr)
				}
			}
		}
	}

	if clip.ID == "" {
		return nil
	}

	return clip
}

// preserveExistingClipFields preserves fields from existing clip that shouldn't be overwritten
func preserveExistingClipFields(newClip, existing *models.Clip) *models.Clip {
	if existing.LocalPath != "" {
		newClip.LocalPath = existing.LocalPath
	}
	if existing.FileHash != "" {
		newClip.FileHash = existing.FileHash
	}
	if existing.DriveLink != "" {
		newClip.DriveLink = existing.DriveLink
	}
	if existing.FolderID != "" {
		newClip.FolderID = existing.FolderID
	}
	if existing.FolderPath != "" {
		newClip.FolderPath = existing.FolderPath
	}
	if existing.DownloadLink != "" {
		newClip.DownloadLink = existing.DownloadLink
	}
	return newClip
}

// GetClipStatus returns the status of a clip
func (s *Service) GetClipStatus(ctx context.Context, clipID string) (*ClipStatusResponse, error) {
	clip, err := s.clipsRepo.GetClip(ctx, clipID)
	if err != nil {
		return nil, err
	}

	resp := &ClipStatusResponse{
		ClipID:       clip.ID,
		Name:         clip.Name,
		HasLocalFile: clip.LocalPath != "",
		LocalPath:    clip.LocalPath,
		DriveLink:    clip.DriveLink,
		HasDriveLink: clip.DriveLink != "",
		FileHash:     clip.FileHash,
		Source:       clip.Source,
		ExternalURL:  clip.ExternalURL,
	}

	return resp, nil
}

// DownloadClip downloads a clip from Artlist
func (s *Service) DownloadClip(ctx context.Context, clipID string, req *DownloadClipRequest) (*DownloadClipResponse, error) {
	clip, err := s.clipsRepo.GetClip(ctx, clipID)
	if err != nil {
		return nil, err
	}

	resp := &DownloadClipResponse{
		OK:     false,
		ClipID: clipID,
	}

	if clip.DownloadLink == "" {
		resp.Error = "no download link available"
		return resp, nil
	}

	s.log.Info("DownloadClip called - not fully implemented", zap.String("clip_id", clipID))
	resp.Error = "DownloadClip not fully implemented"

	return resp, nil
}

// ProcessClip processes a clip: search → download → upload to Drive
func (s *Service) ProcessClip(ctx context.Context, req *ProcessClipRequest) (*ProcessClipResponse, error) {
	resp := &ProcessClipResponse{
		OK:     false,
		ClipID: req.ClipID,
		Status: "pending",
	}

	s.log.Info("ProcessClip called - not fully implemented", zap.String("clip_id", req.ClipID))
	resp.Error = "ProcessClip not fully implemented"

	return resp, nil
}

// ImportScraperDB imports data from a scraper database
func (s *Service) ImportScraperDB(ctx context.Context, dbPath string) (int, error) {
	s.log.Info("ImportScraperDB called - not fully implemented", zap.String("db_path", dbPath))
	return 0, nil
}

// SyncDriveFolder syncs a Google Drive folder to the database
func (s *Service) SyncDriveFolder(ctx context.Context, folderID, mediaType string) (interface{}, error) {
	s.log.Info("SyncDriveFolder called - not fully implemented", zap.String("folder_id", folderID))
	return nil, nil
}
