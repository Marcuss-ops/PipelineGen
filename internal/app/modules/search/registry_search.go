// Package search owns canonical search composition.
package search

import (
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/search"
	pgmedia "github.com/Marcuss-ops/PipelineGen/internal/platform/postgres/media"
	"go.uber.org/zap"
)

// SelectMediaSearchStore resolves the single canonical PostgreSQL media
// search adapter. Retrieval and hydration remain on the same MediaSearcher.
func SelectMediaSearchStore(deps StoreDeps) (search.VectorStorePort, search.MediaReadRepository, bool, error) {
	cfg, pg, log := deps.Config, deps.Database, deps.Log
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
func Build(deps Deps) (search.SearchFanOut, *search.BackendRegistry, *search.Aggregator, error) {
	log, providerReg := deps.Log, deps.Providers
	embeddings, vectorStore, mediaRepo := deps.Embeddings, deps.VectorStore, deps.MediaRepo
	delivery, reranker, resolver := deps.Delivery, deps.Reranker, deps.Resolver
	localScorer := deps.LocalScorer
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
	// MEDIA-SSOT P1-6: when the canonical media read repository is the
	// PostgreSQL MediaSearcher, the local/hash/keyword backend reads the same
	// SSOT. There is no SQLite fallback: a non-PostgreSQL local store simply
	// leaves the local media capability unregistered (fail-closed).
	var localStore PostgresLocalSearchPort
	if ls, ok := mediaRepo.(PostgresLocalSearchPort); ok {
		localStore = ls
	}
	fanOut, backends, aggregator, err := BuildCanonicalSearchFanOut(SearchBackendBuildOpts{
		Logger: log, ProviderReg: providerReg,
		Embeddings: embeddings, VectorStore: vectorStore, MediaRepo: mediaRepo,
		Delivery: delivery, Reranker: reranker, CanonicalResolver: resolver,
		MediaLocalStore: localStore,
		LocalScorer:     localScorer,
	})
	if err != nil {
		log.Error("search.Build: search graph build failed", zap.Error(err))
		return nil, nil, nil, fmt.Errorf("search.Build: build search graph: %w", err)
	}
	return fanOut, backends, aggregator, nil
}
