// Package app — Assets module dependency groups.
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

// CoreRepositoryDeps owns repositories used by the Assets capability.
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

// CoreDeps exposes two real capability groups instead of nine mandatory ports.
// Anonymous embedding preserves the existing selector surface for consumers.
type CoreDeps struct {
	CoreRepositoryDeps
	CoreServiceDeps
}

// newCoreDeps is the single composition assembler for CoreDeps.
func newCoreDeps(repositories CoreRepositoryDeps, services CoreServiceDeps) CoreDeps {
	return CoreDeps{
		CoreRepositoryDeps: repositories,
		CoreServiceDeps:    services,
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
