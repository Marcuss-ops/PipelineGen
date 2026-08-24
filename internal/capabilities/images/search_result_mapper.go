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
	"fmt"

	domain "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"

	"github.com/gin-gonic/gin"
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
	return assetToResultWithCache(a, nil, "", "")
}

// assetToResultWithCache projects an asset.ImageAsset (plus cache-
// provenance fields) into the unified ImageSearchResult DTO.
// Shared by RetrievedSearch + TerritorySearch (territory=all).
//
// Provider fallback policy (PR-IMG-PROVIDER-FALLBACK, July 2026):
//   - Primary: Asset.Provider when it carries a canonical
//     non-"unknown" ImageProvider value (set by the ingest path
//     via ClassifyImageProvider or by the provider registry).
//   - Fallback: retrievalProvider when Asset.Provider is empty
//     or "unknown" — the latter is the explicit godlike/07
//     fail-closed sentinel the ingest layer stamps when no URL
//     pattern could be classified. RetrievalProvider is the
//     faithful ground-truth for "which RetrievalProvider served
//     this row"; preferring it over the stale Asset.Provider
//     closes the silent-fallback signature at the response
//     boundary.
//   - Last resort: "unknown" (coerced from empty Asset.Provider OR
//     when retrievalProvider is also empty / "unknown"; fail-closed).
//     Operators can grep the response for "unknown" to find rows
//     whose origin pipeline could not be identified — silent
//     fake-availability forbidden per godlike/07.
//
// godlike/06 SSOT: this mapper is the SINGLE canonical site
// that materialises ImageSearchResult.Provider. Adding a new
// territory surface MUST reuse this mapper (not re-roll the
// projection loop) so the response shape stays byte-stable.
func assetToResultWithCache(a *domain.ImageAsset, cacheHit *bool, cacheSource, retrievalProvider string) ImageSearchResult {
	if a == nil {
		return ImageSearchResult{}
	}
	provider := string(a.Provider)
	if provider == "" {
		// godlike/07 fail-closed: empty Asset.Provider MUST NOT leak
		// through the response — coerce to the canonical sentinel so
		// callers see a stable string literal they can grep.
		provider = string(domain.ProviderUnknown)
	}
	if provider == string(domain.ProviderUnknown) && retrievalProvider != "" && retrievalProvider != string(domain.ProviderUnknown) {
		// Faithful ground-truth fallback: the service layer stamped
		// a known Retrieval Provider for this row (e.g. "duckduckgo").
		// Prefer it over the stale "unknown" so the response identifies
		// which provider actually served the row.
		provider = retrievalProvider
	}
	return ImageSearchResult{
		AssetID:           a.Hash,
		Origin:            string(a.Origin),
		Provider:          provider,
		PreviewURL:        previewURLForAsset(*a),
		StyleID:           "", // ImageAsset has no Style field today; future migration
		License:           a.License,
		Author:            "", // MetadataJSON carries author today
		CacheHit:          cacheHit,
		CacheSource:       cacheSource,
		RetrievalProvider: retrievalProvider,
	}
}

func boolPtr(v bool) *bool {
	return &v
}

// previewURLForAsset picks the best URL for an asset: prefer
// PathRel when set, else fall back to SourceURL.
func previewURLForAsset(a domain.ImageAsset) string {
	if a.PathRel != "" {
		return a.PathRel
	}
	return a.SourceURL
}

// imageDriveBlock constructs the canonical drive response block for an
// image asset (DoD #8, July 2026). ImageAsset carries DriveFileID and
// PathRel but no DriveFolderID or DriveLink — the link is derived from
// the file ID, and folder_id is empty (images upload directly to the
// configured Drive root folder, no subfolder hierarchy).
func imageDriveBlock(a *domain.ImageAsset) gin.H {
	if a == nil {
		return gin.H{
			"path":      "",
			"folder_id": "",
			"file_id":   "",
			"link":      "",
		}
	}
	link := ""
	if a.DriveFileID != "" {
		link = fmt.Sprintf("https://drive.google.com/file/d/%s/view", a.DriveFileID)
	}
	return gin.H{
		"path":      a.PathRel,
		"folder_id": "",
		"file_id":   a.DriveFileID,
		"link":      link,
	}
}
