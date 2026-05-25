package youtube

import (
	"context"
	"fmt"

	"velox/go-master/internal/media/models"
)

// GetFolder returns a clip folder by ID
func (s *Service) GetFolder(ctx context.Context, folderID string) (*models.ClipFolder, error) {
	if s.clipsRepo == nil {
		return nil, fmt.Errorf("clips repository not available")
	}
	return s.clipsRepo.GetClipFolder(ctx, folderID)
}

// GetFolderByVideoID returns a clip folder by video ID
func (s *Service) GetFolderByVideoID(ctx context.Context, videoID string) (*models.ClipFolder, error) {
	if s.clipsRepo == nil {
		return nil, fmt.Errorf("clips repository not available")
	}
	return s.clipsRepo.GetClipFolderByVideoID(ctx, videoID)
}

// ListFolders returns all clip folders
func (s *Service) ListFolders(ctx context.Context, source string) ([]*models.ClipFolder, error) {
	if s.clipsRepo == nil {
		return nil, fmt.Errorf("clips repository not available")
	}
	return s.clipsRepo.ListClipFolders(ctx, source)
}

// SearchFolders searches clip folders by keyword
func (s *Service) SearchFolders(ctx context.Context, keyword string) ([]*models.ClipFolder, error) {
	if s.clipsRepo == nil {
		return nil, fmt.Errorf("clips repository not available")
	}
	return s.clipsRepo.SearchClipFolders(ctx, keyword)
}

// ListFolderClips returns all clips in a folder by folder ID
func (s *Service) ListFolderClips(ctx context.Context, folderID string) ([]*models.MediaAsset, error) {
	if s.clipsRepo == nil {
		return nil, fmt.Errorf("clips repository not available")
	}
	return s.clipsRepo.ListClipsByFolderID(ctx, folderID)
}
