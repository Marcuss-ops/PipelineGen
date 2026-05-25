package catalog

import (
	"context"
	"errors"

	"velox/go-master/internal/repository/clips"
)

type Repository struct {
	clipsRepo   *clips.Repository
	stockRepo   *clips.Repository
	artlistRepo *clips.Repository
}

func NewRepository(clipsRepo *clips.Repository, stockRepo *clips.Repository, artlistRepo *clips.Repository) *Repository {
	return &Repository{
		clipsRepo:   clipsRepo,
		stockRepo:   stockRepo,
		artlistRepo: artlistRepo,
	}
}

// SearchAll searches across all catalog databases and returns aggregated results.
// It collects errors and returns them joined if no results are found across any database.
func (r *Repository) SearchAll(ctx context.Context, q string) ([]CatalogRecord, error) {
	var results []CatalogRecord
	var errs []error

	// Search Stock DB
	if stockResults, err := r.SearchStock(ctx, q); err != nil {
		errs = append(errs, err)
	} else {
		results = append(results, stockResults...)
	}

	// Search Artlist DB
	if artlistResults, err := r.SearchArtlist(ctx, q); err != nil {
		errs = append(errs, err)
	} else {
		results = append(results, artlistResults...)
	}

	// Search Clips DB
	if clipsResults, err := r.SearchClips(ctx, q); err != nil {
		errs = append(errs, err)
	} else {
		results = append(results, clipsResults...)
	}

	if len(results) == 0 && len(errs) > 0 {
		return nil, errors.Join(errs...)
	}

	return results, nil
}
