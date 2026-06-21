package artlist

import (
	"context"
	"strings"
)

// DBProvider searches the local database for indexed clips.
//
// PR2.5: holds the canonical AssetStore port instead of the concrete
// *assets.ClipsRepository. The port declares SearchByTerms with the
// same signature so the Search() body is unchanged.
type DBProvider struct {
	store AssetStore
}

// NewDBProvider creates a new DBProvider backed by the canonical
// AssetStore port. Production wiring passes bundle.ClipsRepo which
// satisfies AssetStore automatically (its SearchByTerms / SearchClips
// / CountClips / LastUpdatedAtForTerm / Get / Upsert / UpsertClip /
// UpdateSearchTerms surface fully covers the port).
func NewDBProvider(store AssetStore) *DBProvider {
	return &DBProvider{store: store}
}

func (p *DBProvider) Name() string { return "database" }

func (p *DBProvider) Search(ctx context.Context, term string, limit int) ([]ScraperClip, error) {
	if p.store == nil {
		return nil, nil
	}
	keywords := strings.Fields(term)
	dbClips, err := p.store.SearchByTerms(ctx, "artlist", keywords, limit)
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
			PrimaryURL:  clip.ExternalURL(),
			ClipPageURL: clip.GetMetadataString("external_url"),
		})
	}
	return results, nil
}
