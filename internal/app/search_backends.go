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
// AssetsWiring struct.
package app

import (
	"context"
	"fmt"

	assetpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"go.uber.org/zap"

	assetsearch "github.com/Marcuss-ops/PipelineGen/internal/application/assets/search"
	mediasearch "github.com/Marcuss-ops/PipelineGen/internal/application/mediasearch"
	providers "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers"
	search "github.com/Marcuss-ops/PipelineGen/internal/application/search"
	sqassets "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
)

// ── Adapters (one type per backend) ───────────────────────────────────

// providerSearchBackend wraps a single providers.SearchProvider so
// the Aggregator can coordinate fanout. Capabilities are translated
// from the provider-native enum (CapabilitySearch + CapabilityVideo
// + ...) into the search.Capability enum used by Aggregator.Eligible.
//
// SourceRef carries the provider-native identifier (YouTube VideoID,
// artlist item id, etc). The Aggregator's 4-key dedup uses
// Source+"|"+SourceRef as the canonical-provider-identity key.
type providerSearchBackend struct {
	provider providers.SearchProvider
	caps     []search.Capability
	srcName  string
}

func (b *providerSearchBackend) Name() string {
	if b.srcName != "" {
		return b.srcName
	}
	return b.provider.Name()
}

func (b *providerSearchBackend) Capabilities() []search.Capability {
	if b.caps != nil {
		return b.caps
	}
	return []search.Capability{search.CapVideo}
}

func (b *providerSearchBackend) Search(ctx context.Context, q search.Query) ([]search.Candidate, error) {
	// PR-2 (June 2026): provider backends (artlist, youtube, stock)
	// do not support hash-match lookups; the canonical hash path
	// is in domain of the local catalog. A non-empty Query.Hash
	// is intentionally a no-op (return nil, nil) so the fanout
	// continues without cancel.
	if q.Hash != "" {
		return nil, nil
	}
	provReq := providers.SearchRequest{
		Query: q.Text,
		Limit: q.Limit,
		Filters: providers.SearchFilters{
			MediaTypes: mediaTypesFromStrings(q.MediaTypes),
		},
	}
	res, err := b.provider.Search(ctx, provReq)
	if err != nil {
		return nil, err
	}
	out := make([]search.Candidate, 0, len(res.Candidates))
	for _, c := range res.Candidates {
		out = append(out, search.Candidate{
			Source:     b.Name(),
			SourceRef:  c.SourceRef,
			MediaType:  string(c.MediaType),
			Title:      c.Title,
			PreviewURL: c.PreviewURL,
			Hash:       "", // providers don't emit content hash
			Score:      c.Score,
		})
	}
	return out, nil
}

var _ search.SearchBackend = (*providerSearchBackend)(nil)

// localSearchBackend wraps sqassets.ClipsRepository.SearchClipsAdvanced
// (the canonical AdvancedSearchRepo interface). Maps Asset rows
// into search.Candidate. Score is normalised 1.0 (local hits are
// keyword-matched) because the repository already filters by
// metadata; the semantic rerank at the Aggregator level is what
// surfaces relevance ordering across all backends.
type localSearchBackend struct {
	repo    *sqassets.ClipsRepository
	srcName string
}

func (b *localSearchBackend) Name() string {
	if b.srcName != "" {
		return b.srcName
	}
	return "local"
}

func (b *localSearchBackend) Capabilities() []search.Capability {
	return []search.Capability{
		search.CapVideo,
		search.CapImage,
		search.CapAudio,
		search.CapMusic,
	}
}

func (b *localSearchBackend) Search(ctx context.Context, q search.Query) ([]search.Candidate, error) {
	// PR-2 (June 2026): when Query.Hash is non-empty, the local
	// backend fires its hash-match path. The aggregator fans out
	// to EVERY registered backend regardless; non-hash backends
	// ignore Query.Hash and return their text-mode results (which
	// are typically zero when Query.Text is also empty). The
	// hash-source local rows then bubble up with their Hash
	// field populated and the canonical 4-key dedup collapses
	// duplicates by content hash.
	if q.Hash != "" {
		return b.searchByHash(ctx, q)
	}
	return b.searchByText(ctx, q)
}

// searchByHash answers the PR-2 hash-match Query path. Each row
// carries the asset-shape projection (LocalPath + DriveLink +
// ThumbnailURL) so the FindDuplicates handler can render
// duplicates without a second DB lookup. Score = 1.0 because a
// deterministic MD5 match has no semantic scoring; the canonical
// 4-key dedup will merge collisions in the aggregator.
func (b *localSearchBackend) searchByHash(ctx context.Context, q search.Query) ([]search.Candidate, error) {
	hits, err := b.repo.FindClipsByHash(ctx, q.Hash)
	if err != nil {
		return nil, err
	}
	out := make([]search.Candidate, 0, len(hits))
	for _, clip := range hits {
		out = append(out, search.Candidate{
			AssetID:      clip.ID,
			Source:       string(clip.Source),
			SourceRef:    clip.ID,
			MediaType:    string(clip.MediaType),
			Title:        clip.Name,
			Name:         clip.Name,
			LocalPath:    clip.LocalPath(),
			DriveLink:    clip.DriveLink(),
			ThumbnailURL: clip.ThumbnailURL,
			Score:        1.0,
			Hash:         q.Hash,
		})
	}
	return out, nil
}

// searchByText is the pre-PR-2 text path. Kept as a private helper
// so the Search method's hash branch + text branch stay separately
// auditable.
func (b *localSearchBackend) searchByText(ctx context.Context, q search.Query) ([]search.Candidate, error) {
	limit := q.Limit
	if limit <= 0 {
		limit = search.DefaultLimit
	}
	if limit > search.MaxLimit {
		limit = search.MaxLimit
	}
	req := assetpkg.AdvancedSearchRequest{
		Q:      q.Text,
		Limit:  limit,
		Source: sourceOrAll(q.Filters.Source),
	}
	res, err := b.repo.SearchClipsAdvanced(ctx, req)
	if err != nil {
		return nil, err
	}
	out := make([]search.Candidate, 0, len(res.Clips))
	for _, clip := range res.Clips {
		// PR-1: derive score from real signals (title, tags,
		// source, duration) instead of the previous "always 1.0"
		// hardcode. The signal mix caps at 0.95 so a
		// semantic-backend hit with score 0.97 still wins.
		// Asset exposes Duration as time.Duration; convert to
		// ms here so the LocalScore mix is in canonical units.
		// Language is NOT a field on asset.Asset today; the
		// relevant metadata is in clip.Metadata if exposed by the
		// caller (we leave the LocalSignal.Language zero so the
		// language-match signal scores 0, matching the documented
		// "missing signal = no contribution" rule).
		durMs := int(clip.Duration.Milliseconds())
		sig := search.LocalSignal{
			Title:      clip.Name,
			Tags:       clip.Tags,
			Source:     string(clip.Source),
			DurationMs: durMs,
			// Wire q.Filters.DurationMsMin so the duration-fit
			// signal can fire from a non-zero query filter.
			MinDuration: q.Filters.DurationMsMin,
		}
		out = append(out, search.Candidate{
			AssetID:      clip.ID,
			Source:       string(clip.Source),
			SourceRef:    clip.ID,
			Title:        clip.Name,
			Name:         clip.Name,
			MediaType:    string(clip.MediaType),
			LocalPath:    clip.LocalPath(),
			DriveLink:    clip.DriveLink(),
			ThumbnailURL: clip.ThumbnailURL,
			Score:        search.LocalScore(sig, q),
		})
	}
	return out, nil
}

var _ search.SearchBackend = (*localSearchBackend)(nil)

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

	// Fase 6 semantic backend deps (all four must be non-nil to
	// register the backend; nil-safe when any is zero).
	Embedder    search.QueryEmbedder
	VectorStore assetsearch.VectorStorePort
	MediaRepo   mediasearch.MediaReadRepository
	Delivery    mediasearch.AssetDeliveryService
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

	// Fase 6 (July 2026): semantic backend — requires all four
	// ports to be non-nil. Graceful degradation: when Qdrant or
	// the embedder is not yet wired, the backend is silently
	// skipped and the Aggregator operates with providers + local
	// only.
	if opts.Embedder != nil && opts.VectorStore != nil && opts.MediaRepo != nil && opts.Delivery != nil {
		semantic := &semanticSearchBackend{
			embedder:    opts.Embedder,
			vectorStore: opts.VectorStore,
			mediaReader: opts.MediaRepo,
			delivery:    opts.Delivery,
			log:         log,
		}
		if err := reg.Register(semantic); err != nil {
			log.Error("BuildSearchBackends: semantic backend register failed (fail-closed)", zap.Error(err))
			return nil, fmt.Errorf("BuildSearchBackends: semantic backend: %w", err)
		}
		log.Info("BuildSearchBackends: Fase 6 semantic backend registered (two-port Qdrant architecture)")
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
// PR-2 (June 2026): returns BOTH the fan-out AND the bare
// registry so callers can stamp both into AssetsBundle without
// resorting to type-assertion acrobatics on the decorator's
// internal state. The bundle holds the registry directly so
// diagnostic routes + future Health probes can render it without
// going through the decorator.
//
// Fail-closed: BuildSearchBackends error propagates verbatim so
// WireRegistry aborts on a misconfigured backend set instead of
// silently degrading to partial coverage.
func BuildCanonicalSearchFanOut(opts SearchBackendBuildOpts) (search.SearchFanOut, *search.BackendRegistry, error) {
	log := opts.Logger
	if log == nil {
		log = zap.NewNop()
	}
	reg, err := BuildSearchBackends(opts)
	if err != nil {
		return nil, nil, err
	}
	agg := search.NewAggregator(reg, &zapSearchLogAdapter{log: log})
	log.Info("BuildCanonicalSearchFanOut: canonical SearchFanOut wired",
		zap.Int("backends", len(reg.All())))
	return search.NewSearchFanOut(agg), reg, nil
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

// sourceOrAll normalises the legacy clipssearch "source" filter
// semantic. Empty string == "all" (skip server-side filtering).
func sourceOrAll(s string) string {
	if s == "" {
		return "all"
	}
	return s
}

// ── providersBridgeToSearch — composition-only bridge helper ────────────────────
//
// PR-SEARCH-AGGREGATOR-LEGACY (EXPAND phase, June 2026): the legacy
// providers.SearchAggregator (internal/application/assets/providers/
// aggregator.go; NewSearchAggregator ctor + Aggregate method +
// HashQuery branch + ProviderStats/AggregateStats types) is being
// migrated to the canonical *search.Aggregator
// (internal/application/search/aggregator.go).
//
// This function is the COMPOSITION-ONLY bridge that lets the
// migration proceed handler-by-handler (BACKFILL per godlike/07
// §"Migration sequence") WITHOUT a single 6-file diff that can only
// land on a topic branch:
//
//	EXPAND  (this PR)        : ship this bridge helper + deprecation
//	                           record. ZERO callsite changes. CI
//	                           stays GREEN on origin/main because
//	                           the bridge is additive (no
//	                           constructor is replaced, no field
//	                           type is flipped, no file is
//	                           git-rm'd).
//	BACKFILL (next PRs)      : handler-by-handler body rewrites
//	                           from providers.AggregateOptions →
//	                           search.Query. Each migrated handler
//	                           flips its field type to
//	                           *search.Aggregator in the same
//	                           commit. The bridge is documented as
//	                           precedent-only at the composition
//	                           layer; new handlers MUST consume
//	                           *search.Aggregator directly per
//	                           AGENTS.md Pattern 0.
//	CUTOVER (single PR)      : git rm aggregator.go +
//	                           aggregator_test.go; migrate the 7
//	                           legacy tests to
//	                           internal/application/search/
//	                           cross_provider_test.go via canonical
//	                           Search() API (test stubs need full
//	                           rewrites — providers.SearchQuery /
//	                           AggregateOptions have no direct
//	                           equivalent in canonical).
//	CONTRACT (later PR)     : Check 37 in
//	                           scripts/ci-architectural-checks.sh
//	                           gates re-introduction.
//
// The bridge wraps every provider.SearchProvider in the input
// registry as a search.SearchBackend (providerSearchBackend is
// declared above in this file; the canonical wiring is
// search.CapVideo + per-provider backend caps). The result is a
// fresh *search.Aggregator. Future S3c PRs that flip the handler
// field types consume the bridge's return value directly; the
// bridge itself STAYS in this file across the migration as the
// single composition-only seam.
//
// IMPORTANT: this helper is composition-only (lives in
// internal/app /). Per Wave 19 cross-capability rule the search
// package stays stdlib-only; any new handler-level direct
// consumption of *providers.SearchAggregator from
// internal/application/* is forbidden.
// providersBridgeToSearch wraps a *providers.Registry into the canonical
// *search.Aggregator. The implementation REUSES BuildSearchBackends
// (declared above in this file) so future Freeze / register-shape
// changes cannot drift between this bridge and the canonical helper.
// ClipsRepo / MediasearchSvc / WorkspaceID are intentionally left
// nil/empty so only the provider-side backends register — that
// matches the legacy providers.SearchAggregator scope (provider-only
// fan-out; local + semantic live in dedicated adapters registered
// by the canonical WireRegistry path).
func providersBridgeToSearch(reg *providers.Registry, log *zap.Logger) (*search.Aggregator, error) {
	if reg == nil {
		return nil, fmt.Errorf("providersBridgeToSearch: registry is nil (composition root must call WireRegistry -> Freeze() before this helper)")
	}
	if log == nil {
		log = zap.NewNop()
	}
	backendReg, err := BuildSearchBackends(SearchBackendBuildOpts{
		Logger:      log,
		ProviderReg: reg,
		// ClipsRepo: nil, MediasearchSvc: nil, WorkspaceID: "" —
		// BuildSearchBackends skips those branches when nil/empty,
		// so the bridge only registers provider-side backends.
	})
	if err != nil {
		log.Error("providersBridgeToSearch: BuildSearchBackends failed (fail-closed)", zap.Error(err))
		return nil, fmt.Errorf("providersBridgeToSearch: %w", err)
	}
	log.Info("providersBridgeToSearch completed (provider-bridge only; local+semantic live on canonical WireRegistry)",
		zap.Int("provider_backends", len(backendReg.All())))
	return search.NewAggregator(backendReg, &zapSearchLogAdapter{log: log}), nil
}
