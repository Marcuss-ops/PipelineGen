package clipresolver

import (
	"context"
	"velox/go-master/internal/media/clipcatalog"
	"velox/go-master/internal/media/models"
)

// SearchClips searches for clips using the catalog repository
func SearchClips(ctx context.Context, catalogRepo *clipcatalog.Repository, query string, limit int) ([]*models.MediaAsset, error) {
	candidates, err := catalogRepo.FindCandidates(ctx, query, limit)
	if err != nil {
		return nil, err
	}

	clips := make([]*models.MediaAsset, 0, len(candidates))
	for _, c := range candidates {
		clips = append(clips, candidateToClip(c))
	}
	return clips, nil
}

func candidateToClip(c clipcatalog.ClipCandidate) *models.MediaAsset {
	return &models.MediaAsset{
		ID:          c.ID,
		Name:        c.Name,
		DriveLink:   c.DriveLink,
		LocalPath:   c.LocalPath,
		Category:    c.Category,
		SearchTerms: []string{c.SearchText},
		Tags:        c.Tags,
	}
}
