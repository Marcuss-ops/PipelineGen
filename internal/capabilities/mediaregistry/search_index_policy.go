package mediaregistry

// SearchIndexTaxonomySQL is the canonical taxonomy predicate for the derived
// semantic-search projection. It deliberately does not use index_state:
// INDEXED is an observed projection result, while eligibility is a property
// of the canonical asset row and its taxonomy.
const SearchIndexTaxonomySQL = `
    ((media_type = 'video' AND asset_kind IN ('clip','stock_video','generated_video','rendered_video'))
     OR (media_type = 'image' AND asset_kind IN ('stock_image','web_image','ai_image','graphic')))
    AND COALESCE(namespace, '') != ''
    AND COALESCE(source_type, '') != ''
    AND (deleted_at IS NULL OR deleted_at = '')
`

// SearchIndexEligibilitySQL is the complete predicate for assets that can be
// reconstructed into Qdrant points.
const SearchIndexEligibilitySQL = SearchIndexTaxonomySQL + `
    AND COALESCE(embedding_json, '') NOT IN ('', '[]', '{}')
`

// IsSearchIndexEligible is the in-memory counterpart of
// SearchIndexEligibilitySQL. It is strict and fail-closed for incomplete
// canonical rows.
func IsSearchIndexEligible(t AssetTaxonomy, embeddingJSON, deletedAt string) bool {
	if t.Namespace == "" || t.SourceType == "" || embeddingJSON == "" || embeddingJSON == "[]" || embeddingJSON == "{}" || deletedAt != "" {
		return false
	}
	if err := t.Validate(); err != nil {
		return false
	}
	return t.IndexEligibility() == IndexEligibilitySearchable
}
