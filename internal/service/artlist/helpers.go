package artlist

import (
	"context"

	"go.uber.org/zap"

	"velox/go-master/pkg/models"
)

// GetClipStatus returns the status of a clip
func (s *Service) GetClipStatus(ctx context.Context, clipID string) (*ClipStatusResponse, error) {
	clip, err := s.artlistRepo.GetClip(ctx, clipID)
	if err != nil {
		return nil, err
	}

	resp := &ClipStatusResponse{
		ClipID:       clipID,
		Name:         clip.Name,
		HasLocalFile: clip.LocalPath != "",
		LocalPath:    clip.LocalPath,
		DriveLink:    clip.DriveLink,
		HasDriveLink: clip.DriveLink != "" || clip.DownloadLink != "",
		FileHash:     clip.FileHash,
		Source:       clip.Source,
		ExternalURL:  clip.ExternalURL,
	}

	return resp, nil
}

// SearchClips searches clips in the database
func (s *Service) SearchClips(ctx context.Context, term string) []*models.Clip {
	clips, err := s.artlistRepo.SearchClips(ctx, term)
	if err != nil {
		s.log.Error("failed to search clips", zap.Error(err), zap.String("term", term))
		return nil
	}
	return clips
}

// UpsertClip inserts or updates a clip in the database
func (s *Service) UpsertClip(ctx context.Context, clip *models.Clip) error {
	return s.artlistRepo.UpsertClip(ctx, clip)
}
