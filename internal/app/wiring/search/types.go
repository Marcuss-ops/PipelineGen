package search

import (
	assetsearch "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/search"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/assettree"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assetindex"
)

// SearchBundle holds the canonical asset search composition surfaces.
type SearchBundle struct {
	AssetIndexService *assetindex.Service
	AssetTreeService  *assettree.Service
	AssetResolver     *assetindex.Resolver
	ProviderRegistry  *providers.Registry
	SearchFanOut      assetsearch.SearchFanOut
}
