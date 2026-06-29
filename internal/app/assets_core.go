// Package app — AssetsModuleDeps bundle + sub-struct grouping (P0-2 commit 1).
//
// P0-2 (June 2026): the historical AssetsBundle (16-field flat struct,
// package app, cross-capability cross-bundle-read channel) is split
// into a typed AssetsModuleDeps with 4 sub-structs grouped by real
// capability area:
//
//   - Core:        the canonical data layer (clips + voiceover + image
//                  repositories, asset.Service, asset tree + index,
//                  mediaProcessor, catalogSync). The bulk of any
//                  Assets-module wire logic consumes these. Held by
//                  ComposeRoot.Repos + ComposeRoot.Search.
//   - Search:      the search sub-system (clipIndexer, mediasearch,
//                  SearchWorkspaceID, SearchFanOut + BackendRegistry).
//                  SearchFanOut + BackendRegistry are stamped by
//                  WireRegistry BEFORE WireAssets runs (PR-2 single
//                  shared-instance invariant).
//   - Delivery:    the DriveClient — only used by clips upload + sfx
//                  upload (handlers commit 4 reads this through
//                  deps.Delivery.DriveClient).
//   - Background:  the singleton idempotency layer (Store + shared
//                  Gin HandlerFunc) shared by clips + register + media
//                  + youtubeclip + mediaingest endpoints.
//
// 4 sub-structs vs 1 mega-struct: matches AGENTS.md's capability-
// bundle governance rule — each sub-area is a separate Go type, so a
// future refactor can add a new field to e.g. Delivery without
// disturbing the 11 other fields that live in Core.
//
// Pass by pointer (`*AssetsModuleDeps`) to keep symmetry with
// JobsBundle / MediaIngestBundle / StockBundle / ArtlistBundle (all
// pointer-receiver here in package app).
//
// PR4d-chunk2 (June 2026): absorbs the historical AssetsBundle
// 16-field shape — no field is dropped, no field is renamed. The
// only change is GROUPING: each field now lives in the sub-struct
// that matches its real capability area, so future commits (P0-2
// commits 2-6) can move a sub-area's wire logic into a dedicated file
// (assets_search.go, assets_delivery.go, assets_handlers.go,
// assets_jobs.go, assets_storage.go) without disturbing the 11 other
// fields.
package app

import (
	"github.com/gin-gonic/gin"
	"google.golang.org/api/drive/v3"

	"github.com/Marcuss-ops/PipelineGen/internal/application/middleware"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/assettree"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/catalogsync"
	"github.com/Marcuss-ops/PipelineGen/internal/application/mediasearch"
	"github.com/Marcuss-ops/PipelineGen/internal/application/search"
	domainasset "github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/assetindex"
	sqassets "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/indexing/clipindexer"
)

// CoreDeps is the Assets-module data-layer bundle (8 fields).
//
// The bulk of any Assets-module wire logic (clips handler + register
// handler + sfx handler + diag handler) consumes Core. Held by
// ComposeRoot.Repos (clipsRepo + voiceoverRepo + imageRepo + Assets)
// + ComposeRoot.Search (AssetTreeService + AssetIndexService)
// + ComposeRoot.Process (MediaProcessor) + ComposeRoot.Sync
// (CatalogSync).
type CoreDeps struct {
	ClipsRepo          *sqassets.ClipsRepository
	VoiceoverRepo      *sqassets.VoiceoversRepository
	ImageRepo          *sqassets.ImagesRepository
	Assets             *domainasset.Service
	AssetTreeService   *assettree.Service
	AssetIndexService  *assetindex.Service
	MediaProcessor     domainasset.Processor
	CatalogSyncService *catalogsync.Service
}

// SearchDeps is the Assets-module search sub-system bundle (5 fields).
//
// PR-2 (June 2026): SearchFanOut + BackendRegistry are stamped by
// WireRegistry's BuildCanonicalSearchFanOut BEFORE WireAssets runs;
// WireAssets CONSUMES the pre-built slots — the single shared
// instance invariant guarantees stats counters aggregate across every
// search entry-point (YouTube + Assets + Mediasearch + FindDuplicates)
// instead of fragmenting per-handler.
//
// Wave 21 PR 9/10 (June 2026): MediasearchService + SearchWorkspaceID
// are composition inputs for the canonical SearchAggregator. The
// semantic backend activates only when SearchWorkspaceID is non-empty
// (QDRANT-004 tenant-isolation gate).
type SearchDeps struct {
	ClipIndexerService    *clipindexer.Service
	MediasearchService    *mediasearch.Service
	SearchWorkspaceID     string
	SearchFanOut          search.SearchFanOut
	SearchBackendRegistry *search.BackendRegistry
}

// DeliveryDeps is the Assets-module delivery sub-system bundle (1 field).
//
// The DriveClient is the SINGLE shared Drive *gdrive.Service that the
// clips upload + sfx upload + asset delivery paths consume (via
// driveutil.Uploader adapters). Other delivery ports (uploader port,
// index dispatcher, asset tree builder) are CONSTRUCTED inside
// WireAssets from these base deps — they don't need to be in the bundle
// because they're cheap to construct and have no cross-module sharing.
type DeliveryDeps struct {
	DriveClient *drive.Service
}

// BackgroundDeps is the Assets-module background/middleware bundle
// (2 fields).
//
// The idempotency layer is a singleton (one cleanup goroutine per
// app, owned by ComposeRoot.IdempotencyMiddleware) shared by clips +
// register endpoints inside the Assets module + by MediaIngest +
// YouTubeClip in their respective bundles — that's why the Store +
// HandlerFunc travel together.
//
// PR 8 (June 2026): the cleanup goroutine is owned by
// ComposeRoot.IdempotencyMiddleware (single instance per app), NOT
// constructed inside WireAssets — keeps the registry-level lifecycle
// simple and avoids double-ticker leaks.
type BackgroundDeps struct {
	IdempotencyStore        middleware.IdempotencyStore
	IdempotencyStoreHandler gin.HandlerFunc
}

// AssetsModuleDeps is the Assets-module capability bundle, composed by
// registerAssets (formerly the flat AssetsBundle literal in
// registry_assets.go).
//
// Total field count across sub-structs: 16 — same as the historical
// AssetsBundle (PR4d-chunk2 shape — no field is dropped, no field is
// renamed). The only change is GROUPING: each field now lives in the
// sub-struct that matches its real capability area, so future commits
// can move a sub-area's wire logic into a dedicated file
// (assets_search.go, assets_delivery.go, assets_handlers.go,
// assets_jobs.go, assets_storage.go) without disturbing the other
// fields.
//
// The pointer-pass convention (`*AssetsModuleDeps`) keeps symmetry
// with JobsBundle / MediaIngestBundle / StockBundle / ArtlistBundle
// in the same composition-root package.
type AssetsModuleDeps struct {
	Core       CoreDeps
	Search     SearchDeps
	Delivery   DeliveryDeps
	Background BackgroundDeps
}
