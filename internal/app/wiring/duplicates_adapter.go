package wiring

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/duplicates"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets"
)

// clipsRepoDuplicateSource adapts *assets.ClipsRepository to the
// duplicates.Source port. It is the canonical composition-root bridge
// between the SQLite clip store and the duplicate-detection capability.
type clipsRepoDuplicateSource struct {
	name string
	repo *assets.ClipsRepository
}

// NewClipsRepoDuplicateSource returns a duplicates.Source backed by the
// given clips repository. name is the canonical source identity reported
// to callers (e.g. "local").
func NewClipsRepoDuplicateSource(name string, repo *assets.ClipsRepository) duplicates.Source {
	return &clipsRepoDuplicateSource{name: name, repo: repo}
}

// Name returns the source identity.
func (s *clipsRepoDuplicateSource) Name() string {
	return s.name
}

// FindByHash delegates to the repository and maps domain assets to the
// canonical duplicates.DuplicateMatch shape.
func (s *clipsRepoDuplicateSource) FindByHash(ctx context.Context, hash string) ([]duplicates.DuplicateMatch, error) {
	if s == nil || s.repo == nil {
		return nil, nil
	}
	clips, err := s.repo.FindClipsByHash(ctx, hash)
	if err != nil {
		return nil, err
	}
	out := make([]duplicates.DuplicateMatch, 0, len(clips))
	for _, c := range clips {
		out = append(out, duplicates.DuplicateMatch{
			AssetID:      c.ID,
			Source:       string(c.Source),
			Name:         c.Name,
			ThumbnailURL: c.ThumbnailURL,
			LocalPath:    c.LocalPath(),
			DriveLink:    c.DriveLink(),
			Hash:         hash,
		})
	}
	return out, nil
}
