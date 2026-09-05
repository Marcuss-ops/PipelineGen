// Package search owns canonical search composition.
package search

import (
	"database/sql"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/search"
	assetsearch "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/search"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	pgmedia "github.com/Marcuss-ops/PipelineGen/internal/platform/postgres/media"
	sqassets "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets/channels"
	"go.uber.org/zap"
)

// SelectMediaSearchStore resolves the single canonical PostgreSQL media
// search adapter. Retrieval and hydration remain on the same MediaSearcher.
func SelectMediaSearchStore(cfg *config.Config, pg *sql.DB, log *zap.Logger) (assetsearch.VectorStorePort, assetsearch.MediaReadRepository, bool, error) {
	if cfg == nil || !cfg.MediaPostgreSQL.Enabled {
		return nil, nil, false, nil
	}
	if pg == nil {
		return nil, nil, false, fmt.Errorf("media search store: PostgreSQL enabled but canonical media handle is nil")
	}
	if log == nil {
		log = zap.NewNop()
	}
	store := pgmedia.NewMediaSearcher(pg)
	log.Info("POSTGRES-MEDIA-CUTOVER: media semantic search resolved to one PostgreSQL MediaSearcher (pgvector + media_assets hydration)")
	return store, store, true, nil
}

// registerSearchBackend is called only after provider bootstrap and freeze.
// There is intentionally no extra-provider parameter or late-registration
// escape hatch.
func Build(log *zap.Logger, providerReg *providers.Registry, clipsRepo *sqassets.ClipsRepository, embeddings search.EmbeddingChannelRegistry, vectorStore assetsearch.VectorStorePort, mediaRepo search.MediaReadRepository, delivery search.AssetDeliveryService, reranker RerankerClient, resolver search.CanonicalIdentityResolver) (search.SearchFanOut, *search.BackendRegistry, *search.Aggregator, error) {
	if log == nil {
		log = zap.NewNop()
	}
	if providerReg == nil {
		err := fmt.Errorf("search.Build: provider registry is required")
		log.Error(err.Error())
		return nil, nil, nil, err
	}
	if !providerReg.IsFrozen() {
		err := fmt.Errorf("search.Build: provider registry must be bootstrapped and frozen")
		log.Error(err.Error())
		return nil, nil, nil, err
	}
	fanOut, backends, aggregator, err := BuildCanonicalSearchFanOut(SearchBackendBuildOpts{
		Logger: log, ProviderReg: providerReg, ClipsRepo: clipsRepo,
		Embeddings: embeddings, VectorStore: vectorStore, MediaRepo: mediaRepo,
		Delivery: delivery, Reranker: reranker, CanonicalResolver: resolver,
	})
	if err != nil {
		log.Error("search.Build: search graph build failed", zap.Error(err))
		return nil, nil, nil, fmt.Errorf("search.Build: build search graph: %w", err)
	}
	return fanOut, backends, aggregator, nil
}
