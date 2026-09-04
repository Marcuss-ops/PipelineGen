// Package app — search_backends.go is the Wave 21 canonical
// composition-only bridge between the canonical Search capability
// (internal/capabilities/assets/search/) and the concrete backend domains.
//
// Media cutover invariant (September 2026): the semantic catalog backend is
// registered only when ONE adapter implements both VectorStorePort and
// MediaReadRepository. The canonical production implementation is
// platform/postgres/media.MediaSearcher. This prevents accidental
// recomposition of pgvector or Qdrant retrieval with SQLite hydration.
//
// Wave 19 cross-capability rule: this file IS the ONLY place in
// internal/app/ where multiple internal/capabilities/* domains are imported
// at once. The composition root's canonical pattern is "composition-only
// bridge in internal/app/"; capability code remains behind typed ports.
package wiring

import (
	"context"
	"fmt"
	"strings"

	assetpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"go.uber.org/zap"

	providers "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers"
	assetsearch "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/search"
	search "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/search"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/ai/reranker"
	sqassets "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets/channels"
)

type rerankerClient interface {
	IsEnabled() bool
	Weight() float64
	TopK() int
	Rerank(ctx context.Context, query string, candidates []reranker.Candidate) ([]reranker.Result, error)
}

// canonicalMediaSearchStore is the cutover gate for semantic catalog reads.
// Retrieval and hydration MUST be owned by the same adapter. PostgreSQL's
// MediaSearcher satisfies both ports; the legacy Qdrant vector adapter does
// not, so it cannot become a media semantic backend even if it remains wired
// for legitimate non-media owners.
type canonicalMediaSearchStore interface {
	assetsearch.VectorStorePort
	search.MediaReadRepository
}

// ── Composition bridge ─────────────────────────────────────────────────
//
// SearchBackendBuildOpts groups the inputs BuildSearchBackends needs.
// ProviderRegistry and ClipsRepository feed discovery/local backends.
// Embeddings + VectorStore + Delivery feed semantic catalog search.
//
// MediaRepo remains in the shape temporarily for source compatibility with
// older composition callers, but it is deliberately NOT consulted by the
// semantic backend. The media cutover requires the VectorStore itself to
// implement MediaReadRepository, guaranteeing one PostgreSQL read authority.
type SearchBackendBuildOpts struct {
	Logger      *zap.Logger
	ProviderReg *providers.Registry
	ClipsRepo   *sqassets.ClipsRepository

	Embeddings  search.EmbeddingChannelRegistry
	VectorStore assetsearch.VectorStorePort
	MediaRepo   search.MediaReadRepository // compatibility input; not a semantic hydration fallback
	Delivery    search.AssetDeliveryService
	Reranker    rerankerClient

	// CanonicalResolver is the source→asset identity resolver consumed
	// by providerSearchBackend. nil is fail-safe (identity unknown).
	CanonicalResolver search.CanonicalIdentityResolver
}

// BuildSearchBackends registers backends in a fresh BackendRegistry,
// freezes it, and returns it ready to plug into a search.Aggregator.
// Providers and the local catalog remain independently available. Semantic
// media search is stricter: a split vector/hydration authority is rejected by
// construction instead of silently degrading to Qdrant + SQLite.
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

	// POSTGRES-MEDIA-CUTOVER: semantic search requires one concrete adapter
	// to own BOTH retrieval and hydration. A Qdrant-only VectorStorePort can
	// remain alive for non-media domains, but it cannot satisfy this gate and
	// therefore cannot be selected for the media catalog.
	mediaStore, mediaStoreOK := opts.VectorStore.(canonicalMediaSearchStore)
	if opts.VectorStore != nil && !mediaStoreOK {
		log.Info("BuildSearchBackends: vector store ignored for media semantic search because it does not own canonical hydration")
	}
	if opts.Embeddings != nil && mediaStoreOK && opts.Delivery != nil {
		semantic := &semanticSearchBackend{
			embeddings:  opts.Embeddings,
			vectorStore: mediaStore,
			mediaReader: mediaStore,
			delivery:    opts.Delivery,
			log:         log,
			reranker:    opts.Reranker,
		}
		if err := reg.Register(semantic); err != nil {
			log.Error("BuildSearchBackends: semantic backend register failed (fail-closed)", zap.Error(err))
			return nil, fmt.Errorf("BuildSearchBackends: semantic backend: %w", err)
		}
		log.Info("BuildSearchBackends: PostgreSQL media semantic backend registered (pgvector + PostgreSQL hydration)")
	}

	reg.Freeze()
	log.Info("BuildSearchBackends completed (fail-closed)", zap.Int("backends", len(reg.All())))
	return reg, nil
}

// BuildCanonicalSearchFanOut is the composition entry-point. It builds the
// BackendRegistry, wraps it in the canonical search.Aggregator, and exposes
// the SearchFanOut telemetry decorator. Handlers and composition consumers
// share the SAME fan-out instance so backend counters aggregate across all
// search entry points.
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
// the search capability.
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
			out = append(out, search.CapAudio)
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

// mediaTypesSingleFromString maps q.Filters.MediaType to the providers
// SearchFilters.MediaTypes slice shape. Empty input means no media-type
// filter. Delegating to mediaTypesFromStrings keeps one conversion rule.
func mediaTypesSingleFromString(in string) []assetpkg.MediaType {
	return mediaTypesFromStrings([]string{strings.TrimSpace(in)})
}

// sourceOrAll normalises the legacy clipssearch source filter semantic.
// Empty string means all sources.
func sourceOrAll(s string) string {
	if s == "" {
		return "all"
	}
	return s
}
