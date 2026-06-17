package artlist

import (
	"context"
	"strings"

	"velox/go-master/internal/repository/clips"
)

// DBProvider searches the local database for indexed clips.
type DBProvider struct {
	repo *clips.Repository
}

// NewDBProvider creates a new DBProvider.
func NewDBProvider(repo *clips.Repository) *DBProvider {
	return &DBProvider{repo: repo}
}

func (p *DBProvider) Name() string { return "database" }

func (p *DBProvider) Search(ctx context.Context, term string, limit int) ([]ScraperClip, error) {
	if p.repo == nil {
		return nil, nil
	}
	keywords := strings.Fields(term)
	dbClips, err := p.repo.SearchByTerms(ctx, "artlist", keywords, limit)
	if err != nil {
		return nil, err
	}
	if len(dbClips) == 0 {
		return nil, nil
	}

	results := make([]ScraperClip, 0, len(dbClips))
	for _, clip := range dbClips {
		results = append(results, ScraperClip{
			ClipID:      clip.ID,
			ID:          clip.ID,
			Title:       clip.Name,
			Name:        clip.Name,
			PrimaryURL:  clip.ExternalURL,
			ClipPageURL: clip.GetMetadataString("external_url"),
		})
	}
	return results, nil
}
