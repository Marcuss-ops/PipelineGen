package wiring

import (
	jobsoutbox "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs"
	assetsearch "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/search"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/assettree"
	qdrantmaintenance "github.com/Marcuss-ops/PipelineGen/internal/capabilities/maintenance"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/ai/vlm"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/collections"
	qdranthealth "github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/health"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/indexing/clipindexer"
	qdrantsearch "github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/search"
	qdranttransport "github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/transport"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assetindex"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers"
)

// SearchBundle holds the canonical asset search composition surfaces.
type SearchBundle struct {
	AssetIndexService *assetindex.Service
	AssetTreeService  *assettree.Service
	AssetResolver     *assetindex.Resolver
	ProviderRegistry  *providers.Registry
	SearchFanOut      assetsearch.SearchFanOut
}

// ProcessQdrantBundle groups the Qdrant projection/runtime dependencies used by
// the media execution plane.
type ProcessQdrantBundle struct {
	CollectionManager *collections.CollectionManager
	QdrantDeleter     jobsoutbox.VectorPointDeleter
	QdrantRuntime     *qdrant.QdrantRuntime
	VectorSvc         assetsearch.VectorStorePort
	QdrantClient      *qdranttransport.Client
	QdrantHealthProbe *qdranthealth.Probe
	LocatorCleaner    *qdrantmaintenance.LocatorCleaner
	QdrantSearcher    *qdrantsearch.Searcher
}

// ProcessBundle holds the heavy media-processing adapters.
type ProcessBundle struct {
	ProcessQdrantBundle
	MediaProcessor     detail.Processor
	ClipIndexerService *clipindexer.Service
	VLMClient          *vlm.Client
}

// QdrantDeps is the small pre-phase dependency bundle used by outbox wiring.
type QdrantDeps struct {
	Runtime            *qdrant.QdrantRuntime
	ClipIndexerService *clipindexer.Service
	QdrantDeleter      jobsoutbox.VectorPointDeleter
}
