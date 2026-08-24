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
package delivery

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/application/images/routing"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/retrieved"
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
	return s.searchWikipedia(ctx, query, lang)
}

// SearchWikimediaCommons exposes the explicit-license Commons fallback used
// by the retrieved provider. Commons imageinfo carries the actual source
// license and dimensions, so it is safe to classify these candidates as
// verified only when that metadata is present.
func (s *ImageStorageService) SearchWikimediaCommons(ctx context.Context, query string) routing.RetrievalSearchResult {
	if s == nil {
		return routing.RetrievalSearchResult{}
	}
	return s.searchWikimediaCommons(ctx, query)
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

// SearchSearXNGImagesMany exposes the bounded multi-result form used by the
// VidRush image provider. The legacy singular method above remains the
// compatibility surface for callers that intentionally want one best URL.
func (s *ImageStorageService) SearchSearXNGImagesMany(ctx context.Context, query string, limit int) []routing.RetrievalSearchResult {
	if s == nil {
		return nil
	}
	return s.searchSearXNGImagesMany(ctx, query, limit)
}

// SearchDDGWide exposes the private helper used by the
// DuckDuckGoProvider.
func (s *ImageStorageService) SearchDDGWide(ctx context.Context, query string) string {
	if s == nil {
		return ""
	}
	return s.searchDDGWide(ctx, query)
}

// SearchDDGWideMany exposes the bounded multi-result form used by the
// provider-enabled VidRush path. The provider remains the sole owner of DDG
// selection; this bridge preserves alternatives for the common materializer.
func (s *ImageStorageService) SearchDDGWideMany(ctx context.Context, query string, limit int) []string {
	if s == nil {
		return nil
	}
	return s.searchDDGWideMany(ctx, query, limit)
}

// Compile-time assertion: *ImageStorageService satisfies the
// retrieved.StorageBridge contract. Drift between the contract
// and the parent-package implementation surfaces at build time.
var _ retrieved.StorageBridge = (*ImageStorageService)(nil)
