// Package images (application/images) — service_generated_read.go
// holds the generated-territory read seam on Service. Per PR-IMG-SPLIT-4
// (July 2026), this is the canonical application-layer entry point
// for reading AI-generated images from the database.
//
// Golden rule: this is the generated territory READ surface (no
// generation, no retrieved search). It reads existing media_assets
// rows — it never creates or mutates.
package images

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
)

// ListImagesByOrigin returns all image media_assets rows with the
// specified origin, ordered by created_at DESC, hard-capped at
// 200 (per PR-GENERATED-SEARCH-FIX, July 2026). Thin delegate to
// the canonical repo surface at
// internal/platform/sqlite/assets/images_repository.go.
//
// godlike/06 SSOT one-canonical-owner-per-fact: this method is the
// canonical SOLE application-layer entry point for the generated
// territory read seam. The handler at
// internal/capabilities/images/generated_search_handler.go::GeneratedSearch
// routes through here; the port interface GeneratedSearchServicePort
// at internal/capabilities/images/generated/generated_search.go is the
// structural contract (parent *ImageStorageService satisfies it
// transitively via s.Repo().ListImagesByOrigin). Future cross-cutting
// concerns (caching, metrics, additional filtering) can be added in
// one place — the handler does not bypass the service.
func (s *Service) ListImagesByOrigin(ctx context.Context, origin detail.ImageOrigin, limit int) ([]detail.ImageAsset, error) {
	if s == nil {
		return nil, nil
	}
	repo := s.Repo()
	if repo == nil {
		return nil, nil
	}
	return repo.ListImagesByOrigin(ctx, origin, limit)
}
