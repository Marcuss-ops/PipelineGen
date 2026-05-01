package catalog

import (
	"errors"
)

func NewRepository(dataDir string) *Repository {
	return &Repository{dataDir: dataDir}
}

// SearchAll searches across all catalog databases and returns aggregated results.
// It collects errors and returns them joined if no results are found across any database.
func (r *Repository) SearchAll(q string) ([]CatalogRecord, error) {
	var results []CatalogRecord
	var errs []error

	// Search Stock DB
	if stockResults, err := r.SearchStock(q); err != nil {
		errs = append(errs, err)
	} else {
		results = append(results, stockResults...)
	}

	// Search Artlist DB
	if artlistResults, err := r.SearchArtlist(q); err != nil {
		errs = append(errs, err)
	} else {
		results = append(results, artlistResults...)
	}

	// Search Clips DB
	if clipsResults, err := r.SearchClips(q); err != nil {
		errs = append(errs, err)
	} else {
		results = append(results, clipsResults...)
	}

	if len(results) == 0 && len(errs) > 0 {
		return nil, errors.Join(errs...)
	}

	return results, nil
}

