// Package routing — ports.go declares the application-layer read-model
// types referenced from the SQLite images_repository adapter. Per
// AGENTS.md Pattern 0 (Port abstraction layer), the *Application*
// layer owns the canonical contracts that downstream adapters consume;
// the routing sub-package hosts the routing-specific surface so the
// adapter can refer to it via the routing namespace.
//
// FASE 8 (July 2026) — collision avoidance: the canonical
// ImageFilter + ImageSearchResult (used by the ImageSearcher interface
// in dto.go + the FASE 6 ImageSearchResolver composition) live in
// dto.go. This file owns the SQLite-adapter-specific read-model
// shapes (RepositoryListFilter, RepositoryImageRow) which carry
// extra fields the JOIN projections emit (Tags, CreatedAt, etc.).
// Renamed from ImageFilter/ImageSearchResult to avoid the
// declaration collision that pre-FASE-8 broke the build.
package images

import (
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// RepositoryListFilter is the application-layer read-request filter
// shape for the SQLite images_repository ListImages path. Distinct
// from the canonical ImageFilter (in dto.go) which is the
// cross-territory filter consumed by the ImageSearcher interface;
// the SQLite adapter needs the underlying Subject/Slug/Description
// columns to populate the join projection, so the filter carries
// the raw subject-slug match (not just the routing-level SubjectID).
type RepositoryListFilter struct {
	SubjectID string
	Origins   []asset.ImageOrigin
	Providers []string
	StyleIDs  []string
	Tags      []string
	Limit     int
}

// RepositoryImageRow is the application-layer read-model returned by
// the SQLite images_repository when the joined SELECT pulls back rows
// from media_assets (+ optional generated_image_details /
// retrieved_image_details). The DB layer hydrates only the fields
// that have a stable column-backed source; the rest are populated by
// the application-layer mapper (SummaryFromAsset / FromAssetRow in
// catalog/result.go) downstream.
//
// FASE 8: renamed from ImageSearchResult to disambiguate from the
// canonical ImageSearchResult (in dto.go) which the
// ImageSearcher interface returns. The SQLite adapter needs the
// Subject/Slug/Description/Tags/CreatedAt fields to carry the joined
// projection columns that the routing-level DTO doesn't expose.
type RepositoryImageRow struct {
	AssetID       string
	Subject       string
	Slug          string
	Name          string
	PreviewURL    string
	Provider      string
	Origin        asset.ImageOrigin
	License       string
	Author        string
	SourcePageURL string
	DriveLink     string
	LegacyFileMD5 string
	Description   string
	Width         int
	Height        int
	Score         float64
	Tags          []string
	StyleID       string
	StyleVersion  string
	CreatedAt     string
}

// DefaultResolvedLimit is the canonical cap applied when a caller
// passes limit <= 0 or omits the limit entirely. Mirrors the
// historical 25-row cap that the storage_service.go path used to
// hardcode before PR-GENERATED-SEARCH-FIX closed the territory split.
const DefaultResolvedLimit = 25
