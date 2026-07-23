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

	domain "github.com/Marcuss-ops/PipelineGen/internal/domain/asset"

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

func assetToResultWithCache(a *domain.ImageAsset, cacheHit *bool, cacheSource, retrievalProvider string) ImageSearchResult {
	if a == nil {
		return ImageSearchResult{}
	}
	return ImageSearchResult{
		AssetID:           a.Hash,
		Origin:            string(a.Origin),
		Provider:          string(a.Provider),
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
