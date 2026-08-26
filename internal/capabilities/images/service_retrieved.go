// Package images (application/images) — service_retrieved.go holds
// the retrieved-territory search methods on Service. Per PR-IMG-SPLIT-4
// (July 2026), retrieved = images found, downloaded, or retrieved
// from normal sources (NOT AI-generated).
//
// Golden rule: these methods delegate to the Store sub-service and
// never touch the Gen (generation) or JobHandler surfaces.
package images

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
)

// SearchAndDownload searches for and downloads an image using the
// retrieved-territory fallback chain (Wikipedia → SearXNG → DuckDuckGo
// → Drive). Delegates to the Store sub-service.
func (s *Service) SearchAndDownload(ctx context.Context, subjectSlug, displayName, query, lang string, tags []string) (*detail.ImageAsset, error) {
	return s.Store.SearchAndDownload(ctx, subjectSlug, displayName, query, lang, tags)
}

// SearchAndDownloadDetailed searches for and downloads an image and
// returns the canonical asset plus cache provenance for transport layers.
func (s *Service) SearchAndDownloadDetailed(ctx context.Context, subjectSlug, displayName, query, lang string, tags []string) (*SearchResult, error) {
	return s.Store.SearchAndDownloadDetailed(ctx, subjectSlug, displayName, query, lang, tags)
}

// SearchAndDownloadDetailedFromProvider runs the same retrieved pipeline
// while resolving one provider through the shared registry. It is used by
// provider-specific live canaries; it never crosses into generated images.
func (s *Service) SearchAndDownloadDetailedFromProvider(ctx context.Context, subjectSlug, displayName, query, lang string, tags []string, provider detail.ImageProvider) (*SearchResult, error) {
	return s.Store.SearchAndDownloadDetailedFromProvider(ctx, subjectSlug, displayName, query, lang, tags, provider)
}

// SearchAndDownloadManyDetailedFromProvider retrieves up to limit images from
// an explicit provider while preserving the retrieved-image pipeline.
func (s *Service) SearchAndDownloadManyDetailedFromProvider(ctx context.Context, subjectSlug, displayName, query, lang string, tags []string, provider detail.ImageProvider, limit int) ([]*SearchResult, error) {
	return s.Store.SearchAndDownloadManyDetailedFromProvider(ctx, subjectSlug, displayName, query, lang, tags, provider, limit)
}

// SearchWebImage performs a web image search (retrieved territory)
// for a given prompt and slug. Delegates to the Store sub-service.
func (s *Service) SearchWebImage(ctx context.Context, prompt, slug string, tags []string) (*detail.ImageAsset, error) {
	return s.Store.SearchWebImage(ctx, prompt, slug, tags)
}
