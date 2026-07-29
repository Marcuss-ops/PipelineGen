// Package app — AssetsModuleDeps bundle + sub-struct grouping (P0-2 commit 1).
//
// P0-2 (June 2026): the historical AssetsBundle (16-field flat struct,
// package app, cross-capability cross-bundle-read channel) is split
// into a typed AssetsModuleDeps with 4 sub-structs grouped by real
// capability area:
//
//   - Core:        the canonical data layer (clips + voiceover + image
//     repositories, asset.Service, asset tree + index,
//     mediaProcessor, catalogSync). The bulk of any
//     Assets-module wire logic consumes these. Held by
//     wiring.ComposeRoot.Repos + wiring.ComposeRoot.Search.
//   - Search:      the search sub-system (clipIndexer, mediasearch,
//     SearchWorkspaceID, SearchFanOut + BackendRegistry).
//     SearchFanOut + BackendRegistry are stamped by
//     WireRegistry BEFORE WireAssets runs (PR-2 single
//     shared-instance invariant).
//   - Delivery:    the DriveClient — only used by clips upload + sfx
//     upload (handlers commit 4 reads this through
//     deps.Delivery.DriveClient).
//   - Background:  the singleton idempotency layer (Store + shared
//     Gin HandlerFunc) shared by clips + register + media
//   - youtubeclip + mediaingest endpoints.
//
// 4 sub-structs vs 1 mega-struct: matches AGENTS.md's capability-
// bundle governance rule — each sub-area is a separate Go type, so a
// future refactor can add a new field to e.g. Delivery without
// disturbing the 11 other fields that live in Core.
//
// Pass by pointer (`*AssetsModuleDeps`) to keep symmetry with
// wiring.JobsBundle / MediaIngestBundle / wiring.StockBundle / wiring.ArtlistBundle (all
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

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/artifacts"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/assettree"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/catalogsync"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	"github.com/Marcuss-ops/PipelineGen/internal/application/middleware"
	"github.com/Marcuss-ops/PipelineGen/internal/application/search"
	domainasset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/assetindex"
	sqassets "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets/imagesrepo"
	drive "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/indexing/clipindexer"
)

// CoreDeps is the Assets-module data-layer bundle (9 fields).
//
// P0.1 (June 2026): ArtifactService added — the concrete
// *artifacts.Service is constructed in BuildDomainBundle and
// wired through so the upload UseCase can accept real video
// uploads instead of always returning HTTP 500.
//
// The bulk of any Assets-module wire logic (clips handler + register
// handler + sfx handler + diag handler) consumes Core. Held by
// wiring.ComposeRoot.Repos (clipsRepo + voiceoverRepo + imageRepo + Assets)
// + wiring.ComposeRoot.Search (AssetTreeService + AssetIndexService)
// + wiring.ComposeRoot.Process (MediaProcessor) + wiring.ComposeRoot.Sync
// (CatalogSync).
//
// PR-CORE-DEPS-SPLIT (July 2026): split into RepositoryDeps +
// ServiceDeps sub-bundles so CoreDeps stays under the archcheck
// 8-field cap. The grouping mirrors the ownership split between
// repository-like ports and service-like ports.
type CoreDeps struct {
	Repositories RepositoryDeps
	Services     ServiceDeps
}

// RepositoryDeps groups the repository-like ports owned by CoreDeps.
type RepositoryDeps struct {
	ClipsRepo     *sqassets.ClipsRepository
	VoiceoverRepo *sqassets.VoiceoversRepository
	ImageRepo     *imagesrepo.ImagesRepository
}

// ServiceDeps groups the service-like ports owned by CoreDeps.
type ServiceDeps struct {
	Assets             *domainasset.Service
	AssetTreeService   *assettree.Service
	AssetIndexService  *assetindex.Service
	MediaProcessor     domainasset.Processor
	CatalogSyncService *catalogsync.Service
	// P0.1 (June 2026): the concrete artifact blob service wired
	// from BuildDomainBundle → wiring.DomainBundle → CoreDeps.
	ArtifactService *artifacts.Service
}

// SearchDeps is the Assets-module search sub-system bundle (3 fields).
//
// PR-2 (June 2026): SearchFanOut + BackendRegistry are stamped by
// WireRegistry's BuildCanonicalSearchFanOut BEFORE WireAssets runs;
// WireAssets CONSUMES the pre-built slots — the single shared
// instance invariant guarantees stats counters aggregate across every
// search entry-point (YouTube + Assets + FindDuplicates) instead of
// fragmenting per-handler.
//
// PR-SEARCH-LEGACY-MEDIASEARCH-BACKEND-REMOVAL (June 2026): the
// historical MediasearchService + SearchWorkspaceID fields are
// git-rm'd alongside the underlying *mediasearch.Service orchestrator.
// Workspace-gated semantic routing now lives directly inside
// search.Aggregator paths (per-scope), not as dedicated Service-level
// bundle fields.
type SearchDeps struct {
	ClipIndexerService    *clipindexer.Service
	SearchFanOut          search.SearchFanOut
	SearchBackendRegistry *search.BackendRegistry

	// SearchAggregator is the canonical godlike/06 SSOT one-owner-per-fact
	// *search.Aggregator singleton constructed at composition time by
	// BuildCanonicalSearchFanOut (internal/app/search_backends.go) and
	// plumbed through RegistryWiring.searchAgg into this field. The api/
	// layer NEVER constructs a second instance and WireAssets MUST consume
	// this canonical (per percheck_search_aggregator_singleton
	// forward-prevention + PR-DIAGNOSI-FINALE rule 6).
	SearchAggregator *search.Aggregator
}

// DeliveryDeps is the Assets-module delivery sub-system bundle.
//
// FASE 9 Step 2 (June 2026): DriveClient (*gdrive.Service) removed.
// Admin is the canonical Pattern 0 port for Drive operations;
// WireAssets extracts the concrete *drive.Uploader via type assertion
// to construct the legacy adapters that still need it.
type DeliveryDeps struct {
	Admin drive.Admin
	// Publisher is the canonical Drive upload canal (FASE 5, June 2026).
	// All endpoints and jobs that write to Drive MUST use Publisher.Publish
	// instead of calling DriveUploader or FolderManager directly.
	Publisher delivery.Publisher
}

// BackgroundDeps is the Assets-module background/middleware bundle
// (2 fields).
//
// The idempotency layer is a singleton (one cleanup goroutine per
// app, owned by wiring.ComposeRoot.IdempotencyMiddleware) shared by clips +
// register endpoints inside the Assets module + by MediaIngest +
// YouTubeClip in their respective bundles — that's why the Store +
// HandlerFunc travel together.
//
// PR 8 (June 2026): the cleanup goroutine is owned by
// wiring.ComposeRoot.IdempotencyMiddleware (single instance per app), NOT
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
// with wiring.JobsBundle / MediaIngestBundle / wiring.StockBundle / wiring.ArtlistBundle
// in the same composition-root package.
type AssetsModuleDeps struct {
	Core       CoreDeps
	Search     SearchDeps
	Delivery   DeliveryDeps
	Background BackgroundDeps
}
