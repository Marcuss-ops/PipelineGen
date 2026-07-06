// Package images (api/images) — search_result_mapper.go holds
// the shared projection helpers that convert asset types to
// the unified ImageSearchResult DTO. Used by both retrieved
// and generated territory handlers.
//
// This file is shared infrastructure — both territories use
// the same DTO envelope. Territory-specific logic lives in
// the respective handler files.
package images

import (
	domain "github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
)

// assetToResult projects an asset.ImageAsset to a unified
// ImageSearchResult. Shared by RetrievedSearch + TerritorySearch
// (territory=all) handlers.
//
// Direct field access (not getters): asset.ImageAsset is a
// value type exported struct with public fields; no getter
// indirection. StyleID/Author come from MetadataJSON today
// (forward-pointer for direct structure fields).
//
// Accepts a *domain.ImageAsset because the service-layer
// SearchAndDownload / ListImagesBySubject methods return
// pointers; nil is handled by callers (returns empty DTO list).
func assetToResult(a *domain.ImageAsset) ImageSearchResult {
	if a == nil {
		return ImageSearchResult{}
	}
	return ImageSearchResult{
		AssetID:    a.Hash,
		Origin:     string(a.Origin),
		Provider:   string(a.Provider),
		PreviewURL: previewURLForAsset(*a),
		StyleID:    "", // ImageAsset has no Style field today; future migration
		License:    a.License,
		Author:     "", // MetadataJSON carries author today
	}
}

// previewURLForAsset picks the best URL for an asset: prefer
// PathRel when set, else fall back to SourceURL.
func previewURLForAsset(a domain.ImageAsset) string {
	if a.PathRel != "" {
		return a.PathRel
	}
	return a.SourceURL
}
