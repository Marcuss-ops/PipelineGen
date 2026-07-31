// Package app — search backend + fan-out construction (PR4 split).
//
// PR4 mechanical split (June 2026): registerSearchBackend is the
// canonical wiring site for the SearchFanOut decorator + Aggregator
// pair shared across YouTubeClip + Assets. The actual call is invoked
// from registerInternalModules; this file owns the helper so any
// change to the cross-step state's construction is local.
//
// SearchBackendBuildOpts is owned by search_backends.go (Wave 21 PR 9
// canonical owner); this file reaches it via same-package visibility
// without an explicit import.
//
// registerSearchBackend constructs the search capability value consumed by
// later registration phases. It has no RegistryWiring side effects: the
// returned tuple is the explicit edge in the composition graph. Empty
// nil-packaging is intentionally allowed when ProviderReg is nil (test /
// partial deploy scenarios) — callers see a noop fan-out and a nil
// aggregator, which is the canonical fail-closed surface.
package app

import (
	providers "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers"
	assetsearch "github.com/Marcuss-ops/PipelineGen/internal/application/assets/search"
	"github.com/Marcuss-ops/PipelineGen/internal/application/search"
	sqassets "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"go.uber.org/zap"
)

// SearchBackendBuildOpts is owned by search_backends.go (the
// "current line number" drifts with refactors; this helper reaches
// the struct via same-package visibility without an explicit import).
// Do NOT redeclare the struct here (would re-introduce the
// "redeclared in this block" build error this PR fix removed).

// registerSearchBackend constructs the canonical SearchFanOut +
// search.Aggregator pair. Called once per WireRegistry invocation from
// registerInternalModules (Step 1b). Returns:
//
//   - searchFanOut: the SearchFanOut decorator consumed by Assets in Step 5.
//   - searchBackends: the BackendRegistry surface consumed by Assets.
//   - searchAgg: the *search.Aggregator used by YouTubeClip and late bindings.
//
// Empty-input behavior (when ProviderReg is nil) is the canonical
// fail-closed: the fan-out / backends / aggregator are all nil; the
// callers translate the nil to noopFanOut / 503 on the API side.
//
// Reviewer's Q7 invariant: nil provider-reg must NOT panic here;
// production sees the warning log and proceeds with nil for the
// downstream caller to fail-closed.
func registerSearchBackend(log *zap.Logger, providerReg *providers.Registry, clipsRepo *sqassets.ClipsRepository, embeddings search.EmbeddingChannelRegistry, vectorStore assetsearch.VectorStorePort, mediaRepo search.MediaReadRepository, delivery search.AssetDeliveryService, reranker rerankerClient) (search.SearchFanOut, *search.BackendRegistry, *search.Aggregator) {
	var searchFanOut search.SearchFanOut
	var searchBackends *search.BackendRegistry
	var searchAgg *search.Aggregator
	var err error
	searchFanOut, searchBackends, searchAgg, err = BuildCanonicalSearchFanOut(SearchBackendBuildOpts{
		Logger:      log,
		ProviderReg: providerReg,
		ClipsRepo:   clipsRepo,
		// PR-EMBEDDING-CHANNEL-REGISTRY (July 2026): semantic
		// backend deps are nil-safe — the backend only
		// registers when all four are non-nil. Embeddings
		// replaces the historical Embedder search.QueryEmbedder.
		Embeddings:  embeddings,
		VectorStore: vectorStore,
		MediaRepo:   mediaRepo,
		Delivery:    delivery,
		Reranker:    reranker,
	})
	if err != nil {
		log.Error("registerSearchBackend: BuildCanonicalSearchFanOut failed (fail-closed)", zap.Error(err))
		return nil, nil, nil
	}
	log.Info("PR-2: canonical SearchFanOut wired against the composition-root search dependencies (provider registry optional)")
	return searchFanOut, searchBackends, searchAgg
}
