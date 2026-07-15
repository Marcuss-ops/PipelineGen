// Package app — AssetsModuleDeps bundle + sub-struct grouping (P0-2 commit 1).
//
// P0-2 (June 2026): the historical AssetsBundle (16-field flat struct,
// package app, cross-capability cross-bundle-read channel) is split
// into a typed AssetsModuleDeps with 4 sub-structs grouped by real
// capability area:
//
//   - Core:        the canonical data layer (clips + voiceover + image
//     repositories, asset.Service, asset tree + index,
//     mediaProcessor, catalogSync).
//   - Search:      the search sub-system (clipIndexer, SearchFanOut +
//     BackendRegistry + canonical SearchAggregator).
//   - Delivery:    the Drive admin and canonical Publisher ports.
//   - Background:  the singleton idempotency layer.
package app

import (
	"github.com/gin-gonic/gin"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/artifacts"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/assettree"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/catalogsync"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	"github.com/Marcuss-ops/PipelineGen/internal/application/middleware"
	"github.com/Marcuss-ops/PipelineGen/internal/application/search"
	domainasset "github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/assetindex"
	sqassets "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	drive "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/indexing/clipindexer"
)

// CoreRepositoryDeps owns the repositories used by the Assets capability.
type CoreRepositoryDeps struct {
	ClipsRepo     *sqassets.ClipsRepository
	VoiceoverRepo *sqassets.VoiceoversRepository
	ImageRepo     *sqassets.ImagesRepository
}

// CoreServiceDeps owns application services and canonical processing ports.
type CoreServiceDeps struct {
	Assets             *domainasset.Service
	AssetTreeService   *assettree.Service
	AssetIndexService  *assetindex.Service
	MediaProcessor     domainasset.Processor
	CatalogSyncService *catalogsync.Service
	ArtifactService    *artifacts.Service
}

// CoreDeps is the Assets-module data-layer bundle. This transitional direct
// field shape remains until the sole composition literal is migrated through
// newCoreDeps; the following commit embeds the two capability groups.
type CoreDeps struct {
	ClipsRepo          *sqassets.ClipsRepository
	VoiceoverRepo      *sqassets.VoiceoversRepository
	ImageRepo          *sqassets.ImagesRepository
	Assets             *domainasset.Service
	AssetTreeService   *assettree.Service
	AssetIndexService  *assetindex.Service
	MediaProcessor     domainasset.Processor
	CatalogSyncService *catalogsync.Service
	ArtifactService    *artifacts.Service
}

// newCoreDeps is the single composition assembler for CoreDeps. Callers pass
// capability groups rather than a nine-slot flat literal; the final embedding
// change therefore remains local to this file.
func newCoreDeps(repositories CoreRepositoryDeps, services CoreServiceDeps) CoreDeps {
	return CoreDeps{
		ClipsRepo:          repositories.ClipsRepo,
		VoiceoverRepo:      repositories.VoiceoverRepo,
		ImageRepo:          repositories.ImageRepo,
		Assets:             services.Assets,
		AssetTreeService:   services.AssetTreeService,
		AssetIndexService:  services.AssetIndexService,
		MediaProcessor:     services.MediaProcessor,
		CatalogSyncService: services.CatalogSyncService,
		ArtifactService:    services.ArtifactService,
	}
}

// SearchDeps is the Assets-module search sub-system bundle.
type SearchDeps struct {
	ClipIndexerService    *clipindexer.Service
	SearchFanOut          search.SearchFanOut
	SearchBackendRegistry *search.BackendRegistry
	SearchAggregator      *search.Aggregator
}

// DeliveryDeps is the Assets-module delivery sub-system bundle.
type DeliveryDeps struct {
	Admin     drive.Admin
	Publisher delivery.Publisher
}

// BackgroundDeps is the Assets-module background/middleware bundle.
type BackgroundDeps struct {
	IdempotencyStore        middleware.IdempotencyStore
	IdempotencyStoreHandler gin.HandlerFunc
}

// AssetsModuleDeps is the Assets-module capability bundle.
type AssetsModuleDeps struct {
	Core       CoreDeps
	Search     SearchDeps
	Delivery   DeliveryDeps
	Background BackgroundDeps
}
