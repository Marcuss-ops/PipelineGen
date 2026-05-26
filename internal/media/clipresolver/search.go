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

// qdrantResultToCandidate converts a vector store SearchResult to ClipCandidate.
func qdrantResultToCandidate(r SearchResult) clipcatalog.ClipCandidate {
	return clipcatalog.ClipCandidate{
		ID:        r.AssetID,
		Name:      r.Name,
		DriveLink: r.DriveLink,
		LocalPath: r.LocalPath,
		Category:  r.Category,
	}
}

// float64To32 converts a float64 slice to float32.
func float64To32(in []float64) []float32 {
	out := make([]float32, len(in))
	for i, v := range in {
		out[i] = float32(v)
	}
	return out
}
