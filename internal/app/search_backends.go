// Package app — search_backends.go is the Wave 21 canonical
// composition-only bridge between the canonical Search capability
// (internal/application/search/) and the two concrete backend
// domains:
//
//   - providers.SearchProvider — exposed by every source integration
//     (artlist, youtube, stock); keyed by providers.Registry.
//   - assets.ClipsRepository    — the canonical SQLite-backed local
//     catalog. Exposes SearchClipsAdvanced (AdvancedSearchRepo) and
//     FindClipsByHash (the hash-match dedup path).
//
// Today (July 2026, post Fase 6): providerSearchBackend + localSearchBackend
// + semanticSearchBackend. The historical QDRANT-004 single-tenant
// semanticSearchBackend (which wrapped *mediasearch.Service) was git-rm'd
// in PR-SEARCH-LEGACY-MEDIASEARCH-BACKEND-REMOVAL and replaced by the
// canonical Fase 6 two-port architecture (QueryEmbedder + VectorStorePort)
// in search_backend_semantic.go.
//
// LONG-FILES-DECOMPOSITION-2026-07-06 Band B #2: providerSearchBackend →
// search_backend_provider.go, localSearchBackend → search_backend_local.go.
// This file retains the composition bridge + helpers only.
//
// Wave 19 cross-capability rule: this file IS the ONLY place in
// internal/app/ where multiple internal/application/* domains are
// imported at once. The composition root's canonical pattern
// (per AGENTS.md Pattern 0 + Wave 19 PR-2) is "composition-only
// bridge in internal/app/"; this file fills that role for the Search
// capability. The bare search package remains stdlib-only
// (Wave 19 invariant).
//
// PR-2 (June 2026): BuildSearchBackends also constructs the canonical
// search.SearchFanOut — the telemetry decorator that wraps the
// aggregator and exposes the user-spec Option{Hits, Latencies}
// Stats surface. All callers consume SearchFanOut via the
// wiring.AssetsWiring struct.
package app

import (
	"context"
	"fmt"
	"strings"

	assetpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"go.uber.org/zap"

	providers "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers"
	assetsearch "github.com/Marcuss-ops/PipelineGen/internal/application/assets/search"
	search "github.com/Marcuss-ops/PipelineGen/internal/application/search"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/reranker"
	sqassets "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
)

type rerankerClient interface {
	IsEnabled() bool
	Weight() float64
	TopK() int
	Rerank(ctx context.Context, query string, candidates []reranker.Candidate) ([]reranker.Result, error)
}

// ── Composition bridge ─────────────────────────────────────────────────
//
// SearchBackendBuildOpts groups the inputs BuildSearchBackends
// needs. Every backend can be disabled by leaving the corresponding
// fields nil/empty. The ProviderRegistry and ClipsRepository are
// guaranteed by WireAssets's AssetsBundle plumbing (providerRegistry
// is a direct arg, bundle.ClipsRepo is bundle-resident).
//
// Fase 6 (July 2026): Embedder + VectorStore + MediaRepo + Delivery
// are the four ports consumed by the semanticSearchBackend
// (search_backend_semantic.go). When all four are non-nil, the
// semantic backend is registered alongside providers + local.
// When any is nil (e.g. Qdrant disabled, or embedder not yet wired),
// the semantic backend is silently skipped — the Aggregator falls
// back to provider + local backends (graceful degradation).
type SearchBackendBuildOpts struct {
	Logger      *zap.Logger
	ProviderReg *providers.Registry
	ClipsRepo   *sqassets.ClipsRepository

	// PR-EMBEDDING-CHANNEL-REGISTRY (July 2026): Embeddings
	// replaces the historical Embedder search.QueryEmbedder field.
	// The semanticSearchBackend delegates the multi-channel
	// embedding surface to this registry; new channel encoders
	// plug in via registry composition without backend changes.
	// Required (non-nil) to register the semantic backend
	// alongside VectorStore + MediaRepo + Delivery; nil-safe
	// when zero (the semantic backend gracefully skips
	// registration per Wave 19 invariant — Aggregator falls back
	// to provider + local backends).
	Embeddings  search.EmbeddingChannelRegistry
	VectorStore assetsearch.VectorStorePort
	MediaRepo   search.MediaReadRepository
	Delivery    search.AssetDeliveryService
	Reranker    rerankerClient

	// CanonicalResolver is the source→asset identity resolver consumed
	// by providerSearchBackend. PR-SEARCH-UNIVERSE (August 2026): the
	// provider adapter no longer fabricates a canonical AssetID from
	// the provider ID — it delegates to this resolver. nil is fail-safe
	// (the backend degrades to the noop resolver: identity unknown).
	CanonicalResolver search.CanonicalIdentityResolver
}

// BuildSearchBackends registers backends in a fresh BackendRegistry,
// Freeze()s it, and returns it ready to plug into a search.Aggregator.
// Order of registration mirrors the BACKFILL dual-path rationale:
// providers first (deterministic by Name() ordering), then local,
// then semantic (the only one with prerequisites).
//
// PR-1 fail-closed: every Register error — ErrAlreadyRegistered,
// ErrEmptyName, ErrNilBackend — aborts the build and returns the
// error wrapped with the failing backend's identity.
//
// PR-2 (June 2026): this helper still returns the bare registry;
// the canonical SearchFanOut (aggregator + telemetry decorator)
// is built by BuildCanonicalSearchFanOut. WireRegistry calls both
// once. WireAssets and WireYouTubeClip share the SAME SearchFanOut
// instance so stats counters aggregate across all search traffic.
func BuildSearchBackends(opts SearchBackendBuildOpts) (*search.BackendRegistry, error) {
	log := opts.Logger
	if log == nil {
		log = zap.NewNop()
	}
	reg := search.NewBackendRegistry()

	if opts.ProviderReg != nil {
		for _, p := range opts.ProviderReg.All() {
			sp, ok := p.(providers.SearchProvider)
			if !ok {
				continue
			}
			backend := &providerSearchBackend{
				provider: sp,
				caps:     translateCaps(sp.Capabilities()),
				resolver: opts.CanonicalResolver,
			}
			if err := reg.Register(backend); err != nil {
				log.Error("BuildSearchBackends: provider backend register failed (fail-closed)",
					zap.String("provider", sp.Name()),
					zap.Error(err))
				return nil, fmt.Errorf("BuildSearchBackends: provider %q: %w", sp.Name(), err)
			}
		}
	}
	if opts.ClipsRepo != nil {
		if err := reg.Register(&localSearchBackend{repo: opts.ClipsRepo}); err != nil {
			log.Error("BuildSearchBackends: local backend register failed (fail-closed)", zap.Error(err))
			return nil, fmt.Errorf("BuildSearchBackends: local backend: %w", err)
		}
	}

	// PR-EMBEDDING-CHANNEL-REGISTRY (July 2026): semantic backend —
	// requires all four ports to be non-nil. Graceful degradation:
	// when Qdrant / EmbeddingChannelRegistry / hydration / delivery
	// are not yet wired, the backend is silently skipped and the
	// Aggregator operates with providers + local only.
	if opts.Embeddings != nil && opts.VectorStore != nil && opts.MediaRepo != nil && opts.Delivery != nil {
		semantic := &semanticSearchBackend{
			embeddings:  opts.Embeddings,
			vectorStore: opts.VectorStore,
			mediaReader: opts.MediaRepo,
			delivery:    opts.Delivery,
			log:         log,
			reranker:    opts.Reranker,
		}
		if err := reg.Register(semantic); err != nil {
			log.Error("BuildSearchBackends: semantic backend register failed (fail-closed)", zap.Error(err))
			return nil, fmt.Errorf("BuildSearchBackends: semantic backend: %w", err)
		}
		log.Info("BuildSearchBackends: Fase 6 + PR-EMBEDDING-CHANNEL-REGISTRY semantic backend registered (two-port Qdrant + 5-channel registry)")
	}

	reg.Freeze()
	log.Info("BuildSearchBackends completed (fail-closed)", zap.Int("backends", len(reg.All())))
	return reg, nil
}

// BuildCanonicalSearchFanOut is the PR-2 composition entry-point.
// It builds the BackendRegistry via BuildSearchBackends, wraps it
// in the canonical search.Aggregator, and exposes the result
// through the SearchFanOut decorator (the user-spec Option{Hits,
// Latencies} Stats surface). Handlers and the composition root
// share the SAME fan-out instance so per-backend counters
// aggregate across every search entry-point (YouTube
// /api/media/clips/search + Assets /api/clips/search/advanced +
// Mediasearch + FindDuplicates).
//
// Wave 4 (July 2026): also returns the bare *search.Aggregator so
// consumers that need direct query access (e.g. YouTube SearchCatalog)
// can use it without type-asserting the decorator.
//
// Fail-closed: BuildSearchBackends error propagates verbatim so
// WireRegistry aborts on a misconfigured backend set instead of
// silently degrading to partial coverage.
func BuildCanonicalSearchFanOut(opts SearchBackendBuildOpts) (search.SearchFanOut, *search.BackendRegistry, *search.Aggregator, error) {
	log := opts.Logger
	if log == nil {
		log = zap.NewNop()
	}
	reg, err := BuildSearchBackends(opts)
	if err != nil {
		return nil, nil, nil, err
	}
	agg := search.NewAggregator(reg, &zapSearchLogAdapter{log: log})
	log.Info("BuildCanonicalSearchFanOut: canonical SearchFanOut wired",
		zap.Int("backends", len(reg.All())))
	return search.NewSearchFanOut(agg), reg, agg, nil
}

// ── Internal helpers ───────────────────────────────────────────────────

// translateCaps maps the providers.Capability enum into the
// search.Capability enum. Caps that don't map (CapabilityScript,
// CapabilityFetch) are dropped — they're not yet represented in
// the search capability and PR 10 will revisit if necessary.
func translateCaps(in []providers.Capability) []search.Capability {
	out := make([]search.Capability, 0, len(in))
	for _, c := range in {
		switch c {
		case providers.CapabilityVideo:
			out = append(out, search.CapVideo)
		case providers.CapabilityImage:
			out = append(out, search.CapImage)
		case providers.CapabilityMusic:
			out = append(out, search.CapMusic)
		case providers.CapabilityVoice:
			out = append(out, search.CapAudio) // voice ≈ audio for cap bridging
		}
	}
	if len(out) == 0 {
		return []search.Capability{search.CapVideo}
	}
	return out
}

func mediaTypesFromStrings(in []string) []assetpkg.MediaType {
	if len(in) == 0 {
		return nil
	}
	out := make([]assetpkg.MediaType, 0, len(in))
	for _, m := range in {
		if m == "" {
			continue
		}
		out = append(out, assetpkg.MediaType(m))
	}
	return out
}

// mediaTypesSingleFromString is the PR-AGGREGATE-FILTER-UNIFORM
// canonical helper: map q.Filters.MediaType (a SINGLE canonical
// string per architecture/current.yaml#id-30 PR-1) to the
// providers.SearchFilters.MediaTypes ([]asset.MediaType) shape.
// Empty input returns nil (the "no media-type filter active"
// semantic that the Aggregator already preserves). Delegates to
// the existing mediaTypesFromStrings (skip-blanks + same canonical
// trim semantics) so PR-2's helper stays the single source of
// truth for the slice conversion.
func mediaTypesSingleFromString(in string) []assetpkg.MediaType {
	return mediaTypesFromStrings([]string{strings.TrimSpace(in)})
}

// sourceOrAll normalises the legacy clipssearch "source" filter
// semantic. Empty string == "all" (skip server-side filtering).
func sourceOrAll(s string) string {
	if s == "" {
		return "all"
	}
	return s
}

// (PR-PROVIDERS-SEARCHAGGREGATOR-REMOVE, July 2026) — the legacy
// provider→aggregator composition-only bridge is RETIRED. The
// 6 canonical backends (semantic + local + youtube-live,
// artlist-live, stock, images) are now registered via
// BuildSearchBackends above; every search consumer routes
// through *search.Aggregator. The archcheck forward-prevention
// gate `percheck_providers_searchaggregator_ban` pins the
// godlike/06 SSOT — see git log for the migration history.
