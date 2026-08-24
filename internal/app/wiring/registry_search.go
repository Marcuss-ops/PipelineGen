// Package app owns canonical search composition.
package wiring

import (
	"fmt"

	assetsearch "github.com/Marcuss-ops/PipelineGen/internal/application/assets/search"
	"github.com/Marcuss-ops/PipelineGen/internal/application/search"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers"
	sqassets "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets"
	"go.uber.org/zap"
)

// registerSearchBackend is called only after provider bootstrap and freeze.
// There is intentionally no extra-provider parameter or late-registration
// escape hatch.
func registerSearchBackend(log *zap.Logger, providerReg *providers.Registry, clipsRepo *sqassets.ClipsRepository, embeddings search.EmbeddingChannelRegistry, vectorStore assetsearch.VectorStorePort, mediaRepo search.MediaReadRepository, delivery search.AssetDeliveryService, reranker rerankerClient, resolver search.CanonicalIdentityResolver) (search.SearchFanOut, *search.BackendRegistry, *search.Aggregator, error) {
	if log == nil {
		log = zap.NewNop()
	}
	if providerReg == nil {
		err := fmt.Errorf("registerSearchBackend: provider registry is required")
		log.Error(err.Error())
		return nil, nil, nil, err
	}
	if !providerReg.IsFrozen() {
		err := fmt.Errorf("registerSearchBackend: provider registry must be bootstrapped and frozen")
		log.Error(err.Error())
		return nil, nil, nil, err
	}
	fanOut, backends, aggregator, err := BuildCanonicalSearchFanOut(SearchBackendBuildOpts{
		Logger: log, ProviderReg: providerReg, ClipsRepo: clipsRepo,
		Embeddings: embeddings, VectorStore: vectorStore, MediaRepo: mediaRepo,
		Delivery: delivery, Reranker: reranker, CanonicalResolver: resolver,
	})
	if err != nil {
		log.Error("registerSearchBackend: search graph build failed", zap.Error(err))
		return nil, nil, nil, fmt.Errorf("registerSearchBackend: build search graph: %w", err)
	}
	return fanOut, backends, aggregator, nil
}
