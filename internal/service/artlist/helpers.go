package artlist

import (
	"context"

	"go.uber.org/zap"

	"velox/go-master/pkg/models"
)

// ClipStatusService gestisce le operazioni di stato delle clip.
type ClipStatusService struct {
	service *Service
}

// NewClipStatusService crea una nuova istanza di ClipStatusService.
func NewClipStatusService(s *Service) *ClipStatusService {
	return &ClipStatusService{service: s}
}

// GetClipStatus returns the status of a clip
func (cs *ClipStatusService) GetClipStatus(ctx context.Context, clipID string) (*ClipStatusResponse, error) {
	s := cs.service
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
func (ss *SearchService) SearchClips(ctx context.Context, term string) []*models.Clip {
	s := ss.service
	clips, err := s.artlistRepo.SearchClips(ctx, term)
	if err != nil {
		s.log.Error("failed to search clips", zap.Error(err), zap.String("term", term))
		return nil
	}
	return clips
}

// UpsertClip inserts or updates a clip in the database
func (ss *SearchService) UpsertClip(ctx context.Context, clip *models.Clip) error {
	s := ss.service
	return s.artlistRepo.UpsertClip(ctx, clip)
}
