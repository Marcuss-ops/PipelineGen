// Package routing — ports.go declares the application-layer read-model
// types referenced from the SQLite images_repository adapter. Per
// AGENTS.md Pattern 0 (Port abstraction layer), the *Application*
// layer owns the canonical contracts that downstream adapters consume;
// the routing sub-package hosts the routing-specific surface so the
// adapter can refer to it via the routing namespace.
//
// Created during Step 6 wrap-up audit (July 2026) to close the 6
// `undefined: routing.X` build errors that surfaced when
// images_repository.go began exercising the agent's territory split
// (catalog/styles/routing/retrieved/generated sub-packages). Until
// origin/main lands the territory separation upstream, this file
// hosts the routing-side shape so downstream adapters can refer to
// it via the routing namespace; once origin/main defines the canonical
// surface, the routing sub-package becomes a thin re-export layer
// (see architecture/ownership.generated.yaml for the canonical owner).
package routing

import (
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
)

// ImageFilter defines the application-layer read-request filter shape
// for the SQLite images_repository ListImages path. Declared directly
// in this sub-package (not aliased to catalog.ImageFilter) per
// AGENTS.md Pattern 0: the routing sub-package owns its own read-model
// surface, and downstream adapters (infrastructure/database/sqlite/assets)
// consume the type through the routing namespace without cross-package
// fan-out. The Origins field uses asset.ImageOrigin from the canonical
// domain taxonomy (internal/domain/asset/image_taxonomy.go).
type ImageFilter struct {
	SubjectID string
	Origins   []asset.ImageOrigin
	Providers []string
	StyleIDs  []string
	Tags      []string
	Limit     ResolvedLimit
}

// ImageSearchResult is the application-layer read-model returned by
// the SQLite images_repository when the joined SELECT pulls back rows
// from media_assets (+ optional generated_image_details /
// retrieved_image_details). The DB layer hydrates only the fields
// that have a stable column-backed source; the rest are populated by
// the application-layer mapper (SummaryFromAsset / FromAssetRow in
// catalog/result.go) downstream.
//
// Mirror of the public API DTO ImageSearchResult declared in
// internal/api/images/types_search.go. We keep a separate
// application-layer struct so the SQLite adapter doesn't accidentally
// couple to the transport-layer shape (Pattern 8 — API = thin
// transport only). The Name / Width / Height / StyleID / StyleVersion
// fields were added during Step 6 wrap-up audit to match the SQL
// projection emitted by ListImages (LEFT JOIN generated_image_details).
type ImageSearchResult struct {
	AssetID      string
	Subject      string
	Slug         string
	Name         string
	PreviewURL   string
	Provider     string
	Origin       asset.ImageOrigin
	License      string
	Description  string
	Width        int
	Height       int
	Score        float64
	Tags         []string
	StyleID      string
	StyleVersion string
	CreatedAt    string
}

// ResolvedLimit is the effective pagination cap after honouring the
// caller-supplied limit, the package default, and the repository's
// applied cap. Aliased to int for ergonomic call-site readability.
type ResolvedLimit = int

// DefaultResolvedLimit is the canonical cap applied when a caller
// passes limit <= 0 or omits the limit entirely. Mirrors the
// historical 25-row cap that the storage_service.go path used to
// hardcode before the territory split (Step 9 forward-pointer).
const DefaultResolvedLimit ResolvedLimit = 25
