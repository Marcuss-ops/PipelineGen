package assetregistry

import (
	"context"

	"go.uber.org/zap"
	"velox/go-master/internal/repository/clips"
	"velox/go-master/pkg/models"
)

// clipsAdapter adapts a *clips.Repository to the ClipRegistry interface
type clipsAdapter struct {
	repo *clips.Repository
	log  *zap.Logger
}

// NewClipsAdapter creates a new adapter for *clips.Repository
func NewClipsAdapter(repo *clips.Repository, log *zap.Logger) ClipRegistry {
	return &clipsAdapter{
		repo: repo,
		log:  log.Named("clips_adapter"),
	}
}

// SearchClips searches clips in the repository
func (a *clipsAdapter) SearchClips(ctx context.Context, term string) ([]*models.Clip, error) {
	return a.repo.SearchClips(ctx, term)
}

// GetClip retrieves a clip by ID
func (a *clipsAdapter) GetClip(ctx context.Context, id string) (*models.Clip, error) {
	return a.repo.GetClip(ctx, id)
}

// UpsertClip inserts or updates a clip
func (a *clipsAdapter) UpsertClip(ctx context.Context, clip *models.Clip) error {
	return a.repo.UpsertClip(ctx, clip)
}

// CountClips returns the total number of clips
func (a *clipsAdapter) CountClips(ctx context.Context) (int, error) {
	return a.repo.CountClips(ctx)
}

// LastUpdatedAtForTerm returns the last update time for a search term
func (a *clipsAdapter) LastUpdatedAtForTerm(ctx context.Context, term string) (*string, error) {
	return a.repo.LastUpdatedAtForTerm(ctx, term)
}
