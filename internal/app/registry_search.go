// Package app owns canonical search composition.
package app

import (
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers"
	assetsearch "github.com/Marcuss-ops/PipelineGen/internal/application/assets/search"
	"github.com/Marcuss-ops/PipelineGen/internal/application/search"
	sqassets "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"go.uber.org/zap"
)

// registerSearchBackend is called only after provider bootstrap and freeze.
// There is intentionally no extra-provider parameter or late-registration
// escape hatch.
func registerSearchBackend(log *zap.Logger, providerReg *providers.Registry, clipsRepo *sqassets.ClipsRepository, embeddings search.EmbeddingChannelRegistry, vectorStore assetsearch.VectorStorePort, mediaRepo search.MediaReadRepository, delivery search.AssetDeliveryService, reranker rerankerClient) (search.SearchFanOut, *search.BackendRegistry, *search.Aggregator) {
	if log == nil {
		log = zap.NewNop()
	}
	if providerReg == nil || !providerReg.IsFrozen() {
		log.Error("registerSearchBackend: provider registry must be bootstrapped and frozen")
		return nil, nil, nil
	}
	fanOut, backends, aggregator, err := BuildCanonicalSearchFanOut(SearchBackendBuildOpts{
		Logger: log, ProviderReg: providerReg, ClipsRepo: clipsRepo,
		Embeddings: embeddings, VectorStore: vectorStore, MediaRepo: mediaRepo,
		Delivery: delivery, Reranker: reranker,
	})
	if err != nil {
		log.Error("registerSearchBackend: search graph build failed", zap.Error(err))
		return nil, nil, nil
	}
	return fanOut, backends, aggregator
}
