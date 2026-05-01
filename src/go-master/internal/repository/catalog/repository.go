package catalog

func NewRepository(dataDir string) *Repository {
	return &Repository{dataDir: dataDir}
}

// SearchAll searches across all catalog databases and returns aggregated results.
// Note: It currently ignores individual database errors to provide partial results if possible.
func (r *Repository) SearchAll(q string) ([]CatalogRecord, error) {
	var results []CatalogRecord

	// Search Stock DB
	if stockResults, err := r.SearchStock(q); err == nil {
		results = append(results, stockResults...)
	}

	// Search Artlist DB
	if artlistResults, err := r.SearchArtlist(q); err == nil {
		results = append(results, artlistResults...)
	}

	// Search Clips DB
	if clipsResults, err := r.SearchClips(q); err == nil {
		results = append(results, clipsResults...)
	}

	return results, nil
}
