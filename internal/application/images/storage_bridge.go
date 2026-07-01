// Package images — storage_bridge.go exposes the StorageBridge
// surface that the retrieved/ subpackage providers depend on.
//
// Why a bridge: storage_search.go's network round-trip helpers
// (searchWikipedia, searchSearXNGImages, searchDDGWide) are
// intentionally private to keep blast radius on internal package
// refactors minimal. But the retrieved/ subpackage's provider
// implementations call them. Exposing them as a public-but-narrow
// Surface — exactly the methods the bridge contract declares — lets
// the subpackage consume the same behaviour without becoming
// coupled to every private detail of ImageStorageService.
//
// Functional behaviour is UNCHANGED — the public methods are
// thin delegates to the existing private implementations.
//
// Step 8 (July 2026): adds the StorageBridge surface as part of the
// image-restructuring plan; the registry's providers drive
// orchestration via this bridge.
package images

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/application/images/retrieved"
)

// SearchBySlug returns up to `limit` ImageAsset preview URLs that
// are already registered in media_assets for the given subject
// slug. The DriveImageProvider (Step 8) uses this as the canonical
// short-circuit when a previous run already produced an image for
// the same subject — no network round-trip needed.
//
// Returns nil when the subject slug has no prior images.
func (s *ImageStorageService) SearchBySlug(ctx context.Context, slug string, limit int) []string {
	if s == nil || s.repo == nil {
		return nil
	}
	assets, err := s.repo.ListImagesBySubject(ctx, slug)
	if err != nil || len(assets) == 0 {
		return nil
	}
	if limit > 0 && len(assets) > limit {
		assets = assets[:limit]
	}
	out := make([]string, 0, len(assets))
	for i := range assets {
		// assets is a slice of value-typed asset.ImageAsset; we
		// iterate by index to avoid the value-vs-pointer nil-check
		// pitfall. (Pre-Step-8 helper that pre-existed separately.)
		if assets[i].PathRel != "" {
			out = append(out, assets[i].PathRel)
		}
	}
	return out
}

// SearchWikipedia exposes the private helper used by the
// WikipediaProvider. Returns (imageURL, wikiPageTitle) — both empty
// when the title doesn't have a usable image.
func (s *ImageStorageService) SearchWikipedia(ctx context.Context, query, lang string) (string, string) {
	if s == nil {
		return "", ""
	}
	return s.searchWikipedia(query, lang)
}

// SearchSearXNGImages exposes the private helper used by the
// SearXNGProvider. Returns the best image URL or empty when the
// server is unreachable / returned no results.
func (s *ImageStorageService) SearchSearXNGImages(ctx context.Context, query string) string {
	if s == nil {
		return ""
	}
	return s.searchSearXNGImages(ctx, query)
}

// SearchDDGWide exposes the private helper used by the
// DuckDuckGoProvider.
func (s *ImageStorageService) SearchDDGWide(ctx context.Context, query string) string {
	if s == nil {
		return ""
	}
	return s.searchDDGWide(ctx, query)
}

// Compile-time assertion: *ImageStorageService satisfies the
// retrieved.StorageBridge contract. Drift between the contract
// and the parent-package implementation surfaces at build time.
var _ retrieved.StorageBridge = (*ImageStorageService)(nil)
