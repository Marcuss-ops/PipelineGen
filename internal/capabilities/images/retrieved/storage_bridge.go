// Package retrieved (application/images/retrieved) — storage_bridge.go
// declares the StorageBridge interface — the minimal dependency
// surface that providers need from the parent images/ package.
// Per PR-IMG-SPLIT-3 (July 2026), interfaces live in their own file.
//
// The image-storage search methods (searchWikipedia, searchSearXNGImages,
// searchDDGWide) are intentionally private on *ImageStorageService in
// the parent images/ package. To keep them encapsulated while letting
// providers in this subpackage call them, the parent package constructs
// each provider with an opaque StorageBridge. This interface declares
// only the methods providers need.
package retrieved

import (
	"context"
)

type StorageBridge interface {
	SearchWikipedia(ctx context.Context, query, lang string) (imgURL string, wikiTitle string)
	SearchWikimediaCommons(ctx context.Context, query string) RetrievalSearchResult
	SearchSearXNGImages(ctx context.Context, query string) string
	SearchSearXNGImagesMany(ctx context.Context, query string, limit int) []RetrievalSearchResult
	SearchDDGWide(ctx context.Context, query string) string
	// SearchBySlug is the Drive-side list look-up; returns up to limit
	// previously-ingested image URLs for the given subject slug. The
	// registry uses it to short-circuit DriveProvider when the asset is
	// already on disk.
	SearchBySlug(ctx context.Context, slug string, limit int) []string
}
